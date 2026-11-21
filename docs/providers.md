# Agent Provider Adapters & Metadata Integration

## 1. Supported Providers

Ackbar supports the major agent ecosystems:
1. **Claude Code (`claude-code`)**
2. **Google Antigravity (`antigravity`)**
3. **OpenAI Codex (`codex`)**
4. **xAI Grok (`grok`)**
5. **OpenCode (`opencode`)**

---

## 2. Metadata Sources by Provider

### Claude Code
* **Session Registry:** `~/.claude/sessions/<pid>.json` contains process PID, native UUID, CWD, custom session name, entrypoint (`claude-vscode`, `cli`), kind, and version.
* **Transcripts ([`transcript.go`](../internal/agents/claude/transcript.go), [`prompt_clean.go`](../internal/agents/claude/prompt_clean.go)):** `~/.claude/projects/-encoded-cwd/<session-id>.jsonl` stores history, model, `customTitle`, `aiTitle`, and usage blocks. `claude.Plugin` (`ExtractTranscript`, `FilterUserEntry`, `CleanPrompt`; legacy `ClaudeAgentAdapter` fallback) rejects synthetic `type: "user"` runtime entries and strips Claude-injected tags without running other agents' filters.
* **Context Usage:** Calculated from active tokens (`input_tokens + cache_creation + cache_read`) against dynamic model family context limits.
* **Headless Persistent Stdio Execution:** Runs `claude -p --input-format stream-json --output-format stream-json --verbose --permission-prompt-tool stdio` with stdin NDJSON `{"type":"user"}` discriminator.
* **In-Place Stdio Control Gates:** Handles `control_request` (`can_use_tool`) for `ExitPlanMode`, `AskUserQuestion`, and permissions via `control_response` without restarting child.
* **Headless Model Switching:** Sends stdin `control_request` (`set_model`) without restarting child; cold resume passes `--model default` to clear pinned model.
* **CLI Flags & Directory Access:** Passes `--add-dir <dir>` and `--effort`; appends `--allow-dangerously-skip-permissions` alongside `plan`, `auto`, `acceptEdits`, and `dontAsk` permission modes.
* **Hook Registration ([`provision.go`](../internal/agents/claude/provision.go), [`accounts.go`](../internal/agents/claude/accounts.go), [`main.go`](../cmd/ackbar/main.go)):** `ackbar setup-hooks` and `SetupProfileDir` register `SubagentStart` and `SubagentStop` HTTP hooks via `InstallHooks` (legacy `agent_claude.go` fallback).
* **Non-Interactive Inspection & Compaction:** Uses `claude plugin list --json`, `claude agents --json`, and `claude doctor` to prevent interactive TUI hangs. Executes native `/compact` turns in-session.
* **Transcript Rewind:** Clones `.jsonl` transcript up to chosen turn boundary, updates `"sessionId"`, and sets `0600` file permissions.
* **Default Model Resolution ([`models.go`](../internal/agents/claude/models.go)):** Resolves active model across override, runtime stream or transcript, workspace `.claude/settings{.local,}.json` (`WorkspaceModelResolver`), and CLI settings/env.

