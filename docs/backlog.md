# Project Ackbar Backlog & Roadmap

> **Tracking Doc:** Master inventory of active feature initiatives, architectural explorations, and completed milestones for Project Ackbar.

---

## 🎯 Active Backlog Items

### 1. 🧭 Supervisor Multi-Stage Orchestration & Inter-Stage Handoff
* **Status:** Architectural Exploration
* **Scope:**
  * **Two-Lane Task Classification (Fast Track vs. Multi-Stage):** Allow the `⚓ Supervisor` to route small bugs/chores directly into a single `dev-workflow` (`N0`–`N15`) worker session while orchestrating multi-stage initiatives across dedicated Discovery/Design (`kind="design"`) $\rightarrow$ Human Checkpoint $\rightarrow$ Per-Repo Implementation (`kind="code"`) stages.
  * **Hybrid Inter-Stage Handoff:** Combine canonical task deliverables (`task_deliverables` with `content_md` and auto-ingested `.brief.md` / `.impl-notes.md` / `.retro.md`) with target repository git branches for seamless context handoff between stages and across hosts (`@macmini` $\rightarrow$ `@legion`).
  * **Supervisor $\leftrightarrow$ Worker Check-In Loop:** Bounded daemon/Supervisor orchestration when a worker reaches human or stage checkpoints (`N1` question, `N2` plan review, `N11`/`N13` PR ready, `N14`/`N15` merge & retro).

---

### 2. 📱 Mobile Deployment & Distribution Pipeline
* **Status:** Planned
* **Scope:**
  * **Automated Signing & Builds:** Fastlane / GitHub Actions pipeline for iOS IPA (TestFlight / ad-hoc) and Android APK / AAB.

---

### 3. 🔍 Session Management & Workspace Enhancements
* **Status:** Planned
* **Scope:**
  * **Advanced Fleet Search & Tagging:** Multi-factor search across hosts, token usage thresholds (e.g. `>80% context window`), and custom user labels.
  * **Transcript & Artifact Export:** One-click export of chat transcripts, tool calls, and plan diffs to Markdown and JSON formats.

---

## ✅ Completed Milestones

See [docs/archive/completed-tasks.md](archive/completed-tasks.md) for the historical log of completed milestones.
