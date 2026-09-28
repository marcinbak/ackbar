package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DaemonClient interacts with the local Ackbar daemon HTTP API
type DaemonClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewDaemonClient() *DaemonClient {
	baseURL := os.Getenv("ACKBAR_DAEMON_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:7777"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	token := os.Getenv("ACKBAR_TOKEN")
	if token == "" {
		// Try reading from ~/.config/ackbar/token
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if data, err := os.ReadFile(filepath.Join(home, ".config", "ackbar", "token")); err == nil {
				token = strings.TrimSpace(string(data))
			}
		}
	}

	return &DaemonClient{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *DaemonClient) doRequest(ctx context.Context, method, endpoint string, reqBody any, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := c.BaseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer res.Body.Close()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("daemon error (%d): %s", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	if respBody != nil && len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, respBody); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// GetRegisteredTools returns the list of MCP tools supported by Ackbar
func GetRegisteredTools() []Tool {
	return []Tool{
		{
			Name:        "get_current_task",
			Description: "Resolves the current task for this agent session based on working directory, Git branch, or worktree path.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"worktree_path": {
						Type:        "string",
						Description: "Optional filesystem path of the repository or worktree. Defaults to current working directory.",
					},
					"branch": {
						Type:        "string",
						Description: "Optional current Git branch name.",
					},
				},
			},
		},
		{
			Name:        "list_tasks",
			Description: "Queries tasks on the Ackbar Work Board filtered by group, project, or status.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"group": {
						Type:        "string",
						Description: "Filter by top-level group (e.g. Modemobile, Personal). Omit for all.",
					},
					"project": {
						Type:        "string",
						Description: "Filter by project name (e.g. Ackbar, ngl-ios). Omit for all.",
					},
					"status": {
						Type:        "string",
						Description: "Filter by status: NEW, IN_PROGRESS, REVIEW, DONE.",
						Enum:        []string{"NEW", "IN_PROGRESS", "REVIEW", "DONE"},
					},
				},
			},
		},
		{
			Name:        "update_task",
			Description: "Updates task status, substatus, notes, branch, or PR details.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"task_id": {
						Type:        "string",
						Description: "Unique identifier of the task to update.",
					},
					"status": {
						Type:        "string",
						Description: "Task lifecycle status: NEW, IN_PROGRESS, REVIEW, DONE.",
						Enum:        []string{"NEW", "IN_PROGRESS", "REVIEW", "DONE"},
					},
					"substatus": {
						Type:        "string",
						Description: "Granular substatus: active, blocked, in_review, approved, completed, rejected.",
					},
					"notes": {
						Type:        "string",
						Description: "Progress update notes or summary of recent changes.",
					},
					"pr_url": {
						Type:        "string",
						Description: "Pull request URL associated with this task.",
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "report_blocker",
			Description: "Reports that the agent is blocked and waiting for user input, decision, or approval. Surfaces a high-priority red alert on the Ackbar board.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"task_id": {
						Type:        "string",
						Description: "Unique identifier of the task that is blocked.",
					},
					"question": {
						Type:        "string",
						Description: "The specific question or decision needed from the user (e.g. 'Should we downscale bitmap textures or increase JVM heap ceiling?').",
					},
				},
				Required: []string{"task_id", "question"},
			},
		},
		{
			Name:        "clear_blocker",
			Description: "Clears a previously reported blocker once the decision has been resolved and resumes task to active.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"task_id": {
						Type:        "string",
						Description: "Unique identifier of the task to unblock.",
					},
					"resolution_notes": {
						Type:        "string",
						Description: "Optional explanation of how the blocker was resolved.",
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "propose_task",
			Description: "Proposes an unexpected bug, tech debt item, or spin-off opportunity discovered during work into the NEW inbox column with duplicate detection.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"title": {
						Type:        "string",
						Description: "Clear, concise title of the proposed task.",
					},
					"group": {
						Type:        "string",
						Description: "Optional top-level group (e.g. Modemobile, Personal).",
					},
					"project": {
						Type:        "string",
						Description: "Optional project name (e.g. ngl-ios, Ackbar).",
					},
					"notes": {
						Type:        "string",
						Description: "Detailed description of the bug, tech debt, or enhancement.",
					},
					"rationale": {
						Type:        "string",
						Description: "Why this was discovered and why it should be addressed separately.",
					},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "attach_deliverable",
			Description: "Attaches a deliverable artifact (interactive HTML prototype, plan, retrospective, simulator video, screenshot) to a task.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"task_id": {
						Type:        "string",
						Description: "Unique identifier of the task.",
					},
					"title": {
						Type:        "string",
						Description: "Human-readable deliverable title (e.g. 'Change Brief', 'Generative UI Prototype').",
					},
					"kind": {
						Type:        "string",
						Description: "Artifact kind: html_ui, plan, retrospective, video, screenshot, other.",
						Enum:        []string{"html_ui", "plan", "retrospective", "video", "screenshot", "other"},
					},
					"file_path": {
						Type:        "string",
						Description: "Absolute or relative local file path to the deliverable artifact.",
					},
					"url": {
						Type:        "string",
						Description: "Optional external URL (e.g. Figma link, GitHub release).",
					},
				},
				Required: []string{"task_id", "title", "kind"},
			},
		},
		{
			Name:        "merge_pull_request",
			Description: "Squash-merges the approved pull request for a task and marks it DONE.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"task_id": {
						Type:        "string",
						Description: "Unique identifier of the task with an open PR.",
					},
					"method": {
						Type:        "string",
						Description: "Git merge strategy: squash (default), merge, or rebase.",
						Enum:        []string{"squash", "merge", "rebase"},
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "get_standup_report",
			Description: "Generates the project-grouped daily standup or retrospective report.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"group": {
						Type:        "string",
						Description: "Optional group name (e.g. Modemobile, Personal).",
					},
					"days": {
						Type:        "string",
						Description: "Number of days lookback (1 for daily standup, 7 for weekly retro). Default is 1.",
					},
				},
			},
		},
	}
}

