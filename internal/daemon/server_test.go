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
		Agent:       "mock-agent",
		NativeID:    "session-unread-1",
		Cwd:         "/workspace/project",
		State:       StateBlocked,
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
	event *Event
}

func (m *mockDynamicProvider) Agent() string { return "mock-agent" }
func (m *mockDynamicProvider) DisplayName() string { return "Mock Agent" }
func (m *mockDynamicProvider) BrandColor() string { return "#999999" }
func (m *mockDynamicProvider) IconSVG() string { return "<svg></svg>" }
func (m *mockDynamicProvider) ProcessNames() []string { return []string{"mock-agent"} }
func (m *mockDynamicProvider) GetSpawnCommand(tempUUID string) string { return "sleep 5" }
func (m *mockDynamicProvider) GetResumeCommand(nativeID string) string { return "sleep 5" }
func (m *mockDynamicProvider) ReadSessionMetadata(cwd, nativeID string) *SessionMeta { return nil }
func (m *mockDynamicProvider) ResolveSessionTitle(cwd, nativeID string) string { return "" }
func (m *mockDynamicProvider) ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error) { return nil, nil }
func (m *mockDynamicProvider) CleanSessionFiles(home, cwd, nativeID string) error { return nil }
func (m *mockDynamicProvider) IsInstalled() bool { return true }
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

	changed := server.inspectClaudeStatus(ctx, sess)
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
