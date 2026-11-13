package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskScopeMCP_CreateTask_RepoScoping(t *testing.T) {
	ctx := context.Background()
	var lastPayload map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/tasks" && r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			lastPayload = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           "task_123",
				"group_name":   body["group_name"],
				"project_name": body["project_name"],
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewDaemonClient(ts.URL, "claude-code:local:test")

	// Case 1: group_name explicitly passed with repos, project_name omitted
	lastPayload = nil
	res, err := ExecuteTool(ctx, client, "create_task", map[string]any{
		"title":      "Test Task 1",
		"group_name": "Modemobile",
		"repos":      []any{"Modemobile/NGL/ngl-android"},
	})
	if err != nil || res.IsError {
		t.Fatalf("create_task Case 1 failed: %v, res: %+v", err, res)
	}
	if lastPayload["group_name"] != "Modemobile" {
		t.Errorf("Case 1: expected group_name Modemobile, got %v", lastPayload["group_name"])
	}
	if lastPayload["project_name"] != "NGL/ngl-android" {
		t.Errorf("Case 1: expected project_name NGL/ngl-android, got %v", lastPayload["project_name"])
	}
	if sub, ok := lastPayload["subproject_name"]; ok && sub != "" {
		t.Errorf("Case 1: expected cleared subproject_name, got %v", sub)
	}

	// Case 2: repos only matches project_node_path identically
	lastPayload = nil
	res, err = ExecuteTool(ctx, client, "create_task", map[string]any{
		"title": "Test Task 2",
		"repos": []any{"Modemobile/NGL/ngl-android"},
	})
	if err != nil || res.IsError {
		t.Fatalf("create_task Case 2 (repos) failed: %v, res: %+v", err, res)
	}
	reposPayload := lastPayload

	lastPayload = nil
	res, err = ExecuteTool(ctx, client, "create_task", map[string]any{
		"title":             "Test Task 2",
		"project_node_path": "Modemobile/NGL/ngl-android",
	})
	if err != nil || res.IsError {
		t.Fatalf("create_task Case 2 (project_node_path) failed: %v, res: %+v", err, res)
	}
	pnpPayload := lastPayload

	if reposPayload["group_name"] != pnpPayload["group_name"] || reposPayload["project_name"] != pnpPayload["project_name"] {
		t.Errorf("Case 2 mismatch: repos (%v, %v) != pnp (%v, %v)",
			reposPayload["group_name"], reposPayload["project_name"],
			pnpPayload["group_name"], pnpPayload["project_name"])
	}

	// Case 3: top-level repo_node_path
	lastPayload = nil
	res, err = ExecuteTool(ctx, client, "create_task", map[string]any{
		"title":          "Test Task 3",
		"repo_node_path": "Modemobile/NGL/ngl-android",
	})
	if err != nil || res.IsError {
		t.Fatalf("create_task Case 3 failed: %v, res: %+v", err, res)
	}
	if lastPayload["group_name"] != "Modemobile" || lastPayload["project_name"] != "NGL/ngl-android" {
		t.Errorf("Case 3: got (%v, %v), want (Modemobile, NGL/ngl-android)", lastPayload["group_name"], lastPayload["project_name"])
	}
}

func TestTaskScopeMCP_UpdateTask_SchemaAndExecution(t *testing.T) {
	ctx := context.Background()

	tools := GetRegisteredTools()
	var updateTool *Tool
	for i := range tools {
		if tools[i].Name == "update_task" {
			updateTool = &tools[i]
			break
		}
	}
	if updateTool == nil {
		t.Fatalf("update_task tool not found")
	}

	expectedProps := []string{
		"project_node_path",
		"node_path",
		"group_name",
		"project_name",
		"subproject_name",
		"title",
	}
	for _, prop := range expectedProps {
		if _, ok := updateTool.InputSchema.Properties[prop]; !ok {
			t.Errorf("expected property %q in update_task schema", prop)
		}
	}

	var lastPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/tasks" && r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			lastPayload = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           body["id"],
				"group_name":   "Modemobile",
				"project_name": "NGL/ngl-android",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewDaemonClient(ts.URL, "claude-code:local:test")

	res, err := ExecuteTool(ctx, client, "update_task", map[string]any{
		"task_id":           "task_999",
		"project_node_path": "Modemobile/NGL/ngl-android",
	})
	if err != nil || res.IsError {
		t.Fatalf("update_task failed: %v, res: %+v", err, res)
	}

	if lastPayload["project_node_path"] != "Modemobile/NGL/ngl-android" {
		t.Errorf("expected project_node_path in update payload, got %v", lastPayload["project_node_path"])
	}
	if lastPayload["subproject_name"] != "" {
		t.Errorf("expected subproject_name cleared to empty string, got %v", lastPayload["subproject_name"])
	}

	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "Modemobile/NGL/ngl-android") {
		t.Errorf("expected success message containing resolved node path, got %v", res.Content)
	}
}

func TestTaskScopeMCP_CreateTask_UTF8Prefix_NoPanic(t *testing.T) {
	ctx := context.Background()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/tasks" && r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           "task_utf8_123",
				"group_name":   body["group_name"],
				"project_name": body["project_name"],
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewDaemonClient(ts.URL, "claude-code:local:test")

	res, err := ExecuteTool(ctx, client, "create_task", map[string]any{
		"title":        "Test UTF8",
		"group_name":   "\u212a",
		"project_name": "k/",
	})
	if err != nil || res.IsError {
		t.Fatalf("create_task with UTF-8 prefix failed: %v, res: %+v", err, res)
	}
}
