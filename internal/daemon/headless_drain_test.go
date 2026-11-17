package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeadlessRunner_RequestRestartAfterDrain(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
sleep 0.2
echo '{"type":"result","result":"done","is_error":false}'
`
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "drain_test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	runner := NewHeadlessRunner(db, nil)

	immediateCalled := false
	deferred, activeCount := runner.RequestRestartAfterDrain(func() {
		immediateCalled = true
	})
	if deferred {
		t.Errorf("Expected deferred=false when no turns active, got true")
	}
	if activeCount != 0 {
		t.Errorf("Expected activeCount=0, got %d", activeCount)
	}
	if !immediateCalled {
		t.Errorf("Expected immediate restartFn invocation when ActiveTurnCount is 0")
	}

	sess := &Session{
		ID:         "claude-code:local:uuid-drain-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "uuid-drain-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		State:      StateIdle,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	if err := runner.RunTurn(context.Background(), sess, "Start turn"); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if count := runner.ActiveTurnCount(); count != 1 {
		t.Fatalf("Expected ActiveTurnCount=1, got %d", count)
	}

	drainedCalled := make(chan bool, 1)
	deferred, activeCount = runner.RequestRestartAfterDrain(func() {
		drainedCalled <- true
	})
	if !deferred {
		t.Errorf("Expected deferred=true while turn is active")
	}
	if activeCount != 1 {
		t.Errorf("Expected activeCount=1, got %d", activeCount)
	}
	if !runner.IsRestartPending() {
		t.Errorf("Expected IsRestartPending=true")
	}

	select {
	case <-drainedCalled:
	case <-time.After(2 * time.Second):
		t.Fatalf("Timed out waiting for drain callback to be called")
	}

	if runner.IsRestartPending() {
		t.Errorf("Expected IsRestartPending=false after drain completed")
	}
	if count := runner.ActiveTurnCount(); count != 0 {
		t.Errorf("Expected ActiveTurnCount=0 after turn finished, got %d", count)
	}
}

func TestHeadlessRunner_ImmediatePromptQueuePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := InitDB(filepath.Join(tmpDir, "queue_persist.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	runner := NewHeadlessRunner(db, nil)
	sessID := "claude-code:local:uuid-persist-queue"

	item1 := runner.EnqueuePrompt(sessID, "Immediate prompt 1")
	item2 := runner.EnqueuePrompt(sessID, "Immediate prompt 2")

	if item1 == nil || item2 == nil {
		t.Fatalf("EnqueuePrompt returned nil")
	}

	persisted, err := db.ListScheduledPrompts()
	if err != nil {
		t.Fatalf("ListScheduledPrompts failed: %v", err)
	}
	if len(persisted) != 2 {
		t.Fatalf("Expected 2 prompts persisted in DB, got %d", len(persisted))
	}

	runner2 := NewHeadlessRunner(db, nil)
	runner2.LoadScheduledPrompts()

	items, paused := runner2.GetPromptQueue(sessID)
	if paused {
		t.Errorf("Expected queue not paused")
	}
	if len(items) != 2 {
		t.Fatalf("Expected 2 items in loaded queue, got %d", len(items))
	}
	if items[0].Text != "Immediate prompt 1" || items[1].Text != "Immediate prompt 2" {
		t.Errorf("Queue items do not match expected texts: %v, %v", items[0].Text, items[1].Text)
	}

	popped := runner2.DequeuePrompt(sessID)
	if popped == nil || popped.Text != "Immediate prompt 1" {
		t.Fatalf("Unexpected popped item: %+v", popped)
	}

	persistedAfterPop, err := db.ListScheduledPrompts()
	if err != nil {
		t.Fatalf("ListScheduledPrompts failed: %v", err)
	}
	if len(persistedAfterPop) != 1 {
		t.Fatalf("Expected 1 prompt in DB after dequeue, got %d", len(persistedAfterPop))
	}
	if persistedAfterPop[0].Text != "Immediate prompt 2" {
		t.Errorf("Expected remaining prompt in DB to be prompt 2, got %q", persistedAfterPop[0].Text)
	}
}

func TestDB_InitDB_InterruptedHeadlessSessions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "interrupted.db")

	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 1 failed: %v", err)
	}

	headlessSess := &Session{
		ID:         "claude-code:local:interrupted-headless-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "interrupted-headless-1",
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateWorking,
		PID:        4242,
		Activity:   "Running turn...",
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db1.SaveSession(headlessSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	tmuxSess := &Session{
		ID:         "claude-code:local:interrupted-tmux-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "interrupted-tmux-1",
		EngineType: EngineTmux,
		TmuxName:   "ackbar-claude-interrupted-tmux-1",
		Managed:    true,
		State:      StateWorking,
		PID:        4343,
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db1.SaveSession(tmuxSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	if err := db1.Close(); err != nil {
		t.Fatalf("db1.Close failed: %v", err)
	}

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 2 failed: %v", err)
	}
	defer db2.Close()

	interrupted := db2.PopInterruptedHeadlessSessionIDs()
	if len(interrupted) != 1 || interrupted[0] != headlessSess.ID {
		t.Fatalf("Expected interrupted headless IDs [%q], got %v", headlessSess.ID, interrupted)
	}

	if secondPop := db2.PopInterruptedHeadlessSessionIDs(); len(secondPop) != 0 {
		t.Fatalf("Expected empty list on second Pop, got %v", secondPop)
	}

	loadedHeadless, err := db2.GetSession(headlessSess.ID)
	if err != nil || loadedHeadless == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if loadedHeadless.State != StateIdle {
		t.Errorf("Expected headless session state to be StateIdle, got %v", loadedHeadless.State)
	}
	if loadedHeadless.PID != 0 {
		t.Errorf("Expected headless session PID to be 0, got %d", loadedHeadless.PID)
	}
	if loadedHeadless.Activity != "" {
		t.Errorf("Expected headless session activity to be empty, got %q", loadedHeadless.Activity)
	}
}

func TestServer_HandleShutdown_Drain(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
sleep 0.3
echo '{"type":"result","result":"done","is_error":false}'
`
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	server, db := setupTestServer(t)
	restartTriggered := make(chan bool, 1)
	server.restartDaemonFn = func() {
		restartTriggered <- true
	}

	sess := &Session{
		ID:         "claude-code:local:uuid-server-drain-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "uuid-server-drain-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		State:      StateIdle,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	if err := server.headless.RunTurn(context.Background(), sess, "Start turn"); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/shutdown?drain=true", nil)
	rr := httptest.NewRecorder()
	server.Mux().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var res struct {
		Status              string `json:"status"`
		RestartDeferred     bool   `json:"restart_deferred"`
		ActiveHeadlessTurns int    `json:"active_headless_turns"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if res.Status != "draining" {
		t.Errorf("Expected status='draining', got %q", res.Status)
	}
	if !res.RestartDeferred {
		t.Errorf("Expected restart_deferred=true, got false")
	}
	if res.ActiveHeadlessTurns != 1 {
		t.Errorf("Expected active_headless_turns=1, got %d", res.ActiveHeadlessTurns)
	}

	select {
	case <-restartTriggered:
	case <-time.After(2 * time.Second):
		t.Fatalf("Timed out waiting for deferred restart callback to be called")
	}
}

func TestServer_ResumeInterruptedHeadlessSessions(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
sleep 0.1
echo '{"type":"result","result":"resumed","is_error":false}'
`
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	dbPath := filepath.Join(tmpDir, "resume_interrupted.db")
	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 1 failed: %v", err)
	}

	sess := &Session{
		ID:         "claude-code:local:uuid-resume-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "uuid-resume-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateWorking,
		PID:        1234,
		LastPrompt: "Previous prompt",
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db1.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}
	_ = db1.Close()

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 2 failed: %v", err)
	}
	defer db2.Close()

	server := NewServer(db2)
	server.headless.EnqueuePrompt(sess.ID, "Queued next step")

	server.resumeInterruptedHeadlessSessions(context.Background())

	time.Sleep(500 * time.Millisecond)

	loaded, err := db2.GetSession(sess.ID)
	if err != nil || loaded == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if loaded.LastPrompt != "Queued next step" {
		t.Errorf("Expected resumed turn to use queued prompt %q, got %q", "Queued next step", loaded.LastPrompt)
	}
}

