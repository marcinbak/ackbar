package grok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ackbar/internal/agent"
)

func TestPlugin_IdentityCapabilitiesAndCommands(t *testing.T) {
	p := New()
	if p.Key() != "grok" || p.DisplayName() != "xAI Grok" {
		t.Fatalf("identity mismatch: %q %q", p.Key(), p.DisplayName())
	}
	if p.BrandColor() != "#E5E7EB" || !strings.Contains(p.IconSVG(), "<svg") {
		t.Fatalf("branding mismatch")
	}
	caps := p.Capabilities()
	if !caps.SupportsTmux || !caps.SupportsHeadless || caps.HeadlessMode != agent.HeadlessModeACP || !caps.LocksExtraDirsOnResume {
		t.Fatalf("capabilities mismatch: %+v", caps)
	}

	uuid := "12345678-1234-1234-1234-123456789abc"
	if p.SpawnCommand("") != "grok" || p.SpawnCommand("invalid-uuid") != "grok" || p.SpawnCommand(uuid) != "grok -s "+uuid {
		t.Errorf("SpawnCommand mismatch")
	}
	if p.ResumeCommand("valid-session-123") != "grok -r valid-session-123" {
		t.Errorf("ResumeCommand mismatch")
	}
	for _, bad := range []string{"", "-flag", ".", "..", "../unsafe/path", "session\nrm -rf /", "session with spaces"} {
		if cmd := p.ResumeCommand(bad); cmd != "" {
			t.Errorf("expected empty ResumeCommand for %q, got %q", bad, cmd)
		}
	}
}

func TestPlugin_BuildLaunchSpec(t *testing.T) {
	p := New()
	spec, err := p.BuildLaunchSpec(agent.LaunchInput{
		TempUUID:       "12345678-1234-1234-1234-123456789abc",
		Model:          "grok-2",
		Effort:         "high",
		PermissionMode: "bypass",
		ExtraDirs:      []string{"/path/one", "/path/it's a path", "/bad\ndir"},
		Prompt:         "-starts-with-hyphen",
	})
	if err != nil {
		t.Fatalf("BuildLaunchSpec failed: %v", err)
	}
	want := `grok -s 12345678-1234-1234-1234-123456789abc -m grok-2 --effort high --permission-mode bypass --add-dir '/path/one' --add-dir '/path/it'\''s a path' -- "$ACKBAR_INIT_PROMPT"`
	if spec.CommandLine != want {
		t.Errorf("CommandLine mismatch:\ngot:  %q\nwant: %q", spec.CommandLine, want)
	}
	if spec.Env["ACKBAR_INIT_PROMPT"] != "-starts-with-hyphen" {
		t.Errorf("expected ACKBAR_INIT_PROMPT set, got %+v", spec.Env)
	}

	unsafeSpec, _ := p.BuildLaunchSpec(agent.LaunchInput{
		TempUUID:       "12345678-1234-1234-1234-123456789abc",
		Model:          "grok-2;rm -rf /",
		Effort:         "high && echo bad",
		PermissionMode: "bypass|cat",
	})
	if strings.Contains(unsafeSpec.CommandLine, ";") || strings.Contains(unsafeSpec.CommandLine, "&&") || strings.Contains(unsafeSpec.CommandLine, "|") {
		t.Errorf("unsafe metacharacters leaked: %q", unsafeSpec.CommandLine)
	}

	resumedSpec, _ := p.BuildLaunchSpec(agent.LaunchInput{
		NativeID:  "sess-123",
		Resume:    true,
		ExtraDirs: []string{"/path/one"},
	})
	if resumedSpec.CommandLine != "grok -r sess-123" {
		t.Errorf("resumed CommandLine should omit --add-dir, got %q", resumedSpec.CommandLine)
	}
}

