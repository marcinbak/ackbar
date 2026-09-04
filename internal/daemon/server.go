package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"ackbar/internal/relay"
	"ackbar/internal/tmux"
	"ackbar/internal/version"
)

// Provider interface mapping to avoid cyclic dependencies
type Event struct {
	Agent       string
	NativeID    string
	Cwd         string
	Roots       []string
	EventName   string
	State       State
	Blocked     *Blocked
	Activity    string
	Name        string
	Entrypoint  string
	Kind        string
	Version     string
	ContextPct  int
	StartedAt   time.Time
	LastEventAt time.Time
}

type Server struct {
	db              *DB
	providers       map[string]Provider
	subscribers     map[chan *Session]bool
	subMutex        sync.Mutex
	webFS           fs.FS
	configuredToken string
	relayClient     *relay.Client
}

func NewServer(db *DB) *Server {
	StartUploadCleaner(defaultUploadDir, 6*time.Hour)
	return &Server{
		db:          db,
		providers:   make(map[string]Provider),
		subscribers: make(map[chan *Session]bool),
	}
}

func (s *Server) SetToken(token string) {
	s.configuredToken = token
}

func (s *Server) SetWebFS(webFS fs.FS) {
	s.webFS = webFS
}

func (s *Server) RegisterProvider(p Provider) {
	s.providers[p.Agent()] = p
}

func (s *Server) StartRelayClient(ctx context.Context, relayURL, relaySecret string) {
	if relayURL == "" {
		return
	}
	hostName := "local"
	if h := os.Getenv("ACKBAR_HOST"); h != "" {
		hostName = h
	}
	log.Printf("[Daemon] Initializing outbound Ackbar Relay client to %s as host %q...", relayURL, hostName)
	s.relayClient = relay.NewClient(relayURL, hostName, relaySecret, s.Mux())
	s.relayClient.Start(ctx)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("ACKBAR_TOKEN")
		if token == "" {
			token = s.configuredToken
		}

		// If no token is configured, open access (local loopback / Tailscale mesh mode)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow healthz, version, static assets, and CORS preflight OPTIONS without token
		path := r.URL.Path
		if r.Method == http.MethodOptions || path == "/healthz" || path == "/v1/version" {
			next.ServeHTTP(w, r)
			return
		}
		// Allow static web assets so the UI and favicon load before entering token
		if path == "/" || path == "/index.html" || path == "/style.css" || path == "/app.js" || path == "/manifest.json" || path == "/favicon.ico" || strings.HasPrefix(path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		// 1. Check Authorization header: Bearer <token>
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			if strings.TrimPrefix(authHeader, "Bearer ") == token {
				next.ServeHTTP(w, r)
				return
			}
		}

		// 2. Check X-Ackbar-Token header
		if r.Header.Get("X-Ackbar-Token") == token {
			next.ServeHTTP(w, r)
			return
		}

		// 3. Check query parameter ?token=<token> (for SSE and WebSockets)
		if r.URL.Query().Get("token") == token {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", `Bearer realm="ackbar"`)
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Unauthorized: missing or invalid authentication token",
		})
	})
}

func (s *Server) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"ackbard"}`))
	})
	mux.HandleFunc("/v1/hooks/", s.handleHook)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/respond", s.handleRespond)
	mux.HandleFunc("/v1/sessions/transcript", s.handleSessionTranscript)
	mux.HandleFunc("/v1/sessions/", s.handleSessionControl)
	mux.HandleFunc("/v1/sessions/control", s.handleSessionControl)
	mux.HandleFunc("/v1/sessions/pty", s.handlePTY)
	mux.HandleFunc("/v1/sessions/spawn", s.handleSpawn)
	mux.HandleFunc("/v1/agents/discovery", s.handleAgentDiscovery)
	mux.HandleFunc("/v1/providers", s.handleProviders)
	mux.HandleFunc("/v1/documents", s.handleDocuments)
	mux.HandleFunc("/v1/documents/content", s.handleDocumentContent)
	mux.HandleFunc("/v1/nodes", s.handleNodes)
	mux.HandleFunc("/v1/nodes/move", s.handleNodeMove)
	mux.HandleFunc("/v1/hosts", s.handleHosts)
	mux.HandleFunc("/v1/hosts/update", s.handleHostUpdate)
	mux.HandleFunc("/v1/hosts/reconnect", s.handleHostReconnect)
	mux.HandleFunc("/v1/projects/create", s.handleCreateProject)
	mux.HandleFunc("/v1/maintenance/purge", s.handlePurge)
	mux.HandleFunc("/v1/editor/open", s.handleEditorOpen)
	mux.HandleFunc("/v1/version", s.handleVersion)
	mux.HandleFunc("/v1/uploads", s.handleUpload)
	mux.HandleFunc("/v1/shutdown", s.handleShutdown)
	mux.HandleFunc("/v1/events", s.handleEvents)

	// Serve embedded Web GUI
	if s.webFS != nil {
		fileServer := http.FileServer(http.FS(s.webFS))
		mux.Handle("/", fileServer)
	}

	return withCORS(s.authMiddleware(mux))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Ackbar-Host, X-Ackbar-Token")
		if strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") || r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Capture user-assigned node_path mappings for existing sessions
	nodePathMap := make(map[string]string)
	if existing, err := s.db.ListSessions(); err == nil {
		for _, sess := range existing {
			if sess.NodePath != "" {
				nodePathMap[sess.ID] = sess.NodePath
				if sess.NativeID != "" {
					nodePathMap[sess.NativeID] = sess.NodePath
				}
			}
		}
	}

	// Clear sessions table (tree_nodes and hosts are strictly preserved)
	if err := s.db.PurgeSessions(); err != nil {
		http.Error(w, fmt.Sprintf("Purge failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Clear in-memory title cache
	titleCacheMutex.Lock()
	titleCache = make(map[string]TitleCacheEntry)
	titleCacheMutex.Unlock()

	// Re-scan live sessions from active tmux panes & OS processes
	s.scanObservedSessions(r.Context())

	// Re-apply preserved node_path mappings to newly scanned sessions
	if scanned, err := s.db.ListSessions(); err == nil {
		for _, sess := range scanned {
			if path, ok := nodePathMap[sess.ID]; ok && path != "" {
				sess.NodePath = path
				_ = s.db.SaveSession(sess)
				s.broadcast(sess)
			} else if path, ok := nodePathMap[sess.NativeID]; ok && path != "" {
				sess.NodePath = path
				_ = s.db.SaveSession(sess)
				s.broadcast(sess)
			}
		}
	}

	// Forward maintenance purge to remote hosts asynchronously
	if hosts, err := s.db.ListHosts(); err == nil {
		for _, h := range hosts {
			if h.URL != "" && h.Name != "local" {
				go func(targetURL string) {
					req, _ := http.NewRequest(http.MethodPost, strings.TrimSuffix(targetURL, "/")+"/v1/maintenance/purge", nil)
					client := &http.Client{Timeout: 5 * time.Second}
					_, _ = client.Do(req)
				}(h.URL)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "purged_and_rehydrated"})
}

func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Route is /v1/hooks/{agent}
	parts := stringsSplit(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid hook path", http.StatusBadRequest)
		return
	}
	agentName := parts[3]

	p, exists := s.providers[agentName]
	if !exists {
		http.Error(w, fmt.Sprintf("Unsupported agent provider: %s", agentName), http.StatusBadRequest)
		return
	}

	// Read hook payload
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}

	// Fast response to prevent blocking the agent loop
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"enqueued"}`))

	// Process in a goroutine so ingest is single-digit milliseconds
	go s.processHookEvent(p, r.URL.Query().Get("event"), r.Header.Get("X-Ackbar-Host"), body)
}

