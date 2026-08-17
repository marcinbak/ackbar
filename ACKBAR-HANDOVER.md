# Ackbar — Handover Document

**Working name:** Ackbar
**Owner:** Marcin Bąk (marcin.bak@modemobile.com), Mode Inc
**Date:** 2026-08-11
**Status:** Pre-implementation. Requirements consolidated and validated; architecture proposed; several decisions open.

**Purpose of this document.** A single self-contained brief that can be handed to another person — or another Claude session — for design brainstorming and then implementation, without re-deriving any of the research behind it. Everything factual here was verified against vendor documentation during research on 2026-08-11; each claim carries a confidence marker.

**Confidence markers used throughout:**

| Marker | Meaning |
|---|---|
| ✅ | Verified against official vendor documentation; URL cited |
| ⚠️ | Verified but with a material caveat, or verified only from community sources |
| ❓ | Assumption or inference — **must be validated empirically before it is relied upon** |

**Component names used throughout:**

| Binary | Role |
|---|---|
| `ackbar` | The control-centre client on the laptop. Aggregates every machine, renders the tree, filters, tabs. |
| `ackbard` | The per-machine daemon. One instance on every machine including the laptop. Ingests agent hook events, owns session state, supervises spawned sessions. |
| `ackbar-hook` | A ~20-line shim invoked by agents whose hooks deliver JSON on stdin rather than over HTTP (Codex, Antigravity). Reads stdin, POSTs to the local `ackbard`, exits. |

*Named for the admiral who commands a fleet from a tactical display — watching units that operate independently and can be advised but not flown. `ACK` also being what the daemon does with every hook event.*

---

## 1. Product goal

A single application giving one developer an overview of, and control over, all AI coding-agent work running across several machines — a local laptop and one or more remote development boxes — where the sessions are **owned by the machines they run on**, not by the client.

The defining constraint, and the reason existing tools were rejected: **a session started on the dev box keeps running on the dev box.** It survives the client being closed, the laptop sleeping, and the network dropping. The client is a window onto work that lives elsewhere.

### 1.1 Why not an existing tool

Evaluated and rejected during research. Summarised here so the decision is not relitigated:

