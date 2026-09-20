package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractTranscript_Antigravity(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-agy-brain-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	convID := "test-conv-1234"
	logDir := filepath.Join(tmpHome, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0755)

	jsonlContent := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-08-11T13:16:49Z","content":"<USER_REQUEST>\nBuild a new dashboard widget\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nLocal time\n</ADDITIONAL_METADATA>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-08-11T13:17:00Z","content":"I will create the dashboard widget.","thinking":"Planning widget creation","tool_calls":[{"name":"list_dir","args":{"DirectoryPath":"/tmp","toolAction":"Listing files"}}]}
`
	logFile := filepath.Join(logDir, "transcript.jsonl")
	if err := os.WriteFile(logFile, []byte(jsonlContent), 0644); err != nil {
		t.Fatalf("Failed to write mock transcript: %v", err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	transcript, err := ExtractTranscript("antigravity", convID, "/tmp")
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}

	if len(transcript.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(transcript.Messages))
	}

	if transcript.Messages[0].Role != "user" || transcript.Messages[0].Content != "Build a new dashboard widget" {
		t.Errorf("Unexpected user message: %+v", transcript.Messages[0])
	}

	if transcript.Messages[1].Role != "assistant" || !strings.Contains(transcript.Messages[1].Content, "create the dashboard widget") {
		t.Errorf("Unexpected assistant message: %+v", transcript.Messages[1])
	}

	if len(transcript.Messages[1].ToolCalls) != 1 || !strings.Contains(transcript.Messages[1].ToolCalls[0], "list_dir") {
		t.Errorf("Expected tool call list_dir, got: %+v", transcript.Messages[1].ToolCalls)
	}

	ansi := FormatTranscriptANSI(transcript)
	if !strings.Contains(ansi, "Build a new dashboard widget") || !strings.Contains(ansi, "list_dir") {
		t.Errorf("ANSI formatting missing expected content:\n%s", ansi)
	}

	md := FormatTranscriptMarkdown(transcript)
	if !strings.Contains(md, "Build a new dashboard widget") || !strings.Contains(md, "Tools Invoked") {
		t.Errorf("Markdown formatting missing expected content:\n%s", md)
	}
}

func TestExtractTranscript_ClaudeCode(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-claude-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	sessionID := "test-claude-session-5678"
	cwd := "/Users/dev4u/Work/Modemobile/NGL/ngl-ios"
	encodedCwd := encodeClaudeProjectDir(cwd)
	logDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	_ = os.MkdirAll(logDir, 0755)

	jsonlContent := `{"type":"user","message":{"role":"user","content":"Review this PR https://github.com/CurrentMobile/ngl-ios/pull/500"},"timestamp":"2026-08-20T18:28:27.241Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I will review the pull request."},{"type":"tool_use","name":"Bash"}]},"timestamp":"2026-08-20T18:28:30.000Z"}
`
	logFile := filepath.Join(logDir, sessionID+".jsonl")
	if err := os.WriteFile(logFile, []byte(jsonlContent), 0644); err != nil {
		t.Fatalf("Failed to write mock claude transcript: %v", err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	transcript, err := ExtractTranscript("claude-code", sessionID, cwd)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}

	if len(transcript.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(transcript.Messages))
	}

	if transcript.Messages[0].Role != "user" || transcript.Messages[0].Content != "Review this PR https://github.com/CurrentMobile/ngl-ios/pull/500" {
		t.Errorf("Unexpected user message: %+v", transcript.Messages[0])
	}

	if transcript.Messages[1].Role != "assistant" || !strings.Contains(transcript.Messages[1].Content, "review the pull request") {
		t.Errorf("Unexpected assistant message: %+v", transcript.Messages[1])
	}

	md := FormatTranscriptMarkdown(transcript)
	if !strings.Contains(md, "Review this PR") || !strings.Contains(md, "### 👤 User") {
		t.Errorf("Markdown formatting missing user prompt:\n%s", md)
	}
}

func TestExtractTranscript_ClaudeCode_ConsecutiveToolCalls(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-claude-consec-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	sessionID := "test-claude-multi-tool"
	cwd := "/Users/dev4u/Work/Modemobile/NGL/ngl-ios"
	encodedCwd := encodeClaudeProjectDir(cwd)
	logDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	_ = os.MkdirAll(logDir, 0755)

	// User prompt followed by 3 separate tool_use assistant blocks and final text response
	jsonlContent := `{"type":"user","message":{"role":"user","content":"Run tests and check status"},"timestamp":"2026-08-20T18:28:20.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]},"timestamp":"2026-08-20T18:28:21.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"npm test"}}]},"timestamp":"2026-08-20T18:28:22.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"FileRead","input":{"file_path":"package.json"}}]},"timestamp":"2026-08-20T18:28:23.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"All 12 tests passed successfully."}]},"timestamp":"2026-08-20T18:28:24.000Z"}
