package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFormatMCPToolDisplayAndSpoolSummary(t *testing.T) {
	// Full call_mcp_tool with arguments
	disp, detail, isMCP := FormatMCPToolDisplay("call_mcp_tool", map[string]any{
		"ServerName": "ackbar",
		"ToolName":   "get_current_task",
		"Arguments":  map[string]any{"id": "task-1"},
	})
	if !isMCP || disp != "mcp/ackbar/get_current_task" || !strings.Contains(detail, "task-1") {
		t.Fatalf("unexpected full call_mcp_tool display: disp=%q, detail=%q, isMCP=%v", disp, detail, isMCP)
	}
	summary := FormatSpoolToolSummary("call_mcp_tool", map[string]any{
		"ServerName": "ackbar",
		"ToolName":   "get_current_task",
		"Arguments":  map[string]any{"id": "task-1"},
	})
	if !strings.HasPrefix(summary, "mcp/ackbar/get_current_task: ") || !strings.Contains(summary, "task-1") {
		t.Fatalf("unexpected summary: %q", summary)
	}

	// Empty arguments suppresses detail
	disp, detail, isMCP = FormatMCPToolDisplay("call_mcp_tool", map[string]any{
		"ServerName": "ackbar",
		"ToolName":   "get_current_task",
		"Arguments":  map[string]any{},
	})
	if !isMCP || disp != "mcp/ackbar/get_current_task" || detail != "" {
		t.Fatalf("expected empty detail for empty arguments: disp=%q, detail=%q", disp, detail)
	}
	summary = FormatSpoolToolSummary("call_mcp_tool", map[string]any{
		"ServerName": "ackbar",
		"ToolName":   "get_current_task",
		"Arguments":  "{}",
	})
	if summary != "mcp/ackbar/get_current_task" {
		t.Fatalf("expected summary without detail for empty string args: %q", summary)
	}

	// Partial tool name only
	disp, _, isMCP = FormatMCPToolDisplay("call_mcp_tool", map[string]any{"ToolName": "only_tool"})
	if !isMCP || disp != "mcp/only_tool" {
		t.Fatalf("expected mcp/only_tool, got %q", disp)
	}

	// Partial server name only
	disp, _, isMCP = FormatMCPToolDisplay("call_mcp_tool", map[string]any{"ServerName": "only_server"})
	if !isMCP || disp != "mcp/only_server" {
		t.Fatalf("expected mcp/only_server, got %q", disp)
	}

	// Neither server nor tool
	disp, _, isMCP = FormatMCPToolDisplay("call_mcp_tool", map[string]any{})
	if !isMCP || disp != "call_mcp_tool" {
		t.Fatalf("expected call_mcp_tool, got %q", disp)
	}

	// JSON string input coercion
	disp, detail, isMCP = FormatMCPToolDisplay("CALL_MCP_TOOL", `{"server":"gdrive","tool":"search_files","args":{"query":"test"}}`)
	if !isMCP || disp != "mcp/gdrive/search_files" || !strings.Contains(detail, "query") {
		t.Fatalf("failed JSON string parsing: disp=%q, detail=%q, isMCP=%v", disp, detail, isMCP)
	}

	// mcp__server__tool syntax
	disp, detail, isMCP = FormatMCPToolDisplay("mcp__ackbar__list_tasks", map[string]any{"limit": 10})
	if !isMCP || disp != "mcp/ackbar/list_tasks" || !strings.Contains(detail, "10") {
		t.Fatalf("expected mcp/ackbar/list_tasks, got %q, detail=%q", disp, detail)
	}

	// mcp_server_tool syntax
	disp, detail, isMCP = FormatMCPToolDisplay("mcp_stripe_charge", map[string]any{"amount": 500})
	if !isMCP || disp != "mcp/stripe/charge" || !strings.Contains(detail, "500") {
		t.Fatalf("expected mcp/stripe/charge, got %q, detail=%q", disp, detail)
	}

	// Regular tools
	disp, detail, isMCP = FormatMCPToolDisplay("Read", map[string]any{"file_path": "/tmp/test.go"})
	if isMCP || disp != "" || detail != "" {
		t.Fatalf("expected regular tool to not be MCP: disp=%q, isMCP=%v", disp, isMCP)
	}
	summary = FormatSpoolToolSummary("Read", map[string]any{"file_path": "/tmp/test.go"})
	if summary != "Read: /tmp/test.go" {
		t.Fatalf("expected Read: /tmp/test.go, got %q", summary)
	}
	summary = FormatSpoolToolSummary("Bash", map[string]any{"command": "echo 'hello world'"})
	if summary != "Bash: echo 'hello world'" {
		t.Fatalf("expected Bash summary, got %q", summary)
	}

	// Multi-byte UTF-8 rune boundary truncation
	unicodeDetail := strings.Repeat("🚀", 105)
	_, detail, _ = FormatMCPToolDisplay("call_mcp_tool", map[string]any{
		"ServerName": "ackbar",
		"ToolName":   "deploy",
		"Arguments":  unicodeDetail,
	})
	if !utf8.ValidString(detail) {
		t.Fatalf("expected valid UTF-8 string, got invalid")
	}
	expectedTruncated := strings.Repeat("🚀", 100) + "..."
	if detail != expectedTruncated {
		t.Fatalf("expected %q, got %q", expectedTruncated, detail)
	}
	if utf8.RuneCountInString(detail) != 103 {
		t.Fatalf("expected 103 runes, got %d", utf8.RuneCountInString(detail))
	}
}