func TestServer_ResumeIdleSessionQueuedPromptAfterDrain(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
sleep 0.1
echo '{"type":"result","result":"done","is_error":false}'
`
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	dbPath := filepath.Join(tmpDir, "drain_queue.db")
	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	server1 := NewServer(db1)
	server1.restartDaemonFn = func() {}
	server1.shutdownExitFn = func(int) {}

	sess := &Session{
		ID:         "claude-code:local:uuid-drain-idle-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "uuid-drain-idle-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db1.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	if err := server1.headless.RunTurn(context.Background(), sess, "Turn 1"); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	server1.headless.EnqueuePrompt(sess.ID, "Turn 2 immediate queued")

	drainFinished := make(chan bool, 1)
	deferred, activeCount := server1.headless.RequestRestartAfterDrain(func() {
		drainFinished <- true
	})
	if !deferred || activeCount != 1 {
		t.Fatalf("Expected deferred=true, activeCount=1, got deferred=%v, activeCount=%d", deferred, activeCount)
	}

	select {
	case <-drainFinished:
	case <-time.After(2 * time.Second):
		t.Fatalf("Timed out waiting for turn 1 to drain")
	}

	sessAfterDrain, err := db1.GetSession(sess.ID)
	if err != nil || sessAfterDrain == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if sessAfterDrain.State != StateIdle {
		t.Errorf("Expected sessAfterDrain.State=StateIdle, got %v", sessAfterDrain.State)
	}

	_ = db1.Close()

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 2 failed: %v", err)
	}
	defer db2.Close()

	server2 := NewServer(db2)
	server2.restartDaemonFn = func() {}
	server2.shutdownExitFn = func(int) {}
	server2.headless.LoadScheduledPrompts()

	q, _ := server2.headless.GetPromptQueue(sess.ID)
	if len(q) != 1 || q[0].Text != "Turn 2 immediate queued" {
		t.Fatalf("Expected queued prompt preserved in DB, got %+v", q)
	}

	server2.resumeInterruptedHeadlessSessions(context.Background())

	time.Sleep(500 * time.Millisecond)

	sessAfterReboot, err := db2.GetSession(sess.ID)
	if err != nil || sessAfterReboot == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if sessAfterReboot.LastPrompt != "Turn 2 immediate queued" {
		t.Errorf("Expected resumed session LastPrompt to be %q, got %q", "Turn 2 immediate queued", sessAfterReboot.LastPrompt)
	}
}

func TestTransactionalPromptQueue_PreservesOnFailureAndCommitsOnRetry(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	_ = os.WriteFile(mockClaude, []byte("#!/bin/sh\necho '{\"type\":\"result\",\"result\":\"ok\",\"is_error\":false}'\n"), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "tx_queue.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	defer srv.headless.ShutdownAllWorkers(time.Second)

	// 1. Unsupported agent forces RunTurn failure during DispatchNextQueuedPrompt
	sess := &Session{
		ID:         "unsupported:local:tx-1",
		Agent:      "unsupported-agent",
		Host:       "local",
		NativeID:   "tx-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		State:      StateIdle,
	}
	_ = db.SaveSession(sess)
	item := srv.headless.EnqueuePrompt(sess.ID, "Retryable prompt")

	dispatched, err := srv.headless.DispatchNextQueuedPrompt(context.Background(), sess)
	if dispatched || err == nil {
		t.Fatalf("expected DispatchNextQueuedPrompt to fail on unsupported agent, got dispatched=%v err=%v", dispatched, err)
	}

	// Must remain in memory AND in SQLite scheduled_prompts
	q, _ := srv.headless.GetPromptQueue(sess.ID)
	if len(q) != 1 || q[0].ID != item.ID {
		t.Fatalf("expected failed prompt to remain in memory queue, got %+v", q)
	}
	persisted, _ := db.ListScheduledPrompts()
	if len(persisted) != 1 || persisted[0].ID != item.ID {
		t.Fatalf("expected failed prompt to remain in SQLite scheduled_prompts, got %+v", persisted)
	}

	// 2. Retry after fixing agent succeeds and removes from both memory and SQLite
	sess.Agent = "claude-code"
	dispatched, err = srv.headless.DispatchNextQueuedPrompt(context.Background(), sess)
	if !dispatched || err != nil {
		t.Fatalf("expected retry DispatchNextQueuedPrompt to succeed, got dispatched=%v err=%v", dispatched, err)
	}
	waitForWorkerCond(t, 4*time.Second, "retried turn finished", func() bool { return !srv.headless.IsRunning(sess.ID) })

	qAfter, _ := srv.headless.GetPromptQueue(sess.ID)
	if len(qAfter) != 0 {
		t.Fatalf("expected queue empty after commit, got %+v", qAfter)
	}
	persistedAfter, _ := db.ListScheduledPrompts()
	if len(persistedAfter) != 0 {
		t.Fatalf("expected SQLite scheduled_prompts empty after commit, got %+v", persistedAfter)
	}

	// 3. processDueScheduledPrompts on missing tmux session aborts claim cleanly without losing prompt
	tmuxSess := &Session{
		ID:         "claude-code:local:tx-tmux",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "tx-tmux",
		Cwd:        tmpDir,
		EngineType: EngineTmux,
		TmuxName:   "nonexistent-ackbar-tmux-session-tx",
		State:      StateIdle,
	}
	_ = db.SaveSession(tmuxSess)
	past := time.Now().Add(-1 * time.Minute)
	tmuxItem := srv.headless.EnqueueDelayedPrompt(tmuxSess.ID, "Tmux retryable", &past)
	srv.processDueScheduledPrompts(context.Background())
	qTmux, _ := srv.headless.GetPromptQueue(tmuxSess.ID)
	if len(qTmux) != 1 || qTmux[0].ID != tmuxItem.ID {
		t.Fatalf("expected tmux prompt retained after SendInput failure, got %+v", qTmux)
	}
	// Verify item is unlocked (AbortPromptDispatch ran) so a subsequent ClaimReadyPrompt succeeds
	if reclaimed := srv.headless.ClaimReadyPrompt(tmuxSess.ID); reclaimed == nil || reclaimed.ID != tmuxItem.ID {
		t.Fatalf("expected aborted tmux claim to be reclaimable, got %+v", reclaimed)
	}
	srv.headless.AbortPromptDispatch(tmuxItem.ID)

	// 4. resumeInterruptedHeadlessSessions skips sessions whose queue is paused
	dbPathPaused := filepath.Join(tmpDir, "paused_resume.db")
	dbP1, _ := InitDB(dbPathPaused)
	pausedSess := &Session{
		ID:         "claude-code:local:paused-resume",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "paused-resume",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		State:      StateWorking,
		LastPrompt: "Do not auto-continue",
	}
	_ = dbP1.SaveSession(pausedSess)
	_ = dbP1.Close()

	dbP2, _ := InitDB(dbPathPaused)
	defer dbP2.Close()
	srvP := NewServer(dbP2)
	defer srvP.headless.ShutdownAllWorkers(time.Second)
	srvP.headless.SetQueuePaused(pausedSess.ID, true)
	srvP.headless.EnqueuePrompt(pausedSess.ID, "Wait for user")
	srvP.resumeInterruptedHeadlessSessions(context.Background())

	qPaused, _ := srvP.headless.GetPromptQueue(pausedSess.ID)
	if len(qPaused) != 1 || !strings.Contains(qPaused[0].Text, "Wait for user") {
		t.Fatalf("expected paused session queued prompt untouched by resumeInterruptedHeadlessSessions, got %+v", qPaused)
	}
	if srvP.headless.IsRunning(pausedSess.ID) {
		t.Fatalf("expected paused session NOT to start turn on resumeInterruptedHeadlessSessions")
	}
}

func TestTransactionalPromptQueue_CommitAfterSessionMigration(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := InitDB(filepath.Join(tmpDir, "tx_migrate.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	h := NewHeadlessRunner(db, nil)
	oldID := "antigravity:local:temp-uuid-1"
	newID := "antigravity:local:real-conv-99"

	item := h.EnqueuePrompt(oldID, "Migrated prompt")
	sub, unsub := h.Subscribe(oldID)
	defer unsub()

	claimed := h.ClaimReadyPrompt(oldID)
	if claimed == nil || claimed.ID != item.ID {
		t.Fatalf("expected ClaimReadyPrompt(%s) to return %s, got %+v", oldID, item.ID, claimed)
	}

	h.MigrateSession(oldID, newID)

	// Commit with the pre-migration oldID must still clear h.queues[newID], SQLite, and emit queue_update on newID
	h.CommitPromptQueueItem(oldID, claimed.ID)

	qNew, _ := h.GetPromptQueue(newID)
	if len(qNew) != 0 {
		t.Fatalf("expected migrated queue h.queues[%s] empty after commit, got %+v", newID, qNew)
	}
	qOld, _ := h.GetPromptQueue(oldID)
	if len(qOld) != 0 {
		t.Fatalf("expected old queue empty after commit, got %+v", qOld)
	}
	persisted, _ := db.ListScheduledPrompts()
	if len(persisted) != 0 {
		t.Fatalf("expected SQLite scheduled_prompts empty after commit, got %+v", persisted)
	}
	if h.ClaimReadyPrompt(newID) != nil {
		t.Fatalf("expected no remaining item to re-claim on %s", newID)
	}

	select {
	case evt := <-sub:
		if evt.Type != "queue_update" || evt.SessionID != newID || len(evt.QueueItems) != 0 {
			t.Fatalf("expected queue_update for migrated session %s with 0 items, got %+v", newID, evt)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for migrated queue_update event")
	}
}
