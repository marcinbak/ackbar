# AGENTS.md — Project Ackbar Architecture & Guidelines

Welcome to Project Ackbar! This document provides an architectural overview and developer reference for human developers and AI coding agents working on this codebase.

---

## 1. Project Overview

Ackbar is a lightweight, cross-machine control plane and session manager designed to monitor, supervise, and organize agentic programming sessions (Claude Code, OpenAI Codex, Google Antigravity).

### Key Components

*   **`ackbard` ([cmd/ackbard/main.go](file:///Users/dev4u/Work/Ackbar/cmd/ackbard/main.go)):** The daemon engine. Listens on `127.0.0.1:7777`, manages SQLite state persistence, tracks tmux process supervision, exposes SSE event streams (`/v1/events`), and ingests hook events (`/v1/hooks/<agent>`).
*   **`ackbar` ([cmd/ackbar/main.go](file:///Users/dev4u/Work/Ackbar/cmd/ackbar/main.go)):** The TUI dashboard. Built with Charm.sh Bubble Tea. Displays a collapsible logical tree of projects and sessions, attaches in-place to tmux sessions, and handles session controls (restart, terminate, delete, open in code, view docs).
*   **`ackbar-hook` ([cmd/ackbar-hook/main.go](file:///Users/dev4u/Work/Ackbar/cmd/ackbar-hook/main.go)):** A unified stdin-to-HTTP hook shim binary for agents without native HTTP hooks.

---

## 2. Documentation Index

Detailed architectural and technical domain documentation is organized in the `docs/` directory:

*   **Architecture & Networking:** See [docs/architecture.md](file:///Users/dev4u/Work/Ackbar/docs/architecture.md) for cross-machine design, control plane topology, and SSH tunneling.
*   **Daemon Engine:** See [docs/daemon.md](file:///Users/dev4u/Work/Ackbar/docs/daemon.md) for API routes, SQLite migrations, SSE broadcasts, and process supervision.
*   **TUI Dashboard & Controls:** See [docs/tui.md](file:///Users/dev4u/Work/Ackbar/docs/tui.md) for keybindings, tree organization, category subgroups, and tmux attachment.
*   **Agent Providers & Token Limits:** See [docs/providers.md](file:///Users/dev4u/Work/Ackbar/docs/providers.md) for Claude Code, Antigravity, and Codex integrations, dynamic context window limits, and subagent filtering.
*   **Session Naming & Caching:** See [docs/session-naming.md](file:///Users/dev4u/Work/Ackbar/docs/session-naming.md) for title resolution hierarchy and tiered caching rules.
*   **Building & Testing:** See [docs/building.md](file:///Users/dev4u/Work/Ackbar/docs/building.md) for prerequisite packages, compilation steps, test suite execution, and local dev mode setup.
*   **Distribution & Upgrades:** See [docs/distribution.md](file:///Users/dev4u/Work/Ackbar/docs/distribution.md) for Homebrew Tap setup, GoReleaser automation, and service management.
*   **Voice Companion & Audio Briefings:** See [docs/voice-companion.md](file:///Users/dev4u/Work/Ackbar/docs/voice-companion.md) for speech architecture, conversational audio briefings, and hands-free plan approvals.

---

## 3. Package Structure

```
.
├── cmd/
│   ├── ackbar/         # Client TUI application main entrypoint
│   ├── ackbard/        # Daemon server application main entrypoint
│   └── ackbar-hook/    # Shared stdin hook shim CLI main entrypoint
├── internal/
│   ├── client/         # TUI model, views, keybinds, and API client helpers
│   ├── daemon/         # SQLite DB, HTTP server routes, SSE streaming, project normalizer
│   ├── provider/       # Agent adapters (Claude Code, Codex, Antigravity)
│   └── tmux/           # Tmux process supervision wrapper (Spawn, Kill, GetPID)
└── docs/
    ├── building.md     # Build & test documentation
    └── distribution.md # Packaging & upgrade documentation
```

---

## 4. Key Design Rules & Constraints

1.  **Security Constraint (§9):** The `ackbard` HTTP server MUST bind strictly to `127.0.0.1`. Remote client connectivity MUST use SSH tunnels (`ssh -L`).
2.  **CGO-Free SQLite:** Always maintain pure Go database drivers (`modernc.org/sqlite`).
3.  **In-Place Attachment:** Attachment MUST suspend the Bubble Tea app using `tea.ExecProcess`, run `tmux attach` or `ssh -t host tmux attach`, and resume/redraw upon detach.
