# 01 — Data Model & Lifecycle Architecture

## 1. Decoupling Sessions from Tasks

The fundamental model decouples **execution containers** from **business deliverables**:

* **Session (Execution Unit):** Ephemeral agent process, tmux pane, PID, host, and conversation context window. Sessions can be spawned, reset (`/clear`), rotated, restarted, or killed.
* **Task (Logical Unit of Work):** Durable entity representing an engineering objective. Has requirements, deliverables, git branches, pull requests, tracker references, and an append-only timeline.

```
┌────────────────────────────────────────────────────────────────────────┐
│                        LOGICAL WORK LAYER (Task)                       │
│  ID: task_01j8x92k                                                     │
│  Title: "Resilient WebSocket reconnection with exponential backoff"   │
│  Group: "Modemobile" | Project: "NGL" | Subproject: "ngl-ios"          │
│  Status: REVIEW | Substatus: approved                                  │
│  Trackers: [Jira: NGL-409, Linear: ENG-12]                             │
└───────────────────┬────────────────────────────────┬───────────────────┘
                    │                                │
                    ▼                                ▼
       ┌────────────────────────┐       ┌────────────────────────┐
       │   SESSION 1 (@mac)     │       │   SESSION 2 (@legion)  │
       │ Claude Code (iOS UI)   │       │ Antigravity (Backend)  │
       │ State: StateIdle       │       │ State: StateWorking    │
       └────────────────────────┘       └────────────────────────┘
```

---

## 2. Sovereign Identifiers & External Trackers

To remain tracker-agnostic, internal task IDs are never hardcoded to external Jira or Linear issue keys:
1. **Sovereign Primary Key:** A ULID or clean slug (e.g. `task_01j8x92k` or `task-20260925-ws-reconnect`).
2. **External Tracker Links (0 to N):** Stored in a decoupled join table or JSON array.
3. **Dynamic Discovery:** When an agent starts hacking, a sovereign ID is minted. If a Jira ticket (`NGL-409`) or Linear issue (`ENG-12`) is mentioned later in prompts, commit messages, or branches, the tracker link is appended without re-keying the task.

---

## 3. Resolving the M:N Relationship

In real-world agentic workflows:

### A. Multiple Tasks Handled in a Single Session (Serial)
* A developer keeps an interactive Claude Code session open for days across multiple tasks.
* **Resolution:** 
  * The session maintains a pointer to its `current_task_id`.
  * When the agent completes one task and starts another (detected via branch switch, MCP call, or prompt), the session logs a task transition event.
  * Timeline records: *Session A worked on Task 1 from 09:00 to 11:30, then switched to Task 2 at 11:30.*

### B. Multiple Sessions Working on the Same Task (Parallel / Multi-Host)
* Example: Session 1 on `mac` modifies the client app, Session 2 on `legion` updates backend APIs, both targeting `NGL-409`.
* **Resolution:**
  * The **Task** is the primary grouping entity on the overview board.
  * Both worker sessions register under the task's worker pool (`task_workers`).
  * The task card displays all active workers across hosts.

---

## 4. The 4-Pillar Lifecycle: "Who Has the Ball?"

Instead of a flat enum, the lifecycle uses 4 top-level macro columns:

```
┌─────────────┐       ┌─────────────┐       ┌─────────────┐       ┌─────────────┐
│     NEW     │  ──►  │ IN_PROGRESS │  ──►  │   REVIEW    │  ──►  │    DONE     │
│  (Backlog)  │       │ (Agent Work)│       │(Human Eval) │       │  (Closed)   │
└─────────────┘       └─────────────┘       └─────────────┘       └─────────────┘
```

| Primary Status | Meaning | Substatuses |
| :--- | :--- | :--- |
| **`NEW`** | Not started or waiting for prioritization. | `backlog` (queued), `ready` (plan ready), `discovered` (unprompted bug/opportunity found by agent). |
| **`IN_PROGRESS`** | The **Agent** has the ball. Actively executing or blocked on input. | `active` (coding/running tests), `blocked` (waiting on human decision/credentials). |
| **`REVIEW`** | The **Human** has the ball. Agent finished; deliverables await evaluation. | `task_review` (inspecting UI/video/plan), `in_review` (PR opened), `approved` (ready to merge), `changes_requested`. |
| **`DONE`** | Terminal state. Closed, merged, or discarded. | `completed` (PR merged, verified), `rejected` (closed without merge), `abandoned` (spike discarded). |

---

## 5. Multi-Host 24/7 Control Plane & Logical Project Matrix

