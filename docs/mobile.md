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

---

### 🔴 2. Attention Control Queue
The **Attention** tab provides a zero-latency inbox for agent sessions waiting on developer input or permissions:

* **Interactive Blocked ViewPager:** Swipe horizontally through all pending questions and permission prompts.
* **One-Tap Unblock:** Select suggested choices or speak/type a custom answer, then tap **`[⚡ Submit Choice]`** to immediately resume the session in real-time.
* **🔍 Fullscreen Mode (`⤢`):** Tap the expand icon on any pending card to enter an edge-to-edge fullscreen card view with the top app bar and bottom navigation hidden, giving maximum vertical real estate to long prompts, choices, and rationale.
* **In-Progress Stream:** Live monitoring cards for agents actively executing commands.
* **Decision Audit Log:** Timestamped history of all approvals, answers, and decisions made on mobile.

---

### 💬 3. Chat-Style Conversation Transcripts
Tap **Transcript** on any session to open a dedicated agentic chat stream:

* **Right-Aligned User Bubbles:** Elevated slate/indigo bubbles (`#1E2638`) with `👤 You` header and timestamps.
* **Left-Aligned Agent Cards:** High-width terminal surface cards (`#161922`) with full GitHub Flavored Markdown, syntax highlighting, and copy buttons.
* **⚡ Collapsible Tool Invocations:** Visual badges showing executed tools (`🛠️ Bash`, `🛠️ Read`, `🛠️ Edit`) without cluttering the chat.
* **💭 Thought Process Accordions:** Expandable chain-of-thought dropdowns for reasoning analysis.
* **Quick Navigation:** Floating **`[↓ Latest]`** button to glide directly to the latest turn, plus one-tap jump to the interactive terminal.

---

### 🖥️ 4. Live Interactive Terminal (PTY / xterm)
Tap **Live Terminal** on any session to attach to the live `tmux` terminal:

* **WebSocket PTY Stream:** Full-duplex interactive terminal powered by `flutter_pty` / xterm over `ws://<host>:7777/v1/sessions/pty`.
* **Dynamic Auto-Fit & Horizontal Scroll:** Automatically calculates terminal columns and rows on viewport resize, with horizontal scrolling and zoom controls (`-` / `+`).
* **Display Modes:** Toggle between responsive auto-fit mode and standard `80 COL` fixed layout.
* **Touch Accessory Bar:** Dedicated on-screen touch keys for `[Esc]`, `[Tab]`, `[Shift+Tab]`, `[Ctrl+C]`, `[↑]`, `[↓]`, and `[Enter]`.

---

### 📝 5. Plans & Proposal Review
* **Diff Viewer:** Syntax-highlighted git diffs with additions in emerald and deletions in coral.
* **Verification Checklists:** Automated test summaries and step progress gauges.
* **Quick Approval:** Review changes and approve plans directly from your phone.

---

### 🌐 6. Multi-Host Mesh & Tailscale
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
*Executes all 116 unit and widget tests covering models, theme extensions, Riverpod providers, and core UI components.*

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
