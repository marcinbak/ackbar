package provider

import (
	"ackbar/internal/daemon"
	"os"
	"strings"
	"testing"
)

func TestClaudeProvider_ParseHook(t *testing.T) {
	p := NewClaudeProvider()

	// Test UserPromptSubmit
	payload := `{"session_id": "session-claude", "cwd": "/workspace", "hook_event_name": "UserPromptSubmit"}`
	ev, err := p.ParseHook("UserPromptSubmit", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateWorking || ev.Activity != "Processing user prompt" {
		t.Errorf("Unexpected values: state %v, activity %s", ev.State, ev.Activity)
	}

	// Test PermissionRequest
	payload = `{"session_id": "session-claude", "cwd": "/workspace", "hook_event_name": "PermissionRequest", "requested_permission": "run command"}`
	ev, err = p.ParseHook("PermissionRequest", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateBlocked || ev.Blocked == nil || ev.Blocked.Kind != daemon.BlockPermission {
		t.Errorf("Expected blocked permission state, got: %+v", ev)
	}
	if ev.Blocked.Question != "run command" {
		t.Errorf("Expected Question 'run command', got '%s'", ev.Blocked.Question)
	}
	if len(ev.Blocked.Options) != 2 || ev.Blocked.Options[0] != "Allow" || ev.Blocked.Options[1] != "Deny" {
		t.Errorf("Expected options ['Allow', 'Deny'], got %+v", ev.Blocked.Options)
	}

	// Test PreToolUse with AskUserQuestion and options
	payload = `{"session_id": "session-claude", "cwd": "/workspace", "hook_event_name": "PreToolUse", "tool_name": "AskUserQuestion", "tool_input": "{\"questions\":[{\"question\":\"Pick environment:\",\"options\":[\"Staging\",\"Production\"]}]}"}`
	ev, err = p.ParseHook("PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateBlocked || ev.Blocked == nil || ev.Blocked.Kind != daemon.BlockQuestion {
		t.Errorf("Expected blocked question state, got: %+v", ev)
	}
	if ev.Blocked.Question != "Pick environment:" {
		t.Errorf("Expected Question 'Pick environment:', got '%s'", ev.Blocked.Question)
	}
	if len(ev.Blocked.Options) != 2 || ev.Blocked.Options[0] != "Staging" || ev.Blocked.Options[1] != "Production" {
		t.Errorf("Expected options ['Staging', 'Production'], got %+v", ev.Blocked.Options)
	}
}

func TestCodexProvider_ParseHook(t *testing.T) {
	p := NewCodexProvider()

	// Test PreToolUse with request_user_input and options
	payload := `{"session_id": "session-codex", "cwd": "/workspace", "hook_event_name": "PreToolUse", "tool_name": "request_user_input", "tool_input": "{\"question\":\"Confirm deployment?\",\"options\":[\"Yes\",\"No\"]}"}`
	ev, err := p.ParseHook("PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateBlocked || ev.Blocked == nil || ev.Blocked.Kind != daemon.BlockQuestion {
		t.Errorf("Expected blocked question state, got: %+v", ev)
	}
	if ev.Blocked.Question != "Confirm deployment?" {
		t.Errorf("Expected Question 'Confirm deployment?', got '%s'", ev.Blocked.Question)
	}
	if len(ev.Blocked.Options) != 2 || ev.Blocked.Options[0] != "Yes" || ev.Blocked.Options[1] != "No" {
		t.Errorf("Expected options ['Yes', 'No'], got %+v", ev.Blocked.Options)
	}
}

func TestAntigravityProvider_ParseHook(t *testing.T) {
	p := NewAntigravityProvider()

	// Test PreToolUse with ask_question and structured questions/options
	payload := `{"conversationId": "session-agy", "workspacePaths": ["/workspace"], "toolCall": {"name": "ask_question", "args": {"questions": [{"question": "Choose framework", "options": ["React", "Vue", "Svelte"]}]}}}`
	ev, err := p.ParseHook("PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateBlocked || ev.Blocked == nil || ev.Blocked.Kind != daemon.BlockQuestion {
		t.Errorf("Expected blocked question state, got: %+v", ev)
	}
	if ev.Blocked.Question != "Choose framework" {
		t.Errorf("Expected Question 'Choose framework', got '%s'", ev.Blocked.Question)
	}
	if len(ev.Blocked.Options) != 3 || ev.Blocked.Options[0] != "React" || ev.Blocked.Options[1] != "Vue" || ev.Blocked.Options[2] != "Svelte" {
		t.Errorf("Expected options ['React', 'Vue', 'Svelte'], got %+v", ev.Blocked.Options)
	}

	// Test PreToolUse with ask_permission
	payloadPerm := `{"conversationId": "session-agy", "workspacePaths": ["/workspace"], "toolCall": {"name": "ask_permission", "args": {"command": "rm -rf /tmp/cache"}}}`
	evPerm, err := p.ParseHook("PreToolUse", []byte(payloadPerm))
	if err != nil {
		t.Fatalf("ParseHook ask_permission failed: %v", err)
	}
	if evPerm.State != daemon.StateBlocked || evPerm.Blocked == nil || evPerm.Blocked.Kind != daemon.BlockPermission {
		t.Errorf("Expected blocked permission state, got: %+v", evPerm)
	}
	if !strings.Contains(evPerm.Blocked.Question, "rm -rf /tmp/cache") {
		t.Errorf("Expected question to contain command, got '%s'", evPerm.Blocked.Question)
	}
	if len(evPerm.Blocked.Options) != 2 || evPerm.Blocked.Options[0] != "Allow" || evPerm.Blocked.Options[1] != "Deny" {
		t.Errorf("Expected options ['Allow', 'Deny'], got %+v", evPerm.Blocked.Options)
	}
}

func TestProviderDiscovery(t *testing.T) {
	providers := []daemon.Provider{
		NewClaudeProvider(),
		NewCodexProvider(),
		NewAntigravityProvider(),
	}

	for _, p := range providers {
		_ = p.IsInstalled()
		_, setupCmd, err := p.CheckHookConfig()
		if err != nil {
			t.Errorf("CheckHookConfig for %s returned error: %v", p.Agent(), err)
		}
		if setupCmd == "" {
			t.Errorf("Expected setupCmd for %s", p.Agent())
		}
	}
}

func TestAntigravityDiscoveryInLocalBin(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-agy-localbin-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	p := NewAntigravityProvider()

	// 1. When binary does not exist
	if p.IsInstalled() {
		// If agy is globally on host PATH it could be true, but if not it should be false
	}

	// 2. Create ~/.local/bin/agy mock executable
	localBin := tmpHome + "/.local/bin"
	_ = os.MkdirAll(localBin, 0755)
	agyMock := localBin + "/agy"
	_ = os.WriteFile(agyMock, []byte("#!/bin/sh\necho agy\n"), 0755)

	if !p.IsInstalled() {
		t.Errorf("Expected Antigravity to be detected when ~/.local/bin/agy exists")
	}

	// 3. Test hook detection in ~/.antigravity/config/hooks.json
	hooksDir := tmpHome + "/.antigravity/config"
	_ = os.MkdirAll(hooksDir, 0755)
	_ = os.WriteFile(hooksDir+"/hooks.json", []byte(`{"hooks":{"PreInvocation":[{"command":"ackbar-hook antigravity"}]}}`), 0644)

	installed, _, err := p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig failed: %v", err)
	}
	if !installed {
		t.Errorf("Expected hook config in ~/.antigravity/config/hooks.json to be detected")
	}
}
