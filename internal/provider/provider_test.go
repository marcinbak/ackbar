package provider

import (
	"testing"
	"ackbar/internal/daemon"
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
}

func TestCodexProvider_ParseHook(t *testing.T) {
	p := NewCodexProvider()

	// Test PreToolUse with request_user_input
	payload := `{"session_id": "session-codex", "cwd": "/workspace", "hook_event_name": "PreToolUse", "tool_name": "request_user_input"}`
	ev, err := p.ParseHook("PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateBlocked || ev.Blocked == nil || ev.Blocked.Kind != daemon.BlockQuestion {
		t.Errorf("Expected blocked question state, got: %+v", ev)
	}
}

func TestAntigravityProvider_ParseHook(t *testing.T) {
	p := NewAntigravityProvider()

	// Test PreToolUse with ask_question
	payload := `{"conversationId": "session-agy", "workspacePaths": ["/workspace"], "toolCall": {"name": "ask_question"}}`
	ev, err := p.ParseHook("PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateBlocked || ev.Blocked == nil || ev.Blocked.Kind != daemon.BlockQuestion {
		t.Errorf("Expected blocked question state, got: %+v", ev)
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
