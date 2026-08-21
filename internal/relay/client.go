package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type Client struct {
	relayURL   string
	hostName   string
	secret     string
	handler    http.Handler
	wsMu       sync.Mutex
	activeWS   *websocket.Conn
	cancelFunc context.CancelFunc
}

func NewClient(relayURL, hostName, secret string, handler http.Handler) *Client {
	return &Client{
		relayURL: relayURL,
		hostName: hostName,
		secret:   secret,
		handler:  handler,
	}
}

func (c *Client) Start(ctx context.Context) {
	clientCtx, cancel := context.WithCancel(ctx)
	c.cancelFunc = cancel

	go func() {
		backoff := 1 * time.Second
		for {
			select {
			case <-clientCtx.Done():
				return
			default:
			}

			err := c.runSession(clientCtx)
			if err != nil {
				log.Printf("[Relay Client] Connection error: %v. Reconnecting in %v...", err, backoff)
			}

			select {
			case <-clientCtx.Done():
				return
			case <-time.After(backoff):
			}

			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}()
}

func (c *Client) Stop() {
	if c.cancelFunc != nil {
		c.cancelFunc()
	}
	c.wsMu.Lock()
	if c.activeWS != nil {
		_ = c.activeWS.Close()
	}
	c.wsMu.Unlock()
}

func (c *Client) runSession(ctx context.Context) error {
	u, err := url.Parse(c.relayURL)
	if err != nil {
		return fmt.Errorf("invalid relay url: %w", err)
	}

	q := u.Query()
	q.Set("host", c.hostName)
	if c.secret != "" {
		q.Set("secret", c.secret)
	}
	u.RawQuery = q.Encode()

	wsScheme := "ws"
	if u.Scheme == "https" || u.Scheme == "wss" {
		wsScheme = "wss"
	}
	origin := fmt.Sprintf("%s://%s", wsScheme, u.Host)
	targetURL := u.String()
	if strings.HasPrefix(targetURL, "http://") {
		targetURL = "ws://" + strings.TrimPrefix(targetURL, "http://")
	} else if strings.HasPrefix(targetURL, "https://") {
		targetURL = "wss://" + strings.TrimPrefix(targetURL, "https://")
	}

	cfg, err := websocket.NewConfig(targetURL, origin)
	if err != nil {
		return fmt.Errorf("websocket config error: %w", err)
	}

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		return fmt.Errorf("dial error: %w", err)
	}
	defer ws.Close()

	c.wsMu.Lock()
	c.activeWS = ws
	c.wsMu.Unlock()

	log.Printf("[Relay Client] Successfully established outbound tunnel to %s as host %q", u.Host, c.hostName)

	sendMsg := func(msg *TunnelMessage) error {
		c.wsMu.Lock()
		defer c.wsMu.Unlock()
		data, mErr := json.Marshal(msg)
		if mErr != nil {
			return mErr
		}
		return websocket.Message.Send(ws, string(data))
	}

	// Keepalive ping ticker
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sendMsg(&TunnelMessage{Type: TypePing}); err != nil {
					return
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var raw string
		err := websocket.Message.Receive(ws, &raw)
		if err != nil {
			return err
		}

		var msg TunnelMessage
		if jErr := json.Unmarshal([]byte(raw), &msg); jErr != nil {
			continue
		}

		if msg.Type == TypePong {
			continue
		}

		if msg.Type == TypeHTTPRequest {
			go c.handleIncomingRequest(ctx, &msg, sendMsg)
		}
	}
}

func (c *Client) handleIncomingRequest(ctx context.Context, reqMsg *TunnelMessage, send func(*TunnelMessage) error) {
	if reqMsg.IsWS {
		// PTY WebSocket stream over tunnel
		c.handleIncomingWebSocket(ctx, reqMsg, send)
		return
	}

	reqURL, _ := url.Parse(reqMsg.URL)
	httpReq := httptest.NewRequest(reqMsg.Method, reqMsg.URL, bytes.NewReader(reqMsg.Body))
	httpReq.URL = reqURL

	for k, v := range reqMsg.Headers {
		httpReq.Header.Set(k, v)
	}

	isSSE := strings.Contains(reqMsg.Headers["Accept"], "text/event-stream") || strings.HasPrefix(reqMsg.URL, "/v1/events")
	if isSSE {
		c.handleIncomingSSE(ctx, reqMsg.StreamID, httpReq, send)
		return
	}

	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, httpReq)

	res := rec.Result()
	respBody, _ := io.ReadAll(res.Body)

	respHeaders := make(map[string]string)
	for k, v := range res.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	_ = send(&TunnelMessage{
		Type:     TypeHTTPResponse,
		StreamID: reqMsg.StreamID,
		Status:   res.StatusCode,
		Headers:  respHeaders,
		Body:     respBody,
		Done:     true,
	})
}

type sseTunnelWriter struct {
	streamID string
	send     func(*TunnelMessage) error
	headers  http.Header
	headerMu sync.Mutex
	sentHdr  bool
}

func (w *sseTunnelWriter) Header() http.Header {
	return w.headers
}

func (w *sseTunnelWriter) Write(data []byte) (int, error) {
	w.headerMu.Lock()
	if !w.sentHdr {
		w.sentHdr = true
		hdrs := make(map[string]string)
		for k, v := range w.headers {
			if len(v) > 0 {
				hdrs[k] = v[0]
			}
		}
		_ = w.send(&TunnelMessage{
			Type:     TypeHTTPResponse,
			StreamID: w.streamID,
			Status:   http.StatusOK,
			Headers:  hdrs,
		})
	}
	w.headerMu.Unlock()

	err := w.send(&TunnelMessage{
		Type:     TypeData,
		StreamID: w.streamID,
		Body:     data,
	})
	return len(data), err
}

func (w *sseTunnelWriter) WriteHeader(statusCode int) {}
func (w *sseTunnelWriter) Flush()                     {}

func (c *Client) handleIncomingSSE(ctx context.Context, streamID string, httpReq *http.Request, send func(*TunnelMessage) error) {
	writer := &sseTunnelWriter{
		streamID: streamID,
		send:     send,
		headers:  make(http.Header),
	}

	c.handler.ServeHTTP(writer, httpReq)
	_ = send(&TunnelMessage{Type: TypeClose, StreamID: streamID})
}

func (c *Client) handleIncomingWebSocket(ctx context.Context, reqMsg *TunnelMessage, send func(*TunnelMessage) error) {
	// For PTY WebSockets over tunnel, we delegate to handler via loopback request
	httpReq := httptest.NewRequest("GET", reqMsg.URL, nil)
	for k, v := range reqMsg.Headers {
		httpReq.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, httpReq)
}
