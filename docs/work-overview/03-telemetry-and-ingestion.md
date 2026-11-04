# 03 — Explicit MCP Telemetry & Worker Binding Architecture

## 1. Pure MCP Telemetry & Explicit Task Creation

To prevent duplicate/hallucinated tasks and noisy auto-attached files, the Ackbar Task Engine enforces **strict explicit task creation and 100% explicit MCP telemetry**:

```
┌────────────────────────────────────────────────────────────────────────┐
│                    EXPLICIT MCP TELEMETRY PIPELINE                     │
│                                                                        │
│  1. EXPLICIT TASK CREATION ONLY                                        │
│  • Tasks are created exclusively via `ackbar mcp` (`create_task`,      │
│    `propose_task`) or the Work Board UI (`POST /v1/tasks`).            │
│  • Ad-hoc terminal sessions and file edits never auto-create tasks.    │
│                                                                        │
│  2. EXPLICIT WORKER BINDING (`SyncSessionTaskWorker`)                  │
│  • Sessions bound via `task_id` / `current_task_id` at spawn time      │
│    automatically sync live process state (`Working`, `Blocked`, `Idle`)│
│    to their bound task.                                                │
│  • If all bound worker sessions end (`StateEnded`) while a task is     │
│    still `IN_PROGRESS`, the task transitions to `substatus: stalled`.  │
│                                                                        │
│  3. EXPLICIT MCP LIFECYCLE & DELIVERABLES                              │
│  • Agents report workflow nodes (`N0`–`N15`, `DONE`), PR URLs,         │
│    blockers (`report_blocker`), and deliverables (`attach_deliverable`)│
│    directly via `ackbar mcp`.                                          │
│                                                                        │
│  4. ON-DEMAND PR RECONCILIATION                                        │
│  • Opening or refreshing the Work Board (`?reconcile_prs=true`) checks │
│    open PR URLs via `gh pr view` and advances merged PRs to `DONE`.    │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Explicit MCP Tools (`internal/mcp/tools.go`)

| MCP Tool | Purpose | Automated Dashboard Mutation |
| :--- | :--- | :--- |
| **`create_task`** | Creates a structured task bound to a canonical `tree_nodes` path | Inserts `NEW` (`ready`) task and optional `task_workstreams`; optionally spawns worker if `auto_start: true`. |
| **`update_task`** | Reports `workflow_node` (`N0`–`N15`, `DONE`), `status`, `substatus`, `notes`, or `pr_url` | Deterministic `ClassifyWorkflowNodeState` transitions (`N2` $\rightarrow$ `REVIEW/plan_review`, `N11` $\rightarrow$ `REVIEW/pr_review`, `N14`/`N15`/`DONE` $\rightarrow$ `DONE/completed`). |
| **`report_blocker` / `clear_blocker`** | Surfaces or resolves an agent blocker | Sets `substatus: blocked` + `blocker_question` (or restores `active`). |
| **`attach_deliverable`** | Attaches a change brief, HTML prototype, Cloud doc, or retro | Stores deliverable in `task_deliverables` (with `content_md` lazy-loaded on demand in the UI). |
| **`propose_task`** | Logs an unexpected bug or tech-debt item discovered in-flight | Deduplicates by title and inserts into `NEW` (`discovered`). |

---

## 4. Spinoff Bug & Opportunity Discovery (`propose_task`)

When an agent notices an unprompted bug, memory leak, or refactor opportunity during active coding:

```typescript
// MCP Tool Definition: propose_task
{
  "name": "propose_task",
  "description": "Log an unexpected bug, tech-debt item, or opportunity discovered during work without derailing the current task.",
  "parameters": {
    "title": "Bitmap texture memory spike on Android 14",
    "type": "bug | opportunity | tech_debt",
    "severity": "low | medium | high",
    "rationale": "Bitmap allocation exceeds JVM ceiling during rapid fling scroll",
    "file_reference": "ngl-android/src/main/ImageCache.kt#L84"
  }
}
```

### Dashboard Behavior:
* The proposal drops quietly into the **`NEW`** column tagged `[💡 Discovered]`.
* No intrusive sound or push alerts break the user's flow.
* The user can triage at their convenience:
  * `[🚀 Start Agent Now]` ➔ Launches new worktree session.
  * `[🎫 Push to Jira/Linear]` ➔ Files external ticket.
  * `[🗑️ Dismiss]` ➔ Rejects proposal.
