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
| `GET` | `/v1/version` | Returns current daemon version (e.g. `{"version":"20260826.01"}`). |
| `GET` | `/v1/sessions` | Returns all active, managed, and historic sessions with unread state. |
| `GET` | `/v1/events` | SSE stream broadcasting real-time session mutations and state changes. |
| `GET` | `/v1/sessions/pty` | WebSocket endpoint streaming interactive PTY data to `xterm.js`. |
| `POST` | `/v1/sessions/spawn` | Spawns a new agent process in a supervised tmux session using RFC 4122 UUIDv4. |
| `POST` | `/v1/hooks/{agent}` | Ingests agent lifecycle and tool hook events with in-place `/clear` rotation detection. |
| `POST` | `/v1/sessions/control` | Executes control actions: `resume`, `restart`, `kill`, `move`, `rename`, `delete`. |
| `POST` | `/v1/sessions/mark-read` | Marks a session as read (`is_unread = false`), clearing visual unread cues. |
| `GET` | `/v1/sessions/transcript`| Retrieves extracted conversation transcript (JSON or formatted Markdown). |
| `POST` | `/v1/sessions/upload` | Uploads clipboard images or drag-and-dropped PDFs to `/tmp/ackbar-uploads/`. |
| `GET` | `/v1/nodes` | Returns configured logical tree nodes and custom groups. |
| `POST` | `/v1/projects/create` | Creates a new logical project node or pure category subgroup. |
| `POST` | `/v1/nodes/move` | Moves a logical group node to a new tree path. |
| `DELETE`| `/v1/nodes` | Deletes a logical group node from the database. |
| `GET` | `/v1/hosts` | Returns registered local and remote compute hosts. |
| `POST` | `/v1/hosts/update` | Upgrades and restarts `ackbard` on a remote target host. |

---

## 3. In-Place `/clear` Conversation Turn Rotation

When an agent resets its conversation context in-place (e.g. `/clear` in Claude Code, `/reset` in Google Antigravity, or context reset in Codex):

1. **Rotation Detection (`findActiveManagedSessionInCwd`):** When a telemetry hook arrives with a new native UUID from a working directory with an active managed tmux session:
2. **Archiving Previous Turn:** The previous turn is decoupled from tmux supervision (`managed = false`), marked as `⏹️ StateEnded` (`state: 4`) with `activity = "Cleared (context reset)"`, and renamed with the suffix `(Conv 1)` (or prior turn number) with its full token history and transcripts preserved.
3. **Adopting Live Supervisor:** The new session turn inherits the live managed tmux window (`managed = true`), tree group node path, and project key, and is titled `"<Base Title> (Conv 2)"` (or `Conv 3` on subsequent clears).
4. **Resuming Cleared Sessions:** Any historic turn can be resumed anytime into a dedicated new tmux tab via `POST /v1/sessions/control?action=resume&id=...`.

---

## 4. Database Schema & Auto-Migrations

The SQLite database (`~/.config/ackbar/ackbard.db`) uses CGO-free pure Go SQLite (`modernc.org/sqlite`). On daemon startup, `InitDB()` automatically applies non-destructive schema migrations:

* `is_unread`: Tracks whether the session has unviewed state transitions (`1` = unread, `0` = read).
* `last_state_change_at`: Timestamp of the most recent lifecycle state mutation.
* `entrypoint`: Identifies session launch context (`claude-vscode`, `cli`, `antigravity`).
* `kind`: Interactive vs headless mode.
* `version`: Agent software version.
* `context_pct`: Current context window token consumption percentage.
* `git_branch`: Current Git branch associated with the session workspace.
* `worktree_name`: Associated Git worktree directory if applicable.

---

## 5. Linux Service Management (`systemd`)

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
Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

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
