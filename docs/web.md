# Ackbar Web Dashboard & PWA (`web/`)

> **Real-time browser control plane and terminal multiplexer served directly by `ackbard`.**

---

## 1. Overview & Architecture

The **Ackbar Web Dashboard** is a zero-dependency, high-performance web interface built with vanilla modern JavaScript, CSS custom properties, and `xterm.js`.

It is embedded directly into the `ackbard` Go binary and served on `http://127.0.0.1:7777`.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           BROWSER CLIENT / PWA                              │
│                                                                             │
│  ┌───────────────────────┐  ┌────────────────────────────────────────────┐  │
│  │   Sidebar Tree View   │  │       Multi-Tab xterm.js Terminal Tabs     │  │
│  │ (Drag-and-Drop Order) │  │       (Interactive WebSocket Streams)      │  │
│  └───────────┬───────────┘  └─────────────────────┬──────────────────────┘  │
│              │                                    │                         │
│              │ REST API & SSE (/v1/events)        │ WebSocket PTY           │
│              ▼                                    ▼ (/v1/sessions/pty)      │
└──────────────┼────────────────────────────────────┼─────────────────────────┘
               │                                    │
               ▼                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        ACKBARD DAEMON ENGINE (Go)                           │
│                 (HTTP Server, SQLite DB, Tmux Supervisor)                   │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Key Features

### 🖥️ 1. Multi-Tab Terminal Multiplexer
* Powered by `xterm.js` with `FitAddon` and `WebglAddon` (where hardware acceleration is available).
* Attach simultaneously to multiple local and remote agent sessions.
* Full keyboard fidelity, ANSI color rendering, mouse wheel scrolling, and window resize propagation.

### 🌳 2. Hierarchical Sidebar Tree
* Automatic mapping of sessions to Git repositories and custom category folders.
* Drag-and-drop ordering of session nodes and project groups.
* Live status badges (`⚡ WORKING`, `❓ BLOCKED`, `✅ IDLE`, `⚪ OFFLINE`) with real-time updates via Server-Sent Events (`SSE`).
* Context token usage meter (adapting dynamically to 200k, 1M, and 128k token limits).

### ⚡ 3. Command Palette (`Cmd+K` / `Ctrl+K`)
* Instant fuzzy search across all sessions, projects, and remote hosts.
* Quick actions: spawn session, switch tab, open in VS Code, and filter by status.

### 💻 4. One-Click VS Code Bridge
* Right-click any session or click the editor button to open the exact workspace in VS Code.
* Automatically resolves `code --remote ssh-remote+<host> <path>` for remote machines.

### 📖 5. In-App Plan & Markdown Viewer
* Native markdown renderer for viewing `implementation_plan.md`, `task.md`, and `walkthrough.md` side-by-side with running terminals.

---

## 3. Keyboard Shortcuts

| Shortcut | Action |
| :--- | :--- |
| `Cmd+K` / `Ctrl+K` | Open Command Palette |
| `Cmd+T` / `Ctrl+T` | Create New Session |
| `Cmd+W` / `Ctrl+W` | Close Active Terminal Tab |
| `Cmd+1` ... `Cmd+9` | Switch to Terminal Tab 1–9 |
| `Escape` | Dismiss Modals & Command Palette |
| `Ctrl+Shift+R` | Force Refresh Daemon State |

---

## 4. Progressive Web App (PWA) Support

The Web Dashboard includes a valid Web App Manifest (`internal/web/manifest.json`) and service worker hooks. You can install it as a standalone desktop app via Google Chrome, Arc, Edge, or Safari (*"Add to Dock / Install Ackbar"*).
