package provider

import (
	"ackbar/internal/daemon"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// 1. Verify Core Role Interfaces for all providers
	allProviders := []any{claude, antigravity, codex}
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

	// 3. Codex does NOT implement optional capability interfaces (no dummy stubs)
	if _, ok := any(codex).(daemon.StatusInspector); ok {
		t.Errorf("CodexProvider should not implement daemon.StatusInspector")
	}
	if _, ok := any(codex).(daemon.SubagentDiscoverer); ok {
		t.Errorf("CodexProvider should not implement daemon.SubagentDiscoverer")
	}
	if _, ok := any(codex).(daemon.FullProvider); ok {
		t.Errorf("CodexProvider should not implement daemon.FullProvider")
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
