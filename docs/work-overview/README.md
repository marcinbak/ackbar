# Work & Fleet Overview Dashboard

> **Real-time multi-host AI agent task orchestration, Kanban lifecycle tracking, and artifact review for Project Ackbar.**

---

## 1. Overview

Ackbar's **Work & Fleet Overview Dashboard** provides high-level observability and lifecycle management across distributed AI programming agents running on local and remote machines. It unifies high-level task tracking (e.g. Jira/Linear/GitHub tracker tickets) with live agent session processes (Claude Code, Google Antigravity, OpenAI Codex).

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       WORK & FLEET OVERVIEW DASHBOARD                       │
│                                                                             │
│  [💻 Workspace]  [📋 Work Board]       Filters: [All | Modemobile | Personal]│
│                                                                             │
│  ┌───────────────┐ ┌───────────────┐ ┌───────────────┐ ┌───────────────┐   │
│  │   1. NEW      │ │ 2. IN_PROGRESS│ │   3. REVIEW   │ │    4. DONE    │   │
│  │ (Backlog/Plan)│ │(Active Agents)│ │(PR / Review)  │ │ (Merged/Done) │   │
│  │               │ │               │ │               │ │               │   │
│  │  [TASK-101]   │ │  [TASK-102]   │ │  [TASK-99]    │ │  [TASK-98]    │   │
│  │  Setup DB     │ │  Build API    │ │  PR #102      │ │  Shipped v1   │   │
│  │  ⚙️ Claude-1   │ │  🟢 Dev-1     │ │  🟣 Antigrav  │ │  ✅ Closed    │   │
│  └───────────────┘ └───────────────┘ └───────────────┘ └───────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 4-Pillar Task Lifecycle

| Pillar | State | Description | Typical Transitions |
| :--- | :--- | :--- | :--- |
| **1. NEW** | `NEW` | Tasks queued in backlog or undergoing initial architecture planning. | Moves to `IN_PROGRESS` when an agent begins execution. |
| **2. IN_PROGRESS** | `IN_PROGRESS` | Actively supervised by one or more agent worker sessions. | Emits substatuses (`active`, `blocked`, `running`). Moves to `REVIEW` upon PR creation. |
| **3. REVIEW** | `REVIEW` | Work has completed automated testing; PR is awaiting review or human evaluation. | Substatuses include `task_review`, `in_review`, `approved`. Moves to `DONE` once merged. |
| **4. DONE** | `DONE` | Pull request merged, worktree cleaned, or task resolved. | Retained for 30 days before auto-archival. |

---

## 3. Database Schema (`internal/daemon/db.go`)

Task persistence is managed in `ackbard.db` using CGO-free pure Go SQLite (`modernc.org/sqlite`):

* **`tasks`**: Core task records (`id`, `title`, `notes`, `group_name`, `project_name`, `status`, `substatus`, `branch`, `worktree`, `pr_url`, `created_at`, `updated_at`, `closed_at`).
* **`task_workers`**: Associates multiple agent sessions to a task (`task_id`, `session_id`, `role`, `host_name`, `assigned_at`).
* **`task_external_refs`**: Links to external ticket systems (`task_id`, `tracker`, `ref_key`, `url`).
* **`task_deliverables`**: Artifact deliverables (`task_id`, `kind`, `title`, `url_or_path`, `sha`).
* **`task_events`**: Immutable audit log of lifecycle mutations (`task_id`, `event_type`, `payload`, `created_at`).

---

## 4. REST API Endpoints

* **`GET /v1/tasks`**: Returns all tasks with joined workers, external references, and deliverables. Supports optional query filters: `?group=`, `?project=`, `?status=`.
* **`POST /v1/tasks`**: Creates or updates a task.
* **`POST /v1/tasks/event`**: Ingests structured task lifecycle events (`task_id`, `event_type`, `payload`).
* **`POST /v1/tasks/propose`**: Records discovered work proposals with title deduplication into `NEW`.
* **`POST /v1/tasks/deliverable`**: Attaches design mockups, change briefs, retrospectives, or external artifacts.
* **`POST /v1/tasks/sync-workflow`**: Scans `~/.claude/dev-workflow-runs/` to sync node states and companions.
* **`POST /v1/tasks/merge-pr`**: Executes 1-click `gh pr merge --squash` directly from the dashboard.
* **`GET /v1/standup`**: Generates instant, deterministic daily standups or retrospectives in Markdown or JSON.
* **`POST /v1/briefings/synthesize`**: Produces speech-optimized conversational briefings (<45s) for audio playback.
* **`DELETE /v1/tasks?id=<id>`**: Deletes or archives a task.

---

## 5. Web UI Features (`web/js/tasks.js`)

1. **Header Mode Switcher:**
   * Toggle between **`[💻 Workspace]`** (multi-terminal tabs, chat transcript, session tree) and **`[📋 Work Board]`** (Kanban board).
   * Restoring Workspace view triggers synthetic window resize events to ensure `xterm.js` terminals re-fit without distortion.
2. **Scoping & Filtering:**
   * Organization/Group pills (`All`, `Modemobile`, `Personal`).
   * Dynamic Project dropdown populated from active task records.
   * Instant search input filtering across task titles, notes, Git branches, and issue tracker keys.
3. **Zero Context-Switch Navigation:**
   * Clicking any worker badge on a task card instantly switches to Workspace mode and focuses the agent's live terminal/chat tab via `openSessionInTab(session)` / `activateTab(sessionId)`.
4. **Contextual Action Controls:**
   * **1-Click Merge PR:** Directly executes `gh pr merge --squash` on GitHub with loading cues and instant state transition to `DONE`.
   * **Unblock Worker:** Immediately switches to and focuses a blocked agent waiting for input.
   * **Edit Task:** Modal to adjust title, status, substatus, notes, branch, or PR URL.
   * **Create Task:** Quick task creator modal with group and project defaults.
5. **Daily Standup & Voice Companion Briefing:**
   * `[📋 Daily Standup]` toolbar button opens the Standup Modal.
   * Filterable by Scope (`All`, `Modemobile`, `Personal`) and Lookback Window (24h, 48h, 7d).
   * 1-click **"📋 Copy to Clipboard"** for fast Slack/Teams standup updates.
   * **"🎙️ Audio Briefing"** voice playback using the Web Speech API (`SpeechSynthesis`).
6. **Agent Tool & Skill Setup (`ackbar mcp`):**
   * `[⚙️ Agent Setup]` toolbar button opens the Multi-Agent Setup modal.
   * Native pure Go stdio MCP server (`ackbar mcp`) providing task lifecycle, blocker callouts, deliverables, and discovered work triage.
   * 1-click auto-configuration for Claude Code, Google Antigravity, and OpenAI Codex with version drift detection.
7. **Real-time SSE Sync:**
   * Daemon broadcasts trigger debounced task board reloads when the Work Board view is active.
