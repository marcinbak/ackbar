package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPR2A_HeadlessAntigravityBootReKeyAndAliases(t *testing.T) {
	srv, db := setupTestServer(t)

	oldUUID := "11111111-2222-3333-4444-555555555555"
	oldID := "antigravity:local:" + oldUUID
	newUUID := "99999999-8888-7777-6666-555555555555"

	sess := &Session{
		ID:        oldID,
		NativeID:  oldUUID,
		Agent:     "antigravity",
		Host:      srv.HostName(),
		State:     StateWorking,
		StartedAt: time.Now().UTC(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	// Associate a task worker
	task := &Task{
		ID:     "task-rekey-1",
		Title:  "Task ReKey",
		Status: "IN_PROGRESS",
		Workers: []TaskWorker{
			{SessionID: oldID, IsActive: true},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Trigger antigravity init re-key
	srv.headless.handleAntigravityInit(sess, newUUID, "initial prompt")

	expectedNewID := "antigravity:" + srv.HostName() + ":" + newUUID
	if sess.ID != expectedNewID {
		t.Errorf("expected session ID %s, got: %s", expectedNewID, sess.ID)
	}
	if sess.OldID != oldID {
		t.Errorf("expected session OldID %s, got: %s", oldID, sess.OldID)
	}

	// Verify ReassignWorkerSessionID updated task worker
	updatedTask, err := db.GetTaskByID("task-rekey-1")
	if err != nil || updatedTask == nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if len(updatedTask.Workers) > 0 && updatedTask.Workers[0].SessionID != expectedNewID {
		t.Errorf("expected worker session ID %s, got: %s", expectedNewID, updatedTask.Workers[0].SessionID)
	}

	// Verify alias resolution for old_id, bare UUID, and local host alias
	resolvedByOldID, err := db.GetSessionByAlias(oldID)
	if err != nil || resolvedByOldID == nil || resolvedByOldID.ID != expectedNewID {
		t.Errorf("expected alias lookup by oldID to resolve %s, got: %v", expectedNewID, resolvedByOldID)
	}

	resolvedByBareUUID, err := db.GetSessionByAlias(oldUUID)
	if err != nil || resolvedByBareUUID == nil || resolvedByBareUUID.ID != expectedNewID {
		t.Errorf("expected alias lookup by bare UUID to resolve %s, got: %v", expectedNewID, resolvedByBareUUID)
	}

	resolvedByLocalAlias := srv.resolveSession("antigravity:local:" + newUUID)
	if resolvedByLocalAlias == nil || resolvedByLocalAlias.ID != expectedNewID {
		t.Errorf("expected resolveSession with local host alias to resolve %s", expectedNewID)
	}

	// Verify handleRespond supports snake_case session_id
	respondPayload := map[string]string{
		"session_id": expectedNewID,
		"action":     "answer",
		"value":      "confirmed",
	}
	bodyBytes, _ := json.Marshal(respondPayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/respond", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	srv.handleRespond(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from handleRespond with snake_case session_id, got: %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestPR2A_RawSessionNamesAndCustomTitleLock(t *testing.T) {
	srv, db := setupTestServer(t)

	// 1. IsRawSessionName tests
	uuid := "c7b949ba-26f5-4e78-be9a-7a4ca64bc9a5"
	if !IsRawSessionName(uuid + "(") {
		t.Errorf("expected uuid( to be raw session name")
	}
	if !IsRawSessionName(uuid + "(...)") {
		t.Errorf("expected uuid(...) to be raw session name")
	}
	if !IsRawSessionName("<" + uuid + ">") {
		t.Errorf("expected <uuid> to be raw session name")
	}
	if !IsRawSessionName("(" + uuid + ")") {
		t.Errorf("expected (uuid) to be raw session name")
	}
	if IsRawSessionName("Implement Wave 1 PR 2A") {
		t.Errorf("valid feature title should not be raw session name")
	}

	// 2. CustomTitle preservation across SaveSession
	sess := &Session{
		ID:          "claude-code:local:cust-title-1",
		NativeID:    "cust-title-1",
		Agent:       "claude-code",
		Host:        srv.HostName(),
		Name:        "Authoritative Title",
		CustomTitle: "Authoritative Title",
		State:       StateIdle,
		StartedAt:   time.Now().UTC(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	// Overwrite attempt without CustomTitle
	tampered := &Session{
		ID:        sess.ID,
		NativeID:  sess.NativeID,
		Agent:     sess.Agent,
		Host:      sess.Host,
		Name:      uuid + "(",
		State:     StateIdle,
		StartedAt: sess.StartedAt,
	}
	if err := db.SaveSession(tampered); err != nil {
		t.Fatalf("failed to resave tampered session: %v", err)
	}

	loaded, err := db.GetSession(sess.ID)
	if err != nil || loaded == nil {
		t.Fatalf("failed to load session: %v", err)
	}
	if loaded.CustomTitle != "Authoritative Title" || loaded.Name != "Authoritative Title" {
		t.Errorf("expected CustomTitle to remain authoritative, got: name=%s, custom_title=%s", loaded.Name, loaded.CustomTitle)
	}

	// 3. Scanner preservation
	mockProv := &paginationMockProvider{
		agentName: "claude-code",
		messages:  nil,
	}
	srv.RegisterProvider(mockProv)

	srv.refreshExistingSessions(context.Background(), &ScanState{})
	afterScan, _ := db.GetSession(sess.ID)
	if afterScan.Name != "Authoritative Title" {
		t.Errorf("scanner overwrote custom title: %s", afterScan.Name)
	}
}

func TestPR2A_AutoMarkBoundWorkerSessionsDone(t *testing.T) {
	srv, db := setupTestServer(t)

	// Worker 1: idle local session
	w1 := &Session{
		ID:            "claude-code:local:worker-idle",
		NativeID:      "worker-idle",
		Agent:         "claude-code",
		Host:          srv.HostName(),
		State:         StateIdle,
		CurrentTaskID: "task-done-1",
		StartedAt:     time.Now().UTC(),
	}
	_ = db.SaveSession(w1)

	// Worker 2: mid-turn working session
	w2 := &Session{
		ID:            "claude-code:local:worker-working",
		NativeID:      "worker-working",
		Agent:         "claude-code",
		Host:          srv.HostName(),
		State:         StateWorking,
		CurrentTaskID: "task-done-1",
		StartedAt:     time.Now().UTC(),
	}
	_ = db.SaveSession(w2)

	// Supervisor session (IsSupervisor: true)
	sup := &Session{
		ID:            "claude-code:local:supervisor-sess",
		NativeID:      "supervisor-sess",
		Agent:         "claude-code",
		Host:          srv.HostName(),
		State:         StateWorking,
		IsSupervisor:  true,
		CurrentTaskID: "task-done-1",
		StartedAt:     time.Now().UTC(),
	}
	_ = db.SaveSession(sup)

	task := &Task{
		ID:     "task-done-1",
		Title:  "Done Auto Cleanup",
		Status: "IN_PROGRESS",
		Workers: []TaskWorker{
			{SessionID: w1.ID, IsActive: true},
			{SessionID: w2.ID, IsActive: true},
			{SessionID: sup.ID, IsActive: true},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	_ = db.CreateTask(task)

	// Update task status to DONE
	updatePayload := map[string]any{
		"id":     "task-done-1",
		"status": "DONE",
	}
	bodyBytes, _ := json.Marshal(updatePayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	srv.handleTasks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from handleTasks, got: %d (%s)", rec.Code, rec.Body.String())
	}

	// 1. Idle worker should be marked done and ended
	savedW1, _ := db.GetSession(w1.ID)
	if !savedW1.IsDone || savedW1.State != StateEnded {
		t.Errorf("expected worker 1 IsDone=true and StateEnded, got: is_done=%v state=%v", savedW1.IsDone, savedW1.State)
	}

	// 2. Mid-turn worker should have IsDone=true without process killing
	savedW2, _ := db.GetSession(w2.ID)
	if !savedW2.IsDone {
		t.Errorf("expected worker 2 IsDone=true, got: %v", savedW2.IsDone)
	}

	// 3. Supervisor session must NEVER be marked done
	savedSup, _ := db.GetSession(sup.ID)
	if savedSup.IsDone {
		t.Errorf("supervisor session must never be marked done!")
	}

	// 4. Workers on task should all be inactive
	savedTask, _ := db.GetTaskByID("task-done-1")
	for i, w := range savedTask.Workers {
		if w.IsActive {
			t.Errorf("expected worker %d to be inactive", i)
		}
	}
}
