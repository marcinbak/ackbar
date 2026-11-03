package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"ackbar/internal/mcp"
	"ackbar/internal/version"
)

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	// CORS Headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Ackbar-Token, X-Ackbar-Session-ID")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          "ok",
			"name":            "ackbar-mcp",
			"version":         version.Version,
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		sendMCPError(w, nil, mcp.CodeParseError, "Failed to read request body")
		return
	}

	var req mcp.JSONRPCRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		sendMCPError(w, nil, mcp.CodeParseError, fmt.Sprintf("Parse error: %v", err))
		return
	}

	isNotification := req.ID == nil

	switch req.Method {
	case "initialize":
		res := mcp.InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: mcp.ServerCapabilities{
				Tools: &mcp.ToolsCapability{ListChanged: false},
			},
			ServerInfo: mcp.Implementation{
				Name:    "ackbar-mcp",
				Version: version.Version,
			},
			Instructions: "Ackbar Supervisor and Task Management MCP endpoint.",
		}
		sendMCPResult(w, req.ID, res)

	case "notifications/initialized", "initialized":
		if isNotification {
			w.WriteHeader(http.StatusOK)
		} else {
			sendMCPResult(w, req.ID, map[string]any{})
		}

	case "ping":
		sendMCPResult(w, req.ID, map[string]any{})

	case "tools/list":
		tools := mcp.GetRegisteredTools()
		sendMCPResult(w, req.ID, mcp.ListToolsResult{Tools: tools})

	case "tools/call":
		var params mcp.CallToolParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				sendMCPError(w, req.ID, mcp.CodeInvalidParams, fmt.Sprintf("Invalid params: %v", err))
				return
			}
		}

		sessionID := r.Header.Get("X-Ackbar-Session-ID")
		if sessionID == "" {
			sessionID = r.URL.Query().Get("session_id")
		}
		if sessionID == "" {
			sessionID = r.URL.Query().Get("id")
		}

		localBaseURL := "http://127.0.0.1:7777"
		if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && addr != nil {
			localBaseURL = "http://" + addr.String()
		}

		client := mcp.NewDaemonClient(localBaseURL, sessionID)
		if auth := r.Header.Get("Authorization"); auth != "" {
			client.Token = strings.TrimPrefix(auth, "Bearer ")
		} else if tok := r.Header.Get("X-Ackbar-Token"); tok != "" {
			client.Token = tok
		} else if s.configuredToken != "" {
			client.Token = s.configuredToken
		}

		callRes, err := mcp.ExecuteTool(r.Context(), client, params.Name, params.Arguments)
		if err != nil {
			callRes = &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.ToolContent{{Type: "text", Text: err.Error()}},
			}
		}
		sendMCPResult(w, req.ID, callRes)

	default:
		if isNotification {
			w.WriteHeader(http.StatusOK)
		} else {
			sendMCPError(w, req.ID, mcp.CodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
		}
	}
}

func sendMCPResult(w http.ResponseWriter, id any, result any) {
	resp := mcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func sendMCPError(w http.ResponseWriter, id any, code int, message string) {
	resp := mcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &mcp.JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
