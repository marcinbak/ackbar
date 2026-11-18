package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
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

func TestGrokACP_SupervisorPromptClearModelAndYoloLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpDir := t.TempDir()
	argsLog := filepath.Join(tmpDir, "args.log")
	callsLog := filepath.Join(tmpDir, "calls.log")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    printf '%%s\n' "$line" >> %q
    id=$(printf '%%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
    [ -z "$id" ] && continue
    case "$line" in
      *'"method":"initialize"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"protocolVersion":1}}\n' "$id" ;;
      *'"method":"session/new"'*|*'"method":"session/load"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"sessionId":"grok-hard-1"}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ok"}}}}\n'
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
      *)
        printf '{"jsonrpc":"2.0","id":%%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id" ;;
    esac
  done
fi
`, argsLog, callsLog)
	installMockACPGrok(t, script)

	srv, db := setupTestServer(t)
	cwd := filepath.Join(tmpDir, "proj")
	_ = os.MkdirAll(cwd, 0755)
	sess := &Session{
		ID: "grok:local:grok-hard-1", Host: "local", Agent: "grok", NativeID: "grok-hard-1",
		Cwd: cwd, EngineType: EngineHeadless, IsSupervisor: true, Model: "grok-4.7", Effort: "high",
	}
	_ = db.SaveSession(sess)

	runACPTurnAndWait(t, srv.headless, sess, "turn 1")
	calls1, _ := os.ReadFile(callsLog)
	if !strings.Contains(string(calls1), "[System Instruction:") {
		t.Fatalf("expected Supervisor SystemPrompt in Turn 1 prompt, got:\n%s", string(calls1))
	}
	srv.headless.mu.RLock()
	h1 := srv.headless.warmWorkers[sess.ID]
	wp1, cp1 := h1.WorkerPID, h1.ChildPID
	srv.headless.mu.RUnlock()

	patchCfg := func(body any) {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/config", bytes.NewReader(b))
		w := httptest.NewRecorder()
		srv.Mux().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST config %v code=%d body=%s", body, w.Code, w.Body.String())
		}
	}
	patchCfg(map[string]any{"model": "default", "effort": "default"})
	sess, _ = db.GetSession(sess.ID)
	runACPTurnAndWait(t, srv.headless, sess, "turn 2")
	srv.headless.mu.RLock()
	h2 := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if h2 == nil || h2.WorkerPID != wp1 || h2.ChildPID == cp1 {
		t.Fatalf("expected clearing model/effort to restart child in same worker %d (old child %d), got %+v", wp1, cp1, h2)
	}

	patchCfg(map[string]any{"yolo_mode": true})
	sess, _ = db.GetSession(sess.ID)
	runACPTurnAndWait(t, srv.headless, sess, "turn 3")

	argLines := strings.Split(strings.TrimSpace(mustReadFile(t, argsLog)), "\n")
	if len(argLines) < 3 {
		t.Fatalf("expected 3 child spawns, got %d:\n%s", len(argLines), strings.Join(argLines, "\n"))
	}
	if strings.Contains(argLines[1], "-m ") || strings.Contains(argLines[1], "--effort") {
		t.Errorf("expected cleared -m/--effort in second spawn args, got %q", argLines[1])
	}
	if !strings.Contains(argLines[2], "--always-approve") {
		t.Errorf("expected --always-approve in third spawn args, got %q", argLines[2])
	}
	callsAll := mustReadFile(t, callsLog)
	if strings.Count(callsAll, `"method":"session/load"`) < 2 {
		t.Errorf("expected session/load after restart on Turn 2 and Turn 3, got:\n%s", callsAll)
	}
}

func TestGrokACP_ReplayIsolationStderrDrainAndCollision(t *testing.T) {
	t.Run("session/load replay ignored before session/prompt", func(t *testing.T) {
		tmpDir := t.TempDir()
		script := `#!/bin/sh
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    id=$(printf '%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
    [ -z "$id" ] && continue
    case "$line" in
      *'"method":"initialize"'*)
        printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1}}\n' "$id" ;;
      *'"method":"session/load"'*)
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"HISTORICAL_REPLAY"}}}}\n'
        printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"grok-rep-1"}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"FRESH_TURN"}}}}\n'
        printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
    esac
  done
