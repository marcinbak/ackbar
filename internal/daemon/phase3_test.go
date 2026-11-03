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

func TestPhase3_DatabaseAndWorkstreams(t *testing.T) {
	dbFile := "./test_phase3_db.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// 1. Create a task with TaskKind = "design" and multiple workstreams
	now := time.Now().UTC()
	task := &Task{
		ID:          "task_p3_001",
		Title:       "Native Cloud Artifacts & Workflows",
		GroupName:   "Personal",
		ProjectName: "Ackbar",
		TaskKind:    "design",
		Status:      "IN_PROGRESS",
		Substatus:   "active",
		CreatedAt:   now,
		UpdatedAt:   now,
		Workers: []TaskWorker{
			{
				SessionID:    "claude:local:sess1",
				Agent:        "claude-code",
				Host:         "local",
				IsActive:     true,
				RepoNodePath: "Personal/Ackbar",
				WorkflowNode: "N2",
				AssignedAt:   now,
			},
		},
		Workstreams: []TaskWorkstream{
			{
				TaskID:       "task_p3_001",
				RepoNodePath: "Personal/Ackbar",
				TargetHost:   "local",
				WorkflowNode: "N2",
				Status:       "IN_PROGRESS",
				UpdatedAt:    now,
			},
			{
				TaskID:       "task_p3_001",
				RepoNodePath: "Modemobile/NGL/ngl-ios",
				TargetHost:   "local",
				DependsOn:    "Personal/Ackbar",
				WorkflowNode: "N0",
				Status:       "QUEUED",
				UpdatedAt:    now,
			},
		},
	}

	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 2. Read back task and verify TaskKind and Workstreams populated
	fetched, err := db.GetTaskByID("task_p3_001")
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if fetched == nil {
		t.Fatalf("Expected task_p3_001, got nil")
	}
	if fetched.TaskKind != "design" {
		t.Errorf("Expected TaskKind 'design', got '%s'", fetched.TaskKind)
	}
	if len(fetched.Workstreams) != 2 {
		t.Fatalf("Expected 2 workstreams, got %d", len(fetched.Workstreams))
	}
	if fetched.Workstreams[1].Status != "QUEUED" || fetched.Workstreams[1].DependsOn != "Personal/Ackbar" {
		t.Errorf("Unexpected workstream 1: %+v", fetched.Workstreams[1])
	}
	if len(fetched.Workers) != 1 || fetched.Workers[0].WorkflowNode != "N2" || fetched.Workers[0].RepoNodePath != "Personal/Ackbar" {
		t.Errorf("Unexpected worker: %+v", fetched.Workers)
	}

	// 3. Test non-destructive TaskWorker conflict update:
	// Inserting heartbeat with empty repo_node_path and workflow_node must not wipe existing metadata!
	heartbeatWorker := &TaskWorker{
		TaskID:     "task_p3_001",
		SessionID:  "claude:local:sess1",
		Agent:      "claude-code",
		Host:       "local",
		IsActive:   true,
		AssignedAt: time.Now().UTC(),
	}
	if err := db.InsertTaskWorker(heartbeatWorker); err != nil {
		t.Fatalf("InsertTaskWorker failed: %v", err)
	}
	refetched, _ := db.GetTaskByID("task_p3_001")
	if len(refetched.Workers) != 1 {
		t.Fatalf("Expected 1 worker, got %d", len(refetched.Workers))
	}
	if refetched.Workers[0].WorkflowNode != "N2" || refetched.Workers[0].RepoNodePath != "Personal/Ackbar" {
		t.Errorf("Worker metadata was wiped by heartbeat! Got %+v", refetched.Workers[0])
	}

	// 4. Test LWW Sync for Workstreams
	syncPayload, err := db.GetSyncStateSince(now.Add(-time.Minute), "local")
	if err != nil {
		t.Fatalf("GetSyncStateSince failed: %v", err)
	}
	if len(syncPayload.Workstreams) != 2 {
		t.Errorf("Expected 2 workstreams in sync payload, got %d", len(syncPayload.Workstreams))
	}

	// Apply incoming remote LWW workstream with newer timestamp
	futureTime := now.Add(5 * time.Minute)
	remotePayload := &SyncStatePayload{
		Workstreams: []TaskWorkstream{
			{
				TaskID:       "task_p3_001",
				RepoNodePath: "Personal/Ackbar",
				TargetHost:   "local",
				WorkflowNode: "N3",
				Status:       "IN_PROGRESS",
				UpdatedAt:    futureTime,
			},
		},
	}
	if _, err := db.ApplySyncState(remotePayload); err != nil {
		t.Fatalf("ApplySyncState failed: %v", err)
	}
	afterSync, _ := db.GetTaskByID("task_p3_001")
	if afterSync.Workstreams[0].WorkflowNode != "N3" {
		t.Errorf("Expected Workstream node 'N3' after LWW sync, got '%s'", afterSync.Workstreams[0].WorkflowNode)
	}
}