func (s *Server) processHookEvent(p Provider, urlEventName string, headerHost string, body []byte) {
	event, err := p.ParseHook(urlEventName, body)
	if err != nil {
		log.Printf("Error parsing hook payload: %v", err)
		return
	}
	if event == nil {
		return
	}

	host := headerHost
	if host == "" {
		host = "local"
	}

	// ID is "{agent}:{host}:{nativeID}"
	sessionID := fmt.Sprintf("%s:%s:%s", event.Agent, host, event.NativeID)

	// Load existing or initialize new
	sess, err := s.db.GetSession(sessionID)
	if err != nil {
		log.Printf("Error retrieving session %s: %v", sessionID, err)
		return
	}

	if sess == nil {
		// Look for a spawning managed session to adopt
		spawningSess, err := s.findSpawningSession(event.Agent, event.Cwd)
		if err == nil && spawningSess != nil {
			// Adopt! Delete the spawning temp record
			_ = s.db.DeleteSession(spawningSess.ID)

			sess = &Session{
				ID:          sessionID,
				Agent:       event.Agent,
				Host:        host,
				NativeID:    event.NativeID,
				Managed:     true,
				TmuxName:    spawningSess.TmuxName,
				NodePath:    spawningSess.NodePath,
				CustomTitle: spawningSess.CustomTitle,
				StartedAt:   spawningSess.StartedAt,
				LastEventAt: spawningSess.LastEventAt,
			}
			if spawningSess.CustomTitle != "" {
				sess.Name = spawningSess.CustomTitle
			} else if spawningSess.Name != "" && !isRawSessionName(spawningSess.Name) {
				sess.Name = spawningSess.Name
			}
		} else if activeManaged, err := s.findActiveManagedSessionInCwd(event.Agent, host, event.Cwd); err == nil && activeManaged != nil && activeManaged.NativeID != event.NativeID {
			// An active managed tmux session rotated its internal conversation ID (e.g. via /clear or /reset)
			reConv := regexp.MustCompile(`\s*\(Conv\s*(\d+)\)$`)
			var newTurnName string
			baseTitle := activeManaged.CustomTitle
			if baseTitle == "" {
				baseTitle = activeManaged.Name
			}
			if baseTitle == "" || isRawSessionName(baseTitle) {
				baseTitle = activeManaged.Agent
			}

			if m := reConv.FindStringSubmatch(baseTitle); len(m) == 2 {
				num, _ := strconv.Atoi(m[1])
				cleanBase := strings.TrimSpace(reConv.ReplaceAllString(baseTitle, ""))
				newTurnName = fmt.Sprintf("%s (Conv %d)", cleanBase, num+1)
			} else {
				activeManaged.Name = fmt.Sprintf("%s (Conv 1)", baseTitle)
				newTurnName = fmt.Sprintf("%s (Conv 2)", baseTitle)
			}

			// Archive previous conversation turn
			activeManaged.Managed = false
			activeManaged.State = StateEnded
			activeManaged.Activity = "Cleared (context reset)"
			activeManaged.LastEventAt = time.Now()
			_ = s.db.SaveSession(activeManaged)
			s.broadcast(activeManaged)

			// Adopt the live tmux supervisor for the new conversation turn
			sess = &Session{
				ID:         sessionID,
				Agent:      event.Agent,
				Host:       host,
				NativeID:   event.NativeID,
				Managed:    true,
				TmuxName:   activeManaged.TmuxName,
				NodePath:   activeManaged.NodePath,
				ProjectKey: activeManaged.ProjectKey,
				Name:       newTurnName,
				StartedAt:  time.Now(),
			}
		} else {
			sess = &Session{
				ID:        sessionID,
				Agent:     event.Agent,
				Host:      host,
				NativeID:  event.NativeID,
				StartedAt: time.Now(),
			}
		}
	}

	// Update fields
	if event.Cwd != "" {
		sess.Cwd = event.Cwd
	}
	if sess.NodePath == "" && sess.Cwd != "" {
		sess.NodePath = s.resolveSessionNodePath(sess.Cwd)
	}
	if len(event.Roots) > 0 {
		sess.Roots = event.Roots
	}
	// Respect Custom Title priority (never overwrite user-given custom title)
	if sess.CustomTitle != "" {
		sess.Name = sess.CustomTitle
	} else if event.Name != "" && !isRawSessionName(event.Name) {
		// Only adopt event.Name if session doesn't already have an established non-raw name,
		// or if current name is raw/empty
		if sess.Name == "" || isRawSessionName(sess.Name) {
			sess.Name = event.Name
		}
	} else if isRawSessionName(sess.Name) {
		// Attempt title resolution from disk
		if sess.Agent == "antigravity" {
			if title := ReadAntigravitySessionTitle(sess.Cwd, sess.NativeID); title != "" && !isRawSessionName(title) {
				sess.Name = title
			}
		} else if sess.Agent == "claude-code" {
			if meta := ReadClaudeSessionMeta(sess.Cwd, sess.NativeID); meta != nil && meta.Title != "" {
				sess.Name = meta.Title
				if meta.CustomTitle != "" {
					sess.CustomTitle = meta.CustomTitle
				}
				if meta.AITitle != "" {
					sess.AITitle = meta.AITitle
				}
			}
		}
	}
	isStateChange := sess.State != event.State
	prevState := sess.State
	if isStateChange {
		if event.State != StateWorking {
			sess.IsUnread = true
			sess.LastStateChangeAt = time.Now()
		} else {
			sess.IsUnread = false
		}
	}
	sess.State = event.State
	if event.State != StateBlocked {
		sess.Blocked = nil
	} else {
		sess.Blocked = event.Blocked
	}
	sess.Activity = event.Activity

	// Only update LastEventAt on genuine user interaction or agent activity:
	// - Brand new session initialization (sess.LastEventAt is zero)
	// - User prompt submission ("userpromptsubmit", "usersubmit", "user_prompt")
	// - Agent tool execution, progress, turn completion ("pretooluse", "posttooluse", "stop", "sessionend", "permissionrequest", "notification")
	// - Active working or blocked state transitions (into StateWorking, into StateBlocked, or StateWorking -> StateIdle)
	// Do NOT update LastEventAt on passive process boot / reconnect on an existing session ("sessionstart", "start", or StateEnded/Unknown -> StateIdle)
	evtLower := strings.ToLower(event.EventName)
	shouldUpdateLastEvent := false

	if sess.LastEventAt.IsZero() {
		shouldUpdateLastEvent = true
	} else if evtLower == "sessionstart" || evtLower == "start" {
		shouldUpdateLastEvent = false
	} else if evtLower == "userpromptsubmit" || evtLower == "usersubmit" || evtLower == "user_prompt" {
		shouldUpdateLastEvent = true
	} else if evtLower == "pretooluse" || evtLower == "posttooluse" || evtLower == "stop" || evtLower == "sessionend" || evtLower == "permissionrequest" || evtLower == "permission_request" || evtLower == "notification" {
		shouldUpdateLastEvent = true
	} else if isStateChange {
		if event.State == StateWorking || event.State == StateBlocked || (prevState == StateWorking && event.State == StateIdle) {
			shouldUpdateLastEvent = true
		}
	}

	if shouldUpdateLastEvent {
		sess.LastEventAt = event.LastEventAt
	}

	// Resolve project key if not populated or if Cwd changed
	if sess.ProjectKey == "" || (event.Cwd != "" && event.Cwd != sess.Cwd) {
		sess.ProjectKey = GetProjectKey(sess.Cwd)
	}

	// Save to SQLite
	if err := s.db.SaveSession(sess); err != nil {
		log.Printf("Error saving session: %v", err)
		return
	}

	// Broadcast update
	s.broadcast(sess)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessions, err := s.db.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Only verify liveness on active sessions to avoid endpoint latency & exec overhead
	var active []*Session
	for _, sess := range sessions {
		if sess.State != StateEnded {
			active = append(active, sess)
		}
	}
	if len(active) > 0 {
		s.verifySessionLiveness(r.Context(), active)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessions)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan *Session, 10)
	s.subMutex.Lock()
	s.subscribers[ch] = true
	s.subMutex.Unlock()

	defer func() {
		s.subMutex.Lock()
		delete(s.subscribers, ch)
		s.subMutex.Unlock()
		close(ch)
	}()

	// Send initial list of sessions on connect
	sessions, err := s.db.ListSessions()
	if err == nil {
		var active []*Session
		for _, sess := range sessions {
			if sess.State != StateEnded {
				active = append(active, sess)
			}
		}
		if len(active) > 0 {
			s.verifySessionLiveness(r.Context(), active)
		}
		for _, s := range sessions {
			data, err := json.Marshal(s)
			if err == nil {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
		}
		flusher.Flush()
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case sess := <-ch:
			data, err := json.Marshal(sess)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) broadcast(sess *Session) {
	s.subMutex.Lock()
	defer s.subMutex.Unlock()

	for ch := range s.subscribers {
		select {
		case ch <- sess:
		default:
			// slow consumer, drop event
		}
	}
}

// Simple helper to split strings without importing strings package everywhere
func stringsSplit(s, sep string) []string {
	if s == "" {
		return nil
	}
	var res []string
	start := 0
	for i := 0; i+len(sep) <= len(s); {
		if s[i:i+len(sep)] == sep {
			res = append(res, s[start:i])
			start = i + len(sep)
			i = start
		} else {
			i++
		}
	}
	res = append(res, s[start:])
	return res
}

func (s *Server) handleSessionControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("id")
	action := r.URL.Query().Get("action")

	if sessionID == "" || action == "" {
		parts := stringsSplit(r.URL.Path, "/")
		if len(parts) >= 5 {
			sessionID = parts[3]
			action = parts[4]
		}
	}

	if sessionID == "" || action == "" {
		http.Error(w, "Invalid control parameters", http.StatusBadRequest)
		return
	}

	sess, err := s.db.GetSession(sessionID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if sess == nil {
		// Fallback 1: Try with local host alias if sessionID had a remote alias or vice-versa
		parts := strings.Split(sessionID, ":")
		if len(parts) == 3 {
			localID := fmt.Sprintf("%s:local:%s", parts[0], parts[2])
			sess, _ = s.db.GetSession(localID)
		}
	}
	if sess == nil {
		// Fallback 2: Check by NativeID match
		if all, err := s.db.ListSessions(); err == nil {
			for _, sRecord := range all {
				if sRecord.NativeID != "" && strings.HasSuffix(sessionID, ":"+sRecord.NativeID) {
					sess = sRecord
					break
				}
			}
		}
	}
	if sess == nil {
		// Fallback 3: If sessionID specifies a remote host, forward control action to remote host
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 2 && parts[1] != "" && parts[1] != "local" {
			targetHost := parts[1]
			hostRec, err := s.db.GetHost(targetHost)
			if err != nil || hostRec == nil {
				if allHosts, lerr := s.db.ListHosts(); lerr == nil {
					for _, h := range allHosts {
						if h.Name == targetHost || strings.HasSuffix(h.Name, "@"+targetHost) || strings.Contains(h.Name, targetHost) {
							hostRec = h
							break
						}
					}
				}
			}
			if hostRec != nil && hostRec.URL != "" {
				targetURL := fmt.Sprintf("%s/v1/sessions/control?id=%s&action=%s", strings.TrimSuffix(hostRec.URL, "/"), url.QueryEscape(sessionID), url.QueryEscape(action))
				fwdReq, err := http.NewRequest(r.Method, targetURL, r.Body)
				if err == nil {
					fwdReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))
					if resp, err := http.DefaultClient.Do(fwdReq); err == nil {
						defer resp.Body.Close()
						w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
						w.WriteHeader(resp.StatusCode)
						_, _ = io.Copy(w, resp.Body)
						return
					}
				}
			}
		}
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	switch action {
	case "archive":
		sess.Archived = true
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"archived"}`))

	case "unarchive":
		sess.Archived = false
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"unarchived"}`))

	case "documents":
		files, err := os.ReadDir(sess.Cwd)
		if err != nil {
			http.Error(w, "Failed to read workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var docs []string
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			nameLower := strings.ToLower(name)
			if strings.HasSuffix(nameLower, ".md") || strings.HasSuffix(nameLower, ".txt") || strings.HasSuffix(nameLower, ".jsonl") || nameLower == "gemini.md" || nameLower == "agents.md" {
				docs = append(docs, name)
			}
		}

		sort.Slice(docs, func(i, j int) bool {
			pi := getDocPriority(docs[i])
			pj := getDocPriority(docs[j])
			if pi != pj {
				return pi > pj
			}
			return docs[i] < docs[j]
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docs)

	case "kill":
		if sess.Managed && sess.TmuxName != "" {
			_ = tmux.Kill(r.Context(), sess.TmuxName)
		}
		if sess.PID > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(sess.PID)).Run()
		}

		sess.State = StateEnded
		sess.Activity = "Terminated by user"
		sess.LastEventAt = time.Now()

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"killed"}`))

	case "delete":
		if sess.Managed && sess.TmuxName != "" {
			_ = tmux.Kill(r.Context(), sess.TmuxName)
		}
		if sess.PID > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(sess.PID)).Run()
		}

		s.deleteSessionFilesOnDisk(sess)

		_ = s.db.MarkSessionDeleted(sess.ID)
		if sess.NativeID != "" {
			_ = s.db.MarkSessionDeleted(sess.NativeID)
		}
		_ = s.db.DeleteSession(sess.ID)
		if sess.NativeID != "" {
			_ = s.db.DeleteSession(sess.NativeID)
		}

		sess.Deleted = true
		sess.State = StateEnded
		sess.Activity = "Deleted"
		sess.LastEventAt = time.Now()
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "id": sess.ID})

	case "restart":
		if sess.Cwd == "" {
			http.Error(w, "Session working directory is empty", http.StatusBadRequest)
			return
		}

		// Kill existing process/tmux if running
		if sess.Managed && sess.TmuxName != "" {
			_ = tmux.Kill(r.Context(), sess.TmuxName)
		}
		if !sess.Managed && sess.PID > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(sess.PID)).Run()
		}

		// Generate new tmux name if it didn't have one
		tmuxName := sess.TmuxName
		if tmuxName == "" {
			tmuxName = fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
		}

		// Spawn new tmux session
		resumeCmd := s.getResumeCmd(sess.Agent, sess.NativeID)
		err := tmux.Spawn(r.Context(), tmuxName, sess.Cwd, resumeCmd)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to spawn tmux: %v", err), http.StatusInternalServerError)
			return
		}

		// Update state to working/restarting
		sess.Managed = true
		sess.TmuxName = tmuxName
		sess.State = StateWorking
		sess.Activity = "Restarting session..."
		sess.LastEventAt = time.Now()

		// Get and update PID
		if pid, err := tmux.GetPID(r.Context(), tmuxName); err == nil {
			sess.PID = pid
		}

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"restarted"}`))

	case "resume":
		if sess.Cwd == "" {
			http.Error(w, "Session working directory is empty", http.StatusBadRequest)
			return
		}

		tmuxName := sess.TmuxName
		if tmuxName == "" {
			tmuxName = fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
		}
		_ = tmux.Kill(r.Context(), tmuxName)

		resumeCmd := s.getResumeCmd(sess.Agent, sess.NativeID)
		err := tmux.Spawn(r.Context(), tmuxName, sess.Cwd, resumeCmd)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to spawn tmux session: %v", err), http.StatusInternalServerError)
			return
		}

		sess.Managed = true
		sess.TmuxName = tmuxName
		sess.State = StateWorking
		sess.Activity = "Resumed session"
		sess.LastEventAt = time.Now()

		if pid, err := tmux.GetPID(r.Context(), tmuxName); err == nil {
			sess.PID = pid
		}

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "resumed",
			"id":        sess.ID,
			"tmux_name": tmuxName,
			"session":   sess,
		})

	case "move":
		nodePath := r.URL.Query().Get("node_path")
		if nodePath == "" {
			var bodyData struct {
				NodePath string `json:"node_path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&bodyData)
			nodePath = bodyData.NodePath
		}
		if err := s.db.MoveSessionNode(sess.ID, nodePath); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sess.NodePath = nodePath
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"moved"}`))

	case "rename":
		name := r.URL.Query().Get("name")
		if name == "" {
			var bodyData struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&bodyData)
			name = bodyData.Name
		}
		sess.Name = name
		sess.CustomTitle = name
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		cacheKey := fmt.Sprintf("%s:%s", sess.Cwd, sess.NativeID)
		titleCacheMutex.Lock()
		titleCache[cacheKey] = TitleCacheEntry{
			Title:     name,
			Source:    "custom",
			UpdatedAt: time.Now(),
		}
		titleCacheMutex.Unlock()

		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"renamed"}`))

	case "open_editor":
		if sess.Cwd == "" {
			http.Error(w, "Session Cwd is empty", http.StatusBadRequest)
			return
		}
		uri, err := LaunchVSCode(sess.Cwd, sess.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened", "path": sess.Cwd, "host": sess.Host, "uri": uri})

	case "respond":
		value := r.URL.Query().Get("value")
		respAction := r.URL.Query().Get("resp_action")
		if respAction == "" {
			respAction = r.URL.Query().Get("action_type")
		}
		if respAction == "" {
			var bodyData struct {
				Action string `json:"action"`
				Value  string `json:"value"`
			}
			_ = json.NewDecoder(r.Body).Decode(&bodyData)
			if bodyData.Action != "" {
				respAction = bodyData.Action
			}
			if bodyData.Value != "" {
				value = bodyData.Value
			}
		}
		if respAction == "" {
			respAction = "answer"
		}
		actionLower := strings.ToLower(respAction)
		if (sess.Managed || sess.TmuxName != "") && sess.TmuxName != "" {
			switch actionLower {
			case "answer", "input":
				_ = tmux.SendInput(r.Context(), sess.TmuxName, value, true)
			case "allow", "yes", "proceed", "1":
				if sess.Agent == "antigravity" {
					_ = tmux.SendKeys(r.Context(), sess.TmuxName, "1", "Enter")
				} else {
					_ = tmux.SendKeys(r.Context(), sess.TmuxName, "y", "Enter")
				}
			case "deny", "no", "4":
				if sess.Agent == "antigravity" {
					_ = tmux.SendKeys(r.Context(), sess.TmuxName, "4", "Enter")
				} else {
					_ = tmux.SendKeys(r.Context(), sess.TmuxName, "n", "Enter")
				}
			}
		}
		sess.State = StateWorking
		sess.Blocked = nil
		sess.LastEventAt = time.Now()
		switch actionLower {
		case "allow":
			sess.Activity = "Permission allowed"
		case "deny":
			sess.Activity = "Permission denied"
		case "answer", "input":
			if value != "" {
				sess.Activity = fmt.Sprintf("Answered: %s", value)
			} else {
				sess.Activity = "User answered"
			}
		}
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "responded",
			"id":      sess.ID,
			"action":  actionLower,
			"value":   value,
			"state":   sess.State.String(),
			"session": sess,
		})

	case "read", "mark_read", "view":
		sess.IsUnread = false
		_ = s.db.MarkSessionRead(sess.ID)
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "read",
			"id":      sess.ID,
			"session": sess,
		})

	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
}

func (s *Server) handleRespond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionId"`
		Action    string `json:"action"` // "answer" | "allow" | "deny" | "input"
		Value     string `json:"value"`
	}

	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	targetID := req.ID
	if targetID == "" {
		targetID = req.SessionID
	}
	if targetID == "" {
		targetID = r.URL.Query().Get("id")
	}
	if targetID == "" {
		targetID = r.URL.Query().Get("sessionId")
	}

	action := req.Action
	if action == "" {
		action = r.URL.Query().Get("action")
	}

	value := req.Value
	if value == "" {
		value = r.URL.Query().Get("value")
	}

	if targetID == "" || action == "" {
		http.Error(w, "Missing required parameters (id, action)", http.StatusBadRequest)
		return
	}

	sess, err := s.db.GetSession(targetID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}

	if sess == nil {
		// Fallback 1: Try with local host alias
		parts := strings.Split(targetID, ":")
		if len(parts) == 3 {
			localID := fmt.Sprintf("%s:local:%s", parts[0], parts[2])
			sess, _ = s.db.GetSession(localID)
		}
	}

	if sess == nil {
		// Fallback 2: Check by NativeID match
		if all, err := s.db.ListSessions(); err == nil {
			for _, sRecord := range all {
				if sRecord.NativeID != "" && (sRecord.NativeID == targetID || strings.HasSuffix(targetID, ":"+sRecord.NativeID)) {
					sess = sRecord
					break
				}
			}
		}
	}

	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	actionLower := strings.ToLower(action)

	// Send keystrokes if session is managed in tmux
	if sess.Managed && sess.TmuxName != "" {
		switch actionLower {
		case "answer", "input":
			if err := tmux.SendInput(r.Context(), sess.TmuxName, value, true); err != nil {
				log.Printf("Warning: failed to send input to tmux session %s: %v", sess.TmuxName, err)
			}
		case "allow":
			if err := tmux.SendKeys(r.Context(), sess.TmuxName, "y", "Enter"); err != nil {
				log.Printf("Warning: failed to send allow to tmux session %s: %v", sess.TmuxName, err)
			}
		case "deny":
			if err := tmux.SendKeys(r.Context(), sess.TmuxName, "n", "Enter"); err != nil {
				log.Printf("Warning: failed to send deny to tmux session %s: %v", sess.TmuxName, err)
			}
		default:
			http.Error(w, fmt.Sprintf("Unsupported action: %s", action), http.StatusBadRequest)
			return
		}
	}

	// Update session state to StateWorking, clear Blocked, update LastEventAt
	sess.State = StateWorking
	sess.Blocked = nil
	sess.LastEventAt = time.Now()
	switch actionLower {
	case "allow":
		sess.Activity = "Permission allowed"
	case "deny":
		sess.Activity = "Permission denied"
	case "answer", "input":
		if value != "" {
			sess.Activity = fmt.Sprintf("Answered: %s", value)
		} else {
			sess.Activity = "User answered"
		}
	}

	if err := s.db.SaveSession(sess); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save session: %v", err), http.StatusInternalServerError)
		return
	}

	// Broadcast update via SSE
	s.broadcast(sess)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "responded",
		"id":      sess.ID,
		"action":  actionLower,
		"value":   value,
		"state":   sess.State.String(),
		"session": sess,
	})
}

func (s *Server) handleSessionTranscript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("id")
	if sessionID == "" {
		http.Error(w, "Missing id parameter", http.StatusBadRequest)
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format")) // "json", "ansi", "markdown"
	if format == "" {
		format = "json"
	}

	sess, _ := s.db.GetSession(sessionID)
	agent := "claude-code"
	nativeID := sessionID
	cwd := ""
	title := ""

	if sess != nil {
		agent = sess.Agent
		nativeID = sess.NativeID
		cwd = sess.Cwd
		title = sess.Name
	} else {
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 3 {
			agent = parts[0]
			nativeID = parts[2]
		}
	}

	transcript, err := s.ExtractTranscript(agent, nativeID, cwd)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to extract transcript: %v", err), http.StatusNotFound)
		return
	}
	if title != "" {
		transcript.Title = title
	} else if sess != nil {
		transcript.Title = sess.Name
	}

	switch format {
	case "ansi":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(FormatTranscriptANSI(transcript)))
	case "markdown", "md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(FormatTranscriptMarkdown(transcript)))
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"session_id": sessionID,
			"native_id":  nativeID,
			"agent":      agent,
			"title":      transcript.Title,
			"cwd":        transcript.Cwd,
			"messages":   transcript.Messages,
			"ansi":       FormatTranscriptANSI(transcript),
			"markdown":   FormatTranscriptMarkdown(transcript),
		})
	}
}

func (s *Server) handleEditorOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	host := r.URL.Query().Get("host")
	if path == "" {
		var bodyData struct {
			Path string `json:"path"`
			Host string `json:"host"`
		}
		_ = json.NewDecoder(r.Body).Decode(&bodyData)
		path = bodyData.Path
		if host == "" {
			host = bodyData.Host
		}
	}
	if path == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	uri, err := LaunchVSCode(path, host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened", "path": path, "host": host, "uri": uri})
}

func (s *Server) ExtractTranscript(agent, nativeID, cwd string) (*Transcript, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	t := &Transcript{
		NativeID: nativeID,
		Agent:    agent,
		Cwd:      cwd,
		Messages: make([]TranscriptMessage, 0),
	}

	if p, ok := s.providers[agent]; ok {
		msgs, err := p.ExtractTranscript(home, cwd, nativeID)
		if err == nil && len(msgs) > 0 {
			t.Messages = msgs
			return t, nil
		}
	}

	for _, p := range s.providers {
		msgs, err := p.ExtractTranscript(home, cwd, nativeID)
		if err == nil && len(msgs) > 0 {
			t.Messages = msgs
			t.Agent = p.Agent()
			return t, nil
		}
	}

	// Legacy fallback
	return ExtractTranscript(agent, nativeID, cwd)
}

func (s *Server) deleteSessionFilesOnDisk(sess *Session) {
	if sess == nil {
		return
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return
	}

	nativeID := sess.NativeID
	if nativeID == "" {
		parts := strings.Split(sess.ID, ":")
		if len(parts) >= 3 {
			nativeID = parts[2]
		}
	}
	if nativeID == "" {
		return
	}

	if p, ok := s.providers[sess.Agent]; ok {
		_ = p.CleanSessionFiles(home, sess.Cwd, nativeID)
		return
	}

	// Fallback to all providers
	for _, p := range s.providers {
		_ = p.CleanSessionFiles(home, sess.Cwd, nativeID)
	}
}

