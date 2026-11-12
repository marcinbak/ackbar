# Completed Tasks & Milestones Archive

> **Archive Reference:** Historical log of completed milestones, architecture audit remediations, and phased feature roadmaps retired from active documentation across Project Ackbar.

---

## 1. Core Backlog Milestones

Source: [docs/backlog.md](../backlog.md)

* **Mobile Native Session Views & Read-Only Transcript (`feat/mobile-native-session-views`):**
  * Central `SessionNavigator` routes headless sessions to `ChatScreen` and tmux sessions to `TerminalScreen`, clearing unread badges.
  * `FleetSessionCard` renders `CHAT`/`PTY` badges, direct native tap, and `⋯` / long-press `SessionDetailSheet` with mode-matched actions.
  * Attention pending cards feature mode-aware actions (`Open Chat` / `Live Terminal` + `Transcript`); in-progress cards route natively.
  * Dedicated interactive `ChatScreen` with `ChatComposerBar`, live stream controller, Take-the-Wheel promotion, and `View Transcript` action.
  * Pure read-only `TranscriptScreen` with inline search filter, match counter, Copy Markdown, and per-turn speech playback.
  * Modular presentation extraction maintains all touched mobile `.dart` files strictly under 500 lines.
* **Mobile Stream & Connection Reliability (`feat/mobile-stream-reliability`):**
  * Daemon `/v1/events` keepalive heartbeats (`: keepalive\n\n`, default `20s`) with unconditional initial response header flush.
  * Daemon `POST /v1/sessions/prompt` idempotency contract via optional `client_turn_id` (10m TTL, 512-entry bounded LRU cache).
  * Mobile `SSEClient` socket lifecycle teardown, 45s inactivity watchdog, frame-guarded jittered backoff (`1s -> 2s -> 5s -> 10s`), and auto-reconnect session refresh.
  * Headless live SSE streaming (`ChatStreamClient`, `HeadlessChatStreamController`) with `turn_id` + `after_seq` spool catch-up, 40s read watchdog, and idempotent prompt retries.
  * PTY WebSocket auto-reconnect (`TerminalReconnectController`) with 5-attempt backoff, forced resize sync, and resume recovery.
  * App lifecycle foreground sync (`AppLifecycleSyncObserver`) with 2s debounced refresh and 30s offline-host health probe.
  * Concurrent-SSE-safe optimistic rollback across all session mutation actions in `FleetSessionsNotifier`.
* **Mobile Headless Parity (`feat/mobile-headless-parity`):**
  * `HeadlessAuthCard` renders OAuth sign-in cards for `BlockKind.auth` in Attention and Transcript screens.
  * Native `dev.ackbar.app/launcher` HTTPS browser channel (`MainActivity.kt`, `AppDelegate.swift`) with clipboard fallback and remote `callback_url` paste.
  * `HeadlessConfigPillBar` and `HeadlessConfigSheet` for runtime model, effort, execution mode, `extra_dirs`, and `HeadlessMcpSection` health.
  * `HeadlessAutocompleteStrip` for `/`, `@`, and `!` shell indicator with prompt-skill passthrough.
  * `HeadlessCommandDispatcher` executes `! <cmd>` and 20 standalone commands with dark `HeadlessCommandCardView` cards and epoch-guarded `Conv N+1` rotation.
* **Headless Standalone Slash & Inline Shell Command Parity (`feat/headless-command-parity`):**
  * Executes inline shell commands (`! <cmd>`) via `POST /v1/sessions/{id}/shell` (`30s` timeout, `128 KB` cap, `-pgid` kill) with `.chat-bash-card` and `[+ Attach to Prompt]`.
  * Standalone `/` commands for runtime config (`/model`, `/effort`, `/permissions`, `/auto`, `/add-dir`) and SQLite `extra_dirs` persistence.
  * Headless context lifecycle parity (`/clear`, `/reset`, `/compact`, `/handover`) with in-place tab migration and provider-tailored compaction.
  * Inspection, diff, and rewind commands (`/mcp`, `/plugins`, `/agents`, `/doctor`, `/context`, `/cost`, `/diff`, `/rewind`) with allowlisted CLI execution and transcript rollback.
  * Modular daemon files (`headless_commands.go`, `headless_commands_inspect.go`, `headless_rewind.go`, `headless_handover.go`) and frontend modules (`web/js/chat-commands.js`, `web/js/chat-command-cards.js`).
  * Hardened tokenless local security with origin validation (`isTrustedLocalOrigin`) blocking cross-origin mutations.
