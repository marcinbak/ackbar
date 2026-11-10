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
	"sync/atomic"
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
	ToolName    string
	ToolInput   any
	StartedAt   time.Time
	LastEventAt time.Time
}

type HostHealth struct {
	Online               bool
	Version              string
	DisplayName          string
	LatencyMs            int64
	LastCheck            time.Time
	DetectedCapabilities []string
	PolicyTags           []string
	RoutingWeight        int
	Load                 *HostLoadSnapshot
}

type Server struct {
	db                   *DB
	providers            map[string]Provider
	subscribers          map[chan *Session]bool
	subMutex             sync.Mutex
	webFS                fs.FS
	configuredToken      string
	relayClient          *relay.Client
	headless             *HeadlessRunner
	hostName             string
	displayName          string
	hostHealthMap        map[string]*HostHealth
	hostHealthMu         sync.RWMutex
	cachedCapabilities   []string
	capabilitiesCachedAt time.Time
	activeSubagents      map[string][]*SubagentInfo
	subagentsMu          sync.RWMutex
	sessionHookLocks     sync.Map
	blockedBySubagent    sync.Map
	homeDir              string
	livenessRunning      atomic.Bool
	lastLivenessAt       atomic.Int64
	speechCacheMu        sync.RWMutex
	speechCache          map[string]*SpeechSummary
	speechLLMRunner      func(ctx context.Context, content string) (string, []string, error)
	ntfyServer           string
	ntfyTopic            string
	ntfyClient           *http.Client
	ntfyHook             func(payload NtfyPayload)
	voiceBridgeMu        sync.Mutex
	voiceBridge          *MicServeBridge
}

func NewServer(db *DB) *Server {
	StartUploadCleaner(defaultUploadDir, 6*time.Hour)
	s := &Server{
		db:              db,
		providers:       make(map[string]Provider),
		subscribers:     make(map[chan *Session]bool),
		hostHealthMap:   make(map[string]*HostHealth),
		activeSubagents: make(map[string][]*SubagentInfo),
		speechCache:     make(map[string]*SpeechSummary),
		ntfyClient:      &http.Client{Timeout: 5 * time.Second},
	}
	s.headless = NewHeadlessRunner(db, s.broadcast)
	s.headless.SetOnTurnComplete(s.clearSubagents)
	s.headless.LoadScheduledPrompts()
	if host := s.HostName(); host != "local" && db != nil {
		_ = db.MigrateLocalSessions(host)
		s.pruneForeignSessions()
	}
	return s
}

func (s *Server) isForeignSession(host string) bool {
	if s.isLocalHost(host) {
		return false
	}
	if s.HostName() != "local" {
		return true
	}
	return s.resolveHost(host) != nil
}

func (s *Server) pruneForeignSessions() {
	if s.db == nil {
		return
	}
	sessions, err := s.db.ListSessions()
	if err != nil {
		return
	}
	for _, sess := range sessions {
		if sess != nil && s.isForeignSession(sess.Host) {
			_ = s.db.DeleteSession(sess.ID)
		}
	}
}

func (s *Server) SetToken(token string) {
	s.configuredToken = token
}

func (s *Server) SetSpeechLLMRunner(runner func(ctx context.Context, content string) (string, []string, error)) {
	s.speechLLMRunner = runner
}

func (s *Server) SetWebFS(webFS fs.FS) {
	s.webFS = webFS
}

// DefaultSystemHostName returns the normalized short OS hostname (lowercased,
// with .local/.lan/.home suffix stripped), or "" if unavailable.
func DefaultSystemHostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return normalizeSystemHostName(h)
}

func normalizeSystemHostName(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	for _, suffix := range []string{".localdomain", ".local", ".lan", ".home"} {
		if strings.HasSuffix(h, suffix) {
			h = strings.TrimSuffix(h, suffix)
			break
		}
	}
	if idx := strings.IndexByte(h, '.'); idx > 0 {
		h = h[:idx]
	}
	if h == "" || h == "localhost" || h == "local" {
		return ""
	}
	return h
}

func (s *Server) SetHostIdentity(hostName, displayName string) {
	if hostName != "" && !strings.EqualFold(hostName, "local") {
		s.hostName = hostName
	}
	if displayName != "" && !strings.EqualFold(displayName, "local") {
		s.displayName = displayName
	}
	if s.db != nil && s.HostName() != "local" {
		_ = s.db.MigrateLocalSessions(s.HostName())
		s.pruneForeignSessions()
	}
}

