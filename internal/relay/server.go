package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type activeStream struct {
	id         string
	respWriter http.ResponseWriter
	flusher    http.Flusher
	respChan   chan *TunnelMessage
	dataChan   chan []byte
	isWS       bool
	wsConn     *websocket.Conn
	done       chan struct{}
}

type daemonSession struct {
	hostName    string
	ws          *websocket.Conn
	mu          sync.Mutex
	streams     map[string]*activeStream
	streamsMu   sync.RWMutex
	lastSeen    time.Time
	connectedAt time.Time
}

func (ds *daemonSession) send(msg *TunnelMessage) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return websocket.Message.Send(ds.ws, string(data))
}

type Server struct {
	secret    string
	daemons   map[string]*daemonSession
	daemonsMu sync.RWMutex
}

func NewServer(secret string) *Server {
	return &Server{
		secret:  secret,
		daemons: make(map[string]*daemonSession),
	}
}

func (s *Server) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/relay/tunnel", s.handleTunnelWS)
	mux.HandleFunc("/v1/relay/hosts", s.handleListHosts)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"ackbar-relay"}`))
	})
	mux.HandleFunc("/host/", s.handleProxy)
	return withRelayCORS(mux)
}

func withRelayCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Ackbar-Host, X-Ackbar-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	s.daemonsMu.RLock()
	defer s.daemonsMu.RUnlock()

	type hostInfo struct {
		Name        string `json:"name"`
		ConnectedAt string `json:"connected_at"`
		Streams     int    `json:"active_streams"`
	}

	var list []hostInfo
	for name, ds := range s.daemons {
		ds.streamsMu.RLock()
		streamCount := len(ds.streams)
		ds.streamsMu.RUnlock()
		list = append(list, hostInfo{
			Name:        name,
			ConnectedAt: ds.connectedAt.Format(time.RFC3339),
			Streams:     streamCount,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) handleTunnelWS(w http.ResponseWriter, r *http.Request) {
	hostName := r.URL.Query().Get("host")
	secret := r.URL.Query().Get("secret")

	if hostName == "" {
		http.Error(w, "Missing 'host' query parameter", http.StatusBadRequest)
		return
	}

	if s.secret != "" && secret != s.secret {
		http.Error(w, "Unauthorized: invalid relay secret", http.StatusUnauthorized)
		return
	}

	wsHandler := websocket.Server{
		Handler: func(ws *websocket.Conn) {
			s.serveDaemonTunnel(hostName, ws)
		},
		Handshake: func(config *websocket.Config, req *http.Request) error {
			return nil
		},
	}
	wsHandler.ServeHTTP(w, r)
}

func (s *Server) serveDaemonTunnel(hostName string, ws *websocket.Conn) {
	defer ws.Close()

	ds := &daemonSession{
		hostName:    hostName,
		ws:          ws,
		streams:     make(map[string]*activeStream),
		lastSeen:    time.Now(),
		connectedAt: time.Now(),
	}

	s.daemonsMu.Lock()
	if old, exists := s.daemons[hostName]; exists {
		log.Printf("[Relay] Host %q reconnected. Closing old session.", hostName)
		_ = old.ws.Close()
	}
	s.daemons[hostName] = ds
	s.daemonsMu.Unlock()

	log.Printf("[Relay] Daemon %q connected to relay tunnel.", hostName)

	defer func() {
		s.daemonsMu.Lock()
		if curr, exists := s.daemons[hostName]; exists && curr == ds {
			delete(s.daemons, hostName)
		}
		s.daemonsMu.Unlock()
		log.Printf("[Relay] Daemon %q disconnected from relay tunnel.", hostName)
	}()

	for {
		var raw string
		err := websocket.Message.Receive(ws, &raw)
		if err != nil {
			if err != io.EOF {
				log.Printf("[Relay] Daemon %q read error: %v", hostName, err)
			}
			return
		}

		var msg TunnelMessage
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}

		ds.lastSeen = time.Now()

		if msg.Type == TypePing {
			_ = ds.send(&TunnelMessage{Type: TypePong})
			continue
		}

		ds.streamsMu.RLock()
		stream, exists := ds.streams[msg.StreamID]
		ds.streamsMu.RUnlock()

		if !exists {
			continue
		}

		switch msg.Type {
		case TypeHTTPResponse:
			if stream.respChan != nil {
				select {
				case stream.respChan <- &msg:
				case <-stream.done:
				}
			}
		case TypeData:
			if stream.isWS && stream.wsConn != nil {
				_ = websocket.Message.Send(stream.wsConn, msg.Body)
			} else if stream.flusher != nil && stream.respWriter != nil {
				_, _ = stream.respWriter.Write(msg.Body)
				stream.flusher.Flush()
			}
		case TypeClose:
			close(stream.done)
			ds.streamsMu.Lock()
			delete(ds.streams, msg.StreamID)
			ds.streamsMu.Unlock()
		}
	}
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	// Path pattern: /host/<hostname>/...
	subPath := strings.TrimPrefix(r.URL.Path, "/host/")
	parts := strings.SplitN(subPath, "/", 2)
	hostName := parts[0]
	targetPath := "/"
	if len(parts) > 1 {
		targetPath = "/" + parts[1]
	}
	if r.URL.RawQuery != "" {
		targetPath += "?" + r.URL.RawQuery
	}

	s.daemonsMu.RLock()
	ds, exists := s.daemons[hostName]
	s.daemonsMu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("Host %q is not connected to relay", hostName), http.StatusBadGateway)
		return
	}

	// Check if this is a WebSocket request (e.g. /v1/sessions/pty)
	if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
		s.handleProxyWebSocket(w, r, ds, targetPath)
		return
	}

	// Standard HTTP / SSE Proxy
	streamID := generateStreamID()
	bodyBytes, _ := io.ReadAll(r.Body)

	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	flusher, isFlusher := w.(http.Flusher)
	stream := &activeStream{
		id:         streamID,
		respWriter: w,
		flusher:    flusher,
		respChan:   make(chan *TunnelMessage, 1),
		dataChan:   make(chan []byte, 16),
		done:       make(chan struct{}),
	}

	ds.streamsMu.Lock()
	ds.streams[streamID] = stream
	ds.streamsMu.Unlock()

	defer func() {
		ds.streamsMu.Lock()
		delete(ds.streams, streamID)
		ds.streamsMu.Unlock()
	}()

	reqMsg := &TunnelMessage{
		Type:     TypeHTTPRequest,
		StreamID: streamID,
		Method:   r.Method,
		URL:      targetPath,
		Headers:  headers,
		Body:     bodyBytes,
	}

	if err := ds.send(reqMsg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to forward to host %q: %v", hostName, err), http.StatusBadGateway)
		return
	}

	// Wait for initial response header
	select {
	case resp := <-stream.respChan:
		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
		status := resp.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)

		if len(resp.Body) > 0 {
			_, _ = w.Write(resp.Body)
		}
		if isFlusher {
			flusher.Flush()
		}

		if resp.Done {
			return
		}
	case <-time.After(15 * time.Second):
		http.Error(w, "Gateway timeout waiting for host response", http.StatusGatewayTimeout)
		return
	case <-r.Context().Done():
		_ = ds.send(&TunnelMessage{Type: TypeClose, StreamID: streamID})
		return
	}

	// Keep stream alive for SSE / long polling until client or server disconnects
	select {
	case <-stream.done:
	case <-r.Context().Done():
		_ = ds.send(&TunnelMessage{Type: TypeClose, StreamID: streamID})
	}
}

func (s *Server) handleProxyWebSocket(w http.ResponseWriter, r *http.Request, ds *daemonSession, targetPath string) {
	wsHandler := websocket.Server{
		Handler: func(clientWS *websocket.Conn) {
			defer clientWS.Close()
			streamID := generateStreamID()

			stream := &activeStream{
				id:       streamID,
				isWS:     true,
				wsConn:   clientWS,
				done:     make(chan struct{}),
				dataChan: make(chan []byte, 32),
			}

			ds.streamsMu.Lock()
			ds.streams[streamID] = stream
			ds.streamsMu.Unlock()

			defer func() {
				ds.streamsMu.Lock()
				delete(ds.streams, streamID)
				ds.streamsMu.Unlock()
				_ = ds.send(&TunnelMessage{Type: TypeClose, StreamID: streamID})
			}()

			// Initiate WS stream on daemon
			headers := make(map[string]string)
			for k, v := range r.Header {
				if len(v) > 0 {
					headers[k] = v[0]
				}
			}

			reqMsg := &TunnelMessage{
				Type:     TypeHTTPRequest,
				StreamID: streamID,
				Method:   "GET",
				URL:      targetPath,
				Headers:  headers,
				IsWS:     true,
			}

			if err := ds.send(reqMsg); err != nil {
				return
			}

			// Pump messages from Client WebSocket -> Relay -> Daemon Tunnel
			for {
				var data []byte
				err := websocket.Message.Receive(clientWS, &data)
				if err != nil {
					return
				}
				_ = ds.send(&TunnelMessage{
					Type:     TypeData,
					StreamID: streamID,
					Body:     data,
				})
			}
		},
		Handshake: func(config *websocket.Config, req *http.Request) error {
			return nil
		},
	}
	wsHandler.ServeHTTP(w, r)
}

func generateStreamID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
