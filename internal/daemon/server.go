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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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

type Provider interface {
	Agent() string
	ParseHook(eventName string, payload []byte) (*Event, error)
	IsInstalled() bool
	CheckHookConfig() (configured bool, setupCmd string, err error)
}

type Server struct {
	db          *DB
	providers   map[string]Provider
	subscribers map[chan *Session]bool
	subMutex    sync.Mutex
	webFS       fs.FS
}

func NewServer(db *DB) *Server {
	return &Server{
		db:          db,
		providers:   make(map[string]Provider),
		subscribers: make(map[chan *Session]bool),
	}
}

func (s *Server) SetWebFS(webFS fs.FS) {
	s.webFS = webFS
}

func (s *Server) RegisterProvider(p Provider) {
	s.providers[p.Agent()] = p
}

func (s *Server) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/hooks/", s.handleHook)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/", s.handleSessionControl)
	mux.HandleFunc("/v1/sessions/control", s.handleSessionControl)
	mux.HandleFunc("/v1/sessions/pty", s.handlePTY)
	mux.HandleFunc("/v1/sessions/spawn", s.handleSpawn)
	mux.HandleFunc("/v1/agents/discovery", s.handleAgentDiscovery)
	mux.HandleFunc("/v1/documents", s.handleDocuments)
	mux.HandleFunc("/v1/documents/content", s.handleDocumentContent)
	mux.HandleFunc("/v1/nodes", s.handleNodes)
	mux.HandleFunc("/v1/nodes/move", s.handleNodeMove)
	mux.HandleFunc("/v1/hosts", s.handleHosts)
	mux.HandleFunc("/v1/hosts/update", s.handleHostUpdate)
	mux.HandleFunc("/v1/projects/create", s.handleCreateProject)
	mux.HandleFunc("/v1/maintenance/purge", s.handlePurge)
	mux.HandleFunc("/v1/version", s.handleVersion)
	mux.HandleFunc("/v1/shutdown", s.handleShutdown)
	mux.HandleFunc("/v1/events", s.handleEvents)

	// Serve embedded Web GUI
	if s.webFS != nil {
		fileServer := http.FileServer(http.FS(s.webFS))
		mux.Handle("/", fileServer)
	}

	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Ackbar-Host")
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
				ID:        sessionID,
				Agent:     event.Agent,
				Host:      host,
				NativeID:  event.NativeID,
				Managed:   true,
				TmuxName:  spawningSess.TmuxName,
				StartedAt: spawningSess.StartedAt,
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
	if len(event.Roots) > 0 {
		sess.Roots = event.Roots
	}
	if event.Name != "" {
		sess.Name = event.Name
	}
	sess.State = event.State
	sess.Blocked = event.Blocked
	sess.Activity = event.Activity
	sess.LastEventAt = event.LastEventAt

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

	s.verifySessionLiveness(r.Context(), sessions)

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
		s.verifySessionLiveness(r.Context(), sessions)
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

		deleteSessionFilesOnDisk(sess)

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

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"deleted"}`))

	case "restart":
		// Warn logic is client-side, the daemon just executes.
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
		resumeCmd := getResumeCmd(sess.Agent, sess.NativeID)
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

	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
}

func deleteSessionFilesOnDisk(sess *Session) {
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

	// 1. Claude Code transcript files and metadata
	if sess.Agent == "claude-code" || sess.Agent == "" {
		claudeDir := filepath.Join(home, ".claude")
		claudeProjectsDir := filepath.Join(claudeDir, "projects")
		if projDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
			for _, pDir := range projDirs {
				if pDir.IsDir() {
					projPath := filepath.Join(claudeProjectsDir, pDir.Name())
					_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("%s.jsonl", nativeID)))
					_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("agent-%s.jsonl", nativeID)))
					_ = os.RemoveAll(filepath.Join(projPath, nativeID))
				}
			}
		}

		// Clean up session metadata in ~/.claude/sessions/
		sessionsDir := filepath.Join(claudeDir, "sessions")
		if sFiles, err := os.ReadDir(sessionsDir); err == nil {
			for _, sf := range sFiles {
				if strings.HasSuffix(sf.Name(), ".json") {
					metaPath := filepath.Join(sessionsDir, sf.Name())
					if data, err := os.ReadFile(metaPath); err == nil {
						if strings.Contains(string(data), nativeID) {
							_ = os.Remove(metaPath)
						}
					}
				}
			}
		}

		// Clean up tasks, session-env, file-history, shell-snapshots
		_ = os.RemoveAll(filepath.Join(claudeDir, "tasks", nativeID))
		_ = os.RemoveAll(filepath.Join(claudeDir, "session-env", nativeID))
		_ = os.RemoveAll(filepath.Join(claudeDir, "file-history", nativeID))
		_ = os.RemoveAll(filepath.Join(claudeDir, "shell-snapshots", nativeID))
	}

	// 2. Google Antigravity
	if sess.Agent == "antigravity" {
		_ = os.RemoveAll(filepath.Join(home, ".gemini", "antigravity", "brain", nativeID))
		_ = os.Remove(filepath.Join(home, ".gemini", "antigravity", "annotations", nativeID+".pbtxt"))
		_ = os.Remove(filepath.Join(home, ".gemini", "antigravity", "conversations", nativeID+".json"))
	}

	// 3. OpenAI Codex
	if sess.Agent == "codex" {
		codexFile := filepath.Join(home, ".codex", "sessions", fmt.Sprintf("%s.json", nativeID))
		_ = os.Remove(codexFile)
	}
}

func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Agent string `json:"agent"`
		Cwd   string `json:"cwd"`
		Host  string `json:"host"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Agent == "" || req.Cwd == "" {
		http.Error(w, "Missing agent or cwd", http.StatusBadRequest)
		return
	}

	// 1. Forward to remote host if specified
	if req.Host != "" && req.Host != "local" {
		hostRec, err := s.db.GetHost(req.Host)
		if err == nil && hostRec != nil && hostRec.URL != "" {
			targetURL := strings.TrimSuffix(hostRec.URL, "/") + "/v1/sessions/spawn"
			payload, _ := json.Marshal(map[string]string{
				"agent": req.Agent,
				"cwd":   req.Cwd,
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
		}
	}

	tempUUID := generateUUID()
	tmuxName := fmt.Sprintf("ackbar-%s-%s", req.Agent, tempUUID)
	launchCmd := getSpawnCmd(req.Agent, tempUUID)

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
		Managed:     true,
		TmuxName:    tmuxName,
		State:       StateUnknown,
		Activity:    "Spawning session...",
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
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

func getResumeCmd(agent, nativeID string) string {
	if strings.HasPrefix(nativeID, "proc-") {
		nativeID = ""
	}
	switch agent {
	case "claude-code":
		if nativeID != "" && isValidUUID(nativeID) {
			return "claude --resume " + nativeID
		}
		return "claude"
	case "codex":
		if nativeID != "" {
			return "codex exec resume " + nativeID
		}
		return "codex exec"
	case "antigravity":
		if nativeID != "" {
			return "agy --conversation " + nativeID
		}
		return "agy"
	default:
		if nativeID != "" && isValidUUID(nativeID) {
			return "claude --resume " + nativeID
		}
		return "claude"
	}
}

func getSpawnCmd(agent, tempUUID string) string {
	switch agent {
	case "claude-code":
		if tempUUID != "" && isValidUUID(tempUUID) {
			return "claude --session-id " + tempUUID
		}
		return "claude"
	case "codex":
		return "codex exec"
	case "antigravity":
		return "agy"
	case "mock-agent":
		return "sleep 5"
	default:
		return "claude"
	}
}

func getDocPriority(name string) int {
	nameLower := strings.ToLower(name)
	if nameLower == "task.md" {
		return 10
	}
	if nameLower == "implementation_plan.md" {
		return 9
	}
	if nameLower == "walkthrough.md" {
		return 8
	}
	if strings.Contains(nameLower, "plan") {
		return 7
	}
	if strings.Contains(nameLower, "handover") {
		return 6
	}
	return 0
}

type AgentDiscoveryResult struct {
	Agent          string `json:"agent"`
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
			Installed:      installed,
			HookConfigured: configured,
			SetupCmd:       setupCmd,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

type DocumentItem struct {
	Title    string `json:"title"`
	Path     string `json:"path"`
	RelPath  string `json:"rel_path"`
	Priority int    `json:"priority"`
}

func (s *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		cwd = os.Getenv("HOME")
	}

	var docs []DocumentItem
	seen := make(map[string]bool)

	// Scan current directory for key markdown documents
	checkFiles := []string{
		"task.md", "implementation_plan.md", "walkthrough.md",
		"AGENTS.md", "README.md", "backlog.md",
	}

	for _, name := range checkFiles {
		fullPath := filepath.Join(cwd, name)
		if _, err := os.Stat(fullPath); err == nil && !seen[fullPath] {
			seen[fullPath] = true
			docs = append(docs, DocumentItem{
				Title:    name,
				Path:     fullPath,
				RelPath:  name,
				Priority: getDocPriority(name),
			})
		}
	}

	// Scan Antigravity brain artifact markdown files
	home, _ := os.UserHomeDir()
	if home != "" {
		brainDir := filepath.Join(home, ".gemini", "antigravity", "brain")
		if entries, err := os.ReadDir(brainDir); err == nil {
			for _, conv := range entries {
				if conv.IsDir() {
					convPath := filepath.Join(brainDir, conv.Name())
					if files, ferr := os.ReadDir(convPath); ferr == nil {
						for _, f := range files {
							if strings.HasSuffix(f.Name(), ".md") {
								full := filepath.Join(convPath, f.Name())
								if !seen[full] {
									seen[full] = true
									docs = append(docs, DocumentItem{
										Title:    fmt.Sprintf("Antigravity: %s", f.Name()),
										Path:     full,
										RelPath:  f.Name(),
										Priority: getDocPriority(f.Name()),
									})
								}
							}
						}
					}
				}
			}
		}
	}

	// Sort by priority descending
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Priority > docs[j].Priority
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
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "Missing path", http.StatusBadRequest)
		return
	}

	home, _ := os.UserHomeDir()

	var targetDir string
	var gitURL string
	alreadyExisted := false

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

		if !alreadyExisted {
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				log.Printf("[Daemon] Notice: could not create local directory %q: %v", targetDir, err)
			} else {
				alreadyExisted = true
			}
		}

		gitURL = req.GitURL
		if alreadyExisted {
			if gitURL == "" {
				out, err := exec.Command("git", "-C", targetDir, "remote", "get-url", "origin").Output()
				if err == nil {
					gitURL = strings.TrimSpace(string(out))
				}
			}
		} else {
			if gitURL != "" {
				_ = exec.Command("git", "clone", gitURL, targetDir).Run()
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
		// Run initial scan asynchronously on startup
		go s.scanObservedSessions(ctx)

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.scanObservedSessions(ctx)
				sessions, err := s.db.ListSessions()
				if err == nil {
					s.verifySessionLiveness(ctx, sessions)
				}
			}
		}
	}()
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

