package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitTurnDone(h *HeadlessRunner, sessID string, sub chan ChatStreamEvent) chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		for e := range sub {
			if e.Type == "turn_complete" {
				for d := time.Now().Add(3 * time.Second); time.Now().Before(d) && h.IsRunning(sessID); {
					time.Sleep(5 * time.Millisecond)
				}
				return
			}
		}
	}()
	return ch
}

func TestWarmWorker_IdleTTLReaperAndSendTurnRace(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  echo '{"type":"result","result":"ok","is_error":false}'
done
`
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	h := NewHeadlessRunner(db, nil)
	h.SetIdleTTL(80 * time.Millisecond)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{ID: "claude-code:local:reap1", Agent: "claude-code", NativeID: "reap1", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	sub, unsub := h.Subscribe(sess.ID)
	done := waitTurnDone(h, sess.ID, sub)

	if err := h.RunTurn(context.Background(), sess, "turn 1"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for turn 1")
	}
	unsub()

	h.mu.RLock()
	handle := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle == nil {
		t.Fatalf("expected warm worker handle after turn 1")
	}

	deadline := time.Now().Add(3 * time.Second)
	reaped := false
	for time.Now().Before(deadline) {
		if _, alive := probeLiveWorker(&TurnSpoolMeta{TurnID: "dummy", SocketPath: handle.SocketPath}, 0); !alive {
			reaped = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !reaped {
		t.Errorf("worker failed to reap itself after idle TTL expiration")
	}

	sub2, unsub2 := h.Subscribe(sess.ID)
	done2 := waitTurnDone(h, sess.ID, sub2)

	if err := h.RunTurn(context.Background(), sess, "turn 2 after reap"); err != nil {
		t.Fatalf("RunTurn after reap failed: %v", err)
	}
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for turn 2 after reap")
	}
	unsub2()
}

func TestWarmWorker_ConfigFingerprintRotationAndMidTurnDeferral(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  echo '{"type":"result","result":"ok","is_error":false}'
done
`
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{ID: "claude-code:local:rot1", Agent: "claude-code", NativeID: "rot1", Model: "sonnet", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	sub, unsub := h.Subscribe(sess.ID)
	done := waitTurnDone(h, sess.ID, sub)

	if err := h.RunTurn(context.Background(), sess, "prompt 1"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for turn 1")
	}
	unsub1 := unsub
	unsub1()

	h.mu.RLock()
	handle1 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle1 == nil {
		t.Fatalf("expected handle1")
	}

	sess.Model = "opus"
	h.NotifyConfigChanged(sess)

	h.mu.RLock()
	handleAfterNotify := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handleAfterNotify != nil {
		t.Errorf("idle worker should be shut down immediately on config change, got %+v", handleAfterNotify)
	}

	sub2, unsub2 := h.Subscribe(sess.ID)
	done2 := waitTurnDone(h, sess.ID, sub2)

	if err := h.RunTurn(context.Background(), sess, "prompt 2 with opus"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for turn 2")
	}
	unsub2()

	h.mu.RLock()
	handle2 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle2 == nil || handle2.WorkerPID == handle1.WorkerPID {
		t.Errorf("expected rotated worker for opus, got same PID: %+v vs %+v", handle1, handle2)
	}

	sub3, unsub3 := h.Subscribe(sess.ID)
	done3 := make(chan struct{})
	go func() {
		defer close(done3)
		for e := range sub3 {
			if e.Type == "turn_complete" {
				return
			}
		}
	}()
	if err := h.RunTurn(context.Background(), sess, "turn 3 mid-turn config"); err != nil {
		t.Fatalf("RunTurn 3: %v", err)
	}
	sess.Model = "haiku"
	h.NotifyConfigChanged(sess)

	h.mu.RLock()
	midTurnHandle := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if midTurnHandle == nil || !midTurnHandle.rotateAfterTurn {
		t.Errorf("mid-turn config change should set rotateAfterTurn: %+v", midTurnHandle)
	}

	select {
	case <-done3:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for turn 3")
	}
	unsub3()

	for d := time.Now().Add(2 * time.Second); time.Now().Before(d); time.Sleep(20 * time.Millisecond) {
		h.mu.RLock()
		cur := h.warmWorkers[sess.ID]
		h.mu.RUnlock()
		if cur == nil {
			break
		}
	}
	h.mu.RLock()
	reapedHandle := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if reapedHandle != nil {
		t.Errorf("expected worker to be reaped post-turn, got %+v", reapedHandle)
	}
}

func TestWarmWorker_InteractiveBlockAndApprovePlanTeardown(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  echo '{"type":"content_block_start","content_block":{"type":"tool_use","name":"ExitPlanMode","id":"tool1","input":{}}}'
  echo '{"type":"result","result":"waiting","is_error":false}'
done
`
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{ID: "claude-code:local:block1", Agent: "claude-code", NativeID: "block1", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	sub, unsub := h.Subscribe(sess.ID)
	doneBlock := make(chan struct{})
	go func() {
		defer close(doneBlock)
		for e := range sub {
			if e.Type == "interactive_block" {
				return
			}
		}
	}()

	if err := h.RunTurn(context.Background(), sess, "plan something"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	select {
	case <-doneBlock:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for interactive block")
	}
	unsub()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && h.IsRunning(sess.ID) {
		time.Sleep(20 * time.Millisecond)
	}

	h.ClearInteractiveBlockAndWait(sess.ID, 500*time.Millisecond)
	h.mu.RLock()
	handle := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle != nil {
		t.Errorf("ClearInteractiveBlockAndWait must remove warm worker, got %+v", handle)
	}
}

func TestWarmWorker_TakeWheelDoneCancelAndDaemonRestart(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  echo '{"type":"result","result":"ok","is_error":false}'
done
`
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	srv := NewServer(db)
	srv.RegisterProvider(&MockProvider{})
	defer srv.headless.ShutdownAllWorkers(time.Second)

	sess := &Session{
		ID: "claude-code:local:tw1", Agent: "claude-code", NativeID: "tw1",
		Host: "local", Cwd: tmpDir, EngineType: EngineHeadless, State: StateIdle,
	}
	_ = db.SaveSession(sess)

	if err := srv.headless.RunTurn(context.Background(), sess, "turn 1"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && srv.headless.IsRunning(sess.ID) {
		time.Sleep(20 * time.Millisecond)
	}

	srv.headless.mu.RLock()
	handle := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if handle == nil {
		t.Fatalf("expected warm worker before take-wheel")
	}

	twReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/take-wheel", strings.NewReader(`{"session_id":"claude-code:local:tw1"}`))
	twReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, twReq)

	srv.headless.mu.RLock()
	handleAfterTW := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if handleAfterTW != nil {
		t.Errorf("handleTakeWheel must shut down warm worker, got %+v", handleAfterTW)
	}

	srv.headless.CleanupSessionRuntime(sess.ID, sess.NativeID)
	entries, _ := os.ReadDir(filepath.Join(tmpDir, "run"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".worker.json") || strings.HasSuffix(e.Name(), ".sock") {
			t.Errorf("leaked worker artifact after CleanupSessionRuntime: %s", e.Name())
		}
	}

	sess2 := &Session{ID: "claude-code:local:tw2", Agent: "claude-code", NativeID: "tw2", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess2)
	_ = srv.headless.RunTurn(context.Background(), sess2, "deadlock check")
	doneCleanup := make(chan struct{})
	go func() {
		defer close(doneCleanup)
		srv.headless.CleanupSessionRuntime(sess2.ID, sess2.NativeID)
	}()
	select {
	case <-doneCleanup:
	case <-time.After(2 * time.Second):
		t.Fatalf("deadlock in CleanupSessionRuntime while turn in-flight")
	}
}
