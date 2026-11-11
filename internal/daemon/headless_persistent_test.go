package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPersistentArgsAndStdin_ClaudeAndAntigravity(t *testing.T) {
	newClaude := &Session{ID: "c1", Agent: "claude-code", NativeID: "native-c1", Model: "sonnet", Effort: "high"}
	bin, args, err := buildPersistentArgs(newClaude, false)
	if err != nil {
		t.Fatalf("buildPersistentArgs claude new failed: %v", err)
	}
	if !strings.Contains(bin, "claude") {
		t.Errorf("expected claude binary, got %s", bin)
	}
	cmdStr := strings.Join(args, " ")
	if !strings.Contains(cmdStr, "--input-format stream-json") || !strings.Contains(cmdStr, "--output-format stream-json") {
		t.Errorf("missing stream-json flags: %s", cmdStr)
	}
	if !slices.Contains(args, "-p") {
		t.Errorf("claude persistent mode must have -p flag")
	}
	if !slices.Contains(args, "--session-id") || slices.Contains(args, "--resume") {
		t.Errorf("expected --session-id on new conv: %s", cmdStr)
	}

	existClaude := &Session{ID: "c2", Agent: "claude-code", NativeID: "native-c2"}
	_, argsExist, _ := buildPersistentArgs(existClaude, true)
	if !slices.Contains(argsExist, "--resume") {
		t.Errorf("expected --resume on existing conv: %v", argsExist)
	}

	newAgy := &Session{ID: "a1", Agent: "antigravity", NativeID: "native-a1", Model: "gemini-pro"}
	_, agyArgs, err := buildPersistentArgs(newAgy, false)
	if err != nil {
		t.Fatalf("buildPersistentArgs agy failed: %v", err)
	}
	if slices.Contains(agyArgs, "-p") {
		t.Errorf("antigravity persistent args must strictly omit -p: %v", agyArgs)
	}
	if slices.Contains(agyArgs, "--conversation") {
		t.Errorf("new antigravity conv must not have --conversation: %v", agyArgs)
	}

	existAgy := &Session{ID: "a2", Agent: "antigravity", NativeID: "native-a2"}
	_, existAgyArgs, _ := buildPersistentArgs(existAgy, true)
	if !slices.Contains(existAgyArgs, "--conversation") {
		t.Errorf("expected --conversation for existing conv: %v", existAgyArgs)
	}

	cTurn, err := formatPersistentStdinTurn("claude-code", "sess1", false, true, "Hello Claude")
	if err != nil || !strings.Contains(cTurn, `"type":"user"`) || !strings.Contains(cTurn, "Hello Claude") {
		t.Errorf("invalid claude stdin turn: %s (err: %v)", cTurn, err)
	}

	aTurn1, err := formatPersistentStdinTurn("antigravity", "sess1", true, true, "Hello Agy")
	if err != nil || !strings.Contains(aTurn1, `"event":"user"`) || !strings.Contains(aTurn1, "[System Instruction:") {
		t.Errorf("antigravity supervisor Turn 1 missing system instruction or wrong event key: %s", aTurn1)
	}

	aTurn2, err := formatPersistentStdinTurn("antigravity", "sess1", true, false, "Hello Agy 2")
	if err != nil || strings.Contains(aTurn2, "[System Instruction:") {
		t.Errorf("antigravity supervisor Turn 2 must not have system instruction prepended: %s", aTurn2)
	}
}

