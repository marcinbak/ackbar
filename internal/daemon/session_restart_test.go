package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/tmux"
)

type testClaudeProvider struct {
	MockProvider
}

func (p *testClaudeProvider) Agent() string { return "claude-code" }
func (p *testClaudeProvider) GetSpawnCommand(tempUUID string) string {
	return fmt.Sprintf("claude --session-id %s", tempUUID)
}
func (p *testClaudeProvider) GetResumeCommand(nativeID string) string {
	return fmt.Sprintf("claude --resume %s", nativeID)
}
func (p *testClaudeProvider) ParseHook(eventName string, payload []byte) (*Event, error) {
	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	sessionID, _ := data["session_id"].(string)
	cwd, _ := data["cwd"].(string)
	state := StateWorking
	if eventName == "SessionEnd" {
		state = StateEnded
	}
	return &Event{
		Agent:       "claude-code",
		NativeID:    sessionID,
		Cwd:         cwd,
		EventName:   eventName,
		State:       state,
		LastEventAt: time.Now(),
	}, nil
}

func TestHeadlessRestart_KeepsWorkerProcessAliveAndSwapsChildPID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	argsLog := filepath.Join(tmpDir, "claude_invocations.log")
	mcpFile := filepath.Join(tmpDir, ".mcp.json")
	_ = os.WriteFile(mcpFile, []byte(`{"mcpServers":{"linear":{"url":"https://mcp.linear.app/sse"}}}`), 0644)

	mockClaude := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
mcp_marker="none"
if [ -f %q ]; then
  mcp_marker=$(wc -c < %q | tr -d ' ')
fi
echo "$$|$mcp_marker|$*" >> %q
while IFS= read -r line; do
  printf '{"type":"result","result":"pid=%%d mcp=%%s","is_error":false}\n' "$$" "$mcp_marker"
