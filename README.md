# ⚓ Ackbar

> **Lightweight, cross-machine control plane and session manager for AI coding agents.**

[![Version](https://img.shields.io/badge/version-v20260820.09-6366f1.svg)](https://github.com/marcinbak/ackbar/releases)
[![Go Version](https://img.shields.io/badge/go-1.25+-00ADD8.svg)](https://golang.org)
[![Pure Go SQLite](https://img.shields.io/badge/sqlite-CGO--free-blue.svg)](https://modernc.org/sqlite)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey.svg)]()

---

![Ackbar Web Dashboard Preview](docs/images/dashboard_preview.png)

---

## 🧭 What is Ackbar?

**Ackbar** is a developer control plane designed to monitor, organize, and interact with autonomous AI coding agents (**Claude Code**, **Google Antigravity**, **OpenAI Codex**) across all your local workstations, remote GPU compute boxes, and cloud virtual machines.

Ackbar provides both a **rich real-time Web GUI** (`http://127.0.0.1:7777`) and a **pure keyboard-driven Terminal UI (TUI)** built with Charm Bubble Tea. It tracks agent lifecycle telemetry, visualizes context token limits, allows instant in-browser and in-terminal multiplexed attachment, and seamlessly bridges local and remote agent workspaces into VS Code.

---

## 🎯 What Problem Does Ackbar Solve?

As AI software engineering scales from single-prompt experiments to multiple parallel agent sessions running on local and remote machines, developers face critical coordination hurdles:

* 🧩 **Context Fragmentation:** When running agents across 5 different repositories and 3 different machines (MacBook, Linux workstation, GPU box), knowing which agent is running where becomes chaotic.
* ⏳ **Invisible Blockers:** Agents often finish tasks or halt waiting for user input/tool approval (e.g. running a database migration or bash command) without notifying the developer, leaving sessions idling for hours unnoticed.
* 🔌 **Detached Terminal Nightmare:** Managing raw `tmux` sessions across multiple SSH targets requires juggling countless SSH windows, manual window splits, and awkward session reconnections.
* 📊 **Token & Context Blindness:** Developers often hit sudden agent context window degradation or truncation without clear visibility into token consumption against model limits (200k vs 1M tokens).
* 🔀 **Editor Disconnection:** Jumping from an agent terminal back to the exact project directory and git branch inside your local code editor is cumbersome and manual.

**Ackbar resolves all of these by acting as a single, lightweight pane of glass for all your agentic sessions.**

---

## ✨ Key Features

* 🤖 **Multi-Agent Provider Support:** First-class ingestion and supervision for **Anthropic Claude Code**, **Google Antigravity**, and **OpenAI Codex**.
* 🌐 **Full-Featured Web GUI & Mobile PWA:**
  * Interactive multi-tab terminal multiplexer powered by `xterm.js`.
  * Real-time Server-Sent Events (`SSE`) streaming with instant zero-lag updates.
  * Drag-and-drop hierarchical tree organization.
  * Command palette (`Cmd+K` / `Ctrl+K`) for rapid fuzzy navigation.
* 🖥️ **Pure Terminal UI (TUI):**
  * Built with Charm.sh Bubble Tea and Lip Gloss.
  * Zero-overhead in-place `tmux` attachment (`Enter` / `a`) and seamless resume on detach.
  * Works smoothly over minimal SSH sessions.
* 🚦 **Expressive Live Status Indicators:**
  * `⚡` **Work in Progress:** Agent is actively analyzing, planning, or executing commands.
  * `❓` **Waiting for Feedback:** Agent is blocked awaiting user input, permission, or clarification.
  * `✅` **Idle:** Agent has finished its turn and is ready for the next instruction.
  * `⚪` **Disconnected / Unknown:** Session inactive or remote daemon unreachable.
* 🔗 **Cross-Machine Remote Aggregation:**
  * Native multi-host support. Connect remote compute instances transparently using secure SSH tunnels (`ssh -L`).
  * No exposed external ports or public internet attack surface.
* 💻 **One-Click VS Code Integration:**
  * Open any local or remote project directory in VS Code with a single click or keypress (`o`).
  * Automatic `code --remote ssh-remote+<host> <path>` resolution for remote servers.
* 📈 **Dynamic Context Window Gauge:**
  * Live token percentage gauge adapting automatically to model ceilings (200k standard, 1M for Claude 3.7 / Gemini, 128k for Codex/GPT-4).
* 📖 **In-App Project & Plan Viewer:**
  * Built-in Markdown document viewer for inspecting `implementation_plan.md`, `task.md`, `walkthrough.md`, `README.md`, and `AGENTS.md` directly from the dashboard.
* 🗂️ **Categorized Tree Organization:**
  * Automatic filesystem repository matching combined with custom, user-defined category subgroups.
  * Session archiving and stale session cleanup without losing project structure.

---

## 🚀 Quickstart Guides

### 1. How to Build

#### Prerequisites
* **Go Compiler:** Go 1.25 or newer.
* **Tmux (`tmux`):** Required for process supervision (`brew install tmux` on macOS, `sudo apt install tmux` on Linux).
* **Git (`git`):** Used for workspace project root and branch detection.

```bash
# Clone repository
git clone https://github.com/marcinbak/ackbar.git
cd ackbar

# Build all 3 binaries to ~/.local/bin (or bin/)
go build -o ~/.local/bin/ackbard ./cmd/ackbard
go build -o ~/.local/bin/ackbar ./cmd/ackbar
go build -o ~/.local/bin/ackbar-hook ./cmd/ackbar-hook
```

> 📖 **Detailed Build Guide:** See [docs/building.md](docs/building.md) for compilation flags, test suites, and dev mode.

---

### 2. How to Run

#### Step 1: Start the Daemon Engine (`ackbard`)
The daemon runs as a local background service, manages SQLite persistence, tracks tmux processes, and serves the Web UI:

```bash
ackbard
```
*By default, listens strictly on `127.0.0.1:7777` with database stored at `~/.config/ackbar/ackbard.db`.*

#### Step 2: Access the Dashboard
* **Web GUI:** Open [http://127.0.0.1:7777](http://127.0.0.1:7777) in your browser.
* **Terminal UI (TUI):** Launch the client in any terminal window:
  ```bash
  ackbar
  ```

---

### 3. How to Add a Remote Host

Ackbar uses SSH local port forwarding to connect remote compute machines to your local dashboard without exposing ports to the internet.

1. **Install and run `ackbard` on your remote machine:**
   ```bash
   # On remote server (e.g. gpu-box)
   ackbard -port 7777
   ```
2. **Establish an SSH Tunnel from your local machine:**
   ```bash
   # Forward remote port 7777 to local port 7778
   ssh -N -L 7778:127.0.0.1:7777 user@gpu-box
   ```
3. **Register the host in Ackbar:**
   * **In Web GUI:** Click the **`+ Host`** button in the top navbar, enter the name `gpu-box` and endpoint `http://127.0.0.1:7778`.
   * **In TUI:** Press **`R`** to open the Host Registration wizard.

> 📖 **Architecture & Networking Guide:** See [docs/architecture.md](docs/architecture.md) for automated tunnel setups and cross-machine topologies.

---

### 4. How to Create a New Session

You can launch a supervised agent session directly from the UI without touching tmux manually:

* **In Web GUI:**
  1. Click **`+ New Session`** in the top navbar (or click the **`＋`** icon on any project folder).
  2. Select the target **Host** (e.g. `local` or `gpu-box`).
  3. Choose the **Agent** (`Claude Code`, `Antigravity`, or `Codex`).
  4. Enter or browse the **Project Directory** path.
  5. *(Optional)* Provide an initial prompt instruction.
  6. Click **Spawn Session** — an interactive tab will open immediately with full keyboard control.
* **In TUI:**
  * Highlight any project group and press **`s`** to launch a new session wizard.

---

### 5. How to Delete, Archive, and Manage Sessions

* **In Web GUI:**
  * **Switch / Open Tab:** Click on any session row in the sidebar tree.
  * **Close Tab:** Click the **`✕`** icon on the tab header or middle-click the tab.
  * **Session Action Menu:** Right-click any session row to access:
    * 💻 **Open in VS Code:** Launches your editor in the session's workspace.
    * 📖 **View Project Docs:** Reads `task.md`, `README.md`, or `AGENTS.md`.
    * 🔄 **Restart Session:** Restarts the underlying tmux process.
    * 🛑 **Kill Process:** Sends a termination signal to the agent process.
    * 📦 **Archive Session:** Moves the session to the archived view.
    * 🗑️ **Delete Session:** Permanently removes session records.
* **In TUI:**
  * `Space` / `Enter` / `a`: Attach in-place to tmux session.
  * `o`: Open workspace in VS Code.
  * `V`: View documentation.
  * `r`: Restart session.
  * `k`: Terminate (kill) process.
  * `x`: Archive session (`v` to toggle archived view).
  * `d`: Delete session.

---

## 🏗️ Architecture & Component Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                             USER INTERFACES                                 │
│                                                                             │
│   ┌───────────────────────────────┐     ┌───────────────────────────────┐   │
│   │     TUI Client (Bubble Tea)   │     │      Web Dashboard & PWA      │   │
│   │    `cmd/ackbar/main.go`       │     │   `web/index.html` + xterm.js │   │
│   └───────────────┬───────────────┘     └───────────────┬───────────────┘   │
└───────────────────┼─────────────────────────────────────┼───────────────────┘
                    │                                     │
                    │ REST / SSE (/v1/events)             │ WebSocket PTY
                    ▼                                     ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          LOCAL DAEMON ENGINE (ackbard)                      │
│                                                                             │
│  ┌───────────────────────┐  ┌──────────────────────┐  ┌──────────────────┐  │
│  │   HTTP Control Plane  │  │  PTY WS Multiplexer  │  │ SSE Broadcaster  │  │
│  └───────────┬───────────┘  └──────────┬───────────┘  └────────┬─────────┘  │
│              │                         │                       │            │
│  ┌───────────▼─────────────────────────▼───────────────────────▼──────────┐  │
│  │                     Pure Go SQLite (ackbard.db)                        │  │
│  └─────────────────────────────────────┬──────────────────────────────────┘  │
│                                        │                                     │
│  ┌─────────────────────────────────────▼──────────────────────────────────┐  │
│  │                       Tmux Process Supervisor                          │  │
│  │               (Local Sessions & PTY Terminal Streams)                  │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────┬────────────────────────────────────┘
                                         │
                                         │ SSH Tunnel (ssh -L 7778:127.0.0.1:7777)
                                         ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        REMOTE DAEMON ENGINE (ackbard)                       │
│                      (Cloud GPU Box / Remote Linux Server)                  │
│                                                                             │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │   HTTP Server (127.0.0.1:7777) + Pure Go SQLite + Tmux Supervisor      │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Core Components:
1. **`ackbard` ([cmd/ackbard/main.go](cmd/ackbard/main.go)):** The background daemon. Listens on `127.0.0.1:7777`, manages SQLite state, supervises tmux processes, streams SSE notifications, and multiplexes terminal PTY connections.
2. **`ackbar` ([cmd/ackbar/main.go](cmd/ackbar/main.go)):** The terminal dashboard built with Charm.sh Bubble Tea. Handles keyboard-centric navigation and in-place tmux attachment.
3. **`ackbar-hook` ([cmd/ackbar-hook/main.go](cmd/ackbar-hook/main.go)):** A unified stdin hook shim binary for forwarding agent lifecycle events to `ackbard`.

---

## 📚 Documentation Index

Comprehensive guides for every subsystem are located in the `docs/` directory:

| Guide | Description |
| :--- | :--- |
| 🌐 **[Architecture & Networking](docs/architecture.md)** | Multi-host topology, SSH tunneling, security model, and control plane design. |
| ⚙️ **[Daemon Engine & API Reference](docs/daemon.md)** | REST endpoints, SQLite auto-migrations, SSE broadcaster, and PTY WebSocket multiplexer. |
| 🖥️ **[TUI Controls & Keybindings](docs/tui.md)** | Charm Bubble Tea dashboard layout, keyboard shortcuts, and tmux in-place attachment. |
| 🤖 **[Agent Providers & Token Limits](docs/providers.md)** | Claude Code, Antigravity, and Codex adapters, subagent filtering, and dynamic token calculation. |
| 🏷️ **[Session Naming & Caching](docs/session-naming.md)** | Title resolution hierarchy, transcript parsing, and tiered metadata caching rules. |
| 🔨 **[Building & Testing](docs/building.md)** | Toolchain prerequisites, manual build steps, automated test suite, and local dev mode. |
| 📦 **[Distribution & Upgrades](docs/distribution.md)** | Homebrew Tap setup, GoReleaser automation, GitHub releases, and remote host auto-upgrade. |
| 🎙️ **[Voice Companion & Audio Briefings](docs/voice-companion.md)** | Speech architecture, hands-free conversational audio briefings, and mobile companion plans. |

---

## 📄 License

This project is licensed under the **MIT License** — see the [LICENSE](LICENSE) file for details.