func (s *Server) verifySessionLiveness(ctx context.Context, sessions []*Session) {
	for _, sess := range sessions {
		if sess.State == StateEnded {
			continue
		}

		// Grace period for active sessions that sent hook events recently
		if time.Since(sess.LastEventAt) < 15*time.Minute {
			continue
		}

		alive := true
		if sess.Managed && sess.TmuxName != "" {
			alive = tmux.HasSession(ctx, sess.TmuxName)
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
			_ = s.db.SaveSession(sess)
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

	// 0. Refresh names/titles/metadata and prune dead proc-<pid> sessions
	if existingSessions, err := s.db.ListSessions(); err == nil {
		for _, sess := range existingSessions {
			if strings.HasPrefix(sess.NativeID, "proc-") {
				pidStr := strings.TrimPrefix(sess.NativeID, "proc-")
				if pidVal, pErr := strconv.Atoi(pidStr); pErr == nil && pidVal > 0 {
					if !isProcessAlive(pidVal) {
						_ = s.db.DeleteSession(sess.ID)
						_ = s.db.DeleteSession(sess.NativeID)
						continue
					}
				}
			}

			changed := false
			if sess.Agent == "claude-code" && sess.Cwd != "" {
				if meta := ReadClaudeSessionMeta(sess.Cwd, sess.NativeID); meta != nil {
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
				}
			} else if sess.Agent == "antigravity" && sess.Cwd != "" {
				if title := ReadAntigravitySessionTitle(sess.Cwd, sess.NativeID); title != "" && title != sess.Name && !strings.HasPrefix(title, "<") {
					sess.Name = title
					changed = true
				}
			}
			if changed {
				_ = s.db.SaveSession(sess)
				s.broadcast(sess)
			}
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
			tmuxLower := strings.ToLower(tmuxName)

			agent := ""
			if strings.Contains(cmdLower, "antigravity") || strings.Contains(cmdLower, "gemini") || strings.Contains(tmuxLower, "antigravity") || strings.Contains(tmuxLower, "gemini") {
				agent = "antigravity"
			} else if strings.Contains(cmdLower, "codex") || strings.Contains(tmuxLower, "codex") {
				agent = "codex"
			} else if strings.Contains(cmdLower, "claude") || strings.Contains(tmuxLower, "claude") || tmuxName != "" {
				agent = "claude-code"
			}

			if agent != "" && pidStr != "" && cwd != "" {
				var pid int
				fmt.Sscanf(pidStr, "%d", &pid)

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

				var existing *Session
				if targetNativeID != "" {
					existing, _ = s.db.GetSession(fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID))
					if existing == nil {
						if allSessions, err := s.db.ListSessions(); err == nil {
							for _, sObj := range allSessions {
								if sObj.NativeID == targetNativeID {
									existing = sObj
									break
								}
							}
						}
					}
				}

				if existing != nil {
					// Adopt and elevate existing session in place (no duplicate!)
					existing.Managed = true
					existing.State = StateWorking
					existing.PID = pid
					existing.TmuxName = tmuxName
					existing.LastEventAt = time.Now()
					_ = s.db.SaveSession(existing)
					s.broadcast(existing)
					_ = s.db.DeleteSession(fmt.Sprintf("%s:observed:proc-%d", hostName, pid))
					continue
				}

				nativeID := fmt.Sprintf("proc-%d", pid)
				sessID := fmt.Sprintf("%s:observed:%s", hostName, nativeID)

				existingObs, _ := s.db.GetSession(sessID)
				if existingObs == nil {
					sessionName := tmuxName
					if sessionName == "" {
						sessionName = fmt.Sprintf("%s (%s)", agent, nativeID)
					}
					newSess := &Session{
						ID:          sessID,
						Name:        sessionName,
						Agent:       agent,
						Host:        hostName,
						NativeID:    nativeID,
						Cwd:         cwd,
						ProjectKey:  GetProjectKey(cwd),
						State:       StateIdle,
						Managed:     true,
						TmuxName:    tmuxName,
						PID:         pid,
						Activity:    fmt.Sprintf("Observed running agent in tmux '%s' (PID %d)", tmuxName, pid),
						StartedAt:   time.Now(),
						LastEventAt: time.Now(),
					}
					_ = s.db.SaveSession(newSess)
					s.broadcast(newSess)
				} else {
					if existingObs.TmuxName != tmuxName || !existingObs.Managed {
						existingObs.TmuxName = tmuxName
						existingObs.Managed = true
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
			if (strings.Contains(cmdLower, "claude") || strings.Contains(cmdLower, ".claude")) && !strings.Contains(cmdLower, "ackbar") {
				agent = "claude-code"
			} else if (strings.Contains(cmdLower, "antigravity") || strings.Contains(cmdLower, "gemini")) && !strings.Contains(cmdLower, "ackbar") {
				agent = "antigravity"
			} else if strings.Contains(cmdLower, "codex") && !strings.Contains(cmdLower, "ackbar") {
				agent = "codex"
			}

			if agent != "" && pidStr != "" {
				var pid int
				fmt.Sscanf(pidStr, "%d", &pid)
				if pid <= 0 || pid == os.Getpid() {
					continue
				}

				var sID string
				if agent == "claude-code" {
					home, _ := os.UserHomeDir()
					if home != "" {
						sID, _ = findClaudeSessionForPID(home, pid)
					}
				}

				// Check if any existing session in the database already has this PID or native UUID
				hasExistingMatch := false
				if allSess, err := s.db.ListSessions(); err == nil {
					for _, sObj := range allSess {
						if (sObj.PID == pid && sObj.ID != fmt.Sprintf("%s:observed:proc-%d", hostName, pid)) || (sID != "" && sObj.NativeID == sID) {
							sObj.PID = pid
							_ = s.db.SaveSession(sObj)
							_ = s.db.DeleteSession(fmt.Sprintf("%s:observed:proc-%d", hostName, pid))
							hasExistingMatch = true
							break
						}
					}
				}
				if hasExistingMatch {
					continue
				}

				nativeID := fmt.Sprintf("proc-%d", pid)
				sessID := fmt.Sprintf("%s:observed:%s", hostName, nativeID)

				existing, _ := s.db.GetSession(sessID)
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
							StartedAt:   time.Now(),
							LastEventAt: time.Now(),
						}
						if agent == "claude-code" {
							meta := ReadClaudeSessionMeta(cwd, nativeID)
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
							}
						}
						_ = s.db.SaveSession(newSess)
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
								existing, _ := s.db.GetSession(sessID)
								if existing == nil {
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
									s.broadcast(newSess)
								}
							}
						}
					}
				}
			}
		}

		// 4. Scan disk-backed Antigravity brain sessions
		brainDir := filepath.Join(home, ".gemini", "antigravity", "brain")
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
					existing, _ := s.db.GetSession(sessID)
					if existing == nil {
						title := ReadAntigravitySessionTitle("", convID)
						cwd := ""
						firstPrompt := ""
						lastPrompt := ""
						modTime := time.Now()

						if stat, serr := os.Stat(convPath); serr == nil {
							modTime = stat.ModTime()
						}

						if data, rerr := os.ReadFile(logPath); rerr == nil {
							lines := strings.Split(string(data), "\n")
							for _, line := range lines {
								line = strings.TrimSpace(line)
								if line == "" {
									continue
								}
								var step struct {
									Type    string `json:"type"`
									Content string `json:"content"`
								}
								if jerr := json.Unmarshal([]byte(line), &step); jerr == nil {
									if step.Type == "USER_INPUT" && step.Content != "" {
										clean := cleanPromptText(step.Content)
										if clean != "" {
											if firstPrompt == "" {
												firstPrompt = clean
											}
											lastPrompt = clean
										}
									}
									if cwd == "" && strings.Contains(step.Content, "/Users/") {
										for _, cl := range strings.Split(step.Content, "\n") {
											if strings.Contains(cl, " -> ") && strings.Contains(cl, "/Users/") {
												parts := strings.Split(cl, " -> ")
												cand := strings.TrimSpace(parts[0])
												if dirExists(cand) && cand != home && !strings.Contains(cand, "/.gemini") {
													cwd = cand
													break
												}
											}
										}
										if cwd == "" {
											idx := strings.Index(step.Content, "/Users/")
											sub := step.Content[idx:]
											endIdx := strings.IndexAny(sub, " \",\n\r")
											if endIdx != -1 {
												cand := sub[:endIdx]
												if dirExists(cand) && cand != home && !strings.Contains(cand, "/.gemini") {
													cwd = cand
												}
											}
										}
									}
								}
							}
						}

						if title == "" {
							if firstPrompt != "" {
								title = truncateTitle(firstPrompt)
							} else {
								title = fmt.Sprintf("Antigravity (%s)", convID[:8])
							}
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
							FirstPrompt: firstPrompt,
							LastPrompt:  lastPrompt,
						}
						_ = s.db.SaveSession(newSess)
						s.broadcast(newSess)
					}
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

