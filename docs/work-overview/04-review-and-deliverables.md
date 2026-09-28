# 04 — Review & Deliverables Architecture

## 1. Task Review vs. PR Review

A critical insight is that evaluating an agent's work involves more than just a Git code diff:

* **PR Review (Code Level):** Verifying lines of code, static analysis, unit test coverage, and GitHub comments.
* **Task Review (Deliverable Level):** Verifying that the feature or bugfix actually works and achieves the product goal. This requires inspecting rich outputs: interactive UI prototypes, mobile simulator recordings, benchmarks, or architectural plans.

---

## 2. Deliverables Inventory

Tasks in the **`REVIEW`** column can have multiple attached deliverables:

| Deliverable Kind | File Format | What It Represents | Preview / Launch Action |
| :--- | :--- | :--- | :--- |
| **Interactive HTML UI** | `.html` | Generative UI mockups, web components, dashboards. | Opens in default browser or embedded tab. |
| **Simulator Video** | `.mp4`, `.mov` | Screen recording from iOS Simulator / Android Emulator. | Launches system default media player (`QuickTime`). |
| **Screenshot / Image** | `.png`, `.webp` | UI snapshot or test comparison visual. | Launches system image viewer (`Preview`). |
| **Implementation Plan** | `.md` | Architectural plan, trade-offs, and file breakdown. | Opens in markdown reader or VS Code. |
| **Retrospective** | `.retro.md` | `dev-workflow` N15 lessons learned, token costs, interventions. | Opens markdown reader with key metrics highlighted. |
| **Pull Request** | Web URL | GitHub / GitLab pull request with CI status. | Opens GitHub PR in default browser. |

---

## 3. Location Resolution (`host:file_path`)

Deliverables can reside across different physical machines:

1. **Local Artifacts (`host == "local"` or `host == "mac"`):**
   * The dashboard invokes the system default launcher:
     ```bash
     open "/Users/dev4u/.../rokt_preview.html"
     ```
2. **Remote Artifacts (`host == "legion"`):**
   * If an artifact resides on a remote Linux server (`legion`):
     * **Option A (HTTP Serve):** `ackbard` on the remote server serves static uploads via `/v1/artifacts/<id>`.
     * **Option B (SSH Fetch):** The dashboard downloads a temporary copy via `scp` or SSH stream and opens it locally.

---

## 4. The Human Decision Loop

When a task enters the **`REVIEW`** column, the agent stops and yields control to the human:

```
┌────────────────────────────────────────────────────────────────────────┐
│                        REVIEW DECISION ACTIONS                         │
│                                                                        │
│  [✅ Squash & Merge PR]                                                │
│  • Only enabled when substatus == 'approved' and CI checks are green.  │
│  • Executes `gh pr merge --squash --auto`.                             │
│  • Moves task to `DONE: completed`.                                    │
│  • Closes out worktree and transitions external Jira/Linear status.    │
│                                                                        │
│  [🔄 Request Rework]                                                   │
│  • Prompts the user: "What adjustments are needed?"                    │
│  • Example: "Fix button clipping on small screen devices."             │
│  • Transitions task back to `IN_PROGRESS: active`.                     │
│  • Injects prompt into the agent's live session and resumes coding.    │
│                                                                        │
│  [💬 Unblock Agent]                                                    │
│  • Appears when task is `IN_PROGRESS: blocked`.                        │
│  • Displays the agent's question or permission prompt.                 │
│  • Submits user response directly into the agent's PTY.                │
└────────────────────────────────────────────────────────────────────────┘
```
