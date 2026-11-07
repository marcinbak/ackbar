package daemon

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

const (
	// DefaultMicServeAddr is the default loopback TCP address used by `agy mic-serve`.
	DefaultMicServeAddr = "127.0.0.1:4713"

	// DefaultMicSampleRate is the microphone capture PCM rate expected by Antigravity CLI (16 kHz mono S16_LE).
	DefaultMicSampleRate = 16000

	// DefaultSpeakerSampleRate is the default Gemini Live speaker playback PCM rate (24 kHz mono S16_LE).
	DefaultSpeakerSampleRate = 24000

	// maxJetskiFrameBytes matches micwire.ReadFrame's 1 MiB safety cap.
	maxJetskiFrameBytes = 1024 * 1024
)

var (
	defaultBridgeAddrMu sync.RWMutex
	defaultBridgeAddr   string
)

// DefaultVoiceBridgeAddr returns the active daemon MicServeBridge TCP address if started,
// or any preconfigured ANTIGRAVITY_MIC address from the environment.
func DefaultVoiceBridgeAddr() string {
	defaultBridgeAddrMu.RLock()
	addr := defaultBridgeAddr
	defaultBridgeAddrMu.RUnlock()
	if addr != "" {
		return addr
	}
	return strings.TrimSpace(os.Getenv("ANTIGRAVITY_MIC"))
}

func setDefaultVoiceBridgeAddr(addr string) {
	defaultBridgeAddrMu.Lock()
	defaultBridgeAddr = addr
	defaultBridgeAddrMu.Unlock()
}

type wsVoiceFrame struct {
	PayloadType byte
	Data        []byte
}

var voiceBridgeCodec = websocket.Codec{
	Marshal: func(v interface{}) ([]byte, byte, error) {
		switch data := v.(type) {
		case []byte:
			return data, websocket.BinaryFrame, nil
		case string:
			return []byte(data), websocket.TextFrame, nil
		case wsVoiceFrame:
			return data.Data, data.PayloadType, nil
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, 0, err
			}
			return b, websocket.TextFrame, nil
		}
	},
	Unmarshal: func(msg []byte, payloadType byte, v interface{}) error {
		switch target := v.(type) {
		case *wsVoiceFrame:
			copied := make([]byte, len(msg))
			copy(copied, msg)
			target.PayloadType = payloadType
			target.Data = copied
			return nil
		default:
			return fmt.Errorf("unsupported target type: %T", v)
		}
	},
}

// wsVoiceClient represents a connected Web UI or Mobile App WebSocket client
// streaming bidirectional PCM audio for Antigravity's `/voice live` & `/voice dictate`.
type wsVoiceClient struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	sessionID string
}

func (c *wsVoiceClient) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return voiceBridgeCodec.Send(c.conn, string(b))
}

func (c *wsVoiceClient) writeBinary(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return voiceBridgeCodec.Send(c.conn, data)
}

// VoiceBridgeStatus represents the live state of the JETSKI-AUDIO/1 TCP bridge.
type VoiceBridgeStatus struct {
	Type          string `json:"type"`
	BridgeAddr    string `json:"bridge_addr"`
	Recording     bool   `json:"recording"`
	Playing       bool   `json:"playing"`
	SampleRate    int    `json:"sample_rate"`
	MicSampleRate int    `json:"mic_sample_rate"`
	Clients       int    `json:"clients"`
}

// MicServeBridge implements Antigravity CLI's JETSKI-AUDIO/1 (`agy mic-serve`) TCP server
// and bridges raw S16_LE PCM audio to and from Web UI and Mobile App WebSocket clients.
type MicServeBridge struct {
	mu                sync.RWMutex
	listener          net.Listener
	addr              string
	closed            bool
	wsClients         map[*wsVoiceClient]struct{}
	recSubscribers    map[chan []byte]struct{}
	activeSpeakers    int
	speakerSampleRate int
}

// NewMicServeBridge starts a JETSKI-AUDIO/1 TCP listener on listenAddr.
// If listenAddr is empty, it attempts DefaultMicServeAddr ("127.0.0.1:4713") and
// automatically falls back to an ephemeral loopback port ("127.0.0.1:0") if 4713 is busy.
func NewMicServeBridge(listenAddr string) (*MicServeBridge, error) {
	var ln net.Listener
	var err error
	if strings.TrimSpace(listenAddr) == "" {
		ln, err = net.Listen("tcp", DefaultMicServeAddr)
		if err != nil {
			ln, err = net.Listen("tcp", "127.0.0.1:0")
		}
	} else {
		ln, err = net.Listen("tcp", listenAddr)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to listen for mic-serve bridge: %w", err)
	}

	b := &MicServeBridge{
		listener:          ln,
		addr:              ln.Addr().String(),
		wsClients:         make(map[*wsVoiceClient]struct{}),
		recSubscribers:    make(map[chan []byte]struct{}),
		speakerSampleRate: DefaultSpeakerSampleRate,
	}

	if strings.TrimSpace(os.Getenv("ANTIGRAVITY_MIC")) == "" {
		_ = os.Setenv("ANTIGRAVITY_MIC", b.addr)
	}
	setDefaultVoiceBridgeAddr(b.addr)

	go b.acceptLoop()
	return b, nil
}

