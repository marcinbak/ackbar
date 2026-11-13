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
	SessionID  string
	HTTPClient *http.Client
}

func NewDaemonClient(opts ...string) *DaemonClient {
	baseURL := ""
	sessionID := ""
	if len(opts) > 0 {
		baseURL = opts[0]
	}
	if len(opts) > 1 {
		sessionID = opts[1]
	}

	if baseURL == "" {
		baseURL = os.Getenv("ACKBAR_DAEMON_URL")
	}
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
		BaseURL:   baseURL,
		Token:     token,
		SessionID: sessionID,
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
	if c.SessionID != "" {
		req.Header.Set("X-Ackbar-Session-ID", c.SessionID)
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
			Name:        "create_task",
			Description: "Creates a new task on the Ackbar Work Board with optional workstreams, dependencies, and auto-start capability.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"title": {
						Type:        "string",
						Description: "Task title.",
					},
					"project_node_path": {
						Type:        "string",
						Description: "Optional tree node path (e.g. 'Modemobile/NGL' or 'Personal/Ackbar' or 'Modemobile/NGL/ngl-ios'). Splits into group_name, project_name, and subproject_name if not explicitly set.",
					},
					"project_name": {
						Type:        "string",
						Description: "Optional project name (e.g. Ackbar, ngl-ios). Defaults to 'General'.",
					},
					"group_name": {
						Type:        "string",
						Description: "Optional group name (e.g. Modemobile, Personal). Defaults to 'Personal'.",
					},
					"notes": {
						Type:        "string",
						Description: "Optional task notes or instructions.",
					},
					"task_kind": {
						Type:        "string",
						Description: "Task kind: code or design. Defaults to 'code'.",
						Enum:        []string{"code", "design"},
					},
					"repos": {
						Type:        "array",
						Description: "Optional list of multi-repo workstreams as string paths (e.g. ['Modemobile/NGL/ngl-ios']) or objects with repo_node_path, target_host, and depends_on.",
					},
					"repo_node_path": {
						Type:        "string",
						Description: "Optional single repo node path shortcut (e.g. 'Modemobile/NGL/ngl-ios'). Treated as a single-entry workstream if repos is omitted.",
					},
					"target_host": {
						Type:        "string",
						Description: "Optional target host ('auto', 'legion', 'macmini', 'local'). Defaults to 'auto' (75/25 weighted deficit routing across capable 24/7 hosts).",
					},
					"required_capabilities": {
						Type:        "array",
						Description: "Optional list of required host capabilities (e.g. ['ios'], ['android', 'agent:antigravity'], ['mcp:gdrive']) used by the 'auto' host scheduler.",
					},
					"depends_on": {
						Type:        "string",
						Description: "Optional dependency for a single workstream.",
					},
					"auto_start": {
						Type:        "boolean",
						Description: "If true, immediately spawns an agent session for the first ready workstream.",
					},
					"agent": {
						Type:        "string",
						Description: "Optional agent runtime for spawned task worker ('claude-code', 'antigravity', 'codex'). Defaults to caller's agent or 'claude-code'.",
					},
					"prompt": {
						Type:        "string",
						Description: "Optional initial prompt or ticket instructions to send to the spawned task worker session when auto_start is true.",
					},
				},
				Required: []string{"title"},
			},
		},
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
			Description: "Updates task status, substatus, notes, branch, PR details, or workflow node.",
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
					"workflow_node": {
						Type:        "string",
						Description: "dev-workflow node lifecycle phase: N0, N1, N2, ..., N15.",
					},
					"task_kind": {
						Type:        "string",
						Description: "Task kind: code or design.",
						Enum:        []string{"code", "design"},
					},
					"repo_node_path": {
						Type:        "string",
						Description: "Optional repo node path of the active workstream being updated.",
					},
					"title": {
						Type:        "string",
						Description: "Optional updated task title.",
					},
					"project_node_path": {
						Type:        "string",
						Description: "Target project tree node path to move this task to (e.g. 'Modemobile/NGL/ngl-android' or 'Modemobile/NGL').",
					},
					"node_path": {
						Type:        "string",
						Description: "Optional alias for project_node_path.",
					},
					"group_name": {
						Type:        "string",
						Description: "Optional group name when reassigning task scope (alias: group).",
					},
					"project_name": {
						Type:        "string",
						Description: "Optional project name when reassigning task scope (alias: project).",
					},
					"subproject_name": {
						Type:        "string",
						Description: "Optional subproject label (pass empty string to clear).",
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
			Description: "Attaches a deliverable artifact (interactive HTML prototype, plan, retrospective, simulator video, screenshot, or cloud artifact) to a task.",
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
						Description: "Artifact kind: prd, html_ui, slides, plan, learnings, pr, retro, retrospective, video, screenshot, mockup, doc, other.",
						Enum:        []string{"prd", "html_ui", "slides", "plan", "learnings", "pr", "retro", "retrospective", "video", "screenshot", "mockup", "doc", "other"},
					},
					"provider": {
						Type:        "string",
						Description: "Artifact provider: claude_artifact, gdocs, gslides, gsheets, gdrive, github, local. Auto-inferred if omitted.",
						Enum:        []string{"claude_artifact", "gdocs", "gslides", "gsheets", "gdrive", "github", "local"},
					},
					"file_path": {
						Type:        "string",
						Description: "Absolute or relative local file path to the deliverable artifact.",
					},
					"url": {
						Type:        "string",
						Description: "Optional external URL (e.g. Google Docs, Slides, Claude Artifact, Figma, GitHub PR).",
					},
					"content_md": {
						Type:        "string",
						Description: "Raw markdown content for brief, retrospective, architecture plan, or learnings deliverable.",
					},
				},
				Required: []string{"task_id", "title"},
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
		{
			Name:        "bind_supervisor_session",
			Description: "Binds a Supervisor session to a target project tree node and sets its custom title on Turn 1.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"session_id": {
						Type:        "string",
						Description: "Session ID or native UUID of the supervisor session.",
					},
					"node_path": {
						Type:        "string",
						Description: "Target project node path in the Ackbar tree (e.g. 'Personal/Ackbar' or 'Modemobile/NGL').",
					},
					"project_node_path": {
						Type:        "string",
						Description: "Optional alias for node_path.",
					},
					"title": {
						Type:        "string",
						Description: "Human-readable title for the Supervisor session (e.g. '⚓ NGL Feature Planning').",
					},
				},
			},
		},
		{
			Name:        "list_sessions",
			Description: "List active, blocked, or idle sessions across local and federated hosts with current activity, context %, and blocker questions.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"host": {
						Type:        "string",
						Description: "Optional machine alias filter (e.g. 'local', 'devbox', 'macmini').",
					},
					"state": {
						Type:        "string",
						Description: "Optional session state filter ('working', 'idle', 'blocked', 'ended').",
						Enum:        []string{"working", "idle", "blocked", "ended"},
					},
					"is_supervisor": {
						Type:        "boolean",
						Description: "Optional filter for supervisor sessions.",
					},
					"federated": {
						Type:        "boolean",
						Description: "Whether to query federated peer hosts (default true).",
					},
				},
			},
		},
		{
			Name:        "get_session_output",
			Description: "Fetch the latest assistant messages, active implementation plan, or current blocker options for a session.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"session_id": {
						Type:        "string",
						Description: "Unique session ID (e.g. 'claude-code:macmini:8492' or local native ID).",
					},
					"recent_count": {
						Type:        "integer",
						Description: "Number of recent assistant messages to return (default 3).",
					},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name:        "send_session_input",
			Description: "Send a user prompt, answer a blocker question, or respond to an interactive permission prompt for a session.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"session_id": {
						Type:        "string",
						Description: "Target session ID.",
					},
					"input": {
						Type:        "string",
						Description: "Text input to send, answer to blocker question, or follow-up prompt (alias: text, prompt).",
					},
					"text": {
						Type:        "string",
						Description: "Alias for input.",
					},
					"prompt": {
						Type:        "string",
						Description: "Alias for input.",
					},
					"action": {
						Type:        "string",
						Description: "Action type: 'prompt' (new user prompt), 'answer' (answer blocker), 'allow' (permit operation), 'deny' (deny operation), 'select_option' (select choice).",
						Enum:        []string{"prompt", "answer", "allow", "deny", "select_option"},
					},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name:        "list_hosts",
			Description: "List all registered fleet hosts (local and remote) with their online status, detected capabilities (ios, android, agent:*, mcp:*), policy tags (24/7, drain), routing weight (75/25), and live load telemetry (working_sessions, cpu_load_pct, ram_free_gb, overloaded).",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"include_metrics": {
						Type:        "boolean",
						Description: "If true, includes multi-day historical utilization summaries and 24h hourly heatmap.",
					},
					"days": {
						Type:        "integer",
						Description: "Lookback window in days when include_metrics is true (default 14).",
					},
				},
			},
		},
		{
			Name:        "spawn_session",
			Description: "Spawn a new agent session on a local or remote host (or 'auto' via capability & 75/25 weighted load balancing) with an initial prompt or task assignment.",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"target_host": {
						Type:        "string",
						Description: "Target machine alias ('auto', 'local', 'macmini', 'legion'). Pass 'auto' for capability + 75/25 weighted load balancing.",
					},
					"required_capabilities": {
						Type:        "array",
						Description: "Optional list of required host capabilities (e.g. ['ios'], ['android'], ['mcp:gdrive']) for 'auto' host selection.",
					},
					"node_path": {
						Type:        "string",
						Description: "Logical tree node path (e.g. 'Personal/Ackbar' or 'Modemobile/NGL/ngl-ios').",
					},
					"directory": {
						Type:        "string",
						Description: "Working directory on the target host. Optional if node_path is provided.",
					},
					"agent": {
						Type:        "string",
						Description: "Agent runtime ('claude-code', 'codex', 'antigravity'). Defaults to 'claude-code'.",
					},
					"prompt": {
						Type:        "string",
						Description: "Initial prompt or task description to execute.",
					},
					"title": {
						Type:        "string",
						Description: "Human-readable title for the session.",
					},
					"is_supervisor": {
						Type:        "boolean",
						Description: "Whether to spawn this as an Ackbar Supervisor session.",
					},
					"task_id": {
						Type:        "string",
						Description: "Optional associated task ID.",
					},
					"task_kind": {
						Type:        "string",
						Description: "Task kind ('code' or 'design').",
					},
					"headless": {
						Type:        "boolean",
						Description: "Whether to run in headless streaming mode instead of tmux.",
					},
				},
			},
		},
		{
			Name:        "control_session",
			Description: "Execute lifecycle control actions on a session (resume, restart, kill, handover, done, later).",
			InputSchema: ToolInputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"session_id": {
						Type:        "string",
						Description: "Target session ID.",
					},
					"action": {
						Type:        "string",
						Description: "Lifecycle action to perform.",
						Enum:        []string{"resume", "restart", "kill", "handover", "done", "later"},
					},
					"target_agent": {
						Type:        "string",
						Description: "Target agent for handover action (optional).",
					},
				},
				Required: []string{"session_id", "action"},
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
	case "create_task":
		return executeCreateTask(ctx, client, args)
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
	case "bind_supervisor_session":
		return executeBindSupervisorSession(ctx, client, args)
	case "list_hosts":
		return executeListHosts(ctx, client, args)
	case "list_sessions":
		return executeListSessions(ctx, client, args)
	case "get_session_output":
		return executeGetSessionOutput(ctx, client, args)
	case "send_session_input":
		return executeSendSessionInput(ctx, client, args)
	case "spawn_session":
		return executeSpawnSession(ctx, client, args)
	case "control_session":
		return executeControlSession(ctx, client, args)
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

