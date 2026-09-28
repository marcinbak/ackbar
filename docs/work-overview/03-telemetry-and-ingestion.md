# 03 — Telemetry, Hooks & Ingestion Architecture

## 1. The Three-Tier Ingestion Strategy

To balance token economics, real-time speed, and high-fidelity discovery, ingestion is divided across three layers:

```
┌────────────────────────────────────────────────────────────────────────┐
│                        THREE-TIER INGESTION PIPELINE                   │
│                                                                        │
│  TIER 1: PROGRAMMATIC WATCHER (0 Tokens / 0ms Latency)                 │
│  • Tool hooks (`PreToolUse`/`PostToolUse`) intercept commands.         │
│  • Git branch & worktree regex (`feat/NGL-409` ➔ Jira `NGL-409`).      │
│  • `gh pr create` stdout regex ➔ captures PR URL & flips to REVIEW.    │
│  • Process exits ➔ transitions session state.                          │
│                                                                        │
│  TIER 2: IN-FLIGHT SKILL + PROMPT + MCP (Minimal Tokens)               │
│  • Working agent calls `propose_task` when it spots an unprompted bug. │
│  • Agent calls `task_update` to record custom notes or milestones.     │
│  • Prevents the agent from derailing its main objective.               │
│                                                                        │
│  TIER 3: POST-EXECUTION SYNTHESIZER (On-Demand / Completion Only)      │
│  • Runs when session enters REVIEW or when generating standups.        │
│  • Lightweight model (Gemini Flash / Claude Haiku) writes 1-line       │
│    executive bullet points from raw activity logs.                     │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Tool Execution Hooks (`PreToolUse` & `PostToolUse`)

Both Claude Code and Antigravity provide native lifecycle hooks before and after tool calls. The Ackbar hook shim (`cmd/ackbar-hook` and `/v1/hooks/<agent>`) monitors these tool events transparently:

### Ingested Tool Patterns:

| Agent Tool Action | Intercepted Payload | Automated Dashboard Mutation |
| :--- | :--- | :--- |
| **`Bash` ➔ `gh pr create`** | Stdout: `https://github.com/modemobile/ngl-ios/pull/82` | Binds PR URL, attaches PR number `#82`, and transitions task to `status: REVIEW, substatus: in_review`. |
| **`Bash` ➔ `git checkout -b feat/NGL-409`** | Command string contains `NGL-409` | Extracts Jira tracker reference `NGL-409` with zero token cost. |
| **`Bash` ➔ `go test` / `flutter test`** | Exit code: `0` (pass) or `1` (fail) | Sets task `ci_status` badge: `PASSING` or `FAILING`. |
| **`Write` / `Edit` ➔ `*.html`, `*.png`** | File path in tool arguments | Automatically registers file as an attached deliverable artifact. |
| **`Write` ➔ `*.retro.md`** | Detects dev-workflow N15 retro write | Attaches retrospective deliverable and prepares standup data. |

---

## 3. Integration with `dev-workflow` Skill

The workspace's `dev-workflow` skill maintains a persistent run state on disk at `~/.claude/dev-workflow-runs/`:
* `<repo>-<KEY>.json` (node transitions, `human_interventions`, `tier`, `artifacts`, `pr`)
* `<repo>-<KEY>.retro.md` (Node N15 retrospective, token report, lessons learned)
* `<repo>-<KEY>.brief.md` (Node N2 change brief, objectives)

### Integration Points:
1. **Node N2 (Change Brief):** Automatically populates the task title, requirements, and scope when the planner finishes.
2. **Node N11 (PR Opened):** Transitions task to `REVIEW` with PR URL and branch bound.
3. **Node N15 (Retrospective & Lessons Learned):**
   * The daemon reads the `.retro.md` sidecar and attaches it as a deliverable.
   * Any bullet points under `## Proposals` or `## Future Work` are automatically parsed and inserted into the `NEW` inbox as discovered tasks.

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
