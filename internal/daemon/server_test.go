package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"ackbar/internal/tmux"
)

type MockProvider struct{}

func (m *MockProvider) Agent() string {
	return "mock-agent"
}

func (m *MockProvider) IsInstalled() bool {
	return true
}

func (m *MockProvider) CheckHookConfig() (bool, string, error) {
	return true, "mock-setup-cmd", nil
}

func (m *MockProvider) ParseHook(eventName string, payload []byte) (*Event, error) {
	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}

	sessionID, _ := data["session_id"].(string)
	cwd, _ := data["cwd"].(string)
	activity, _ := data["activity"].(string)

	return &Event{
		Agent:       "mock-agent",
		NativeID:    sessionID,
		Cwd:         cwd,
		EventName:   eventName,
		State:       StateWorking,
		Activity:    activity,
		LastEventAt: time.Now(),
	}, nil
}

func TestServer_Integration(t *testing.T) {
	// Create temporary db file
	dbFile := "./test_integration.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.RegisterProvider(&MockProvider{})

	// 1. Test POST Hook Ingest
	payload := `{"session_id": "session-123", "cwd": "/path/to/project", "activity": "compiling"}`
	req := httptest.NewRequest("POST", "/v1/hooks/mock-agent?event=SessionStart", bytes.NewBufferString(payload))
	req.Header.Set("X-Ackbar-Host", "test-host")

	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var status map[string]string
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("Failed to parse response body: %v", err)
	}
	if status["status"] != "enqueued" {
		t.Errorf("Expected status enqueued, got %s", status["status"])
	}

	// Wait briefly for asynchronous background ingestion
	time.Sleep(300 * time.Millisecond)

	// 2. Test GET Sessions
	reqGet := httptest.NewRequest("GET", "/v1/sessions", nil)
	wGet := httptest.NewRecorder()
	server.Mux().ServeHTTP(wGet, reqGet)

	respGet := wGet.Result()
	if respGet.StatusCode != http.StatusOK {
		t.Errorf("Expected GET status 200, got %d", respGet.StatusCode)
	}

	var sessions []*Session
	if err := json.NewDecoder(respGet.Body).Decode(&sessions); err != nil {
		t.Fatalf("Failed to decode sessions: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session in database, got %d", len(sessions))
	}

	s := sessions[0]
	if s.ID != "mock-agent:test-host:session-123" {
		t.Errorf("Expected ID 'mock-agent:test-host:session-123', got '%s'", s.ID)
	}
	if s.Cwd != "/path/to/project" {
		t.Errorf("Expected Cwd '/path/to/project', got '%s'", s.Cwd)
	}
	if s.Activity != "compiling" {
		t.Errorf("Expected Activity 'compiling', got '%s'", s.Activity)
	}

	// 3. Test POST Archive Action
	reqArchive := httptest.NewRequest("POST", "/v1/sessions/mock-agent:test-host:session-123/archive", nil)
	wArchive := httptest.NewRecorder()
	server.Mux().ServeHTTP(wArchive, reqArchive)

	respArchive := wArchive.Result()
	if respArchive.StatusCode != http.StatusOK {
		t.Errorf("Expected Archive status 200, got %d", respArchive.StatusCode)
	}

	// Verify in DB that it is archived
	sArchived, err := db.GetSession("mock-agent:test-host:session-123")
	if err != nil {
		t.Fatalf("Failed to fetch session: %v", err)
	}
	if !sArchived.Archived {
		t.Errorf("Expected session to be archived")
	}

	// 4. Test POST Unarchive Action
	reqUnarchive := httptest.NewRequest("POST", "/v1/sessions/mock-agent:test-host:session-123/unarchive", nil)
	wUnarchive := httptest.NewRecorder()
	server.Mux().ServeHTTP(wUnarchive, reqUnarchive)

	respUnarchive := wUnarchive.Result()
	if respUnarchive.StatusCode != http.StatusOK {
		t.Errorf("Expected Unarchive status 200, got %d", respUnarchive.StatusCode)
	}

	// Verify in DB that it is unarchived
	sUnarchived, err := db.GetSession("mock-agent:test-host:session-123")
	if err != nil {
		t.Fatalf("Failed to fetch session: %v", err)
	}
	if sUnarchived.Archived {
		t.Errorf("Expected session to be unarchived")
	}

	// 5. Test POST Spawn Action (requires tmux)
	if _, err := exec.LookPath("tmux"); err == nil {
		spawnPayload := `{"agent": "mock-agent", "cwd": "` + os.TempDir() + `"}`
		reqSpawn := httptest.NewRequest("POST", "/v1/sessions/spawn", bytes.NewBufferString(spawnPayload))
		wSpawn := httptest.NewRecorder()
		server.Mux().ServeHTTP(wSpawn, reqSpawn)

		respSpawn := wSpawn.Result()
		if respSpawn.StatusCode != http.StatusOK {
			if strings.Contains(wSpawn.Body.String(), "Operation not permitted") {
				t.Skip("tmux socket restricted in sandbox execution")
			} else {
				t.Errorf("Expected Spawn status 200, got %d (body: %s)", respSpawn.StatusCode, wSpawn.Body.String())
			}
		}

		var spawnResp map[string]string
		_ = json.NewDecoder(respSpawn.Body).Decode(&spawnResp)
		tempUUID := spawnResp["session_id"]
		if tempUUID == "" {
			t.Errorf("Expected session_id in spawn response")
		}

		// Verify session was added to DB
		dbID := "mock-agent:local:" + tempUUID
		sSpawning, err := db.GetSession(dbID)
		if err != nil || sSpawning == nil {
			t.Fatalf("Spawning session not found in database: %v", err)
		}

		if !sSpawning.Managed || sSpawning.TmuxName == "" {
			t.Errorf("Expected spawning session to be managed and have a tmux name")
		}

		// Cleanup the tmux session spawned
		_ = tmux.Kill(context.Background(), sSpawning.TmuxName)
	}
}

