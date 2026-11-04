package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTasks_DatabaseAndAPI(t *testing.T) {
	dbFile := "./test_tasks.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// 1. Test CreateTask
	task := &Task{
		ID:          "task_001",
		Title:       "Implement Tasks Overview",
		GroupName:   "Modemobile",
		ProjectName: "NGL",
		Status:      "IN_PROGRESS",
		Substatus:   "active",
		Workers: []TaskWorker{
			{SessionID: "claude:mac:123", Agent: "claude-code", Host: "mac", IsActive: true},
		},
		ExternalRefs: []TaskExternalRef{
			{Tracker: "jira", RefKey: "NGL-101", URL: "https://jira.example.com/NGL-101"},
		},
		Deliverables: []TaskDeliverable{
			{Kind: "plan", Title: "Design Doc", Host: "mac"},
		},
	}

	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 2. Test GetTaskByID
	fetched, err := db.GetTaskByID("task_001")
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if fetched == nil {
		t.Fatalf("Expected task_001, got nil")
	}
	if fetched.Title != task.Title {
		t.Errorf("Expected title %s, got %s", task.Title, fetched.Title)
	}
	if len(fetched.Workers) != 1 || fetched.Workers[0].TaskID != "task_001" {
		t.Errorf("Expected 1 worker bound to task_001, got %+v", fetched.Workers)
	}
	if len(fetched.ExternalRefs) != 1 || fetched.ExternalRefs[0].TaskID != "task_001" {
		t.Errorf("Expected 1 external ref bound to task_001, got %+v", fetched.ExternalRefs)
	}
	if len(fetched.Deliverables) != 1 || fetched.Deliverables[0].TaskID != "task_001" {
		t.Errorf("Expected 1 deliverable bound to task_001, got %+v", fetched.Deliverables)
	}

	// 3. Test GetTasks list
	allTasks, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(allTasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(allTasks))
	}
	if len(allTasks[0].Workers) != 1 {
		t.Errorf("Expected worker populated in GetTasks, got %+v", allTasks[0].Workers)
	}

	// 4. Test UpdateTask
	fetched.Status = "REVIEW"
	fetched.Substatus = "approved"
	if err := db.UpdateTask(fetched); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	updated, err := db.GetTaskByID("task_001")
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if updated.Status != "REVIEW" || updated.Substatus != "approved" {
		t.Errorf("Expected status REVIEW/approved, got %s/%s", updated.Status, updated.Substatus)
	}

	// 5. Test HTTP server handlers
	server := &Server{
		db: db,
	}

	// GET /v1/tasks
	req := httptest.NewRequest(http.MethodGet, "/v1/tasks", nil)
	w := httptest.NewRecorder()
	server.handleTasks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/tasks returned status %d", w.Code)
	}
	var resTasks []Task
	if err := json.Unmarshal(w.Body.Bytes(), &resTasks); err != nil {
		t.Fatalf("Failed to decode tasks response: %v", err)
	}
	if len(resTasks) != 1 {
		t.Fatalf("Expected 1 task returned, got %d", len(resTasks))
	}

	// POST /v1/tasks/event with ambient branch and PR detection
	eventPayload := TaskEventPayload{
		TaskID:    "task_001",
		EventType: "log",
		Payload:   "Switched to branch 'feat/user-auth' and created https://github.com/mode/ngl-ios/pull/42",
	}
	bodyBytes, _ := json.Marshal(eventPayload)
	reqEvent := httptest.NewRequest(http.MethodPost, "/v1/tasks/event", bytes.NewReader(bodyBytes))
	wEvent := httptest.NewRecorder()
	server.handleTaskEvent(wEvent, reqEvent)
	if wEvent.Code != http.StatusOK {
		t.Fatalf("POST /v1/tasks/event returned status %d: %s", wEvent.Code, wEvent.Body.String())
	}

	afterEvent, err := db.GetTaskByID("task_001")
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if afterEvent.Branch != "feat/user-auth" {
		t.Errorf("Expected branch 'feat/user-auth', got '%s'", afterEvent.Branch)
	}
	if afterEvent.PRURL != "https://github.com/mode/ngl-ios/pull/42" {
		t.Errorf("Expected PRURL 'https://github.com/mode/ngl-ios/pull/42', got '%s'", afterEvent.PRURL)
	}
	if afterEvent.PRState != "OPEN" {
		t.Errorf("Expected PRState 'OPEN', got '%s'", afterEvent.PRState)
	}

	// 6. Test UpdateTask fails for non-existent task
	nonExistent := &Task{ID: "does_not_exist", Title: "Fake"}
	if err := db.UpdateTask(nonExistent); err == nil {
		t.Fatalf("Expected error updating non-existent task, got nil")
	}

	// 7. Test Cascade Delete
	if _, err := db.db.Exec("DELETE FROM tasks WHERE id = 'task_001'"); err != nil {
		t.Fatalf("Failed to delete task: %v", err)
	}
	var count int
	_ = db.db.QueryRow("SELECT COUNT(*) FROM task_workers WHERE task_id = 'task_001'").Scan(&count)
	if count != 0 {
		t.Errorf("Expected 0 workers after cascade delete, got %d", count)
	}

	// 8. Test Empty GetTasks returns empty slice, not null
	emptyTasks, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed on empty DB: %v", err)
	}
	if emptyTasks == nil {
		t.Errorf("Expected non-nil slice from empty GetTasks")
	}
	emptyBytes, _ := json.Marshal(emptyTasks)
	if string(emptyBytes) != "[]" {
		t.Errorf("Expected '[]' json output, got '%s'", string(emptyBytes))
	}
}