func executeCreateTask(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	title := getString(args, "title")
	if title == "" {
		return formatError("title is required"), nil
	}

	projectNodePath := getString(args, "project_node_path")
	if projectNodePath == "" {
		projectNodePath = getString(args, "node_path")
	}
	var pGroup, pProject, pSubproject string
	if projectNodePath != "" {
		parts := strings.Split(strings.Trim(projectNodePath, "/"), "/")
		if len(parts) >= 1 && parts[0] != "" {
			pGroup = parts[0]
		}
		if len(parts) >= 2 {
			pProject = strings.Join(parts[1:], "/")
		}
	}

	projectName := getString(args, "project_name")
	if projectName == "" {
		projectName = getString(args, "project")
	}
	if projectName == "" && pProject != "" {
		projectName = pProject
	}

	groupName := getString(args, "group_name")
	if groupName == "" {
		groupName = getString(args, "group")
	}
	if groupName == "" && pGroup != "" {
		groupName = pGroup
	}

	subprojectName := getString(args, "subproject_name")
	if subprojectName == "" && pSubproject != "" {
		subprojectName = pSubproject
	}

	repoNodePath := getString(args, "repo_node_path")
	if reposRaw, ok := args["repos"].([]any); (!ok || len(reposRaw) == 0) && repoNodePath != "" {
		args["repos"] = []any{repoNodePath}
	}

	// If project_node_path, group_name, and project_name were not explicitly provided,
	// derive pGroup, pProject, and pSubproject from the first entry in repos/workstreams:
	if projectNodePath == "" && (projectName == "" || strings.EqualFold(projectName, "General")) {
		firstRepo := ""
		if reposRaw, ok := args["repos"].([]any); ok && len(reposRaw) > 0 {
			if rStr, ok := reposRaw[0].(string); ok {
				firstRepo = strings.TrimSpace(rStr)
			} else if rMap, ok := reposRaw[0].(map[string]any); ok {
				firstRepo = getString(rMap, "repo_node_path")
				if firstRepo == "" {
					firstRepo = getString(rMap, "path")
				}
			}
		}
		if firstRepo != "" {
			parts := strings.Split(strings.Trim(firstRepo, "/"), "/")
			if len(parts) >= 1 && parts[0] != "" {
				pGroup = parts[0]
				if groupName == "" {
					groupName = parts[0]
				}
			}
			if len(parts) >= 2 {
				pProject = strings.Join(parts[1:], "/")
				projectName = pProject
				subprojectName = ""
			}
		}
	}

	if groupName != "" && strings.HasPrefix(strings.ToLower(projectName), strings.ToLower(groupName)+"/") {
		projectName = projectName[len(groupName)+1:]
	} else if groupName == "" && strings.Contains(projectName, "/") {
		parts := strings.Split(projectName, "/")
		groupName = parts[0]
		projectName = strings.Join(parts[1:], "/")
	}

	if projectName == "" {
		projectName = "General"
	}
	if groupName == "" {
		groupName = "Personal"
	}

	taskKind := getString(args, "task_kind")
	if taskKind == "" {
		taskKind = "code"
	}

	taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())
	autoStart, _ := getBool(args, "auto_start")
	initialStatus := "NEW"
	initialSubstatus := "ready"
	if autoStart {
		initialStatus = "IN_PROGRESS"
		initialSubstatus = "active"
	}

	payload := map[string]any{
		"id":           taskID,
		"title":        title,
		"project_name": projectName,
		"group_name":   groupName,
		"status":       initialStatus,
		"substatus":    initialSubstatus,
		"task_kind":    taskKind,
		"notes":        getString(args, "notes"),
	}
	if projectNodePath != "" {
		payload["project_node_path"] = projectNodePath
	}
	if subprojectName != "" {
		payload["subproject_name"] = subprojectName
	}

	topDependsOn := getString(args, "depends_on")
	topTargetHost := getString(args, "target_host")
	reqCaps := getStringSlice(args, "required_capabilities")
	if topTargetHost != "" {
		payload["target_host"] = topTargetHost
	}
	if len(reqCaps) > 0 {
		payload["required_capabilities"] = reqCaps
	}

	// Build workstreams if provided
	var workstreams []map[string]any
	if reposRaw, ok := args["repos"].([]any); ok && len(reposRaw) > 0 {
		for _, rItem := range reposRaw {
			if rStr, ok := rItem.(string); ok {
				rStr = strings.TrimSpace(rStr)
				if rStr == "" {
					continue
				}
				wsStatus := "IN_PROGRESS"
				var dep string
				if topDependsOn != "" {
					if strings.EqualFold(rStr, topDependsOn) {
						wsStatus = "IN_PROGRESS"
						dep = ""
					} else {
						wsStatus = "QUEUED"
						dep = topDependsOn
					}
				}
				workstreams = append(workstreams, map[string]any{
					"task_id":        taskID,
					"repo_node_path": rStr,
					"target_host":    topTargetHost,
					"depends_on":     dep,
					"workflow_node":  "N0",
					"status":         wsStatus,
					"updated_at":     time.Now().UTC(),
				})
			} else if rMap, ok := rItem.(map[string]any); ok {
				rPath := getString(rMap, "repo_node_path")
				if rPath == "" {
					rPath = getString(rMap, "path")
				}
				if rPath == "" {
					continue
				}
				dep := getString(rMap, "depends_on")
				if dep == "" && topDependsOn != "" && !strings.EqualFold(rPath, topDependsOn) {
					dep = topDependsOn
				}
				wsStatus := "IN_PROGRESS"
				if dep != "" {
					wsStatus = "QUEUED"
				}
				targetHost := getString(rMap, "target_host")
				if targetHost == "" {
					targetHost = topTargetHost
				}
				workstreams = append(workstreams, map[string]any{
					"task_id":        taskID,
					"repo_node_path": rPath,
					"target_host":    targetHost,
					"depends_on":     dep,
					"workflow_node":  "N0",
					"status":         wsStatus,
					"updated_at":     time.Now().UTC(),
				})
			}
		}
	} else if topDependsOn != "" {
		workstreams = append(workstreams, map[string]any{
			"task_id":        taskID,
			"repo_node_path": projectName,
			"target_host":    topTargetHost,
			"depends_on":     topDependsOn,
			"workflow_node":  "N0",
			"status":         "QUEUED",
			"updated_at":     time.Now().UTC(),
		})
	}

	if len(workstreams) > 0 {
		payload["workstreams"] = workstreams
	}

	var createdTask map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/tasks", payload, &createdTask); err != nil {
		return formatError(fmt.Sprintf("Failed to create task: %v", err)), nil
	}

	callerSessID := client.SessionID
	if callerSessID == "" {
		callerSessID = os.Getenv("ACKBAR_SESSION_ID")
	}
	if callerSessID == "" {
		callerSessID = os.Getenv("CLAUDE_SESSION_ID")
	}

	// Auto-bind Supervisor fallback: if caller is a supervisor session and not bound yet, bind to target project node
	if callerSessID != "" {
		isSupervisor := os.Getenv("ACKBAR_IS_SUPERVISOR") == "1" || strings.Contains(callerSessID, "supervisor")
		nodePathEmpty := true

		var sessList []map[string]any
		if err := client.doRequest(ctx, http.MethodGet, "/v1/sessions", nil, &sessList); err == nil {
			for _, s := range sessList {
				id, _ := s["id"].(string)
				oldID, _ := s["old_id"].(string)
				nativeID, _ := s["native_id"].(string)
				if id == callerSessID || (oldID != "" && oldID == callerSessID) || (nativeID != "" && nativeID == callerSessID) {
					if isSup, ok := s["is_supervisor"].(bool); ok && isSup {
						isSupervisor = true
					}
					if np, _ := s["node_path"].(string); strings.TrimSpace(np) != "" {
						nodePathEmpty = false
					}
					if id != "" {
						callerSessID = id
					}
					break
				}
			}
		}

		if isSupervisor && nodePathEmpty {
			targetNodePath := ""
			if pGroup != "" && pProject != "" {
				parts := strings.Split(pProject, "/")
				targetNodePath = pGroup + "/" + parts[0]
			} else if projectNodePath != "" {
				parts := strings.Split(strings.Trim(projectNodePath, "/"), "/")
				if len(parts) >= 2 {
					targetNodePath = parts[0] + "/" + parts[1]
				} else {
					targetNodePath = projectNodePath
				}
			}
			if targetNodePath == "" {
				targetNodePath = groupName + "/" + projectName
			}
			if (targetNodePath == "" || targetNodePath == "Personal/General") && len(workstreams) > 0 {
				rPath := getString(workstreams[0], "repo_node_path")
				if rPath != "" {
					parts := strings.Split(strings.Trim(rPath, "/"), "/")
					if len(parts) >= 2 {
						targetNodePath = parts[0] + "/" + parts[1]
						if projectName == "General" {
							projectName = parts[1]
						}
					} else {
						targetNodePath = rPath
						if projectName == "General" {
							projectName = rPath
						}
					}
				}
			}
			bindTitle := "⚓ " + projectName + ": " + title
			var bindRes map[string]any
			_ = client.doRequest(ctx, http.MethodPost, "/v1/sessions/bind-supervisor", map[string]any{
				"session_id": callerSessID,
				"node_path":  targetNodePath,
				"title":      bindTitle,
			}, &bindRes)
		}
	}

	if autoStart {
		readyRepo := ""
		readyHost := ""
		// Use createdTask's resolved workstreams if available
		if createdWS, ok := createdTask["workstreams"].([]any); ok && len(createdWS) > 0 {
			for _, wsItem := range createdWS {
				if ws, mOk := wsItem.(map[string]any); mOk && getString(ws, "status") == "IN_PROGRESS" {
					readyRepo = getString(ws, "repo_node_path")
					readyHost = getString(ws, "target_host")
					break
				}
			}
		}
		if readyRepo == "" && len(workstreams) > 0 {
			for _, ws := range workstreams {
				if getString(ws, "status") == "IN_PROGRESS" {
					readyRepo = getString(ws, "repo_node_path")
					readyHost = getString(ws, "target_host")
					break
				}
			}
		}
		if readyHost == "" {
			if topTargetHost != "" {
				readyHost = topTargetHost
			} else if len(reqCaps) > 0 || readyRepo != "" {
				readyHost = "auto"
			}
		}
		spawnCwd := readyRepo
		if spawnCwd == "" {
			spawnCwd = projectName
		}

		workerAgent := getString(args, "agent")
		if workerAgent == "" {
			if strings.HasPrefix(callerSessID, "antigravity:") {
				workerAgent = "antigravity"
			} else {
				workerAgent = "claude-code"
			}
		}

		workerPrompt := getString(args, "prompt")
		if workerPrompt == "" {
			notes := getString(args, "notes")
			if notes != "" {
				workerPrompt = fmt.Sprintf("Work on task %s (%s): %s", taskID, title, notes)
			} else {
				workerPrompt = fmt.Sprintf("Work on task %s: %s", taskID, title)
			}
		}

		spawnPayload := map[string]any{
			"agent":                 workerAgent,
			"cwd":                   spawnCwd,
			"node_path":             readyRepo,
			"host":                  readyHost,
			"required_capabilities": reqCaps,
			"task_id":               taskID,
			"task_kind":             taskKind,
			"repo_node_path":        readyRepo,
			"name":                  title,
			"prompt":                workerPrompt,
		}
		var spawnRes map[string]any
		_ = client.doRequest(ctx, http.MethodPost, "/v1/sessions/spawn", spawnPayload, &spawnRes)
		if spawnRes != nil {
			if warn, ok := spawnRes["load_warning"].(string); ok && warn != "" {
				createdTask["load_warning"] = warn
			}
			if reason, ok := spawnRes["routing_reason"].(string); ok && reason != "" {
				createdTask["routing_reason"] = reason
			}
			if h, ok := spawnRes["host"].(string); ok && h != "" {
				createdTask["spawned_host"] = h
			}
		}
	}

	data, _ := json.MarshalIndent(createdTask, "", "  ")
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
	if t := getString(args, "title"); t != "" {
		payload["title"] = t
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
	if wn := getString(args, "workflow_node"); wn != "" {
		payload["workflow_node"] = wn
	}
	if tk := getString(args, "task_kind"); tk != "" {
		payload["task_kind"] = tk
	}
	if rnp := getString(args, "repo_node_path"); rnp != "" {
		payload["repo_node_path"] = rnp
	}

	pnp := getString(args, "project_node_path")
	if pnp == "" {
		pnp = getString(args, "node_path")
	}
	if pnp != "" {
		payload["project_node_path"] = pnp
	}

	gn := getString(args, "group_name")
	if gn == "" {
		gn = getString(args, "group")
	}
	if gn != "" {
		payload["group_name"] = gn
	}

	pn := getString(args, "project_name")
	if pn == "" {
		pn = getString(args, "project")
	}
	if pn != "" {
		payload["project_name"] = pn
	}

	if _, hasSub := args["subproject_name"]; hasSub {
		payload["subproject_name"] = getString(args, "subproject_name")
	} else if pnp != "" {
		payload["subproject_name"] = ""
	}

	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/tasks", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to update task: %v", err)), nil
	}

	if resGroup, ok := res["group_name"].(string); ok && resGroup != "" {
		if resProj, ok := res["project_name"].(string); ok && resProj != "" {
			return formatSuccess(fmt.Sprintf("Task %s updated successfully (%s/%s)", taskID, resGroup, resProj)), nil
		}
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

	groupName := getString(args, "group_name")
	if groupName == "" {
		groupName = getString(args, "group")
	}
	projectName := getString(args, "project_name")
	if projectName == "" {
		projectName = getString(args, "project")
	}
	if groupName != "" && strings.HasPrefix(strings.ToLower(projectName), strings.ToLower(groupName)+"/") {
		projectName = projectName[len(groupName)+1:]
	} else if groupName == "" && strings.Contains(projectName, "/") {
		parts := strings.Split(projectName, "/")
		groupName = parts[0]
		projectName = strings.Join(parts[1:], "/")
	}

	payload := map[string]any{
		"title":        title,
		"group_name":   groupName,
		"project_name": projectName,
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
	if kind == "" {
		kind = "doc"
	}
	if taskID == "" || title == "" {
		return formatError("task_id and title are required"), nil
	}

	filePath := getString(args, "file_path")
	urlStr := getString(args, "url")
	contentMD := getString(args, "content_md")
	provider := getString(args, "provider")

	if filePath == "" && urlStr == "" && contentMD == "" {
		return formatError("either file_path, url, or content_md is required"), nil
	}

	payload := map[string]any{
		"task_id":    taskID,
		"title":      title,
		"kind":       kind,
		"file_path":  filePath,
		"url":        urlStr,
		"provider":   provider,
		"content_md": contentMD,
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

func executeBindSupervisorSession(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	sessionID := getString(args, "session_id")
	if sessionID == "" && client != nil && client.SessionID != "" {
		sessionID = client.SessionID
	}
	if sessionID == "" {
		sessionID = os.Getenv("ACKBAR_SESSION_ID")
	}
	if sessionID == "" {
		sessionID = os.Getenv("CLAUDE_SESSION_ID")
	}

	nodePath := getString(args, "node_path")
	if nodePath == "" {
		nodePath = getString(args, "project_node_path")
	}

	title := getString(args, "title")
	if title == "" {
		title = getString(args, "custom_title")
	}

	if sessionID == "" || nodePath == "" {
		return formatError("session_id and node_path are required"), nil
	}

	payload := map[string]any{
		"session_id": sessionID,
		"node_path":  nodePath,
		"title":      title,
	}

	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/sessions/bind-supervisor", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to bind supervisor session: %v", err)), nil
	}

	data, _ := json.MarshalIndent(res, "", "  ")
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: string(data)}},
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

