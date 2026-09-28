package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStandup_AggregationAndClassification(t *testing.T) {
	dbFile := "./test_standup.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	now := time.Now()
	recentDoneTime := now.Add(-2 * time.Hour)
	oldDoneTime := now.Add(-48 * time.Hour)

	// Task 1: Shipped within 24h
	taskShippedRecent := &Task{
		ID:          "task_shipped_1",
		Title:       "Biometric authentication support",
		GroupName:   "Modemobile",
		ProjectName: "ngl-ios",
		Status:      "DONE",
		Substatus:   "completed",
		PRURL:       "https://github.com/modemobile/ngl-ios/pull/42",
		PRNumber:    42,
		CompletedAt: &recentDoneTime,
		Deliverables: []TaskDeliverable{
			{Title: "Change Brief", Kind: "plan"},
		},
	}
	_ = db.CreateTask(taskShippedRecent)

	// Task 2: Shipped 48h ago (should only appear in >=2 days window)
	taskShippedOld := &Task{
		ID:          "task_shipped_2",
		Title:       "Legacy database migration",
		GroupName:   "Modemobile",
		ProjectName: "ngl-ios",
		Status:      "DONE",
		Substatus:   "completed",
		PRURL:       "https://github.com/modemobile/ngl-ios/pull/40",
		PRNumber:    40,
		CompletedAt: &oldDoneTime,
	}
	_ = db.CreateTask(taskShippedOld)

	// Task 3: Blocked task
	taskBlocked := &Task{
		ID:              "task_blocked_1",
		Title:           "Fix image generation memory leak",
		GroupName:       "Modemobile",
		ProjectName:     "ngl-android",
		Status:          "IN_PROGRESS",
		Substatus:       "blocked",
		BlockerQuestion: "Should bitmap textures be downscaled or heap ceiling increased?",
		Workers: []TaskWorker{
			{SessionID: "claude:mac:1", Agent: "claude-code", Host: "macbook", IsActive: true},
		},
	}
	_ = db.CreateTask(taskBlocked)

	// Task 4: In Progress / Review with Jira RefKey
	taskReview := &Task{
		ID:          "task_review_1",
		Title:       "Ad mediation rate-limiting",
		GroupName:   "Modemobile",
		ProjectName: "MEA",
		Status:      "REVIEW",
		Substatus:   "in_review",
		Branch:      "feat/rate-limit",
		PRURL:       "https://github.com/modemobile/mea/pull/56",
		PRNumber:    56,
		Workers: []TaskWorker{
			{SessionID: "codex:legion:1", Agent: "codex", Host: "legion", IsActive: true},
		},
		ExternalRefs: []TaskExternalRef{
			{RefKey: "MEA-142", Tracker: "jira"},
		},
	}
	_ = db.CreateTask(taskReview)

	// Task 5: Discovered new task
	taskDiscovered := &Task{
		ID:          "task_disc_1",
		Title:       "Explore WebTransport multiplexing",
		GroupName:   "Modemobile",
		ProjectName: "Ackbar",
		Status:      "NEW",
		Substatus:   "discovered",
		Notes:       "Discovered during telemetry review",
	}
	_ = db.CreateTask(taskDiscovered)

	// Task 6: Completed task with leftover stale BlockerQuestion (must NOT be counted as blocked)
	taskDoneStaleBlocker := &Task{
		ID:              "task_shipped_stale_blocker",
		Title:           "Secure Keychain storage",
		GroupName:       "Modemobile",
		ProjectName:     "ngl-ios",
		Status:          "DONE",
		Substatus:       "completed",
		BlockerQuestion: "Stale question that was resolved before merge",
		CompletedAt:     &recentDoneTime,
	}
	_ = db.CreateTask(taskDoneStaleBlocker)

	// 1. Test 1-Day Standup
	report1, err := server.BuildStandupReport("Modemobile", 1)
	if err != nil {
		t.Fatalf("BuildStandupReport failed: %v", err)
	}

	if report1.TotalShipped != 2 {
		t.Errorf("Expected 2 shipped tasks in 1-day window, got %d", report1.TotalShipped)
	}
	if report1.TotalBlocked != 1 {
		t.Errorf("Expected 1 blocked task, got %d", report1.TotalBlocked)
	}
	if report1.TotalInProgress != 1 {
		t.Errorf("Expected 1 in-progress task, got %d", report1.TotalInProgress)
	}
	if report1.TotalDiscovered != 1 {
		t.Errorf("Expected 1 discovered task, got %d", report1.TotalDiscovered)
	}

	// Verify Markdown content
	if !strings.Contains(report1.Markdown, "[🚀 SHIPPED] Biometric authentication support (PR #42)") {
		t.Errorf("Expected shipped item with PR in markdown, got:\n%s", report1.Markdown)
	}
	if !strings.Contains(report1.Markdown, "[⚠️ BLOCKED] Fix image generation memory leak") {
		t.Errorf("Expected blocked item in markdown, got:\n%s", report1.Markdown)
	}
	if !strings.Contains(report1.Markdown, "Should bitmap textures be downscaled or heap ceiling increased?") {
		t.Errorf("Expected blocker question in markdown, got:\n%s", report1.Markdown)
	}
	if !strings.Contains(report1.Markdown, "[🔍 IN REVIEW] [MEA-142] Ad mediation rate-limiting") {
		t.Errorf("Expected in review item with external ref key in markdown, got:\n%s", report1.Markdown)
	}

	// Verify Spoken Briefing content
	if !strings.Contains(report1.SpokenBriefing, "Attention is required on blocked tasks") {
		t.Errorf("Expected blocker urgency in spoken briefing, got:\n%s", report1.SpokenBriefing)
	}
	if !strings.Contains(report1.SpokenBriefing, "Should bitmap textures be downscaled") {
		t.Errorf("Expected blocker question spoken, got:\n%s", report1.SpokenBriefing)
	}
	if !strings.Contains(report1.SpokenBriefing, "Recently shipped") {
		t.Errorf("Expected recently shipped section in spoken briefing, got:\n%s", report1.SpokenBriefing)
	}

	// 2. Test 3-Day Standup (Retrospective includes older shipped task)
	report3, err := server.BuildStandupReport("Modemobile", 3)
	if err != nil {
		t.Fatalf("BuildStandupReport failed: %v", err)
	}

	if report3.TotalShipped != 3 {
		t.Errorf("Expected 3 shipped tasks in 3-day retrospective window, got %d", report3.TotalShipped)
	}
}