func TestTelemetry_RegexExtractors(t *testing.T) {
	// 1. Issue Key
	if k := ExtractIssueKey("feat/NGL-409-ws-reconnect"); k != "NGL-409" {
		t.Errorf("Expected NGL-409, got %q", k)
	}
	if k := ExtractIssueKey("git commit -m 'fix(auth): resolve PROJ-123 login issue'"); k != "PROJ-123" {
		t.Errorf("Expected PROJ-123, got %q", k)
	}
	if k := ExtractIssueKey("simple-branch-name"); k != "" {
		t.Errorf("Expected empty string, got %q", k)
	}

	// 2. PR URL
	url, num := ExtractPRURL("Created pull request https://github.com/modemobile/ngl-ios/pull/82 successfully")
	if url != "https://github.com/modemobile/ngl-ios/pull/82" || num != 82 {
		t.Errorf("Expected PR #82 URL, got url=%q, num=%d", url, num)
	}

	// 3. Branch Command
	if b := ExtractBranchCommand("git checkout -b feat/my-cool-feature origin/main"); b != "feat/my-cool-feature" {
		t.Errorf("Expected feat/my-cool-feature, got %q", b)
	}
	if b := ExtractBranchCommand("git switch -c fix/bug-123"); b != "fix/bug-123" {
		t.Errorf("Expected fix/bug-123, got %q", b)
	}
	if b := ExtractBranchCommand("git worktree add -b feat/phase3 .worktrees/phase3"); b != "feat/phase3" {
		t.Errorf("Expected feat/phase3, got %q", b)
	}
}

func TestSyncSessionTaskWorker_ExplicitBindingAndStalled(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_worker_sync.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	// 1. Unbound session does NOT create a task
	unboundSess := &Session{
		ID:         "claude:mac:uuid-unbound",
		Agent:      "claude-code",
		Host:       "mac",
		GitBranch:  "feat/unbound",
		Cwd:        "/Users/dev4u/Work/ngl-ios",
		ProjectKey: "ngl-ios",
		AccountID:  "claude-code:work",
		State:      StateWorking,
	}
	server.SyncSessionTaskWorker(unboundSess)

	tasks, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("Expected 0 tasks created for unbound session, got %d", len(tasks))
	}

	// 2. Explicitly bound task
	task := &Task{
		ID:          "task-explicit-1",
		Title:       "Implement Biometric Auth",
		GroupName:   "Modemobile",
		ProjectName: "ngl-ios",
		Status:      "IN_PROGRESS",
		Substatus:   "active",
		Branch:      "feat/NGL-409-reconnect",
		ExternalRefs: []TaskExternalRef{
			{Tracker: "jira", RefKey: "NGL-409"},
		},
		Workers: []TaskWorker{
			{
				TaskID:     "task-explicit-1",
				SessionID:  "claude:mac:uuid-bound",
				Host:       "mac",
				Agent:      "claude-code",
				IsActive:   true,
				AssignedAt: time.Now().UTC(),
			},
		},
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	boundSess := &Session{
		ID:            "claude:mac:uuid-bound",
		CurrentTaskID: task.ID,
		Agent:         "claude-code",
		Host:          "mac",
		GitBranch:     "feat/NGL-409-reconnect",
		Cwd:           "/Users/dev4u/Work/ngl-ios",
		State:         StateWorking,
	}

	// 3. Test Session Blocked state sync
	boundSess.State = StateBlocked
	boundSess.Blocked = &Blocked{
		Kind:     BlockQuestion,
		Question: "Which database should we use?",
	}
	server.SyncSessionTaskWorker(boundSess)

	taskBlocked, err := db.GetTaskByID(task.ID)
	if err != nil || taskBlocked == nil {
		t.Fatalf("Failed to fetch task after blocked: %v", err)
	}
	if taskBlocked.Substatus != "blocked" {
		t.Errorf("Expected task substatus blocked, got %s", taskBlocked.Substatus)
	}
	if taskBlocked.BlockerQuestion != "Which database should we use?" {
		t.Errorf("Expected blocker question attached, got %q", taskBlocked.BlockerQuestion)
	}

	// 4. Test Session Unblocked
	boundSess.State = StateWorking
	boundSess.Blocked = nil
	server.SyncSessionTaskWorker(boundSess)

	taskUnblocked, err := db.GetTaskByID(task.ID)
	if err != nil || taskUnblocked == nil {
		t.Fatalf("Failed to fetch task after unblock: %v", err)
	}
	if taskUnblocked.Substatus != "active" || taskUnblocked.BlockerQuestion != "" {
		t.Errorf("Expected substatus active and empty blocker question, got %s / %q", taskUnblocked.Substatus, taskUnblocked.BlockerQuestion)
	}

	// 5. Test Session Ended -> worker marked inactive and task substatus transitions to stalled
	boundSess.State = StateEnded
	server.SyncSessionTaskWorker(boundSess)

	taskEnded, err := db.GetTaskByID(task.ID)
	if err != nil || taskEnded == nil {
		t.Fatalf("Failed to fetch task after end: %v", err)
	}
	if taskEnded.Substatus != "stalled" {
		t.Errorf("Expected task substatus stalled when all workers end, got %s", taskEnded.Substatus)
	}
	for _, w := range taskEnded.Workers {
		if w.SessionID == boundSess.ID && w.IsActive {
			t.Errorf("Expected worker %s to be inactive, but was active", w.SessionID)
		}
	}
}

