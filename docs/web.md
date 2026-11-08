# Ackbar Web Dashboard & PWA (`web/`)

> **Real-time browser control plane and terminal multiplexer served directly by `ackbard`.**

---

## 1. Overview & Architecture

The **Ackbar Web Dashboard** is a zero-dependency, high-performance web interface built with vanilla modern JavaScript, CSS custom properties, and `xterm.js`.

It is embedded directly into the `ackbard` Go binary and served on `http://127.0.0.1:7777`.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           BROWSER CLIENT / PWA                              │
│                                                                             │
│  ┌───────────────────────┐  ┌────────────────────────────────────────────┐  │
│  │   Sidebar Tree View   │  │       Multi-Tab xterm.js Terminal Tabs     │  │
│  │ (Drag-and-Drop Order) │  │       (Interactive WebSocket Streams)      │  │
│  └───────────┬───────────┘  └─────────────────────┬──────────────────────┘  │
│              │                                    │                         │
│              │ REST API & SSE (/v1/events)        │ WebSocket PTY           │
│              ▼                                    ▼ (/v1/sessions/pty)      │
└──────────────┼────────────────────────────────────┼─────────────────────────┘
               │                                    │
               ▼                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        ACKBARD DAEMON ENGINE (Go)                           │
│                 (HTTP Server, SQLite DB, Tmux Supervisor)                   │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Key Features