### Antigravity
* **Session Annotations:** `~/.gemini/antigravity/annotations/<session-id>.pbtxt` contains custom user-assigned titles (`title: "..."`).
* **Artifact Metadata:** `task.md.metadata.json`, `implementation_plan.md`, `walkthrough.md` under `~/.gemini/antigravity/brain/<session-id>/`.
* **Transcript XML Envelope Stripping ([`transcript_format.go`](../internal/agents/antigravity/transcript_format.go), [`transcript.go`](../internal/agents/antigravity/transcript.go)):** Antigravity wraps `USER_INPUT` entries in `transcript.jsonl` inside `<USER_REQUEST>...</USER_REQUEST>` followed by runtime metadata tags (`<ADDITIONAL_METADATA>`, `<USER_SETTINGS_CHANGE>`, `<CONTEXT_SUMMARY>`, `<SYSTEM_MESSAGE>`, `<EPHEMERAL_MESSAGE>`). `antigravity.Plugin` (`FilterUserEntry`, `ExtractTranscript`, with `AntigravityAgentAdapter` legacy fallback) rejects non-`"USER_EXPLICIT"`/`"USER"` sources, slices between the first `<USER_REQUEST>` and last `</USER_REQUEST>`, and strips standalone metadata blocks.
* **Conversation ID Adoption & Workspace Normalization:** Because Antigravity (`agy`) allocates its native conversation UUID lazily upon the first user prompt (rather than via a CLI `--session-id` flag at spawn time), `ackbard` automatically adopts the placeholder session (`tempUUID`) into the real conversation UUID across hooks, headless `"init"` events, presence `.lock` inspection, and brain transcript scans, normalizing tool-call paths (`AbsolutePath`, `Path`, `TargetFile`, `Cwd`, `DirectoryPath`, `SearchPath`) to the enclosing Git repository or `.worktrees/<branch>` root and emitting `old_id` so open client tabs migrate seamlessly.
* **Headless Persistent Stdio Execution:** Runs `agy --input-format stream-json --output-format stream-json` (omits `-p`). Dispatches turns via stdin NDJSON `{"event":"user"}` discriminator.
* **Headless Model Switching:** `agy` lacks stdin `control_request`; model switches swap child via `restart_child` using `--conversation <convID> --model <new>`.
* **CLI Flags & Directory Access:** Passes `--add-dir <dir>` for extra workspace access. Omits `--effort` flag (encoded in model IDs). Rejects `permission_mode: "dontAsk"` with `400 Bad Request`.
* **Non-Interactive Inspection & Compaction:** Avoids `agy mcp list` to protect header credentials, using `mcp-status` instead. Uses `agy plugin list --json` and `agy agents`. Compaction triggers daemon rotation to `Conv N+1`.
* **Transcript Rewind:** Rewinds by seeding bounded historical steps into a fresh conversation turn.
* **Default Model Resolution:** Resolves active model via 3 tiers: explicit override, runtime stream (`raw["init"]["model"]`) or transcript, or CLI settings/env.
* **Startup Init Race & Process Scanning:** Buffers pre-turn `{"event":"init"}` event until Turn 1. Background scanner ignores `--input-format` to avoid misclassifying warm workers.
* **Session File Cleanup (`CleanSessionFiles`):** Removes `brain/<session-id>/`, `conversations/<session-id>.*`, `annotations/<session-id>.*`, `presence/<session-id>.*`, and prunes `<session-id>` from `cache/conversation_metadata.json` across default (`~/.gemini/antigravity`, `~/.gemini/antigravity-cli`, `~/.antigravity`) and `~/.gemini-profiles/*` directories.
* **Headless Worker Plugin Environment (`buildWorkerEnv`):** Prepends `~/.gemini/antigravity-cli/bin` and `~/.gemini/antigravity/bin` to `PATH` and exports `ANTIGRAVITY_APP_DATA_DIR` so shims resolve cached binaries.
* **Plugin Status Probing (`probeWorkspacePlugins`):** Probes compiled `plugin_data/<tool>/bin/<tool>-*` binaries with `auth status --json` to prevent network downloads during status checks.

### OpenAI Codex
* **Session Index:** `~/.codex/session_index.jsonl` contains thread titles (`thread_name`), native IDs (`id`), and update timestamps.
* **Transcripts:** `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` and `~/.codex/archived_sessions/` contain structured execution history, tool invocations, and token usage blocks.
* **Hook Configuration:** `~/.codex/hooks.json` or `~/.codex/config.toml` pipes event payloads to `ackbar-hook --agent=codex`.

