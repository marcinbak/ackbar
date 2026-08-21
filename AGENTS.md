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
    *   **Mobile App (iOS & Android):** See [docs/mobile.md](file:///Users/dev4u/Work/Ackbar/docs/mobile.md) for Flutter architecture, Attention queue, fullscreen mode, chat transcripts, and live terminal.
    *   **Web Dashboard & PWA:** See [docs/web.md](file:///Users/dev4u/Work/Ackbar/docs/web.md) for browser multiplexing, xterm.js tabs, and command palette.
    *   **TUI Dashboard & Controls:** See [docs/tui.md](file:///Users/dev4u/Work/Ackbar/docs/tui.md) for keybindings, tree organization, category subgroups, and tmux attachment.
*   **Networking & Remote Access:** See [docs/networking-and-remote-access.md](file:///Users/dev4u/Work/Ackbar/docs/networking-and-remote-access.md) for Cloudflare Tunnels, Ackbar Relay, Caddy reverse proxy, and token authentication.
*   **Architecture & Networking:** See [docs/architecture.md](file:///Users/dev4u/Work/Ackbar/docs/architecture.md) for cross-machine design, control plane topology, and SSH tunneling.
*   **Daemon Engine:** See [docs/daemon.md](file:///Users/dev4u/Work/Ackbar/docs/daemon.md) for API routes, SQLite migrations, SSE broadcasts, and process supervision.
*   **Agent Providers & Token Limits:** See [docs/providers.md](file:///Users/dev4u/Work/Ackbar/docs/providers.md) for Claude Code, Antigravity, and Codex integrations, dynamic context window limits, and subagent filtering.
*   **Session Naming & Caching:** See [docs/session-naming.md](file:///Users/dev4u/Work/Ackbar/docs/session-naming.md) for title resolution hierarchy and tiered caching rules.
*   **Building & Testing:** See [docs/building.md](file:///Users/dev4u/Work/Ackbar/docs/building.md) for prerequisite packages, compilation steps, test suite execution, and local dev mode setup.
*   **Distribution & Upgrades:** See [docs/distribution.md](file:///Users/dev4u/Work/Ackbar/docs/distribution.md) for Homebrew Tap setup, GoReleaser automation, and service management.
*   **Voice Companion & Audio Briefings:** See [docs/voice-companion.md](file:///Users/dev4u/Work/Ackbar/docs/voice-companion.md) for speech architecture, conversational audio briefings, and hands-free plan approvals.
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
5.  **Strict PR-Based Git Worktree Workflow:** All development, bug fixes, refactoring, and feature additions MUST follow the PR workflow outlined below. Only the repository owner (`marcinbak`) is authorized to merge PRs into `main`.

---

## 5. PR-Based Development Workflow

Every developer and AI coding agent working on Project Ackbar must follow this standardized development lifecycle:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     DEVELOPMENT & PULL REQUEST LIFECYCLE                    │
│                                                                             │
│  1. Create Worktree ──► 2. Implement & Test ──► 3. Push & Create PR ──► 4. User Merges │
│     .worktrees/<branch>    go test / flutter test   gh pr create            marcinbak    │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Step 1: Create an Isolated Git Worktree
Never work directly in the root workspace or on the `main` branch. Always create an isolated git worktree with an appropriate branch prefix (`feat/`, `fix/`, `refactor/`, `docs/`):
```bash
git worktree add -b feat/<feature-name> .worktrees/<feature-name> main
```

### Step 2: Implement Changes & Run Test Suite
Navigate to the worktree directory and make your changes. Verify that all automated tests pass before committing:
```bash
# In the worktree directory:
# Run Go unit & integration test suites
go test -v ./...

# If mobile/Flutter files were modified:
cd mobile && flutter test
```

### Step 3: Commit & Push Branch
Commit changes with semantic commit messages and push the dedicated branch to `origin`:
```bash
git add -A
git commit -m "feat(subsystem): brief summary of changes"
git push -u origin feat/<feature-name>
```

### Step 4: Create a GitHub Pull Request (PR)
Create a Pull Request against `main` using the GitHub CLI:
```bash
gh pr create \
  --title "feat(subsystem): brief description" \
  --body "### Summary of Changes\n- Detail 1\n- Detail 2\n\n### Verification\n- go test ./... passed\n- flutter test passed"
```

### Step 5: Clean Up Worktree After Merge
Once the PR has been reviewed and merged by `@marcinbak`:
```bash
git checkout main
git pull origin main
git worktree remove .worktrees/<feature-name>
git branch -d feat/<feature-name>
```

