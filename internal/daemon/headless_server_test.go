package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/tmux"
)

func setupTestServer(t *testing.T) (*Server, *DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "server_test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	srv := NewServer(db)
	srv.RegisterProvider(&MockProvider{})
	return srv, db
}

func TestServer_SpawnHeadlessSession(t *testing.T) {
	srv, db := setupTestServer(t)

	payload := map[string]string{
		"agent":       "claude-code",
		"cwd":         "/tmp",
		"name":        "My Headless Task",
		"engine_type": EngineHeadless,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["status"] != "spawned" || resp["engine_type"] != EngineHeadless {
		t.Errorf("Unexpected response: %+v", resp)
	}

	sessionID, _ := resp["session_id"].(string)
	fullID := "claude-code:local:" + sessionID

	sess, err := db.GetSession(fullID)
	if err != nil || sess == nil {
		t.Fatalf("Expected session in db, err: %v", err)
	}

	if sess.EngineType != EngineHeadless {
		t.Errorf("Expected EngineType == 'headless', got: %s", sess.EngineType)
	}
	if sess.CustomTitle != "My Headless Task" {
		t.Errorf("Expected CustomTitle == 'My Headless Task', got: %s", sess.CustomTitle)
	}
	if sess.State != StateIdle {
		t.Errorf("Expected State == StateIdle, got: %v", sess.State)
	}
}

func TestServer_PromptAndChatStream(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:local:uuid-test-prompt",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-test-prompt",
		Cwd:         "/tmp",
		State:       StateIdle,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	// Test chat stream connection
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamReq := httptest.NewRequest(http.MethodGet, "/v1/sessions/chat/stream?session_id="+sess.ID, nil).WithContext(ctx)
	streamRecorder := httptest.NewRecorder()

	doneCh := make(chan bool)
	go func() {
		srv.Mux().ServeHTTP(streamRecorder, streamReq)
		doneCh <- true
	}()

	// Wait a moment for connection
	time.Sleep(50 * time.Millisecond)

	// Emit a mock event via headless runner
	srv.headless.Emit(sess.ID, ChatStreamEvent{
		SessionID: sess.ID,
		Type:      "text_delta",
		Text:      "Streaming test chunk",
	})

	time.Sleep(50 * time.Millisecond)
	cancel() // Close stream

	select {
	case <-doneCh:
	case <-time.After(1 * time.Second):
		t.Fatalf("Stream did not close after context cancel")
	}

	bodyStr := streamRecorder.Body.String()
	if !strings.Contains(bodyStr, "connected") {
		t.Errorf("Expected stream to contain initial connected event, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "Streaming test chunk") {
		t.Errorf("Expected stream to contain emitted chunk, got: %s", bodyStr)
	}
}

func TestServer_CancelTurn(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:local:uuid-test-cancel",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-test-cancel",
		Cwd:         "/tmp",
		State:       StateWorking,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	payload := map[string]string{
		"session_id": sess.ID,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/cancel", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "cancelled" {
		t.Errorf("Expected status == 'cancelled', got: %s", resp["status"])
	}

	updated, _ := db.GetSession(sess.ID)
	if updated.State != StateIdle {
		t.Errorf("Expected session to be reset to StateIdle, got: %v", updated.State)
	}
}

func TestServer_TakeWheel(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:local:uuid-test-takewheel",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-test-takewheel",
		Cwd:         t.TempDir(),
		State:       StateIdle,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	payload := map[string]string{
		"session_id": sess.ID,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/take-wheel", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Mux().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["status"] != "ok" {
			t.Errorf("Expected status == 'ok', got: %s", resp["status"])
		}

		updated, _ := db.GetSession(sess.ID)
		if updated.EngineType != EngineTmux {
			t.Errorf("Expected EngineType to be updated to tmux, got: %s", updated.EngineType)
		}
		if updated.TmuxName == "" {
			t.Errorf("Expected TmuxName to be set")
		}
		_ = tmux.Kill(context.Background(), updated.TmuxName)
	}
}

func TestServer_TakeWheel_AliasResolution(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:macbook:uuid-alias-test",
		Agent:       "claude-code",
		Host:        "macbook",
		NativeID:    "uuid-alias-test",
		Cwd:         t.TempDir(),
		State:       StateIdle,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	// Call take-wheel using "local" host alias instead of "macbook"
	payload := map[string]string{
		"session_id": "claude-code:local:uuid-alias-test",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/take-wheel", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Mux().ServeHTTP(w, req)
	defer func() {
		_ = tmux.Kill(context.Background(), "ackbar-claude-code-uuid-alias-test")
	}()

	// In test environment without real tmux, it should not fail with 404
	if w.Code == http.StatusNotFound {
		t.Fatalf("Expected session to be found via alias resolution, got 404: %s", w.Body.String())
	}
}

func TestServer_TakeWheel_Idempotent(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:macbook:uuid-idempotent-test",
		Agent:       "claude-code",
		Host:        "macbook",
		NativeID:    "uuid-idempotent-test",
		Cwd:         t.TempDir(),
		State:       StateIdle,
		EngineType:  EngineTmux,
		TmuxName:    "ackbar-claude-code-uuid-idempotent-test",
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	payload := map[string]string{
		"session_id": sess.ID,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/take-wheel", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Mux().ServeHTTP(w, req)
	defer func() {
		_ = tmux.Kill(context.Background(), "ackbar-claude-code-uuid-idempotent-test")
	}()

	// In test environment without tmux running, it tries to spawn or reuse
	// It should never 404 or panic
	if w.Code == http.StatusNotFound {
		t.Fatalf("Expected session to be found, got 404")
	}
}

func TestServer_TakeWheel_CancelsActiveTurn(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:local:uuid-cancel-takewheel",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-cancel-takewheel",
		Cwd:         t.TempDir(),
		State:       StateWorking,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	// Simulate an active headless turn
	srv.headless.mu.Lock()
	srv.headless.processes[sess.ID] = nil
	srv.headless.mu.Unlock()

	payload := map[string]string{
		"session_id": sess.ID,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/take-wheel", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Mux().ServeHTTP(w, req)
	defer func() {
		_ = tmux.Kill(context.Background(), "ackbar-claude-code-uuid-cancel-takewheel")
	}()

	if w.Code == http.StatusConflict {
		t.Fatalf("Expected take-wheel to cancel active turn instead of returning 409 Conflict: %s", w.Body.String())
	}
	if srv.headless.IsRunning(sess.ID) {
		t.Errorf("Expected active headless turn to be stopped by take-wheel")
	}
}

func TestServer_SwitchHeadless(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:local:uuid-switch-headless",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-switch-headless",
		Cwd:         t.TempDir(),
		State:       StateWorking,
		EngineType:  EngineTmux,
		TmuxName:    "ackbar-claude-code-uuid-switch-headless",
		PID:         4242,
		Managed:     true,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	payload := map[string]string{
		"session_id": sess.ID,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/switch-headless", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from switch-headless, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" || resp["engine_type"] != EngineHeadless {
		t.Errorf("Unexpected response: %+v", resp)
	}

	updated, err := db.GetSession(sess.ID)
	if err != nil || updated == nil {
		t.Fatalf("Expected session in DB, got err=%v", err)
	}
	if updated.EngineType != EngineHeadless {
		t.Errorf("Expected EngineType == 'headless', got %q", updated.EngineType)
	}
	if updated.TmuxName != "" {
		t.Errorf("Expected TmuxName == '', got %q", updated.TmuxName)
	}
	if updated.PID != 0 {
		t.Errorf("Expected PID == 0, got %d", updated.PID)
	}
	if updated.State != StateIdle {
		t.Errorf("Expected State == StateIdle, got %v", updated.State)
	}

	// Verify unsupported agent (e.g. codex) is rejected with 400 Bad Request before killing tmux
	unsupportedSess := &Session{
		ID:          "codex:local:uuid-codex-tmux",
		Agent:       "codex",
		Host:        "local",
		NativeID:    "uuid-codex-tmux",
		Cwd:         t.TempDir(),
		State:       StateWorking,
		EngineType:  EngineTmux,
		TmuxName:    "ackbar-codex-uuid-codex-tmux",
		Managed:     true,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(unsupportedSess)

	unsupportedBody, _ := json.Marshal(map[string]string{"session_id": unsupportedSess.ID})
	reqUnsupported := httptest.NewRequest(http.MethodPost, "/v1/sessions/switch-headless", bytes.NewBuffer(unsupportedBody))
	reqUnsupported.Header.Set("Content-Type", "application/json")
	wUnsupported := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wUnsupported, reqUnsupported)

	if wUnsupported.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for unsupported agent, got %d", wUnsupported.Code)
	}
	stillTmux, _ := db.GetSession(unsupportedSess.ID)
	if stillTmux == nil || stillTmux.EngineType != EngineTmux || stillTmux.TmuxName == "" {
		t.Errorf("Expected unsupported session to remain in tmux mode, got %+v", stillTmux)
	}
}

func TestServer_PromptQueueEndpoints(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:local:test-queue-endpoints",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "test-queue-endpoints",
		Cwd:         t.TempDir(),
		State:       StateIdle,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	// 1. First prompt while idle -> status: "running"
	body1, _ := json.Marshal(map[string]string{
		"session_id": sess.ID,
		"prompt":     "Prompt 1",
	})
	req1 := httptest.NewRequest(http.MethodPost, "/v1/sessions/prompt", bytes.NewBuffer(body1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for first prompt, got %d: %s", w1.Code, w1.Body.String())
	}
	var resp1 map[string]string
	_ = json.Unmarshal(w1.Body.Bytes(), &resp1)
	if resp1["status"] != "running" {
		t.Errorf("Expected status running, got %+v", resp1)
	}

	// 2. Mark session as running by registering a dummy process
	srv.headless.mu.Lock()
	srv.headless.processes[sess.ID] = nil
	srv.headless.mu.Unlock()

	// 3. Second prompt while running -> status: "queued"
	body2, _ := json.Marshal(map[string]string{
		"session_id": sess.ID,
		"prompt":     "Queued Prompt 2",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/v1/sessions/prompt", bytes.NewBuffer(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for queued prompt, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp2 map[string]interface{}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2["status"] != "queued" || resp2["id"] == "" {
		t.Errorf("Expected status queued with id, got %+v", resp2)
	}
	queuedID := resp2["id"].(string)

	// 4. GET queue
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/sessions/prompt/queue?session_id="+sess.ID, nil)
	wGet := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for GET queue, got %d: %s", wGet.Code, wGet.Body.String())
	}
	var getResp struct {
		Items []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"items"`
		Paused bool `json:"paused"`
	}
	_ = json.Unmarshal(wGet.Body.Bytes(), &getResp)
	if len(getResp.Items) != 1 || getResp.Items[0].ID != queuedID || getResp.Items[0].Text != "Queued Prompt 2" {
		t.Errorf("Unexpected GET queue response: %+v", getResp)
	}
	if getResp.Paused {
		t.Errorf("Expected queue not paused")
	}

	// 5. DELETE item from queue
	reqDel := httptest.NewRequest(http.MethodDelete, "/v1/sessions/prompt/queue?session_id="+sess.ID+"&item_id="+queuedID, nil)
	wDel := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for DELETE queue item, got %d: %s", wDel.Code, wDel.Body.String())
	}

	// Verify queue is now empty
	itemsAfterDel, _ := srv.headless.GetPromptQueue(sess.ID)
	if len(itemsAfterDel) != 0 {
		t.Errorf("Expected queue to be empty after delete, got %d items", len(itemsAfterDel))
	}

	// 6. Test pause & resume
	srv.headless.EnqueuePrompt(sess.ID, "Prompt to resume")
	srv.headless.SetQueuePaused(sess.ID, true)

	reqResume := httptest.NewRequest(http.MethodPost, "/v1/sessions/prompt/queue/resume?session_id="+sess.ID, nil)
	wResume := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wResume, reqResume)
	if wResume.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for resume, got %d: %s", wResume.Code, wResume.Body.String())
	}
	if srv.headless.IsQueuePaused(sess.ID) {
		t.Errorf("Expected queue to be unpaused after resume")
	}
}

type mockDiscovererProvider struct {
	MockProvider
}

func (m *mockDiscovererProvider) Agent() string { return "claude-code" }
func (m *mockDiscovererProvider) ListSubagents(home, cwd, nativeID string) ([]*ActiveSubagent, error) {
	return nil, nil
}

func TestServer_GetRunningSubagents_ReconciliationPruning(t *testing.T) {
	srv, db := setupTestServer(t)
	srv.RegisterProvider(&mockDiscovererProvider{})

	sess := &Session{
		ID:          "claude-code:local:test-stale-subagent",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "test-stale-subagent",
		Cwd:         t.TempDir(),
		State:       StateWorking,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	// Add an in-memory subagent with StartedAt 10 minutes ago
	srv.subagentsMu.Lock()
	srv.activeSubagents[sess.ID] = []*SubagentInfo{
		{
			ID:        "sub-stale-1",
			Name:      "Stale Worker",
			Role:      "Worker",
			State:     "running",
			StartedAt: time.Now().Add(-10 * time.Minute),
		},
	}
	srv.subagentsMu.Unlock()

	// getRunningSubagents should reconcile against disk, find 0 on disk, and prune the stale in-memory subagent
	running := srv.getRunningSubagents(sess.ID, sess.NativeID, sess.Agent, sess.Cwd)
	if len(running) != 0 {
		t.Errorf("Expected stale subagent to be pruned, got %d running subagents", len(running))
	}

	// Verify in-memory map was pruned
	srv.subagentsMu.RLock()
	inMem := srv.activeSubagents[sess.ID]
	srv.subagentsMu.RUnlock()
	if len(inMem) != 0 {
		t.Errorf("Expected activeSubagents map to be cleaned, got %d entries", len(inMem))
	}
}

func TestInspectClaudeStatus_SessionNotFoundError(t *testing.T) {
	sess := &Session{
		ID:          "claude-code:local:test-sess-not-found",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "test-sess-not-found",
		Cwd:         t.TempDir(),
		State:       StateWorking,
		EngineType:  EngineTmux,
		TmuxName:    "ackbar-claude-code-test-sess-not-found",
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}

	// In test environment without tmux running, InspectClaudeStatus should transition to StateEnded
	changed := InspectClaudeStatus(context.Background(), sess)
	if !changed {
		t.Errorf("Expected InspectClaudeStatus to return changed=true when pane is absent or ended")
	}
	if sess.State != StateEnded {
		t.Errorf("Expected sess.State to be StateEnded, got: %s", sess.State)
	}
}

type mockInFlightTranscriptProvider struct {
	MockProvider
	agent    string
	messages []TranscriptMessage
}

func (m *mockInFlightTranscriptProvider) Agent() string {
	if m.agent != "" {
		return m.agent
	}
	return "claude-code"
}

func (m *mockInFlightTranscriptProvider) ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error) {
	if len(m.messages) == 0 {
		return nil, os.ErrNotExist
	}
	return m.messages, nil
}

func TestServer_HeadlessInFlightPrompt(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:          "claude-code:local:uuid-inflight-turn",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-inflight-turn",
		Cwd:         t.TempDir(),
		State:       StateWorking,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	srv.headless.mu.Lock()
	srv.headless.processes[sess.ID] = &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}
	srv.headless.activePrompts[sess.ID] = TranscriptMessage{
		Role:      "user",
		Content:   "Implement headless in-flight prompts",
		Timestamp: time.Now(),
	}
	srv.headless.mu.Unlock()

	defer func() {
		srv.headless.mu.Lock()
		delete(srv.headless.processes, sess.ID)
		delete(srv.headless.activePrompts, sess.ID)
		srv.headless.mu.Unlock()
	}()

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil)
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK when transcript is empty but turn is active, got %d: %s", w.Code, w.Body.String())
	}

	var respA map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &respA); err != nil {
		t.Fatalf("Failed to parse transcript json: %v", err)
	}
	msgsA, _ := respA["messages"].([]interface{})
	if len(msgsA) != 1 {
		t.Fatalf("Expected 1 in-flight message, got %d", len(msgsA))
	}
	firstMsg, _ := msgsA[0].(map[string]interface{})
	if firstMsg["role"] != "user" || !strings.Contains(firstMsg["content"].(string), "Implement headless in-flight prompts") {
		t.Errorf("Unexpected in-flight message content: %+v", firstMsg)
	}

	priorProvider := &mockInFlightTranscriptProvider{
		agent: "claude-code",
		messages: []TranscriptMessage{
			{Role: "user", Content: "First prompt", Timestamp: time.Now().Add(-1 * time.Minute)},
			{Role: "assistant", Content: "First answer", Timestamp: time.Now().Add(-30 * time.Second)},
		},
	}
	srv.RegisterProvider(priorProvider)

	wB := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wB, req)

	if wB.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with prior messages, got %d: %s", wB.Code, wB.Body.String())
	}

	var respB map[string]interface{}
	if err := json.Unmarshal(wB.Body.Bytes(), &respB); err != nil {
		t.Fatalf("Failed to parse transcript json: %v", err)
	}
	msgsB, _ := respB["messages"].([]interface{})
	if len(msgsB) != 3 {
		t.Fatalf("Expected 3 messages (2 prior + 1 in-flight), got %d: %+v", len(msgsB), msgsB)
	}
	lastMsg, _ := msgsB[2].(map[string]interface{})
	if lastMsg["role"] != "user" || !strings.Contains(lastMsg["content"].(string), "Implement headless in-flight prompts") {
		t.Errorf("Expected in-flight prompt at tail, got: %+v", lastMsg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamReq := httptest.NewRequest(http.MethodGet, "/v1/sessions/chat/stream?session_id="+sess.ID, nil).WithContext(ctx)
	streamRec := httptest.NewRecorder()

	streamDone := make(chan bool)
	go func() {
		srv.Mux().ServeHTTP(streamRec, streamReq)
		streamDone <- true
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-streamDone:
	case <-time.After(1 * time.Second):
		t.Fatalf("Stream did not finish after cancellation")
	}

	streamBody := streamRec.Body.String()
	if !strings.Contains(streamBody, `"type":"turn_start"`) || !strings.Contains(streamBody, "Implement headless in-flight prompts") {
		t.Errorf("Expected stream to emit turn_start with active prompt upon mid-turn connection, got: %s", streamBody)
	}
}
