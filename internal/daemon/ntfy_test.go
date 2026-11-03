package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	if payload1.Title != "PR Review Needed: Implement push notifications" {
		t.Errorf("unexpected title: %q", payload1.Title)
	}
	if payload1.Click != "ackbar://session?id=sess_worker_1" {
		t.Errorf("unexpected click deep-link: %q", payload1.Click)
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

	// 2. Task without active worker: falls back to task click
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
	if payload2.Title != "Plan Review Needed: Architecture brief" {
		t.Errorf("unexpected title for plan_review: %q", payload2.Title)
	}
	if payload2.Click != "ackbar://task?id=task_2" {
		t.Errorf("unexpected click deep-link: %q", payload2.Click)
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
	if payload.Title != "Task Blocked: Database migration" {
		t.Errorf("unexpected title: %q", payload.Title)
	}
	if payload.Click != "ackbar://attention?session_id=sess_db_1" {
		t.Errorf("unexpected click URL: %q", payload.Click)
	}
	if payload.Message != "[Ackbar] Should we run destructive reset?" {
		t.Errorf("unexpected message: %q", payload.Message)
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
	if payloadNoWorker.Message != "Task is blocked waiting for input" {
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
		ID:    "claude:local:sess_perm",
		Name:  "Refactor Auth",
		State: StateBlocked,
		Blocked: &Blocked{
			Kind:   BlockPermission,
			Reason: "Command execution requires permission",
		},
	}
	payloadPerm := s.buildSessionBlockedPayload(StateWorking, sessPerm)
	if payloadPerm == nil {
		t.Fatalf("expected payloadPerm not nil")
	}
	if payloadPerm.Title != "Permission Needed: Refactor Auth" {
		t.Errorf("unexpected title: %q", payloadPerm.Title)
	}
	if payloadPerm.Click != "ackbar://attention?session_id=claude%3Alocal%3Asess_perm" {
		t.Errorf("unexpected click: %q", payloadPerm.Click)
	}
	if payloadPerm.Message != "Command execution requires permission" {
		t.Errorf("unexpected message: %q", payloadPerm.Message)
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
	if payloadQuest.Title != "Question: my-org/backend" {
		t.Errorf("unexpected title: %q", payloadQuest.Title)
	}
	if payloadQuest.Message != "Which port to expose?" {
		t.Errorf("unexpected message: %q", payloadQuest.Message)
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
		if p.Title == "PR Review Needed: Test task" {
			hasReview = true
		}
		if p.Title == "Task Blocked: Test task" && p.Message == "[Ackbar] Need confirmation to proceed?" {
			hasBlocked = true
		}
	}
	if !hasReview {
		t.Errorf("missing PR review payload")
	}
	if !hasBlocked {
		t.Errorf("missing Task blocked payload")
	}
}