func getBool(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	switch val := v.(type) {
	case bool:
		return val, true
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		if s == "true" || s == "1" || s == "yes" {
			return true, true
		}
		if s == "false" || s == "0" || s == "no" {
			return false, true
		}
	case float64:
		return val != 0, true
	case int, int64, int32:
		return val != 0, true
	}
	return false, false
}

func getInt(m map[string]any, key string, defaultVal int) int {
	if m == nil {
		return defaultVal
	}
	v, ok := m[key]
	if !ok || v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(val), "%d", &n); err == nil {
			return n
		}
	}
	return defaultVal
}

func executeListSessions(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	hostFilter := getString(args, "host")
	stateFilter := strings.ToLower(getString(args, "state"))
	isSupervisorFilter, hasIsSupervisor := getBool(args, "is_supervisor")
	federated := true
	if fedVal, hasFed := getBool(args, "federated"); hasFed {
		federated = fedVal
	}

	endpoint := fmt.Sprintf("/v1/sessions?federated=%t", federated)
	var sessions []map[string]any
	if err := client.doRequest(ctx, http.MethodGet, endpoint, nil, &sessions); err != nil {
		return formatError(fmt.Sprintf("Failed to list sessions: %v", err)), nil
	}

	var matched []map[string]any
	for _, sess := range sessions {
		if hostFilter != "" && !strings.EqualFold(getString(sess, "host"), hostFilter) {
			continue
		}
		if stateFilter != "" {
			sessStateStr := strings.ToLower(getString(sess, "state"))
			switch sessStateStr {
			case "1":
				sessStateStr = "working"
			case "2":
				sessStateStr = "blocked"
			case "3":
				sessStateStr = "idle"
			case "4":
				sessStateStr = "ended"
			case "5":
				sessStateStr = "failed"
			}
			if sessStateStr != stateFilter {
				continue
			}
		}
		if hasIsSupervisor {
			isSup, _ := getBool(sess, "is_supervisor")
			if isSup != isSupervisorFilter {
				continue
			}
		}
		matched = append(matched, sess)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d session(s):\n\n", len(matched)))
	for _, s := range matched {
		id := getString(s, "id")
		name := getString(s, "name")
		if name == "" {
			name = getString(s, "custom_title")
		}
		if name == "" {
			name = id
		}
		agent := getString(s, "agent")
		host := getString(s, "host")
		stateStr := getString(s, "state")
		switch stateStr {
		case "1":
			stateStr = "working"
		case "2":
			stateStr = "blocked"
		case "3":
			stateStr = "idle"
		case "4":
			stateStr = "ended"
		case "5":
			stateStr = "failed"
		}
		activity := getString(s, "activity")
		ctxPct := getString(s, "context_pct")
		taskID := getString(s, "current_task_id")

		sb.WriteString(fmt.Sprintf("- **%s** (`%s`)\n", name, id))
		sb.WriteString(fmt.Sprintf("  - Host: `%s` | Agent: `%s` | State: **%s**", host, agent, stateStr))
		if ctxPct != "" && ctxPct != "0" {
			sb.WriteString(fmt.Sprintf(" | Context: %s%%", ctxPct))
		}
		if taskID != "" {
			sb.WriteString(fmt.Sprintf(" | Task: `%s`", taskID))
		}
		sb.WriteString("\n")
		if activity != "" {
			sb.WriteString(fmt.Sprintf("  - Activity: %s\n", activity))
		}
		if blocked, ok := s["blocked"].(map[string]any); ok && blocked != nil {
			q := getString(blocked, "question")
			if q != "" {
				sb.WriteString(fmt.Sprintf("  - ⚠️ Blocked Question: %s\n", q))
			}
			if opts, ok := blocked["options"].([]any); ok && len(opts) > 0 {
				var optStrs []string
				for _, opt := range opts {
					optStrs = append(optStrs, fmt.Sprint(opt))
				}
				sb.WriteString(fmt.Sprintf("  - Choices: [%s]\n", strings.Join(optStrs, ", ")))
			}
		}
	}

	jsonBytes, _ := json.MarshalIndent(matched, "", "  ")
	sb.WriteString("\n```json\n")
	sb.WriteString(string(jsonBytes))
	sb.WriteString("\n```\n")

	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: sb.String()}},
	}, nil
}