* **Headless MCP & Plugin Authentication Broker (`feat/headless-mcp-auth`):**
  * Intercepts tool and turn OAuth failures (`BlockAuth`) for Google Workspace plugins, Antigravity, and Claude MCP servers.
  * Spawns allowlisted OAuth processes (`POST /v1/sessions/auth-tool`) with 5m TTL and 64KB bounded output buffers.
  * Relays callbacks (`POST /v1/sessions/auth-callback`) to loopback listener (`127.0.0.1:<port>`) or subprocess stdin without SSRF.
  * Inspects configured MCP and plugin health (`GET /v1/sessions/{id}/mcp-status`) using parallel cached binary probes.
  * Renders `.card-auth` interactive cards and `/config` popover status pills in Web UI (`chat-auth.js`).
* **Detached Headless Worker Spool, Replay & Warm Persistent Supervision:**
  * Detached `--headless-worker` sidecar (`Setsid: true`) with warm persistent multi-turn `stream-json` execution and Unix domain socket IPC.
  * Idle-TTL auto-reap (`ACKBAR_HEADLESS_IDLE_TTL`, default `20m`), config rotation, and transparent race respawn.
  * Monotonic NDJSON turn spooling (`turn_<id>.ndjson`), cursor-based SSE replay (`?after_seq`, `Last-Event-ID`), and transcript synthesis (`in_flight_turn`).
  * Startup re-attachment of live `running_turn` and `idle_warm` workers on daemon reboot without duplicate `--resume`.
  * Comprehensive runtime cleanup on manual `Done` and periodic background `auto_done` sweep.
  * Cross-OS idle-sleep inhibitor (`caffeinate` / `systemd-inhibit`) active strictly during running turns.
* **Unified Supervisor Control Layer, Task Orchestrator & Voice Companion (Ackbar 2.0):**
  * **4-Pillar Work Board & Multi-Repo Workstreams (`docs/work-overview/`):** Full `NEW` $\rightarrow$ `IN_PROGRESS` $\rightarrow$ `REVIEW` $\rightarrow$ `DONE` Kanban lifecycle with per-repository `task_workstreams`, single active worker enforcement per `(task_id, repo_node_path)`, canonical substatus normalization, and 1-click GitHub PR squash & merge (`POST /v1/tasks/merge-pr`).
  * **Zero-Input `⚓ Supervisor` Dispatchers & Dedicated `📋 Task` Sessions:** 1-tap `[+ ⚓ New Supervisor]` opens a session in `~/.ackbar/supervisor/`, auto-binds via `bind_supervisor_session`, and dispatches dedicated `📋 Task` sessions with direct CLI prompt arguments and automatic `IN_PROGRESS` activation.
  * **Monotonic `dev-workflow` State Sync & Deliverable Auto-Attachment (`workflow_watcher.go`, `telemetry_tasks.go`):** Maps `N0`–`N15` nodes across hook telemetry, `POST /v1/tasks`, and `~/.claude/dev-workflow-runs/*.json` (update-only for existing tasks) with monotonic status guards and automatic `.brief.md`, `.impl-notes.md`, and `.retro.md` deliverable ingestion.
  * **Host Capabilities, Weighted Load Balancing (`75/25`) & Fleet Utilization Telemetry (`host_capabilities.go`):** Auto-detects agents, MCP plugins, and UI test runtimes (`ios`, `android`, `chrome`, `docker`, `gpu`); infers platform capabilities (`["ios"]` / `["android"]`) from repo paths; routes `"auto"` spawns via capacity-adjusted 75/25 deficit scoring; and records 5-minute / 1-hour SQLite utilization rollups (`host_load_metrics`).
  * **Decoupled Voice/Audio Companion & 2-Tier Lazy Speech Pipeline (`docs/voice-companion.md`):** Hands-free and silent commuter modes with client Markdown sanitizer, `POST /v1/sessions/summarize-speech` (stateless Haiku + SQLite cache + structural AST fallback), and interactive quick-reply chips across Web and Mobile.