func TestPlugin_ParseHook(t *testing.T) {
	p := New()

	ev, err := p.ParseHook("", []byte(`{"session_id":"grok-sess-1","event":"UserPromptSubmit","prompt":"Fix unit tests","cwd":"/repo"}`))
	if err != nil || ev == nil || ev.Agent != "grok" || ev.State != agent.StateWorking || !strings.Contains(ev.Activity, "Fix unit tests") {
		t.Fatalf("prompt event mismatch: %+v err=%v", ev, err)
	}
	if evTool, err := p.ParseHook("", []byte(`{"session_id":"grok-sess-1","event":"PreToolUse","name":"bash"}`)); err != nil || evTool.Activity != "Running bash" {
		t.Fatalf("tool event mismatch: %+v err=%v", evTool, err)
	}
	if evPerm, err := p.ParseHook("", []byte(`{"session_id":"grok-sess-1","event":"ApprovalRequest","reason":"Run sudo command"}`)); err != nil || evPerm.State != agent.StateBlocked || evPerm.Blocked.Kind != agent.BlockPermission {
		t.Fatalf("approval event mismatch: %+v err=%v", evPerm, err)
	}
	if evStop, err := p.ParseHook("", []byte(`{"session_id":"grok-sess-1","event":"Stop"}`)); err != nil || evStop.State != agent.StateIdle {
		t.Fatalf("stop event mismatch: %+v err=%v", evStop, err)
	}
	if evCamel, err := p.ParseHook("", []byte(`{"sessionId":"grok-sess-camel","event":"user_prompt_submit","prompt":"CamelCase test"}`)); err != nil || evCamel == nil || evCamel.NativeID != "grok-sess-camel" {
		t.Fatalf("camelCase event mismatch: %+v err=%v", evCamel, err)
	}
	if evSub, err := p.ParseHook("", []byte(`{"sessionId":"grok-sess-sub","subagentType":"explore","event":"user_prompt_submit"}`)); err != nil || evSub != nil {
		t.Errorf("expected nil for subagent prompt, got %+v err=%v", evSub, err)
	}
	if evNotif, err := p.ParseHook("", []byte(`{"sessionId":"grok-sess-1","event":"notification","notification_type":"permission_prompt","message":"Permission needed"}`)); err != nil || evNotif == nil || evNotif.Blocked.Kind != agent.BlockPermission {
		t.Errorf("notification event mismatch: %+v err=%v", evNotif, err)
	}
	if evClosed, err := p.ParseHook("", []byte(`{"sessionId":"grok-sess-1","event":"stop","stop_reason":"channel_closed"}`)); err != nil || evClosed.State != agent.StateEnded {
		t.Errorf("channel_closed mismatch: %+v err=%v", evClosed, err)
	}
	if evRL, err := p.ParseHook("", []byte(`{"sessionId":"grok-sess-1","event":"stop_failure","error_type":"rate_limit_exceeded"}`)); err != nil || evRL.Blocked.Kind != agent.BlockRateLimit {
		t.Errorf("rate limit event mismatch: %+v err=%v", evRL, err)
	}
	if evAuth, err := p.ParseHook("", []byte(`{"sessionId":"grok-sess-1","event":"stop_failure","error_type":"auth_failure"}`)); err != nil || evAuth.Blocked.Kind != agent.BlockAuth {
		t.Errorf("auth event mismatch: %+v err=%v", evAuth, err)
	}
	if evAsk, err := p.ParseHook("", []byte(`{"sessionId":"grok-sess-1","event":"pre_tool_use","name":"AskUserQuestion","input":{"question":"Proceed?"}}`)); err != nil || evAsk.Blocked.Kind != agent.BlockQuestion {
		t.Errorf("ask question event mismatch: %+v err=%v", evAsk, err)
	}
	if _, err := p.ParseHook("", []byte("{corrupt json")); err == nil {
		t.Errorf("expected error on corrupt JSON")
	}
	if string(p.HookAckPayload("PreToolUse")) != "{}" {
		t.Errorf("HookAckPayload mismatch")
	}
}

