package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUpdateHookSessionMetadataAndTitle_PreservesPureHeadlessCwd(t *testing.T) {
	srv, db := setupTestServer(t)

	mainCwd := filepath.Join(t.TempDir(), "repo")
	worktreeCwd := filepath.Join(mainCwd, ".worktrees", "feat-x")
	_ = os.MkdirAll(worktreeCwd, 0755)

	// Pure headless session with established Cwd ignores hook Cwd drift into worktree or parent
	headlessSess := &Session{
		ID:         "claude-code:local:hook-cwd-1",
		NativeID:   "hook-cwd-1",
		Agent:      "claude-code",
		Host:       "local",
		Cwd:        mainCwd,
		EngineType: EngineHeadless,
		State:      StateWorking,
	}
	_ = db.SaveSession(headlessSess)
	srv.updateHookSessionMetadataAndTitle(headlessSess, &Event{Agent: "claude-code", Cwd: worktreeCwd})
	if headlessSess.Cwd != mainCwd {
		t.Fatalf("pure headless Cwd must not be overwritten by hook event: want %q, got %q", mainCwd, headlessSess.Cwd)
	}

	// Empty pure headless Cwd is populated by hook event
	emptyHeadless := &Session{
		ID:         "claude-code:local:hook-cwd-2",
		NativeID:   "hook-cwd-2",
		Agent:      "claude-code",
		Host:       "local",
		EngineType: EngineHeadless,
		State:      StateWorking,
	}
	srv.updateHookSessionMetadataAndTitle(emptyHeadless, &Event{Agent: "claude-code", Cwd: worktreeCwd})
	if emptyHeadless.Cwd != worktreeCwd {
		t.Fatalf("expected empty pure headless Cwd populated with %q, got %q", worktreeCwd, emptyHeadless.Cwd)
	}

	// Tmux session still shortens related worktree path to parent mainCwd
	tmuxSess := &Session{
		ID:         "claude-code:local:hook-cwd-3",
		NativeID:   "hook-cwd-3",
		Agent:      "claude-code",
		Host:       "local",
		Cwd:        worktreeCwd,
		TmuxName:   "ackbar-claude-code-hook-cwd-3",
		EngineType: EngineTmux,
		State:      StateWorking,
	}
	srv.updateHookSessionMetadataAndTitle(tmuxSess, &Event{Agent: "claude-code", Cwd: mainCwd})
	if tmuxSess.Cwd != mainCwd {
		t.Fatalf("expected tmux session sameOrRelatedDir to update Cwd to %q, got %q", mainCwd, tmuxSess.Cwd)
	}
}

func TestReattachLiveWorkers_ConfigMismatchDuringActiveTurnSetsRotateAfterTurn(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	releaseFile := filepath.Join(tmpHome, "release-rot.flag")

	installMockClaude(t, fmt.Sprintf(`
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"working"}}\n'
while [ ! -f %q ]; do sleep 0.02; done
printf '{"type":"result","result":"done"}\n'
`, releaseFile))

	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 1: %v", err)
	}
	s1 := NewServer(db1)
	sess := newTestHeadlessSession("claude-code:local:reattach-rot", "reattach-rot", tmpHome, StateIdle)
	sess.Model = "sonnet"
	_ = db1.SaveSession(sess)

	if err := s1.headless.RunTurn(context.Background(), sess, "turn in progress"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	runDir := DefaultHeadlessRunDir(dbPath)
	var workerPID int
	waitForWorkerCond(t, 4*time.Second, "worker running", func() bool {
		m, _ := findFirstMetaInDir(runDir)
		if m != nil && m.Status == "running" && m.WorkerPID > 1 {
			workerPID = m.WorkerPID
			return true
		}
		return false
	})

	// Simulate ConfigFingerprint change on session while turn is actively running
	sess.Model = "opus"
	_ = db1.SaveSession(sess)
	s1.headless.ReleaseSleepInhibitor()
	_ = db1.Close()

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 2: %v", err)
	}
	defer db2.Close()
	s2 := NewServer(db2)
	defer s2.headless.ShutdownAllWorkers(time.Second)

	if !isProcessAlive(workerPID) {
		t.Fatalf("expected active worker PID %d NOT to be killed during ReattachLiveWorkers on config mismatch", workerPID)
	}
	s2.headless.mu.RLock()
	handle := s2.headless.warmWorkers[sess.ID]
	s2.headless.mu.RUnlock()
	if handle == nil || !handle.rotateAfterTurn {
		t.Fatalf("expected adopted handle with rotateAfterTurn=true, got %+v", handle)
	}

	_ = os.WriteFile(releaseFile, []byte("go"), 0600)
	waitForWorkerCond(t, 4*time.Second, "turn completes and rotates worker", func() bool {
		s2.headless.mu.RLock()
		h := s2.headless.warmWorkers[sess.ID]
		s2.headless.mu.RUnlock()
		return !s2.headless.IsRunning(sess.ID) && h == nil && !isProcessAlive(workerPID)
	})
}