* **Real-Time Mobile Push Notifications & Deep-Linking (`ntfy.sh`):**
  * Built-in `ntfy.sh` dispatcher (`internal/daemon/ntfy.go`) triggering push notifications with `ackbar://` mobile deep-links and Ackbar icons when sessions enter `StateBlocked` or tasks enter `REVIEW` (`plan_review`, `pr_review`, `design_review`) or `blocked`.
  * Test notification endpoint (`POST /v1/notifications/test`) and 1-click fleet-wide `ntfy_topic` / `ntfy_server` propagation across connected hosts.
* **Release Automation & Homebrew Tap:**
  * GoReleaser v2 configuration (`.goreleaser.yaml`) cross-compiling for macOS (`arm64`, `amd64`) and Linux (`amd64`, `arm64`).
  * Automated Homebrew tap formula generation (`marcinbak/homebrew-ackbar`) with `brew services` daemon management.
  * GitHub Actions CI/CD workflows (`.github/workflows/release.yml`, `.github/workflows/ci.yml`) and enforced PR version bumping (`./scripts/bump-version.sh`).
* **Unread vs. Read Lifecycle Dimension & Animated State Indicators:**
  * `is_unread` and `last_state_change_at` schema tracking with glowing pulse cues on newly blocked or idle sessions.
  * Automatic read acknowledgment (`POST /v1/sessions/mark-read`) when focusing sessions in Web or Mobile.
  * Real-time animated working state spinners (`⚙️` rotating in Web & Mobile).
* **In-Place Cleared Conversation Turn Rotation & 1-Click Resume:**
  * Context resets (`/clear`, `/reset`) seamlessly archive prior turns as `(Conv 1)` / `⏹️ ENDED` and assign the live tmux window to `(Conv 2)`.
  * Universal 1-click conversation resumption (`POST /v1/sessions/control?action=resume&id=...`) launching native provider resume CLIs across Web (`▶ Resume in Tmux`) and Mobile (`▶ Resume Conversation`).
* **Web Multi-Tab Lifecycle Context Menu:**
  * Right-click tab context menu supporting Close Tab, Close Other Tabs, Close Tabs to the Right, and Close All Tabs.
* **Later Section & Session Parking:**
  * Added collapsible `⏳ Later` subsections to project groups and Unassigned folder with toggleable collapse memory and configurable default.
  * Parked sessions are immune to auto-done (`isSessionDone`) and auto-archive (`isSessionAutoArchived`) timers.
  * Direct session controls (`POST /v1/sessions/control?action=later` / `unlater`), drag-and-drop support, and right-click context menu options.
  * Sessions in Later remain parked during prompt interaction until explicitly moved by the user.
* **Official Provider Vector Logos:**
  * High-performance vector `CustomPainter` paths and inline SVGs for Claude Code (pixelated CLI creature), Google Antigravity (Gaussian arch curve with Google gradient), and OpenAI Codex (interlocking Blossom vortex).
* **Single Source of Truth Versioning:**
  * `internal/version/VERSION` embedded via `//go:embed` across all binaries and synchronized with `mobile/lib/core/constants/app_version.dart` via `./scripts/bump-version.sh`.
* **OSC 52 Clipboard & Auto-Copy Stream:**
  * Terminal clipboard synchronization from remote Linux/Legion tmux sessions to macOS host clipboard and Web UI.
* **Strict PR-Based Git Worktree Workflow & Open Source Guidelines:**
  * Branch protection enforced on `main`, `AGENTS.md` worktree workflow, `CONTRIBUTING.md`, and issue/PR templates.
* **Automated Session Handover & Context Rotation:**
  * 1-click context rotation (`POST /v1/sessions/handover`) with automated briefing synthesis, context clearing (`/clear`, `/reset`), and fresh turn reseeding.
  * Configurable suggestion toggle and token threshold percentage (`handover_suggestion_enabled`, `handover_threshold_pct`) via daemon settings API and Settings dialog.
  * Warning/danger context badges, quick-action handover button, confirmation modal, and right-click context menu options across Web UI and Mobile companion.
* **Chat Message Deduplication & Streaming Turn Preservation:**
  * Eliminated user prompt duplication in Web GUI caused by race condition between prompt dispatch fetch and immediate SSE `turn_start` events.
  * Normalized CRLF (`\r\n`) newlines to LF (`\n`) to ensure reliable text comparison on multi-line and pasted inputs.
  * Preserved ongoing assistant streaming turns when follow-up prompts are queued.
  * Added defensive consecutive message deduplication in mobile companion transcript views.