func (s *Server) HostName() string {
	if s.hostName != "" && !strings.EqualFold(s.hostName, "local") {
		return s.hostName
	}
	if h := os.Getenv("ACKBAR_HOST"); h != "" && !strings.EqualFold(h, "local") {
		return h
	}
	if s.db != nil {
		if val, err := s.db.GetSetting("host_name"); err == nil && val != "" && !strings.EqualFold(val, "local") {
			return val
		}
	}
	return "local"
}

func (s *Server) DisplayName() string {
	if s.displayName != "" && !strings.EqualFold(s.displayName, "local") {
		return s.displayName
	}
	if d := os.Getenv("ACKBAR_DISPLAY_NAME"); d != "" && !strings.EqualFold(d, "local") {
		return d
	}
	if s.db != nil {
		if val, err := s.db.GetSetting("display_name"); err == nil && val != "" && !strings.EqualFold(val, "local") {
			return val
		}
	}
	return s.HostName()
}

func (s *Server) SetNtfyConfig(server, topic string) {
	if server != "" {
		s.ntfyServer = server
	}
	if topic != "" {
		s.ntfyTopic = topic
	}
}

func (s *Server) SetNtfyHook(hook func(payload NtfyPayload)) {
	s.ntfyHook = hook
}

func (s *Server) NtfyServer() string {
	if s.ntfyServer != "" {
		return s.ntfyServer
	}
	if env := os.Getenv("ACKBAR_NTFY_SERVER"); env != "" {
		return env
	}
	if s.db != nil {
		if val, err := s.db.GetSetting("ntfy_server"); err == nil && val != "" {
			return val
		}
	}
	return "https://ntfy.sh"
}