func executeGetSessionOutput(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	sessionID := getString(args, "session_id")
	if sessionID == "" {
		return formatError("session_id is required"), nil
	}
	recentCount := getInt(args, "recent_count", 3)
	if recentCount <= 0 {
		recentCount = 3
	}

	endpoint := fmt.Sprintf("/v1/sessions/transcript?id=%s&format=json", url.QueryEscape(sessionID))
	var data map[string]any
	if err := client.doRequest(ctx, http.MethodGet, endpoint, nil, &data); err != nil {
		return formatError(fmt.Sprintf("Failed to fetch session transcript: %v", err)), nil
	}

	title := getString(data, "title")
	agent := getString(data, "agent")
	messagesRaw, _ := data["messages"].([]any)

	var assistantMsgs []map[string]any
	for _, mRaw := range messagesRaw {
		if m, ok := mRaw.(map[string]any); ok {
			role := getString(m, "role")
			if role == "assistant" {
				assistantMsgs = append(assistantMsgs, m)
			}
		}
	}
	// Fallback to all messages if no assistant messages explicitly identified
	if len(assistantMsgs) == 0 {
		for _, mRaw := range messagesRaw {
			if m, ok := mRaw.(map[string]any); ok {
				assistantMsgs = append(assistantMsgs, m)
			}
		}
	}

	startIdx := 0
	if len(assistantMsgs) > recentCount {
		startIdx = len(assistantMsgs) - recentCount
	}
	recent := assistantMsgs[startIdx:]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Output for Session `%s`\n", sessionID))
	if title != "" {
		sb.WriteString(fmt.Sprintf("**Title:** %s | **Agent:** %s\n\n", title, agent))
	}

	if len(recent) == 0 {
		sb.WriteString("*(No messages found in transcript)*\n")
	} else {
		for i, m := range recent {
			role := getString(m, "role")
			content := getString(m, "content")
			sb.WriteString(fmt.Sprintf("#### Message %d (%s):\n%s\n\n", i+1, role, content))
		}
	}

	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: strings.TrimSpace(sb.String())}},
	}, nil
}