done
`, mcpFile, mcpFile, argsLog)
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("write mock claude: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "restart_headless.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	srv := NewServer(db)
	srv.RegisterProvider(&testClaudeProvider{})
	srv.homeDir = tmpDir
	defer srv.headless.ShutdownAllWorkers(time.Second)

	nativeUUID := "11111111-2222-3333-4444-666666666666"
	sess := &Session{
		ID:         "claude-code:local:" + nativeUUID,
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   nativeUUID,
		Cwd:        tmpDir,
		Managed:    true,
		EngineType: EngineHeadless,
		State:      StateIdle,
		StartedAt:  time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	sub1, unsub1 := srv.headless.Subscribe(sess.ID)
	done1 := waitTurnDone(srv.headless, sess.ID, sub1)
	if err := srv.headless.RunTurn(context.Background(), sess, "turn 1 before mcp auth"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for turn 1")
	}
	unsub1()

	srv.headless.mu.RLock()
	hBefore := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if hBefore == nil || hBefore.WorkerPID <= 1 {
		t.Fatalf("expected warm worker after turn 1, got %+v", hBefore)
	}
	statusBefore, err := callWorkerSocket(hBefore.SocketPath, workerIPCReq{Op: "status"}, 500*time.Millisecond)
	if err != nil || statusBefore == nil || !statusBefore.OK {
		t.Fatalf("worker status before restart failed: %v %+v", err, statusBefore)
	}
	workerPIDBefore := statusBefore.WorkerPID
	childPIDBefore := statusBefore.ChildPID

	// Simulate transcript written on disk during turn 1 + MCP connector authenticated/updated externally.
	projDir := filepath.Join(tmpDir, ".claude", "projects", encodeClaudeProjectDir(tmpDir))
	_ = os.MkdirAll(projDir, 0755)
	_ = os.WriteFile(filepath.Join(projDir, nativeUUID+".jsonl"), []byte(`{"type":"user"}`+"\n"), 0644)
	_ = os.WriteFile(mcpFile, []byte(`{"mcpServers":{"linear":{"url":"https://mcp.linear.app/sse"},"stripe":{"url":"https://mcp.stripe.com/http"}}}`), 0644)

	// Seed stale MCP cache and stale Blocked state to verify restart clears both.
	mcpStatusCacheMu.Lock()
	mcpStatusCache[sess.ID] = &mcpStatusCacheEntry{
		cachedAt: time.Now(),
		data:     &SessionMCPStatusResponse{},
	}
	mcpStatusCacheMu.Unlock()
	sess.State = StateBlocked
	sess.Blocked = &Blocked{Kind: BlockAuth, Reason: "MCP needs auth", Since: time.Now()}
	_ = db.SaveSession(sess)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id="+sess.ID+"&action=restart", nil)
	rec := httptest.NewRecorder()
	srv.handleSessionControl(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restart headless returned %d: %s", rec.Code, rec.Body.String())
	}

	var respPayload struct {
		Status           string   `json:"status"`
		EngineType       string   `json:"engine_type"`
		RestartedInPlace bool     `json:"restarted_in_place"`
		Session          *Session `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &respPayload); err != nil {
		t.Fatalf("unmarshal restart response: %v", err)
	}
	if !respPayload.RestartedInPlace {
		t.Fatalf("expected restarted_in_place=true, got false (body=%s)", rec.Body.String())
	}
	if respPayload.EngineType != EngineHeadless {
		t.Fatalf("expected engine_type=%q, got %q", EngineHeadless, respPayload.EngineType)
	}

	afterSess, _ := db.GetSession(sess.ID)
	if afterSess == nil {
		t.Fatalf("session missing after restart")
	}
	if afterSess.EngineType != EngineHeadless || afterSess.TmuxName != "" {
		t.Fatalf("headless session corrupted into tmux: %+v", afterSess)
	}
	if afterSess.State != StateIdle || afterSess.Blocked != nil {
		t.Fatalf("expected StateIdle and nil Blocked after headless restart, got state=%s blocked=%+v", afterSess.State, afterSess.Blocked)
	}

	mcpStatusCacheMu.RLock()
	_, cacheStillPresent := mcpStatusCache[sess.ID]
	mcpStatusCacheMu.RUnlock()
	if cacheStillPresent {
		t.Errorf("expected mcpStatusCache entry invalidated on session restart")
	}

	statusAfter, err := callWorkerSocket(hBefore.SocketPath, workerIPCReq{Op: "status"}, 500*time.Millisecond)
	if err != nil || statusAfter == nil || !statusAfter.OK {
		t.Fatalf("worker socket should remain alive across restart_child, got err=%v resp=%+v", err, statusAfter)
	}
	if statusAfter.WorkerPID != workerPIDBefore {
		t.Errorf("expected outer headless worker PID unchanged (%d), got %d", workerPIDBefore, statusAfter.WorkerPID)
	}
	if statusAfter.ChildPID == childPIDBefore || statusAfter.ChildPID <= 1 {
		t.Errorf("expected inner CLI child PID to swap (before=%d, after=%d)", childPIDBefore, statusAfter.ChildPID)
	}

	// Verify second turn runs on the pre-warmed new child PID without spawning another process.
	sub2, unsub2 := srv.headless.Subscribe(sess.ID)
	var resultText string
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		for evt := range sub2 {
			if evt.Type == "turn_complete" {
				resultText = evt.Text
				return
			}
		}
	}()
	if err := srv.headless.RunTurn(context.Background(), afterSess, "turn 2 after mcp auth"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for turn 2")
	}
	unsub2()

	expectedPrefix := fmt.Sprintf("pid=%d ", statusAfter.ChildPID)
	if !strings.HasPrefix(resultText, expectedPrefix) {
		t.Errorf("expected turn 2 to execute on pre-warmed child PID %d, got %q", statusAfter.ChildPID, resultText)
	}

	invocations, _ := os.ReadFile(argsLog)
	lines := strings.Split(strings.TrimSpace(string(invocations)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 CLI child spawns (initial + in-place restart), got %d:\n%s", len(lines), string(invocations))
	}
	if !strings.Contains(lines[0], "--session-id "+nativeUUID) {
		t.Errorf("expected first spawn to use --session-id, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "--resume "+nativeUUID) {
		t.Errorf("expected restarted child to switch to --resume, got %q", lines[1])
	}
}

func TestHeadlessRestart_MidTurnCancelsActiveTurnAndPreservesWorker(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	startedMarker := filepath.Join(tmpDir, "turn_started")

	mockClaude := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *hang_mid_turn*)
      touch %q
      sleep 30
      ;;
    *)
      printf '{"type":"result","result":"pid=%%d ok","is_error":false}\n' "$$"
      ;;
  esac
