package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func installMockClaude(t *testing.T, body string) string {
	t.Helper()
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatalf("write mock claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return scriptPath
}

func waitForWorkerCond(t *testing.T, timeout time.Duration, desc string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func findFirstMetaInDir(runDir string) (*TurnSpoolMeta, string) {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return nil, ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".meta.json") {
			mp := filepath.Join(runDir, e.Name())
			if m, err := ReadTurnSpoolMeta(mp); err == nil && m != nil {
				return m, mp
			}
		}
	}
	return nil, ""
}

func newTestHeadlessSession(id, nativeID, cwd string, state State) *Session {
	return &Session{
		ID: id, NativeID: nativeID, Agent: "claude-code", Host: "local",
		Cwd: cwd, EngineType: EngineHeadless, Managed: true, State: state,
	}
}

func TestHeadlessWorker_SurvivesDaemonRestartAndReattaches(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "ackbard.db")
	releaseFile := filepath.Join(dbDir, "release.flag")

	installMockClaude(t, fmt.Sprintf(`
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"chunk-1 "}}\n'
while [ ! -f %q ]; do sleep 0.02; done
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"chunk-2"}}\n'
printf '{"type":"result","result":"chunk-1 chunk-2"}\n'
`, releaseFile))

	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB(db1): %v", err)
	}
	s1 := NewServer(db1)
	sess := newTestHeadlessSession("claude-code:local:reattach-1", "reattach-1", tmpHome, StateIdle)
	_ = db1.SaveSession(sess)
	if err := s1.headless.RunTurn(context.Background(), sess, "Survive daemon restart"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	runDir := DefaultHeadlessRunDir(dbPath)
	var liveMeta *TurnSpoolMeta
	waitForWorkerCond(t, 4*time.Second, "worker running with chunk-1 spooled", func() bool {
		m, _ := findFirstMetaInDir(runDir)
		if m == nil || m.WorkerPID <= 0 || m.Status != "running" {
			return false
		}
		evts, _, _ := ReadTurnSpoolAfter(m.SpoolPath, 0)
		for _, e := range evts {
			if e.Type == "text_delta" && strings.Contains(e.Text, "chunk-1") {
				liveMeta = m
				return true
			}
		}
		return false
	})

	s1.headless.ReleaseSleepInhibitor()
	_ = db1.Close()

	if !isProcessAlive(liveMeta.WorkerPID) {
		t.Fatalf("expected detached worker PID %d to survive daemon shutdown", liveMeta.WorkerPID)
	}

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB(db2): %v", err)
	}
	defer db2.Close()
	s2 := NewServer(db2)
	defer s2.headless.ReleaseSleepInhibitor()

	if interrupted := db2.PopInterruptedHeadlessSessionIDs(); slices.Contains(interrupted, sess.ID) {
		t.Fatalf("reattached session %s must be removed from interruptedHeadlessIDs, got %v", sess.ID, interrupted)
	}
	if !s2.headless.IsRunning(sess.ID) {
		t.Fatalf("expected s2.headless.IsRunning(%s) == true after ReattachLiveWorkers", sess.ID)
	}
	if reattachedSess, _ := db2.GetSession(sess.ID); reattachedSess == nil || reattachedSess.State != StateWorking {
		t.Fatalf("expected StateWorking on reattached session, got %+v", reattachedSess)
	}

	subCh, cleanup := s2.headless.SubscribeWithReplay(sess.ID, "", 0, "claude-code")
	defer cleanup()
	_ = os.WriteFile(releaseFile, []byte("go"), 0600)

	var combinedText string
	var prevSeq int64
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-subCh:
			if evt.Seq <= prevSeq {
				t.Fatalf("expected strictly monotonic Seq > %d, got %d (%+v)", prevSeq, evt.Seq, evt)
			}
			prevSeq = evt.Seq
			if evt.Type == "text_delta" {
				combinedText += evt.Text
			}
			if evt.Type == "turn_complete" {
				goto completed
			}
		case <-deadline:
			t.Fatalf("timed out waiting for turn_complete after reattach")
		}
	}
