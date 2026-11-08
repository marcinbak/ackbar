# Building & Running Ackbar

This document covers system requirements, build instructions, testing procedures, and local execution guidelines for Project Ackbar.

---

## 1. System Requirements

*   **Go Compiler:** Go 1.25 or newer.
*   **Terminal Multiplexer (`tmux`):** Required for process supervision and managed sessions.
    *   **macOS:** `brew install tmux`
    *   **Linux (Ubuntu/Debian):** `sudo apt-get install tmux`
*   **Version Control (`git`):** Used by `ackbard` for remote URL normalization and project key resolution.
*   **Optional:** `code` (VS Code CLI) in system PATH for the Open in VS Code feature. For remote sessions, the VS Code **Remote - SSH** extension (`ms-vscode-remote.remote-ssh`) is required.

---

## 2. Building Binaries

To build all 4 project binaries, execute the following commands in the workspace root:

```bash
# 1. Daemon Engine (ackbard)
go build -o bin/ackbard ./cmd/ackbard

# 2. CLI & Web GUI Launcher (ackbar)
go build -o bin/ackbar ./cmd/ackbar

# 3. Agent Lifecycle Hook Shim (ackbar-hook)
go build -o bin/ackbar-hook ./cmd/ackbar-hook

# 4. Outbound Reverse Tunnel Relay (ackbar-relay)
go build -o bin/ackbar-relay ./cmd/ackbar-relay
```

To install them into your user PATH (e.g. `~/.local/bin`):

```bash
go build -o ~/.local/bin/ackbard ./cmd/ackbard
go build -o ~/.local/bin/ackbar ./cmd/ackbar
go build -o ~/.local/bin/ackbar-hook ./cmd/ackbar-hook
go build -o ~/.local/bin/ackbar-relay ./cmd/ackbar-relay
```

---

## 3. Running Automated Tests

Run the full suite of unit and integration tests across all packages:

```bash
go test ./...
```

*   **`internal/daemon`**: Tests SQLite persistence, event ingestion, SSE broadcasting, and control routes.
*   **`internal/provider`**: Tests hook payload parsing for Claude Code, Codex, and Antigravity.
*   **`internal/tmux`**: Tests detached process spawning, PID tracking, and termination.

---

## 4. Local Execution & Development Mode

1.  **Start Daemon (`ackbard`):**
    ```bash
    go run ./cmd/ackbard
    ```
    *Listens on `127.0.0.1:7777` by default and initializes SQLite database at `~/.config/ackbar/ackbard.db`.*

2.  **Launch Web GUI (`ackbar` or `http://127.0.0.1:7777`):**
    ```bash
    go run ./cmd/ackbar
    ```
    *Ensures `ackbard` is running and automatically opens `http://127.0.0.1:7777` in your default browser.*

3.  **Simulate Events:**
    Run the simulation script to test real-time SSE updates and blocked state alerts:
    ```bash
    python3 scratch/simulate.py
    ```