func TestPhase3_ClassifyWorkflowNodeState(t *testing.T) {
	tests := []struct {
		name               string
		node               string
		sessState          State
		hasExplicitBlocker bool
		expectedStatus     string
		expectedSubstatus  string
	}{
		{"N0 ready", "N0", StateIdle, false, "NEW", "ready"},
		{"N0 working", "N0", StateWorking, false, "IN_PROGRESS", "active"},
		{"N1 active", "N1", StateWorking, false, "IN_PROGRESS", "active"},
		{"N1 question idle", "N1", StateIdle, false, "IN_PROGRESS", "question"},
		{"N1 question blocked", "N1", StateBlocked, false, "IN_PROGRESS", "question"},
		{"N2 active", "N2", StateWorking, false, "IN_PROGRESS", "active"},
		{"N2 plan review idle", "N2", StateIdle, false, "REVIEW", "plan_review"},
		{"N2 plan review blocked", "N2", StateBlocked, false, "REVIEW", "plan_review"},
		{"N3 active", "N3", StateWorking, false, "IN_PROGRESS", "active"},
		{"N3 stalled idle", "N3", StateIdle, false, "IN_PROGRESS", "stalled"},
		{"N3 blocked explicit", "N3", StateWorking, true, "IN_PROGRESS", "blocked"},
		{"N3 blocked state", "N3", StateBlocked, false, "IN_PROGRESS", "blocked"},
		{"N11 pr_review", "N11", StateWorking, false, "REVIEW", "pr_review"},
		{"N12 pr_review", "N12", StateIdle, false, "REVIEW", "pr_review"},
		{"N14 completed", "N14", StateIdle, false, "DONE", "completed"},
		{"N15 completed", "N15", StateIdle, false, "DONE", "completed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, substatus := ClassifyWorkflowNodeState(tc.node, tc.sessState, tc.hasExplicitBlocker)
			if status != tc.expectedStatus || substatus != tc.expectedSubstatus {
				t.Errorf("ClassifyWorkflowNodeState(%s, %v, %v) = (%s, %s); want (%s, %s)",
					tc.node, tc.sessState, tc.hasExplicitBlocker, status, substatus, tc.expectedStatus, tc.expectedSubstatus)
			}
		})
	}
}