type SessionMeta struct {
	Title         string
	CustomTitle   string
	AITitle       string
	AIDescription string
	FirstPrompt   string
	LastPrompt    string
	Entrypoint    string
	Kind          string
	Version       string
	GitBranch     string
	ContextPct    int
}

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
		if data, rerr := os.ReadFile(filePath); rerr == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var obj struct {
					Type        string `json:"type"`
					Message     struct {
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

	// 1. Direct Lookup: Check ~/.gemini/antigravity/annotations/<sessionID>.pbtxt
	if targetID != "" {
		annoPath := filepath.Join(home, ".gemini", "antigravity", "annotations", targetID+".pbtxt")
		if data, err := os.ReadFile(annoPath); err == nil {
			content := string(data)
			if idx := strings.Index(content, `title:"`); idx != -1 {
				sub := content[idx+len(`title:"`):]
				if endIdx := strings.Index(sub, `"`); endIdx != -1 {
					return sub[:endIdx]
				}
			}
		}
	}

	// 2. Check brain task summary fallback
	if targetID != "" {
		metaPath := filepath.Join(home, ".gemini", "antigravity", "brain", targetID, "task.md.metadata.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(data, &meta); err == nil && meta.Summary != "" {
				return truncateTitle(meta.Summary)
			}
		}
	}

	// 3. Check central Antigravity proto registry: ~/.gemini/antigravity/agyhub_summaries_proto.pb
	if targetID != "" {
		protoPath := filepath.Join(home, ".gemini", "antigravity", "agyhub_summaries_proto.pb")
		if data, err := os.ReadFile(protoPath); err == nil {
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
					if !strings.HasPrefix(w, "file:") && !strings.HasPrefix(w, "git@") && !strings.Contains(w, "Users/") && len(w) >= 5 {
						return truncateTitle(w)
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