completed:
	if combinedText != "chunk-1 chunk-2" {
		t.Fatalf("expected combined streamed text 'chunk-1 chunk-2', got %q", combinedText)
	}
	waitForWorkerCond(t, 3*time.Second, "session finalized to StateIdle", func() bool {
		cur, _ := db2.GetSession(sess.ID)
		m, _ := ReadTurnSpoolMeta(liveMeta.MetaPath)
		return cur != nil && cur.State == StateIdle && m != nil && m.FinalizedByDaemon
	})
}

func TestHeadlessWorker_OfflineCompletionAndDeadWorkerFallback(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "ackbard.db")
	releaseFile := filepath.Join(dbDir, "offline-release.flag")

	installMockClaude(t, fmt.Sprintf(`
while [ ! -f %q ]; do sleep 0.02; done
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"offline finish"}}\n'
printf '{"type":"result","result":"offline finish"}\n'
`, releaseFile))

	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB(db1): %v", err)
	}
	s1 := NewServer(db1)
	sessDoneOffline := newTestHeadlessSession("claude-code:local:offline-done", "offline-done", tmpHome, StateIdle)
	sessDeadWorker := newTestHeadlessSession("claude-code:local:dead-worker", "dead-worker", tmpHome, StateWorking)
	_ = db1.SaveSession(sessDoneOffline)
	_ = db1.SaveSession(sessDeadWorker)

	if err := s1.headless.RunTurn(context.Background(), sessDoneOffline, "Finish while daemon offline"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	runDir := DefaultHeadlessRunDir(dbPath)
	var offlineMeta *TurnSpoolMeta
	waitForWorkerCond(t, 4*time.Second, "worker running", func() bool {
		m, _ := findFirstMetaInDir(runDir)
		if m != nil && m.Status == "running" {
			offlineMeta = m
			return true
		}
		return false
	})

	s1.headless.ReleaseSleepInhibitor()
	_ = db1.Close()

	_ = os.WriteFile(releaseFile, []byte("go"), 0600)
	waitForWorkerCond(t, 4*time.Second, "worker finished while offline", func() bool {
		m, _ := ReadTurnSpoolMeta(offlineMeta.MetaPath)
		return m != nil && m.Status == "completed" && !isProcessAlive(m.WorkerPID)
	})

	deadMetaPath := filepath.Join(runDir, "dead-turn.meta.json")
	deadSpoolPath := filepath.Join(runDir, "dead-turn.ndjson")
	_ = os.WriteFile(deadSpoolPath, []byte{}, 0600)
	_ = WriteTurnSpoolMeta(deadMetaPath, &TurnSpoolMeta{
		TurnID: "dead-turn", SessionID: sessDeadWorker.ID, NativeID: sessDeadWorker.NativeID,
		Agent: "claude-code", Prompt: "Crashed turn", SpoolPath: deadSpoolPath, MetaPath: deadMetaPath,
		WorkerPID: 999999, StartedAt: time.Now().UTC(), Status: "running",
	})

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB(db2): %v", err)
	}
	defer db2.Close()
	s2 := NewServer(db2)
	defer s2.headless.ReleaseSleepInhibitor()

	interrupted := db2.PopInterruptedHeadlessSessionIDs()
	if slices.Contains(interrupted, sessDoneOffline.ID) {
		t.Fatalf("offline-completed session must NOT remain in interruptedHeadlessIDs: %v", interrupted)
	}
	if !slices.Contains(interrupted, sessDeadWorker.ID) {
		t.Fatalf("dead-worker session MUST remain in interruptedHeadlessIDs for resume fallback: %v", interrupted)
	}
	sOffline, _ := db2.GetSession(sessDoneOffline.ID)
	mOffline, _ := ReadTurnSpoolMeta(offlineMeta.MetaPath)
	if sOffline == nil || sOffline.State != StateIdle || mOffline == nil || !mOffline.FinalizedByDaemon {
		t.Fatalf("expected offline-completed session finalized to StateIdle, got sess=%+v meta=%+v", sOffline, mOffline)
	}
	if fileExists(deadMetaPath) || fileExists(deadSpoolPath) {
		t.Fatalf("expected dead worker spool artifacts to be cleaned up")
	}
}

