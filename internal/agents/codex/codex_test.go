package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/version"
)

func TestPlugin_IdentityCapabilitiesLaunchAndAccounts(t *testing.T) {
	p := New()
	if p.Key() != "codex" || p.DisplayName() != "OpenAI Codex" || p.BrandColor() != "#10A37F" {
		t.Fatalf("identity mismatch: %q %q %q", p.Key(), p.DisplayName(), p.BrandColor())
	}
	if !strings.Contains(p.IconSVG(), "<svg") || len(p.BinaryNames()) != 1 || p.BinaryNames()[0] != "codex" {
		t.Fatalf("branding/binary mismatch")
	}
	caps := p.Capabilities()
	if !caps.SupportsTmux || caps.SupportsHeadless || !caps.SupportsHookShim || caps.SupportsNativeHooks || !caps.SupportsMultiAccount || !caps.SupportsTranscript || caps.NativeIDStrategy != agent.NativeIDLazyAdopt {
		t.Fatalf("capabilities mismatch: %+v", caps)
	}

	uuid := "019fa2cd-7d2b-7510-8940-e80ae59e2757"
	if p.SpawnCommand("any") != "codex exec" {
		t.Errorf("SpawnCommand mismatch: %q", p.SpawnCommand("any"))
	}
	if p.ResumeCommand(uuid) != "codex exec resume "+uuid {
		t.Errorf("ResumeCommand(uuid) mismatch: %q", p.ResumeCommand(uuid))
	}
	if p.ResumeCommand("not-a-uuid") != "codex exec" || p.ResumeCommand("") != "codex exec" {
		t.Errorf("ResumeCommand fallback mismatch")
	}

	spec, err := p.BuildLaunchSpec(agent.LaunchInput{TempUUID: uuid, Prompt: "hello", Env: map[string]string{"A": "B"}})
	if err != nil || spec.CommandLine != "codex exec" || spec.PromptInCommand || spec.Env["A"] != "B" {
		t.Fatalf("BuildLaunchSpec spawn mismatch: %+v err=%v", spec, err)
	}
	resumed, err := p.BuildLaunchSpec(agent.LaunchInput{Resume: true, NativeID: uuid})
	if err != nil || resumed.CommandLine != "codex exec resume "+uuid || resumed.PromptInCommand {
		t.Fatalf("BuildLaunchSpec resume mismatch: %+v err=%v", resumed, err)
	}

	tmpHome := t.TempDir()
	if p.APIKeyEnvVar() != "OPENAI_API_KEY" || p.LoginCommand() != "codex login" {
		t.Fatalf("APIKeyEnvVar/LoginCommand mismatch: %q %q", p.APIKeyEnvVar(), p.LoginCommand())
	}
	cfgDir, err := p.SetupProfileDir(tmpHome, "work", "")
	if err != nil || cfgDir != filepath.Join(tmpHome, ".codex-profiles", "work") {
		t.Fatalf("SetupProfileDir = (%q, %v)", cfgDir, err)
	}
	env := p.AccountEnvVars(tmpHome, &agent.AgentAccount{Name: "work", ConfigDir: "~/custom-codex", Env: map[string]string{"OPENAI_API_KEY": "sk-1"}})
	if env["CODEX_HOME"] != filepath.Join(tmpHome, "custom-codex") || env["ACKBAR_ACCOUNT"] != "work" || env["OPENAI_API_KEY"] != "sk-1" {
		t.Fatalf("AccountEnvVars mismatch: %+v", env)
	}
	if dirs := p.SkillDirs(tmpHome); len(dirs) != 1 || dirs[0] != filepath.Join(tmpHome, ".codex", "skills") {
		t.Fatalf("SkillDirs = %v", dirs)
	}
	profDirs := p.ProfileSkillDirs(tmpHome, &agent.AgentAccount{ConfigDir: "~/.codex-profiles/work"})
	if len(profDirs) != 1 || profDirs[0] != filepath.Join(tmpHome, ".codex-profiles", "work", "skills") {
		t.Fatalf("ProfileSkillDirs = %v", profDirs)
	}

	t.Setenv("CODEX_HOME", filepath.Join(tmpHome, "env-codex"))
	if got := HomeDir(""); got != filepath.Join(tmpHome, "env-codex") {
		t.Errorf("HomeDir('') with CODEX_HOME = %q", got)
	}
	if got := HomeDir(filepath.Join(tmpHome, "other")); got != filepath.Join(tmpHome, "other", ".codex") {
		t.Errorf("HomeDir(other) should ignore CODEX_HOME, got %q", got)
	}
}

