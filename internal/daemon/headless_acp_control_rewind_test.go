package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agents/grok"
)

func waitForBlockedSession(t *testing.T, db *DB, sessID string, wantKind BlockKind) *Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := db.GetSession(sessID)
		if err == nil && s != nil && s.State == StateBlocked && s.Blocked != nil && s.Blocked.Kind == wantKind {
			return s
		}
		time.Sleep(15 * time.Millisecond)
	}
	s, _ := db.GetSession(sessID)
	t.Fatalf("timed out waiting for session %s blocked=%s (got %+v)", sessID, wantKind, s)
	return nil
}

func postRespondJSON(t *testing.T, srv *Server, body map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/respond", bytes.NewReader(b))
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /v1/sessions/respond status=%d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestGrokACP_ControlPromptsAndUnknownNonDeadlock(t *testing.T) {
	tmpDir := t.TempDir()
	callsLog := filepath.Join(tmpDir, "calls.log")
	respLog := filepath.Join(tmpDir, "srv_resp.log")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    printf '%%s\n' "$line" >> %q
    id=$(printf '%%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
    [ -z "$id" ] && continue
    case "$line" in
      *'"method":"initialize"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"protocolVersion":1}}\n' "$id" ;;
      *'"method":"session/new"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"sessionId":"grok-ctrl-1"}}\n' "$id" ;;
      *'"method":"session/set_mode"'*|*'"method":"session/set_config_option"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        case "$line" in
          *'PERM_ALLOW'*)
            printf '{"jsonrpc":"2.0","id":77,"method":"_x.ai/unknown_ext","params":{}}\n'
            IFS= read -r unk_reply && printf '%%s\n' "$unk_reply" >> %q
            printf '{"jsonrpc":"2.0","id":88,"method":"session/request_permission","params":{"toolCall":{"title":"Bash","rawInput":{"command":"ls"}},"options":[{"optionId":"opt_once","kind":"allow_once","name":"Allow Once"},{"optionId":"opt_always","kind":"allow_always","name":"Always Allow"},{"optionId":"opt_deny","kind":"reject_once","name":"Reject"}]}}\n'
            IFS= read -r perm_reply && printf '%%s\n' "$perm_reply" >> %q ;;
          *'EXIT_PLAN'*)
            printf '{"jsonrpc":"2.0","id":"plan-1","method":"_x.ai/exit_plan_mode","params":{"planContent":"Step 1: edit"}}\n'
            IFS= read -r plan_reply && printf '%%s\n' "$plan_reply" >> %q ;;
          *'ASK_Q'*)
            printf '{"jsonrpc":"2.0","id":99,"method":"_x.ai/ask_user_question","params":{"questions":[{"id":"q1","question":"Which DB?","options":[{"label":"SQLite"},{"label":"Postgres"}]}]}}\n'
            IFS= read -r q_reply && printf '%%s\n' "$q_reply" >> %q ;;
          *'PERM_DENY'*)
            printf '{"jsonrpc":"2.0","id":101,"method":"session/request_permission","params":{"toolCall":{"title":"Bash"},"options":[{"optionId":"opt_once","kind":"allow_once","name":"Allow"},{"optionId":"opt_deny","kind":"reject_once","name":"Reject"}]}}\n'
            IFS= read -r deny_reply && printf '%%s\n' "$deny_reply" >> %q ;;
        esac
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ctrl-done"}}}}\n'
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
      *)
        printf '{"jsonrpc":"2.0","id":%%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id" ;;
    esac
  done
