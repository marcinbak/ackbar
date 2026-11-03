package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"ackbar/internal/mcp"
	"ackbar/internal/version"
)

func TestServerMCP_Endpoint(t *testing.T) {
	tempDir := t.TempDir()
	db, err := InitDB(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	// 1. Test GET /v1/mcp
	t.Run("GET /v1/mcp returns capabilities", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/v1/mcp")
		if err != nil {
			t.Fatalf("GET /v1/mcp failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if body["name"] != "ackbar-mcp" || body["version"] != version.Version {
			t.Errorf("unexpected body: %+v", body)
		}
	})

	// 2. Test OPTIONS /v1/mcp (CORS preflight)
	t.Run("OPTIONS /v1/mcp CORS", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/v1/mcp", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("OPTIONS /v1/mcp failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}
		if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("expected Access-Control-Allow-Origin: *")
		}
	})

	// 3. Test POST /v1/mcp initialize
	t.Run("POST /v1/mcp initialize", func(t *testing.T) {
		initReq := mcp.JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "initialize",
		}
		data, _ := json.Marshal(initReq)
		resp, err := http.Post(ts.URL+"/v1/mcp", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatalf("initialize failed: %v", err)
		}
		defer resp.Body.Close()

		var rpcResp mcp.JSONRPCResponse
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("failed to decode json-rpc: %v", err)
		}
		if rpcResp.Error != nil {
			t.Fatalf("unexpected RPC error: %+v", rpcResp.Error)
		}

		resBytes, _ := json.Marshal(rpcResp.Result)
		var initRes mcp.InitializeResult
		_ = json.Unmarshal(resBytes, &initRes)
		if initRes.ProtocolVersion != "2024-11-05" || initRes.ServerInfo.Name != "ackbar-mcp" {
			t.Errorf("unexpected initialize result: %+v", initRes)
		}
	})

	// 4. Test POST /v1/mcp tools/list
	t.Run("POST /v1/mcp tools/list", func(t *testing.T) {
		listReq := mcp.JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      2,
			Method:  "tools/list",
		}
		data, _ := json.Marshal(listReq)
		resp, err := http.Post(ts.URL+"/v1/mcp", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatalf("tools/list failed: %v", err)
		}
		defer resp.Body.Close()

		var rpcResp mcp.JSONRPCResponse
		_ = json.NewDecoder(resp.Body).Decode(&rpcResp)
		if rpcResp.Error != nil {
			t.Fatalf("unexpected RPC error: %+v", rpcResp.Error)
		}

		resBytes, _ := json.Marshal(rpcResp.Result)
		var listRes mcp.ListToolsResult
		_ = json.Unmarshal(resBytes, &listRes)
		if len(listRes.Tools) == 0 {
			t.Fatalf("expected registered tools, got 0")
		}

		hasListSessions := false
		for _, tool := range listRes.Tools {
			if tool.Name == "list_sessions" {
				hasListSessions = true
				break
			}
		}
		if !hasListSessions {
			t.Errorf("expected list_sessions tool to be listed")
		}
	})

	// 5. Test POST /v1/mcp tools/call (calling list_sessions)
	t.Run("POST /v1/mcp tools/call list_sessions", func(t *testing.T) {
		params := mcp.CallToolParams{
			Name: "list_sessions",
			Arguments: map[string]any{
				"federated": false,
			},
		}
		paramBytes, _ := json.Marshal(params)
		callReq := mcp.JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "tools/call",
			Params:  paramBytes,
		}
		data, _ := json.Marshal(callReq)
		resp, err := http.Post(ts.URL+"/v1/mcp", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatalf("tools/call failed: %v", err)
		}
		defer resp.Body.Close()

		var rpcResp mcp.JSONRPCResponse
		_ = json.NewDecoder(resp.Body).Decode(&rpcResp)
		if rpcResp.Error != nil {
			t.Fatalf("unexpected RPC error: %+v", rpcResp.Error)
		}

		resBytes, _ := json.Marshal(rpcResp.Result)
		var callRes mcp.CallToolResult
		_ = json.Unmarshal(resBytes, &callRes)
		if callRes.IsError {
			t.Fatalf("unexpected call tool error: %+v", callRes)
		}
		if len(callRes.Content) == 0 {
			t.Errorf("expected text content in tool result")
		}
	})

	// 6. Test POST /v1/mcp tools/call with spoofed Host header (no SSRF / token leak)
	t.Run("POST /v1/mcp tools/call with spoofed Host header does not SSRF", func(t *testing.T) {
		params := mcp.CallToolParams{
			Name: "list_sessions",
			Arguments: map[string]any{
				"federated": false,
			},
		}
		paramBytes, _ := json.Marshal(params)
		callReq := mcp.JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      4,
			Method:  "tools/call",
			Params:  paramBytes,
		}
		data, _ := json.Marshal(callReq)
		req, err := http.NewRequest("POST", ts.URL+"/v1/mcp", bytes.NewReader(data))
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		// Malicious attacker attempts to spoof Host header to trigger loopback SSRF or leak token
		req.Host = "evil.example.com"

		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("tools/call with evil host failed: %v", err)
		}
		defer resp.Body.Close()

		var rpcResp mcp.JSONRPCResponse
		_ = json.NewDecoder(resp.Body).Decode(&rpcResp)
		if rpcResp.Error != nil {
			t.Fatalf("unexpected RPC error: %+v", rpcResp.Error)
		}

		resBytes, _ := json.Marshal(rpcResp.Result)
		var callRes mcp.CallToolResult
		_ = json.Unmarshal(resBytes, &callRes)
		if callRes.IsError {
			t.Fatalf("unexpected call tool error with spoofed Host: %+v", callRes)
		}
		if len(callRes.Content) == 0 {
			t.Errorf("expected text content in tool result")
		}
	})
}
