package provider

import (
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

func TestProviderDiscovery(t *testing.T) {
	providers := []daemon.Provider{
		NewClaudeProvider(),
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

	allProviders := []any{claude}
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

	if _, ok := any(claude).(daemon.StatusInspector); !ok {
		t.Errorf("ClaudeProvider should implement daemon.StatusInspector")
	}
	if _, ok := any(claude).(daemon.SubagentDiscoverer); !ok {
		t.Errorf("ClaudeProvider should implement daemon.SubagentDiscoverer")
	}
	if _, ok := any(claude).(daemon.FullProvider); !ok {
		t.Errorf("ClaudeProvider should implement daemon.FullProvider")
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

func TestClaudeProvider_ExtractTranscript_FiltersSyntheticUserEntries(t *testing.T) {
	tmpHome := t.TempDir()
	sessionID := "97cc5fa1-9e20-4af7-90f2-d222f6bdbe8d"
	cwd := "/home/dev4u/Work/ngl-android"
	encodedCwd := strings.ReplaceAll(cwd, "/", "-")
	logDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	jsonlContent := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"after recent RN upgrade I got feedback that debug builds of main are broken"},"timestamp":"2026-11-09T19:31:22.262Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/screenshot.png"}}]},"timestamp":"2026-11-09T20:14:04.000Z"}`,
		`{"type":"user","isMeta":true,"turnCompanion":true,"message":{"role":"user","content":"[Image: original 1080x2400, displayed at 900x2000. Multiply coordinates by 1.20 to map to original image.]"},"timestamp":"2026-11-09T20:14:05.491Z"}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /home/dev4u/.claude/skills/dev-workflow\n\n# Dev Workflow"}]},"timestamp":"2026-11-09T20:14:10.000Z"}`,
		`{"type":"user","origin":{"kind":"task-notification"},"message":{"role":"user","content":"<task-notification>\n<task-id>abc</task-id>\n<status>completed</status>\n<summary>Build finished</summary>\n</task-notification>"},"timestamp":"2026-11-09T20:15:00.000Z"}`,
		`{"type":"user","message":{"role":"user","content":"Another Claude session sent a message:\n<teammate-message teammate_id=\"clean-review\" color=\"blue\">\n{\"type\":\"idle_notification\"}\n</teammate-message>"},"timestamp":"2026-11-09T20:16:00.000Z"}`,
		`{"type":"user","isCompactSummary":true,"isVisibleInTranscriptOnly":true,"message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context."},"timestamp":"2026-11-09T20:17:00.000Z"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]},"timestamp":"2026-11-09T20:18:00.000Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Verified on emulator; ready to merge."}]},"timestamp":"2026-11-09T20:20:00.000Z"}`,
		`{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"merge without announcing"},"timestamp":"2026-11-09T20:31:15.030Z"}`,
	}, "\n") + "\n"

	logFile := filepath.Join(logDir, sessionID+".jsonl")
	if err := os.WriteFile(logFile, []byte(jsonlContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	p := NewClaudeProvider()
	msgs, err := p.ExtractTranscript(tmpHome, cwd, sessionID)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}

	if len(msgs) != 3 {
		t.Fatalf("Expected 3 messages (user, assistant, user) with synthetic entries filtered and assistant turns coalesced, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || !strings.Contains(msgs[0].Content, "after recent RN upgrade") {
		t.Errorf("Unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Content, "Verified on emulator") || len(msgs[1].ToolCalls) != 1 {
		t.Errorf("Expected coalesced assistant message across synthetic entries, got: %+v", msgs[1])
	}
	if msgs[2].Role != "user" || msgs[2].Content != "merge without announcing" {
		t.Errorf("Unexpected second user message: %+v", msgs[2])
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

	// 3. Team config (even with recent mtime) must default to completed without active transcripts,
	// and deduplicate "subagent-1" which was already discovered in Step 1 under "agent-1.meta.json"
	teamDir := filepath.Join(tmpHome, ".claude", "teams", "session-22222222")
	if err := os.MkdirAll(teamDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	teamCfg := `{
		"name": "session-22222222",
		"leadSessionId": "22222222-3333-4444-5555-666666666666",
		"members": [
			{"name": "team-lead", "agentType": "team-lead"},
			{"name": "subagent-1", "agentId": "team-sub-1", "agentType": "task-runner"},
			{"name": "worker-1", "agentType": "Worker"}
		]
	}`
	teamCfgPath := filepath.Join(teamDir, "config.json")
	if err := os.WriteFile(teamCfgPath, []byte(teamCfg), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cp := NewClaudeProvider()
	subs, err := cp.ListSubagents(tmpHome, cwd, sessionID)
	if err != nil {
		t.Fatalf("ListSubagents failed: %v", err)
	}

	if len(subs) != 3 {
		t.Fatalf("Expected 3 subagents (subagent-1 deduplicated), got %d: %+v", len(subs), subs)
	}

	for _, sub := range subs {
		if sub.State != "completed" {
			t.Errorf("Expected subagent %s to have state 'completed', got %q", sub.Name, sub.State)
		}
	}
}

func TestClaudeProvider_ParseHook_SessionStartSource(t *testing.T) {
	cp := &ClaudeProvider{}
	uuid := "3d24eef2-5087-49eb-a9e2-b77d75eb586f"

	// 1. Default startup SessionStart -> "Session started" with non-zero StartedAt
	evStart, err := cp.ParseHook("SessionStart", []byte(`{"session_id":"`+uuid+`","hook_event_name":"SessionStart","source":"startup"}`))
	if err != nil || evStart == nil {
		t.Fatalf("ParseHook startup failed: %v", err)
	}
	if evStart.Activity != "Session started" || evStart.StartedAt.IsZero() {
		t.Errorf("Expected 'Session started' with StartedAt set, got %q (StartedAt=%v)", evStart.Activity, evStart.StartedAt)
	}

	// 2. Context compaction SessionStart -> "Context compacted" with zero StartedAt
	evCompact, err := cp.ParseHook("SessionStart", []byte(`{"session_id":"`+uuid+`","hook_event_name":"SessionStart","source":"compact"}`))
	if err != nil || evCompact == nil {
		t.Fatalf("ParseHook compact failed: %v", err)
	}
	if evCompact.Activity != "Context compacted" || !evCompact.StartedAt.IsZero() {
		t.Errorf("Expected 'Context compacted' with zero StartedAt, got %q (StartedAt=%v)", evCompact.Activity, evCompact.StartedAt)
	}

	// 3. Resume SessionStart -> "Session resumed" with zero StartedAt
	evResume, err := cp.ParseHook("SessionStart", []byte(`{"session_id":"`+uuid+`","hook_event_name":"SessionStart","source":"resume"}`))
	if err != nil || evResume == nil {
		t.Fatalf("ParseHook resume failed: %v", err)
	}
	if evResume.Activity != "Session resumed" || !evResume.StartedAt.IsZero() {
		t.Errorf("Expected 'Session resumed' with zero StartedAt, got %q (StartedAt=%v)", evResume.Activity, evResume.StartedAt)
	}
}