func TestHeadlessWorker_PermissionsNoSecretsAndShortSocketFallback(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("ACKBAR_UNIT_SECRET", "top-secret-never-write-to-disk-999")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret-never-persist")

	deepDir := filepath.Join(t.TempDir(), strings.Repeat("deep-segment-", 8))
	if err := os.MkdirAll(deepDir, 0700); err != nil {
		t.Fatalf("MkdirAll deepDir: %v", err)
	}
	dbPath := filepath.Join(deepDir, "ackbard.db")
	releaseFile := filepath.Join(tmpHome, "perm-release.flag")

	installMockClaude(t, fmt.Sprintf(`
printf '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"checking perms"}}\n'
while [ ! -f %q ]; do sleep 0.02; done
printf '{"type":"result","result":"ok"}\n'
`, releaseFile))

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ReleaseSleepInhibitor()

	sess := newTestHeadlessSession("claude-code:local:perm-sock", "perm-sock", tmpHome, StateIdle)
	_ = db.SaveSession(sess)
	if err := srv.headless.RunTurn(context.Background(), sess, "Verify permissions and short socket"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	runDir := DefaultHeadlessRunDir(dbPath)
	var meta *TurnSpoolMeta
	var metaPath string
	waitForWorkerCond(t, 4*time.Second, "worker socket live", func() bool {
		m, mp := findFirstMetaInDir(runDir)
		if m != nil && m.Status == "running" && fileExists(m.SocketPath) {
			meta, metaPath = m, mp
			return true
		}
		return false
	})

	if runInfo, err := os.Stat(runDir); err != nil || runInfo.Mode().Perm() != 0700 {
		t.Fatalf("expected runDir 0700, got %v (err=%v)", runInfo, err)
	}
	for _, p := range []string{metaPath, meta.SpoolPath, meta.SocketPath} {
		if fi, sErr := os.Stat(p); sErr != nil || fi.Mode().Perm() != 0600 {
			t.Fatalf("expected 0600 permissions on %s, got %v (err=%v)", p, fi, sErr)
		}
	}
	if meta.SocketTempDir == "" || !strings.Contains(filepath.Base(meta.SocketTempDir), "akw-") || len(meta.SocketPath) > 96 {
		t.Fatalf("expected unpredictable <=96B akw-* socket fallback, got dir=%q sock=%q", meta.SocketTempDir, meta.SocketPath)
	}
	if tdInfo, err := os.Stat(meta.SocketTempDir); err != nil || tdInfo.Mode().Perm() != 0700 {
		t.Fatalf("expected SocketTempDir 0700, got %v (err=%v)", tdInfo, err)
	}

	rawMetaBytes, _ := os.ReadFile(metaPath)
	if strings.Contains(string(rawMetaBytes), "top-secret-never-write-to-disk-999") ||
		strings.Contains(string(rawMetaBytes), "sk-ant-secret-never-persist") {
		t.Fatalf("secret leaked into .meta.json: %s", string(rawMetaBytes))
	}

	_ = os.WriteFile(releaseFile, []byte("done"), 0600)
	waitForWorkerCond(t, 4*time.Second, "worker cleanup of socket and SocketTempDir", func() bool {
		return !srv.headless.IsRunning(sess.ID) && !fileExists(meta.SocketPath) && !fileExists(meta.SocketTempDir)
	})
}

func TestChatStreamAndTranscript_SpoolReplayAndSynthesis(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	releaseFile := filepath.Join(tmpHome, "stream-release.flag")

	installMockClaude(t, fmt.Sprintf(`
printf '{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan..."}}\n'
printf '{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"main.go"}}}\n'
printf '{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Hello "}}\n'
while [ ! -f %q ]; do sleep 0.02; done
printf '{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"world!"}}\n'
printf '{"type":"result","result":"Hello world!"}\n'
`, releaseFile))

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ReleaseSleepInhibitor()

	sess := newTestHeadlessSession("claude-code:local:stream-replay", "stream-replay", tmpHome, StateIdle)
	_ = db.SaveSession(sess)
	if err := srv.headless.RunTurn(context.Background(), sess, "Stream and synthesize"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	runDir := DefaultHeadlessRunDir(dbPath)
	var turnID string
	waitForWorkerCond(t, 10*time.Second, "seq >= 4 spooled", func() bool {
		m, _ := findFirstMetaInDir(runDir)
		if m == nil {
			return false
		}
		_, maxSeq, _ := ReadTurnSpoolAfter(m.SpoolPath, 0)
		if maxSeq >= 4 {
			turnID = m.TurnID
			return true
		}
		return false
	})

	trRec := httptest.NewRecorder()
	srv.handleSessionTranscript(trRec, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil))
	if trRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for mid-turn transcript, got %d: %s", trRec.Code, trRec.Body.String())
	}
	var trResp struct {
		Messages     []TranscriptMessage    `json:"messages"`
		InFlightTurn map[string]interface{} `json:"in_flight_turn"`
	}
	_ = json.Unmarshal(trRec.Body.Bytes(), &trResp)
	if len(trResp.Messages) != 2 || !trResp.Messages[0].InFlight || !trResp.Messages[1].InFlight || trResp.Messages[1].TurnID != turnID {
		t.Fatalf("expected 2 in-flight synthesized messages with TurnID=%s, got %+v", turnID, trResp.Messages)
	}
	if trResp.InFlightTurn == nil || trResp.InFlightTurn["turn_id"] != turnID || trResp.InFlightTurn["active"] != true {
		t.Fatalf("unexpected in_flight_turn: %+v", trResp.InFlightTurn)
	}

	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handleChatStream))
	defer httpSrv.Close()

	req, _ := http.NewRequest(http.MethodGet, httpSrv.URL+"?session_id="+sess.ID, nil)
	req.Header.Set("Last-Event-ID", fmt.Sprintf("%s:2", turnID))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("GET chat stream: %v", err)
	}
	defer resp.Body.Close()
	_ = os.WriteFile(releaseFile, []byte("go"), 0600)

	var seenSeqs []int64
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			var evt ChatStreamEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt) == nil && evt.Seq > 0 {
				seenSeqs = append(seenSeqs, evt.Seq)
				if evt.Type == "turn_complete" {
					break
				}
			}
		}
	}
	if !slices.Equal(seenSeqs, []int64{3, 4, 5, 6}) {
		t.Fatalf("expected gapless deduplicated seqs [3 4 5 6] after Last-Event-ID %s:2, got %v", turnID, seenSeqs)
	}
	waitForWorkerCond(t, 3*time.Second, "turn finalized", func() bool { return !srv.headless.IsRunning(sess.ID) })

	projDir := filepath.Join(tmpHome, ".claude", "projects", "test-proj")
	_ = os.MkdirAll(projDir, 0755)
	nowStr := time.Now().UTC().Format(time.RFC3339)
	jsonl := fmt.Sprintf(
		"{\"type\":\"user\",\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"Stream and synthesize\"}}\n"+
			"{\"type\":\"assistant\",\"timestamp\":%q,\"message\":{\"role\":\"assistant\",\"content\":\"Hello world!\"}}\n",
		nowStr, nowStr,
	)
	_ = os.WriteFile(filepath.Join(projDir, sess.NativeID+".jsonl"), []byte(jsonl), 0644)

	trRec2 := httptest.NewRecorder()
	srv.handleSessionTranscript(trRec2, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil))
	var trResp2 struct {
		Messages     []TranscriptMessage    `json:"messages"`
		InFlightTurn map[string]interface{} `json:"in_flight_turn"`
	}
	_ = json.Unmarshal(trRec2.Body.Bytes(), &trResp2)
	if trResp2.InFlightTurn != nil || len(trResp2.Messages) != 2 || trResp2.Messages[0].TurnID != turnID || trResp2.Messages[1].TurnID != turnID {
		t.Fatalf("expected suppressed in_flight_turn and 2 deduplicated messages, got in_flight=%+v msgs=%+v", trResp2.InFlightTurn, trResp2.Messages)
	}
}
