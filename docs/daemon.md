# Ackbar Daemon Engine (`ackbard`)

## 1. Responsibilities

The `ackbard` daemon is the central backend running on every monitored machine (local workstation and remote compute servers).

### Key Subsystems:
1. **HTTP Control Plane:** Exposes REST endpoints for session listing, lifecycle controls (`kill`, `restart`, `move`, `rename`, `delete`), host management, and project node creation.
2. **Web Dashboard & PWA Frontend:** Serves an embedded desktop/mobile Web GUI with tabs, real-time live terminal emulation (`xterm.js`), and collapsible project trees.
3. **PTY WebSocket Multiplexer (`/v1/sessions/pty`):** Attaches in-place to running tmux panes using binary WebSocket frames, bidirectional keepalive heartbeats, and dynamic canvas resizing.
4. **Hook Ingestion Engine:** Ingests live telemetry hooks from agents (`/v1/hooks/<agent>`). Responses are sent immediately (`200 OK`) and processed asynchronously to prevent blocking the agent execution loop.
5. **SSE Broadcaster:** Streams real-time updates to all connected TUI and Web clients over `/v1/events`.
6. **SQLite Persistence:** Stores session state, activity history, and logical tree hierarchies in `~/.config/ackbar/ackbard.db` (CGO-free pure Go SQLite).
7. **Built-in Self-Rotating Logger:** Automatically writes, caps (10MB), and daily-rotates daemon logs in `~/.config/ackbar/logs/` with 7-day automatic retention pruning.
8. **Background Process Scanner:** Periodically inspects `tmux list-panes` and the OS process table (`ps`) to supervise unmanaged and managed agent sessions.

---

## 2. API Endpoints Reference

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/v1/version` | Returns current daemon version (e.g. `{"version":"20260817.22"}`). |
| `GET` | `/v1/sessions` | Returns all active, managed, and historic sessions. |
| `GET` | `/v1/events` | SSE stream broadcasting real-time session mutations. |
| `GET` | `/v1/sessions/pty` | WebSocket endpoint streaming interactive PTY data to `xterm.js`. |
| `POST` | `/v1/sessions/spawn` | Spawns a new agent process in a supervised tmux session using RFC 4122 UUIDv4. |
| `POST` | `/v1/hooks/{agent}` | Ingests agent lifecycle and tool hook events. |
| `POST` | `/v1/sessions/control` | Executes control commands (`kill`, `restart`, `move`, `rename`, `delete`). |
| `GET` | `/v1/nodes` | Returns configured logical tree nodes and custom groups. |
| `POST` | `/v1/projects/create` | Creates a new logical project node or pure category subgroup. |
| `POST` | `/v1/nodes/move` | Moves a logical group node to a new tree path. |
| `DELETE`| `/v1/nodes` | Deletes a logical group node from the database. |
| `GET` | `/v1/hosts` | Returns registered local and remote compute hosts. |
| `POST` | `/v1/hosts/update` | Upgrades and restarts `ackbard` on a remote target host. |

---

## 3. Database Schema & Auto-Migrations

The SQLite database (`~/.config/ackbar/ackbard.db`) uses CGO-free pure Go SQLite (`modernc.org/sqlite`). On daemon startup, `InitDB()` automatically applies non-destructive schema migrations:

* `entrypoint`: Identifies session launch context (`claude-vscode`, `cli`, `antigravity`).
* `kind`: Interactive vs headless mode.
* `version`: Agent software version.
* `context_pct`: Current context window token consumption percentage.
* `git_branch`: Current Git branch associated with the session workspace.
* `worktree_name`: Associated Git worktree directory if applicable.