func TestPlugin_ProvisionStatusHooksAndScanner(t *testing.T) {
	tmpHome := t.TempDir()
	p := New()

	if err := p.InstallHooks(tmpHome); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	codexDir := filepath.Join(tmpHome, ".codex")
	_ = os.MkdirAll(codexDir, 0o700)
	_ = os.WriteFile(filepath.Join(codexDir, "config.json"), []byte(`{"model":"gpt-5"}`), 0o600)

	if err := p.Provision(context.Background(), tmpHome); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	st := p.CheckStatus(context.Background(), tmpHome)
	if !st.Detected || !st.MCPInstalled || st.MCPCommand != "ackbar mcp" || !st.SkillInstalled || st.SkillVersion != version.Version || !st.RulesInstalled || st.RulesVersion != version.Version || st.DriftDetected {
		t.Fatalf("unexpected CheckStatus after Provision: %+v", st)
	}
	var cfg map[string]any
	cfgBytes, _ := os.ReadFile(filepath.Join(codexDir, "config.json"))
	if json.Unmarshal(cfgBytes, &cfg) != nil || cfg["model"] != "gpt-5" {
		t.Fatalf("Provision clobbered existing config.json keys: %s", string(cfgBytes))
	}

	ok, setupCmd, err := p.CheckHookConfig(tmpHome)
	if err != nil || ok || setupCmd != "ackbar-hook --agent=codex" {
		t.Fatalf("initial CheckHookConfig = (%v, %q, %v)", ok, setupCmd, err)
	}
	_ = os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"ackbar-hook --agent=codex"}]}]}}`), 0o644)
	if ok, _, _ := p.CheckHookConfig(tmpHome); !ok {
		t.Error("expected CheckHookConfig true for hooks.json")
	}
	_ = os.Remove(filepath.Join(codexDir, "hooks.json"))
	_ = os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(`hooks = "http://127.0.0.1:7777/v1/hooks/codex"`), 0o644)
	if ok, _, _ := p.CheckHookConfig(tmpHome); !ok {
		t.Error("expected CheckHookConfig true for config.toml")
	}

	evDefault, err := p.ParseHook("", []byte(`{"hook_event_name":"UserPromptSubmit"}`))
	if err != nil || evDefault.NativeID != "default" || evDefault.State != agent.StateWorking {
		t.Fatalf("default NativeID hook mismatch: %+v err=%v", evDefault, err)
	}
	evAsk, err := p.ParseHook("", []byte(`{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"request_user_input","tool_input":"{\"question\":\"Proceed?\",\"options\":[\"Yes\",\"No\"]}"}`))
	if err != nil || evAsk.State != agent.StateBlocked || evAsk.Blocked.Kind != agent.BlockQuestion || evAsk.Blocked.Question != "Proceed?" {
		t.Fatalf("question hook mismatch: %+v err=%v", evAsk, err)
	}
	evStop, err := p.ParseHook("", []byte(`{"session_id":"s1","hook_event_name":"Stop"}`))
	if err != nil || evStop.State != agent.StateEnded {
		t.Fatalf("stop hook mismatch: %+v err=%v", evStop, err)
	}
	if string(p.HookAckPayload("PreToolUse")) != "{}" {
		t.Errorf("HookAckPayload mismatch")
	}

	if !p.IsValidNativeID("default") || !p.IsValidNativeID("sess-1") || p.IsValidNativeID("../bad") {
		t.Error("IsValidNativeID mismatch")
	}
	if !p.IsIgnoredCommand("codex mcp list") || !p.IsIgnoredCommand("codex login") || p.IsIgnoredCommand("codex") {
		t.Error("IsIgnoredCommand mismatch")
	}
	if !p.MatchProcessCommand("codex", "codex exec") || p.MatchProcessCommand("codex", "ackbar-hook --agent=codex") {
		t.Error("MatchProcessCommand mismatch")
	}
	if limited, _, _ := p.DetectRateLimit("You've hit your usage limit · resets in 5m", time.Now()); !limited {
		t.Error("expected DetectRateLimit true")
	}
}
