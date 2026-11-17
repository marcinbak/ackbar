package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sendClaudeHookEvent(srv *Server, host, nativeID, cwd, eventName, agentType string, state State) {
	srv.processParsedHookEvent(&Event{
		Agent:       "claude-code",
		NativeID:    nativeID,
		Cwd:         cwd,
		EventName:   eventName,
		ToolName:    agentType,
		State:       state,
		LastEventAt: time.Now(),
	}, eventName, host, "", false)
}

func fetchSubagentCountViaHTTP(t *testing.T, srv *Server, id string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/subagents?id="+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/sessions/subagents failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		RunningCount int `json:"running_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal /v1/sessions/subagents: %v", err)
	}
	return resp.RunningCount
}

func drainLatestBroadcast(ch chan *Session) *Session {
	var latest *Session
	for {
		select {
		case s := <-ch:
			latest = s
		default:
			return latest
		}
	}
}

func TestBgTasks_ChangedReplaceSemanticsAndIdleTTLDeferral(t *testing.T) {
	now := time.Now()
	if wait, hold := computeBgTaskIdleHold(0, 100*time.Millisecond, now, now.Add(time.Minute)); hold || wait != 0 {
		t.Fatalf("expected hold=false when bgCount=0, got hold=%v wait=%v", hold, wait)
	}
	if wait, hold := computeBgTaskIdleHold(1, 100*time.Millisecond, now, now.Add(time.Hour)); !hold || wait != 100*time.Millisecond {
		t.Fatalf("expected hold=true wait=100ms within 6h cap, got hold=%v wait=%v", hold, wait)
	}
	if _, hold := computeBgTaskIdleHold(1, 100*time.Millisecond, now, now.Add(maxBgTaskIdleHold+time.Second)); hold {
		t.Fatalf("expected hold=false past 6h safety cap")
	}

	tmpDir := t.TempDir()
	clearGate := filepath.Join(tmpDir, "clear.gate")
	installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo '{"type":"system","subtype":"task_started","task_id":"task_initial","is_backgrounded":true}'
echo '{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"t_keep","status":"running"},{"task_id":"t_amb","status":"running","ambient":true},{"task_id":"t_off","status":"running","off_host":true},{"task_id":"t_done","status":"completed"}]}'
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Started bg task."}}'
echo '{"type":"result","result":"Started bg task.","is_error":false}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"system","subtype":"background_tasks_changed","tasks":[]}'
read -r next_turn
`, clearGate))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ShutdownAllWorkers(time.Second)
	srv.headless.SetIdleTTL(80 * time.Millisecond)

	sess := newTestHeadlessSession("claude-code:local:bg-ttl", "bg-ttl", tmpDir, StateIdle)
	_ = db.SaveSession(sess)

	if err := srv.headless.RunTurn(context.Background(), sess, "Start background job"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	waitForWorkerCond(t, 5*time.Second, "turn1 idle_warm with 1 active bg task", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateIdle && srv.headless.HasWarmWorker(sess.ID) && srv.headless.GetBgTaskCount(sess.ID) == 1
	})

	srv.headless.mu.RLock()
	wh := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if wh == nil || wh.WorkerMetaPath == "" {
		t.Fatalf("expected warm worker handle with WorkerMetaPath")
	}
	wm, wErr := ReadSessionWorkerMeta(wh.WorkerMetaPath)
	if wErr != nil || wm == nil || wm.BgTaskCount != 1 {
		t.Fatalf("expected workerMeta.BgTaskCount=1 (replace semantics excluding ambient/off_host/completed), got %+v err=%v", wm, wErr)
	}

	time.Sleep(240 * time.Millisecond)
	if !srv.headless.HasWarmWorker(sess.ID) {
		t.Fatalf("expected warm worker to stay alive past 80ms idleTTL while BgTaskCount=1")
	}
	pingResp, pErr := callWorkerSocket(wm.SocketPath, workerIPCReq{Op: "ping"}, time.Second)
	if pErr != nil || pingResp == nil || !pingResp.OK || pingResp.BgTaskCount != 1 {
		t.Fatalf("expected ping OK with BgTaskCount=1 while deferred, got %+v err=%v", pingResp, pErr)
	}

	_ = os.WriteFile(clearGate, []byte("clear"), 0600)
	waitForWorkerCond(t, 5*time.Second, "bg count cleared to 0", func() bool {
		return srv.headless.GetBgTaskCount(sess.ID) == 0
	})
	waitForWorkerCond(t, 5*time.Second, "warm worker shuts down once bgTaskCount=0", func() bool {
		return !srv.headless.HasWarmWorker(sess.ID)
	})
}

func TestBgTasks_UnsolicitedStdoutOpensBgTurnWithoutDroppingOutput(t *testing.T) {
	tmpDir := t.TempDir()
	emitGate := filepath.Join(tmpDir, "emit.gate")
	finishGate := filepath.Join(tmpDir, "finish.gate")
	installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Turn 1 done."}}'
echo '{"type":"result","result":"Turn 1 done.","is_error":false}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Unsolicited bg output!"}}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"result","result":"Unsolicited bg output!","is_error":false}'
read -r hold_line
`, emitGate, finishGate))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ShutdownAllWorkers(time.Second)

	sess := newTestHeadlessSession("claude-code:local:bg-unsolicited", "bg-unsolicited", tmpDir, StateIdle)
	_ = db.SaveSession(sess)

	if err := srv.headless.RunTurn(context.Background(), sess, "User initial prompt"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "Turn 1 settled", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID) && srv.headless.HasWarmWorker(sess.ID)
	})

	sub, cleanup := srv.headless.SubscribeWithReplay(sess.ID, "", 0, sess.Agent)
	defer cleanup()

	_ = os.WriteFile(emitGate, []byte("emit"), 0600)

	var seenBgDelta, seenNonEmptySyntheticStart bool
	var bgTurnID string
	deadline := time.After(5 * time.Second)
	for !seenBgDelta {
		select {
		case evt := <-sub:
			if strings.HasPrefix(evt.TurnID, "turn_bg_") {
				bgTurnID = evt.TurnID
				if evt.Type == "turn_start" && strings.TrimSpace(evt.Text) != "" {
					seenNonEmptySyntheticStart = true
				}
				if evt.Type == "text_delta" && strings.Contains(evt.Text, "Unsolicited bg output!") {
					seenBgDelta = true
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for unsolicited turn_bg_* text_delta")
		}
	}
	if seenNonEmptySyntheticStart {
		t.Fatalf("expected synthetic turn_start to omit user Text")
	}

	trRec := httptest.NewRecorder()
	srv.handleSessionTranscript(trRec, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil))
	if trRec.Code != http.StatusOK {
		t.Fatalf("mid-turn transcript status %d: %s", trRec.Code, trRec.Body.String())
	}
	var tr struct {
		Messages []TranscriptMessage `json:"messages"`
	}
	_ = json.Unmarshal(trRec.Body.Bytes(), &tr)
	if len(tr.Messages) != 1 || tr.Messages[0].Role != "assistant" || !strings.Contains(tr.Messages[0].Content, "Unsolicited bg output!") {
		t.Fatalf("expected live synthetic transcript to contain only 1 assistant message and zero fake user bubble, got %+v", tr.Messages)
	}

	_ = os.WriteFile(finishGate, []byte("finish"), 0600)

	seenBgComplete := false
	deadline2 := time.After(5 * time.Second)
	for !seenBgComplete {
		select {
		case evt := <-sub:
			if evt.TurnID == bgTurnID && evt.Type == "turn_complete" {
				seenBgComplete = true
			}
		case <-deadline2:
			t.Fatalf("timed out waiting for %s turn_complete", bgTurnID)
		}
	}

	waitForWorkerCond(t, 5*time.Second, "session back to StateIdle after bg turn", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
	})

	after, _ := db.GetSession(sess.ID)
	if after.FirstPrompt != "User initial prompt" || after.LastPrompt != "User initial prompt" {
		t.Fatalf("expected synthetic bg turn not to clobber FirstPrompt/LastPrompt, got first=%q last=%q", after.FirstPrompt, after.LastPrompt)
	}

	runDir, _ := srv.headless.resolveRunDir(false)
	bgMeta, err := ReadTurnSpoolMeta(filepath.Join(runDir, bgTurnID+".meta.json"))
	if err != nil || bgMeta == nil || !bgMeta.IsSynthetic {
		t.Fatalf("expected synthetic bg turn meta for %s, got %+v err=%v", bgTurnID, bgMeta, err)
	}
	snap, err := SynthesizeInFlightTurn(bgMeta, sess.Agent)
	if err != nil || snap == nil || !snap.IsSynthetic || snap.UserPrompt != "" || !strings.Contains(snap.AssistantText, "Unsolicited bg output!") {
		t.Fatalf("expected synthetic snapshot with empty UserPrompt and bg assistant text, got %+v err=%v", snap, err)
	}
}