func TestReattachLiveWorkers_LiveTurnDeduplicatesSeqAndSkipsLiveEmitOnReplay(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	releaseFile := filepath.Join(tmpHome, "release-dedup.flag")

	installMockClaude(t, fmt.Sprintf(`
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"part1-"}}\n'
while [ ! -f %q ]; do sleep 0.02; done
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"part2"}}\n'
printf '{"type":"result","result":"part1-part2"}\n'
`, releaseFile))

	db1, _ := InitDB(dbPath)
	s1 := NewServer(db1)
	sess := newTestHeadlessSession("claude-code:local:dedup-1", "dedup-1", tmpHome, StateIdle)
	_ = db1.SaveSession(sess)
	if err := s1.headless.RunTurn(context.Background(), sess, "dedup prompt"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	runDir := DefaultHeadlessRunDir(dbPath)
	waitForWorkerCond(t, 4*time.Second, "part1 spooled", func() bool {
		m, _ := findFirstMetaInDir(runDir)
		if m == nil {
			return false
		}
		evts, _, _ := ReadTurnSpoolAfter(m.SpoolPath, 0)
		for _, e := range evts {
			if e.Type == "text_delta" && e.Text == "part1-" {
				return true
			}
		}
		return false
	})
	s1.headless.ReleaseSleepInhibitor()
	_ = db1.Close()

	db2, _ := InitDB(dbPath)
	defer db2.Close()
	h2 := NewHeadlessRunner(db2, nil)
	defer h2.ShutdownAllWorkers(time.Second)

	liveCh, unsub := h2.Subscribe(sess.ID)
	defer unsub()

	h2.ReattachLiveWorkers()

	// Ensure silent replay did not emit any pre-existing events on liveCh
	select {
	case unexpected := <-liveCh:
		t.Fatalf("ReattachLiveWorkers must not emit pre-existing spool events on live bus, got %+v", unexpected)
	case <-time.After(100 * time.Millisecond):
	}

	_ = os.WriteFile(releaseFile, []byte("go"), 0600)
	var liveSeqs []int64
	var liveText string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-liveCh:
			if evt.Seq > 0 {
				liveSeqs = append(liveSeqs, evt.Seq)
			}
			if evt.Type == "text_delta" {
				liveText += evt.Text
			}
			if evt.Type == "turn_complete" {
				goto done
			}
		case <-deadline:
			t.Fatalf("timed out waiting for turn_complete on liveCh")
		}
	}
done:
	if liveText != "part2" {
		t.Fatalf("expected only post-reattach delta 'part2' on liveCh, got %q (seqs=%v)", liveText, liveSeqs)
	}
	// Check strictly increasing seqs > 2 (seq 1=turn_start, seq 2=text_delta 'part1-')
	if !slices.Equal(liveSeqs, []int64{3, 4}) {
		t.Fatalf("expected monitorWorkerTurn to resume strictly from maxSeq=2 and emit [3 4], got %v", liveSeqs)
	}
}