func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Agent    string `json:"agent"`
		Cwd      string `json:"cwd"`
		Host     string `json:"host"`
		NodePath string `json:"node_path"`
		Name     string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Agent == "" || req.Cwd == "" {
		http.Error(w, "Missing agent or cwd", http.StatusBadRequest)
		return
	}

	if req.NodePath == "" {
		req.NodePath = s.resolveSessionNodePath(req.Cwd)
	}

	// 1. Forward to remote host if specified
	if req.Host != "" && req.Host != "local" {
		hostRec, err := s.db.GetHost(req.Host)
		if err != nil || hostRec == nil {
			if allHosts, lerr := s.db.ListHosts(); lerr == nil {
				for _, h := range allHosts {
					if h.Name == req.Host || strings.HasSuffix(h.Name, "@"+req.Host) || strings.Contains(h.Name, req.Host) {
						hostRec = h
						break
					}
				}
			}
		}
		if hostRec != nil && hostRec.URL != "" {
			targetURL := strings.TrimSuffix(hostRec.URL, "/") + "/v1/sessions/spawn"
			payload, _ := json.Marshal(map[string]string{
				"agent":     req.Agent,
				"cwd":       req.Cwd,
				"node_path": req.NodePath,
				"name":      req.Name,
			})
			resp, err := http.Post(targetURL, "application/json", bytes.NewBuffer(payload))
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to spawn on remote host %s: %v", req.Host, err), http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		} else {
			http.Error(w, fmt.Sprintf("Remote host '%s' not found or unreachable", req.Host), http.StatusNotFound)
			return
		}
	}

	// Expand ~ to user home directory and ensure directory exists
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(req.Cwd, "~/") && home != "" {
		req.Cwd = filepath.Join(home, req.Cwd[2:])
	} else if req.Cwd == "~" && home != "" {
		req.Cwd = home
	}
	_ = os.MkdirAll(req.Cwd, 0755)

	tempUUID := generateUUID()
	tmuxName := fmt.Sprintf("ackbar-%s-%s", req.Agent, tempUUID)
	launchCmd := s.getSpawnCmd(req.Agent, tempUUID)

	err := tmux.Spawn(r.Context(), tmuxName, req.Cwd, launchCmd)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to spawn session: %v", err), http.StatusInternalServerError)
		return
	}

	// Insert temporary spawning session
	sess := &Session{
		ID:          fmt.Sprintf("%s:local:%s", req.Agent, tempUUID),
		Agent:       req.Agent,
		Host:        "local",
		NativeID:    tempUUID,
		Cwd:         req.Cwd,
		NodePath:    req.NodePath,
		Name:        req.Name,
		Managed:     true,
		TmuxName:    tmuxName,
		State:       StateUnknown,
		Activity:    "Spawning session...",
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if req.Name != "" {
		sess.CustomTitle = req.Name
	}

	if pid, err := tmux.GetPID(r.Context(), tmuxName); err == nil {
		sess.PID = pid
	}

	if err := s.db.SaveSession(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.broadcast(sess)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":     "spawning",
		"session_id": tempUUID,
	})
}

func (s *Server) findSpawningSession(agent, cwd string) (*Session, error) {
	sessions, err := s.db.ListSessions()
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if sess.Agent == agent && sess.Cwd == cwd && sess.Managed && sess.State == StateUnknown {
			return sess, nil
		}
	}
	return nil, nil
}

func (s *Server) findActiveManagedSessionInCwd(agent, host, cwd string) (*Session, error) {
	if cwd == "" {
		return nil, nil
	}
	sessions, err := s.db.ListSessions()
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if sess.Agent == agent && sess.Host == host && sess.Cwd == cwd && sess.Managed && sess.State != StateEnded && sess.TmuxName != "" {
			return sess, nil
		}
	}
	return nil, nil
}

func isValidUUID(u string) bool {
	if len(u) != 36 {
		return false
	}
	for i, c := range u {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Server) getResumeCmd(agent, nativeID string) string {
	if strings.HasPrefix(nativeID, "proc-") {
		nativeID = ""
	}
	if p, ok := s.providers[agent]; ok {
		return p.GetResumeCommand(nativeID)
	}
	if nativeID != "" && isValidUUID(nativeID) {
		return "claude --resume " + nativeID
	}
	return "claude"
}

func (s *Server) getSpawnCmd(agent, tempUUID string) string {
	if p, ok := s.providers[agent]; ok {
		return p.GetSpawnCommand(tempUUID)
	}
	if agent == "mock-agent" {
		return "sleep 5"
	}
	if tempUUID != "" && isValidUUID(tempUUID) {
		return "claude --session-id " + tempUUID
	}
	return "claude"
}

func classifyDoc(name string) (category, label string, priority int) {
	nl := strings.ToLower(name)
	switch {
	case nl == "task.md" || nl == "implementation_plan.md" || nl == "walkthrough.md":
		return "plan", "Active Plan", 10
	case strings.Contains(nl, "plan") || strings.Contains(nl, "todo") || strings.Contains(nl, "backlog"):
		return "plan", "Plan / Task", 8
	case nl == "agents.md" || nl == "claude.md" || nl == "architecture.md":
		return "guidelines", "Guidelines", 7
	case nl == "readme.md" || strings.HasPrefix(nl, "readme"):
		return "project", "Readme", 5
	case strings.Contains(nl, "prd") || strings.Contains(nl, "rfc") || strings.Contains(nl, "spec"):
		return "project", "Specification", 6
	case strings.Contains(nl, "guide") || strings.Contains(nl, "handover"):
		return "guidelines", "Guide", 6
	default:
		return "other", "Document", 2
	}
}

func getDocPriority(name string) int {
	_, _, prio := classifyDoc(name)
	return prio
}

type AgentDiscoveryResult struct {
	Agent          string `json:"agent"`
	DisplayName    string `json:"display_name,omitempty"`
	Installed      bool   `json:"installed"`
	HookConfigured bool   `json:"hook_configured"`
	SetupCmd       string `json:"setup_cmd"`
}

func (s *Server) handleAgentDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var results []AgentDiscoveryResult
	for name, p := range s.providers {
		installed := p.IsInstalled()
		configured, setupCmd, _ := p.CheckHookConfig()

		results = append(results, AgentDiscoveryResult{
			Agent:          name,
			DisplayName:    p.DisplayName(),
			Installed:      installed,
			HookConfigured: configured,
			SetupCmd:       setupCmd,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var results []ProviderDTO
	for _, p := range s.providers {
		configured, setupCmd, _ := p.CheckHookConfig()
		results = append(results, ProviderDTO{
			Agent:          p.Agent(),
			DisplayName:    p.DisplayName(),
			BrandColor:     p.BrandColor(),
			IconSVG:        p.IconSVG(),
			IsInstalled:    p.IsInstalled(),
			HookConfigured: configured,
			SetupCmd:       setupCmd,
			ProcessNames:   p.ProcessNames(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

type DocumentItem struct {
	Title         string `json:"title"`
	Path          string `json:"path"`
	RelPath       string `json:"rel_path"`
	Category      string `json:"category"`
	CategoryLabel string `json:"category_label"`
	Priority      int    `json:"priority"`
	Size          int64  `json:"size"`
	ModTime       string `json:"mod_time,omitempty"`
}

func (s *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cwd := r.URL.Query().Get("cwd")
	agent := r.URL.Query().Get("agent")
	nativeID := r.URL.Query().Get("native_id")
	sessionID := r.URL.Query().Get("session_id")

	// If sessionID is provided, attempt to resolve session metadata
	if sessionID != "" {
		if sess, err := s.db.GetSession(sessionID); err == nil && sess != nil {
			if cwd == "" {
				cwd = sess.Cwd
			}
			if agent == "" {
				agent = sess.Agent
			}
			if nativeID == "" {
				nativeID = sess.NativeID
			}
		}
	}

	if cwd == "" {
		cwd = os.Getenv("HOME")
	}

	var docs []DocumentItem
	seen := make(map[string]bool)

	addDoc := func(fullPath, title, relPath, category, categoryLabel string, priority int) {
		if seen[fullPath] {
			return
		}
		fi, err := os.Stat(fullPath)
		if err != nil || fi.IsDir() {
			return
		}
		seen[fullPath] = true
		docs = append(docs, DocumentItem{
			Title:         title,
			Path:          fullPath,
			RelPath:       relPath,
			Category:      category,
			CategoryLabel: categoryLabel,
			Priority:      priority,
			Size:          fi.Size(),
			ModTime:       fi.ModTime().Format(time.RFC3339),
		})
	}

	// 1. Scan root project directory for markdown files
	if entries, err := os.ReadDir(cwd); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			nameLower := strings.ToLower(name)
			if strings.HasSuffix(nameLower, ".md") || strings.HasSuffix(nameLower, ".markdown") {
				full := filepath.Join(cwd, name)
				cat, label, prio := classifyDoc(name)
				addDoc(full, name, name, cat, label, prio)
			}
		}
	}

	// 2. Scan docs/ and doc/ subdirectories (up to 3 levels deep)
	for _, docDirName := range []string{"docs", "doc", "documentation"} {
		docDir := filepath.Join(cwd, docDirName)
		if dirExists(docDir) {
			_ = filepath.WalkDir(docDir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d == nil {
					return nil
				}
				if d.IsDir() {
					rel, _ := filepath.Rel(docDir, p)
					if strings.Count(rel, string(filepath.Separator)) > 3 {
						return filepath.SkipDir
					}
					return nil
				}
				nameLower := strings.ToLower(d.Name())
				if strings.HasSuffix(nameLower, ".md") || strings.HasSuffix(nameLower, ".markdown") {
					rel, _ := filepath.Rel(cwd, p)
					addDoc(p, d.Name(), rel, "docs", "Project Docs", 4)
				}
				return nil
			})
		}
	}

	// 3. Scan .claude/ directory if present
	claudeDir := filepath.Join(cwd, ".claude")
	if dirExists(claudeDir) {
		_ = filepath.WalkDir(claudeDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if !d.IsDir() {
				nameLower := strings.ToLower(d.Name())
				if strings.HasSuffix(nameLower, ".md") {
					rel, _ := filepath.Rel(cwd, p)
					cat, label, prio := classifyDoc(d.Name())
					if cat == "other" {
						cat, label = "plan", "Claude Plan"
					}
					addDoc(p, d.Name(), rel, cat, label, prio)
				}
			}
			return nil
		})
	}

	// 4. Antigravity Brain Artifacts — ONLY for the specific active session!
	// NEVER dump all past Antigravity conversation brain folders into unrelated sessions!
	if (agent == "antigravity" || strings.Contains(strings.ToLower(agent), "antigravity") || strings.Contains(strings.ToLower(agent), "agy")) && nativeID != "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			candidateDirs := []string{
				filepath.Join(home, ".gemini", "antigravity", "brain", nativeID),
				filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID),
				filepath.Join(home, ".antigravity", "brain", nativeID),
			}
			for _, convBrainDir := range candidateDirs {
				if dirExists(convBrainDir) {
					if files, ferr := os.ReadDir(convBrainDir); ferr == nil {
						for _, f := range files {
							if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
								full := filepath.Join(convBrainDir, f.Name())
								cat, label, prio := classifyDoc(f.Name())
								if cat == "other" {
									cat, label = "plan", "Antigravity Plan"
								}
								addDoc(full, fmt.Sprintf("⚡ %s", f.Name()), f.Name(), cat, label, prio+5)
							}
						}
					}
				}
			}
		}
	}

	// Sort by Priority descending, then ModTime descending, then Title
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Priority != docs[j].Priority {
			return docs[i].Priority > docs[j].Priority
		}
		if docs[i].ModTime != docs[j].ModTime {
			return docs[i].ModTime > docs[j].ModTime
		}
		return docs[i].Title < docs[j].Title
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(docs)
}

