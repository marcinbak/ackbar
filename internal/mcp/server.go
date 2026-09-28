package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"ackbar/internal/version"
)

// Server implements the MCP stdio JSON-RPC 2.0 server
type Server struct {
	client *DaemonClient
	in     io.Reader
	out    io.Writer
	mu     sync.Mutex
}

// NewServer creates a new MCP stdio server
func NewServer(client *DaemonClient, in io.Reader, out io.Writer) *Server {
	if client == nil {
		client = NewDaemonClient()
	}
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	return &Server{
		client: client,
		in:     in,
		out:    out,
	}
}

// Run starts processing JSON-RPC messages from stdin until EOF or context cancellation
func (s *Server) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	// Support messages up to 4MB
	const maxMessageSize = 4 * 1024 * 1024
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxMessageSize)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, CodeParseError, fmt.Sprintf("Parse error: %v", err))
			continue
		}

		s.handleRequest(ctx, &req)
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleRequest(ctx context.Context, req *JSONRPCRequest) {
	// Notifications (no ID) do not receive responses
	isNotification := req.ID == nil

	switch req.Method {
	case "initialize":
		var params InitializeParams
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &params)
		}

		result := InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: ServerCapabilities{
				Tools: &ToolsCapability{ListChanged: false},
			},
			ServerInfo: Implementation{
				Name:    "ackbar",
				Version: version.Version,
			},
			Instructions: "Ackbar Task Management MCP Server. Use these tools to report blockers, update status, attach deliverables, and propose discovered work.",
		}
		s.sendResult(req.ID, result)

	case "notifications/initialized", "initialized":
		// No response required for notifications
		return

	case "ping":
		if !isNotification {
			s.sendResult(req.ID, map[string]any{})
		}

	case "tools/list":
		tools := GetRegisteredTools()
		s.sendResult(req.ID, ListToolsResult{Tools: tools})

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.sendError(req.ID, CodeInvalidParams, fmt.Sprintf("Invalid params: %v", err))
			return
		}

		callRes, err := ExecuteTool(ctx, s.client, params.Name, params.Arguments)
		if err != nil {
			s.sendError(req.ID, CodeInternalError, err.Error())
			return
		}
		s.sendResult(req.ID, callRes)

	default:
		if !isNotification {
			s.sendError(req.ID, CodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
		}
	}
}

func (s *Server) sendResult(id any, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.writeJSON(&resp)
}

func (s *Server) sendError(id any, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	s.writeJSON(&resp)
}

func (s *Server) writeJSON(v any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = s.out.Write(data)
	_, _ = s.out.Write([]byte("\n"))
}