func TestStandup_HTTPHandlers(t *testing.T) {
	dbFile := "./test_standup_http.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	task := &Task{
		ID:          "task_test_01",
		Title:       "Test task for HTTP endpoints",
		GroupName:   "Modemobile",
		ProjectName: "Ackbar",
		Status:      "REVIEW",
		Substatus:   "in_review",
		PRURL:       "https://github.com/marcinbak/ackbar/pull/999",
	}
	_ = db.CreateTask(task)

	// 1. GET /v1/standup (Default text/markdown)
	req := httptest.NewRequest("GET", "/v1/standup?group=Modemobile", nil)
	rec := httptest.NewRecorder()
	server.handleStandup(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 OK from GET /v1/standup, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/markdown") {
		t.Errorf("Expected text/markdown content type, got %s", ct)
	}
	if !strings.Contains(rec.Body.String(), "## 📅 Daily Standup") {
		t.Errorf("Expected markdown title in response body, got:\n%s", rec.Body.String())
	}

	// 2. GET /v1/standup?format=json
	reqJSON := httptest.NewRequest("GET", "/v1/standup?group=Modemobile&format=json", nil)
	recJSON := httptest.NewRecorder()
	server.handleStandup(recJSON, reqJSON)

	if recJSON.Code != http.StatusOK {
		t.Errorf("Expected 200 OK from GET /v1/standup?format=json, got %d", recJSON.Code)
	}
	var report StandupReport
	if err := json.Unmarshal(recJSON.Body.Bytes(), &report); err != nil {
		t.Fatalf("Failed to parse JSON standup report: %v", err)
	}
	if report.Group != "Modemobile" {
		t.Errorf("Expected group Modemobile, got %s", report.Group)
	}

	// 3. POST /v1/briefings/synthesize
	bodySyn, _ := json.Marshal(map[string]any{"group": "Modemobile", "mode": "standup"})
	reqSyn := httptest.NewRequest("POST", "/v1/briefings/synthesize", bytes.NewReader(bodySyn))
	recSyn := httptest.NewRecorder()
	server.handleBriefingSynthesize(recSyn, reqSyn)

	if recSyn.Code != http.StatusOK {
		t.Errorf("Expected 200 OK from POST /v1/briefings/synthesize, got %d", recSyn.Code)
	}
	var synResp struct {
		SpokenText string `json:"spoken_text"`
		Duration   int    `json:"duration_estimate_sec"`
	}
	if err := json.Unmarshal(recSyn.Body.Bytes(), &synResp); err != nil {
		t.Fatalf("Failed to parse synthesize response: %v", err)
	}
	if synResp.SpokenText == "" {
		t.Errorf("Expected non-empty spoken_text in briefing response")
	}
	if synResp.Duration <= 0 {
		t.Errorf("Expected positive duration estimate, got %d", synResp.Duration)
	}

	// 4. POST /v1/tasks/merge-pr validation
	// Missing task_id
	bodyEmpty, _ := json.Marshal(map[string]any{"task_id": ""})
	reqMerge := httptest.NewRequest("POST", "/v1/tasks/merge-pr", bytes.NewReader(bodyEmpty))
	recMerge := httptest.NewRecorder()
	server.handleTaskMergePR(recMerge, reqMerge)
	if recMerge.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for empty task_id, got %d", recMerge.Code)
	}

	// Task not found
	bodyNotFound, _ := json.Marshal(map[string]any{"task_id": "nonexistent_task"})
	reqMerge = httptest.NewRequest("POST", "/v1/tasks/merge-pr", bytes.NewReader(bodyNotFound))
	recMerge = httptest.NewRecorder()
	server.handleTaskMergePR(recMerge, reqMerge)
	if recMerge.Code != http.StatusNotFound {
		t.Errorf("Expected 404 for nonexistent task, got %d", recMerge.Code)
	}

	// Invalid merge method
	bodyBadMethod, _ := json.Marshal(map[string]any{"task_id": task.ID, "method": "invalid_method"})
	reqMerge = httptest.NewRequest("POST", "/v1/tasks/merge-pr", bytes.NewReader(bodyBadMethod))
	recMerge = httptest.NewRecorder()
	server.handleTaskMergePR(recMerge, reqMerge)
	if recMerge.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for invalid merge method, got %d", recMerge.Code)
	}

	// Task without PR
	taskNoPR := &Task{
		ID:          "task_no_pr",
		Title:       "Task without PR",
		GroupName:   "Modemobile",
		ProjectName: "Ackbar",
		Status:      "REVIEW",
	}
	_ = db.CreateTask(taskNoPR)

	bodyNoPR, _ := json.Marshal(map[string]any{"task_id": taskNoPR.ID})
	reqMerge = httptest.NewRequest("POST", "/v1/tasks/merge-pr", bytes.NewReader(bodyNoPR))
	recMerge = httptest.NewRecorder()
	server.handleTaskMergePR(recMerge, reqMerge)
	if recMerge.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for task without PR URL, got %d", recMerge.Code)
	}

	// Task already DONE
	taskDone := &Task{
		ID:          "task_already_done",
		Title:       "Task already completed",
		GroupName:   "Modemobile",
		ProjectName: "Ackbar",
		Status:      "DONE",
		PRURL:       "https://github.com/marcinbak/ackbar/pull/101",
	}
	_ = db.CreateTask(taskDone)

	bodyDone, _ := json.Marshal(map[string]any{"task_id": taskDone.ID})
	reqMerge = httptest.NewRequest("POST", "/v1/tasks/merge-pr", bytes.NewReader(bodyDone))
	recMerge = httptest.NewRecorder()
	server.handleTaskMergePR(recMerge, reqMerge)
	if recMerge.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for already DONE task, got %d", recMerge.Code)
	}

	// Task unapproved / IN_PROGRESS
	taskInProg := &Task{
		ID:          "task_still_in_progress",
		Title:       "Task not ready for review",
		GroupName:   "Modemobile",
		ProjectName: "Ackbar",
		Status:      "IN_PROGRESS",
		PRURL:       "https://github.com/marcinbak/ackbar/pull/102",
	}
	_ = db.CreateTask(taskInProg)

	bodyInProg, _ := json.Marshal(map[string]any{"task_id": taskInProg.ID})
	reqMerge = httptest.NewRequest("POST", "/v1/tasks/merge-pr", bytes.NewReader(bodyInProg))
	recMerge = httptest.NewRecorder()
	server.handleTaskMergePR(recMerge, reqMerge)
	if recMerge.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for unapproved/IN_PROGRESS task, got %d", recMerge.Code)
	}
}