func TestCrashedSpool_MergeInFlightTranscriptVisiblePast10sWithoutAssistantText(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	db, _ := InitDB(dbPath)
	defer db.Close()

	srv := NewServer(db)
	defer srv.headless.ReleaseSleepInhibitor()

	sess := newTestHeadlessSession("claude-code:local:crash-vis", "crash-vis", tmpHome, StateWorking)
	_ = db.SaveSession(sess)
	runDir := DefaultHeadlessRunDir(dbPath)
	_ = os.MkdirAll(runDir, 0700)

	metaPath := filepath.Join(runDir, "turn_crash_vis.meta.json")
	spoolPath := filepath.Join(runDir, "turn_crash_vis.ndjson")
	startedAt := time.Now().Add(-45 * time.Second).UTC()
	w, err := OpenTurnSpoolWriter(spoolPath, "turn_crash_vis")
	if err != nil {
		t.Fatalf("OpenTurnSpoolWriter: %v", err)
	}
	_, _ = w.Append(ChatStreamEvent{SessionID: sess.ID, TurnID: "turn_crash_vis", Type: "turn_start", Text: "Run build", Timestamp: startedAt})
	_ = w.Close()

	_ = WriteTurnSpoolMeta(metaPath, &TurnSpoolMeta{
		TurnID: "turn_crash_vis", SessionID: sess.ID, OrigSessionID: sess.ID, NativeID: sess.NativeID,
		Agent: "claude-code", Cwd: tmpHome, Prompt: "Run build",
		SocketPath: filepath.Join(runDir, "missing.sock"), SpoolPath: spoolPath, MetaPath: metaPath,
		WorkerPID: 999998, StartedAt: startedAt, Status: "running",
	})

	srv.headless.ReattachLiveWorkers()

	// Backdate FinishedAt > 10s ago to simulate inspecting transcript long after crash
	m, _ := ReadTurnSpoolMeta(metaPath)
	m.FinishedAt = time.Now().Add(-30 * time.Second).UTC()
	_ = WriteTurnSpoolMeta(metaPath, m)

	rec := httptest.NewRecorder()
	srv.handleSessionTranscript(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("transcript GET returned %d: %s", rec.Code, rec.Body.String())
	}
	var trResp struct {
		Messages     []TranscriptMessage    `json:"messages"`
		InFlightTurn map[string]interface{} `json:"in_flight_turn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &trResp); err != nil {
		t.Fatalf("decode transcript: %v", err)
	}
	if len(trResp.Messages) != 2 {
		t.Fatalf("expected crashed turn user + error assistant messages visible past 10s, got %+v", trResp.Messages)
	}
	if trResp.Messages[0].Content != "Run build" || trResp.Messages[1].Content != crashedWorkerErrorText {
		t.Fatalf("unexpected crashed transcript messages: %+v", trResp.Messages)
	}
	if trResp.InFlightTurn == nil || trResp.InFlightTurn["turn_id"] != "turn_crash_vis" {
		t.Fatalf("expected in_flight_turn metadata preserved for crashed spool, got %+v", trResp.InFlightTurn)
	}
}

func TestReattachLiveWorkers_BlockedControlMismatchSetsRotateAfterTurn(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	db, _ := InitDB(dbPath)
	defer db.Close()

	runDir := DefaultHeadlessRunDir(dbPath)
	_ = os.MkdirAll(runDir, 0700)

	sess := newTestHeadlessSession("claude-code:local:blocked-ctrl", "blocked-ctrl", tmpHome, StateWorking)
	sess.Model = "opus"
	_ = db.SaveSession(sess)

	sockPath, sockTempDir, err := allocateWorkerSocketPath(runDir, "w_ctrl")
	if err != nil {
		t.Fatalf("allocateWorkerSocketPath: %v", err)
	}
	if sockTempDir != "" {
		defer os.RemoveAll(sockTempDir)
	}
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var req workerIPCReq
				_ = json.NewDecoder(conn).Decode(&req)
				resp := workerIPCResp{OK: true, WorkerPID: 999997, ChildPID: 999996, State: "blocked_control"}
				if req.Op == "shutdown" {
					resp.State = "shutting_down"
				}
				_ = json.NewEncoder(conn).Encode(resp)
			}(c)
		}
	}()

	wmPath := filepath.Join(runDir, "w_ctrl.worker.json")
	_ = WriteSessionWorkerMeta(wmPath, &SessionWorkerMeta{
		WorkerID:          "w_ctrl",
		SessionID:         sess.ID,
		OrigSessionID:     sess.ID,
		NativeID:          sess.NativeID,
		Agent:             "claude-code",
		Cwd:               tmpHome,
		ConfigFingerprint: "stale-config-fp",
		SocketPath:        sockPath,
		SocketTempDir:     sockTempDir,
		WorkerMetaPath:    wmPath,
		WorkerPID:         999997,
		State:             "blocked_control",
		ActiveTurnID:      "turn_ctrl",
		StartedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})

	h := NewHeadlessRunner(db, nil)
	h.ReattachLiveWorkers()

	h.mu.RLock()
	handle := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if handle == nil || !handle.rotateAfterTurn {
		t.Fatalf("expected blocked_control worker with mismatched ConfigFingerprint adopted with rotateAfterTurn=true, got %+v", handle)
	}
}

func TestCrashedSpool_MergeInFlightTranscriptSurfacesErrorOverPartialDiskPreamble(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	db, _ := InitDB(dbPath)
	defer db.Close()

	srv := NewServer(db)
	defer srv.headless.ReleaseSleepInhibitor()

	sess := newTestHeadlessSession("claude-code:local:crash-preamble", "crash-preamble", tmpHome, StateWorking)
	_ = db.SaveSession(sess)

	// Write on-disk JSONL containing the user prompt + a long partial assistant preamble (longer than the error text)
	projDir := filepath.Join(tmpHome, ".claude", "projects", encodeClaudeProjectDir(tmpHome))
	_ = os.MkdirAll(projDir, 0755)
	ts := time.Now().Add(-20 * time.Second).UTC().Format(time.RFC3339)
	preamble := "Let me check the configuration files and repository structure carefully before modifying anything in the workspace."
	jsonl := fmt.Sprintf(
		"{\"type\":\"user\",\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"Fix bug\"}}\n"+
			"{\"type\":\"assistant\",\"timestamp\":%q,\"message\":{\"role\":\"assistant\",\"content\":%q}}\n",
		ts, ts, preamble,
	)
	_ = os.WriteFile(filepath.Join(projDir, sess.NativeID+".jsonl"), []byte(jsonl), 0644)

	runDir := DefaultHeadlessRunDir(dbPath)
	_ = os.MkdirAll(runDir, 0700)
	metaPath := filepath.Join(runDir, "turn_crash_preamble.meta.json")
	spoolPath := filepath.Join(runDir, "turn_crash_preamble.ndjson")
	w, _ := OpenTurnSpoolWriter(spoolPath, "turn_crash_preamble")
	_, _ = w.Append(ChatStreamEvent{SessionID: sess.ID, TurnID: "turn_crash_preamble", Type: "turn_start", Text: "Fix bug", Timestamp: time.Now().Add(-20 * time.Second).UTC()})
	_ = w.Close()
	_ = WriteTurnSpoolMeta(metaPath, &TurnSpoolMeta{
		TurnID: "turn_crash_preamble", SessionID: sess.ID, OrigSessionID: sess.ID, NativeID: sess.NativeID,
		Agent: "claude-code", Cwd: tmpHome, Prompt: "Fix bug",
		SocketPath: filepath.Join(runDir, "missing_preamble.sock"), SpoolPath: spoolPath, MetaPath: metaPath,
		WorkerPID: 999995, StartedAt: time.Now().Add(-20 * time.Second).UTC(), Status: "running",
	})

	srv.headless.ReattachLiveWorkers()

	rec := httptest.NewRecorder()
	srv.handleSessionTranscript(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("transcript GET returned %d: %s", rec.Code, rec.Body.String())
	}
	var trResp struct {
		Messages []TranscriptMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &trResp); err != nil {
		t.Fatalf("decode transcript: %v", err)
	}
	if len(trResp.Messages) != 2 {
		t.Fatalf("expected 2 transcript messages, got %+v", trResp.Messages)
	}
	content := trResp.Messages[1].Content
	if !strings.Contains(content, preamble) || !strings.Contains(content, crashedWorkerErrorText) {
		t.Fatalf("expected partial on-disk preamble AND crash error surfaced, got %q", content)
	}

	// Short on-disk preamble (< len(errText)) must also be preserved alongside crash error
	shortPreamble := "Checking."
	shortJSONL := fmt.Sprintf(
		"{\"type\":\"user\",\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"Fix bug\"}}\n"+
			"{\"type\":\"assistant\",\"timestamp\":%q,\"message\":{\"role\":\"assistant\",\"content\":%q}}\n",
		ts, ts, shortPreamble,
	)
	_ = os.WriteFile(filepath.Join(projDir, sess.NativeID+".jsonl"), []byte(shortJSONL), 0644)

	recShort := httptest.NewRecorder()
	srv.handleSessionTranscript(recShort, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil))
	if recShort.Code != http.StatusOK {
		t.Fatalf("short preamble transcript GET returned %d: %s", recShort.Code, recShort.Body.String())
	}
	var trShort struct {
		Messages []TranscriptMessage `json:"messages"`
	}
	if err := json.Unmarshal(recShort.Body.Bytes(), &trShort); err != nil {
		t.Fatalf("decode short transcript: %v", err)
	}
	if len(trShort.Messages) != 2 {
		t.Fatalf("expected 2 transcript messages for short preamble, got %+v", trShort.Messages)
	}
	shortContent := trShort.Messages[1].Content
	if !strings.Contains(shortContent, shortPreamble) || !strings.Contains(shortContent, crashedWorkerErrorText) {
		t.Fatalf("expected short on-disk preamble %q AND crash error surfaced, got %q", shortPreamble, shortContent)
	}
}

func TestFinalizeCrashedTurnSpool_PreservesLivePersistentWorkerSocket(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	db, _ := InitDB(dbPath)
	defer db.Close()

	runDir := DefaultHeadlessRunDir(dbPath)
	_ = os.MkdirAll(runDir, 0700)

	sess := newTestHeadlessSession("claude-code:local:keep-sock", "keep-sock", tmpHome, StateIdle)
	_ = db.SaveSession(sess)

	sockPath, sockTempDir, err := allocateWorkerSocketPath(runDir, "w_live_keep")
	if err != nil {
		t.Fatalf("allocateWorkerSocketPath: %v", err)
	}
	if sockTempDir != "" {
		defer os.RemoveAll(sockTempDir)
	}
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var req workerIPCReq
				_ = json.NewDecoder(conn).Decode(&req)
				resp := workerIPCResp{OK: req.TurnID == "", WorkerPID: 999994, ChildPID: 999993, State: "idle_warm"}
				_ = json.NewEncoder(conn).Encode(resp)
			}(c)
		}
	}()

	wmPath := filepath.Join(runDir, "w_live_keep.worker.json")
	_ = WriteSessionWorkerMeta(wmPath, &SessionWorkerMeta{
		WorkerID: "w_live_keep", SessionID: sess.ID, OrigSessionID: sess.ID, NativeID: sess.NativeID,
		Agent: "claude-code", Cwd: tmpHome, ConfigFingerprint: ConfigFingerprint(sess),
		SocketPath: sockPath, SocketTempDir: sockTempDir, WorkerMetaPath: wmPath,
		WorkerPID: 999994, State: "idle_warm", StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	metaPath := filepath.Join(runDir, "turn_stale_crash.meta.json")
	spoolPath := filepath.Join(runDir, "turn_stale_crash.ndjson")
	_ = os.WriteFile(spoolPath, []byte(`{"seq":1,"type":"turn_start","text":"old"}`+"\n"), 0600)
	_ = WriteTurnSpoolMeta(metaPath, &TurnSpoolMeta{
		TurnID: "turn_stale_crash", SessionID: sess.ID, OrigSessionID: sess.ID, NativeID: sess.NativeID,
		Agent: "claude-code", Cwd: tmpHome, Prompt: "old",
		SocketPath: sockPath, SocketTempDir: sockTempDir, SpoolPath: spoolPath, MetaPath: metaPath,
		WorkerMetaPath: wmPath, WorkerPID: 999994, StartedAt: time.Now().UTC(), Status: "running",
	})

	h := NewHeadlessRunner(db, nil)
	h.ReattachLiveWorkers()

	if !fileExists(sockPath) {
		t.Fatalf("expected active persistent worker socket %s NOT to be deleted by stale turn crash finalization", sockPath)
	}
}
