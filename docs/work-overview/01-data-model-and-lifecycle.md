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

## 5. Multi-Host 3-Way LWW Peer Sync & Dynamic Repo Mapping (`node_host_paths`)

### 5.1 Uniform 3-Way Peer Sync (`macbook` $\leftrightarrow$ `macmini` $\leftrightarrow$ `legion`)
Projects in Ackbar are **logical product namespaces** (`tree_nodes`), never pinned to a single physical host. A single logical project can be:
* **A Monorepo** checked out on multiple hosts (e.g. `Personal/Ackbar` with Go backend, Web UI, and Flutter iOS/Android).
* **A Multi-Repo Product** spanning specialized repositories (e.g. `Modemobile/NGL` containing `ngl-ios`, `ngl-android`, `ngl-web`, and `ngl-image-generation`; or `Modemobile/MEA` containing `ad-mediation-api`, `current-android`, and `news-api`).

To ensure the iPhone app and MacBook always display the exact same folder tree and task board:
* **All hosts (`macbook`, `macmini`, `legion`) participate in Peer-to-Peer Last-Write-Wins (LWW) Sync (`updated_at`):**
  * While `macbook` is awake, any manual session, folder edit (`tree_nodes`), or task mutation on `macbook` pushes immediately to online peers (`legion` and `macmini`).
  * When `macbook`'s lid is closed, `legion` and `macmini` remain online 24/7 and serve the full synced tree to the iPhone app, displaying `macbook` sessions with an `[offline]` badge.
  * When `macbook` wakes up, its `ackbard` runs a delta pull (`GET /v1/sync/state?since=<last_sync_ts>`) from `legion`/`macmini` to catch up in `<100ms`.

### 5.2 Dynamic Per-Host Repository Paths (`node_host_paths`)
Rather than hardcoding host columns on `tree_nodes`, `ackbard` stores a normalized **`node_host_paths`** table (`node_path`, `host`, `project_dir`, `git_remote`, `capabilities`, `updated_at`) so any host can register its local checkout directory:

| Logical Tree Node (`tree_nodes.path`) | `@macmini` (`ios`, `macos`) | `@legion` (`android`, `go`, `backend`, `gpu`) | `@macbook` (`ios`, `android`, `go`) |
| :--- | :--- | :--- | :--- |
| **`Personal/Ackbar`** *(Monorepo)* | `~/Personal/ackbar` | `/home/dev4u/Personal/ackbar` | `~/Work/Ackbar` |
| **`Modemobile/NGL/ngl-ios`** | `~/Work/NGL/ngl-ios` | *(N/A — iOS/Xcode only)* | `~/Work/NGL/ngl-ios` |
| **`Modemobile/NGL/ngl-android`** | `~/Work/NGL/ngl-android` | `/home/dev4u/Work/NGL/ngl-android` | `~/Work/NGL/ngl-android` |
| **`Modemobile/NGL/ngl-web`** | `~/Work/NGL/ngl-web` | `/home/dev4u/Work/NGL/ngl-web` | `~/Work/NGL/ngl-web` |
| **`Modemobile/MEA/ad-mediation-api`** | `~/Work/MEA/ad-mediation-api` | `/home/dev4u/Work/MEA/ad-mediation-api` | `~/Work/MEA/ad-mediation-api` |

### 5.3 Host Capabilities, Weighted Load Balancing (`75/25`) & Utilization Telemetry
To allow the `⚓ Supervisor` and `ackbard` (`target_host: "auto"`) to route tasks intelligently without hardcoded host names—while preserving scarce iOS capacity on `macmini`—each host reports **hybrid capabilities** and **live load telemetry** over the existing `GET /v1/version` health heartbeat (every 5–10s in `ensureHostTunnels`, plus an instant pre-spawn refresh):

1. **Hybrid Host Capabilities (`detected_capabilities` + `policy_tags`):**
   * **`detected_capabilities` (Auto-Probed by `ackbard`):**
     * **Platform & UI Test Runtimes:** `macos`, `linux`, `ios` (`xcrun simctl`), `android` (`adb` / `emulator`), `chrome` (Playwright/Chrome), `docker`, `gpu`.
     * **Installed & Authenticated AI Agents:** `agent:claude-code`, `agent:antigravity`, `agent:codex`.
     * **Configured MCP Plugins:** `mcp:gdrive`, `mcp:gdocs`, `mcp:gslides`, `mcp:gsheets`, `mcp:linear`, etc.
   * **`policy_tags` & `routing_weight` (Manual Host Policy, LWW-Synced):**
     * `24/7` (`always_on`) vs. `ephemeral` (laptops like `macbook`), plus optional `drain` / `priority`.
     * **Configurable `routing_weight` (`0..100`):** Defaults to **`legion = 75`**, **`macmini = 25`**, and **`macbook = 0`** so 75% of general/non-iOS tasks (Design tasks, Supervisors, Android/backend/web repos) route to `legion`, preserving `macmini` headroom for `ios`-exclusive tasks.
     * **Ephemeral Laptop Policy:** Hosts without `24/7` (`routing_weight = 0`, e.g. `macbook`) are **never** auto-selected for background/Supervisor tasks unless explicitly targeted by the user or all `24/7` hosts are offline.