func executeSendSessionInput(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	sessionID := getString(args, "session_id")
	if sessionID == "" {
		return formatError("session_id is required"), nil
	}
	input := getString(args, "input")
	if input == "" {
		input = getString(args, "text")
	}
	if input == "" {
		input = getString(args, "prompt")
	}
	action := strings.ToLower(getString(args, "action"))

	// 1. If explicit response action (allow/deny/answer/select_option), send to /v1/sessions/respond
	if action == "allow" || action == "deny" || action == "answer" || action == "select_option" {
		targetAction := action
		if targetAction == "select_option" {
			targetAction = "answer"
		}
		payload := map[string]any{
			"id":         sessionID,
			"session_id": sessionID,
			"action":     targetAction,
			"value":      input,
			"input":      input,
		}
		var res map[string]any
		if err := client.doRequest(ctx, http.MethodPost, "/v1/sessions/respond", payload, &res); err != nil {
			return formatError(fmt.Sprintf("Failed to respond to session blocker: %v", err)), nil
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return &CallToolResult{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Responded with action '%s':\n%s", targetAction, string(data))}},
		}, nil
	}

	// 2. If action is "prompt", send to /v1/sessions/prompt
	if action == "prompt" {
		payload := map[string]any{
			"id":         sessionID,
			"session_id": sessionID,
			"prompt":     input,
		}
		var res map[string]any
		if err := client.doRequest(ctx, http.MethodPost, "/v1/sessions/prompt", payload, &res); err != nil {
			return formatError(fmt.Sprintf("Failed to send prompt to session: %v", err)), nil
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return &CallToolResult{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Prompt dispatched:\n%s", string(data))}},
		}, nil
	}

	// 3. Action is omitted (""): check if session is currently blocked
	isBlocked := false
	var sessions []map[string]any
	if err := client.doRequest(ctx, http.MethodGet, "/v1/sessions?federated=true", nil, &sessions); err == nil {
		for _, s := range sessions {
			if getString(s, "id") == sessionID || getString(s, "native_id") == sessionID {
				st := strings.ToLower(getString(s, "state"))
				if st == "2" || st == "blocked" || s["blocked"] != nil {
					isBlocked = true
				}
				break
			}
		}
	}

	if isBlocked {
		payload := map[string]any{
			"id":         sessionID,
			"session_id": sessionID,
			"action":     "answer",
			"value":      input,
			"input":      input,
		}
		var res map[string]any
		if err := client.doRequest(ctx, http.MethodPost, "/v1/sessions/respond", payload, &res); err != nil {
			return formatError(fmt.Sprintf("Failed to answer blocked session: %v", err)), nil
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return &CallToolResult{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Answered blocked session:\n%s", string(data))}},
		}, nil
	}

	// Send normal prompt
	promptPayload := map[string]any{
		"id":         sessionID,
		"session_id": sessionID,
		"prompt":     input,
	}
	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/sessions/prompt", promptPayload, &res); err != nil {
		// Fallback to respond if prompt returned an error
		respondPayload := map[string]any{
			"id":         sessionID,
			"session_id": sessionID,
			"action":     "answer",
			"value":      input,
			"input":      input,
		}
		if respErr := client.doRequest(ctx, http.MethodPost, "/v1/sessions/respond", respondPayload, &res); respErr == nil {
			data, _ := json.MarshalIndent(res, "", "  ")
			return &CallToolResult{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Responded to session:\n%s", string(data))}},
			}, nil
		}
		return formatError(fmt.Sprintf("Failed to send prompt to session: %v", err)), nil
	}

	data, _ := json.MarshalIndent(res, "", "  ")
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Prompt dispatched:\n%s", string(data))}},
	}, nil
}

func getStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	var out []string
	switch arr := v.(type) {
	case []string:
		for _, s := range arr {
			if st := strings.TrimSpace(s); st != "" {
				out = append(out, st)
			}
		}
	case []any:
		for _, it := range arr {
			if st := strings.TrimSpace(fmt.Sprint(it)); st != "" {
				out = append(out, st)
			}
		}
	case string:
		for _, part := range strings.Split(arr, ",") {
			if st := strings.TrimSpace(part); st != "" {
				out = append(out, st)
			}
		}
	}
	return out
}

