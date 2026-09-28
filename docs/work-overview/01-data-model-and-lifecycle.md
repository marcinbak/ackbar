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

## 5. In-Flight Creation vs. Backlog Queuing

* **In-Flight Auto-Creation (Primary Day-to-Day Path):**
  When a developer spawns a session in Ackbar with a prompt or git branch, the task entity is **born directly in `IN_PROGRESS: active`**. It completely bypasses `NEW`.
* **Backlog / Spec-First (Optional Path):**
  Tasks in `NEW` exist when:
  1. Pulling unstarted tickets assigned in Jira/Linear.
  2. An agent writes an `implementation_plan.md` that outlines future phases.
  3. An agent discovers an unprompted bug (`discovered`).

---

## 6. Draft SQLite Schema

```sql
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,                       -- Sovereign ULID, e.g. 'task_01j8x92k'
    title TEXT NOT NULL,
    group_name TEXT NOT NULL,                  -- Top-level group, e.g. 'Modemobile', 'Personal'
    project_name TEXT NOT NULL,                -- Second-level project, e.g. 'NGL', 'MEA'
    subproject_name TEXT,                      -- Optional third-level, e.g. 'ngl-ios'
    status TEXT NOT NULL DEFAULT 'NEW',        -- 'NEW', 'IN_PROGRESS', 'REVIEW', 'DONE'
    substatus TEXT NOT NULL DEFAULT 'ready',   -- 'active', 'blocked', 'approved', 'completed', etc.
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
    agent TEXT NOT NULL,                       -- 'claude-code', 'antigravity', 'codex'
    host TEXT NOT NULL,                        -- 'mac', 'legion'
    is_active INTEGER DEFAULT 1,
    assigned_at TIMESTAMP NOT NULL,
    PRIMARY KEY (task_id, session_id),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_deliverables (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    kind TEXT NOT NULL,                        -- 'html_ui', 'video', 'image', 'plan', 'pr', 'retro'
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
