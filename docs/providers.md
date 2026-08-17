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

## 4. Subagent Isolation

Child subagents (e.g. Claude Code Explore/Task sidechains or Antigravity subtrajectories) are filtered out:
* Hook payloads with `is_sidechain: true` or non-empty `agent_id` are discarded.
* Transcript directories ignore `<parent-id>/subagents/` subfolders.
