package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/daemon"
)

func TestClaudeProvider_ParseHook(t *testing.T) {
	p := NewClaudeProvider()
	validUUID := "11111111-2222-3333-4444-555555555555"

	// Test UserPromptSubmit
	payload := `{"session_id": "` + validUUID + `", "cwd": "/workspace", "hook_event_name": "UserPromptSubmit"}`
	ev, err := p.ParseHook("UserPromptSubmit", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateWorking || ev.Activity != "Processing user prompt" {
		t.Errorf("Unexpected values: state %v, activity %s", ev.State, ev.Activity)
	}

	// Test PermissionRequest
	payload = `{"session_id": "` + validUUID + `", "cwd": "/workspace", "hook_event_name": "PermissionRequest", "requested_permission": "run command"}`
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
	payload = `{"session_id": "` + validUUID + `", "cwd": "/workspace", "hook_event_name": "PreToolUse", "tool_name": "AskUserQuestion", "tool_input": "{\"questions\":[{\"question\":\"Pick environment:\",\"options\":[\"Staging\",\"Production\"]}]}"}`
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

	// Test Notification with generic prompt (should NOT block, should be Idle)
	payload = `{"session_id": "` + validUUID + `", "cwd": "/workspace", "hook_event_name": "Notification", "notification_type": "user_prompt"}`
	ev, err = p.ParseHook("Notification", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateIdle {
		t.Errorf("Generic prompt notification should be Idle, got: %+v", ev)
	}

	// Test Notification with idle_prompt (should be Idle)
	payload = `{"session_id": "` + validUUID + `", "cwd": "/workspace", "hook_event_name": "Notification", "notification_type": "idle_prompt"}`
	ev, err = p.ParseHook("Notification", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateIdle || ev.Activity != "Awaiting user prompt" {
		t.Errorf("idle_prompt notification should be StateIdle, got state %v, activity %s", ev.State, ev.Activity)
	}

	// Test Notification with explicit permission_prompt (should block)
	payload = `{"session_id": "` + validUUID + `", "cwd": "/workspace", "hook_event_name": "Notification", "notification_type": "permission_prompt", "requested_permission": "run bash"}`
	ev, err = p.ParseHook("Notification", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateBlocked || ev.Blocked == nil || ev.Blocked.Kind != daemon.BlockPermission {
		t.Errorf("Expected blocked permission state, got: %+v", ev)
	}

	// Test Stop event (sets StateIdle)
	payload = `{"session_id": "` + validUUID + `", "cwd": "/workspace", "hook_event_name": "Stop"}`
	ev, err = p.ParseHook("Stop", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.State != daemon.StateIdle || ev.Activity != "Awaiting user prompt" {
		t.Errorf("Expected idle state on Stop, got state %v, activity %s", ev.State, ev.Activity)
	}

	// Test IsSidechain (snake_case) should be ignored
	payload = `{"session_id": "` + validUUID + `", "is_sidechain": true, "hook_event_name": "PreToolUse", "tool_name": "Bash"}`
	ev, err = p.ParseHook("PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev != nil {
		t.Errorf("Expected nil event for is_sidechain: true, got %+v", ev)
	}

	// Test IsSidechain (camelCase) should be ignored
	payload = `{"session_id": "` + validUUID + `", "isSidechain": true, "hook_event_name": "PreToolUse", "tool_name": "Bash"}`
	ev, err = p.ParseHook("PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev != nil {
		t.Errorf("Expected nil event for isSidechain: true, got %+v", ev)
	}

	// Test AgentID (subagent) should be ignored
	payload = `{"session_id": "` + validUUID + `", "agent_id": "subagent-123", "hook_event_name": "PreToolUse", "tool_name": "Bash"}`
	ev, err = p.ParseHook("PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev != nil {
		t.Errorf("Expected nil event for agent_id: 'subagent-123', got %+v", ev)
	}

	// Test non-UUID session IDs (e.g. "test", "default", empty) should be ignored
	for _, nonUUID := range []string{"test", "default", "mock-session", "", "12345"} {
		payload = `{"session_id": "` + nonUUID + `", "cwd": "/workspace", "hook_event_name": "PreToolUse", "tool_name": "Bash"}`
		ev, err = p.ParseHook("PreToolUse", []byte(payload))
		if err != nil {
			t.Fatalf("ParseHook failed for non-UUID %q: %v", nonUUID, err)
		}
		if ev != nil {
			t.Errorf("Expected nil event for non-UUID session_id %q, got %+v", nonUUID, ev)
		}
	}
}

func TestClaudeProvider_GetResumeCommand(t *testing.T) {
	p := NewClaudeProvider()

	// Valid UUID should return claude --resume <uuid>
	validUUID := "61bfc5e5-1e22-4b01-87d1-219a626336ee"
	if cmd := p.GetResumeCommand(validUUID); cmd != "claude --resume "+validUUID {
		t.Errorf("Expected 'claude --resume %s', got %q", validUUID, cmd)
	}

	// Invalid / empty / non-UUID should return empty string (never bare "claude")
	for _, invalid := range []string{"test", "default", "", "proc-123", "not-a-uuid"} {
		if cmd := p.GetResumeCommand(invalid); cmd != "" {
			t.Errorf("Expected empty resume command for invalid ID %q, got %q", invalid, cmd)
		}
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

	// Test postinvocation (should set StateIdle)
	payloadPost := `{"conversationId": "session-agy", "workspacePaths": ["/workspace"]}`
	evPost, err := p.ParseHook("postinvocation", []byte(payloadPost))
	if err != nil {
		t.Fatalf("ParseHook postinvocation failed: %v", err)
	}
	if evPost.State != daemon.StateIdle || evPost.Activity != "Awaiting user prompt" {
		t.Errorf("Expected StateIdle on postinvocation, got state %v, activity %s", evPost.State, evPost.Activity)
	}
}

func TestProviderDiscovery(t *testing.T) {
	providers := []daemon.Provider{
		NewClaudeProvider(),
		NewCodexProvider(),
		NewAntigravityProvider(),
		NewGrokProvider(),
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

func TestClaudeCheckHookConfig_OnlySettingsJson(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-claude-hookconfig-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	p := NewClaudeProvider()

	// 1. When no config files exist
	installed, _, _ := p.CheckHookConfig()
	if installed {
		t.Errorf("Expected false when no config exists")
	}

	// 2. When hooks exist ONLY in legacy ~/.claude.json (should be ignored)
	legacyFile := filepath.Join(tmpHome, ".claude.json")
	_ = os.WriteFile(legacyFile, []byte(`{"hooks":{"UserPromptSubmit":[{"command":"ackbar-hook claude-code UserPromptSubmit"}]}}`), 0644)
	installed, _, _ = p.CheckHookConfig()
	if installed {
		t.Errorf("Expected false when hooks exist only in legacy ~/.claude.json")
	}

	// 3. When hooks exist in canonical ~/.claude/settings.json
	claudeDir := filepath.Join(tmpHome, ".claude")
	_ = os.MkdirAll(claudeDir, 0755)
	settingsFile := filepath.Join(claudeDir, "settings.json")
	_ = os.WriteFile(settingsFile, []byte(`{"hooks":{"UserPromptSubmit":[{"command":"ackbar-hook claude-code UserPromptSubmit"}]}}`), 0644)
	installed, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig error: %v", err)
	}
	if !installed {
		t.Errorf("Expected true when hooks exist in ~/.claude/settings.json")
	}
}

func TestProviderInterfaceConformance(t *testing.T) {
	providers := []daemon.Provider{
		NewClaudeProvider(),
		NewAntigravityProvider(),
		NewCodexProvider(),
		NewGrokProvider(),
		NewOpenCodeProvider(),
	}

	testUUID := "12345678-1234-1234-1234-123456789abc"

	for _, p := range providers {
		agent := p.Agent()
		if agent == "" {
			t.Errorf("Provider %T returned empty Agent()", p)
		}
		if p.DisplayName() == "" {
			t.Errorf("Provider %s returned empty DisplayName()", agent)
		}
		if p.BrandColor() == "" || !strings.HasPrefix(p.BrandColor(), "#") {
			t.Errorf("Provider %s returned invalid BrandColor(): %s", agent, p.BrandColor())
		}
		if p.IconSVG() == "" || !strings.Contains(p.IconSVG(), "<svg") {
			t.Errorf("Provider %s returned invalid IconSVG()", agent)
		}
		if len(p.ProcessNames()) == 0 {
			t.Errorf("Provider %s returned empty ProcessNames()", agent)
		}

		spawnCmd := p.GetSpawnCommand(testUUID)
		if spawnCmd == "" {
			t.Errorf("Provider %s returned empty GetSpawnCommand", agent)
		}

		resumeCmd := p.GetResumeCommand(testUUID)
		if resumeCmd == "" {
			t.Errorf("Provider %s returned empty GetResumeCommand", agent)
		}

		// Test CleanSessionFiles with empty string does not panic
		if err := p.CleanSessionFiles("/tmp", "/workspace", ""); err != nil {
			t.Errorf("Provider %s CleanSessionFiles returned error on empty id: %v", agent, err)
		}
	}
}

func TestProviderCapabilityConformance(t *testing.T) {
	claude := NewClaudeProvider()
	antigravity := NewAntigravityProvider()
	codex := NewCodexProvider()
	grok := NewGrokProvider()
	opencode := NewOpenCodeProvider()

	// 1. Verify Core Role Interfaces for all providers
	allProviders := []any{claude, antigravity, codex, grok, opencode}
	for _, p := range allProviders {
		if _, ok := p.(daemon.AgentIdentity); !ok {
			t.Errorf("Provider %T must implement daemon.AgentIdentity", p)
		}
		if _, ok := p.(daemon.ProcessDetector); !ok {
			t.Errorf("Provider %T must implement daemon.ProcessDetector", p)
		}
		if _, ok := p.(daemon.HookParser); !ok {
			t.Errorf("Provider %T must implement daemon.HookParser", p)
		}
		if _, ok := p.(daemon.SessionLifecycle); !ok {
			t.Errorf("Provider %T must implement daemon.SessionLifecycle", p)
		}
		if _, ok := p.(daemon.TranscriptReader); !ok {
			t.Errorf("Provider %T must implement daemon.TranscriptReader", p)
		}
		if _, ok := p.(daemon.Provider); !ok {
			t.Errorf("Provider %T must implement daemon.Provider", p)
		}
	}

	// 2. Claude & Antigravity support FullProvider (StatusInspector + SubagentDiscoverer)
	if _, ok := any(claude).(daemon.StatusInspector); !ok {
		t.Errorf("ClaudeProvider should implement daemon.StatusInspector")
	}
	if _, ok := any(claude).(daemon.SubagentDiscoverer); !ok {
		t.Errorf("ClaudeProvider should implement daemon.SubagentDiscoverer")
	}
	if _, ok := any(claude).(daemon.FullProvider); !ok {
		t.Errorf("ClaudeProvider should implement daemon.FullProvider")
	}

	if _, ok := any(antigravity).(daemon.StatusInspector); !ok {
		t.Errorf("AntigravityProvider should implement daemon.StatusInspector")
	}
	if _, ok := any(antigravity).(daemon.SubagentDiscoverer); !ok {
		t.Errorf("AntigravityProvider should implement daemon.SubagentDiscoverer")
	}
	if _, ok := any(antigravity).(daemon.FullProvider); !ok {
		t.Errorf("AntigravityProvider should implement daemon.FullProvider")
	}

	// 3. Codex implements StatusInspector, but not SubagentDiscoverer
	if _, ok := any(codex).(daemon.StatusInspector); !ok {
		t.Errorf("CodexProvider should implement daemon.StatusInspector")
	}
	if _, ok := any(codex).(daemon.SubagentDiscoverer); ok {
		t.Errorf("CodexProvider should not implement daemon.SubagentDiscoverer")
	}
	if _, ok := any(codex).(daemon.FullProvider); ok {
		t.Errorf("CodexProvider should not implement daemon.FullProvider")
	}

	// 4. Grok implements StatusInspector, but not SubagentDiscoverer
	if _, ok := any(grok).(daemon.StatusInspector); !ok {
		t.Errorf("GrokProvider should implement daemon.StatusInspector")
	}
	if _, ok := any(grok).(daemon.SubagentDiscoverer); ok {
		t.Errorf("GrokProvider should not implement daemon.SubagentDiscoverer")
	}
	if _, ok := any(grok).(daemon.FullProvider); ok {
		t.Errorf("GrokProvider should not implement daemon.FullProvider")
	}

	// 5. OpenCode implements StatusInspector, but not SubagentDiscoverer
	if _, ok := any(opencode).(daemon.StatusInspector); !ok {
		t.Errorf("OpenCodeProvider should implement daemon.StatusInspector")
	}
	if _, ok := any(opencode).(daemon.SubagentDiscoverer); ok {
		t.Errorf("OpenCodeProvider should not implement daemon.SubagentDiscoverer")
	}
	if _, ok := any(opencode).(daemon.FullProvider); ok {
		t.Errorf("OpenCodeProvider should not implement daemon.FullProvider")
	}
}

func TestClaudeProvider_ExtractTranscript_CoalescesToolCalls(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-provider-claude-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	sessionID := "11111111-2222-3333-4444-555555555555"
	cwd := "/Users/dev4u/Work/App"
	encodedCwd := strings.ReplaceAll(cwd, "/", "-")
	logDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	_ = os.MkdirAll(logDir, 0755)

	jsonlContent := `{"type":"user","message":{"role":"user","content":"Run linter and tests"},"timestamp":"2026-08-20T18:28:20.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"npm run lint"}}]},"timestamp":"2026-08-20T18:28:21.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"npm test"}}]},"timestamp":"2026-08-20T18:28:22.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Linter and tests passed."}]},"timestamp":"2026-08-20T18:28:23.000Z"}
`
	logFile := filepath.Join(logDir, sessionID+".jsonl")
	if err := os.WriteFile(logFile, []byte(jsonlContent), 0644); err != nil {
		t.Fatalf("Failed to write mock claude transcript: %v", err)
	}

	p := NewClaudeProvider()
	msgs, err := p.ExtractTranscript(tmpHome, cwd, sessionID)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("Expected 2 coalesced messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "Run linter and tests" {
		t.Errorf("Unexpected user message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Content, "passed") {
		t.Errorf("Unexpected assistant message: %+v", msgs[1])
	}
	if len(msgs[1].ToolCalls) != 2 {
		t.Fatalf("Expected 2 tool calls in assistant turn, got %d: %v", len(msgs[1].ToolCalls), msgs[1].ToolCalls)
	}
	if !strings.Contains(msgs[1].ToolCalls[0], "Bash: npm run lint") {
		t.Errorf("Expected first tool call 'Bash: npm run lint', got %q", msgs[1].ToolCalls[0])
	}
	if !strings.Contains(msgs[1].ToolCalls[1], "Bash: npm test") {
		t.Errorf("Expected second tool call 'Bash: npm test', got %q", msgs[1].ToolCalls[1])
	}
}

func TestProvider_ListSubagents_ClaudeAndAntigravity(t *testing.T) {
	tmpHome := t.TempDir()
	sessionID := "16bef887-9a34-43e5-897f-49c514e6bf6a"
	cwd := "/test/workspace"
	slug := "-test-workspace"

	// 1. Create subagents/ on disk for Claude
	subDir := filepath.Join(tmpHome, ".claude", "projects", slug, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	metaContent := `{"name":"recovery-agent","agentType":"task-runner","description":"Running recovery"}`
	if err := os.WriteFile(filepath.Join(subDir, "agent-rec-1.meta.json"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 2. Create teams/ config for Claude
	teamDir := filepath.Join(tmpHome, ".claude", "teams", "session-16bef887")
	if err := os.MkdirAll(teamDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	teamCfg := `{
		"name": "session-16bef887",
		"leadSessionId": "16bef887-9a34-43e5-897f-49c514e6bf6a",
		"members": [
			{"name": "team-lead", "agentType": "team-lead"},
			{"name": "planner-997", "agentType": "Plan", "prompt": "Plan task"}
		]
	}`
	if err := os.WriteFile(filepath.Join(teamDir, "config.json"), []byte(teamCfg), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Test Claude ListSubagents
	cp := NewClaudeProvider()
	subs, err := cp.ListSubagents(tmpHome, cwd, sessionID)
	if err != nil {
		t.Fatalf("Claude ListSubagents failed: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("Expected 2 subagents (1 disk + 1 team), got %d: %+v", len(subs), subs)
	}

	// Test path traversal sanitization
	traversalSubs, err := cp.ListSubagents(tmpHome, cwd, "../../../etc/passwd")
	if err != nil {
		t.Fatalf("Expected nil error for traversal, got %v", err)
	}
	if len(traversalSubs) != 0 {
		t.Errorf("Expected 0 subagents for path traversal, got %d", len(traversalSubs))
	}

	// 3. Create Antigravity subagent on disk
	agDir := filepath.Join(tmpHome, ".gemini", "antigravity", "brain", sessionID, ".system_generated", "subagents")
	if err := os.MkdirAll(agDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	agMeta := `{
		"conversationId": "sub-ag-1",
		"subagentDescriptor": {"typeName": "reviewer", "role": "Code Reviewer"},
		"state": "RUNNING"
	}`
	if err := os.WriteFile(filepath.Join(agDir, "sub-ag-1.json"), []byte(agMeta), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ap := NewAntigravityProvider()
	agSubs, err := ap.ListSubagents(tmpHome, cwd, sessionID)
	if err != nil {
		t.Fatalf("Antigravity ListSubagents failed: %v", err)
	}
	if len(agSubs) != 1 {
		t.Fatalf("Expected 1 antigravity subagent, got %d", len(agSubs))
	}
	if agSubs[0].Name != "reviewer" || agSubs[0].Role != "Code Reviewer" || agSubs[0].State != "running" {
		t.Errorf("Unexpected antigravity subagent: %+v", agSubs[0])
	}
}

func TestClaudeProvider_ListSubagents_StopReasonAndInactivity(t *testing.T) {
	tmpHome := t.TempDir()
	sessionID := "22222222-3333-4444-5555-666666666666"
	cwd := "/test/workspace"
	slug := "-test-workspace"

	subDir := filepath.Join(tmpHome, ".claude", "projects", slug, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// 1. Subagent with companion .jsonl ending in attachment lines after an "end_turn" message
	meta1 := `{"name":"subagent-1","agentType":"task-runner","description":"Completed task"}`
	if err := os.WriteFile(filepath.Join(subDir, "agent-1.meta.json"), []byte(meta1), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	jsonl1 := `{"type":"assistant","message":{"stop_reason":"end_turn"}}` + "\n" +
		`{"type":"attachment","payload":{"reminder":"total_tokens_reminder"}}` + "\n"
	if err := os.WriteFile(filepath.Join(subDir, "agent-1.jsonl"), []byte(jsonl1), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 2. Subagent whose file is older than 30 minutes (stale running subagent)
	meta2 := `{"name":"subagent-2","agentType":"task-runner","description":"Stale task"}`
	if err := os.WriteFile(filepath.Join(subDir, "agent-2.meta.json"), []byte(meta2), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	jsonl2 := `{"type":"assistant","message":{"stop_reason":null}}` + "\n"
	jsonl2Path := filepath.Join(subDir, "agent-2.jsonl")
	if err := os.WriteFile(jsonl2Path, []byte(jsonl2), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	oldTime := time.Now().Add(-45 * time.Minute)
	if err := os.Chtimes(jsonl2Path, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}

	// 3. Stale team config older than 30 minutes
	teamDir := filepath.Join(tmpHome, ".claude", "teams", "session-22222222")
	if err := os.MkdirAll(teamDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	teamCfg := `{
		"name": "session-22222222",
		"leadSessionId": "22222222-3333-4444-5555-666666666666",
		"members": [
			{"name": "team-lead", "agentType": "team-lead"},
			{"name": "worker-1", "agentType": "Worker"}
		]
	}`
	teamCfgPath := filepath.Join(teamDir, "config.json")
	if err := os.WriteFile(teamCfgPath, []byte(teamCfg), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Chtimes(teamCfgPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}

	cp := NewClaudeProvider()
	subs, err := cp.ListSubagents(tmpHome, cwd, sessionID)
	if err != nil {
		t.Fatalf("ListSubagents failed: %v", err)
	}

	if len(subs) != 3 {
		t.Fatalf("Expected 3 subagents, got %d: %+v", len(subs), subs)
	}

	for _, sub := range subs {
		if sub.State != "completed" {
			t.Errorf("Expected subagent %s to have state 'completed', got %q", sub.Name, sub.State)
		}
	}
}

func TestCodexCheckHookConfig(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-codex-hook-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	p := NewCodexProvider()

	// 1. When no config exists
	configured, _, err := p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if configured {
		t.Errorf("Expected false when no hook config exists")
	}

	// 2. When ~/.codex/hooks.json has ackbar-hook configured
	codexDir := filepath.Join(tmpHome, ".codex")
	_ = os.MkdirAll(codexDir, 0755)
	hooksContent := `{
		"hooks": {
			"UserPromptSubmit": [{
				"hooks": [{"type": "command", "command": "ackbar-hook --agent=codex"}]
			}]
		}
	}`
	_ = os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(hooksContent), 0644)

	configured, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if !configured {
		t.Errorf("Expected true when hooks.json contains ackbar-hook")
	}

	// 3. Fallback when hooks.json is removed but config.toml exists
	_ = os.Remove(filepath.Join(codexDir, "hooks.json"))
	configToml := `hooks = "http://127.0.0.1:7777/v1/hooks/codex"`
	_ = os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(configToml), 0644)

	configured, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if !configured {
		t.Errorf("Expected true when config.toml contains 127.0.0.1:7777")
	}
}

func TestCodexResolveSessionTitle(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-codex-title-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	p := NewCodexProvider()

	// 1. With non-existent index
	if title := p.ResolveSessionTitle("/tmp", "session-123"); title != "" {
		t.Errorf("Expected empty title when index does not exist, got %q", title)
	}

	// 2. Populate session_index.jsonl with initial and updated thread names
	codexDir := filepath.Join(tmpHome, ".codex")
	_ = os.MkdirAll(codexDir, 0755)
	indexLines := `{"id":"session-123","thread_name":"Build responsive fleet UI","updated_at":"2026-09-28T09:00:00Z"}` + "\n" +
		`{"id":"session-456","thread_name":"Implement auth middleware","updated_at":"2026-09-28T10:00:00Z"}` + "\n" +
		`{"id":"session-123","thread_name":"Build responsive fleet UI - Updated","updated_at":"2026-09-28T10:30:00Z"}` + "\n"
	_ = os.WriteFile(filepath.Join(codexDir, "session_index.jsonl"), []byte(indexLines), 0644)

	if title := p.ResolveSessionTitle("/tmp", "session-123"); title != "Build responsive fleet UI - Updated" {
		t.Errorf("Expected 'Build responsive fleet UI - Updated', got %q", title)
	}
	if title := p.ResolveSessionTitle("/tmp", "session-456"); title != "Implement auth middleware" {
		t.Errorf("Expected 'Implement auth middleware', got %q", title)
	}
	if title := p.ResolveSessionTitle("/tmp", "session-unknown"); title != "" {
		t.Errorf("Expected empty title for unknown session, got %q", title)
	}
}

func TestCodexExtractTranscriptAndMetadata(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-codex-transcript-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	sessionID := "019fa2cd-7d2b-7510-8940-e80ae59e2757"
	sessDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "09", "28")
	_ = os.MkdirAll(sessDir, 0755)

	logLines := []string{
		`{"timestamp":"2026-09-28T09:00:00Z","ordinal":0,"type":"session_meta","payload":{"id":"` + sessionID + `","cli_version":"0.150.0","provenance":{"model":"gpt-5.6-terra"}}}`,
		`{"timestamp":"2026-09-28T09:00:01Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"System prompt"}]}}`,
		`{"timestamp":"2026-09-28T09:00:02Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n/work\n</environment_context>"}]}}`,
		`{"timestamp":"2026-09-28T09:00:03Z","ordinal":3,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Refactor the database queries"}]}}`,
		`{"timestamp":"2026-09-28T09:00:04Z","ordinal":4,"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"git status"}}`,
		`{"timestamp":"2026-09-28T09:00:05Z","ordinal":5,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I analyzed git status and refactored the queries."}]}}`,
		`{"timestamp":"2026-09-28T09:00:06Z","ordinal":6,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":50000},"model_context_window":200000},"rate_limits":{"primary":{"used_percent":25.0}}}}`,
	}

	logPath := filepath.Join(sessDir, fmt.Sprintf("rollout-2026-09-28T09-00-00-%s.jsonl", sessionID))
	_ = os.WriteFile(logPath, []byte(strings.Join(logLines, "\n")), 0644)

	p := NewCodexProvider()

	// 1. Test ExtractTranscript
	msgs, err := p.ExtractTranscript(tmpHome, "/work", sessionID)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("Expected 2 transcript messages (1 user, 1 assistant), got %d: %+v", len(msgs), msgs)
	}

	if msgs[0].Role != "user" || msgs[0].Content != "Refactor the database queries" {
		t.Errorf("Unexpected user message: %+v", msgs[0])
	}

	if msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Content, "refactored the queries") {
		t.Errorf("Unexpected assistant message: %+v", msgs[1])
	}
	if len(msgs[1].ToolCalls) != 1 || !strings.Contains(msgs[1].ToolCalls[0], "exec") {
		t.Errorf("Expected tool call attached to assistant, got: %+v", msgs[1].ToolCalls)
	}

	// 2. Test ReadSessionMetadata
	meta := p.ReadSessionMetadata("/work", sessionID)
	if meta == nil {
		t.Fatalf("Expected non-nil SessionMeta")
	}
	if meta.FirstPrompt != "Refactor the database queries" {
		t.Errorf("Expected FirstPrompt 'Refactor the database queries', got %q", meta.FirstPrompt)
	}
	if meta.Version != "0.150.0" {
		t.Errorf("Expected Version '0.150.0', got %q", meta.Version)
	}
	if meta.ContextPct != 25 {
		t.Errorf("Expected ContextPct 25, got %d", meta.ContextPct)
	}
	if meta.LastMessageAt.IsZero() {
		t.Errorf("Expected non-zero LastMessageAt")
	}
}

func TestGrokProvider_ParseHook(t *testing.T) {
	p := NewGrokProvider()

	// 1. UserPromptSubmit / prompt
	promptPayload := `{"session_id": "grok-sess-1", "event": "UserPromptSubmit", "prompt": "Fix unit tests", "cwd": "/repo"}`
	ev, err := p.ParseHook("", []byte(promptPayload))
	if err != nil {
		t.Fatalf("ParseHook failed: %v", err)
	}
	if ev.Agent != "grok" || ev.State != daemon.StateWorking || ev.NativeID != "grok-sess-1" || !strings.Contains(ev.Activity, "Fix unit tests") {
		t.Errorf("Unexpected prompt event: %+v", ev)
	}

	// 2. PreToolUse
	toolPayload := `{"session_id": "grok-sess-1", "event": "PreToolUse", "name": "bash"}`
	evTool, err := p.ParseHook("", []byte(toolPayload))
	if err != nil {
		t.Fatalf("ParseHook tool failed: %v", err)
	}
	if evTool.State != daemon.StateWorking || evTool.Activity != "Running bash" {
		t.Errorf("Unexpected tool event: %+v", evTool)
	}

	// 3. ApprovalRequest
	permPayload := `{"session_id": "grok-sess-1", "event": "ApprovalRequest", "reason": "Run sudo command"}`
	evPerm, err := p.ParseHook("", []byte(permPayload))
	if err != nil {
		t.Fatalf("ParseHook approval failed: %v", err)
	}
	if evPerm.State != daemon.StateBlocked || evPerm.Blocked == nil || evPerm.Blocked.Kind != daemon.BlockPermission {
		t.Errorf("Unexpected approval event: %+v", evPerm)
	}

	// 4. Stop
	stopPayload := `{"session_id": "grok-sess-1", "event": "Stop"}`
	evStop, err := p.ParseHook("", []byte(stopPayload))
	if err != nil {
		t.Fatalf("ParseHook stop failed: %v", err)
	}
	if evStop.State != daemon.StateIdle {
		t.Errorf("Unexpected stop event: %+v", evStop)
	}

	// 5. Corrupt JSON payload returns error
	if _, err := p.ParseHook("", []byte("{corrupt json")); err == nil {
		t.Errorf("Expected error on corrupt JSON payload")
	}
}

func TestGrokProvider_GetResumeCommand(t *testing.T) {
	p := NewGrokProvider()

	if cmd := p.GetResumeCommand(""); cmd != "grok" {
		t.Errorf("Expected 'grok' for empty id, got %q", cmd)
	}
	if cmd := p.GetResumeCommand("valid-session-123"); cmd != "grok resume valid-session-123" {
		t.Errorf("Expected 'grok resume valid-session-123', got %q", cmd)
	}
	if cmd := p.GetResumeCommand("../unsafe/path"); cmd != "grok" {
		t.Errorf("Expected fallback 'grok' for path traversal id, got %q", cmd)
	}
	if cmd := p.GetResumeCommand("session\nrm -rf /"); cmd != "grok" {
		t.Errorf("Expected fallback 'grok' for newline injection id, got %q", cmd)
	}
	if cmd := p.GetResumeCommand("session with spaces"); cmd != "grok" {
		t.Errorf("Expected fallback 'grok' for space-containing id, got %q", cmd)
	}
}

func TestGrokCheckHookConfig(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-grok-hook-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	p := NewGrokProvider()

	// 1. When no config exists
	configured, setupCmd, err := p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if configured {
		t.Errorf("Expected false when no hook config exists")
	}
	if setupCmd != "ackbar-hook --agent=grok" {
		t.Errorf("Expected setupCmd 'ackbar-hook --agent=grok', got %q", setupCmd)
	}

	// 2. When ~/.grok/hooks.json has ackbar-hook configured
	grokDir := filepath.Join(tmpHome, ".grok")
	_ = os.MkdirAll(grokDir, 0755)
	hooksContent := `{"hooks": [{"command": "ackbar-hook --agent=grok"}]}`
	_ = os.WriteFile(filepath.Join(grokDir, "hooks.json"), []byte(hooksContent), 0644)

	configured, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if !configured {
		t.Errorf("Expected true when hooks.json contains ackbar-hook")
	}

	// 3. Fallback when hooks.json is removed but config.toml exists
	_ = os.Remove(filepath.Join(grokDir, "hooks.json"))
	configToml := `hooks = "http://127.0.0.1:7777/v1/hooks/grok"`
	_ = os.WriteFile(filepath.Join(grokDir, "config.toml"), []byte(configToml), 0644)

	configured, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if !configured {
		t.Errorf("Expected true when config.toml contains 127.0.0.1:7777")
	}

	// 4. Fallback when config.toml is removed but config.json exists
	_ = os.Remove(filepath.Join(grokDir, "config.toml"))
	configJson := `{"hook": "http://127.0.0.1:7777/v1/hooks/grok"}`
	_ = os.WriteFile(filepath.Join(grokDir, "config.json"), []byte(configJson), 0644)

	configured, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if !configured {
		t.Errorf("Expected true when config.json contains 127.0.0.1:7777")
	}
}

func TestGrokResolveSessionTitle(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-grok-title-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	p := NewGrokProvider()

	// 1. With non-existent index
	if title := p.ResolveSessionTitle("/tmp", "session-123"); title != "" {
		t.Errorf("Expected empty title when index does not exist, got %q", title)
	}

	// 2. Populate session_index.jsonl
	grokDir := filepath.Join(tmpHome, ".grok")
	_ = os.MkdirAll(grokDir, 0755)
	indexLines := `{"id":"session-123","title":"Add search filters","updated_at":"2026-09-28T09:00:00Z"}` + "\n" +
		`{"id":"session-456","title":"Refactor router","updated_at":"2026-09-28T10:00:00Z"}` + "\n" +
		`{"id":"session-123","title":"Add search filters - Updated","updated_at":"2026-09-28T10:30:00Z"}` + "\n"
	_ = os.WriteFile(filepath.Join(grokDir, "session_index.jsonl"), []byte(indexLines), 0644)

	if title := p.ResolveSessionTitle("/tmp", "session-123"); title != "Add search filters - Updated" {
		t.Errorf("Expected 'Add search filters - Updated', got %q", title)
	}
	if title := p.ResolveSessionTitle("/tmp", "session-456"); title != "Refactor router" {
		t.Errorf("Expected 'Refactor router', got %q", title)
	}
}

func TestGrokExtractTranscriptAndMetadata(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-grok-transcript-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	sessionID := "grok-sess-999"
	sessDir := filepath.Join(tmpHome, ".grok", "sessions")
	_ = os.MkdirAll(sessDir, 0755)

	logLines := []string{
		`{"timestamp":"2026-09-28T09:00:00Z","type":"init","version":"1.2.0"}`,
		`{"timestamp":"2026-09-28T09:00:01Z","role":"user","content":"Optimize database performance"}`,
		`{"timestamp":"2026-09-28T09:00:02Z","type":"tool_call","name":"explain","input":{"query":"SELECT * FROM users"}}`,
		`{"timestamp":"2026-09-28T09:00:03Z","role":"assistant","content":"I analyzed the query plan and added indexes."}`,
		`{"timestamp":"2026-09-28T09:00:04Z","type":"usage","rate_limits":{"primary":{"used_percent":40.0}}}`,
	}

	logPath := filepath.Join(sessDir, fmt.Sprintf("%s.jsonl", sessionID))
	_ = os.WriteFile(logPath, []byte(strings.Join(logLines, "\n")), 0644)

	p := NewGrokProvider()

	// 1. Test ExtractTranscript
	msgs, err := p.ExtractTranscript(tmpHome, "/work", sessionID)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages (1 user, 1 assistant), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "Optimize database performance" {
		t.Errorf("Unexpected user message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Content, "added indexes") {
		t.Errorf("Unexpected assistant message: %+v", msgs[1])
	}
	if len(msgs[1].ToolCalls) != 1 || !strings.Contains(msgs[1].ToolCalls[0], "explain") {
		t.Errorf("Expected tool call attached to assistant, got: %+v", msgs[1].ToolCalls)
	}

	// 2. Test ReadSessionMetadata
	meta := p.ReadSessionMetadata("/work", sessionID)
	if meta == nil {
		t.Fatalf("Expected non-nil SessionMeta")
	}
	if meta.FirstPrompt != "Optimize database performance" {
		t.Errorf("Expected FirstPrompt 'Optimize database performance', got %q", meta.FirstPrompt)
	}
	if meta.Version != "1.2.0" {
		t.Errorf("Expected Version '1.2.0', got %q", meta.Version)
	}
	if meta.ContextPct != 40 {
		t.Errorf("Expected ContextPct 40, got %d", meta.ContextPct)
	}
	if meta.LastMessageAt.IsZero() {
		t.Errorf("Expected non-zero LastMessageAt")
	}
}

func TestOpenCodeProvider_ParseHook(t *testing.T) {
	p := NewOpenCodeProvider()

	// 1. UserPromptSubmit
	promptPayload := `{"session_id": "opencode-sess-1", "event": "UserPromptSubmit", "cwd": "/work"}`
	evPrompt, err := p.ParseHook("", []byte(promptPayload))
	if err != nil {
		t.Fatalf("ParseHook prompt failed: %v", err)
	}
	if evPrompt.State != daemon.StateWorking || evPrompt.Activity != "Processing user prompt" {
		t.Errorf("Unexpected prompt event: %+v", evPrompt)
	}

	// 2. PreToolUse
	toolPayload := `{"session_id": "opencode-sess-1", "event": "PreToolUse", "tool_name": "bash"}`
	evTool, err := p.ParseHook("", []byte(toolPayload))
	if err != nil {
		t.Fatalf("ParseHook tool failed: %v", err)
	}
	if evTool.State != daemon.StateWorking || evTool.Activity != "Executing tool: bash" {
		t.Errorf("Unexpected tool event: %+v", evTool)
	}

	// 3. ApprovalRequest
	permPayload := `{"session_id": "opencode-sess-1", "event": "ApprovalRequest", "reason": "Run migration script"}`
	evPerm, err := p.ParseHook("", []byte(permPayload))
	if err != nil {
		t.Fatalf("ParseHook approval failed: %v", err)
	}
	if evPerm.State != daemon.StateBlocked || evPerm.Blocked == nil || evPerm.Blocked.Kind != daemon.BlockPermission {
		t.Errorf("Unexpected approval event: %+v", evPerm)
	}

	// 4. Stop
	stopPayload := `{"session_id": "opencode-sess-1", "event": "Stop"}`
	evStop, err := p.ParseHook("", []byte(stopPayload))
	if err != nil {
		t.Fatalf("ParseHook stop failed: %v", err)
	}
	if evStop.State != daemon.StateIdle {
		t.Errorf("Unexpected stop event: %+v", evStop)
	}

	// 5. Corrupt JSON payload returns error
	if _, err := p.ParseHook("", []byte("{corrupt json")); err == nil {
		t.Errorf("Expected error on corrupt JSON payload")
	}
}

func TestOpenCodeProvider_GetResumeCommand(t *testing.T) {
	p := NewOpenCodeProvider()

	if cmd := p.GetResumeCommand(""); cmd != "opencode" {
		t.Errorf("Expected 'opencode' for empty id, got %q", cmd)
	}
	if cmd := p.GetResumeCommand("valid-session-123"); cmd != "opencode resume valid-session-123" {
		t.Errorf("Expected 'opencode resume valid-session-123', got %q", cmd)
	}
	if cmd := p.GetResumeCommand("../unsafe/path"); cmd != "opencode" {
		t.Errorf("Expected fallback 'opencode' for path traversal id, got %q", cmd)
	}
	if cmd := p.GetResumeCommand("session\nrm -rf /"); cmd != "opencode" {
		t.Errorf("Expected fallback 'opencode' for newline injection id, got %q", cmd)
	}
	if cmd := p.GetResumeCommand("session with spaces"); cmd != "opencode" {
		t.Errorf("Expected fallback 'opencode' for space-containing id, got %q", cmd)
	}
}

func TestOpenCodeCheckHookConfig(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-opencode-hook-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	p := NewOpenCodeProvider()

	// 1. When no config exists
	configured, setupCmd, err := p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if configured {
		t.Errorf("Expected false when no hook config exists")
	}
	if setupCmd != "ackbar-hook --agent=opencode" {
		t.Errorf("Expected setupCmd 'ackbar-hook --agent=opencode', got %q", setupCmd)
	}

	// 2. When ~/.opencode/hooks.json has ackbar-hook configured
	opencodeDir := filepath.Join(tmpHome, ".opencode")
	_ = os.MkdirAll(opencodeDir, 0755)
	hooksContent := `{"hooks": [{"command": "ackbar-hook --agent=opencode"}]}`
	_ = os.WriteFile(filepath.Join(opencodeDir, "hooks.json"), []byte(hooksContent), 0644)

	configured, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if !configured {
		t.Errorf("Expected true when hooks.json contains ackbar-hook")
	}

	// 3. Fallback when hooks.json is removed but config.json exists
	_ = os.Remove(filepath.Join(opencodeDir, "hooks.json"))
	configJson := `{"hook": "http://127.0.0.1:7777/v1/hooks/opencode"}`
	_ = os.WriteFile(filepath.Join(opencodeDir, "config.json"), []byte(configJson), 0644)

	configured, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if !configured {
		t.Errorf("Expected true when config.json contains 127.0.0.1:7777")
	}

	// 4. Fallback when config.json is removed but config.toml exists
	_ = os.Remove(filepath.Join(opencodeDir, "config.json"))
	configToml := `hooks = "http://127.0.0.1:7777/v1/hooks/opencode"`
	_ = os.WriteFile(filepath.Join(opencodeDir, "config.toml"), []byte(configToml), 0644)

	configured, _, err = p.CheckHookConfig()
	if err != nil {
		t.Fatalf("CheckHookConfig returned unexpected error: %v", err)
	}
	if !configured {
		t.Errorf("Expected true when config.toml contains 127.0.0.1:7777")
	}
}

func TestOpenCodeResolveSessionTitle(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-opencode-title-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	p := NewOpenCodeProvider()

	// 1. With non-existent session
	if title := p.ResolveSessionTitle("/tmp", "session-123"); title != "" {
		t.Errorf("Expected empty title when session file does not exist, got %q", title)
	}

	// 2. Populate session JSONL file with user prompt
	sessDir := filepath.Join(tmpHome, ".opencode", "sessions")
	_ = os.MkdirAll(sessDir, 0755)
	logLines := `{"timestamp":"2026-09-28T09:00:00Z","type":"init","version":"0.5.0"}` + "\n" +
		`{"timestamp":"2026-09-28T09:00:01Z","role":"user","content":"Implement dark mode theme"}` + "\n"
	_ = os.WriteFile(filepath.Join(sessDir, "session-123.jsonl"), []byte(logLines), 0644)

	if title := p.ResolveSessionTitle("/tmp", "session-123"); title != "Implement dark mode theme" {
		t.Errorf("Expected 'Implement dark mode theme', got %q", title)
	}

	// 3. Custom title in metadata takes precedence
	logLinesWithTitle := logLines + `{"timestamp":"2026-09-28T09:00:02Z","title":"Dark Mode Overhaul"}` + "\n"
	_ = os.WriteFile(filepath.Join(sessDir, "session-123.jsonl"), []byte(logLinesWithTitle), 0644)

	if title := p.ResolveSessionTitle("/tmp", "session-123"); title != "Dark Mode Overhaul" {
		t.Errorf("Expected 'Dark Mode Overhaul', got %q", title)
	}
}

func TestOpenCodeExtractTranscriptAndMetadata(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-opencode-transcript-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	sessionID := "opencode-sess-999"
	sessDir := filepath.Join(tmpHome, ".opencode", "sessions", sessionID)
	_ = os.MkdirAll(sessDir, 0755)

	logLines := []string{
		`{"timestamp":"2026-09-28T09:00:00Z","type":"init","version":"0.5.0"}`,
		`{"timestamp":"2026-09-28T09:00:01Z","role":"user","content":"Implement auth middleware"}`,
		`{"timestamp":"2026-09-28T09:00:02Z","type":"tool_call","name":"read_file","input":{"path":"internal/auth.go"}}`,
		`{"timestamp":"2026-09-28T09:00:03Z","role":"assistant","content":"I implemented JWT verification in auth middleware."}`,
		`{"timestamp":"2026-09-28T09:00:04Z","type":"usage","rate_limits":{"primary":{"used_percent":55.0}}}`,
	}

	logPath := filepath.Join(sessDir, "transcript.jsonl")
	_ = os.WriteFile(logPath, []byte(strings.Join(logLines, "\n")), 0644)

	p := NewOpenCodeProvider()

	// 1. Test ExtractTranscript
	msgs, err := p.ExtractTranscript(tmpHome, "/work", sessionID)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages (1 user, 1 assistant), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "Implement auth middleware" {
		t.Errorf("Unexpected user message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Content, "JWT verification") {
		t.Errorf("Unexpected assistant message: %+v", msgs[1])
	}
	if len(msgs[1].ToolCalls) != 1 || !strings.Contains(msgs[1].ToolCalls[0], "read_file") {
		t.Errorf("Expected tool call attached to assistant, got: %+v", msgs[1].ToolCalls)
	}

	// 2. Test ReadSessionMetadata
	meta := p.ReadSessionMetadata("/work", sessionID)
	if meta == nil {
		t.Fatalf("Expected non-nil SessionMeta")
	}
	if meta.FirstPrompt != "Implement auth middleware" {
		t.Errorf("Expected FirstPrompt 'Implement auth middleware', got %q", meta.FirstPrompt)
	}
	if meta.Version != "0.5.0" {
		t.Errorf("Expected Version '0.5.0', got %q", meta.Version)
	}
	if meta.ContextPct != 55 {
		t.Errorf("Expected ContextPct 55, got %d", meta.ContextPct)
	}
	if meta.LastMessageAt.IsZero() {
		t.Errorf("Expected non-zero LastMessageAt")
	}

	// 3. Test CleanSessionFiles
	if err := p.CleanSessionFiles(tmpHome, "/work", sessionID); err != nil {
		t.Fatalf("CleanSessionFiles failed: %v", err)
	}
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Errorf("Expected sessDir %q to be deleted", sessDir)
	}
}