### xAI Grok
* **Session Storage & Usage Metrics:** `$GROK_HOME/sessions/<url-encoded-cwd>/<session-id>/` stores `summary.json`, `signals.json` (`contextTokensUsed`, `contextWindowTokens`), `chat_history.jsonl` (`<user_query>` extraction, `synthetic_reason` filtering), `updates.jsonl` (ACP fallback), and nested `session` totals in `usage.json` (`1 USD = 1e10 ticks`).
* **Headless Persistent ACP Stdio ([`headless.go`](../internal/agents/grok/headless.go), [`acp_driver.go`](../internal/agents/grok/acp_driver.go)):** Runs `grok agent stdio --no-auto-update` over JSON-RPC 2.0; authenticates (`xai.api_key` or `cached_token`) and resumes via `session/load`.
* **ACP Control, Switching & Rewind ([`acp_control.go`](../internal/agents/grok/acp_control.go)):** Handles `session/request_permission`, `_x.ai/exit_plan_mode`, `_x.ai/ask_user_question`, live `session/set_model`/`session/set_mode`, `session/cancel`, and `_x.ai/rewind/*`.
* **CLI Flags & Directory Access:** Passes `-m`, `--effort`, `--permission-mode`, `--add-dir` on initial spawn (`!IsExisting`), and `--always-approve` only on `yolo_mode`/`bypassPermissions`.
* **Dynamic Model Discovery ([`acp_models.go`](../internal/agents/grok/acp_models.go)):** Reads `$GROK_HOME/acp_models_cache.json` (from `_x.ai/models/update`) ahead of `~/.grok/models_cache.json`, filtering `hidden: true`.
* **Slash Command Catalog (`CommandCatalog`):** Merges `$GROK_HOME/acp_commands_cache.json` with native `/compact`, `/context`, `/always-approve`, `/goal`, and `/workflow` commands.
* **CLI Lifecycle & Turn Rotation:** Spawns interactive turns `grok -s <uuid>` with `-- "$ACKBAR_INIT_PROMPT"`, resumes `grok -r <id>`, and resolves `/clear` rotation ownership via `$GROK_HOME/active_sessions.json` (`ResolveSessionOwner`).
* **Hook Configuration:** `~/.grok/hooks/ackbar.json` (`{"hooks": {...}}` native HTTP hooks to `http://127.0.0.1:7777/v1/hooks/grok`, `matcher` omitted) and `[compat.claude] hooks = false` in `~/.grok/config.toml`.

### OpenCode
* **Session Index:** `~/.opencode/sessions/` directory contains active and completed sessions in JSONL format (`<id>.jsonl` or `<id>/transcript.jsonl`).
* **Transcripts:** Records message history, tool calls, user queries, and token usage blocks.
* **Hook Configuration:** `~/.opencode/hooks.json`, `~/.opencode/config.json`, or `~/.opencode/config.toml` pipes event payloads to `ackbar-hook --agent=opencode`.

---

## 3. Dynamic Model Context Window Ceilings

Token usage percentage calculation dynamically adapts to model families:

| Model Family / Patterns | Context Limit | Example Models |
| :--- | :--- | :--- |
| `fable`, `opus`, `sonnet-4/5`, `claude-4/5`, `1m`, `gemini` | **1,000,000 tokens (1M)** | `claude-fable-5`, `claude-opus-5`, `gemini-1.5-pro`, `gemini-4-carbon-high` |
| `gpt-4`, `codex` | **128,000 tokens (128k)** | `gpt-4o`, `codex-exec` |
| Standard / Legacy (`claude-3-5-*`, `claude-3-7-*`) | **200,000 tokens (200k)** | `claude-3-5-sonnet`, `claude-3-7-sonnet` |

---

## 4. Subagent Discovery & Isolation

