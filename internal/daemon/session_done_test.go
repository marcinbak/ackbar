package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func countMetaFilesInDir(runDir string) int {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".meta.json") {
			n++
		}
	}
	return n
}

func TestSpoolRetention_BoundedPerSessionDeleteAndStalePrune(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")

	installMockClaude(t, `
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"turn ok"}}\n'
printf '{"type":"result","result":"turn ok"}\n'
`)

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ReleaseSleepInhibitor()

	sess := &Session{
		ID:         "claude-code:local:retain-1",
		NativeID:   "retain-1",
		Agent:      "claude-code",
		Host:       "local",
		Cwd:        tmpHome,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sess)
	runDir := DefaultHeadlessRunDir(dbPath)

	if err := srv.headless.RunTurn(context.Background(), sess, "First turn"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	waitForWorkerCond(t, 4*time.Second, "turn 1 completed", func() bool {
		return !srv.headless.IsRunning(sess.ID)
	})
	turn1Meta, _ := findFirstMetaInDir(runDir)
	if turn1Meta == nil || countMetaFilesInDir(runDir) != 1 {
		t.Fatalf("expected 1 spool after Turn 1, got %d", countMetaFilesInDir(runDir))
	}

	sess2, _ := db.GetSession(sess.ID)
	if err := srv.headless.RunTurn(context.Background(), sess2, "Second turn"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}
	waitForWorkerCond(t, 4*time.Second, "turn 2 completed", func() bool {
		return !srv.headless.IsRunning(sess.ID)
	})
	if fileExists(turn1Meta.MetaPath) || fileExists(turn1Meta.SpoolPath) {
		t.Fatalf("expected Turn 1 spool files to be pruned when Turn 2 started")
	}
	if countMetaFilesInDir(runDir) != 1 {
		t.Fatalf("expected bounded retention of 1 spool after Turn 2, got %d", countMetaFilesInDir(runDir))
	}

	staleMetaPath := filepath.Join(runDir, "stale-old.meta.json")
	staleSpoolPath := filepath.Join(runDir, "stale-old.ndjson")
	_ = os.WriteFile(staleSpoolPath, []byte{}, 0600)
	_ = WriteTurnSpoolMeta(staleMetaPath, &TurnSpoolMeta{
		TurnID:            "stale-old",
		SessionID:         "claude-code:local:old-sess",
		NativeID:          "old-sess",
		Agent:             "claude-code",
		SpoolPath:         staleSpoolPath,
		MetaPath:          staleMetaPath,
		StartedAt:         time.Now().UTC().Add(-26 * time.Hour),
		FinishedAt:        time.Now().UTC().Add(-25 * time.Hour),
		Status:            "completed",
		FinalizedByDaemon: true,
	})
	srv.headless.PruneStaleSpools(24 * time.Hour)
	if fileExists(staleMetaPath) || fileExists(staleSpoolPath) {
		t.Fatalf("expected >24h stale spool to be removed by PruneStaleSpools")
	}
	if countMetaFilesInDir(runDir) != 1 {
		t.Fatalf("expected recent spool for retain-1 to remain, got %d", countMetaFilesInDir(runDir))
	}

	delBody, _ := json.Marshal(map[string]string{"id": sess.ID, "action": "delete"})
	delRec := httptest.NewRecorder()
	srv.handleSessionControl(delRec, httptest.NewRequest(http.MethodPost, "/v1/sessions/control", bytes.NewReader(delBody)))
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete control failed: %d %s", delRec.Code, delRec.Body.String())
	}
	if countMetaFilesInDir(runDir) != 0 {
		t.Fatalf("expected 0 spools after session delete, got %d", countMetaFilesInDir(runDir))
	}
}