func TestWarmWorker_ClaudeMultiTurnSamePIDAndRepeatedText(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  echo '{"type":"content_block_delta","delta":{"type":"text_delta","text":"Repeated block"}}'
  echo '{"type":"result","result":"ok","is_error":false}'
done
`
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{ID: "claude-code:local:m1", Agent: "claude-code", NativeID: "m1", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	sub1, unsub1 := h.Subscribe(sess.ID)
	doneSub1 := make(chan struct{})
	go func() {
		defer close(doneSub1)
		for e := range sub1 {
			if e.Type == "turn_complete" {
				return
			}
		}
	}()

	if err := h.RunTurn(context.Background(), sess, "prompt 1"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}

	select {
	case <-doneSub1:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for turn 1 complete")
	}
	unsub1()

	waitDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(waitDeadline) && h.IsRunning(sess.ID) {
		time.Sleep(10 * time.Millisecond)
	}

	h.mu.RLock()
	handle1 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle1 == nil || handle1.WorkerPID <= 1 {
		t.Fatalf("expected warm worker handle after turn 1, got %+v", handle1)
	}

	var evtsTurn2 []ChatStreamEvent
	sub2, unsub2 := h.Subscribe(sess.ID)
	doneSub2 := make(chan struct{})
	go func() {
		defer close(doneSub2)
		for e := range sub2 {
			evtsTurn2 = append(evtsTurn2, e)
			if e.Type == "turn_complete" {
				return
			}
		}
	}()

	if err := h.RunTurn(context.Background(), sess, "prompt 2"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}
	if !h.IsRunning(sess.ID) {
		t.Errorf("expected IsRunning true during turn 2")
	}
	if _, ok := h.GetActiveTurnPrompt(sess.ID); !ok {
		t.Errorf("expected GetActiveTurnPrompt true during turn 2")
	}

	select {
	case <-doneSub2:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for turn 2 complete")
	}
	unsub2()

	waitDeadline2 := time.Now().Add(3 * time.Second)
	for time.Now().Before(waitDeadline2) && h.IsRunning(sess.ID) {
		time.Sleep(10 * time.Millisecond)
	}

	h.mu.RLock()
	handle2 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle2 == nil || handle2.WorkerPID != handle1.WorkerPID {
		t.Fatalf("worker PID changed on turn 2: expected %d, got %+v", handle1.WorkerPID, handle2)
	}

	foundRepeated := false
	for _, e := range evtsTurn2 {
		if strings.Contains(e.Text, "Repeated block") {
			foundRepeated = true
			break
		}
	}
	if !foundRepeated {
		t.Errorf("Turn 2 dropped repeated text; evts: %+v", evtsTurn2)
	}

	runDir, _ := h.resolveRunDir(false)
	var turnMetas []*TurnSpoolMeta
	checkDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(checkDeadline) {
		turnMetas = nil
		entries, _ := os.ReadDir(runDir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "turn_") && strings.HasSuffix(e.Name(), ".meta.json") {
				if m, err := ReadTurnSpoolMeta(filepath.Join(runDir, e.Name())); err == nil && m != nil {
					turnMetas = append(turnMetas, m)
				}
			}
		}
		if len(turnMetas) >= 2 {
			allDone := true
			for _, m := range turnMetas {
				if m.Status != "completed" || !m.FinalizedByDaemon {
					allDone = false
					break
				}
			}
			if allDone {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(turnMetas) < 2 {
		t.Fatalf("expected at least 2 turn metas, got %d", len(turnMetas))
	}
	for _, m := range turnMetas {
		if m.Status != "completed" || !m.FinalizedByDaemon {
			t.Errorf("expected Status == completed and FinalizedByDaemon == true for %s, got Status=%q FinalizedByDaemon=%v", m.TurnID, m.Status, m.FinalizedByDaemon)
		}
	}
}

func TestWarmWorker_AntigravityBootInitRaceAndMigration(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockAgy := filepath.Join(binDir, "agy")
	script := `#!/bin/sh
echo '{"event":"init","conversation_id":"migrated-uuid-777"}'
while IFS= read -r line; do
  echo '{"event":"result","result":{"status":"SUCCESS"}}'
done
`
	if err := os.WriteFile(mockAgy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{ID: "antigravity:local:temp1", Agent: "antigravity", NativeID: "temp1", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	if err := h.RunTurn(context.Background(), sess, "turn 1 prompt"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.IsRunning(sess.ID) {
		time.Sleep(20 * time.Millisecond)
	}

	if sess.NativeID != "migrated-uuid-777" {
		t.Errorf("expected session nativeID migrated to migrated-uuid-777, got %s", sess.NativeID)
	}

	h.mu.RLock()
	handle1 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle1 == nil {
		t.Fatalf("expected warm worker for sess.ID %s", sess.ID)
	}

	if err := h.RunTurn(context.Background(), sess, "turn 2 prompt"); err != nil {
		t.Fatalf("RunTurn 2 failed: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.IsRunning(sess.ID) {
		time.Sleep(20 * time.Millisecond)
	}

	h.mu.RLock()
	handle2 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle2 == nil || handle2.WorkerPID != handle1.WorkerPID {
		t.Errorf("worker PID changed on turn 2 after nativeID migration: %+v vs %+v", handle1, handle2)
	}
}

func TestWarmWorker_SleepInhibitorActiveTurnOnly(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  sleep 0.2
  echo '{"type":"result","result":"ok","is_error":false}'
done
`
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{ID: "claude-code:local:sleep1", Agent: "claude-code", NativeID: "sleep1", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	if err := h.RunTurn(context.Background(), sess, "go"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !h.IsSleepInhibitorActive() {
		t.Errorf("sleep inhibitor should be active during turn execution")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.IsRunning(sess.ID) {
		time.Sleep(20 * time.Millisecond)
	}
	if h.IsSleepInhibitorActive() {
		t.Errorf("sleep inhibitor must be released when turn ends and worker is idle_warm")
	}
}