Child subagents (e.g. Claude Code Explore/Task teammates or Antigravity subtrajectories) are filtered from polluting the top-level tree as standalone sessions, while their live lifecycle is actively tracked under the parent session:
* **Session Tree Isolation & Parent Hook Routing:**
  * Hook payloads with `is_sidechain: true` or non-empty `agent_id` are ignored as top-level sessions.
  * **Claude Subagent Lifecycle Preservation ([`server_hooks.go`](../internal/daemon/server_hooks.go)):** Keeps `SubagentStart` counts active across `Stop` hooks until `SubagentStop`, `SessionEnd`, or warm worker exit.
  * For Antigravity (`invoke_subagent`), subagent conversations lack an `is_sidechain` hook field. `ackbard` identifies subagents via `<brain>/<parent>/.system_generated/subagents/<subagent>.json`, `step_index: 0` `SYSTEM_MESSAGE` steps containing `<SYSTEM_MESSAGE> ... sender=<parent-uuid>` (while protecting user-initiated root sessions whose `step_index: 0` is `USER_INPUT` or that have user `.pbtxt` annotations), and `.system_generated/messages/*.json` invocation entries.
  * Incoming `/v1/hooks/antigravity` events for a subagent `conversationId` delete any stray standalone subagent row and route the update (`RunningSubagents`, `StateWorking` activity `Subagent (<role>): <tool>`, `StateBlocked` propagation when a subagent invokes `ask_permission` / `ask_custom_permission` / `ask_question`, and headless SSE `EmitSubagentsUpdate`) to the resolved parent session via `antigravity.Plugin.ResolveSubagentParent` ([`subagent_resolve.go`](../internal/agents/antigravity/subagent_resolve.go)) or legacy `ResolveAntigravitySubagentParent`, clearing the parent's `Blocked` state back to `StateWorking` via `SubagentBlockInspector` once the subagent resumes.
  * For Grok, subagent sessions reside in `<sessionDir>/subagents/*.jsonl` and are filtered via `IsSubagentSession` so child sessions never pollute the top-level tree. Parent routing uses `grok.ResolveSubagentParent` to attribute child activity to the parent session.
* **Structured Discovery (`Provider.ListSubagents`):**
  * **Claude Code:** Discovers teammates from `~/.claude/projects/<slug>/<session-id>/subagents/` (`agent-*.meta.json` and companion `.jsonl` non-empty `stop_reason` inspection across 64KB tails, ignoring trailing attachment entries) and team configurations in `~/.claude/teams/*/config.json` (which default to `"completed"` unless backed by an active subagent transcript).
  * **Antigravity:** Discovers subagents from `.system_generated/subagents/*.json` across default and profile brain directories (`~/.gemini/antigravity/brain`, `~/.gemini/antigravity-cli/brain`, `~/.antigravity/brain`, `~/.gemini-profiles/*/brain`). Because Antigravity records `"state": "SUBAGENT_STATE_ALIVE"` in `.json` sidecars, `ListSubagents` inspects each subagent's own `<brain>/<subagent-id>/.system_generated/logs/transcript.jsonl` to determine whether the subagent is actively `"running"` vs `"completed"` (final `PLANNER_RESPONSE` with `DONE`/`CANCELED`/`ERROR` and no `tool_calls`) and extracts the subagent's initial task prompt and start timestamp from `step_index: 0`.
  * **Grok:** Discovers subagents from `<sessionDir>/subagents/*.jsonl` via `ListSubagents`. Parses subagent execution history, model info, task queries, and completion status from JSONL record tails.