func TestPlugin_CheckHookConfigAndCleanSafety(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	p := New()

	configured, setupCmd, err := p.CheckHookConfig(tmpHome)
	if err != nil || configured || setupCmd != "ackbar-hook --agent=grok" {
		t.Fatalf("initial CheckHookConfig = (%v, %q, %v)", configured, setupCmd, err)
	}

	grokDir := filepath.Join(tmpHome, ".grok")
	_ = os.MkdirAll(grokDir, 0o755)
	_ = os.WriteFile(filepath.Join(grokDir, "hooks.json"), []byte(`{"hooks":[{"command":"ackbar-hook --agent=grok"}]}`), 0o644)
	if ok, _, _ := p.CheckHookConfig(tmpHome); !ok {
		t.Errorf("expected CheckHookConfig true for hooks.json")
	}
	_ = os.Remove(filepath.Join(grokDir, "hooks.json"))
	_ = os.WriteFile(filepath.Join(grokDir, "config.toml"), []byte(`hooks = "http://127.0.0.1:7777/v1/hooks/grok"`), 0o644)
	if ok, _, _ := p.CheckHookConfig(tmpHome); !ok {
		t.Errorf("expected CheckHookConfig true for config.toml")
	}
	_ = os.Remove(filepath.Join(grokDir, "config.toml"))
	_ = os.WriteFile(filepath.Join(grokDir, "config.json"), []byte(`{"hook":"http://127.0.0.1:7777/v1/hooks/grok"}`), 0o644)
	if ok, _, _ := p.CheckHookConfig(tmpHome); !ok {
		t.Errorf("expected CheckHookConfig true for config.json")
	}

	authFile := filepath.Join(grokDir, "auth.json")
	_ = os.WriteFile(authFile, []byte(`{"token":"secret"}`), 0o600)
	for _, unsafeID := range []string{"..", ".", "-flag", "../unsafe"} {
		if err := p.CleanSessionFiles(tmpHome, "/work", unsafeID); err != nil {
			t.Errorf("CleanSessionFiles(%q) err = %v", unsafeID, err)
		}
	}
	if _, err := os.Stat(authFile); err != nil {
		t.Errorf("CleanSessionFiles removed auth.json: %v", err)
	}
}

func TestPlugin_AccountsAndScanner(t *testing.T) {
	tmpHome := t.TempDir()
	p := New()

	cfgDir, err := p.SetupProfileDir(tmpHome, "work", "")
	if err != nil {
		t.Fatalf("SetupProfileDir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "hooks", "ackbar.json")); err != nil {
		t.Errorf("missing profile hooks file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "config.toml")); err != nil {
		t.Errorf("missing profile config.toml: %v", err)
	}

	env := p.AccountEnvVars(tmpHome, &agent.AgentAccount{Name: "work", ConfigDir: "~/custom-grok", Env: map[string]string{"XAI_API_KEY": "k1"}})
	if env["GROK_HOME"] != filepath.Join(tmpHome, "custom-grok") || env["ACKBAR_ACCOUNT"] != "work" || env["XAI_API_KEY"] != "k1" {
		t.Errorf("AccountEnvVars mismatch: %+v", env)
	}
	if !p.IsAccountLoggedIn(tmpHome, &agent.AgentAccount{Env: map[string]string{"XAI_API_KEY": "k1"}}) {
		t.Errorf("expected logged in with XAI_API_KEY")
	}
	if p.LoginCommand() != "grok login" {
		t.Errorf("LoginCommand = %q", p.LoginCommand())
	}
	if !p.IsValidNativeID("sess-1") || p.IsValidNativeID("default") || p.IsValidNativeID("../bad") {
		t.Errorf("IsValidNativeID mismatch")
	}
	if !p.IsIgnoredCommand("grok mcp list") || !p.IsIgnoredCommand("grok agent stdio --no-auto-update") || !p.IsIgnoredCommand("grok --single=hi") || p.IsIgnoredCommand("grok -r sess-1") {
		t.Errorf("IsIgnoredCommand mismatch")
	}
	if !p.MatchProcessCommand("grok", "grok -s 123") || p.MatchProcessCommand("grok", "ackbar-hook --agent=grok") {
		t.Errorf("MatchProcessCommand mismatch")
	}
}
