package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRelay_EndToEnd(t *testing.T) {
	// 1. Start Relay Server
	relayServer := NewServer("test-secret")
	relayHTTP := httptest.NewServer(relayServer.Mux())
	defer relayHTTP.Close()

	// 2. Mock Daemon Handler
	daemonMux := http.NewServeMux()
	daemonMux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"host":   "legion",
		})
	})

	// 3. Connect Daemon Client via Outbound Tunnel
	relayWSURL := fmt.Sprintf("ws://%s/v1/relay/tunnel", relayHTTP.Listener.Addr().String())
	client := NewClient(relayWSURL, "legion", "test-secret", daemonMux)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)

	// Wait briefly for tunnel handshake
	time.Sleep(150 * time.Millisecond)

	// 4. Client requests host via Relay: GET /host/legion/v1/sessions
	reqURL := fmt.Sprintf("%s/host/legion/v1/sessions", relayHTTP.URL)
	resp, err := http.Get(reqURL)
	if err != nil {
		t.Fatalf("Failed to execute request through relay: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from relay proxy, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var data map[string]string
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("Failed to parse response JSON: %v", err)
	}

	if data["host"] != "legion" {
		t.Fatalf("Expected host 'legion', got %q", data["host"])
	}
}