done
`, startedMarker)
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "midturn_restart.db"))
	srv := NewServer(db)
	srv.RegisterProvider(&testClaudeProvider{})
	srv.homeDir = tmpDir
	defer srv.headless.ShutdownAllWorkers(time.Second)

	nativeUUID := "55555555-6666-7777-8888-999999999999"
	sess := &Session{
		ID: "claude-code:local:" + nativeUUID, Agent: "claude-code", Host: "local",
		NativeID: nativeUUID, Cwd: tmpDir, Managed: true, EngineType: EngineHeadless, State: StateIdle,
	}
	_ = db.SaveSession(sess)

	if err := srv.headless.RunTurn(context.Background(), sess, "hang_mid_turn"); err != nil {
		t.Fatalf("RunTurn mid-turn: %v", err)
	}
	for d := time.Now().Add(3 * time.Second); time.Now().Before(d); {
		if _, err := os.Stat(startedMarker); err == nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	srv.headless.mu.RLock()
	hBefore := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	stBefore, err := callWorkerSocket(hBefore.SocketPath, workerIPCReq{Op: "status"}, 500*time.Millisecond)
	if err != nil || stBefore == nil || !stBefore.OK {
		t.Fatalf("worker status mid-turn failed: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.handleSessionControl(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id="+sess.ID+"&action=restart", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("mid-turn restart failed: %d %s", rec.Code, rec.Body.String())
	}

	// Give monitorWorkerTurn goroutine time to finalize old turn spool and verify it didn't clobber warmWorkers.
	time.Sleep(80 * time.Millisecond)

	srv.headless.mu.RLock()
	hAfter := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if hAfter == nil {
		t.Fatalf("expected warmWorkers entry preserved after mid-turn restart_child")
	}
	stAfter, err := callWorkerSocket(hAfter.SocketPath, workerIPCReq{Op: "status"}, 500*time.Millisecond)
	if err != nil || stAfter == nil || !stAfter.OK {
		t.Fatalf("worker socket dead after mid-turn restart: %v", err)
	}
	if stAfter.WorkerPID != stBefore.WorkerPID || stAfter.ChildPID == stBefore.ChildPID {
		t.Fatalf("expected same WorkerPID (%d==%d) and new ChildPID (%d!=%d)", stBefore.WorkerPID, stAfter.WorkerPID, stBefore.ChildPID, stAfter.ChildPID)
	}

	afterSess, _ := db.GetSession(sess.ID)
	sub, unsub := srv.headless.Subscribe(sess.ID)
	done := waitTurnDone(srv.headless, sess.ID, sub)
	if err := srv.headless.RunTurn(context.Background(), afterSess, "fast_turn"); err != nil {
		t.Fatalf("RunTurn after mid-turn restart failed: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for fast_turn after mid-turn restart")
	}
	unsub()
}

func TestHeadlessAutoReload_MCPConfigFingerprintSwapsChildWithoutWorkerRestart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mcpFile := filepath.Join(tmpDir, ".mcp.json")
	_ = os.WriteFile(mcpFile, []byte(`{"mcpServers":{}}`), 0644)

	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  echo '{"type":"result","result":"ok","is_error":false}'
done
`
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "mcp_fp.db"))
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	nativeUUID := "22222222-3333-4444-5555-666666666666"
	sess := &Session{
		ID: "claude-code:local:" + nativeUUID, Agent: "claude-code", Host: "local",
		NativeID: nativeUUID, Cwd: tmpDir, EngineType: EngineHeadless,
	}
	_ = db.SaveSession(sess)

	sub1, unsub1 := h.Subscribe(sess.ID)
	done1 := waitTurnDone(h, sess.ID, sub1)
	if err := h.RunTurn(context.Background(), sess, "turn 1"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	<-done1
	unsub1()
	sess.FirstPrompt = "turn 1"

	projDir := filepath.Join(tmpDir, ".claude", "projects", encodeClaudeProjectDir(tmpDir))
	_ = os.MkdirAll(projDir, 0755)
	_ = os.WriteFile(filepath.Join(projDir, nativeUUID+".jsonl"), []byte(`{"type":"user"}`+"\n"), 0644)

	h.mu.RLock()
	handle := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	st1, _ := callWorkerSocket(handle.SocketPath, workerIPCReq{Op: "status"}, 500*time.Millisecond)

	// Modify .mcp.json externally without calling action=restart.
	time.Sleep(5 * time.Millisecond)
	_ = os.WriteFile(mcpFile, []byte(`{"mcpServers":{"new-connector":{"url":"https://example.com/mcp"}}}`), 0644)

	sub2, unsub2 := h.Subscribe(sess.ID)
	done2 := waitTurnDone(h, sess.ID, sub2)
	if err := h.RunTurn(context.Background(), sess, "turn 2"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}
	<-done2
	unsub2()

	st2, err := callWorkerSocket(handle.SocketPath, workerIPCReq{Op: "status"}, 500*time.Millisecond)
	if err != nil || st2 == nil || !st2.OK {
		t.Fatalf("expected same worker socket alive after MCP fingerprint auto-reload: %v", err)
	}
	if st2.WorkerPID != st1.WorkerPID {
		t.Errorf("expected same WorkerPID (%d), got %d", st1.WorkerPID, st2.WorkerPID)
	}
	if st2.ChildPID == st1.ChildPID {
		t.Errorf("expected ChildPID to swap automatically after .mcp.json update, stayed %d", st2.ChildPID)
	}
}