func executeListHosts(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	includeMetrics, _ := getBool(args, "include_metrics")
	if includeMetrics {
		days := getInt(args, "days", 14)
		var utilData map[string]any
		endpoint := fmt.Sprintf("/v1/hosts/utilization?days=%d", days)
		if err := client.doRequest(ctx, http.MethodGet, endpoint, nil, &utilData); err != nil {
			return formatError(fmt.Sprintf("Failed to query host utilization: %v", err)), nil
		}
		data, _ := json.MarshalIndent(utilData, "", "  ")
		return &CallToolResult{
			Content: []ToolContent{{Type: "text", Text: string(data)}},
		}, nil
	}

	var hosts []map[string]any
	if err := client.doRequest(ctx, http.MethodGet, "/v1/hosts?include_local=true", nil, &hosts); err != nil {
		return formatError(fmt.Sprintf("Failed to list hosts: %v", err)), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d fleet host(s):\n\n", len(hosts)))
	for _, h := range hosts {
		name := getString(h, "name")
		disp := getString(h, "display_name")
		if disp == "" {
			disp = name
		}
		online, _ := getBool(h, "online")
		statusStr := "🟢 online"
		if !online {
			statusStr = "⚪ offline"
		}
		weight := getInt(h, "routing_weight", 0)
		caps := getStringSlice(h, "detected_capabilities")
		tags := getStringSlice(h, "policy_tags")
		sb.WriteString(fmt.Sprintf("- **%s** (`%s`) — %s | Weight: `%d%%` | Tags: `%v`\n", disp, name, statusStr, weight, tags))
		if len(caps) > 0 {
			sb.WriteString(fmt.Sprintf("  - Capabilities: `%s`\n", strings.Join(caps, "`, `")))
		}
		if load, ok := h["load"].(map[string]any); ok && load != nil {
			working := getInt(load, "working_sessions", 0)
			cpu := getString(load, "cpu_load_pct")
			ram := getString(load, "ram_free_gb")
			overloaded, _ := getBool(load, "overloaded")
			warn := ""
			if overloaded {
				warn = " ⚠️ OVERLOADED"
			}
			sb.WriteString(fmt.Sprintf("  - Load: `%d` working session(s) | CPU: `%s%%` | Free RAM: `%s GB`%s\n", working, cpu, ram, warn))
		}
	}
	jsonBytes, _ := json.MarshalIndent(hosts, "", "  ")
	sb.WriteString("\n```json\n")
	sb.WriteString(string(jsonBytes))
	sb.WriteString("\n```\n")
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: sb.String()}},
	}, nil
}

