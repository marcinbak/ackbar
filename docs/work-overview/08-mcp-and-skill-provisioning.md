# 08 — Tasks MCP Server & Skill Provisioning Architecture

## 1. Overview

Ackbar's Work & Fleet Overview Dashboard manages the lifecycle of tasks across four pillars (`NEW`, `IN_PROGRESS`, `REVIEW`, `DONE`). To allow supervised AI coding agents (Claude Code, Google Antigravity, OpenAI Codex) to natively interact with this system, Ackbar provides:

1. **Ackbar Tasks Stdio MCP Server (`ackbar mcp`):** A lightweight, pure Go JSON-RPC 2.0 stdio MCP server.
2. **Embedded `ackbar-tasks` Skill (`SKILL.md`):** A versioned, embedded skill packaged via `go:embed`.
3. **Multi-Agent Provisioning Engine (`internal/daemon/provisioner.go`):** Automated, non-destructive configuration of connected agents.

---

## 2. The Tasks MCP Server (`ackbar mcp`)

### Protocol & Invocation
* **Transport:** Stdio (`os.Stdin` / `os.Stdout`) speaking JSON-RPC 2.0.
* **Command:** `ackbar mcp`
* **Daemon Link:** Connects to local `ackbard` (`http://127.0.0.1:7777` or `ACKBAR_DAEMON_URL`).

### Exposed Tools (Unified Session Control + Supervisor & Task Orchestration):

| Tool | Category | Parameters | Description |
| :--- | :--- | :--- | :--- |
| **`list_hosts`** | Supervisor Fleet | `include_metrics?` | Lists all registered hosts with online status, `detected_capabilities` (`ios`, `android`, `agent:*`, `mcp:*`), `policy_tags` (`24/7`, `drain`), `routing_weight` (`75`/`25`), and live `load` (`working_sessions`, `cpu_load_pct`, `ram_free_gb`, `overloaded`). |
| **`list_sessions`** | Supervisor Session | `host?`, `state?` | Lists active, blocked, or idle sessions across all federated hosts (`GET /v1/sessions?federated=true`). |
| **`get_session_output`** | Supervisor Session | `session_id!`, `recent_count?`, `offset?`, `char_offset?`, `max_chars?`, `include_all_roles?` | Fetches paginated assistant or all-role output with `truncated` metadata (`GET /v1/sessions/transcript`). |
| **`send_session_input`** | Supervisor Session | `session_id!`, `input!` | Answers a blocked question, approves a plan, or sends a follow-up prompt (`POST /v1/sessions/respond`). |
| **`spawn_session`** | Supervisor Session | `host?` (`"auto"`), `dir!`, `agent!`, `required_capabilities?`, `prompt?` | Spawns a new interactive Tmux or headless coding session on the target host (or `"auto"` via capability + 75/25 weighted deficit routing, returning `load_warning` if overloaded). |
| **`control_session`** | Supervisor Session | `session_id!`, `action!` | Executes session lifecycle actions (`resume`, `restart`, `kill`, `handover`, `done`, `later`). |
| **`bind_supervisor_session`** | Supervisor Dispatcher | `session_id!`, `project_node_path!`, `title!` | Auto-binds a zero-input `⚓ Supervisor` session to a project (`is_supervisor=1`) and sets its title on the first message. |
| **`create_task`** | Task Orchestrator | `title!`, `task_kind?`, `project_node_path?`, `repo_node_path?`, `repos[]`, `target_host?` (`"auto"`), `required_capabilities?`, `depends_on?`, `auto_start?` | Creates a task on the Work Board, resolving canonical group and project from `project_node_path`, `repo_node_path`, or `repos[0]`. |
| **`get_current_task`** | Task Worker | `worktree_path?`, `branch?` | Auto-detects and binds to the active task based on working directory, Git branch, or session. |
| **`list_tasks`** | Task Worker | `group?`, `project?`, `status?` | Queries tasks filtered by scope and lifecycle state (`NEW`, `IN_PROGRESS`, `REVIEW`, `DONE`). |
| **`update_task`** | Task Worker | `task_id!`, `title?`, `status?`, `substatus?`, `workflow_node?`, `notes?`, `pr_url?`, `project_node_path?`, `group_name?`, `project_name?`, `subproject_name?` | Updates task fields or moves task to another project node path, clearing subproject on moves. |
| **`report_blocker`** | Task Worker | `task_id!`, `question!` | Flags task as `blocked` and records the blocker question. Surfaces immediate red warning alert on board & Voice Companion. |
| **`clear_blocker`** | Task Worker | `task_id!`, `resolution_notes?` | Clears blocker and resumes task to `active`. |
| **`propose_task`** | Task Worker | `title!`, `group?`, `project?`, `notes?`, `rationale?` | Logs discovered bugs or tech debt into the `NEW` inbox column with title deduplication. |
| **`attach_deliverable`** | Task Worker | `task_id!`, `title?`, `kind?`/`type?`, `file_path?`, `url?`, `content_md?`, `provider?` | Attaches artifacts (`url`, `file_path`, `content_md`); supports `type` alias, auto-title, and confined local file auto-reading. |
| **`get_deliverable`** | Task Worker / Supervisor | `id?`, `task_id?`, `kind?`/`type?`, `max_chars?`, `char_offset?` | Fetches deliverable metadata and rune-safe cursor-paginated `content_md` (default 20,000, max 50,000 chars). |
| **`list_retros`** | Supervisor / Reporting / Worker | `since?`, `until?`, `node_path?`, `repo?`, `host?`, `agent?`, `task_id?`, `limit?` | Queries compact retrospective summaries and structured `RetroMetrics` without raw `content_md` payload. |
| **`merge_pull_request`** | Task Worker | `task_id!`, `method?` | Executes 1-click squash & merge via daemon (`N14`) and unlocks any dependent sequential repo workstreams. |
| **`get_standup_report`** | Supervisor / Reporting | `group?`, `days?` | Generates project-grouped Markdown and conversational audio briefing text. |

