package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	if defaultSleepInhibitorCmd("linux", 1) != nil || defaultSleepInhibitorCmd("darwin", 0) != nil {
		t.Fatalf("expected parentPID <= 1 to return nil inhibitor cmd")
	}
	darwinCmd := defaultSleepInhibitorCmd("darwin", 4242)
	if darwinCmd == nil || filepath.Base(darwinCmd.Path) != "caffeinate" || strings.Join(darwinCmd.Args, " ") != "caffeinate -i -w 4242" || darwinCmd.SysProcAttr == nil || !darwinCmd.SysProcAttr.Setpgid {
		t.Fatalf("unexpected darwin inhibitor cmd: %+v", darwinCmd)
	}

	linuxCmd := defaultSleepInhibitorCmd("linux", 4242)
	if linuxCmd == nil || filepath.Base(linuxCmd.Path) != "systemd-inhibit" || !strings.Contains(strings.Join(linuxCmd.Args, " "), "kill -0 4242") || linuxCmd.SysProcAttr == nil || !linuxCmd.SysProcAttr.Setpgid {
		t.Fatalf("unexpected linux inhibitor cmd: %+v", linuxCmd)
	}

	childPIDFile := filepath.Join(t.TempDir(), "subshell.pid")
	var starts atomic.Int32
	inh := &SleepInhibitor{
		goos:      "linux",
		parentPID: os.Getpid(),
		cmdFactory: func(_ string, _ int) *exec.Cmd {
			starts.Add(1)
			return exec.Command("sh", "-c", fmt.Sprintf("sh -c 'echo $$ > %q; exec sleep 30' & wait", childPIDFile))
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

	var subshellPID int
	waitForWorkerCond(t, 2*time.Second, "inhibitor child subshell started", func() bool {
		b, err := os.ReadFile(childPIDFile)
		if err != nil {
			return false
		}
		p, pErr := strconv.Atoi(strings.TrimSpace(string(b)))
		if pErr == nil && p > 1 && isProcessAlive(p) {
			subshellPID = p
			return true
		}
		return false
	})

	inh.SyncActiveTurns(2)
	if starts.Load() != 1 {
		t.Fatalf("expected single shared inhibitor process on 1->2 transition, got %d", starts.Load())
	}

	inh.SyncActiveTurns(0)
	waitForWorkerCond(t, 2*time.Second, "inhibitor process group and child subshell terminated on 2->0", func() bool {
		inh.mu.Lock()
		cleared := inh.cmd == nil
		inh.mu.Unlock()
		return cleared && !isProcessAlive(pid) && !isProcessAlive(subshellPID)
	})
}

func TestHeadlessWorker_RecycledPIDStaleMetaNotTrustedOrKilled(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")

	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 1: %v", err)
	}
	sessReattach := newTestHeadlessSession("claude-code:local:recycled-reattach", "recycled-reattach", tmpHome, StateWorking)
	sessCleanup := newTestHeadlessSession("claude-code:local:recycled-cleanup", "recycled-cleanup", tmpHome, StateIdle)
	_ = db1.SaveSession(sessReattach)
	_ = db1.SaveSession(sessCleanup)
	_ = db1.Close()

	runDir := DefaultHeadlessRunDir(dbPath)
	_ = os.MkdirAll(runDir, 0700)
	selfPID := os.Getpid()

	reattachMetaPath := filepath.Join(runDir, "turn_recycled_reattach.meta.json")
	reattachSpoolPath := filepath.Join(runDir, "turn_recycled_reattach.ndjson")
	_ = os.WriteFile(reattachSpoolPath, []byte(`{"seq":1,"type":"turn_start","text":"stale"}`+"\n"), 0600)
	_ = WriteTurnSpoolMeta(reattachMetaPath, &TurnSpoolMeta{
		TurnID:        "turn_recycled_reattach",
		SessionID:     sessReattach.ID,
		OrigSessionID: sessReattach.ID,
		NativeID:      sessReattach.NativeID,
		Agent:         "claude-code",
		Cwd:           tmpHome,
		Prompt:        "stale",
		SocketPath:    filepath.Join(runDir, "missing_reattach.sock"),
		SpoolPath:     reattachSpoolPath,
		MetaPath:      reattachMetaPath,
		WorkerPID:     selfPID,
		ChildPID:      selfPID,
		StartedAt:     time.Now().Add(-time.Minute).UTC(),
		Status:        "running",
	})

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 2: %v", err)
	}
	defer db2.Close()
	srv2 := NewServer(db2)
	defer srv2.headless.ReleaseSleepInhibitor()
	srv2.headless.ReattachLiveWorkers()

	if srv2.headless.IsRunning(sessReattach.ID) {
		t.Fatalf("expected stale meta with recycled WorkerPID=os.Getpid() NOT to be treated as running")
	}
	if !fileExists(reattachMetaPath) || !fileExists(reattachSpoolPath) {
		t.Fatalf("expected crashed reattach spool files preserved on disk")
	}
	reattachMetaAfter, err := ReadTurnSpoolMeta(reattachMetaPath)
	if err != nil || reattachMetaAfter == nil || reattachMetaAfter.Status != "failed" || reattachMetaAfter.ExitCode != -1 || !reattachMetaAfter.FinalizedByDaemon {
		t.Fatalf("expected reattach meta finalized as failed/-1, got %+v (err=%v)", reattachMetaAfter, err)
	}
	reattachEvts, _, _ := ReadTurnSpoolAfter(reattachSpoolPath, 0)
	hasInterruptErr := false
	for _, ev := range reattachEvts {
		if ev.Type == "error" && ev.IsError && ev.Text == crashedWorkerErrorText {
			hasInterruptErr = true
			break
		}
	}
	if !hasInterruptErr {
		t.Fatalf("expected interrupted error event appended to spool, got %+v", reattachEvts)
	}
	if !srv2.headless.IsQueuePaused(sessReattach.ID) {
		t.Fatalf("expected crashed recycled-PID session queue to be paused")
	}
	if slices.Contains(db2.PopInterruptedHeadlessSessionIDs(), sessReattach.ID) {
		t.Fatalf("expected crashed recycled-PID session cleared from interruptedHeadlessIDs")
	}
	if reattachSessAfter, _ := db2.GetSession(sessReattach.ID); reattachSessAfter == nil || reattachSessAfter.State != StateIdle || reattachSessAfter.PID != 0 || reattachSessAfter.Activity != "Turn error: "+crashedWorkerErrorText {
		t.Fatalf("unexpected crashed recycled-PID session state: %+v", reattachSessAfter)
	}

	cleanupMetaPath := filepath.Join(runDir, "turn_recycled_cleanup.meta.json")
	cleanupSpoolPath := filepath.Join(runDir, "turn_recycled_cleanup.ndjson")
	_ = os.WriteFile(cleanupSpoolPath, []byte(`{"seq":1,"type":"turn_start","text":"stale2"}`+"\n"), 0600)
	_ = WriteTurnSpoolMeta(cleanupMetaPath, &TurnSpoolMeta{
		TurnID:     "turn_recycled_cleanup",
		SessionID:  sessCleanup.ID,
		NativeID:   sessCleanup.NativeID,
		Agent:      "claude-code",
		SocketPath: filepath.Join(runDir, "missing_cleanup.sock"),
		SpoolPath:  cleanupSpoolPath,
		MetaPath:   cleanupMetaPath,
		WorkerPID:  selfPID,
		ChildPID:   selfPID,
		StartedAt:  time.Now().Add(-time.Minute).UTC(),
		Status:     "running",
	})
	srv2.headless.CleanupSessionRuntime(sessCleanup.ID, sessCleanup.NativeID)
	if fileExists(cleanupMetaPath) || fileExists(cleanupSpoolPath) {
		t.Fatalf("expected CleanupSessionRuntime to remove stale spool files without killing recycled os.Getpid()")
	}
}