fi
`
		installMockACPGrok(t, script)
		srv, db := setupTestServer(t)
		cwd := filepath.Join(tmpDir, "proj")
		_ = os.MkdirAll(cwd, 0755)
		sess := &Session{ID: "grok:local:grok-rep-1", Host: "local", Agent: "grok", NativeID: "grok-rep-1", Cwd: cwd, EngineType: EngineHeadless, LastPrompt: "prev"}
		_ = db.SaveSession(sess)

		evts := runACPTurnAndWait(t, srv.headless, sess, "new prompt")
		var combined strings.Builder
		for _, ev := range evts {
			combined.WriteString(ev.Text)
		}
		if !strings.Contains(combined.String(), "FRESH_TURN") || strings.Contains(combined.String(), "HISTORICAL_REPLAY") {
			t.Fatalf("expected FRESH_TURN only without HISTORICAL_REPLAY, got %q", combined.String())
		}
	})

	t.Run("invalid and colliding sessionId rejected", func(t *testing.T) {
		tmpDir := t.TempDir()
		script := `#!/bin/sh
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    id=$(printf '%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
    [ -z "$id" ] && continue
    case "$line" in
      *'"method":"initialize"'*)
        printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1}}\n' "$id" ;;
      *'"method":"session/new"'*)
        printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"../bad"}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
    esac
  done
fi
`
		installMockACPGrok(t, script)
		srv, db := setupTestServer(t)
		cwd := filepath.Join(tmpDir, "proj")
		_ = os.MkdirAll(cwd, 0755)
		sess := &Session{ID: "grok:local:safe-1", Host: "local", Agent: "grok", NativeID: "safe-1", Cwd: cwd, EngineType: EngineHeadless}
		_ = db.SaveSession(sess)
		runACPTurnAndWait(t, srv.headless, sess, "hi")
		if bad, _ := db.GetSession("grok:local:../bad"); bad != nil {
			t.Fatalf("unexpected invalid migrated session: %+v", bad)
		}
		if cur, _ := db.GetSession("grok:local:safe-1"); cur == nil || cur.NativeID != "safe-1" {
			t.Fatalf("expected original safe-1 preserved, got %+v", cur)
		}

		existing := &Session{ID: "grok:local:col-uuid", Host: "local", Agent: "grok", NativeID: "col-uuid", Cwd: cwd, FirstPrompt: "keep me", EngineType: EngineHeadless}
		_ = db.SaveSession(existing)
		tempSess := &Session{ID: "grok:local:temp-c1", Host: "local", Agent: "grok", NativeID: "temp-c1", Cwd: cwd, EngineType: EngineHeadless}
		srv.headless.handleAntigravityInit(tempSess, "col-uuid", "overwrite?")
		if tempSess.ID != "grok:local:temp-c1" {
			t.Fatalf("expected colliding ID migration refused, got %s", tempSess.ID)
		}
		if kept, _ := db.GetSession("grok:local:col-uuid"); kept == nil || kept.FirstPrompt != "keep me" {
			t.Fatalf("existing colliding session was clobbered: %+v", kept)
		}
	})

	t.Run("stderr drained on crash before surfacing error", func(t *testing.T) {
		tmpDir := t.TempDir()
		script := `#!/bin/sh
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    id=$(printf '%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
    [ -z "$id" ] && continue
    case "$line" in
      *'"method":"initialize"'*)
        printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1}}\n' "$id" ;;
      *'"method":"session/new"'*)
        printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"grok-err-1"}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        printf 'fatal: invalid api key\n' >&2
        exit 1 ;;
    esac
  done
