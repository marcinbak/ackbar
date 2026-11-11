# Project Ackbar Backlog & Roadmap

> **Tracking Doc:** Master inventory of active feature initiatives, architectural explorations, and completed milestones for Project Ackbar.

---

## 🎯 Active Backlog Items

### 1. 🧭 Supervisor Multi-Stage Orchestration & Inter-Stage Handoff
* **Status:** Architectural Exploration
* **Scope:**
  * **Two-Lane Task Classification (Fast Track vs. Multi-Stage):** Allow the `⚓ Supervisor` to route small bugs/chores directly into a single `dev-workflow` (`N0`–`N15`) worker session while orchestrating multi-stage initiatives across dedicated Discovery/Design (`kind="design"`) $\rightarrow$ Human Checkpoint $\rightarrow$ Per-Repo Implementation (`kind="code"`) stages.
  * **Hybrid Inter-Stage Handoff:** Combine canonical task deliverables (`task_deliverables` with `content_md` and auto-ingested `.brief.md` / `.impl-notes.md` / `.retro.md`) with target repository git branches for seamless context handoff between stages and across hosts (`@macmini` $\rightarrow$ `@legion`).
  * **Supervisor $\leftrightarrow$ Worker Check-In Loop:** Bounded daemon/Supervisor orchestration when a worker reaches human or stage checkpoints (`N1` question, `N2` plan review, `N11`/`N13` PR ready, `N14`/`N15` merge & retro).

---

### 2. 📱 Mobile Deployment & Distribution Pipeline
* **Status:** Planned
* **Scope:**
  * **Automated Signing & Builds:** Fastlane / GitHub Actions pipeline for iOS IPA (TestFlight / ad-hoc) and Android APK / AAB.
  * **Background SSE Keep-Alive:** Enhanced reconnection resilience when the mobile client is minimized during active streaming sessions.

---

### 3. 🔍 Session Management & Workspace Enhancements
* **Status:** Planned
* **Scope:**
  * **Advanced Fleet Search & Tagging:** Multi-factor search across hosts, token usage thresholds (e.g. `>80% context window`), and custom user labels.
  * **Transcript & Artifact Export:** One-click export of chat transcripts, tool calls, and plan diffs to Markdown and JSON formats.

---

## ✅ Completed Milestones

* [x] **Detached Headless Worker Spool, Replay & Runtime Lifecycle:**
  * Detached `--headless-worker` sidecar process (`Setsid: true`) with Unix domain socket IPC and `/dev/null` stdio isolation.
  * Monotonic NDJSON turn spooling (`<turn_id>.ndjson`), cursor-based SSE replay (`?after_seq`, `Last-Event-ID`), and transcript synthesis (`in_flight_turn`).
  * Startup re-attachment of live workers on daemon reboot without duplicate `--resume`.
  * Comprehensive runtime cleanup on manual `Done` and periodic background `auto_done` sweep.
  * Cross-OS idle-sleep inhibitor (`caffeinate` / `systemd-inhibit`) and systemd `KillMode=process` support.
* [x] **Unified Supervisor Control Layer, Task Orchestrator & Voice Companion (Ackbar 2.0):**
  * **4-Pillar Work Board & Multi-Repo Workstreams (`docs/work-overview/`):** Full `NEW` $\rightarrow$ `IN_PROGRESS` $\rightarrow$ `REVIEW` $\rightarrow$ `DONE` Kanban lifecycle with per-repository `task_workstreams`, single active worker enforcement per `(task_id, repo_node_path)`, canonical substatus normalization, and 1-click GitHub PR squash & merge (`POST /v1/tasks/merge-pr`).
  * **Zero-Input `⚓ Supervisor` Dispatchers & Dedicated `📋 Task` Sessions:** 1-tap `[+ ⚓ New Supervisor]` opens a session in `~/.ackbar/supervisor/`, auto-binds via `bind_supervisor_session`, and dispatches dedicated `📋 Task` sessions with direct CLI prompt arguments and automatic `IN_PROGRESS` activation.
  * **Monotonic `dev-workflow` State Sync & Deliverable Auto-Attachment (`workflow_watcher.go`, `telemetry_tasks.go`):** Maps `N0`–`N15` nodes across hook telemetry, `POST /v1/tasks`, and `~/.claude/dev-workflow-runs/*.json` (update-only for existing tasks) with monotonic status guards and automatic `.brief.md`, `.impl-notes.md`, and `.retro.md` deliverable ingestion.
  * **Host Capabilities, Weighted Load Balancing (`75/25`) & Fleet Utilization Telemetry (`host_capabilities.go`):** Auto-detects agents, MCP plugins, and UI test runtimes (`ios`, `android`, `chrome`, `docker`, `gpu`); infers platform capabilities (`["ios"]` / `["android"]`) from repo paths; routes `"auto"` spawns via capacity-adjusted 75/25 deficit scoring; and records 5-minute / 1-hour SQLite utilization rollups (`host_load_metrics`).
  * **Decoupled Voice/Audio Companion & 2-Tier Lazy Speech Pipeline (`docs/voice-companion.md`):** Hands-free and silent commuter modes with client Markdown sanitizer, `POST /v1/sessions/summarize-speech` (stateless Haiku + SQLite cache + structural AST fallback), and interactive quick-reply chips across Web and Mobile.
