# Contributing to Project Ackbar

Thank you for your interest in contributing to **Ackbar**! ⚓

Ackbar is an open-source, lightweight, cross-machine control plane and session manager designed to monitor, supervise, and organize autonomous AI coding agents (Claude Code, OpenAI Codex, Google Antigravity).

Whether you are reporting a bug, improving documentation, submitting feature ideas, or writing code, we welcome your contributions.

---

## 📜 Code of Conduct

We are committed to providing a friendly, safe, and welcoming environment for all contributors. Please treat fellow community members and maintainers with kindness, empathy, and respect.

---

## 🛠️ Prerequisites & Local Setup

To build, run, and test Ackbar locally, ensure you have the following tools installed:

* **Go Compiler:** Go 1.25 or newer (`go version`).
* **Tmux (`tmux`):** Required for session supervision (`brew install tmux` on macOS, `sudo apt install tmux` on Linux).
* **Flutter SDK:** 3.19 or newer (`flutter --version`) if working on the native mobile companion app (`mobile/`).
* **Git:** Version control and worktree management.
* **GitHub CLI (`gh`):** Recommended for creating Pull Requests (`brew install gh`).

### Clone the Repository

```bash
git clone https://github.com/marcinbak/ackbar.git
cd ackbar
```

---

## 🏗️ Project Architecture & Key Constraints

Before submitting changes, please familiarize yourself with the core architecture in [AGENTS.md](AGENTS.md) and [docs/](docs/).

### Core Architectural Rules

1. **Pure Go (CGO-Free) SQLite:** All database persistence MUST use pure Go database drivers (`modernc.org/sqlite`). CGO must remain disabled for cross-compilation simplicity.
2. **Default Localhost Binding:** The `ackbard` HTTP daemon server MUST bind strictly to `127.0.0.1` by default unless explicitly configured otherwise by the user.
3. **In-Place Terminal Attachment:** TUI session attachment MUST suspend the Bubble Tea program using `tea.ExecProcess`, attach to tmux in-place, and resume cleanly upon detach.
4. **Decoupled Architecture:** Client interfaces (TUI, Web Dashboard, Mobile App) are strictly decoupled from daemon internals and communicate via HTTP REST, SSE (`/v1/events`), and WebSocket PTY (`/v1/sessions/pty`).

---

## 🔄 Development & Pull Request Workflow

We use a **Strict PR-Based Git Worktree Workflow**. Direct pushes to the `main` branch are blocked by GitHub branch protection. All code changes must go through a branch and Pull Request reviewed by `@marcinbak`.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     CONTRIBUTOR DEVELOPMENT LIFECYCLE                       │
│                                                                             │
│  1. Create Worktree ──► 2. Implement & Test ──► 3. Push & Create PR ──► 4. Maintainer Review │
│     .worktrees/<branch>    go test / flutter test   gh pr create            @marcinbak    │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Step 1: Create an Isolated Git Worktree

Always develop on a dedicated feature or bugfix branch inside an isolated git worktree:

```bash
# Branch naming convention: feat/*, fix/*, docs/*, refactor/*, test/*
git worktree add -b feat/my-new-feature .worktrees/my-new-feature main
cd .worktrees/my-new-feature
```

### Step 2: Implement Changes & Run Tests

Make your code modifications. Ensure all automated tests pass and code is formatted cleanly:

```bash
# Format Go code
go fmt ./...

# Run full Go test suite
go test -v ./...

# If mobile/Flutter files were modified:
cd mobile
flutter analyze
flutter test
cd ..
```

### Step 3: Commit Changes

Write clear, semantic commit messages following the [Conventional Commits](https://www.conventionalcommits.org/) specification:

* `feat(subsystem): add support for X`
* `fix(web): resolve terminal disconnection bug`
* `docs(readme): update networking topology matrix`
* `refactor(daemon): simplify session event dispatcher`
* `test(provider): add tests for parser edge case`

```bash
git add -A
git commit -m "feat(web): add keyboard shortcut for quick search"
```

### Step 4: Push Branch & Submit Pull Request

Push your branch to GitHub and create a Pull Request:

```bash
git push -u origin feat/my-new-feature

# Open PR using GitHub CLI
gh pr create --title "feat(web): add keyboard shortcut for quick search" --body "..."
```

---

## 🧪 Testing Guidelines

* **Go Backend & Daemons:**
  * Add unit tests for new API routes, providers, database queries, and parser logic in `*_test.go` files alongside the implementation.
  * Use `httptest.NewServer` and in-memory SQLite (`:memory:`) for integration tests.
* **Flutter Mobile App:**
  * Add widget and unit tests under `mobile/test/`.
  * Verify theme consistency and state transitions using Riverpod test containers.
* **Web Dashboard:**
  * Test browser multiplexing across desktop and mobile browsers (Chrome, Safari, Firefox).

---

## 💡 Submitting Issues & Feature Requests

* **Bug Reports:** Provide clear reproduction steps, your OS/architecture, daemon version (`ackbard -v`), and relevant logs (`~/.config/ackbar/logs/ackbard.log`).
* **Feature Proposals:** Explain the problem you are solving, proposed UX/API, and how it fits into the broader control plane architecture.

---

## 💬 Questions & Discussion

If you have questions about architecture, design decisions, or getting started, feel free to open a [GitHub Discussion](https://github.com/marcinbak/ackbar/discussions) or submit an Issue.

Thank you for helping make Ackbar better! 🚀
