# Go Backend, CLI & Core Engine Audit

This document details the architectural analysis of the Go daemon (`ackbard`), CLI tool (`ackbar`), reverse tunnel client (`ackbar-relay`), MCP server, and provider subsystems.

## 1. Go & CLI Production Hotspots

Production hotspots ranked by non-blank/non-comment code lines multiplied by 90-day git commit churn.

| Hotspot File | Code L | Total L | Churn | Violated Principles | Proposed Behavior-Neutral Split |
| :--- | ---: | ---: | ---: | :--- | :--- |
| [server.go](../../internal/daemon/server.go#L56-L1035) | 949 | 1,035 | 110 | Single Responsibility, God-struct, Flat Mux | `server.go` (lifecycle), `http_middleware.go`, `sse_broker.go`, `scheduler.go`, `server_system.go` |
| [db.go](../../internal/daemon/db.go#L18-L3805) | 3,510 | 3,805 | 32 | Single Responsibility, High Coupling | `internal/store/{sqlite,sessions,tasks,hosts,nodes,accounts,sync}.go` + versioned migrations |
| [headless.go](../../internal/daemon/headless.go#L75-L1980) | 1,819 | 1,980 | 22 | Single Responsibility, Open/Closed | `internal/headless/{runner,chat_broker,prompt_queue,blocker_gate,drain}.go` + provider stream adapters |
| [server_sessions.go](../../internal/daemon/server_sessions.go#L29-L2058) | 1,848 | 2,058 | 15 | Single Responsibility, Open/Closed | `session_resolver.go`, `peer_proxy.go`, `handlers_session_{query,lifecycle,spawn,respond,engine}.go` |
| [status_inspector.go](../../internal/daemon/status_inspector.go#L1-L2069) | 1,911 | 2,069 | 13 | Boundary Inversion, Open/Closed | Move scraping logic into `internal/provider/{claude,antigravity,codex}/status.go` behind `agent.Provider` |
| [transcript.go](../../internal/daemon/transcript.go#L1-L1606) | 1,473 | 1,606 | 16 | Boundary Inversion, Duplication | Consolidate duplicate parsers into `internal/provider/<agent>/{transcript,subagent}.go` |
| [tools.go](../../internal/mcp/tools.go#L1-L1984) | 1,849 | 1,984 | 8 | Single Responsibility, Open/Closed | `client.go`, `registry.go` (typed `ToolSpec` map), `tools_{tasks,sessions,fleet}.go` |
| [main.go](../../cmd/ackbar/main.go#L27-L1015) | 926 | 1,015 | 14 | Single Responsibility, God CLI Entrypoint | `internal/cli/{web,hooks,account,agent,update}.go` using `flag.FlagSet` + daemon HTTP API |

## 2. Boundary Inversion & Provider Drift

The daemon imports provider types indirectly, while [internal/provider](../../internal/provider/claude_code.go#L9) directly imports `internal/daemon`. Because Go prohibits cyclic imports, `internal/daemon` cannot import `internal/provider`. Consequently, `8,461` lines of agent-specific logic live inside `internal/daemon`, while `internal/provider` contains only `2,380` lines.

### Subsystem Logic Duplication and Behavioral Drift

| Domain Logic Area | `internal/daemon` Implementation | `internal/provider` Implementation | Drift Impact |
| :--- | :--- | :--- | :--- |
| **Project Dir Encoding** | [transcript.go](../../internal/daemon/transcript.go#L484-L497): replaces `/`, `.`, `_` with `-` | [claude_code_transcript.go](../../internal/provider/claude_code_transcript.go#L43): replaces only `/` | Provider path lookups fail on `.worktrees/` directory structures |
| **Transcript Extraction** | [transcript.go](../../internal/daemon/transcript.go#L57-L73): checks only `antigravity` and `claude-code` | [codex_transcript.go](../../internal/provider/codex_transcript.go), [grok_transcript.go](../../internal/provider/grok_transcript.go), [opencode_transcript.go](../../internal/provider/opencode_transcript.go) | Provider transcripts for Codex, Grok, and OpenCode remain completely unused by daemon |
| **Subagent Discovery** | [transcript.go](../../internal/daemon/transcript.go): parses subagents with 15m staleness window | [claude_code_subagent.go](../../internal/provider/claude_code_subagent.go#L31): duplicate scanner with 30m window | Inconsistent subagent session detection and mismatched lifetime thresholds |
| **Hook Initialization** | [agent_claude.go](../../internal/daemon/agent_claude.go): `SetupProfileDir` writes hook configuration | [main.go](../../cmd/ackbar/main.go): `runSetupHooks` performs manual JSON editing | Settings and env variables diverge between CLI hook install and daemon runtime |
| **CLI Flag Builders** | [agent_antigravity.go](../../internal/daemon/agent_antigravity.go), [agent_claude.go](../../internal/daemon/agent_claude.go), [agent_other.go](../../internal/daemon/agent_other.go) | [main.go](../../cmd/ackbar/main.go): duplicate flag assembly | 4 divergent command-line argument construction sites across daemon and CLI |

### The 9 OCP Switch Sites Required to Add an Agent

Adding support for any new agent runtime requires modifying switch statements across 9 distinct locations:
1. `internal/daemon/agent_*.go`: capability adapter registration
2. `internal/daemon/status_inspector.go`: terminal status scraper
3. `internal/daemon/transcript.go`: transcript extractor switch
4. `internal/daemon/headless.go`: stream NDJSON parser dispatch
5. `internal/daemon/server_sessions.go`: spawn routing switch
6. `internal/daemon/server_projects.go`: hook directory configuration
7. `cmd/ackbar/main.go`: CLI hook installation dispatch
8. `internal/provider/`: provider adapter registration
9. `internal/mcp/tools.go`: supervisor session spawn validator

Extracting an `internal/agent` package with a unified `agent.Provider` interface resolves this boundary inversion and collapses these 9 switch points into single registrations.

## 3. Security, SQLite & Reliability Defects

| Location | Severity | Technical Defect | Architectural Risk |
| :--- | :--- | :--- | :--- |
| [main.go](../../cmd/ackbard/main.go#L26) | **P0** | Defaults `-host` flag to `"0.0.0.0"` instead of `"127.0.0.1"` | Binds daemon port `7777` to all interfaces, exposing control plane to LAN peers |
| [server.go](../../internal/daemon/server.go#L339-L343), [pty.go](../../internal/daemon/pty.go#L180-L183) | **P0** | Optional auth defaults to no token; wildcard CORS; disables WS `Origin` check | Allows browser cross-origin pages to send CSRF requests and establish PTY WebSockets |
| [pty.go](../../internal/daemon/pty.go#L310) | **P0** | PTY handler creates new `tmux` shell for any unauthenticated session ID | Arbitrary remote command execution via unauthenticated WebSocket connection |
| [pty.go](../../internal/daemon/pty.go#L94-L106), [upload.go](../../internal/daemon/upload.go#L187-L194), [server_hosts.go](../../internal/daemon/server_hosts.go#L797-L805) | **P0** | Formats SSH remote commands with Go `%q` and omits `--` before `sshTarget` | `%q` uses double quotes, allowing POSIX `$(...)` subshell expansion; missing `--` enables `-oProxyCommand` flag injection |
| [server.go](../../internal/daemon/server.go#L352) | **P0** | `authMiddleware` allowlists legacy `/app.js` and omits `/js/*` static assets | Web UI assets return 401 when token auth is enabled before [auth.js](../../web/js/auth.js) can supply token |
| [main.go](../../cmd/ackbar-hook/main.go#L54-L72) | **P0** | Stdin hook shim sends HTTP POST without `Authorization` or `ACKBAR_TOKEN` | All agent hook notifications fail when daemon operates with token authentication |
| [files.go](../../internal/daemon/files.go#L192-L248) | **P1** | `/v1/files/content` serves absolute paths under `$HOME` without path restriction | Exposes sensitive user credentials including `~/.claude`, `~/.gemini`, `~/.kube/config`, and `ackbard.db` |
| [client.go](../../internal/relay/client.go#L275-L284) | **P1** | Passes `httptest.NewRecorder()` to PTY WebSocket handler | `ResponseRecorder` lacks `http.Hijacker` interface; drops `TypeData` WebSocket frames over reverse tunnel relay |
| [server_sessions.go](../../internal/daemon/server_sessions.go#L226-L236) | **P1** | `handlePurge` fans out `POST /v1/maintenance/purge` without hop count or auth | Mutual peer hosts cause infinite HTTP request amplification loops |
| [db.go](../../internal/daemon/db.go#L234-L239) | **P1** | Omits `sql.DB.SetMaxOpenConns`; defaults SQLite pool to unlimited connections | Triggers frequent `SQLITE_BUSY` errors under concurrent HTTP and hook writes |
| [db.go](../../internal/daemon/db.go#L615-L907) | **P1** | Uses `DEFERRED` transactions with `db.Begin()` then upgrades locks | Concurrent write transactions fail with busy timeout during lock upgrade |
| [db.go](../../internal/daemon/db.go) | **P1** | Executes external `git` subprocesses inside active `sql.Rows` iteration loops | Extends SQLite read locks, blocking daemon writers while git process runs |
| [db.go](../../internal/daemon/db.go#L3413-L3425) | **P1** | Runs 4 independent `SELECT count(*)` database queries inside `sort.SliceStable` | O(N log N) database queries executed during session list sorting |
| [server.go](../../internal/daemon/server.go) | **P1** | Unlocked access to `Server.activeWatchers`; un-cancelled ticker in `StartUploadCleaner` | Race condition under concurrent session closures; background goroutine leak |

## 4. Healthy Go Patterns to Preserve

The following Go subsystems exhibit clean architecture and should serve as implementation patterns:
* **`internal/router` ([jev.go](../../internal/router/jev.go#L1-L497)):** Pure leaf package implementing weighted routing algorithms. Contains zero external dependencies, zero daemon coupling, and 100% test coverage.
* **`internal/mcp` ([server.go](../../internal/mcp/server.go#L24-L47)):** Pure JSON-RPC stdio protocol server with clean byte transport boundaries and zero domain state leakage.
* **`internal/daemon/logger.go` ([logger.go](../../internal/daemon/logger.go#L1-L180)):** Thread-safe ring buffer logger with non-blocking reads and isolated SSE broadcasting.
* **`ptyResizeTracker` in `internal/daemon/pty.go`:** Clean debounce tracker preventing terminal resize storms.
* **`internal/skills` ([skills.go](../../internal/skills/skills.go#L1-L74)):** Modular skill locator with clear filesystem discovery boundaries.
