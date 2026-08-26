# Ackbar TUI Client (`ackbar`)

## 1. Dashboard Layout & Visual Design

The TUI is built with Charm.sh Bubble Tea and Lip Gloss, presenting a responsive dashboard with live animations and state cues:

```
┌─ PROJECT ACKBAR (v20260826.01) ─────────────────────────────────────────────┐
│ 🌐 Hosts: local 🟢 (v20260826.01) | legion 🟢 (v20260826.01)                │
│                                                                             │
│ ▼ Modemobile/NGL/ngl-ios                                                    │
│   ◐ "NGL iOS Immersive Background" [VSCODE] [ctx: 58%] @local 🟢 (working)  │
│ ▼ Modemobile/NGL/ngl-android                                                │
│   ● ❓ "Immersive-Background" [tmux: Immersive-Background] [ctx: 66%] @legion│
└─────────────────────────────────────────────────────────────────────────────┘
```

### Live Visual Indicators:
* **Active Working Animation:** Sessions in `StateWorking` cycle through rotating quadrant symbols (`◐` $\rightarrow$ `◓` $\rightarrow$ `◑` $\rightarrow$ `◒`).
* **Unread State Indicator (`●`):** A bright accent indicator appears beside sessions that recently transitioned into `Blocked` (`❓`) or `Idle` (`✅`). Highlighting or attaching to the session automatically clears the unread state via `POST /v1/sessions/mark-read`.

---

## 2. Keybindings & Interactions

| Key | Action | Description |
| :--- | :--- | :--- |
| `↑` / `k`, `↓` / `j` | Navigate | Move cursor up and down the tree (automatically marks focused session as read). |
| `Space` / `Enter` / `a` | Toggle / Attach | Expand/collapse groups or attach in-place to tmux session. |
| `t` | Open in New Tab | Spawn a new terminal tab or window (iTerm2, Ghostty, WezTerm, Terminal.app, Tmux). |
| `N` | New Project / Subgroup | Launch wizard to create a project folder or empty category subgroup. |
| `M` | Move Node / Session | Move highlighted group or session to a different logical tree path. |
| `s` | Spawn Session | Launch a new managed tmux session in the highlighted project directory. |
| `o` | Open in VSCode | Launch `code <path>` in the session's workspace directory. |
| `V` | View Documents | View workspace plans (`task.md`, `implementation_plan.md`, `walkthrough.md`). |
| `H` | Agent Discovery | Discover installed agent binaries and configure missing hook handlers. |
| `R` | Register Host | Wizard to connect and cross-compile `ackbard` for a remote SSH machine. |
| `r` | Restart / Resume | Restart active session or resume ended/cleared conversation in a fresh tmux tab. |
| `k` | Terminate (Kill) | Send SIGKILL to terminate the agent process/tmux session. |
| `P` | Purge & Re-index | Safe database maintenance: purges stale entries while strictly preserving groups. |
| `x` | Archive Session | Toggle session between active and archived views. |
| `v` | Toggle Archived View | Switch between active and archived session lists. |
| `d` | Delete Session / Group | Delete session or empty category group node. |

---

## 3. Pure Subgroups vs. Project Folders

* **Project Folder Groups:** Created with a filesystem path. Any agent started in that directory or its subdirectories is automatically mapped into this group.
* **Empty Subgroups (Category Nodes):** Created by pressing `Enter` on an empty folder path in the `N` wizard. Used for organizing sessions manually via `M`.