### 5.1 Why Projects Are Host-Agnostic (`legion` + `macmini` 24/7 Topology)
Projects in Ackbar are **logical product namespaces** (`tree_nodes`), never pinned to a single physical host. A single logical project can be:
* **A Monorepo** checked out on both 24/7 hosts (e.g. `Personal/Ackbar` with Go backend, Web UI, and Flutter iOS/Android).
* **A Multi-Repo Product** spanning specialized repositories (e.g. `Modemobile/NGL` containing `ngl-ios`, `ngl-android`, `ngl-web`, and `ngl-image-generation`; or `Modemobile/MEA` containing `ad-mediation-api`, `current-android`, and `news-api`).

To solve the missing session tree on the iPhone app when `macbook` is asleep, the two 24/7 hosts (**`legion`** on Linux/Android and **`macmini`** on macOS/Xcode/iOS Simulator) form a **replicated Control Plane**:
* `tree_nodes`, session `node_path`/`custom_title`/`is_later`, `tasks`, `task_workstreams`, and `task_deliverables` sync continuously between `legion` and `macmini`.
* Connecting to **either** `legion` or `macmini` from the iPhone (`mobile/`) or MacBook (`web/` / TUI) via `GET /v1/nodes?federated=true`, `GET /v1/sessions?federated=true`, and `GET /v1/tasks?federated=true` returns the identical project tree and task board.

| Logical Tree Node (`tree_nodes.path`) | Repo Type | `@macmini` Path (`ios`, `macos`) | `@legion` Path (`android`, `go`, `backend`, `gpu`) |
| :--- | :--- | :--- | :--- |
| **`Personal/Ackbar`** | Monorepo | `~/Personal/ackbar` | `/home/dev4u/Personal/ackbar` |
| **`Modemobile/NGL/ngl-ios`** | Sub-Repo | `~/Work/NGL/ngl-ios` | *(N/A — iOS/Xcode only)* |
| **`Modemobile/NGL/ngl-android`** | Sub-Repo | `~/Work/NGL/ngl-android` | `/home/dev4u/Work/NGL/ngl-android` |
| **`Modemobile/NGL/ngl-web`** | Sub-Repo | `~/Work/NGL/ngl-web` | `/home/dev4u/Work/NGL/ngl-web` |
| **`Modemobile/MEA/ad-mediation-api`** | Sub-Repo | `~/Work/MEA/ad-mediation-api` | `/home/dev4u/Work/MEA/ad-mediation-api` |

---

## 6. Stage-Gate Execution Engine, Sequential Workstreams & Completion Verification

### 6.1 Ephemeral Stage Worker Lifecycle
Instead of keeping a single context-bloated session alive across planning, coding, and review, `ackbard` spawns fresh, isolated worker sessions per **Workstream Stage**:

1. **`PLAN` (`IN_PROGRESS: PLAN`):** Spawns a planner session in `.worktrees/<task-slug>` that writes `.ackbar/plan.md` and calls `complete_stage(stage="PLAN")` $\rightarrow$ `ackbard` verifies the artifact, **terminates the planner pane**, and moves the workstream to **`REVIEW: PLAN_REVIEW`** (`0` idle RAM/tokens while waiting for the human).
2. **`IMPLEMENT` (`IN_PROGRESS: IMPLEMENT`):** Once the human approves (`review_task_stage(action="approve")`), `ackbard` spawns a fresh implementer session seeded only with `.ackbar/plan.md`.
3. **`REVIEW` (`IN_PROGRESS: REVIEW`):** Upon `complete_stage(stage="IMPLEMENT")`, `ackbard` terminates the coder pane and spawns the 3-way reviewer + `.ackbar/review/index.html` generator $\rightarrow$ moves to **`REVIEW: FINAL_REVIEW`**.

### 6.2 Sequential Cross-Platform Workstreams (`depends_on` + `.ackbar/learnings.md`)
When a cross-platform feature should be validated on one platform first (e.g. finish and verify **iOS** on `macmini` before starting **Android** on `legion`):
* Both `ngl-ios` and `ngl-android` belong to a **single Task** via `task_workstreams`, with `ngl-android` declaring `depends_on = "Modemobile/NGL/ngl-ios"`.
* While `ngl-ios` runs (`PLAN` $\rightarrow$ `IMPLEMENT` $\rightarrow$ `REVIEW` on `@macmini`), `ngl-android` stays `QUEUED` (`0` sessions, `0` tokens).
* When `ngl-ios` completes, the agent writes **`.ackbar/learnings.md`** ( capturing API payload quirks, edge cases, and architecture decisions discovered on iOS).
* `ackbard` automatically unlocks `ngl-android` on `@legion` and injects **both** the task spec and `ngl-ios`'s `.ackbar/learnings.md` into the Android planner session.

