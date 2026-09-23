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

*   **User Interface Clients:**
    *   **Mobile App (iOS & Android):** See [docs/mobile.md](file:///Users/dev4u/Work/Ackbar/docs/mobile.md) for Flutter architecture, Attention queue, fullscreen mode, chat transcripts, live terminal, and new session creation.
    *   **Web Dashboard & PWA:** See [docs/web.md](file:///Users/dev4u/Work/Ackbar/docs/web.md) for browser multiplexing, xterm.js tabs, and command palette.
    *   **TUI Dashboard & Controls:** See [docs/tui.md](file:///Users/dev4u/Work/Ackbar/docs/tui.md) for keybindings, tree organization, category subgroups, and tmux attachment.
*   **Networking & Remote Access:** See [docs/networking-and-remote-access.md](file:///Users/dev4u/Work/Ackbar/docs/networking-and-remote-access.md) for Cloudflare Tunnels, Ackbar Relay, Caddy reverse proxy, and token authentication.
*   **Architecture & Networking:** See [docs/architecture.md](file:///Users/dev4u/Work/Ackbar/docs/architecture.md) for cross-machine design, control plane topology, and SSH tunneling.
*   **Daemon Engine:** See [docs/daemon.md](file:///Users/dev4u/Work/Ackbar/docs/daemon.md) for API routes, SQLite migrations, SSE broadcasts, and process supervision.
*   **Agent Providers & Token Limits:** See [docs/providers.md](file:///Users/dev4u/Work/Ackbar/docs/providers.md) for Claude Code, Antigravity, and Codex integrations, dynamic context window limits, and subagent filtering.
*   **Session Naming & Caching:** See [docs/session-naming.md](file:///Users/dev4u/Work/Ackbar/docs/session-naming.md) for title resolution hierarchy and tiered caching rules.
*   **Building & Testing:** See [docs/building.md](file:///Users/dev4u/Work/Ackbar/docs/building.md) for prerequisite packages, compilation steps, test suite execution, and local dev mode setup.
*   **Development Workflow Manifest:** See [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md) for automated verification gates, test tiers (T0-T3), skip rules, and review policies governed by the `dev-workflow` skill.
*   **Distribution & Upgrades:** See [docs/distribution.md](file:///Users/dev4u/Work/Ackbar/docs/distribution.md) for Homebrew Tap setup, GoReleaser automation, and service management.
*   **Voice Companion & Audio Briefings:** See [docs/voice-companion.md](file:///Users/dev4u/Work/Ackbar/docs/voice-companion.md) for speech architecture, conversational audio briefings, and hands-free plan approvals.
*   **Backlog & Roadmap:** See [docs/backlog.md](file:///Users/dev4u/Work/Ackbar/docs/backlog.md) for the master feature roadmap and completed milestones.
*   **Backlog: Blocker Push Notifications:** See [docs/backlog-push-notifications.md](file:///Users/dev4u/Work/Ackbar/docs/backlog-push-notifications.md) for architecture trade-offs, `ntfy.sh` deep-linking, lock-screen actions, and implementation roadmap.

---

## 3. Package Structure

```
.
├── cmd/
│   ├── ackbar/         # Client TUI application main entrypoint
│   ├── ackbard/        # Daemon server application main entrypoint
│   ├── ackbar-hook/    # Shared stdin hook shim CLI main entrypoint
│   └── ackbar-relay/   # Outbound reverse WebSocket tunnel relay server entrypoint
├── internal/
│   ├── client/         # TUI model, views, keybinds, and API client helpers
│   ├── daemon/         # SQLite DB, HTTP server routes, SSE streaming, project normalizer
│   ├── provider/       # Agent adapters (Claude Code, Codex, Antigravity)
│   ├── relay/          # Outbound reverse tunnel server and daemon client
│   ├── tmux/           # Tmux process supervision wrapper (Spawn, Kill, GetPID)
│   └── version/        # Version source of truth (VERSION file & go:embed)
├── web/                # Embedded Web GUI static assets, xterm multiplexer, and fs.FS
├── mobile/             # Native Flutter companion application (iOS & Android)
│   ├── lib/            # Riverpod state, decoupled theme, and feature presentation screens
│   └── test/           # Mobile unit and widget test suite
└── docs/               # Technical subsystem architecture and user guides
```

---

## 4. Key Design Rules & Constraints

1.  **Security Constraint (§9):** The `ackbard` HTTP server binds strictly to `127.0.0.1` by default or operates behind token authentication / outbound reverse tunnel relay.
2.  **CGO-Free SQLite:** Always maintain pure Go database drivers (`modernc.org/sqlite`).
3.  **In-Place Attachment:** Attachment MUST suspend the Bubble Tea app using `tea.ExecProcess`, run `tmux attach` or `ssh -t host tmux attach`, and resume/redraw upon detach.
4.  **No Direct Pushes to `main`:** Direct pushes to `main` are strictly blocked by GitHub branch protection. All code changes must be submitted via Pull Requests.
5.  **Strict PR-Based Git Worktree Workflow:** All development, bug fixes, refactoring, and feature additions MUST follow the PR workflow governed by the `dev-workflow` skill and [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md). Only the repository owner (`marcinbak`) is authorized to merge PRs into `main`.

---

## 5. Development Workflow & PR Guidelines

All development on Project Ackbar is governed by the **`dev-workflow`** skill and project facts defined in [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md).

### Core Lifecycle

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     DEVELOPMENT & PULL REQUEST LIFECYCLE                    │
│                                                                             │
│  1. Plan (N2) ──► 2. Worktree (N3) ──► 3. T0 Tests (N5) ──► 4. Review (N6) │
│     Change brief    .worktrees/<branch>     go/flutter tests     Fan-out x3 │
│                                                                             │
│               ──► 5. Push & PR (N11) ──► 6. Merge & Clean (N14)             │
│                      gh pr create            marcinbak (owner)              │
└─────────────────────────────────────────────────────────────────────────────┘
```

1. **Preflight & Planning (N0–N2):** Manifest facts are loaded from [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md). Architecture plans require explicit user confirmation before any code is modified.
2. **Isolated Git Worktrees (N3):** Never work directly in the root workspace or on the `main` branch. Always create an isolated git worktree branched from `origin/main`:
   ```bash
   git worktree add -b feat/<name> .worktrees/<name> origin/main
   ```
3. **Static Verification (N5):** Verify against the T0 gate set defined in [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md) (`gofmt -l .`, `go test -v ./...`, and `flutter test` if mobile was touched).
4. **Three-Way Review Fan-Out (N6–N8):** Automated reviews run across `context-reviewer`, `clean-reviewer`, and `security-reviewer` before creating a PR.
5. **PR Submission & Branch Protection (N11–N14):** Push branch and create PR using `gh pr create`. Direct pushes to `main` are strictly blocked. Only the repository owner (`marcinbak`) is authorized to merge PRs.
6. **Worktree Cleanup:** Once merged, pull `main` in the root workspace, remove the worktree, and delete the branch:
   ```bash
   git checkout main && git pull origin main
   git worktree remove .worktrees/<name>
   git branch -d <branch>
   ```

