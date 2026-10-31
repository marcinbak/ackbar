package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"ackbar/internal/relay"
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
	ToolName    string
	ToolInput   any
	StartedAt   time.Time
	LastEventAt time.Time
}

type HostHealth struct {
	Online      bool
	Version     string
	DisplayName string
	LatencyMs   int64
	LastCheck   time.Time
}

type Server struct {
	db                *DB
	providers         map[string]Provider
	subscribers       map[chan *Session]bool
	subMutex          sync.Mutex
	webFS             fs.FS
	configuredToken   string
	relayClient       *relay.Client
	headless          *HeadlessRunner
	hostName          string
	displayName       string
	hostHealthMap     map[string]*HostHealth
	hostHealthMu      sync.RWMutex
	activeSubagents   map[string][]*SubagentInfo
	subagentsMu       sync.RWMutex
	sessionHookLocks  sync.Map
	blockedBySubagent sync.Map
	homeDir           string
}

func NewServer(db *DB) *Server {
	StartUploadCleaner(defaultUploadDir, 6*time.Hour)
	s := &Server{
		db:              db,
		providers:       make(map[string]Provider),
		subscribers:     make(map[chan *Session]bool),
		hostHealthMap:   make(map[string]*HostHealth),
		activeSubagents: make(map[string][]*SubagentInfo),
	}
	s.headless = NewHeadlessRunner(db, s.broadcast)
	s.headless.SetOnTurnComplete(s.clearSubagents)
	if host := s.HostName(); host != "local" && db != nil {
		_ = db.MigrateLocalSessions(host)
	}
	return s
}

func (s *Server) SetToken(token string) {
	s.configuredToken = token
}

func (s *Server) SetWebFS(webFS fs.FS) {
	s.webFS = webFS
}

func (s *Server) SetHostIdentity(hostName, displayName string) {
	if hostName != "" {
		s.hostName = hostName
	}
	if displayName != "" {
		s.displayName = displayName
	}
	if s.db != nil && s.HostName() != "local" {
		_ = s.db.MigrateLocalSessions(s.HostName())
	}
}

func (s *Server) HostName() string {
	if s.hostName != "" {
		return s.hostName
	}
	if h := os.Getenv("ACKBAR_HOST"); h != "" {
		return h
	}
	if s.db != nil {
		if val, err := s.db.GetSetting("host_name"); err == nil && val != "" {
			return val
		}
	}
	return "local"
}

func (s *Server) DisplayName() string {
	if s.displayName != "" {
		return s.displayName
	}
	if d := os.Getenv("ACKBAR_DISPLAY_NAME"); d != "" {
		return d
	}
	if s.db != nil {
		if val, err := s.db.GetSetting("display_name"); err == nil && val != "" {
			return val
		}
	}
	return s.HostName()
}

func extractHostname(val string) string {
	h := strings.TrimSpace(val)
	if h == "" {
		return ""
	}
	if strings.Contains(h, "://") {
		if u, err := url.Parse(h); err == nil && u.Host != "" {
			h = u.Host
		}
	}
	if idx := strings.LastIndex(h, "@"); idx != -1 {
		h = h[idx+1:]
	}
	if hostOnly, _, err := net.SplitHostPort(h); err == nil && hostOnly != "" {
		return hostOnly
	}
	return h
}

func (s *Server) isLocalHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" || strings.EqualFold(h, "local") || strings.EqualFold(h, "localhost") || h == "127.0.0.1" || h == "::1" {
		return true
	}
	hostName := s.HostName()
	displayName := s.DisplayName()
	if strings.EqualFold(h, hostName) || (displayName != "" && strings.EqualFold(h, displayName)) {
		return true
	}
	extracted := extractHostname(h)
	if extracted != "" {
		if strings.EqualFold(extracted, "local") || strings.EqualFold(extracted, "localhost") || extracted == "127.0.0.1" || extracted == "::1" {
			return true
		}
		if strings.EqualFold(extracted, hostName) || (displayName != "" && strings.EqualFold(extracted, displayName)) {
			return true
		}
	}
	return false
}

func (s *Server) isRequestForLocalDaemon(r *http.Request, host string) bool {
	if s.isLocalHost(host) {
		return true
	}
	if r != nil && r.Host != "" {
		reqHostName := extractHostname(r.Host)
		targetHostName := extractHostname(host)
		if reqHostName != "" && targetHostName != "" && strings.EqualFold(reqHostName, targetHostName) {
			return true
		}
	}
	return false
}

func (s *Server) RegisterProvider(p Provider) {
	s.providers[p.Agent()] = p
}