* **Working & Blocked State Inspection (`InspectClaudeStatus`, `antigravity.Plugin.InspectStatus` & `grok.Plugin.InspectStatus`):**
  * When subagents are actively running and neither the live terminal nor parent/subagent transcripts are blocked, parent sessions evaluate to `StateWorking` (1) with dynamic activity descriptions (`Subagent running: <name>`) and active spinners.
  * **Grok Status & Usage Inspection (`grok.Plugin.InspectStatus`, `ComputeTranscriptUsage`):** Evaluates live execution state across `signals.json` (`contextTokensUsed`, `contextWindowTokens`), `summary.json`, `chat_history.jsonl`, running subagents, and nested `session` totals in `usage.json` (`1 USD = 1e10 ticks`), mapping active tool calls to `StateWorking` or `StateBlocked`.
  * **Antigravity Permission, Question & Transcript Inspection:**
    * **Live Tmux Permission Detection (`detectAntigravityPermissionPrompt`):** Detects all Antigravity CLI `ToolConfirmationModel`, `ExpandedView`, `SubagentDetailModel`, and `ConversationModel.renderMultiLineSubagentAlert` permission prompts (including `"Run this command?"`, `"Accept this file edit?"`, `"Allow creation of this file?"`, `"Allow access to this file?"`, `"Allow access to this URL?"`, `"Allow sandbox bypass for command execution?"`, `"Allow administrator elevation?"`, `"Grant requested permission?"`, `"Expanded Tool Permission Editor"`, `"⚠ Approval Required"`, `" needs approval for "`, and numbered choice lines), extracting the live header question, context/target reason, and numbered options (`1. ...`, `2. ...`, `3. ...`) even when a subagent approval banner appears above an idle `>` prompt or while subagents are running on disk.
    * **Hook & Transcript Permission/Question Inspection:** `antigravity.Plugin.ParseHook` ([`hooks.go`](../internal/agents/antigravity/hooks.go)), `InspectStatus` ([`tmux.go`](../internal/agents/antigravity/tmux.go)), and `InspectSubagentBlock` ([`tmux_transcript_steps.go`](../internal/agents/antigravity/tmux_transcript_steps.go)) (with legacy `AntigravityProvider` fallback) detect uncompleted `ask_question`, `ask_permission`, and `ask_custom_permission` tool calls (reading `transcript_full.jsonl` when `truncated_fields` contains `"tool_calls"`), transitioning to `StateBlocked` and unblocking when `TOOL_RESPONSE` or `PostToolUse` arrives.
  * **Claude Code Question, Prompt & Transcript Inspection:** Evaluates permission and question prompts in the bottom 8 non-empty tmux lines against idle prompt (`❯`, `❯ Type `, `❯ Try `, `❯ Press `) and spinner line indices, and falls back to the main `.jsonl` transcript (`stop_reason: "tool_use"` for `AskUserQuestion`/`ExitPlanMode` -> `StateBlocked`; `stop_reason: "end_turn"` -> `StateIdle`).
  * **15s Blocked Grace Window:** Both status inspectors preserve `StateBlocked` for 15 seconds after `Blocked.Since` / `UpdatedAt` / `LastStateChangeAt` unless a newer transcript step or active spinner proves the turn has progressed.

---

## 5. Multi-Account Profile Management

Ackbar supports running separate account profiles (e.g. Work, Personal, Client) for Claude Code, Google Antigravity, OpenAI Codex, and xAI Grok across local and remote fleet hosts.

### Isolation Architecture
* **Claude Code (`claude-code`):** Each non-default profile maps to `~/.claude-profiles/<name>/` and is launched with `CLAUDE_CONFIG_DIR=~/.claude-profiles/<name>`. Pre-configured HTTP hooks automatically route to `http://127.0.0.1:7777/v1/hooks/claude-code?account=<name>`.
* **Google Antigravity (`antigravity`):** Profiles isolate configurations under `~/.gemini-profiles/<name>/` via `GEMINI_CLI_HOME=~/.gemini-profiles/<name>`.
* **OpenAI Codex (`codex`):** Profiles isolate configurations under `~/.codex-profiles/<name>/` via `CODEX_HOME` and `OPENAI_API_KEY` (`ackbar account add codex <name> --api-key`).
* **xAI Grok (`grok`):** Profiles isolate configurations under `~/.grok-profiles/<name>/` via `GROK_HOME` and `XAI_API_KEY`. Login verification checks `auth.json` or `XAI_API_KEY`.
* **Single-Profile Presentation:** If an agent only has the default profile configured, the account selector is hidden in the UI and account badges are omitted, keeping single-profile workspaces clean.
* **Fleet Propagation:** When adding an account (`--all-hosts` or via the Settings modal), Ackbar automatically checks agent discovery (`/v1/agents/discovery`) on each host and registers the profile only on hosts where the agent is installed.
* **Group Memory:** Ackbar remembers the preferred account for each group/subgroup on each host (`by_host[host].account`), pre-selecting it when spawning subsequent sessions.