func TestTelemetry_APIProposeAndDeliverables(t *testing.T) {
	dbFile := "./test_propose.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	// 1. POST /v1/tasks/propose
	prop := TaskProposePayload{
		Title:         "Memory leak in WebSocket client",
		Type:          "bug",
		Severity:      "high",
		Rationale:     "Unclosed stream handler holds onto activity context",
		FileReference: "src/net/ws.go#L42",
		GroupName:     "Modemobile",
		ProjectName:   "Ackbar",
	}
	body, _ := json.Marshal(prop)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/propose", bytes.NewReader(body))
	w := httptest.NewRecorder()
	server.handleTaskPropose(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /v1/tasks/propose returned %d: %s", w.Code, w.Body.String())
	}

	var created Task
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Status != "NEW" || created.Substatus != "discovered" {
		t.Errorf("Expected NEW/discovered, got %s/%s", created.Status, created.Substatus)
	}
	if created.Title != prop.Title {
		t.Errorf("Expected title %q, got %q", prop.Title, created.Title)
	}

	// 2. POST /v1/tasks/deliverable
	del := TaskDeliverablePayload{
		TaskID:   created.ID,
		Kind:     "mockup",
		Title:    "Dashboard Screen Mockup",
		FilePath: "/path/to/screen.png",
	}
	bodyDel, _ := json.Marshal(del)
	reqDel := httptest.NewRequest(http.MethodPost, "/v1/tasks/deliverable", bytes.NewReader(bodyDel))
	wDel := httptest.NewRecorder()
	server.handleTaskDeliverable(wDel, reqDel)

	if wDel.Code != http.StatusCreated {
		t.Fatalf("POST /v1/tasks/deliverable returned %d: %s", wDel.Code, wDel.Body.String())
	}

	taskWithDel, _ := db.GetTaskByID(created.ID)
	if len(taskWithDel.Deliverables) != 1 {
		t.Fatalf("Expected 1 deliverable, got %d", len(taskWithDel.Deliverables))
	}
}

func TestTelemetry_ProposeAndDeliverableValidation(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_validation.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	dummyTask := &Task{
		ID:          "task_validation_test",
		Title:       "Validation test",
		GroupName:   "Modemobile",
		ProjectName: "Ackbar",
		Status:      "IN_PROGRESS",
	}
	if err := db.CreateTask(dummyTask); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 1. Test handleTaskPropose validation
	// Empty title rejected
	bodyEmpty, _ := json.Marshal(map[string]any{"title": ""})
	req := httptest.NewRequest("POST", "/v1/tasks/propose", bytes.NewReader(bodyEmpty))
	rec := httptest.NewRecorder()
	server.handleTaskPropose(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for empty propose title, got %d", rec.Code)
	}

	// 2. Test handleTaskDeliverable validation
	// Path traversal rejected
	bodyTraversal, _ := json.Marshal(map[string]any{
		"task_id":   dummyTask.ID,
		"kind":      "plan",
		"file_path": "../../../etc/passwd",
	})
	req = httptest.NewRequest("POST", "/v1/tasks/deliverable", bytes.NewReader(bodyTraversal))
	rec = httptest.NewRecorder()
	server.handleTaskDeliverable(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for deliverable path traversal, got %d", rec.Code)
	}

	// Invalid URL scheme rejected
	bodyBadURL, _ := json.Marshal(map[string]any{
		"task_id": dummyTask.ID,
		"kind":    "plan",
		"url":     "ftp://malicious.example.com",
	})
	req = httptest.NewRequest("POST", "/v1/tasks/deliverable", bytes.NewReader(bodyBadURL))
	rec = httptest.NewRecorder()
	server.handleTaskDeliverable(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for non-http(s) URL, got %d", rec.Code)
	}

	// Invalid kind rejected
	bodyBadKind, _ := json.Marshal(map[string]any{
		"task_id": dummyTask.ID,
		"kind":    "invalid_kind",
		"title":   "Valid title",
	})
	req = httptest.NewRequest("POST", "/v1/tasks/deliverable", bytes.NewReader(bodyBadKind))
	rec = httptest.NewRecorder()
	server.handleTaskDeliverable(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for invalid deliverable kind, got %d", rec.Code)
	}
}