func TestServer_Respond(t *testing.T) {
	dbFile := "./test_respond.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// Create a blocked session
	sess := &Session{
		ID:        "claude-code:local:uuid-1234",
		Agent:     "claude-code",
		Host:      "local",
		NativeID:  "uuid-1234",
		Cwd:       "/path/to/project",
		State:     StateBlocked,
		Blocked: &Blocked{
			Kind:     BlockQuestion,
			Reason:   "Which database?",
			Question: "Which database?",
			Options:  []string{"PostgreSQL", "SQLite"},
			Since:    time.Now().Add(-5 * time.Minute),
		},
		Activity:    "Waiting for user response",
		StartedAt:   time.Now().Add(-10 * time.Minute),
		LastEventAt: time.Now().Add(-5 * time.Minute),
	}

	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("Failed to save initial session: %v", err)
	}

	// 1. Test POST /v1/sessions/respond with action "answer"
	respondPayload := `{"id": "claude-code:local:uuid-1234", "action": "answer", "value": "2"}`
	req := httptest.NewRequest("POST", "/v1/sessions/respond", bytes.NewBufferString(respondPayload))
	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d (body: %s)", resp.StatusCode, w.Body.String())
	}

	var respData map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &respData); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}
	if respData["status"] != "responded" {
		t.Errorf("Expected status 'responded', got '%v'", respData["status"])
	}
	if respData["action"] != "answer" {
		t.Errorf("Expected action 'answer', got '%v'", respData["action"])
	}
	if respData["value"] != "2" {
		t.Errorf("Expected value '2', got '%v'", respData["value"])
	}

	// Verify DB state updated to StateWorking and Blocked cleared
	updatedSess, err := db.GetSession("claude-code:local:uuid-1234")
	if err != nil {
		t.Fatalf("Failed to retrieve updated session: %v", err)
	}
	if updatedSess.State != StateWorking {
		t.Errorf("Expected StateWorking (%d), got %d", StateWorking, updatedSess.State)
	}
	if updatedSess.Blocked != nil {
		t.Errorf("Expected Blocked to be nil, got %+v", updatedSess.Blocked)
	}
	if !strings.Contains(updatedSess.Activity, "2") {
		t.Errorf("Expected Activity to mention response, got %s", updatedSess.Activity)
	}

	// 2. Test action "allow"
	updatedSess.State = StateBlocked
	updatedSess.Blocked = &Blocked{
		Kind:     BlockPermission,
		Reason:   "Run bash command",
		Question: "Allow running bash?",
		Options:  []string{"Allow", "Deny"},
		Since:    time.Now(),
	}
	_ = db.SaveSession(updatedSess)

	allowPayload := `{"id": "claude-code:local:uuid-1234", "action": "allow"}`
	reqAllow := httptest.NewRequest("POST", "/v1/sessions/respond", bytes.NewBufferString(allowPayload))
	wAllow := httptest.NewRecorder()
	server.Mux().ServeHTTP(wAllow, reqAllow)

	if wAllow.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for allow, got %d", wAllow.Code)
	}

	sAllowed, _ := db.GetSession("claude-code:local:uuid-1234")
	if sAllowed.State != StateWorking || sAllowed.Blocked != nil {
		t.Errorf("Expected StateWorking and nil Blocked after allow, got state %v, blocked %+v", sAllowed.State, sAllowed.Blocked)
	}

	// 3. Test action "deny" with sessionId fallback
	sAllowed.State = StateBlocked
	sAllowed.Blocked = &Blocked{
		Kind:   BlockPermission,
		Reason: "Run risky script",
		Since:  time.Now(),
	}
	_ = db.SaveSession(sAllowed)

	denyPayload := `{"sessionId": "claude-code:local:uuid-1234", "action": "deny"}`
	reqDeny := httptest.NewRequest("POST", "/v1/sessions/respond", bytes.NewBufferString(denyPayload))
	wDeny := httptest.NewRecorder()
	server.Mux().ServeHTTP(wDeny, reqDeny)

	if wDeny.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for deny, got %d", wDeny.Code)
	}

	sDenied, _ := db.GetSession("claude-code:local:uuid-1234")
	if sDenied.State != StateWorking || sDenied.Blocked != nil {
		t.Errorf("Expected StateWorking and nil Blocked after deny, got state %v, blocked %+v", sDenied.State, sDenied.Blocked)
	}

	// 4. Test action "input"
	sDenied.State = StateBlocked
	sDenied.Blocked = &Blocked{
		Kind:   BlockQuestion,
		Reason: "Provide API key",
		Since:  time.Now(),
	}
	_ = db.SaveSession(sDenied)

	inputPayload := `{"id": "claude-code:local:uuid-1234", "action": "input", "value": "sk-12345"}`
	reqInput := httptest.NewRequest("POST", "/v1/sessions/respond", bytes.NewBufferString(inputPayload))
	wInput := httptest.NewRecorder()
	server.Mux().ServeHTTP(wInput, reqInput)

	if wInput.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for input, got %d", wInput.Code)
	}

	sInput, _ := db.GetSession("claude-code:local:uuid-1234")
	if sInput.State != StateWorking || sInput.Blocked != nil {
		t.Errorf("Expected StateWorking and nil Blocked after input, got state %v, blocked %+v", sInput.State, sInput.Blocked)
	}

	// 5. Test Error Handling
	// Missing params
	reqBad := httptest.NewRequest("POST", "/v1/sessions/respond", bytes.NewBufferString(`{}`))
	wBad := httptest.NewRecorder()
	server.Mux().ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for missing params, got %d", wBad.Code)
	}

	// Non-existent session
	reqNotFound := httptest.NewRequest("POST", "/v1/sessions/respond", bytes.NewBufferString(`{"id": "non-existent-session", "action": "allow"}`))
	wNotFound := httptest.NewRecorder()
	server.Mux().ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found, got %d", wNotFound.Code)
	}

	// Method not allowed
	reqGet := httptest.NewRequest("GET", "/v1/sessions/respond", nil)
	wGet := httptest.NewRecorder()
	server.Mux().ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405 Method Not Allowed, got %d", wGet.Code)
	}
}