// ExecuteTool dispatches the tool call to the local daemon
func ExecuteTool(ctx context.Context, client *DaemonClient, name string, args map[string]any) (*CallToolResult, error) {
	if client == nil {
		client = NewDaemonClient()
	}

	switch name {
	case "get_current_task":
		return executeGetCurrentTask(ctx, client, args)
	case "list_tasks":
		return executeListTasks(ctx, client, args)
	case "update_task":
		return executeUpdateTask(ctx, client, args)
	case "report_blocker":
		return executeReportBlocker(ctx, client, args)
	case "clear_blocker":
		return executeClearBlocker(ctx, client, args)
	case "propose_task":
		return executeProposeTask(ctx, client, args)
	case "attach_deliverable":
		return executeAttachDeliverable(ctx, client, args)
	case "merge_pull_request":
		return executeMergePR(ctx, client, args)
	case "get_standup_report":
		return executeGetStandup(ctx, client, args)
	default:
		return &CallToolResult{
			IsError: true,
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Unknown tool: %s", name)}},
		}, nil
	}
}

func executeGetCurrentTask(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	worktreePath := getString(args, "worktree_path")
	branch := getString(args, "branch")

	if worktreePath == "" {
		if wd, err := os.Getwd(); err == nil {
			worktreePath = wd
		}
	}
	if branch == "" && worktreePath != "" {
		// Only run git rev-parse if worktreePath is a valid directory with a .git entry
		if fi, err := os.Stat(worktreePath); err == nil && fi.IsDir() {
			gitPath := filepath.Join(worktreePath, ".git")
			if _, err := os.Stat(gitPath); err == nil {
				cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
				cmd.Dir = worktreePath
				if out, err := cmd.Output(); err == nil {
					branch = strings.TrimSpace(string(out))
				}
			}
		}
	}

	// Fetch all tasks
	var tasks []map[string]any
	if err := client.doRequest(ctx, http.MethodGet, "/v1/tasks", nil, &tasks); err != nil {
		return formatError(fmt.Sprintf("Failed to fetch tasks from daemon: %v", err)), nil
	}

	// Match logic:
	// 1. Exact match on worktree_path
	// 2. Exact match on branch
	// 3. Substring match on worktree_path basename
	var bestMatch map[string]any
	cleanWd := filepath.Clean(worktreePath)
	wdBase := filepath.Base(cleanWd)

	for _, t := range tasks {
		status := getString(t, "status")
		if status == "DONE" {
			continue // prefer active tasks
		}

		tWorktree := getString(t, "worktree_path")
		tBranch := getString(t, "branch")

		if worktreePath != "" && tWorktree != "" && filepath.Clean(tWorktree) == cleanWd {
			bestMatch = t
			break
		}
		if branch != "" && tBranch != "" && tBranch == branch {
			bestMatch = t
			break
		}
		if tWorktree != "" && filepath.Base(filepath.Clean(tWorktree)) == wdBase {
			bestMatch = t
		}
	}

	if bestMatch == nil {
		return &CallToolResult{
			Content: []ToolContent{{
				Type: "text",
				Text: fmt.Sprintf("No active task directly matched current worktree (%s) or branch (%s). Use list_tasks to search all tasks.", worktreePath, branch),
			}},
		}, nil
	}

	data, _ := json.MarshalIndent(bestMatch, "", "  ")
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: string(data)}},
	}, nil
}