### 6.3 Detecting True Stage Completion vs. Premature Idle
Agents frequently print an outline or status update and drop into `StateIdle` (`Stop` hook) without actually finishing their stage. `ackbard` distinguishes true completion from premature idling via a **3-part contract**:

1. **Explicit `complete_stage` MCP Signal:** `StateIdle` alone **never** advances a task stage. Workers must call `complete_stage(task_id, stage, summary, learnings_md)` or `report_blocker(task_id, question)`.
2. **Deterministic Zero-Token Gate Verification (Inside `complete_stage`):**
   * **`PLAN` Gate:** Rejects completion if `.ackbar/plan.md` is missing or `< 200` bytes.
   * **`IMPLEMENT` Gate:** Rejects completion if `git status --porcelain` has uncommitted files or `git rev-list origin/main..HEAD` has `0` commits.
   * **`REVIEW` Gate:** Rejects completion if `.ackbar/review/index.html` or the PR URL is missing.
3. **Premature Idle Watchdog (`ackbard`):**
   * If a task worker enters `StateIdle` for `> 15s` without calling `complete_stage` or `report_blocker`, `ackbard` auto-nudges the session (up to 2 retries):
     > *"[Ackbar Orchestrator]: You went idle without calling `complete_stage` or `report_blocker`. Finish the stage and call `complete_stage`, or call `report_blocker` if you need user input."*
   * If the session goes idle a 3rd time without calling either tool, `ackbard` transitions the task to `IN_PROGRESS: blocked` (`"Worker stalled without completing stage"`) and surfaces it in Attention / **⚓ Ackbar Supervisor**.

---

## 7. Coexistence with Manual Sessions & Automatic Cleanup

* **100% Backwards-Compatible Manual Workflow:** Spawning or attaching to manual sessions in **`[💻 Workspace]`** works unchanged.
* **Structured `📋 Task` Grouping in Session Tree:** Task worker sessions carry `task_id` + `task_stage` metadata and automatically group under a collapsible **`📋 Task`** subgroup inside their project folder (`resolveSessionNodePath` in `web/js/state.js` / TUI / Mobile), separate from active manual sessions and `✅ Done` / `⏳ Later`:
  ```text
  ▼ 📁 Modemobile / NGL
    ├── 💻 Manual Sessions (2)
    ├── 📋 Task: NGL-409 Rokt Attributes (1 active)
    │     └── ⚙️ [IMPL • iOS] NGL-409 @macmini
    ├── ⏳ Later (1)
    └── ✅ Done (4)
  ```
* **Two-Phase Automatic Cleanup (Zero Worktree/Session Clutter):**
  1. **Per-Stage Pane Exit:** As soon as `complete_stage` succeeds, `ackbard` terminates that stage's tmux pane (`tmux kill-window`) while preserving the transcript in SQLite.
  2. **Task `DONE` Teardown:** When the PR is merged or the task moves to `DONE`, `ackbard` automatically runs `git worktree remove .worktrees/<task-slug>` + `git branch -d <branch>` on each participating host and marks all associated worker sessions `archived=1`.

---

## 8. SQLite Schema (`internal/daemon/db.go`)