// Addr returns the bound TCP address (e.g. "127.0.0.1:4713") for ANTIGRAVITY_MIC.
func (b *MicServeBridge) Addr() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.addr
}

// Status returns a snapshot of the bridge state.
func (b *MicServeBridge) Status() VoiceBridgeStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	rate := b.speakerSampleRate
	if rate <= 0 {
		rate = DefaultSpeakerSampleRate
	}
	return VoiceBridgeStatus{
		Type:          "status",
		BridgeAddr:    b.addr,
		Recording:     len(b.recSubscribers) > 0,
		Playing:       b.activeSpeakers > 0,
		SampleRate:    rate,
		MicSampleRate: DefaultMicSampleRate,
		Clients:       len(b.wsClients),
	}
}

// Close stops the TCP listener and disconnects all active clients.
func (b *MicServeBridge) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	ln := b.listener
	clients := make([]*wsVoiceClient, 0, len(b.wsClients))
	for c := range b.wsClients {
		clients = append(clients, c)
	}
	b.wsClients = make(map[*wsVoiceClient]struct{})
	for ch := range b.recSubscribers {
		close(ch)
	}
	b.recSubscribers = make(map[chan []byte]struct{})
	b.mu.Unlock()

	for _, c := range clients {
		_ = c.conn.Close()
	}
	if ln != nil {
		return ln.Close()
	}
	return nil
}

func (b *MicServeBridge) acceptLoop() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			b.mu.RLock()
			closed := b.closed
			b.mu.RUnlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go b.handleTCPConn(conn)
	}
}

// handleTCPConn multiplexes incoming Antigravity CLI TCP connections according to JETSKI-AUDIO/1:
//   - Speaker connections immediately send "JETSKI-AUDIO/1 speaker rate=<hz>\n".
//   - Microphone recording connections send no greeting and immediately read raw 16kHz S16_LE mono PCM.
func (b *MicServeBridge) handleTCPConn(conn net.Conn) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(75 * time.Millisecond))
	peek, err := br.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})

	if err == nil && len(peek) > 0 && peek[0] == 'J' {
		b.serveSpeakerConn(conn, br)
		return
	}
	if err != nil {
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			// Connection closed before any data or timeout
			return
		}
	}
	b.serveRecordingConn(conn, br)
}

func (b *MicServeBridge) serveSpeakerConn(conn net.Conn, br *bufio.Reader) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := br.ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}

	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 || fields[0] != "JETSKI-AUDIO/1" || fields[1] != "speaker" || !strings.HasPrefix(fields[2], "rate=") {
		_, _ = io.WriteString(conn, "JETSKI-AUDIO/1 ERR invalid speaker handshake\n")
		return
	}
	rateStr := strings.TrimPrefix(fields[2], "rate=")
	rate, err := strconv.Atoi(rateStr)
	if err != nil || rate <= 0 {
		_, _ = io.WriteString(conn, "JETSKI-AUDIO/1 ERR invalid sample rate\n")
		return
	}

	if _, err := io.WriteString(conn, "JETSKI-AUDIO/1 OK\n"); err != nil {
		return
	}

	b.mu.Lock()
	b.activeSpeakers++
	b.speakerSampleRate = rate
	b.mu.Unlock()
	b.broadcastStatus()

	defer func() {
		b.mu.Lock()
		if b.activeSpeakers > 0 {
			b.activeSpeakers--
		}
		b.mu.Unlock()
		b.broadcastStatus()
	}()

	var hdr [5]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return
		}
		kind := hdr[0]
		length := binary.LittleEndian.Uint32(hdr[1:5])
		if length > maxJetskiFrameBytes {
			_, _ = io.WriteString(conn, "JETSKI-AUDIO/1 ERR frame exceeds max size\n")
			return
		}
		var payload []byte
		if length > 0 {
			payload = make([]byte, length)
			if _, err := io.ReadFull(br, payload); err != nil {
				return
			}
		}

		switch kind {
		case 'A': // Audio PCM S16_LE mono frame
			if len(payload) > 0 {
				b.broadcastSpeakerAudio(payload)
			}
		case 'C': // Clear / flush playback queue immediately (user barge-in)
			b.broadcastControl(map[string]any{
				"type": "clear",
			})
		}
	}
}

