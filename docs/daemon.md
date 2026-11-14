# Ackbar Daemon Engine (`ackbard`)

## 1. Responsibilities

The `ackbard` daemon is the central backend running on every monitored machine (local workstation and remote compute servers).

### Key Subsystems:
1. **HTTP Control Plane:** Exposes REST endpoints for session listing, lifecycle controls (`kill`, `restart`, `move`, `rename`, `delete`), host management, project node creation, and standalone commands (`shell`, `config`, `clear`, `compact`, `inspect`, `diff`, `rewind`). Hardened with local origin validation (`isTrustedLocalOrigin`) blocking cross-origin mutating requests with `403 Forbidden`.
2. **Web Dashboard & PWA Frontend:** Serves an embedded desktop/mobile Web GUI with tabs, real-time live terminal emulation (`xterm.js`), and collapsible project trees.
3. **PTY WebSocket Multiplexer (`/v1/sessions/pty`):** Attaches in-place to running tmux panes using binary WebSocket frames and bidirectional keepalive heartbeats. Resizing is deduplicated so the agent only gets `SIGWINCH` on real size changes (see [PTY Terminal Sizing](#pty-terminal-sizing-v1sessionspty)).
4. **Hook Ingestion Engine:** Ingests live telemetry hooks from agents (`/v1/hooks/<agent>`). Responses are sent immediately (`200 OK`) after synchronously reserving a slot in a per-session FIFO queue (`reserveSessionHook` keyed by `agent:nativeID`), ensuring asynchronous processing strictly preserves arrival order and prevents out-of-order `PostToolUse` or `PreToolUse` hooks from clobbering `Stop` (`StateIdle`) or `PermissionRequest` (`StateBlocked`) transitions.
5. **SSE Broadcaster (`/v1/events`):** Streams real-time updates to connected Web and Mobile clients over `/v1/events`. Flushes HTTP response headers immediately upon connection and emits periodic `: keepalive\n\n` comments (`20s` default, configurable via `Server.SetEventsKeepaliveInterval` in [`server.go`](../internal/daemon/server.go)) to keep client connections alive.
6. **SQLite Persistence:** Stores session state, activity history, and logical tree hierarchies in `~/.config/ackbar/ackbard.db` (CGO-free pure Go SQLite).
7. **Built-in Self-Rotating Logger:** Automatically writes, caps (10MB), and daily-rotates daemon logs in `~/.config/ackbar/logs/` with 7-day automatic retention pruning.
8. **Background Session Scanner:** Periodically inspects `tmux list-panes` and on-disk conversation directories (`session_scanner.go`) to reconcile and supervise Ackbar-managed agent sessions (external unmanaged sessions created outside Ackbar are ignored and pruned on startup).

### Modular Daemon File Layout (`internal/daemon/`)
* **`server.go`:** Core `Server` struct, HTTP `Mux()` route registration, token `authMiddleware`, CORS wrapper, SSE `/v1/events` broadcaster with immediate header flush and `: keepalive\n\n` heartbeat (20s default), background loop, version/update/settings/shutdown handlers, and normalized OS hostname fallback (`DefaultSystemHostName`).
* **`server_hooks.go`:** Hook ingestion (`/v1/hooks/<agent>`), per-session FIFO serialization (`reserveSessionHook`), managed-only session resolution, spawning session adoption, in-place `/clear` / `/reset` turn rotation, and hook-driven subagent tracking.
* **`server_sessions.go`:** Session listing, maintenance purge, remote-host proxy resolution (`resolveSessionOrProxy`), lifecycle control actions (`/v1/sessions/control`), session configuration (`PATCH /v1/sessions/{id}/config`), interactive response dispatch (`applyInteractiveResponse`, `/v1/sessions/respond`), mode switching between headless and tmux (`/v1/sessions/take-wheel` and `/v1/sessions/switch-headless`), session spawn (`/v1/sessions/spawn`), and meta-routing (`/v1/meta/resolve`).
* **`server_suggestions.go`:** Slash command (`/`), workspace file (`@`), and agent-aware model suggestions endpoint (`GET /v1/sessions/{id}/suggestions?kind=commands|files|models&q=...`) with 10-second directory caching, 5-minute Antigravity model discovery caching, and bounded `git ls-files` / `filepath.WalkDir` traversal.
* **`server_chat.go`:** Headless/tmux prompt submission (`/v1/sessions/prompt` with `delay_seconds`, `deliver_at`, inline `/delay <duration> <prompt>` scheduling, and `client_turn_id` deduplication), prompt queue management (`GET`/`DELETE`/`POST` `/v1/sessions/prompt/queue` with `action: send_now`), `scheduled_prompts` SQLite persistence, SSE chat streaming (`/v1/sessions/chat/stream`), turn cancellation, automated session handover (`/v1/sessions/handover`), and transcript endpoints.
* **`transcript_cache.go`:** Bounded in-memory transcript cache (128 entries) with `(size, mtimeNano)` validation, incremental Claude `.jsonl` seek-append parsing, and 3-second subagent discovery caching.
* **`prompt_dedup.go`:** Bounded in-memory LRU prompt deduplication cache (512 entries, 10m TTL) tracking in-flight reservations and committed status for idempotent `client_turn_id` submissions.
* **`headless.go`, `headless_persistent.go`, `headless_spool.go`:** Headless coordinator, warm worker lifecycle management, turn dispatch/reuse, and monotonic NDJSON turn spooling.
* **`headless_worker.go`, `headless_config.go`, `headless_worker_ipc.go`:** Detached `--headless-worker` process, persistent stdio stream loop, configuration fingerprinting, and Unix socket RPC.
* **`headless_commands.go`:** Inline shell execution (`POST /v1/sessions/{id}/shell`), directory validation (`/add-dir`), in-place conversation rotation, `/clear`, `/reset`, and `/compact`.
* **`headless_commands_inspect.go`:** Allowlisted CLI inspection (`/v1/sessions/{id}/inspect`), git diff inspection (`/v1/sessions/{id}/diff`), and transcript token/cost metrics.
* **`headless_rewind.go`:** Human turn rewind listing and execution (`/v1/sessions/{id}/rewind`), transcript cloning, and history seeding.
* **`headless_handover.go`:** Deterministic 4-point handover briefing synthesis and headless context compaction.
* **`headless_auth_detect.go`, `headless_auth_broker.go`, `headless_auth_flow.go`:** Detects OAuth authentication blocks (`BlockAuth`), manages allowlisted auth processes, relays loopback/stdin callbacks, and auto-resumes sessions.
* **`server_mcp_status.go`:** Inspects configured MCP servers, probes compiled plugin authentication status, and caches status responses with 10s TTL.
* **`headless_reattach.go` & `headless_stream_replay.go`:** Daemon reboot worker recovery, socket re-attachment, and gapless SSE replay (`?after_seq`, `Last-Event-ID`).
* **`session_done.go`:** Manual `Done` runtime cleanup, process teardown, spool pruning, and periodic `auto_done` sweep.
* **`sleep_inhibitor.go`:** Cross-OS POSIX idle-sleep inhibitor (`caffeinate` / `systemd-inhibit`) active during running headless turns.
* **`rate_limit.go`:** Rate-limit reset timestamp parsers (pipe-delimited epoch, relative durations, 12h/24h/month clock times with past rollover and +1m safety buffer) and `/delay` duration/slash-command parsers.
* **`server_subagents.go`:** Subagent discovery endpoint (`/v1/sessions/subagents`) and in-memory/disk subagent lifecycle reconciliation.
* **`server_accounts.go`:** Multi-account profile management (`/v1/accounts`), agent discovery (`/v1/agents/discovery`), provider catalog (`/v1/providers`), and fleet propagation.
* **`server_hosts.go`:** Remote host health tracking, SSH target resolution, host CRUD (`/v1/hosts`), cross-compiled remote upgrades (`/v1/hosts/update`), and resilient SSH tunnel management (`/v1/hosts/reconnect`, `ensureHostTunnels`).
* **`server_projects.go`:** Workspace document discovery (`/v1/documents`), tree node CRUD and move (`/v1/nodes`), project creation (`/v1/projects/create`), worktree/path normalization, and VS Code URI launching (`/v1/editor/open`).
* **`server_tasks.go` & `task_scope.go`:** Task CRUD (`/v1/tasks`), canonical project node scope resolution (`resolveTaskNodeScope`), task moves, and subproject normalization.
* **`server_deliverables.go`, `db_deliverables.go`, `deliverables_retro.go`:** Task deliverable endpoints (`/v1/deliverables`, `/v1/retros`, `/v1/tasks/deliverable`), rune-safe cursor pagination, auto-ingested `.retro.md` & `run-state.json` parsing, and LWW deliverable replication.
* **`server_sync.go`:** Uniform 3-way Last-Write-Wins (LWW) peer-to-peer sync engine (`/v1/sync/state`, `/v1/sync/push`, `/v1/nodes/paths`) and background delta reconciliation.
* **`session_scanner.go`:** Liveness verification (`verifySessionLiveness`) and background session scanner (`scanObservedSessions` decomposed into `refreshExistingSessions`, `scanTmuxPanes`, and `scanDiskSessions` for managed sessions).
* **`telemetry_tasks.go` & `workflow_watcher.go`:** Monotonic workflow node (`N0`–`N15`) and task/workstream status synchronization (`SyncSessionTaskWorker`, `IngestBoundTaskTelemetry`), canonical pillar substatus normalization (`normalizeTaskStatusAndSubstatus`), single active worker enforcement per `(task_id, repo_node_path)`, and update-only `dev-workflow` run watcher (`IngestDevWorkflowRuns` scanning `~/.claude/dev-workflow-runs/*.json` strictly for existing non-`DONE` tasks and auto-attaching `.brief.md`, `.impl-notes.md`, and `.retro.md` deliverables without auto-creating ambient tasks).
* **`ntfy.go`:** `ntfy.sh` push notification dispatcher for PR/Plan Review transitions (`REVIEW`) and Task/Session blockers (`StateBlocked` / `substatus == "blocked"`), emitting `ackbar://` mobile deep-links, Ackbar notification icons, session-title + group-path subtitles, test notification dispatch (`POST /v1/notifications/test`), and fleet-wide `ntfy` settings propagation.
* **`agent_claude.go`, `agent_antigravity.go`, `agent_other.go`:** Per-agent `AgentAdapter` implementations (`ClaudeAgentAdapter`, `AntigravityAgentAdapter`, `CodexAgentAdapter`, `GrokAgentAdapter`, `OpenCodeAgentAdapter`) encapsulating agent-specific CLI flags, approval keys, account profile setup, PID-to-session resolution, and disk session discovery.

---

## 2. API Endpoints Reference

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/v1/version` | Returns current daemon version, canonical host name, and display name. |
| `GET` | `/v1/sessions` | Returns all active, managed, and historic sessions with unread state (supports `?federated=true` for cross-host session aggregation). |
| `GET` | `/v1/events` | SSE stream broadcasting real-time session mutations with immediate header flush and periodic keepalive heartbeats (`: keepalive\n\n`, 20s default). |
| `POST` | `/v1/sessions/prompt` | Submits prompt to headless or tmux session (supports `delay_seconds`, `deliver_at`, and idempotent `client_turn_id` deduplication). |
| `GET` | `/v1/sessions/chat/stream` | SSE stream for real-time turn text, thoughts, subagents, and keepalive heartbeats (supports `?after_seq=<n>`, `?turn_id=<id>`, and `Last-Event-ID`). |
| `GET` | `/v1/sessions/pty` | WebSocket endpoint streaming interactive PTY data to `xterm.js`. |
| `POST` | `/v1/sessions/spawn` | Spawns a new agent process in a supervised tmux session using RFC 4122 UUIDv4 (supports prompt injection and zero-input `is_supervisor: true`). |
| `POST` | `/v1/sessions/bind-supervisor` | Auto-binds a Supervisor session (`is_supervisor = true`) to a logical project `node_path` and `title` on Turn 1, broadcasting SSE and LWW sync. |
| `POST` | `/v1/meta/resolve` | Derives host, agent, group, and existing session match from prompt via Jev AI / heuristics. |
| `POST` | `/v1/hooks/{agent}` | Ingests agent lifecycle and tool hook events with in-place `/clear` rotation detection. |
| `POST` | `/v1/sessions/control` | Executes control actions: `resume`, `restart`, `kill`, `move`, `rename`, `delete`, `handover`, `later`, `unlater`, `done`, `active`, `update_config`, `respond`. |
| `PATCH` | `/v1/sessions/{id}/config` | Updates session configuration (`model`, `effort`, `permission_mode`, `extra_dirs`). |
| `POST` | `/v1/sessions/{id}/shell` | Executes shell command in session CWD via `/bin/sh -c` (30s timeout, 128KB cap, `-pgid` kill). |
| `POST` | `/v1/sessions/{id}/clear` | Rotates conversation context in-place to `Conv N+1` while preserving session configuration. |
| `POST` | `/v1/sessions/{id}/compact` | Triggers context compaction (native stream-json turn for Claude, daemon rotation for Antigravity). |
| `GET` | `/v1/sessions/{id}/inspect` | Runs allowlisted CLI inspection (`kind=plugins|agents|doctor|context|cost`) and computes token metrics. |
| `GET` | `/v1/sessions/{id}/diff` | Returns working directory Git status, diffstat, and unified diff (`GIT_OPTIONAL_LOCKS=0`). |
| `GET` | `/v1/sessions/{id}/rewind` | Lists human turn checkpoints available for conversation rollback. |
| `POST` | `/v1/sessions/{id}/rewind` | Rolls back session to target turn checkpoint via transcript clone or bounded history seed. |
| `GET` | `/v1/sessions/{id}/suggestions` | Returns `/` command/skill suggestions (`kind=commands`), `@` relative file path suggestions (`kind=files&q=...`), or agent-aware model options (`kind=models&q=...`). |
| `POST` | `/v1/sessions/handover`| Initiates automated session handover, briefing extraction, and context rotation. |
| `POST` | `/v1/sessions/mark-read` | Marks a session as read (`is_unread = false`), clearing visual unread cues. |
| `GET` | `/v1/sessions/transcript`| Retrieves conversation transcript with turn spool synthesis (supports pagination `max_chars`, `offset`, `char_offset`, `role`, `format=ndjson`, gzip, and tmux fallback). |
| `POST` | `/v1/sessions/upload` | Uploads clipboard images or drag-and-dropped PDFs to `/tmp/ackbar-uploads/`. |
| `GET` | `/v1/settings` | Returns daemon settings including `host_name`, `display_name`, auto-done thresholds, `later_collapsed_by_default`, `hide_session_tabs`, handover suggestions, and `ntfy_topic` / `ntfy_server`. |
| `POST` | `/v1/settings` | Updates and persists daemon settings in SQLite (supports `propagate_ntfy: "true"` to push `ntfy_topic` and `ntfy_server` to all connected fleet hosts). |
| `POST` | `/v1/notifications/test` | Dispatches an immediate test push notification to `ntfy.sh` (or custom server) to verify mobile/desktop configuration. |
| `GET` | `/v1/nodes` | Returns configured logical tree nodes and custom groups with host paths (supports `?federated=true`). |
| `POST` | `/v1/projects/create` | Creates a new logical project node or pure category subgroup. |
| `POST` | `/v1/nodes/move` | Moves a logical group node to a new tree path. |
| `DELETE`| `/v1/nodes` | Deletes a logical group node from the database. |
| `GET` | `/v1/nodes/paths` | Returns registered host execution paths (`node_host_paths`) for tree nodes. |
| `POST` | `/v1/nodes/paths` | Registers or updates a host-specific project execution path (`node_host_paths`) for a node. |
| `GET` | `/v1/hosts` | Returns registered local and remote compute hosts. |
| `POST` | `/v1/hosts/update` | Upgrades and restarts `ackbard` on a remote target host. |
| `GET` | `/v1/tasks` | Returns tasks across all projects with associated workers, external refs, and deliverable headers (`has_content_md: true`, omitting inline `content_md`). Supports `?federated=true` and `?reconcile_prs=true` (on-demand GitHub PR state check via `gh pr view`). |
| `POST` | `/v1/tasks` | Creates or updates a task; resolves canonical tree scope, clears subprojects on move, and auto-marks bound worker sessions `Done` on `DONE`. |
| `DELETE` | `/v1/tasks` | Soft-deletes a task (`deleted_at = now`) and cascades cleanup across workers, workstreams, external refs, and deliverables for LWW peer sync. |
| `POST` | `/v1/tasks/event` | Ingests structured lifecycle events (`agent_blocked`, `agent_unblocked`, etc.) for explicitly bound tasks. |
| `POST` | `/v1/tasks/propose` | Records explicit agent-discovered work proposals (`propose_task`) into the `NEW` inbox column with duplicate detection and canonical `tree_nodes` scoping. |
| `GET` | `/v1/tasks/deliverable` | Lazily fetches a single deliverable (`?id=<id>` or `?deliverable_id=<id>`) including its full `content_md` payload on demand. |
| `POST` | `/v1/tasks/deliverable` | Attaches design mockups, change briefs, retrospectives, or cloud artifacts to a task (supports `type` alias, auto-title, and confined local file auto-reading). |
| `GET` | `/v1/deliverables` | Fetches deliverable metadata and full or rune-safe cursor-paginated `content_md` (`/v1/deliverables/{id}` or `?id=...`, `?task_id=...&kind=...`, `max_chars`, `char_offset`). |
| `GET` | `/v1/retros` | Returns compact retrospective summaries and structured `RetroMetrics` without raw `content_md` (`since`, `until`, `node_path`, `repo`, `host`, `agent`, `task_id`, `limit`). |
| `POST` | `/v1/tasks/sync-workflow` | Triggers immediate reconciliation of active worker liveness (`SyncSessionTaskWorker`) for explicitly bound tasks. |
| `POST` | `/v1/tasks/merge-pr` | Executes 1-click `gh pr merge --squash` for a task with an open PR and transitions state to `DONE`. |
| `GET` | `/v1/sync/state` | Returns delta sync state payload (nodes, host paths, tasks, workstreams, deliverables) since `?since=<timestamp>`. |
| `POST` | `/v1/sync/push` | Ingests and reconciles peer sync payload via 3-way Last-Write-Wins (LWW). |
| `GET` | `/v1/standup` | Deterministic daily standup and weekly retro report generation (grouped by project in Markdown or JSON). |
| `POST` | `/v1/briefings/synthesize` | Generates speech-optimized conversational text briefing (<45s) for voice synthesis. |
| `GET` | `/v1/agents/status` | Returns detected agent runtimes (Claude, Antigravity, Codex, Claude Desktop) with MCP, skill, rules (`rules_installed`, `rules_version`), and skills repo status. |
| `POST` | `/v1/agents/provision` | Provisions or upgrades Ackbar Tasks MCP, Skill, and global rules into connected agent configs, and optionally syncs skills repo. |
| `POST` | `/v1/agents/skills-repo/sync` | Clones or fast-forward pulls the shared skills repository and runs `./install.sh` (or fallback copy). |
| `POST` | `/v1/mcp` | Streamable HTTP JSON-RPC 2.0 MCP endpoint (`initialize`, `tools/list`, `tools/call`). Resolves local loopback listener to prevent SSRF and hairpinning. |
| `POST` | `/v1/sessions/take-wheel` | Stops any active headless turn and resumes the session in an interactive `tmux` terminal (`engine_type = "tmux"`). |
| `POST` | `/v1/sessions/switch-headless` | Stops the running `tmux` session and switches the session to turn-by-turn headless mode (`engine_type = "headless"`, `tmux_name = ""`). |
| `POST` | `/v1/sessions/auth-tool` | Spawns allowlisted OAuth login process (`plugin:*`, `agent:agy`, or `mcp:*`) for a session with 5m TTL and extracts browser authorization URL. |
| `POST` | `/v1/sessions/auth-callback` | Completes active OAuth flow by relaying callback URL/code to loopback listener (`127.0.0.1:<port>`) or subprocess stdin. |
| `GET` | `/v1/sessions/{id}/mcp-status` | Returns sanitized configured MCP servers and Google Workspace plugin authentication statuses (`10s` TTL cache, `?refresh=1` bypass). |
| `POST` | `/v1/sessions/summarize-speech` | On-demand speech summarizer with 2-level caching (in-memory + SQLite `speech_summaries`), headless Haiku synthesis ($0 flat OAuth), optional cloud API, and pure-Go structural AST fallback. |
| `POST` | `/v1/update` | Updates daemon executable; if headless turns are active, daemon restart is deferred (`restart_deferred: true`) until active turns drain. |
| `POST` | `/v1/shutdown` | Gracefully shuts down daemon (supports `?drain=true` to defer restart until active headless turns complete). |

### PTY Terminal Sizing (`/v1/sessions/pty`)

Each WebSocket gets its own attach PTY running `tmux attach-session` (local) or single-invocation `ssh -t -- <target> <script>` (remote), started at client `cols`/`rows` query parameters.

* **Single-SSH Remote Attach:** Remote PTY attach combines session validation, creation, and `exec tmux attach-session` in one SSH command, eliminating redundant handshake round-trips.
* **Pre-attach:** the daemon sets `mouse on` and `window-size latest` (both the session option and the window option). It never calls `resize-window`: that silently switches the window to `window-size manual` and sizes it to the full client height, hiding the bottom row behind the status line. Re-asserting `latest` on every attach also heals sessions left in `manual` mode by older builds.
* **Resize messages** (`{"type":"resize","cols":C,"rows":R}`) only call `TIOCSWINSZ` on the attach PTY. The tmux client forwards the new size and `window-size latest` makes the window follow it. No `tmux` or `ssh` subprocess runs per resize.
* **Deduplication:** a resize whose size matches the last applied size (seeded with the start size), or that is below 10×4, is dropped (`ptyResizeTracker` in `internal/daemon/pty.go`). Resize messages are always consumed and never written to the terminal.
* **Scroll control messages** (`{"type":"scroll","direction":"up"|"down"|"page_up"|"page_down"|"bottom","lines":N}`) execute `tmux` copy-mode scroll actions (`copy-mode -e` / `copy-mode -eu`, `send-keys -X -N <lines> scroll-up|scroll-down`, `send-keys -X page-down`, and `copy-mode -q` when `scroll_position == 0` or `bottom`) on the target local or remote SSH `tmux` session (`performTmuxScroll` in `internal/daemon/pty.go`), allowing mobile clients to scroll `tmux` history smoothly without fighting the alternate screen buffer.

> **Why this matters:** every window size change delivers `SIGWINCH` to the agent. Antigravity answers each `SIGWINCH` by clearing the screen and scrollback and reprinting its whole conversation. Claude Code only repaints its live region, so redundant resizes are invisible there. Clients must therefore never resize, or send redraw keystrokes such as Ctrl+L, unless the size really changed.

When several clients with different sizes are attached to one session (web, mobile), the window still resizes whenever a different client becomes the latest active one. That is inherent to `window-size latest`. A client becomes "latest" through a keystroke or a real size change. Merely viewing it does not count. So after typing on mobile, switching back to an unchanged web tab keeps the mobile-sized window (cropped or letterboxed) until the first keystroke or browser resize in that tab. This is deliberate: the old behaviour forced `resize-window` on every tab activation, which caused the Antigravity replay described above.

### Headless Execution Engine (`internal/daemon/headless.go`)

Headless sessions (`engine_type = "headless"`, `tmux_name = ""`) execute turns through detached worker processes (`ackbard --headless-worker`) without allocating a `tmux` session:

* **Warm Persistent Worker (`Setsid: true`):** Spawns detached `ackbard --headless-worker --worker-spec <path> --idle-ttl <dur>`. Keeps child CLI and MCP servers alive across turns.
* **Unix Domain Socket RPC:** Communicates over `0600` sockets in `~/.config/ackbar/run` (or short temp directory). Supports `status`, `send_turn`, `restart_child`, `subscribe`, `cancel`, and `shutdown`.
* **Worker Sidecar Isolation:** Persists worker metadata to `w_<workerID>.worker.json` (`0600`). Retains per-turn metadata in `turn_<id>.meta.json`.
* **Monotonic Turn Spool (`turn_<id>.ndjson`):** Sequenced events stream to disk atomically with `0600` permissions. Pre-writes `seq: 1` (`turn_start`) before provider output arrives.
* **Idle-Warm Lifetime & Auto-Reap:** Idle workers wait in `idle_warm` state. Worker exits after idle timeout (`ACKBAR_HEADLESS_IDLE_TTL`, default `20m`).
* **Idle-TTL Race Recovery:** If `send_turn` races with idle timeout reap, `HeadlessRunner.RunTurn` automatically respawns a warm worker.
* **Configuration & MCP Fingerprints (`ConfigFingerprint`, `MCPFingerprint`):** `ConfigFingerprint` rotates full workers on model/effort/permission changes; `MCPFingerprint` swaps only the inner CLI child (`restart_child`) on MCP/credential updates.
* **Active Turn Configuration Rotation:** If configuration or MCP auth changes mid-turn, runner flags the worker or child for post-turn rotation.
* **Gapless SSE Replay & Synthesis:** `/v1/sessions/chat/stream` supports `?after_seq`, `?turn_id`, `Last-Event-ID`; `/v1/sessions/transcript` synthesizes `in_flight_turn` (`activity_started_at`, `activity_kind`).
* **Startup Re-Attachment & Recovery (`InitDB`):** Discovers `w_<workerID>.worker.json` files on boot. Re-attaches `running_turn` workers and registers `idle_warm` workers for zero-cold-start reuse.
* **State Authority (`s.headless.IsRunning`):** `HeadlessRunner.IsRunning(sess.ID)` provides the source of truth for turn status. Exit hooks do not flip active turns to `StateIdle`.
* **Runtime Cleanup & Server-Side `auto_done`:** Marking `Done` or `auto_done` sweep stops active workers, kills tmux sessions, prunes spools, and resets runtime state.
* **OS Idle-Sleep Protection (`sleep_inhibitor.go`):** Holds sleep inhibitor strictly while `ActiveTurnCount() > 0`. Releases lock immediately when worker enters `idle_warm`.
* **Process Teardown & Cross-OS Safety:** Verifies socket `TurnID` before signaling PIDs. Child process-group teardown uses `Setpgid: true` and `> 1` PID/PGID guards.
* **In-Place Restart & Worker Teardown:** `restart` swaps the headless CLI child (`RestartSessionChild`) or respawns the tmux pane (`tmux.RespawnWithEnv`); `take-wheel`, `done`, and `kill` reap warm workers.
* **Daemon Restart Resilience (`InitDB` & Drain Mode):** Updates and shutdowns with `drain=true` defer restart until active turns finish. Interrupted sessions reset to `StateIdle` on boot.
* **Per-Turn Invocation & Conversation Continuity:**
  * **Claude Code:** Runs `claude -p --input-format stream-json --output-format stream-json --verbose`. Dispatches turns via stdin NDJSON `{"type":"user","message":{"role":"user","content":"<prompt>"}}`.
  * **Google Antigravity:** Runs `agy --input-format stream-json --output-format stream-json` omitting `-p`. Dispatches turns via stdin NDJSON `{"event":"user","message":{"role":"user","content":"<prompt>"}}`.
  * **Antigravity Boot Init Handling:** Buffers startup `{"event":"init"}` event until Turn 1 attaches. Migrates temporary spawn ID to canonical conversation ID atomically.
  * **Safe First-Turn Recovery:** Persists prompts only after initial event arrives or process succeeds. Prevents broken resume state on early startup failure.
  * **Initial Prompt on Spawn:** Passing non-empty `prompt` to `POST /v1/sessions/spawn` triggers Turn 1 immediately in background.
  * **Session Configuration:** Passes `--model`, `--effort` (Claude Code only), and permission flags (`--permission-mode plan` or `--dangerously-skip-permissions`).
  * **Interactive Tool Interception:** Blocks on `AskUserQuestion`, `ask_question`, or `ExitPlanMode`. Approval synchronously reaps worker and resumes with updated permissions.
  * **OAuth Authentication Interception (`BlockAuth`):** Pauses queue on OAuth failure, persists `blocked_meta`, emits `interactive_block`, and auto-resumes on successful callback.
  * **Prompt Queue & Auto-Dispatch:** Persists queued prompts in SQLite (`scheduled_prompts`). Dispatches due prompts via 5-second background loop or manual trigger.

### SSE Event Stream Keepalive & Prompt Idempotency

* **`/v1/events` Keepalive Heartbeats:** The `/v1/events` endpoint in [`server.go`](../internal/daemon/server.go) flushes HTTP headers immediately upon connection, even with an empty session list. It emits periodic `: keepalive\n\n` comments on a configurable atomic interval (`20s` default, configured via `Server.SetEventsKeepaliveInterval`).
* **`POST /v1/sessions/prompt` Idempotency:** Accepts an optional `client_turn_id` string (maximum 128 characters; values exceeding 128 return `400 Bad Request`).
  * **Bounded LRU Cache:** Uses [`prompt_dedup.go`](../internal/daemon/prompt_dedup.go) with a 10-minute TTL and 512 entries keyed by `sessionID + "\x00" + clientTurnID`.
  * **Coalescing & Replay:** In-flight duplicate requests await the initial reservation. Committed duplicates return `200 OK` with `{"status": "running", "deduplicated": true}` (and `queue_depth` when queued) without duplicate execution.
  * **Failure Safety:** Non-200 dispatch errors (`400`/`500`) abort the reservation, enabling clients to retry immediately.

### Transcript Caching & Reverse-NDJSON Streaming (`/v1/sessions/transcript`)

* **Progressive Reverse-NDJSON (`format=ndjson`):** Emits line 1 `meta` header followed by newest-to-oldest `msg` lines formatted via [`CoalesceTranscriptMessagesForWire`](file:///Users/dev4u/Work/Ackbar/internal/daemon/server_chat.go).
* **Early Flush Intervals:** HTTP flusher flushes chunk immediately after `meta` and message indices 0, 5, 24, and total-1.
* **Payload Trimming (`include_formats=false`):** Skips generating heavy ANSI and Markdown strings during JSON serialization to reduce payload size and CPU usage.
* **Response Compression:** Automatically compresses JSON responses larger than 2KB with `gzip` when requested via `Accept-Encoding: gzip`.
* **Bounded In-Memory Cache ([`transcript_cache.go`](file:///Users/dev4u/Work/Ackbar/internal/daemon/transcript_cache.go)):** Caches up to 128 parsed transcripts by `(agent, nativeID, cwd)` with file size, modification timestamp, and 128-byte header validation.
* **Incremental Seek-Append:** Performs fast `Seek` append parsing on Claude `.jsonl` files when file size grows and header matches.
* **Pre-Filtered JSONL Parsing:** Skips non-message lines (tool progress, file snapshots) before JSON unmarshaling to prevent memory thrash.
* **Subagent Discovery Cache:** Caches disk-discovered subagents for 3 seconds to avoid repetitive directory scans during active polling.
* **Streaming Proxy Timeout Safety:** Proxied remote transcripts use `ResponseHeaderTimeout: 10s` so streaming NDJSON responses do not terminate prematurely.
* **Pagination & Output Controls ([`transcript_pagination.go`](file:///Users/dev4u/Work/Ackbar/internal/daemon/transcript_pagination.go)):** Supports `max_chars` (500–50000, default 10000), `offset`, `char_offset`, and `role=assistant|all`; returns `truncated` and `pagination` metadata.
* **Tmux Capture Pane Fallback:** Captures active pane buffer (up to 500 lines) via `tmux.CapturePane` when transcripts are empty or unwritten.

### Pure-Go Git Branch Resolution & Fast Session Queries

* **Zero-Subprocess Branch Resolution ([`git.go`](file:///Users/dev4u/Work/Ackbar/internal/daemon/git.go)):** Resolves `.git/HEAD` and `.git` worktree pointer files in pure Go with a 60-second positive and negative cache.
* **Scan Loop Subprocess Elimination:** Database session scan loops (`GetSession`, `ListSessions`) read pre-persisted `git_branch` columns without invoking subprocesses.
* **Fast Alias Lookup:** SQLite indexes on `native_id` and `old_id` resolve headless Antigravity IDs via [`GetSessionByOldID`](file:///Users/dev4u/Work/Ackbar/internal/daemon/db.go) and [`GetSessionByAlias`](file:///Users/dev4u/Work/Ackbar/internal/daemon/db.go).
* **Title Lock & Sanitization:** Explicit `CustomTitle` values persist across scans; background loops reject raw `<uuid>(` OSC window titles.

---

## 3. In-Place `/clear` Conversation Turn Rotation

When an agent resets its conversation context in-place (e.g. `/clear` in Claude Code, `/reset` in Google Antigravity, or context reset in Codex):

1. **Rotation Detection (`findActiveManagedSessionInCwd`):** When a telemetry hook arrives with a new native UUID from a working directory with an active managed tmux session:
2. **Archiving Previous Turn:** The previous turn is decoupled from tmux supervision (`managed = false`), marked as `⏹️ StateEnded` (`state: 4`) with `activity = "Cleared (context reset)"`, and renamed with the suffix `(Conv 1)` (or prior turn number) with its full token history and transcripts preserved.
3. **Adopting Live Supervisor:** The new session turn inherits the live managed tmux window (`managed = true`), tree group node path, and project key, and is titled `"<Base Title> (Conv 2)"` (or `Conv 3` on subsequent clears).
4. **Resuming Cleared Sessions:** Any historic turn can be resumed anytime into a dedicated new tmux tab via `POST /v1/sessions/control?action=resume&id=...`.

---

## 4. Automated Session Handover & Context Rotation (`POST /v1/sessions/handover`)

When long-running autonomous sessions consume significant token context (`context_pct >= 60%`), Ackbar provides 1-click Automated Session Handover to eliminate context bloat while maintaining task continuity:

### Request Format:
```json
{
  "id": "claude-code:local:uuid-here",
  "strategy": "in_place", // "in_place" (default) or "new_session"
  "custom_instruction": "Focus next on adding unit tests for server.go",
  "sync": false // default false; returns 202 Accepted and orchestrates asynchronously
}
```

### Execution Flow:
1. **Safety Verification:** Requires sessions to be managed and idle/blocked (`StateIdle` or `StateBlocked`). In-flight active command runs (`StateWorking`) are rejected with `409 Conflict`.
2. **Automated Handover Extraction:** Injects a structured briefing prompt into the agent's PTY. The agent synthesizes a 4-point briefing (Objectives, Milestones, Git State, Next Steps).
3. **Context Rotation:** Once the briefing finishes, sends `/reset` (Antigravity) or `/clear` (Claude Code / Codex) to trigger context rotation.
4. **Reseeding:** Re-injects the generated briefing and custom instructions into the fresh turn, achieving 0% token context with uninterrupted productivity.

### Handover & Workspace Settings (`/v1/settings`):
* `auto_done_enabled`: `"true"` | `"false"` (default: `"false"`) — toggle background sweep marking inactive sessions `Done`.
* `auto_done_hours`: Integer (default: `24`) — inactivity hours before eligible non-working sessions become `Done`.
* **Task Completion Auto-Done:** Transitioning a task to `DONE` automatically marks bound non-supervisor worker sessions as `is_done = true` and `StateEnded`.
* `handover_suggestion_enabled`: `"true"` | `"false"` (default: `"true"`) — toggle automated UI suggestions.
* `handover_threshold_pct`: Integer between `10` and `95` (default: `"60"`) — context window threshold for triggering warnings and quick-action chips.
* `later_collapsed_by_default`: `"true"` | `"false"` (default: `"false"`) — whether the ⏳ Later subsections start collapsed by default.
* `hide_session_tabs`: `"true"` | `"false"` (default: `"false"`) — hides the Web UI top session tab bar (`.tab-strip-wrapper`) and enables Single-View Mode where opening a session replaces the previously open view.

---

## 5. Database Schema & Auto-Migrations

The SQLite database (`~/.config/ackbar/ackbard.db`) uses CGO-free pure Go SQLite (`modernc.org/sqlite`). On daemon startup, `InitDB()` automatically applies non-destructive schema migrations:

* `last_event_at`: Timestamp of the most recent user interaction (session creation/spawn/resume, `UserPromptSubmit` hook, interactive blocker response, chat/terminal prompt submission, or new user message in on-disk transcripts via `SessionMeta.LastUserMessageAt` / Antigravity `USER_INPUT` steps). Background agent tool calls (`PreToolUse`, `PostToolUse`), subagent lifecycle events, and status inspector polling do not advance `last_event_at`, keeping session tree ordering stable across concurrent working sessions.
* `is_unread`: Tracks whether the session has unviewed state transitions (`1` = unread, `0` = read).
* `is_later`: Tracks whether the session is parked in the Later section (`1` = later, `0` = normal). Sessions marked as later are immune to auto-done and auto-archive timers.
* `last_state_change_at`: Timestamp of the most recent lifecycle state mutation.
* `entrypoint`: Identifies session launch context (`claude-vscode`, `cli`, `antigravity`).
* `kind`: Interactive vs headless mode.
* `version`: Agent software version.
* `context_pct`: Current context window token consumption percentage.
* `git_branch`: Current Git branch associated with the session workspace.
* `worktree_name`: Associated Git worktree directory if applicable.
* `old_id`: Pre-adoption temporary session ID preserved when an Antigravity session adopts its canonical conversation UUID, enabling seamless fallback resolution across `/v1/sessions/*` and `/v1/sessions/pty`.
* `is_supervisor`: Tracks whether the session is a zero-input `⚓ Supervisor` session (`1` = supervisor, `0` = normal).
* `current_task_id`: Tracks the active Ackbar task ID bound to the session (if any), categorizing the session under the `📋 Tasks` subsection. Each host strictly stores only its own local sessions in the `sessions` table (no cross-host session replicas).
* `blocked_meta`: Persists structured JSON metadata (`auth_target`, `auth_kind`, `auth_server`, `auth_url`, `auth_hint`, `flow_id`) for sessions in `BlockAuth` state.
* `extra_dirs`: Persists JSON array of additional allowed directory paths (`--add-dir`) for agent workspace access.
* `task_deliverables` LWW columns: Adds `updated_at`, `provider`, `content_md`, `deleted_at` (tombstone for LWW sync), `metadata_json` (structured `RetroMetrics`), and `dedup_key` with indexes for peer replication.

---

## 6. Linux Service Management (`systemd`)

On Linux hosts (such as remote GPU compute boxes), `ackbard` can run as a background `systemd` user service:

### Service Unit (`~/.config/systemd/user/ackbard.service`)
```ini
[Unit]
Description=Ackbar Agent Control Plane Daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/ackbard
Restart=always
RestartSec=3s
KillMode=process
Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

> **Process Isolation:** `KillMode=process` ensures `systemctl --user restart ackbard` terminates only the daemon process. Detached headless workers continue running and re-attach upon daemon restart.

### Commands:
```bash
# Enable lingering so service persists across logouts and starts on boot
loginctl enable-linger $USER

# Start and enable user service
systemctl --user daemon-reload
systemctl --user enable --now ackbard

# Instant non-blocking restarts
systemctl --user restart ackbard
```

---

## 7. Host Identity & Display Name Configuration

Each `ackbard` instance identifies itself across the multi-machine fleet using a canonical host name and an optional human-friendly display name:

* **Canonical Host Name (`host_name`):** Alphanumeric machine identifier used in session IDs (`claude-code:<host>:<uuid>`), API routing, and SSH targeting. Defaults to `"local"` if unspecified.
* **Display Name (`display_name`):** User-facing label displayed in Web UI badges (`@MacBook`), header host status pills, tabs, and inspector modals. Defaults to the canonical host name if unspecified.

### Configuration Hierarchy:
1. **CLI Flags:** `ackbard --host-name macbook --display-name "MacBook Air"`
2. **Environment Variables:** `ACKBAR_HOST=macbook` and `ACKBAR_DISPLAY_NAME="MacBook Air"` (ideal for LaunchAgents and systemd units).
3. **Persistent SQLite Settings:** Configured via the Web UI **⚙️ Settings** modal or `POST /v1/settings` with JSON `{"host_name":"macbook","display_name":"MacBook Air"}`.
4. **Fallback:** Defaults to `"local"`.

### Non-Destructive Database Migration:
When a daemon's host name is configured away from `"local"`, `ackbard` automatically migrates existing database records (`sessions` and `deleted_sessions`) to use the new host identifier. Lookups using legacy session IDs remain fully backward-compatible.

---

## 8. Shared Skills Repository Synchronization

Ackbar provides automated synchronization of external git repositories containing skills, tools, and extensions across all agents on local and remote fleet hosts.

### Persistent Settings Keys
Stored in the SQLite `settings` table:
* `skills_repo_path`: Local filesystem path to the cloned repository (must reside under `$HOME`).
* `skills_repo_url`: Git repository URL (e.g. `https://...`, `git@...`, or `ssh://...`).
* `skills_repo_last_sync`: RFC 3339 timestamp of the last successful pull and install run.
* `skills_repo_last_check`: RFC 3339 timestamp of the last upstream remote fetch or sync check.

### Auto-Discovery Contract
If `skills_repo_path` is not explicitly set in the database, `ackbard` inspects agent skill directories (`~/.gemini/config/skills/`, `~/.gemini/antigravity-cli/skills/`, `~/.claude/skills/`):
1. **Marker Files:** Looks for `.ackbar-skills-source` or `.personal-config-managed` containing `source_repo=<path>`.
2. **Symlinks:** Inspects symlink targets and walks parent directories to identify the enclosing `.git` repository root.
3. Discovered repositories are validated against security constraints and persisted to database settings.

### Security Boundaries
* **Path Validation (`validateRepoPath`):** Target repository paths must be absolute paths strictly within the invoking user's home directory (`$HOME`). Paths starting with `-`, pointing to the root `$HOME` itself, or located within security-sensitive directories (`~/.ssh`, `~/.gnupg`, etc.) are rejected.
* **URL Validation (`validateGitURL`):** Disallows argument/option injection (rejects leading `-`).
* **Process Ownership:** The `install.sh` script is only executed if its POSIX file ownership (`stat_t.Uid`) strictly matches the running `ackbard` process UID.

### Execution & Fallback Contract
1. **Clean Fast-Forward Pull:** Runs `git fetch origin` followed by `git pull --ff-only` if `git status --porcelain` is clean. If uncommitted local changes exist, the pull is safely skipped to avoid clobbering work.
2. **Installer Execution:** If `install.sh` exists at the root of the repository, executes `/bin/bash ./install.sh` with `HOME` and `ACKBAR_CLAUDE_DIRS` (colon-delimited list of custom account config directories from `accounts`).
3. **Fallback Directory Copy:** If no `install.sh` is present, recursively mirrors any skills in `skills/*/SKILL.md` or `plugins/*/skills/*/SKILL.md` to `~/.claude/skills/`, `~/.gemini/config/skills/`, `~/.gemini/antigravity-cli/skills/`, and `~/.codex/skills/`.
4. **Periodic Auto-Sync:** The background loop checks `skills_repo_last_check` on startup and hourly. If more than 24 hours have elapsed since the last check, an automated background sync is triggered.

---

## 9. Supervisor MCP & Voice Companion Engine

### Streamable HTTP MCP Endpoint (`POST /v1/mcp`)
Exposes JSON-RPC 2.0 MCP capabilities over HTTP for supervisor sessions and external connectors (e.g. Claude Desktop, mobile companions).
* **Protocol Methods:** `initialize` (returns protocol `2024-11-05` and server info `ackbar-mcp`), `ping`, `tools/list`, and `tools/call`.
* **Security & SSRF Mitigation:** Loopback listener address is extracted directly from the request context (`http.LocalAddrContextKey`) rather than the unvalidated client `Host` header, preventing internal loopback SSRF, reverse-proxy hairpinning, and token leakage. Request bodies are constrained with `http.MaxBytesReader(w, r.Body, 2<<20)`.
* **Exposed Supervisor Tools:** `list_sessions`, `get_session_output` (paginated via `max_chars`, `offset`, `char_offset`, `include_all_roles`), `send_session_input` (with automatic `select_option -> answer` routing and blocked-state detection), `spawn_session`, `control_session`, and `bind_supervisor_session` (with `client.SessionID` fallback and automatic fallback binding via `create_task`).
* **Dedicated Supervisor Dispatcher Workspace (`~/.ackbar/supervisor/`):** Zero-input supervisor sessions default to `~/.ackbar/supervisor/` with generated `CLAUDE.md` and `AGENTS.md` containing dispatcher rules and dynamic project tree nodes catalog. Spawns inject `BuildSupervisorSystemPrompt` and `ACKBAR_IS_SUPERVISOR=1`.
* **Task Worker Auto-Dispatch:** `create_task` supports optional `agent` (`claude-code`, `antigravity`, `codex`) and `prompt` forwarding, auto-binding supervisor sessions to project groups. Conversation rotation (`/clear`) strictly preserves `IsSupervisor` and `CurrentTaskID`.

### On-Demand Speech Summarizer (`POST /v1/sessions/summarize-speech`)
Generates spoken audio briefing text and interactive suggested chips for assistant turns:
* **2-Tier Synthesis Pipeline:**
  1. **Fast Cache Check:** Checks in-memory sync map and SQLite `speech_summaries` table by `(session_id, message_index)` before transcript lookup.
  2. **Stateless Flat-Rate Haiku ($0):** Invokes headless Claude CLI (`claude -p --model haiku --no-session-persistence`) using stripped billed credentials.
  3. **Cloud API Fallback:** Queries Anthropic Messages API (`claude-3-5-haiku-latest`) if `anthropic_api_key` / `speech_api_key` is configured in DB settings or `ACKBAR_SPEECH_API_KEY` env var.
  4. **Pure-Go Structural AST Fallback (`BuildStructuralSpeechSummary`):** Strips fenced code blocks, box drawing diagrams, and HTML tags; extracts Markdown `##`/`###` section outlines and open questions; generates 2-3 sentence spoken text and clickable reply chips.
* **SQLite Persistence (`speech_summaries`):**
  * `session_id TEXT NOT NULL`
  * `message_index INTEGER NOT NULL`
  * `spoken_text TEXT NOT NULL`
  * `suggested_chips TEXT NOT NULL DEFAULT '[]'`
  * `created_at DATETIME DEFAULT CURRENT_TIMESTAMP`
  * `PRIMARY KEY (session_id, message_index)`