func executeSpawnSession(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	targetHost := getString(args, "target_host")
	reqCaps := getStringSlice(args, "required_capabilities")
	nodePath := getString(args, "node_path")
	isSupervisor, _ := getBool(args, "is_supervisor")
	if targetHost == "" {
		if len(reqCaps) > 0 || (nodePath != "" && !isSupervisor) {
			targetHost = "auto"
		} else {
			targetHost = "local"
		}
	}
	directory := getString(args, "directory")
	agent := getString(args, "agent")
	if agent == "" {
		agent = "claude-code"
	}
	prompt := getString(args, "prompt")
	title := getString(args, "title")
	taskID := getString(args, "task_id")
	taskKind := getString(args, "task_kind")
	headless, _ := getBool(args, "headless")

	engineType := "tmux"
	if headless {
		engineType = "headless"
	}

	payload := map[string]any{
		"host":                  targetHost,
		"required_capabilities": reqCaps,
		"agent":                 agent,
		"cwd":                   directory,
		"node_path":             nodePath,
		"name":                  title,
		"prompt":                prompt,
		"is_supervisor":         isSupervisor,
		"task_id":               taskID,
		"task_kind":             taskKind,
		"engine_type":           engineType,
	}

	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, "/v1/sessions/spawn", payload, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to spawn session: %v", err)), nil
	}

	data, _ := json.MarshalIndent(res, "", "  ")
	headerMsg := "Session spawned successfully:"
	if warn, ok := res["load_warning"].(string); ok && warn != "" {
		headerMsg = fmt.Sprintf("Session spawned (⚠️ %s):", warn)
	}
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("%s\n%s", headerMsg, string(data))}},
	}, nil
}