fi
`, callsLog, respLog, respLog, respLog, respLog, respLog)
	installMockACPGrok(t, script)

	srv, db := setupTestServer(t)
	cwd := filepath.Join(tmpDir, "proj")
	_ = os.MkdirAll(cwd, 0755)
	sess := &Session{ID: "grok:local:grok-ctrl-1", Host: "local", Agent: "grok", NativeID: "grok-ctrl-1", Cwd: cwd, EngineType: EngineHeadless}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	stepTurn := func(prompt string, kind BlockKind, action, value string) {
		sub, unsub := srv.headless.Subscribe(sess.ID)
		defer unsub()
		if err := srv.headless.RunTurn(t.Context(), sess, prompt); err != nil {
			t.Fatalf("RunTurn(%q): %v", prompt, err)
		}
		waitForBlockedSession(t, db, sess.ID, kind)
		postRespondJSON(t, srv, map[string]any{"id": sess.ID, "action": action, "value": value})
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-sub:
				if ev.Type == "turn_complete" || ev.Type == "error" {
					end := time.Now().Add(2 * time.Second)
					for time.Now().Before(end) && srv.headless.IsRunning(sess.ID) {
						time.Sleep(10 * time.Millisecond)
					}
					return
				}
			case <-deadline:
				t.Fatalf("timed out finishing %q", prompt)
			}
		}
	}

	stepTurn("PERM_ALLOW", BlockPermission, "allow", "Always Allow")
	sess.PermissionMode = "plan"
	sess.MetadataUpdatedAt = time.Now().UTC()
	_ = db.SaveSession(sess)
	srv.headless.NotifyConfigChanged(sess)

	stepTurn("EXIT_PLAN", BlockApproval, "allow", "Approve Plan")
	if reloaded, _ := db.GetSession(sess.ID); reloaded == nil || reloaded.PermissionMode != "" {
		t.Fatalf("expected plan approval to reset PermissionMode to empty default for grok, got %+v", reloaded)
	} else {
		sess = reloaded
	}
	stepTurn("ASK_Q", BlockQuestion, "answer", "SQLite")
	stepTurn("PERM_DENY", BlockPermission, "deny", "")

	respBytes, _ := os.ReadFile(respLog)
	respStr := string(respBytes)
	for _, want := range []string{`"id":77`, `-32601`, `"optionId":"opt_always"`, `"id":"plan-1"`, `"outcome":"approved"`, `"id":99`, `SQLite`, `"optionId":"opt_deny"`} {
		if !strings.Contains(respStr, want) {
			t.Errorf("expected %s in server responses, got:\n%s", want, respStr)
		}
	}
	callsBytes, _ := os.ReadFile(callsLog)
	if !strings.Contains(string(callsBytes), `"modeId":"auto"`) {
		t.Errorf("expected post-plan session/set_mode auto call, got:\n%s", string(callsBytes))
	}
}

func TestGrokACP_CancelWarmAndRewindNativeVsCold(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tmpDir := t.TempDir()
	callsLog := filepath.Join(tmpDir, "calls.log")
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
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"sessionId":"grok-rew-1"}}\n' "$id" ;;
      *'"method":"_x.ai/rewind/points"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"points":[{"id":"pt-1","turn":1,"preview":"first"},{"id":"pt-2","turn":2,"preview":"second"}]}}\n' "$id" ;;
      *'"method":"_x.ai/rewind/execute"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"rewoundTo":"pt-2"}}\n' "$id" ;;
      *'"method":"session/prompt"'*)
        case "$line" in
          *'WAIT_CANCEL'*)
            printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"working..."}}}}\n'
            while IFS= read -r cLine; do
              printf '%%s\n' "$cLine" >> %q
              case "$cLine" in
                *'"method":"session/cancel"'*)
                  printf '{"jsonrpc":"2.0","id":%%s,"result":{"stopReason":"cancelled"}}\n' "$id"
                  break ;;
              esac
            done ;;
          *)
            printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"resumed-ok"}}}}\n'
            printf '{"jsonrpc":"2.0","id":%%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
        esac ;;
      *)
        printf '{"jsonrpc":"2.0","id":%%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id" ;;
    esac
  done
fi
`, callsLog, callsLog)
	installMockACPGrok(t, script)

	srv, db := setupTestServer(t)
	cwd := filepath.Join(tmpDir, "proj")
	_ = os.MkdirAll(cwd, 0755)
	sess := &Session{ID: "grok:local:grok-rew-1", Host: "local", Agent: "grok", NativeID: "grok-rew-1", Cwd: cwd, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	sub1, unsub1 := srv.headless.Subscribe(sess.ID)
	defer unsub1()
	if err := srv.headless.RunTurn(t.Context(), sess, "WAIT_CANCEL"); err != nil {
		t.Fatalf("RunTurn WAIT_CANCEL: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for gotChunk := false; !gotChunk; {
		select {
		case ev := <-sub1:
			if ev.Type == "text_delta" {
				gotChunk = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for chunk before cancel")
		}
	}
	if err := srv.headless.CancelTurn(sess.ID); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) && srv.headless.IsRunning(sess.ID) {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.headless.HasWarmWorker(sess.ID) {
		t.Fatalf("expected warm worker retained after graceful ACP session/cancel")
	}
	srv.headless.mu.RLock()
	h1 := srv.headless.warmWorkers[sess.ID]
	wp1, cp1 := h1.WorkerPID, h1.ChildPID
	srv.headless.mu.RUnlock()

	runACPTurnAndWait(t, srv.headless, sess, "AFTER_CANCEL")
	srv.headless.mu.RLock()
	h2 := srv.headless.warmWorkers[sess.ID]
	srv.headless.mu.RUnlock()
	if h2 == nil || h2.WorkerPID != wp1 || h2.ChildPID != cp1 {
		t.Fatalf("expected same WorkerPID/ChildPID after cancel, got %+v vs (%d,%d)", h2, wp1, cp1)
	}

	getRew := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/rewind", nil)
	getW := httptest.NewRecorder()
	srv.Mux().ServeHTTP(getW, getRew)
	if getW.Code != http.StatusOK || !strings.Contains(getW.Body.String(), `"acp_native":true`) || !strings.Contains(getW.Body.String(), `"pt-2"`) {
		t.Fatalf("expected warm native rewind points, got %d: %s", getW.Code, getW.Body.String())
	}

	postRew := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/rewind", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.Mux().ServeHTTP(w, r)
		return w
	}
	if w := postRew(`{"before_turn":2}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mode":"acp_native"`) {
		t.Fatalf("expected acp_native rewind execute, got %d: %s", w.Code, w.Body.String())
	}
	if w := postRew(`{"cancel":true}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mode":"cancel_rewind"`) {
		t.Fatalf("expected cancel_rewind ok, got %d: %s", w.Code, w.Body.String())
	}

	srv.headless.ShutdownSessionWorker(sess.ID, sess.NativeID, time.Second)
	sessDir := filepath.Join(grok.HomeDir(home), "sessions", grok.EncodeCwd(cwd), sess.NativeID)
	_ = os.MkdirAll(sessDir, 0755)
	_ = os.WriteFile(filepath.Join(sessDir, "chat_history.jsonl"), []byte("{\"role\":\"user\",\"content\":\"first\",\"timestamp\":\"2026-11-18T10:00:00Z\"}\n{\"role\":\"user\",\"content\":\"second\",\"timestamp\":\"2026-11-18T10:01:00Z\"}\n"), 0600)
	if w := postRew(`{"before_turn":2}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mode":"seeded_history"`) {
		t.Fatalf("expected cold seeded_history fallback, got %d: %s", w.Code, w.Body.String())
	}

	callsBytes, _ := os.ReadFile(callsLog)
	for _, want := range []string{`"method":"session/cancel"`, `"method":"_x.ai/rewind/points"`, `"method":"_x.ai/rewind/execute"`, `"method":"_x.ai/cancelRewind"`} {
		if !strings.Contains(string(callsBytes), want) {
			t.Errorf("expected %s in calls log, got:\n%s", want, string(callsBytes))
		}
	}
}