* **Take the Wheel Zombie Shell Resilience & Subagent Lifecycle:**
  * Prevented `handleTakeWheel` from reusing stale/zombie tmux sessions containing dead bash shells; stale sessions are terminated and cleanly respawned with `resumeCmd`.
  * Fixed subagent completion tracking by propagating `ToolName` in headless `tool_result` SSE events and reconciling daemon in-memory `activeSubagents` with disk discoverers (`SubagentDiscoverer`).
  * Optimized headless CLI execution using `/dev/null` for Stdin, eliminating Claude Code's 3-second startup stdin wait delay.
  * Added OS-level liveliness verification in `HeadlessRunner.IsRunning` to prune dead processes and prevent prompt queue locks.

---

## 2. Architecture Audit Remediations (2026-11)

Source: [docs/audit/](../audit/README.md)

* **Mobile `SSEClient.subscribeHost` Reconnect Leak & Keepalives (`#243`):** Fixed socket/subscription leak on cancel, added `/v1/events` `20s` `: keepalive`, `45s` client watchdog, and jittered backoff (`mobile/lib/core/network/sse_client.dart`, `internal/daemon/server.go`).
* **Mobile `transcript_screen.dart` 1,974-Line Hotspot & 3s Polling (`#243`, `#245`):** Replaced 3s full-transcript polling with live `/v1/sessions/chat/stream` (`turn_id` + `after_seq`) in `ChatScreen` / `HeadlessChatStreamController` and decomposed `transcript_screen.dart` to a `465`-line read-only viewer (`< 500 LOC` across all split widgets).
* **Mobile `attention_screen.dart` Dead Modal Removal (`#245`):** Deleted the unused 181-line modal dialog from `attention_screen.dart` and `session_detail_sheet.dart` and wired mode-matched `SessionNavigator` actions.
* **Dart Formatting Baseline & CI Gate (`#247`):** Formatted 17 legacy unformatted `.dart` files and added `dart format --output=none --set-exit-if-changed .` to `.github/workflows/ci.yml` (`Flutter Mobile Test Suite`).

---

## 3. Work & Fleet Overview 5-Phase Roadmap

Source: [docs/work-overview/07-open-questions-and-roadmap.md](../work-overview/07-open-questions-and-roadmap.md)

* **Phase 1: 3-Way LWW Peer Sync & Dynamic `node_host_paths`:**
  * Uniform LWW peer sync across `macbook`, `macmini`, and `legion` for `tree_nodes`, `node_host_paths`, `tasks`, `task_deliverables`, and session metadata (`node_path`, `custom_title`, `is_later`).
  * Startup/wake delta pull (`/v1/sync/state?since=<ts>`) and `[offline]` badges for asleep hosts.
* **Phase 2: Zero-Input `⚓ Supervisors` & `📋 Tasks` Tree Sections:**
  * Added `is_supervisor` flag, `[⚓ Supervisors]` sidebar switcher, and 1-tap `[+ ⚓ New Supervisor]` (zero upfront inputs).
  * Added `bind_supervisor_session` MCP tool to auto-bind project and title on first turn, plus dedicated `📋 Tasks` folder grouping.
* **Phase 3: Native Cloud Artifacts & `dev-workflow` Node Mapping:**
  * Supported Product/Design tasks (`kind="design"`, `guard.py` bypassed) and Code tasks (`kind="code"`, 1 Tmux session per repo workstream).
  * Connected `attach_deliverable` to Claude Published Artifacts and Antigravity Google Workspace (`gdrive`/`gdocs`/`gslides`/`gsheets`) with synced `content_md` for cross-host learnings.
  * Mapped `dev-workflow` nodes (`N1`/`N2`/`N11`/`N15`) on `StateIdle`, auto-closing Tmux at `N15`, and unlocking sequential repos on `N14`.
* **Phase 4: Voice Companion & 2-Tier Lazy Speech Pipeline:**
  * Implemented `POST /v1/sessions/summarize-speech` with `$0` stateless Haiku (`claude -p --model haiku`) and `(session_id, msg_idx)` cache.
  * Added Tier 1 client Markdown speech sanitizer, `[🔊 Auto-Read]`, `[🎤 Mic]`, per-message `[🔊 Read]` / `[↺ Read Again]` buttons, and interactive quick-reply drill-down chips.
  * Added `POST /v1/mcp` Streamable HTTP JSON-RPC endpoint and Claude Desktop supervisor config provisioning.
