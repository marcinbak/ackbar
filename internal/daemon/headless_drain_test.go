package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