func TestBgTasks_DebouncedAutoPokeCancelAndSiblingCoalesce(t *testing.T) {
	t.Run("default 600ms debounce sends BgAutoPokePrompt and transcript filters synthetic prompt", func(t *testing.T) {
		tmpDir := t.TempDir()
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		stdinLog := filepath.Join(tmpDir, "poke.log")
		notifGate := filepath.Join(tmpDir, "notif.gate")
		nativeID := "11111111-1111-1111-1111-111111111111"
		projectDir := filepath.Join(homeDir, ".claude", "projects", encodeClaudeProjectDir(tmpDir))
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatalf("mkdir projectDir: %v", err)
		}
		jsonlPath := filepath.Join(projectDir, nativeID+".jsonl")

		installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo "$turn1" >> %q
printf '%%s\n' '{"type":"user","timestamp":"2026-11-17T10:00:00Z","message":{"role":"user","content":"Launch async job"}}' >> %q
echo '{"type":"system","subtype":"task_started","task_id":"bg_1","is_backgrounded":true}'
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Working asynchronously."}}'
printf '%%s\n' '{"type":"assistant","timestamp":"2026-11-17T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"Working asynchronously."}]}}' >> %q
echo '{"type":"result","result":"Working asynchronously.","is_error":false}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"system","subtype":"task_notification","task_id":"bg_1","status":"completed","summary":"Job finished"}'
read -r poke_turn
echo "$poke_turn" >> %q
printf '%%s\n' '{"type":"user","timestamp":"2026-11-17T10:00:05Z","message":{"role":"user","content":%q}}' >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Background build verified."}}'
printf '%%s\n' '{"type":"assistant","timestamp":"2026-11-17T10:00:06Z","message":{"role":"assistant","content":[{"type":"text","text":"Background build verified."}]}}' >> %q
echo '{"type":"result","result":"Background build verified.","is_error":false}'
read -r hold_line
`, stdinLog, jsonlPath, jsonlPath, notifGate, stdinLog, BgAutoPokePrompt, jsonlPath, jsonlPath))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:"+nativeID, nativeID, tmpDir, StateIdle)
		_ = db.SaveSession(sess)

		if err := srv.headless.RunTurn(context.Background(), sess, "Launch async job"); err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "idle_warm after Turn 1", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID) && srv.headless.HasWarmWorker(sess.ID)
		})

		_ = os.WriteFile(notifGate, []byte("go"), 0600)

		time.Sleep(350 * time.Millisecond)
		earlyLines := strings.Split(strings.TrimSpace(mustReadFile(t, stdinLog)), "\n")
		if len(earlyLines) != 1 {
			t.Fatalf("expected auto-poke not to fire before 600ms debounce, got stdin lines=%d", len(earlyLines))
		}

		waitForWorkerCond(t, 5*time.Second, "auto-poke turn delivered and finalized", func() bool {
			raw, _ := os.ReadFile(stdinLog)
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			cur, _ := db.GetSession(sess.ID)
			return len(lines) >= 2 && cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})

		lines := strings.Split(strings.TrimSpace(mustReadFile(t, stdinLog)), "\n")
		if len(lines) != 2 || !strings.Contains(lines[1], BgAutoPokePrompt) {
			t.Fatalf("expected second stdin line to contain BgAutoPokePrompt, got %v", lines)
		}

		after, _ := db.GetSession(sess.ID)
		if after.LastPrompt != "Launch async job" {
			t.Fatalf("expected LastPrompt to stay 'Launch async job', got %q", after.LastPrompt)
		}

		trRec := httptest.NewRecorder()
		srv.handleSessionTranscript(trRec, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID+"&format=json", nil))
		if trRec.Code != http.StatusOK {
			t.Fatalf("transcript status %d: %s", trRec.Code, trRec.Body.String())
		}
		var tr struct {
			Messages []TranscriptMessage `json:"messages"`
		}
		_ = json.Unmarshal(trRec.Body.Bytes(), &tr)
		userCount := 0
		sawBgReply := false
		for _, m := range tr.Messages {
			if m.Role == "user" {
				userCount++
				if strings.Contains(m.Content, BgAutoPokePrompt) {
					t.Fatalf("expected synthetic BgAutoPokePrompt user entry filtered from transcript, got %+v", m)
				}
			}
			if m.Role == "assistant" && strings.Contains(m.Content, "Background build verified.") {
				sawBgReply = true
			}
		}
		if userCount != 1 || !sawBgReply {
			t.Fatalf("expected 1 user message + bg assistant reply in transcript, got userCount=%d sawBgReply=%v msgs=%+v", userCount, sawBgReply, tr.Messages)
		}
	})

	t.Run("ignores ambient/worker_restart and cancels pending auto-poke when RunTurn runs", func(t *testing.T) {
		t.Setenv("ACKBAR_BG_POKE_DEBOUNCE", "350ms")
		tmpDir := t.TempDir()
		stdinLog := filepath.Join(tmpDir, "cancel.log")
		gate := filepath.Join(tmpDir, "cancel.gate")
		installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo "$turn1" >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Turn 1"}}'
echo '{"type":"result","result":"Turn 1","is_error":false}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"system","subtype":"task_notification","task_id":"amb_1","ambient":true,"status":"completed"}'
echo '{"type":"system","subtype":"task_notification","task_id":"rst_1","reason":"worker_restart","status":"completed"}'
echo '{"type":"system","subtype":"task_notification","task_id":"real_1","status":"completed"}'
read -r turn2
echo "$turn2" >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Explicit turn reply"}}'
echo '{"type":"result","result":"Explicit turn reply","is_error":false}'
read -r hold_line
`, stdinLog, gate, stdinLog))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:bg-cancel", "bg-cancel", tmpDir, StateIdle)
		_ = db.SaveSession(sess)

		if err := srv.headless.RunTurn(context.Background(), sess, "Turn 1"); err != nil {
			t.Fatalf("RunTurn 1: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "idle_warm", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})

		_ = os.WriteFile(gate, []byte("go"), 0600)
		time.Sleep(100 * time.Millisecond)
		if err := srv.headless.RunTurn(context.Background(), sess, "Explicit user follow-up"); err != nil {
			t.Fatalf("RunTurn 2: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "Turn 2 settled", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})

		time.Sleep(450 * time.Millisecond)
		lines := strings.Split(strings.TrimSpace(mustReadFile(t, stdinLog)), "\n")
		if len(lines) != 2 || !strings.Contains(lines[1], "Explicit user follow-up") || strings.Contains(strings.Join(lines, "\n"), BgAutoPokePrompt) {
			t.Fatalf("expected explicit RunTurn to cancel auto-poke and send only 2 turns, got %v", lines)
		}
	})

	t.Run("coalesces sibling task notifications until active bgTaskCount reaches zero", func(t *testing.T) {
		t.Setenv("ACKBAR_BG_POKE_DEBOUNCE", "150ms")
		t.Setenv("ACKBAR_BG_POKE_MAX_COALESCE", "2s")
		tmpDir := t.TempDir()
		stdinLog := filepath.Join(tmpDir, "coalesce.log")
		gate1 := filepath.Join(tmpDir, "c1.gate")
		gate2 := filepath.Join(tmpDir, "c2.gate")
		installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo "$turn1" >> %q
echo '{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"tA","status":"running"},{"task_id":"tB","status":"running"}]}'
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Spawned 2 tasks"}}'
echo '{"type":"result","result":"Spawned 2 tasks","is_error":false}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"system","subtype":"task_notification","task_id":"tA","status":"completed"}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"system","subtype":"task_notification","task_id":"tB","status":"completed"}'
read -r poke_turn
echo "$poke_turn" >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Both handled"}}'
echo '{"type":"result","result":"Both handled","is_error":false}'
read -r hold_line
`, stdinLog, gate1, gate2, stdinLog))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:bg-coalesce", "bg-coalesce", tmpDir, StateIdle)
		_ = db.SaveSession(sess)

		if err := srv.headless.RunTurn(context.Background(), sess, "Launch two"); err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "idle_warm with 2 bg tasks", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID) && srv.headless.GetBgTaskCount(sess.ID) == 2
		})

		_ = os.WriteFile(gate1, []byte("go"), 0600)
		waitForWorkerCond(t, 5*time.Second, "bg count drops to 1", func() bool {
			return srv.headless.GetBgTaskCount(sess.ID) == 1
		})

		time.Sleep(260 * time.Millisecond)
		midLines := strings.Split(strings.TrimSpace(mustReadFile(t, stdinLog)), "\n")
		if len(midLines) != 1 {
			t.Fatalf("expected auto-poke to defer while sibling tB active, got %v", midLines)
		}

		_ = os.WriteFile(gate2, []byte("go"), 0600)
		waitForWorkerCond(t, 5*time.Second, "single coalesced poke completed", func() bool {
			raw, _ := os.ReadFile(stdinLog)
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			cur, _ := db.GetSession(sess.ID)
			return len(lines) >= 2 && cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})

		lines := strings.Split(strings.TrimSpace(mustReadFile(t, stdinLog)), "\n")
		if len(lines) != 2 || !strings.Contains(lines[1], BgAutoPokePrompt) {
			t.Fatalf("expected exactly 1 coalesced BgAutoPokePrompt line, got %v", lines)
		}
	})
}

func TestBgTasks_HooksSubagentPreservationAndWorkerExitCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	exitGate := filepath.Join(tmpDir, "exit.gate")
	installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Ready"}}'
echo '{"type":"result","result":"Ready","is_error":false}'
while [ ! -f %q ]; do
  sleep 0.02
done
exit 0
`, exitGate))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ShutdownAllWorkers(time.Second)

	nativeID := "22222222-2222-2222-2222-222222222222"
	sess := newTestHeadlessSession("claude-code:"+srv.HostName()+":"+nativeID, nativeID, tmpDir, StateIdle)
	sess.Host = srv.HostName()
	_ = db.SaveSession(sess)

	subCh := make(chan *Session, 32)
	srv.subMutex.Lock()
	srv.subscribers[subCh] = true
	srv.subMutex.Unlock()
	defer func() {
		srv.subMutex.Lock()
		delete(srv.subscribers, subCh)
		srv.subMutex.Unlock()
	}()

	if err := srv.headless.RunTurn(context.Background(), sess, "Warm up"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "warm worker ready", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateIdle && srv.headless.HasWarmWorker(sess.ID)
	})
	_ = drainLatestBroadcast(subCh)

	sendClaudeHookEvent(srv, sess.Host, nativeID, tmpDir, "SubagentStart", "Explore", StateWorking)
	if cnt := fetchSubagentCountViaHTTP(t, srv, sess.ID); cnt != 1 {
		t.Fatalf("expected running_count=1 after SubagentStart, got %d", cnt)
	}
	if b := drainLatestBroadcast(subCh); b == nil || b.RunningSubagents != 1 {
		t.Fatalf("expected broadcast RunningSubagents=1 after SubagentStart, got %+v", b)
	}

	sendClaudeHookEvent(srv, sess.Host, nativeID, tmpDir, "Stop", "", StateIdle)
	if cnt := fetchSubagentCountViaHTTP(t, srv, sess.ID); cnt != 1 {
		t.Fatalf("expected Stop hook to preserve SubagentStart running_count=1, got %d", cnt)
	}
	if b := drainLatestBroadcast(subCh); b == nil || b.RunningSubagents != 1 {
		t.Fatalf("expected broadcast RunningSubagents=1 after Stop, got %+v", b)
	}

	sendClaudeHookEvent(srv, sess.Host, nativeID, tmpDir, "SubagentStop", "Explore", StateWorking)
	if cnt := fetchSubagentCountViaHTTP(t, srv, sess.ID); cnt != 0 {
		t.Fatalf("expected SubagentStop to drop running_count to 0, got %d", cnt)
	}
	if b := drainLatestBroadcast(subCh); b == nil || b.RunningSubagents != 0 {
		t.Fatalf("expected broadcast RunningSubagents=0 after SubagentStop, got %+v", b)
	}

	sendClaudeHookEvent(srv, sess.Host, nativeID, tmpDir, "SubagentStart", "Reviewer", StateWorking)
	if cnt := fetchSubagentCountViaHTTP(t, srv, sess.ID); cnt != 1 {
		t.Fatalf("expected running_count=1 after second SubagentStart, got %d", cnt)
	}
	_ = drainLatestBroadcast(subCh)

	_ = os.WriteFile(exitGate, []byte("bye"), 0600)
	waitForWorkerCond(t, 5*time.Second, "warm worker exited and cleared RunningSubagents", func() bool {
		return !srv.headless.HasWarmWorker(sess.ID) && len(srv.getRunningSubagents(sess.ID, nativeID, "claude-code", tmpDir)) == 0
	})
	if cnt := fetchSubagentCountViaHTTP(t, srv, sess.ID); cnt != 0 {
		t.Fatalf("expected warm worker exit to clear running_count to 0, got %d", cnt)
	}
}

