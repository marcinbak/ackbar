---
name: ackbar-tasks
version: "20261104.08"
description: "Native Ackbar Task Management: track active work, report blockers without stalling, attach rich deliverables (UI mockups, plans, retros), and propose discovered bugs/tech-debt directly into the Ackbar Work Board."
argument-hint: "[task-id or action]"
---

# Ackbar Tasks Skill

Provides guidelines for AI coding agents (Claude Code, Google Antigravity, OpenAI Codex) interacting with **Project Ackbar's Work & Fleet Overview Dashboard** using the `ackbar` MCP tools.

---

## 1. Core Principles

1. **Zero Silent Stalls:** Never wait passively for user decisions or credentials. If you are blocked on an architectural choice, schema question, or external API key, immediately call `report_blocker`. This highlights your session on the Ackbar board, emits an attention alert, and allows the human supervisor to answer directly.
2. **Ambient Discovery:** At the start of a task or session, call `get_current_task` to bind to the active task, branch, and worktree.
3. **Deliverables Discipline:** Whenever creating deliverables (implementation plans `.md`, generative HTML UI prototypes `.html`, test recordings `.mp4`, or retrospectives `.retro.md`), call `attach_deliverable`.
4. **Discovered Work Capture:** When noticing an unprompted bug or refactor opportunity during active coding, call `propose_task` instead of derailing your current objective or losing it in scrollback.

---

## 2. Tool Usage Reference

### A. Discovering & Starting Work
```json
// Resolve active task for this worktree / branch
mcp__ackbar__get_current_task({})

// Or list active tasks in progress (can filter by ?kind=code|design)
mcp__ackbar__list_tasks({ "status": "IN_PROGRESS" })

// Or inspect fleet host capabilities, routing weights (75/25), and live load before dispatching
mcp__ackbar__list_hosts({ "include_metrics": true, "days": 14 })

// Or create a new task with multi-repo workstreams, capability routing, agent runtime, worker prompt, and auto-start
mcp__ackbar__create_task({
  "title": "Migrate telemetry to WebSocket sync",
  "project_name": "Ackbar",
  "group_name": "Personal",
  "task_kind": "code",
  "agent": "claude-code", // or "antigravity", "codex" (defaults to caller agent or claude-code)
  "target_host": "auto",  // "auto" uses capability filtering + 75/25 weighted deficit load balancing across 24/7 hosts
  "required_capabilities": ["ios"], // e.g. ["ios"], ["android"], ["mcp:gdrive"]
  "prompt": "Implement WebSocket telemetry sync per specs in docs/telemetry.md",
  "repos": [
    { "repo_node_path": "Personal/Ackbar", "target_host": "auto" },
    { "repo_node_path": "Modemobile/NGL/ngl-ios", "depends_on": "Personal/Ackbar", "required_capabilities": ["ios"] }
  ],
  "auto_start": true
})
```

### B. Reporting & Clearing Blockers
When you need user clarification (e.g. choice between two implementation strategies):
```json
mcp__ackbar__report_blocker({
  "task_id": "task_123",
  "question": "Should bitmap textures be downscaled before caching or should JVM heap ceiling be increased?"
})
```
When the user provides the answer:
```json
mcp__ackbar__clear_blocker({
  "task_id": "task_123",
  "resolution_notes": "User selected texture downscaling to preserve low-end device RAM."
})
```

