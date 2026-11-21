package provider

import (
	"os"
	"path/filepath"
	"testing"

	"ackbar/internal/agent"
	"ackbar/internal/agents/claude"
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
		{"SubagentStart", `{"session_id":"` + uuid + `","agent_id":"sub-1","agent_type":"coder"}`},
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
}
