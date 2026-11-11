# Ackbar Mobile Client (`mobile/`)

> **Cross-machine agentic companion app for iOS and Android built with Flutter & Riverpod.**

---

## 1. Overview & Architecture

The **Ackbar Mobile App** is a native cross-platform companion client designed to monitor, supervise, and unblock autonomous AI coding sessions (Claude Code, Antigravity, Codex) on the go.

It communicates with local and remote `ackbard` daemons over standard HTTP/REST, Server-Sent Events (`SSE`), and WebSockets (`ws://`), with native support for **Tailscale** mesh networking.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           ACKBAR MOBILE (Flutter)                           │
│                                                                             │
│  ┌──────────────┐  ┌──────────────────┐  ┌──────────────┐  ┌─────────────┐  │
│  │  Fleet View  │  │ Attention Queue  │  │  Plans View  │  │  Hosts View │  │
│  └──────┬───────┘  └────────┬─────────┘  └──────┬───────┘  └──────┬──────┘  │
│         │                   │                   │                 │         │
│  ┌──────▼───────────────────▼───────────────────▼─────────────────▼──────┐  │
│  │                    Riverpod State Management Layer                    │  │
│  │      (fleetSessionsProvider, attentionQueueProvider, hostsProvider)   │  │
│  └──────────────────────────────────┬────────────────────────────────────┘  │
│                                     │                                       │
│          ┌──────────────────────────┴──────────────────────────┐            │
│          │ HTTP REST & SSE (/v1/events)                        │ WebSocket │
│          ▼                                                     ▼ PTY       │
└──────────┼─────────────────────────────────────────────────────┼────────────┘
           │                                                     │
           ▼                                                     ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        ACKBARD DAEMON CONTROL PLANE                         │
│                    (Local Workstation / Remote Devbox)                      │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Key Technical Characteristics
* **UI Framework:** Flutter 3.x with Riverpod 2.5 state management.
* **Decoupled Theme System:** Based on Google Stitch design tokens (*Terminal Velocity* dark theme: `#0E1015` background, `#161922` surface, `#10B981` emerald, `#06B6D4` cyan, `#EF4444` coral, `#F59E0B` amber).
* **Zero Cloud Dependencies:** Communicates directly with your self-hosted `ackbard` daemons over LAN or private Tailscale VPN.

---

## 2. Core Tabs & Capabilities

### 🚀 1. Fleet Dashboard
* **Collapsible Project Folders:** Logical organization of sessions by repository and category.
* **Rich Session Cards:** Displays agent avatar badges, git branch tags, host badges (`@local`, `@gpu-box`), dynamic context token gauge bars, active elapsed time, and status pills:
  * `⚙️` *(animated spinner)* **WORKING:** Active command execution or code generation.
  * `❓` **BLOCKED:** Waiting for developer response or permission.
  * `✅` **IDLE:** Turn completed, awaiting next prompt.
  * `⏹️` **ENDED:** Process completed or cleared into a new turn.
  * `🛑` **FAILED:** Process crashed or error.
  * `◌` **STANDBY:** Unmanaged or disconnected.
* **Unread State Indicator:** A glowing pulse indicator highlights sessions that transitioned into `BLOCKED` or `IDLE`. Tapping the card automatically clears the unread state via `POST /v1/sessions/mark-read`.
* **Live Search & Filter:** Real-time fuzzy filtering by session title, agent, or group.
* **Session Details & Controls Sheet:** Tap any session to inspect metadata, **▶ Resume Conversation**, restart, kill, archive, or delete.
* **New Agent Session Creation:** Spawn new sessions across supervised hosts directly from mobile via Floating Action Button (`+ NEW SESSION`), top AppBar action, per-folder quick shortcuts (`+`), or empty-state CTA. Supports Claude Code, Google Antigravity, and OpenAI Codex in either live `tmux` terminal or `headless` chat mode, with autocomplete chips for known project directories, case-insensitive host alias resolution, inline modal error feedback, and automatic navigation into `TranscriptScreen` (`headless`) or `TerminalScreen` (`tmux`) upon launch.

---

### 🔴 2. Attention Control Queue
The **Attention** tab provides a zero-latency inbox for agent sessions waiting on developer input or permissions:

* **Interactive Blocked ViewPager:** Swipe horizontally through all pending questions and permission prompts.
* **One-Tap Unblock:** Select suggested choices or speak/type a custom answer, then tap **`[⚡ Submit Choice]`** to immediately resume the session in real-time.
* **🔍 Fullscreen Mode (`⤢`):** Tap the expand icon on any pending card to enter an edge-to-edge fullscreen card view with the top app bar and bottom navigation hidden, giving maximum vertical real estate to long prompts, choices, and rationale.
* **In-Progress Stream:** Live monitoring cards for agents actively executing commands.
* **Decision Audit Log:** Timestamped history of all approvals, answers, and decisions made on mobile.
* **OAuth Sign-In Cards (`HeadlessAuthCard`):** Renders for `BlockKind.auth` sessions with native HTTPS browser launcher, clipboard fallback, remote `callback_url` paste, retry, and dismiss.

