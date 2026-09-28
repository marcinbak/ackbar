package mcp

import "encoding/json"

// JSON-RPC 2.0 structures
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

func (r JSONRPCResponse) MarshalJSON() ([]byte, error) {
	if r.Error != nil {
		return json.Marshal(&struct {
			JSONRPC string        `json:"jsonrpc"`
			ID      any           `json:"id"`
			Error   *JSONRPCError `json:"error"`
		}{
			JSONRPC: r.JSONRPC,
			ID:      r.ID,
			Error:   r.Error,
		})
	}
	return json.Marshal(&struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Result  any    `json:"result"`
	}{
		JSONRPC: r.JSONRPC,
		ID:      r.ID,
		Result:  r.Result,
	})
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// MCP Protocol structures
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      Implementation `json:"clientInfo"`
	Options         map[string]any `json:"options,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema ToolInputSchema `json:"inputSchema"`
}

type ToolInputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]PropertyDef `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type PropertyDef struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}
