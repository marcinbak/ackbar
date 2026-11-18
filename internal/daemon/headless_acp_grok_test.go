package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installMockACPGrok(t *testing.T, script string) string {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "grok"), []byte(script), 0755); err != nil {
		t.Fatalf("write mock grok: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return binDir
}

func runACPTurnAndWait(t *testing.T, hr *HeadlessRunner, sess *Session, prompt string) []ChatStreamEvent {
	t.Helper()
	origID := sess.ID
	sub, unsub := hr.Subscribe(origID)
	defer unsub()
	if err := hr.RunTurn(context.Background(), sess, prompt); err != nil {
		t.Fatalf("RunTurn(%q): %v", prompt, err)
	}
	var evts []ChatStreamEvent
	deadline := time.After(6 * time.Second)
	for {
		select {
		case ev := <-sub:
			evts = append(evts, ev)
			if ev.Type == "turn_complete" || ev.Type == "error" || ev.Type == "turn_cancelled" {
				waitEnd := time.Now().Add(2 * time.Second)
				for time.Now().Before(waitEnd) && (hr.IsRunning(origID) || hr.IsRunning(sess.ID)) {
					time.Sleep(10 * time.Millisecond)
				}
				return evts
			}
		case <-deadline:
			t.Fatalf("timed out waiting for turn %q", prompt)
		}
	}
}

func TestGrokACP_WarmContinuityAndQueueBgHold(t *testing.T) {
	tmpDir := t.TempDir()
	callsLog := filepath.Join(tmpDir, "calls.log")
	turnState := filepath.Join(tmpDir, "turn.num")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    printf '%%s\n' "$line" >> %q
    id=$(printf '%%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
    [ -z "$id" ] && continue
    case "$line" in
      *'"method":"initialize"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cached_token","name":"Cached"}]}}\n' "$id" ;;
      *'"method":"authenticate"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{}}\n' "$id" ;;
      *'"method":"session/new"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"sessionId":"grok-bg-1"}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        if [ ! -f %q ]; then
          : > %q
          printf '{"jsonrpc":"2.0","method":"_x.ai/queue/changed","params":{"tasks":[{"task_id":"bg-1","status":"running"}]}}\n'
          printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"bg-held"}}}}\n'
        else
          printf '{"jsonrpc":"2.0","method":"_x.ai/queue/changed","params":{"tasks":[]}}\n'
          printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"bg-cleared"}}}}\n'
        fi
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
      *)
        printf '{"jsonrpc":"2.0","id":%%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id" ;;
    esac
  done
fi
`, callsLog, turnState, turnState)
	installMockACPGrok(t, script)

	srv, db := setupTestServer(t)
	srv.headless.SetIdleTTL(180 * time.Millisecond)
	cwd := filepath.Join(tmpDir, "proj")
	_ = os.MkdirAll(cwd, 0755)
	sess := &Session{ID: "grok:local:grok-bg-1", Host: "local", Agent: "grok", NativeID: "grok-bg-1", Cwd: cwd, EngineType: EngineHeadless}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	runACPTurnAndWait(t, srv.headless, sess, "turn 1")
	time.Sleep(320 * time.Millisecond)
	if !srv.headless.HasWarmWorker(sess.ID) {
		t.Fatalf("expected warm worker held past 180ms idleTTL while bg tasks > 0")
	}
	srv.headless.mu.RLock()
	h1 := srv.headless.warmWorkers[sess.ID]
	wp1, cp1 := h1.WorkerPID, h1.ChildPID
	srv.headless.mu.RUnlock()

	runACPTurnAndWait(t, srv.headless, sess, "turn 2")
	srv.headless.mu.RLock()
	h2 := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if h2 == nil || h2.WorkerPID != wp1 || h2.ChildPID != cp1 {
		t.Fatalf("expected same WorkerPID/ChildPID after bg hold, got %+v vs (%d,%d)", h2, wp1, cp1)
	}
}

func TestGrokACP_LazyUUIDAdoptionAndColdLoad(t *testing.T) {
	tmpDir := t.TempDir()
	callsLog := filepath.Join(tmpDir, "calls.log")
	adoptedUUID := "019a0000-0000-7000-8000-000000000042"
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    printf '%%s\n' "$line" >> %q
    id=$(printf '%%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
    [ -z "$id" ] && continue
    case "$line" in
      *'"method":"initialize"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"protocolVersion":1}}\n' "$id" ;;
      *'"method":"session/new"'*|*'"method":"session/load"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"sessionId":%q}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"adopted-ok"}}}}\n'
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
      *)
        printf '{"jsonrpc":"2.0","id":%%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id" ;;
    esac
  done
fi
`, callsLog, adoptedUUID)
	installMockACPGrok(t, script)

	srv, db := setupTestServer(t)
	cwd := filepath.Join(tmpDir, "proj")
	_ = os.MkdirAll(cwd, 0755)
	oldID := "grok:local:temp-init-1"
	sess := &Session{ID: oldID, Host: "local", Agent: "grok", NativeID: "temp-init-1", Cwd: cwd, EngineType: EngineHeadless}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	runACPTurnAndWait(t, srv.headless, sess, "init turn")
	newID := "grok:local:" + adoptedUUID
	migrated, err := db.GetSession(newID)
	if err != nil || migrated == nil || migrated.OldID != oldID {
		t.Fatalf("expected migrated session %s with OldID=%s, got %+v err=%v", newID, oldID, migrated, err)
	}
	if !srv.headless.HasWarmWorker(newID) {
		t.Fatalf("expected warm worker migrated to %s", newID)
	}

	srv.headless.ShutdownSessionWorker(newID, adoptedUUID, time.Second)
	runACPTurnAndWait(t, srv.headless, migrated, "cold resume turn")

	callsBytes, _ := os.ReadFile(callsLog)
	if !strings.Contains(string(callsBytes), `"method":"session/load"`) || !strings.Contains(string(callsBytes), adoptedUUID) {
		t.Fatalf("expected cold resume via session/load for %s, got:\n%s", adoptedUUID, string(callsBytes))
	}
}

