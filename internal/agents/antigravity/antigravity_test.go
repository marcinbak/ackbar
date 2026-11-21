package antigravity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/version"
)

func TestIdentityCapabilitiesAndRateLimit(t *testing.T) {
	p := New()
	if p.Key() != "antigravity" || p.DisplayName() != "Google Antigravity" || p.BrandColor() != "#4285F4" {
		t.Fatalf("unexpected identity/branding: %q %q %q", p.Key(), p.DisplayName(), p.BrandColor())
	}
	if !slices.Contains(p.Aliases(), "agy") || !slices.Contains(p.BinaryNames(), "agy") {
		t.Fatalf("unexpected aliases/binaries: %v %v", p.Aliases(), p.BinaryNames())
	}
	caps := p.Capabilities()
	if !caps.SupportsTmux || !caps.SupportsHeadless || caps.HeadlessMode != agent.HeadlessModePersistent ||
		caps.NativeIDStrategy != agent.NativeIDLazyAdopt || !caps.SupportsSubagents || caps.SupportsEffortLevel {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if slices.Contains(caps.SupportedPermissionModes, "dontAsk") {
		t.Fatalf("antigravity must not list dontAsk mode: %v", caps.SupportedPermissionModes)
	}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	if ok, _, reason := p.DetectRateLimit("Quota exceeded, resets in 5 minutes", now); !ok || !strings.Contains(reason, "Quota exceeded") {
		t.Fatalf("expected rate limit match, got ok=%v reason=%q", ok, reason)
	}
	if ok, _, _ := p.DetectRateLimit("normal output", now); ok {
		t.Fatal("expected no rate limit on normal text")
	}
}

func TestProvisionAndAccounts(t *testing.T) {
	p := New()
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".gemini", "antigravity"), 0o755)
	if !p.IsInstalled(home) {
		t.Fatal("expected IsInstalled true with .gemini/antigravity dir")
	}
	st := p.CheckStatus(context.Background(), home)
	if !st.Detected || !st.DriftDetected || st.MCPInstalled {
		t.Fatalf("expected detected+drift before provision: %+v", st)
	}
	if err := p.Provision(context.Background(), home); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	st = p.CheckStatus(context.Background(), home)
	if !st.MCPInstalled || !st.SkillInstalled || !st.RulesInstalled || st.DriftDetected || st.RulesVersion != version.Version {
		t.Fatalf("expected clean provisioned status, got %+v", st)
	}
	if err := p.InstallHooks(home); err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}
	ok, _, err := p.CheckHookConfig(home)
	if err != nil || !ok {
		t.Fatalf("expected CheckHookConfig true after InstallHooks, got ok=%v err=%v", ok, err)
	}

	profDir, err := p.SetupProfileDir(home, "work", "")
	if err != nil {
		t.Fatalf("SetupProfileDir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profDir, "config", "hooks.json")); err != nil {
		t.Fatalf("expected profile hooks.json: %v", err)
	}
	acct := &agent.AgentAccount{Name: "work", ConfigDir: "~/work-prof", Env: map[string]string{"FOO": "bar"}}
	env := p.AccountEnvVars(home, acct)
	if env["GEMINI_CLI_HOME"] != filepath.Join(home, "work-prof") || env["ACKBAR_ACCOUNT"] != "work" || p.APIKeyEnvVar() != "GEMINI_API_KEY" {
		t.Fatalf("unexpected account env/key: %v %q", env, p.APIKeyEnvVar())
	}
	if sDirs := p.ProfileSkillDirs(home, acct); len(sDirs) != 2 {
		t.Fatalf("expected 2 profile skill dirs, got %v", sDirs)
	}
}