func (s *Server) NtfyTopic() string {
	if s.ntfyTopic != "" {
		return s.ntfyTopic
	}
	if env := os.Getenv("ACKBAR_NTFY_TOPIC"); env != "" {
		return env
	}
	if s.db != nil {
		if val, err := s.db.GetSetting("ntfy_topic"); err == nil && val != "" {
			return val
		}
	}
	return ""
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
	mux.HandleFunc("/v1/mcp", s.handleMCP)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/respond", s.handleRespond)
	mux.HandleFunc("/v1/sessions/subagents", s.handleSessionSubagents)
	mux.HandleFunc("/v1/sessions/transcript", s.handleSessionTranscript)
	mux.HandleFunc("/v1/sessions/", s.handleSessionDispatcher)
	mux.HandleFunc("/v1/sessions/control", s.handleSessionControl)
	mux.HandleFunc("/v1/sessions/handover", s.handleSessionHandover)
	mux.HandleFunc("/v1/sessions/pty", s.handlePTY)
	mux.HandleFunc("/v1/sessions/spawn", s.handleSpawn)
	mux.HandleFunc("/v1/sessions/bind-supervisor", s.handleBindSupervisor)
	mux.HandleFunc("/v1/meta/resolve", s.handleMetaResolve)
	mux.HandleFunc("/v1/sessions/prompt", s.handlePrompt)
	mux.HandleFunc("/v1/sessions/prompt/queue", s.handlePromptQueue)
	mux.HandleFunc("/v1/sessions/prompt/queue/resume", s.handlePromptQueueResume)
	mux.HandleFunc("/v1/sessions/summarize-speech", s.handleSummarizeSpeech)
	mux.HandleFunc("/v1/sessions/voice-bridge", s.handleVoiceBridge)
	mux.HandleFunc("/v1/sessions/voice-bridge/status", s.handleVoiceBridgeStatus)
	mux.HandleFunc("/v1/sessions/chat/stream", s.handleChatStream)
	mux.HandleFunc("/v1/sessions/cancel", s.handleCancelTurn)
	mux.HandleFunc("/v1/sessions/take-wheel", s.handleTakeWheel)
	mux.HandleFunc("/v1/sessions/switch-headless", s.handleSwitchHeadless)
	mux.HandleFunc("/v1/accounts", s.handleAccounts)
	mux.HandleFunc("/v1/accounts/", s.handleAccountOps)
	mux.HandleFunc("/v1/agents/discovery", s.handleAgentDiscovery)
	mux.HandleFunc("/v1/providers", s.handleProviders)
	mux.HandleFunc("/v1/documents", s.handleDocuments)
	mux.HandleFunc("/v1/documents/content", s.handleDocumentContent)
	mux.HandleFunc("/v1/nodes", s.handleNodes)
	mux.HandleFunc("/v1/nodes/move", s.handleNodeMove)
	mux.HandleFunc("/v1/nodes/paths", s.handleNodeHostPaths)
	mux.HandleFunc("/v1/sync/state", s.handleSyncState)
	mux.HandleFunc("/v1/sync/push", s.handleSyncPush)
	mux.HandleFunc("/v1/hosts", s.handleHosts)
	mux.HandleFunc("/v1/hosts/utilization", s.handleHostUtilization)
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
	mux.HandleFunc("/v1/notifications/test", s.handleTestNotification)
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
	mux.HandleFunc("/v1/agents/skills-repo/sync", s.handleSkillsRepoSync)
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
		localSessions := make([]*Session, 0, len(sessions))
		var active []*Session
		for _, sess := range sessions {
			if sess == nil {
				continue
			}
			if s.isForeignSession(sess.Host) {
				_ = s.db.DeleteSession(sess.ID)
				continue
			}
			localSessions = append(localSessions, sess)
			if sess.State != StateEnded && s.isLocalHost(sess.Host) {
				active = append(active, sess)
			}
		}
		if len(active) > 0 {
			s.verifyHeadlessLiveness(active)
			s.triggerAsyncLiveness()
		}
		for _, sess := range localSessions {
			data, err := json.Marshal(sess)
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
	tags, weight := s.LocalPolicyAndWeight()
	load := s.CollectLocalHostLoad()
	resp := map[string]interface{}{
		"version":               version.Version,
		"host":                  s.HostName(),
		"display_name":          s.DisplayName(),
		"detected_capabilities": s.GetDetectedCapabilities(),
		"policy_tags":           tags,
		"routing_weight":        weight,
		"load":                  load,
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
		localOnly := r.URL.Query().Get("local_only") == "true"
		info, _ := version.CheckLatestRelease(r.Context(), true)
		targetVer := version.Version
		if info != nil && version.IsNewer(info.Version, targetVer) {
			targetVer = info.Version
		}

		var res *version.UpdateResult
		var localRestartNeeded bool

		if info != nil && version.IsNewer(info.Version, version.Version) {
			var err error
			res, err = version.UpdateToLatest(r.Context(), "")
			if err != nil {
				http.Error(w, fmt.Sprintf("Update failed: %v", err), http.StatusInternalServerError)
				return
			}
			localRestartNeeded = true
			if res.NewVersion != "" && res.NewVersion != "latest" {
				targetVer = res.NewVersion
			}
		} else {
			res = &version.UpdateResult{
				PreviousVersion: version.Version,
				NewVersion:      version.Version,
				Method:          "local",
				Message:         fmt.Sprintf("Local daemon is already at v%s.", version.Version),
			}
		}

		var hostSummaries []HostUpdateSummary
		if !localOnly {
			hostSummaries = s.UpdateOutdatedRemoteHosts(targetVer)
			var msgLines []string
			if res.Message != "" {
				msgLines = append(msgLines, res.Message)
			}
			for _, hs := range hostSummaries {
				if hs.Message != "" {
					msgLines = append(msgLines, fmt.Sprintf("• %s: %s", hs.Host, hs.Message))
				}
			}
			res.Message = strings.Join(msgLines, "\n")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"previous_version": res.PreviousVersion,
			"new_version":      targetVer,
			"method":           res.Method,
			"message":          res.Message,
			"host_results":     hostSummaries,
			"restarting_local": localRestartNeeded,
		})

		if localRestartNeeded {
			restartLocalDaemonAsync()
		}

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
		"ntfy_server":                 "https://ntfy.sh",
		"ntfy_topic":                  "",
	}
	for k, v := range defaults {
		if _, exists := settings[k]; !exists {
			settings[k] = v
		}
	}
	settings["host_name"] = s.HostName()
	if disp := s.DisplayName(); !strings.EqualFold(disp, "local") {
		settings["display_name"] = disp
	} else {
		settings["display_name"] = ""
	}
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
		propagateNtfy := req["propagate_ntfy"] == "true"
		delete(req, "propagate_ntfy")

		if strings.EqualFold(strings.TrimSpace(req["host_name"]), "local") {
			delete(req, "host_name")
		}
		if strings.EqualFold(strings.TrimSpace(req["display_name"]), "local") {
			req["display_name"] = ""
		}

		if err := s.db.SetSettings(req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if propagateNtfy && strings.TrimSpace(req["ntfy_topic"]) != "" {
			server := strings.TrimSpace(req["ntfy_server"])
			if server == "" {
				server = s.NtfyServer()
			}
			go s.propagateSettingsToFleet(map[string]string{
				"ntfy_topic":  strings.TrimSpace(req["ntfy_topic"]),
				"ntfy_server": server,
			})
		}
		if newHost, ok := req["host_name"]; ok && newHost != "" {
			s.hostName = newHost
			if s.db != nil && newHost != "local" {
				_ = s.db.MigrateLocalSessions(newHost)
				s.pruneForeignSessions()
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
		// Run initial scan, host tunnel check, dev-workflow run sync, skills repo auto-sync, and peer sync asynchronously on startup
		go s.scanObservedSessions(ctx)
		go s.ensureHostTunnels(ctx)
		go func() { _ = s.IngestDevWorkflowRuns() }()
		go s.maybeAutoSyncSkillsRepo(ctx)
		go s.PullFromPeers(ctx)

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		schedTicker := time.NewTicker(5 * time.Second)
		defer schedTicker.Stop()
		hourlyTicker := time.NewTicker(1 * time.Hour)
		defer hourlyTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-schedTicker.C:
				s.processDueScheduledPrompts(ctx)
			case <-hourlyTicker.C:
				go s.maybeAutoSyncSkillsRepo(ctx)
				if s.db != nil {
					_ = s.db.RollupAndPruneHostLoadMetrics(time.Now().UTC())
				}
			case <-ticker.C:
				s.scanObservedSessions(ctx)
				go s.ensureHostTunnels(ctx)
				go func() { _ = s.IngestDevWorkflowRuns() }()
				go s.PullFromPeers(ctx)
				go s.recordFleetLoadSamples(time.Now().UTC())
				sessions, err := s.db.ListActiveSessions()
				if err == nil && len(sessions) > 0 {
					s.verifySessionLiveness(ctx, sessions)
				}
			}
		}
	}()
}

