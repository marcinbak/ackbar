package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

func TestTelemetry_ToolInterceptionAndSync(t *testing.T) {
	dbFile := "./test_telemetry.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	// 1. Test SyncSessionTaskWorker auto-creates task for feature branch
	sess := &Session{
		ID:         "claude:mac:uuid-456",
		Agent:      "claude-code",
		Host:       "mac",
		GitBranch:  "feat/NGL-409-reconnect",
		Cwd:        "/Users/dev4u/Work/ngl-ios",
		ProjectKey: "ngl-ios",
		AccountID:  "claude-code:work",
		State:      StateWorking,
	}

	server.SyncSessionTaskWorker(sess)

	tasks, err := db.GetTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Expected 1 task auto-created, got %d (err: %v)", len(tasks), err)
	}

	task := &tasks[0]
	if task.Branch != "feat/NGL-409-reconnect" {
		t.Errorf("Expected task branch feat/NGL-409-reconnect, got %s", task.Branch)
	}
	if task.Status != "IN_PROGRESS" || task.Substatus != "active" {
		t.Errorf("Expected status IN_PROGRESS/active, got %s/%s", task.Status, task.Substatus)
	}
	if len(task.Workers) != 1 || task.Workers[0].SessionID != sess.ID {
		t.Errorf("Expected worker bound, got %+v", task.Workers)
	}
	if len(task.ExternalRefs) != 1 || task.ExternalRefs[0].RefKey != "NGL-409" {
		t.Errorf("Expected external ref NGL-409, got %+v", task.ExternalRefs)
	}

	// 2. Test IngestToolTelemetry: PR URL detection advances to REVIEW
	eventPR := &Event{
		ToolName: "Bash",
		ToolInput: map[string]any{
			"command": "gh pr create --title 'Fix reconnect' && echo 'https://github.com/modemobile/ngl-ios/pull/99'",
		},
	}
	server.IngestToolTelemetry(sess, eventPR)

	taskAfterPR, err := db.GetTaskByID(task.ID)
	if err != nil || taskAfterPR == nil {
		t.Fatalf("Failed to fetch task after PR: %v", err)
	}
	if taskAfterPR.Status != "REVIEW" || taskAfterPR.Substatus != "in_review" {
		t.Errorf("Expected status REVIEW/in_review after PR hook, got %s/%s", taskAfterPR.Status, taskAfterPR.Substatus)
	}
	if taskAfterPR.PRURL != "https://github.com/modemobile/ngl-ios/pull/99" || taskAfterPR.PRNumber != 99 {
		t.Errorf("Expected PR #99 bound, got %s (#%d)", taskAfterPR.PRURL, taskAfterPR.PRNumber)
	}

	// 3. Test Session Blocked state sync
	sess.State = StateBlocked
	sess.Blocked = &Blocked{
		Kind:     BlockQuestion,
		Question: "Which database should we use?",
	}
	server.SyncSessionTaskWorker(sess)

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
	sess.State = StateWorking
	sess.Blocked = nil
	server.SyncSessionTaskWorker(sess)

	taskUnblocked, err := db.GetTaskByID(task.ID)
	if err != nil || taskUnblocked == nil {
		t.Fatalf("Failed to fetch task after unblock: %v", err)
	}
	if taskUnblocked.Substatus != "active" || taskUnblocked.BlockerQuestion != "" {
		t.Errorf("Expected substatus active and empty blocker question, got %s / %q", taskUnblocked.Substatus, taskUnblocked.BlockerQuestion)
	}

	// 5. Test File Deliverable Ingestion
	eventFile := &Event{
		ToolName: "write_to_file",
		ToolInput: map[string]any{
			"TargetFile": "/Users/dev4u/Work/ngl-ios/docs/mockup.png",
		},
	}
	server.IngestToolTelemetry(sess, eventFile)

	taskWithDel, err := db.GetTaskByID(task.ID)
	if err != nil || len(taskWithDel.Deliverables) == 0 {
		t.Fatalf("Expected deliverable attached to task, got %+v", taskWithDel.Deliverables)
	}
	if taskWithDel.Deliverables[0].Kind != "mockup" || taskWithDel.Deliverables[0].Title != "mockup.png" {
		t.Errorf("Expected mockup deliverable, got %+v", taskWithDel.Deliverables[0])
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

func TestTelemetry_WorkflowWatcherIngestion(t *testing.T) {
	dbFile := "./test_workflow_watcher.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	tempDir, err := os.MkdirTemp("", "dev-workflow-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jsonPath := tempDir + "/test-repo-ABC-101.json"
	briefPath := tempDir + "/test-repo-ABC-101.brief.md"
	retroPath := tempDir + "/test-repo-ABC-101.retro.md"

	ticket := "ABC-101"
	run := DevWorkflowRun{
		Ticket:   &ticket,
		Repo:     "modemobile/ngl-ios",
		Branch:   "feat/ABC-101-auth",
		Worktree: ".worktrees/feat-ABC-101",
		Node:     "N15",
		PR: &DevWorkflowPR{
			Number: 42,
			URL:    "https://github.com/modemobile/ngl-ios/pull/42",
		},
	}
	runBytes, _ := json.Marshal(run)
	_ = os.WriteFile(jsonPath, runBytes, 0644)

	briefContent := `# Change brief — ABC-101 (Biometric authentication support)

## Acceptance criteria
Enable Face ID and Touch ID authentication across login workflows.`
	_ = os.WriteFile(briefPath, []byte(briefContent), 0644)

	retroContent := `# Retro — feat/ABC-101-auth

## Proposals
- Implement hardware security key backup for enterprise accounts
- Add biometric lock timeout configuration`
	_ = os.WriteFile(retroPath, []byte(retroContent), 0644)

	// Ingest single run
	if err := server.ingestSingleWorkflowRun(jsonPath); err != nil {
		t.Fatalf("ingestSingleWorkflowRun failed: %v", err)
	}

	// Verify main task
	task, err := db.GetTaskByBranch("feat/ABC-101-auth")
	if err != nil || task == nil {
		t.Fatalf("Expected task for branch feat/ABC-101-auth, got nil (err: %v)", err)
	}
	if task.Title != "ABC-101 (Biometric authentication support)" {
		t.Errorf("Expected brief title, got %q", task.Title)
	}
	if task.Status != "DONE" || task.Substatus != "completed" {
		t.Errorf("Expected DONE/completed, got %s/%s", task.Status, task.Substatus)
	}
	if task.PRURL != "https://github.com/modemobile/ngl-ios/pull/42" {
		t.Errorf("Expected PR URL, got %s", task.PRURL)
	}
	if len(task.Deliverables) < 2 {
		t.Errorf("Expected at least 2 deliverables (brief + retro), got %d", len(task.Deliverables))
	}

	// Verify retro proposals were parsed into NEW inbox tasks
	allTasks, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}

	var proposalCount int
	for _, t := range allTasks {
		if t.Status == "NEW" && t.Substatus == "discovered" {
			proposalCount++
		}
	}
	if proposalCount != 2 {
		t.Errorf("Expected 2 discovered proposals in NEW, got %d", proposalCount)
	}
}

