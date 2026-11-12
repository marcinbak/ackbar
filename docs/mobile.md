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
│                         ┌───────────▼───────────┐                           │
│                         │   SessionNavigator    │                           │
│                         └───┬───────┬───────┬───┘                           │
│         ┌───────────────────┘       │       └───────────────────┐           │
│         ▼                           ▼                           ▼           │
│    ChatScreen                 TerminalScreen            TranscriptScreen    │
│  (headless chat)               (live tmux PTY)        (read-only search/TTS)│
│         │                           │                           │           │
│         │ HTTP REST & SSE           │ WebSocket (PTY)           │ HTTP REST │
│         ▼                           ▼                           ▼           │
└─────────┼───────────────────────────┼───────────────────────────┼───────────┘
          │                           │                           │            
          ▼                           ▼                           ▼            
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
* **Rich Session Cards (`FleetSessionCard`):** Displays agent avatar badges, git branch tags, host badges (`@local`, `@gpu-box`), dynamic context token gauge bars, active elapsed time, and status pills:
  * `⚙️` *(animated spinner)* **WORKING:** Active command execution or code generation.
  * `❓` **BLOCKED:** Waiting for developer response or permission.
  * `✅` **IDLE:** Turn completed, awaiting next prompt.
  * `⏹️` **ENDED:** Process completed or cleared into a new turn.
  * `🛑` **FAILED:** Process crashed or error.
  * `◌` **STANDBY:** Unmanaged or disconnected.
* **Native Mode Badges & Direct Launch:** Displays `CHAT` or `PTY` badge. Card tap opens native mode, clearing unread state.
* **Session Controls Sheet (`SessionDetailSheet`):** Compact `⋯` or long-press opens actions. Primary button matches mode (**`Open Chat`** / **`Open Terminal`**).
* **Unread State Indicator:** A glowing pulse indicator highlights `BLOCKED` or `IDLE` sessions. Card tap clears unread state via `POST /v1/sessions/mark-read`.
* **Live Search & Filter:** Real-time fuzzy filtering by session title, agent, or group.
* **New Agent Session Creation (`NewSessionSheet`):** Spawns agent sessions across hosts. Routes immediately into `ChatScreen` or `TerminalScreen` via `SessionNavigator.openSession`.

---

### 🔴 2. Attention Control Queue
The **Attention** tab provides a zero-latency inbox for agent sessions waiting on developer input or permissions:

* **Interactive Blocked ViewPager:** Swipe horizontally through all pending questions and permission prompts.
* **Mode-Aware Pending Actions:** Pending cards provide mode-matched actions (**`Open Chat`** or **`Live Terminal`**) plus secondary **`Transcript`**.
* **One-Tap Unblock:** Select suggested choices or speak/type a custom answer, then tap **`[⚡ Submit Choice]`** to immediately resume the session in real-time.
* **🔍 Fullscreen Mode (`⤢`):** Tap expand icon to enter edge-to-edge card view hiding top and bottom navigation bars.
* **In-Progress Stream:** Live monitoring cards for running agents. Tapping card routes directly via `SessionNavigator.openSession`.
* **Decision Audit Log:** Timestamped history of all approvals, answers, and decisions made on mobile.
* **OAuth Sign-In Cards (`HeadlessAuthCard`):** Renders for `BlockKind.auth` sessions with native HTTPS browser launcher, clipboard fallback, remote `callback_url` paste, retry, and dismiss.

---

### 💬 3. Native Chat Mode (ChatScreen)
Tap any headless session card or **Open Chat** to enter the dedicated interactive chat view ([`chat_screen.dart`](../mobile/lib/features/chat/presentation/chat_screen.dart)):

* **Interactive Chat Composer (`ChatComposerBar`):** Dedicated prompt composer ([`chat_composer_bar.dart`](../mobile/lib/features/chat/presentation/chat_composer_bar.dart)) supporting multiline input, action buttons, prompt skills passthrough, and send trigger.
* **Header Actions:** Controls Antigravity Live Voice, Auto-Read TTS, Mic STT, Take-the-Wheel PTY promotion, and View Transcript.
* **Live SSE Streaming:** `HeadlessChatStreamController` streams deltas from `/v1/sessions/chat/stream`. Resumes turns via monotonic spool cursors with idempotent prompt retries.
* **Shared Message Bubbles (`TranscriptTurnBubble`):** Elevated user bubbles and agent cards with Markdown and copy actions ([`transcript_turn_bubble.dart`](../mobile/lib/features/transcript/presentation/transcript_turn_bubble.dart)).
* **Tool & Chain-of-Thought Badges (`TranscriptToolWidgets`):** Collapsible tool invocation cards and reasoning accordions ([`transcript_tool_widgets.dart`](../mobile/lib/features/transcript/presentation/transcript_tool_widgets.dart)).
* **Subagents Dock (`SubagentsDock`):** Collapsible child agent dock tracking delegated subagent status, progress, and turn counts ([`subagents_dock.dart`](../mobile/lib/features/transcript/presentation/subagents_dock.dart)).
* **Runtime Configuration:** Pill bar displays model, effort, auto/plan mode, directories, and context. Sheet manages models, directories, and MCP status.
* **Composer Autocomplete (`HeadlessAutocompleteStrip`):** Detects `/` for commands, `@` for files, and `!` for shell mode.
* **Command Dispatcher (`HeadlessCommandDispatcher`):** Runs `! <cmd>` and 20 standalone commands with inline cards.
* **In-Place Turn Rotation (`Conv N+1`):** `/clear`, `/reset`, `/rewind`, and `/handover` rotate sessions in-place using epoch guards while clearing ephemeral cards.
* **Inline OAuth Sign-In (`HeadlessAuthCard`):** Renders interactive cards for `BlockKind.auth` with native browser launcher, remote redirect paste, retry, and dismiss.

