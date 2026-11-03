# 07 — Open Questions, Challenges & Roadmap

## 1. Challenges & Evaluated Resolution Options

### Challenge 1: Multi-Host Synchronization & Offline Hosts
* **Problem:** Remote compute servers (e.g. `legion`) may sleep, disconnect, or reboot. Standup queries shouldn't fail if a remote host is temporarily unreachable.
* **Resolution Options:**
  * **Option A (Decentralized On-Demand Query):** Query each host over SSH when opening the dashboard. *Drawback: Slow and fails if a machine is offline.*
  * **Option B (Primary Event Ledger in Local SQLite — Recommended):** Whenever a remote daemon emits a task event, it streams it to the primary workstation daemon via Ackbar Relay / WebSocket. Historical milestones are preserved in local SQLite even if the remote host shuts down overnight.

### Challenge 2: Guarding Against Hallucinated Completion
* **Problem:** An agent might report "Task complete!" even though unit tests are failing or uncommitted changes remain in the worktree.
* **Resolution Options:**
  * **Deterministic Gate Verification:** A task cannot move to `approved` / `completed` until the daemon verifies that `git status --porcelain` is clean, the PR has been opened, and CI checks are passing.

### Challenge 3: Deduplication of Discovered Bugs
* **Problem:** Two separate sessions (e.g. one in `ngl-ios`, one in `ngl-android`) might discover the same underlying backend API bug.
* **Resolution Options:**
  * **Option A (Manual Triage):** Both appear in `NEW: discovered`; the user can dismiss one as duplicate.
  * **Option B (Fuzzy Title Match):** The ingestion pipeline flags potential duplicates in the inbox if titles share significant string or file reference overlap.

---

## 2. Resolved Open Questions

1. **Auto-Archive Retention in `DONE`:**
   * **Decision:** Keep tasks in the database indefinitely, but visually collapse or hide tasks in the `DONE` column that are older than **7 days**. This perfectly supports the Weekly Retrospective use case without cluttering the daily view.
2. **Bidirectional External Tracker Sync:**
   * **Decision:** Do not reinvent bidirectional sync in Ackbar. Rely on existing GitHub-Jira or GitHub-Linear integrations. When clicking `[⚡ Squash & Merge PR]` on the dashboard, Ackbar simply executes the GitHub merge. GitHub's native webhooks will handle moving the Jira ticket to "Done". This keeps Ackbar's scope tight.
3. **Standup Delivery Destinations:**
   * **Decision:** Avoid building hardcoded Slack webhook integrations directly into the `ackbard` daemon. Instead, expose a generic `webhook_url` configuration, or allow an agent equipped with a Slack MCP tool to dispatch the generated Markdown automatically during the standup synthesis phase.

---

## 3. Unified 4-Phase Implementation Roadmap

```
┌────────────────────────────────────────────────────────────────────────┐
│                        IMPLEMENTATION PHASES                           │
│                                                                        │
│  PHASE 1: 24/7 FEDERATED CONTROL PLANE & UNIFIED SUPERVISOR MCP        │
│  • Sync `tree_nodes`, per-host repo paths (`path_macmini`,             │
│    `path_legion`), and session `node_path` across `legion` + `macmini` │
│    so Mobile (`iphone`) & Web (`macbook`) share the exact folder tree. │
│  • Implement `GET /v1/sessions?federated=true`, `/v1/nodes`,           │
│    and `/v1/tasks` on `ackbard`.                                       │
│  • Expand `ackbar mcp` (`internal/mcp/tools.go`) with the 5 Session    │
│    Control tools + 4 Task Stage-Gate tools (`complete_stage`, etc.).   │
│                                                                        │
│  PHASE 2: STAGE-GATE ENGINE, SEQUENTIAL WORKSTREAMS & WATCHDOG         │
│  • Add `task_workstreams` (`depends_on`, `learnings_md`) for parallel  │
│    and sequential cross-platform flows (e.g. `ngl-ios` ➔ `ngl-android`)│
│  • Implement deterministic `complete_stage` verification gates and the │
│    Premature Idle Watchdog (15s timer + up to 2 auto-nudges).          │
│  • Group task worker sessions under `📋 Task` in the session tree and  │
│    auto-clean `.worktrees/<task>` + archive sessions on `DONE`.        │
│                                                                        │
│  PHASE 3: VOICE COMPANION & 2-TIER LAZY SPEECH PIPELINE                │
│  • Implement `POST /v1/sessions/summarize-speech` with `$0` stateless  │
│    Haiku (`claude -p --model haiku`) and `(session_id, msg_idx)` cache.│
│  • Add Tier 1 client Markdown speech sanitizer, `[🔊 Auto-Read]`,      │
│    `[🎤 Mic]`, and per-message `[🔊 Read]` / `[↺ Read Again]` buttons. │
│                                                                        │
│  PHASE 4: INLINE WORK BOARD ARTIFACT DRAWER & COMMUTER QUICK-CHIPS     │
│  • Inline `.ackbar/plan.md` & `.ackbar/review/index.html` drawer with  │
│    1-tap `[✅ Approve Stage]`, `[💬 Request Changes]`, `[⚡ Merge PR]`.│
│  • Render interactive `suggested_chips` in mobile/web chat composer.   │
└────────────────────────────────────────────────────────────────────────┘
```