func TestGrokACP_CatalogsLiveConfigAndYoloRestart(t *testing.T) {
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
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"sessionId":"grok-cfg-1","models":{"availableModels":[{"modelId":"grok-4.20-live","name":"Grok 4.20 Live","supportsReasoningEffort":true}],"currentModelId":"grok-4.20-live"}}}\n' "$id"
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"compact","description":"Compact"},{"name":"goal","description":"Goal"},{"name":"workflow","description":"Workflow"}]}}}\n' ;;
      *'"method":"session/set_model"'*|*'"method":"session/set_mode"'*|*'"method":"session/set_config_option"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"cfg-ok"}}}}\n'
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
	sess := &Session{ID: "grok:local:grok-cfg-1", Host: "local", Agent: "grok", NativeID: "grok-cfg-1", Cwd: cwd, EngineType: EngineHeadless}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	runACPTurnAndWait(t, srv.headless, sess, "boot catalogs")
	getSuggestions := func(kind string) string {
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/suggestions?kind="+kind, nil)
		w := httptest.NewRecorder()
		srv.Mux().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET suggestions?kind=%s code=%d body=%s", kind, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if modelsJSON := getSuggestions("models"); !strings.Contains(modelsJSON, "grok-4.20-live") {
		t.Fatalf("expected live model grok-4.20-live in suggestions, got: %s", modelsJSON)
	}
	cmdsJSON := getSuggestions("commands")
	for _, want := range []string{"/compact", "/goal", "/workflow"} {
		if !strings.Contains(cmdsJSON, want) {
			t.Fatalf("expected %s in commands suggestions, got: %s", want, cmdsJSON)
		}
	}

	runACPTurnAndWait(t, srv.headless, sess, "/compact")
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
	patchCfg(map[string]any{"model": "grok-4.20-live", "effort": "high", "permission_mode": "plan"})
	srv.headless.mu.RLock()
	hLive := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if hLive == nil || hLive.WorkerPID != wp1 || hLive.ChildPID != cp1 {
		t.Fatalf("expected live model/effort/mode update to preserve ChildPID=%d WorkerPID=%d, got %+v", cp1, wp1, hLive)
	}

	patchCfg(map[string]any{"yolo_mode": true})
	sess, _ = db.GetSession(sess.ID)
	runACPTurnAndWait(t, srv.headless, sess, "after yolo")
	srv.headless.mu.RLock()
	hYolo := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if hYolo == nil || hYolo.WorkerPID != wp1 || hYolo.ChildPID == cp1 || hYolo.ChildPID <= 1 {
		t.Fatalf("expected yolo_mode toggle to restart child in same worker %d (old child %d), got %+v", wp1, cp1, hYolo)
	}

	callsBytes, _ := os.ReadFile(callsLog)
	for _, sub := range []string{`"/compact"`, `"method":"session/set_model"`, `"method":"session/set_mode"`} {
		if !strings.Contains(string(callsBytes), sub) {
			t.Errorf("expected %s in calls log, got:\n%s", sub, string(callsBytes))
		}
	}
	argsBytes, _ := os.ReadFile(argsLog)
	if !strings.Contains(string(argsBytes), "--always-approve") {
		t.Errorf("expected --always-approve after yolo_mode restart, got:\n%s", string(argsBytes))
	}
}