func TestHeadlessResume_PreservesHeadlessModeWithoutTmux(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := InitDB(filepath.Join(tmpDir, "resume_headless.db"))
	srv := NewServer(db)
	srv.RegisterProvider(&testClaudeProvider{})

	sess := &Session{
		ID:         "claude-code:local:33333333-4444-5555-6666-777777777777",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "33333333-4444-5555-6666-777777777777",
		Cwd:        tmpDir,
		Managed:    true,
		EngineType: EngineHeadless,
		State:      StateEnded,
		IsDone:     true,
	}
	_ = db.SaveSession(sess)

	rec := httptest.NewRecorder()
	srv.handleSessionControl(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id="+sess.ID+"&action=resume", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("resume headless failed: %d %s", rec.Code, rec.Body.String())
	}

	got, _ := db.GetSession(sess.ID)
	if got.EngineType != EngineHeadless || got.TmuxName != "" || got.IsDone || got.State != StateIdle {
		t.Fatalf("expected headless session resumed cleanly without tmux, got %+v", got)
	}
}

func TestTmuxRestart_InPlacePaneRespawnEnvAndPreTurn1Fallback(t *testing.T) {
	if !tmux.IsTmuxInstalled() {
		t.Skip("tmux not installed on host, skipping test")
	}
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)

	profileDir := filepath.Join(tmpDir, "claude-work-profile")
	_ = os.MkdirAll(profileDir, 0755)
	invocationFile := filepath.Join(tmpDir, "tmux_claude.log")

	mockClaude := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