func TestBgTasks_WatchWarmWorkerColdStartDialRetry(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	defer runner.ShutdownAllWorkers(time.Second)

	sockPath := filepath.Join(t.TempDir(), "delayed.sock")
	workerID := "w-cold-retry"
	sessID := "claude-code:local:cold-retry"
	wh := &warmWorkerHandle{
		WorkerID:   workerID,
		SessionID:  sessID,
		NativeID:   "cold-retry",
		SocketPath: sockPath,
		WorkerPID:  os.Getpid(),
	}
	runner.mu.Lock()
	runner.warmWorkers[sessID] = wh
	runner.mu.Unlock()

	lnReady := make(chan struct{})
	doneServe := make(chan struct{})
	go func() {
		defer close(doneServe)
		time.Sleep(160 * time.Millisecond)
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Errorf("listen delayed sock: %v", err)
			return
		}
		defer ln.Close()
		close(lnReady)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var req workerIPCReq
		_ = json.Unmarshal(line, &req)
		frame, _ := json.Marshal(workerIPCResp{OK: true, State: "idle_warm", BgTaskCount: 2})
		_, _ = conn.Write(append(frame, '\n'))
		time.Sleep(250 * time.Millisecond)
	}()

	runner.ensureWarmWatcher(wh)
	<-lnReady
	waitForWorkerCond(t, 3*time.Second, "watchWarmWorker attached after 160ms socket delay", func() bool {
		return runner.GetBgTaskCount(sessID) == 2 && runner.HasWarmWorker(sessID)
	})
	runner.stopAllWarmWatchers()
	<-doneServe
}

