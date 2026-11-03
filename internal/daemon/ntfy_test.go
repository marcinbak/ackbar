package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNtfy_TaskReviewPayload(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewServer(db)
	s.SetNtfyConfig("https://ntfy.example.com", "my-topic")

	// Pre-create worker session in DB with custom title / node path
	_ = db.SaveSession(&Session{
		ID:        "sess_worker_1",
		Agent:     "claude-code",
		Host:      "local",
		Name:      "Auth Session",
		NodePath:  "Personal/Ackbar",
		State:     StateWorking,
		StartedAt: time.Now(),
	})

	// 1. Task with active worker session and PR number
	task1 := &Task{
		ID:          "task_1",
		Title:       "Implement push notifications",
		ProjectName: "Ackbar",
		Branch:      "feat/ntfy",
		Status:      "REVIEW",
		Substatus:   "in_review",
		PRURL:       "https://github.com/marcinbak/ackbar/pull/42",
		PRNumber:    42,
		Workers: []TaskWorker{
			{SessionID: "sess_worker_1", IsActive: true},
		},
	}

	payload1 := s.buildTaskReviewPayload("IN_PROGRESS", task1)
	if payload1 == nil {
		t.Fatalf("expected payload1 not nil")
	}
	if payload1.Topic != "my-topic" {
		t.Errorf("expected topic 'my-topic', got %q", payload1.Topic)
	}
	if payload1.Title != "Auth Session" {
		t.Errorf("unexpected title: %q", payload1.Title)
	}
	if payload1.Click != "ackbar://session?id=sess_worker_1" {
		t.Errorf("unexpected click deep-link: %q", payload1.Click)
	}
	if payload1.Icon != AckbarNotificationIconURL {
		t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, payload1.Icon)
	}
	expectedMsg1 := "📂 Personal / Ackbar\n🟢 PR Review Needed (#42)\nTask: Implement push notifications"
	if payload1.Message != expectedMsg1 {
		t.Errorf("unexpected message: %q, expected: %q", payload1.Message, expectedMsg1)
	}
	if len(payload1.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(payload1.Actions))
	}
	if payload1.Actions[0].Label != "Open PR #42" || payload1.Actions[0].URL != task1.PRURL {
		t.Errorf("unexpected action: %+v", payload1.Actions[0])
	}
	if payload1.Priority != 4 {
		t.Errorf("expected priority 4, got %d", payload1.Priority)
	}

	// 2. Task without active worker: falls back to task click and task title
	task2 := &Task{
		ID:          "task_2",
		Title:       "Architecture brief",
		ProjectName: "Ackbar",
		Status:      "REVIEW",
		Substatus:   "plan_review",
		PRURL:       "https://github.com/marcinbak/ackbar/pull/99",
	}

	payload2 := s.buildTaskReviewPayload("", task2)
	if payload2 == nil {
		t.Fatalf("expected payload2 not nil")
	}
	if payload2.Title != "Architecture brief" {
		t.Errorf("unexpected title for plan_review: %q", payload2.Title)
	}
	if payload2.Click != "ackbar://task?id=task_2" {
		t.Errorf("unexpected click deep-link: %q", payload2.Click)
	}
	if payload2.Icon != AckbarNotificationIconURL {
		t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, payload2.Icon)
	}
	expectedMsg2 := "📂 Ackbar\n🟢 Plan Review Needed"
	if payload2.Message != expectedMsg2 {
		t.Errorf("unexpected message: %q, expected: %q", payload2.Message, expectedMsg2)
	}
	if len(payload2.Actions) != 1 || payload2.Actions[0].Label != "Open PR" {
		t.Errorf("unexpected action label: %+v", payload2.Actions)
	}
}

