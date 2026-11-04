package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhase4_SupervisorSessionTools(t *testing.T) {
	tools := GetRegisteredTools()
	toolsByName := make(map[string]Tool)
	for _, tool := range tools {
		toolsByName[tool.Name] = tool
	}

	// 1. Verify all 5 Supervisor Session Control tools exist in GetRegisteredTools()
	requiredTools := []string{
		"list_sessions",
		"get_session_output",
		"send_session_input",
		"spawn_session",
		"control_session",
	}

	for _, toolName := range requiredTools {
		tool, ok := toolsByName[toolName]
		if !ok {
			t.Fatalf("expected tool %q to be registered", toolName)
		}
		if tool.InputSchema.Type != "object" {
			t.Errorf("tool %q schema type expected 'object', got %q", toolName, tool.InputSchema.Type)
		}
	}

	// Verify schemas
	if _, ok := toolsByName["list_sessions"].InputSchema.Properties["federated"]; !ok {
		t.Errorf("expected federated property on list_sessions")
	}
	if _, ok := toolsByName["get_session_output"].InputSchema.Properties["recent_count"]; !ok {
		t.Errorf("expected recent_count property on get_session_output")
	}
	if _, ok := toolsByName["send_session_input"].InputSchema.Properties["action"]; !ok {
		t.Errorf("expected action property on send_session_input")
	}
	if _, ok := toolsByName["spawn_session"].InputSchema.Properties["is_supervisor"]; !ok {
		t.Errorf("expected is_supervisor property on spawn_session")
	}
	if _, ok := toolsByName["control_session"].InputSchema.Properties["action"]; !ok {
		t.Errorf("expected action property on control_session")
	}

	// 2. Mock daemon server for testing tool execution
	var (
		lastControlID       string
		lastControlAction   string
		lastSpawnPayload    map[string]any
		lastRespondPayload  map[string]any
		lastPromptPayload   map[string]any
		lastHandoverPayload map[string]any
		lastBoundPayload    map[string]any
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// GET /v1/sessions
		if r.URL.Path == "/v1/sessions" && r.Method == http.MethodGet {
			sessions := []map[string]any{
				{
					"id":            "claude-code:macmini:101",
					"agent":         "claude-code",
					"host":          "macmini",
					"state":         "working",
					"activity":      "Running unit tests",
					"context_pct":   42,
					"is_supervisor": false,
					"name":          "Feature Implementation",
				},
				{
					"id":            "claude-code:local:102",
					"agent":         "claude-code",
					"host":          "local",
					"state":         "blocked",
					"activity":      "Waiting for permission",
					"context_pct":   60,
					"is_supervisor": false,
					"blocked": map[string]any{
						"question": "Run git push origin main?",
						"options":  []any{"Allow", "Deny"},
					},
				},
				{
					"id":            "claude-code:local:103",
					"agent":         "claude-code",
					"host":          "local",
					"state":         "idle",
					"is_supervisor": true,
					"name":          "⚓ Main Supervisor",
				},
			}
			_ = json.NewEncoder(w).Encode(sessions)
			return
		}

		// GET /v1/sessions/transcript
		if r.URL.Path == "/v1/sessions/transcript" && r.Method == http.MethodGet {
			sessID := r.URL.Query().Get("id")
			resp := map[string]any{
				"session_id": sessID,
				"title":      "Feature Planning",
				"agent":      "claude-code",
				"messages": []map[string]any{
					{"role": "user", "content": "What is the plan?"},
					{"role": "assistant", "content": "First assistant reply."},
					{"role": "user", "content": "Looks good, proceed."},
					{"role": "assistant", "content": "Second assistant reply with details."},
					{"role": "assistant", "content": "Third assistant reply with plan summary."},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// POST /v1/sessions/prompt
		if r.URL.Path == "/v1/sessions/prompt" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&lastPromptPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "queued"})
			return
		}

		// POST /v1/sessions/respond
		if r.URL.Path == "/v1/sessions/respond" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&lastRespondPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "responded"})
			return
		}

		// POST /v1/sessions/spawn
		if r.URL.Path == "/v1/sessions/spawn" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&lastSpawnPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "claude-code:local:999",
				"status": "spawning",
			})
			return
		}

		// POST /v1/sessions/handover
		if r.URL.Path == "/v1/sessions/handover" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&lastHandoverPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "handover_initiated"})
			return
		}

		// POST /v1/sessions/control
		if r.URL.Path == "/v1/sessions/control" && r.Method == http.MethodPost {
			lastControlID = r.URL.Query().Get("id")
			lastControlAction = r.URL.Query().Get("action")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
			return
		}

		// POST /v1/tasks
		if r.URL.Path == "/v1/tasks" && r.Method == http.MethodPost {
			var p map[string]any
			_ = json.NewDecoder(r.Body).Decode(&p)
			_ = json.NewEncoder(w).Encode(p)
			return
		}

		// POST /v1/sessions/bind-supervisor
		if r.URL.Path == "/v1/sessions/bind-supervisor" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&lastBoundPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "bound", "bound_id": lastBoundPayload["session_id"]})
			return
		}

		http.Error(w, "unhandled: "+r.URL.Path, http.StatusNotFound)
	}))
	defer ts.Close()

	client := NewDaemonClient(ts.URL)

	ctx := context.Background()

	// 3. Test list_sessions
	t.Run("list_sessions with filters", func(t *testing.T) {
		res, err := ExecuteTool(ctx, client, "list_sessions", map[string]any{
			"state": "blocked",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %v", res.Content[0].Text)
		}
		text := res.Content[0].Text
		if !strings.Contains(text, "claude-code:local:102") {
			t.Errorf("expected blocked session 102 in output, got:\n%s", text)
		}
		if !strings.Contains(text, "Run git push origin main?") {
			t.Errorf("expected blocked question in output, got:\n%s", text)
		}
		if strings.Contains(text, "claude-code:macmini:101") {
			t.Errorf("did not expect working session 101 when filtering by blocked")
		}
	})

	// 4. Test get_session_output
	t.Run("get_session_output", func(t *testing.T) {
		res, err := ExecuteTool(ctx, client, "get_session_output", map[string]any{
			"session_id":   "claude-code:macmini:101",
			"recent_count": 2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %v", res.Content[0].Text)
		}
		text := res.Content[0].Text
		if !strings.Contains(text, "Third assistant reply") {
			t.Errorf("expected latest assistant message, got:\n%s", text)
		}
		if !strings.Contains(text, "Second assistant reply") {
			t.Errorf("expected second assistant message, got:\n%s", text)
		}
	})

	// 5. Test send_session_input
	t.Run("send_session_input action=prompt", func(t *testing.T) {
		lastPromptPayload = nil
		res, err := ExecuteTool(ctx, client, "send_session_input", map[string]any{
			"session_id": "claude-code:macmini:101",
			"input":      "Build Phase 4",
			"action":     "prompt",
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}
		if lastPromptPayload == nil || lastPromptPayload["prompt"] != "Build Phase 4" {
			t.Errorf("expected prompt payload to be sent, got: %+v", lastPromptPayload)
		}
	})

	t.Run("send_session_input action=allow", func(t *testing.T) {
		lastRespondPayload = nil
		res, err := ExecuteTool(ctx, client, "send_session_input", map[string]any{
			"session_id": "claude-code:local:102",
			"action":     "allow",
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}
		if lastRespondPayload == nil || lastRespondPayload["action"] != "allow" {
			t.Errorf("expected respond payload with allow action, got: %+v", lastRespondPayload)
		}
	})

	t.Run("send_session_input fallback on blocked session", func(t *testing.T) {
		lastRespondPayload = nil
		// session 102 is blocked in the mock sessions list, so omitting action should route to respond
		res, err := ExecuteTool(ctx, client, "send_session_input", map[string]any{
			"session_id": "claude-code:local:102",
			"input":      "yes please",
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}
		if lastRespondPayload == nil || lastRespondPayload["action"] != "answer" {
			t.Errorf("expected respond payload with answer action for blocked session, got: %+v", lastRespondPayload)
		}
	})

	// 6. Test spawn_session
	t.Run("spawn_session", func(t *testing.T) {
		lastSpawnPayload = nil
		res, err := ExecuteTool(ctx, client, "spawn_session", map[string]any{
			"target_host":   "macmini",
			"node_path":     "Personal/Ackbar",
			"prompt":        "Implement Phase 4",
			"title":         "Voice Companion",
			"is_supervisor": true,
			"headless":      true,
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}
		if lastSpawnPayload == nil {
			t.Fatalf("expected spawn payload to be sent")
		}
		if lastSpawnPayload["host"] != "macmini" || lastSpawnPayload["engine_type"] != "headless" {
			t.Errorf("unexpected spawn payload: %+v", lastSpawnPayload)
		}
	})

	// 7. Test control_session
	t.Run("control_session restart", func(t *testing.T) {
		lastControlID = ""
		lastControlAction = ""
		res, err := ExecuteTool(ctx, client, "control_session", map[string]any{
			"session_id": "claude-code:macmini:101",
			"action":     "restart",
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}
		if lastControlID != "claude-code:macmini:101" || lastControlAction != "restart" {
			t.Errorf("expected restart control on 101, got id=%s action=%s", lastControlID, lastControlAction)
		}
	})

	t.Run("control_session handover", func(t *testing.T) {
		lastHandoverPayload = nil
		res, err := ExecuteTool(ctx, client, "control_session", map[string]any{
			"session_id":   "claude-code:macmini:101",
			"action":       "handover",
			"target_agent": "codex",
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}
		if lastHandoverPayload == nil || lastHandoverPayload["target_agent"] != "codex" {
			t.Errorf("expected handover payload to be sent, got: %+v", lastHandoverPayload)
		}
	})

	t.Run("send_session_input action select_option maps to answer", func(t *testing.T) {
		lastRespondPayload = nil
		res, err := ExecuteTool(ctx, client, "send_session_input", map[string]any{
			"session_id": "claude-code:local:102",
			"action":     "select_option",
			"input":      "option-2",
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}
		if lastRespondPayload == nil || lastRespondPayload["action"] != "answer" {
			t.Errorf("expected select_option action to map to 'answer', got: %+v", lastRespondPayload)
		}
		if lastRespondPayload["value"] != "option-2" {
			t.Errorf("expected value 'option-2', got: %+v", lastRespondPayload)
		}
	})

	t.Run("bind_supervisor_session client.SessionID fallback", func(t *testing.T) {
		clientWithSess := NewDaemonClient(ts.URL, "claude-code:local:supervisor-42")
		res, err := ExecuteTool(ctx, clientWithSess, "bind_supervisor_session", map[string]any{
			"node_path": "Personal/Ackbar",
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}
		if !strings.Contains(res.Content[0].Text, "claude-code:local:supervisor-42") {
			t.Errorf("expected bound session ID in output, got: %s", res.Content[0].Text)
		}
	})

	t.Run("create_task auto-binds supervisor session and propagates custom agent/prompt", func(t *testing.T) {
		lastBoundPayload = nil
		lastSpawnPayload = nil

		clientSup := NewDaemonClient(ts.URL, "claude-code:local:supervisor-99")
		res, err := ExecuteTool(ctx, clientSup, "create_task", map[string]any{
			"title":             "Investigate memory leak",
			"project_node_path": "Modemobile/NGL/ngl-android",
			"auto_start":        true,
			"agent":             "antigravity",
			"prompt":            "Analyze heap dump in /tmp/heap.hprof",
			"repos": []any{
				"Modemobile/NGL/ngl-android",
			},
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}

		// 1. Verify supervisor was auto-bound to Modemobile/NGL
		if lastBoundPayload == nil {
			t.Fatalf("expected supervisor to be auto-bound via POST /v1/sessions/bind-supervisor")
		}
		if lastBoundPayload["session_id"] != "claude-code:local:supervisor-99" {
			t.Errorf("expected session_id 'claude-code:local:supervisor-99', got %v", lastBoundPayload["session_id"])
		}
		if lastBoundPayload["node_path"] != "Modemobile/NGL" {
			t.Errorf("expected node_path 'Modemobile/NGL', got %v", lastBoundPayload["node_path"])
		}
		if !strings.Contains(lastBoundPayload["title"].(string), "Investigate memory leak") {
			t.Errorf("expected title to contain task title, got %v", lastBoundPayload["title"])
		}

		// 2. Verify worker session was spawned with antigravity and custom prompt
		if lastSpawnPayload == nil {
			t.Fatalf("expected worker session to be spawned")
		}
		if lastSpawnPayload["agent"] != "antigravity" {
			t.Errorf("expected worker agent 'antigravity', got %v", lastSpawnPayload["agent"])
		}
		if lastSpawnPayload["prompt"] != "Analyze heap dump in /tmp/heap.hprof" {
			t.Errorf("expected worker prompt 'Analyze heap dump in /tmp/heap.hprof', got %v", lastSpawnPayload["prompt"])
		}
	})

	t.Run("create_task auto_start default agent and prompt from caller and notes", func(t *testing.T) {
		lastBoundPayload = nil
		lastSpawnPayload = nil

		clientAgy := NewDaemonClient(ts.URL, "antigravity:local:supervisor-100")
		res, err := ExecuteTool(ctx, clientAgy, "create_task", map[string]any{
			"title":        "Upgrade compiler toolchain",
			"project_name": "Ackbar",
			"group_name":   "Personal",
			"notes":        "Switch to Go 1.24 toolchain",
			"auto_start":   true,
		})
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: %v, res: %+v", err, res)
		}

		if lastSpawnPayload == nil {
			t.Fatalf("expected worker session to be spawned")
		}
		// Since caller session starts with antigravity:, agent should default to antigravity
		if lastSpawnPayload["agent"] != "antigravity" {
			t.Errorf("expected worker agent 'antigravity' inferred from caller ID, got %v", lastSpawnPayload["agent"])
		}
		p, ok := lastSpawnPayload["prompt"].(string)
		if !ok || !strings.Contains(p, "Switch to Go 1.24 toolchain") {
			t.Errorf("expected worker prompt generated from notes, got %v", lastSpawnPayload["prompt"])
		}
	})
}