func TestPhase3_SequentialRepoUnlock(t *testing.T) {
	dbFile := "./test_phase3_unlock.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	now := time.Now().UTC()
	task := &Task{
		ID:          "task_unlock_001",
		Title:       "Multi-repo pipeline",
		GroupName:   "Personal",
		ProjectName: "Ackbar",
		Status:      "DONE",
		Substatus:   "completed",
		CompletedAt: &now,
		Workstreams: []TaskWorkstream{
			{
				TaskID:       "task_unlock_001",
				RepoNodePath: "repo1",
				Status:       "DONE",
				WorkflowNode: "N14",
				UpdatedAt:    now,
			},
			{
				TaskID:       "task_unlock_001",
				RepoNodePath: "repo2",
				DependsOn:    "repo1",
				Status:       "QUEUED",
				WorkflowNode: "N0",
				UpdatedAt:    now,
			},
		},
	}

	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	_ = db.InsertTaskDeliverable(&TaskDeliverable{
		TaskID:    "task_unlock_001",
		Kind:      "learnings",
		Title:     "API Schema Learnings",
		ContentMD: "Use proto-based serialization for repo2 clients",
	})

	// Trigger sequential unlock for repo1
	unlocked := server.unlockSequentialWorkstreams(task, "repo1")
	if !unlocked {
		t.Fatalf("Expected unlockSequentialWorkstreams to return true")
	}

	// Task must revert to IN_PROGRESS so it stays active on board for repo2!
	if task.Status != "IN_PROGRESS" || task.Substatus != "active" {
		t.Errorf("Expected task status IN_PROGRESS/active, got %s/%s", task.Status, task.Substatus)
	}
	if task.CompletedAt != nil {
		t.Errorf("Expected CompletedAt to be reset to nil, got %v", task.CompletedAt)
	}

	// repo2 must be unlocked to IN_PROGRESS
	if task.Workstreams[1].Status != "IN_PROGRESS" {
		t.Errorf("Expected repo2 status IN_PROGRESS, got %s", task.Workstreams[1].Status)
	}

	// Verify handoff notes appended from learnings deliverable
	if !strings.Contains(task.Notes, "[Handoff to repo2]") || !strings.Contains(task.Notes, "API Schema Learnings") {
		t.Errorf("Expected handoff note in task.Notes, got %q", task.Notes)
	}
}

