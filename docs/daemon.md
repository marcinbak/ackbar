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
| `GET` | `/v1/version` | Returns current daemon version, canonical host name, and display name. |
| `GET` | `/v1/sessions` | Returns all active, managed, and historic sessions with unread state. |
| `GET` | `/v1/events` | SSE stream broadcasting real-time session mutations and state changes. |
| `GET` | `/v1/sessions/pty` | WebSocket endpoint streaming interactive PTY data to `xterm.js`. |
| `POST` | `/v1/sessions/spawn` | Spawns a new agent process in a supervised tmux session using RFC 4122 UUIDv4 (supports prompt injection). |
| `POST` | `/v1/meta/resolve` | Derives host, agent, group, and existing session match from prompt via Jev AI / heuristics. |
| `POST` | `/v1/hooks/{agent}` | Ingests agent lifecycle and tool hook events with in-place `/clear` rotation detection. |
| `POST` | `/v1/sessions/control` | Executes control actions: `resume`, `restart`, `kill`, `move`, `rename`, `delete`, `handover`. |
| `POST` | `/v1/sessions/handover`| Initiates automated session handover, briefing extraction, and context rotation. |
| `POST` | `/v1/sessions/mark-read` | Marks a session as read (`is_unread = false`), clearing visual unread cues. |
| `GET` | `/v1/sessions/transcript`| Retrieves extracted conversation transcript (JSON or formatted Markdown). |
| `POST` | `/v1/sessions/upload` | Uploads clipboard images or drag-and-dropped PDFs to `/tmp/ackbar-uploads/`. |
| `GET` | `/v1/settings` | Returns daemon settings including `host_name`, `display_name`, auto-done thresholds, and handover suggestions. |
| `POST` | `/v1/settings` | Updates and persists daemon settings in SQLite. |
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

### Handover Settings (`/v1/settings`):
* `handover_suggestion_enabled`: `"true"` | `"false"` (default: `"true"`) — toggle automated UI suggestions.
* `handover_threshold_pct`: Integer between `10` and `95` (default: `"60"`) — context window threshold for triggering warnings and quick-action chips.

---

## 5. Database Schema & Auto-Migrations

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

---

## 6. Host Identity & Display Name Configuration

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

