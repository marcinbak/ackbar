# Project Ackbar Backlog & Roadmap

> **Tracking Doc:** Master inventory of active feature initiatives, architectural explorations, and completed milestones for Project Ackbar.

---

## 🎯 Active Backlog Items

### 1. 🔔 Real-Time Mobile Push Notifications & Deep-Linking
* **Status:** Ready for Implementation
* **Detailed Spec:** [`docs/backlog-push-notifications.md`](backlog-push-notifications.md)
* **Scope:**
  * **Daemon Event Dispatcher (`ackbard`):** Trigger webhook / APNs dispatch when sessions enter `StateBlocked` (clarifying questions, tool permission prompts, or plan reviews).
  * **Zero-Cost Relay (`ntfy.sh`):** Use open-source `ntfy.sh` relay for native iOS/Android lock-screen delivery without requiring a paid $99/yr Apple Developer entitlement.
  * **Lock-Screen Action Endpoints:** Interactive buttons (`[⚡ Allow (y)]`, `[🛑 Deny (n)]`, `[📱 Open in Ackbar]`) to unblock agents with 1 tap.
  * **Custom URL Deep-Linking:** Configure `ackbar://attention?session_id=<uuid>` to open directly into Fullscreen Attention Mode.

---

### 2. 🎙️ Voice-Driven Mobile Companion & Audio Briefings
* **Status:** Architecture Designed
* **Detailed Spec:** [`docs/voice-companion.md`](voice-companion.md)
* **Scope:**
  * **On-Demand Conversational Briefing:** Spoken status summary of cross-machine work in under 45 seconds.
  * **Spoken Plan Reviews:** Voice synthesis of `implementation_plan.md` architectural summaries and trade-offs.
  * **Voice Approval & Keystroke Injection:** Transcribe user voice approvals ("Approve with verbose logging", "Pick option 1") and inject directly into the agent's PTY.

---

### 3. 📱 Mobile Deployment & Distribution Pipeline
* **Status:** Planned
* **Scope:**
  * **Automated Signing & Builds:** Fastlane / GitHub Actions pipeline for iOS IPA (TestFlight / ad-hoc) and Android APK / AAB.
  * **Background SSE Keep-Alive:** Enhanced reconnection resilience when the mobile client is minimized during active streaming sessions.

---

### 4. 🔍 Session Management & Workspace Enhancements
* **Status:** Planned
* **Scope:**
  * **Advanced Fleet Search & Tagging:** Multi-factor search across hosts, token usage thresholds (e.g. `>80% context window`), and custom user labels.
  * **Transcript & Artifact Export:** One-click export of chat transcripts, tool calls, and plan diffs to Markdown and JSON formats.

---

## ✅ Completed Milestones

* [x] **Release Automation & Homebrew Tap:**
  * GoReleaser v2 configuration (`.goreleaser.yaml`) cross-compiling for macOS (`arm64`, `amd64`) and Linux (`amd64`, `arm64`).
  * Automated Homebrew tap formula generation (`marcinbak/homebrew-ackbar`) with `brew services` daemon management.
  * GitHub Actions CI/CD workflows (`.github/workflows/release.yml`, `.github/workflows/ci.yml`).
* [x] **Unread vs. Read Lifecycle Dimension & Animated State Indicators:**
  * `is_unread` and `last_state_change_at` schema tracking with glowing pulse cues on newly blocked or idle sessions.
  * Automatic read acknowledgment (`POST /v1/sessions/mark-read`) when focusing sessions in Web, TUI, or Mobile.
  * Real-time animated working state spinners (`⚙️` rotating in Web & Mobile, cycling quadrant `◐ ◓ ◑ ◒` in Bubble Tea TUI).
* [x] **In-Place Cleared Conversation Turn Rotation & 1-Click Resume:**
  * Context resets (`/clear`, `/reset`) seamlessly archive prior turns as `(Conv 1)` / `⏹️ ENDED` and assign the live tmux window to `(Conv 2)`.
  * Universal 1-click conversation resumption (`POST /v1/sessions/control?action=resume&id=...`) launching native provider resume CLIs across Web (`▶ Resume in Tmux`), TUI (`r`), and Mobile (`▶ Resume Conversation`).
* [x] **Web Multi-Tab Lifecycle Context Menu:**
  * Right-click tab context menu supporting Close Tab, Close Other Tabs, Close Tabs to the Right, and Close All Tabs.
* [x] **Official Provider Vector Logos:**
  * High-performance vector `CustomPainter` paths and inline SVGs for Claude Code (pixelated CLI creature), Google Antigravity (Gaussian arch curve with Google gradient), and OpenAI Codex (interlocking Blossom vortex).
* [x] **Single Source of Truth Versioning:**
  * Root `VERSION` file embedded via `//go:embed` across all binaries; duplicate `internal/web/` directory removed.
* [x] **OSC 52 Clipboard & Auto-Copy Stream:**
  * Terminal clipboard synchronization from remote Linux/Legion tmux sessions to macOS host clipboard and Web UI.
* [x] **Strict PR-Based Git Worktree Workflow & Open Source Guidelines:**
  * Branch protection enforced on `main`, `AGENTS.md` worktree workflow, `CONTRIBUTING.md`, and issue/PR templates.