func (b *MicServeBridge) serveRecordingConn(conn net.Conn, br *bufio.Reader) {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.recSubscribers[ch] = struct{}{}
	b.mu.Unlock()
	b.broadcastStatus()

	defer func() {
		b.mu.Lock()
		delete(b.recSubscribers, ch)
		b.mu.Unlock()
		b.broadcastStatus()
	}()

	// Detect when Antigravity CLI calls CloseWrite() to stop recording.
	stopCh := make(chan struct{})
	go func() {
		defer close(stopCh)
		_, _ = br.ReadByte()
	}()

	for {
		select {
		case <-stopCh:
			return
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			if len(chunk) == 0 {
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write(chunk); err != nil {
				return
			}
		}
	}
}

// PushMicPCM forwards raw 16kHz mono S16_LE PCM bytes from a browser/mobile client
// to all active Antigravity CLI recording connections.
func (b *MicServeBridge) PushMicPCM(pcm []byte) {
	if len(pcm) == 0 {
		return
	}
	copied := make([]byte, len(pcm))
	copy(copied, pcm)

	b.mu.RLock()
	subs := make([]chan []byte, 0, len(b.recSubscribers))
	for ch := range b.recSubscribers {
		subs = append(subs, ch)
	}
	b.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- copied:
		default:
			// Drop oldest frame if consumer lags
		}
	}
}

func (b *MicServeBridge) broadcastSpeakerAudio(pcm []byte) {
	b.mu.RLock()
	clients := make([]*wsVoiceClient, 0, len(b.wsClients))
	for c := range b.wsClients {
		clients = append(clients, c)
	}
	b.mu.RUnlock()

	for _, c := range clients {
		_ = c.writeBinary(pcm)
	}
}

func (b *MicServeBridge) broadcastControl(msg any) {
	b.mu.RLock()
	clients := make([]*wsVoiceClient, 0, len(b.wsClients))
	for c := range b.wsClients {
		clients = append(clients, c)
	}
	b.mu.RUnlock()

	for _, c := range clients {
		_ = c.writeJSON(msg)
	}
}

func (b *MicServeBridge) broadcastStatus() {
	status := b.Status()
	b.broadcastControl(status)
}

func (b *MicServeBridge) addWSClient(c *wsVoiceClient) {
	b.mu.Lock()
	b.wsClients[c] = struct{}{}
	b.mu.Unlock()
	_ = c.writeJSON(b.Status())
}

func (b *MicServeBridge) removeWSClient(c *wsVoiceClient) {
	b.mu.Lock()
	delete(b.wsClients, c)
	b.mu.Unlock()
}

// EnsureVoiceBridge initializes the server's MicServeBridge if not already running.
func (s *Server) EnsureVoiceBridge() *MicServeBridge {
	s.voiceBridgeMu.Lock()
	defer s.voiceBridgeMu.Unlock()
	if s.voiceBridge != nil {
		return s.voiceBridge
	}
	bridge, err := NewMicServeBridge(os.Getenv("ACKBAR_MIC_BRIDGE_ADDR"))
	if err != nil {
		log.Printf("[VoiceBridge] Failed to bind default address, falling back to ephemeral port: %v", err)
		bridge, err = NewMicServeBridge("127.0.0.1:0")
		if err != nil {
			log.Printf("[VoiceBridge] Failed to initialize MicServeBridge: %v", err)
			return nil
		}
	}
	log.Printf("[VoiceBridge] Antigravity JETSKI-AUDIO/1 bridge listening on %s", bridge.Addr())
	s.voiceBridge = bridge
	return s.voiceBridge
}

// VoiceBridgeAddr returns the active MicServeBridge TCP address (starting it if needed).
func (s *Server) VoiceBridgeAddr() string {
	b := s.EnsureVoiceBridge()
	if b == nil {
		return DefaultMicServeAddr
	}
	return b.Addr()
}

