package agentutil

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

type ACPRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ACPRPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("acp rpc error %d: %s", e.Code, e.Message)
}

type acpCallResult struct {
	result json.RawMessage
	rpcErr *ACPRPCError
	err    error
}

type ACPConn struct {
	r io.Reader
	w io.Writer

	OnNotification  func(method string, params map[string]any)
	OnServerRequest func(rawID json.RawMessage, reqID string, method string, params map[string]any) bool
	OnRawLine       func(line []byte)

	nextID  atomic.Int64
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan acpCallResult
	closed  bool
	readErr error

	startOnce sync.Once
	doneOnce  sync.Once
	done      chan struct{}
}

func NewACPConn(r io.Reader, w io.Writer) *ACPConn {
	return &ACPConn{r: r, w: w, pending: make(map[string]chan acpCallResult), done: make(chan struct{})}
}

func (c *ACPConn) Start() {
	if c != nil {
		c.startOnce.Do(func() { go c.readLoop() })
	}
}

func (c *ACPConn) Done() <-chan struct{} { return c.done }

func (c *ACPConn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

func (c *ACPConn) Call(ctx context.Context, method string, params any) (json.RawMessage, *ACPRPCError, error) {
	if c == nil {
		return nil, nil, io.ErrClosedPipe
	}
	id := c.nextID.Add(1)
	idKey := "n:" + strconv.FormatInt(id, 10)
	ch := make(chan acpCallResult, 1)

	c.mu.Lock()
	if c.closed {
		err := c.readErr
		if err == nil {
			err = io.EOF
		}
		c.mu.Unlock()
		return nil, nil, err
	}
	c.pending[idKey] = ch
	c.mu.Unlock()

	frame := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.writeJSON(frame); err != nil {
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, nil, err
	}

	select {
	case res := <-ch:
		return res.result, res.rpcErr, res.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, nil, ctx.Err()
	case <-c.done:
		select {
		case res := <-ch:
			return res.result, res.rpcErr, res.err
		default:
		}
		if err := c.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, io.EOF
	}
}

func (c *ACPConn) SendNotification(method string, params any) error {
	if c == nil {
		return io.ErrClosedPipe
	}
	return c.writeJSON(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *ACPConn) SendResponse(rawID json.RawMessage, result any) error {
	if c == nil {
		return io.ErrClosedPipe
	}
	trimmed := bytes.TrimSpace(rawID)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("invalid json-rpc id")
	}
	if result == nil {
		result = map[string]any{}
	}
	return c.writeJSON(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{JSONRPC: "2.0", ID: trimmed, Result: result})
}

func (c *ACPConn) SendError(rawID json.RawMessage, code int, message string) error {
	if c == nil {
		return io.ErrClosedPipe
	}
	trimmed := bytes.TrimSpace(rawID)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("invalid json-rpc id")
	}
	return c.writeJSON(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   *ACPRPCError    `json:"error"`
	}{JSONRPC: "2.0", ID: trimmed, Error: &ACPRPCError{Code: code, Message: message}})
}

func (c *ACPConn) writeJSON(v any) error {
	if c.w == nil {
		return io.ErrClosedPipe
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

func (c *ACPConn) readLoop() {
	var closeErr error
	defer func() {
		if closeErr == nil {
			closeErr = io.EOF
		}
		c.mu.Lock()
		c.closed = true
		c.readErr = closeErr
		for idKey, ch := range c.pending {
			ch <- acpCallResult{err: closeErr}
			delete(c.pending, idKey)
		}
		c.mu.Unlock()
		c.doneOnce.Do(func() { close(c.done) })
	}()

	if c.r == nil {
		return
	}
	scanner := bufio.NewScanner(c.r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var env struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			Result  json.RawMessage `json:"result"`
			Error   *ACPRPCError    `json:"error"`
		}
		if err := json.Unmarshal(line, &env); err != nil || env.JSONRPC == "" {
			if c.OnRawLine != nil {
				c.OnRawLine(append([]byte(nil), line...))
			}
			continue
		}

		reqID, idKey, hasID := parseJSONRPCID(env.ID)
		if hasID && env.Method == "" {
			c.mu.Lock()
			ch := c.pending[idKey]
			delete(c.pending, idKey)
			c.mu.Unlock()
			if ch != nil {
				var resCopy json.RawMessage
				if len(env.Result) > 0 {
					resCopy = append([]byte(nil), env.Result...)
				}
				ch <- acpCallResult{result: resCopy, rpcErr: env.Error}
			}
			continue
		}

		var params map[string]any
		if trimmedP := bytes.TrimSpace(env.Params); len(trimmedP) > 0 && !bytes.Equal(trimmedP, []byte("null")) {
			_ = json.Unmarshal(trimmedP, &params)
		}
		if hasID && env.Method != "" {
			rawIDCopy := append([]byte(nil), bytes.TrimSpace(env.ID)...)
			handled := c.OnServerRequest != nil && c.OnServerRequest(rawIDCopy, reqID, env.Method, params)
			if !handled {
				// Reply immediately so child processes never deadlock on unknown ACP extension methods.
				_ = c.SendError(rawIDCopy, -32601, "Method not found: "+env.Method)
			}
			continue
		}
		if !hasID && env.Method != "" && c.OnNotification != nil {
			c.OnNotification(env.Method, params)
		}
	}
	closeErr = scanner.Err()
}

func parseJSONRPCID(raw json.RawMessage) (display string, lookupKey string, ok bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", "", false
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return s, "s:" + s, true
		}
		return "", "", false
	}
	var num json.Number
	if json.Unmarshal(trimmed, &num) == nil && num.String() != "" {
		return num.String(), "n:" + num.String(), true
	}
	return "", "", false
}