* [x] **Real-Time Mobile Push Notifications & Deep-Linking (`ntfy.sh`):**
  * Built-in `ntfy.sh` dispatcher (`internal/daemon/ntfy.go`) triggering push notifications with `ackbar://` mobile deep-links and Ackbar icons when sessions enter `StateBlocked` or tasks enter `REVIEW` (`plan_review`, `pr_review`, `design_review`) or `blocked`.
  * Test notification endpoint (`POST /v1/notifications/test`) and 1-click fleet-wide `ntfy_topic` / `ntfy_server` propagation across connected hosts.
* [x] **Release Automation & Homebrew Tap:**
  * GoReleaser v2 configuration (`.goreleaser.yaml`) cross-compiling for macOS (`arm64`, `amd64`) and Linux (`amd64`, `arm64`).
  * Automated Homebrew tap formula generation (`marcinbak/homebrew-ackbar`) with `brew services` daemon management.
  * GitHub Actions CI/CD workflows (`.github/workflows/release.yml`, `.github/workflows/ci.yml`) and enforced PR version bumping (`./scripts/bump-version.sh`).
* [x] **Unread vs. Read Lifecycle Dimension & Animated State Indicators:**
  * `is_unread` and `last_state_change_at` schema tracking with glowing pulse cues on newly blocked or idle sessions.
  * Automatic read acknowledgment (`POST /v1/sessions/mark-read`) when focusing sessions in Web or Mobile.
  * Real-time animated working state spinners (`⚙️` rotating in Web & Mobile).
* [x] **In-Place Cleared Conversation Turn Rotation & 1-Click Resume:**
  * Context resets (`/clear`, `/reset`) seamlessly archive prior turns as `(Conv 1)` / `⏹️ ENDED` and assign the live tmux window to `(Conv 2)`.
  * Universal 1-click conversation resumption (`POST /v1/sessions/control?action=resume&id=...`) launching native provider resume CLIs across Web (`▶ Resume in Tmux`) and Mobile (`▶ Resume Conversation`).
* [x] **Web Multi-Tab Lifecycle Context Menu:**
  * Right-click tab context menu supporting Close Tab, Close Other Tabs, Close Tabs to the Right, and Close All Tabs.
* [x] **Later Section & Session Parking:**
  * Added collapsible `⏳ Later` subsections to project groups and Unassigned folder with toggleable collapse memory and configurable default.
  * Parked sessions are immune to auto-done (`isSessionDone`) and auto-archive (`isSessionAutoArchived`) timers.
  * Direct session controls (`POST /v1/sessions/control?action=later` / `unlater`), drag-and-drop support, and right-click context menu options.
  * Sessions in Later remain parked during prompt interaction until explicitly moved by the user.
* [x] **Official Provider Vector Logos:**
  * High-performance vector `CustomPainter` paths and inline SVGs for Claude Code (pixelated CLI creature), Google Antigravity (Gaussian arch curve with Google gradient), and OpenAI Codex (interlocking Blossom vortex).
* [x] **Single Source of Truth Versioning:**
  * `internal/version/VERSION` embedded via `//go:embed` across all binaries and synchronized with `mobile/lib/core/constants/app_version.dart` via `./scripts/bump-version.sh`.
* [x] **OSC 52 Clipboard & Auto-Copy Stream:**
  * Terminal clipboard synchronization from remote Linux/Legion tmux sessions to macOS host clipboard and Web UI.
* [x] **Strict PR-Based Git Worktree Workflow & Open Source Guidelines:**
  * Branch protection enforced on `main`, `AGENTS.md` worktree workflow, `CONTRIBUTING.md`, and issue/PR templates.
* [x] **Automated Session Handover & Context Rotation:**
  * 1-click context rotation (`POST /v1/sessions/handover`) with automated briefing synthesis, context clearing (`/clear`, `/reset`), and fresh turn reseeding.
  * Configurable suggestion toggle and token threshold percentage (`handover_suggestion_enabled`, `handover_threshold_pct`) via daemon settings API and Settings dialog.
  * Warning/danger context badges, quick-action handover button, confirmation modal, and right-click context menu options across Web UI and Mobile companion.
* [x] **Chat Message Deduplication & Streaming Turn Preservation:**
  * Eliminated user prompt duplication in Web GUI caused by race condition between prompt dispatch fetch and immediate SSE `turn_start` events.
  * Normalized CRLF (`\r\n`) newlines to LF (`\n`) to ensure reliable text comparison on multi-line and pasted inputs.
  * Preserved ongoing assistant streaming turns when follow-up prompts are queued.
  * Added defensive consecutive message deduplication in mobile companion transcript views.
* [x] **Take the Wheel Zombie Shell Resilience & Subagent Lifecycle:**
  * Prevented `handleTakeWheel` from reusing stale/zombie tmux sessions containing dead bash shells; stale sessions are terminated and cleanly respawned with `resumeCmd`.
  * Fixed subagent completion tracking by propagating `ToolName` in headless `tool_result` SSE events and reconciling daemon in-memory `activeSubagents` with disk discoverers (`SubagentDiscoverer`).
  * Optimized headless CLI execution using `/dev/null` for Stdin, eliminating Claude Code's 3-second startup stdin wait delay.
  * Added OS-level liveliness verification in `HeadlessRunner.IsRunning` to prune dead processes and prevent prompt queue locks.