func TestTasks_FilteringAndBlockerClearing(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_filter.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	_ = db.CreateTask(&Task{
		ID:              "task_f1",
		Title:           "Modemobile NGL Task",
		GroupName:       "Modemobile",
		ProjectName:     "NGL",
		Status:          "IN_PROGRESS",
		Substatus:       "blocked",
		BlockerQuestion: "Which database schema should we use?",
	})
	_ = db.CreateTask(&Task{
		ID:          "task_f2",
		Title:       "Ackbar Control Plane Task",
		GroupName:   "Ackbar",
		ProjectName: "Ackbar",
		Status:      "DONE",
		Substatus:   "active",
	})

	// 1. Filter by status=IN_PROGRESS
	req := httptest.NewRequest("GET", "/v1/tasks?status=IN_PROGRESS", nil)
	rec := httptest.NewRecorder()
	server.handleTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rec.Code)
	}
	var res []Task
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res) != 1 || res[0].ID != "task_f1" {
		t.Errorf("Expected 1 task matching status=IN_PROGRESS, got %d", len(res))
	}

	// 2. Filter by group=Ackbar
	req = httptest.NewRequest("GET", "/v1/tasks?group=Ackbar", nil)
	rec = httptest.NewRecorder()
	server.handleTasks(rec, req)
	var resAckbar []Task
	_ = json.Unmarshal(rec.Body.Bytes(), &resAckbar)
	if len(resAckbar) != 1 || resAckbar[0].ID != "task_f2" {
		t.Errorf("Expected 1 task matching group=Ackbar, got %d", len(resAckbar))
	}

	// 3. Clear blocker: POST with substatus=active
	bodyUnblock, _ := json.Marshal(map[string]any{
		"id":        "task_f1",
		"substatus": "active",
	})
	req = httptest.NewRequest("POST", "/v1/tasks", bytes.NewReader(bodyUnblock))
	rec = httptest.NewRecorder()
	server.handleTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on unblock POST, got %d", rec.Code)
	}

	// Verify blocker question was reset in database
	unblocked, err := db.GetTaskByID("task_f1")
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if unblocked.Substatus != "active" {
		t.Errorf("Expected substatus active, got %s", unblocked.Substatus)
	}
	if unblocked.BlockerQuestion != "" {
		t.Errorf("Expected blocker question to be cleared, got: %s", unblocked.BlockerQuestion)
	}
}

func TestTasks_DeleteAndDeduplication(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_del_dedup.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// 1. Create a task with worker, external ref, deliverable
	task := &Task{
		ID:          "task_to_del",
		Title:       "Task to be deleted",
		GroupName:   "Personal",
		ProjectName: "Ackbar",
		Status:      "NEW",
		Substatus:   "discovered",
		Workers: []TaskWorker{
			{SessionID: "sess_1", Agent: "claude-code", Host: "mac", IsActive: true},
		},
		ExternalRefs: []TaskExternalRef{
			{Tracker: "linear", RefKey: "ACK-123"},
		},
		Deliverables: []TaskDeliverable{
			{Kind: "plan", Title: "Brief"},
		},
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 2. Propose a task with identical title -> should deduplicate and return 200 with existing task
	prop := TaskProposePayload{
		Title:       "Task to be deleted",
		GroupName:   "Personal",
		ProjectName: "Ackbar",
	}
	propBody, _ := json.Marshal(prop)
	reqProp := httptest.NewRequest(http.MethodPost, "/v1/tasks/propose", bytes.NewReader(propBody))
	recProp := httptest.NewRecorder()
	server.handleTaskPropose(recProp, reqProp)

	if recProp.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for deduplicated propose, got %d: %s", recProp.Code, recProp.Body.String())
	}
	var dedupTask Task
	_ = json.Unmarshal(recProp.Body.Bytes(), &dedupTask)
	if dedupTask.ID != "task_to_del" {
		t.Errorf("Expected existing task_to_del returned, got %s", dedupTask.ID)
	}

	// Ensure still only 1 task in DB
	allTasks, err := db.GetTasks()
	if err != nil || len(allTasks) != 1 {
		t.Fatalf("Expected 1 task after dedup, got %d (err: %v)", len(allTasks), err)
	}

	// Proposing for a different project with identical title should NOT deduplicate
	propOther := TaskProposePayload{
		Title:       "Task to be deleted",
		GroupName:   "Modemobile",
		ProjectName: "OtherProject",
	}
	propOtherBody, _ := json.Marshal(propOther)
	reqPropOther := httptest.NewRequest(http.MethodPost, "/v1/tasks/propose", bytes.NewReader(propOtherBody))
	recPropOther := httptest.NewRecorder()
	server.handleTaskPropose(recPropOther, reqPropOther)

	if recPropOther.Code != http.StatusCreated {
		t.Errorf("Expected 201 Created for different project propose, got %d", recPropOther.Code)
	}
	var createdOther Task
	_ = json.Unmarshal(recPropOther.Body.Bytes(), &createdOther)
	if createdOther.ID == "task_to_del" {
		t.Errorf("Expected new task ID for different project, got %s", createdOther.ID)
	}
	_ = db.DeleteTask(createdOther.ID)

	// 3. Test DELETE /v1/tasks?id=task_to_del
	reqDel := httptest.NewRequest(http.MethodDelete, "/v1/tasks?id=task_to_del", nil)
	recDel := httptest.NewRecorder()
	server.handleTasks(recDel, reqDel)

	if recDel.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for task deletion, got %d: %s", recDel.Code, recDel.Body.String())
	}

	// Verify task deleted
	fetched, err := db.GetTaskByID("task_to_del")
	if err != nil {
		t.Fatalf("GetTaskByID error: %v", err)
	}
	if fetched != nil {
		t.Errorf("Expected task to be nil after deletion, got %+v", fetched)
	}

	// Verify cascading deletes
	var workerCount, refCount, delCount int
	_ = db.db.QueryRow("SELECT COUNT(*) FROM task_workers WHERE task_id = 'task_to_del'").Scan(&workerCount)
	_ = db.db.QueryRow("SELECT COUNT(*) FROM task_external_refs WHERE task_id = 'task_to_del'").Scan(&refCount)
	_ = db.db.QueryRow("SELECT COUNT(*) FROM task_deliverables WHERE task_id = 'task_to_del'").Scan(&delCount)
	if workerCount != 0 || refCount != 0 || delCount != 0 {
		t.Errorf("Expected 0 cascaded rows, got workers=%d, refs=%d, dels=%d", workerCount, refCount, delCount)
	}

	// 4. Test DELETE /v1/tasks for non-existent task -> 404
	reqDel404 := httptest.NewRequest(http.MethodDelete, "/v1/tasks?id=non_existent", nil)
	recDel404 := httptest.NewRecorder()
	server.handleTasks(recDel404, reqDel404)
	if recDel404.Code != http.StatusNotFound {
		t.Errorf("Expected 404 for non-existent task, got %d", recDel404.Code)
	}

	// 5. Test DELETE /v1/tasks without ID -> 400
	reqDel400 := httptest.NewRequest(http.MethodDelete, "/v1/tasks", nil)
	recDel400 := httptest.NewRecorder()
	server.handleTasks(recDel400, reqDel400)
	if recDel400.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for delete without ID, got %d", recDel400.Code)
	}
}