func TestExtractAntigravityTranscriptMessages_MCPTools(t *testing.T) {
	tmpHome, err := os.MkdirTemp("", "test-ag-mcp-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	convID := "conv-mcp-test"
	logDir := filepath.Join(tmpHome, ".gemini", "antigravity-cli", "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	transcriptJSONL := strings.Join([]string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-16T12:00:00Z","content":"<USER_REQUEST>Check tasks</USER_REQUEST>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-16T12:00:05Z","content":"Checking task state...","tool_calls":[{"name":"call_mcp_tool","args":{"ServerName":"ackbar","ToolName":"get_current_task","Arguments":{}}},{"name":"call_mcp_tool","args":{"ServerName":"ackbar","ToolName":"list_tasks","Arguments":{"status":"IN_PROGRESS"}}}]}`,
	}, "\n") + "\n"

	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(transcriptJSONL), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(transcriptJSONL), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	msgs, err := ExtractAntigravityTranscriptMessages(tmpHome, convID)
	if err != nil {
		t.Fatalf("ExtractAntigravityTranscriptMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if len(msgs[1].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(msgs[1].ToolCalls))
	}
	if msgs[1].ToolCalls[0] != "mcp/ackbar/get_current_task" {
		t.Errorf("expected mcp/ackbar/get_current_task, got %q", msgs[1].ToolCalls[0])
	}
	if !strings.HasPrefix(msgs[1].ToolCalls[1], "mcp/ackbar/list_tasks: ") || !strings.Contains(msgs[1].ToolCalls[1], "IN_PROGRESS") {
		t.Errorf("expected mcp/ackbar/list_tasks with status detail, got %q", msgs[1].ToolCalls[1])
	}
}

func TestSynthesizeInFlightTurn_MCPToolUpgrade(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-spool-mcp-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	spoolPath := filepath.Join(tmpDir, "turn.jsonl")
	t0 := time.Now().Add(-10 * time.Second)
	t1 := t0.Add(1 * time.Second)
	t2 := t0.Add(2 * time.Second)

	events := []ChatStreamEvent{
		{
			Seq:       1,
			Type:      "turn_start",
			Text:      "Check task",
			Timestamp: t0,
		},
		{
			Seq:       2,
			Type:      "tool_start",
			ToolName:  "call_mcp_tool",
			ToolInput: nil,
			Timestamp: t1,
		},
		{
			Seq:      3,
			Type:     "tool_result",
			ToolName: "call_mcp_tool",
			ToolInput: map[string]any{
				"ServerName": "ackbar",
				"ToolName":   "list_tasks",
				"Arguments":  map[string]any{"status": "IN_PROGRESS"},
			},
			Timestamp: t2,
		},
	}

	f, err := os.OpenFile(spoolPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("failed to open spool file: %v", err)
	}
	for _, e := range events {
		data, err := json.Marshal(e)
		if err != nil {
			f.Close()
			t.Fatalf("failed to marshal event: %v", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			t.Fatalf("failed to write event: %v", err)
		}
	}
	f.Close()

	meta := &TurnSpoolMeta{
		TurnID:    "turn-mcp-1",
		SessionID: "antigravity:local:sess-mcp",
		NativeID:  "sess-mcp",
		Agent:     "antigravity",
		Prompt:    "Check task",
		SpoolPath: spoolPath,
		StartedAt: t0,
		Status:    "running",
	}

	snap, err := SynthesizeInFlightTurn(meta, "antigravity")
	if err != nil {
		t.Fatalf("SynthesizeInFlightTurn failed: %v", err)
	}
	if len(snap.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(snap.ToolCalls))
	}
	if !strings.HasPrefix(snap.ToolCalls[0], "mcp/ackbar/list_tasks: ") || !strings.Contains(snap.ToolCalls[0], "IN_PROGRESS") {
		t.Fatalf("expected upgraded MCP tool call in snapshot, got %q", snap.ToolCalls[0])
	}
}