func executeListTasks(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	group := getString(args, "group")
	project := getString(args, "project")
	status := getString(args, "status")

	endpoint := "/v1/tasks?"
	var params []string
	if group != "" {
		params = append(params, "group="+url.QueryEscape(group))
	}
	if project != "" {
		params = append(params, "project="+url.QueryEscape(project))
	}
	if status != "" {
		params = append(params, "status="+url.QueryEscape(status))
	}
	endpoint += strings.Join(params, "&")

	var tasks []map[string]any
	if err := client.doRequest(ctx, http.MethodGet, endpoint, nil, &tasks); err != nil {
		return formatError(fmt.Sprintf("Failed to list tasks: %v", err)), nil
	}

	// Defense-in-depth in-memory filtering
	if group != "" || project != "" || status != "" {
		var filtered []map[string]any
		for _, t := range tasks {
			if group != "" && !strings.EqualFold(getString(t, "group_name"), group) {
				continue
			}
			if project != "" && !strings.EqualFold(getString(t, "project_name"), project) {
				continue
			}
			if status != "" && !strings.EqualFold(getString(t, "status"), status) {
				continue
			}
			filtered = append(filtered, t)
		}
		tasks = filtered
	}

	data, _ := json.MarshalIndent(tasks, "", "  ")
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: string(data)}},
	}, nil
}

func executeUpdateTask(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	taskID := getString(args, "task_id")
	if taskID == "" {
		return formatError("task_id is required"), nil
	}

	payload := map[string]any{
		"id": taskID,
	}
	if s := getString(args, "status"); s != "" {
		payload["status"] = s
	}
	if sub := getString(args, "substatus"); sub != "" {
		payload["substatus"] = sub
	}
	if notes := getString(args, "notes"); notes != "" {
		payload["notes"] = notes
	}
	if prUrl := getString(args, "pr_url"); prUrl != "" {
		payload["pr_url"] = prUrl
	}

	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/tasks", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to update task: %v", err)), nil
	}

	return formatSuccess(fmt.Sprintf("Task %s updated successfully", taskID)), nil
}

func executeReportBlocker(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	taskID := getString(args, "task_id")
	question := getString(args, "question")
	if taskID == "" || question == "" {
		return formatError("task_id and question are required"), nil
	}

	// 1. Update task with substatus: blocked and blocker_question
	payload := map[string]any{
		"id":               taskID,
		"substatus":        "blocked",
		"blocker_question": question,
	}
	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/tasks", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to set task blocker: %v", err)), nil
	}

	// 2. Log event
	_ = client.doRequest(ctx, http.MethodPost, "/v1/tasks/event", map[string]any{
		"task_id":    taskID,
		"event_type": "agent_blocked",
		"payload": map[string]any{
			"question": question,
		},
	}, nil)

	return formatSuccess(fmt.Sprintf("Blocker reported for task %s. Ackbar board and notification queue updated.", taskID)), nil
}