### C. Attaching & Inspecting Deliverables / Retrospectives
When creating plans, design prototypes, retrospectives, architecture briefs, or linking Google Docs / Claude Artifacts:
```json
// Local file deliverable (auto-reads content_md if omitted; 'type' accepted as alias for 'kind')
mcp__ackbar__attach_deliverable({
  "task_id": "task_123",
  "title": "Interactive Generative UI Mockup",
  "kind": "html_ui",
  "file_path": "/path/to/mockup.html"
})

// Cloud artifact (Google Doc, Slides, Sheets, or Claude Artifact)
mcp__ackbar__attach_deliverable({
  "task_id": "task_123",
  "title": "Design Specs & Flow",
  "kind": "prd",
  "url": "https://docs.google.com/document/d/1abc.../edit"
})

// Inline Markdown deliverable (renders directly in Web modal)
mcp__ackbar__attach_deliverable({
  "task_id": "task_123",
  "title": "Post-Implementation Learnings",
  "kind": "learnings",
  "content_md": "## Key Learnings\n- Pure Go SQLite outperforms CGO wrapper in daemon throughput."
})

// Fetch deliverable markdown with rune-safe cursor pagination (max_chars up to 50000)
mcp__ackbar__get_deliverable({
  "task_id": "task_123",
  "kind": "retro",
  "max_chars": 20000,
  "char_offset": 0
})

// Query compact retrospective summaries and structured RetroMetrics without full markdown
mcp__ackbar__list_retros({
  "node_path": "Personal/Ackbar",
  "since": "2026-11-01",
  "limit": 20
})
```
Supported kinds: `prd`, `html_ui`, `slides`, `plan`, `learnings`, `pr`, `retro`, `retrospective`, `video`, `screenshot`, `mockup`, `doc`, `other` (accepts `type` as alias; auto-infers title).
Supported providers (auto-inferred if omitted): `claude_artifact`, `gdocs`, `gslides`, `gsheets`, `gdrive`, `github`, `local`.
Note: Local `.retro.md` deliverables auto-ingest canonical `run-state.json` metrics (`RetroMetrics`) and replicate across peer daemons via LWW mesh sync.

### D. Proposing Discovered Tasks
When spotting an unexpected bug or future optimization:
```json
mcp__ackbar__propose_task({
  "title": "Fix memory leak in Android rapid scroll image decoding",
  "group": "Modemobile",
  "project": "ngl-android",
  "notes": "Bitmap allocations exceed JVM ceiling during rapid fling in ImageCache.kt",
  "rationale": "Discovered while profiling network requests; out of scope for current PR."
})
```

### E. Moving to Review, Updating Workflow Nodes & Merging
To update your dev-workflow node progress (N0 through N15) or review status:
```json
// Update active workflow node (e.g. N2 plan review, N6 verification, N11 PR)
mcp__ackbar__update_task({
  "task_id": "task_123",
  "workflow_node": "N2"
})

// Move task to another project node (e.g. from Due-Dilligence to NGL/ngl-android):
mcp__ackbar__update_task({
  "task_id": "task_123",
  "project_node_path": "Modemobile/NGL/ngl-android"
})

// When your pull request is opened:
mcp__ackbar__update_task({
  "task_id": "task_123",
  "workflow_node": "N11",
  "status": "REVIEW",
  "substatus": "in_review",
  "pr_url": "https://github.com/org/repo/pull/42"
})
```
When authorized to merge:
```json
mcp__ackbar__merge_pull_request({
  "task_id": "task_123",
  "method": "squash"
})
```

### F. Supervisor Dispatcher Protocol (Turn 1)
When acting as an **⚓ Ackbar Supervisor Dispatcher** (session workspace `~/.ackbar/supervisor`):
1. **Hard Rule 1 — Zero Product Code:** Never write product code, edit repository files, or create git worktrees directly in this supervisor session workspace. All execution work belongs in dedicated worker sessions.
2. **Hard Rule 2 — Turn 1 Dispatch & Binding:** On Turn 1 (or whenever the user mentions a project, Jira ticket such as `NGL-XXX`, or initiative), immediately:
   - Call `bind_supervisor_session` to place this supervisor session under the target project node:
     ```json
     mcp__ackbar__bind_supervisor_session({
       "session_id": "<your-session-id>",
       "node_path": "Modemobile/NGL",
       "title": "⚓ NGL Feature Planning"
     })
     ```
   - Call `create_task` with `auto_start: true`, specifying `title`, `repos`, `agent`, and `prompt` (with the complete ticket requirements) to launch an execution worker in the actual repository. (Note: `create_task` will also automatically fallback-bind your supervisor session to the target project node if `bind_supervisor_session` was omitted).
3. **Hard Rule 3 — Supervise via MCP Tools:** Use `list_sessions`, `get_session_output`, `send_session_input`, `list_tasks`, and `control_session` to monitor, steer, and verify worker progress. Report milestones and blockers to the user.
