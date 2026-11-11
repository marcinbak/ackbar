package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AuthBroker struct {
	server          *Server
	mu              sync.Mutex
	activeFlows     map[string]*AuthFlow
	completedFlows  map[string]*AuthFlow
	authURLTimeout  time.Duration
	commandResolver func(sess *Session, target string, db *DB, home string) ([]string, string, bool, error)
}

func newAuthBroker(s *Server) *AuthBroker {
	return &AuthBroker{
		server:          s,
		activeFlows:     make(map[string]*AuthFlow),
		completedFlows:  make(map[string]*AuthFlow),
		authURLTimeout:  10 * time.Second,
		commandResolver: resolveAllowlistedAuthCommand,
	}
}

func (s *Server) handleSessionAuthTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Payload too large or read failure", http.StatusBadRequest)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Target    string `json:"target"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid json", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.Target == "" {
		http.Error(w, "Missing session_id or target", http.StatusBadRequest)
		return
	}

	sess, handled := s.resolveSessionOrProxy(w, http.MethodPost, req.SessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return "/v1/sessions/auth-tool"
	})
	if handled {
		return
	}

	b := s.authBroker
	if b == nil {
		http.Error(w, "Auth broker not initialized", http.StatusInternalServerError)
		return
	}

	b.CancelSession(req.SessionID)

	b.mu.Lock()
	if len(b.activeFlows) >= 16 {
		b.mu.Unlock()
		http.Error(w, "Too many active auth flows", http.StatusTooManyRequests)
		return
	}
	b.mu.Unlock()

	home, _ := os.UserHomeDir()
	resolver := b.commandResolver
	if resolver == nil {
		resolver = resolveAllowlistedAuthCommand
	}
	argv, dir, acceptsStdin, err := resolver(sess, req.Target, s.db, home)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to resolve auth command: %v", err), http.StatusBadRequest)
		return
	}

	flowCtx, flowCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	cmd := exec.CommandContext(flowCtx, argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(buildWorkerEnv(sess, s.db, home), "DISPLAY=", "BROWSER=true")
	cmd.WaitDelay = 2 * time.Second

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		flowCancel()
		http.Error(w, fmt.Sprintf("Failed to open stdin pipe: %v", err), http.StatusInternalServerError)
		return
	}

	ringBuf := newAuthRingBuffer(64 * 1024)
	cmd.Stdout = ringBuf
	cmd.Stderr = ringBuf

	if err := cmd.Start(); err != nil {
		flowCancel()
		http.Error(w, fmt.Sprintf("Failed to start auth process: %v", err), http.StatusInternalServerError)
		return
	}

	randBytes := make([]byte, 16)
	_, _ = rand.Read(randBytes)
	flowID := hex.EncodeToString(randBytes)

	flow := &AuthFlow{
		ID:           flowID,
		SessionID:    req.SessionID,
		Target:       req.Target,
		AcceptsStdin: acceptsStdin,
		stdin:        stdinPipe,
		cmd:          cmd,
		cancel:       flowCancel,
		buf:          ringBuf,
		doneCh:       make(chan struct{}),
		startedAt:    time.Now(),
	}

	b.mu.Lock()
	b.activeFlows[req.SessionID] = flow
	b.mu.Unlock()

	go b.watchAuthFlow(flow)

	deadline := time.Now().Add(b.authURLTimeout)
	var parsedURL *url.URL
	var rawURL string

	for time.Now().Before(deadline) {
		select {
		case <-flow.doneCh:
			break
		default:
		}
		text := ringBuf.String()
		if m := oauthURLRegex.FindString(text); m != "" {
			if u, err := validateAuthURL(m); err == nil {
				parsedURL = u
				rawURL = m
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if parsedURL == nil {
		b.CancelSession(req.SessionID)
		http.Error(w, "Failed to capture auth url within timeout", http.StatusGatewayTimeout)
		return
	}

	time.Sleep(100 * time.Millisecond)
	postURLText := ringBuf.String()

	recordedPort := 0
	if redirectParam := parsedURL.Query().Get("redirect_uri"); redirectParam != "" {
		if ru, err := url.Parse(redirectParam); err == nil {
			if ru.Hostname() == "127.0.0.1" || ru.Hostname() == "localhost" {
				if p, _ := strconv.Atoi(ru.Port()); p >= 1024 && p <= 65535 {
					recordedPort = p
				}
			}
		}
	}
	if recordedPort == 0 {
		if pm := oauthPortRegex.FindStringSubmatch(postURLText); len(pm) > 1 {
			if p, _ := strconv.Atoi(pm[1]); p >= 1024 && p <= 65535 {
				recordedPort = p
			}
		}
	}
	flow.RecordedPort = recordedPort

	if sess != nil && sess.Blocked != nil && sess.Blocked.Kind == BlockAuth {
		sess.Blocked.AuthURL = rawURL
		sess.Blocked.FlowID = flow.ID
		if s.db != nil {
			_ = s.db.SaveSession(sess)
		}
		s.broadcast(sess)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "started",
		"flow_id":       flow.ID,
		"auth_url":      rawURL,
		"recorded_port": flow.RecordedPort,
		"accepts_stdin": flow.AcceptsStdin,
	})
}

func (s *Server) handleSessionAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Payload too large or read failure", http.StatusBadRequest)
		return
	}
	var req struct {
		SessionID   string `json:"session_id"`
		FlowID      string `json:"flow_id"`
		CallbackURL string `json:"callback_url"`
		Code        string `json:"code"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid json", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.FlowID == "" {
		http.Error(w, "Missing session_id or flow_id", http.StatusBadRequest)
		return
	}

	_, handled := s.resolveSessionOrProxy(w, http.MethodPost, req.SessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return "/v1/sessions/auth-callback"
	})
	if handled {
		return
	}

	b := s.authBroker
	if b == nil {
		http.Error(w, "Auth broker not initialized", http.StatusInternalServerError)
		return
	}

	b.mu.Lock()
	flow := b.activeFlows[req.SessionID]
	if flow == nil || flow.ID != req.FlowID {
		flow = b.completedFlows[req.FlowID]
	}
	b.mu.Unlock()

	if flow == nil {
		http.Error(w, "Auth flow not found or expired", http.StatusNotFound)
		return
	}

	select {
	case <-flow.doneCh:
		if flow.exitCode == 0 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "authenticated", "completed": true})
			return
		}
	default:
	}

	val := strings.TrimSpace(req.CallbackURL)
	if val == "" {
		val = strings.TrimSpace(req.Code)
	}
	if val == "" {
		http.Error(w, "Missing callback_url or code", http.StatusBadRequest)
		return
	}

	isLoopbackHTTP := flow.RecordedPort >= 1024 && flow.RecordedPort <= 65535 &&
		(strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") || strings.HasPrefix(val, "/"))

	dialSuccess := false
	if isLoopbackHTTP {
		u, err := url.Parse(val)
		if err == nil {
			if u.Port() != "" && u.Port() != strconv.Itoa(flow.RecordedPort) {
				http.Error(w, "Port mismatch", http.StatusBadRequest)
				return
			}
			if u.Query().Get("code") == "" && u.Query().Get("error") == "" {
				http.Error(w, "Missing code or error query parameter", http.StatusBadRequest)
				return
			}
			targetLoopbackURL := fmt.Sprintf("http://127.0.0.1:%d%s", flow.RecordedPort, u.RequestURI())
			client := &http.Client{
				Timeout: 5 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			resp, dialErr := client.Get(targetLoopbackURL)
			if dialErr == nil {
				dialSuccess = true
				_, _ = io.CopyN(io.Discard, resp.Body, 64<<10)
				_ = resp.Body.Close()
			}
		}
	}

	if !dialSuccess {
		if !flow.AcceptsStdin {
			http.Error(w, "Loopback connection failed and stdin not accepted", http.StatusBadRequest)
			return
		}
		flow.mu.Lock()
		if flow.stdinUsed {
			flow.mu.Unlock()
			http.Error(w, "Stdin callback already provided", http.StatusBadRequest)
			return
		}
		if len(val) > 4096 || strings.ContainsAny(val, "\r\n\x00") {
			flow.mu.Unlock()
			http.Error(w, "Invalid callback token", http.StatusBadRequest)
			return
		}
		for i := 0; i < len(val); i++ {
			if val[i] < 0x20 || val[i] > 0x7e {
				flow.mu.Unlock()
				http.Error(w, "Invalid character in token", http.StatusBadRequest)
				return
			}
		}
		isCleanCode := oauthCodeRegex.MatchString(val)
		uCheck, uErr := url.Parse(val)
		isValidURL := uErr == nil && (uCheck.Scheme == "https" || uCheck.Scheme == "http") && (uCheck.Query().Get("code") != "" || uCheck.Query().Get("error") != "")
		if !isCleanCode && !isValidURL {
			flow.mu.Unlock()
			http.Error(w, "Unrecognized callback token format", http.StatusBadRequest)
			return
		}
		flow.stdinUsed = true
		flow.mu.Unlock()

		_, _ = flow.stdin.Write([]byte(val + "\n"))
		_ = flow.stdin.Close()
	}

	select {
	case <-flow.doneCh:
		if flow.exitCode == 0 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "authenticated", "completed": true})
			return
		}
		http.Error(w, "Authentication subprocess failed", http.StatusBadRequest)
		return
	case <-time.After(8 * time.Second):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "pending", "completed": false})
		return
	case <-r.Context().Done():
		return
	}
}