---

### 💬 3. Chat-Style Conversation Transcripts
Tap **Transcript** on any session to open a dedicated agentic chat stream:

* **Right-Aligned User Bubbles:** Elevated slate/indigo bubbles (`#1E2638`) with `👤 You` header and timestamps.
* **Left-Aligned Agent Cards:** High-width terminal surface cards (`#161922`) with full GitHub Flavored Markdown, syntax highlighting, and copy buttons.
* **⚡ Collapsible Tool Invocations:** Visual badges showing executed tools (`🛠️ Bash`, `🛠️ Read`, `🛠️ Edit`) without cluttering the chat.
* **💭 Thought Process Accordions:** Expandable chain-of-thought dropdowns for reasoning analysis.
* **Live SSE Streaming & Spool Catch-Up:** `HeadlessChatStreamController` connects to `/v1/sessions/chat/stream` for real-time text, thinking, and tool deltas. Resumes cleanly across reconnections via `turn_id` and `after_seq` cursors with a 40s read watchdog and idempotent `client_turn_id` prompt retries.
* **Resilient Host & Transcript Resolution:** Preserves existing session and transcript state across transient network/timeout failures, avoids SSE connection churn during background host health polls, forwards per-host `authToken` across all transcript/subagent/prompt endpoints, and surfaces retryable error banners when transcript endpoints are unreachable.
* **Quick Navigation:** Floating **`[↓ Latest]`** button to glide directly to the latest turn, plus one-tap jump to the interactive terminal.
* **Headless Runtime Configuration (`HeadlessConfigPillBar` & `HeadlessConfigSheet`):** Header pill bar displays active model, effort, execution mode (`AUTO`/`PLAN`), directories, and `Context %`. Tapping opens a sheet with model suggestions, effort picker (hidden on `antigravity`), directory manager, and live `HeadlessMcpSection` status.
* **Composer Autocomplete & Shell Mode (`HeadlessAutocompleteStrip`):** Detects leading `/` for command suggestions, `@` for file suggestions, and `!` for shell mode.
* **Inline Slash & Shell Dispatcher (`HeadlessCommandDispatcher`):** Intercepts `! <cmd>` and 20 standalone commands. Emits inline dark `HeadlessCommandCardView` cards with `[+ Attach to Prompt]`.
* **In-Place Turn Rotation (`Conv N+1`):** `/clear`, `/reset`, `/rewind`, and `/handover` rotate sessions in-place using epoch guards while clearing ephemeral cards.
* **Inline OAuth Sign-In (`HeadlessAuthCard`):** Renders interactive cards for `BlockKind.auth` with native browser launcher, remote redirect paste, retry, and dismiss.

---

### 🖥️ 4. Live Interactive Terminal (PTY / xterm)
Tap **Live Terminal** on any session to attach to the live `tmux` terminal:

* **WebSocket PTY Stream:** Full-duplex interactive terminal powered by `flutter_pty` / xterm over `ws://<host>:7777/v1/sessions/pty`.
* **Dynamic Auto-Fit & Horizontal Scroll:** Automatically calculates terminal columns and rows on viewport resize, with horizontal scrolling and zoom controls (`-` / `+`).
* **Display Modes:** Toggle between responsive auto-fit mode and standard `80 COL` fixed layout.
* **Tmux Touch Scrollback & Floating Bottom Pill:** Vertical touch dragging and fling gestures on the terminal canvas dispatch structured `scroll` control messages (`up`, `down`, `page_up`, `page_down`, `bottom`) over the PTY WebSocket so `tmux` copy-mode scrolls smoothly even while `tmux` is in the alternate screen buffer (`smcup`), displaying a floating **`[↓ Bottom]`** pill while scrolled up in history.
* **Touch Accessory Bar:** Dedicated on-screen touch keys for `[Esc]`, `[Tab]`, `[Shift+Tab]`, `[Ctrl+C]`, `[↑]`, `[↓]`, `[PgUp]`, `[PgDn]`, and `[Enter]`.
* **PTY WebSocket Auto-Reconnect:** `TerminalReconnectController` executes 5 automatic reconnect attempts (`1s -> 2s -> 5s`) with forced resize synchronization, `[Tap to Reconnect]` manual banner fallback, and reconnection on `AppLifecycleState.resumed`.

---

### 📝 5. Plans & Proposal Review
* **Diff Viewer:** Syntax-highlighted git diffs with additions in emerald and deletions in coral.
* **Verification Checklists:** Automated test summaries and step progress gauges.
* **Quick Approval:** Review changes and approve plans directly from your phone.