2. **Live Load Telemetry & Deterministic Deficit/Headroom Scoring (`target_host: "auto"`):**
   * Each heartbeat reports active working load (ignoring `Idle`/`Blocked`/`Later` sessions) alongside OS pressure:
     * `working_sessions` (`StateWorking`), `queued_workstreams`, `cpu_load_pct`, `ram_free_gb`, `ram_total_gb`, `disk_free_gb`, and `overloaded` (`cpu_load_pct > 85%` or `ram_free_gb < 2GB`).
   * **Explicit `required_capabilities` Filtering:** When the `⚓ Supervisor` calls `create_task` or `spawn_session`, it passes `required_capabilities` (e.g. `["ios"]`, `["android", "agent:antigravity"]`, `["mcp:gdrive"]`). `ackbard` filters online `24/7` hosts by `required_capabilities` (and `node_host_paths` for `kind="code"` tasks).
   * **Capacity-Adjusted 75/25 Deficit Scoring:** When multiple `24/7` hosts qualify, `ackbard` targets the configured `75/25` ratio across active working sessions, dynamically tilted by live CPU and free RAM headroom.
   * **Never Block on Overload (Spawn + Warn):** If the selected host is under heavy load (`overloaded == true`), `ackbard` **spawns immediately anyway** and returns a `load_warning` in the API/MCP response so the UI badges the session and the Supervisor alerts the user.

3. **Multi-Week Historical Utilization (`host_load_metrics`):**
   * Each daemon records downsampled **5-minute rollups** (compacted to **1-hour buckets** after 7 days, retained for 90 days) capturing `avg_working_sessions`, `max_working_sessions`, `avg_cpu_pct`, `max_cpu_pct`, `min_ram_free_gb`, and `tasks_spawned_count`.
   * Surfaces in Web and Mobile as a **24-Hour Utilization Heatmap (Hour-of-Day $\times$ Host)** and **7d/30d Split & Headroom Charts** so routing weights (`75/25`) can be tuned empirically over weeks.

---

## 6. Zero-Input `⚓ Supervisor` Dispatchers, Product vs. Code Tasks & Native Cloud Artifacts

### 6.1 Zero-Input `⚓ Supervisor` Sessions (Lightweight Project Dispatchers)
1. **1-Click / 1-Tap `[+ ⚓ New Supervisor]`:** Clicking `[+ ⚓ New Supervisor]` in the **`[⚓ Supervisors]`** sidebar view opens an instant blank session on a 24/7 host (`macmini` or `legion`) with **zero upfront modal inputs** (no pre-selecting host, folder, or branch).
2. **First-Turn Project Binding (`bind_supervisor_session`):** When the user says *"Start working on task MEA-204 in MEA"* or *"Let's prepare onboarding designs for NGL"*, the Supervisor calls `bind_supervisor_session` to bind itself to `Modemobile/MEA` or `Modemobile/NGL` (`is_supervisor=1`) and titles itself automatically.
3. **Dispatcher Role (Zero Context Bloat):** The `⚓ Supervisor` stays lightweight—it creates the Task on the Work Board and spawns a dedicated **`📋 Task`** session (`kind="design"` or `kind="code"`) for the deep work. Multiple Supervisor sessions can exist per project (e.g. per Epic/Initiative).

### 6.2 Dedicated Product/Design Tasks vs. Engineering Repo Tasks + Native Hosted Artifacts
Both Product Discovery and Code Implementation run in their own isolated **`📋 Task`** sessions and publish **native cloud artifacts** (`attach_deliverable`) instead of cluttering a GitHub artifacts repo:

* **🎨 Product / Design Task Sessions (`kind="design"`):**
  * Used for ideation, `/grill-me`, PRDs, UI mockups, architecture slide decks, and spreadsheet models.
  * `dev-workflow`'s `guard.py` is bypassed (`task_kind="design"`) so creative exploration is never blocked by code-worktree or unit-test gates.