func TestTelemetry_ResolveTaskGroupName(t *testing.T) {
	// 1. Session with NodePath takes precedence
	sessNode := &Session{
		NodePath: "Personal/Ackbar",
		Cwd:      "/Users/dev4u/Work/Ackbar",
	}
	if g := resolveTaskGroupName(sessNode); g != "Personal" {
		t.Errorf("Expected Personal from NodePath, got %q", g)
	}

	sessModemobile := &Session{
		NodePath: "Modemobile/NGL/ngl-ios",
		Cwd:      "/Users/dev4u/Work/ngl-ios",
	}
	if g := resolveTaskGroupName(sessModemobile); g != "Modemobile" {
		t.Errorf("Expected Modemobile from NodePath, got %q", g)
	}

	// 2. Ackbar cwd without NodePath should be Personal, not Modemobile (even if under /Work/Ackbar)
	sessAckbar := &Session{
		Cwd:        "/Users/dev4u/Work/Ackbar",
		ProjectKey: "Ackbar",
	}
	if g := resolveTaskGroupName(sessAckbar); g != "Personal" {
		t.Errorf("Expected Personal for Ackbar in /Work/Ackbar, got %q", g)
	}

	// 3. Modemobile project or account
	sessMode := &Session{
		Cwd:        "/Users/dev4u/Work/ngl-ios",
		ProjectKey: "ngl-ios",
		AccountID:  "claude:work:modemobile",
	}
	if g := resolveTaskGroupName(sessMode); g != "Modemobile" {
		t.Errorf("Expected Modemobile, got %q", g)
	}
}

func TestTask_DeduplicateTasks(t *testing.T) {
	dbFile := "./test_task_dedup.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Insert 3 duplicated tasks for NGL-1041
	for i := 1; i <= 3; i++ {
		task := &Task{
			ID:          fmt.Sprintf("task_dup_%d", i),
			Title:       "NGL-1041",
			GroupName:   "Modemobile",
			ProjectName: "ngl-android",
			Status:      "IN_PROGRESS",
			Substatus:   "active",
			ExternalRefs: []TaskExternalRef{
				{
					Tracker: "jira",
					RefKey:  "NGL-1041",
				},
			},
		}
		if err := db.CreateTask(task); err != nil {
			t.Fatalf("CreateTask failed: %v", err)
		}
	}

	allTasks, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(allTasks) != 3 {
		t.Fatalf("Expected 3 tasks before dedup, got %d", len(allTasks))
	}

	deleted, err := db.DeduplicateTasks()
	if err != nil {
		t.Fatalf("DeduplicateTasks failed: %v", err)
	}
	if deleted != 2 {
		t.Errorf("Expected 2 deleted tasks, got %d", deleted)
	}

	allTasks, err = db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(allTasks) != 1 {
		t.Errorf("Expected 1 task remaining after dedup, got %d", len(allTasks))
	}
}