* **Phase 5: Mobile App Alignment (`mobile/` Flutter Client):**
  * Replaced flat `groupedSessionsProvider` (`projectDisplayName`) with hierarchical `tree_nodes` (`/v1/nodes?federated=true`) and Dart port of `resolveSessionNodePath()`.
  * Added `[🌐 All Tree | ⚓ Supervisors]` switcher, 1-tap `[+ ⚓ New Supervisor]` FAB, and `⚓ Supervisors` / `📋 Tasks` / `⏳ Later` / `✅ Done` folder subsections in `fleet_screen.dart`.
  * Added Mobile Work Board / Tasks view (`GET /v1/tasks?federated=true`) with 1-tap links to hosted deliverables and `[⚡ Squash & Merge PR]`.
  * Wired Voice Companion controls (`[🔊 Auto-Read]`, `[🎤 Mic]`, `[🔊 Read]`, `[↺ Read Again]`, quick chips) into `transcript_screen.dart`.

---

## 4. Voice Companion & Live Audio Bridge 4-Phase Roadmap

Source: [docs/voice-companion.md](../voice-companion.md)

* **Phase 1: Supervisor MCP & Multi-Host Transcript Proxy (`internal/mcp`, `internal/daemon`):**
  * Added `list_sessions`, `get_session_output`, `send_session_input`, `spawn_session`, and `control_session` to `internal/mcp/tools.go`.
  * Added remote host proxy fallback to `GET /v1/sessions/transcript` and `GET /v1/sessions?federated=true` in `internal/daemon/server.go`.
  * Added Claude Desktop (`claude_desktop_config.json`) to `internal/daemon/provisioner.go`.
* **Phase 2: On-Demand Speech Summarizer & Chat Audio Controls (`internal/daemon`, `web/`):**
  * Implemented `POST /v1/sessions/summarize-speech` on `ackbard` with `(session_id, message_index)` caching, headless Haiku integration, direct Cloud API support, and pure-Go structural fallback.
  * Added Tier 1 Markdown Speech Sanitizer, header toggles (`[🔊 Auto-Read]`, `[🎤 Mic]`), and per-message bubble buttons (`[🔊 Read]`, `[⏹️ Stop]`, `[↺ Read Again]`, `[📖 Read Full]`) to `web/js/chat.js`.
* **Phase 3: Remote HTTP MCP & Commuter Quick-Chips (`internal/daemon`, `web/`):**
  * Exposed Streamable HTTP MCP (`POST /v1/mcp`) on `ackbard` with JSON-RPC 2.0 and loopback listener resolution.
  * Rendered interactive `suggested_chips` in `web/js/chat.js` for 1-tap exploration of implementation plans.
* **Phase 4: Native Mobile TTS/STT & Antigravity `JETSKI-AUDIO/1` Live Voice Bridge (`internal/daemon`, `web/`, `mobile/`):**
  * **Part A (Antigravity CLI Native `/voice live` `JETSKI-AUDIO/1` Audio Bridge):** Implemented `MicServeBridge` in `internal/daemon/micserve_bridge.go` listening on `127.0.0.1:4713` (or fallback loopback port), automatically injecting `ANTIGRAVITY_MIC=<bridgeAddr>` into spawned/attached `antigravity` sessions. Exposed `GET /v1/sessions/voice-bridge` and `GET /v1/sessions/voice-bridge/status` on `ackbard` to stream 16 kHz mono `S16_LE` mic PCM from Web/Mobile to `agy` and 24 kHz mono `S16_LE` speaker PCM + instant barge-in buffer flushes from `agy` back to Web Audio API and Mobile.
  * **Part B (Mobile Native TTS/STT for Work Claude Code Supervisor & All Agents):** Implemented `PlatformSpeechEngine` over `MethodChannel('dev.ackbar.app/speech')` in `mobile/lib/core/services/audio_companion_service.dart` backed by native iOS (`AVSpeechSynthesizer` + `SFSpeechRecognizer`) and Android (`TextToSpeech` + `SpeechRecognizer`), with automatic `InMemorySpeechEngine` fallback in headless tests.