* **⚙️ Engineering Repo Task Sessions (`kind="code"`):**
  * Spawns **one Tmux session per target repository** (e.g. `ngl-ios` on `macmini`, `ngl-android` on `legion`) starting in the main repo directory.
  * Driven by **`dev-workflow` (`N0`–`N15`)** inside that Tmux session: creates `.worktrees/<branch>` at `N3`, runs T0 static gates & simulator checks at `N5`, runs 3-way review at `N6–N8`, opens the PR at `N11`, cleans up the worktree after merge at `N14`, writes the retro at `N15`, and **auto-closes the Tmux window at `N15`** (moving the Ackbar session to `✅ Done`).
* **☁️ Native Hosted Artifacts (Claude Published Artifacts + Google Workspace):**
  * **Claude Code Sessions:** Publish deliverables via **Claude Published Artifacts** and register the hosted URL + `content_md` via `attach_deliverable`.
  * **Google Antigravity Sessions:** Publish deliverables via the **Google Workspace plugins (`gdrive`, `gdocs`, `gslides`, `gsheets`)** under `Ackbar/projects/<project-id>/tasks/<task-id>/` and register the `docs.google.com` / `drive.google.com` URL + `content_md` via `attach_deliverable`.

### 6.3 Sequential Cross-Platform Workstreams (`iOS First` $\rightarrow$ `Android Second`)
When a cross-platform feature should be validated on iOS (`@macmini`) before starting Android (`@legion`):
* Both `ngl-ios` and `ngl-android` belong to a **single Task** via `task_workstreams`, with `ngl-android` declaring `depends_on = "Modemobile/NGL/ngl-ios"`.
* By default, `ngl-android` waits in `QUEUED` until `ngl-ios`'s PR is **merged (`N14` / `DONE`)** (unless manually overridden earlier).
* At `N14`/`N15`, `ngl-ios` publishes its **Learnings** deliverable (Claude Published Artifact or Google Doc + synced `content_md` in `task_deliverables`).
* `legion` automatically unlocks `ngl-android` and seeds its Tmux session with the task spec, design artifacts, and the synced `ios-learnings` (`content_md`).

### 6.4 Resolving "Premature Idle vs. Done" via `dev-workflow` Node Mapping
Because finished task sessions **auto-close their Tmux window at `N15`** and move to **`✅ Done`**, a finished task session never sits in `StateIdle`. Whenever an open task session enters `StateIdle` (`Stop` hook), `ackbard` classifies it by its active `dev-workflow` node:
* **`N1` (Grill-Me):** **`🟠 BLOCKED: Clarifying Question`** — surfaced in Attention Queue and Voice Companion.
* **`N2` (Change Brief / Plan):** **`🟣 REVIEW: Plan Approval`** — links the hosted Plan artifact with `[✅ Approve Plan]`.
* **`N3`–`N10` (Mid-Implementation / Tests / Review without `report_blocker`):** **`⚠️ STALLED MID-WORKFLOW`** — badged `⚠️ Stalled` with 1-tap `[▶ Nudge]`.
* **`N11`–`N13` (PR Shipped):** **`🟣 REVIEW: PR Ready`** — displays PR link and `[⚡ Squash & Merge PR]`.
* **`N14`–`N15` (Merged & Retro Complete):** **`✅ DONE`** — Tmux window auto-closed and session moved to `✅ Done`.

---

## 7. Coexistence with Manual Sessions & Project Folder Hierarchy

Inside `[💻 Workspace]`, a top sidebar switcher (`[🌐 All Tree]` | `[⚓ Supervisors]`) provides 1-click access to all Supervisor dispatchers, while each project folder organizes sessions into four collapsible sections:

```text
▼ 📁 Modemobile / NGL
  ├── ⚓ Supervisors (1)
  │     └── ⚓ NGL Supervisor (Dispatcher)
  ├── 📋 Tasks (2 active)
  │     ├── 🎨 [Design] Onboarding PRD & UI
  │     └── ⚙️ [N5 • iOS] NGL-409 @macmini
  ├── 💻 Manual Sessions (2)
  ├── ⏳ Later (1)
  └── ✅ Done (5)  ◄── Closed N15 task workers land here
```

---

## 8. SQLite Schema (`internal/daemon/db.go`)