func (s *Server) handleDocumentContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	docPath := r.URL.Query().Get("path")
	if docPath == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	content, err := os.ReadFile(docPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read file: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"path":    docPath,
		"content": string(content),
	})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		nodes, err := s.db.ListNodes()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nodes)

	case http.MethodPost:
		var node TreeNode
		if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		if node.Path == "" {
			http.Error(w, "Missing node path", http.StatusBadRequest)
			return
		}
		node.CreatedAt = time.Now()
		if err := s.db.SaveNode(&node); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"saved"}`))

	case http.MethodDelete:
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "Missing path parameter", http.StatusBadRequest)
			return
		}
		if err := s.db.DeleteNode(path); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"deleted"}`))

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		hosts, err := s.db.ListHosts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hosts)

	case http.MethodPost:
		var h HostRecord
		if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		if h.Name == "" {
			http.Error(w, "Missing host name", http.StatusBadRequest)
			return
		}
		h.CreatedAt = time.Now()
		if err := s.db.SaveHost(&h); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"saved"}`))

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "Missing name parameter", http.StatusBadRequest)
			return
		}
		if err := s.db.DeleteHost(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"deleted"}`))

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHostUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hostName := r.URL.Query().Get("name")
	if hostName == "" {
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		hostName = req.Name
	}
	if hostName == "" {
		http.Error(w, "Missing host name", http.StatusBadRequest)
		return
	}

	if hostName == "local" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "Local host daemon is managed directly on this machine (run 'ackbar setup-hooks')",
		})
		return
	}

	host, err := s.db.GetHost(hostName)
	if err != nil || host == nil {
		if allHosts, lerr := s.db.ListHosts(); lerr == nil {
			for _, h := range allHosts {
				if h.Name == hostName || h.SSHTarget == hostName || strings.EqualFold(h.Name, hostName) || strings.Contains(strings.ToLower(h.Name), strings.ToLower(hostName)) {
					host = h
					break
				}
			}
		}
	}
	if host == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Host '%s' not registered", hostName),
		})
		return
	}

	sshTarget := host.SSHTarget
	if sshTarget == "" {
		sshTarget = host.Name
	}

	// 1. Detect target OS and Architecture via SSH
	detectCmd := exec.Command("ssh", "-o", "ConnectTimeout=8", "-o", "BatchMode=yes", sshTarget, "uname -s && uname -m")
	out, err := detectCmd.CombinedOutput()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("SSH connection to '%s' failed: %s (%v)", sshTarget, strings.TrimSpace(string(out)), err),
		})
		return
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Could not parse remote OS/architecture: %q", string(out)),
		})
		return
	}

	targetOSRaw := strings.ToLower(strings.TrimSpace(lines[0]))
	targetArchRaw := strings.ToLower(strings.TrimSpace(lines[1]))

	goos := "linux"
	if strings.Contains(targetOSRaw, "darwin") {
		goos = "darwin"
	} else if strings.Contains(targetOSRaw, "freebsd") {
		goos = "freebsd"
	}

	goarch := "amd64"
	if strings.Contains(targetArchRaw, "aarch64") || strings.Contains(targetArchRaw, "arm64") {
		goarch = "arm64"
	} else if strings.Contains(targetArchRaw, "arm") {
		goarch = "arm"
	} else if strings.Contains(targetArchRaw, "i386") || strings.Contains(targetArchRaw, "686") {
		goarch = "386"
	}

	// 2. Find Go project source directory
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "Work/Ackbar"),
		filepath.Join(home, "src/Ackbar"),
		filepath.Join(home, "Projects/Ackbar"),
		".",
	}
	srcDir := ""
	for _, c := range candidates {
		if _, serr := os.Stat(filepath.Join(c, "cmd/ackbard/main.go")); serr == nil {
			srcDir = c
			break
		}
	}
	if srcDir == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "Ackbar source directory not found to compile binaries",
		})
		return
	}

	// 3. Create temporary directory and cross-compile
	tmpDir, err := os.MkdirTemp("", "ackbar-remote-update-*")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create temp build dir: %v", err), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	ackbardPath := filepath.Join(tmpDir, "ackbard")
	hookPath := filepath.Join(tmpDir, "ackbar-hook")

	cmdBuild1 := exec.Command("go", "build", "-ldflags=-s -w", "-o", ackbardPath, "./cmd/ackbard")
	cmdBuild1.Dir = srcDir
	cmdBuild1.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if bout, berr := cmdBuild1.CombinedOutput(); berr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Failed to cross-compile ackbard for %s/%s: %s", goos, goarch, string(bout)),
		})
		return
	}

	cmdBuild2 := exec.Command("go", "build", "-ldflags=-s -w", "-o", hookPath, "./cmd/ackbar-hook")
	cmdBuild2.Dir = srcDir
	cmdBuild2.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if bout, berr := cmdBuild2.CombinedOutput(); berr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Failed to cross-compile ackbar-hook for %s/%s: %s", goos, goarch, string(bout)),
		})
		return
	}

	// 4. Deploy binaries to remote host via SCP using atomic staging files
	_ = exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "mkdir -p ~/.local/bin").Run()

	scpCmd1 := exec.Command("scp", "-o", "BatchMode=yes", ackbardPath, fmt.Sprintf("%s:~/.local/bin/ackbard.new", sshTarget))
	if sout, serr := scpCmd1.CombinedOutput(); serr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Failed to upload ackbard to %s: %s", sshTarget, string(sout)),
		})
		return
	}

	scpCmd2 := exec.Command("scp", "-o", "BatchMode=yes", hookPath, fmt.Sprintf("%s:~/.local/bin/ackbar-hook.new", sshTarget))
	if sout, serr := scpCmd2.CombinedOutput(); serr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Failed to upload ackbar-hook to %s: %s", sshTarget, string(sout)),
		})
		return
	}

	// 5. Atomically replace binaries, set permissions, and restart daemon on remote host
	remoteRestartScript := `chmod +x ~/.local/bin/ackbard.new ~/.local/bin/ackbar-hook.new && mv -f ~/.local/bin/ackbard.new ~/.local/bin/ackbard && mv -f ~/.local/bin/ackbar-hook.new ~/.local/bin/ackbar-hook && pkill -9 -x ackbard || true; sleep 0.5; nohup ~/.local/bin/ackbard >/dev/null 2>&1 < /dev/null &`
	restartCmd := exec.Command("ssh", "-n", "-f", "-o", "BatchMode=yes", sshTarget, remoteRestartScript)
	_ = restartCmd.Run()

	// 6. Verify remote daemon responsiveness
	time.Sleep(1 * time.Second)
	remoteVersion := version.Version
	if host.URL != "" {
		client := &http.Client{Timeout: 3 * time.Second}
		if resp, err := client.Get(strings.TrimRight(host.URL, "/") + "/v1/version"); err == nil {
			var vResp struct {
				Version string `json:"version"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&vResp); err == nil && vResp.Version != "" {
				remoteVersion = vResp.Version
			}
			_ = resp.Body.Close()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "success",
		"message":     fmt.Sprintf("Successfully upgraded ackbard on '%s' (%s/%s) to v%s", hostName, goos, goarch, remoteVersion),
		"host":        hostName,
		"version":     remoteVersion,
		"target_os":   goos,
		"target_arch": goarch,
	})
}

// killTunnelOnPort terminates any dead/stale SSH tunnel listening on the given local port
// and waits until the port is confirmed released by the operating system.
func killTunnelOnPort(ctx context.Context, port string) error {
	if port == "" {
		return nil
	}

	myPID := fmt.Sprintf("%d", os.Getpid())

	// 1. Identify specifically listening PIDs on this port (avoiding client sockets like Chrome or ackbard)
	lsofOut, _ := exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("lsof -n -iTCP:%s -sTCP:LISTEN -t 2>/dev/null", port)).Output()
	pids := strings.Fields(strings.TrimSpace(string(lsofOut)))
	for _, pid := range pids {
		if pid != "" && pid != myPID {
			_ = exec.CommandContext(ctx, "kill", "-15", pid).Run()
		}
	}

	// 2. Also kill any ssh command specifically forwarding this port to 127.0.0.1
	_ = exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("pkill -9 -f \"ssh.*-L.*%s:127\\.0\\.0\\.1\" 2>/dev/null || true", port)).Run()

	// 3. Force kill listening PIDs if still lingering
	if len(pids) > 0 {
		time.Sleep(100 * time.Millisecond)
		for _, pid := range pids {
			if pid != "" && pid != myPID {
				_ = exec.CommandContext(ctx, "kill", "-9", pid).Run()
			}
		}
	}

	// 4. Poll net.DialTimeout until the port is freed or timeout after 1.5s
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 50*time.Millisecond)
		if err != nil {
			// Port is successfully freed!
			return nil
		}
		conn.Close()
		time.Sleep(50 * time.Millisecond)
	}

	return nil
}

// spawnSSHTunnel safely clears any stale tunnel on port and spawns a new resilient SSH tunnel with forward failure detection.
func spawnSSHTunnel(ctx context.Context, port, sshTarget string) error {
	var lastErr error
	var lastOut string

	for attempt := 1; attempt <= 3; attempt++ {
		_ = killTunnelOnPort(ctx, port)
		time.Sleep(150 * time.Millisecond)

		sshCmd := fmt.Sprintf("ssh -f -o ExitOnForwardFailure=yes -o ConnectTimeout=8 -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -N -L %s:127.0.0.1:7777 %s", port, sshTarget)
		cmd := exec.CommandContext(ctx, "sh", "-c", sshCmd)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = err
		lastOut = strings.TrimSpace(string(out))
		time.Sleep(400 * time.Millisecond)
	}

	if lastOut != "" {
		return fmt.Errorf("%v (%s)", lastErr, lastOut)
	}
	return lastErr
}

func (s *Server) handleHostReconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hostName := r.URL.Query().Get("name")
	if hostName == "" {
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		hostName = req.Name
	}
	if hostName == "" {
		http.Error(w, "Missing host name", http.StatusBadRequest)
		return
	}

	host, err := s.db.GetHost(hostName)
	if err != nil || host == nil {
		if allHosts, lerr := s.db.ListHosts(); lerr == nil {
			for _, h := range allHosts {
				if h.Name == hostName || strings.HasSuffix(h.Name, "@"+hostName) || strings.Contains(h.Name, hostName) {
					host = h
					break
				}
			}
		}
	}
	if host == nil {
		http.Error(w, "Host not found", http.StatusNotFound)
		return
	}

	sshTarget := host.SSHTarget
	if sshTarget == "" {
		sshTarget = host.Name
	}

	// Determine target local port from host.URL (e.g. http://127.0.0.1:7778 -> 7778)
	port := "7778"
	if u, err := url.Parse(host.URL); err == nil && u.Port() != "" {
		port = u.Port()
	}

	// Launch resilient SSH tunnel
	if err := spawnSSHTunnel(r.Context(), port, sshTarget); err != nil {
		http.Error(w, fmt.Sprintf("Failed to launch SSH tunnel: %v", err), http.StatusInternalServerError)
		return
	}

	// Poll remote daemon health over the newly spawned tunnel (allowing up to ~4-5s for wake-from-sleep network re-association)
	testURL := fmt.Sprintf("http://127.0.0.1:%s/v1/version", port)
	client := http.Client{Timeout: 1000 * time.Millisecond}
	var vData map[string]interface{}
	var lastCheckErr error

	for attempt := 0; attempt < 8; attempt++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := client.Get(testURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&vData)
			resp.Body.Close()
			lastCheckErr = nil
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		lastCheckErr = err
	}

	if lastCheckErr != nil || vData == nil {
		http.Error(w, fmt.Sprintf("SSH tunnel spawned but remote daemon is unreachable: %v", lastCheckErr), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Successfully reconnected SSH tunnel to '%s' (port %s)", host.Name, port),
		"version": vData["version"],
	})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path       string `json:"path"`        // Tree path, e.g. "Work/ProjectY/ProjectY-web"
		Name       string `json:"name"`        // Folder name or absolute/relative directory path
		ProjectDir string `json:"project_dir"` // Alternative directory field sent by Web GUI
		GitURL     string `json:"git_url"`     // Optional git origin
		BaseDir    string `json:"base_dir"`    // Optional base directory, defaults to ~/Projects
		CloneRepo  bool   `json:"clone_repo"`  // If true, clone repository into workspace directory
		Host       string `json:"host"`        // Optional target host ("local" or remote host alias)
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "Missing path", http.StatusBadRequest)
		return
	}

	// 1. Forward to remote host if specified
	if req.Host != "" && req.Host != "local" {
		hostRec, err := s.db.GetHost(req.Host)
		if err == nil && hostRec != nil && hostRec.URL != "" {
			targetURL := strings.TrimSuffix(hostRec.URL, "/") + "/v1/projects/create"
			remotePayload, _ := json.Marshal(map[string]interface{}{
				"path":        req.Path,
				"project_dir": req.ProjectDir,
				"name":        req.Name,
				"git_url":     req.GitURL,
				"base_dir":    req.BaseDir,
				"clone_repo":  req.CloneRepo,
			})
			resp, rErr := http.Post(targetURL, "application/json", bytes.NewBuffer(remotePayload))
			if rErr != nil {
				log.Printf("[Daemon] Warning: failed to forward project create to remote host %s: %v", req.Host, rErr)
			} else {
				_ = resp.Body.Close()
			}
		}
	}

	home, _ := os.UserHomeDir()

	var targetDir string
	var gitURL string
	alreadyExisted := false
	cloned := false

	folderInput := req.Name
	if folderInput == "" && req.ProjectDir != "" {
		folderInput = req.ProjectDir
	}

	if folderInput != "" {
		if strings.HasPrefix(folderInput, "~/") && home != "" {
			folderInput = filepath.Join(home, folderInput[2:])
		}

		if filepath.IsAbs(folderInput) || dirExists(folderInput) {
			targetDir = filepath.Clean(folderInput)
		} else {
			baseDir := req.BaseDir
			if baseDir == "" {
				if home != "" {
					baseDir = filepath.Join(home, "Projects")
				} else {
					baseDir = "."
				}
			}
			targetDir = filepath.Join(baseDir, folderInput)
		}

		alreadyExisted = dirExists(targetDir)
		hasGit := dirExists(filepath.Join(targetDir, ".git"))

		// If clone requested and directory is not yet a git repository
		if req.GitURL != "" && req.CloneRepo && !hasGit {
			log.Printf("[Daemon] Cloning repo %s into %s...", req.GitURL, targetDir)
			_ = os.MkdirAll(filepath.Dir(targetDir), 0755)
			out, cloneErr := exec.Command("git", "clone", req.GitURL, targetDir).CombinedOutput()
			if cloneErr != nil {
				log.Printf("[Daemon] Warning: git clone failed: %s (%v)", strings.TrimSpace(string(out)), cloneErr)
			} else {
				cloned = true
				alreadyExisted = true
			}
		}

		if !alreadyExisted {
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				log.Printf("[Daemon] Notice: could not create local directory %q: %v", targetDir, err)
			} else {
				alreadyExisted = true
			}
		}

		gitURL = req.GitURL
		if gitURL == "" && dirExists(filepath.Join(targetDir, ".git")) {
			out, err := exec.Command("git", "-C", targetDir, "remote", "get-url", "origin").Output()
			if err == nil {
				gitURL = strings.TrimSpace(string(out))
			}
		}

		targetDir = expandPath(targetDir)
	} else {
		gitURL = req.GitURL
	}

	node := &TreeNode{
		Path:       req.Path,
		ProjectDir: targetDir,
		GitURL:     gitURL,
		CreatedAt:  time.Now(),
	}

	if err := s.db.SaveNode(node); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save tree node: %v", err), http.StatusInternalServerError)
		return
	}

	// Auto-bind any existing sessions whose Cwd matches targetDir
	sessions, err := s.db.ListSessions()
	if err == nil {
		for _, sess := range sessions {
			if sess.Cwd != "" && sameOrSubDir(sess.Cwd, targetDir) {
				if sess.NodePath == "" || len(req.Path) >= len(sess.NodePath) {
					sess.NodePath = req.Path
					_ = s.db.SaveSession(sess)
					s.broadcast(sess)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "created",
		"path":            req.Path,
		"project_dir":     targetDir,
		"already_existed": alreadyExisted,
		"cloned":          cloned,
	})
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	cleaned := filepath.Clean(path)
	if eval, err := filepath.EvalSymlinks(cleaned); err == nil {
		return eval
	}
	return cleaned
}

func sameOrSubDir(cwd, projDir string) bool {
	if cwd == "" || projDir == "" {
		return false
	}
	c := expandPath(cwd)
	p := expandPath(projDir)

	if c == p {
		return true
	}
	if strings.HasPrefix(c, p+"/") {
		return true
	}
	return false
}

func (s *Server) resolveSessionNodePath(cwd string) string {
	if cwd == "" {
		return ""
	}
	nodes, err := s.db.ListNodes()
	if err != nil || len(nodes) == 0 {
		return ""
	}

	// 1. Longest exact or sub-directory match against registered node.ProjectDir
	var bestMatch string
	var bestLen int
	for _, n := range nodes {
		if n.ProjectDir != "" && sameOrSubDir(cwd, n.ProjectDir) {
			if len(n.ProjectDir) > bestLen {
				bestMatch = n.Path
				bestLen = len(n.ProjectDir)
			}
		}
	}
	if bestMatch != "" {
		return bestMatch
	}

	// 2. Segment match: check from leaf to group prefixes
	cleanCwd := strings.ToLower(filepath.Clean(cwd))
	cwdParts := strings.Split(cleanCwd, string(filepath.Separator))

	// Check if any registered node leaf matches cwd (e.g. "ngl-android" in "Modemobile/NGL/ngl-android")
	for _, n := range nodes {
		leaf := strings.ToLower(filepath.Base(n.Path))
		if len(leaf) > 3 {
			for _, part := range cwdParts {
				if part == leaf {
					return n.Path
				}
			}
		}
	}

	// Check if any group prefix matches cwd (e.g. "Modemobile" in "/home/dev4u/Work/modemobile")
	for _, n := range nodes {
		parts := strings.Split(n.Path, "/")
		for _, seg := range parts {
			segLower := strings.ToLower(seg)
			if len(segLower) > 3 {
				for _, part := range cwdParts {
					if part == segLower {
						return seg
					}
				}
			}
		}
	}

	return ""
}

func (s *Server) handleNodeMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if req.OldPath == "" || req.NewPath == "" {
		http.Error(w, "Missing old_path or new_path", http.StatusBadRequest)
		return
	}

	if err := s.db.MoveNode(req.OldPath, req.NewPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"moved"}`))
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"version": version.Version})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"shutting_down"}`))
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()
}

func (s *Server) StartBackgroundLoop(ctx context.Context) {
	go func() {
		// Run initial scan & host tunnel check asynchronously on startup
		go s.scanObservedSessions(ctx)
		go s.ensureHostTunnels(ctx)

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.scanObservedSessions(ctx)
				s.ensureHostTunnels(ctx)
				sessions, err := s.db.ListActiveSessions()
				if err == nil && len(sessions) > 0 {
					s.verifySessionLiveness(ctx, sessions)
				}
			}
		}
	}()
}

// ensureHostTunnels verifies that local SSH tunnels for registered remote hosts are active, and revives them if down.
func (s *Server) ensureHostTunnels(ctx context.Context) {
	hosts, err := s.db.ListHosts()
	if err != nil || len(hosts) == 0 {
		return
	}
	for _, h := range hosts {
		if h.SSHTarget == "" && h.Name == "" {
			continue
		}
		sshTarget := h.SSHTarget
		if sshTarget == "" {
			sshTarget = h.Name
		}
		if h.URL == "" {
			continue
		}
		u, err := url.Parse(h.URL)
		if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
			continue
		}
		port := u.Port()
		if port == "" {
			continue
		}

		// Check if port is already answering
		client := http.Client{Timeout: 1500 * time.Millisecond}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/v1/version", port))
		if err == nil {
			resp.Body.Close()
			continue // tunnel is healthy!
		}

		// Tunnel is down or not established; attempt background reconnection
		log.Printf("[Host Tunnel Manager] SSH tunnel for host %q (port %s) is down. Reconnecting...", h.Name, port)
		if err := spawnSSHTunnel(ctx, port, sshTarget); err != nil {
			log.Printf("[Host Tunnel Manager] Failed to revive tunnel for %q (port %s): %v", h.Name, port, err)
		}
	}
}

// readTail reads up to maxBytes from the end of a file to prevent high memory allocations
func readTail(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	size := stat.Size()
	offset := int64(0)
	if size > maxBytes {
		offset = size - maxBytes
	}

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	return io.ReadAll(file)
}

// readHead reads up to maxBytes from the beginning of a file to prevent reading multi-megabyte files
func readHead(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := make([]byte, maxBytes)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

func decodeClaudeProjectDirInfo(raw string) (baseRepoDir string, worktreeDir string) {
	if strings.HasPrefix(raw, "-") {
		raw = raw[1:]
	}

	// Handle explicit worktree split patterns (e.g. "--claude-worktrees-", "--worktrees-", "-claude-worktrees-")
	wtTokens := []struct {
		token string
		sub   []string
	}{
		{"--claude-worktrees-", []string{".claude", "worktrees"}},
		{"-claude-worktrees-", []string{".claude", "worktrees"}},
		{"--worktrees-", []string{".worktrees"}},
		{"-worktrees-", []string{".worktrees"}},
	}
	for _, wt := range wtTokens {
		if idx := strings.Index(raw, wt.token); idx != -1 {
			prefix := raw[:idx]
			wtName := raw[idx+len(wt.token):]
			parent := decodeClaudeProjectDir(prefix)
			parts := append([]string{parent}, wt.sub...)
			parts = append(parts, wtName)
			return parent, filepath.Join(parts...)
		}
	}

	decoded := decodeClaudeProjectDir(raw)
	return decoded, ""
}

func decodeClaudeProjectDir(raw string) string {
	if strings.HasPrefix(raw, "-") {
		raw = raw[1:]
	}

	// Handle explicit worktree split patterns (e.g. "--claude-worktrees-", "--worktrees-", "-claude-worktrees-")
	wtTokens := []struct {
		token string
		sub   []string
	}{
		{"--claude-worktrees-", []string{".claude", "worktrees"}},
		{"-claude-worktrees-", []string{".claude", "worktrees"}},
		{"--worktrees-", []string{".worktrees"}},
		{"-worktrees-", []string{".worktrees"}},
	}
	for _, wt := range wtTokens {
		if idx := strings.Index(raw, wt.token); idx != -1 {
			prefix := raw[:idx]
			wtName := raw[idx+len(wt.token):]
			parent := decodeClaudeProjectDir(prefix)
			parts := append([]string{parent}, wt.sub...)
			parts = append(parts, wtName)
			return filepath.Join(parts...)
		}
	}

	current := "/"
	remaining := raw
	for remaining != "" {
		if !dirExists(current) {
			break
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			break
		}

		var matchedEntry string
		var matchedLen int

		for _, e := range entries {
			eName := e.Name()
			cleanName := strings.TrimPrefix(eName, ".")
			if strings.HasPrefix(remaining, cleanName) {
				if len(cleanName) > matchedLen {
					matchedEntry = eName
					matchedLen = len(cleanName)
				}
			}
		}

		if matchedEntry != "" {
			current = filepath.Join(current, matchedEntry)
			remaining = strings.TrimPrefix(strings.TrimPrefix(remaining[matchedLen:], "-"), "-")
		} else {
			parts := strings.SplitN(remaining, "-", 2)
			candidate := filepath.Join(current, parts[0])
			hiddenCandidate := filepath.Join(current, "."+parts[0])
			if dirExists(hiddenCandidate) {
				current = hiddenCandidate
			} else {
				current = candidate
			}
			if len(parts) > 1 {
				remaining = parts[1]
			} else {
				remaining = ""
			}
		}
	}
	return current
}

func (s *Server) inspectAntigravityStatus(ctx context.Context, sess *Session) bool {
	return InspectAntigravityStatus(ctx, sess)
}

func InspectAntigravityStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "antigravity" || sess.State == StateEnded {
		return false
	}
	changed := false
	home, _ := os.UserHomeDir()

	// 1. Check live tmux screen for permission prompts or confirmations
	if sess.TmuxName != "" {
		if out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output(); err == nil {
			paneText := string(out)
			lines := strings.Split(paneText, "\n")
			startIdx := len(lines) - 25
			if startIdx < 0 {
				startIdx = 0
			}
			tailText := strings.Join(lines[startIdx:], "\n")

			if strings.Contains(tailText, "Requesting permission for:") || strings.Contains(tailText, "Do you want to proceed?") {
				cmdReason := ""
				if idx := strings.Index(tailText, "Requesting permission for:"); idx != -1 {
					sub := tailText[idx+len("Requesting permission for:"):]
					if endIdx := strings.Index(sub, "Do you want to proceed?"); endIdx != -1 {
						cmdReason = strings.TrimSpace(sub[:endIdx])
					}
				}
				if cmdReason == "" {
					cmdReason = "Tool permission requested"
				}

				if sess.State != StateBlocked || sess.Blocked == nil {
					sess.State = StateBlocked
					sess.Blocked = &Blocked{
						Kind:     BlockPermission,
						Reason:   cmdReason,
						Question: "Do you want to proceed with: " + truncateTitle(cmdReason),
						Options:  []string{"1. Yes", "2. Yes, and always allow in this conversation", "4. No"},
						Since:    time.Now(),
					}
					sess.Activity = "Waiting for permission: " + truncateTitle(cmdReason)
					sess.LastEventAt = time.Now()
					changed = true
				}
				return changed
			} else if strings.Contains(tailText, "Are you sure?") || strings.Contains(tailText, "[y/N]") || strings.Contains(tailText, "[Y/n]") {
				if sess.State != StateBlocked || sess.Blocked == nil {
					sess.State = StateBlocked
					sess.Blocked = &Blocked{
						Kind:     BlockPermission,
						Reason:   "Confirmation required",
						Question: "Confirmation required",
						Options:  []string{"Yes", "No"},
						Since:    time.Now(),
					}
					sess.Activity = "Waiting for confirmation"
					sess.LastEventAt = time.Now()
					changed = true
				}
				return changed
			}
		}
	}

	// 2. Check transcript.jsonl for ask_question or plan approval (using tail 64KB read to avoid reading entire file)
	if home != "" && sess.NativeID != "" {
		brainDirs := []string{
			filepath.Join(home, ".gemini", "antigravity", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".gemini", "antigravity-cli", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".antigravity", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
		}
		for _, logPath := range brainDirs {
			if data, err := readTail(logPath, 64*1024); err == nil && len(data) > 0 {
				lines := strings.Split(string(data), "\n")
				for i := len(lines) - 1; i >= 0; i-- {
					line := strings.TrimSpace(lines[i])
					if line == "" {
						continue
					}
					var step struct {
						Type      string `json:"type"`
						Content   string `json:"content"`
						ToolCalls []struct {
							Name string                 `json:"name"`
							Args map[string]interface{} `json:"args"`
						} `json:"tool_calls"`
						CreatedAt string `json:"created_at"`
					}
					if jerr := json.Unmarshal([]byte(line), &step); jerr == nil {
						stepTime, _ := time.Parse(time.RFC3339, step.CreatedAt)
						if stepTime.IsZero() {
							stepTime = time.Now()
						}

						for _, tc := range step.ToolCalls {
							if tc.Name == "ask_question" {
								q, opts := ExtractAntigravityQuestionAndOptions(tc.Args)
								if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Question != q {
									sess.State = StateBlocked
									sess.Blocked = &Blocked{
										Kind:     BlockQuestion,
										Reason:   q,
										Question: q,
										Options:  opts,
										Since:    stepTime,
									}
									if q != "" {
										sess.Activity = "Question: " + truncateTitle(q)
									} else {
										sess.Activity = "Waiting for user response"
									}
									sess.LastEventAt = stepTime
									changed = true
								}
								return changed
							}
						}

						if strings.Contains(step.Content, "Note: You have just created an artifact and requested user feedback") ||
							strings.Contains(step.Content, "Stop calling tools to end your turn, and allow the user to review the artifact") {
							if sess.State != StateBlocked || sess.Blocked == nil {
								sess.State = StateBlocked
								sess.Blocked = &Blocked{
									Kind:     BlockQuestion,
									Reason:   "Plan approval required",
									Question: "Please review and approve the implementation plan",
									Options:  []string{"Proceed", "Provide Feedback"},
									Since:    stepTime,
								}
								sess.Activity = "Waiting for plan feedback"
								sess.LastEventAt = stepTime
								changed = true
							}
							return changed
						}

						break
					}
				}
				break
			}
		}
	}

	// 3. If it was blocked, but live tmux pane and transcript show it is now unblocked
	if sess.State == StateBlocked {
		if sess.TmuxName != "" || isProcessAlive(sess.PID) {
			sess.State = StateWorking
			sess.Blocked = nil
			sess.Activity = "Working..."
			sess.LastEventAt = time.Now()
			changed = true
		} else {
			sess.State = StateEnded
			sess.Blocked = nil
			sess.Activity = "Session ended"
			changed = true
		}
	}

	return changed
}

func (s *Server) inspectClaudeStatus(ctx context.Context, sess *Session) bool {
	return InspectClaudeStatus(ctx, sess)
}

func InspectClaudeStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "claude-code" || sess.State == StateEnded {
		return false
	}
	changed := false

	if sess.TmuxName != "" {
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
		if err != nil {
			sess.State = StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			return true
		}

		// Check if Claude process is actually alive under pane PID
		if sess.PID > 0 && !isProcessAlive(sess.PID) {
			if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs == nil && len(strings.TrimSpace(string(outPs))) > 0 {
				// Child process alive
			} else {
				sess.State = StateEnded
				sess.Activity = "Session ended (process exited)"
				sess.PID = 0
				sess.Blocked = nil
				return true
			}
		}

		paneText := string(out)
		lines := strings.Split(paneText, "\n")
		startIdx := len(lines) - 25
		if startIdx < 0 {
			startIdx = 0
		}
		tailText := strings.Join(lines[startIdx:], "\n")

		// 1. Permission prompt / confirmation
		if strings.Contains(tailText, "Do you want to run") ||
			strings.Contains(tailText, "Do you want to proceed") ||
			strings.Contains(tailText, "Allow once") ||
			strings.Contains(tailText, "Allow always") ||
			strings.Contains(tailText, "[y/N]") ||
			strings.Contains(tailText, "[Y/n]") ||
			strings.Contains(tailText, "Permission requested") ||
			strings.Contains(tailText, "Authorize tool execution") ||
			strings.Contains(tailText, "Are you sure?") {
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockPermission {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockPermission,
					Reason:   "Tool permission requested",
					Question: "Permission required",
					Options:  []string{"Allow", "Deny"},
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for tool authorization"
				sess.LastEventAt = time.Now()
				changed = true
			}
			return changed
		}

		// 2. Question / user choice / prompt selection
		if strings.Contains(tailText, "Enter to select") ||
			strings.Contains(tailText, "Tab/Arrow keys to navigate") ||
			strings.Contains(tailText, "✔ Submit") ||
			strings.Contains(tailText, "Waiting for user response") ||
			strings.Contains(tailText, "AskUserQuestion") ||
			strings.Contains(tailText, "Type something.") ||
			strings.Contains(tailText, "Chat about this") {
			q, opts := extractClaudeQuestionAndOptions(tailText)
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Question != q {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockQuestion,
					Reason:   q,
					Question: q,
					Options:  opts,
					Since:    time.Now(),
				}
				if q != "" && q != "Waiting for user response" && q != "Waiting for user input" {
					sess.Activity = "Question: " + truncateTitle(q)
				} else {
					sess.Activity = "Waiting for user input"
				}
				sess.LastEventAt = time.Now()
				changed = true
			}
			return changed
		}

		// 3. Active generation / tool spinner
		hasSpinner := strings.Contains(tailText, "⠋") || strings.Contains(tailText, "⠙") ||
			strings.Contains(tailText, "⠹") || strings.Contains(tailText, "⠸") ||
			strings.Contains(tailText, "⠼") || strings.Contains(tailText, "⠴") ||
			strings.Contains(tailText, "⠦") || strings.Contains(tailText, "⠧") ||
			strings.Contains(tailText, "⠇") || strings.Contains(tailText, "⠏") ||
			strings.Contains(tailText, "Thinking...") ||
			strings.Contains(tailText, "Running tool:")

		if hasSpinner {
			if sess.State != StateWorking {
				sess.State = StateWorking
				sess.Blocked = nil
				sess.Activity = "Working..."
				changed = true
			}
			return changed
		}

		// 4. Interactive prompt idle (sitting at ❯ or awaiting prompt)
		hasPrompt := strings.Contains(tailText, "❯") ||
			strings.Contains(tailText, "auto mode on") ||
			strings.Contains(tailText, "bypass mode") ||
			strings.Contains(tailText, "shift+tab to cycle") ||
			strings.Contains(tailText, "? for shortcuts")

		if hasPrompt {
			if sess.State != StateIdle {
				sess.State = StateIdle
				sess.Blocked = nil
				sess.Activity = "Awaiting user prompt"
				changed = true
			}
			return changed
		}

		// 5. If it was blocked, but tmux pane is unblocked and alive
		if sess.State == StateBlocked {
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
	}

	return changed
}

func extractClaudeQuestionAndOptions(tailText string) (string, []string) {
	lines := strings.Split(tailText, "\n")
	var options []string
	firstOptIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		clean := strings.TrimPrefix(trimmed, "❯ ")
		clean = strings.TrimPrefix(clean, "❯")
		clean = strings.TrimSpace(clean)

		if len(clean) > 2 && clean[0] >= '1' && clean[0] <= '9' && clean[1] == '.' {
			optText := strings.TrimSpace(clean[2:])
			if optText != "" && !strings.HasPrefix(optText, "Type something") && !strings.HasPrefix(optText, "Chat about this") {
				options = append(options, optText)
				if firstOptIdx == -1 {
					firstOptIdx = i
				}
			}
		}
	}

	question := ""
	if firstOptIdx != -1 {
		for j := firstOptIdx - 1; j >= 0; j-- {
			prev := strings.TrimSpace(lines[j])
			if prev == "" || strings.HasPrefix(prev, "─") || strings.HasPrefix(prev, "←") || strings.HasPrefix(prev, "❯") || strings.HasPrefix(prev, "┌") || strings.HasPrefix(prev, "│") || strings.HasPrefix(prev, "└") {
				continue
			}
			question = prev
			break
		}
	}

	if question == "" {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasSuffix(trimmed, "?") && !strings.Contains(trimmed, "shortcuts") && !strings.Contains(trimmed, "want to proceed") {
				question = trimmed
				break
			}
		}
	}

	if question == "" {
		question = "Waiting for user input"
	}

	return question, options
}

func (s *Server) verifySessionLiveness(ctx context.Context, sessions []*Session) {
	// Query all active local tmux sessions once to avoid O(N) subprocess executions
	activeTmux, _ := tmux.ListSessions(ctx)

	for _, sess := range sessions {
		if sess.State == StateEnded {
			continue
		}

		if p, ok := s.providers[sess.Agent]; ok {
			if p.InspectStatus(ctx, sess) {
				_ = s.db.SaveSession(sess)
				s.broadcast(sess)
			}
		}

		if sess.State == StateEnded {
			continue
		}

		// Grace period for active sessions that sent hook events recently
		if time.Since(sess.LastEventAt) < 15*time.Minute {
			continue
		}

		alive := true
		if sess.Managed && sess.TmuxName != "" {
			if sess.Host == "local" || sess.Host == "" {
				alive = activeTmux[sess.TmuxName]
				if alive && sess.PID > 0 && !isProcessAlive(sess.PID) {
					if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs != nil || len(strings.TrimSpace(string(outPs))) == 0 {
						alive = false
					}
				}
			} else {
				alive = tmux.HasSession(ctx, sess.TmuxName)
			}
		} else if !sess.Managed && sess.PID > 0 {
			proc, err := os.FindProcess(sess.PID)
			if err != nil {
				alive = false
			} else {
				err = proc.Signal(syscall.Signal(0))
				if err != nil {
					alive = false
				}
			}
		}

		if !alive {
			sess.State = StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			_ = s.db.SaveSession(sess)
			s.broadcast(sess)
		}
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func (s *Server) scanObservedSessions(ctx context.Context) {
	hostName := "local"
	if h := os.Getenv("ACKBAR_HOST"); h != "" {
		hostName = h
	}

	existingSessions, err := s.db.ListSessions()
	if err != nil {
		return
	}

	// Build in-memory lookup indices to avoid repeated DB queries & inner loops
	knownIDs := make(map[string]*Session, len(existingSessions))
	knownByPID := make(map[int]*Session, len(existingSessions))
	knownByNativeID := make(map[string]*Session, len(existingSessions))

	for _, sess := range existingSessions {
		knownIDs[sess.ID] = sess
		if sess.PID > 0 {
			knownByPID[sess.PID] = sess
		}
		if sess.NativeID != "" {
			knownByNativeID[sess.NativeID] = sess
		}
	}

	// 0. Refresh names/titles/metadata and prune dead proc-<pid> sessions
	for _, sess := range existingSessions {
		if strings.HasPrefix(sess.NativeID, "proc-") {
			pidStr := strings.TrimPrefix(sess.NativeID, "proc-")
			if pidVal, pErr := strconv.Atoi(pidStr); pErr == nil && pidVal > 0 {
				if !isProcessAlive(pidVal) {
					_ = s.db.DeleteSession(sess.ID)
					_ = s.db.DeleteSession(sess.NativeID)
					delete(knownIDs, sess.ID)
					delete(knownByPID, pidVal)
					delete(knownByNativeID, sess.NativeID)
					continue
				}
			}
		}

		// IMMUTABILITY GUARD: Ended sessions with established names are static and do not need disk rescans
		if sess.State == StateEnded && sess.Name != "" && !isRawSessionName(sess.Name) {
			continue
		}

		changed := false
		if p, ok := s.providers[sess.Agent]; ok && sess.NativeID != "" {
			if meta := p.ReadSessionMetadata(sess.Cwd, sess.NativeID); meta != nil {
				if meta.Title != "" && meta.Title != sess.Name && !strings.HasPrefix(meta.Title, "<") {
					sess.Name = meta.Title
					changed = true
				}
				if meta.Entrypoint != "" && meta.Entrypoint != sess.Entrypoint {
					sess.Entrypoint = meta.Entrypoint
					changed = true
				}
				if meta.Kind != "" && meta.Kind != sess.Kind {
					sess.Kind = meta.Kind
					changed = true
				}
				if meta.Version != "" && meta.Version != sess.Version {
					sess.Version = meta.Version
					changed = true
				}
				if meta.ContextPct > 0 && meta.ContextPct != sess.ContextPct {
					sess.ContextPct = meta.ContextPct
					changed = true
				}
				if !meta.LastMessageAt.IsZero() && (sess.LastEventAt.IsZero() || meta.LastMessageAt.After(sess.LastEventAt)) {
					sess.LastEventAt = meta.LastMessageAt
					changed = true
				}
			}
			if p.InspectStatus(ctx, sess) {
				changed = true
			}
		}
		if sess.NodePath == "" && sess.Cwd != "" {
			if np := s.resolveSessionNodePath(sess.Cwd); np != "" {
				sess.NodePath = np
				changed = true
			}
		}
		if changed {
			_ = s.db.SaveSession(sess)
			s.broadcast(sess)
		}
	}

	// 1. Scan tmux panes for running agent CLIs
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_pid} #{pane_current_path} #{session_name} #{pane_current_command}").Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, " ", 4)
			if len(parts) < 4 {
				continue
			}
			pidStr, cwd, tmuxName, cmdName := parts[0], parts[1], parts[2], parts[3]
			cmdLower := strings.ToLower(cmdName)

			agent := ""
			actualPID := 0
			var panePID int
			fmt.Sscanf(pidStr, "%d", &panePID)
			if panePID > 0 {
				actualPID = panePID
			}

			for _, p := range s.providers {
				prefix := fmt.Sprintf("ackbar-%s-", p.Agent())
				if strings.HasPrefix(tmuxName, prefix) {
					agent = p.Agent()
					break
				}
			}

			if panePID > 0 {
				if out, err := exec.CommandContext(ctx, "ps", "-eo", "ppid,pid,command").Output(); err == nil {
					for _, pline := range strings.Split(string(out), "\n") {
						pline = strings.TrimSpace(pline)
						if pline == "" {
							continue
						}
						fields := strings.Fields(pline)
						if len(fields) >= 3 {
							ppidVal, _ := strconv.Atoi(fields[0])
							if ppidVal == panePID {
								cPid, _ := strconv.Atoi(fields[1])
								cCmd := strings.ToLower(strings.Join(fields[2:], " "))
								for _, p := range s.providers {
									for _, pName := range p.ProcessNames() {
										if strings.Contains(cCmd, pName) {
											agent = p.Agent()
											actualPID = cPid
											break
										}
									}
									if agent != "" {
										break
									}
								}
								if agent != "" {
									break
								}
							}
						}
					}
				}
			}

			if agent == "" {
				for _, p := range s.providers {
					for _, pName := range p.ProcessNames() {
						if strings.Contains(cmdLower, pName) {
							agent = p.Agent()
							break
						}
					}
					if agent != "" {
						break
					}
				}
			}

			if agent == "" {
				continue
			}

			if agent != "" && actualPID > 0 && cwd != "" {
				pid := actualPID

				// If no child process found and pane is just a dead shell, mark session as ended
				if actualPID == panePID && (cmdLower == "bash" || cmdLower == "zsh" || cmdLower == "sh" || cmdLower == "fish") {
					targetNativeID := ""
					if strings.HasPrefix(tmuxName, fmt.Sprintf("ackbar-%s-", agent)) {
						targetNativeID = strings.TrimPrefix(tmuxName, fmt.Sprintf("ackbar-%s-", agent))
					} else if strings.HasPrefix(tmuxName, "ackbar-") {
						parts := strings.Split(tmuxName, "-")
						if len(parts) >= 3 {
							targetNativeID = parts[len(parts)-1]
						}
					}
					if targetNativeID != "" {
						var existing *Session
						if ex, ok := knownIDs[fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)]; ok {
							existing = ex
						} else if ex, ok := knownByNativeID[targetNativeID]; ok {
							existing = ex
						}
						if existing != nil && existing.State != StateEnded {
							existing.State = StateEnded
							existing.Activity = "Session ended (process exited)"
							existing.PID = 0
							existing.Blocked = nil
							_ = s.db.SaveSession(existing)
							s.broadcast(existing)
						}
					}
					continue
				}

				// Try resolving the session UUID from tmuxName or ~/.claude/sessions/
				targetNativeID := ""
				if strings.HasPrefix(tmuxName, fmt.Sprintf("ackbar-%s-", agent)) {
					targetNativeID = strings.TrimPrefix(tmuxName, fmt.Sprintf("ackbar-%s-", agent))
				} else if strings.HasPrefix(tmuxName, "ackbar-") {
					parts := strings.Split(tmuxName, "-")
					if len(parts) >= 3 {
						targetNativeID = parts[len(parts)-1]
					}
				}

				if agent == "claude-code" && targetNativeID == "" {
					home, _ := os.UserHomeDir()
					if home != "" {
						sID, _ := findClaudeSessionForPID(home, pid)
						if sID != "" {
							targetNativeID = sID
						}
					}
				}

				if targetNativeID != "" {
					if s.db.IsSessionDeleted(targetNativeID) || s.db.IsSessionDeleted(fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)) {
						_ = tmux.Kill(ctx, tmuxName)
						if pid > 0 {
							_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
						}
						continue
					}
				}

				var existing *Session
				if targetNativeID != "" {
					if ex, ok := knownIDs[fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)]; ok {
						existing = ex
					} else if ex, ok := knownByNativeID[targetNativeID]; ok {
						existing = ex
					}
				}

				if existing != nil {
					// Adopt and elevate existing session in place (no duplicate!)
					existing.Managed = true
					existing.PID = pid
					existing.TmuxName = tmuxName
					if existing.NodePath == "" && existing.Cwd != "" {
						existing.NodePath = s.resolveSessionNodePath(existing.Cwd)
					}
					if p, ok := s.providers[agent]; ok {
						p.InspectStatus(ctx, existing)
					}
					if existing.State == StateEnded || existing.State == StateUnknown {
						existing.State = StateIdle
						existing.Activity = "Awaiting user prompt"
					}
					_ = s.db.SaveSession(existing)
					s.broadcast(existing)
					_ = s.db.DeleteSession(fmt.Sprintf("%s:observed:proc-%d", hostName, pid))
					continue
				}

				nativeID := fmt.Sprintf("proc-%d", pid)
				sessID := fmt.Sprintf("%s:observed:%s", hostName, nativeID)
				if targetNativeID != "" && isUUID(targetNativeID) {
					nativeID = targetNativeID
					sessID = fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)
				}

				existingObs := knownIDs[sessID]
				if existingObs == nil {
					sessionName := ""
					lastTime := time.Now()
					if p, ok := s.providers[agent]; ok && targetNativeID != "" {
						sessionName = p.ResolveSessionTitle(cwd, targetNativeID)
						if meta := p.ReadSessionMetadata(cwd, targetNativeID); meta != nil && !meta.LastMessageAt.IsZero() {
							lastTime = meta.LastMessageAt
						}
					}
					if sessionName == "" {
						if targetNativeID != "" && len(targetNativeID) >= 8 {
							sessionName = fmt.Sprintf("%s (%s)", agent, targetNativeID[:8])
						} else {
							sessionName = fmt.Sprintf("%s (%s)", agent, nativeID)
						}
					}
					newSess := &Session{
						ID:          sessID,
						Name:        sessionName,
						Agent:       agent,
						Host:        hostName,
						NativeID:    nativeID,
						Cwd:         cwd,
						NodePath:    s.resolveSessionNodePath(cwd),
						ProjectKey:  GetProjectKey(cwd),
						State:       StateIdle,
						Managed:     true,
						TmuxName:    tmuxName,
						PID:         pid,
						Activity:    fmt.Sprintf("Observed running agent in tmux '%s' (PID %d)", tmuxName, pid),
						StartedAt:   lastTime,
						LastEventAt: lastTime,
					}
					if p, ok := s.providers[agent]; ok {
						p.InspectStatus(ctx, newSess)
					}
					_ = s.db.SaveSession(newSess)
					knownIDs[sessID] = newSess
					knownByPID[pid] = newSess
					s.broadcast(newSess)
				} else {
					obsChanged := false
					if existingObs.TmuxName != tmuxName || !existingObs.Managed {
						existingObs.TmuxName = tmuxName
						existingObs.Managed = true
						obsChanged = true
					}
					if existingObs.NodePath == "" && existingObs.Cwd != "" {
						if np := s.resolveSessionNodePath(existingObs.Cwd); np != "" {
							existingObs.NodePath = np
							obsChanged = true
						}
					}
					if obsChanged {
						_ = s.db.SaveSession(existingObs)
						s.broadcast(existingObs)
					}
				}
			}
		}
	}

	// 2. Scan OS process table for running claude / antigravity / codex processes
	psOut, err := exec.CommandContext(ctx, "ps", "-eo", "pid,command").Output()
	if err == nil {
		lines := strings.Split(string(psOut), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			pidStr := parts[0]
			fullCmd := strings.Join(parts[1:], " ")
			cmdLower := strings.ToLower(fullCmd)

			agent := ""
			cmdFields := strings.Fields(fullCmd)
			if len(cmdFields) > 0 {
				binName := filepath.Base(cmdFields[0])
				binLower := strings.ToLower(binName)
				if binLower == "claude" || ((binLower == "node" || binLower == "bun") && (strings.Contains(cmdLower, "@anthropic-ai/claude-code") || strings.Contains(cmdLower, "claude-code") || strings.Contains(cmdLower, "/claude.js") || strings.Contains(cmdLower, "bin/claude"))) {
					if !strings.Contains(cmdLower, "ackbar") && !strings.Contains(cmdLower, "jest-worker") && !strings.Contains(cmdLower, "react-native") && !strings.Contains(cmdLower, "yarn") {
						agent = "claude-code"
					}
				} else if binLower == "antigravity" || binLower == "agy" || strings.Contains(cmdLower, "bin/agy") {
					if !strings.Contains(cmdLower, "ackbar") {
						agent = "antigravity"
					}
				} else if binLower == "codex" {
					if !strings.Contains(cmdLower, "ackbar") {
						agent = "codex"
					}
				}
			}

			if agent != "" && pidStr != "" {
				var pid int
				fmt.Sscanf(pidStr, "%d", &pid)
				if pid <= 0 || pid == os.Getpid() {
					continue
				}

				// Fast check: is this PID already mapped to an active session in our index?
				if exSess, ok := knownByPID[pid]; ok && exSess.ID != fmt.Sprintf("%s:observed:proc-%d", hostName, pid) {
					continue
				}

				var sID string
				if agent == "claude-code" {
					home, _ := os.UserHomeDir()
					if home != "" {
						sID, _ = findClaudeSessionForPID(home, pid)
					}
					if sID == "" {
						for i, arg := range cmdFields {
							if (arg == "--session-id" || arg == "-s" || arg == "--resume" || arg == "-r") && i+1 < len(cmdFields) {
								cID := strings.TrimSpace(cmdFields[i+1])
								if IsUUID(cID) {
									sID = cID
									break
								}
							}
						}
					}
				} else if agent == "antigravity" {
					for i, arg := range cmdFields {
						if (arg == "--conversation" || arg == "-c" || arg == "--conversation-id") && i+1 < len(cmdFields) {
							cID := strings.TrimSpace(cmdFields[i+1])
							if IsUUID(cID) {
								sID = cID
								break
							}
						}
					}
				}

				// Check if any existing session in the database already has this PID or native UUID using in-memory index
				hasExistingMatch := false
				if sID != "" {
					if sObj, ok := knownByNativeID[sID]; ok {
						if sObj.PID != pid {
							sObj.PID = pid
							_ = s.db.SaveSession(sObj)
							_ = s.db.DeleteSession(fmt.Sprintf("%s:observed:proc-%d", hostName, pid))
							knownByPID[pid] = sObj
						}
						hasExistingMatch = true
					}
				}
				if hasExistingMatch {
					continue
				}

				nativeID := fmt.Sprintf("proc-%d", pid)
				sessID := fmt.Sprintf("%s:observed:%s", hostName, nativeID)

				existing := knownIDs[sessID]
				if existing == nil {
					cwd := ""
					if link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil && link != "" {
						cwd = link
					} else {
						lsofOut, lerr := exec.CommandContext(ctx, "lsof", "-a", "-p", pidStr, "-d", "cwd", "-fn").Output()
						if lerr == nil {
							for _, lline := range strings.Split(string(lsofOut), "\n") {
								if idx := strings.LastIndex(lline, " "); idx != -1 {
									candidate := strings.TrimSpace(lline[idx:])
									if strings.HasPrefix(candidate, "/") {
										cwd = candidate
										break
									}
								}
							}
						}
					}

					if cwd != "" {
						title := fmt.Sprintf("%s (%s)", agent, nativeID)
						lastTime := time.Now()
						if agent == "claude-code" {
							if meta := ReadClaudeSessionMeta(cwd, nativeID); meta != nil && !meta.LastMessageAt.IsZero() {
								lastTime = meta.LastMessageAt
							}
						}
						newSess := &Session{
							ID:          sessID,
							Name:        title,
							Agent:       agent,
							Host:        hostName,
							NativeID:    nativeID,
							Cwd:         cwd,
							ProjectKey:  GetProjectKey(cwd),
							State:       StateIdle,
							Managed:     false,
							PID:         pid,
							Activity:    fmt.Sprintf("Observed running agent process (PID %d)", pid),
							StartedAt:   lastTime,
							LastEventAt: lastTime,
						}
						if p, ok := s.providers[agent]; ok {
							meta := p.ReadSessionMetadata(cwd, nativeID)
							if meta != nil {
								if meta.CustomTitle != "" {
									newSess.Name = meta.CustomTitle
								} else if meta.AITitle != "" {
									newSess.Name = meta.AITitle
								} else if meta.FirstPrompt != "" {
									newSess.Name = truncateTitle(meta.FirstPrompt)
								}
								newSess.Entrypoint = meta.Entrypoint
								newSess.Kind = meta.Kind
								newSess.Version = meta.Version
								newSess.CustomTitle = meta.CustomTitle
								newSess.AITitle = meta.AITitle
								newSess.AIDescription = meta.AIDescription
								newSess.FirstPrompt = meta.FirstPrompt
								newSess.LastPrompt = meta.LastPrompt
								newSess.GitBranch = meta.GitBranch
								newSess.ContextPct = meta.ContextPct
							}
						}
						_ = s.db.SaveSession(newSess)
						knownIDs[sessID] = newSess
						knownByPID[pid] = newSess
						s.broadcast(newSess)
					}
				}
			}
		}
	}

	// 3. Scan disk-backed Claude Code project logs to restore past sessions
	home, _ := os.UserHomeDir()
	if home != "" {
		claudeProjectsDir := filepath.Join(home, ".claude", "projects")
		if projDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
			for _, pDir := range projDirs {
				if pDir.IsDir() {
					projPath := filepath.Join(claudeProjectsDir, pDir.Name())
					if files, err := os.ReadDir(projPath); err == nil {
						for _, f := range files {
							if strings.HasSuffix(f.Name(), ".jsonl") && !strings.HasPrefix(f.Name(), "agent-") {
								sessionUUID := strings.TrimSuffix(f.Name(), ".jsonl")
								sessID := fmt.Sprintf("claude-code:%s:%s", hostName, sessionUUID)
								if s.db.IsSessionDeleted(sessID) || s.db.IsSessionDeleted(sessionUUID) {
									continue
								}
								// Skip expensive disk reads if session is already known in DB
								if knownIDs[sessID] != nil || knownByNativeID[sessionUUID] != nil {
									continue
								}

								baseDir, wtDir := decodeClaudeProjectDirInfo(pDir.Name())
								cwd := baseDir
								if cwd == "" && wtDir != "" {
									cwd = wtDir
								}

								meta := ReadClaudeSessionMeta(cwd, sessionUUID)
								title := ""
								if meta != nil {
									if meta.CustomTitle != "" {
										title = meta.CustomTitle
									} else if meta.AITitle != "" {
										title = meta.AITitle
									} else if meta.FirstPrompt != "" && !strings.HasPrefix(meta.FirstPrompt, "/") && meta.FirstPrompt != "config" && meta.FirstPrompt != "claude" {
										title = truncateTitle(meta.FirstPrompt)
									} else if meta.Title != "" && !strings.HasPrefix(meta.Title, "/") && meta.Title != "config" && meta.Title != "claude" {
										title = meta.Title
									}
								}
								if title == "" {
									rawTitle := ReadClaudeSessionTitle(cwd, sessionUUID)
									if rawTitle != "" && !strings.HasPrefix(rawTitle, "/") && rawTitle != "config" && rawTitle != "claude" {
										title = rawTitle
									}
								}
								if title == "" {
									if meta != nil && meta.LastPrompt != "" && !strings.HasPrefix(meta.LastPrompt, "/") {
										title = truncateTitle(meta.LastPrompt)
									}
								}
								if title == "" {
									title = fmt.Sprintf("Claude Code (%s)", sessionUUID[:8])
								}

								stat, _ := f.Info()
								modTime := time.Now()
								isOld := false
								if stat != nil {
									modTime = stat.ModTime()
									if time.Since(modTime) > 7*24*time.Hour {
										isOld = true
									}
								}

								branch := ""
								if meta != nil && meta.GitBranch != "" {
									branch = meta.GitBranch
								} else if wtDir != "" {
									branch = ResolveGitBranch(wtDir)
								} else if cwd != "" {
									branch = ResolveGitBranch(cwd)
								}

								newSess := &Session{
									ID:          sessID,
									Name:        title,
									Agent:       "claude-code",
									Host:        hostName,
									NativeID:    sessionUUID,
									Cwd:         cwd,
									ProjectKey:  GetProjectKey(cwd),
									State:       StateEnded,
									Managed:     false,
									Activity:    "Session ended",
									StartedAt:   modTime,
									LastEventAt: modTime,
									ContextPct:  ReadClaudeContextUsage(cwd, sessionUUID),
									GitBranch:   branch,
									Archived:    isOld,
								}
								if meta != nil {
									newSess.Entrypoint = meta.Entrypoint
									newSess.Kind = meta.Kind
									newSess.Version = meta.Version
									newSess.CustomTitle = meta.CustomTitle
									newSess.AITitle = meta.AITitle
									newSess.AIDescription = meta.AIDescription
									newSess.FirstPrompt = meta.FirstPrompt
									newSess.LastPrompt = meta.LastPrompt
								}
								_ = s.db.SaveSession(newSess)
								knownIDs[sessID] = newSess
								s.broadcast(newSess)
							}
						}
					}
				}
			}
		}

		// 4. Scan disk-backed Antigravity brain sessions across all possible locations
		brainDirs := []string{
			filepath.Join(home, ".gemini", "antigravity", "brain"),
			filepath.Join(home, ".gemini", "antigravity-cli", "brain"),
			filepath.Join(home, ".antigravity", "brain"),
		}
		for _, brainDir := range brainDirs {
			if bDirs, err := os.ReadDir(brainDir); err == nil {
				for _, bDir := range bDirs {
					convPath := filepath.Join(brainDir, bDir.Name())
					logPath := filepath.Join(convPath, ".system_generated", "logs", "transcript.jsonl")
					// Only process real conversation directories with valid transcript logs
					if bDir.IsDir() && fileExists(logPath) && isUUID(bDir.Name()) {
						convID := bDir.Name()
						sessID := fmt.Sprintf("antigravity:%s:%s", hostName, convID)
						if s.db.IsSessionDeleted(sessID) || s.db.IsSessionDeleted(convID) {
							continue
						}

						// Check if this is an internal subagent conversation
						if isAntigravitySubagent(home, convID) {
							if existing := knownIDs[sessID]; existing != nil {
								_ = s.db.DeleteSession(sessID)
								existing.Deleted = true
								existing.Activity = "Deleted"
								s.broadcast(existing)
								delete(knownIDs, sessID)
							}
							continue
						}

						existing := knownIDs[sessID]
						if existing == nil && convID != "" {
							existing = knownByNativeID[convID]
						}
						if existing == nil {
							title := ReadAntigravitySessionTitle("", convID)
							cwd := extractAntigravityWorkspace(logPath, home)
							modTime := time.Now()

							if stat, serr := os.Stat(convPath); serr == nil {
								modTime = stat.ModTime()
							}

							if title == "" {
								title = fmt.Sprintf("Antigravity (%s)", convID[:8])
							}

							isOld := time.Since(modTime) > 7*24*time.Hour

							newSess := &Session{
								ID:          sessID,
								Name:        title,
								Agent:       "antigravity",
								Host:        hostName,
								NativeID:    convID,
								Cwd:         cwd,
								ProjectKey:  GetProjectKey(cwd),
								State:       StateEnded,
								Managed:     false,
								Activity:    "Session ended",
								StartedAt:   modTime,
								LastEventAt: modTime,
								ContextPct:  0,
								Archived:    isOld,
							}
							_ = s.db.SaveSession(newSess)
							knownIDs[sessID] = newSess
							s.broadcast(newSess)
						} else if isRawSessionName(existing.Name) {
							if title := ReadAntigravitySessionTitle(existing.Cwd, convID); title != "" && !isRawSessionName(title) {
								existing.Name = title
								_ = s.db.SaveSession(existing)
								s.broadcast(existing)
							}
						}
					}
				}
			}
		}

		// 5. Database Sanitation: Clean up any obsolete/orphaned subagents in DB
		for _, sObj := range existingSessions {
			if sObj.Agent == "antigravity" && sObj.NativeID != "" && isUUID(sObj.NativeID) {
				if isAntigravitySubagent(home, sObj.NativeID) {
					_ = s.db.DeleteSession(sObj.ID)
					sObj.Deleted = true
					sObj.Activity = "Deleted"
					s.broadcast(sObj)
				}
			}
		}
	}
}

func findClaudeSessionForPID(home string, pid int) (sessionID, name string) {
	if home == "" || pid <= 0 {
		return "", ""
	}
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	// 1. Direct PID match
	metaFile := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", pid))
	if data, err := os.ReadFile(metaFile); err == nil {
		var meta struct {
			SessionID string `json:"sessionId"`
			Name      string `json:"name"`
		}
		if err := json.Unmarshal(data, &meta); err == nil && meta.SessionID != "" {
			return meta.SessionID, meta.Name
		}
	}
	// 2. Child PID match via pgrep -P
	if out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output(); err == nil {
		for _, cpStr := range strings.Fields(string(out)) {
			if cp, err := strconv.Atoi(cpStr); err == nil && cp > 0 {
				cMetaFile := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", cp))
				if data, err := os.ReadFile(cMetaFile); err == nil {
					var meta struct {
						SessionID string `json:"sessionId"`
						Name      string `json:"name"`
					}
					if err := json.Unmarshal(data, &meta); err == nil && meta.SessionID != "" {
						return meta.SessionID, meta.Name
					}
				}
			}
		}
	}
	return "", ""
}

type TitleCacheEntry struct {
	Title     string
	Source    string // "custom", "ai", "prompt", "fallback"
	UpdatedAt time.Time
}

var (
	titleCacheMutex sync.RWMutex
	titleCache      = make(map[string]TitleCacheEntry)
)

func getModelContextLimit(modelName string) int {
	modelLower := strings.ToLower(modelName)
	if strings.Contains(modelLower, "fable") || strings.Contains(modelLower, "opus") || strings.Contains(modelLower, "1m") || strings.Contains(modelLower, "gemini") || strings.Contains(modelLower, "sonnet-4") || strings.Contains(modelLower, "sonnet-5") || strings.Contains(modelLower, "claude-4") || strings.Contains(modelLower, "claude-5") {
		return 1000000
	}
	if strings.Contains(modelLower, "gpt-4") || strings.Contains(modelLower, "codex") {
		return 128000
	}
	return 200000
}

func ReadClaudeContextUsage(cwd, sessionID string) int {
	home, err := os.UserHomeDir()
	if err != nil || cwd == "" {
		return 0
	}

	targetID := sessionID
	if strings.HasPrefix(sessionID, "proc-") {
		targetID = ""
	}

	encodedCwd := strings.ReplaceAll(cwd, "/", "-")
	projDir := filepath.Join(home, ".claude", "projects", encodedCwd)

	files, err := os.ReadDir(projDir)
	if err != nil {
		return 0
	}

	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			if targetID == "" || strings.Contains(f.Name(), targetID) {
				filePath := filepath.Join(projDir, f.Name())
				file, err := os.Open(filePath)
				if err != nil {
					continue
				}

				stat, err := file.Stat()
				if err != nil || stat.Size() == 0 {
					file.Close()
					continue
				}

				offset := stat.Size() - 65536
				if offset < 0 {
					offset = 0
				}
				_, _ = file.Seek(offset, 0)
				buf, _ := io.ReadAll(file)
				file.Close()

				lines := strings.Split(string(buf), "\n")
				for i := len(lines) - 1; i >= 0; i-- {
					line := strings.TrimSpace(lines[i])
					if line == "" {
						continue
					}
					if strings.Contains(line, `"usage"`) {
						var wrapper struct {
							Message *struct {
								Model string `json:"model"`
								Usage *struct {
									InputTokens              int `json:"input_tokens"`
									CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
									CacheReadInputTokens     int `json:"cache_read_input_tokens"`
								} `json:"usage"`
							} `json:"message"`
						}
						if err := json.Unmarshal([]byte(line), &wrapper); err == nil && wrapper.Message != nil && wrapper.Message.Usage != nil {
							u := wrapper.Message.Usage
							totalTokens := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
							if totalTokens > 0 {
								limit := getModelContextLimit(wrapper.Message.Model)
								pct := (totalTokens * 100) / limit
								if pct > 100 {
									pct = 100
								}
								return pct
							}
						}
					}
				}
			}
		}
	}

	return 0
}

func ReadClaudeSessionMeta(cwd, sessionID string) *SessionMeta {
	title := ReadClaudeSessionTitle(cwd, sessionID)
	metaInfo := &SessionMeta{
		Title:      title,
		ContextPct: ReadClaudeContextUsage(cwd, sessionID),
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return metaInfo
	}

	targetID := sessionID
	var targetPID int
	if strings.HasPrefix(sessionID, "proc-") {
		pidStr := strings.TrimPrefix(sessionID, "proc-")
		targetPID, _ = strconv.Atoi(pidStr)
		targetID = ""
	}

	// 1. Resolve metadata and sessionId from ~/.claude/sessions/ if PID is known or targetID is provided
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if files, err := os.ReadDir(sessionsDir); err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				metaPath := filepath.Join(sessionsDir, f.Name())
				if data, err := os.ReadFile(metaPath); err == nil {
					var meta struct {
						PID        int    `json:"pid"`
						SessionID  string `json:"sessionId"`
						Name       string `json:"name"`
						NameSource string `json:"nameSource"`
						Entrypoint string `json:"entrypoint"`
						Kind       string `json:"kind"`
						Version    string `json:"version"`
						Cwd        string `json:"cwd"`
					}
					if err := json.Unmarshal(data, &meta); err == nil {
						if (targetPID > 0 && meta.PID == targetPID) || (targetID != "" && meta.SessionID == targetID) {
							if meta.Name != "" && meta.NameSource == "custom" && metaInfo.CustomTitle == "" {
								metaInfo.CustomTitle = meta.Name
							}
							if meta.Entrypoint != "" {
								metaInfo.Entrypoint = meta.Entrypoint
							}
							if meta.Kind != "" {
								metaInfo.Kind = meta.Kind
							}
							if meta.Version != "" {
								metaInfo.Version = meta.Version
							}
							if targetID == "" && meta.SessionID != "" {
								targetID = meta.SessionID
							}
							break
						}
					}
				}
			}
		}
	}

	// 2. Scan transcript file for prompts, summaries, and titles
	encodedCwd := strings.ReplaceAll(cwd, "/", "-")
	var targetFiles []string
	claudeProjectsDir := filepath.Join(home, ".claude", "projects")

	if encodedCwd != "" && encodedCwd != "-" {
		projDir := filepath.Join(claudeProjectsDir, encodedCwd)
		if targetID != "" {
			targetFiles = append(targetFiles, filepath.Join(projDir, targetID+".jsonl"))
		}
	}

	if len(targetFiles) == 0 || !fileExists(targetFiles[0]) {
		if pDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
			for _, pDir := range pDirs {
				if pDir.IsDir() {
					projPath := filepath.Join(claudeProjectsDir, pDir.Name())
					if targetID != "" {
						tf := filepath.Join(projPath, targetID+".jsonl")
						if fileExists(tf) {
							targetFiles = append(targetFiles, tf)
							break
						}
					}
				}
			}
		}
	}

	for _, filePath := range targetFiles {
		if info, serr := os.Stat(filePath); serr == nil {
			if metaInfo.LastMessageAt.IsZero() || info.ModTime().After(metaInfo.LastMessageAt) {
				metaInfo.LastMessageAt = info.ModTime()
			}
		}
		if data, rerr := readTail(filePath, 64*1024); rerr == nil && len(data) > 0 {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var obj struct {
					Type    string `json:"type"`
					Message struct {
						Role    string `json:"role"`
						Content any    `json:"content"`
					} `json:"message"`
					CustomTitle string `json:"customTitle"`
					Title       string `json:"title"`
					AITitle     string `json:"aiTitle"`
					Summary     string `json:"summary"`
					Cwd         string `json:"cwd"`
					GitBranch   string `json:"gitBranch"`
					LastPrompt  string `json:"lastPrompt"`
					Prompt      string `json:"prompt"`
				}
				if jerr := json.Unmarshal([]byte(line), &obj); jerr == nil {
					if obj.GitBranch != "" && metaInfo.GitBranch == "" {
						metaInfo.GitBranch = obj.GitBranch
					}
					if obj.CustomTitle != "" {
						metaInfo.CustomTitle = obj.CustomTitle
					}
					if obj.AITitle != "" {
						metaInfo.AITitle = obj.AITitle
					}
					if obj.Title != "" && metaInfo.AITitle == "" {
						metaInfo.AITitle = obj.Title
					}
					if obj.Summary != "" {
						metaInfo.AIDescription = obj.Summary
					}

					promptText := ""
					if obj.LastPrompt != "" {
						promptText = obj.LastPrompt
					} else if obj.Prompt != "" {
						promptText = obj.Prompt
					} else if obj.Type == "user" || obj.Message.Role == "user" {
						if s, ok := obj.Message.Content.(string); ok {
							promptText = s
						} else if arr, ok := obj.Message.Content.([]any); ok && len(arr) > 0 {
							for _, itm := range arr {
								if firstObj, ok := itm.(map[string]any); ok {
									if text, ok := firstObj["text"].(string); ok && text != "" {
										promptText = text
										break
									}
								}
							}
						}
					}

					if promptText != "" {
						clean := cleanPromptText(promptText)
						if clean != "" {
							if metaInfo.FirstPrompt == "" {
								metaInfo.FirstPrompt = clean
							}
							metaInfo.LastPrompt = clean
						}
					}
				}
			}
			break
		}
	}

	return metaInfo
}

func ReadAntigravitySessionTitle(cwd, sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	targetID := sessionID
	if strings.HasPrefix(sessionID, "proc-") {
		targetID = ""
	}

	if targetID == "" {
		return ""
	}

	// 1. Direct Lookup: Check annotations in all Antigravity dirs (.gemini/antigravity, .gemini/antigravity-cli, .antigravity)
	annotationDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "annotations"),
		filepath.Join(home, ".gemini", "antigravity-cli", "annotations"),
		filepath.Join(home, ".antigravity", "annotations"),
	}
	for _, aDir := range annotationDirs {
		annoPath := filepath.Join(aDir, targetID+".pbtxt")
		if data, err := os.ReadFile(annoPath); err == nil {
			content := string(data)
			if idx := strings.Index(content, `title:"`); idx != -1 {
				sub := content[idx+len(`title:"`):]
				if endIdx := strings.Index(sub, `"`); endIdx != -1 {
					t := strings.TrimSpace(sub[:endIdx])
					if t != "" {
						return t
					}
				}
			}
		}
	}

	// 2. Check conversation_metadata.json cache
	metadataPaths := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".gemini", "antigravity", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".antigravity", "cache", "conversation_metadata.json"),
	}
	for _, mPath := range metadataPaths {
		if data, err := os.ReadFile(mPath); err == nil {
			var meta struct {
				Conversations map[string]struct {
					Summary struct {
						Title   string `json:"Title"`
						Preview string `json:"Preview"`
					} `json:"summary"`
				} `json:"conversations"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				if c, exists := meta.Conversations[targetID]; exists {
					if c.Summary.Title != "" {
						return c.Summary.Title
					}
					if c.Summary.Preview != "" && c.Summary.Preview != "Session Exit Command" {
						return truncateTitle(c.Summary.Preview)
					}
				}
			}
		}
	}

	// 3. Check brain task summary fallback
	brainDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain"),
		filepath.Join(home, ".antigravity", "brain"),
	}
	for _, bDir := range brainDirs {
		metaPath := filepath.Join(bDir, targetID, "task.md.metadata.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(data, &meta); err == nil && meta.Summary != "" {
				return truncateTitle(meta.Summary)
			}
		}
	}

	// 4. Scan transcript for CHECKPOINT objective or first user prompt (using bounded head read)
	for _, bDir := range brainDirs {
		logPath := filepath.Join(bDir, targetID, ".system_generated", "logs", "transcript.jsonl")
		if data, err := readHead(logPath, 64*1024); err == nil && len(data) > 0 {
			lines := strings.Split(string(data), "\n")
			firstPromptTitle := ""
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var step struct {
					Type    string `json:"type"`
					Content string `json:"content"`
				}
				if json.Unmarshal([]byte(line), &step) == nil {
					if step.Type == "CHECKPOINT" && strings.Contains(step.Content, "# USER Objective:") {
						idx := strings.Index(step.Content, "# USER Objective:")
						sub := strings.TrimSpace(step.Content[idx+len("# USER Objective:"):])
						if end := strings.Index(sub, "\n"); end != -1 {
							sub = strings.TrimSpace(sub[:end])
						}
						if sub != "" && !strings.HasPrefix(sub, "<") && len(sub) >= 4 && !IsRawSessionName(sub) {
							return truncateTitle(sub)
						}
					}
					if step.Type == "USER_INPUT" && step.Content != "" && firstPromptTitle == "" {
						clean := cleanAntigravityPrompt(step.Content)
						if clean != "" && !strings.HasPrefix(clean, "/") && !strings.HasPrefix(clean, "<") && !IsRawSessionName(clean) {
							firstPromptTitle = truncateTitle(clean)
						}
					}
				}
			}
			if firstPromptTitle != "" {
				return firstPromptTitle
			}
		}
	}

	// 5. Check central Antigravity proto registry fallback: ~/.gemini/antigravity/agyhub_summaries_proto.pb
	protoPaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "agyhub_summaries_proto.pb"),
		filepath.Join(home, ".gemini", "antigravity-cli", "agyhub_summaries_proto.pb"),
		filepath.Join(home, ".antigravity", "agyhub_summaries_proto.pb"),
	}
	for _, protoPath := range protoPaths {
		if data, err := readHead(protoPath, 256*1024); err == nil && len(data) > 0 {
			str := string(data)
			if idx := strings.Index(str, targetID); idx != -1 {
				sub := str[idx+len(targetID):]
				if len(sub) > 250 {
					sub = sub[:250]
				}
				var words []string
				var cur strings.Builder
				for _, r := range sub {
					if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' || r == '-' || r == '_' || r == ':' || r == '/' || r == '(' || r == ')' {
						cur.WriteRune(r)
					} else {
						if cur.Len() >= 4 {
							words = append(words, strings.TrimSpace(cur.String()))
						}
						cur.Reset()
					}
				}
				if cur.Len() >= 4 {
					words = append(words, strings.TrimSpace(cur.String()))
				}
				for _, w := range words {
					if !strings.HasPrefix(w, "file:") && !strings.HasPrefix(w, "git@") && !strings.Contains(w, "Users/") && !strings.Contains(w, "home/") && len(w) >= 5 && !IsRawSessionName(w) {
						return truncateTitle(w)
					}
				}
			}
		}
	}

	return ""
}

func extractAntigravityWorkspace(logPath, home string) string {
	data, err := readHead(logPath, 64*1024)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")

	// 1. Scan for user_information mapping: "/path -> corpus"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, " -> ") {
			idx := strings.Index(line, " -> ")
			startIdx := strings.LastIndexAny(line[:idx], " \t\n\r\"'[]")
			if startIdx == -1 {
				startIdx = 0
			} else {
				startIdx++
			}
			cand := strings.TrimSpace(line[startIdx:idx])
			cand = strings.TrimPrefix(cand, "file://")
			if dirExists(cand) && cand != home && !strings.Contains(cand, "/.gemini") {
				return cand
			}
		}
	}

	// 2. Scan tool calls in first 50 steps for authoritative workspace paths
	maxLines := len(lines)
	if maxLines > 50 {
		maxLines = 50
	}
	for i := 0; i < maxLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var step struct {
			ToolCalls []struct {
				Name string                 `json:"name"`
				Args map[string]interface{} `json:"args"`
			} `json:"tool_calls"`
		}
		if err := json.Unmarshal([]byte(line), &step); err == nil {
			for _, tc := range step.ToolCalls {
				for _, k := range []string{"Cwd", "DirectoryPath", "SearchPath"} {
					if v, ok := tc.Args[k]; ok {
						if s, ok := v.(string); ok && s != "" {
							s = strings.Trim(s, "\"")
							if dirExists(s) && s != home && !strings.Contains(s, "/.gemini") {
								return s
							}
						}
					}
				}
				if v, ok := tc.Args["AbsolutePath"]; ok {
					if s, ok := v.(string); ok && s != "" {
						s = strings.Trim(s, "\"")
						dir := filepath.Dir(s)
						if dirExists(dir) && dir != home && !strings.Contains(dir, "/.gemini") {
							return dir
						}
					}
				}
			}
		}
	}

	return ""
}

func ReadClaudeSessionTitle(cwd, sessionID string) string {
	cacheKey := fmt.Sprintf("%s:%s", cwd, sessionID)
	titleCacheMutex.RLock()
	if cached, ok := titleCache[cacheKey]; ok && cached.Title != "" {
		if cached.Source == "custom" {
			titleCacheMutex.RUnlock()
			return cached.Title
		}
	}
	titleCacheMutex.RUnlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	targetID := sessionID
	var targetPID int
	if strings.HasPrefix(sessionID, "proc-") {
		pidStr := strings.TrimPrefix(sessionID, "proc-")
		targetPID, _ = strconv.Atoi(pidStr)
		targetID = ""
	}

	var title string
	var source string = "fallback"
	var derivedTitle string

	// 1. Check ~/.claude/sessions/*.json session registry files strictly for exact PID or SessionID match
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if files, err := os.ReadDir(sessionsDir); err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				metaPath := filepath.Join(sessionsDir, f.Name())
				if data, err := os.ReadFile(metaPath); err == nil {
					var meta struct {
						PID        int    `json:"pid"`
						SessionID  string `json:"sessionId"`
						Name       string `json:"name"`
						NameSource string `json:"nameSource"`
					}
					if err := json.Unmarshal(data, &meta); err == nil {
						if (targetPID > 0 && meta.PID == targetPID) || (targetID != "" && meta.SessionID == targetID) {
							if meta.Name != "" && meta.NameSource == "custom" {
								title = meta.Name
								source = "custom"
							} else if meta.Name != "" && derivedTitle == "" {
								derivedTitle = meta.Name
							}
							if targetID == "" && meta.SessionID != "" {
								targetID = meta.SessionID
							}
							break
						}
					}
				}
			}
		}
	}

	if title == "" && targetID != "" {
		var firstPrompt string
		var aiTitle string

		// 2. Check ~/.claude/projects/ strictly for targetID transcript
		claudeProjectsDir := filepath.Join(home, ".claude", "projects")
		var targetFiles []string
		encodedCwd := strings.ReplaceAll(cwd, "/", "-")
		if encodedCwd != "" && encodedCwd != "-" {
			projDir := filepath.Join(claudeProjectsDir, encodedCwd)
			tf := filepath.Join(projDir, targetID+".jsonl")
			if fileExists(tf) {
				targetFiles = append(targetFiles, tf)
			}
		}
		if len(targetFiles) == 0 {
			if pDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
				for _, pDir := range pDirs {
					if pDir.IsDir() {
						tf := filepath.Join(claudeProjectsDir, pDir.Name(), targetID+".jsonl")
						if fileExists(tf) {
							targetFiles = append(targetFiles, tf)
							break
						}
					}
				}
			}
		}

		for _, filePath := range targetFiles {
			file, err := os.Open(filePath)
			if err == nil {
				stat, _ := file.Stat()
				fileSize := stat.Size()

				// Tail read (last 64KB) for customTitle, rename, or aiTitle
				if fileSize > 0 {
					offset := fileSize - 65536
					if offset < 0 {
						offset = 0
					}
					_, _ = file.Seek(offset, 0)
					tailBuf, _ := io.ReadAll(file)
					tailLines := strings.Split(string(tailBuf), "\n")
					for i := len(tailLines) - 1; i >= 0; i-- {
						line := strings.TrimSpace(tailLines[i])
						if line == "" {
							continue
						}
						var entry struct {
							Type        string `json:"type"`
							CustomTitle string `json:"customTitle"`
							AgentName   string `json:"agentName"`
							AITitle     string `json:"aiTitle"`
							Title       string `json:"title"`
							Name        string `json:"name"`
						}
						if err := json.Unmarshal([]byte(line), &entry); err == nil {
							if entry.CustomTitle != "" {
								title = entry.CustomTitle
								source = "custom"
								break
							}
							if entry.AgentName != "" {
								title = entry.AgentName
								source = "custom"
								break
							}
							if entry.Title != "" && title == "" {
								title = entry.Title
								source = "ai"
							}
							if entry.AITitle != "" && aiTitle == "" {
								aiTitle = entry.AITitle
							}
							if entry.Type == "rename" && entry.Name != "" {
								title = entry.Name
								source = "custom"
								break
							}
						}
					}
				}

				// Head read (first 64KB) for prompt or ai-title if no title
				if title == "" && fileSize > 0 {
					_, _ = file.Seek(0, 0)
					buf := make([]byte, 65536)
					n, _ := io.ReadFull(file, buf)
					jsonLines := strings.Split(string(buf[:n]), "\n")
					for i := 0; i < len(jsonLines); i++ {
						line := strings.TrimSpace(jsonLines[i])
						if line == "" {
							continue
						}
						var entry struct {
							Type    string `json:"type"`
							Message struct {
								Role    string `json:"role"`
								Content any    `json:"content"`
							} `json:"message"`
							AITitle     string `json:"aiTitle"`
							Title       string `json:"title"`
							CustomTitle string `json:"customTitle"`
							LastPrompt  string `json:"lastPrompt"`
							Prompt      string `json:"prompt"`
						}
						if err := json.Unmarshal([]byte(line), &entry); err == nil {
							if entry.CustomTitle != "" && title == "" {
								title = entry.CustomTitle
								source = "custom"
							}
							if entry.AITitle != "" && aiTitle == "" {
								aiTitle = entry.AITitle
							}
							if entry.Title != "" && aiTitle == "" {
								aiTitle = entry.Title
							}
							pText := entry.LastPrompt
							if pText == "" {
								pText = entry.Prompt
							}
							if pText == "" && (entry.Type == "user" || entry.Message.Role == "user") {
								if s, ok := entry.Message.Content.(string); ok {
									pText = s
								}
							}
							if pText != "" && firstPrompt == "" {
								firstPrompt = cleanPromptText(pText)
							}
						}
					}
				}
				file.Close()
			}
			if title != "" {
				break
			}
		}

		if title == "" && aiTitle != "" {
			title = aiTitle
			source = "ai"
		}
		if title == "" && firstPrompt != "" {
			title = truncateTitle(firstPrompt)
			source = "prompt"
		}
		if title == "" && derivedTitle != "" {
			title = derivedTitle
			source = "fallback"
		}
	}

	// 3. Fallback to history.jsonl strictly matching targetID
	if title == "" && targetID != "" {
		historyPath := filepath.Join(home, ".claude", "history.jsonl")
		if data, err := os.ReadFile(historyPath); err == nil {
			lines := strings.Split(string(data), "\n")
			for i := len(lines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(lines[i])
				if line == "" {
					continue
				}
				var entry struct {
					SessionID    string `json:"sessionId"`
					SessionIDOld string `json:"session_id"`
					DisplayName  string `json:"displayName"`
					CustomTitle  string `json:"customTitle"`
					Title        string `json:"title"`
					Display      string `json:"display"`
					Prompt       string `json:"prompt"`
				}
				if err := json.Unmarshal([]byte(line), &entry); err == nil {
					sID := entry.SessionID
					if sID == "" {
						sID = entry.SessionIDOld
					}
					if sID == targetID {
						if entry.DisplayName != "" {
							title = entry.DisplayName
							source = "custom"
							break
						}
						if entry.CustomTitle != "" {
							title = entry.CustomTitle
							source = "custom"
							break
						}
						if entry.Title != "" {
							title = entry.Title
							source = "ai"
							break
						}
						p := entry.Display
						if p == "" {
							p = entry.Prompt
						}
						if p != "" {
							title = truncateTitle(cleanPromptText(p))
							source = "prompt"
							break
						}
					}
				}
			}
		}
	}

	if title == "" && targetID != "" && len(targetID) >= 8 {
		title = fmt.Sprintf("Claude Code (%s)", targetID[:8])
	}

	if title != "" {
		titleCacheMutex.Lock()
		titleCache[cacheKey] = TitleCacheEntry{
			Title:     title,
			Source:    source,
			UpdatedAt: time.Now(),
		}
		titleCacheMutex.Unlock()
	}

	return title
}

func TruncateTitle(text string) string {
	return truncateTitle(text)
}

func truncateTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	cleanLines := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			cleanLines = append(cleanLines, l)
		}
	}
	if len(cleanLines) == 0 {
		return ""
	}
	res := cleanLines[0]
	if len(res) < 6 && len(cleanLines) > 1 {
		res = res + " " + cleanLines[1]
	}
	if len(res) > 50 {
		return res[:47] + "..."
	}
	return res
}

func cleanPromptText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Strip XML wrapper tags like <USER_REQUEST> or <CONTEXT_SUMMARY>
	for strings.HasPrefix(text, "<") {
		idx := strings.Index(text, ">")
		if idx != -1 && idx < len(text)-1 {
			text = strings.TrimSpace(text[idx+1:])
		} else {
			break
		}
	}
	// Remove trailing closing tags
	if idx := strings.Index(text, "</"); idx != -1 {
		text = strings.TrimSpace(text[:idx])
	}
	return text
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func isUUID(s string) bool {
	return IsUUID(s)
}

// IsUUID returns true if s matches standard 36-character hyphenated UUID format
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

func isRawSessionName(n string) bool {
	return IsRawSessionName(n)
}

// IsRawSessionName returns true if n is empty, agent identifier, raw UUID, or generic placeholder
func IsRawSessionName(n string) bool {
	n = strings.TrimSpace(n)
	n = strings.TrimSuffix(n, ":")
	n = strings.TrimSpace(n)
	if n == "" || n == "antigravity" || n == "claude-code" || n == "codex" || n == "cli" || n == "mock-agent" {
		return true
	}
	if strings.HasPrefix(n, "ackbar-") || strings.HasPrefix(n, "proc-") || IsUUID(n) {
		return true
	}
	if strings.HasPrefix(n, "antigravity (") || strings.HasPrefix(n, "claude-code (") || strings.HasPrefix(n, "codex (") || strings.HasPrefix(n, "Claude Code (") || strings.HasPrefix(n, "Antigravity (") {
		return true
	}
	if isGenericDirSlug(n) {
		return true
	}
	return false
}

func isGenericDirSlug(n string) bool {
	lastHyphen := strings.LastIndex(n, "-")
	if lastHyphen <= 0 || lastHyphen == len(n)-1 {
		return false
	}
	numPart := n[lastHyphen+1:]
	for _, c := range numPart {
		if c < '0' || c > '9' {
			return false
		}
	}
	prefix := n[:lastHyphen]
	// Match lowercase directory slugs with no spaces (e.g. ngl-android-23, modemobile-1, skip2q-4)
	if prefix == strings.ToLower(prefix) && !strings.Contains(prefix, " ") && !strings.Contains(prefix, "_") {
		return true
	}
	return false
}

func isAntigravitySubagent(home, convID string) bool {
	if convID == "" || !IsUUID(convID) {
		return false
	}
	// 1. Check if user annotation exists (annotations are ONLY created by the IDE/CLI for real user root sessions)
	annoPaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "annotations", convID+".pbtxt"),
		filepath.Join(home, ".gemini", "antigravity-cli", "annotations", convID+".pbtxt"),
		filepath.Join(home, ".antigravity", "annotations", convID+".pbtxt"),
	}
	for _, p := range annoPaths {
		if fileExists(p) {
			return false // It has a real user annotation file -> NOT a subagent!
		}
	}

	// 2. Check conversation_metadata.json if present
	metaPaths := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".gemini", "antigravity", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".antigravity", "cache", "conversation_metadata.json"),
	}
	for _, mp := range metaPaths {
		if data, err := os.ReadFile(mp); err == nil {
			var meta struct {
				Conversations map[string]struct {
					IsInternal bool `json:"is_internal"`
				} `json:"conversations"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				if c, exists := meta.Conversations[convID]; exists {
					return c.IsInternal
				}
			}
		}
	}

	// 3. If no user annotation file exists and not marked as a root conversation in metadata, it is a subagent
	return true
}

