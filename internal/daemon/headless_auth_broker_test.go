package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateAuthURL(t *testing.T) {
	good := "https://accounts.google.com/o/oauth2/v2/auth?client_id=123&redirect_uri=http%3A%2F%2F127.0.0.1%3A8085%2F"
	u, err := validateAuthURL(good)
	if err != nil || u == nil || u.Scheme != "https" {
		t.Fatalf("expected valid https url, got: %v (err=%v)", u, err)
	}

	bads := []string{
		"http://accounts.google.com/auth",
		"javascript:alert(1)",
		"https://user:pass@google.com/auth",
		"https://",
		"https://google.com/auth?q=hello world",
		"https://google.com/auth\x00extra",
		strings.Repeat("a", 4097),
	}
	for _, bad := range bads {
		if _, err := validateAuthURL(bad); err == nil {
			t.Errorf("expected error for url %q, got nil", bad)
		}
	}
}

func TestResolveAllowlistedAuthCommand(t *testing.T) {
	tmpDir := t.TempDir()
	home := tmpDir
	geminiDir := filepath.Join(home, ".gemini")
	scriptsDir := filepath.Join(geminiDir, "config", "plugins", "gslides", "skills", "gslides", "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptsDir, "gslides")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	sess := &Session{ID: "claude-code:local:test", Agent: "claude-code", Cwd: tmpDir}
	argv, _, acceptsStdin, err := resolveAllowlistedAuthCommand(sess, "plugin:gslides", nil, home)
	if err != nil {
		t.Fatalf("failed to resolve gslides command: %v", err)
	}
	if len(argv) != 3 || argv[1] != "auth" || argv[2] != "login" || !acceptsStdin {
		t.Errorf("unexpected argv: %v", argv)
	}

	if _, _, _, err := resolveAllowlistedAuthCommand(sess, "plugin:unknown", nil, home); err == nil {
		t.Errorf("expected error for unknown plugin, got nil")
	}

	if _, _, _, err := resolveAllowlistedAuthCommand(sess, "sh -c rm -rf /", nil, home); err == nil {
		t.Errorf("expected error for arbitrary command, got nil")
	}
}

func TestAuthBroker_ToolAndCallbackLoopbackFlow(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(db)

	sess := &Session{
		ID:        "claude-code:local:flow-1",
		Agent:     "claude-code",
		Host:      s.HostName(),
		State:     StateBlocked,
		Blocked:   &Blocked{Kind: BlockAuth, AuthTarget: "plugin:gslides", AuthServer: "gslides"},
		StartedAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatal(err)
	}

	receivedCallback := false
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	var activeFlowRef *AuthFlow
	httpSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("code") == "test-oauth-code" {
				receivedCallback = true
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("OK"))
				if activeFlowRef != nil && activeFlowRef.stdin != nil {
					_, _ = activeFlowRef.stdin.Write([]byte("done\n"))
				}
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
		}),
	}
	go func() { _ = httpSrv.Serve(ln) }()
	defer func() { _ = httpSrv.Shutdown(context.Background()) }()

	scriptDir := t.TempDir()
	s.authBroker.authURLTimeout = 2 * time.Second
	s.authBroker.commandResolver = func(sess *Session, target string, db *DB, home string) ([]string, string, bool, error) {
		script := fmt.Sprintf(`#!/bin/sh
echo "Waiting for browser authorization on http://127.0.0.1:%d/ ..."
echo "https://accounts.google.com/o/oauth2/v2/auth?redirect_uri=http%%3A%%2F%%2F127.0.0.1%%3A%d%%2F"
read line
exit 0
`, port, port)
		tmp := filepath.Join(scriptDir, "fake-auth.sh")
		if err := os.WriteFile(tmp, []byte(script), 0755); err != nil {
			return nil, "", false, err
		}
		return []string{"/bin/sh", tmp}, "", true, nil
	}

	toolBody, _ := json.Marshal(map[string]string{
		"session_id": sess.ID,
		"target":     "plugin:gslides",
	})
	reqTool := httptest.NewRequest(http.MethodPost, "/v1/sessions/auth-tool", bytes.NewReader(toolBody))
	wTool := httptest.NewRecorder()
	s.handleSessionAuthTool(wTool, reqTool)

	if wTool.Code != http.StatusOK {
		t.Fatalf("auth-tool failed (%d): %s", wTool.Code, wTool.Body.String())
	}

	var toolResp struct {
		Status       string `json:"status"`
		FlowID       string `json:"flow_id"`
		AuthURL      string `json:"auth_url"`
		RecordedPort int    `json:"recorded_port"`
	}
	if err := json.Unmarshal(wTool.Body.Bytes(), &toolResp); err != nil {
		t.Fatal(err)
	}
	if toolResp.RecordedPort != port || toolResp.FlowID == "" {
		t.Fatalf("unexpected toolResp: %+v", toolResp)
	}

	s.authBroker.mu.Lock()
	activeFlowRef = s.authBroker.activeFlows[sess.ID]
	s.authBroker.mu.Unlock()

	updatedSess, _ := db.GetSession(sess.ID)
	if updatedSess.Blocked == nil || updatedSess.Blocked.FlowID != toolResp.FlowID {
		t.Errorf("flow_id not persisted to session: %+v", updatedSess.Blocked)
	}

	cbBody, _ := json.Marshal(map[string]string{
		"session_id":   sess.ID,
		"flow_id":      toolResp.FlowID,
		"callback_url": fmt.Sprintf("http://127.0.0.1:%d/?code=test-oauth-code", port),
	})
	reqCb := httptest.NewRequest(http.MethodPost, "/v1/sessions/auth-callback", bytes.NewReader(cbBody))
	wCb := httptest.NewRecorder()
	s.handleSessionAuthCallback(wCb, reqCb)

	if wCb.Code != http.StatusOK {
		t.Fatalf("auth-callback failed (%d): %s", wCb.Code, wCb.Body.String())
	}
	if !receivedCallback {
		t.Errorf("loopback server did not receive forwarded callback")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := db.GetSession(sess.ID)
		if cur != nil && cur.Blocked == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	finalSess, _ := db.GetSession(sess.ID)
	if finalSess.Blocked != nil {
		t.Errorf("session still blocked after successful auth: %+v", finalSess.Blocked)
	}
	if finalSess.State != StateIdle && finalSess.State != StateWorking {
		t.Errorf("expected StateIdle or StateWorking, got %v", finalSess.State)
	}
}