| Option | Why rejected |
|---|---|
| **Claude Code Desktop app** (SSH sessions) ✅ | Closest first-party fit — native SSH sessions, editor pane, sidebar filterable by status/project/environment. But the desktop app *hosts* the session; it is not an observer of an independent one. Also Claude Code only, and its integrated terminal is local-sessions-only. [Docs](https://code.claude.com/docs/en/desktop) |
| **`claude remote-control`** ✅ | Drives a local session from another device; does not aggregate, and is single-agent. [Docs](https://code.claude.com/docs/en/remote-control) |
| **ccmanager, claude-squad, Crystal/Nimbalyst, amux, Conductor** ⚠️ | None covers local+remote *and* an editor *and* multi-project grouping *and* status overview. Several are dead or dying: claude-squad's last release was 2025-03; vibe-kanban's vendor shut down 2026-04. |
| **VelaTerm** ⚠️ | Feature-complete on paper but closed-source, ~7-week-old domain, no ToS, no privacy policy, broken release metadata pointing at a non-existent GitHub repo. Not trustworthy for a tool that wraps shell and agent credentials. See §10.4. |

---

## 2. Consolidated requirements

Requirements are given stable IDs so they can be referenced in later discussion. Grouped by the component that owns them.

### 2.1 Host agent (runs on every machine)

| ID | Requirement | Source |
|---|---|---|
| **H1** | Expose the state of all agent sessions running on this machine | Original + recap |
| **H2** | Define one or more **project roots** — directories under which this machine's projects live | Recap |
| **H3** | Create a new agent session for a specified project, choosing which agent CLI to use | Recap |
| **H4** | Create a new project: an empty folder, or a clone from a git remote | Recap |
| **H5** | Restart an existing session — same conversation, fresh process (e.g. to pick up changed environment variables) | Recap |
| **H6** | Keep sessions running independently of any connected client | Original premise |

### 2.2 Client application (runs on the laptop)

| ID | Requirement | Source |
|---|---|---|
| **C1** | Add and manage multiple machines; the local machine is present by default | Recap |
| **C2** | Present every session with its machine, its agent, and its current state | Recap |
| **C3** | Organise sessions into a user-defined tree of **arbitrary depth** (e.g. `ProjectY → Android → payment-integration`) | Recap |
| **C4** | Each session belongs to a **project**; a project present on several machines appears as **one node** | Recap |
| **C5** | Restart a session from the client | Recap |
| **C6** | Open sessions in **tabs**; closing a tab detaches but does **not** kill the session | Recap |
| **C7** | A dedicated pane or filter for sessions **awaiting a user decision** | Recap |
| **C8** | Archive a session, hiding it from the default view | Recap |
| **C9** | Open the selected session's project in an editor | Earlier discussion — decided: hand off to VS Code |

### 2.3 Cross-cutting

| ID | Requirement | Source |
|---|---|---|
| **X1** | Support Claude Code, OpenAI Codex CLI, and Google Antigravity | Recap |
| **X2** | Remain extensible to further agents without touching the core | Earlier discussion |
| **X3** | No new network attack surface — no inbound ports beyond loopback | Derived from the VelaTerm review; see §9 |

---

## 3. Requirement validation

This is the most important section. Every requirement was checked against verified agent capabilities. Two requirements change the architecture materially and are called out first.

### 3.1 Architecture-changing findings

#### Finding A — H3/H5/C5/C6 turn the daemon from an *observer* into a *supervisor*

The original design was read-only: agents push lifecycle events, the daemon aggregates, the client renders. Creating sessions (H3), restarting them (H5/C5), and attaching to a live terminal (C6) all require the daemon to **own the process lifecycle**. You cannot restart, or attach a terminal to, a process you did not start.

**Consequence — a hard rule for the design:**

> Sessions **spawned by the daemon** are fully manageable: restart, attach, kill.
> Sessions **started manually** by the user (a bare `claude` in some terminal) are **observe-only** — they appear in the tree with state, but restart and attach are unavailable.

The client must surface this distinction honestly (see `Capabilities` in §6.3), not silently disable buttons.

**Recommended mechanism:** the daemon spawns each session inside a **tmux session** it names and tracks. tmux then provides process supervision, detach/reattach semantics, and scrollback for free — and it is what makes C6 ("closing a tab doesn't kill it") almost trivial rather than a bespoke PTY-supervision subsystem. See §7.2 for the alternative.

#### Finding B — C6 conflicts with the previously chosen TUI client

The client form factor was provisionally settled as a terminal TUI. **C6 (tabs of live, attachable terminal sessions) is a poor fit for a TUI**: implementing terminal tabs inside a terminal means reimplementing tmux inside tmux. Two coherent resolutions:

| Option | Shape | Consequence |
|---|---|---|
| **B1 — TUI as dashboard + handoff** | TUI shows the tree, status, and filters. "Open" shells out to `tmux attach` (local) or `ssh -t host tmux attach -t <name>` (remote). Tabs are tmux windows, not app tabs. | Simplest by far. C6 is satisfied by tmux's own semantics. But "tabs" are not *in* your app, and the UX is two tools rather than one. |
| **B2 — GUI (Tauri) with embedded terminals** | xterm.js per tab, PTY relayed from the daemon over WebSocket. Real tabs, real editor pane later. | Matches the stated requirement precisely. Multiplies client effort — this is the single largest scope driver in the project. |

**This is the top open question (§8, Q1).** The recap's language ("tabs like VS Code edit tabs") points at B2; the earlier stack decision points at B1. It needs an explicit call before implementation starts, because it changes the client stack, the daemon's API surface (PTY relay or not), and the timeline by weeks.

### 3.2 Per-requirement verdicts

| ID | Verdict | Notes |
|---|---|---|
| **H1** | ✅ Feasible | All three target agents expose lifecycle hooks. Fidelity varies — see §5. |
| **H2** | ✅ Trivial | Daemon config file. Support a **list** of roots, not one; developers rarely keep everything under a single tree. |
| **H3** | ✅ Feasible, ⚠️ scope | Requires supervisor model (Finding A) and a per-agent launch-command config. Straightforward once tmux is the substrate. |
| **H4** | ✅ Feasible, ⚠️ security | `mkdir` and `git clone` are easy; the security implication is not. This gives the API arbitrary-ish code execution and needs the host's git credentials. Hard requirement: loopback-only binding, reached via SSH tunnel (§9). |
| **H5** | ⚠️ Conditional | Only for daemon-spawned sessions. Resume flags verified per agent (§5.4). **❓ The specific goal — "restart to pick up changed env vars" — must be validated empirically per agent**: resume rehydrates conversation from a transcript, and whether each agent re-reads the environment on resume is not documented. |
| **H6** | ✅ Feasible | tmux, or the daemon double-forking spawned processes. tmux recommended. |
| **C1** | ✅ Feasible | One SSH tunnel per remote host; local daemon reached directly. Local machine runs the *same* daemon binary, so "local" is not a special case in the client. |
| **C2** | ✅ Feasible | Covered by the canonical session model (§6.1). |
| **C3** | ✅ Feasible | Client-side, user-defined tree persisted locally. **Must be kept distinct from the physical host/filesystem tree** — see §6.4, the two-tree model. |
| **C4** | ✅ Feasible, ⚠️ ambiguous | Needs an explicit project identity rule. Proposed in §6.2; known ambiguities listed there (forks, monorepos, git worktrees, coincidental folder names). |
| **C5** | ⚠️ Conditional | As H5. |
| **C6** | ⚠️ Major scope | See Finding B. Cheap under B1, expensive under B2. |
| **C7** | ✅ Feasible, ⚠️ uneven | This is a filter on `Blocked` state. Signal fidelity differs sharply by agent — Claude Code is excellent, Antigravity is a workaround, Codex is mode-gated. See §5.3. |
| **C8** | ✅ Trivial | Client-side flag. Optionally propagate to the agent where it has a native concept (Codex has `codex archive` ✅). |
| **C9** | ✅ Trivial | `code --remote ssh-remote+<host> <path>` for remote, `code <path>` for local. Decided earlier in preference to an embedded editor. |
| **X1** | ⚠️ Two of three solid | Claude Code and Codex both expose full hooks including a permission event. **Antigravity is the weak one** — see §5.2 and Risk R2. |
| **X2** | ✅ By design | Provider interface, §6.5. |
| **X3** | ✅ By design | §9. |

---

## 4. Architecture

```
┌───────────────────────── LAPTOP ─────────────────────────┐
│                                                          │
│   Client (TUI or GUI — Q1)                               │
│     • logical group tree (user-defined, persisted here)  │
│     • session list + status + filters                    │
│     • tabs / attach                                      │
│     • "open in VS Code" handoff                          │
│         │                    │                           │
│         │ HTTP + SSE         │ HTTP + SSE                │
│         ▼                    ▼ (through SSH tunnel)      │
│   ┌──────────────┐     ═══════════════════════════════╗  │
│   │ ackbard      │                                    ║  │
│   │ (localhost)  │                                    ║  │
│   └──────────────┘                                    ║  │
└───────────────────────────────────────────────────────╫──┘
                                                        ║
              ssh -N -L 7777:127.0.0.1:7777 devbox      ║
                                                        ║
┌───────────────────────── DEV BOX ──────────────────────╫──┐
│                                                        ▼  │
│   ┌────────────────────────────────────────────────────┐  │
│   │ ackbard  (binds 127.0.0.1 only)                    │  │
│   │   ├── ingest: POST /v1/hooks/{agent}               │  │
│   │   ├── providers (per agent, own their transport)   │  │
│   │   ├── state machine  → SQLite                      │  │
│   │   ├── control: spawn / restart / kill              │  │
│   │   └── serve: GET /v1/sessions, /v1/events (SSE)    │  │
│   └────────────────────────────────────────────────────┘  │
│         ▲                            │                    │
│         │ hook events                │ spawns             │
│         │                            ▼                    │
│   ┌─────┴──────────┐        ┌──────────────────┐          │
│   │ claude / codex │◄───────┤ tmux sessions    │          │
│   │ / agy processes│        └──────────────────┘          │
│   └────────────────┘                                      │
└───────────────────────────────────────────────────────────┘
```

**Key property:** the same `ackbard` binary runs on every machine including the laptop. The client talks to N daemons over identical APIs; "local" is just the one at `127.0.0.1` with no tunnel. This keeps the client free of special cases and means anything that works remotely works locally.

---

## 5. Agent integration matrix

All findings verified 2026-08-11.

### 5.1 The good news

The ecosystem has converged on Claude Code's hook design. Codex, Gemini CLI, Cursor CLI, and Copilot CLI all now ship a `hooks.json` with near-identical semantics — session id, cwd, transcript path, event name, and `SessionStart` / `PreToolUse` / `PostToolUse` / `Stop`. **Adding a new agent is largely field renaming**, on the order of 30 lines per adapter.

### 5.2 Per-agent detail (the three required agents)

#### Claude Code — reference implementation, best instrumented ✅

- **Hook config:** `~/.claude/settings.json` (applies to all sessions on the machine), or per-project. Hooks from different sources merge rather than replace. [Docs](https://code.claude.com/docs/en/hooks)
- **Unique advantage: native `type: "http"` handler.** Alone among the three, it POSTs directly to a URL — no per-event process spawn. Config:
  ```json
  { "hooks": { "Stop": [ { "hooks": [ {
      "type": "http",
      "url": "http://127.0.0.1:7777/v1/hooks/claude-code",
      "timeout": 5
  } ] } ] } }
  ```
- **Common payload fields:** `session_id`, `cwd`, `transcript_path`, `hook_event_name`, `permission_mode`, `prompt_id`, plus `agent_id` / `agent_type` inside subagents.
- **Events relevant here:** `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PermissionRequest`, `Notification`, `Stop`, `SubagentStart`, `SubagentStop`.
- **Blocked signal — three independent sources**, the richest of any agent: the `Notification` event (matchers `agent_needs_input`, `permission_prompt`), the dedicated `PermissionRequest` event, and `AskUserQuestion` appearing as `tool_name` in `PreToolUse`. ⚠️ Community reports conflict on whether `PreToolUse` fires for `AskUserQuestion` on all versions ([#12031](https://github.com/anthropics/claude-code/issues/12031)) — but `Notification` and `PermissionRequest` make that redundant.
- **Bonus poll surface:** `claude agents --json [--all] [--cwd <path>]` returns `sessionId`, `name`, `cwd`, `state` (`working` / `blocked` / `done` / `failed` / `stopped`), `waitingFor`, `pid`, `startedAt`, `kind`. ⚠️ Covers **background** sessions only, not interactive ones in other terminals unless backgrounded. Useful for backfill on daemon restart. [Docs](https://code.claude.com/docs/en/agent-view)
- **Transcripts:** `~/.claude/projects/<slug>/<session-id>.jsonl`. ⚠️ Docs state the format is internal and changes between versions — use for discovery and last-activity timestamps, not deep parsing.

#### OpenAI Codex CLI — strong second ✅

- **Hook config:** `~/.codex/hooks.json`, `<repo>/.codex/hooks.json`, or inline `[hooks]` tables in `config.toml`. `CODEX_HOME` defaults to `~/.codex`. [Docs](https://learn.chatgpt.com/docs/hooks)
- **11 events:** `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, **`PermissionRequest`**, `PostToolUse`, `PreCompact`, `PostCompact`, `SubagentStart`, `SubagentStop`, `Stop`.
- **Transport:** `type: "command"` — JSON arrives on **stdin**, not HTTP. Requires the shim (§7.1).
- **Payload:** `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`, `turn_id`, `permission_mode`, plus event-specific `tool_name` / `tool_input` / `tool_use_id`.
- **Blocked signal:** `PermissionRequest` — first-class. ⚠️ Its question tool `request_user_input` is **gated to Plan mode by default**, so question-blocking may be invisible in normal mode.
- **Also available:** `~/.codex/sessions/YYYY/MM/DD/rollout-<id>.jsonl`; `codex exec --json` (NDJSON); `codex app-server` (JSON-RPC with `thread/list`, `turn/*`, `item/*/requestApproval`). ⚠️ `thread/list` reflects *stored* threads, not live ones.
- **Legacy `notify`:** `notify = ["python3", "/path/notify.py"]` in `config.toml`, fires only `agent-turn-complete`. Superseded by hooks; ignore.

#### Google Antigravity — the weak link ⚠️

- **Hook config:** `hooks.json` in `.agents/` (workspace) or `~/.gemini/config/`. [Docs](https://antigravity.google/docs/hooks)
- **Only 5 events:** `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop`. **No `SessionStart` / `SessionEnd`, no permission-request event.**
- **Payload shape differs from every other agent** — this will silently break a Claude-shaped parser:
  ```json
  {
    "toolCall": { "name": "run_command", "args": { "CommandLine": "npm test", "Cwd": "..." } },
    "stepIdx": 19,
    "conversationId": "ec33ebf9-...",
    "workspacePaths": ["/workspace/project"],
    "transcriptPath": "~/.gemini/antigravity/brain/<id>/.system_generated/logs/transcript.jsonl",
    "modelName": "gemini-3.6-flash-medium"
  }
  ```
  Note `toolCall.name`, **not** `tool_name`. And `workspacePaths` is an **array**, not a scalar `cwd`.
- **Blocked signal — the workaround (Marcin's hypothesis, confirmed):** Antigravity has built-in tools **`ask_question`** ("Ask multiple-choice questions") and **`ask_permission`** ("Request additional scoped permissions"). `matcher` is a **regex**, so `"ask_question|ask_permission"` on `PreToolUse` yields a blocked signal despite the missing permission event. ✅ Tools confirmed in official docs.
- ⚠️ **Risk:** `PostToolUse` is documented only as "fires after a tool completes" — no ordering guarantee, nothing about denied or abandoned tools, and it carries no result field (only `toolCall` + optional `error`). **Do not rely on Pre/Post pairing.** See §7.3.
- ⚠️ **Risk:** an [unresolved forum thread](https://discuss.ai.google.dev/t/do-antigravity-ide-2-0-actually-execute-plugin-hooks-pretooluse-posttooluse-or-is-that-cli-only-right-now/176814) reports hooks firing **only in the `agy` CLI**, with zero invocations from the Antigravity IDE and the 2.0 desktop app. Changelog v2.6.0 (2026-08-07) mentions hook fixes, consistent with them having been partly broken. **Validate on the exact surface being targeted.**
- **Session/conversation resume:** `--conversation <id>`, `--continue` / `-c`. ✅
- ❓ Conversation storage path and format are undocumented; `transcriptPath` from the hook payload is the practical handle.

### 5.3 Blocked-signal fidelity — the honest summary

C7 ("show me what's waiting on me") is the highest-value feature and the one whose quality varies most:

| Agent | Blocked signal | Quality |
|---|---|---|
| Claude Code | `Notification` + `PermissionRequest` + `AskUserQuestion` tool | **Excellent** |
| Codex CLI | `PermissionRequest` event | **Good** (question tool mode-gated) |
| Antigravity | `PreToolUse` matching `ask_question\|ask_permission` | **Workaround** — no permission event; pairing unreliable |

For context, agents **not** in scope but relevant if X2 is exercised: OpenCode is the best of all (a real HTTP server on :4096 with SSE `permission.updated` / `session.idle`); Gemini CLI's hooks [fire too late to be useful](https://github.com/google-gemini/gemini-cli/issues/20605); Cursor CLI's `AskQuestion` [does not fire hooks at all](https://forum.cursor.com/t/cursor-cli-askquestion-tool-skips-pretooluse-and-posttooluse-hooks/161836) — a vendor-confirmed bug; Aider has no structured surface whatsoever.

### 5.4 Restart / resume flags (for H5)

| Agent | Resume mechanism | Confidence |
|---|---|---|
| Claude Code | `claude --resume <session-id>`, `claude --continue` | ✅ documented |
| Codex CLI | `codex exec resume [SESSION_ID] [--last]` | ✅ documented |
| Antigravity | `--conversation <id>`, `--continue` / `-c` | ✅ documented |

❓ **Unvalidated in all three:** whether resuming re-reads the process environment, which is the actual point of H5. The mechanism is sound — kill the process, re-exec with a fresh environment, pass the resume flag — but confirm empirically before promising the feature.

---

## 6. Domain model

### 6.1 Canonical session

Deliberately the **lowest common denominator** across agents. Vendor-specific concepts must never leak in.

```go
type Session struct {
    // Identity
    ID        string    // "{agent}:{host}:{nativeID}" — namespaced, see §7.4
    Agent     string    // "claude-code" | "codex" | "antigravity"
    Host      string    // machine alias, e.g. "devbox"
    NativeID  string    // agent's own session/conversation id

    // Location
    Cwd       string    // primary working directory
    Roots     []string  // Cursor/Antigravity send arrays; keep both
    ProjectKey string   // see §6.2
    GitBranch string
    Worktree  string    // if applicable

    // State
    State        State     // see below
    Blocked      *Blocked  // populated iff State == Blocked
    Activity     string    // one-line "what it's doing now"
    StartedAt    time.Time
    LastEventAt  time.Time // drives staleness — see §7.3

    // Management
    Managed      bool      // daemon-spawned → restart/attach available
    TmuxName     string    // if managed
    PID          int
    Archived     bool
}

type State int
const (
    StateUnknown State = iota  // provider cannot tell — never guess
    StateWorking               // actively running tools / generating
    StateBlocked               // waiting on the human
    StateIdle                  // turn finished, awaiting next prompt
    StateEnded                 // session over
    StateFailed
)

type Blocked struct {
    Kind   BlockKind // Permission | Question
    Reason string    // free text, e.g. tool name or question summary
    Since  time.Time
}
```

Two notes that come from hard-won findings:

- **`StateUnknown` must exist and must be used.** Some providers genuinely cannot distinguish idle from blocked. Rendering `unknown` is correct; rendering a confident `idle` is a lie the user will act on.
- **`Blocked.Kind` distinguishes `Permission` from `Question`.** They warrant different urgency in the UI, and Antigravity surfaces them as two different tools.

### 6.2 Project identity (for C4)

The rule for deciding that a directory on host A and a directory on host B are "the same project":

```
projectKey = normalise(git remote "origin" URL)     // preferred
          ?? "path:" + basename(cwd)                 // fallback
```

Normalisation must strip protocol, credentials, trailing `.git`, and case — so `git@github.com:mode/app.git` and `https://github.com/Mode/app` collapse to `github.com/mode/app`.

⚠️ **Known ambiguities to decide on (see Q4):**

- **Forks and mirrors** collapse into one node — sometimes wanted, sometimes not.
- **Monorepos**: two sessions in different subdirectories of one repo get the same key. Probably correct, but it means the tree needs to disambiguate below project level.
- **Git worktrees**: same remote, different branch and path. Claude Code creates worktrees automatically per session — this case will be common and needs an explicit display decision.
- **Coincidental folder names** under the path fallback (`~/work/api` on two machines being genuinely different projects).

Recommendation: compute the key automatically, but let the user **manually merge or split** project nodes in the client, persisted locally. Automatic matching will be right most of the time and wrong often enough to need an escape hatch.

### 6.3 Capabilities

Advertised per provider **and** per session, so the client can degrade honestly rather than presenting dead controls:

```go
type Capabilities struct {
    LiveStatus     bool // pushes events, vs poll-only
    BlockedSignal  bool // can detect waiting-on-user at all
    BlockedKind    bool // can distinguish permission from question
    Transcript     bool
    CanRestart     bool // implies Managed
    CanAttach      bool // implies Managed
    CanList        bool // has a native list-sessions command
}
```

### 6.4 The two-tree model (for C3 + C4)

A point worth stating explicitly because conflating these is the most likely design error:

- **The physical tree** — host → project root → directory — is discovered by the daemon and is not user-editable.
- **The logical tree** — `ProjectY → Android → payment-integration`, arbitrary depth — is **created by the user, owned by the client, and persisted on the laptop**. It groups *projects*, not directories.

Sessions attach to a Project. Projects attach to a node in the logical tree. A project that exists on three machines is **one** logical node with sessions from three hosts underneath it — which is exactly what C4 asks for, and only works if the two trees are kept separate.

The logical tree is client-side state. It should be a plain JSON or SQLite file on the laptop, trivially backed up and hand-editable.

### 6.5 Provider interface (for X2)

The critical abstraction. **Do not model providers as "hook receivers"** — OpenCode (and any future server-based agent) breaks that immediately by requiring the daemon to dial *out* and subscribe. Model a provider as an event source that owns its own transport:

```go
type Provider interface {
    Agent() string
    Caps() Capabilities
    Run(ctx context.Context, emit func(Event)) error
    // optional, when Caps().CanRestart:
    Spawn(ctx context.Context, req SpawnRequest) (Session, error)
    Restart(ctx context.Context, sessionID string) error
}
```

Claude Code's provider registers an HTTP route and waits. Codex's registers a route the shim posts to. A future OpenCode provider dials `http://127.0.0.1:4096/event` and subscribes to SSE. An Aider provider would poll `/proc`. The core never knows the difference.

### 6.6 Canonical event set

The only vocabulary the state machine understands. Vendor payloads are translated at the edge by per-agent adapters and never reach the core.

```
SessionObserved{ agent, nativeID, host, cwd, roots[], transcriptPath, startedAt }
TurnStarted
ToolStarted{ name, stepRef }
ToolFinished{ name, stepRef, error? }
Blocked{ kind, reason, stepRef }
Unblocked{ stepRef }
TurnFinished
SessionEnded{ reason }
```

---

## 7. Key design decisions

### 7.1 Hook transport — native HTTP where available, shim elsewhere

Only Claude Code offers `type: "http"`. Codex and Antigravity use command hooks receiving JSON on stdin. Ship a small shim binary alongside the daemon:

```
ackbar-hook --agent codex --url http://127.0.0.1:7777/v1/hooks/codex
```

It reads stdin, POSTs, exits. ~20 lines, same repository, same release artifact.

**Use native HTTP for Claude Code** rather than the shim: hooks sit in the agent's critical path, and avoiding a process spawn per event matters.

**Corollary — a hard performance rule:** the ingest endpoint must accept, enqueue, and return `200` in single-digit milliseconds. All real work — state transitions, SQLite writes, SSE fan-out — happens off the request path. A slow hook stalls the agent mid-turn.

### 7.2 Process substrate — tmux

Sessions spawned by the daemon run inside named tmux sessions. This buys process supervision, detach/reattach, and scrollback for free, and makes C6 almost trivial. It also means a user can reach a session with plain `tmux attach` when the daemon is down — a valuable property.

**Alternative considered:** the daemon owns PTYs directly and relays them over WebSocket. More control, no tmux dependency, but it means writing PTY supervision, scrollback buffering, and resize handling. Recommended only if Q1 resolves to B2 (GUI) *and* the tmux dependency proves awkward.

### 7.3 Self-healing state machine — do not trust event pairing

Antigravity documents no ordering guarantee for `PreToolUse`/`PostToolUse`, says nothing about denied or abandoned tools, and provides no result payload. Claude Code has version-dependent quirks around `AskUserQuestion`. **A strict bracket-matching state machine will get stuck showing `blocked` forever.**

Design the transition rules so a pending block closes on **any** of:

1. A matching `ToolFinished` (paired on `stepIdx` / `tool_use_id`)
2. `TurnFinished` for the same session
3. The next `TurnStarted` or `ToolStarted` for the same session
4. A timeout

Additionally, every session carries `LastEventAt`, and the client applies a **staleness rule**: a session claiming `Blocked` with no events for hours renders as `blocked · 3h · stale`, not as a confident badge. With signal fidelity this uneven, honest degradation matters more than precision.

### 7.4 Namespaced identifiers

Session IDs are `{agent}:{host}:{nativeID}`. Trivial now, painful to retrofit once state is persisted.

### 7.5 Editor — hand off to VS Code

`code --remote ssh-remote+<host> <path>` for remote projects, `code <path>` for local. One line of code for the real editor with full editing and language servers.

**Alternative considered and deferred:** embedding code-server in a webview (only viable under B2). It would give genuine VS Code inside the app and eliminate any file-reading-over-SSH code, since code-server already sits on the box with the files. Costs: a few hundred MB per host, and extensions come from Open VSX rather than Microsoft's marketplace (Microsoft's terms don't permit third-party products), so Pylance and the official C/C++ extension are unavailable. Worth revisiting only after the tree works.

### 7.6 Stack

Provisionally **Go** for the daemon — best-in-class cross-compilation to the dev box (`GOOS=linux go build`, scp one static binary), stdlib `net/http` is sufficient, goroutines map cleanly onto N ingest handlers → one state owner → M SSE subscribers, and `modernc.org/sqlite` keeps the binary cgo-free.

Rust was the runner-up; its stronger case (an `enum` state machine with exhaustive matching, and compiling into a future Tauri backend) is weakened by the fact that the *remote* daemon must remain a standalone service regardless, so an in-process laptop daemon would create two asymmetric code paths.

⚠️ **The client stack is not settled and depends on Q1.**

---

## 8. Open questions for brainstorming

| # | Question | Why it matters |
|---|---|---|
| **Q1** | **Client form factor: TUI + tmux handoff (B1), or Tauri GUI with embedded terminals (B2)?** | The single biggest decision. Determines client stack, whether the daemon needs a PTY relay endpoint, and whether this is a two-week or two-month project. C6 as written points at B2. |
| **Q2** | Should the daemon manage *only* sessions it spawned, or also try to adopt manually-started ones? | Adoption is possible (hooks fire regardless of who started the process) but such sessions can't be restarted or attached. Affects how prominent the `Managed` distinction must be in the UI. |
| **Q3** | Is per-agent launch configuration per-host, per-project, or both? | Different projects need different models, permission modes, env. Suggest per-host defaults overridable per-project. |
| **Q4** | How should project-identity collisions be resolved? (§6.2) | Forks, monorepos, and git worktrees all collide under the proposed rule. Manual merge/split is proposed; confirm it's enough. |
| **Q5** | Does archiving (C8) propagate to the agent, or stay client-side? | Codex has a native `codex archive`; Claude Code has archived sessions in agent view. Propagating is tidier but couples the client to agent state. |
| **Q6** | Multi-client: will more than one client ever watch the same daemon? | Affects whether logical-tree state and archive flags belong on the laptop or the daemon. Currently assumed single-client. |
| **Q7** | What is the notification story for blocked sessions? | The whole value of C7 is *not having to look*. Needs a decision: OS notification from the local daemon, terminal bell, or push to phone. |
| **Q8** | Is Antigravity a hard V1 requirement, given Risk R2? | If its hooks don't fire on the target surface, the requirement can't be met regardless of design quality. |

---

## 9. Security model

The whole project began with a security review that rejected VelaTerm partly for a weak remote-access design (self-signed TLS, trust-on-first-use, shared password on a LAN port). It would be ironic to reproduce that. The following are treated as **requirements, not preferences**:

1. **`ackbard` binds `127.0.0.1` only.** Never `0.0.0.0`, never a LAN interface, no configuration option to do so.
2. **All remote access is via SSH port-forward**: `ssh -N -L 7777:127.0.0.1:7777 devbox`. Authentication, encryption, and key management are SSH's job. **Do not invent a token scheme, a TLS story, or a pairing flow.**
3. This is not incidental hardening — it is what makes H4 acceptable. That endpoint runs `git clone` with the host's credentials and spawns processes; it is a remote-code-execution surface by design and must never be reachable by anything but a local SSH-authenticated tunnel.
4. **Hook payloads are untrusted input.** They contain prompt text, tool arguments, and file paths from whatever the agent is doing. Treat as data — never interpolate into shell commands, never render as markup without escaping.
5. If off-LAN access is wanted later, **Tailscale** is the answer, not a public listener.

---

## 10. Risks

| ID | Risk | Severity | Mitigation |
|---|---|---|---|
| **R1** | **C6 scope explosion.** Embedded terminal tabs (B2) is the majority of client effort and is easy to underestimate. | **High** | Resolve Q1 before writing client code. Consider shipping B1 first and treating B2 as a later, informed decision. |
| **R2** | **Antigravity hooks may not fire** on the IDE or 2.0 desktop — [unresolved](https://discuss.ai.google.dev/t/do-antigravity-ide-2-0-actually-execute-plugin-hooks-pretooluse-posttooluse-or-is-that-cli-only-right-now/176814). X1 depends on them. | **High** | **Validate first, before any Antigravity work.** Write a hook that appends to a file, run one session on the target surface, confirm. If it fails, Antigravity support is CLI-only or deferred. |
| **R3** | **H5 may not achieve its actual goal.** Resume flags exist ✅, but whether resuming re-reads env vars is undocumented ❓. | Medium | Empirical test per agent, early. Fall back to "kill and start fresh session in same project" if resume won't pick up the environment. |
| **R4** | **Vendor hook APIs are young and changing.** Cursor's question tool doesn't fire hooks; Gemini's fire at the wrong time; Codex's question tool is mode-gated; Antigravity's changelog shows recent hook fixes. | Medium | The adapter layer contains the blast radius. Pin behaviour with a per-agent conformance test that runs a real session and asserts which events arrive. |
| **R5** | **Transcript formats are explicitly unstable.** Claude Code's docs say the JSONL format is internal and changes between versions. | Low | Use transcripts only for discovery and timestamps. Never parse them for state. |
| **R6** | **tmux becomes a hard dependency** on every host. | Low | Acceptable — it's on every dev box already. Document it as a prerequisite. |
| **R7** | **Single-user assumptions** may not survive if this is ever shared with the team. | Low | Note it; don't design for it now. |

### 10.4 Context: the VelaTerm review

This project began as a security assessment of [velaterm.com](https://velaterm.com/), a closed-source Tauri terminal/agent manager. Summary, since it explains several decisions above:

- Vendor **VLINX Software, Inc.** is real and ~9 years old (Protector4J since 2017), which reduces fly-by-night risk.
- But: **no terms of service, no privacy policy, no EULA**; domain registered 2026-06-20 (~7 weeks old at review); no source available.
- Its release API pointed at `github.com/vlinx/vlx-term` — **a repository that does not exist**, under a GitHub username belonging to an unrelated individual. The only working OTA channel was on a personal account, with signed metadata that **expired 2026-07-22**.
- No negative reports found anywhere — but also almost no independent scrutiny (one 6-point HN thread, two influencer tweets). "Clean record" meant "unexamined," not "vetted."

The conclusion — that a tool sitting at maximum privilege over your shell, SSH keys, and agent credentials needs open source, a real legal document, or an established reputation, and VelaTerm had none of the three — is what motivated building this instead.

---

## 11. Suggested phasing

Deliberately sequenced so the riskiest unknowns are tested before the expensive work starts.

### Phase 0 — De-risk (do this first, ~half a day)

- Confirm Antigravity hooks fire on the target surface (R2)
- Confirm each agent's resume picks up a changed environment variable (R3)
- Confirm Claude Code's `type: "http"` hook round-trips to a trivial local listener

**These three tests determine whether X1 and H5 are achievable as written.** Everything else is wasted effort if they fail.

### Phase 1 — Read-only aggregation

`ackbard` with: canonical model, provider interface, Claude Code adapter, self-healing state machine, SQLite, `GET /v1/sessions`, `GET /v1/events` (SSE). Client shows the flat session list across local + one remote host over an SSH tunnel, with a blocked filter (C7) and "open in VS Code" (C9).

*Delivers: H1, H6, C1, C2, C7, C9, X3.*

### Phase 2 — Organisation

Project identity and merging (C4), the logical group tree (C3), archiving (C8), project roots and discovery (H2).

*Delivers: H2, C3, C4, C8.*

### Phase 3 — Control

Supervisor mode: tmux-based spawn (H3), project creation and git clone (H4), restart (H5/C5). Requires the §9 security model to be in place first.

*Delivers: H3, H4, H5, C5.*

### Phase 4 — Attach

Tabs and live terminals (C6), in whichever form Q1 resolved to.

*Delivers: C6.*

### Phase 5 — More agents

Codex adapter, then Antigravity (conditional on Phase 0). Should be ~30 lines each plus a conformance test if the abstraction is right — this phase is the test of whether X2 was designed correctly.

*Delivers: X1, X2.*

---

## Appendix A — Reference URLs

**Claude Code:** [Hooks](https://code.claude.com/docs/en/hooks) · [Agent view](https://code.claude.com/docs/en/agent-view) · [Sessions](https://code.claude.com/docs/en/sessions) · [Desktop app](https://code.claude.com/docs/en/desktop) · [Remote Control](https://code.claude.com/docs/en/remote-control) · [Tools reference](https://code.claude.com/docs/en/tools-reference) · [Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview)

**Codex CLI:** [Hooks](https://learn.chatgpt.com/docs/hooks) · [CLI reference](https://learn.chatgpt.com/docs/cli/reference) · [app-server](https://learn.chatgpt.com/docs/app-server.md) · [Advanced config](https://developers.openai.com/codex/config-advanced)

**Antigravity:** [Hooks](https://antigravity.google/docs/hooks) · [Headless CLI](https://antigravity.google/docs/cli/headless) · [Permissions](https://antigravity.google/docs/permissions) · [Changelog](https://antigravity.google/changelog) · [Hook-execution forum thread](https://discuss.ai.google.dev/t/do-antigravity-ide-2-0-actually-execute-plugin-hooks-pretooluse-posttooluse-or-is-that-cli-only-right-now/176814)

**Other agents (for X2):** [OpenCode server API](https://opencode.ai/docs/server/) · [Gemini CLI hooks](https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md) · [Cursor hooks](https://cursor.com/docs/hooks) · [Copilot CLI hooks](https://docs.github.com/en/copilot/reference/hooks-configuration)

**Prior art reviewed:** [ccmanager](https://github.com/kbwo/ccmanager) · [claude-squad](https://github.com/smtg-ai/claude-squad) *(likely abandoned)* · [amux](https://github.com/mixpeek/amux) · [Nimbalyst](https://github.com/nimbalyst/nimbalyst) · [coder/mux](https://github.com/coder/mux) · [claudecodeui](https://github.com/siteboon/claudecodeui) · [code-server](https://github.com/coder/code-server)

---

## Appendix B — Requirement traceability

| Req | Phase | Verdict | Key risk |
|---|---|---|---|
| H1 | 1 | ✅ | — |
| H2 | 2 | ✅ | — |
| H3 | 3 | ✅ | Supervisor model |
| H4 | 3 | ✅ | Security (§9) |
| H5 | 3 | ⚠️ | R3 — env vars on resume |
| H6 | 1 | ✅ | — |
| C1 | 1 | ✅ | — |
| C2 | 1 | ✅ | — |
| C3 | 2 | ✅ | Two-tree separation |
| C4 | 2 | ⚠️ | Q4 — identity collisions |
| C5 | 3 | ⚠️ | As H5 |
| C6 | 4 | ⚠️ | **R1 — largest scope item; blocked on Q1** |
| C7 | 1 | ✅ | Fidelity varies by agent |
| C8 | 2 | ✅ | — |
| C9 | 1 | ✅ | — |
| X1 | 5 | ⚠️ | **R2 — Antigravity hooks** |
| X2 | 5 | ✅ | — |
| X3 | 1 | ✅ | Non-negotiable |