---

### 🖥️ 4. Live Interactive Terminal (TerminalScreen)
Tap any tmux session card or **Open Terminal** to attach to the live `tmux` terminal ([`terminal_screen.dart`](../mobile/lib/features/terminal/presentation/terminal_screen.dart)):

* **WebSocket PTY Stream:** Full-duplex interactive terminal powered by `flutter_pty` / xterm over `ws://<host>:7777/v1/sessions/pty`.
* **1-Tap View Transcript:** AppBar action button opens pure read-only `TranscriptScreen` with loop-safe `showOpenSessionAction: false`.
* **Dynamic Auto-Fit & Horizontal Scroll:** Automatically calculates terminal columns and rows on viewport resize, with horizontal scrolling and zoom controls (`-` / `+`).
* **Display Modes:** Toggle between responsive auto-fit mode and standard `80 COL` fixed layout.
* **Tmux Touch Scrollback & Floating Bottom Pill:** Vertical touch dragging dispatches structured scroll commands (`up`, `down`, `page_up`, `page_down`, `bottom`) over WebSocket with floating bottom pill.
* **Touch Accessory Bar:** Dedicated on-screen touch keys for `[Esc]`, `[Tab]`, `[Shift+Tab]`, `[Ctrl+C]`, `[↑]`, `[↓]`, `[PgUp]`, `[PgDn]`, and `[Enter]`.
* **PTY WebSocket Auto-Reconnect:** Executes 5 automatic reconnect attempts (`1s -> 2s -> 5s`) with forced resize synchronization and resume reconnection.

---

### 📜 5. Read-Only Structured Transcript (TranscriptScreen)
Tap **View Transcript** from Chat or Terminal, or tap **Transcript** in `SessionDetailSheet` to open the pure read-only viewer ([`transcript_screen.dart`](../mobile/lib/features/transcript/presentation/transcript_screen.dart)):

* **Pure Read-Only Layout:** Displays turn history without composer input bars, prompt pills, or mutable execution widgets.
* **Inline Search Filter & Counter:** Toggleable search bar (`transcript_search_toggle`) with real-time text query filtering (`transcript_search_input`) and dynamic match counter pill.
* **One-Tap Actions:** Copy full transcript Markdown to clipboard (`transcript_copy_button`) and trigger Auto-Read speech playback.
* **Loop-Safe Navigation:** Displays **Open Chat** / **Open Terminal** only when opened from list views; hidden from live session views.
* **Audio & Speech Playback:** Header Auto-Read toggle and per-turn speech bubble controls ([`AudioCompanionService`](../mobile/lib/core/services/audio_companion_service.dart)) with sanitized speech text.
* **Subagent Progress Inspection:** Embeds collapsible `SubagentsDock` to inspect parallel agent turns and states.

---

### 📝 6. Plans & Proposal Review
* **Diff Viewer:** Syntax-highlighted git diffs with additions in emerald and deletions in coral.
* **Verification Checklists:** Automated test summaries and step progress gauges.
* **Quick Approval:** Review changes and approve plans directly from your phone.

---

### 🌐 7. Multi-Host Mesh & Tailscale
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
*Executes all 298 unit and widget tests covering models, providers, `SessionNavigator`, `ChatScreen`, `TranscriptScreen`, streaming, and fleet cards.*

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
   * **1-Tap Creation:** The **`⚓+`** floating / header action immediately spawns an unbound supervisor session (`POST /v1/sessions/spawn` with `is_supervisor: true`, zero modal prompts) and routes directly into `ChatScreen` via `SessionNavigator.openSession`.