func TestPhase3_Deliverables_CloudAutoInferenceAndMarkdown(t *testing.T) {
	dbFile := "./test_phase3_deliv.db"
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
		ID:          "task_deliv_001",
		Title:       "Deliverables Test",
		GroupName:   "Personal",
		ProjectName: "Ackbar",
		Status:      "IN_PROGRESS",
		Substatus:   "active",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 1. Test auto-inference for Google Docs URL
	reqBody, _ := json.Marshal(map[string]any{
		"task_id": "task_deliv_001",
		"title":   "PRD Document",
		"kind":    "prd",
		"url":     "https://docs.google.com/document/d/12345/edit",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/deliverable", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	server.handleTaskDeliverable(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	var d1 TaskDeliverable
	if err := json.NewDecoder(rec.Body).Decode(&d1); err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if d1.Provider != "gdocs" {
		t.Errorf("Expected provider 'gdocs', got '%s'", d1.Provider)
	}

	// 2. Test auto-inference for Claude Artifact URL
	reqBody2, _ := json.Marshal(map[string]any{
		"task_id": "task_deliv_001",
		"title":   "Claude Generative UI Artifact",
		"kind":    "html_ui",
		"url":     "https://claude.site/artifacts/abc-123",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/v1/tasks/deliverable", bytes.NewReader(reqBody2))
	rec2 := httptest.NewRecorder()
	server.handleTaskDeliverable(rec2, req2)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var d2 TaskDeliverable
	_ = json.NewDecoder(rec2.Body).Decode(&d2)
	if d2.Provider != "claude_artifact" {
		t.Errorf("Expected provider 'claude_artifact', got '%s'", d2.Provider)
	}

	// 3. Test pure content_md deliverable with no file_path or url (e.g. learnings)
	reqBody3, _ := json.Marshal(map[string]any{
		"task_id":    "task_deliv_001",
		"title":      "Key Architecture Learnings",
		"kind":       "learnings",
		"content_md": "# Learnings\n- LWW synchronization avoids conflicts.",
	})
	req3 := httptest.NewRequest(http.MethodPost, "/v1/tasks/deliverable", bytes.NewReader(reqBody3))
	rec3 := httptest.NewRecorder()
	server.handleTaskDeliverable(rec3, req3)

	if rec3.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created for pure content_md, got %d: %s", rec3.Code, rec3.Body.String())
	}
	var d3 TaskDeliverable
	_ = json.NewDecoder(rec3.Body).Decode(&d3)
	if d3.ContentMD != "# Learnings\n- LWW synchronization avoids conflicts." {
		t.Errorf("ContentMD mismatch: got %s", d3.ContentMD)
	}

	// 4. Verify deliverables list on task
	fetched, _ := db.GetTaskByID("task_deliv_001")
	if len(fetched.Deliverables) != 3 {
		t.Fatalf("Expected 3 deliverables on task, got %d", len(fetched.Deliverables))
	}
}

func TestPhase3_TasksKindFilterAndWorkflowNode(t *testing.T) {
	dbFile := "./test_phase3_kind.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	_ = db.CreateTask(&Task{
		ID:          "task_code_1",
		Title:       "Code Feature",
		GroupName:   "Personal",
		ProjectName: "Ackbar",
		TaskKind:    "code",
		Status:      "IN_PROGRESS",
	})
	_ = db.CreateTask(&Task{
		ID:          "task_design_1",
		Title:       "Design Mockup",
		GroupName:   "Personal",
		ProjectName: "Ackbar",
		TaskKind:    "design",
		Status:      "IN_PROGRESS",
	})

	// GET /v1/tasks?kind=design
	req := httptest.NewRequest(http.MethodGet, "/v1/tasks?kind=design", nil)
	rec := httptest.NewRecorder()
	server.handleTasks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	var tasks []Task
	if err := json.NewDecoder(rec.Body).Decode(&tasks); err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "task_design_1" {
		t.Errorf("Expected 1 design task, got %d tasks: %+v", len(tasks), tasks)
	}

	// POST /v1/tasks updating workflow_node
	postBody, _ := json.Marshal(map[string]any{
		"id":            "task_code_1",
		"workflow_node": "N2",
	})
	reqPost := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(postBody))
	recPost := httptest.NewRecorder()
	server.handleTasks(recPost, reqPost)

	if recPost.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on update, got %d: %s", recPost.Code, recPost.Body.String())
	}
	updated, _ := db.GetTaskByID("task_code_1")
	if updated.Status != "REVIEW" || updated.Substatus != "plan_review" {
		t.Errorf("Expected status REVIEW/plan_review at N2, got %s/%s", updated.Status, updated.Substatus)
	}
}

func TestPhase3_SpawnValidationAndCwdResolution(t *testing.T) {
	dbFile := "./test_phase3_spawn.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	tempDir := t.TempDir()
	_ = db.SaveNode(&TreeNode{
		Path:       "Modemobile/NGL/ngl-ios",
		ProjectDir: tempDir,
		CreatedAt:  time.Now(),
	})

	// 1. Invalid TaskID must return 400 Bad Request
	spawnBad := map[string]any{
		"agent":       "claude-code",
		"engine_type": "headless",
		"cwd":         tempDir,
		"task_id":     "invalid; rm -rf /",
	}
	bBad, _ := json.Marshal(spawnBad)
	reqBad := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(bBad))
	recBad := httptest.NewRecorder()
	server.handleSpawn(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 for malicious task_id, got %d", recBad.Code)
	}

	// 2. Logical NodePath as cwd resolved to registered ProjectDir
	spawnGood := map[string]any{
		"agent":       "claude-code",
		"engine_type": "headless",
		"cwd":         "Modemobile/NGL/ngl-ios",
		"task_id":     "task_clean-123",
		"task_kind":   "DESIGN",
	}
	bGood, _ := json.Marshal(spawnGood)
	reqGood := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(bGood))
	recGood := httptest.NewRecorder()
	server.handleSpawn(recGood, reqGood)
	if recGood.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for valid spawn, got %d: %s", recGood.Code, recGood.Body.String())
	}
	var respGood map[string]any
	_ = json.NewDecoder(recGood.Body).Decode(&respGood)
	sessID, _ := respGood["session_id"].(string)
	sess, err := db.GetSession(sessID)
	if err != nil || sess == nil {
		t.Fatalf("Spawned session not found in DB: %v", err)
	}
	if sess.Cwd != tempDir {
		t.Errorf("Expected sess.Cwd resolved to %q, got %q", tempDir, sess.Cwd)
	}
}