---

### 🌐 6. Multi-Host Mesh & Tailscale
* **Bottom-Bar Fleet Health Badge:** The `Hosts` bottom navigation tab displays a round count badge showing the total number of configured hosts, dynamically colored by fleet connectivity: emerald green when all hosts are online, amber orange when some hosts are offline, and coral red when all hosts are offline.
* **App Version Header:** The top `DAEMON HOSTS` toolbar on the `Hosts` tab displays the mobile app version pill (`AppVersion.displayVersion`, synced with `VERSION`) alongside per-node daemon versions, keeping the `Fleet`, `Attention`, and `Plans` toolbars uncluttered.
* **Tailnet Status Banner:** Live connection indicator for your Tailscale network.
* **Host Management:** Register multiple Mac, Linux, and cloud servers.
* **Telemetry & Health:** Real-time latency tracking (ms), uptime, active session counts, and daemon version checks.

---

## 3. Building & Deployment

### Prerequisites
* **Flutter SDK:** 3.19 or newer (`flutter doctor`).
* **Xcode:** (for iOS deployment) with an active Development Team selected in `ios/Runner.xcodeproj`.
* **Android Studio / SDK:** (for Android deployment).

### Running in Local Dev Mode
```bash
cd mobile
flutter pub get
flutter run
```

### Running Automated Test Suite
```bash
cd mobile
flutter test
```
*Executes all 280 unit and widget tests covering models, Riverpod providers, SSE lifecycle, live chat streaming, terminal reconnect, lifecycle sync, optimistic rollback, session spawning, node path resolution, hierarchical fleet tree, work board, and headless cards.*

### Installing to iOS Device
```bash
cd mobile
flutter run --release -d <device-id>
```
*Or build and deploy via Xcode / `devicectl`:*
```bash
flutter build ipa --release
xcrun devicectl device install app --device <device-uuid> build/ios/archive/Runner.xcarchive/Products/Applications/Runner.app
```

### Installing to Android Device
```bash
cd mobile
flutter run --release -d <android-device-id>
```

---

## 4. Design Assets & Iconography

* **App Display Name:** `Ackbar`
* **App Icon:** Master 1024x1024 vector cybernetic command anchor (`⚓`) with glowing electric cyan (`#06B6D4`) and neon emerald (`#10B981`) telemetry radar graphics on an obsidian titanium background (`#0E1015`).
* **Asset Catalog:** Located in `mobile/ios/Runner/Assets.xcassets/AppIcon.appiconset/` and `mobile/android/app/src/main/res/mipmap-*/`.

---

## 5. Implemented Phase 5: Ackbar 2.0 Mobile App Alignment