func TestBgTasks_TranscriptMergeAfterRecentTurnAndBusyRace(t *testing.T) {
	t.Run("synthetic merge does not collide with Turn 1 within 2s", func(t *testing.T) {
		tmpDir := t.TempDir()
		runner := NewHeadlessRunner(nil, nil)
		srv := &Server{headless: runner}

		now := time.Now().UTC()
		turnID := "turn_bg_recent"
		metaPath := filepath.Join(tmpDir, turnID+".meta.json")
		spoolPath := filepath.Join(tmpDir, turnID+".ndjson")
		w, err := OpenTurnSpoolWriter(spoolPath, turnID)
		if err != nil {
			t.Fatalf("OpenTurnSpoolWriter: %v", err)
		}
		_, _ = w.Append(ChatStreamEvent{SessionID: "s-merge", TurnID: turnID, Type: "turn_start", Timestamp: now})
		_, _ = w.Append(ChatStreamEvent{SessionID: "s-merge", TurnID: turnID, Type: "text_delta", Text: "Background summary ready", Timestamp: now.Add(20 * time.Millisecond)})
		_ = w.Close()

		meta := &TurnSpoolMeta{
			TurnID:      turnID,
			SessionID:   "s-merge",
			Agent:       "claude-code",
			SpoolPath:   spoolPath,
			MetaPath:    metaPath,
			StartedAt:   now,
			Status:      "running",
			IsSynthetic: true,
		}
		_ = WriteTurnSpoolMeta(metaPath, meta)
		runner.mu.Lock()
		runner.sessionSpools["s-merge"] = meta
		runner.mu.Unlock()

		base := &Transcript{
			SessionID: "s-merge",
			Agent:     "claude-code",
			Messages: []TranscriptMessage{
				{Role: "user", Content: "Turn 1 prompt", Timestamp: now.Add(-1200 * time.Millisecond)},
				{Role: "assistant", Content: "Turn 1 longer assistant message here", Timestamp: now.Add(-600 * time.Millisecond)},
			},
		}
		mergedLive, inFlight := srv.mergeInFlightSpoolIntoTranscript(&Transcript{SessionID: base.SessionID, Agent: base.Agent, Messages: append([]TranscriptMessage(nil), base.Messages...)}, &Session{ID: "s-merge", Agent: "claude-code", EngineType: EngineHeadless}, "s-merge", "", "claude-code", tmpDir)
		if inFlight == nil || len(mergedLive.Messages) != 3 || mergedLive.Messages[2].Content != "Background summary ready" || mergedLive.Messages[1].Content != "Turn 1 longer assistant message here" {
			t.Fatalf("expected live synthetic merge to append 3rd message cleanly, got %+v", mergedLive.Messages)
		}

		meta.Status = "completed"
		meta.FinishedAt = now.Add(40 * time.Millisecond)
		_ = WriteTurnSpoolMeta(metaPath, meta)
		mergedDone, _ := srv.mergeInFlightSpoolIntoTranscript(&Transcript{SessionID: base.SessionID, Agent: base.Agent, Messages: append([]TranscriptMessage(nil), base.Messages...)}, &Session{ID: "s-merge", Agent: "claude-code", EngineType: EngineHeadless}, "s-merge", "", "claude-code", tmpDir)
		if len(mergedDone.Messages) != 3 || mergedDone.Messages[2].Content != "Background summary ready" {
			t.Fatalf("expected finished synthetic merge before JSONL flush not to be suppressed by Turn 1, got %+v", mergedDone.Messages)
		}

		subMsStart := now.Truncate(time.Millisecond).Add(900 * time.Microsecond)
		meta.StartedAt = subMsStart
		_ = WriteTurnSpoolMeta(metaPath, meta)
		flushed := &Transcript{
			SessionID: "s-merge",
			Agent:     "claude-code",
			Messages: []TranscriptMessage{
				{Role: "user", Content: "Turn 1 prompt", Timestamp: subMsStart.Add(-1200 * time.Millisecond)},
				{Role: "assistant", Content: "Background summary ready", Timestamp: subMsStart.Truncate(time.Millisecond)},
			},
		}
		mergedFlushed, _ := srv.mergeInFlightSpoolIntoTranscript(flushed, &Session{ID: "s-merge", Agent: "claude-code", EngineType: EngineHeadless}, "s-merge", "", "claude-code", tmpDir)
		if len(mergedFlushed.Messages) != 2 {
			t.Fatalf("expected millisecond-truncated JSONL flush to deduplicate completed synthetic turn, got %+v", mergedFlushed.Messages)
		}
	})

	t.Run("RunTurn busy race returns ErrTurnInProgress and preserves warm worker", func(t *testing.T) {
		tmpDir := t.TempDir()
		gate1 := filepath.Join(tmpDir, "busy1.gate")
		gate2 := filepath.Join(tmpDir, "busy2.gate")
		installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Turn 1"}}'
echo '{"type":"result","result":"Turn 1","is_error":false}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Busy bg running"}}'
while [ ! -f %q ]; do
  sleep 0.02
done
echo '{"type":"result","result":"Busy bg running","is_error":false}'
read -r hold_line
`, gate1, gate2))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:busy-race", "busy-race", tmpDir, StateIdle)
		_ = db.SaveSession(sess)

		if err := srv.headless.RunTurn(context.Background(), sess, "Turn 1"); err != nil {
			t.Fatalf("RunTurn 1: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "Turn 1 idle_warm", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && srv.headless.HasWarmWorker(sess.ID)
		})

		srv.headless.stopAllWarmWatchers()
		srv.headless.mu.RLock()
		wh := srv.headless.warmWorkers[sess.ID]
		srv.headless.mu.RUnlock()
		origWorkerID := wh.WorkerID

		_ = os.WriteFile(gate1, []byte("go"), 0600)
		waitForWorkerCond(t, 5*time.Second, "worker entered running_turn for turn_bg_*", func() bool {
			resp, err := callWorkerSocket(wh.SocketPath, workerIPCReq{Op: "ping"}, time.Second)
			return err == nil && resp != nil && resp.State == "running_turn" && strings.HasPrefix(resp.TurnID, "turn_bg_")
		})

		err := srv.headless.RunTurn(context.Background(), sess, "Concurrent prompt")
		if !errors.Is(err, ErrTurnInProgress) {
			t.Fatalf("expected ErrTurnInProgress on busy warm worker, got %v", err)
		}
		srv.headless.mu.RLock()
		curWH := srv.headless.warmWorkers[sess.ID]
		srv.headless.mu.RUnlock()
		if curWH == nil || curWH.WorkerID != origWorkerID {
			t.Fatalf("expected original warm worker %s preserved on busy race, got %+v", origWorkerID, curWH)
		}

		_ = os.WriteFile(gate2, []byte("done"), 0600)
		waitForWorkerCond(t, 5*time.Second, "observed bg turn finalized cleanly", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})
	})
}