func executeControlSession(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	sessionID := getString(args, "session_id")
	action := strings.ToLower(getString(args, "action"))
	targetAgent := getString(args, "target_agent")

	if sessionID == "" || action == "" {
		return formatError("session_id and action are required"), nil
	}

	if action == "handover" {
		payload := map[string]any{
			"id":         sessionID,
			"session_id": sessionID,
			"strategy":   "in_place",
		}
		if targetAgent != "" {
			payload["target_agent"] = targetAgent
		}
		var res map[string]any
		if err := client.doRequest(ctx, http.MethodPost, "/v1/sessions/handover", payload, &res); err != nil {
			return formatError(fmt.Sprintf("Failed to execute handover: %v", err)), nil
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return &CallToolResult{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Handover initiated:\n%s", string(data))}},
		}, nil
	}

	endpoint := fmt.Sprintf("/v1/sessions/control?id=%s&action=%s", url.QueryEscape(sessionID), url.QueryEscape(action))
	var res map[string]any
	if err := client.doRequest(ctx, http.MethodPost, endpoint, nil, &res); err != nil {
		return formatError(fmt.Sprintf("Failed to execute control action '%s': %v", action, err)), nil
	}

	data, _ := json.MarshalIndent(res, "", "  ")
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Action '%s' completed:\n%s", action, string(data))}},
	}, nil
}