printf 'args=%%s|cfg=%%s|custom=%%s|sess=%%s|sup=%%s|task=%%s\n' "$*" "$CLAUDE_CONFIG_DIR" "$MCP_EXTRA_TOKEN" "$ACKBAR_SESSION_ID" "$ACKBAR_IS_SUPERVISOR" "$ACKBAR_TASK_ID" >> %q
sleep 30
`, invocationFile)
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "tmux_restart.db"))
	_ = db.SaveAccount(&AgentAccount{
		ID:        "acc-work",
		Agent:     "claude-code",
		Name:      "Work Profile",
		ConfigDir: profileDir,
		Env: map[string]string{
			"MCP_EXTRA_TOKEN": "tok_secret_123",
			"PATH":            binDir + ":" + os.Getenv("PATH"),
		},
	})
	clProvider := &testClaudeProvider{}
	srv := NewServer(db)
	srv.RegisterProvider(clProvider)
	srv.hostName = "local"
	srv.homeDir = tmpDir

	nativeUUID := "44444444-5555-6666-7777-888888888888"
	tmuxName := "ackbar-claude-code-" + nativeUUID
	_ = tmux.Kill(context.Background(), tmuxName)
	defer tmux.Kill(context.Background(), tmuxName)

	if err := tmux.SpawnWithEnv(context.Background(), tmuxName, tmpDir, "sleep 30", map[string]string{"PATH": os.Getenv("PATH")}); err != nil {
		if strings.Contains(err.Error(), "Operation not permitted") {
			t.Skip("tmux socket creation blocked by sandbox environment")
		}
		t.Fatalf("initial tmux.SpawnWithEnv: %v", err)
	}
	pidInitial, _ := tmux.GetPID(context.Background(), tmuxName)

	sess := &Session{
		ID:            "claude-code:local:" + nativeUUID,
		Agent:         "claude-code",
		Host:          "local",
		NativeID:      nativeUUID,
		Cwd:           tmpDir,
		Managed:       true,
		EngineType:    EngineTmux,
		TmuxName:      tmuxName,
		State:         StateUnknown,
		AccountID:     "acc-work",
		IsSupervisor:  true,
		CurrentTaskID: "task_abc",
		StartedAt:     time.Now(),
	}
	_ = db.SaveSession(sess)

	// 1. Pre-turn-1 restart (no .jsonl on disk yet) -> must use --session-id, keep tmux alive, and inject full env.
	rec1 := httptest.NewRecorder()
	srv.handleSessionControl(rec1, httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id="+sess.ID+"&action=restart", nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("pre-turn-1 tmux restart failed: %d %s", rec1.Code, rec1.Body.String())
	}
	if !tmux.HasSession(context.Background(), tmuxName) {
		t.Fatalf("expected tmux session %s to stay alive", tmuxName)
	}
	pidAfter1, _ := tmux.GetPID(context.Background(), tmuxName)
	if pidAfter1 == pidInitial {
		t.Errorf("expected tmux pane PID to swap on restart, stayed %d", pidAfter1)
	}
	deadline1 := time.Now().Add(3 * time.Second)
	var preTurnLog string
	for time.Now().Before(deadline1) {
		if b, rErr := os.ReadFile(invocationFile); rErr == nil && strings.Contains(string(b), "--session-id "+nativeUUID) {
			preTurnLog = string(b)
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(preTurnLog, "args=--session-id "+nativeUUID) {
		t.Fatalf("expected pre-turn-1 restart to fall back to '--session-id %s', got log:\n%s", nativeUUID, preTurnLog)
	}

	// Verify late SessionEnd hook from dying CLI process is suppressed during restart grace window.
	endPayload, _ := json.Marshal(map[string]string{
		"session_id":      nativeUUID,
		"cwd":             tmpDir,
		"hook_event_name": "SessionEnd",
	})
	srv.processHookEvent(clProvider, "SessionEnd", "local", endPayload)
	afterHookSess, _ := db.GetSession(sess.ID)
	if afterHookSess.State == StateEnded {
		t.Fatalf("expected late SessionEnd hook during tmux in-place restart to be suppressed, got StateEnded")
	}

	// 2. Write conversation file in the custom Account ConfigDir and inject binDir via Account Env, then restart again -> must switch to --resume!
	_ = db.SaveAccount(&AgentAccount{
		ID:        "acc-work",
		Agent:     "claude-code",
		Name:      "Work Profile",
		ConfigDir: profileDir,
		Env: map[string]string{
			"MCP_EXTRA_TOKEN": "tok_secret_123",
			"PATH":            binDir + ":" + os.Getenv("PATH"),
		},
	})
	acctProjDir := filepath.Join(profileDir, "projects", encodeClaudeProjectDir(tmpDir))
	_ = os.MkdirAll(acctProjDir, 0755)
	_ = os.WriteFile(filepath.Join(acctProjDir, nativeUUID+".jsonl"), []byte(`{"type":"user"}`+"\n"), 0644)

	rec2 := httptest.NewRecorder()
	srv.handleSessionControl(rec2, httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id="+sess.ID+"&action=restart", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("post-turn-1 tmux restart failed: %d %s", rec2.Code, rec2.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	var logData string
	for time.Now().Before(deadline) {
		if b, rErr := os.ReadFile(invocationFile); rErr == nil && strings.Contains(string(b), "--resume "+nativeUUID) {
			logData = string(b)
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(logData, "args=--resume "+nativeUUID) {
		t.Fatalf("expected post-turn-1 restart to run 'claude --resume %s', got log:\n%s", nativeUUID, logData)
	}
	if !strings.Contains(logData, "cfg="+profileDir) ||
		!strings.Contains(logData, "custom=tok_secret_123") ||
		!strings.Contains(logData, "sess="+sess.ID) ||
		!strings.Contains(logData, "sup=1") ||
		!strings.Contains(logData, "task=task_abc") {
		t.Fatalf("expected full account + session env in respawned tmux pane, got:\n%s", logData)
	}
}
