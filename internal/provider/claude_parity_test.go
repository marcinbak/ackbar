package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agents/claude"
	"ackbar/internal/daemon"
)

func TestClaudeProvider_PluginParity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	leg := NewClaudeProvider()
	plg := claude.New()

	if leg.Agent() != plg.Key() || leg.DisplayName() != plg.DisplayName() ||
		leg.BrandColor() != plg.BrandColor() || leg.IconSVG() != plg.IconSVG() {
		t.Fatal("identity/branding mismatch between legacy ClaudeProvider and claude.Plugin")
	}

	uuid := "11111111-2222-4333-8444-555555555555"
	if leg.GetSpawnCommand(uuid) != plg.SpawnCommand(uuid) || leg.GetResumeCommand(uuid) != plg.ResumeCommand(uuid) ||
		leg.GetSpawnCommand("") != plg.SpawnCommand("") || leg.GetResumeCommand("bad") != plg.ResumeCommand("bad") {
		t.Fatal("spawn/resume command mismatch")
	}

	okL, _, _ := leg.CheckHookConfig()
	okP, _, _ := plg.CheckHookConfig(home)
	if okL != okP || okL {
		t.Fatalf("expected unconfigured before setup: legacy=%v plugin=%v", okL, okP)
	}
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`), 0o644)
	if err := plg.InstallHooks(home); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	okL, _, _ = leg.CheckHookConfig()
	okP, _, _ = plg.CheckHookConfig(home)
	if !okL || !okP {
		t.Fatalf("expected configured after InstallHooks: legacy=%v plugin=%v", okL, okP)
	}

	for _, tc := range []struct {
		evt     string
		payload string
	}{
		{"SessionStart", `{"session_id":"` + uuid + `","source":"compact","cwd":"/tmp"}`},
		{"PreToolUse", `{"session_id":"` + uuid + `","tool_name":"ExitPlanMode","tool_input":{"plan":"Step 1"}}`},
		{"PreToolUse", `{"session_id":"` + uuid + `","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which?","options":["A","B"]}]}}`},
		{"SubagentStart", `{"session_id":"` + uuid + `","hook_event_name":"SubagentStart","agent_id":"sub-1","agent_type":"coder"}`},
		{"Stop", `{"session_id":"` + uuid + `","last_assistant_message":"5-hour limit reached, resets at 14:30"}`},
		{"Stop", `{"sessionId":"` + uuid + `"}`},
		{"Stop", `{"session_id":"not-a-uuid"}`},
	} {
		evL, errL := leg.ParseHook(tc.evt, []byte(tc.payload))
		evP, errP := plg.ParseHook(tc.evt, []byte(tc.payload))
		if (errL != nil) != (errP != nil) || (evL == nil) != (evP == nil) {
			t.Fatalf("ParseHook(%s) nil/err mismatch: leg=(%+v,%v) plg=(%+v,%v)", tc.evt, evL, errL, evP, errP)
		}
		if evL != nil && (evL.State != evP.State || evL.ToolName != evP.ToolName || evL.Activity != evP.Activity) {
			t.Fatalf("ParseHook(%s) mismatch: leg=%+v vs plg=%+v", tc.evt, evL, evP)
		}
		if evL != nil && evL.Blocked != nil && (evL.Blocked.Kind != agent.BlockKind(evP.Blocked.Kind) || evL.Blocked.Question != evP.Blocked.Question) {
			t.Fatalf("ParseHook(%s) blocked mismatch: leg=%+v vs plg=%+v", tc.evt, evL.Blocked, evP.Blocked)
		}
	}

	subDir := filepath.Join(home, ".claude", "projects", "-tmp", uuid, "subagents")
	_ = os.MkdirAll(subDir, 0o755)
	_ = os.WriteFile(filepath.Join(subDir, "agent-9.meta.json"), []byte(`{"name":"helper","agentType":"tester"}`), 0o644)
	_ = os.WriteFile(filepath.Join(subDir, "agent-9.jsonl"), []byte(`{"type":"assistant","message":{"stop_reason":"end_turn"}}`), 0o644)
	subsL, errL := leg.ListSubagents(home, "/tmp", uuid)
	subsP, errP := plg.ListActiveSubagents(home, "/tmp", uuid)
	if errL != nil || errP != nil || len(subsL) != 1 || len(subsP) != 1 || subsL[0].State != subsP[0].State || subsL[0].Name != subsP[0].Name {
		t.Fatalf("ListSubagents mismatch: leg=%+v (%v) vs plg=%+v (%v)", subsL, errL, subsP, errP)
	}

	// CleanSessionFiles parity across session-env, tasks, subagent dirs, and .jsonl
	cleanID := "33333333-4444-4555-8666-777777777777"
	cleanProj := filepath.Join(home, ".claude", "projects", "-tmp")
	_ = os.MkdirAll(filepath.Join(cleanProj, cleanID, "subagents"), 0o755)
	_ = os.MkdirAll(filepath.Join(home, ".claude", "session-env", cleanID), 0o755)
	_ = os.MkdirAll(filepath.Join(home, ".claude", "tasks", cleanID), 0o755)
	_ = os.WriteFile(filepath.Join(cleanProj, cleanID+".jsonl"), []byte(`{"type":"user"}`+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cleanProj, cleanID, "subagents", "a.meta.json"), []byte(`{}`), 0o644)
	if err := plg.CleanSessionFiles(home, "/tmp", cleanID); err != nil {
		t.Fatalf("plg.CleanSessionFiles: %v", err)
	}
	if plg.HasConversationArtifacts(home, cleanID) {
		t.Fatal("expected HasConversationArtifacts false after CleanSessionFiles")
	}
	for _, gone := range []string{
		filepath.Join(home, ".claude", "session-env", cleanID),
		filepath.Join(home, ".claude", "tasks", cleanID),
		filepath.Join(cleanProj, cleanID),
		filepath.Join(cleanProj, cleanID+".jsonl"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("expected %s deleted, stat err=%v", gone, err)
		}
	}
}

func TestClaudeHooksDualMode_SubagentLifecycleAndExitPlanMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		legacyEnv string
	}{
		{name: "plugin", legacyEnv: ""},
		{name: "legacy", legacyEnv: "claude-code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv(agent.LegacyAgentsEnvVar, tc.legacyEnv)

			db, err := daemon.InitDB(filepath.Join(t.TempDir(), "hooks.db"))
			if err != nil {
				t.Fatalf("InitDB: %v", err)
			}
			srv := daemon.NewServer(db)
			if tc.legacyEnv == "" {
				if err := srv.RegisterPlugin(claude.New()); err != nil {
					t.Fatalf("RegisterPlugin: %v", err)
				}
			} else {
				srv.RegisterProvider(NewClaudeProvider())
			}

			uuid := "44444444-5555-4666-8777-888888888888"
			sessID := "claude-code:" + srv.HostName() + ":" + uuid
			_ = db.SaveSession(&daemon.Session{
				ID: sessID, Agent: "claude-code", Host: srv.HostName(), NativeID: uuid,
				Cwd: home, Managed: true, State: daemon.StateIdle, StartedAt: time.Now(),
			})

			postHook := func(path, evt, body string) {
				req := httptest.NewRequest(http.MethodPost, path+"?event="+evt, strings.NewReader(body))
				rec := httptest.NewRecorder()
				srv.Mux().ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s?event=%s returned %d: %s", path, evt, rec.Code, rec.Body.String())
				}
			}
			getRunningCount := func() int {
				req := httptest.NewRequest(http.MethodGet, "/v1/sessions/subagents?id="+sessID, nil)
				rec := httptest.NewRecorder()
				srv.Mux().ServeHTTP(rec, req)
				var res struct {
					RunningCount int `json:"running_count"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &res)
				return res.RunningCount
			}
			waitCond := func(cond func(*daemon.Session, int) bool, desc string) {
				for d := time.Now().Add(2 * time.Second); time.Now().Before(d); {
					s, _ := db.GetSession(sessID)
					rc := getRunningCount()
					if s != nil && cond(s, rc) {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				cur, _ := db.GetSession(sessID)
				t.Fatalf("timed out waiting for %s; sess=%+v running_count=%d", desc, cur, getRunningCount())
			}

			postHook("/v1/hooks/claude-code", "SubagentStart", `{"session_id":"`+uuid+`","hook_event_name":"SubagentStart","agent_id":"sub-1","agent_type":"coder","cwd":"`+home+`"}`)
			waitCond(func(_ *daemon.Session, rc int) bool { return rc == 1 }, "SubagentStart running_count == 1")

			stopPath := "/v1/hooks/claude-code"
			if tc.legacyEnv == "" {
				stopPath = "/v1/hooks/claude"
			}
			postHook(stopPath, "Stop", `{"session_id":"`+uuid+`","hook_event_name":"Stop","cwd":"`+home+`"}`)
			waitCond(func(s *daemon.Session, rc int) bool { return s.State == daemon.StateIdle && rc == 1 }, "Stop -> Idle preserving running_count == 1")

			postHook("/v1/hooks/claude-code", "SubagentStop", `{"session_id":"`+uuid+`","hook_event_name":"SubagentStop","agent_id":"sub-1","agent_type":"coder","cwd":"`+home+`"}`)
			waitCond(func(_ *daemon.Session, rc int) bool { return rc == 0 }, "SubagentStop running_count == 0")

			postHook("/v1/hooks/claude-code", "PreToolUse", `{"session_id":"`+uuid+`","hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{"plan":"Step 1"},"cwd":"`+home+`"}`)
			waitCond(func(s *daemon.Session, _ int) bool {
				return s.State == daemon.StateBlocked && s.Blocked != nil && s.Blocked.Kind == daemon.BlockApproval
			}, "ExitPlanMode blocked")
		})
	}
}
