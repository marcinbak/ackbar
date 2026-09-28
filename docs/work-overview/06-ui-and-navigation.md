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