---

## 3. The Embedded `ackbar-tasks` Skill

The skill is embedded into the Ackbar binary (`internal/skills/ackbar-tasks/SKILL.md`) using `go:embed` and stamped with the exact Ackbar release version:

### Guiding Principles for Connected Agents:
1. **Zero Silent Stalls:** Never wait passively for user decisions or credentials. Call `report_blocker` immediately so the human supervisor is alerted.
2. **Ambient Discovery:** Call `get_current_task` at session startup to bind context to the task.
3. **Deliverables Discipline:** Whenever generating architectural plans, generative HTML UI prototypes, or retrospectives, call `attach_deliverable`.
4. **Discovered Work Capture:** When noticing an unprompted bug or refactor opportunity, call `propose_task` instead of derailing current work.

---

## 4. Multi-Agent Provisioning & Configuration

Ackbar automatically detects and configures the following agents:

| Agent | Config File | Skill Destination |
| :--- | :--- | :--- |
| **Claude Code** | `~/.claude.json` | `~/.claude/skills/ackbar-tasks/SKILL.md` |
| **Google Antigravity** | `~/.gemini/config/mcp_config.json` *(and legacy `~/.gemini/antigravity/mcp_config.json`)* | `~/.gemini/antigravity-cli/skills/ackbar-tasks/SKILL.md` |
| **OpenAI Codex** | `~/.codex/config.json` | `~/.codex/skills/ackbar-tasks/SKILL.md` |

### Non-Destructive JSON Merging
The provisioner parses existing configuration files without stripping comments or unrelated settings, injecting or updating the `mcpServers.ackbar` block:

```json
{
  "mcpServers": {
    "ackbar": {
      "command": "ackbar",
      "args": ["mcp"]
    }
  }
}
```

---

## 5. Usage & Management

### CLI Command
* Check status across all agents:
  ```bash
  ackbar agent status
  ```
* Configure all detected agents:
  ```bash
  ackbar agent setup
  ```
* Configure a specific agent:
  ```bash
  ackbar agent setup --agent=claude-code
  ```

### Web UI
Click the **`[⚙️ Agent Setup]`** button in the Work Board toolbar to open the Agent Tool & Skill Setup modal. View detected agents, configuration status, version drift indicators, and click **`[⚡ Auto-Configure Agents]`** for instant 1-click provisioning.
