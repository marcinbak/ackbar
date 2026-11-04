# Project Ackbar Backlog & Roadmap

> **Tracking Doc:** Master inventory of active feature initiatives, architectural explorations, and completed milestones for Project Ackbar.

---

## 🎯 Active Backlog Items

### 1. 🔔 Real-Time Mobile Push Notifications & Deep-Linking
* **Status:** Ready for Implementation
* **Detailed Spec:** [`docs/backlog-push-notifications.md`](backlog-push-notifications.md)
* **Scope:**
  * **Daemon Event Dispatcher (`ackbard`):** Trigger webhook / APNs dispatch when sessions enter `StateBlocked` (clarifying questions, tool permission prompts, or plan reviews).
  * **Zero-Cost Relay (`ntfy.sh`):** Use open-source `ntfy.sh` relay for native iOS/Android lock-screen delivery without requiring a paid $99/yr Apple Developer entitlement.
  * **Lock-Screen Action Endpoints:** Interactive buttons (`[⚡ Allow (y)]`, `[🛑 Deny (n)]`, `[📱 Open in Ackbar]`) to unblock agents with 1 tap.
  * **Custom URL Deep-Linking:** Configure `ackbar://attention?session_id=<uuid>` to open directly into Fullscreen Attention Mode.

---

### 2. 🎙️ Unified Supervisor Control Layer, Task Orchestrator & Voice Companion (Ackbar 2.0)
* **Status:** Architecture Designed (Grilled & Locked)
* **Detailed Specs:** [`docs/voice-companion.md`](voice-companion.md) & [`docs/work-overview/`](work-overview/README.md)
* **Scope:**
  * **Uniform 3-Way LWW Peer Sync (`macbook` $\leftrightarrow$ `macmini` $\leftrightarrow$ `legion`) & Dynamic `node_host_paths`:** Replicate `tree_nodes`, `node_host_paths`, session metadata (`node_path`, `custom_title`, `is_later`, `is_supervisor`), `tasks`, `task_workstreams`, and `task_deliverables` across all hosts with startup/wake delta pull and `[offline]` badges for asleep hosts.
  * **Zero-Input `⚓ Supervisor` Dispatchers & Dedicated `📋 Task` Sessions:** 1-tap `[+ ⚓ New Supervisor]` opens a blank session with zero upfront inputs, auto-binds to the project via `bind_supervisor_session` on the first turn, and dispatches dedicated **`📋 Task`** sessions for Product/Design (`kind="design"`) or Repo Code (`kind="code"`).
  * **Native Hosted Artifacts (Claude Published Artifacts + Google Drive/Docs/Slides):** Attach hosted deliverables (`url`) + synced `content_md` via `attach_deliverable` using Claude Published Artifacts (for Claude Code) and Google Workspace (`gdrive`, `gdocs`, `gslides`, `gsheets` under `Ackbar/projects/<project>/tasks/<task>/` for Antigravity).
  * **`dev-workflow` State Mapping, N15 Auto-Close & N14 Sequential Handoff:** Map `dev-workflow` nodes (`N1`/`N2`/`N11`/`N15`) when sessions enter `StateIdle`, auto-close Tmux at `N15`, and unlock sequential dependent repo workstreams (`ngl-ios` `@macmini` $\rightarrow$ `ngl-android` `@legion`) upon `N14` merge.
  * **Decoupled Voice/Audio Companion & 2-Tier Lazy Speech Pipeline:** Hands-free mode (`🎤 Mic ON` + `🔊 Auto-Read ON`), silent commuter mode (`🎤 Mic OFF` + `🔊 Auto-Read ON` + 1-tap quick chips), and desk mode (`Auto-Read OFF` with ad-hoc `[🔊 Read]` / `[↺ Read Again]`), powered by a 0ms client Markdown speech sanitizer and on-demand `POST /v1/sessions/summarize-speech` via stateless headless Haiku (`claude -p --model haiku`).
  * **Host Capabilities, Weighted Load Balancing (`75/25`) & Fleet Utilization Telemetry:**
    * **Hybrid Capability Discovery:** Auto-detect installed/authenticated AI agents (`agent:claude-code`, `agent:antigravity`, `agent:codex`), MCP plugins (`mcp:gdrive`, etc.), and UI test runtimes (`ios`, `android`, `chrome`, `docker`, `gpu`) alongside manual policy tags (`24/7`, `priority`, `drain`) and `routing_weight` (`legion=75`, `macmini=25`, `macbook=0`).
    * **Piggybacked `/v1/version` Heartbeat & Deterministic `"auto"` Scheduler:** Exchange live active working session counts (`StateWorking` + queued tasks) and OS resource headroom (CPU %, free RAM GB, free disk) over the 5–10s health heartbeat; match explicit `required_capabilities` from the Supervisor and route non-iOS tasks via capacity-adjusted 75/25 deficit scoring (spawning immediately with a `load_warning` if overloaded, and never auto-routing background work to non-`24/7` laptops unless all `24/7` hosts are offline).
    * **Multi-Week Utilization Rollups (`host_load_metrics`) & Heatmaps:** Record 5-minute / 1-hour SQLite time-series buckets (retained 90 days) and render 24-hour Hour-of-Day utilization heatmaps and 7d/30d split trends in Web and Mobile.