func TestPhase3_WorkingSessionUnstallsTask(t *testing.T) {
	dbFile := "./test_phase3_unstall.db"
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
		ID:        "task_stalled_01",
		Title:     "Stalled Task",
		Status:    "IN_PROGRESS",
		Substatus: "stalled",
	}
	_ = db.CreateTask(task)

	sess := &Session{
		ID:            "sess_working_01",
		Agent:         "claude-code",
		Host:          "local",
		State:         StateWorking,
		CurrentTaskID: "task_stalled_01",
	}
	_ = db.SaveSession(sess)

	server.SyncSessionTaskWorker(sess)

	updated, _ := db.GetTaskByID("task_stalled_01")
	if updated.Substatus != "active" {
		t.Errorf("Expected task substatus 'active' when worker resumed working, got %q", updated.Substatus)
	}
}

func TestPhase3_UpdateWorkflowNodeTargetsActiveWorkstream(t *testing.T) {
	dbFile := "./test_phase3_target_ws.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{
		db: db,
	}

	now := time.Now().UTC()
	task := &Task{
		ID:        "task_multi_01",
		Title:     "Multi Pipeline",
		Status:    "IN_PROGRESS",
		Substatus: "active",
		Workers: []TaskWorker{
			{
				TaskID:       "task_multi_01",
				SessionID:    "sess_worker_generic",
				RepoNodePath: "",
				WorkflowNode: "N13",
				IsActive:     true,
			},
		},
		Workstreams: []TaskWorkstream{
			{
				TaskID:       "task_multi_01",
				RepoNodePath: "repo-alpha",
				Status:       "IN_PROGRESS",
				WorkflowNode: "N13",
				UpdatedAt:    now,
			},
			{
				TaskID:       "task_multi_01",
				RepoNodePath: "repo-beta",
				DependsOn:    "repo-alpha",
				Status:       "QUEUED",
				WorkflowNode: "N0",
				UpdatedAt:    now,
			},
		},
	}
	_ = db.CreateTask(task)

	// Update task with WorkflowNode N14 without specifying repo_node_path
	postBody, _ := json.Marshal(map[string]any{
		"id":            "task_multi_01",
		"workflow_node": "N14",
	})
	reqPost := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(postBody))
	recPost := httptest.NewRecorder()
	server.handleTasks(recPost, reqPost)

	if recPost.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", recPost.Code, recPost.Body.String())
	}

	updated, _ := db.GetTaskByID("task_multi_01")
	if len(updated.Workstreams) != 2 {
		t.Fatalf("Expected 2 workstreams, got %d", len(updated.Workstreams))
	}
	wsAlpha := updated.Workstreams[0]
	wsBeta := updated.Workstreams[1]

	if wsAlpha.Status != "DONE" || wsAlpha.WorkflowNode != "N14" {
		t.Errorf("Expected repo-alpha to be DONE/N14, got %s/%s", wsAlpha.Status, wsAlpha.WorkflowNode)
	}
	// repo-beta must be unlocked to IN_PROGRESS and retain its workflow_node N0 (NOT overwritten to N14!)
	if wsBeta.Status != "IN_PROGRESS" || wsBeta.WorkflowNode != "N0" {
		t.Errorf("Expected repo-beta to be IN_PROGRESS/N0, got %s/%s", wsBeta.Status, wsBeta.WorkflowNode)
	}

	// generic worker without repo_node_path must receive WorkflowNode N14
	if len(updated.Workers) != 1 || updated.Workers[0].WorkflowNode != "N14" {
		t.Errorf("Expected generic worker without repo_node_path to receive WorkflowNode N14, got %+v", updated.Workers)
	}
}