func TestSessionFilters_DBAndHTTPAndFederated(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ReleaseSleepInhibitor()

	now := time.Now().UTC()
	activeSess := &Session{
		ID: "claude-code:local:filt-active", NativeID: "filt-active", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateIdle,
		StartedAt: now, LastEventAt: now, UpdatedAt: now, MetadataUpdatedAt: now,
	}
	doneSess := &Session{
		ID: "claude-code:local:filt-done", NativeID: "filt-done", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, IsDone: true,
		StartedAt: now, LastEventAt: now.Add(-time.Minute), UpdatedAt: now, MetadataUpdatedAt: now,
	}
	archivedSess := &Session{
		ID: "claude-code:local:filt-archived", NativeID: "filt-archived", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, Archived: true,
		StartedAt: now, LastEventAt: now.Add(-2 * time.Minute), UpdatedAt: now, MetadataUpdatedAt: now,
	}
	for _, s := range []*Session{activeSess, doneSess, archivedSess} {
		if err := db.SaveSession(s); err != nil {
			t.Fatalf("SaveSession(%s): %v", s.ID, err)
		}
	}

	assertIDs := func(label string, list []*Session, want ...string) {
		t.Helper()
		got := make(map[string]bool, len(list))
		for _, s := range list {
			got[s.ID] = true
		}
		if len(got) != len(want) {
			t.Fatalf("%s: expected %v, got %d sessions (%+v)", label, want, len(list), got)
		}
		for _, id := range want {
			if !got[id] {
				t.Fatalf("%s: missing expected session %s in %+v", label, id, got)
			}
		}
	}

	allDB, err := db.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	assertIDs("ListSessions", allDB, activeSess.ID, doneSess.ID, archivedSess.ID)

	noArchDB, err := db.ListSessionsFiltered(false, true)
	if err != nil {
		t.Fatalf("ListSessionsFiltered(false, true): %v", err)
	}
	assertIDs("ListSessionsFiltered(false, true)", noArchDB, activeSess.ID, doneSess.ID)

	noDoneDB, err := db.ListSessionsFiltered(true, false)
	if err != nil {
		t.Fatalf("ListSessionsFiltered(true, false): %v", err)
	}
	assertIDs("ListSessionsFiltered(true, false)", noDoneDB, activeSess.ID, archivedSess.ID)

	onlyActiveDB, err := db.ListSessionsFiltered(false, false)
	if err != nil {
		t.Fatalf("ListSessionsFiltered(false, false): %v", err)
	}
	assertIDs("ListSessionsFiltered(false, false)", onlyActiveDB, activeSess.ID)

	decodeSessions := func(target string) []*Session {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleSessions(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d: %s", target, rec.Code, rec.Body.String())
		}
		var out []*Session
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", target, err)
		}
		return out
	}

	assertIDs("GET /v1/sessions", decodeSessions("/v1/sessions"), activeSess.ID, doneSess.ID, archivedSess.ID)
	assertIDs("GET /v1/sessions?include_archived=false&include_done=false",
		decodeSessions("/v1/sessions?include_archived=false&include_done=false"), activeSess.ID)
	assertIDs("GET /v1/sessions?include_archived=0&include_done=1",
		decodeSessions("/v1/sessions?include_archived=0&include_done=1"), activeSess.ID, doneSess.ID)

	var peerMu sync.Mutex
	var peerQuery string
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		peerMu.Lock()
		peerQuery = r.URL.RawQuery
		peerMu.Unlock()
		_ = json.NewEncoder(w).Encode([]*Session{
			{ID: "claude-code:peer1:p-active", NativeID: "p-active", Agent: "claude-code", Host: "peer1", State: StateIdle, LastEventAt: now},
			{ID: "claude-code:peer1:p-done", NativeID: "p-done", Agent: "claude-code", Host: "peer1", State: StateEnded, IsDone: true, LastEventAt: now},
			{ID: "claude-code:peer1:p-arch", NativeID: "p-arch", Agent: "claude-code", Host: "peer1", State: StateEnded, Archived: true, LastEventAt: now},
		})
	}))
	defer peerSrv.Close()

	if err := db.SaveHost(&HostRecord{Name: "peer1", URL: peerSrv.URL}); err != nil {
		t.Fatalf("SaveHost: %v", err)
	}
	fed := decodeSessions("/v1/sessions?federated=true&include_archived=false&include_done=false")
	peerMu.Lock()
	gotPeerQuery := peerQuery
	peerMu.Unlock()
	if !strings.Contains(gotPeerQuery, "include_archived=false") || !strings.Contains(gotPeerQuery, "include_done=false") {
		t.Fatalf("expected federated peer query to forward filters, got %q", gotPeerQuery)
	}
	assertIDs("federated filtered", fed, activeSess.ID, "claude-code:peer1:p-active")
}

