package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agents/antigravity"
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

	agyPlugin := antigravity.New()
	reg := agent.NewRegistry()
	_ = reg.Register(agyPlugin)

	newAgy := &Session{ID: "a1", Agent: "antigravity", NativeID: "native-a1", Model: "gemini-pro"}
	agySpec, err := agyPlugin.BuildTurnSpec(agent.HeadlessTurnInput{Session: newAgy, IsExisting: false})
	if err != nil {
		t.Fatalf("antigravity BuildTurnSpec new failed: %v", err)
	}
	agyArgs := agySpec.Args
	if slices.Contains(agyArgs, "-p") {
		t.Errorf("antigravity persistent args must strictly omit -p: %v", agyArgs)
	}
	if slices.Contains(agyArgs, "--conversation") {
		t.Errorf("new antigravity conv must not have --conversation: %v", agyArgs)
	}

	existAgy := &Session{ID: "a2", Agent: "antigravity", NativeID: "native-a2"}
	existAgySpec, err := agyPlugin.BuildTurnSpec(agent.HeadlessTurnInput{Session: existAgy, IsExisting: true})
	if err != nil {
		t.Fatalf("antigravity BuildTurnSpec existing failed: %v", err)
	}
	existAgyArgs := existAgySpec.Args
	if !slices.Contains(existAgyArgs, "--conversation") {
		t.Errorf("expected --conversation for existing conv: %v", existAgyArgs)
	}

	cTurn, err := formatPersistentStdinTurn("claude-code", "sess1", false, true, "Hello Claude")
	if err != nil || !strings.Contains(cTurn, `"type":"user"`) || !strings.Contains(cTurn, "Hello Claude") {
		t.Errorf("invalid claude stdin turn: %s (err: %v)", cTurn, err)
	}

	aTurn1, err := formatPersistentStdinTurnWithRegistry(reg, agent.PersistentTurnInput{
		Agent: "antigravity", SessionID: "sess1", IsSupervisor: true, IsFirstTurn: true, Prompt: "Hello Agy",
	})
	if err != nil || !strings.Contains(aTurn1, `"event":"user"`) || !strings.Contains(aTurn1, "[System Instruction:") {
		t.Errorf("antigravity supervisor Turn 1 missing system instruction or wrong event key: %s", aTurn1)
	}

	aTurn2, err := formatPersistentStdinTurnWithRegistry(reg, agent.PersistentTurnInput{
		Agent: "antigravity", SessionID: "sess1", IsSupervisor: true, IsFirstTurn: false, Prompt: "Hello Agy 2",
	})
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
	reg := agent.NewRegistry()
	_ = reg.Register(antigravity.New())
	h.SetPluginRegistry(reg)
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
	reg := agent.NewRegistry()
	_ = reg.Register(antigravity.New())
	h.SetPluginRegistry(reg)
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
	reg := agent.NewRegistry()
	_ = reg.Register(antigravity.New())
	h.SetPluginRegistry(reg)
	defer h.ShutdownAllWorkers(time.Second)
	defer h.ReleaseSleepInhibitor()
	h.sleepInhibitor.cmdFactory = func(_ string, _ int) *exec.Cmd { return exec.Command("sleep", "30") }

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

	if err := h.RunTurn(context.Background(), sess, "turn 2"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}
	if !h.IsSleepInhibitorActive() {
		t.Errorf("sleep inhibitor should be active during turn 2 execution")
	}
	deadline2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline2) && h.IsRunning(sess.ID) {
		time.Sleep(20 * time.Millisecond)
	}
	if h.IsSleepInhibitorActive() {
		t.Errorf("sleep inhibitor must be released when turn 2 ends and worker is idle_warm")
	}
}

