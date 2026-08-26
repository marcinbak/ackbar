# Session Naming & Per-Agent Resolution Hierarchy

Ackbar employs a **Per-Agent Tiered Title Resolution Hierarchy** designed specifically for each AI agent ecosystem (Claude Code, Google Antigravity, OpenAI Codex). 

Each agent has its own dedicated title resolver function and metadata extractor that traverses that agent's native disk structures, cache formats, and transcript conventions.

---

## 1. Per-Agent Resolution Breakdown

```
+---------------------------------------------------------------------------------------------------------+
|                                    PER-AGENT RESOLUTION PIPELINE                                        |
+------------------------------------+------------------------------------+-------------------------------+
|       ✳️ Claude Code (Anthropic)   |    🪐 Antigravity (Google)         |      ⚡ OpenAI Codex          |
+------------------------------------+------------------------------------+-------------------------------+
| Tier 1: Custom / Session Registry  | Tier 1: User Annotation            | Tier 1: Session Metadata      |
|  - ~/.claude/sessions/*.json name  |  - annotations/<id>.pbtxt ("title")|  - ~/.codex/sessions/*.json   |
|  - Transcript tail "customTitle"   |                                    |    ("title" / "name")         |
|  - history.jsonl "displayName"     |                                    |                               |
+------------------------------------+------------------------------------+-------------------------------+
| Tier 2: AI-Generated Title         | Tier 2: Brain Task Summary         | Tier 2: AI Summary            |
|  - Transcript "aiTitle" / "summary"|  - brain/<id>/task.md metadata     |  - Codex completion summary   |
+------------------------------------+------------------------------------+-------------------------------+
| Tier 3: First User Task Prompt     | Tier 3: First User Task Input      | Tier 3: First User Prompt     |
|  - First prompt in transcript/log  |  - First USER_INPUT in transcript  |  - First user prompt in log   |
|    (skips /config, /mcp, XML tags) |    (skips system wrappers)         |                               |
+------------------------------------+------------------------------------+-------------------------------+
| Tier 4: Fallback Slug              | Tier 4: Fallback Slug              | Tier 4: Fallback Slug         |
|  - "Claude Code (<uuid[:8]>)"      |  - "Antigravity (<uuid[:8]>)"      |  - "Codex (<uuid[:8]>)"       |
+------------------------------------+------------------------------------+-------------------------------+
```

---

## 2. Detailed Per-Agent Resolution Logic

### ✳️ Claude Code (`claude-code`)
1. **Tier 1: Explicit Custom Name / Session Registry**
   * Checks `~/.claude/sessions/*.json` for an exact `sessionId` matching the session with a non-empty `name`.
   * Checks the last 64KB (tail) of the project's `.jsonl` transcript for `customTitle`, `agentName`, or `type: "rename"`.
   * Checks `~/.claude/history.jsonl` for `displayName` or `customTitle`.
2. **Tier 2: AI-Generated Title (`aiTitle`)**
   * Inspects transcript entries for `type: "ai-title"` or `aiTitle`.
3. **Tier 3: First Real User Task Prompt**
   * Reads initial user prompt from transcript or `history.jsonl`.
   * **Clean Filters:** Automatically skips utility slash commands (`/config`, `/mcp`, `/add-dir`, `/compact`, `/help`) and XML wrapper tags (`<USER_REQUEST>`, `<CONTEXT_SUMMARY>`) to ensure only actual task instructions become titles.
4. **Tier 4: CWD Session Registry Fallback**
   * Matches session registry by directory path (`cwd`).
5. **Tier 5: Fallback ID**
   * `Claude Code (<uuid[:8]>)`

---

### 🪐 Google Antigravity (`antigravity`)
1. **Tier 1: Explicit User Annotation / Title**
   * Checks `~/.gemini/antigravity/annotations/<conversationId>.pbtxt` for `title: "..."`.
2. **Tier 2: Brain Task Metadata Summary**
   * Checks `~/.gemini/antigravity/brain/<conversationId>/task.md.metadata.json` or `task.md` for `summary: "..."`.
3. **Tier 3: First Real User Instruction**
   * Scans `~/.gemini/antigravity/brain/<conversationId>/.system_generated/logs/transcript.jsonl` for the first step of `type: "USER_INPUT"`.
   * Strips XML wrapper tags (`<USER_REQUEST>`, `<system_instructions>`) and truncates multi-line prompts cleanly.
4. **Tier 4: Fallback ID**
   * `Antigravity (<conversationId[:8]>)`

---

### ⚡ OpenAI Codex (`codex`)
1. **Tier 1: Explicit Session Metadata**
   * Checks `~/.codex/sessions/<sessionId>.json` for `title` or `name`.
2. **Tier 2: First User Message**
   * Extracts first user instruction from `~/.codex/sessions/<sessionId>.json`.
3. **Tier 3: Fallback ID**
   * `Codex (<sessionId[:8]>)`

---

## 3. In-Memory Title Cache with Source Tracking

Title resolution results are stored in an in-memory cache with thread-safe source tracking:

```go
type TitleCacheEntry struct {
    Title     string    // Resolved title text
    Source    string    // "custom" | "ai" | "prompt" | "fallback"
    UpdatedAt time.Time // Timestamp of last evaluation
}
```

### Cache Upgrades & Invalidation Rules:
* **No Downgrades:** A cache entry with `Source == "custom"` will never be overwritten by lower-priority AI titles or prompt snippets.
* **Dynamic Upgrades:** If a session was previously cached as `Source == "ai"` or `"prompt"`, and a subsequent scan discovers a user custom name (e.g. user renamed the session or metadata was written), the cache is upgraded immediately to `"custom"`.
* **Instant Rename Invalidation:** Triggering the `rename` control action updates both the SQLite persistence layer and the in-memory `titleCache` instantly without requiring a daemon restart.

---

## 4. In-Place Context Reset & Multi-Turn Suffixing

When an agent command like `/clear` or `/reset` is executed inside a live tmux session:

1. **Base Title Extraction:** The daemon parses the base title (stripping existing `(Conv N)` suffixes).
2. **Turn Suffix Assignment:**
   * The archived turn is finalized as `"<Base Title> (Conv 1)"` with status `StateEnded`.
   * The active live turn is named `"<Base Title> (Conv 2)"` (incrementing on subsequent resets to `(Conv 3)`, `(Conv 4)`).
3. **Cache Synchronization:** The `titleCache` is updated for both the prior UUID and the new UUID to guarantee stable, collision-free titles across all views.