fi
`
		installMockACPGrok(t, script)
		srv, db := setupTestServer(t)
		cwd := filepath.Join(tmpDir, "proj")
		_ = os.MkdirAll(cwd, 0755)
		sess := &Session{ID: "grok:local:grok-err-1", Host: "local", Agent: "grok", NativeID: "grok-err-1", Cwd: cwd, EngineType: EngineHeadless}
		_ = db.SaveSession(sess)

		evts := runACPTurnAndWait(t, srv.headless, sess, "crash me")
		var errText string
		for _, ev := range evts {
			if ev.Type == "error" {
				errText += ev.Text
			}
		}
		if !strings.Contains(errText, "fatal: invalid api key") {
			t.Fatalf("expected drained stderr in error event, got %q (events=%+v)", errText, evts)
		}
	})
}

func TestGrokACP_ControlValidationScannerRewindAndAdopt(t *testing.T) {
	if resolveRespondApproval(&Blocked{Kind: BlockPermission}, "answer", "do not always allow") {
		t.Fatalf("negated answer text must not approve BlockPermission")
	}
	if !resolveRespondApproval(&Blocked{Kind: BlockPermission}, "answer", "Always Allow") {
		t.Fatalf("exact Always Allow must approve BlockPermission")
	}

	srv, db := setupTestServer(t)
	if !isIgnoredAgentCommandWithRegistry(srv.PluginRegistry(), "grok", "grok agent stdio --no-auto-update") {
		t.Errorf("expected grok agent stdio ignored via PluginRegistry")
	}
	if isIgnoredAgentCommandWithRegistry(srv.PluginRegistry(), "grok", "grok") {
		t.Errorf("expected bare interactive grok not ignored")
	}

	claudeSess := &Session{ID: "claude-code:local:rew-c1", Host: "local", Agent: "claude-code", NativeID: "rew-c1", Cwd: t.TempDir(), EngineType: EngineHeadless}
	_ = db.SaveSession(claudeSess)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+claudeSess.ID+"/rewind", strings.NewReader(`{"cancel":true}`))
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "cancel_rewind") {
		t.Fatalf("expected non-ACP cancel rewind rejected with 400, got %d: %s", w.Code, w.Body.String())
	}

	sockPath := filepath.Join(t.TempDir(), "adopt.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, aErr := ln.Accept()
			if aErr != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadBytes('\n')
			var req workerIPCReq
			_ = json.Unmarshal(line, &req)
			b, _ := json.Marshal(workerIPCResp{OK: true, ChildPID: 123, State: "idle_warm"})
			_, _ = c.Write(append(b, '\n'))
			_ = c.Close()
			if req.Op == "shutdown" {
				return
			}
		}
	}()

	adoptDBPath := filepath.Join(t.TempDir(), "ackbard.db")
	db2, _ := InitDB(adoptDBPath)
	defer db2.Close()
	adoptSess := &Session{ID: "grok:local:ad-1", Host: "local", Agent: "grok", NativeID: "ad-1", Cwd: t.TempDir(), EngineType: EngineHeadless}
	_ = db2.SaveSession(adoptSess)
	runDir := DefaultHeadlessRunDir(adoptDBPath)
	_ = os.MkdirAll(runDir, 0700)
	_ = WriteSessionWorkerMeta(filepath.Join(runDir, "w1.worker.json"), &SessionWorkerMeta{
		WorkerID: "w1", SessionID: adoptSess.ID, NativeID: adoptSess.NativeID, Agent: "grok",
		ConfigFingerprint: ConfigFingerprint(adoptSess), ImmutableFingerprint: "imm-abc", SocketPath: sockPath, State: "idle_warm",
	})
	hr2 := NewHeadlessRunner(db2, nil)
	defer hr2.ShutdownAllWorkers(time.Second)
	hr2.ReattachLiveWorkers()
	hr2.mu.RLock()
	wh := hr2.warmWorkers[adoptSess.ID]
	hr2.mu.RUnlock()
	if wh == nil || wh.ImmutableFingerprint != "imm-abc" {
		t.Fatalf("expected ImmutableFingerprint preserved on adoptLiveWorkers, got %+v", wh)
	}
}