---

### 3. 📱 Mobile Deployment & Distribution Pipeline
* **Status:** Planned
* **Scope:**
  * **Automated Signing & Builds:** Fastlane / GitHub Actions pipeline for iOS IPA (TestFlight / ad-hoc) and Android APK / AAB.
  * **Background SSE Keep-Alive:** Enhanced reconnection resilience when the mobile client is minimized during active streaming sessions.

---

### 4. 🔍 Session Management & Workspace Enhancements
* **Status:** Planned
* **Scope:**
  * **Advanced Fleet Search & Tagging:** Multi-factor search across hosts, token usage thresholds (e.g. `>80% context window`), and custom user labels.
  * **Transcript & Artifact Export:** One-click export of chat transcripts, tool calls, and plan diffs to Markdown and JSON formats.

---

## ✅ Completed Milestones

* [x] **Release Automation & Homebrew Tap:**
  * GoReleaser v2 configuration (`.goreleaser.yaml`) cross-compiling for macOS (`arm64`, `amd64`) and Linux (`amd64`, `arm64`).
  * Automated Homebrew tap formula generation (`marcinbak/homebrew-ackbar`) with `brew services` daemon management.
  * GitHub Actions CI/CD workflows (`.github/workflows/release.yml`, `.github/workflows/ci.yml`).
* [x] **Unread vs. Read Lifecycle Dimension & Animated State Indicators:**
  * `is_unread` and `last_state_change_at` schema tracking with glowing pulse cues on newly blocked or idle sessions.
  * Automatic read acknowledgment (`POST /v1/sessions/mark-read`) when focusing sessions in Web, TUI, or Mobile.
  * Real-time animated working state spinners (`⚙️` rotating in Web & Mobile, cycling quadrant `◐ ◓ ◑ ◒` in Bubble Tea TUI).
* [x] **In-Place Cleared Conversation Turn Rotation & 1-Click Resume:**
  * Context resets (`/clear`, `/reset`) seamlessly archive prior turns as `(Conv 1)` / `⏹️ ENDED` and assign the live tmux window to `(Conv 2)`.
  * Universal 1-click conversation resumption (`POST /v1/sessions/control?action=resume&id=...`) launching native provider resume CLIs across Web (`▶ Resume in Tmux`), TUI (`r`), and Mobile (`▶ Resume Conversation`).
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
  * Root `VERSION` file embedded via `//go:embed` across all binaries; duplicate `internal/web/` directory removed.
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