func TestNtfy_TaskBlockedPayload(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewServer(db)
	s.SetNtfyConfig("https://ntfy.example.com", "my-topic")

	task := &Task{
		ID:              "task_b",
		Title:           "Database migration",
		ProjectName:     "Ackbar",
		Substatus:       "blocked",
		BlockerQuestion: "Should we run destructive reset?",
		Workers: []TaskWorker{
			{SessionID: "sess_db_1", IsActive: true},
		},
	}

	payload := s.buildTaskBlockedPayload("active", "", task)
	if payload == nil {
		t.Fatalf("expected payload not nil")
	}
	if payload.Title != "Database migration" {
		t.Errorf("unexpected title: %q", payload.Title)
	}
	if payload.Click != "ackbar://session?id=sess_db_1" {
		t.Errorf("unexpected click URL: %q", payload.Click)
	}
	if payload.Icon != AckbarNotificationIconURL {
		t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, payload.Icon)
	}
	expectedMsg := "📂 Ackbar\n❓ Blocked: Should we run destructive reset?"
	if payload.Message != expectedMsg {
		t.Errorf("unexpected message: %q, expected: %q", payload.Message, expectedMsg)
	}

	// Without worker session: falls back to task deep-link
	taskNoWorker := &Task{
		ID:        "task_noworker",
		Title:     "Standalone item",
		Substatus: "blocked",
	}
	payloadNoWorker := s.buildTaskBlockedPayload("", "", taskNoWorker)
	if payloadNoWorker == nil {
		t.Fatalf("expected payloadNoWorker not nil")
	}
	if payloadNoWorker.Click != "ackbar://task?id=task_noworker" {
		t.Errorf("unexpected fallback click: %q", payloadNoWorker.Click)
	}
	if payloadNoWorker.Icon != AckbarNotificationIconURL {
		t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, payloadNoWorker.Icon)
	}
	if payloadNoWorker.Message != "❓ Blocked: Task is blocked waiting for input" {
		t.Errorf("unexpected fallback message: %q", payloadNoWorker.Message)
	}
}

func TestNtfy_SessionBlockedPayload(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewServer(db)
	s.SetNtfyConfig("https://ntfy.example.com", "my-topic")

	// 1. Permission block
	sessPerm := &Session{
		ID:       "claude:local:sess_perm",
		Name:     "Refactor Auth",
		NodePath: "Personal/Ackbar",
		State:    StateBlocked,
		Blocked: &Blocked{
			Kind:   BlockPermission,
			Reason: "Command execution requires permission",
		},
	}
	payloadPerm := s.buildSessionBlockedPayload(StateWorking, sessPerm)
	if payloadPerm == nil {
		t.Fatalf("expected payloadPerm not nil")
	}
	if payloadPerm.Title != "Refactor Auth" {
		t.Errorf("unexpected title: %q", payloadPerm.Title)
	}
	if payloadPerm.Click != "ackbar://session?id=claude%3Alocal%3Asess_perm" {
		t.Errorf("unexpected click: %q", payloadPerm.Click)
	}
	if payloadPerm.Icon != AckbarNotificationIconURL {
		t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, payloadPerm.Icon)
	}
	expectedMsgPerm := "📂 Personal / Ackbar\n🔒 Permission Needed: Command execution requires permission"
	if payloadPerm.Message != expectedMsgPerm {
		t.Errorf("unexpected message: %q, expected: %q", payloadPerm.Message, expectedMsgPerm)
	}

	// 2. Question block with fallback name to ProjectKey
	sessQuest := &Session{
		ID:         "codex:local:sess_q",
		ProjectKey: "my-org/backend",
		State:      StateBlocked,
		Blocked: &Blocked{
			Kind:     BlockQuestion,
			Question: "Which port to expose?",
		},
	}
	payloadQuest := s.buildSessionBlockedPayload(StateWorking, sessQuest)
	if payloadQuest == nil {
		t.Fatalf("expected payloadQuest not nil")
	}
	if payloadQuest.Title != "my-org/backend" {
		t.Errorf("unexpected title: %q", payloadQuest.Title)
	}
	if payloadQuest.Click != "ackbar://session?id=codex%3Alocal%3Asess_q" {
		t.Errorf("unexpected click: %q", payloadQuest.Click)
	}
	if payloadQuest.Icon != AckbarNotificationIconURL {
		t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, payloadQuest.Icon)
	}
	expectedMsgQuest := "📂 my-org/backend\n❓ Question: Which port to expose?"
	if payloadQuest.Message != expectedMsgQuest {
		t.Errorf("unexpected message: %q, expected: %q", payloadQuest.Message, expectedMsgQuest)
	}

	// 3. Duplicate suppression: oldState was already StateBlocked
	dupPayload := s.buildSessionBlockedPayload(StateBlocked, sessQuest)
	if dupPayload != nil {
		t.Errorf("expected duplicate suppression, got non-nil payload: %+v", dupPayload)
	}
}