func TestSessionDone_ManualAndAutoDoneCleanupPreservesTranscriptAndSurvivesInitDB(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	releaseFile := filepath.Join(tmpHome, "done-hold.flag")

	installMockClaude(t, fmt.Sprintf(`
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"working..."}}\n'
while [ ! -f %q ]; do sleep 0.02; done
`, releaseFile))

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	srv := NewServer(db)

	sess := &Session{
		ID:         "claude-code:local:done-sess",
		NativeID:   "done-sess",
		Agent:      "claude-code",
		Host:       "local",
		Cwd:        tmpHome,
		EngineType: EngineHeadless,
		TmuxName:   "ackbar_done_sess",
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sess)

	projDir := filepath.Join(tmpHome, ".claude", "projects", "done-proj")
	_ = os.MkdirAll(projDir, 0755)
	jsonlPath := filepath.Join(projDir, sess.NativeID+".jsonl")
	ts := time.Now().UTC().Format(time.RFC3339)
	jsonlContent := fmt.Sprintf(
		"{\"type\":\"user\",\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"Keep my history\"}}\n"+
			"{\"type\":\"assistant\",\"timestamp\":%q,\"message\":{\"role\":\"assistant\",\"content\":\"History preserved.\"}}\n",
		ts, ts,
	)
	_ = os.WriteFile(jsonlPath, []byte(jsonlContent), 0644)

	if err := srv.headless.RunTurn(context.Background(), sess, "Long running turn"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	srv.headless.EnqueuePrompt(sess.ID, "Queued follow-up")

	runDir := DefaultHeadlessRunDir(dbPath)
	var workerPID int
	waitForWorkerCond(t, 4*time.Second, "worker active before Done", func() bool {
		m, _ := findFirstMetaInDir(runDir)
		if m != nil && m.WorkerPID > 0 && m.Status == "running" {
			workerPID = m.WorkerPID
			return true
		}
		return false
	})

	doneBody, _ := json.Marshal(map[string]string{"id": sess.ID, "action": "done"})
	doneRec := httptest.NewRecorder()
	srv.handleSessionControl(doneRec, httptest.NewRequest(http.MethodPost, "/v1/sessions/control", bytes.NewReader(doneBody)))
	if doneRec.Code != http.StatusOK {
		t.Fatalf("action=done failed: %d %s", doneRec.Code, doneRec.Body.String())
	}

	waitForWorkerCond(t, 4*time.Second, "worker killed and spools removed on Done", func() bool {
		return !srv.headless.IsRunning(sess.ID) && !isProcessAlive(workerPID) && countMetaFilesInDir(runDir) == 0
	})

	qItems, _ := srv.headless.GetPromptQueue(sess.ID)
	if len(qItems) != 0 {
		t.Fatalf("expected prompt queue cleared on Done, got %d items", len(qItems))
	}
	afterDone, err := db.GetSession(sess.ID)
	if err != nil || afterDone == nil {
		t.Fatalf("GetSession after Done: %v", err)
	}
	if !afterDone.IsDone || afterDone.State != StateEnded || afterDone.PID != 0 || afterDone.Blocked != nil || afterDone.TmuxName != "" || afterDone.EngineType != EngineHeadless {
		t.Fatalf("unexpected session state after Done: %+v", afterDone)
	}
	if !fileExists(jsonlPath) {
		t.Fatalf("provider .jsonl transcript must remain intact on disk after Done")
	}
	trRec := httptest.NewRecorder()
	srv.handleSessionTranscript(trRec, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil))
	if trRec.Code != http.StatusOK || !strings.Contains(trRec.Body.String(), "History preserved.") {
		t.Fatalf("expected transcript readable after Done, got %d: %s", trRec.Code, trRec.Body.String())
	}

	srv.adoptExistingTmuxSession(context.Background(), nil, afterDone, "claude-code", "ackbar_done-sess", tmpHome, 123, 124)
	if checkAfterAdopt, _ := db.GetSession(sess.ID); checkAfterAdopt.TmuxName != "" {
		t.Fatalf("adoptExistingTmuxSession resurrected TmuxName on Done session: %+v", checkAfterAdopt)
	}

	tmuxDone := &Session{
		ID:         "claude-code:local:tmux-done-1",
		NativeID:   "tmux-done-1",
		Agent:      "claude-code",
		Host:       "local",
		Cwd:        tmpHome,
		EngineType: EngineTmux,
		Managed:    false,
		IsDone:     true,
		State:      StateEnded,
		TmuxName:   "",
	}
	_ = db.SaveSession(tmuxDone)
	srv.headless.ReleaseSleepInhibitor()
	_ = db.Close()

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB(db2): %v", err)
	}
	defer db2.Close()
	srv2 := NewServer(db2)
	defer srv2.headless.ReleaseSleepInhibitor()

	for _, checkID := range []string{sess.ID, tmuxDone.ID} {
		reloaded, err := db2.GetSession(checkID)
		if err != nil || reloaded == nil {
			t.Fatalf("Done session %s was deleted by InitDB!", checkID)
		}
		if !reloaded.IsDone || reloaded.TmuxName != "" {
			t.Fatalf("Done session %s mutated by InitDB: %+v", checkID, reloaded)
		}
	}

	_ = db2.SetSettings(map[string]string{
		"auto_done_enabled": "true",
		"auto_done_hours":   "2",
	})
	oldTs := time.Now().UTC().Add(-3 * time.Hour)

	eligible := &Session{
		ID: "claude-code:local:11111111-2222-3333-4444-555555555555", NativeID: "11111111-2222-3333-4444-555555555555", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateIdle,
		StartedAt: oldTs, LastEventAt: oldTs, UpdatedAt: oldTs, MetadataUpdatedAt: oldTs,
	}
	laterProtected := &Session{
		ID: "claude-code:local:auto-later", NativeID: "auto-later", Agent: "claude-code",
		Host: "local", EngineType: EngineHeadless, State: StateIdle, IsLater: true,
		StartedAt: oldTs, LastEventAt: oldTs, UpdatedAt: oldTs, MetadataUpdatedAt: oldTs,
	}
	queuedProtected := &Session{
		ID: "claude-code:local:auto-queued", NativeID: "auto-queued", Agent: "claude-code",
		Host: "local", EngineType: EngineHeadless, State: StateIdle,
		StartedAt: oldTs, LastEventAt: oldTs, UpdatedAt: oldTs, MetadataUpdatedAt: oldTs,
	}
	_ = db2.SaveSession(eligible)
	_ = db2.SaveSession(laterProtected)
	_ = db2.SaveSession(queuedProtected)
	srv2.headless.EnqueuePrompt(queuedProtected.ID, "Pending work")

	srv2.sweepAutoDoneSessions(context.Background())

	gotEligible, _ := db2.GetSession(eligible.ID)
	gotLater, _ := db2.GetSession(laterProtected.ID)
	gotQueued, _ := db2.GetSession(queuedProtected.ID)
	if gotEligible == nil || !gotEligible.IsDone {
		t.Fatalf("expected inactive idle session marked IsDone by sweepAutoDoneSessions, got %+v", gotEligible)
	}
	if gotLater == nil || gotLater.IsDone {
		t.Fatalf("expected IsLater session skipped by sweepAutoDoneSessions, got %+v", gotLater)
	}
	if gotQueued == nil || gotQueued.IsDone {
		t.Fatalf("expected session with queued prompts skipped by sweepAutoDoneSessions, got %+v", gotQueued)
	}

	_ = os.WriteFile(releaseFile, []byte("go"), 0600)
	resumeBody, _ := json.Marshal(map[string]string{"id": eligible.ID, "action": "resume"})
	resRec := httptest.NewRecorder()
	srv2.handleSessionControl(resRec, httptest.NewRequest(http.MethodPost, "/v1/sessions/control", bytes.NewReader(resumeBody)))
	if resRec.Code != http.StatusOK {
		t.Fatalf("action=resume failed: %d %s", resRec.Code, resRec.Body.String())
	}
	if resumed, _ := db2.GetSession(eligible.ID); resumed == nil || resumed.IsDone {
		t.Fatalf("expected action=resume to clear IsDone=false, got %+v", resumed)
	}
	waitForWorkerCond(t, 4*time.Second, "resumed turn finished cleanly", func() bool {
		return !srv2.headless.IsRunning(eligible.ID)
	})
}