---

## 6. Provider Interface Architecture (Interface Segregation)

Ackbar's agent integration model follows the Interface Segregation Principle (ISP), decomposing provider responsibilities into cohesive role interfaces and optional capability interfaces:

### Core Role Interfaces (`internal/daemon/provider.go`)
1. **`AgentIdentity`:** Provides agent identity, display naming, brand hex color, and SVG icon.
2. **`ProcessDetector`:** Inspects CLI binary existence, process names, and hook setup.
3. **`HookParser`:** Ingests incoming telemetry payloads and translates them into canonical `daemon.Event` objects.
4. **`SessionLifecycle`:** Generates shell commands for spawning (`GetSpawnCommand`) and resuming (`GetResumeCommand`) agent sessions.
5. **`TranscriptReader`:** Extracts session metadata, titles, transcripts, and cleans session storage.

The base `Provider` interface embeds these 5 core interfaces:
```go
type Provider interface {
	AgentIdentity
	ProcessDetector
	HookParser
	SessionLifecycle
	TranscriptReader
}
```

### Optional Capability Interfaces
Optional capabilities are probed dynamically at runtime using Go type assertions, eliminating dummy stubs in lightweight CLI adapters:
1. **`StatusInspector` (`p.(StatusInspector)`):** Optional live tmux pane, status bar, and child process inspection for active working/idle/blocked states.
2. **`SubagentDiscoverer` (`p.(SubagentDiscoverer)`):** Optional structured on-disk subagent tracking (`ListSubagents`).
3. **`InteractiveController`:** Provides agent-specific interactive terminal commands (`GetResetCommand`) and permission prompt keystrokes (`GetApprovalKeys`).
4. **`AccountConfigurator`:** Provides multi-account profile environment injection (`AccountEnvVars`), login verification (`IsAccountLoggedIn`), profile directory & hook initialization (`SetupProfileDir`), and CLI login commands (`GetLoginCommand`).
5. **`ProcessInspector`:** Provides native session ID validation (`IsValidNativeID`), non-interactive CLI subcommand filtering (`IsIgnoredCommand`), OS process matching (`MatchProcessCommand`), and PID-to-session resolution (`ResolveSessionForPID`, `ResolveSessionOwner`).
6. **`DiskSessionDiscoverer`:** Provides conversation artifact detection (`HasConversationArtifacts`), subagent session filtering (`IsSubagentSession`), workspace/brain document scanning (`ListWorkspaceDocuments`), and managed-session disk reconciliation (`ScanDiskSessions`).
7. **`UserEntryFilter`:** Provides agent-specific user transcript entry filtering and prompt normalization (`FilterUserEntry(raw map[string]interface{}, content string) (clean string, keep bool)`), ensuring each adapter (`ClaudeAgentAdapter`, `AntigravityAgentAdapter` legacy fallback, and `internal/agents/{antigravity,claude,codex,grok,opencode}` plugins) runs only its own sanitization rules.
8. **`RateLimitDetector`:** Optional agent-specific rate-limit error pattern and reset time detection (`DetectRateLimit(text string, now time.Time) (limited bool, resetAt time.Time, reason string)`).
9. **`AgentAdapter`:** Composite operational adapter interface combining `InteractiveController`, `AccountConfigurator`, `ProcessInspector`, `DiskSessionDiscoverer`, `UserEntryFilter`, and `RateLimitDetector` (implemented by `ClaudeAgentAdapter`, `AntigravityAgentAdapter`, and `defaultAgentAdapter` in `internal/daemon/agent_*.go` and embedded on legacy providers in `internal/provider/`).
10. **`FullProvider`:** Helper interface representing providers that implement all core and optional capabilities (implemented by `PluginBridge`, Claude Code, and Google Antigravity).