// handleVoiceBridgeStatus serves GET /v1/sessions/voice-bridge/status.
func (s *Server) handleVoiceBridgeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bridge := s.EnsureVoiceBridge()
	if bridge == nil {
		http.Error(w, "Voice bridge unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(bridge.Status())
}

// handleVoiceBridge upgrades GET /v1/sessions/voice-bridge to a bidirectional WebSocket:
//   - Binary frames (Client -> Server): raw 16kHz mono S16_LE microphone PCM chunks.
//   - Binary frames (Server -> Client): raw S16_LE mono speaker PCM chunks from Antigravity CLI.
//   - Text JSON frames (Client <-> Server): status updates, clear/barge-in events, and session voice triggers.
func (s *Server) handleVoiceBridge(w http.ResponseWriter, r *http.Request) {
	server := websocket.Server{
		Handler: s.serveVoiceBridgeWS,
		Handshake: func(config *websocket.Config, req *http.Request) error {
			return nil
		},
	}
	server.ServeHTTP(w, r)
}

func (s *Server) serveVoiceBridgeWS(wsConn *websocket.Conn) {
	defer wsConn.Close()

	bridge := s.EnsureVoiceBridge()
	if bridge == nil {
		return
	}

	req := wsConn.Request()
	sessionID := ""
	if req != nil && req.URL != nil {
		sessionID = strings.TrimSpace(req.URL.Query().Get("session_id"))
		if sessionID == "" {
			sessionID = strings.TrimSpace(req.URL.Query().Get("id"))
		}
	}

	client := &wsVoiceClient{
		conn:      wsConn,
		sessionID: sessionID,
	}
	bridge.addWSClient(client)
	defer bridge.removeWSClient(client)

	if sessionID != "" {
		s.syncSessionMicEnv(sessionID, bridge.Addr())
	}

	for {
		var frame wsVoiceFrame
		if err := voiceBridgeCodec.Receive(wsConn, &frame); err != nil {
			return
		}
		if frame.PayloadType == websocket.BinaryFrame {
			bridge.PushMicPCM(frame.Data)
			continue
		}
		if frame.PayloadType == websocket.TextFrame {
			var ctrl struct {
				Type      string `json:"type"`
				SessionID string `json:"session_id"`
				Command   string `json:"command"`
			}
			if err := json.Unmarshal(frame.Data, &ctrl); err != nil {
				continue
			}
			targetSessionID := strings.TrimSpace(ctrl.SessionID)
			if targetSessionID == "" {
				targetSessionID = client.sessionID
			}
			switch ctrl.Type {
			case "ping":
				_ = client.writeJSON(map[string]any{"type": "pong"})
			case "status":
				_ = client.writeJSON(bridge.Status())
			case "start_live", "start_dictate", "stop_voice", "toggle_live", "voice_command":
				if targetSessionID != "" {
					err := s.triggerAntigravityVoiceCommand(targetSessionID, ctrl.Type, ctrl.Command, bridge.Addr())
					if err != nil {
						_ = client.writeJSON(map[string]any{
							"type":  "error",
							"error": err.Error(),
						})
					} else {
						_ = client.writeJSON(map[string]any{
							"type":       "command_sent",
							"action":     ctrl.Type,
							"session_id": targetSessionID,
						})
					}
				}
			}
		}
	}
}

func (s *Server) syncSessionMicEnv(sessionID, bridgeAddr string) {
	if s.db == nil || sessionID == "" || bridgeAddr == "" {
		return
	}
	sess := s.resolveSession(sessionID)
	if sess == nil {
		return
	}
	if sess.TmuxName != "" && validTaskIDRegex.MatchString(sess.TmuxName) {
		_ = exec.Command("tmux", "set-environment", "-t", sess.TmuxName, "ANTIGRAVITY_MIC", bridgeAddr).Run()
	}
}

func (s *Server) triggerAntigravityVoiceCommand(sessionID, actionType, customCmd, bridgeAddr string) error {
	if s.db == nil {
		return fmt.Errorf("database not initialized")
	}
	sess := s.resolveSession(sessionID)
	if sess == nil {
		return fmt.Errorf("session %q not found", sessionID)
	}

	s.syncSessionMicEnv(sessionID, bridgeAddr)

	if sess.TmuxName == "" || !validTaskIDRegex.MatchString(sess.TmuxName) {
		return fmt.Errorf("session %q is running in headless mode; click 'Take the Wheel' to attach interactive tmux for /voice live", sessionID)
	}

	switch actionType {
	case "toggle_live":
		// F6 key toggles /voice live directly in Antigravity TUI
		return exec.Command("tmux", "send-keys", "-t", sess.TmuxName, "F6").Run()
	case "start_live":
		return s.sendSlashCommandToTmux(sess.TmuxName, "/voice live")
	case "start_dictate":
		return s.sendSlashCommandToTmux(sess.TmuxName, "/voice dictate")
	case "stop_voice":
		return s.sendSlashCommandToTmux(sess.TmuxName, "/voice stop")
	case "voice_command":
		cmd := strings.TrimSpace(customCmd)
		if !strings.HasPrefix(cmd, "/voice") {
			return fmt.Errorf("unsupported voice command %q", cmd)
		}
		return s.sendSlashCommandToTmux(sess.TmuxName, cmd)
	default:
		return nil
	}
}

func (s *Server) sendSlashCommandToTmux(tmuxName, slashCmd string) error {
	if err := exec.Command("tmux", "send-keys", "-t", tmuxName, "-l", slashCmd).Run(); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	return exec.Command("tmux", "send-keys", "-t", tmuxName, "Enter").Run()
}