3. **Mobile Work Board & Hosted Deliverables (`work_board_screen.dart` & `task_card.dart`):**
   * **Work Board Screen:** Added `WorkBoardScreen` backed by `tasksProvider` (`GET /v1/tasks?federated=true`) with 4-pillar tabs (**`NEW`**, **`IN_PROGRESS`**, **`REVIEW`**, **`DONE`**) and group filter chips (`All`, `Modemobile`, `Personal`).
   * **Task Cards & Deliverables:** `TaskCard` renders task metadata, assignee badges, workstream PR links, and the **`[⚡ Squash & Merge PR]`** action (`POST /v1/tasks/merge-pr`). External deliverables (Claude Published Artifacts, Google Docs/Slides/Drive) launch in native viewers; inline Markdown deliverables open in `DeliverableMarkdownModal`.

4. **Two-Tier Voice & Audio Companion (`audio_companion_service.dart`, `chat_screen.dart` & `transcript_screen.dart`):**
   * **Two-Tier Pipeline:** `AudioCompanionService` integrates Tier 1 client-side Markdown speech sanitization (`speech_sanitizer.dart`) and Tier 2 remote Haiku summarization (`POST /v1/sessions/summarize-speech`) with client caching.
   * **UI Controls:** `ChatScreen` and `TranscriptScreen` feature header toggles (**`[🔊 Auto-Read]`**, **`[🎤 Mic]`**), per-message bubble actions (**`[🔊 Read]`**, **`[⏹️ Stop]`**, **`[↺ Read Again]`**), and the persistent **`SpeechCompanionBar`** with commuter quick-reply drill-down chips.

5. **Headless Session & Command Parity (`features/headless/`, `features/chat/` & `features/transcript/`):**
   * **`HeadlessAuthCard`:** Renders on `BlockKind.auth` in `AttentionScreen`, `ChatScreen`, and `TranscriptScreen`. Launches external browser via native `dev.ackbar.app/launcher` HTTPS channel (`MainActivity.kt`, `AppDelegate.swift`) with clipboard fallback. Supports collapsible remote `callback_url` paste, retry, terminal attach, and dismiss.
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

---

## 7. Implemented Phase 7: Mobile Native Session Views & Read-Only Transcript

Phase 7 establishes a 3-surface session model (interactive Chat, live Terminal, read-only Transcript) with central routing and `< 500 LOC` modular presentation components:

| Subsystem | File Reference | Architecture & Native Routing Behavior |
| :--- | :--- | :--- |
| **Session Navigator** | [`session_navigator.dart`](../mobile/lib/core/navigation/session_navigator.dart) | Central router (`47 LOC`). Routes headless sessions to `ChatScreen` and tmux to `TerminalScreen`. Clears unread session badges. Provides loop-safe `openTranscript`. |
| **Interactive Chat Screen** | [`chat_screen.dart`](../mobile/lib/features/chat/presentation/chat_screen.dart) & [`chat_composer_bar.dart`](../mobile/lib/features/chat/presentation/chat_composer_bar.dart) | Dedicated headless chat (`474 LOC` + `249 LOC`). Houses live SSE stream controller, Take-the-Wheel PTY promotion, 1-tap `View Transcript`, and command cards. |
| **Read-Only Transcript Screen** | [`transcript_screen.dart`](../mobile/lib/features/transcript/presentation/transcript_screen.dart) | Pure read-only transcript viewer (`465 LOC`). Features inline search filter, match counter, Copy Markdown, Auto-Read TTS, and loop-safe session button. |
| **Fleet Session Card & Detail** | [`fleet_session_card.dart`](../mobile/lib/features/fleet/presentation/fleet_session_card.dart) & [`session_detail_sheet.dart`](../mobile/lib/features/fleet/presentation/session_detail_sheet.dart) | `FleetSessionCard` (`277 LOC`) renders `CHAT`/`PTY` badges. `SessionDetailSheet` (`464 LOC`) provides mode-matched actions (**`Open Chat`** / **`Open Terminal`**) and **`Transcript`**. |
| **Attention Queue Routing** | [`attention_screen.dart`](../mobile/lib/features/attention/presentation/attention_screen.dart) | Mode-aware Pending buttons (**`Open Chat`** / **`Live Terminal`** + **`Transcript`**). In-Progress card tap routes directly via `SessionNavigator.openSession`. |
| **Shared Turn & Tool Modules** | [`transcript_turn_bubble.dart`](../mobile/lib/features/transcript/presentation/transcript_turn_bubble.dart) & [`transcript_tool_widgets.dart`](../mobile/lib/features/transcript/presentation/transcript_tool_widgets.dart) | Modular presentation components (`352 LOC` + `364 LOC`). Renders Markdown bubbles, tool invocations, and reasoning accordions across Chat and Transcript. |
| **Subagents Dock & Host Resolver** | [`subagents_dock.dart`](../mobile/lib/features/transcript/presentation/subagents_dock.dart) & [`session_host_resolver.dart`](../mobile/lib/core/utils/session_host_resolver.dart) | Collapsible subagents dock (`240 LOC`) and host resolver (`89 LOC`). All touched presentation files strictly `< 500 LOC`. |