func TestNtfy_DuplicateSuppression(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewServer(db)
	s.SetNtfyConfig("https://ntfy.example.com", "my-topic")

	taskReview := &Task{
		ID:     "task_rev",
		Title:  "Review task",
		Status: "REVIEW",
	}
	// Already in REVIEW: oldStatus == "REVIEW"
	if p := s.buildTaskReviewPayload("REVIEW", taskReview); p != nil {
		t.Errorf("expected review duplicate to be suppressed, got %+v", p)
	}
	if p := s.buildTaskReviewPayload("review", taskReview); p != nil {
		t.Errorf("expected case-insensitive review duplicate to be suppressed, got %+v", p)
	}

	taskBlocked := &Task{
		ID:              "task_blk",
		Title:           "Blocked task",
		Substatus:       "blocked",
		BlockerQuestion: "Same question",
	}
	// Already blocked with same question
	if p := s.buildTaskBlockedPayload("blocked", "Same question", taskBlocked); p != nil {
		t.Errorf("expected blocked duplicate with same question to be suppressed, got %+v", p)
	}

	// But if question changed while blocked, it should NOT be suppressed
	taskBlocked.BlockerQuestion = "New follow-up question"
	if p := s.buildTaskBlockedPayload("blocked", "Same question", taskBlocked); p == nil {
		t.Errorf("expected new question to generate notification, got nil")
	}

	// When task is active (not blocked) but BlockerQuestion changes, it must NOT notify
	taskActive := &Task{
		ID:              "task_act",
		Title:           "Active task",
		Substatus:       "active",
		BlockerQuestion: "Stale question being cleared or edited",
	}
	if p := s.buildTaskBlockedPayload("active", "", taskActive); p != nil {
		t.Errorf("expected non-blocked task with question change to return nil, got %+v", p)
	}
}

func TestNtfy_ConfigPrecedence(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewServer(db)

	// Precedence 3: Default
	if s.NtfyServer() != "https://ntfy.sh" {
		t.Errorf("expected default server 'https://ntfy.sh', got %q", s.NtfyServer())
	}
	if s.NtfyTopic() != "" {
		t.Errorf("expected default topic '', got %q", s.NtfyTopic())
	}

	// Precedence 2: DB settings
	_ = db.SetSettings(map[string]string{
		"ntfy_server": "https://ntfy.custom-db.io",
		"ntfy_topic":  "db-topic",
	})
	if s.NtfyServer() != "https://ntfy.custom-db.io" {
		t.Errorf("expected DB server override, got %q", s.NtfyServer())
	}
	if s.NtfyTopic() != "db-topic" {
		t.Errorf("expected DB topic override, got %q", s.NtfyTopic())
	}

	// Precedence 1: Explicit CLI/SetNtfyConfig override
	s.SetNtfyConfig("https://ntfy.cli-flag.com", "cli-topic")
	if s.NtfyServer() != "https://ntfy.cli-flag.com" {
		t.Errorf("expected CLI server override, got %q", s.NtfyServer())
	}
	if s.NtfyTopic() != "cli-topic" {
		t.Errorf("expected CLI topic override, got %q", s.NtfyTopic())
	}
}

func TestTasksAPI_ReviewAndBlockedTriggerNtfy(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	var receivedMu sync.Mutex
	var received []NtfyPayload

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p NtfyPayload
		if err := json.Unmarshal(body, &p); err == nil {
			receivedMu.Lock()
			received = append(received, p)
			receivedMu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer mockServer.Close()

	s := NewServer(db)
	s.SetNtfyConfig(mockServer.URL, "test-integration-topic")

	// 1. Create a task in IN_PROGRESS (should not trigger REVIEW)
	createReq := Task{
		Title:       "Test task",
		ProjectName: "Ackbar",
		Status:      "IN_PROGRESS",
		Substatus:   "active",
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(body))
	s.handleTasks(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", w.Code)
	}
	var created Task
	_ = json.NewDecoder(w.Body).Decode(&created)

	// 2. Transition task to REVIEW
	updateReq := Task{
		ID:        created.ID,
		Status:    "REVIEW",
		Substatus: "in_review",
		PRURL:     "https://github.com/org/repo/pull/10",
		PRNumber:  10,
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(body))
	s.handleTasks(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// 3. Transition task to blocked
	blockReq := Task{
		ID:              created.ID,
		Substatus:       "blocked",
		BlockerQuestion: "Need confirmation to proceed?",
	}
	body, _ = json.Marshal(blockReq)
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(body))
	s.handleTasks(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// Wait up to 2 seconds for goroutines to deliver payloads to mock server
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		receivedMu.Lock()
		count := len(received)
		receivedMu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	receivedMu.Lock()
	defer receivedMu.Unlock()
	if len(received) < 2 {
		t.Fatalf("expected at least 2 ntfy payloads, got %d", len(received))
	}

	hasReview := false
	hasBlocked := false
	for _, p := range received {
		if p.Topic != "test-integration-topic" {
			t.Errorf("unexpected topic: %s", p.Topic)
		}
		if p.Title == "Test task" && strings.Contains(p.Message, "🟢 PR Review Needed (#10)") {
			hasReview = true
			if !strings.Contains(p.Message, "📂 Ackbar") {
				t.Errorf("expected review message to contain group path, got %q", p.Message)
			}
			if p.Icon != AckbarNotificationIconURL {
				t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, p.Icon)
			}
			if p.Click != fmt.Sprintf("ackbar://task?id=%s", created.ID) {
				t.Errorf("unexpected click URL: %q", p.Click)
			}
		}
		if p.Title == "Test task" && p.Message == "📂 Ackbar\n❓ Blocked: Need confirmation to proceed?" {
			hasBlocked = true
			if p.Icon != AckbarNotificationIconURL {
				t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, p.Icon)
			}
			if p.Click != fmt.Sprintf("ackbar://task?id=%s", created.ID) {
				t.Errorf("unexpected click URL: %q", p.Click)
			}
		}
	}
	if !hasReview {
		t.Errorf("missing PR review payload")
	}
	if !hasBlocked {
		t.Errorf("missing Task blocked payload")
	}
}

