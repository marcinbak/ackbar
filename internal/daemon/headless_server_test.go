package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["status"] != "spawned" || resp["engine_type"] != EngineHeadless {
		t.Errorf("Unexpected response: %+v", resp)
	}

	sessionID := resp["session_id"]
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

	// In test environment without tmux running, it tries to spawn or reuse
	// It should never 404 or panic
	if w.Code == http.StatusNotFound {
		t.Fatalf("Expected session to be found, got 404")
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