```sql
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,                       -- Sovereign ULID, e.g. 'task_01j8x92k'
    title TEXT NOT NULL,
    group_name TEXT NOT NULL,                  -- Top-level group, e.g. 'Modemobile', 'Personal'
    project_name TEXT NOT NULL,                -- Second-level project, e.g. 'NGL', 'MEA'
    subproject_name TEXT,                      -- Optional third-level, e.g. 'ngl-ios'
    status TEXT NOT NULL DEFAULT 'NEW',        -- 'NEW', 'IN_PROGRESS', 'REVIEW', 'DONE'
    substatus TEXT NOT NULL DEFAULT 'ready',   -- 'PLAN', 'PLAN_REVIEW', 'IMPLEMENT', 'REVIEW', 'FINAL_REVIEW', 'blocked', 'completed'
    notes TEXT,                                -- Latest summary / activity note
    blocker_question TEXT,                     -- Question string if substatus == 'blocked'
    branch TEXT,                               -- Active git branch name
    worktree_path TEXT,                        -- Associated worktree directory
    pr_url TEXT,                               -- GitHub / GitLab PR URL
    pr_number INTEGER,                         -- PR number
    pr_state TEXT,                             -- 'OPEN', 'MERGED', 'CLOSED'
    ci_status TEXT,                            -- 'PENDING', 'PASSING', 'FAILING'
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS task_workstreams (
    task_id TEXT NOT NULL,
    repo_node_path TEXT NOT NULL,              -- e.g. 'Modemobile/NGL/ngl-ios', 'Modemobile/NGL/ngl-android'
    target_host TEXT NOT NULL,                 -- 'macmini' or 'legion'
    depends_on TEXT,                           -- Optional upstream repo_node_path for sequential handoff
    stage TEXT NOT NULL DEFAULT 'QUEUED',      -- 'QUEUED', 'PLAN', 'PLAN_REVIEW', 'IMPLEMENT', 'REVIEW', 'FINAL_REVIEW', 'DONE'
    status TEXT NOT NULL DEFAULT 'NEW',        -- 'NEW', 'IN_PROGRESS', 'REVIEW', 'DONE'
    branch TEXT,                               -- e.g. 'feat/task-104-rokt'
    worktree_path TEXT,                        -- e.g. '.worktrees/task-104-rokt'
    pr_url TEXT,
    learnings_md TEXT,                         -- Cross-platform learnings passed to dependent workstreams
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (task_id, repo_node_path),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_external_refs (
    task_id TEXT NOT NULL,
    tracker TEXT NOT NULL,                     -- 'jira', 'linear', 'github'
    ref_key TEXT NOT NULL,                     -- 'NGL-409', 'ENG-12', '#82'
    url TEXT,
    PRIMARY KEY (task_id, tracker, ref_key),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_workers (
    task_id TEXT NOT NULL,
    session_id TEXT NOT NULL,                  -- Ackbar session ID '{agent}:{host}:{nativeID}'
    repo_node_path TEXT,                       -- Associated repo workstream
    stage TEXT,                                -- 'PLAN', 'IMPLEMENT', 'REVIEW'
    agent TEXT NOT NULL,                       -- 'claude-code', 'antigravity', 'codex'
    host TEXT NOT NULL,                        -- 'macmini', 'legion'
    is_active INTEGER DEFAULT 1,
    nudge_count INTEGER DEFAULT 0,             -- Premature Idle Watchdog retry counter
    assigned_at TIMESTAMP NOT NULL,
    PRIMARY KEY (task_id, session_id),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_deliverables (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    kind TEXT NOT NULL,                        -- 'html_ui', 'video', 'image', 'plan', 'learnings', 'pr', 'retro'
    title TEXT NOT NULL,
    host TEXT NOT NULL,                        -- Host where artifact resides
    file_path TEXT,                            -- Absolute path on target host
    url TEXT,                                  -- Web URL if applicable
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

-- Performance Indices
CREATE INDEX IF NOT EXISTS idx_tasks_group_project ON tasks(group_name, project_name);
CREATE INDEX IF NOT EXISTS idx_tasks_status_updated ON tasks(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_task_workers_active ON task_workers(is_active);
CREATE INDEX IF NOT EXISTS idx_task_workstreams_task ON task_workstreams(task_id);

-- Future-proofing: Archive table to prevent UI queries from scanning years of closed tasks
CREATE TABLE IF NOT EXISTS archived_tasks (
    id TEXT PRIMARY KEY,
    original_task_json TEXT NOT NULL,          -- JSON payload of the entire task tree
    archived_at TIMESTAMP NOT NULL
);
```

---

## 7. Edge Cases & Data Hygiene

1. **Zombie Sessions:** If a remote host crashes or disconnects without emitting an exit event, its worker record might remain `is_active=1` perpetually. To mitigate this, `ackbard` will implement a daily sweep job that pings hosts with currently `is_active=1` sessions. If a session is unresponsive, it is marked as `is_active=0` and will not be pinged again.
2. **Artifact Retention Policies:** Rich deliverables (`task_deliverables`) like `.mp4` simulator videos and UI snapshots can cause disk bloat on remote hosts. The system should run a cron job to automatically delete underlying binary files for tasks that have been in the `DONE` state for more than 30 days.
3. **Archiving Stale Tasks:** While the default is to hide tasks older than 7 days, a long-term sweep will move ancient tasks (e.g., > 90 days) into the `archived_tasks` table and purge them from the active `tasks` and join tables to keep UI queries fast.
