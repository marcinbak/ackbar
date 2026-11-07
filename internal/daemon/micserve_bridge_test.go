package daemon

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestMicServeBridge_SpeakerAndRecordingProtocol(t *testing.T) {
	t.Setenv("ACKBAR_MIC_BRIDGE_ADDR", "127.0.0.1:0")

	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	bridge := srv.EnsureVoiceBridge()
	if bridge == nil {
		t.Fatalf("expected EnsureVoiceBridge to return non-nil bridge")
	}
	defer bridge.Close()

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	// 1. Verify HTTP status endpoint
	statusRes, err := http.Get(ts.URL + "/v1/sessions/voice-bridge/status")
	if err != nil {
		t.Fatalf("GET /v1/sessions/voice-bridge/status failed: %v", err)
	}
	defer statusRes.Body.Close()
	if statusRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from status endpoint, got %d", statusRes.StatusCode)
	}
	var initialStatus VoiceBridgeStatus
	if err := json.NewDecoder(statusRes.Body).Decode(&initialStatus); err != nil {
		t.Fatalf("failed to decode status JSON: %v", err)
	}
	if initialStatus.BridgeAddr != bridge.Addr() {
		t.Fatalf("expected bridge_addr %q, got %q", bridge.Addr(), initialStatus.BridgeAddr)
	}

	// 2. Connect a WebSocket client (simulating Browser / Mobile App)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/sessions/voice-bridge"
	wsConn, err := websocket.Dial(wsURL, "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("failed to dial voice-bridge websocket: %v", err)
	}
	defer wsConn.Close()

	audioReceived := make(chan []byte, 4)
	clearReceived := make(chan struct{}, 2)
	recordingActive := make(chan bool, 4)

	go func() {
		for {
			var frame wsVoiceFrame
			if err := voiceBridgeCodec.Receive(wsConn, &frame); err != nil {
				return
			}
			if frame.PayloadType == websocket.BinaryFrame {
				audioReceived <- frame.Data
			} else if frame.PayloadType == websocket.TextFrame {
				var msg struct {
					Type      string `json:"type"`
					Recording bool   `json:"recording"`
				}
				if json.Unmarshal(frame.Data, &msg) == nil {
					if msg.Type == "clear" {
						clearReceived <- struct{}{}
					} else if msg.Type == "status" && msg.Recording {
						recordingActive <- true
					}
				}
			}
		}
	}()

	// 3. Test Speaker Playback Connection (`agy` -> `MicServeBridge` -> WebSocket client)
	speakerConn, err := net.DialTimeout("tcp", bridge.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial bridge TCP speaker: %v", err)
	}
	defer speakerConn.Close()

	if _, err := io.WriteString(speakerConn, "JETSKI-AUDIO/1 speaker rate=24000\n"); err != nil {
		t.Fatalf("failed to write speaker hello: %v", err)
	}
	speakerReader := bufio.NewReader(speakerConn)
	reply, err := speakerReader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read speaker handshake reply: %v", err)
	}
	if strings.TrimSpace(reply) != "JETSKI-AUDIO/1 OK" {
		t.Fatalf("unexpected speaker handshake reply: %q", reply)
	}

	// Send 'A' (Audio PCM) frame: [ 'A', uint32_le(len), payload... ]
	expectedPCM := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	var hdr [5]byte
	hdr[0] = 'A'
	binary.LittleEndian.PutUint32(hdr[1:5], uint32(len(expectedPCM)))
	if _, err := speakerConn.Write(hdr[:]); err != nil {
		t.Fatalf("failed to write 'A' frame header: %v", err)
	}
	if _, err := speakerConn.Write(expectedPCM); err != nil {
		t.Fatalf("failed to write 'A' frame payload: %v", err)
	}

	select {
	case gotPCM := <-audioReceived:
		if !bytes.Equal(gotPCM, expectedPCM) {
			t.Fatalf("expected speaker PCM %v, got %v", expectedPCM, gotPCM)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for speaker PCM over WebSocket")
	}

	// Send 'C' (Clear / Barge-In) frame: [ 'C', 0, 0, 0, 0 ]
	hdr[0] = 'C'
	binary.LittleEndian.PutUint32(hdr[1:5], 0)
	if _, err := speakerConn.Write(hdr[:]); err != nil {
		t.Fatalf("failed to write 'C' frame: %v", err)
	}

	select {
	case <-clearReceived:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for 'clear' control event over WebSocket")
	}

	// 4. Test Microphone Recording Connection (WebSocket client -> `MicServeBridge` -> `agy`)
	recConn, err := net.DialTimeout("tcp", bridge.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial bridge TCP recorder: %v", err)
	}
	defer recConn.Close()

	// Wait for the bridge's 75ms hello-peek deadline to classify recConn as a recording stream
	select {
	case <-recordingActive:
		// Recording stream registered
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for recording status=true")
	}

	micChunk := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60}
	if err := voiceBridgeCodec.Send(wsConn, micChunk); err != nil {
		t.Fatalf("failed to send binary mic PCM over WebSocket: %v", err)
	}

	gotMic := make([]byte, len(micChunk))
	_ = recConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(recConn, gotMic); err != nil {
		t.Fatalf("failed to read mic PCM from TCP recording connection: %v", err)
	}
	if !bytes.Equal(gotMic, micChunk) {
		t.Fatalf("expected mic PCM %v, got %v", micChunk, gotMic)
	}

	// Verify Clean Half-Close (CloseWrite) stops the recording connection with EOF
	if tcpConn, ok := recConn.(*net.TCPConn); ok {
		_ = tcpConn.CloseWrite()
		_ = recConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		buf := make([]byte, 16)
		_, err := recConn.Read(buf)
		if err != io.EOF {
			t.Fatalf("expected io.EOF after CloseWrite, got %v", err)
		}
	}
}

func TestResolveAccountEnv_InjectsAntigravityMic(t *testing.T) {
	t.Setenv("ACKBAR_MIC_BRIDGE_ADDR", "127.0.0.1:0")

	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	defer func() {
		if srv.voiceBridge != nil {
			_ = srv.voiceBridge.Close()
		}
	}()

	_, envVars := srv.resolveAccountEnv("antigravity", "", "/tmp", true)
	if envVars == nil {
		t.Fatalf("expected non-nil envVars for antigravity agent")
	}
	micAddr := envVars["ANTIGRAVITY_MIC"]
	if micAddr == "" {
		t.Fatalf("expected ANTIGRAVITY_MIC to be injected into antigravity session envVars")
	}
	if micAddr != srv.VoiceBridgeAddr() {
		t.Fatalf("expected ANTIGRAVITY_MIC=%q, got %q", srv.VoiceBridgeAddr(), micAddr)
	}
}
