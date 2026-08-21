package relay

type MessageType string

const (
	TypeHTTPRequest  MessageType = "http_request"
	TypeHTTPResponse MessageType = "http_response"
	TypeData         MessageType = "data"
	TypeClose        MessageType = "close"
	TypePing         MessageType = "ping"
	TypePong         MessageType = "pong"
)

type TunnelMessage struct {
	Type     MessageType       `json:"type"`
	StreamID string            `json:"stream_id,omitempty"`
	Method   string            `json:"method,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Status   int               `json:"status,omitempty"`
	Body     []byte            `json:"body,omitempty"`
	IsWS     bool              `json:"is_ws,omitempty"`
	Done     bool              `json:"done,omitempty"`
	Error    string            `json:"error,omitempty"`
}