func cleanEnvForVSCode(env []string) []string {
	var cleaned []string
	for _, e := range env {
		if strings.HasPrefix(e, "VSCODE_IPC_HOOK_CLI=") ||
			strings.HasPrefix(e, "ELECTRON_RUN_AS_NODE=") ||
			strings.HasPrefix(e, "NODE_OPTIONS=") {
			continue
		}
		cleaned = append(cleaned, e)
	}
	return cleaned
}

func LaunchVSCode(path, host string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}

	var vscodeURI string
	isRemote := host != "" && host != "local"

	if isRemote {
		hostLabel := host
		formattedPath := path
		if !strings.HasPrefix(formattedPath, "/") {
			formattedPath = "/" + formattedPath
		}
		vscodeURI = fmt.Sprintf("vscode://vscode-remote/ssh-remote+%s%s", hostLabel, formattedPath)
	} else {
		formattedPath := path
		if !strings.HasPrefix(formattedPath, "/") {
			formattedPath = "/" + formattedPath
		}
		vscodeURI = fmt.Sprintf("vscode://file%s", formattedPath)
	}

	var launchErr error
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("open", vscodeURI)
		if err := cmd.Start(); err == nil {
			return vscodeURI, nil
		} else {
			launchErr = err
		}
	} else if runtime.GOOS == "linux" {
		cmd := exec.Command("xdg-open", vscodeURI)
		if err := cmd.Start(); err == nil {
			return vscodeURI, nil
		} else {
			launchErr = err
		}
	}

	codeBin := findCodeBinary()
	var cliCmd *exec.Cmd
	if isRemote {
		cliCmd = exec.Command(codeBin, "--remote", fmt.Sprintf("ssh-remote+%s", host), path)
	} else {
		cliCmd = exec.Command(codeBin, path)
	}
	cliCmd.Env = cleanEnvForVSCode(os.Environ())

	if err := cliCmd.Start(); err == nil {
		return vscodeURI, nil
	} else {
		if launchErr != nil {
			return vscodeURI, fmt.Errorf("failed to open via URL (%v) and CLI (%s: %v)", launchErr, codeBin, err)
		}
		return vscodeURI, fmt.Errorf("failed to launch VS Code (%s): %v", codeBin, err)
	}
}

func findCodeBinary() string {
	if p, err := exec.LookPath("code"); err == nil {
		return p
	}
	candidates := []string{
		"/usr/local/bin/code",
		"/opt/homebrew/bin/code",
		"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
		"/Applications/Visual Studio Code - Insiders.app/Contents/Resources/app/bin/code",
		"/Applications/Cursor.app/Contents/Resources/app/bin/cursor",
		"/Applications/Windsurf.app/Contents/Resources/app/bin/windsurf",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "code"
}
