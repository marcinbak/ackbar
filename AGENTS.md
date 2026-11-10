# AGENTS.md — Project Ackbar Architecture & Guidelines

Welcome to Project Ackbar! This document provides an architectural overview and developer reference for human developers and AI coding agents working on this codebase.

---

## 1. Project Overview

Ackbar is a lightweight, cross-machine control plane and session manager designed to monitor, supervise, and organize agentic programming sessions (Claude Code, OpenAI Codex, Google Antigravity).

### Key Components

*   **`ackbard` ([cmd/ackbard/main.go](file:///Users/dev4u/Work/Ackbar/cmd/ackbard/main.go)):** The daemon engine. Listens on `127.0.0.1:7777`, manages SQLite state persistence, tracks tmux process supervision, exposes SSE event streams (`/v1/events`), and ingests hook events (`/v1/hooks/<agent>`).
*   **`ackbar` ([cmd/ackbar/main.go](file:///Users/dev4u/Work/Ackbar/cmd/ackbar/main.go)):** The CLI tool, Web GUI launcher, and MCP & account management tool. Handles CLI subcommands, Web GUI launching, hook setup, MCP stdio serving, and account profile management.
*   **`ackbar-hook` ([cmd/ackbar-hook/main.go](file:///Users/dev4u/Work/Ackbar/cmd/ackbar-hook/main.go)):** A unified stdin-to-HTTP hook shim binary for agents without native HTTP hooks.

---

## 2. Documentation Index

Detailed architectural and technical domain documentation is organized in the `docs/` directory:

*   **User Interface Clients:**
    *   **Mobile App (iOS & Android):** See [docs/mobile.md](file:///Users/dev4u/Work/Ackbar/docs/mobile.md) for Flutter architecture, Attention queue, fullscreen mode, chat transcripts, live terminal, and new session creation.
    *   **Web Dashboard & PWA:** See [docs/web.md](file:///Users/dev4u/Work/Ackbar/docs/web.md) for browser multiplexing, xterm.js tabs, and command palette.
*   **Networking & Remote Access:** See [docs/networking-and-remote-access.md](file:///Users/dev4u/Work/Ackbar/docs/networking-and-remote-access.md) for Cloudflare Tunnels, Ackbar Relay, Caddy reverse proxy, and token authentication.
*   **Architecture & Networking:** See [docs/architecture.md](file:///Users/dev4u/Work/Ackbar/docs/architecture.md) for cross-machine design, control plane topology, and SSH tunneling.
*   **Daemon Engine:** See [docs/daemon.md](file:///Users/dev4u/Work/Ackbar/docs/daemon.md) for API routes, SQLite migrations, SSE broadcasts, and process supervision.
*   **Agent Providers & Token Limits:** See [docs/providers.md](file:///Users/dev4u/Work/Ackbar/docs/providers.md) for Claude Code, Antigravity, and Codex integrations, dynamic context window limits, and subagent filtering.
*   **Session Naming & Caching:** See [docs/session-naming.md](file:///Users/dev4u/Work/Ackbar/docs/session-naming.md) for title resolution hierarchy and tiered caching rules.
*   **Building & Testing:** See [docs/building.md](file:///Users/dev4u/Work/Ackbar/docs/building.md) for prerequisite packages, compilation steps, test suite execution, and local dev mode setup.
*   **Development Workflow Manifest:** See [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md) for automated verification gates, test tiers (T0-T3), skip rules, and review policies governed by the `dev-workflow` skill.
*   **Distribution & Upgrades:** See [docs/distribution.md](file:///Users/dev4u/Work/Ackbar/docs/distribution.md) for Homebrew Tap setup, GoReleaser automation, and service management.
*   **Voice Companion & Supervisor Control Layer:** See [docs/voice-companion.md](docs/voice-companion.md) for hands-free/commuter voice & text-to-audio modes, Supervisor MCP session tools, progressive plan drill-downs, and the 2-tier lazy speech pipeline.
*   **Work & Fleet Overview Dashboard:** See [docs/work-overview/README.md](docs/work-overview/README.md) for the 4-pillar Work Board lifecycle, `dev-workflow` state synchronization, host capabilities & 75/25 weighted routing, and `ackbar mcp` tools.
*   **Architecture Audit (2026-11):** See [docs/audit/README.md](docs/audit/README.md) for hotspot rankings, boundary/cycle diagnostics, cross-client parity gaps, and prioritized remediation waves.
*   **Backlog & Roadmap:** See [docs/backlog.md](file:///Users/dev4u/Work/Ackbar/docs/backlog.md) for the master feature roadmap and completed milestones.

---

## 3. Package Structure

```
.
├── cmd/
│   ├── ackbar/         # CLI tool, Web GUI launcher, and MCP/account manager
│   ├── ackbard/        # Daemon server application main entrypoint
│   ├── ackbar-hook/    # Shared stdin hook shim CLI main entrypoint
│   └── ackbar-relay/   # Outbound reverse WebSocket tunnel relay server entrypoint
├── internal/
│   ├── daemon/         # SQLite DB, modular HTTP server handlers (server_*.go), session_scanner.go, SSE streaming, and agent capability adapters (agent_*.go)
│   ├── provider/       # Agent adapters (Claude Code, Codex, Antigravity, Grok, OpenCode)
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
3.  **No Direct Pushes to `main`:** Direct pushes to `main` are strictly blocked by GitHub branch protection. All code changes must be submitted via Pull Requests.
4.  **Strict PR-Based Git Worktree Workflow:** All development, bug fixes, refactoring, and feature additions MUST follow the PR workflow governed by the `dev-workflow` skill and [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md).
5.  **Enforced PR Version Bump:** Every pull request MUST bump `internal/version/VERSION` above `origin/main` via `./scripts/bump-version.sh`. The bump is strictly gated by `./scripts/bump-version.sh --check` in T0 and GitHub Actions CI.
6.  **Zero Redundant Comments:** Code comments may only state non-obvious invariants or *why* a workaround exists. Comments restating *what* the code does are a `minor` defect.
7.  **Doc & File Budgets:** `AGENTS.md` must stay under 160 lines. `docs/*.md` updates must edit tables/bullets in-place without duplicating other docs.

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
│     ──► 5. Docs Sync (N10) ──► 6. Push & PR (N11) ──► 7. Merge & Clean (N14)│
│         doc_update_targets        gh pr create           gh pr merge --squash│
└─────────────────────────────────────────────────────────────────────────────┘
```

1. **Preflight & Planning (N0–N2):** Manifest facts are loaded from [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md). Architecture plans require explicit user confirmation before any code is modified.
2. **Isolated Git Worktrees (N3):** Never work directly in the root workspace or on the `main` branch. Always create an isolated git worktree branched from `origin/main`:
   ```bash
   git worktree add -b feat/<name> .worktrees/<name> origin/main
   ```
3. **Static Verification (N5):** Verify against the T0 gate set defined in [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md) (`./scripts/bump-version.sh --check`, `gofmt -l .`, `go test -v ./...`, and `flutter test` if mobile was touched). Every PR must bump `internal/version/VERSION` using `./scripts/bump-version.sh` before committing.
4. **Three-Way Review Fan-Out (N6–N8):** Automated reviews run across `context-reviewer`, `clean-reviewer`, and `security-reviewer` before creating a PR.
5. **Project State & Documentation Sync (N10):** After review/verification fixes settle and before opening the PR, audit the branch diff against `doc_update_targets` (`AGENTS.md`, `docs/*.md`), commit any needed doc updates, and verify accuracy (or record an explicit 1-line skip justification).
6. **PR Submission & Automatic Merge (`merge_policy: auto`, N11–N14):** Push branch and create PR using `gh pr create`. Direct pushes to `main` are strictly blocked. Per `merge_policy: auto` in [docs/dev-workflow.md](file:///Users/dev4u/Work/Ackbar/docs/dev-workflow.md), once T0 verification, N6–N8 reviews, N10 doc sync, and CI checks pass, merge the PR automatically using `gh pr merge --squash --delete-branch`.
7. **Worktree Cleanup:** Step out to the root workspace before merging (or after merge), pull `main`, and remove any remaining worktree/branch:
   ```bash
   git checkout main && git pull origin main
   git worktree remove .worktrees/<name>
   git branch -d <branch>
   ```

---

## 6. Communication & Documentation Rules

### Style (ASD-STE100 & Strunk Heuristic)
- Eliminate conversational filler, greetings, sign-offs, hedging, and meta-commentary.
- Lead with the answer, command, or code in the first sentence.
- Write in ASD-STE100 style: active voice, literal phrasing, simplest word choice, no metaphors, no synonyms for stylistic variety.
- State limitations as technical constraints, never apologies.
- Prefer tables and bullet points over prose paragraphs. Keep bullets under 20 words unless explaining a complex causal chain.

### Anti-Redundancy (Single Source of Truth)
- Never duplicate information across code comments, `docs/*.md`, PR descriptions, and chat.
- Code comments: document *why* (invariants/constraints), never *what* the code literally does.
- Docs (`docs/*.md`): document current architecture using tables and file links; never paste large code blocks or changelog history.
- PR bodies: max 5 bullets covering intent, blast radius, and verification gates.
- Chat replies: report only the outcome, key decisions, and links to changed files/artifacts.


