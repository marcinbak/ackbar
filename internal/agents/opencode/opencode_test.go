package opencode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
)

func TestPlugin_IdentityCapabilitiesLaunchHooksAndScanner(t *testing.T) {
	p := New()
	if p.Key() != "opencode" || p.DisplayName() != "OpenCode" || p.BrandColor() != "#6366F1" {
		t.Fatalf("identity mismatch: %q %q %q", p.Key(), p.DisplayName(), p.BrandColor())
	}
	if len(p.Aliases()) != 1 || p.Aliases()[0] != "open-code" {
		t.Fatalf("Aliases = %v", p.Aliases())
	}
	if len(p.BinaryNames()) != 2 || p.BinaryNames()[0] != "opencode" || p.BinaryNames()[1] != "open-code" {
		t.Fatalf("BinaryNames = %v", p.BinaryNames())
	}
	if !strings.Contains(p.IconSVG(), "<svg") {
		t.Fatal("missing IconSVG")
	}
	if _, ok := any(p).(agent.Provisioner); ok {
		t.Fatal("opencode plugin must not implement agent.Provisioner")
	}
	if _, ok := any(p).(agent.InstallationDetector); !ok {
		t.Fatal("opencode plugin must implement agent.InstallationDetector")
	}
	caps := p.Capabilities()
	if !caps.SupportsTmux || caps.SupportsHeadless || !caps.SupportsHookShim || caps.SupportsNativeHooks || caps.SupportsMultiAccount || !caps.SupportsTranscript || caps.NativeIDStrategy != agent.NativeIDLazyAdopt {
		t.Fatalf("capabilities mismatch: %+v", caps)
	}

	if p.SpawnCommand("") != "opencode" || p.ResumeCommand("") != "opencode" {
		t.Error("empty spawn/resume mismatch")
	}
	if p.ResumeCommand("valid-session-123") != "opencode resume valid-session-123" {
		t.Errorf("ResumeCommand valid = %q", p.ResumeCommand("valid-session-123"))
	}
	for _, bad := range []string{"../unsafe/path", "session\nrm -rf /", "session with spaces"} {
		if cmd := p.ResumeCommand(bad); cmd != "opencode" {
			t.Errorf("ResumeCommand(%q) = %q, want 'opencode'", bad, cmd)
		}
	}

	spec, err := p.BuildLaunchSpec(agent.LaunchInput{Prompt: "hello", Env: map[string]string{"K": "V"}})
	if err != nil || spec.CommandLine != "opencode" || spec.PromptInCommand || spec.Env["K"] != "V" {
		t.Fatalf("BuildLaunchSpec spawn = %+v err=%v", spec, err)
	}
	resSpec, err := p.BuildLaunchSpec(agent.LaunchInput{Resume: true, NativeID: "sess-1"})
	if err != nil || resSpec.CommandLine != "opencode resume sess-1" || resSpec.PromptInCommand {
		t.Fatalf("BuildLaunchSpec resume = %+v err=%v", resSpec, err)
	}

	tmpHome := t.TempDir()
	if p.LoginCommand() != "opencode login" {
		t.Errorf("LoginCommand = %q", p.LoginCommand())
	}
	cfgDir, err := p.SetupProfileDir(tmpHome, "work", "")
	if err != nil || cfgDir != filepath.Join(tmpHome, ".opencode-profiles", "work") {
		t.Fatalf("SetupProfileDir = (%q, %v)", cfgDir, err)
	}

	configured, setupCmd, err := p.CheckHookConfig(tmpHome)
	if err != nil || configured || setupCmd != "ackbar-hook --agent=opencode" {
		t.Fatalf("initial CheckHookConfig = (%v, %q, %v)", configured, setupCmd, err)
	}
	ocDir := filepath.Join(tmpHome, ".opencode")
	_ = os.MkdirAll(ocDir, 0o755)
	if !p.IsInstalled(tmpHome) {
		t.Error("expected IsInstalled true with ~/.opencode dir")
	}
	_ = os.WriteFile(filepath.Join(ocDir, "hooks.json"), []byte(`{"hooks":[{"command":"ackbar-hook --agent=opencode"}]}`), 0o644)
	if ok, _, _ := p.CheckHookConfig(tmpHome); !ok {
		t.Error("expected CheckHookConfig true for hooks.json")
	}
	_ = os.Remove(filepath.Join(ocDir, "hooks.json"))
	_ = os.WriteFile(filepath.Join(ocDir, "config.json"), []byte(`{"hook":"http://127.0.0.1:7777/v1/hooks/opencode"}`), 0o644)
	if ok, _, _ := p.CheckHookConfig(tmpHome); !ok {
		t.Error("expected CheckHookConfig true for config.json")
	}
	_ = os.Remove(filepath.Join(ocDir, "config.json"))
	_ = os.WriteFile(filepath.Join(ocDir, "config.toml"), []byte(`hooks = "http://127.0.0.1:7777/v1/hooks/opencode"`), 0o644)
	if ok, _, _ := p.CheckHookConfig(tmpHome); !ok {
		t.Error("expected CheckHookConfig true for config.toml")
	}

	evPrompt, err := p.ParseHook("", []byte(`{"session_id":"oc-1","event":"UserPromptSubmit","cwd":"/work"}`))
	if err != nil || evPrompt.State != agent.StateWorking || evPrompt.Activity != "Processing user prompt" {
		t.Fatalf("prompt hook = %+v err=%v", evPrompt, err)
	}
	evTool, err := p.ParseHook("", []byte(`{"session_id":"oc-1","event":"PreToolUse","tool_name":"bash"}`))
	if err != nil || evTool.Activity != "Executing tool: bash" {
		t.Fatalf("tool hook = %+v err=%v", evTool, err)
	}
	evPerm, err := p.ParseHook("", []byte(`{"session_id":"oc-1","event":"ApprovalRequest","reason":"Run migration"}`))
	if err != nil || evPerm.State != agent.StateBlocked || evPerm.Blocked.Kind != agent.BlockPermission {
		t.Fatalf("perm hook = %+v err=%v", evPerm, err)
	}
	evStop, err := p.ParseHook("", []byte(`{"session_id":"oc-1","event":"Stop"}`))
	if err != nil || evStop.State != agent.StateIdle {
		t.Fatalf("stop hook = %+v err=%v", evStop, err)
	}
	if _, err := p.ParseHook("", []byte(`{bad json`)); err == nil {
		t.Error("expected error on corrupt JSON")
	}
	if string(p.HookAckPayload("PreToolUse")) != "{}" {
		t.Error("HookAckPayload mismatch")
	}

	if !p.IsValidNativeID("oc-1") || !p.IsValidNativeID("default") || p.IsValidNativeID("../bad") {
		t.Error("IsValidNativeID mismatch")
	}
	if !p.IsIgnoredCommand("open-code models") || !p.IsIgnoredCommand("opencode login") || p.IsIgnoredCommand("opencode") {
		t.Error("IsIgnoredCommand mismatch")
	}
	if !p.MatchProcessCommand("open-code", "open-code") || p.MatchProcessCommand("opencode", "ackbar-hook --agent=opencode") {
		t.Error("MatchProcessCommand mismatch")
	}
	if limited, _, _ := p.DetectRateLimit("rate limit reached · retry in 5m", time.Now()); !limited {
		t.Error("expected DetectRateLimit true")
	}
	if p.InspectStatus(context.Background(), &agent.Session{Agent: "opencode", State: agent.StateEnded}) {
		t.Error("expected InspectStatus false for ended session")
	}
}
