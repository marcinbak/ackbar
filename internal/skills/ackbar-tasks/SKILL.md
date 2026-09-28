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

// Or list active tasks in progress
mcp__ackbar__list_tasks({ "status": "IN_PROGRESS" })
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

### C. Attaching Deliverables
When creating plans, design prototypes, or retrospectives:
```json
mcp__ackbar__attach_deliverable({
  "task_id": "task_123",
  "title": "Interactive Generative UI Mockup",
  "kind": "html_ui",
  "file_path": "/path/to/mockup.html"
})
```
Supported kinds: `html_ui`, `plan`, `retrospective`, `video`, `screenshot`, `other`.

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

### E. Moving to Review & Merging
When your pull request is opened:
```json
mcp__ackbar__update_task({
  "task_id": "task_123",
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