func executeClearBlocker(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	taskID := getString(args, "task_id")
	if taskID == "" {
		return formatError("task_id is required"), nil
	}
	notes := getString(args, "resolution_notes")

	payload := map[string]any{
		"id":               taskID,
		"substatus":        "active",
		"blocker_question": "",
	}
	if notes != "" {
		payload["notes"] = notes
	}

	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/tasks", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to clear blocker: %v", err)), nil
	}

	_ = client.doRequest(ctx, http.MethodPost, "/v1/tasks/event", map[string]any{
		"task_id":    taskID,
		"event_type": "agent_unblocked",
		"payload": map[string]any{
			"resolution": notes,
		},
	}, nil)

	return formatSuccess(fmt.Sprintf("Blocker cleared for task %s. Task resumed to active.", taskID)), nil
}

func executeProposeTask(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	title := getString(args, "title")
	if title == "" {
		return formatError("title is required"), nil
	}

	payload := map[string]any{
		"title":        title,
		"group_name":   getString(args, "group"),
		"project_name": getString(args, "project"),
		"notes":        getString(args, "notes"),
		"rationale":    getString(args, "rationale"),
	}

	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/tasks/propose", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to propose task: %v", err)), nil
	}

	data, _ := json.MarshalIndent(res, "", "  ")
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: string(data)}},
	}, nil
}

func executeAttachDeliverable(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	taskID := getString(args, "task_id")
	title := getString(args, "title")
	kind := getString(args, "kind")
	if taskID == "" || title == "" || kind == "" {
		return formatError("task_id, title, and kind are required"), nil
	}

	payload := map[string]any{
		"task_id":   taskID,
		"title":     title,
		"kind":      kind,
		"file_path": getString(args, "file_path"),
		"url":       getString(args, "url"),
	}

	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/tasks/deliverable", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to attach deliverable: %v", err)), nil
	}

	return formatSuccess(fmt.Sprintf("Deliverable '%s' [%s] attached to task %s successfully.", title, kind, taskID)), nil
}

func executeMergePR(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	taskID := getString(args, "task_id")
	if taskID == "" {
		return formatError("task_id is required"), nil
	}
	method := getString(args, "method")
	if method == "" {
		method = "squash"
	}

	payload := map[string]any{
		"task_id": taskID,
		"method":  method,
	}

	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/tasks/merge-pr", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to merge PR: %v", err)), nil
	}

	data, _ := json.MarshalIndent(res, "", "  ")
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: string(data)}},
	}, nil
}

func executeGetStandup(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	group := getString(args, "group")
	days := getString(args, "days")
	if days == "" {
		days = "1"
	}

	endpoint := fmt.Sprintf("/v1/standup?format=json&days=%s", days)
	if group != "" {
		endpoint += "&group=" + group
	}

	var report map[string]any
	if err := client.doRequest(ctx, http.MethodGet, endpoint, nil, &report); err != nil {
		return formatError(fmt.Sprintf("Failed to fetch standup: %v", err)), nil
	}

	markdown, _ := report["markdown"].(string)
	spoken, _ := report["spoken_briefing"].(string)

	output := fmt.Sprintf("%s\n\n---\n🎙️ **Audio Briefing Text:**\n%s", markdown, spoken)
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: output}},
	}, nil
}

// Helpers
func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case float64:
		return fmt.Sprintf("%.0f", val)
	case int, int64, int32:
		return fmt.Sprintf("%d", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func formatSuccess(msg string) *CallToolResult {
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: msg}},
	}
}

func formatError(msg string) *CallToolResult {
	return &CallToolResult{
		IsError: true,
		Content: []ToolContent{{Type: "text", Text: "Error: " + msg}},
	}
}
