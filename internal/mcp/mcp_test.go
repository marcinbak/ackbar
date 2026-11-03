package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBindSupervisorSessionTool(t *testing.T) {
	tools := GetRegisteredTools()
	found := false
	for _, tool := range tools {
		if tool.Name == "bind_supervisor_session" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected bind_supervisor_session to be registered in GetRegisteredTools()")
	}

	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/bind-supervisor" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "bound",
			"id":     receivedPayload["session_id"],
		})
	}))
	defer ts.Close()

	client := &DaemonClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	res, err := ExecuteTool(context.Background(), client, "bind_supervisor_session", map[string]any{
		"session_id": "claude-code:local:sup-123",
		"node_path":  "Personal/Ackbar",
		"title":      "⚓ Ackbar Phase 2 Supervisor",
	})
	if err != nil {
		t.Fatalf("ExecuteTool failed: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success result, got %+v", res)
	}
	if receivedPayload["session_id"] != "claude-code:local:sup-123" ||
		receivedPayload["node_path"] != "Personal/Ackbar" ||
		receivedPayload["title"] != "⚓ Ackbar Phase 2 Supervisor" {
		t.Fatalf("unexpected payload sent to daemon: %+v", receivedPayload)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, `"status": "bound"`) {
		t.Fatalf("unexpected tool output: %+v", res.Content)
	}
}
