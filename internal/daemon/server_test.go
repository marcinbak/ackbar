package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type MockProvider struct{}

func (m *MockProvider) Agent() string {
	return "mock-agent"
}

func (m *MockProvider) DisplayName() string {
	return "Mock Agent"
}

func (m *MockProvider) BrandColor() string {
	return "#999999"
}

func (m *MockProvider) IconSVG() string {
	return "<svg></svg>"
}

func (m *MockProvider) ProcessNames() []string {
	return []string{"mock-agent"}
}

func (m *MockProvider) GetSpawnCommand(tempUUID string) string {
	return "sleep 5"
}

func (m *MockProvider) GetResumeCommand(nativeID string) string {
	return "sleep 5"
}

func (m *MockProvider) ReadSessionMetadata(cwd, nativeID string) *SessionMeta {
	return nil
}

func (m *MockProvider) ResolveSessionTitle(cwd, nativeID string) string {
	return ""
}

func (m *MockProvider) ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error) {
	return nil, nil
}

func (m *MockProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	return nil
}

func (m *MockProvider) InspectStatus(ctx context.Context, sess *Session) bool {
	return false
}

func (m *MockProvider) ListSubagents(home, cwd, nativeID string) ([]*ActiveSubagent, error) {
	return nil, nil
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
	name, _ := data["name"].(string)
	activity, _ := data["activity"].(string)

	return &Event{
		Agent:       "mock-agent",
		NativeID:    sessionID,
		Cwd:         cwd,
		Name:        name,
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
		ID:       "claude-code:local:uuid-1234",
		Agent:    "claude-code",
		Host:     "local",
		NativeID: "uuid-1234",
		Cwd:      "/path/to/project",
		State:    StateBlocked,
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
		ID:       "claude-code:local:managed-tmux-1",
		Agent:    "claude-code",
		Host:     "local",
		NativeID: "managed-tmux-1",
		Cwd:      os.TempDir(),
		Managed:  true,
		TmuxName: sessionName,
		State:    StateBlocked,
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

func TestIsRawSessionName(t *testing.T) {
	rawTests := []struct {
		input string
		isRaw bool
	}{
		{"", true},
		{"   ", true},
		{"claude-code", true},
		{"antigravity", true},
		{"codex", true},
		{"proc-12345", true},
		{"ackbar-claude-code-abc", true},
		{"e9e03db7-9a31-46a1-88d7-bd6e1b885092", true},
		{"claude-code (e9e03db7)", true},
		{"Antigravity (proc-555)", true},
		{"ngl-android-23", true},
		{"modemobile-1", true},
		{"skip2q-4", true},
		{"app-42", true},
		{"Pick up NGL-234 React Native upgrade", false},
		{"NGL-400 incremental Firebase messaging refactor", false},
		{"Fix login crash on Android", false},
		{"NGL-433", false},
	}

	for _, tt := range rawTests {
		got := IsRawSessionName(tt.input)
		if got != tt.isRaw {
			t.Errorf("IsRawSessionName(%q) = %v; want %v", tt.input, got, tt.isRaw)
		}
	}
}

func TestSessionTitleStability_CustomTitleNotOverwrittenByHooks(t *testing.T) {
	dbFile := "./test_title_stability.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.RegisterProvider(&MockProvider{})

	// 1. Seed session with custom title
	sess := &Session{
		ID:          "mock-agent:local:session-stable-1",
		Agent:       "mock-agent",
		Host:        "local",
		NativeID:    "session-stable-1",
		Cwd:         "/Users/dev4u/Work/Modemobile/NGL/ngl-android",
		Name:        "Given Custom Name",
		CustomTitle: "Given Custom Name",
		State:       StateWorking,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 2. Ingest hook with a raw slug name via HTTP
	payload := `{"session_id": "session-stable-1", "cwd": "/Users/dev4u/Work/Modemobile/NGL/ngl-android", "name": "ngl-android-23", "activity": "Running tool"}`
	req := httptest.NewRequest("POST", "/v1/hooks/mock-agent", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	// Wait briefly for asynchronous background ingestion
	time.Sleep(300 * time.Millisecond)

	saved, err := db.GetSession("mock-agent:local:session-stable-1")
	if err != nil || saved == nil {
		t.Fatalf("Failed to retrieve session: %v", err)
	}

	if saved.Name != "Given Custom Name" {
		t.Errorf("Expected Name to remain 'Given Custom Name', got %q", saved.Name)
	}
	if saved.CustomTitle != "Given Custom Name" {
		t.Errorf("Expected CustomTitle to remain 'Given Custom Name', got %q", saved.CustomTitle)
	}
}

func TestSessionRename_SetsCustomTitleAndUpdatesCache(t *testing.T) {
	dbFile := "./test_rename_custom.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	sess := &Session{
		ID:          "mock-agent:local:session-rename-1",
		Agent:       "mock-agent",
		Host:        "local",
		NativeID:    "session-rename-1",
		Cwd:         "/workspace/project",
		Name:        "Old Name",
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	req := httptest.NewRequest("POST", "/v1/sessions/control?action=rename&id=mock-agent:local:session-rename-1&name=Brand+New+Title", nil)
	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on rename, got %d (%s)", w.Code, w.Body.String())
	}

	saved, _ := db.GetSession("mock-agent:local:session-rename-1")
	if saved.Name != "Brand New Title" {
		t.Errorf("Expected Name 'Brand New Title', got %q", saved.Name)
	}
	if saved.CustomTitle != "Brand New Title" {
		t.Errorf("Expected CustomTitle 'Brand New Title', got %q", saved.CustomTitle)
	}

	cacheKey := "/workspace/project:session-rename-1"
	titleCacheMutex.RLock()
	cached, ok := titleCache[cacheKey]
	titleCacheMutex.RUnlock()

	if !ok || cached.Title != "Brand New Title" || cached.Source != "custom" {
		t.Errorf("Expected titleCache entry with Source=custom, got %+v (ok=%v)", cached, ok)
	}
}

func TestUnreadState_LifecycleAndMarkRead(t *testing.T) {
	dbFile := "./test_unread_lifecycle.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	sess := &Session{
		ID:          "mock-agent:local:session-unread-1",
		Agent:       "mock-agent",
		Host:        "local",
		NativeID:    "session-unread-1",
		Cwd:         "/workspace/project",
		Name:        "Test Session",
		State:       StateWorking,
		IsUnread:    false,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	// 1. Ingest state change (Working -> Idle)
	event := &Event{
		Agent:       "mock-agent",
		NativeID:    "session-unread-1",
		Cwd:         "/workspace/project",
		State:       StateIdle,
		Activity:    "Turn complete",
		LastEventAt: time.Now(),
	}
	body, _ := json.Marshal(map[string]interface{}{
		"session_id": "session-unread-1",
		"cwd":        "/workspace/project",
	})
	server.processHookEvent(&mockDynamicProvider{event: event}, "Stop", "local", body)

	// Verify session is now unread with updated state
	updated, err := db.GetSession("mock-agent:local:session-unread-1")
	if err != nil || updated == nil {
		t.Fatalf("Failed to retrieve updated session: %v", err)
	}
	if updated.State != StateIdle {
		t.Errorf("Expected state Idle, got %v", updated.State)
	}
	if !updated.IsUnread {
		t.Errorf("Expected IsUnread to be true on state change")
	}
	if updated.LastStateChangeAt.IsZero() {
		t.Errorf("Expected LastStateChangeAt to be populated")
	}

	// 2. Mark as read via HTTP control endpoint
	req := httptest.NewRequest("POST", "/v1/sessions/control?action=read&id=mock-agent:local:session-unread-1", nil)
	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on mark read, got %d (%s)", w.Code, w.Body.String())
	}

	readSess, _ := db.GetSession("mock-agent:local:session-unread-1")
	if readSess.IsUnread {
		t.Errorf("Expected IsUnread to be false after mark read, got true")
	}

	// 3. User submits prompt -> Transition Idle -> Working (IsUnread must remain FALSE)
	workingEvent := &Event{
		Agent:       "mock-agent",
		NativeID:    "session-unread-1",
		Cwd:         "/workspace/project",
		State:       StateWorking,
		Activity:    "Processing user prompt",
		LastEventAt: time.Now(),
	}
	server.processHookEvent(&mockDynamicProvider{event: workingEvent}, "UserPromptSubmit", "local", body)

	workingSess, _ := db.GetSession("mock-agent:local:session-unread-1")
	if workingSess.State != StateWorking {
		t.Errorf("Expected state Working, got %v", workingSess.State)
	}
	if workingSess.IsUnread {
		t.Errorf("Expected IsUnread to be false when transitioning to StateWorking")
	}

	// 4. Intermediate tool event while Working (IsUnread must remain FALSE)
	toolEvent := &Event{
		Agent:       "mock-agent",
		NativeID:    "session-unread-1",
		Cwd:         "/workspace/project",
		State:       StateWorking,
		Activity:    "Running tool: bash",
		LastEventAt: time.Now(),
	}
	server.processHookEvent(&mockDynamicProvider{event: toolEvent}, "PreToolUse", "local", body)

	toolSess, _ := db.GetSession("mock-agent:local:session-unread-1")
	if toolSess.IsUnread {
		t.Errorf("Expected IsUnread to remain false during intermediate tool execution")
	}

	// 5. Agent requests permission -> Transition Working -> Blocked (IsUnread must become TRUE)
	blockedEvent := &Event{
		Agent:    "mock-agent",
		NativeID: "session-unread-1",
		Cwd:      "/workspace/project",
		State:    StateBlocked,
		Blocked: &Blocked{
			Kind:   BlockPermission,
			Reason: "Allow bash",
			Since:  time.Now(),
		},
		Activity:    "Waiting for tool authorization",
		LastEventAt: time.Now(),
	}
	server.processHookEvent(&mockDynamicProvider{event: blockedEvent}, "PermissionRequest", "local", body)

	blockedSess, _ := db.GetSession("mock-agent:local:session-unread-1")
	if blockedSess.State != StateBlocked {
		t.Errorf("Expected state Blocked, got %v", blockedSess.State)
	}
	if !blockedSess.IsUnread {
		t.Errorf("Expected IsUnread to be true when transitioning to StateBlocked")
	}
}

type mockDynamicProvider struct {
	agentName string
	event     *Event
}

func (m *mockDynamicProvider) Agent() string {
	if m.agentName != "" {
		return m.agentName
	}
	return "mock-agent"
}
func (m *mockDynamicProvider) DisplayName() string                                   { return "Mock Agent" }
func (m *mockDynamicProvider) BrandColor() string                                    { return "#999999" }
func (m *mockDynamicProvider) IconSVG() string                                       { return "<svg></svg>" }
func (m *mockDynamicProvider) ProcessNames() []string                                { return []string{"mock-agent"} }
func (m *mockDynamicProvider) GetSpawnCommand(tempUUID string) string                { return "sleep 5" }
func (m *mockDynamicProvider) GetResumeCommand(nativeID string) string               { return "sleep 5" }
func (m *mockDynamicProvider) ReadSessionMetadata(cwd, nativeID string) *SessionMeta { return nil }
func (m *mockDynamicProvider) ResolveSessionTitle(cwd, nativeID string) string       { return "" }
func (m *mockDynamicProvider) ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error) {
	return nil, nil
}
func (m *mockDynamicProvider) CleanSessionFiles(home, cwd, nativeID string) error    { return nil }
func (m *mockDynamicProvider) InspectStatus(ctx context.Context, sess *Session) bool { return false }
func (m *mockDynamicProvider) ListSubagents(home, cwd, nativeID string) ([]*ActiveSubagent, error) {
	return nil, nil
}
func (m *mockDynamicProvider) IsInstalled() bool                      { return true }
func (m *mockDynamicProvider) CheckHookConfig() (bool, string, error) { return true, "", nil }
func (m *mockDynamicProvider) ParseHook(eventName string, payload []byte) (*Event, error) {
	return m.event, nil
}

func TestInPlaceClearSessionRotation_AdoptsTmuxAndIncrementsTurn(t *testing.T) {
	dbFile := "./test_clear_rotation.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// 1. Initial managed session
	sess1 := &Session{
		ID:          "mock-agent:local:native-turn-1",
		Agent:       "mock-agent",
		Host:        "local",
		NativeID:    "native-turn-1",
		Cwd:         "/workspace/project-rot",
		Name:        "Project Alpha",
		Managed:     true,
		TmuxName:    "ackbar-mock-native-turn-1",
		NodePath:    "Work/project-rot",
		ProjectKey:  "project-rot",
		State:       StateWorking,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(sess1); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 2. Ingest hook with new native ID (Turn 2 after /clear)
	event2 := &Event{
		Agent:       "mock-agent",
		NativeID:    "native-turn-2",
		Cwd:         "/workspace/project-rot",
		State:       StateWorking,
		Activity:    "Processing prompt",
		LastEventAt: time.Now(),
	}
	body2, _ := json.Marshal(map[string]interface{}{
		"session_id": "native-turn-2",
		"cwd":        "/workspace/project-rot",
	})
	server.processHookEvent(&mockDynamicProvider{event: event2}, "UserPromptSubmit", "local", body2)

	// Verify Turn 1 is archived
	archived1, err := db.GetSession("mock-agent:local:native-turn-1")
	if err != nil || archived1 == nil {
		t.Fatalf("Failed to retrieve archived turn 1: %v", err)
	}
	if archived1.Managed {
		t.Errorf("Expected turn 1 to no longer be managed")
	}
	if archived1.State != StateEnded {
		t.Errorf("Expected turn 1 state to be StateEnded, got %v", archived1.State)
	}
	if archived1.Name != "Project Alpha (Conv 1)" {
		t.Errorf("Expected turn 1 name 'Project Alpha (Conv 1)', got %q", archived1.Name)
	}
	if archived1.Activity != "Cleared (context reset)" {
		t.Errorf("Expected turn 1 activity 'Cleared (context reset)', got %q", archived1.Activity)
	}

	// Verify Turn 2 adopted the live tmux session
	turn2, err := db.GetSession("mock-agent:local:native-turn-2")
	if err != nil || turn2 == nil {
		t.Fatalf("Failed to retrieve turn 2 session: %v", err)
	}
	if !turn2.Managed {
		t.Errorf("Expected turn 2 to be managed")
	}
	if turn2.TmuxName != "ackbar-mock-native-turn-1" {
		t.Errorf("Expected turn 2 TmuxName 'ackbar-mock-native-turn-1', got %q", turn2.TmuxName)
	}
	if turn2.Name != "Project Alpha (Conv 2)" {
		t.Errorf("Expected turn 2 name 'Project Alpha (Conv 2)', got %q", turn2.Name)
	}
	if turn2.NodePath != "Work/project-rot" {
		t.Errorf("Expected turn 2 NodePath 'Work/project-rot', got %q", turn2.NodePath)
	}

	// 3. Ingest hook with third native ID (Turn 3 after another /clear)
	event3 := &Event{
		Agent:       "mock-agent",
		NativeID:    "native-turn-3",
		Cwd:         "/workspace/project-rot",
		State:       StateWorking,
		Activity:    "Processing prompt 3",
		LastEventAt: time.Now(),
	}
	body3, _ := json.Marshal(map[string]interface{}{
		"session_id": "native-turn-3",
		"cwd":        "/workspace/project-rot",
	})
	server.processHookEvent(&mockDynamicProvider{event: event3}, "UserPromptSubmit", "local", body3)

	// Verify Turn 2 is archived as Conv 2
	archived2, _ := db.GetSession("mock-agent:local:native-turn-2")
	if archived2.Managed || archived2.State != StateEnded || archived2.Name != "Project Alpha (Conv 2)" {
		t.Errorf("Unexpected archived turn 2 state: %+v", archived2)
	}

	// Verify Turn 3 is named Conv 3 and is managed
	turn3, _ := db.GetSession("mock-agent:local:native-turn-3")
	if !turn3.Managed || turn3.Name != "Project Alpha (Conv 3)" {
		t.Errorf("Unexpected turn 3 state: %+v", turn3)
	}
}

func TestSessionControl_ResumeAction(t *testing.T) {
	dbFile := "./test_resume_action.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	sess := &Session{
		ID:          "mock-agent:local:session-ended-1",
		Agent:       "mock-agent",
		Host:        "local",
		NativeID:    "session-ended-1",
		Cwd:         "/tmp",
		Name:        "Ended Session",
		State:       StateEnded,
		Managed:     false,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	req := httptest.NewRequest("POST", "/v1/sessions/control?action=resume&id=mock-agent:local:session-ended-1", nil)
	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on resume, got %d (%s)", w.Code, w.Body.String())
	}

	resumed, _ := db.GetSession("mock-agent:local:session-ended-1")
	if !resumed.Managed {
		t.Errorf("Expected resumed session to be managed")
	}
	if resumed.State != StateWorking {
		t.Errorf("Expected resumed session state StateWorking, got %v", resumed.State)
	}
	if resumed.Activity != "Resumed session" {
		t.Errorf("Expected activity 'Resumed session', got %q", resumed.Activity)
	}
	if resumed.TmuxName == "" {
		t.Errorf("Expected TmuxName to be set on resumed session")
	}

	// Clean up spawned tmux session if active
	if resumed.TmuxName != "" {
		_ = tmux.Kill(context.Background(), resumed.TmuxName)
	}
}

func TestSessionHandover_Settings(t *testing.T) {
	dbFile := "./test_handover_settings.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// 1. GET /v1/settings returns handover defaults
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	wGet := httptest.NewRecorder()
	server.Mux().ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("GET /v1/settings failed: %d", wGet.Code)
	}
	var settings map[string]string
	if err := json.Unmarshal(wGet.Body.Bytes(), &settings); err != nil {
		t.Fatalf("Failed to parse settings: %v", err)
	}
	if settings["handover_suggestion_enabled"] != "true" {
		t.Errorf("Expected handover_suggestion_enabled default 'true', got %q", settings["handover_suggestion_enabled"])
	}
	if settings["handover_threshold_pct"] != "60" {
		t.Errorf("Expected handover_threshold_pct default '60', got %q", settings["handover_threshold_pct"])
	}

	// 2. POST /v1/settings updates handover settings
	newSettings := map[string]string{
		"handover_suggestion_enabled": "false",
		"handover_threshold_pct":      "75",
	}
	body, _ := json.Marshal(newSettings)
	reqPost := httptest.NewRequest(http.MethodPost, "/v1/settings", bytes.NewReader(body))
	wPost := httptest.NewRecorder()
	server.Mux().ServeHTTP(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("POST /v1/settings failed: %d", wPost.Code)
	}
	var updatedSettings map[string]string
	_ = json.Unmarshal(wPost.Body.Bytes(), &updatedSettings)
	if updatedSettings["handover_suggestion_enabled"] != "false" {
		t.Errorf("Expected handover_suggestion_enabled 'false', got %q", updatedSettings["handover_suggestion_enabled"])
	}
	if updatedSettings["handover_threshold_pct"] != "75" {
		t.Errorf("Expected handover_threshold_pct '75', got %q", updatedSettings["handover_threshold_pct"])
	}
}