func TestHandleEvents_FiltersSnapshotButStreamsLiveTransitions(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ReleaseSleepInhibitor()

	now := time.Now().UTC()
	activeSess := &Session{
		ID: "claude-code:local:sse-active", NativeID: "sse-active", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateIdle,
		StartedAt: now, LastEventAt: now, UpdatedAt: now, MetadataUpdatedAt: now,
	}
	doneSess := &Session{
		ID: "claude-code:local:sse-done", NativeID: "sse-done", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, IsDone: true,
		StartedAt: now, LastEventAt: now, UpdatedAt: now, MetadataUpdatedAt: now,
	}
	archivedSess := &Session{
		ID: "claude-code:local:sse-archived", NativeID: "sse-archived", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, Archived: true,
		StartedAt: now, LastEventAt: now, UpdatedAt: now, MetadataUpdatedAt: now,
	}
	for _, s := range []*Session{activeSess, doneSess, archivedSess} {
		if err := db.SaveSession(s); err != nil {
			t.Fatalf("SaveSession(%s): %v", s.ID, err)
		}
	}

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/events?include_archived=false&include_done=false", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()

	events := make(chan Session, 8)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var s Session
			if json.Unmarshal([]byte(payload), &s) == nil {
				select {
				case events <- s:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	select {
	case first := <-events:
		if first.ID != activeSess.ID {
			t.Fatalf("expected initial snapshot event %s, got %+v", activeSess.ID, first)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for initial SSE snapshot")
	}

	select {
	case extra := <-events:
		t.Fatalf("expected done/archived sessions omitted from initial snapshot, got %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	doneUpdate := *activeSess
	doneUpdate.IsDone = true
	srv.broadcast(&doneUpdate)

	archivedUpdate := *activeSess
	archivedUpdate.Archived = true
	srv.broadcast(&archivedUpdate)

	var sawDone, sawArchived bool
	for !(sawDone && sawArchived) {
		select {
		case ev := <-events:
			if ev.ID == activeSess.ID && ev.IsDone {
				sawDone = true
			}
			if ev.ID == activeSess.ID && ev.Archived {
				sawArchived = true
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for live transitions (sawDone=%v sawArchived=%v)", sawDone, sawArchived)
		}
	}
}

func TestSweepAutoArchiveSessions(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ReleaseSleepInhibitor()

	if err := db.SetSettings(map[string]string{
		"auto_archive_enabled": "true",
		"auto_archive_days":    "7",
	}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	now := time.Now().UTC()
	staleTs := now.Add(-9 * 24 * time.Hour)
	recentTs := now.Add(-2 * 24 * time.Hour)

	oldDone := &Session{
		ID: "claude-code:local:arch-old-done", NativeID: "arch-old-done", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, IsDone: true,
		StartedAt: staleTs, LastEventAt: staleTs, UpdatedAt: staleTs, MetadataUpdatedAt: staleTs,
	}
	recentDone := &Session{
		ID: "claude-code:local:arch-recent-done", NativeID: "arch-recent-done", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, IsDone: true,
		StartedAt: recentTs, LastEventAt: recentTs, UpdatedAt: recentTs, MetadataUpdatedAt: recentTs,
	}
	oldActive := &Session{
		ID: "claude-code:local:arch-old-active", NativeID: "arch-old-active", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateIdle, IsDone: false,
		StartedAt: staleTs, LastEventAt: staleTs, UpdatedAt: staleTs, MetadataUpdatedAt: staleTs,
	}
	oldLater := &Session{
		ID: "claude-code:local:arch-old-later", NativeID: "arch-old-later", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, IsDone: true, IsLater: true,
		StartedAt: staleTs, LastEventAt: staleTs, UpdatedAt: staleTs, MetadataUpdatedAt: staleTs,
	}
	remoteDone := &Session{
		ID: "claude-code:remote-box:arch-remote", NativeID: "arch-remote", Agent: "claude-code",
		Host: "remote-box", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, IsDone: true,
		StartedAt: staleTs, LastEventAt: staleTs, UpdatedAt: staleTs, MetadataUpdatedAt: staleTs,
	}
	for _, s := range []*Session{oldDone, recentDone, oldActive, oldLater, remoteDone} {
		if err := db.SaveSession(s); err != nil {
			t.Fatalf("SaveSession(%s): %v", s.ID, err)
		}
	}

	srv.sweepAutoArchiveSessions(context.Background())

	if got, _ := db.GetSession(oldDone.ID); got == nil || !got.Archived {
		t.Fatalf("expected stale Done session archived, got %+v", got)
	}
	if got, _ := db.GetSession(recentDone.ID); got == nil || got.Archived {
		t.Fatalf("expected recent Done session preserved, got %+v", got)
	}
	if got, _ := db.GetSession(oldActive.ID); got == nil || got.Archived {
		t.Fatalf("expected non-Done session preserved, got %+v", got)
	}
	if got, _ := db.GetSession(oldLater.ID); got == nil || got.Archived {
		t.Fatalf("expected IsLater session preserved, got %+v", got)
	}
	if got, _ := db.GetSession(remoteDone.ID); got == nil || got.Archived {
		t.Fatalf("expected remote host session preserved, got %+v", got)
	}

	if err := db.SetSettings(map[string]string{"auto_archive_enabled": "false"}); err != nil {
		t.Fatalf("SetSettings disable: %v", err)
	}
	disabledDone := &Session{
		ID: "claude-code:local:arch-disabled", NativeID: "arch-disabled", Agent: "claude-code",
		Host: "local", Cwd: tmpHome, EngineType: EngineHeadless, State: StateEnded, IsDone: true,
		StartedAt: staleTs, LastEventAt: staleTs, UpdatedAt: staleTs, MetadataUpdatedAt: staleTs,
	}
	if err := db.SaveSession(disabledDone); err != nil {
		t.Fatalf("SaveSession disabledDone: %v", err)
	}
	srv.sweepAutoArchiveSessions(context.Background())
	if got, _ := db.GetSession(disabledDone.ID); got == nil || got.Archived {
		t.Fatalf("expected auto_archive_enabled=false to skip archiving, got %+v", got)
	}
}