func TestServer_Respond_ManagedTmux(t *testing.T) {
	if !tmux.IsTmuxInstalled() {
		t.Skip("tmux not installed on host, skipping test")
	}

	dbFile := "./test_respond_tmux.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	sessionName := "test-respond-tmux-sess"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = tmux.Kill(ctx, sessionName)
	if err := tmux.Spawn(ctx, sessionName, os.TempDir(), "cat"); err != nil {
		if strings.Contains(err.Error(), "Operation not permitted") {
			t.Skip("tmux socket creation blocked in sandbox")
		}
		t.Fatalf("Failed to spawn tmux session: %v", err)
	}
	defer tmux.Kill(ctx, sessionName)

	sess := &Session{
		ID:        "claude-code:local:managed-tmux-1",
		Agent:     "claude-code",
		Host:      "local",
		NativeID:  "managed-tmux-1",
		Cwd:       os.TempDir(),
		Managed:   true,
		TmuxName:  sessionName,
		State:     StateBlocked,
		Blocked: &Blocked{
			Kind:     BlockQuestion,
			Reason:   "Select mode",
			Question: "Select mode",
			Options:  []string{"Fast", "Thorough"},
			Since:    time.Now(),
		},
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	// Send answer
	respondPayload := `{"id": "claude-code:local:managed-tmux-1", "action": "answer", "value": "Fast"}`
	req := httptest.NewRequest("POST", "/v1/sessions/respond", bytes.NewBufferString(respondPayload))
	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d (%s)", w.Code, w.Body.String())
	}

	updated, _ := db.GetSession("claude-code:local:managed-tmux-1")
	if updated.State != StateWorking || updated.Blocked != nil {
		t.Errorf("Expected StateWorking and nil Blocked, got state %v, blocked %+v", updated.State, updated.Blocked)
	}
}
