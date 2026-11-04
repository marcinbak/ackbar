# 06 — UI Design, Mode Switching & Navigation

## 1. Zero Context-Switching Principle

The Work Overview board is not an isolated external website or separate desktop app. It is integrated directly into the core **Ackbar** user interface as a native view mode.

```
┌────────────────────────────────────────────────────────────────────────┐
│ ⚓ ACKBAR v0.9.4   [💻 Workspace (Terminal & Chat)]  [📋 Work Board]   │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. In-App Navigation: Tab Focus vs. Terminal Popups

Rather than spawning isolated terminal popups, the dashboard leverages Ackbar's existing **PTY tab strip** (`tabStrip` in `web/js/tabs.js`):

1. **Clicking a Worker Pill on a Task Card:**
   * User clicks `[Claude Code @mac (42%)]` on an active task.
   * The dashboard flips the app mode from `Work Board` to `Workspace`.
   * Invokes `activateTab(sessionId)` to bring that session's live terminal canvas into view immediately.
   * If the tab was closed, it automatically re-opens it in the tab strip.
2. **Returning to the Board:**
   * Clicking the `[📋 Work Board]` mode button returns instantly to the Kanban view with scroll position and active filters preserved.

---

## 3. Minimalist, Linear-Inspired Card Design

To prevent visual fatigue and clutter, the board follows a strict **progressive disclosure** design philosophy:

### Clean Card at Rest:
* **Header:** Quiet tracker key (`NGL-409`), project tag (`NGL`), and a subtle substatus dot.
* **Body:** Clean title and 1-line note.
* **Footer:** Worker indicator pill (`@mac`) and relative timestamp (`2h ago`).

### Contextual Actions (Shown Only When Needed):
* **`[⚡ Squash & Merge PR #82]`:** Surfaces only when substatus is `approved` and CI is green.
* **`[💬 Unblock Agent]`:** Surfaces only when the session is `blocked`.
* **`[👁️ Inspect Deliverables]`:** Surfaces only when deliverables (`.html`, `.mp4`, `.retro.md`) are attached.

---

## 4. TUI Dashboard Integration (`cmd/ackbar`)

In the terminal Bubble Tea interface:
* Pressing **`b`** toggles between the logical project tree and the 4-pillar Task Board.
* Arrow keys navigate between `NEW`, `IN_PROGRESS`, `REVIEW`, and `DONE` columns.
* Pressing **`enter`** on an active task attaches in-place to that tmux session (`tea.ExecProcess`).
* Pressing **`s`** pops open the formatted daily standup markdown reader.

---

## 5. Mobile App Alignment (`mobile/` — Phase 5 — Implemented)

**Phase 5** is fully implemented and active, bringing the Flutter Mobile App (`mobile/lib/`) into complete functional parity with the Web GUI and TUI Supervisor & Task Control Plane:

### 5.1 Hierarchical Session Tree Parity (`fleet_providers.dart` & `fleet_tree_view.dart`)
* **Hierarchical Node Resolution:** Replaced the legacy flat 1-level `projectDisplayName` map with `fleetNodesProvider` (fetching `GET /v1/nodes?federated=true`) and `resolveSessionNodePath(session, nodes)` ported from `web/js/state.js` to Dart (`lib/core/models/node_path_resolver.dart`). Sessions now resolve to their full hierarchical path (`Modemobile/NGL/ngl-ios`, `Personal/Ackbar`, etc.).
* **Collapsible Fleet Tree View:** `fleet_tree_view.dart` renders recursive, collapsible multi-level folder trees in `fleet_screen.dart` with standardized project subsections: **`⚓ Supervisors`**, **`📋 Tasks`**, **Manual Sessions**, **`⏳ Later`**, and **`✅ Done`**, including `[offline]` badges for sessions on asleep hosts.

### 5.2 Zero-Input `⚓ Supervisors` Filter & Creation on Mobile
* **Filter Toggle:** The **`[🌐 All Tree | ⚓ Supervisors]`** segmented control at the top of `fleet_screen.dart` toggles between the full hierarchical tree and active supervisor sessions.
* **1-Tap Spawning:** The **`⚓+`** 1-tap New Supervisor action spawns a supervisor session via `POST /v1/sessions/spawn` (`is_supervisor: true`, zero upfront modal inputs) and immediately pushes `TranscriptScreen`.

### 5.3 Mobile Work Board & Hosted Deliverables (`work_board_screen.dart`)
* **Mobile Work Board (`WorkBoardScreen`):** Backed by `tasksProvider` (`GET /v1/tasks?federated=true`), featuring horizontal pillar tabs (**`NEW`**, **`IN_PROGRESS`**, **`REVIEW`**, **`DONE`**) and group filters (`All`, `Modemobile`, `Personal`).
* **Interactive Task Cards (`TaskCard`):** Displays task metadata, assignee badges, workstream PR links, and `[⚡ Squash & Merge PR]` action button (`POST /v1/tasks/merge-pr`).
* **Deliverable Inspection (`DeliverableMarkdownModal`):** Render 1-tap deliverable chips on task cards that open external links (**Claude Published Artifacts**, **Google Docs / Slides / Drive**) natively on device, or inspect inline Markdown deliverables (`.md`, `.retro.md`, `.plan.md`) via the bottom-sheet `DeliverableMarkdownModal`.

### 5.4 Mobile Voice & Audio Companion (`transcript_screen.dart`)
* **Two-Tier Speech Pipeline:** Powered by `AudioCompanionService` with Tier 1 client-side Markdown speech sanitization (`speech_sanitizer.dart`) and Tier 2 remote Haiku summarization (`POST /v1/sessions/summarize-speech`).
* **Transcript Audio Controls (`SpeechCompanionBar`):** Embedded in `transcript_screen.dart`, providing header toggles (**`[🔊 Auto-Read]`**, **`[🎤 Mic]`**), persistent bottom companion bar, per-message bubble actions (**`[🔊 Read]`**, **`[⏹️ Stop]`**, **`[↺ Read Again]`**), and commuter quick-reply drill-down chips.
