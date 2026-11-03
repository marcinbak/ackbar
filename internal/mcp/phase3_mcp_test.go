package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhase3_MCPTools(t *testing.T) {
	tools := GetRegisteredTools()
	toolsByName := make(map[string]Tool)
	for _, tool := range tools {
		toolsByName[tool.Name] = tool
	}

	// 1. Verify create_task tool schema
	createTool, ok := toolsByName["create_task"]
	if !ok {
		t.Fatalf("expected create_task tool to be registered")
	}
	if createTool.InputSchema.Properties["repos"].Type != "array" {
		t.Errorf("expected repos property to be array")
	}
	if createTool.InputSchema.Properties["task_kind"].Type != "string" {
		t.Errorf("expected task_kind property to be string")
	}

	// 2. Verify update_task has workflow_node
	updateTool, ok := toolsByName["update_task"]
	if !ok {
		t.Fatalf("expected update_task tool to be registered")
	}
	if updateTool.InputSchema.Properties["workflow_node"].Type != "string" {
		t.Errorf("expected workflow_node property on update_task")
	}

	// 3. Verify attach_deliverable has provider and content_md
	delivTool, ok := toolsByName["attach_deliverable"]
	if !ok {
		t.Fatalf("expected attach_deliverable tool to be registered")
	}
	if delivTool.InputSchema.Properties["provider"].Type != "string" {
		t.Errorf("expected provider property on attach_deliverable")
	}
	if delivTool.InputSchema.Properties["content_md"].Type != "string" {
		t.Errorf("expected content_md property on attach_deliverable")
	}

	// 4. Test ExecuteTool create_task
	var receivedTasksPayload map[string]any
	var receivedSpawnPayload map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/tasks" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&receivedTasksPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     receivedTasksPayload["id"],
				"title":  receivedTasksPayload["title"],
				"status": "NEW",
			})
			return
		}
		if r.URL.Path == "/v1/sessions/spawn" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&receivedSpawnPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "spawning",
			})
			return
		}
		if r.URL.Path == "/v1/tasks/deliverable" && r.Method == http.MethodPost {
			var deliv map[string]any
			_ = json.NewDecoder(r.Body).Decode(&deliv)
			_ = json.NewEncoder(w).Encode(deliv)
			return
		}
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
	}))
	defer ts.Close()

	client := &DaemonClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	res, err := ExecuteTool(context.Background(), client, "create_task", map[string]any{
		"title":        "Deploy cross-cluster sync",
		"project_name": "Ackbar",
		"group_name":   "Personal",
		"task_kind":    "code",
		"repos": []any{
			map[string]any{
				"repo_node_path": "Personal/Ackbar",
				"target_host":    "local",
			},
			map[string]any{
				"repo_node_path": "Modemobile/NGL/ngl-ios",
				"depends_on":     "Personal/Ackbar",
			},
		},
		"auto_start": true,
	})
	if err != nil {
		t.Fatalf("ExecuteTool create_task failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("Expected create_task success, got error: %+v", res)
	}

	if receivedTasksPayload["title"] != "Deploy cross-cluster sync" {
		t.Errorf("Unexpected title sent to daemon: %v", receivedTasksPayload["title"])
	}
	if receivedTasksPayload["task_kind"] != "code" {
		t.Errorf("Unexpected task_kind sent to daemon: %v", receivedTasksPayload["task_kind"])
	}
	workstreams, ok := receivedTasksPayload["workstreams"].([]any)
	if !ok || len(workstreams) != 2 {
		t.Fatalf("Expected 2 workstreams sent to daemon, got %+v", receivedTasksPayload["workstreams"])
	}

	// Auto start triggered spawn
	if receivedSpawnPayload["task_kind"] != "code" || receivedSpawnPayload["repo_node_path"] != "Personal/Ackbar" {
		t.Errorf("Unexpected spawn payload: %+v", receivedSpawnPayload)
	}

	// 5. Test ExecuteTool update_task with workflow_node
	resUpdate, err := ExecuteTool(context.Background(), client, "update_task", map[string]any{
		"task_id":        "task_123",
		"workflow_node":  "N2",
		"repo_node_path": "Personal/Ackbar",
	})
	if err != nil || resUpdate.IsError {
		t.Fatalf("update_task failed: %v, res: %+v", err, resUpdate)
	}
	if receivedTasksPayload["workflow_node"] != "N2" || receivedTasksPayload["repo_node_path"] != "Personal/Ackbar" {
		t.Errorf("Unexpected update payload sent to daemon: %+v", receivedTasksPayload)
	}

	// 6. Test ExecuteTool attach_deliverable with content_md
	resDeliv, err := ExecuteTool(context.Background(), client, "attach_deliverable", map[string]any{
		"task_id":    "task_123",
		"title":      "Retrospective Summary",
		"kind":       "learnings",
		"content_md": "# Retrospective\n- Completed Phase 3 successfully.",
	})
	if err != nil || resDeliv.IsError {
		t.Fatalf("attach_deliverable failed: %v, res: %+v", err, resDeliv)
	}
	if len(resDeliv.Content) == 0 || !strings.Contains(resDeliv.Content[0].Text, "successfully") {
		t.Errorf("Unexpected deliverable tool output: %+v", resDeliv.Content)
	}
}
