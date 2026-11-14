package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPR2A_GetSessionIDsByTaskID_And_DoneWithoutWorkers(t *testing.T) {
	srv, db := setupTestServer(t)

	taskID := "task-noworkers-1"

	sActive := &Session{
		ID:            "claude-code:local:worker-active",
		NativeID:      "worker-active",
		Agent:         "claude-code",
		Host:          srv.HostName(),
		State:         StateIdle,
		CurrentTaskID: taskID,
		StartedAt:     time.Now().UTC(),
	}
	if err := db.SaveSession(sActive); err != nil {
		t.Fatalf("failed to save active session: %v", err)
	}

	sDeleted := &Session{
		ID:            "claude-code:local:worker-deleted",
		NativeID:      "worker-deleted",
		Agent:         "claude-code",
		Host:          srv.HostName(),
		State:         StateIdle,
		CurrentTaskID: taskID,
		StartedAt:     time.Now().UTC(),
	}
	if err := db.SaveSession(sDeleted); err != nil {
		t.Fatalf("failed to save deleted session: %v", err)
	}

	if _, err := db.db.Exec("INSERT INTO deleted_sessions(id, deleted_at) VALUES (?, ?)", sDeleted.ID, time.Now().UTC()); err != nil {
		t.Fatalf("failed to insert into deleted_sessions: %v", err)
	}

	ids, err := db.GetSessionIDsByTaskID(taskID)
	if err != nil {
		t.Fatalf("GetSessionIDsByTaskID failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != sActive.ID {
		t.Fatalf("expected only active session %s, got: %v", sActive.ID, ids)
	}

	task := &Task{
		ID:        taskID,
		Title:     "Task Without Explicit Workers",
		Status:    "IN_PROGRESS",
		Workers:   nil,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	updatePayload := map[string]any{
		"id":     taskID,
		"status": "DONE",
	}
	bodyBytes, _ := json.Marshal(updatePayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	srv.handleTasks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from handleTasks, got: %d (%s)", rec.Code, rec.Body.String())
	}

	savedActive, err := db.GetSession(sActive.ID)
	if err != nil || savedActive == nil {
		t.Fatalf("failed to load active session: %v", err)
	}
	if !savedActive.IsDone {
		t.Errorf("expected session bound solely via CurrentTaskID to be marked IsDone=true")
	}
}

func TestPR2A_RemotePeerDoneAuthHeaderForwarding(t *testing.T) {
	srv, db := setupTestServer(t)

	type peerReqInfo struct {
		Auth   string
		Token  string
		Action string
		ID     string
	}
	ch := make(chan peerReqInfo, 1)

	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		ch <- peerReqInfo{
			Auth:   r.Header.Get("Authorization"),
			Token:  r.Header.Get("X-Ackbar-Token"),
			Action: body["action"],
			ID:     body["id"],
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer peerSrv.Close()

	if err := db.SaveHost(&HostRecord{
		Name: "peer-host",
		URL:  peerSrv.URL,
	}); err != nil {
		t.Fatalf("failed to save host: %v", err)
	}

	srv.configuredToken = "test-secret-peer-token"

	remoteSess := &Session{
		ID:            "claude-code:peer-host:worker-remote",
		NativeID:      "worker-remote",
		Agent:         "claude-code",
		Host:          "peer-host",
		State:         StateWorking,
		CurrentTaskID: "task-peer-done-1",
		StartedAt:     time.Now().UTC(),
	}
	if err := db.SaveSession(remoteSess); err != nil {
		t.Fatalf("failed to save remote session: %v", err)
	}

	task := &Task{
		ID:        "task-peer-done-1",
		Title:     "Remote Task Cleanup",
		Status:    "IN_PROGRESS",
		Workers:   []TaskWorker{{SessionID: remoteSess.ID, IsActive: true}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	updatePayload := map[string]any{
		"id":     "task-peer-done-1",
		"status": "DONE",
	}
	bodyBytes, _ := json.Marshal(updatePayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	srv.handleTasks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from handleTasks, got: %d (%s)", rec.Code, rec.Body.String())
	}

	select {
	case info := <-ch:
		if info.Auth != "Bearer test-secret-peer-token" {
			t.Errorf("expected Authorization header 'Bearer test-secret-peer-token', got: %s", info.Auth)
		}
		if info.Token != "test-secret-peer-token" {
			t.Errorf("expected X-Ackbar-Token 'test-secret-peer-token', got: %s", info.Token)
		}
		if info.Action != "done" || info.ID != remoteSess.ID {
			t.Errorf("expected action=done, id=%s, got action=%s, id=%s", remoteSess.ID, info.Action, info.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for peer /v1/sessions/control call")
	}

	savedRemote, _ := db.GetSession(remoteSess.ID)
	if savedRemote == nil || !savedRemote.IsDone || savedRemote.State != StateEnded {
		t.Errorf("expected local copy of remote session to be IsDone=true and StateEnded")
	}
}
