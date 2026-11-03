---
name: ackbar-tasks
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

// Or create a new task with multi-repo workstreams and auto-start
mcp__ackbar__create_task({
  "title": "Migrate telemetry to WebSocket sync",
  "project_name": "Ackbar",
  "group_name": "Personal",
  "task_kind": "code",
  "repos": [
    { "repo_node_path": "Personal/Ackbar", "target_host": "local" },
    { "repo_node_path": "Modemobile/NGL/ngl-ios", "depends_on": "Personal/Ackbar" }
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

### C. Attaching Deliverables & Native Cloud Artifacts
When creating plans, design prototypes, retrospectives, architecture briefs, or linking Google Docs / Claude Artifacts:
```json
// Local file deliverable
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
```
Supported kinds: `prd`, `html_ui`, `slides`, `plan`, `learnings`, `pr`, `retro`, `retrospective`, `video`, `screenshot`, `mockup`, `doc`, `other`.
Supported providers (auto-inferred if omitted): `claude_artifact`, `gdocs`, `gslides`, `gsheets`, `gdrive`, `github`, `local`.

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

### F. Binding a Zero-Input `⚓ Supervisor` Session (Turn 1)
When acting as a Supervisor session (spawned via `[+ ⚓ New Supervisor]` without an upfront project folder) and the user mentions a target project or goal on Turn 1, immediately bind your session to the project tree node and set a descriptive title:
```json
mcp__ackbar__bind_supervisor_session({
  "session_id": "<your-session-id>",
  "node_path": "Modemobile/NGL",
  "title": "⚓ NGL Feature Planning"
})
```
