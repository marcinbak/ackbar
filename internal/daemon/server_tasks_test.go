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
}