`
	logFile := filepath.Join(logDir, sessionID+".jsonl")
	if err := os.WriteFile(logFile, []byte(jsonlContent), 0644); err != nil {
		t.Fatalf("Failed to write mock claude transcript: %v", err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	transcript, err := ExtractTranscript("claude-code", sessionID, cwd)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}

	// Must be coalesced into 2 messages: 1 user, 1 assistant (NOT 5 messages!)
	if len(transcript.Messages) != 2 {
		t.Fatalf("Expected 2 coalesced messages, got %d", len(transcript.Messages))
	}

	asst := transcript.Messages[1]
	if asst.Role != "assistant" {
		t.Errorf("Expected assistant role, got %s", asst.Role)
	}
	if !strings.Contains(asst.Content, "All 12 tests passed") {
		t.Errorf("Expected final text in assistant message, got: %q", asst.Content)
	}
	if len(asst.ToolCalls) != 3 {
		t.Fatalf("Expected 3 tool calls, got %d: %v", len(asst.ToolCalls), asst.ToolCalls)
	}
	if !strings.Contains(asst.ToolCalls[0], "Bash: git status") {
		t.Errorf("Expected first tool call 'Bash: git status', got %q", asst.ToolCalls[0])
	}
	if !strings.Contains(asst.ToolCalls[1], "Bash: npm test") {
		t.Errorf("Expected second tool call 'Bash: npm test', got %q", asst.ToolCalls[1])
	}
	if !strings.Contains(asst.ToolCalls[2], "FileRead: package.json") {
		t.Errorf("Expected third tool call 'FileRead: package.json', got %q", asst.ToolCalls[2])
	}
}

func TestFormatToolSummaryCounts(t *testing.T) {
	// Single tool
	if res := formatToolSummaryCounts([]string{"Bash: ls"}); res != "Bash" {
		t.Errorf("Expected 'Bash', got %q", res)
	}
	// Same tool repeated
	if res := formatToolSummaryCounts([]string{"Bash: ls", "Bash: pwd", "Bash: cat file"}); res != "Bash x3" {
		t.Errorf("Expected 'Bash x3', got %q", res)
	}
	// Mixed tools
	mixed := []string{"Bash: ls", "Grep: pattern", "Bash: rm", "View: file.go"}
	if res := formatToolSummaryCounts(mixed); res != "Bash x2, Grep x1, View x1" {
		t.Errorf("Expected 'Bash x2, Grep x1, View x1', got %q", res)
	}
}

func TestExtractSubagents_Antigravity(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-subagents-agy-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	convID := "test-conv-subagents"
	logDir := filepath.Join(tmpHome, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0755)

	jsonlContent := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-18T00:00:00Z","content":"Review the auth service"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-18T00:01:00Z","content":"Spawning parallel reviewers","tool_calls":[{"name":"invoke_subagent","args":{"Subagents":[{"Role":"Code Reviewer A","TypeName":"research","Prompt":"Review Kotlin backend"},{"Role":"Security Reviewer","TypeName":"research","Prompt":"Audit JWT validation"}]}}]}
{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-09-18T00:01:05Z","content":"Created the following subagents:\n{\n  \"conversationId\": \"sub-conv-a\"\n}\n{\n  \"conversationId\": \"sub-conv-sec\"\n}"}
{"step_index":3,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"2026-09-18T00:02:00Z","content":"[Message] timestamp=2026-09-18T00:02:00Z sender=sub-conv-a content=Kotlin review done"}
`
	logFile := filepath.Join(logDir, "transcript.jsonl")
	if err := os.WriteFile(logFile, []byte(jsonlContent), 0644); err != nil {
		t.Fatalf("Failed to write mock transcript: %v", err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	running, err := ExtractSubagents("antigravity", convID, "/tmp")
	if err != nil {
		t.Fatalf("ExtractSubagents failed: %v", err)
	}

	// sub-conv-a completed; only Security Reviewer (sub-conv-sec) should still be running
	if len(running) != 1 {
		t.Fatalf("Expected 1 running subagent, got %d: %+v", len(running), running)
	}

	if running[0].Name != "Security Reviewer" {
		t.Errorf("Expected Security Reviewer, got %s", running[0].Name)
	}
	if running[0].Prompt != "Audit JWT validation" {
		t.Errorf("Expected 'Audit JWT validation', got %s", running[0].Prompt)
	}
	if running[0].State != "running" {
		t.Errorf("Expected running state, got %s", running[0].State)
	}
}

func TestExtractSubagents_ClaudeCode(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-subagents-claude-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	sessionID := "test-session-subagents"
	cwd := "/Users/dev4u/Work/testapp"
	encodedCwd := encodeClaudeProjectDir(cwd)
	logDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	_ = os.MkdirAll(logDir, 0755)

	jsonlContent := `{"type":"user","message":{"role":"user","content":"Run tests in parallel"},"timestamp":"2026-09-18T00:00:00.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Spawning agents"},{"type":"tool_use","id":"toolu-1","name":"Agent","input":{"subagent_type":"Explore","prompt":"Explore API routes"}},{"type":"tool_use","id":"toolu-2","name":"Agent","input":{"subagent_type":"Tester","prompt":"Run unit tests"}}]},"timestamp":"2026-09-18T00:01:00.000Z"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu-1","content":"Exploration finished"}]},"timestamp":"2026-09-18T00:02:00.000Z"}
`
	logFile := filepath.Join(logDir, sessionID+".jsonl")
	if err := os.WriteFile(logFile, []byte(jsonlContent), 0644); err != nil {
		t.Fatalf("Failed to write mock claude transcript: %v", err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	running, err := ExtractSubagents("claude-code", sessionID, cwd)
	if err != nil {
		t.Fatalf("ExtractSubagents failed: %v", err)
	}

	// toolu-1 completed; only toolu-2 (Tester) should still be running
	if len(running) != 1 {
		t.Fatalf("Expected 1 running subagent, got %d: %+v", len(running), running)
	}

	if running[0].Name != "Tester" {
		t.Errorf("Expected Tester, got %s", running[0].Name)
	}
	if running[0].Prompt != "Run unit tests" {
		t.Errorf("Expected 'Run unit tests', got %s", running[0].Prompt)
	}
	if running[0].State != "running" {
		t.Errorf("Expected running state, got %s", running[0].State)
	}
}

func TestEncodeClaudeProjectDir(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "/Users/dev4u/Work/Ackbar",
			expected: "-Users-dev4u-Work-Ackbar",
		},
		{
			input:    "/home/dev4u/Work/ngl-android/.claude/worktrees/fix-e2e-onboarding-back-race",
			expected: "-home-dev4u-Work-ngl-android--claude-worktrees-fix-e2e-onboarding-back-race",
		},
		{
			input:    "/home/dev4u/Work/repo_name.with.dots/sub-dir",
			expected: "-home-dev4u-Work-repo-name-with-dots-sub-dir",
		},
	}

	for _, tc := range tests {
		got := encodeClaudeProjectDir(tc.input)
		if got != tc.expected {
			t.Errorf("encodeClaudeProjectDir(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}
