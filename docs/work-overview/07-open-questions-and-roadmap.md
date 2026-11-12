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

## 3. Implementation Roadmap Archive

All 5 implementation phases are complete. See [docs/archive/completed-tasks.md](../archive/completed-tasks.md#3-work--fleet-overview-5-phase-roadmap) for the historical roadmap.