func TestNtfy_TestNotificationEndpoint(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewServer(db)

	// 1. Without topic -> 400 Bad Request
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/test", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.handleTestNotification(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when topic not configured, got %d", w.Code)
	}

	// 2. With mock ntfy server and explicit topic in payload
	var received NtfyPayload
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer mockServer.Close()

	bodyJSON, _ := json.Marshal(map[string]string{
		"ntfy_topic":  "custom-test-topic",
		"ntfy_server": mockServer.URL,
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/notifications/test", bytes.NewReader(bodyJSON))
	w = httptest.NewRecorder()
	s.handleTestNotification(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body: %s)", w.Code, w.Body.String())
	}
	if received.Topic != "custom-test-topic" {
		t.Errorf("expected topic 'custom-test-topic', got %q", received.Topic)
	}
	if received.Icon != AckbarNotificationIconURL {
		t.Errorf("expected icon %q, got %q", AckbarNotificationIconURL, received.Icon)
	}
	if received.Click != "ackbar://session" {
		t.Errorf("expected click 'ackbar://session', got %q", received.Click)
	}
	if !strings.Contains(received.Message, "Test notification from Ackbar Web UI") {
		t.Errorf("unexpected test message: %q", received.Message)
	}
}

func TestSettings_PropagateNtfyToFleet(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	var receivedSettingsMu sync.Mutex
	var receivedSettings map[string]string

	mockPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/settings" && r.Method == http.MethodPost {
			var m map[string]string
			if err := json.NewDecoder(r.Body).Decode(&m); err == nil {
				receivedSettingsMu.Lock()
				receivedSettings = m
				receivedSettingsMu.Unlock()
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockPeer.Close()

	// Register remote host pointing to mockPeer
	_ = db.SaveHost(&HostRecord{
		Name:      "devbox",
		URL:       mockPeer.URL,
		Online:    true,
		CreatedAt: time.Now(),
	})

	s := NewServer(db)
	s.SetHostIdentity("local", "Local Master")

	postBody, _ := json.Marshal(map[string]string{
		"ntfy_topic":     "fleet-wide-topic",
		"ntfy_server":    "https://ntfy.sh",
		"propagate_ntfy": "true",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/settings", bytes.NewReader(postBody))
	w := httptest.NewRecorder()
	s.handleSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from handleSettings, got %d", w.Code)
	}

	// Verify local settings were stored
	if topic, _ := db.GetSetting("ntfy_topic"); topic != "fleet-wide-topic" {
		t.Errorf("expected db ntfy_topic 'fleet-wide-topic', got %q", topic)
	}
	if val, _ := db.GetSetting("propagate_ntfy"); val != "" {
		t.Errorf("expected propagate_ntfy not to be persisted in db, got %q", val)
	}

	// Wait up to 2 seconds for goroutine fleet propagation
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		receivedSettingsMu.Lock()
		done := receivedSettings != nil
		receivedSettingsMu.Unlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	receivedSettingsMu.Lock()
	defer receivedSettingsMu.Unlock()
	if receivedSettings == nil {
		t.Fatalf("expected mockPeer to receive settings propagation")
	}
	if receivedSettings["ntfy_topic"] != "fleet-wide-topic" {
		t.Errorf("expected peer ntfy_topic 'fleet-wide-topic', got %q", receivedSettings["ntfy_topic"])
	}
	if _, ok := receivedSettings["propagate_ntfy"]; ok {
		t.Errorf("propagate_ntfy should be stripped before forwarding to peer")
	}
}

func TestNtfy_ResolveTaskSessionAndFormatting(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewServer(db)

	// 1. nil task
	if res := s.resolveTaskSession(nil); res != nil {
		t.Errorf("expected nil for nil task, got %+v", res)
	}

	// 2. task with worker not in DB
	taskWorkerNotDB := &Task{
		ID: "task_x",
		Workers: []TaskWorker{
			{SessionID: "sess_not_in_db", IsActive: true},
		},
	}
	res := s.resolveTaskSession(taskWorkerNotDB)
	if res == nil || res.ID != "sess_not_in_db" {
		t.Fatalf("expected stub session with ID sess_not_in_db, got %+v", res)
	}

	// 3. Match via CurrentTaskID with ended vs active session preference
	sessEnded := &Session{
		ID:            "sess_ended",
		Agent:         "claude-code",
		Host:          "local",
		State:         StateEnded,
		CurrentTaskID: "task_match_id",
		StartedAt:     time.Now(),
	}
	sessActive := &Session{
		ID:            "sess_active",
		Agent:         "claude-code",
		Host:          "local",
		State:         StateWorking,
		CurrentTaskID: "task_match_id",
		StartedAt:     time.Now(),
	}
	_ = db.SaveSession(sessEnded)
	_ = db.SaveSession(sessActive)

	taskMatchID := &Task{ID: "task_match_id"}
	res = s.resolveTaskSession(taskMatchID)
	if res == nil || res.ID != "sess_active" {
		t.Errorf("expected active session sess_active, got %+v", res)
	}

	// 4. Match via GitBranch
	sessBranch := &Session{
		ID:        "sess_branch",
		Agent:     "claude-code",
		Host:      "local",
		State:     StateWorking,
		GitBranch: "feat/fleet-sync",
		StartedAt: time.Now(),
	}
	_ = db.SaveSession(sessBranch)
	taskBranch := &Task{ID: "task_unknown", Branch: "feat/fleet-sync"}
	res = s.resolveTaskSession(taskBranch)
	if res == nil || res.ID != "sess_branch" {
		t.Errorf("expected sess_branch, got %+v", res)
	}

	// 5. Test formatSessionDisplayName hierarchy
	s1 := &Session{ID: "s1", CustomTitle: "Custom Title", Name: "Session Name"}
	if d := formatSessionDisplayName(s1, "Fallback"); d != "Custom Title" {
		t.Errorf("expected Custom Title, got %q", d)
	}
	s2 := &Session{ID: "s2", Name: "Session Name"}
	if d := formatSessionDisplayName(s2, "Fallback"); d != "Session Name" {
		t.Errorf("expected Session Name, got %q", d)
	}
	s3 := &Session{ID: "s3"}
	if d := formatSessionDisplayName(s3, "Fallback"); d != "Fallback" {
		t.Errorf("expected Fallback, got %q", d)
	}
	if d := formatSessionDisplayName(nil, "Fallback"); d != "Fallback" {
		t.Errorf("expected Fallback for nil session, got %q", d)
	}
	s4 := &Session{ID: "s4", ProjectKey: "org/project"}
	if d := formatSessionDisplayName(s4, ""); d != "org/project" {
		t.Errorf("expected org/project, got %q", d)
	}
	s5 := &Session{ID: "s5"}
	if d := formatSessionDisplayName(s5, ""); d != "s5" {
		t.Errorf("expected s5, got %q", d)
	}
	if d := formatSessionDisplayName(nil, ""); d != "Ackbar Session" {
		t.Errorf("expected Ackbar Session, got %q", d)
	}

	// 6. Test formatGroupPathSubtitle
	sessNode := &Session{NodePath: "Personal/Ackbar/Daemon"}
	if p := formatGroupPathSubtitle(sessNode, nil); p != "Personal / Ackbar / Daemon" {
		t.Errorf("expected 'Personal / Ackbar / Daemon', got %q", p)
	}

	// unassigned NodePath ignored, fallback to task
	sessUnassigned := &Session{NodePath: "unassigned", ProjectKey: "fallback-proj"}
	taskDedupe := &Task{
		GroupName:      "Personal",
		ProjectName:    "personal",
		SubprojectName: "Ackbar",
	}
	if p := formatGroupPathSubtitle(sessUnassigned, taskDedupe); p != "Personal / Ackbar" {
		t.Errorf("expected deduplicated adjacent segments 'Personal / Ackbar', got %q", p)
	}

	// Fallback to project key
	if p := formatGroupPathSubtitle(sessUnassigned, nil); p != "fallback-proj" {
		t.Errorf("expected 'fallback-proj', got %q", p)
	}
}