func TestHooksAndParseHook(t *testing.T) {
	p := New()
	if string(p.HookAckPayload("pretooluse")) != `{"decision":"allow"}` {
		t.Fatalf("unexpected PreToolUse ack: %s", p.HookAckPayload("pretooluse"))
	}
	if string(p.HookAckPayload("Stop")) != "{}" {
		t.Fatalf("unexpected Stop ack: %s", p.HookAckPayload("Stop"))
	}
	ev, err := p.ParseHook("PreToolUse", []byte(`{"conversationId":"11111111-2222-3333-4444-555555555555","workspacePaths":["/tmp/proj"],"toolCall":{"name":"ask_question","args":{"question":"Pick?","options":["A","B"]}}}`))
	if err != nil || ev.State != agent.StateBlocked || ev.Blocked == nil || ev.Blocked.Kind != agent.BlockQuestion || ev.Cwd != "/tmp/proj" {
		t.Fatalf("unexpected PreToolUse ask_question parse: %+v err=%v", ev, err)
	}
	evPlan, err := p.ParseHook("PreToolUse", []byte(`{"conversationId":"11111111-2222-3333-4444-555555555555","toolCall":{"name":"exit_plan_mode","args":{"plan":"# Plan"}}}`))
	if err != nil || evPlan.Blocked == nil || evPlan.Blocked.Kind != agent.BlockApproval {
		t.Fatalf("unexpected plan approval event: %+v err=%v", evPlan, err)
	}
	evPerm, err := p.ParseHook("PreToolUse", []byte(`{"conversationId":"11111111-2222-3333-4444-555555555555","toolCall":{"name":"ask_permission","args":{"command":"rm -rf /tmp/x"}}}`))
	if err != nil || evPerm.Blocked == nil || evPerm.Blocked.Kind != agent.BlockPermission || evPerm.Blocked.Reason != "rm -rf /tmp/x" {
		t.Fatalf("unexpected permission event: %+v err=%v", evPerm, err)
	}
}

