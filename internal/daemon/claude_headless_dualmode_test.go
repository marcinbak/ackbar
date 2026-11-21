package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
)

func TestClaudeHeadlessDualMode_StdioControlAndLiveSetModel(t *testing.T) {
	for _, tc := range []struct {
		name       string
		legacyEnv  string
		wantPlugin bool
	}{
		{name: "plugin", legacyEnv: "", wantPlugin: true},
		{name: "legacy", legacyEnv: "claude-code", wantPlugin: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv(agent.LegacyAgentsEnvVar, tc.legacyEnv)

			stdinLog := filepath.Join(t.TempDir(), "claude_stdin.log")
			binDir := t.TempDir()
			script := fmt.Sprintf(`#!/usr/bin/env bash
read -r turn1
printf '%%s\n' "$turn1" >> %q
echo '{"type":"system","subtype":"init","model":"claude-sonnet-4-6"}'
echo '{"type":"control_request","request_id":"req_plan_1","request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode","tool_use_id":"tu_p1","input":{"plan":"Ship it"}}}'
read -r ctrl1
printf '%%s\n' "$ctrl1" >> %q
echo '{"type":"control_request","request_id":"req_ask_1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"tu_q1","input":{"questions":[{"question":"Pick?","options":[{"label":"OptA"},{"label":"OptB"}]}]}}}'
read -r ctrl2
printf '%%s\n' "$ctrl2" >> %q
echo '{"type":"result","result":"turn1-ok","is_error":false}'
while IFS= read -r line; do
  printf '%%s\n' "$line" >> %q
  case "$line" in
    *'"subtype":"set_model"'*)
      echo '{"type":"control_response","response":{"subtype":"success","response":{"model":"claude-opus-4-6"}}}'
      ;;
    *)
      echo '{"type":"result","result":"turn2-ok","is_error":false}'
      ;;
  esac
done
`, stdinLog, stdinLog, stdinLog, stdinLog)
			if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
				t.Fatalf("write mock claude: %v", err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			srv, db := setupTestServer(t)
			srv.homeDir = home
			_, hasPlugin := srv.PluginRegistry().Get("claude-code")
			if hasPlugin != tc.wantPlugin {
				t.Fatalf("PluginRegistry.Get(claude-code) = %v, want %v", hasPlugin, tc.wantPlugin)
			}

			uuid := "55555555-6666-4777-8888-999999999999"
			sessID := "claude-code:" + srv.HostName() + ":" + uuid
			_ = db.SaveSession(&Session{
				ID: sessID, Agent: "claude-code", Host: srv.HostName(), NativeID: uuid,
				Cwd: home, EngineType: EngineHeadless, PermissionMode: "plan", Managed: true, State: StateIdle,
			})
			loadSess := func() *Session { s, _ := db.GetSession(sessID); return s }
			waitCond := func(desc string, cond func() bool) {
				t.Helper()
				for d := time.Now().Add(5 * time.Second); time.Now().Before(d); {
					if cond() {
						return
					}
					time.Sleep(15 * time.Millisecond)
				}
				t.Fatalf("timed out waiting for %s", desc)
			}

			if err := srv.headless.RunTurn(context.Background(), loadSess(), "start"); err != nil {
				t.Fatalf("RunTurn 1: %v", err)
			}
			waitCond("ExitPlanMode block", func() bool {
				s := loadSess()
				return s != nil && s.State == StateBlocked && s.Blocked != nil && s.Blocked.Kind == BlockApproval
			})
			if handled, err := srv.headless.TryAnswerControlAction(context.Background(), loadSess(), "allow", "", ""); !handled || err != nil {
				t.Fatalf("TryAnswerControlAction allow = (%v, %v)", handled, err)
			}

			waitCond("AskUserQuestion block", func() bool {
				s := loadSess()
				return s != nil && s.State == StateBlocked && s.Blocked != nil && s.Blocked.Kind == BlockQuestion
			})
			if handled, err := srv.headless.TryAnswerControlAction(context.Background(), loadSess(), "answer", "OptA", ""); !handled || err != nil {
				t.Fatalf("TryAnswerControlAction answer = (%v, %v)", handled, err)
			}
			waitCond("turn 1 idle", func() bool { return !srv.headless.IsRunning(sessID) })

			srv.headless.mu.RLock()
			h1 := srv.headless.warmWorkers[sessID]
			var workerPID1, childPID1 int
			if h1 != nil {
				workerPID1, childPID1 = h1.WorkerPID, h1.ChildPID
			}
			srv.headless.mu.RUnlock()
			if workerPID1 <= 1 || childPID1 <= 1 {
				t.Fatalf("expected valid warm worker PIDs, got %+v", h1)
			}

			cur := loadSess()
			cur.Model = "claude-opus-4-6"
			cur.MetadataUpdatedAt = time.Now().UTC()
			_ = db.SaveSession(cur)
			srv.headless.NotifyConfigChanged(cur)

			srv.headless.mu.RLock()
			h2 := srv.headless.warmWorkers[sessID]
			srv.headless.mu.RUnlock()
			if h2 == nil || h2.WorkerPID != workerPID1 || h2.ChildPID != childPID1 {
				t.Fatalf("expected live set_model without child restart: (%d,%d) vs (%+v)", workerPID1, childPID1, h2)
			}

			if err := srv.headless.RunTurn(context.Background(), loadSess(), "turn 2"); err != nil {
				t.Fatalf("RunTurn 2: %v", err)
			}
			waitCond("turn 2 idle", func() bool { return !srv.headless.IsRunning(sessID) })

			rawStdin, err := os.ReadFile(stdinLog)
			if err != nil {
				t.Fatalf("read stdinLog: %v", err)
			}
			var ctrlCount int
			var foundSetModel bool
			for _, line := range strings.Split(strings.TrimSpace(string(rawStdin)), "\n") {
				var msg map[string]any
				if json.Unmarshal([]byte(line), &msg) != nil {
					continue
				}
				if msg["type"] == "control_response" {
					ctrlCount++
				} else if msg["type"] == "control_request" {
					if req, _ := msg["request"].(map[string]any); req["subtype"] == "set_model" && req["model"] == "claude-opus-4-6" {
						foundSetModel = true
					}
				}
			}
			if ctrlCount < 2 || !foundSetModel {
				t.Fatalf("expected >=2 control_responses (%d) and set_model (%v); stdin:\n%s", ctrlCount, foundSetModel, string(rawStdin))
			}
		})
	}
}
