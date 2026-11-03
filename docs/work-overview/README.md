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

## 2. 4-Pillar Task Lifecycle (`dev-workflow` + Product/Design Tasks)

Tasks progress through 4 macro pillars (`NEW` $\rightarrow$ `IN_PROGRESS` $\rightarrow$ `REVIEW` $\rightarrow$ `DONE`) supporting both **Product/Design Tasks (`kind="design"`)** and **Engineering Repo Tasks (`kind="code"`, driven by `dev-workflow` N0–N15 inside Tmux)**:

| Pillar | State | Substatuses | Description & Workflow Mapping |
| :--- | :--- | :--- | :--- |
| **1. NEW** | `NEW` | `backlog`, `ready`, `discovered` | Feature, design, or bug queued via **`⚓ Supervisor`** or Work Board. |
| **2. IN_PROGRESS** | `IN_PROGRESS` | `active`, `question` (`N1`), `stalled` (`N3–N10`) | Dedicated **`📋 Task`** session executing Product/Design work or repo `dev-workflow` in Tmux (`@macmini` / `@legion`). Stopping at `N1` flags `question`; stopping in `N3–N10` without `report_blocker` flags `stalled`. |
| **3. REVIEW** | `REVIEW` | `design_review`, `plan_review` (`N2`), `pr_review` (`N11`), `approved` | **Human Checkpoint:** Hosted deliverable (Claude Published Artifact or Google Doc/Slides/Drive via `gdrive`/`gdocs`/`gslides`) or GitHub PR (`N11`) awaits approval. |
| **4. DONE** | `DONE` | `completed` (`N14–N15`), `rejected`, `abandoned` | Design approved or PR merged (`N14`), `.worktrees/<branch>` removed, retro published (`N15`), Tmux window auto-closed, and session moved to `✅ Done`. |

---

## 3. Database Schema (`internal/daemon/db.go`)

Task and tree persistence is managed in `ackbard.db` using CGO-free pure Go SQLite (`modernc.org/sqlite`) and replicated via **Uniform 3-Way LWW Peer Sync** (`macbook` $\leftrightarrow$ `macmini` $\leftrightarrow$ `legion`):

* **`tree_nodes` & `node_host_paths`**: Host-agnostic logical project & repo hierarchy (`tree_nodes`) paired with dynamic per-host checkout directories (`node_host_paths: node_path, host, project_dir, git_remote, capabilities, updated_at`).
* **`tasks`**: Core task records (`id`, `title`, `task_kind`, `notes`, `group_name`, `project_name`, `status`, `substatus`, `branch`, `worktree`, `pr_url`, `created_at`, `updated_at`, `closed_at`).
* **`task_workstreams`**: Per-repository workstream state (`task_id`, `repo_node_path`, `target_host`, `depends_on`, `workflow_node`, `status`, `branch`, `worktree_path`, `pr_url`) supporting both parallel and `N14`-merge-triggered sequential execution (e.g. `ngl-ios` `@macmini` $\rightarrow$ `ngl-android` `@legion`).
* **`task_workers`**: Associates dedicated Tmux worker sessions to a task/workstream (`task_id`, `repo_node_path`, `workflow_node`, `session_id`, `host`, `assigned_at`).
* **`task_external_refs`**: Links to external ticket systems (`task_id`, `tracker`, `ref_key`, `url`).
* **`task_deliverables`**: Cloud-hosted deliverables (`task_id`, `kind`, `provider`, `title`, `url`, `content_md`) linking **Claude Published Artifacts** and **Google Workspace / Drive** (`gdrive`, `gdocs`, `gslides`, `gsheets`) plus synced `content_md` for cross-host agent handoff.
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