func TestTelemetry_HardenedProposalsAndWorkflow(t *testing.T) {
	dbFile := "./test_hardened.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	// 1. Test Node mapping N12, N13 -> REVIEW: in_review, N14, N15 -> DONE: completed
	s12, sub12 := mapWorkflowNodeToTaskStatus("N12")
	if s12 != "REVIEW" || sub12 != "in_review" {
		t.Errorf("Expected N12 to map to REVIEW/in_review, got %s/%s", s12, sub12)
	}
	s13, sub13 := mapWorkflowNodeToTaskStatus("N13")
	if s13 != "REVIEW" || sub13 != "in_review" {
		t.Errorf("Expected N13 to map to REVIEW/in_review, got %s/%s", s13, sub13)
	}
	s14, sub14 := mapWorkflowNodeToTaskStatus("N14")
	if s14 != "DONE" || sub14 != "completed" {
		t.Errorf("Expected N14 to map to DONE/completed, got %s/%s", s14, sub14)
	}

	// 2. Test Retro Proposal parsing with numbers in title and dedup
	tempDir := t.TempDir()
	retroPath := filepath.Join(tempDir, "sample.retro.md")
	retroContent := `# Retro
## Proposals
- 12-factor configuration support
* Add 2FA security prompt
1. 3D map rendering feature
`
	_ = os.WriteFile(retroPath, []byte(retroContent), 0644)

	server.parseRetroProposalsIntoNewTasks(retroPath, "Modemobile", "Ackbar")

	tasks, err := db.GetTasks()
	if err != nil || len(tasks) != 3 {
		t.Fatalf("Expected 3 tasks created from retro proposals, got %d (err: %v)", len(tasks), err)
	}

	// Verify "12-factor" was preserved cleanly
	found12 := false
	for _, tk := range tasks {
		if tk.Title == "12-factor configuration support" {
			found12 = true
			break
		}
	}
	if !found12 {
		t.Errorf("Expected title '12-factor configuration support' preserved intact, got tasks: %+v", tasks)
	}

	// Move one task to IN_PROGRESS
	tasks[0].Status = "IN_PROGRESS"
	if err := db.UpdateTask(&tasks[0]); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	// Run parseRetroProposalsIntoNewTasks again — should not create duplicate tasks!
	server.parseRetroProposalsIntoNewTasks(retroPath, "Modemobile", "Ackbar")
	tasksAfter, err := db.GetTasks()
	if err != nil || len(tasksAfter) != 3 {
		t.Errorf("Expected 3 tasks (no duplicates created on second run), got %d", len(tasksAfter))
	}

	// 3. Test handleTaskPropose validation
	// Empty title rejected
	bodyEmpty, _ := json.Marshal(map[string]any{"title": ""})
	req := httptest.NewRequest("POST", "/v1/tasks/propose", bytes.NewReader(bodyEmpty))
	rec := httptest.NewRecorder()
	server.handleTaskPropose(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for empty propose title, got %d", rec.Code)
	}

	// 4. Test handleTaskDeliverable validation
	// Path traversal rejected
	bodyTraversal, _ := json.Marshal(map[string]any{
		"task_id":   tasks[0].ID,
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
		"task_id": tasks[0].ID,
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
		"task_id": tasks[0].ID,
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

func TestWorkflowWatcher_Deduplication(t *testing.T) {
	dbFile := "./test_workflow_dedup.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	tempDir, err := os.MkdirTemp("", "dev-workflow-dedup-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jsonPath := filepath.Join(tempDir, "ngl-android-NGL-993.json")
	ticket := "NGL-993"

	// 1. Initial planning state: Branch and Worktree are empty (Node N2)
	run := DevWorkflowRun{
		Ticket:   &ticket,
		Repo:     "ngl-android",
		Branch:   "",
		Worktree: "",
		Node:     "N2",
		Notes:    "Initial planning",
	}
	data, _ := json.Marshal(run)
	_ = os.WriteFile(jsonPath, data, 0644)

	// Ingest 3 times in a row (simulating 30s background loop)
	for i := 0; i < 3; i++ {
		if err := server.ingestSingleWorkflowRun(jsonPath); err != nil {
			t.Fatalf("ingestSingleWorkflowRun attempt %d failed: %v", i+1, err)
		}
	}

	tasks, err := db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected exactly 1 task after 3 ingestions, got %d", len(tasks))
	}
	if tasks[0].Title != "NGL-993" {
		t.Errorf("Expected title NGL-993, got %q", tasks[0].Title)
	}
	if tasks[0].Status != "IN_PROGRESS" {
		t.Errorf("Expected status IN_PROGRESS, got %s", tasks[0].Status)
	}

	// 2. Transition to Node N4: Branch and Worktree are now created
	run.Branch = "feat/NGL-993-size-matters"
	run.Worktree = "/home/dev4u/Work/ngl-android/.claude/worktrees/NGL-993"
	run.Node = "N4"
	data, _ = json.Marshal(run)
	_ = os.WriteFile(jsonPath, data, 0644)

	if err := server.ingestSingleWorkflowRun(jsonPath); err != nil {
		t.Fatalf("ingestSingleWorkflowRun transition to N4 failed: %v", err)
	}

	tasks, err = db.GetTasks()
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected still exactly 1 task after branch creation, got %d", len(tasks))
	}
	if tasks[0].Branch != "feat/NGL-993-size-matters" {
		t.Errorf("Expected updated branch, got %q", tasks[0].Branch)
	}
	if tasks[0].WorktreePath != "/home/dev4u/Work/ngl-android/.claude/worktrees/NGL-993" {
		t.Errorf("Expected updated worktree, got %q", tasks[0].WorktreePath)
	}

	// 3. Test string PR URL unmarshaling and transition to REVIEW
	jsonContent := `{
		"ticket": "NGL-993",
		"repo": "ngl-android",
		"branch": "feat/NGL-993-size-matters",
		"worktree": "/home/dev4u/Work/ngl-android/.claude/worktrees/NGL-993",
		"node": "N12",
		"pr": "https://github.com/CurrentMobile/ngl-android/pull/555"
	}`
	_ = os.WriteFile(jsonPath, []byte(jsonContent), 0644)

	if err := server.ingestSingleWorkflowRun(jsonPath); err != nil {
		t.Fatalf("ingestSingleWorkflowRun with string PR failed: %v", err)
	}

	task, err := db.GetTaskByExternalRef("NGL-993")
	if err != nil || task == nil {
		t.Fatalf("GetTaskByExternalRef failed: %v", err)
	}
	if task.PRNumber != 555 {
		t.Errorf("Expected PR number 555, got %d", task.PRNumber)
	}
	if task.Status != "REVIEW" {
		t.Errorf("Expected status REVIEW, got %s", task.Status)
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