In **Phase 5**, the Flutter Mobile App (`mobile/lib/`) was upgraded to complete functional parity with the Web GUI Supervisor & Task Control Plane (see [`docs/work-overview/06-ui-and-navigation.md`](work-overview/06-ui-and-navigation.md#4-mobile-app-alignment-mobile--phase-5--implemented)):

1. **Hierarchical Session Tree Parity (`fleet_providers.dart`, `node_path_resolver.dart` & `fleet_tree_view.dart`):**
   * **Node Path Resolution:** Ported `resolveSessionNodePath(session, nodes)` from `web/js/state.js` to Dart (`mobile/lib/core/utils/node_path_resolver.dart`), resolving sessions against federated `TreeNode` trees from `fleetNodesProvider` (`GET /v1/nodes?federated=true`).
   * **Multi-Level Tree View:** Replaced the legacy flat project map with `fleet_tree_view.dart`, rendering recursive collapsible folder trees in `fleet_screen.dart` with standardized project subsections: **`⚓ Supervisors`**, **`📋 Tasks`**, **Manual Sessions**, **`⏳ Later`**, and **`✅ Done`**, complete with `[offline]` badges for sessions on asleep hosts.

2. **Zero-Input `⚓ Supervisors` Filter & 1-Tap Spawning (`fleet_screen.dart`):**
   * **Segmented Filter:** The **`[🌐 All Tree | ⚓ Supervisors]`** toggle in `fleet_screen.dart` switches between the full tree and all active supervisor sessions across hosts.
   * **1-Tap Creation:** The **`⚓+`** floating / header action immediately spawns an unbound supervisor session (`POST /v1/sessions/spawn` with `is_supervisor: true`, zero modal prompts) and routes directly into `TranscriptScreen`.

3. **Mobile Work Board & Hosted Deliverables (`work_board_screen.dart` & `task_card.dart`):**
   * **Work Board Screen:** Added `WorkBoardScreen` backed by `tasksProvider` (`GET /v1/tasks?federated=true`) with 4-pillar tabs (**`NEW`**, **`IN_PROGRESS`**, **`REVIEW`**, **`DONE`**) and group filter chips (`All`, `Modemobile`, `Personal`).
   * **Task Cards & Deliverables:** `TaskCard` renders task metadata, assignee badges, workstream PR links, and the **`[⚡ Squash & Merge PR]`** action (`POST /v1/tasks/merge-pr`). External deliverables (Claude Published Artifacts, Google Docs/Slides/Drive) launch in native viewers; inline Markdown deliverables open in `DeliverableMarkdownModal`.

4. **Two-Tier Voice & Audio Companion (`audio_companion_service.dart` & `transcript_screen.dart`):**
   * **Two-Tier Pipeline:** `AudioCompanionService` integrates Tier 1 client-side Markdown speech sanitization (`speech_sanitizer.dart`) and Tier 2 remote Haiku summarization (`POST /v1/sessions/summarize-speech`) with client caching.
   * **UI Controls:** `transcript_screen.dart` features header toggles (**`[🔊 Auto-Read]`**, **`[🎤 Mic]`**), per-message bubble actions (**`[🔊 Read]`**, **`[⏹️ Stop]`**, **`[↺ Read Again]`**), and the persistent **`SpeechCompanionBar`** with commuter quick-reply drill-down chips.

5. **Headless Session & Command Parity (`features/headless/` & `features/transcript/`):**
   * **`HeadlessAuthCard`:** Renders on `BlockKind.auth` in `AttentionScreen` and `TranscriptScreen`. Launches external browser via native `dev.ackbar.app/launcher` HTTPS channel (`MainActivity.kt`, `AppDelegate.swift`) with clipboard fallback. Supports collapsible remote `callback_url` paste, retry, terminal attach, and dismiss.
   * **`HeadlessConfigPillBar` & `HeadlessConfigSheet`:** Displays model, effort (hidden for Antigravity), `AUTO`/`PLAN` mode, `extra_dirs`, and `Context %`. Embeds `HeadlessMcpSection` with live MCP/plugin pills and 1-tap sign-in.
   * **Composer Autocomplete & Command Dispatcher:** `HeadlessAutocompleteStrip` handles `/` commands, `@` files, and `!` shell indicator. `HeadlessCommandDispatcher` executes `! <cmd>` and 20 standalone slash commands, passing prompt skills through. Renders inline dark `HeadlessCommandCardView` cards (`+ Attach to Prompt`, inspect, `/rewind`) and handles epoch-guarded `Conv N+1` rotation.

---

## 6. Implemented Phase 6: Mobile Stream & Connection Reliability

Phase 6 implements persistent connection reliability, socket lifecycle management, and optimistic mutation rollback:

| Subsystem | File Reference | Architecture & Reliability Behavior |
| :--- | :--- | :--- |
| **Fleet SSE Client** | [`sse_client.dart`](../mobile/lib/core/network/sse_client.dart) | Explicit per-attempt `http.Client` socket teardown on cancellation. 45s keepalive inactivity watchdog, frame-guarded jittered backoff (`1s -> 2s -> 5s -> 10s`), and `onHostReconnected` session refresh callback. |
| **Headless Chat Streaming** | [`chat_stream_client.dart`](../mobile/lib/core/network/chat_stream_client.dart) & [`headless_chat_stream_controller.dart`](../mobile/lib/features/transcript/application/headless_chat_stream_controller.dart) | Connects to `/v1/sessions/chat/stream` for live SSE deltas (`text_delta`, `thought_delta`, `tool_start`). Supports `turn_id` + `after_seq` spool catch-up, 40s read watchdog, `in_flight_turn` hydration, and idempotent `client_turn_id` prompt retries. |
| **Terminal Reconnect** | [`terminal_reconnect_controller.dart`](../mobile/lib/features/terminal/application/terminal_reconnect_controller.dart) & [`terminal_screen.dart`](../mobile/lib/features/terminal/presentation/terminal_screen.dart) | 5-attempt PTY WebSocket auto-reconnect (`1s -> 2s -> 5s`) with forced resize synchronization, scrollback preservation, `[Tap to Reconnect]` manual banner, and reconnection on `AppLifecycleState.resumed`. |
| **Lifecycle Foreground Sync** | [`app_lifecycle_sync_observer.dart`](../mobile/lib/core/services/app_lifecycle_sync_observer.dart) | Debounced (`>= 2s`) foreground sync (`refreshHosts`, `refreshSessions`, `refreshNodes`) on `AppLifecycleState.resumed`. Runs a 30s background health probe when any host is offline. |
| **Optimistic Rollback** | [`fleet_providers.dart`](../mobile/lib/core/providers/fleet_providers.dart) | Concurrent-SSE-safe optimistic rollback returning `Future<bool>` across session mutation actions (`respondToSession`, `terminateSession`, `deleteSession`, `archiveSession`, `resumeSession`, `restartSession`, `handoverSession`). Surfaces error feedback on failure without false positive toasts. |