func TestAuthBroker_StdinCallbackFlow(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(db)

	sess := &Session{
		ID:        "antigravity:local:flow-stdin",
		Agent:     "antigravity",
		Host:      s.HostName(),
		State:     StateBlocked,
		Blocked:   &Blocked{Kind: BlockAuth, AuthTarget: "agent:agy", AuthServer: "antigravity"},
		StartedAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatal(err)
	}

	scriptDir := t.TempDir()
	s.authBroker.authURLTimeout = 2 * time.Second
	s.authBroker.commandResolver = func(sess *Session, target string, db *DB, home string) ([]string, string, bool, error) {
		script := `#!/bin/sh
echo "https://accounts.google.com/o/oauth2/auth?redirect_uri=https%3A%2F%2Fantigravity.google%2Foauth-callback"
read line
if [ "$line" = "valid-oauth-code-123" ]; then
  exit 0
else
  exit 1
fi
`
		tmp := filepath.Join(scriptDir, "fake-agy.sh")
		if err := os.WriteFile(tmp, []byte(script), 0755); err != nil {
			return nil, "", false, err
		}
		return []string{"/bin/sh", tmp}, "", true, nil
	}

	toolBody, _ := json.Marshal(map[string]string{
		"session_id": sess.ID,
		"target":     "agent:agy",
	})
	reqTool := httptest.NewRequest(http.MethodPost, "/v1/sessions/auth-tool", bytes.NewReader(toolBody))
	wTool := httptest.NewRecorder()
	s.handleSessionAuthTool(wTool, reqTool)

	if wTool.Code != http.StatusOK {
		t.Fatalf("auth-tool failed (%d): %s", wTool.Code, wTool.Body.String())
	}

	var toolResp struct {
		FlowID       string `json:"flow_id"`
		AuthURL      string `json:"auth_url"`
		RecordedPort int    `json:"recorded_port"`
	}
	_ = json.Unmarshal(wTool.Body.Bytes(), &toolResp)
	if toolResp.RecordedPort != 0 {
		t.Errorf("expected recorded_port 0 for agy acp --login, got %d", toolResp.RecordedPort)
	}

	cbBody, _ := json.Marshal(map[string]string{
		"session_id": sess.ID,
		"flow_id":    toolResp.FlowID,
		"code":       "valid-oauth-code-123",
	})
	reqCb := httptest.NewRequest(http.MethodPost, "/v1/sessions/auth-callback", bytes.NewReader(cbBody))
	wCb := httptest.NewRecorder()
	s.handleSessionAuthCallback(wCb, reqCb)

	if wCb.Code != http.StatusOK {
		t.Fatalf("auth-callback failed (%d): %s", wCb.Code, wCb.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := db.GetSession(sess.ID)
		if cur != nil && cur.Blocked == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	finalSess, _ := db.GetSession(sess.ID)
	if finalSess.Blocked != nil || (finalSess.State != StateIdle && finalSess.State != StateWorking) {
		t.Errorf("expected session unblocked into StateIdle or StateWorking, got: %+v", finalSess)
	}
}

func TestAuthBroker_RespondRetryAndDismiss(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(db)

	sess := &Session{
		ID:        "antigravity:local:auth-resp",
		Agent:     "antigravity",
		Host:      s.HostName(),
		State:     StateBlocked,
		Blocked:   &Blocked{Kind: BlockAuth, AuthTarget: "plugin:gdocs", AuthServer: "gdocs"},
		StartedAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatal(err)
	}
	s.headless.SetQueuePaused(sess.ID, true)

	// 1. Dismiss
	dismissBody, _ := json.Marshal(map[string]string{
		"sessionId": sess.ID,
		"action":    "dismiss",
	})
	reqDismiss := httptest.NewRequest(http.MethodPost, "/v1/sessions/respond", bytes.NewReader(dismissBody))
	wDismiss := httptest.NewRecorder()
	s.handleRespond(wDismiss, reqDismiss)

	if wDismiss.Code != http.StatusOK {
		t.Fatalf("dismiss failed (%d): %s", wDismiss.Code, wDismiss.Body.String())
	}
	cur, _ := db.GetSession(sess.ID)
	if cur.Blocked != nil || cur.State != StateIdle {
		t.Errorf("expected unblocked idle after dismiss, got: %+v", cur)
	}
	if s.headless.IsQueuePaused(sess.ID) {
		t.Errorf("expected session queue to be unpaused after dismiss")
	}

	// 2. Retry
	cur.State = StateBlocked
	cur.Blocked = &Blocked{Kind: BlockAuth, AuthTarget: "plugin:gdocs", AuthServer: "gdocs"}
	_ = db.SaveSession(cur)

	retryBody, _ := json.Marshal(map[string]string{
		"sessionId": cur.ID,
		"action":    "retry",
	})
	reqRetry := httptest.NewRequest(http.MethodPost, "/v1/sessions/respond", bytes.NewReader(retryBody))
	wRetry := httptest.NewRecorder()
	s.handleRespond(wRetry, reqRetry)

	if wRetry.Code != http.StatusOK {
		t.Fatalf("retry failed (%d): %s", wRetry.Code, wRetry.Body.String())
	}
	cur2, _ := db.GetSession(sess.ID)
	if cur2.Blocked != nil {
		t.Errorf("expected Blocked cleared after retry, got: %+v", cur2.Blocked)
	}
	time.Sleep(150 * time.Millisecond)
}

func TestAuthBroker_CrossSessionCompletedFlowAndDeadFlowNoSSRF(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(db)

	sess1 := &Session{ID: "antigravity:local:sess1", Agent: "antigravity", Host: s.HostName(), StartedAt: time.Now()}
	sess2 := &Session{ID: "antigravity:local:sess2", Agent: "antigravity", Host: s.HostName(), StartedAt: time.Now()}
	_ = db.SaveSession(sess1)
	_ = db.SaveSession(sess2)

	doneCh := make(chan struct{})
	close(doneCh)
	deadFlow := &AuthFlow{
		ID:           "flow-dead-1",
		SessionID:    sess1.ID,
		RecordedPort: 9999,
		exitCode:     1,
		doneCh:       doneCh,
		startedAt:    time.Now(),
	}
	s.authBroker.completedFlows["flow-dead-1"] = deadFlow

	// 1. Cross-session lookup from sess2 must return 404
	cbCrossBody, _ := json.Marshal(map[string]string{
		"session_id":   sess2.ID,
		"flow_id":      "flow-dead-1",
		"callback_url": "http://127.0.0.1:9999/?code=abc",
	})
	reqCross := httptest.NewRequest(http.MethodPost, "/v1/sessions/auth-callback", bytes.NewReader(cbCrossBody))
	wCross := httptest.NewRecorder()
	s.handleSessionAuthCallback(wCross, reqCross)
	if wCross.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-session flow lookup, got %d", wCross.Code)
	}

	// 2. Dead flow with non-zero exit code on owning session must return 400 without dialing RecordedPort
	cbDeadBody, _ := json.Marshal(map[string]string{
		"session_id":   sess1.ID,
		"flow_id":      "flow-dead-1",
		"callback_url": "http://127.0.0.1:9999/?code=abc",
	})
	reqDead := httptest.NewRequest(http.MethodPost, "/v1/sessions/auth-callback", bytes.NewReader(cbDeadBody))
	wDead := httptest.NewRecorder()
	s.handleSessionAuthCallback(wDead, reqDead)
	if wDead.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for dead flow, got %d: %s", wDead.Code, wDead.Body.String())
	}
}
