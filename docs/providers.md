# Agent Provider Adapters & Metadata Integration

## 1. Supported Providers

Ackbar supports the three major agent ecosystems:
1. **Claude Code (`claude-code`)**
2. **Google Antigravity (`antigravity`)**
3. **OpenAI Codex (`codex`)**

---

## 2. Metadata Sources by Provider

### Claude Code
* **Session Registry:** `~/.claude/sessions/<pid>.json` contains process PID, native UUID, CWD, custom session name, entrypoint (`claude-vscode`, `cli`), kind, and version.
* **Transcripts:** `~/.claude/projects/-encoded-cwd/<session-id>.jsonl` contains execution history, model name, `customTitle`, `aiTitle`, and token usage blocks.
* **Context Usage:** Calculated from active tokens (`input_tokens + cache_creation + cache_read`) against dynamic model family context limits.

### Antigravity
* **Session Annotations:** `~/.gemini/antigravity/annotations/<session-id>.pbtxt` contains custom user-assigned titles (`title: "..."`).
* **Artifact Metadata:** `task.md.metadata.json`, `implementation_plan.md`, `walkthrough.md` under `~/.gemini/antigravity/brain/<session-id>/`.

---

## 3. Dynamic Model Context Window Ceilings

Token usage percentage calculation dynamically adapts to model families:

| Model Family / Patterns | Context Limit | Example Models |
| :--- | :--- | :--- |
| `fable`, `opus`, `sonnet-4/5`, `claude-4/5`, `1m`, `gemini` | **1,000,000 tokens (1M)** | `claude-fable-5`, `claude-opus-5`, `gemini-1.5-pro` |
| `gpt-4`, `codex` | **128,000 tokens (128k)** | `gpt-4o`, `codex-exec` |
| Standard / Legacy (`claude-3-5-*`, `claude-3-7-*`) | **200,000 tokens (200k)** | `claude-3-5-sonnet`, `claude-3-7-sonnet` |

---

## 4. Subagent Discovery & Isolation

Child subagents (e.g. Claude Code Explore/Task teammates or Antigravity subtrajectories) are filtered from polluting the top-level tree as standalone sessions, but their live lifecycle is actively tracked under the parent session:
* **Session Tree Isolation:** Hook payloads with `is_sidechain: true` or non-empty `agent_id` are ignored as top-level sessions.
* **Structured Discovery (`Provider.ListSubagents`):**
  * **Claude Code:** Discovers teammates from `~/.claude/projects/<slug>/<session-id>/subagents/` (`agent-*.meta.json` and companion `.jsonl` stop reasons) and team configurations in `~/.claude/teams/*/config.json`.
  * **Antigravity:** Discovers subagents from `.system_generated/subagents/*.json` across brain directories and profiles.
* **Working State Reflection:** When subagents are actively running, parent sessions evaluate to `StateWorking` (1) with dynamic activity descriptions (`Subagent running: <name>`) and active spinners.

---

## 5. Multi-Account Profile Management

Ackbar supports running separate account profiles (e.g. Work, Personal, Client) for Claude Code and Google Antigravity across local and remote fleet hosts.

### Isolation Architecture
* **Claude Code (`claude-code`):** Each non-default profile maps to `~/.claude-profiles/<name>/` and is launched with `CLAUDE_CONFIG_DIR=~/.claude-profiles/<name>`. Pre-configured HTTP hooks automatically route to `http://127.0.0.1:7777/v1/hooks/claude-code?account=<name>`.
* **Google Antigravity (`antigravity`):** Profiles isolate configurations under `~/.gemini-profiles/<name>/` via `GEMINI_CLI_HOME=~/.gemini-profiles/<name>`.
* **Single-Profile Presentation:** If an agent only has the default profile configured, the account selector is hidden in the UI and account badges are omitted, keeping single-profile workspaces clean.
* **Fleet Propagation:** When adding an account (`--all-hosts` or via the Settings modal), Ackbar automatically checks agent discovery (`/v1/agents/discovery`) on each host and registers the profile only on hosts where the agent is installed.
* **Group Memory:** Ackbar remembers the preferred account for each group/subgroup on each host (`by_host[host].account`), pre-selecting it when spawning subsequent sessions.