func TestTask_DeduplicateTasks_BranchAware(t *testing.T) {
	dbFile := "./test_task_dedup_branch.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// 1. Legitimate task A for NGL-1041 with its own branch and PR
	taskA := &Task{
		ID:          "task_ngl1041_branch",
		Title:       "NGL-1041 — Add Firebase Crashlytics (iOS)",
		GroupName:   "Modemobile",
		ProjectName: "ngl-ios",
		Status:      "REVIEW",
		Substatus:   "active",
		Branch:      "NGL-1041-add-firebase-crashlytics",
		PRURL:       "https://github.com/CurrentMobile/ngl-ios/pull/561",
		PRNumber:    561,
		ExternalRefs: []TaskExternalRef{
			{Tracker: "jira", RefKey: "NGL-1041"},
		},
	}
	if err := db.CreateTask(taskA); err != nil {
		t.Fatalf("CreateTask A failed: %v", err)
	}
	_ = db.InsertTaskWorker(&TaskWorker{
		TaskID:    taskA.ID,
		SessionID: "sess_worker_a",
		Agent:     "claude-code",
		Host:      "macbook",
		IsActive:  true,
	})

	// 2. Legitimate task B with a completely DIFFERENT branch, which happens to reference NGL-1041 in external refs
	taskB := &Task{
		ID:          "task_icloud_release",
		Title:       "icloud restore",
		GroupName:   "Modemobile",
		ProjectName: "ngl-ios",
		Status:      "REVIEW",
		Substatus:   "active",
		Branch:      "ci/release-submit-only-and-phased-release",
		PRURL:       "https://github.com/CurrentMobile/ngl-ios/pull/560",
		PRNumber:    560,
		ExternalRefs: []TaskExternalRef{
			{Tracker: "jira", RefKey: "NGL-1040"},
			{Tracker: "jira", RefKey: "NGL-1041"},
			{Tracker: "jira", RefKey: "NGL-974"},
		},
	}
	if err := db.CreateTask(taskB); err != nil {
		t.Fatalf("CreateTask B failed: %v", err)
	}
	// Give task B more workers than task A
	for w := 1; w <= 3; w++ {
		_ = db.InsertTaskWorker(&TaskWorker{
			TaskID:    taskB.ID,
			SessionID: fmt.Sprintf("sess_worker_b_%d", w),
			Agent:     "claude-code",
			Host:      "macbook",
			IsActive:  true,
		})
	}

	// 3. Three orphaned empty-branch tasks for NGL-1041 (created during planning before branch existed)
	for i := 1; i <= 3; i++ {
		taskDup := &Task{
			ID:          fmt.Sprintf("task_orphan_%d", i),
			Title:       "NGL-1041",
			GroupName:   "Modemobile",
			ProjectName: "ngl-ios",
			Status:      "IN_PROGRESS",
			Substatus:   "active",
			Branch:      "",
			ExternalRefs: []TaskExternalRef{
				{Tracker: "jira", RefKey: "NGL-1041"},
			},
		}
		if err := db.CreateTask(taskDup); err != nil {
			t.Fatalf("CreateTask orphan %d failed: %v", i, err)
		}
	}

	tasksBefore, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(tasksBefore) != 5 {
		t.Fatalf("Expected 5 tasks before dedup, got %d", len(tasksBefore))
	}

	deleted, err := db.DeduplicateTasks()
	if err != nil {
		t.Fatalf("DeduplicateTasks failed: %v", err)
	}
	if deleted != 3 {
		t.Errorf("Expected exactly 3 orphaned tasks deleted, got %d", deleted)
	}

	tasksAfter, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(tasksAfter) != 2 {
		t.Fatalf("Expected exactly 2 tasks remaining, got %d", len(tasksAfter))
	}

	// Verify Task A was preserved as the primary for NGL-1041
	savedA, err := db.GetTaskByID("task_ngl1041_branch")
	if err != nil || savedA == nil {
		t.Fatalf("Task A should NOT have been deleted: %v", err)
	}
	if savedA.Branch != "NGL-1041-add-firebase-crashlytics" {
		t.Errorf("Expected Task A branch preserved, got %q", savedA.Branch)
	}
	if savedA.PRNumber != 561 {
		t.Errorf("Expected Task A PR 561 preserved, got %d", savedA.PRNumber)
	}

	// Verify Task B was also preserved with its own distinct branch
	savedB, err := db.GetTaskByID("task_icloud_release")
	if err != nil || savedB == nil {
		t.Fatalf("Task B should NOT have been deleted: %v", err)
	}
	if savedB.Branch != "ci/release-submit-only-and-phased-release" {
		t.Errorf("Expected Task B branch preserved, got %q", savedB.Branch)
	}
	if savedB.PRNumber != 560 {
		t.Errorf("Expected Task B PR 560 preserved, got %d", savedB.PRNumber)
	}
}

