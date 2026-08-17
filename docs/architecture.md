# Ackbar Architecture & Technical Design

## 1. System Overview

Ackbar is a cross-machine control plane and session manager designed to monitor, supervise, and organize agentic programming sessions (`Claude Code`, `OpenAI Codex`, `Google Antigravity`).

```
+-------------------------------------------------------------------------+
|                              ACKBAR TUI                                  |
|   (Interactive Charm.sh Bubble Tea Terminal Dashboard & Process Viewer) |
+--------------------+--------------------------------+-------------------+
                     |                                | (SSH Port Forward)
                     v                                v
+------------------------------------+   +------------------------------------+
|        LOCAL ACKBAR DAEMON         |   |        REMOTE ACKBAR DAEMON        |
|      (ackbard @ 127.0.0.1:7777)    |   |     (ackbard @ remote 127.0.0.1)   |
+-----------------+------------------+   +-----------------+------------------+
                  |                                        |
  +---------------+---------------+        +---------------+---------------+
  |               |               |        |               |               |
  v               v               v        v               v               v
SQLite DB     SSE Events      Hook API  SQLite DB     SSE Events      Hook API
(~/.ackbard.db) (/v1/events)  (/v1/hooks)  (~/.ackbard.db) (/v1/events)  (/v1/hooks)
```

---

## 2. Core Components

1. **`ackbard` (Daemon Server):**
   * Binds strictly to `127.0.0.1:7777`.
   * Pure Go embedded SQLite engine (`modernc.org/sqlite`).
   * High-throughput JSON hook ingestion endpoint (`/v1/hooks/<agent>`).
   * Real-time Server-Sent Events (SSE) broadcast stream (`/v1/events`).
   * Background process scanner combining `tmux list-panes` and OS process table (`ps`).
2. **`ackbar` (TUI Client):**
   * Built with Charm.sh Bubble Tea and Lip Gloss styling.
   * Multi-host aggregation querying local and remote tunneled daemons.
   * Hierarchical logical project & subgroup tree.
   * In-place tmux pane attachment (`tea.ExecProcess`).
3. **`ackbar-hook` (Hook Shim CLI):**
   * Universal stdin-to-HTTP hook adapter for CLI agents lacking native HTTP hooks.

---

## 3. Cross-Machine Networking & Security Model

* **Strict Localhost Binding:** To prevent unauthorized exposure, `ackbard` binds exclusively to `127.0.0.1:7777` on each machine.
* **Encrypted SSH Tunneling:** The TUI establishes encrypted SSH tunnels (`ssh -L <port>:127.0.0.1:7777 <target>`) to communicate with remote daemons.
* **Passwordless SSH Requirement:** Remote host registration and background updates leverage standard OpenSSH keys.