func TestSessionHandover_Validation(t *testing.T) {
	dbFile := "./test_handover_val.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// 1. Missing ID
	reqNoID := httptest.NewRequest(http.MethodPost, "/v1/sessions/handover", bytes.NewReader([]byte(`{}`)))
	wNoID := httptest.NewRecorder()
	server.Mux().ServeHTTP(wNoID, reqNoID)
	if wNoID.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for missing ID, got %d", wNoID.Code)
	}

	// 2. Non-existent session
	reqNotFound := httptest.NewRequest(http.MethodPost, "/v1/sessions/handover?id=non-existent", nil)
	wNotFound := httptest.NewRecorder()
	server.Mux().ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found for non-existent session, got %d", wNotFound.Code)
	}

	// 3. Unmanaged session
	unmanagedSess := &Session{
		ID:          "claude-code:local:unmanaged-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "unmanaged-1",
		Managed:     false,
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(unmanagedSess)

	reqUnmanaged := httptest.NewRequest(http.MethodPost, "/v1/sessions/handover?id=claude-code:local:unmanaged-1", nil)
	wUnmanaged := httptest.NewRecorder()
	server.Mux().ServeHTTP(wUnmanaged, reqUnmanaged)
	if wUnmanaged.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for unmanaged session, got %d (%s)", wUnmanaged.Code, wUnmanaged.Body.String())
	}

	// 4. Actively working session
	workingSess := &Session{
		ID:          "claude-code:local:working-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "working-1",
		Managed:     true,
		TmuxName:    "ackbar-working-1",
		State:       StateWorking,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(workingSess)

	reqWorking := httptest.NewRequest(http.MethodPost, "/v1/sessions/handover?id=claude-code:local:working-1", nil)
	wWorking := httptest.NewRecorder()
	server.Mux().ServeHTTP(wWorking, reqWorking)
	if wWorking.Code != http.StatusConflict {
		t.Errorf("Expected 409 Conflict for actively working session, got %d (%s)", wWorking.Code, wWorking.Body.String())
	}

	// 5. Valid Idle session (Async trigger)
	idleSess := &Session{
		ID:          "claude-code:local:idle-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "idle-1",
		Managed:     true,
		TmuxName:    "ackbar-idle-1",
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(idleSess)

	reqIdle := httptest.NewRequest(http.MethodPost, "/v1/sessions/handover?id=claude-code:local:idle-1", nil)
	wIdle := httptest.NewRecorder()
	server.Mux().ServeHTTP(wIdle, reqIdle)
	if wIdle.Code != http.StatusAccepted {
		t.Errorf("Expected 202 Accepted for async handover, got %d (%s)", wIdle.Code, wIdle.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(wIdle.Body.Bytes(), &resp)
	if resp["status"] != "handover_initiated" {
		t.Errorf("Expected status 'handover_initiated', got %v", resp["status"])
	}

	// 6. Valid via control action parameter: /v1/sessions/control?action=handover&id=...
	controlSess := &Session{
		ID:          "claude-code:local:control-1",
		Host:        "local",
		Agent:       "claude-code",
		Managed:     true,
		TmuxName:    "ackbar-control-1",
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(controlSess)

	reqControl := httptest.NewRequest(http.MethodPost, "/v1/sessions/control?action=handover&id=claude-code:local:control-1", nil)
	wControl := httptest.NewRecorder()
	server.Mux().ServeHTTP(wControl, reqControl)
	if wControl.Code != http.StatusAccepted {
		t.Errorf("Expected 202 Accepted from control endpoint, got %d", wControl.Code)
	}
}

func TestSessionHandover_Execution(t *testing.T) {
	dbFile := "./test_handover_exec.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	sess := &Session{
		ID:          "claude-code:local:exec-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "exec-1",
		Name:        "Feature Task",
		Managed:     true,
		TmuxName:    "ackbar-mock-handover-test",
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
		ContextPct:  75,
	}
	_ = db.SaveSession(sess)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// executeSessionHandover will attempt prompt dispatch
	_ = server.executeSessionHandover(ctx, sess, "in_place", "Verify tests", "Generate briefing prompt")

	updated, err := db.GetSession("claude-code:local:exec-1")
	if err != nil || updated == nil {
		t.Fatalf("Expected session to exist after handover attempt: %v", err)
	}
	if !strings.Contains(updated.Activity, "Handover") {
		t.Errorf("Expected activity to record Handover lifecycle, got %q", updated.Activity)
	}
}

func TestInspectClaudeStatus_TmuxIntegration(t *testing.T) {
	if !tmux.IsTmuxInstalled() {
		t.Skip("tmux not installed, skipping TestInspectClaudeStatus_TmuxIntegration")
	}

	dbFile := "./test_inspect_claude.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	ctx := context.Background()

	tmuxName := fmt.Sprintf("test-claude-%d", time.Now().UnixNano())
	defer tmux.Kill(ctx, tmuxName)

	// Spawn tmux session that outputs Claude Code interactive prompt
	if err := tmux.Spawn(ctx, tmuxName, os.TempDir(), "echo '─────────────────────────────────────────'; echo '❯ '; sleep 30"); err != nil {
		t.Fatalf("Failed to spawn test tmux session: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	pid, _ := tmux.GetPID(ctx, tmuxName)

	sess := &Session{
		ID:          "claude-code:local:test-sess-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "test-sess-1",
		State:       StateWorking, // currently thought to be working
		TmuxName:    tmuxName,
		PID:         pid,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}

	var changed bool
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		if server.inspectClaudeStatus(ctx, sess) {
			changed = true
			break
		}
	}
	if !changed {
		t.Errorf("Expected inspectClaudeStatus to report changed=true")
	}
	if sess.State != StateIdle {
		t.Errorf("Expected session state StateIdle when pane shows prompt, got %v", sess.State)
	}
	if sess.Activity != "Awaiting user prompt" {
		t.Errorf("Expected activity 'Awaiting user prompt', got %q", sess.Activity)
	}
}

func TestExtractClaudeQuestionAndOptions(t *testing.T) {
	samplePane := `
  Three forks I'd like settled before writing:
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
←  ☐ Portability  ☐ Config file  ☐ Run state  ✔ Submit  →

Where should the portable skill live, and how tracker-agnostic should it be?

❯ 1. Global skill, Jira-only
     Move to ~/.claude/skills/dev-workflow/, repo keeps only the config file. Tracker stays Jira but key/URL/statuses/JQL come from config. Simplest
     real portability — works for any Jira project, any stack.
  2. Global skill, tracker-agnostic
     Same relocation, but the tracker becomes an adapter (Jira/Linear/GitHub Issues) described in the config. More upfront work, and untestable
     until you have a non-Jira project.
  3. Stay in-repo, but cleanly split
     Skill stays at .claude/skills/dev-workflow/ with all project specifics moved into the config file. Portable by copy-paste rather than by
     install.
  4. Type something.
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  5. Chat about this

Enter to select · Tab/Arrow keys to navigate · Esc to cancel
`
	q, opts := extractClaudeQuestionAndOptions(samplePane)
	if q != "Where should the portable skill live, and how tracker-agnostic should it be?" {
		t.Errorf("Unexpected question: %q", q)
	}
	if len(opts) != 3 {
		t.Fatalf("Expected 3 options, got %d: %v", len(opts), opts)
	}
	if opts[0] != "Global skill, Jira-only" {
		t.Errorf("Option 0 mismatch: %q", opts[0])
	}
	if opts[1] != "Global skill, tracker-agnostic" {
		t.Errorf("Option 1 mismatch: %q", opts[1])
	}
	if opts[2] != "Stay in-repo, but cleanly split" {
		t.Errorf("Option 2 mismatch: %q", opts[2])
	}
}

func TestExtractClaudeQuestionAndOptions_BoxDrawingBorders(t *testing.T) {
	samplePane := `
│   Which parts of the backup do you want to restore?                                                                                               │
│ ───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── │
│ ❯ 1. Restore local config files (~/.config, ~/.zshrc, ~/.ssh, etc.)                                                                               │
│   2. Restore app data & preferences (~/Library/Application Support, Preferences)                                                                  │
│   3. Restore work workspaces and git repositories                                                                                                 │
│   4. Type something.                                                                                                                              │
│ ───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── │
│   5. Chat about this                                                                                                                              │
│                                                                                                                                                   │
│ ↑/↓ to navigate · Enter to select · Esc to cancel                                                                                                 │
`
	q, opts := extractClaudeQuestionAndOptions(samplePane)
	if q != "Which parts of the backup do you want to restore?" {
		t.Errorf("Unexpected question: %q", q)
	}
	if len(opts) != 3 {
		t.Fatalf("Expected 3 options, got %d: %v", len(opts), opts)
	}
	if opts[0] != "Restore local config files (~/.config, ~/.zshrc, ~/.ssh, etc.)" {
		t.Errorf("Option 0 mismatch: %q", opts[0])
	}
	if opts[1] != "Restore app data & preferences (~/Library/Application Support, Preferences)" {
		t.Errorf("Option 1 mismatch: %q", opts[1])
	}
	if opts[2] != "Restore work workspaces and git repositories" {
		t.Errorf("Option 2 mismatch: %q", opts[2])
	}
}

func TestExtractClaudeQuestionAndOptions_TopAndBottomBoxBorders(t *testing.T) {
	samplePane := `
Which database engine should we configure for local development?
┌─────────────────────────────────────────────────────────────────────────────────┐
│ ❯ 1. SQLite (pure Go)                                                           │
│   2. PostgreSQL (Docker)                                                        │
└─────────────────────────────────────────────────────────────────────────────────┘
↑/↓ to navigate · Enter to select · Esc to cancel
`
	q, opts := extractClaudeQuestionAndOptions(samplePane)
	if q != "Which database engine should we configure for local development?" {
		t.Errorf("Unexpected question: %q", q)
	}
	if len(opts) != 2 {
		t.Fatalf("Expected 2 options, got %d: %v", len(opts), opts)
	}
	if opts[0] != "SQLite (pure Go)" {
		t.Errorf("Option 0 mismatch: %q", opts[0])
	}
	if opts[1] != "PostgreSQL (Docker)" {
		t.Errorf("Option 1 mismatch: %q", opts[1])
	}
}

func TestInspectClaudeStatus_QuestionPrioritizedOverSubagent(t *testing.T) {
	if !tmux.IsTmuxInstalled() {
		t.Skip("tmux not installed, skipping TestInspectClaudeStatus_QuestionPrioritizedOverSubagent")
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	sessionID := "33333333-4444-5555-6666-777777777777"
	cwd := "/test/workspace"
	slug := "-test-workspace"

	// Create a running subagent on disk
	subDir := filepath.Join(tmpHome, ".claude", "projects", slug, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	meta := `{"name":"device-verifier","agentType":"verifier","description":"Verifying device"}`
	if err := os.WriteFile(filepath.Join(subDir, "agent-v1.meta.json"), []byte(meta), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-v1.jsonl"), []byte(`{"type":"assistant"}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ctx := context.Background()
	tmuxName := fmt.Sprintf("test-claude-q-sub-%d", time.Now().UnixNano())
	defer tmux.Kill(ctx, tmuxName)

	script := `
echo "│   Which parts of the backup do you want to restore?                                                                                               │"
echo "│ ───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── │"
echo "│ ❯ 1. Restore local config files                                                                                                                   │"
echo "│   2. Restore app data                                                                                                                             │"
echo "│ ↑/↓ to navigate · Enter to select · Esc to cancel                                                                                                 │"
sleep 30
`
	if err := tmux.Spawn(ctx, tmuxName, os.TempDir(), script); err != nil {
		t.Fatalf("Failed to spawn test tmux session: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	pid, _ := tmux.GetPID(ctx, tmuxName)

	sess := &Session{
		ID:          "claude-code:local:" + sessionID,
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    sessionID,
		Cwd:         cwd,
		State:       StateWorking,
		Activity:    "Subagent running: device-verifier",
		TmuxName:    tmuxName,
		PID:         pid,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}

	var changed bool
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		if InspectClaudeStatus(ctx, sess) {
			changed = true
			break
		}
	}
	if !changed {
		t.Errorf("Expected InspectClaudeStatus to report changed=true")
	}
	if sess.State != StateBlocked {
		t.Fatalf("Expected session state StateBlocked, got %v (activity: %s)", sess.State, sess.Activity)
	}
	if sess.Blocked == nil || sess.Blocked.Kind != BlockQuestion {
		t.Fatalf("Expected Blocked with BlockQuestion, got %+v", sess.Blocked)
	}
	if sess.Blocked.Question != "Which parts of the backup do you want to restore?" {
		t.Errorf("Expected question text, got %q", sess.Blocked.Question)
	}
	if len(sess.Blocked.Options) != 2 {
		t.Errorf("Expected 2 options, got %d: %v", len(sess.Blocked.Options), sess.Blocked.Options)
	}
}

func TestInspectAntigravityStatus_QuestionDetectedWithTrailingOutput(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	convUUID := "44444444-5555-6666-7777-888888888888"
	brainDir := filepath.Join(tmpHome, ".gemini", "antigravity", "brain", convUUID, ".system_generated", "logs")
	if err := os.MkdirAll(brainDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	transcript := `{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Hello"}` + "\n" +
		`{"step_index":2,"type":"PLANNER_RESPONSE","created_at":"2026-09-25T15:00:00Z","tool_calls":[{"name":"ask_question","args":{"questions":[{"question":"Proceed with deployment?","options":["Yes","No"]}]}}]}` + "\n" +
		`{"step_index":3,"type":"GENERIC","content":"Command output from pre-check"}` + "\n" +
		`{"step_index":4,"type":"PLANNER_RESPONSE","content":"Please make your choice above."}` + "\n"

	if err := os.WriteFile(filepath.Join(brainDir, "transcript.jsonl"), []byte(transcript), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	sess := &Session{
		ID:          "antigravity:local:" + convUUID,
		Agent:       "antigravity",
		Host:        "local",
		NativeID:    convUUID,
		State:       StateWorking,
		Activity:    "Working...",
		PID:         os.Getpid(),
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}

	ctx := context.Background()
	changed := InspectAntigravityStatus(ctx, sess)
	if !changed {
		t.Errorf("Expected InspectAntigravityStatus to report changed=true")
	}
	if sess.State != StateBlocked {
		t.Fatalf("Expected session state StateBlocked, got %v (activity: %s)", sess.State, sess.Activity)
	}
	if sess.Blocked == nil || sess.Blocked.Kind != BlockQuestion {
		t.Fatalf("Expected Blocked with BlockQuestion, got %+v", sess.Blocked)
	}
	if sess.Blocked.Question != "Proceed with deployment?" {
		t.Errorf("Expected question 'Proceed with deployment?', got %q", sess.Blocked.Question)
	}
	if len(sess.Blocked.Options) != 2 || sess.Blocked.Options[0] != "Yes" || sess.Blocked.Options[1] != "No" {
		t.Errorf("Expected ['Yes', 'No'], got %v", sess.Blocked.Options)
	}
}

func TestInspectAntigravityStatus_AnsweredQuestionDoesNotBlock(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	convUUID := "55555555-6666-7777-8888-999999999999"
	brainDir := filepath.Join(tmpHome, ".gemini", "antigravity", "brain", convUUID, ".system_generated", "logs")
	if err := os.MkdirAll(brainDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	transcript := `{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Deploy"}` + "\n" +
		`{"step_index":2,"type":"PLANNER_RESPONSE","created_at":"2026-09-25T15:00:00Z","tool_calls":[{"name":"ask_question","args":{"questions":[{"question":"Proceed?","options":["Yes","No"]}]}}]}` + "\n" +
		`{"step_index":3,"source":"MODEL","type":"ASK_QUESTION","created_at":"2026-09-25T15:01:00Z","content":"A1: Yes"}` + "\n" +
		`{"step_index":4,"type":"PLANNER_RESPONSE","tool_calls":[{"name":"run_command","args":{"CommandLine":"make build"}}]}` + "\n"

	if err := os.WriteFile(filepath.Join(brainDir, "transcript.jsonl"), []byte(transcript), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	sess := &Session{
		ID:          "antigravity:local:" + convUUID,
		Agent:       "antigravity",
		Host:        "local",
		NativeID:    convUUID,
		State:       StateWorking,
		Activity:    "Working...",
		PID:         os.Getpid(),
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}

	ctx := context.Background()
	InspectAntigravityStatus(ctx, sess)
	if sess.State == StateBlocked {
		t.Fatalf("Expected session NOT to be blocked since question was answered, got StateBlocked")
	}
}

func TestInspectClaudeStatus_QuestionPromptBlocked(t *testing.T) {
	if !tmux.IsTmuxInstalled() {
		t.Skip("tmux not installed, skipping TestInspectClaudeStatus_QuestionPromptBlocked")
	}

	dbFile := "./test_inspect_claude_question.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	ctx := context.Background()

	tmuxName := fmt.Sprintf("test-claude-q-%d", time.Now().UnixNano())
	defer tmux.Kill(ctx, tmuxName)

	script := `
echo "Where should the portable skill live?"
echo "❯ 1. Global skill"
echo "  2. In-repo skill"
echo "Enter to select · Tab/Arrow keys to navigate · Esc to cancel"
sleep 30
`
	if err := tmux.Spawn(ctx, tmuxName, os.TempDir(), script); err != nil {
		t.Fatalf("Failed to spawn test tmux session: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	pid, _ := tmux.GetPID(ctx, tmuxName)

	sess := &Session{
		ID:          "claude-code:local:test-sess-q",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "test-sess-q",
		State:       StateWorking,
		TmuxName:    tmuxName,
		PID:         pid,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}

	var changed bool
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		if server.inspectClaudeStatus(ctx, sess) {
			changed = true
			break
		}
	}
	if !changed {
		t.Errorf("Expected inspectClaudeStatus to report changed=true")
	}
	if sess.State != StateBlocked {
		t.Errorf("Expected session state StateBlocked, got %v", sess.State)
	}
	if sess.Blocked == nil || sess.Blocked.Kind != BlockQuestion {
		t.Fatalf("Expected Blocked with BlockQuestion, got %+v", sess.Blocked)
	}
	if sess.Blocked.Question != "Where should the portable skill live?" {
		t.Errorf("Expected question text, got %q", sess.Blocked.Question)
	}
	if len(sess.Blocked.Options) != 2 {
		t.Errorf("Expected 2 options, got %d: %v", len(sess.Blocked.Options), sess.Blocked.Options)
	}
}

func TestResolveSessionNodePath(t *testing.T) {
	dbFile := "./test_resolve_node_path.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	_ = db.SaveNode(&TreeNode{
		Path:       "Modemobile/NGL/ngl-android",
		ProjectDir: "/Users/dev4u/Work/Modemobile/NGL/ngl-android",
	})
	_ = db.SaveNode(&TreeNode{
		Path:       "Modemobile/Due-Dilligence",
		ProjectDir: "",
	})

	// 1. Exact project directory match
	p1 := server.resolveSessionNodePath("/Users/dev4u/Work/Modemobile/NGL/ngl-android")
	if p1 != "Modemobile/NGL/ngl-android" {
		t.Errorf("Expected Modemobile/NGL/ngl-android, got %q", p1)
	}

	// 2. Sub-directory match
	p2 := server.resolveSessionNodePath("/Users/dev4u/Work/Modemobile/NGL/ngl-android/app/src")
	if p2 != "Modemobile/NGL/ngl-android" {
		t.Errorf("Expected sub-directory to match Modemobile/NGL/ngl-android, got %q", p2)
	}

	// 3. Ancestor group segment match when cwd has no exact node
	p3 := server.resolveSessionNodePath("/home/dev4u/Work/modemobile/skills/plugins/cs-mcp")
	if p3 != "Modemobile" {
		t.Errorf("Expected ancestor segment match Modemobile, got %q", p3)
	}
}

func TestSessionAdoption_PreservesNodePath(t *testing.T) {
	dbFile := "./test_session_adoption_nodepath.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	mockP := &mockDynamicProvider{agentName: "claude-code"}
	server.RegisterProvider(mockP)

	// Spawning temporary session with assigned NodePath
	spawningID := "claude-code:local:temp-spawn-123"
	spawningSess := &Session{
		ID:          spawningID,
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "temp-spawn-123",
		Cwd:         "/home/dev4u/Work/modemobile",
		NodePath:    "Modemobile",
		CustomTitle: "Custom Task Title",
		Managed:     true,
		TmuxName:    "ackbar-claude-code-temp-spawn-123",
		State:       StateUnknown,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(spawningSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Claude Code starts and sends first hook event with real UUID
	realEvent := &Event{
		Agent:       "claude-code",
		NativeID:    "11111111-2222-3333-4444-555555555456",
		Cwd:         "/home/dev4u/Work/modemobile",
		State:       StateWorking,
		Activity:    "Thinking...",
		LastEventAt: time.Now(),
	}
	mockP.event = realEvent

	payload, _ := json.Marshal(map[string]interface{}{"type": "test"})
	server.processHookEvent(mockP, "test", "local", payload)

	adopted, err := db.GetSession("claude-code:local:11111111-2222-3333-4444-555555555456")
	if err != nil || adopted == nil {
		t.Fatalf("Adopted session not found in DB: %v", err)
	}
	if adopted.NodePath != "Modemobile" {
		t.Errorf("Expected adopted session NodePath to be Modemobile, got %q", adopted.NodePath)
	}
	if adopted.CustomTitle != "Custom Task Title" {
		t.Errorf("Expected CustomTitle to be preserved, got %q", adopted.CustomTitle)
	}
	if !adopted.Managed {
		t.Errorf("Expected adopted session to be Managed")
	}
}

func TestSessionArchiveControl_PersistsAndTogglesInDB(t *testing.T) {
	dbFile := "./test_archive_control.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	sess := &Session{
		ID:          "claude-code:local:arch-test-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "arch-test-1",
		Cwd:         "/workspace/project",
		Name:        "Archive Test Session",
		Archived:    false,
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 1. Archive
	reqArchive := httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id=claude-code:local:arch-test-1&action=archive", nil)
	wArchive := httptest.NewRecorder()
	server.handleSessionControl(wArchive, reqArchive)
	if wArchive.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on archive, got %d: %s", wArchive.Code, wArchive.Body.String())
	}

	sessDB, err := db.GetSession("claude-code:local:arch-test-1")
	if err != nil || sessDB == nil {
		t.Fatalf("Failed to fetch session from DB: %v", err)
	}
	if !sessDB.Archived {
		t.Errorf("Expected session to be archived in DB")
	}

	// 2. Unarchive
	reqUnarchive := httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id=claude-code:local:arch-test-1&action=unarchive", nil)
	wUnarchive := httptest.NewRecorder()
	server.handleSessionControl(wUnarchive, reqUnarchive)
	if wUnarchive.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on unarchive, got %d: %s", wUnarchive.Code, wUnarchive.Body.String())
	}

	sessDB2, err := db.GetSession("claude-code:local:arch-test-1")
	if err != nil || sessDB2 == nil {
		t.Fatalf("Failed to fetch session from DB: %v", err)
	}
	if sessDB2.Archived {
		t.Errorf("Expected session to be unarchived in DB")
	}
}

func TestSessionReconnect_DoesNotPromoteLastEventAt(t *testing.T) {
	dbFile := "./test_reconnect_last_event.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	mockP := &mockDynamicProvider{agentName: "claude-code"}
	server.RegisterProvider(mockP)

	oldTime := time.Now().Add(-48 * time.Hour)
	existingSess := &Session{
		ID:          "claude-code:local:11111111-2222-3333-4444-555555555001",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "11111111-2222-3333-4444-555555555001",
		Cwd:         "/workspace/project",
		Name:        "Old Session",
		State:       StateEnded,
		StartedAt:   oldTime,
		LastEventAt: oldTime,
	}
	if err := db.SaveSession(existingSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Process SessionStart hook event (CLI reconnecting)
	reconnectEvent := &Event{
		Agent:       "claude-code",
		NativeID:    "11111111-2222-3333-4444-555555555001",
		Cwd:         "/workspace/project",
		EventName:   "SessionStart",
		State:       StateIdle,
		Activity:    "Session started",
		LastEventAt: time.Now(),
	}
	mockP.event = reconnectEvent

	payload, _ := json.Marshal(map[string]interface{}{"type": "SessionStart"})
	server.processHookEvent(mockP, "SessionStart", "local", payload)

	sessAfter, err := db.GetSession("claude-code:local:11111111-2222-3333-4444-555555555001")
	if err != nil || sessAfter == nil {
		t.Fatalf("Failed to fetch session: %v", err)
	}

	// LastEventAt MUST NOT be updated to now; it must stay at its previous timestamp!
	if sessAfter.LastEventAt.Unix() != oldTime.Unix() {
		t.Errorf("Expected LastEventAt to remain %v, got %v", oldTime, sessAfter.LastEventAt)
	}
	if sessAfter.State != StateIdle {
		t.Errorf("Expected state to be StateIdle, got %v", sessAfter.State)
	}
}

func TestUserPromptSubmit_PromotesLastEventAt(t *testing.T) {
	dbFile := "./test_prompt_last_event.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	mockP := &mockDynamicProvider{agentName: "claude-code"}
	server.RegisterProvider(mockP)

	oldTime := time.Now().Add(-48 * time.Hour)
	existingSess := &Session{
		ID:          "claude-code:local:11111111-2222-3333-4444-555555555002",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "11111111-2222-3333-4444-555555555002",
		Cwd:         "/workspace/project",
		Name:        "Old Session",
		State:       StateIdle,
		StartedAt:   oldTime,
		LastEventAt: oldTime,
	}
	if err := db.SaveSession(existingSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// User submits prompt
	promptTime := time.Now()
	promptEvent := &Event{
		Agent:       "claude-code",
		NativeID:    "11111111-2222-3333-4444-555555555002",
		Cwd:         "/workspace/project",
		EventName:   "UserPromptSubmit",
		State:       StateWorking,
		Activity:    "Processing user prompt",
		LastEventAt: promptTime,
	}
	mockP.event = promptEvent

	payload, _ := json.Marshal(map[string]interface{}{"type": "UserPromptSubmit"})
	server.processHookEvent(mockP, "UserPromptSubmit", "local", payload)

	sessAfter, err := db.GetSession("claude-code:local:11111111-2222-3333-4444-555555555002")
	if err != nil || sessAfter == nil {
		t.Fatalf("Failed to fetch session: %v", err)
	}

	// LastEventAt MUST be updated to prompt time
	if sessAfter.LastEventAt.Unix() != promptTime.Unix() {
		t.Errorf("Expected LastEventAt to be %v, got %v", promptTime, sessAfter.LastEventAt)
	}
	if sessAfter.State != StateWorking {
		t.Errorf("Expected state to be StateWorking, got %v", sessAfter.State)
	}
}

func TestSessionDoneControl_PersistsAndTogglesInDB(t *testing.T) {
	dbFile := "./test_session_done.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	sess := &Session{
		ID:        "claude-code:local:done-test-1",
		Agent:     "claude-code",
		Host:      "local",
		NativeID:  "done-test-1",
		Cwd:       "/workspace/project",
		State:     StateIdle,
		StartedAt: time.Now(),
		IsDone:    false,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 1. Mark as done via /v1/sessions/control?id=...&action=done
	reqDone := httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id=claude-code:local:done-test-1&action=done", nil)
	wDone := httptest.NewRecorder()
	server.handleSessionControl(wDone, reqDone)

	if wDone.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", wDone.Code, wDone.Body.String())
	}

	sessDone, err := db.GetSession("claude-code:local:done-test-1")
	if err != nil || sessDone == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if !sessDone.IsDone {
		t.Errorf("Expected IsDone to be true, got false")
	}

	// 2. Mark as active via action=active
	reqActive := httptest.NewRequest(http.MethodPost, "/v1/sessions/control?id=claude-code:local:done-test-1&action=active", nil)
	wActive := httptest.NewRecorder()
	server.handleSessionControl(wActive, reqActive)

	if wActive.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", wActive.Code, wActive.Body.String())
	}

	sessActive, err := db.GetSession("claude-code:local:done-test-1")
	if err != nil || sessActive == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if sessActive.IsDone {
		t.Errorf("Expected IsDone to be false, got true")
	}
}

func TestSettings_GetAndSetEndpoints(t *testing.T) {
	dbFile := "./test_settings_endpoint.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// 1. GET settings (should return defaults)
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	wGet := httptest.NewRecorder()
	server.handleSettings(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", wGet.Code)
	}

	var defaults map[string]string
	if err := json.Unmarshal(wGet.Body.Bytes(), &defaults); err != nil {
		t.Fatalf("Failed to parse defaults: %v", err)
	}
	if defaults["auto_done_enabled"] != "true" || defaults["auto_done_hours"] != "24" {
		t.Errorf("Unexpected default settings: %+v", defaults)
	}

	// 2. POST settings update
	payload, _ := json.Marshal(map[string]string{
		"auto_done_enabled": "false",
		"auto_done_hours":   "48",
	})
	reqPost := httptest.NewRequest(http.MethodPost, "/v1/settings", bytes.NewReader(payload))
	wPost := httptest.NewRecorder()
	server.handleSettings(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", wPost.Code)
	}

	var updated map[string]string
	if err := json.Unmarshal(wPost.Body.Bytes(), &updated); err != nil {
		t.Fatalf("Failed to parse updated settings: %v", err)
	}
	if updated["auto_done_enabled"] != "false" || updated["auto_done_hours"] != "48" {
		t.Errorf("Expected updated settings, got: %+v", updated)
	}
}

func TestReadClaudeSessionMeta_UsesMessageTimestampNotFileMTime(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	testCwd := "/Users/test/projects/my-app"
	encodedCwd := strings.ReplaceAll(testCwd, "/", "-")
	projDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("Failed to create projDir: %v", err)
	}

	sessionUUID := "11111111-2222-3333-4444-555555555555"
	jsonlPath := filepath.Join(projDir, sessionUUID+".jsonl")

	msgTime1 := "2026-09-01T10:00:00.000Z"
	msgTime2 := "2026-09-01T10:05:30.123Z"
	expectedTime, err := time.Parse(time.RFC3339Nano, msgTime2)
	if err != nil {
		t.Fatalf("Failed to parse expectedTime: %v", err)
	}

	lines := []string{
		fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"Can you implement this?"},"timestamp":%q}`, msgTime1),
		fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done!"}]},"timestamp":%q}`, msgTime2),
	}
	if err := os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("Failed to write jsonl: %v", err)
	}

	// Deliberately set the file's filesystem mtime to current time or future time
	// (simulating Claude resuming/opening the file without sending prompts)
	fakeTouchTime := time.Now().Add(1 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(jsonlPath, fakeTouchTime, fakeTouchTime); err != nil {
		t.Fatalf("Failed to chtimes: %v", err)
	}

	// 1. ReadClaudeSessionMeta should return the genuine message timestamp, NOT the mtime
	meta := ReadClaudeSessionMeta(testCwd, sessionUUID)
	if meta == nil {
		t.Fatalf("Expected meta to be non-nil")
	}
	if !meta.LastMessageAt.Equal(expectedTime) {
		t.Errorf("Expected LastMessageAt to be %v, got %v (fakeTouchTime=%v)", expectedTime, meta.LastMessageAt, fakeTouchTime)
	}

	// 2. scanObservedSessions should resynchronize corrupted LastEventAt back to transcript timestamp
	dbPath := filepath.Join(tmpHome, "test_resync.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create db: %v", err)
	}
	defer db.Close()

	sessID := "claude-code:local:" + sessionUUID
	corruptedSession := &Session{
		ID:          sessID,
		Name:        "Test Session",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    sessionUUID,
		Cwd:         testCwd,
		ProjectKey:  GetProjectKey(testCwd),
		State:       StateIdle,
		LastEventAt: fakeTouchTime, // Corrupted by past reconnect
	}
	if err := db.SaveSession(corruptedSession); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	server := NewServer(db)
	mockP := &mockClaudeProvider{}
	server.RegisterProvider(mockP)
	server.scanObservedSessions(context.Background())

	restored, err := db.GetSession(sessID)
	if err != nil || restored == nil {
		t.Fatalf("Failed to retrieve session after scan: %v", err)
	}
	if !restored.LastEventAt.Equal(expectedTime) {
		t.Errorf("Expected scanObservedSessions to restore LastEventAt to %v, got %v", expectedTime, restored.LastEventAt)
	}
}

type mockClaudeProvider struct {
	MockProvider
}

func (m *mockClaudeProvider) Agent() string { return "claude-code" }
func (m *mockClaudeProvider) ReadSessionMetadata(cwd, nativeID string) *SessionMeta {
	return ReadClaudeSessionMeta(cwd, nativeID)
}

func TestReadClaudeSessionMeta_NoTimestampsDoesNotUseMTime(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	testCwd := "/Users/test/projects/legacy-app"
	encodedCwd := strings.ReplaceAll(testCwd, "/", "-")
	projDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("Failed to create projDir: %v", err)
	}

	sessionUUID := "22222222-3333-4444-5555-666666666666"
	jsonlPath := filepath.Join(projDir, sessionUUID+".jsonl")

	// Transcript lines without timestamp
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"legacy prompt"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"legacy reply"}]}}`,
	}
	if err := os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("Failed to write jsonl: %v", err)
	}

	meta := ReadClaudeSessionMeta(testCwd, sessionUUID)
	if meta == nil {
		t.Fatalf("Expected meta to be non-nil")
	}
	if !meta.LastMessageAt.IsZero() {
		t.Errorf("Expected LastMessageAt to be zero time when no timestamps present, got %v", meta.LastMessageAt)
	}
}

func TestInitDB_CleansUpDeletedTmuxName(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_deleted_tmux.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	// Insert corrupted session with (deleted) tmux_name
	corrupted := &Session{
		ID:       "claude-code:local:uuid-test-1234",
		Agent:    "claude-code",
		Host:     "local",
		NativeID: "uuid-test-1234",
		Name:     "Test Session",
		TmuxName: "(deleted)",
		State:    StateEnded,
	}
	if err := db.SaveSession(corrupted); err != nil {
		t.Fatalf("Failed to save corrupted session: %v", err)
	}

	// Manually ensure DB has '(deleted)' since SaveSession might have run
	_, err = db.db.Exec("UPDATE sessions SET tmux_name = '(deleted)' WHERE id = ?", corrupted.ID)
	if err != nil {
		t.Fatalf("Failed to set (deleted): %v", err)
	}

	db.Close()

	// Re-initialize DB (triggers InitDB self-healing migration)
	reopenedDB, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to reopen db: %v", err)
	}
	defer reopenedDB.Close()

	fixedSess, err := reopenedDB.GetSession(corrupted.ID)
	if err != nil || fixedSess == nil {
		t.Fatalf("Failed to retrieve session: %v", err)
	}

	expectedTmux := "ackbar-claude-code-uuid-test-1234"
	if fixedSess.TmuxName != expectedTmux {
		t.Errorf("Expected TmuxName to be healed to %q, got %q", expectedTmux, fixedSess.TmuxName)
	}
}

func TestIsIgnoredAgentCommand(t *testing.T) {
	tests := []struct {
		agent    string
		fullCmd  string
		expected bool
	}{
		// Claude Code utility subcommands and flags
		{"claude-code", "claude mcp list 2>&1 | head -30", true},
		{"claude-code", "claude mcp add --help 2>&1", true},
		{"claude-code", "claude --version", true},
		{"claude-code", "claude -v", true},
		{"claude-code", "claude -h", true},
		{"claude-code", "claude --help", true},
		{"claude-code", "claude doctor", true},
		{"claude-code", "claude update", true},
		{"claude-code", "claude upgrade", true},
		{"claude-code", "claude auth login", true},
		{"claude-code", "claude -p 'summarize this'", true},
		{"claude-code", "claude --print 'summarize this'", true},
		{"claude-code", "node /home/dev4u/.npm-global/bin/claude.js mcp list", true},
		{"claude-code", "/usr/local/bin/claude doctor", true},

		// Claude Code legitimate interactive sessions
		{"claude-code", "claude", false},
		{"claude-code", "claude --session-id 5bfb8b42-4f9e-44e9-b1ae-4bff549b5f4f", false},
		{"claude-code", "claude --resume c9278ce0-cddc-4968-9abf-7212a17fc723", false},
		{"claude-code", "claude -r c9278ce0-cddc-4968-9abf-7212a17fc723", false},

		// Antigravity utility subcommands and flags
		{"antigravity", "agy mcp list", true},
		{"antigravity", "agy --version", true},
		{"antigravity", "agy -v", true},
		{"antigravity", "agy -p 'prompt'", true},
		{"antigravity", "agy --print 'prompt'", true},
		{"antigravity", "agy --prompt 'prompt'", true},
		{"antigravity", "agy models", true},
		{"antigravity", "agy update", true},
		{"antigravity", "agy", false},
		{"antigravity", "agy --conversation 12345678-1234-1234-1234-123456789abc", false},

		// Codex utility subcommands and flags
		{"codex", "codex mcp list", true},
		{"codex", "codex --version", true},
		{"codex", "codex -v", true},
		{"codex", "codex login", true},
		{"codex", "codex -p 'prompt'", true},
		{"codex", "codex", false},
	}

	for _, tt := range tests {
		got := isIgnoredAgentCommand(tt.agent, tt.fullCmd)
		if got != tt.expected {
			t.Errorf("isIgnoredAgentCommand(%q, %q) = %v; expected %v", tt.agent, tt.fullCmd, got, tt.expected)
		}
	}
}

func TestScanObservedSessions_DeadProcSessionBroadcastsDeleted(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := InitDB(filepath.Join(tmpDir, "test_dead_proc.db"))
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}
	defer db.Close()

	s := NewServer(db)
	eventCh := make(chan *Session, 20)
	s.subMutex.Lock()
	s.subscribers[eventCh] = true
	s.subMutex.Unlock()

	// Create a dead proc session (using an extremely high non-existent PID)
	deadPID := 99999998
	sessID := fmt.Sprintf("local:observed:proc-%d", deadPID)
	deadSess := &Session{
		ID:       sessID,
		Agent:    "claude-code",
		Host:     "local",
		NativeID: fmt.Sprintf("proc-%d", deadPID),
		Name:     "claude-code (proc-99999998)",
		State:    StateWorking,
		PID:      deadPID,
	}
	if err := db.SaveSession(deadSess); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	// Run scanObservedSessions
	s.scanObservedSessions(context.Background())

	// Verify session is deleted from DB
	retrieved, _ := db.GetSession(sessID)
	if retrieved != nil {
		t.Errorf("Expected session %s to be deleted from DB, but still found: %+v", sessID, retrieved)
	}

	// Verify broadcast occurred with Deleted = true
	foundDeletedBroadcast := false
drainLoop:
	for {
		select {
		case sess := <-eventCh:
			if sess.ID == sessID && sess.Deleted && sess.Activity == "Deleted" {
				foundDeletedBroadcast = true
				break drainLoop
			}
		default:
			break drainLoop
		}
	}
	if !foundDeletedBroadcast {
		t.Errorf("Expected a deletion broadcast for %s with Deleted=true", sessID)
	}
}

func TestHook_DropNonUUIDClaudeCodeSessionID(t *testing.T) {
	dbFile := "./test_drop_non_uuid.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	mockP := &mockDynamicProvider{agentName: "claude-code"}
	server.RegisterProvider(mockP)

	for _, invalidID := range []string{"test", "default", "proc-123", "", "invalid-uuid"} {
		mockP.event = &Event{
			Agent:       "claude-code",
			NativeID:    invalidID,
			Cwd:         "/home/dev4u/Work/repo",
			EventName:   "PreToolUse",
			State:       StateWorking,
			LastEventAt: time.Now(),
		}
		body, _ := json.Marshal(map[string]interface{}{
			"session_id": invalidID,
			"cwd":        "/home/dev4u/Work/repo",
		})
		server.processHookEvent(mockP, "PreToolUse", "local", body)

		sess, _ := db.GetSession(fmt.Sprintf("claude-code:local:%s", invalidID))
		if sess != nil {
			t.Errorf("Expected non-UUID session ID %q to be dropped, but found in DB: %+v", invalidID, sess)
		}
	}
}

func TestTurnRotation_ConcurrentSessionDoesNotHijackLivingActiveManaged(t *testing.T) {
	dbFile := "./test_concurrent_no_hijack.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	mockP := &mockDynamicProvider{agentName: "claude-code"}
	server.RegisterProvider(mockP)

	// 1. Existing active managed session running with live PID (current test process PID)
	livePID := os.Getpid()
	sess1ID := "claude-code:local:11111111-1111-1111-1111-111111111111"
	sess1 := &Session{
		ID:          sess1ID,
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "11111111-1111-1111-1111-111111111111",
		Cwd:         "/home/dev4u/Work/ngl-android",
		Name:        "Original Active Session",
		Managed:     true,
		TmuxName:    "ackbar-claude-code-11111111-1111-1111-1111-111111111111",
		NodePath:    "Work/ngl-android",
		ProjectKey:  "ngl-android",
		State:       StateWorking,
		PID:         livePID,
		StartedAt:   time.Now().Add(-1 * time.Hour),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(sess1); err != nil {
		t.Fatalf("Failed to save sess1: %v", err)
	}

	// 2. Incoming hook for a concurrent session in the SAME cwd, but different UUID
	sess2UUID := "22222222-2222-2222-2222-222222222222"
	mockP.event = &Event{
		Agent:       "claude-code",
		NativeID:    sess2UUID,
		Cwd:         "/home/dev4u/Work/ngl-android",
		EventName:   "SessionStart",
		State:       StateIdle,
		Activity:    "Session started",
		LastEventAt: time.Now(),
	}
	body, _ := json.Marshal(map[string]interface{}{
		"session_id": sess2UUID,
		"cwd":        "/home/dev4u/Work/ngl-android",
	})
	server.processHookEvent(mockP, "SessionStart", "local", body)

	// 3. Verify sess1 was NOT hijacked or archived as StateEnded
	dbSess1, err := db.GetSession(sess1ID)
	if err != nil || dbSess1 == nil {
		t.Fatalf("Failed to retrieve sess1 from DB: %v", err)
	}
	if !dbSess1.Managed {
		t.Errorf("Expected sess1 to remain Managed, got %v", dbSess1.Managed)
	}
	if dbSess1.State == StateEnded {
		t.Errorf("Expected sess1 state to remain active, but was erroneously marked StateEnded")
	}
	if dbSess1.Name != "Original Active Session" {
		t.Errorf("Expected sess1 name to remain unchanged, got %q", dbSess1.Name)
	}
	if dbSess1.TmuxName != "ackbar-claude-code-11111111-1111-1111-1111-111111111111" {
		t.Errorf("Expected sess1 TmuxName to remain intact, got %q", dbSess1.TmuxName)
	}

	// 4. Verify sess2 was created as an independent unmanaged session, without stealing sess1's supervisor
	sess2ID := fmt.Sprintf("claude-code:local:%s", sess2UUID)
	dbSess2, err := db.GetSession(sess2ID)
	if err != nil || dbSess2 == nil {
		t.Fatalf("Failed to retrieve sess2 from DB: %v", err)
	}
	if dbSess2.Managed {
		t.Errorf("Expected sess2 to NOT be managed (did not hijack sess1 supervisor)")
	}
	if dbSess2.TmuxName != "" {
		t.Errorf("Expected sess2 TmuxName to be empty, got %q", dbSess2.TmuxName)
	}
}

type namedMockProvider struct {
	MockProvider
	agentName string
}

func (p *namedMockProvider) Agent() string {
	return p.agentName
}

func (p *namedMockProvider) DisplayName() string {
	return p.agentName
}

func TestAgentDiscoveryDeterministicOrdering(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_discovery.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.RegisterProvider(&namedMockProvider{agentName: "codex"})
	server.RegisterProvider(&namedMockProvider{agentName: "antigravity"})
	server.RegisterProvider(&namedMockProvider{agentName: "claude-code"})
	server.RegisterProvider(&namedMockProvider{agentName: "custom-agent"})

	for iter := 0; iter < 10; iter++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/agents/discovery", nil)
		w := httptest.NewRecorder()
		server.handleAgentDiscovery(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", w.Code)
		}

		var discovery []AgentDiscoveryResult
		if err := json.Unmarshal(w.Body.Bytes(), &discovery); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}

		if len(discovery) != 4 {
			t.Fatalf("Expected 4 discovery results, got %d", len(discovery))
		}

		expectedOrder := []string{"claude-code", "antigravity", "codex", "custom-agent"}
		for i, exp := range expectedOrder {
			if discovery[i].Agent != exp {
				t.Fatalf("Iter %d: expected agent[%d] to be %q, got %q", iter, i, exp, discovery[i].Agent)
			}
		}
	}
}

func TestIsAntigravitySubagent(t *testing.T) {
	tmpDir := t.TempDir()
	convID := "57ae3db6-0595-4f7e-aff7-ec6494aef363"

	// 1. When no annotations, no metadata, no brain logs exist -> false (do not assume subagent!)
	if isAntigravitySubagent(tmpDir, convID) {
		t.Fatalf("Expected false when no files exist, got true")
	}

	// 2. When annotation file exists -> false
	annoDir := filepath.Join(tmpDir, ".gemini", "antigravity", "annotations")
	_ = os.MkdirAll(annoDir, 0755)
	_ = os.WriteFile(filepath.Join(annoDir, convID+".pbtxt"), []byte("title: test"), 0644)
	if isAntigravitySubagent(tmpDir, convID) {
		t.Fatalf("Expected false when annotation file exists, got true")
	}
	_ = os.Remove(filepath.Join(annoDir, convID+".pbtxt"))

	// 3. When conversation_metadata.json marks it internal -> true
	cacheDir := filepath.Join(tmpDir, ".gemini", "antigravity", "cache")
	_ = os.MkdirAll(cacheDir, 0755)
	metaContent := fmt.Sprintf(`{"conversations": {"%s": {"is_internal": true}}}`, convID)
	_ = os.WriteFile(filepath.Join(cacheDir, "conversation_metadata.json"), []byte(metaContent), 0644)
	if !isAntigravitySubagent(tmpDir, convID) {
		t.Fatalf("Expected true when conversation_metadata marks is_internal: true, got false")
	}

	// 4. When conversation_metadata.json marks it NOT internal -> false
	metaContentFalse := fmt.Sprintf(`{"conversations": {"%s": {"is_internal": false}}}`, convID)
	_ = os.WriteFile(filepath.Join(cacheDir, "conversation_metadata.json"), []byte(metaContentFalse), 0644)
	if isAntigravitySubagent(tmpDir, convID) {
		t.Fatalf("Expected false when conversation_metadata marks is_internal: false, got true")
	}
	_ = os.Remove(filepath.Join(cacheDir, "conversation_metadata.json"))

	// 5. When transcript contains subagent invocation markers -> true
	brainLogDir := filepath.Join(tmpDir, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	_ = os.MkdirAll(brainLogDir, 0755)
	logContent := `{"type": "USER_INPUT", "content": "You are a subagent helping with tasks"}` + "\n"
	_ = os.WriteFile(filepath.Join(brainLogDir, "transcript.jsonl"), []byte(logContent), 0644)
	if !isAntigravitySubagent(tmpDir, convID) {
		t.Fatalf("Expected true when transcript contains subagent marker, got false")
	}
}

func TestDatabaseSanitation_AntigravityManagedProtected(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	sess := &Session{
		ID:       "antigravity:local:57ae3db6-0595-4f7e-aff7-ec6494aef363",
		Agent:    "antigravity",
		Host:     "local",
		NativeID: "57ae3db6-0595-4f7e-aff7-ec6494aef363",
		Cwd:      "/tmp/project",
		State:    StateIdle,
		Managed:  true,
		Activity: "Active session",
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Trigger scanObservedSessions
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server.scanObservedSessions(ctx)

	// Session must still exist in DB!
	stored, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if stored == nil {
		t.Fatalf("Managed Antigravity session was erroneously deleted by sanitation!")
	}
}

func TestHandleSpawn_PersistsTreeNode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.RegisterProvider(&namedMockProvider{agentName: "mock-agent"})

	cwd := filepath.Join(t.TempDir(), "my-project")
	spawnPayload := map[string]string{
		"agent":     "mock-agent",
		"cwd":       cwd,
		"node_path": "Personal/Ackbar",
	}
	body, _ := json.Marshal(spawnPayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleSpawn(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	node, err := db.GetNode("Personal/Ackbar")
	if err != nil {
		t.Fatalf("GetNode failed: %v", err)
	}
	if node == nil {
		t.Fatalf("TreeNode 'Personal/Ackbar' was not persisted in DB")
	}
	if node.ProjectDir != cwd {
		t.Fatalf("Expected ProjectDir %q, got %q", cwd, node.ProjectDir)
	}
}

func TestHandleSpawn_WithPrompt(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_spawn_prompt.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.RegisterProvider(&namedMockProvider{agentName: "mock-agent"})

	cwd := filepath.Join(t.TempDir(), "my-project-prompt")
	spawnPayload := map[string]string{
		"agent":     "mock-agent",
		"cwd":       cwd,
		"node_path": "Personal/Ackbar",
		"prompt":    "Initial task prompt to inject",
	}
	body, _ := json.Marshal(spawnPayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleSpawn(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestIsAntigravitySubagent_ReviewerPromptsAndMessages(t *testing.T) {
	tempHome := t.TempDir()
	convIDReviewer := "11111111-2222-3333-4444-555555555555"
	convIDSecurity := "22222222-3333-4444-5555-666666666666"
	convIDMessage := "33333333-4444-5555-6666-777777777777"
	convIDAdvisory := "55555555-6666-7777-8888-999999999999"
	convIDClean := "66666666-7777-8888-9999-000000000000"
	convIDUser := "44444444-5555-6666-7777-888888888888"

	// 1. Reviewer A transcript
	revLogDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", convIDReviewer, ".system_generated", "logs")
	_ = os.MkdirAll(revLogDir, 0755)
	_ = os.WriteFile(filepath.Join(revLogDir, "transcript.jsonl"), []byte(`{"type":"USER_INPUT","content":"You are Reviewer A (Context-Aware Code Reviewer). Inspect changes..."}`), 0644)

	// 2. Dedicated Security Reviewer transcript
	secLogDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", convIDSecurity, ".system_generated", "logs")
	_ = os.MkdirAll(secLogDir, 0755)
	_ = os.WriteFile(filepath.Join(secLogDir, "transcript.jsonl"), []byte(`{"type":"USER_INPUT","content":"You are the Dedicated Security Reviewer. Perform a focused audit..."}`), 0644)

	// 3. Message dispatch subagent
	msgDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", convIDMessage, ".system_generated", "messages")
	_ = os.MkdirAll(msgDir, 0755)
	_ = os.WriteFile(filepath.Join(msgDir, "msg-1.json"), []byte(`{"sourceMetadata":{"tool":{"conversationId":"root-conv-123","name":"send_message"}}}`), 0644)

	// 4. Advisory read-only subagent
	advLogDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", convIDAdvisory, ".system_generated", "logs")
	_ = os.MkdirAll(advLogDir, 0755)
	_ = os.WriteFile(filepath.Join(advLogDir, "transcript.jsonl"), []byte(`{"type":"USER_INPUT","content":"ADVISORY: You are read-only. Do not edit files, run commands, or invoke subagents."}`), 0644)

	// 5. Clean reviewer subagent
	cleanLogDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", convIDClean, ".system_generated", "logs")
	_ = os.MkdirAll(cleanLogDir, 0755)
	_ = os.WriteFile(filepath.Join(cleanLogDir, "transcript.jsonl"), []byte(`{"type":"USER_INPUT","content":"You are the clean-reviewer for Project Ackbar. Audit git diff..."}`), 0644)

	// 6. Real user session with annotation
	annoDir := filepath.Join(tempHome, ".gemini", "antigravity", "annotations")
	_ = os.MkdirAll(annoDir, 0755)
	_ = os.WriteFile(filepath.Join(annoDir, convIDUser+".pbtxt"), []byte(`title:"My Real Task"`), 0644)
	userLogDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", convIDUser, ".system_generated", "logs")
	_ = os.MkdirAll(userLogDir, 0755)
	_ = os.WriteFile(filepath.Join(userLogDir, "transcript.jsonl"), []byte(`{"type":"USER_INPUT","content":"Please build the mobile UI."}`), 0644)

	if !isAntigravitySubagent(tempHome, convIDReviewer) {
		t.Errorf("expected convIDReviewer to be detected as subagent")
	}
	if !isAntigravitySubagent(tempHome, convIDSecurity) {
		t.Errorf("expected convIDSecurity to be detected as subagent")
	}
	if !isAntigravitySubagent(tempHome, convIDMessage) {
		t.Errorf("expected convIDMessage to be detected as subagent")
	}
	if !isAntigravitySubagent(tempHome, convIDAdvisory) {
		t.Errorf("expected convIDAdvisory to be detected as subagent")
	}
	if !isAntigravitySubagent(tempHome, convIDClean) {
		t.Errorf("expected convIDClean to be detected as subagent")
	}
	if isAntigravitySubagent(tempHome, convIDUser) {
		t.Errorf("expected convIDUser NOT to be detected as subagent")
	}
}

func TestIsAntigravitySubagent_ParentSubagentRegistry(t *testing.T) {
	tempHome := t.TempDir()
	parentID := "11111111-aaaa-bbbb-cccc-111111111111"
	childID := "22222222-bbbb-cccc-dddd-222222222222"

	// Create child record under parent's .system_generated/subagents/ directory
	subagentDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", parentID, ".system_generated", "subagents")
	if err := os.MkdirAll(subagentDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subagentDir, childID+".json"), []byte(`{"id":"`+childID+`","role":"Security Reviewer"}`), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if !isAntigravitySubagent(tempHome, childID) {
		t.Fatalf("expected childID to be recognized as subagent from parent registry")
	}
	if isAntigravitySubagent(tempHome, parentID) {
		t.Fatalf("expected parentID NOT to be recognized as subagent")
	}

	// Test precomputed map support
	precomputed := map[string]bool{
		strings.ToLower(childID): true,
	}
	if !isAntigravitySubagent(tempHome, childID, precomputed) {
		t.Fatalf("expected childID to be recognized via precomputed map")
	}
	if isAntigravitySubagent(tempHome, parentID, precomputed) {
		t.Fatalf("expected parentID NOT to be recognized via precomputed map")
	}
}

func TestDatabaseSanitation_AntigravitySubagentsPurged(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dbPath := filepath.Join(tempHome, "test_sanitation.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	parentID := "11111111-aaaa-bbbb-cccc-111111111111"
	subagentID := "33333333-cccc-dddd-eeee-333333333333"
	activeSubID := "33333333-cccc-dddd-eeee-444444444444"
	managedSubID := "33333333-cccc-dddd-eeee-555555555555"
	alivePIDSubID := "33333333-cccc-dddd-eeee-666666666666"
	userSessID := "44444444-dddd-eeee-ffff-444444444444"

	// Register subagents under parent
	subagentDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", parentID, ".system_generated", "subagents")
	_ = os.MkdirAll(subagentDir, 0755)
	_ = os.WriteFile(filepath.Join(subagentDir, subagentID+".json"), []byte(`{}`), 0644)
	_ = os.WriteFile(filepath.Join(subagentDir, activeSubID+".json"), []byte(`{}`), 0644)
	_ = os.WriteFile(filepath.Join(subagentDir, managedSubID+".json"), []byte(`{}`), 0644)
	_ = os.WriteFile(filepath.Join(subagentDir, alivePIDSubID+".json"), []byte(`{}`), 0644)

	// 1. Ended, unmanaged subagent -> MUST be purged
	subSess := &Session{
		ID:       "antigravity:local:" + subagentID,
		Agent:    "antigravity",
		Host:     "local",
		NativeID: subagentID,
		Cwd:      tempHome,
		State:    StateEnded,
		Managed:  false,
	}
	if err := db.SaveSession(subSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 2. Active subagent (StateWorking) -> MUST NOT be purged
	activeSubSess := &Session{
		ID:       "antigravity:local:" + activeSubID,
		Agent:    "antigravity",
		Host:     "local",
		NativeID: activeSubID,
		Cwd:      tempHome,
		State:    StateWorking,
		Managed:  false,
	}
	if err := db.SaveSession(activeSubSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 3. Managed subagent -> MUST NOT be purged
	managedSubSess := &Session{
		ID:       "antigravity:local:" + managedSubID,
		Agent:    "antigravity",
		Host:     "local",
		NativeID: managedSubID,
		Cwd:      tempHome,
		State:    StateEnded,
		Managed:  true,
	}
	if err := db.SaveSession(managedSubSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 4. Subagent with alive PID -> MUST NOT be purged
	alivePIDSubSess := &Session{
		ID:       "antigravity:local:" + alivePIDSubID,
		Agent:    "antigravity",
		Host:     "local",
		NativeID: alivePIDSubID,
		Cwd:      tempHome,
		State:    StateEnded,
		PID:      os.Getpid(),
		Managed:  false,
	}
	if err := db.SaveSession(alivePIDSubSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 5. Legitimate user session (ended, unmanaged) -> MUST NOT be purged
	userSess := &Session{
		ID:       "antigravity:local:" + userSessID,
		Agent:    "antigravity",
		Host:     "local",
		NativeID: userSessID,
		Cwd:      tempHome,
		State:    StateEnded,
		Managed:  false,
	}
	if err := db.SaveSession(userSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Run scanObservedSessions
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server.scanObservedSessions(ctx)

	// Verify subagent session was purged from DB
	subAfter, err := db.GetSession(subSess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if subAfter != nil {
		t.Fatalf("expected subagent session %s to be purged from DB, but it still exists", subSess.ID)
	}

	// Verify active subagent was NOT purged
	activeAfter, err := db.GetSession(activeSubSess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if activeAfter == nil {
		t.Fatalf("expected active subagent session %s to remain in DB, but it was purged", activeSubSess.ID)
	}

	// Verify managed subagent was NOT purged
	managedAfter, err := db.GetSession(managedSubSess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if managedAfter == nil {
		t.Fatalf("expected managed subagent session %s to remain in DB, but it was purged", managedSubSess.ID)
	}

	// Verify alive PID subagent was NOT purged
	aliveAfter, err := db.GetSession(alivePIDSubSess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if aliveAfter == nil {
		t.Fatalf("expected alive PID subagent session %s to remain in DB, but it was purged", alivePIDSubSess.ID)
	}

	// Verify user session was NOT purged
	userAfter, err := db.GetSession(userSess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if userAfter == nil {
		t.Fatalf("expected user session %s to remain in DB, but it was purged", userSess.ID)
	}
}

func TestSessionControl_EmptyCwdFallbackToNodePath(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_fallback.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.RegisterProvider(&namedMockProvider{agentName: "mock-agent"})

	projectDir := filepath.Join(tempDir, "my-project")
	_ = os.MkdirAll(projectDir, 0755)

	// Save TreeNode with ProjectDir
	_ = db.SaveNode(&TreeNode{
		Path:       "Work/MyProject",
		ProjectDir: projectDir,
	})

	// Save session with empty Cwd and valid NodePath
	sessID := "mock-agent:local:test-fallback-sess"
	expectedTmuxName := "ackbar-mock-agent-test-fallback-sess"
	defer func() {
		_ = tmux.Kill(context.Background(), expectedTmuxName)
	}()

	sess := &Session{
		ID:       sessID,
		Agent:    "mock-agent",
		Host:     "local",
		NativeID: "test-fallback-sess",
		Cwd:      "", // Empty!
		NodePath: "Work/MyProject",
		State:    StateEnded,
		Managed:  true,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 1. Test resume fallback
	reqResume := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/sessions/control?id=%s&action=resume", sessID), nil)
	wResume := httptest.NewRecorder()
	server.handleSessionControl(wResume, reqResume)

	if wResume.Code != http.StatusOK {
		t.Fatalf("expected resume with empty Cwd to succeed via NodePath fallback, got %d: %s", wResume.Code, wResume.Body.String())
	}

	updatedResume, _ := db.GetSession(sessID)
	if updatedResume == nil || updatedResume.Cwd != projectDir {
		t.Fatalf("expected Cwd to be populated with %q, got %+v", projectDir, updatedResume)
	}

	// 2. Test restart fallback
	sess.Cwd = ""
	_ = db.SaveSession(sess)
	reqRestart := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/sessions/control?id=%s&action=restart", sessID), nil)
	wRestart := httptest.NewRecorder()
	server.handleSessionControl(wRestart, reqRestart)

	if wRestart.Code != http.StatusOK {
		t.Fatalf("expected restart with empty Cwd to succeed via NodePath fallback, got %d: %s", wRestart.Code, wRestart.Body.String())
	}

	updatedRestart, _ := db.GetSession(sessID)
	if updatedRestart == nil || updatedRestart.Cwd != projectDir {
		t.Fatalf("expected Cwd to be populated with %q, got %+v", projectDir, updatedRestart)
	}
}

func TestScanObservedSessions_AssignsAndBackfillsNodePath(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dbPath := filepath.Join(tempHome, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Register tree node Personal/Ackbar
	_ = db.SaveNode(&TreeNode{
		Path:       "Personal/Ackbar",
		ProjectDir: "", // Leaf fallback match
	})

	server := NewServer(db)

	// 1. Create a disk Antigravity session with Ackbar workspace
	convID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	convDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", convID)
	logDir := filepath.Join(convDir, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0755)

	targetCwd := filepath.Join(tempHome, "Work", "Ackbar")
	_ = os.MkdirAll(targetCwd, 0755)
	_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(fmt.Sprintf(`%s -> marcinbak/ackbar
{"type":"USER_INPUT","content":"Refactor the codebase"}`, targetCwd)), 0644)

	// Create user annotation
	annoDir := filepath.Join(tempHome, ".gemini", "antigravity", "annotations")
	_ = os.MkdirAll(annoDir, 0755)
	_ = os.WriteFile(filepath.Join(annoDir, convID+".pbtxt"), []byte(`title:"Refactoring Task"`), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	server.scanObservedSessions(ctx)

	sessID := fmt.Sprintf("antigravity:local:%s", convID)
	sess, err := db.GetSession(sessID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if sess == nil {
		t.Fatalf("Expected session to be scanned into DB, but was nil")
	}
	if sess.NodePath != "Personal/Ackbar" {
		t.Errorf("Expected newly scanned session NodePath to be 'Personal/Ackbar', got %q", sess.NodePath)
	}

	// 2. Clear NodePath on an existing ended session and verify scanObservedSessions backfills it
	sess.NodePath = ""
	_ = db.SaveSession(sess)

	server.scanObservedSessions(ctx)

	updated, err := db.GetSession(sessID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if updated.NodePath != "Personal/Ackbar" {
		t.Errorf("Expected backfilled NodePath to be 'Personal/Ackbar', got %q", updated.NodePath)
	}
}

func TestScanObservedSessions_PurgesReviewerSubagents(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dbPath := filepath.Join(tempHome, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// Create a reviewer subagent on disk
	subID := "55555555-6666-7777-8888-999999999999"
	subLogDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", subID, ".system_generated", "logs")
	_ = os.MkdirAll(subLogDir, 0755)
	_ = os.WriteFile(filepath.Join(subLogDir, "transcript.jsonl"), []byte(`{"type":"USER_INPUT","content":"You are Reviewer B (Clean-Context Code Quality Reviewer)."}`), 0644)

	// Seed this subagent into the database as an ended session
	sessID := fmt.Sprintf("antigravity:local:%s", subID)
	subSess := &Session{
		ID:       sessID,
		Name:     "You are Reviewer B (Clean-Context Code Quality Reviewer).",
		Agent:    "antigravity",
		Host:     "local",
		NativeID: subID,
		Cwd:      "/some/path",
		State:    StateEnded,
		Managed:  false,
	}
	_ = db.SaveSession(subSess)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run scan & sanitation
	server.scanObservedSessions(ctx)

	// Subagent must be purged from database
	purged, err := db.GetSession(sessID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if purged != nil {
		t.Fatalf("Expected reviewer subagent to be purged from DB, but still exists: %+v", purged)
	}
}

func TestAccountsAPI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "accounts_api_test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// 1. GET /v1/accounts initially returns seeded default accounts
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts", nil)
	w := httptest.NewRecorder()
	server.handleAccounts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	var accounts []*AgentAccount
	if err := json.Unmarshal(w.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("Failed to decode accounts: %v", err)
	}
	if len(accounts) < 2 {
		t.Fatalf("Expected at least default accounts, got %d", len(accounts))
	}

	// 2. POST /v1/accounts to create a new profile
	createPayload := map[string]interface{}{
		"agent":        "claude-code",
		"name":         "work",
		"display_name": "Work Team",
		"config_dir":   filepath.Join(t.TempDir(), "claude-work"),
		"env": map[string]string{
			"ANTHROPIC_API_KEY": "sk-work-test",
		},
		"is_default": true,
	}
	body, _ := json.Marshal(createPayload)
	reqPost := httptest.NewRequest(http.MethodPost, "/v1/accounts", bytes.NewBuffer(body))
	wPost := httptest.NewRecorder()
	server.handleAccounts(wPost, reqPost)
	if wPost.Code != http.StatusOK {
		t.Fatalf("POST /v1/accounts failed (%d): %s", wPost.Code, wPost.Body.String())
	}

	var created AgentAccount
	_ = json.Unmarshal(wPost.Body.Bytes(), &created)
	if created.Name != "work" || created.ID != "claude-code:work" {
		t.Errorf("Expected ID claude-code:work, got %s", created.ID)
	}
	if !created.IsLoggedIn {
		t.Errorf("Expected IsLoggedIn true due to ANTHROPIC_API_KEY, got false")
	}

	// Verify profile directory was initialized with settings.json hook
	settingsFile := filepath.Join(created.ConfigDir, "settings.json")
	if _, err := os.Stat(settingsFile); err != nil {
		t.Errorf("Expected settings.json created at %s, got error: %v", settingsFile, err)
	}

	// 3. GET /v1/accounts?agent=claude-code
	reqList := httptest.NewRequest(http.MethodGet, "/v1/accounts?agent=claude-code", nil)
	wList := httptest.NewRecorder()
	server.handleAccounts(wList, reqList)
	var claudeAccounts []*AgentAccount
	_ = json.Unmarshal(wList.Body.Bytes(), &claudeAccounts)
	if len(claudeAccounts) != 2 {
		t.Fatalf("Expected 2 claude accounts (default and work), got %d", len(claudeAccounts))
	}

	// 4. Test delete default vs custom account
	reqDelDefault := httptest.NewRequest(http.MethodDelete, "/v1/accounts/claude-code:default", nil)
	wDelDefault := httptest.NewRecorder()
	server.handleAccountOps(wDelDefault, reqDelDefault)
	if wDelDefault.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 when deleting default account, got %d", wDelDefault.Code)
	}

	reqDelWork := httptest.NewRequest(http.MethodDelete, "/v1/accounts/claude-code:work", nil)
	wDelWork := httptest.NewRecorder()
	server.handleAccountOps(wDelWork, reqDelWork)
	if wDelWork.Code != http.StatusOK {
		t.Errorf("Expected 200 when deleting work account, got %d", wDelWork.Code)
	}
}

func TestHostIdentity_VersionAndSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_host_identity.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	// Initial default should be "local"
	if server.HostName() != "local" {
		t.Errorf("Expected initial HostName 'local', got %q", server.HostName())
	}
	if server.DisplayName() != "local" {
		t.Errorf("Expected initial DisplayName 'local', got %q", server.DisplayName())
	}

	// 1. Set host identity
	server.SetHostIdentity("macbook", "MacBook Air")
	if server.HostName() != "macbook" {
		t.Errorf("Expected HostName 'macbook', got %q", server.HostName())
	}
	if server.DisplayName() != "MacBook Air" {
		t.Errorf("Expected DisplayName 'MacBook Air', got %q", server.DisplayName())
	}

	// 2. Query /v1/version
	reqVer := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	wVer := httptest.NewRecorder()
	server.handleVersion(wVer, reqVer)
	if wVer.Code != http.StatusOK {
		t.Fatalf("GET /v1/version failed: %d", wVer.Code)
	}
	var verResp map[string]interface{}
	if err := json.Unmarshal(wVer.Body.Bytes(), &verResp); err != nil {
		t.Fatalf("Failed to parse /v1/version response: %v", err)
	}
	if verResp["host"] != "macbook" {
		t.Errorf("Expected version response host 'macbook', got %v", verResp["host"])
	}
	if verResp["display_name"] != "MacBook Air" {
		t.Errorf("Expected version response display_name 'MacBook Air', got %v", verResp["display_name"])
	}

	// 3. Query /v1/settings GET
	reqSettingsGet := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	wSettingsGet := httptest.NewRecorder()
	server.handleSettings(wSettingsGet, reqSettingsGet)
	if wSettingsGet.Code != http.StatusOK {
		t.Fatalf("GET /v1/settings failed: %d", wSettingsGet.Code)
	}
	var settingsResp map[string]string
	if err := json.Unmarshal(wSettingsGet.Body.Bytes(), &settingsResp); err != nil {
		t.Fatalf("Failed to parse /v1/settings response: %v", err)
	}
	if settingsResp["host_name"] != "macbook" {
		t.Errorf("Expected settings host_name 'macbook', got %q", settingsResp["host_name"])
	}
	if settingsResp["display_name"] != "MacBook Air" {
		t.Errorf("Expected settings display_name 'MacBook Air', got %q", settingsResp["display_name"])
	}

	// 4. Update via /v1/settings POST
	updatePayload := map[string]string{
		"host_name":    "laptop",
		"display_name": "My Work Laptop",
	}
	body, _ := json.Marshal(updatePayload)
	reqSettingsPost := httptest.NewRequest(http.MethodPost, "/v1/settings", bytes.NewReader(body))
	wSettingsPost := httptest.NewRecorder()
	server.handleSettings(wSettingsPost, reqSettingsPost)
	if wSettingsPost.Code != http.StatusOK {
		t.Fatalf("POST /v1/settings failed: %d", wSettingsPost.Code)
	}
	if server.HostName() != "laptop" {
		t.Errorf("Expected updated HostName 'laptop', got %q", server.HostName())
	}
	if server.DisplayName() != "My Work Laptop" {
		t.Errorf("Expected updated DisplayName 'My Work Laptop', got %q", server.DisplayName())
	}

	// 5. Verify persisted in DB
	savedHost, _ := db.GetSetting("host_name")
	savedDisp, _ := db.GetSetting("display_name")
	if savedHost != "laptop" || savedDisp != "My Work Laptop" {
		t.Errorf("Expected settings in DB to be laptop / My Work Laptop, got %q / %q", savedHost, savedDisp)
	}
}

func TestHostIdentity_HandleSpawn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_spawn_identity.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.SetHostIdentity("macbook", "MacBook Pro")
	server.RegisterProvider(&namedMockProvider{agentName: "mock-agent"})

	cwd := filepath.Join(t.TempDir(), "project-spawn")
	spawnPayload := map[string]string{
		"agent": "mock-agent",
		"cwd":   cwd,
	}
	body, _ := json.Marshal(spawnPayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleSpawn(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse spawn response: %v", err)
	}

	if resp["host"] != "macbook" {
		t.Errorf("Expected spawn response host 'macbook', got %v", resp["host"])
	}
	sessID, ok := resp["id"].(string)
	if !ok || !strings.HasPrefix(sessID, "mock-agent:macbook:") {
		t.Errorf("Expected session ID prefix 'mock-agent:macbook:', got %v", resp["id"])
	}
}

func TestIsLocalHost(t *testing.T) {
	server := NewServer(nil)
	server.SetHostIdentity("macbook", "MacBook Pro")

	tests := []struct {
		host     string
		expected bool
	}{
		{"", true},
		{"local", true},
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"macbook", true},
		{"dev4u@macbook", true},
		{"devbox", false},
		{"dev4u@legion", false},
		{"remote-host", false},
	}

	for _, tt := range tests {
		got := server.isLocalHost(tt.host)
		if got != tt.expected {
			t.Errorf("isLocalHost(%q) = %v; want %v", tt.host, got, tt.expected)
		}
	}
}

func TestResolveSSHTarget(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_ssh_target.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	_ = db.SaveHost(&HostRecord{
		Name:      "dev4u@legion",
		URL:       "http://127.0.0.1:7778",
		SSHTarget: "dev4u@legion",
	})

	if target := server.resolveSSHTarget("legion"); target != "dev4u@legion" {
		t.Errorf("Expected resolveSSHTarget('legion') = 'dev4u@legion', got %q", target)
	}
	if target := server.resolveSSHTarget("dev4u@legion"); target != "dev4u@legion" {
		t.Errorf("Expected resolveSSHTarget('dev4u@legion') = 'dev4u@legion', got %q", target)
	}
	if target := server.resolveSSHTarget("other-box"); target != "other-box" {
		t.Errorf("Expected resolveSSHTarget('other-box') = 'other-box', got %q", target)
	}
}

func TestSpawnSSHTunnel_FailureHandling(t *testing.T) {
	start := time.Now()
	err := spawnSSHTunnel("19999", "nonexistent-test-host.invalid")
	dur := time.Since(start)

	if err == nil {
		t.Errorf("Expected spawnSSHTunnel to fail for nonexistent host, but got nil")
	}

	if dur > 10*time.Second {
		t.Errorf("spawnSSHTunnel took too long: %v", dur)
	}
}

func TestHosts_EnrichedWithHealth(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_hosts_health.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	_ = db.SaveHost(&HostRecord{
		Name:      "dev4u@legion",
		URL:       "http://127.0.0.1:7778",
		SSHTarget: "dev4u@legion",
	})

	server.updateHostHealth("dev4u@legion", true, "20260916.04", "Legion", 42)

	req := httptest.NewRequest(http.MethodGet, "/v1/hosts", nil)
	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var hosts []HostRecord
	if err := json.Unmarshal(w.Body.Bytes(), &hosts); err != nil {
		t.Fatalf("Failed to decode hosts JSON: %v", err)
	}

	if len(hosts) != 1 {
		t.Fatalf("Expected 1 host, got %d", len(hosts))
	}

	h := hosts[0]
	if !h.Online {
		t.Errorf("Expected host to be reported online")
	}
	if h.Version != "20260916.04" {
		t.Errorf("Expected version 20260916.04, got %q", h.Version)
	}
	if h.DisplayName != "Legion" {
		t.Errorf("Expected display name Legion, got %q", h.DisplayName)
	}
	if h.LatencyMs != 42 {
		t.Errorf("Expected latency 42ms, got %d", h.LatencyMs)
	}
}

func TestHandleSessionSubagents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_subagents.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	sessionID := "claude-code:local:test-subagents-sess"
	sess := &Session{
		ID:        sessionID,
		Agent:     "claude-code",
		Host:      "local",
		NativeID:  "test-subagents-sess",
		Cwd:       "/tmp/test",
		State:     StateWorking,
		StartedAt: time.Now(),
	}
	_ = db.SaveSession(sess)

	// Initially 0 running subagents
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/subagents?id="+sessionID, nil)
	w := httptest.NewRecorder()
	server.handleSessionSubagents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		SessionID    string         `json:"session_id"`
		RunningCount int            `json:"running_count"`
		Subagents    []SubagentInfo `json:"subagents"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.RunningCount != 0 || len(res.Subagents) != 0 {
		t.Fatalf("Expected 0 subagents, got %d", res.RunningCount)
	}

	// Add an active subagent in memory
	sub := &SubagentInfo{
		ID:        "sub-1",
		Name:      "Reviewer A",
		Role:      "research",
		Prompt:    "Verify changes",
		State:     "running",
		StartedAt: time.Now(),
	}
	server.addSubagent(sessionID, sess.NativeID, sub)

	// Verify /v1/sessions/subagents returns it
	w2 := httptest.NewRecorder()
	server.handleSessionSubagents(w2, req)
	_ = json.Unmarshal(w2.Body.Bytes(), &res)
	if res.RunningCount != 1 || len(res.Subagents) != 1 {
		t.Fatalf("Expected 1 running subagent, got %d", res.RunningCount)
	}
	if res.Subagents[0].Name != "Reviewer A" || res.Subagents[0].Prompt != "Verify changes" {
		t.Errorf("Unexpected subagent details: %+v", res.Subagents[0])
	}

	// Remove subagent
	server.removeSubagent(sessionID, sess.NativeID, "sub-1")

	w3 := httptest.NewRecorder()
	server.handleSessionSubagents(w3, req)
	_ = json.Unmarshal(w3.Body.Bytes(), &res)
	if res.RunningCount != 0 {
		t.Fatalf("Expected 0 running subagents after removal, got %d", res.RunningCount)
	}
}
func TestHandleMetaResolve(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_meta_resolve.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Seed nodes and historical sessions to establish project-to-agent affinity
	_ = db.SaveNode(&TreeNode{Path: "Ackbar/Backend", ProjectDir: "/path/to/backend"})
	_ = db.SaveNode(&TreeNode{Path: "Ackbar/Mobile", ProjectDir: "/path/to/mobile"})
	_ = db.SaveSession(&Session{
		ID:       "antigravity:local:sess-mobile",
		Agent:    "antigravity",
		Host:     "local",
		NodePath: "Ackbar/Mobile",
		Managed:  true,
		State:    StateIdle,
	})

	server := NewServer(db)

	// Test 1: POST with prompt for mobile project
	body := `{"prompt": "Fix drawer navigation in mobile"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/meta/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	server.handleMetaResolve(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		Host     string `json:"host"`
		Agent    string `json:"agent"`
		NodePath string `json:"node_path"`
		Cwd      string `json:"cwd"`
		Source   string `json:"source"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if res.Agent != "antigravity" {
		t.Errorf("Expected Agent 'antigravity', got %q", res.Agent)
	}
	if res.NodePath != "Ackbar/Mobile" {
		t.Errorf("Expected NodePath 'Ackbar/Mobile', got %q", res.NodePath)
	}
	if res.Cwd != "/path/to/mobile" {
		t.Errorf("Expected Cwd '/path/to/mobile', got %q", res.Cwd)
	}
	if res.Source != "heuristic" {
		t.Errorf("Expected Source 'heuristic' when no API key is provided, got %q", res.Source)
	}
}

func TestExtractConvIDFromPath(t *testing.T) {
	cases := []struct {
		path     string
		expected string
	}{
		{
			path:     "/home/dev4u/.gemini/antigravity-cli/presence/b06d1787-6f62-4515-9a05-acea8c7958a6.lock",
			expected: "b06d1787-6f62-4515-9a05-acea8c7958a6",
		},
		{
			path:     "/Users/dev4u/.gemini/antigravity-cli/presence/f74f0375-1d61-4c1c-baa4-67c8c8630a4e.lock",
			expected: "f74f0375-1d61-4c1c-baa4-67c8c8630a4e",
		},
		{
			path:     "/home/dev4u/.gemini/antigravity-cli/brain/b06d1787-6f62-4515-9a05-acea8c7958a6/.system_generated/logs/transcript.jsonl",
			expected: "b06d1787-6f62-4515-9a05-acea8c7958a6",
		},
		{
			path:     "/home/dev4u/.gemini/antigravity-cli/conversations/b06d1787-6f62-4515-9a05-acea8c7958a6.db",
			expected: "b06d1787-6f62-4515-9a05-acea8c7958a6",
		},
		{
			path:     "/home/dev4u/.gemini/antigravity-cli/annotations/b06d1787-6f62-4515-9a05-acea8c7958a6.pbtxt",
			expected: "b06d1787-6f62-4515-9a05-acea8c7958a6",
		},
		{
			path:     "/some/other/path/not-uuid.lock",
			expected: "",
		},
		{
			path:     "/proc/1234/fd/1",
			expected: "",
		},
		{
			path:     "",
			expected: "",
		},
	}

	for _, tc := range cases {
		got := extractConvIDFromPath(tc.path)
		if got != tc.expected {
			t.Errorf("extractConvIDFromPath(%q) = %q; expected %q", tc.path, got, tc.expected)
		}
	}
}

func TestNewlySpawnedSession_StartupGracePeriodPreserved(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	tempUUID := "088ed517-ac0f-4b81-b387-16d401a0db58"
	sess := &Session{
		ID:          "antigravity:local:" + tempUUID,
		Agent:       "antigravity",
		Host:        "local",
		NativeID:    tempUUID,
		Cwd:         "/tmp/project",
		State:       StateUnknown,
		Activity:    "Spawning session...",
		Managed:     true,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Trigger scanObservedSessions with a bounded context
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server.scanObservedSessions(ctx)

	stored, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if stored == nil {
		t.Fatalf("Session was deleted!")
	}
	// Session was spawned within 15 seconds, so it must NOT be marked StateEnded
	if stored.State == StateEnded {
		t.Errorf("Newly spawned session in StateUnknown was prematurely marked StateEnded during startup grace period")
	}
}

func TestAntigravityProcess_NoSessionID_DoesNotCreateProcSession(t *testing.T) {
	// Verify that findAntigravitySessionForPID returns empty string for non-existent PID
	fakePID := 99999997
	sID := findAntigravitySessionForPID(context.Background(), fakePID)
	if sID != "" {
		t.Errorf("Expected empty sessionID for non-existent PID, got %q", sID)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// Seed a dead ghost proc session
	ghostID := fmt.Sprintf("local:observed:proc-%d", fakePID)
	ghostSess := &Session{
		ID:       ghostID,
		Agent:    "antigravity",
		Host:     "local",
		NativeID: fmt.Sprintf("proc-%d", fakePID),
		Name:     "antigravity (proc-99999997)",
		State:    StateWorking,
		PID:      fakePID,
	}
	_ = db.SaveSession(ghostSess)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server.scanObservedSessions(ctx)

	// Verify dead ghost session was cleaned up
	retrievedGhost, _ := db.GetSession(ghostID)
	if retrievedGhost != nil {
		t.Errorf("Expected ghost proc session %s to be pruned from DB, but still found: %+v", ghostID, retrievedGhost)
	}
}

func TestParsePbtxtTitle(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{`title:"Preparing for MacMini setup"`, "Preparing for MacMini setup"},
		{`title: "Preparing for MacMini setup"`, "Preparing for MacMini setup"},
		{`  title:   "Indented with spaces"  `, "Indented with spaces"},
		{`last_user_view_time:{seconds:123}`, ""},
		{``, ""},
	}
	for _, c := range cases {
		got := parsePbtxtTitle(c.input)
		if got != c.expected {
			t.Errorf("parsePbtxtTitle(%q) = %q; want %q", c.input, got, c.expected)
		}
	}
}

func TestAntigravitySession_RenameAnnotationUpgradesName(t *testing.T) {
	tempHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", origHome)

	annoDir := filepath.Join(tempHome, ".gemini", "antigravity-cli", "annotations")
	if err := os.MkdirAll(annoDir, 0755); err != nil {
		t.Fatalf("Failed to create annoDir: %v", err)
	}

	convUUID := "b06d1787-6f62-4515-9a05-acea8c7958a6"
	annoFile := filepath.Join(annoDir, convUUID+".pbtxt")
	if err := os.WriteFile(annoFile, []byte(`title:"Preparing for MacMini setup"`), 0644); err != nil {
		t.Fatalf("Failed to write annoFile: %v", err)
	}

	// 1. Verify ReadAntigravityAnnotationTitle reads the title correctly
	gotTitle := ReadAntigravityAnnotationTitle(convUUID)
	if gotTitle != "Preparing for MacMini setup" {
		t.Fatalf("Expected ReadAntigravityAnnotationTitle to return 'Preparing for MacMini setup', got %q", gotTitle)
	}

	// 2. Set up DB and server
	dbPath := filepath.Join(t.TempDir(), "test_rename.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// Create dummy brain directory with transcript so Step 4 disk scan finds it
	brainDir := filepath.Join(tempHome, ".gemini", "antigravity-cli", "brain", convUUID, ".system_generated", "logs")
	if err := os.MkdirAll(brainDir, 0755); err != nil {
		t.Fatalf("Failed to create brainDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(brainDir, "transcript.jsonl"), []byte(`{"type":"USER_INPUT","content":"Initial prompt"}`+"\n"), 0644); err != nil {
		t.Fatalf("Failed to write transcript: %v", err)
	}

	sessID := fmt.Sprintf("antigravity:local:%s", convUUID)
	existing := &Session{
		ID:          sessID,
		Agent:       "antigravity",
		Host:        "local",
		NativeID:    convUUID,
		Name:        "Automating New Mac Setup", // Non-raw initial name
		State:       StateEnded,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(existing); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	// Run disk scan
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server.scanObservedSessions(ctx)

	// Verify name was upgraded from annotation
	updated, err := db.GetSession(sessID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if updated == nil {
		t.Fatalf("Session %s not found in DB", sessID)
	}
	if updated.Name != "Preparing for MacMini setup" {
		t.Errorf("Expected session Name to be upgraded to 'Preparing for MacMini setup', got %q", updated.Name)
	}

	// 3. Verify user's CustomTitle is NOT overwritten by subsequent annotation
	updated.CustomTitle = "My Custom Overridden Title"
	updated.Name = "My Custom Overridden Title"
	if err := db.SaveSession(updated); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}
	if err := os.WriteFile(annoFile, []byte(`title:"A Different Title"`), 0644); err != nil {
		t.Fatalf("Failed to write annoFile: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	server.scanObservedSessions(ctx2)

	reChecked, _ := db.GetSession(sessID)
	if reChecked.Name != "My Custom Overridden Title" {
		t.Errorf("Expected CustomTitle to be preserved, got %q", reChecked.Name)
	}
}