func TestHeadlessBuildTurnAndStreamParser(t *testing.T) {
	p := New()
	if p.SupportsStdioControl() || p.DefaultPermissionMode() != "bypassPermissions" {
		t.Fatal("unexpected persistent defaults")
	}
	if _, sup := p.FormatSetModel("gemini-4-carbon-high"); sup {
		t.Fatal("expected FormatSetModel unsupported")
	}
	specPers, err := p.BuildTurnSpec(agent.HeadlessTurnInput{
		Session:      &agent.Session{ID: "s1", NativeID: "conv-1", Cwd: "/tmp", PermissionMode: "plan", Model: "gemini-4-carbon-high", ExtraDirs: []string{"/extra"}},
		IsExisting:   true,
		SystemPrompt: "SUP",
	})
	if err != nil || slices.Contains(specPers.Args, "-p") || !slices.Contains(specPers.Args, "--input-format") || !slices.Contains(specPers.Args, "--conversation") || !slices.Contains(specPers.Args, "--mode") {
		t.Fatalf("unexpected persistent spec: %+v err=%v", specPers, err)
	}
	stdinTurn1, err := p.FormatStdinTurn(agent.PersistentTurnInput{IsSupervisor: true, IsFirstTurn: true, SystemPrompt: "SYS", Prompt: "Go"})
	if err != nil || !strings.Contains(stdinTurn1, `"event":"user"`) || !strings.Contains(stdinTurn1, `[System Instruction: SYS]`) {
		t.Fatalf("unexpected stdin turn 1: %q err=%v", stdinTurn1, err)
	}

	sp := p.NewStreamParser("sess-1")
	initEvts, handled, _ := sp.ParseStreamLine([]byte(`{"event":"init","conversation_id":"c-9","model":"gemini-4-carbon-high"}`))
	if !handled || len(initEvts) != 1 || initEvts[0].Type != "session_init" || initEvts[0].NativeID != "c-9" || initEvts[0].Model != "gemini-4-carbon-high" {
		t.Fatalf("unexpected init events: %+v handled=%v", initEvts, handled)
	}
	_, _, _ = sp.ParseStreamLine([]byte(`{"event":"step_update","step_update":{"step_index":1,"step_type":"agent_response","state":"ACTIVE","text_delta":"First part"}}`))
	dupEvts, _, _ := sp.ParseStreamLine([]byte(`{"event":"step_update","step_update":{"step_index":1,"step_type":"agent_response","state":"DONE","text_delta":"First part"}}`))
	if len(dupEvts) != 0 {
		t.Fatalf("expected duplicate DONE text_delta ignored, got %+v", dupEvts)
	}
	_, _, _ = sp.ParseStreamLine([]byte(`{"event":"step_update","step_update":{"step_index":2,"step_type":"error_message","error":"boom detail"}}`))
	resEvts, _, _ := sp.ParseStreamLine([]byte(`{"event":"result","result":{"status":"ERROR","error":"boom detail"}}`))
	if len(resEvts) != 1 || resEvts[0].Type != "turn_complete" || !resEvts[0].IsError || resEvts[0].Text != "boom detail" {
		t.Fatalf("expected single error turn_complete event, got %+v", resEvts)
	}
	nullEvts, handledNull, _ := sp.ParseStreamLine([]byte(`{"event":"result","result":null}`))
	if !handledNull || len(nullEvts) != 1 || nullEvts[0].Type != "turn_complete" || nullEvts[0].IsError {
		t.Fatalf("expected turn_complete on null result, got %+v handled=%v", nullEvts, handledNull)
	}
	_, _, _ = sp.ParseStreamLine([]byte(`{"event":"step_update","step_update":{"step_index":3,"step_type":"error_message","error":"eof disconnect"}}`))
	eofEvts := sp.(agent.StreamFlusher).FlushEOF()
	if len(eofEvts) != 1 || eofEvts[0].Type != "error" || !eofEvts[0].IsError || eofEvts[0].Text != "eof disconnect" {
		t.Fatalf("expected error event from FlushEOF, got %+v", eofEvts)
	}
	if second := sp.(agent.StreamFlusher).FlushEOF(); len(second) != 0 {
		t.Fatalf("expected empty FlushEOF after reset, got %+v", second)
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(stdinTurn1), &raw)
	if args, ok := p.InspectSubcommandArgs("plugins"); !ok || !slices.Contains(args, "--json") {
		t.Fatalf("unexpected inspect plugins args: %v %v", args, ok)
	}
	if args, ok := p.InspectSubcommandArgs("auth_login"); !ok || !slices.Equal(args, []string{"acp", "--login"}) {
		t.Fatalf("unexpected inspect auth_login args: %v %v", args, ok)
	}
	if _, ok := p.InspectSubcommandArgs("doctor"); ok {
		t.Fatal("expected doctor CLI subcommand false for antigravity")
	}
}

func TestInstallHooksLocalBinPreference(t *testing.T) {
	p := New()
	home := t.TempDir()
	hookPath := filepath.Join(home, ".gemini", "config", "hooks.json")

	if err := p.InstallHooks(home); err != nil {
		t.Fatalf("InstallHooks fallback failed: %v", err)
	}
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	if !strings.Contains(string(data), `"command": "ackbar-hook --agent=antigravity --event=SessionStart"`) {
		t.Fatalf("expected bare ackbar-hook fallback, got:\n%s", string(data))
	}

	localBin := filepath.Join(home, ".local", "bin", "ackbar-hook")
	if err := os.MkdirAll(filepath.Dir(localBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.InstallHooks(home); err != nil {
		t.Fatalf("InstallHooks local bin failed: %v", err)
	}
	data, err = os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read updated hooks.json: %v", err)
	}
	wantCmd := localBin + " --agent=antigravity --event=SessionStart"
	if !strings.Contains(string(data), wantCmd) {
		t.Fatalf("expected %q in hooks.json, got:\n%s", wantCmd, string(data))
	}
	profDir, err := p.SetupProfileDir(home, "localbin-prof", "")
	if err != nil {
		t.Fatalf("SetupProfileDir failed: %v", err)
	}
	profData, err := os.ReadFile(filepath.Join(profDir, "config", "hooks.json"))
	if err != nil || !strings.Contains(string(profData), wantCmd) {
		t.Fatalf("expected %q in profile hooks.json, err=%v got:\n%s", wantCmd, err, string(profData))
	}
}
