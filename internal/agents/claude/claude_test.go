package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"ackbar/internal/agent"
)

const testUUID = "11111111-2222-4333-8444-555555555555"

func TestIdentityBrandingAndCapabilities(t *testing.T) {
	p := New()
	if p.Key() != "claude-code" || p.DisplayName() != "Claude Code" || p.BrandColor() != "#D97706" {
		t.Fatalf("unexpected identity: %s / %s / %s", p.Key(), p.DisplayName(), p.BrandColor())
	}
	if !slices.Equal(p.Aliases(), []string{"claude"}) {
		t.Fatalf("unexpected aliases: %v", p.Aliases())
	}
	if !strings.Contains(p.IconSVG(), "claude-logo") {
		t.Fatalf("missing IconSVG")
	}
	var asAny any = p
	if _, ok := asAny.(agent.StreamParserFactory); ok {
		t.Fatal("claude.Plugin must intentionally NOT implement StreamParserFactory")
	}
	caps := p.Capabilities()
	if caps.NativeIDStrategy != agent.NativeIDEagerUUID || !caps.SupportsNativeHooks || caps.SupportsHookShim || !caps.SupportsEffortLevel {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestHeadlessBuildTurnSpecAndControls(t *testing.T) {
	p := New()
	home := t.TempDir()
	spec, err := p.BuildTurnSpec(agent.HeadlessTurnInput{
		HomeDir: home,
		Session: &agent.Session{
			NativeID: testUUID, Cwd: "/repo", Model: "claude-sonnet-4-6", Effort: "high",
			PermissionMode: "plan", ExtraDirs: []string{"/extra/a"}, IsSupervisor: true,
		},
		SystemPrompt: "SUP",
	})
	if err != nil {
		t.Fatalf("BuildTurnSpec persistent: %v", err)
	}
	for _, want := range []string{"--input-format", "stdio", "--allow-dangerously-skip-permissions", "--permission-mode", "plan", "--session-id", testUUID, "--model", "claude-sonnet-4-6", "--effort", "high", "--append-system-prompt", "SUP", "--add-dir", "/extra/a"} {
		if !slices.Contains(spec.Args, want) {
			t.Fatalf("persistent args missing %q: %v", want, spec.Args)
		}
	}

	oneshot, err := p.BuildTurnSpec(agent.HeadlessTurnInput{
		HomeDir:    home,
		Prompt:     "Fix bug",
		IsExisting: true,
		Session:    &agent.Session{NativeID: testUUID, Cwd: "/repo", PermissionMode: "dontAsk"},
	})
	if err != nil {
		t.Fatalf("BuildTurnSpec oneshot: %v", err)
	}
	if !slices.Contains(oneshot.Args, "-p") || !slices.Contains(oneshot.Args, "Fix bug") || !slices.Contains(oneshot.Args, "--resume") || !slices.Contains(oneshot.Args, "default") {
		t.Fatalf("unexpected oneshot args: %v", oneshot.Args)
	}
	if slices.Contains(oneshot.Args, "--permission-prompt-tool") {
		t.Fatalf("oneshot should not include stdio permission tool: %v", oneshot.Args)
	}

	stdinTurn, err := p.FormatStdinTurn(agent.PersistentTurnInput{Prompt: "hello"})
	if err != nil || !strings.Contains(stdinTurn, `"type":"user"`) || !strings.Contains(stdinTurn, "hello") {
		t.Fatalf("FormatStdinTurn = %q err=%v", stdinTurn, err)
	}
	setModel, ok := p.FormatSetModel("claude-opus-4-6")
	if !ok || !strings.Contains(setModel, `"subtype":"set_model"`) {
		t.Fatalf("FormatSetModel = %q %v", setModel, ok)
	}
	for _, k := range []string{"plugins", "agents", "doctor", "mcp_login"} {
		if args, ok := p.InspectSubcommandArgs(k); !ok || len(args) == 0 {
			t.Fatalf("InspectSubcommandArgs(%q) failed", k)
		}
	}
	evts, err := p.ParseStreamEvent([]byte(`{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"hi"}]}}`))
	if err != nil || len(evts) < 2 {
		t.Fatalf("ParseStreamEvent = %+v err=%v", evts, err)
	}
}

func TestProvisionAndInstallHooks(t *testing.T) {
	p := New()
	home := t.TempDir()
	if err := p.InstallHooks(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist when settings missing, got %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"theme":"dark","hooks":{"Stop":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Provision(context.Background(), home); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	st := p.CheckStatus(context.Background(), home)
	if !st.Detected || !st.MCPInstalled || !st.SkillInstalled || !st.RulesInstalled || st.DriftDetected {
		t.Fatalf("unexpected status after Provision: %+v", st)
	}
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"permissions":{"allow":["Write(~/.memories/log/**)"]}}`), 0o644)
	if err := p.InstallHooks(home); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	rawSettings, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(rawSettings), "Edit(~/.memories/log/**)") || !strings.Contains(string(rawSettings), "SubagentStart") {
		t.Fatalf("unexpected settings after InstallHooks:\n%s", rawSettings)
	}
	rawClaudeJSON, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	var parsed map[string]any
	_ = json.Unmarshal(rawClaudeJSON, &parsed)
	if _, hasLegacy := parsed["hooks"]; hasLegacy {
		t.Fatalf("expected legacy hooks deleted from .claude.json: %s", rawClaudeJSON)
	}

	// Malformed JSON must not be overwritten.
	bad := `{"broken":`
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(bad), 0o600)
	if err := p.Provision(context.Background(), home); err == nil {
		t.Fatal("expected error provisioning malformed .claude.json")
	}
	afterBad, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if string(afterBad) != bad {
		t.Fatalf("malformed .claude.json was altered: %s", afterBad)
	}
}

func TestAccountsAndModelsPrecedence(t *testing.T) {
	p := New()
	home := t.TempDir()
	cfgDir, err := p.SetupProfileDir(home, "work profile", "")
	if err != nil {
		t.Fatalf("SetupProfileDir: %v", err)
	}
	cfgData, _ := os.ReadFile(filepath.Join(cfgDir, "settings.json"))
	if !strings.Contains(string(cfgData), "work+profile") {
		t.Fatalf("expected URL-escaped account in profile hook URL: %s", cfgData)
	}
	acct := &agent.AgentAccount{Name: "work", ConfigDir: cfgDir, Env: map[string]string{"ANTHROPIC_MODEL": "claude-opus-4-6[1m]"}}
	if !p.IsAccountLoggedIn(home, acct) {
		t.Fatal("expected profile with settings.json logged in")
	}
	cwd := t.TempDir()
	_ = os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, ".claude", "settings.json"), []byte(`{"model":"claude-sonnet-4-6[1m]"}`), 0o644)
	if got := p.ResolveWorkspaceModel(home, cwd, acct); got != "claude-sonnet-4-6" {
		t.Fatalf("ResolveWorkspaceModel = %q, want claude-sonnet-4-6", got)
	}
	fifo := filepath.Join(cwd, ".claude", "settings.local.json")
	if err := syscall.Mkfifo(fifo, 0o600); err == nil {
		done := make(chan string, 1)
		go func() { done <- p.ResolveWorkspaceModel(home, cwd, acct) }()
		select {
		case got := <-done:
			if got != "claude-sonnet-4-6" {
				t.Fatalf("FIFO fallback got %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("ResolveWorkspaceModel hung on FIFO")
		}
	}
}

func TestHooksAndTmuxLaunch(t *testing.T) {
	p := New()
	ev, err := p.ParseHook("PreToolUse", []byte(`{"session_id":"`+testUUID+`","hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{"plan":"Step 1"}}`))
	if err != nil || ev == nil || ev.State != agent.StateBlocked || ev.Blocked.Kind != agent.BlockApproval {
		t.Fatalf("ExitPlanMode ParseHook = %+v err=%v", ev, err)
	}
	custEv, err := p.ParseHook("CustomEvent", []byte(`{"session_id":"`+testUUID+`"}`))
	if err != nil || custEv == nil || custEv.Activity != "Event: CustomEvent" {
		t.Fatalf("default evtName activity = %+v err=%v", custEv, err)
	}

	spec, err := p.BuildLaunchSpec(agent.LaunchInput{TempUUID: testUUID, Prompt: "hello", Env: map[string]string{"X": "1"}})
	if err != nil || !spec.PromptInCommand || !strings.Contains(spec.CommandLine, `--session-id `+testUUID+` "$ACKBAR_INIT_PROMPT"`) || strings.Contains(spec.CommandLine, ` -- `) {
		t.Fatalf("BuildLaunchSpec normal = %+v err=%v", spec, err)
	}
	dashSpec, err := p.BuildLaunchSpec(agent.LaunchInput{TempUUID: testUUID, Prompt: " --dangerously-skip-permissions", Env: map[string]string{"X": "1"}})
	if err != nil || !dashSpec.PromptInCommand || !strings.Contains(dashSpec.CommandLine, `--session-id `+testUUID+` -- "$ACKBAR_INIT_PROMPT"`) {
		t.Fatalf("BuildLaunchSpec leading dash = %+v err=%v", dashSpec, err)
	}

	aliasSess := &agent.Session{Agent: "claude", EngineType: agent.EngineTmux, TmuxName: "ackbar-claude-nonexistent-alias-test"}
	if !p.InspectStatus(context.Background(), aliasSess) || aliasSess.State != agent.StateEnded {
		t.Fatalf("InspectStatus should accept 'claude' alias, got %+v", aliasSess)
	}

	// Tail 8 MiB history.jsonl read (n2): entry after >8 MiB of noise must still resolve.
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	histPath := filepath.Join(home, ".claude", "history.jsonl")
	hf, err := os.OpenFile(histPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	padLine := strings.Repeat("x", 1024)
	for i := 0; i < 8400; i++ {
		_, _ = hf.WriteString(`{"sessionId":"00000000-0000-4000-8000-000000000000","display":"` + padLine + `"}` + "\n")
	}
	histID := "99999999-8888-4777-8666-555555555555"
	_, _ = hf.WriteString(`{"sessionId":"` + histID + `","display":"Tail History Title"}` + "\n")
	_ = hf.Close()
	if got := p.ResolveSessionTitle(home, "/repo", histID); got != "Tail History Title" {
		t.Fatalf("ResolveSessionTitle tail >8MiB = %q, want Tail History Title", got)
	}
}
