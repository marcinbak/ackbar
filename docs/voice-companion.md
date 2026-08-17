# Voice-Driven Mobile Companion & Audio Briefings

This document specifies the technical architecture, conversational protocol, and integration design for Project Ackbar's voice-driven mobile companion experience.

---

## 1. Overview & Objectives

The Voice Companion provides a hands-free, speech-driven interface (Mobile Web PWA or lightweight native mobile client) allowing developers to:
1. **Request On-Demand Audio Briefings:** Ask for a verbal status report on ongoing projects and sessions across all local and remote servers.
2. **Hear Implementation Plans & Trade-Offs:** Listen to spoken summaries of proposed architectural plans (`implementation_plan.md`) without reading raw markdown on mobile screens.
3. **Listen to Blocked Questions & Options:** Hear clarifying multiple-choice questions, context, and rationale from blocked agents.
4. **Speak Approvals & Dispatches:** Verbally approve plans, answer questions, or pause sessions, automatically routing commands back to the underlying agent.

---

## 2. System Architecture

```
┌────────────────────────────────────────────────────────┐
│               Mobile Device (PWA / App)                │
│  🎤 Voice Input ──► [Audio AI] ◄── 🔊 Spoken Briefing  │
└──────────────────────────┬─────────────────────────────┘
                           │ WebSocket / HTTPS
                           ▼
┌────────────────────────────────────────────────────────┐
│                   ackbard Control Plane                │
│  1. Queries SQLite state (Blocked, Working, Idle)      │
│  2. Reads implementation_plan.md & agent questions     │
│  3. LLM synthesis ➔ Formats conversational audio brief │
│  4. Action Dispatch ➔ Injects user response into PTY   │
└────────────────────────────────────────────────────────┘
```

### Components:
* **Audio Capture & Synthesis (Client):** Mobile browser Web Speech API (`SpeechRecognition` / `SpeechSynthesis`) or streaming audio WebSocket (OpenAI Realtime Audio / Gemini Live Audio).
* **Synthesis Engine (`ackbard`):** Aggregates session states, disk transcripts, and active plan artifacts into a concise, speech-optimized conversational briefing.
* **Command Dispatcher (`ackbard`):** Maps transcribed voice intents into PTY keystrokes, tool approval tokens, or session state transitions.

---

## 3. Conversational Protocol & Flows

### Flow A: On-Demand Work Briefing
1. **User speaks:** *"Give me a summary of my ongoing work."*
2. **Daemon Aggregation:** `ackbard` fetches all sessions from local SQLite and remote endpoints (e.g. `legion`).
3. **Synthesis Prompt:**
   ```
   You are an executive engineering briefer. Summarize the user's active AI agent sessions in a conversational, spoken tone. Group by project, state active goals, and highlight any session waiting for input or plan review. Keep it under 45 seconds of spoken audio.
   ```
4. **Audio Output:**
   > *"You have 3 active projects. On Skip2Q, the Antigravity agent is actively working on QR scanning. On Modemobile, Claude Code has finished an implementation plan for Rokt attributes and is waiting for your review. The plan adds 3 new tracking events and asks whether to enable debug logging. Would you like to approve it or hear the details?"*

---

### Flow B: Spoken Plan Review & Approval
1. **User speaks:** *"Tell me about the Rokt plan."*
2. **Daemon Artifact Reader:** Reads `implementation_plan.md` from the project's artifact directory.
3. **Audio Output:**
   > *"The plan modifies two files in ngl-ios to add missing user attributes and integrate Rokt feedback. It requires user feedback on whether to enable verbose logging in staging. You can say 'Approve with verbose logging' or 'Reject'."*
4. **User speaks:** *"Approve with verbose logging."*
5. **Action Dispatch:** `ackbard` sends approval input directly to the agent's PTY / hook endpoint.

---

### Flow C: Spoken Multiple-Choice Questions
1. **Agent State:** Agent enters `StateBlocked` with `Blocked.Kind = "question"`.
2. **Audio Output:**
   > *"Claude Code on Ackbar has a question: 'Which database driver should we use for SQLite?' Option 1: modernc pure Go driver. Option 2: mattn cgo driver. Which should I select?"*
3. **User speaks:** *"Pick option 1."*
4. **Action Dispatch:** `ackbard` submits Option 1 to the blocked session.

---

## 4. API Endpoints (`ackbard`)

* `POST /v1/briefings/synthesize`: Returns a structured text and audio briefing payload summarizing active sessions, blockers, and plan diffs.
* `POST /v1/briefings/dispatch`: Accepts transcribed user intent, maps to target session ID, and executes the approval or terminal input injection.