func (s *Server) processDueScheduledPrompts(ctx context.Context) {
	if s.headless == nil || s.db == nil {
		return
	}

	sessions, err := s.db.ListActiveSessions()
	if err != nil || len(sessions) == 0 {
		return
	}

	now := time.Now()
	for _, sess := range sessions {
		if !s.isLocalHost(sess.Host) {
			continue
		}

		items, _ := s.headless.GetPromptQueue(sess.ID)
		if len(items) == 0 {
			continue
		}

		hasDueScheduled := false
		for _, it := range items {
			if it != nil && it.DeliverAt != nil && !it.DeliverAt.After(now) {
				hasDueScheduled = true
				break
			}
		}
		if !hasDueScheduled {
			continue
		}

		if sess.State == StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == BlockRateLimit {
			sess.Blocked = nil
			sess.State = StateIdle
			s.headless.SetQueuePaused(sess.ID, false)
			_ = s.db.SaveSession(sess)
			s.broadcast(sess)
		}

		// Only dispatch when session is in StateIdle (never when blocked on permissions/questions or working)
		if sess.State != StateIdle {
			continue
		}

		if sess.EngineType == EngineHeadless {
			if !s.headless.IsRunning(sess.ID) && !s.headless.IsQueuePaused(sess.ID) {
				popped := s.headless.DequeuePrompt(sess.ID)
				if popped != nil {
					s.headless.EmitQueueUpdate(sess.ID)
					go s.headless.RunTurn(context.Background(), sess, popped.Text)
				}
			}
		} else if sess.EngineType == EngineTmux || sess.TmuxName != "" {
			popped := s.headless.DequeuePrompt(sess.ID)
			if popped != nil {
				s.headless.EmitQueueUpdate(sess.ID)
				if sess.TmuxName != "" {
					_ = tmux.SendInput(ctx, sess.TmuxName, popped.Text, true)
					sess.State = StateWorking
					sess.Activity = "Processing scheduled prompt"
					sess.LastEventAt = time.Now()
					_ = s.db.SaveSession(sess)
					s.broadcast(sess)
				}
			}
		}
	}
}

func (s *Server) maybeAutoSyncSkillsRepo(ctx context.Context) {
	home := s.homeDir
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	MaybeAutoSyncSkillsRepo(ctx, home, s.db)
}

func (s *Server) handleSessionDispatcher(w http.ResponseWriter, r *http.Request) {
	parts := stringsSplit(r.URL.Path, "/")
	if len(parts) >= 5 {
		sessionID := parts[3]
		subAction := strings.ToLower(parts[4])
		switch subAction {
		case "suggestions":
			s.handleSessionSuggestions(w, r, sessionID)
			return
		case "config":
			s.handleSessionConfig(w, r, sessionID)
			return
		case "respond":
			s.handleRespond(w, r)
			return
		}
	}
	s.handleSessionControl(w, r)
}