func (s *Server) StartRelayClient(ctx context.Context, relayURL, relaySecret string) {
	if relayURL == "" {
		return
	}
	hostName := s.HostName()
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
		if path == "/" || path == "/index.html" || path == "/style.css" || path == "/app.js" || path == "/manifest.json" || path == "/favicon.ico" || path == "/apple-touch-icon.png" || strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/icons/") {
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
	mux.HandleFunc("/v1/sessions/subagents", s.handleSessionSubagents)
	mux.HandleFunc("/v1/sessions/transcript", s.handleSessionTranscript)
	mux.HandleFunc("/v1/sessions/", s.handleSessionControl)
	mux.HandleFunc("/v1/sessions/control", s.handleSessionControl)
	mux.HandleFunc("/v1/sessions/handover", s.handleSessionHandover)
	mux.HandleFunc("/v1/sessions/pty", s.handlePTY)
	mux.HandleFunc("/v1/sessions/spawn", s.handleSpawn)
	mux.HandleFunc("/v1/meta/resolve", s.handleMetaResolve)
	mux.HandleFunc("/v1/sessions/prompt", s.handlePrompt)
	mux.HandleFunc("/v1/sessions/prompt/queue", s.handlePromptQueue)
	mux.HandleFunc("/v1/sessions/prompt/queue/resume", s.handlePromptQueueResume)
	mux.HandleFunc("/v1/sessions/chat/stream", s.handleChatStream)
	mux.HandleFunc("/v1/sessions/cancel", s.handleCancelTurn)
	mux.HandleFunc("/v1/sessions/take-wheel", s.handleTakeWheel)
	mux.HandleFunc("/v1/accounts", s.handleAccounts)
	mux.HandleFunc("/v1/accounts/", s.handleAccountOps)
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
	mux.HandleFunc("/v1/files/content", s.handleFileContent)
	mux.HandleFunc("/v1/files/open", s.handleFileOpen)
	mux.HandleFunc("/v1/version", s.handleVersion)
	mux.HandleFunc("/v1/update", s.handleUpdate)
	mux.HandleFunc("/v1/settings", s.handleSettings)
	mux.HandleFunc("/v1/uploads", s.handleUpload)
	mux.HandleFunc("/v1/shutdown", s.handleShutdown)
	mux.HandleFunc("/v1/tasks", s.handleTasks)
	mux.HandleFunc("/v1/tasks/event", s.handleTaskEvent)
	mux.HandleFunc("/v1/tasks/propose", s.handleTaskPropose)
	mux.HandleFunc("/v1/tasks/deliverable", s.handleTaskDeliverable)
	mux.HandleFunc("/v1/tasks/sync-workflow", s.handleTaskSyncWorkflow)
	mux.HandleFunc("/v1/tasks/merge-pr", s.handleTaskMergePR)
	mux.HandleFunc("/v1/tasks/deduplicate", s.handleTaskDeduplicate)
	mux.HandleFunc("/v1/standup", s.handleStandup)
	mux.HandleFunc("/v1/briefings/synthesize", s.handleBriefingSynthesize)
	mux.HandleFunc("/v1/agents/status", s.handleAgentsStatus)
	mux.HandleFunc("/v1/agents/provision", s.handleAgentsProvision)
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

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"version":      version.Version,
		"host":         s.HostName(),
		"display_name": s.DisplayName(),
	}

	info, err := version.CheckLatestRelease(r.Context(), false)
	if err == nil && info != nil {
		resp["latest_version"] = info.Version
		resp["update_available"] = info.IsNewer
		resp["release_url"] = info.ReleaseURL
		resp["install_type"] = info.InstallType
		resp["is_homebrew"] = info.IsHomebrew
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		force := r.URL.Query().Get("force") == "true"
		info, err := version.CheckLatestRelease(r.Context(), force)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to check for updates: %v", err), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)

	case http.MethodPost:
		res, err := version.UpdateToLatest(r.Context(), "")
		if err != nil {
			http.Error(w, fmt.Sprintf("Update failed: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) populateDefaultSettings(settings map[string]string) map[string]string {
	if settings == nil {
		settings = make(map[string]string)
	}
	defaults := map[string]string{
		"auto_done_enabled":           "true",
		"auto_done_hours":             "24",
		"auto_archive_enabled":        "true",
		"auto_archive_days":           "7",
		"done_collapsed_by_default":   "true",
		"later_collapsed_by_default":  "false",
		"handover_suggestion_enabled": "true",
		"handover_threshold_pct":      "60",
		"hide_session_tabs":           "false",
	}
	for k, v := range defaults {
		if _, exists := settings[k]; !exists {
			settings[k] = v
		}
	}
	settings["host_name"] = s.HostName()
	settings["display_name"] = s.DisplayName()
	return settings
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.db.GetAllSettings()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		settings = s.populateDefaultSettings(settings)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(settings)

	case http.MethodPost:
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		if err := s.db.SetSettings(req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if newHost, ok := req["host_name"]; ok && newHost != "" {
			s.hostName = newHost
			if s.db != nil && newHost != "local" {
				_ = s.db.MigrateLocalSessions(newHost)
			}
		}
		if newDisp, ok := req["display_name"]; ok {
			s.displayName = newDisp
		}
		settings, _ := s.db.GetAllSettings()
		settings = s.populateDefaultSettings(settings)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(settings)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
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
		// Run initial scan, host tunnel check, and dev-workflow run ingestion asynchronously on startup
		go s.scanObservedSessions(ctx)
		go s.ensureHostTunnels(ctx)
		go s.IngestDevWorkflowRuns()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.scanObservedSessions(ctx)
				go s.ensureHostTunnels(ctx)
				go s.IngestDevWorkflowRuns()
				sessions, err := s.db.ListActiveSessions()
				if err == nil && len(sessions) > 0 {
					s.verifySessionLiveness(ctx, sessions)
				}
			}
		}
	}()
}
