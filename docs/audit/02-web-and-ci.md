# Web Dashboard & CI/CD Pipeline Audit

This document details the architectural analysis of the Web GUI (`web/`) and GitHub Actions CI pipelines (`.github/workflows/ci.yml`).

## 1. Web Subsystem Hotspot Breakdown

The Web GUI uses vanilla ES modules without a bundler, served directly via Go `embed.FS`.

| Hotspot File | Code L | Total L | Churn | Architecture & Modularity Defect | Proposed Target Decomposition |
| :--- | ---: | ---: | ---: | :--- | :--- |
| [chat.js](../../web/js/chat.js) | 5,197 | 5,706 | 23 | 72 exports; mixes 9 distinct subsystems in a single file | Split into 9 modules under `web/js/chat/` (see decomposition table below) |
| [tasks.js](../../web/js/tasks.js) | 1,745 | 1,937 | 11 | Mixes Kanban board, modals, standup TTS, and host MCP provisioning | `web/js/tasks/{board,modals,standup,provisioning}.js` |
| [modals.js](../../web/js/modals.js) | 1,684 | 1,881 | 16 | 9 unrelated dialog flows (settings, spawn, handover, hosts, updates) | Co-locate modals with features: `web/js/modals/{settings,spawn,handover,hosts,update}.js` |
| [tabs.js](../../web/js/tabs.js) | 1,262 | 1,391 | 13 | Mixes tab strip navigation, xterm mounting, and PTY/Headless engine toggle | Keep tab UI in `tabs.js`; move terminal mounting to `terminal.js`; toggle to `session-engine.js` |
| [utils.js](../../web/js/utils.js) | 980 | 1,094 | 12 | 50 helper functions; regex HTML sanitizer; inline SVG dictionaries | Split into `formatting.js`, `sanitizer.js`, and `svg-icons.js` |
| [api.js](../../web/js/api.js) | 970 | 1,052 | 17 | Network client directly renders DOM (`renderHosts`, badges, update modal) | Pure HTTP/WS client in `api.js`; UI updates moved to `hosts.js` and `update.js` |
| [state.js](../../web/js/state.js#L218-L337) | 312 | 354 | 9 | Eagerly queries DOM elements during module import before DOM is ready | Guard queries behind `initAppState()` function called after `DOMContentLoaded` |
| [app.js](../../web/app.js) | 3 | 3 | 2 | Deprecated 3-line legacy redirect shim referencing obsolete root script | Remove file and update server route registrations |
| [web_test.go](../../web/web_test.go#L97-L522) | 480 | 522 | 8 | Assertions check brittle substring occurrences in JS files (`strings.Contains`) | Replace with Node.js behavioral test runner running actual module exports |

### `web/js/chat.js` 9-Subsystem Decomposition Plan

Decomposing `chat.js` into focused modules requires updating `//go:embed` in [web/web.go](../../web/web.go#L8). Go's embed pattern `js/*.js` does not match nested directories recursively; it must be updated to `js/*.js js/*/*.js`.

| Proposed Module | Line Budget | Scope of Extracted Responsibilities |
| :--- | :--- | :--- |
| `web/js/chat/composer.js` | ~600 L | Input textarea, prompt history, auto-resize, keybind handlers, draft preservation |
| `web/js/chat/voice-tts.js` | ~450 L | Browser SpeechSynthesis API wrapper, audio playback queue, speech interruption |
| `web/js/chat/voice-live.js` | ~550 L | Live WebSocket voice streaming client, mic capture audio node, audio spectrum canvas |
| `web/js/chat/controls.js` | ~500 L | Chat configuration popover, voice selection dropdown, schedule timer controls |
| `web/js/chat/interactive-cards.js` | ~600 L | Attention blocker cards, tool confirmation buttons, interactive question prompts |
| `web/js/chat/transcript.js` | ~700 L | Virtualized scroll container, transcript pagination, turn chunking, scroll pinning |
| `web/js/chat/file-links.js` | ~450 L | Workspace file pill rendering, diff viewers, external editor URL schemes |
| `web/js/chat/message-renderer.js` | ~800 L | Markdown rendering pipeline, code syntax highlighting, tool call cards, copy actions |
| `web/js/chat/stream.js` | ~550 L | SSE event listener, `requestAnimationFrame` delta queue, turn assembly state machine |

## 2. Module Coupling & Circular Import Graph

12 of the 17 JavaScript files in `web/js/` form a single Strongly Connected Component (SCC) containing 16 direct two-way circular import cycles:
* `api.js` ↔ `modals.js`, `api.js` ↔ `tabs.js`, `api.js` ↔ `tree.js`
* `chat.js` ↔ `context-menu.js`, `chat.js` ↔ `details.js`, `chat.js` ↔ `tabs.js`
* `context-menu.js` ↔ `tabs.js`, `context-menu.js` ↔ `tree.js`
* `details.js` ↔ `modals.js`, `details.js` ↔ `tabs.js`
* `modals.js` ↔ `tree.js`, `palette.js` ↔ `tabs.js`
* `tabs.js` ↔ `terminal.js`, `tabs.js` ↔ `tree.js`
* `terminal.js` ↔ `utils.js`, `tree.js` ↔ `utils.js`

In addition, [auth.js](../../web/js/auth.js#L21-L49) monkey-patches `window.fetch` globally to inject `Authorization` headers, and several modules leak mutable state through `window.*` globals (`window.state`, `window.openSessionInTab`, `window.AckbarChat`, `window.speakText`). Replacing direct cross-module imports with an acyclic event bus and unified state store removes the SCC.

## 3. Security & Frontend Risks

| Location | Severity | Vulnerability Description | Remediation |
| :--- | :--- | :--- | :--- |
| [palette.js](../../web/js/palette.js#L251-L281) | **P0** | Untrusted `sess.name`, `sess.git_branch`, and `sess.cwd` interpolate into `innerHTML` | Use `textContent` or `escapeHtml()` helper |
| [tabs.js](../../web/js/tabs.js#L1343-L1344) | **P0** | `renderOverflowMenu` injects `tab.session.name` and `tab.session.agent` into `innerHTML` | Escape session name and agent before DOM injection |
| [modals.js](../../web/js/modals.js#L65-L1709) | **P0** | Account IDs, host hostnames, and API error strings interpolate into modal HTML | Sanitize all interpolated modal variables |
| [utils.js](../../web/js/utils.js#L780-L797) | **P0** | `getAgentBadgeHtml` injects `p.icon_svg`, `p.display_name`, and fallback `agent` into HTML | Sanitize SVG markup and escape agent display names |
| [details.js](../../web/js/details.js#L509-L513) | **P0** | `openTranscriptViewerTab` injects `errorMsg` into `<p>${errorMsg}</p>` | Use text node assignment instead of template literal interpolation |
| [utils.js](../../web/js/utils.js#L75-L99) | **P0** | `sanitizeRenderedMarkdownHtml` uses a 4-tag blacklist (`script, iframe, object, embed`) with regex fallback; allows `<style>`, `<base>`, `<meta>`, `<svg>` | Replace blacklist with strict DOMParser tag and attribute allowlist |
| [tasks.js](../../web/js/tasks.js#L1304-L1321) | **P0** | Duplicates incomplete markdown sanitizer with similar blacklist bypasses | Consolidate into single allowlist sanitizer module |
| [index.html](../../web/index.html#L459-L463) | **P0** | Loads `marked` parser from unversioned/unpinned CDN without Subresource Integrity (SRI) | Vendor library into `web/vendor/marked.min.js` or add SRI hash |
| [server.go](../../internal/daemon/server.go#L352) | **P0** | `authMiddleware` allowlists `/app.js` but omits `/js/*`, returning 401 on scripts | Add `/js/*` prefix to unauthenticated static asset route table |

## 4. CI & Verification Gate Drift

Comparison between claimed gates in [docs/dev-workflow.md](../dev-workflow.md#L37-L45) and actual gates in [.github/workflows/ci.yml](../../.github/workflows/ci.yml#L12-L75).

| Verification Gate | Claimed Status | Actual CI Implementation | Enforcement Gap & Action Required |
| :--- | :--- | :--- | :--- |
| `go vet ./...` | `✅ CI` | Absent in CI workflow | Add `go vet ./...` to GitHub Actions `test` job |
| `AGENTS.md <= 160` | `✅ CI` | Absent in CI workflow | Add line count assertion to CI preflight step |
| `dart format` | `✅ CI` | Absent in CI workflow | Add `dart format --output=none --set-exit-if-changed .` |
| `go mod tidy` clean diff | `✅ CI` | Runs `go mod tidy` without `git diff --exit-code` | Add `git diff --exit-code go.mod go.sum` after tidy |
| `web/js/chat.test.mjs` | Not Listed | Test file exists ([chat.test.mjs](../../web/js/chat.test.mjs)) but never runs in CI | Add `node --test web/js/*.test.mjs` to T0 and CI |
| Module cycle detection | Not Listed | Absent in T0 and CI | Add `madge --circular web/js/` check to CI |
| Hotspot size budget | Not Listed | Absent in T0 and CI | Add `hotspots.py --check` against architectural exemptions |

## 5. Healthy Web & CI Patterns to Preserve

* **Zero-Build ES Modules ([web.go](../../web/web.go)):** Direct browser loading of native ES modules avoids bundler configuration overhead and enables rapid iteration.
* **Strict Mermaid LRU Cache ([mermaid.js](../../web/js/mermaid.js#L65-L280)):** Configures Mermaid with `securityLevel: 'strict'`, bounded 200-entry LRU cache, and clean SVG rendering fallback.
* **Frame-Rate Delta Batching ([chat.js](../../web/js/chat.js#L4866-L4907)):** Coalesces high-frequency SSE message deltas using `requestAnimationFrame`, avoiding UI locking during fast streaming.