func TestWarmWorker_RecyclesProcessOnFailedTurnAndSetsErrorActivity(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockAgy := filepath.Join(binDir, "agy")
	countFile := filepath.Join(tmpDir, "turn_count")

	script := fmt.Sprintf(`#!/bin/sh
echo '{"event":"init","conversation_id":"migrated-uuid-err-1"}'
count_file="%s"
turn=1
if [ -f "$count_file" ]; then
  turn=$(cat "$count_file")
fi
while IFS= read -r line; do
  if [ "$turn" -eq 1 ]; then
    echo 2 > "$count_file"
    echo '{"event":"step_update","step_update":{"step_type":"error_message","error":"API error (attempt 5): daily-cloudcode-pa.googleapis.com RST: connection reset by peer"}}'
    echo '{"event":"result","result":{"status":"ERROR","error":"API error (attempt 5): daily-cloudcode-pa.googleapis.com RST: connection reset by peer"}}'
  else
    echo '{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"Recovered on turn 2"}}'
    echo '{"event":"result","result":{"status":"SUCCESS","response":"Recovered on turn 2"}}'
  fi
done
`, countFile)
	if err := os.WriteFile(mockAgy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	h := NewHeadlessRunner(db, nil)
	reg := agent.NewRegistry()
	_ = reg.Register(antigravity.New())
	h.SetPluginRegistry(reg)
	t.Cleanup(func() {
		h.ShutdownAllWorkers(time.Second)
	})

	sess := &Session{ID: "antigravity:local:fail-test-1", Agent: "antigravity", NativeID: "fail-test-1", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	streamCh, cancelSub := h.Subscribe(sess.ID)
	var turn1Events []ChatStreamEvent
	collectDone := make(chan struct{})
	go func() {
		defer close(collectDone)
		for evt := range streamCh {
			turn1Events = append(turn1Events, evt)
		}
	}()

	if err := h.RunTurn(context.Background(), sess, "prompt 1"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.IsRunning(sess.ID) {
		time.Sleep(20 * time.Millisecond)
	}
	cancelSub()
	<-collectDone

	var hasErrorEvt, hasTurnCompleteEvt bool
	for _, evt := range turn1Events {
		if evt.Type == "error" {
			hasErrorEvt = true
		}
		if evt.Type == "turn_complete" {
			hasTurnCompleteEvt = true
		}
	}
	if !hasErrorEvt {
		t.Errorf("expected Turn 1 to emit error event, got events: %+v", turn1Events)
	}
	if hasTurnCompleteEvt {
		t.Errorf("expected Turn 1 to NOT emit turn_complete event, got events: %+v", turn1Events)
	}

	var updatedSess *Session
	waitDbDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(waitDbDeadline) {
		if s, err := db.GetSession(sess.ID); err == nil && s != nil {
			if strings.HasPrefix(s.Activity, "Turn error:") {
				updatedSess = s
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if updatedSess == nil {
		var err error
		updatedSess, err = db.GetSession(sess.ID)
		if err != nil || updatedSess == nil {
			t.Fatalf("GetSession failed: %v", err)
		}
	}
	if !strings.HasPrefix(updatedSess.Activity, "Turn error: API error (attempt 5):") {
		t.Errorf("expected activity to start with 'Turn error: API error (attempt 5):', got %q", updatedSess.Activity)
	}
	if !h.IsQueuePaused(sess.ID) {
		t.Errorf("expected queue to be paused after failed turn")
	}

	h.mu.RLock()
	handle1 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle1 != nil {
		t.Errorf("expected warmWorkers handle to be evicted after failed turn, got %+v", handle1)
	}

	runDir := filepath.Join(tmpDir, "run")
	entries, _ := os.ReadDir(runDir)
	var turn1WorkerPID int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".meta.json") {
			if m, err := ReadTurnSpoolMeta(filepath.Join(runDir, e.Name())); err == nil && m != nil && m.WorkerPID > 0 {
				turn1WorkerPID = m.WorkerPID
			}
		}
	}
	if turn1WorkerPID > 0 {
		exitDeadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(exitDeadline) {
			if err := syscall.Kill(turn1WorkerPID, 0); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err := syscall.Kill(turn1WorkerPID, 0); err == nil {
			t.Errorf("expected worker PID %d to have exited after failure", turn1WorkerPID)
		}
	}

	h.SetQueuePaused(sess.ID, false)
	if err := h.RunTurn(context.Background(), updatedSess, "prompt 2"); err != nil {
		t.Fatalf("RunTurn 2 failed: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.IsRunning(sess.ID) {
		time.Sleep(20 * time.Millisecond)
	}

	h.mu.RLock()
	handle2 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle2 == nil {
		t.Fatalf("expected warm worker after successful turn 2")
	}
	if turn1WorkerPID > 0 && handle2.WorkerPID == turn1WorkerPID {
		t.Errorf("expected new worker process on turn 2, got same PID %d", handle2.WorkerPID)
	}
}