### 🖥️ 1. Multi-Tab Terminal Multiplexer
* Powered by `xterm.js` with `FitAddon` and `WebglAddon` (where hardware acceleration is available).
* Attach simultaneously to multiple local and remote agent sessions.
* Full keyboard fidelity, ANSI color rendering, mouse wheel scrolling, and window resize propagation.
* **No redundant redraws:** `sendTerminalResize` only sends a resize when the size differs from the last one sent on that socket. Reconnects always re-send. Switching a tab from chat to terminal view repaints the xterm viewport locally (`terminal.refresh`) and never sends Ctrl+L to the agent. Each extra resize or Ctrl+L made agents such as Antigravity clear and replay their whole transcript. See [daemon PTY sizing](daemon.md#pty-terminal-sizing-v1sessionspty).
* **Mutually Exclusive Execution Modes (`headless` Chat ↔ `tmux` Terminal):** Each session runs in either turn-by-turn **Headless Chat mode** (`engine_type = "headless"`, mounted strictly with `.session-chat-view` and zero `xterm.js` instances) or interactive **Tmux Terminal mode** (`engine_type = "tmux"`, mounted strictly with `.session-term-view` and `xterm.js`). Users can switch a session between modes at any time via **`🏎️ Take the Wheel in Terminal`** (`POST /v1/sessions/take-wheel`, which stops any active headless turn and resumes `native_id` in `tmux`), **`💬 Switch to Headless Mode`** (`POST /v1/sessions/switch-headless`, which stops the `tmux` session and transitions the tab to headless chat), the sidebar context menu, or the statusbar engine badge (`#sbModelBadge`).
* **Single-View Mode (`hide_session_tabs` Setting):** Configurable under **⚙️ Settings → Workspace Tab Bar (`Hide Session Tabs (Single-View Mode)`)**. When enabled (`hide_session_tabs = "true"`), `.tab-strip-wrapper` is hidden to reclaim vertical space, and selecting a session, details panel, transcript, or document in the sidebar tree opens it in the same single window (closing previously open views and hiding `↗ Open in New Tab` in the context menu).
* **Headless Chat Focus & Voice Dictation Support:** Headless tabs do not instantiate `xterm.js` or `.xterm-helper-textarea`, eliminating hidden textarea focus competition. `.chat-composer-textarea` deterministically handles `text/plain` clipboard pastes in JavaScript (both when focused and at the document capture level), includes a `Cmd+V` / `Ctrl+V` `keydown` clipboard fallback for macOS dictation overlays (such as Wispr Flow), re-asserts native responder focus on window focus return, and guards composer DOM attributes (`placeholder`, `innerHTML`) against unnecessary mutations during live SSE updates.
* **Auto-Healing Turn Recovery:** When returning to an inactive session or waking up from laptop sleep, the tab auto-detects finished turns from background SSE events (`/v1/events`) and auto-reconnects live streams on tab switch and focus.
* **Collision-Safe Tab Identity Migration:** When a spawned session rotates from its temporary placeholder ID to its native agent conversation UUID (`old_id` $\rightarrow$ `id`), `tabs.js` deduplicates open tabs by `id`, `old_id`, `tmux_name`, and `host + agent + native_id`, migrating the live tab in place (and reconnecting chat/terminal streams) without creating duplicate or uncloseable orphan DOM tabs.
* **Tab Right-Click Context Menu:** Right-click any tab for quick lifecycle actions:
  * **✕ Close Tab:** Closes the target tab.
  * **🗂️ Close Other Tabs:** Closes all open tabs except the selected one.
  * **➡️ Close Tabs to the Right:** Closes all tabs to the right of the active tab.
  * **🗑️ Close All Tabs:** Closes all open tabs and returns to the dashboard empty state.

### 🌳 2. Hierarchical Sidebar Tree & State Indicators
* Automatic mapping of sessions to Git repositories and custom category folders.
* Drag-and-drop ordering of session nodes and project groups.
* **Animated Status Badges:**
  * `⚙️` *(animated spinner)* **WORKING:** Agent is actively generating or executing tools.
  * `❓` **BLOCKED:** Agent is waiting for user confirmation or choice.
  * `✅` **IDLE:** Turn completed, awaiting next prompt.
  * `⏹️` **ENDED:** Process finished or cleared into a new turn.
  * `🛑` **FAILED:** Process crashed or error.
  * `◌` **STANDBY:** Unmanaged session or host disconnected.
* **Unread vs. Read State Cue:** A pulsing dot indicates unread transitions to `BLOCKED` or `IDLE`. Focusing or clicking the session automatically marks it as read.
* **⏳ Later Section & Collapsible Parking:** Each project group and Unassigned folder contains a collapsible `⏳ Later` section for parked sessions. Sessions in Later are immune to auto-completion (`auto_done`) and auto-archival, and remain in Later during prompt interaction until explicitly moved to Active or Done.
* **1-Click Conversation Resumption:** Ended turns can be resumed directly into a new tmux tab via right-click **`▶ Resume in Tmux`** or the details view button.
* Context token usage meter (adapting dynamically to 200k, 1M, and 128k token limits).

### ⚡ 3. Command Palette (`Cmd+K` / `Ctrl+K`)
* Instant fuzzy search across all sessions, projects, and remote hosts.
* Quick actions: spawn session, switch tab, open in VS Code, and filter by status.

### 💻 4. One-Click VS Code Bridge
* Right-click any session or click the editor button to open the exact workspace in VS Code.
* Automatically resolves `code --remote ssh-remote+<host> <path>` for remote machines.

### 📖 5. In-App Plan & Markdown Viewer (Right Inspector Pane)
* **Collapsible Right Inspector Pane (`#inspectorPanel`):** **Session Details (`ℹ`)**, **Markdown Plans & Project Docs (`📄`)**, and **Conversation Transcripts (`📜`)** open in a collapsible, drag-resizable Right Inspector Pane with a header and **`✕` Close** button (`#btnCloseInspector`) so they can be viewed side-by-side without replacing the active session's Chat or Terminal view.
* Native markdown renderer (`marked.js`) with syntax copy buttons for viewing `implementation_plan.md`, `task.md`, `walkthrough.md`, and project documentation alongside live terminal and chat sessions.
* Width is drag-resizable via `#inspectorResizer` (`ackbar_inspector_width` in `localStorage`) and automatically refits the active `xterm.js` terminal on open, close, and resize.

### 📎 6. Clipboard Image Paste & File Drag-and-Drop
* **Instant Clipboard Paste:** Paste screenshots (`Cmd+V`) directly into any active terminal tab.
* **Drag-and-Drop Attachments:** Drop image files (`PNG`, `JPG`, `JPEG`, `WEBP`, `GIF`, `BMP`, `SVG`) or `PDF` documents directly onto the terminal.
* **Automatic Upload & Path Injection:** Uploads files to `/tmp/ackbar-uploads/` on the local machine or remote compute hosts (e.g. Legion) and injects the quoted file path into the terminal prompt.
* **Automatic Garbage Collection:** Background cleaner prunes temporary uploads older than 7 days.

### 🌐 7. Smart Cross-Host Folder Mapping & Autocomplete
* **Host-Scoped Path Suggestions:** Folder suggestions (`<datalist>`) dynamically filter to show paths previously active on the selected host.
* **Intelligent Cross-Host Translation:** When switching between `local` (macOS) and remote machines (e.g. `legion` on Linux), the folder path automatically translates based on:
  1. *Project Matching:* Existing sessions sharing the same Git project key or directory basename.
  2. *Configured Remote Roots:* Target host configured `remote_cwd` (e.g. `/home/dev4u/Work`).
  3. *Home Prefix Mapping:* Automatic `/Users/<user>/...` $\longleftrightarrow$ `/home/<user>/...` translation.
* **Auto-Discovery of Installed Agents:** Dynamically detects and updates installed agent options (`Claude Code`, `Google Antigravity`, `OpenAI Codex`) whenever the host dropdown changes.

### 📋 8. Work & Fleet Overview Kanban Board
* **Dual View Modes:** Seamlessly switch between **`[💻 Workspace]`** (multi-tab terminals & agent chats) and **`[📋 Work Board]`** (cross-host agent Kanban dashboard).
* **Strict Explicit Task Creation & Pure MCP Telemetry:** Tasks are created exclusively via `ackbar mcp` (`create_task`, `propose_task`) or the Work Board **`[+ New Task]`** modal (`POST /v1/tasks`), eliminating ambient background scrapers.
* **Canonical `tree_nodes` Scoping:** Tasks bind directly to canonical `tree_nodes` paths (e.g. `Modemobile/NGL/ngl-ios`, `Personal/Ackbar`), deriving `group_name` (top segment) and `project_name` (remaining sub-path) consistently across Group pills and the Project filter dropdown.
* **4-Pillar Lifecycle Columns:**
  * `NEW`: Backlog & proposed items (`ready` / `discovered`) with a 1-click **`[▶ Start]`** button that opens a lightweight Start modal pre-filled from `node_host_paths`, spawns a dedicated `📋 Task` session bound to `task.id`, transitions the task to `IN_PROGRESS`, and opens the session tab.
  * `IN_PROGRESS`: Active agent execution (`active`, `blocked`, `question`, `stalled`). If all worker sessions for an `IN_PROGRESS` task end without reaching `REVIEW` or `DONE`, the card is marked `stalled` with 1-click **`[▶ Resume]`**, **`[✓ Done]`**, and **`[🗑️ Delete]`** actions.
  * `REVIEW`: Human checkpoint for plans (`plan_review`), designs (`design_review`), and pull requests (`pr_review` / `in_review`).
  * `DONE`: Completed & merged tasks. Capped by default to tasks completed in the last 7 days with a **`[Show older (N)]`** toggle at the bottom of the column.
* **On-Demand PR Reconciliation & Lazy Deliverables:** Opening or refreshing the Work Board checks open PR states (`GET /v1/tasks?federated=true&reconcile_prs=true`) without background polling, and deliverable markdown (`content_md`) is loaded lazily on demand (`GET /v1/tasks/deliverable?id=<id>`) when a deliverable chip is clicked.
* **Zero Context-Switch Navigation:** Clicking any active worker pill on a task card switches straight to Workspace mode and focuses the agent session tab (`openSessionInTab` / `activateTab`).

### 🎙️ 9. Voice Companion & 2-Tier Lazy Speech Pipeline (`chat.js`)
* **Header Controls:**
  * **`[🔊 Auto-Read]` (`.btn-chat-autoread`):** Toggle automated voice playback of incoming assistant messages on turn completion (`turn_complete` SSE event). Preference persists in `localStorage` (`ackbar_chat_autoread`).
  * **`[🎤 Mic]` (`.btn-chat-mic`):** Voice dictation via Web Speech API (`SpeechRecognition` / `webkitSpeechRecognition`) with pulsing red recording animation. Includes punctuation-resilient keyword navigation (`"repeat that"`, `"replay"`, `"read that again"`) and automatic concise prompt instruction appending when Auto-Read is active.
* **Per-Message Spoken Controls:**
  * **`[🔊 Read]` (`.btn-read-chat-msg`):** Speaks the message. Short conversational turns (<120 words, simple prose) synthesize immediately via browser SpeechSynthesis. Complex structural turns lazily query `POST /v1/sessions/summarize-speech` to synthesize an Outline-First spoken briefing with suggested chips.
  * **`[⏹️ Stop]` (`.btn-stop-chat-msg`):** Stops active speech playback immediately. Speech also cancels automatically when user dispatches a new prompt, cancels a turn, or encounters an error.
  * **`[↺ Read Again]` (`.btn-replay-chat-msg`):** Replays the cached spoken briefing without re-querying the backend.
  * **`[📖 Read Full]` (`.btn-full-read-chat-msg`):** Bypasses the outline briefing to read the full sanitized message aloud.
* **Commuter Quick-Reply Chips (`.chat-suggested-chips`):**
  * Displays interactive drill-down buttons (e.g. `[Hear Schema Changes]`, `[Hear Open Question]`, `[Approve Plan]`) below assistant turns for 1-tap/hands-free plan exploration.

### 🎛️ 10. Headless UX Controls, Interactive Tool Cards & Autocomplete (`chat.js`)
* **Composer Status Pill Bar & `/config` Popover (`.chat-composer-bar`, `.chat-config-popover`):**
  * Displays interactive pills above the chat input for **Model**, **Reasoning Effort** (`low`, `medium`, `high`, `max`, hidden for Antigravity sessions), **Execution Mode** (`⚡ Auto Mode` vs `📝 Plan Mode`), and **Context Usage** (`% Context` gauge derived from `session.context_pct` with agent-aware fallback limits: 1M tokens for Antigravity, 200k tokens for Claude Code).
  * Clicking `⚙️ /config` (or typing `/config`) opens the Session Configuration popover to adjust model, effort, and execution mode (`PATCH /v1/sessions/{id}/config`) and inspect context window usage.
  * Dynamically queries `GET /v1/sessions/{id}/suggestions?kind=models` to populate agent-specific model catalogs (e.g. Gemini 4 Argon, Gemini 3.8 Flash, GPT-OSS for Antigravity; Claude Fable 5.1, Claude Opus/Sonnet for Claude Code).
* **`/` Command, `@` File, and Model Autocomplete (`.chat-autocomplete-menu`):**
  * Typing `/` at the start of input queries `GET /v1/sessions/{id}/suggestions?kind=commands`, surfacing built-in client actions (`/config`, `/plan`, `/clear`, `/compact`, `/handover`, `/terminal`) alongside discovered workspace workflows (`.claude/commands/*.md`, `.agents/workflows/*.md`) and skills (`SKILL.md`).
  * Typing `@<query>` after start-of-input or whitespace queries `GET /v1/sessions/{id}/suggestions?kind=files` to fuzzy-match relative workspace files.
  * Queries `GET /v1/sessions/{id}/suggestions?kind=models` for agent-scoped model suggestion catalogs.
* **Interactive Question, Plan Approval & Task Cards:**
  * **`AskUserQuestion` / `ask_question` (`.chat-interactive-card`):** Renders single- and multi-question forms supporting radio buttons, `multi_select` checkboxes, and custom `"Other"` text answers, submitting directly to `POST /v1/sessions/respond`.
  * **`ExitPlanMode` (`.card-approval`):** Renders the markdown plan preview with **`✓ Approve & Execute`** (auto-switching the session from `Plan Mode` to `Auto Mode`) and **`Request Changes`** feedback input.
  * **`TodoWrite` / `todo_write` (`.chat-todo-card`):** Renders a read-only live Task Progress checklist (`completed`, `in_progress`, `pending`) both during live SSE streaming and in historical transcripts.
* **Markdown Image Cards, Captions & Interactive Carousels (`chat.js`, `utils.js`):**
  * **Inline Image Cards & Captions (`.chat-file-preview-card`, `.chat-file-preview-caption`):** Automatically rewrites local Markdown image links (`![Alt](path/to/img.png)`) into interactive preview cards with Open in Preview action and subtle captions whenever alt text provides non-filename context.
  * **Interactive Carousels (`.chat-carousel`):** Preprocesses 3- and 4-backtick ````carousel blocks and unfenced `<!-- slide -->` separators into tabbed / step carousels featuring slide counters (`1 / N`), `‹ Prev` / `Next ›` navigation controls, and a `Grid` / `Slide` view switcher for side-by-side multi-column comparison.
  * **Cross-OS Home Fallback (`/Users/<user>` ↔ `/home/<user>`):** Translates absolute home directory image paths between macOS and Linux in `/v1/files/content` while enforcing sensitive credential blocks.

### 📊 11. Mermaid Flowchart & Architecture Diagram Rendering (`mermaid.js`)
* **Automatic Detection:** Automatically identifies Mermaid diagrams in markdown code fences (````mermaid`) as well as unlabelled code blocks whose first non-comment line begins with valid diagram syntax (`flowchart`, `graph`, `sequenceDiagram`, `stateDiagram`, `classDiagram`, `erDiagram`, `gantt`, `pie`, `gitGraph`, `mindmap`, `timeline`, `xychart-beta`, `C4Context`, etc.).
* **Zero-Flicker Streaming Cache:** `mermaidSvgCache` caches rendered SVGs by diagram source text, enabling instant synchronous upgrades during live SSE token streaming (`text_delta`) without layout shift or redraw flicker.
* **Dual View Mode (`[Diagram]` / `[Code]`):** Each diagram block renders an integrated toolbar featuring a view mode toggle (`.btn-mermaid-toggle`) to switch seamlessly between the graphical SVG diagram and the raw flowchart text.
* **1-Click Source Copy:** Includes a dedicated **`[Copy Text]`** button (`.btn-copy-mermaid`) to copy the diagram source directly to the clipboard.
* **Multi-Surface Integration:** Enabled across interactive chat transcripts, `ExitPlanMode` plan approval cards, Right Inspector Pane markdown document/plan viewers, and Kanban task deliverable modals.


---

## 3. Keyboard Shortcuts

| Shortcut | Action |
| :--- | :--- |
| `Cmd+K` / `Ctrl+K` | Open Command Palette |
| `Cmd+T` / `Ctrl+T` | Create New Session |
| `Cmd+W` / `Ctrl+W` | Close Active Terminal Tab |
| `Cmd+1` ... `Cmd+9` | Switch to Terminal Tab 1–9 |
| `Escape` | Dismiss Modals & Command Palette |
| `Ctrl+Shift+R` | Force Refresh Daemon State |

---

## 4. Progressive Web App (PWA) Support

The Web Dashboard includes a valid Web App Manifest (`internal/web/manifest.json`) and service worker hooks. You can install it as a standalone desktop app via Google Chrome, Arc, Edge, or Safari (*"Add to Dock / Install Ackbar"*).

---

## 5. Modular Frontend Architecture (`web/js/`)

The web frontend is organized as zero-build native ECMAScript modules (`<script type="module">`) embedded directly into the Go binary via `//go:embed index.html style.css app.js manifest.json js/*.js`:

* **`state.js`**: Central application state (`sessions`, `hosts`, `openTabs`, `treeNodes`), element cache `el`, and localStorage persistence.
* **`auth.js`**: Token management and `window.fetch` interceptor with automatic 401 token authentication prompt.
* **`utils.js`**: Markdown setup and rendering (`marked.js`), formatting helpers, host utilities, and path translation.
* **`api.js`**: REST client endpoints (`/v1/sessions`, `/v1/hosts`, `/v1/nodes`, `/v1/settings`, etc.).
* **`sse.js`**: Server-Sent Events multi-host subscriber and real-time state synchronization.
* **`tree.js`**: Sidebar tree hierarchy, drag-and-drop reordering, and done / auto-archived sections.
* **`tasks.js`**: Work & Fleet Overview Kanban board, 4-pillar task lifecycle, organization/project filters, and zero context-switch workspace tab navigation.
* **`tabs.js`**: Terminal and chat tab strip lifecycle, overflow menu, and persistence.
* **`terminal.js`**: `xterm.js` terminal tabs, WebSocket PTY streams, resizing, and attachment uploads.
* **`chat.js`**: Interactive chat interface, streaming transcript, tool call formatters, and composer queue.
* **`mermaid.js`**: Mermaid flowchart and diagram parser, dark-theme renderer, zero-flicker SVG cache, and interactive Diagram/Code view switcher.
* **`details.js`**: Right Inspector Pane (`#inspectorPanel`) controller (`openInspectorPane`, `closeInspectorPane`, `refitActiveTerminal`), session details view (`openSessionDetailsTab`), markdown plan/doc viewer (`openDocViewerTab`), and rich transcript viewer (`openTranscriptViewerTab`).
* **`palette.js`**: Command palette fuzzy search (`Cmd+K` / `Ctrl+K`) and dispatcher launcher.
* **`context-menu.js`**: Context menus for sessions, groups, tabs, and chat files.
* **`modals.js`**: Modal dialogs for new sessions, groups, hosts, settings (including Push Notifications `ntfy.sh` topic configuration, test notifications, and fleet-wide propagation), and context handover.
* **`statusbar.js`**: Status bar indicators.
* **`app.js`**: Main module entrypoint, event listeners, and lifecycle boot.
* **`web/app.js`**: Backward-compatibility proxy dynamically importing `/js/app.js`.