func TestServer_TaskDeduplicateEndpoint(t *testing.T) {
	dbFile := "./test_task_dedup_endpoint.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{db: db}

	// Insert duplicate tasks
	for i := 1; i <= 2; i++ {
		task := &Task{
			ID:          fmt.Sprintf("task_dup_ep_%d", i),
			Title:       "DUP-100",
			GroupName:   "Personal",
			ProjectName: "Ackbar",
			Status:      "IN_PROGRESS",
			ExternalRefs: []TaskExternalRef{
				{Tracker: "jira", RefKey: "DUP-100"},
			},
		}
		_ = db.CreateTask(task)
	}

	// 1. GET not allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/tasks/deduplicate", nil)
	wGet := httptest.NewRecorder()
	server.handleTaskDeduplicate(wGet, reqGet)
	if wGet.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405 Method Not Allowed, got %d", wGet.Code)
	}

	// 2. POST triggers deduplication
	reqPost := httptest.NewRequest(http.MethodPost, "/v1/tasks/deduplicate", nil)
	wPost := httptest.NewRecorder()
	server.handleTaskDeduplicate(wPost, reqPost)
	if wPost.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", wPost.Code, wPost.Body.String())
	}

	var resp struct {
		Deleted int64  `json:"deleted"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(wPost.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if resp.Deleted != 1 {
		t.Errorf("Expected 1 deleted task, got %d", resp.Deleted)
	}
	if resp.Status != "success" {
		t.Errorf("Expected status 'success', got %q", resp.Status)
	}
}

func TestDB_PruneAllLegacyTasksOnce(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_prune.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Seed legacy tasks and child rows
	task1 := &Task{
		ID:          "legacy_task_1",
		Title:       "Old Ambient Task 1",
		GroupName:   "Modemobile",
		ProjectName: "ngl-ios",
		Status:      "IN_PROGRESS",
		Workers: []TaskWorker{
			{
				TaskID:     "legacy_task_1",
				SessionID:  "sess_1",
				Host:       "host1",
				Agent:      "claude",
				AssignedAt: time.Now().UTC(),
				IsActive:   true,
			},
		},
		Deliverables: []TaskDeliverable{
			{
				ID:     "del_1",
				TaskID: "legacy_task_1",
				Kind:   "plan",
				Title:  "Old Plan",
				Host:   "host1",
			},
		},
	}
	task2 := &Task{
		ID:          "legacy_task_2",
		Title:       "Old Ambient Task 2",
		GroupName:   "Modemobile",
		ProjectName: "ngl-android",
		Status:      "NEW",
	}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("CreateTask 1 failed: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("CreateTask 2 failed: %v", err)
	}

	// Reset the setting flag so PruneAllLegacyTasksOnce runs
	if _, err := db.db.Exec("UPDATE settings SET value = 'false' WHERE key = 'legacy_tasks_pruned_v1'"); err != nil {
		t.Fatalf("Failed to reset setting: %v", err)
	}

	count, err := db.PruneAllLegacyTasksOnce()
	if err != nil {
		t.Fatalf("PruneAllLegacyTasksOnce failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 tasks pruned, got %d", count)
	}

	tasks, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("Expected 0 active tasks after pruning, got %d", len(tasks))
	}

	// Verify child table rows were cleaned
	var workerCount int
	_ = db.db.QueryRow("SELECT COUNT(*) FROM task_workers").Scan(&workerCount)
	if workerCount != 0 {
		t.Errorf("Expected 0 task_workers remaining, got %d", workerCount)
	}

	// Running again should be a no-op (idempotent)
	count2, err := db.PruneAllLegacyTasksOnce()
	if err != nil {
		t.Fatalf("Second PruneAllLegacyTasksOnce failed: %v", err)
	}
	if count2 != 0 {
		t.Errorf("Expected 0 pruned on second run, got %d", count2)
	}
}

func TestClassifyWorkflowNodeState_DoneAndCompleted(t *testing.T) {
	for _, node := range []string{"done", "DONE", "completed", "COMPLETED", "N14", "N15"} {
		st, sub := ClassifyWorkflowNodeState(node, StateWorking, false)
		if st != "DONE" || sub != "completed" {
			t.Errorf("ClassifyWorkflowNodeState(%q) = (%q, %q), want (DONE, completed)", node, st, sub)
		}
	}

	// Test blocked state
	st, sub := ClassifyWorkflowNodeState("N4", StateBlocked, true)
	if st != "IN_PROGRESS" || sub != "blocked" {
		t.Errorf("Expected IN_PROGRESS/blocked for blocked state, got %s/%s", st, sub)
	}

	// Test review state
	st, sub = ClassifyWorkflowNodeState("N12", StateWorking, false)
	if st != "REVIEW" || sub != "pr_review" {
		t.Errorf("Expected REVIEW/pr_review for N12, got %s/%s", st, sub)
	}

	// Test working state
	st, sub = ClassifyWorkflowNodeState("N4", StateWorking, false)
	if st != "IN_PROGRESS" || sub != "active" {
		t.Errorf("Expected IN_PROGRESS/active for N4, got %s/%s", st, sub)
	}
}

func TestTasks_LazyDeliverableContent(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_lazy_del.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{db: db}

	heavyContent := "# Architecture Plan\n\nDetailed breakdown with diagrams and steps..."
	del := TaskDeliverable{
		ID:        "del_heavy_1",
		TaskID:    "task_lazy_test",
		Kind:      "plan",
		Title:     "Architecture Plan",
		Host:      "mac",
		ContentMD: heavyContent,
	}
	task := &Task{
		ID:           "task_lazy_test",
		Title:        "Task with heavy deliverable",
		GroupName:    "Modemobile",
		ProjectName:  "Ackbar",
		Status:       "IN_PROGRESS",
		Deliverables: []TaskDeliverable{del},
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 1. GET /v1/tasks without include_content -> ContentMD is stripped, HasContentMD is true
	req1 := httptest.NewRequest("GET", "/v1/tasks", nil)
	rec1 := httptest.NewRecorder()
	server.handleTasks(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("GET /v1/tasks returned %d", rec1.Code)
	}

	var tasksResp1 []Task
	if err := json.Unmarshal(rec1.Body.Bytes(), &tasksResp1); err != nil {
		t.Fatalf("Failed to decode tasks: %v", err)
	}
	if len(tasksResp1) != 1 || len(tasksResp1[0].Deliverables) != 1 {
		t.Fatalf("Expected 1 task with 1 deliverable, got %+v", tasksResp1)
	}
	d1 := tasksResp1[0].Deliverables[0]
	if d1.ContentMD != "" {
		t.Errorf("Expected empty ContentMD in standard list, got %q", d1.ContentMD)
	}
	if !d1.HasContentMD {
		t.Errorf("Expected HasContentMD to be true")
	}

	// 2. GET /v1/tasks?include_content=true -> ContentMD is included
	req2 := httptest.NewRequest("GET", "/v1/tasks?include_content=true", nil)
	rec2 := httptest.NewRecorder()
	server.handleTasks(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /v1/tasks?include_content=true returned %d", rec2.Code)
	}

	var tasksResp2 []Task
	if err := json.Unmarshal(rec2.Body.Bytes(), &tasksResp2); err != nil {
		t.Fatalf("Failed to decode tasks: %v", err)
	}
	d2 := tasksResp2[0].Deliverables[0]
	if d2.ContentMD != heavyContent {
		t.Errorf("Expected full ContentMD, got %q", d2.ContentMD)
	}

	// 3. GET /v1/tasks/deliverable?id=del_heavy_1 -> returns full deliverable JSON
	req3 := httptest.NewRequest("GET", "/v1/tasks/deliverable?id="+del.ID, nil)
	rec3 := httptest.NewRecorder()
	server.handleTaskDeliverable(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("GET /v1/tasks/deliverable returned %d: %s", rec3.Code, rec3.Body.String())
	}

	var singleDel TaskDeliverable
	if err := json.Unmarshal(rec3.Body.Bytes(), &singleDel); err != nil {
		t.Fatalf("Failed to decode single deliverable: %v", err)
	}
	if singleDel.ID != del.ID || singleDel.ContentMD != heavyContent {
		t.Errorf("Single deliverable mismatch: ID=%q, ContentMD=%q", singleDel.ID, singleDel.ContentMD)
	}
}

func TestTasks_ReconcilePRs(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_reconcile.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{db: db}

	task := &Task{
		ID:          "task_open_pr",
		Title:       "Fix network reconnect",
		GroupName:   "Modemobile",
		ProjectName: "ngl-ios",
		Status:      "IN_PROGRESS",
		Substatus:   "active",
		PRURL:       "https://github.com/modemobile/ngl-ios/pull/777",
		PRState:     "OPEN",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Mock checkGitHubPRState
	origCheck := checkGitHubPRState
	defer func() { checkGitHubPRState = origCheck }()

	checkGitHubPRState = func(ctx context.Context, prURL string) (string, error) {
		if prURL == "https://github.com/modemobile/ngl-ios/pull/777" {
			return "MERGED", nil
		}
		return "OPEN", nil
	}

	// Call GET /v1/tasks?reconcile_prs=true
	req := httptest.NewRequest("GET", "/v1/tasks?reconcile_prs=true", nil)
	rec := httptest.NewRecorder()
	server.handleTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/tasks?reconcile_prs=true returned %d", rec.Code)
	}

	updated, err := db.GetTaskByID(task.ID)
	if err != nil || updated == nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if updated.Status != "DONE" || updated.Substatus != "completed" {
		t.Errorf("Expected task status DONE/completed, got %s/%s", updated.Status, updated.Substatus)
	}
	if updated.PRState != "MERGED" {
		t.Errorf("Expected PRState MERGED, got %s", updated.PRState)
	}
	if updated.CompletedAt == nil {
		t.Errorf("Expected CompletedAt to be set")
	}
}

func TestServer_NormalizeTaskNodeScope(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_normalize.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{db: db}

	// Register a tree node
	node := &TreeNode{
		Path:       "Modemobile/NGL/ngl-ios",
		GitURL:     "github.com/currentmobile/ngl-ios",
		ProjectDir: "/Users/dev4u/Work/ngl-ios",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := db.SaveNode(node); err != nil {
		t.Fatalf("SaveNode failed: %v", err)
	}

	// Case 1: Full path passed in projectName
	g, p := server.normalizeTaskNodeScope("", "Modemobile/NGL/ngl-ios", "", "")
	if g != "Modemobile" || p != "NGL/ngl-ios" {
		t.Errorf("Case 1: got (%q, %q), want (Modemobile, NGL/ngl-ios)", g, p)
	}

	// Case 2: Group duplicate prefix in projectName
	g, p = server.normalizeTaskNodeScope("Modemobile", "Modemobile/NGL/ngl-ios", "", "")
	if g != "Modemobile" || p != "NGL/ngl-ios" {
		t.Errorf("Case 2: got (%q, %q), want (Modemobile, NGL/ngl-ios)", g, p)
	}

	// Case 3: Matched against registered node GitURL / leaf name
	g, p = server.normalizeTaskNodeScope("", "ngl-ios", "", "")
	if g != "Modemobile" || p != "NGL/ngl-ios" {
		t.Errorf("Case 3: got (%q, %q), want (Modemobile, NGL/ngl-ios)", g, p)
	}

	// Case 4: Matched against session NodePath
	sess := &Session{
		ID:       "sess-node-1",
		NodePath: "Personal/Ackbar",
	}
	_ = db.SaveSession(sess)

	g, p = server.normalizeTaskNodeScope("", "", "", sess.ID)
	if g != "Personal" || p != "Ackbar" {
		t.Errorf("Case 4: got (%q, %q), want (Personal, Ackbar)", g, p)
	}
}