func TestSleepInhibitor_LifecycleAndCrossOSCommands(t *testing.T) {
	darwinCmd := defaultSleepInhibitorCmd("darwin", 4242)
	if darwinCmd == nil || filepath.Base(darwinCmd.Path) != "caffeinate" || strings.Join(darwinCmd.Args, " ") != "caffeinate -i -w 4242" {
		t.Fatalf("unexpected darwin inhibitor cmd: %+v", darwinCmd)
	}

	linuxCmd := defaultSleepInhibitorCmd("linux", 4242)
	if linuxCmd == nil || filepath.Base(linuxCmd.Path) != "systemd-inhibit" || !strings.Contains(strings.Join(linuxCmd.Args, " "), "kill -0 4242") {
		t.Fatalf("unexpected linux inhibitor cmd: %+v", linuxCmd)
	}

	var starts atomic.Int32
	inh := &SleepInhibitor{
		goos:      "linux",
		parentPID: os.Getpid(),
		cmdFactory: func(_ string, _ int) *exec.Cmd {
			starts.Add(1)
			return exec.Command("sh", "-c", "sleep 30")
		},
	}
	defer inh.Release()

	inh.SyncActiveTurns(0)
	if starts.Load() != 0 {
		t.Fatalf("expected 0 inhibitor spawns at 0 active turns, got %d", starts.Load())
	}

	inh.SyncActiveTurns(1)
	if starts.Load() != 1 {
		t.Fatalf("expected 1 inhibitor spawn on 0->1 transition, got %d", starts.Load())
	}
	inh.mu.Lock()
	pid := inh.cmd.Process.Pid
	inh.mu.Unlock()

	inh.SyncActiveTurns(2)
	if starts.Load() != 1 {
		t.Fatalf("expected single shared inhibitor process on 1->2 transition, got %d", starts.Load())
	}

	inh.SyncActiveTurns(0)
	waitForWorkerCond(t, 2*time.Second, "inhibitor process terminated on 2->0", func() bool {
		inh.mu.Lock()
		cleared := inh.cmd == nil
		inh.mu.Unlock()
		return cleared && !isProcessAlive(pid)
	})
}
