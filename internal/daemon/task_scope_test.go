package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskScope_MatchNode_EdgeCases(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_match_edge.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{db: db}

	dueNode := &TreeNode{
		Path:       "Modemobile/Due-Dilligence",
		ProjectDir: "/Users/dev4u/Work/Modemobile/DueDilligence",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := db.SaveNode(dueNode); err != nil {
		t.Fatalf("SaveNode failed: %v", err)
	}

	for _, badQuery := range []string{"/", ".git", "/.git/", "igence"} {
		g, p, ok := server.matchNode("", badQuery)
		if ok {
			t.Errorf("matchNode(\"\", %q) unexpectedly matched (%q, %q)", badQuery, g, p)
		}
	}

	for _, badQuery := range []string{"/", ".git", "/.git/", "igence"} {
		g, p, ok := server.matchNode("Modemobile", badQuery)
		if ok {
			t.Errorf("matchNode(\"Modemobile\", %q) unexpectedly matched (%q, %q)", badQuery, g, p)
		}
	}
}

func TestTaskScope_LiveDBReproduction_CreateTask(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_live_db.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{db: db}

	nodes := []*TreeNode{
		{Path: "Modemobile", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{Path: "Modemobile/AppLock", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{
			Path:       "Modemobile/Due-Dilligence",
			ProjectDir: "/Users/dev4u/Work/Modemobile/DueDilligence",
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		},
		{Path: "Modemobile/NGL", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{Path: "Modemobile/NGL/ngl-android", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
	}
	for _, n := range nodes {
		if err := db.SaveNode(n); err != nil {
			t.Fatalf("SaveNode %q failed: %v", n.Path, err)
		}
	}

	if err := db.SaveNodeHostPath(&NodeHostPath{
		NodePath:   "Modemobile/Due-Dilligence",
		Host:       "macbook",
		ProjectDir: "/Users/dev4u/Work/Modemobile/DueDilligence",
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveNodeHostPath failed: %v", err)
	}

	boundSess := &Session{
		ID:       "supervisor-session-live",
		NodePath: "Modemobile/Due-Dilligence",
		Cwd:      "/Users/dev4u/Work/Modemobile/DueDilligence",
	}
	if err := db.SaveSession(boundSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	body := []byte(`{
		"group_name": "Modemobile",
		"project_name": "General",
		"task": {
			"title": "Fix rapid fling crash in feed",
			"group_name": "Modemobile",
			"project_name": "General",
			"workstreams": [
				{
					"repo_node_path": "Modemobile/NGL/ngl-android",
					"status": "IN_PROGRESS"
				}
			]
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(body))
	req.Header.Set("X-Ackbar-Session-ID", boundSess.ID)
	w := httptest.NewRecorder()
	server.handleTasks(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /v1/tasks status %d, body: %s", w.Code, w.Body.String())
	}

	var created Task
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("Failed to unmarshal created task: %v", err)
	}

	if created.GroupName != "Modemobile" || created.ProjectName != "NGL/ngl-android" || created.SubprojectName != "" {
		t.Errorf("got (%q, %q, %q), want (Modemobile, NGL/ngl-android, \"\")", created.GroupName, created.ProjectName, created.SubprojectName)
	}
}

func TestTaskScope_ExplicitMove_Task(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_explicit_move.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{db: db}

	nodes := []*TreeNode{
		{
			Path:       "Modemobile/Due-Dilligence",
			ProjectDir: "/Users/dev4u/Work/Modemobile/DueDilligence",
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		},
		{
			Path:      "Modemobile/NGL/ngl-android",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
	}
	for _, n := range nodes {
		_ = db.SaveNode(n)
	}

	taskID := "task_due_dilligence_1"
	initialTime := time.Now().UTC().Add(-10 * time.Minute)
	initialTask := &Task{
		ID:          taskID,
		Title:       "Audit existing licenses",
		GroupName:   "Modemobile",
		ProjectName: "Due-Dilligence",
		Status:      "IN_PROGRESS",
		Substatus:   "active",
		CreatedAt:   initialTime,
		UpdatedAt:   initialTime,
		Workers: []TaskWorker{
			{
				SessionID: "worker_ngl_android",
				Host:      "macbook",
				IsActive:  true,
			},
		},
		Workstreams: []TaskWorkstream{
			{
				TaskID:       taskID,
				RepoNodePath: "Modemobile/NGL/ngl-android",
				Status:       "IN_PROGRESS",
				UpdatedAt:    initialTime,
			},
		},
	}
	if err := db.CreateTask(initialTask); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	movePayload := []byte(`{
		"id": "task_due_dilligence_1",
		"project_node_path": "Modemobile/NGL/ngl-android"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(movePayload))
	w := httptest.NewRecorder()
	server.handleTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /v1/tasks update returned %d: %s", w.Code, w.Body.String())
	}

	updated, err := db.GetTaskByID(taskID)
	if err != nil || updated == nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}

	if updated.GroupName != "Modemobile" || updated.ProjectName != "NGL/ngl-android" || updated.SubprojectName != "" {
		t.Errorf("moved task got (%q, %q, %q), want (Modemobile, NGL/ngl-android, \"\")", updated.GroupName, updated.ProjectName, updated.SubprojectName)
	}

	if len(updated.Workstreams) != 1 || updated.Workstreams[0].RepoNodePath != "Modemobile/NGL/ngl-android" {
		t.Errorf("workstreams corrupted: %+v", updated.Workstreams)
	}

	if !updated.UpdatedAt.After(initialTime) {
		t.Errorf("expected UpdatedAt to advance after move")
	}

	syncState, err := db.GetSyncStateSince(initialTime.Add(time.Second), "macbook")
	if err != nil {
		t.Fatalf("GetSyncStateSince failed: %v", err)
	}
	foundInSync := false
	for _, synct := range syncState.Tasks {
		if synct.ID == taskID {
			foundInSync = true
			break
		}
	}
	if !foundInSync {
		t.Errorf("expected moved task %s in sync payload", taskID)
	}
}

func TestTaskScope_ExplicitMove_ClearsSubproject(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_clear_subproject.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := &Server{db: db}

	_ = db.SaveNode(&TreeNode{
		Path:      "Modemobile/NGL",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	taskID := "task_legacy_sub"
	legacyTask := &Task{
		ID:             taskID,
		Title:          "Legacy Subproject Task",
		GroupName:      "Modemobile",
		ProjectName:    "NGL",
		SubprojectName: "ngl-android",
		Status:         "NEW",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := db.CreateTask(legacyTask); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	movePayload := []byte(`{
		"id": "task_legacy_sub",
		"project_node_path": "Modemobile/NGL"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(movePayload))
	w := httptest.NewRecorder()
	server.handleTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /v1/tasks update returned %d: %s", w.Code, w.Body.String())
	}

	updated, err := db.GetTaskByID(taskID)
	if err != nil || updated == nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}

	if updated.GroupName != "Modemobile" || updated.ProjectName != "NGL" || updated.SubprojectName != "" {
		t.Errorf("got (%q, %q, %q), want (Modemobile, NGL, \"\")", updated.GroupName, updated.ProjectName, updated.SubprojectName)
	}
}
