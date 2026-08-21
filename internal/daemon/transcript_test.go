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
