package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMCPServer_HandshakeAndToolsList(t *testing.T) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}

	client := &DaemonClient{
		BaseURL:    "http://127.0.0.1:9999",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}
	server := NewServer(client, in, out)

	// 1. Send initialize
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"clientInfo": map[string]any{
				"name":    "test-agent",
				"version": "1.0",
			},
		},
	}
	initBytes, _ := json.Marshal(initReq)
	in.Write(initBytes)
	in.WriteString("\n")

	// 2. Send tools/list
	listReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}
	listBytes, _ := json.Marshal(listReq)
	in.Write(listBytes)
	in.WriteString("\n")

	// 3. Send ping
	pingReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "ping",
	}
	pingBytes, _ := json.Marshal(pingReq)
	in.Write(pingBytes)
	in.WriteString("\n")

	// Run server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Run(ctx); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse responses
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("Expected 3 response lines, got %d:\n%s", len(lines), out.String())
	}

	// Verify initialize response
	var initResp JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("Failed to parse initialize response: %v", err)
	}
	if initResp.Error != nil {
		t.Errorf("Unexpected error in initialize: %v", initResp.Error)
	}

	// Verify tools/list response
	var listResp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  ListToolsResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("Failed to parse tools/list response: %v", err)
	}
	if len(listResp.Result.Tools) < 8 {
		t.Errorf("Expected at least 8 tools, got %d", len(listResp.Result.Tools))
	}

	// Verify tools include report_blocker and propose_task
	hasBlockerTool := false
	hasProposeTool := false
	for _, tool := range listResp.Result.Tools {
		if tool.Name == "report_blocker" {
			hasBlockerTool = true
		}
		if tool.Name == "propose_task" {
			hasProposeTool = true
		}
	}
	if !hasBlockerTool {
		t.Errorf("Expected report_blocker tool in tools/list")
	}
	if !hasProposeTool {
		t.Errorf("Expected propose_task tool in tools/list")
	}
}

func TestMCPServer_ToolExecution(t *testing.T) {
	// Setup mock HTTP daemon
	var receivedPath string
	var receivedBody []byte

	mockDaemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedBody, _ = io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "id": "task_123"})
		case "/v1/tasks/propose":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "created", "task": map[string]any{"id": "prop_1", "title": "Memory leak"}})
		case "/v1/tasks/event":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockDaemon.Close()

	client := &DaemonClient{
		BaseURL:    mockDaemon.URL,
		HTTPClient: mockDaemon.Client(),
	}

	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	server := NewServer(client, in, out)

	// Call report_blocker tool
	callReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "report_blocker",
			"arguments": map[string]any{
				"task_id":  "task_123",
				"question": "Waiting on database schema choice",
			},
		},
	}
	reqBytes, _ := json.Marshal(callReq)
	in.Write(reqBytes)
	in.WriteString("\n")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Run(ctx); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var resp struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      int            `json:"id"`
		Result  CallToolResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse tool call response: %v", err)
	}

	if resp.Result.IsError {
		t.Errorf("Expected success, got error: %v", resp.Result.Content)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, "Blocker reported") {
		t.Errorf("Unexpected content: %v", resp.Result.Content)
	}

	// Verify mock received request
	if receivedPath == "" {
		t.Errorf("Expected request path to be recorded")
	}
	if !strings.Contains(string(receivedBody), "Waiting on database schema choice") {
		t.Errorf("Expected question in daemon request body, got: %s", string(receivedBody))
	}
}
