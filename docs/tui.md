# Ackbar TUI Client (`ackbar`)

## 1. Dashboard Layout & Visual Design

The TUI is built with Charm.sh Bubble Tea and Lip Gloss, presenting a responsive dashboard:

```
┌─ PROJECT ACKBAR (v20260814.05) ─────────────────────────────────────────────┐
│ 🌐 Hosts: local 🟢 (v20260814.05) | legion 🟢 (v20260814.05)                │
│                                                                             │
│ ▼ Modemobile/NGL/ngl-ios                                                    │
│   ● "NGL iOS Immersive Background" [VSCODE] [ctx: 58%] @local 🟢 (active)   │
│ ▼ Modemobile/NGL/ngl-android                                                │
│   ● "Immersive-Background" [tmux: Immersive-Background] [ctx: 66%] @legion 🟢│
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Keybindings & Interactions

| Key | Action | Description |
| :--- | :--- | :--- |
| `↑` / `k`, `↓` / `j` | Navigate | Move cursor up and down the tree. |
| `Space` / `Enter` / `a` | Toggle / Attach | Expand/collapse groups or attach in-place to tmux session. |
| `t` | Open in New Tab | Spawn a new terminal tab or window (iTerm2, Ghostty, WezTerm, Terminal.app, Tmux). |
| `N` | New Project / Subgroup | Launch wizard to create a project folder or empty category subgroup. |
| `M` | Move Node / Session | Move highlighted group or session to a different logical tree path. |
| `s` | Spawn Session | Launch a new managed tmux session in the highlighted project directory. |
| `o` | Open in VSCode | Launch `code <path>` in the session's workspace directory. |
| `V` | View Documents | View workspace plans (`task.md`, `implementation_plan.md`, `walkthrough.md`). |
| `H` | Agent Discovery | Discover installed agent binaries and configure missing hook handlers. |
| `R` | Register Host | Wizard to connect and cross-compile `ackbard` for a remote SSH machine. |
| `r` | Restart Session | Restart selected session in a managed tmux session. |
| `k` | Terminate (Kill) | Send SIGKILL to terminate the agent process/tmux session. |
| `P` | Purge & Re-index | Safe database maintenance: purges stale entries while strictly preserving groups. |
| `x` | Archive Session | Toggle session between active and archived views. |
| `v` | Toggle Archived View | Switch between active and archived session lists. |
| `d` | Delete Session / Group | Delete session or empty category group node. |

---

## 3. Pure Subgroups vs. Project Folders

* **Project Folder Groups:** Created with a filesystem path. Any agent started in that directory or its subdirectories is automatically mapped into this group.
* **Empty Subgroups (Category Nodes):** Created by pressing `Enter` on an empty folder path in the `N` wizard. Used for organizing sessions manually via `M`.
