# 05 — Standup & Reporting Architecture

## 1. The Daily Standup Engine

The primary goal of the daily standup engine is to generate a clean, copy-pasteable Markdown summary in under 2 seconds, ready for Slack, Teams, or verbal delivery.

### Standup Formula:
1. **🚀 Completed & Merged (Last 24h):** PRs merged, verified tasks, and closed spikes.
2. **⚙️ In Progress (Today):** Active agent sessions, branches in flight, tasks in review.
3. **⚠️ Blockers & Decisions Needed:** Blocked agents waiting for user input, schema decisions, or API keys.

---

## 2. Project-Grouped Formatting

Rather than presenting an unreadable flat dump of 10 tasks, the standup report is hierarchically organized by project under the active top-level group:

```markdown
## 📅 Daily Standup — Modemobile (Sep 25, 2026)

### 📁 NGL
• [MERGED] [NGL-409] Add Rokt user attributes & tracking in ngl-ios (PR #82)
• [⚠️ BLOCKED] [NGL-512] Fix image-generation memory leak on Android — Blocker: Do we downscale bitmap textures before caching or increase JVM heap ceiling?

### 📁 MEA
• [✅ READY TO MERGE] [MEA-142] MEA Ad Mediation API rate-limiting & Redis eviction (PR #56 approved by Sarah, all CI green)

### 📁 AppLock
• [⚙️ IN PROGRESS] [APPLOCK-88] AppLock biometric PIN vault fallback logic — Implementing Keychain biometric fallback with 100% test coverage.
```

---

## 3. Weekly Retrospective Rollups

The weekly update aggregates 7 days of activity across all projects in the selected group:

### Weekly Summary Sections:
1. **Shipped Deliverables:** Grouped by Jira Epic or project milestone.
2. **Key PRs Merged:** Total PRs opened vs. merged.
3. **Friction Points & Human Interventions:** Aggregated from `dev-workflow` run states (`~/.claude/dev-workflow-runs/*.json`), highlighting where agents needed human assistance most frequently.
4. **Token Economics:** Total tokens and estimated API spend across Claude Code, Antigravity, and Codex.

---

## 4. Synthesis Architecture: Deterministic vs. LLM

The reporting engine operates in two modes:

1. **Deterministic Markdown Template (Instant / 0 Tokens):**
   * Formats the standup directly from SQLite task records.
   * Runs locally with zero network calls and zero cost.
2. **LLM Executive Synthesizer (On-Demand):**
   * When requested, a lightweight model (Gemini Flash or Claude Haiku) polishes the raw bullets into a polished, conversational executive summary.

---

## 5. Voice Companion Integration (`/v1/briefings/synthesize`)

Because Ackbar includes an architecture for voice-driven audio briefings (`docs/voice-companion.md`), the standup engine directly feeds the `/v1/briefings/synthesize` endpoint:

* **Spoken Briefing Request:** *"Give me my Modemobile standup update."*
* **Speech Synthesis:**
  > *"You have 4 tasks on Modemobile today. In NGL, PR 82 for Rokt attributes was merged yesterday, but the Android image generation agent is blocked asking about bitmap caching. In MEA, PR 56 is approved and ready to merge. Would you like me to merge PR 56 now?"*
* **Spoken Action Dispatch:** User says *"Yes, merge it."* ➔ triggers automated PR merge!