```sql
CREATE TABLE IF NOT EXISTS node_host_paths (
    node_path TEXT NOT NULL,                   -- Logical tree_nodes.path, e.g. 'Modemobile/NGL/ngl-ios'
    host TEXT NOT NULL,                        -- 'macmini', 'legion', 'macbook'
    project_dir TEXT NOT NULL,                 -- Host-local path, e.g. '/Users/dev4u/Work/NGL/ngl-ios'
    git_remote TEXT,                           -- Optional git origin URL for auto-linking
    capabilities TEXT,                         -- JSON array, e.g. '["ios","macos","simulator"]'
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (node_path, host)
);

-- Host Policy & Historical Load Rollups (5-min buckets compacted to 1-hr after 7d; retained 90d)
-- Note: hosts table is extended with policy_tags TEXT ('["24/7"]'), routing_weight INTEGER DEFAULT 50,
-- and detected_capabilities TEXT ('["linux","android","agent:claude-code","mcp:gdrive"]')
CREATE TABLE IF NOT EXISTS host_load_metrics (
    host TEXT NOT NULL,                        -- 'legion', 'macmini', 'macbook'
    bucket_start TIMESTAMP NOT NULL,           -- 5-min or 1-hr UTC bucket start
    bucket_sec INTEGER NOT NULL DEFAULT 300,   -- 300 (5m) or 3600 (1h)
    avg_working_sessions REAL NOT NULL,        -- Average StateWorking sessions during bucket
    max_working_sessions INTEGER NOT NULL,     -- Peak StateWorking sessions during bucket
    avg_cpu_pct REAL NOT NULL,                 -- Average OS CPU load % (0..100)
    max_cpu_pct REAL NOT NULL,                 -- Peak OS CPU load % (0..100)
    min_ram_free_gb REAL NOT NULL,             -- Minimum free RAM in GB during bucket
    tasks_spawned_count INTEGER DEFAULT 0,     -- Number of tasks/sessions routed to host in bucket
    PRIMARY KEY (host, bucket_start, bucket_sec)
);

CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,                       -- Sovereign ULID, e.g. 'task_01j8x92k'
    title TEXT NOT NULL,
    task_kind TEXT NOT NULL DEFAULT 'code',    -- 'design' (Product/UI) or 'code' (dev-workflow repo task)
    group_name TEXT NOT NULL,                  -- Top-level group, e.g. 'Modemobile', 'Personal'
    project_name TEXT NOT NULL,                -- Second-level project, e.g. 'NGL', 'MEA'
    subproject_name TEXT,                      -- Optional third-level, e.g. 'ngl-ios'
    status TEXT NOT NULL DEFAULT 'NEW',        -- 'NEW', 'IN_PROGRESS', 'REVIEW', 'DONE'
    substatus TEXT NOT NULL DEFAULT 'ready',   -- 'question', 'plan_review', 'active', 'stalled', 'pr_review', 'completed'
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
    target_host TEXT NOT NULL,                 -- 'macmini', 'legion', 'macbook'
    depends_on TEXT,                           -- Optional upstream repo_node_path (unlocks on N14 merge by default)
    workflow_node TEXT DEFAULT 'N0',           -- Active dev-workflow node ('N0'..'N15')
    status TEXT NOT NULL DEFAULT 'NEW',        -- 'QUEUED', 'IN_PROGRESS', 'REVIEW', 'DONE'
    branch TEXT,                               -- e.g. 'feat/task-104-rokt'
    worktree_path TEXT,                        -- e.g. '.worktrees/task-104-rokt'
    pr_url TEXT,
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
    workflow_node TEXT,                        -- Active dev-workflow node ('N0'..'N15')
    agent TEXT NOT NULL,                       -- 'claude-code', 'antigravity', 'codex'
    host TEXT NOT NULL,                        -- 'macmini', 'legion', 'macbook'
    is_active INTEGER DEFAULT 1,
    assigned_at TIMESTAMP NOT NULL,
    PRIMARY KEY (task_id, session_id),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_deliverables (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    kind TEXT NOT NULL,                        -- 'prd', 'html_ui', 'slides', 'plan', 'learnings', 'pr', 'retro'
    title TEXT NOT NULL,
    provider TEXT,                             -- 'claude_artifact', 'gdrive', 'gdocs', 'gslides', 'gsheets', 'github'
    host TEXT NOT NULL,                        -- Host where created
    file_path TEXT,                            -- Optional local path or Drive path
    url TEXT,                                  -- Hosted URL (Claude Artifact URL or docs.google.com / drive.google.com)
    content_md TEXT,                           -- Synced markdown content for cross-host agent handoff (e.g. learnings)
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
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
