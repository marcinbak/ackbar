package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCP_DeliverablesToolRegistrationAndSchemas(t *testing.T) {
	tools := GetRegisteredTools()
	toolsByName := make(map[string]Tool)
	for _, tool := range tools {
		toolsByName[tool.Name] = tool
	}

	for _, name := range []string{"list_retros", "get_deliverable", "attach_deliverable"} {
		if _, ok := toolsByName[name]; !ok {
			t.Fatalf("expected tool %q to be registered", name)
		}
	}

	listRetros := toolsByName["list_retros"]
	for _, prop := range []string{"since", "until", "node_path", "repo", "host", "agent", "task_id", "limit"} {
		if _, ok := listRetros.InputSchema.Properties[prop]; !ok {
			t.Errorf("list_retros missing property %q", prop)
		}
	}

	getDel := toolsByName["get_deliverable"]
	for _, prop := range []string{"id", "deliverable_id", "task_id", "kind", "type", "max_chars", "char_offset"} {
		if _, ok := getDel.InputSchema.Properties[prop]; !ok {
			t.Errorf("get_deliverable missing property %q", prop)
		}
	}

	attachDel := toolsByName["attach_deliverable"]
	for _, prop := range []string{"task_id", "kind", "type", "title", "file_path", "url", "content_md", "provider"} {
		if _, ok := attachDel.InputSchema.Properties[prop]; !ok {
			t.Errorf("attach_deliverable missing property %q", prop)
		}
	}
	if len(attachDel.InputSchema.Required) != 1 || attachDel.InputSchema.Required[0] != "task_id" {
		t.Errorf("attach_deliverable required expected ['task_id'], got %v", attachDel.InputSchema.Required)
	}
}

func TestMCP_ExecuteListRetros(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/retros" && r.Method == http.MethodGet {
			capturedQuery = r.URL.RawQuery
			retros := []map[string]any{
				{
					"id":             "del_retro_1",
					"task_id":        "task_retro_101",
					"title":          "Retrospective",
					"host":           "legion",
					"kind":           "retro",
					"has_content_md": true,
					"content_chars":  500,
					"metrics": map[string]any{
						"node_path":     "Personal/Ackbar",
						"repo":          "ackbar",
						"branch":        "feat/mesh",
						"agent":         "claude-code",
						"outcome":       "success",
						"completed_at":  "2026-11-14T02:00:00Z",
						"tier":          "T0",
						"raw_tokens":    150000,
						"cache_hit_pct": 85.5,
						"pr_url":        "https://github.com/dev4u/ackbar/pull/42",
						"headlines":     []any{"All gates green", "Zero churn"},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"retros": retros,
				"count":  len(retros),
			})
			return
		}
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
	}))
	defer ts.Close()

	client := NewDaemonClient(ts.URL)
	ctx := context.Background()

	res, err := ExecuteTool(ctx, client, "list_retros", map[string]any{
		"repo":  "ackbar",
		"since": "2026-11-01",
		"limit": 10,
	})
	if err != nil {
		t.Fatalf("ExecuteTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error response: %s", res.Content[0].Text)
	}

	if !strings.Contains(capturedQuery, "repo=ackbar") || !strings.Contains(capturedQuery, "since=2026-11-01") {
		t.Errorf("expected query params passed to daemon, got %q", capturedQuery)
	}

	text := res.Content[0].Text
	if !strings.Contains(text, "del_retro_1") || !strings.Contains(text, "task_retro_101") {
		t.Errorf("expected retro id and task id in table, got:\n%s", text)
	}
	if !strings.Contains(text, "All gates green") {
		t.Errorf("expected headline in output, got:\n%s", text)
	}
}

func TestMCP_ExecuteGetDeliverable(t *testing.T) {
	var capturedPath string
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1/deliverables") && r.Method == http.MethodGet {
			capturedPath = r.URL.Path
			capturedQuery = r.URL.RawQuery
			del := map[string]any{
				"id":             "del_get_test",
				"task_id":        "task_get_101",
				"kind":           "retro",
				"title":          "Retrospective",
				"host":           "legion",
				"file_path":      "/path/to/retro.md",
				"content_md":     "# Full Markdown Content",
				"has_content_md": true,
				"content_bytes":  23,
				"content_chars":  23,
				"page":           1,
				"page_size":      100,
				"total_pages":    1,
				"total_chars":    23,
				"has_more":       false,
			}
			_ = json.NewEncoder(w).Encode(del)
			return
		}
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
	}))
	defer ts.Close()

	client := NewDaemonClient(ts.URL)
	ctx := context.Background()

	t.Run("By deliverable id", func(t *testing.T) {
		res, err := ExecuteTool(ctx, client, "get_deliverable", map[string]any{
			"id":          "del_get_test",
			"max_chars":   500,
			"char_offset": 0,
		})
		if err != nil {
			t.Fatalf("ExecuteTool failed: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error response: %s", res.Content[0].Text)
		}
		if capturedPath != "/v1/deliverables" {
			t.Errorf("expected path /v1/deliverables, got %q", capturedPath)
		}
		if !strings.Contains(capturedQuery, "id=del_get_test") || !strings.Contains(capturedQuery, "max_chars=500") {
			t.Errorf("expected query params, got %q", capturedQuery)
		}
		if !strings.Contains(res.Content[0].Text, "# Full Markdown Content") {
			t.Errorf("expected content in output, got:\n%s", res.Content[0].Text)
		}
	})

	t.Run("By task_id and type", func(t *testing.T) {
		res, err := ExecuteTool(ctx, client, "get_deliverable", map[string]any{
			"task_id": "task_get_101",
			"type":    "retro",
		})
		if err != nil {
			t.Fatalf("ExecuteTool failed: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error response: %s", res.Content[0].Text)
		}
		if !strings.Contains(capturedQuery, "task_id=task_get_101") || !strings.Contains(capturedQuery, "kind=retro") {
			t.Errorf("expected query params task_id and kind, got %q", capturedQuery)
		}
	})
}

func TestMCP_ExecuteAttachDeliverable(t *testing.T) {
	var capturedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/tasks/deliverable" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "del_attached_123",
				"task_id":    capturedPayload["task_id"],
				"kind":       capturedPayload["type"],
				"title":      capturedPayload["title"],
				"content_md": capturedPayload["content_md"],
			})
			return
		}
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
	}))
	defer ts.Close()

	client := NewDaemonClient(ts.URL)
	ctx := context.Background()

	res, err := ExecuteTool(ctx, client, "attach_deliverable", map[string]any{
		"task_id":    "task_attach_456",
		"type":       "retro",
		"content_md": "# Attached Content",
	})
	if err != nil {
		t.Fatalf("ExecuteTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error response: %s", res.Content[0].Text)
	}

	if capturedPayload["task_id"] != "task_attach_456" {
		t.Errorf("expected task_id task_attach_456, got %v", capturedPayload["task_id"])
	}
	if capturedPayload["type"] != "retro" {
		t.Errorf("expected type retro, got %v", capturedPayload["type"])
	}
	if capturedPayload["title"] != "Retrospective" {
		t.Errorf("expected title auto-defaulted to Retrospective, got %v", capturedPayload["title"])
	}
	if !strings.Contains(res.Content[0].Text, "del_attached_123") {
		t.Errorf("expected del_attached_123 in response, got %s", res.Content[0].Text)
	}
}
