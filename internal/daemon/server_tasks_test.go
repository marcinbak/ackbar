package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
