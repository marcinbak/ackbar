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