### Modular File Structure (`internal/provider/` & `internal/agents/`)
Legacy providers in `internal/provider/` (`Claude Code`, `Antigravity`) use `<agent>.go`, `<agent>_hook.go`, `<agent>_transcript.go`, and `<agent>_subagent.go`. Standalone plugins in `internal/agents/<agent>/` implement `agent.Plugin` and optional facets directly.

### Agent Contract & Registry (`internal/agent`, `internal/agentutil`)
New and migrated adapters implement the leaf contract without importing `internal/daemon` or `internal/provider`:
* **Contract & Facets ([facets.go](../internal/agent/facets.go), [base.go](../internal/agent/base.go), [capabilities.go](../internal/agent/capabilities.go), [headless_types.go](../internal/agent/headless_types.go), [types_stream.go](../internal/agent/types_stream.go)):** Canonical DTOs, `Capabilities`, `LaunchSpec.PromptInCommand`, `PersistentTurnInput`, `RewindTurn`, `DiskScanInput.IsSessionDeleted`, and optional facets:
  * **Headless & Rewind:** `HeadlessDriver`, `PersistentHeadlessDriver` (`SupportsStdioControl`, `DefaultPermissionMode`, `FormatStdinTurn`, `FormatSetModel`, `BasePersistentHeadless`), `ACPDriver`, and `SessionRewinder` (`BaseSessionRewinder`).
  * **Transcripts & Models:** `Transcripts`, opt-in `IncrementalTranscripts` (`AppendTranscript`, no base default), `TranscriptStatter`, `TranscriptUsageInspector` (`CacheWriteTokens`, `ThinkingTokens`), `ModelCatalog` (`NormalizeModelID`), `ModelResolver`, and `WorkspaceModelResolver`.
  * **Discovery, Provisioning & CLI:** `InstallationDetector`, `Provisioner` (`SkillDirs`), `Accounts` (`APIKeyEnvVar`, `ProfileSkillDirs`), `TMuxDriver`, `Hooks`, `SubagentBlockInspector`, `Scanner`, `RateLimiter`, `StreamParserFactory`, `CommandCatalog`, and `CLIInspector`.
* **Registry ([registry.go](../internal/agent/registry.go), [registry_facets.go](../internal/agent/registry_facets.go), [agents/registry.go](../internal/agents/registry.go)):** Thread-safe lookup, `As*` accessors (`AsPersistentHeadless`, `AsWorkspaceModelResolver`, `AsSubagentBlockInspector`, etc.), and `NewDefaultRegistry` (`DefaultPluginsFromEnv` wired into `cmd/ackbard`, `cmd/ackbar`, `cmd/ackbar-hook`).
* **Shared Helpers ([fs.go](../internal/agentutil/fs.go), [provision.go](../internal/agentutil/provision.go), [acp_rpc.go](../internal/agentutil/acp_rpc.go)):** Leaf filesystem, CLI exec, ID validation, TOML, JSON/Markdown provisioning (`UpsertJSONMCPServer`, `InspectJSONMCPServer`), rate-limit, prompt parsing, and `ACPConn` JSON-RPC helpers.
* **Leaf Plugins ([antigravity](../internal/agents/antigravity/plugin.go), [claude](../internal/agents/claude/plugin.go), [codex](../internal/agents/codex/plugin.go), [grok](../internal/agents/grok/plugin.go), [opencode](../internal/agents/opencode/plugin.go)):**
  * `antigravity.New()` implements all Antigravity facets (`PersistentHeadlessDriver`, `StreamParserFactory`, `CLIInspector`, `SessionRewinder`, `SubagentBlockInspector`, `ModelCatalog`, `ModelResolver`, `TranscriptUsageInspector`, `Scanner`, `RateLimiter`, etc.).
  * `claude.New()` implements `PersistentHeadlessDriver` (`SupportsStdioControl`, `FormatSetModel`), `CLIInspector`, `SessionRewinder`, `IncrementalTranscripts`, `TranscriptUsageInspector`, `WorkspaceModelResolver`, `Scanner`, and `RateLimiter` (omits `StreamParserFactory`).
  * `codex.New()` implements `Plugin`, `Branding`, `InstallationDetector`, `Provisioner` (`SkillDirs`), `Accounts` (`OPENAI_API_KEY`), `TMuxDriver`, `Hooks`, `Transcripts`, `Scanner`, `RateLimiter`, and `TranscriptStatter`.
  * `grok.New()` implements `Plugin`, `Provisioner` (`SkillDirs`), `ModelCatalog` (`NormalizeModelID`), `ACPDriver`, `CommandCatalog`, and `Accounts` (`XAI_API_KEY`).
  * `opencode.New()` implements `Plugin`, `Branding`, `InstallationDetector` (without `Provisioner`), `Accounts`, `TMuxDriver`, `Hooks`, `Transcripts`, `Scanner`, `RateLimiter`, and `TranscriptStatter`.
* **Daemon & Headless Bridge ([provider_bridge.go](../internal/daemon/provider_bridge.go), [headless_plugin.go](../internal/daemon/headless_plugin.go), [legacy_env.go](../internal/agent/legacy_env.go)):** [`PluginBridge`](../internal/daemon/provider_bridge.go), [`Server.RegisterPlugin`](../internal/daemon/server.go), and `HeadlessRunner` dispatch plugin facets first; `ACKBAR_LEGACY_AGENTS` (`antigravity`, `agy`, `all`, `*`) restores `internal/provider/antigravity*.go` and [`agent_antigravity.go`](../internal/daemon/agent_antigravity.go) (preserved until Wave 6); `claude-code`/`claude` restores `ClaudeProvider` and [`agent_claude.go`](../internal/daemon/agent_claude.go) during Wave 5 burn-in.

---

## 7. Shared Skills & Tools Distribution

Ackbar provisions the `ackbar-tasks` MCP server and task management skills across all connected agent configurations:

* **Claude Code (`claude-code`):** Configures `mcpServers.ackbar` in `~/.claude.json` (or profile config directory) and deploys `SKILL.md` to `~/.claude/skills/ackbar-tasks/`.
* **Google Antigravity (`antigravity`):** Configures `mcpServers.ackbar` in `~/.gemini/antigravity/mcp_config.json` and deploys `SKILL.md` to both `~/.gemini/config/skills/ackbar-tasks/` (for IDE / shared agent environments) and `~/.gemini/antigravity-cli/skills/ackbar-tasks/` (for CLI environments).
* **OpenAI Codex (`codex`):** Configures `mcpServers.ackbar` in `~/.codex/config.json`, deploys `SKILL.md` to `~/.codex/skills/ackbar-tasks/`, and writes global rules to `~/.codex/AGENTS.md`.
* **xAI Grok (`grok`):** Configures `[mcp_servers.ackbar]` in `~/.grok/config.toml`, deploys `SKILL.md` to `~/.grok/skills/ackbar-tasks/`, and writes global rules to `~/.grok/rules/ackbar.md`.

When an external skills repository is configured (via `ackbar agent setup --skills-repo-url=...`, `POST /v1/agents/skills-repo/sync`, or Web UI modal), custom skills from the external repository are automatically synchronized and installed across all supported agents.


