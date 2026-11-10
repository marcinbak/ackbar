# Mobile Companion & Cross-Client Parity Audit

This document details the architectural analysis of the Flutter companion app (`mobile/`), cross-client logic duplication, and documentation accuracy.

## 1. Flutter Module Structure & Analyzer Gates

The mobile app codebase exhibits structural inversion between `core/` and `features/`, paired with suppressed static analyzer rules:

### Suppressed Analyzer Lint Rules

[mobile/analysis_options.yaml](../../mobile/analysis_options.yaml) disables 6 critical lint rules:
* `unused_element: ignore`: masks 363 lines of dead code, including an unused modal dialog implementation ([attention_screen.dart](../../mobile/lib/features/attention/presentation/attention_screen.dart#L952-L1133))
* `use_build_context_synchronously: ignore`: hides un-mounted `BuildContext` references across async HTTP/SSE calls
* `unused_import: ignore`: conceals circular import residue across core packages
* `deprecated_member_use: ignore`, `prefer_const_constructors: ignore`, `prefer_const_declarations: ignore`: inhibits compiler optimizations

### Architectural Layering Inversion

* **God-Package `core/`:** Contains domain models, network transports, utilities, and Riverpod notifiers. Feature directories (`features/*/`) contain presentation widgets only.
* **UI Leaks in Domain Models:** [session.dart](../../mobile/lib/core/models/session.dart) and [host.dart](../../mobile/lib/core/models/host.dart) directly import Flutter `package:flutter/material.dart` and widget types (`Color`, `IconData`), violating domain isolation.

## 2. Mobile Hotspots & Defects

| Hotspot File | Code L | Total L | Churn | Architecture Defect | Proposed Decomposition |
| :--- | ---: | ---: | ---: | :--- | :--- |
| [fleet_providers.dart](../../mobile/lib/core/providers/fleet_providers.dart) | 1,145 | 1,293 | 18 | Mixes 8 distinct domains; sequential HTTP loops; dead `PlansNotifier`; global rebuilds | Split into `features/{hosts,fleet,tasks,attention}/application/` + `host_resolver.dart` |
| [transcript_screen.dart](../../mobile/lib/features/transcript/presentation/transcript_screen.dart) | 1,974 | 2,082 | 11 | 18 state fields; 3s full polling instead of SSE; tool regex parser; 7 UI regions | Extract `transcript_controller.dart` + 6 presentation widgets under `widgets/` |
| [attention_screen.dart](../../mobile/lib/features/attention/presentation/attention_screen.dart) | 1,091 | 1,133 | 12 | Pager, form, card list, 181L dead modal; mock mic text; shared controller bug | Extract pager, blocked session card, in-progress list, and decision history widgets |
| [terminal_screen.dart](../../mobile/lib/features/terminal/presentation/terminal_screen.dart) | 760 | 853 | 9 | Terminal state machine mixed with WebSocket I/O and touch accessory bar | Separate WebSocket PTY transport from terminal accessory bar UI |
| [hosts_screen.dart](../../mobile/lib/features/hosts/presentation/hosts_screen.dart) | 480 | 545 | 7 | Contains simulated Tailnet latency and synthetic log viewer stubs | Connect to actual daemon host telemetry endpoints |
| [main_navigation_shell.dart](../../mobile/lib/features/main_navigation_shell.dart) | 240 | 283 | 6 | Root `Scaffold` contains nested `Scaffold` instances in tab views | Unify layout into a single root `Scaffold` |

### Critical Runtime Defects in Mobile

* **SSE Reconnect Leak:** [sse_client.dart](../../mobile/lib/core/network/sse_client.dart#L15-L75) `subscribeHost` omits `onCancel`, leaking infinite 3s HTTP retry loops upon host changes.
* **Attention Mic Mock:** [attention_screen.dart](../../mobile/lib/features/attention/presentation/attention_screen.dart#L598-L603) hardcodes `'Proceed with pure Go modernc.org/sqlite'` on mic press instead of audio recording.
* **Attention Controller Bleed:** A single `_customInputController` instance is shared across all `PageView` cards, leaking text across unrelated sessions.
* **Attention Action Bug:** Line 626 sends `action: 'answer'` instead of `'allow'` or `'deny'` for permission requests.
* **Riverpod Rebuild Storm:** 54 of 56 `ref.watch` calls omit `.select(...)` and domain models lack value equality (`operator ==`), triggering full app re-renders on every SSE tick.
* **Redundant Network Calls:** App executes 3 redundant `/v1/sessions` calls on startup and triggers N-ary fan-outs on `federated: true` peer hosts.

## 3. 3-Way Cross-Client Logic Duplication

Seven core domain rules are duplicated across Go backend, Web UI, and Mobile Flutter app, causing behavioral divergence:

| Domain Rule | Daemon (`internal/daemon`) | Web Dashboard (`web/js`) | Mobile App (`mobile/lib`) | Divergence Impact |
| :--- | :--- | :--- | :--- | :--- |
| **1. Git & Node Path Matching** | [server_projects.go](../../internal/daemon/server_projects.go#L699-L768) matches git remotes | [tree.js](../../web/js/tree.js#L383-L418) normalizes URL endings | [node_path_resolver.dart](../../mobile/lib/core/utils/node_path_resolver.dart#L5-L136) parses regex | Sessions associate with different project nodes across Web and Mobile |
| **2. Auto-Done & Auto-Archive** | Evaluates idle thresholds from `ackbard.db` | Queries daemon session state directly | Never reads daemon settings; keeps 24h idle sessions `Active` | Session status diverges between Web and Mobile interfaces |
| **3. Handover Threshold** | Triggers context handover at 60% capacity | Relies on daemon SSE notification | Hardcodes handover warning at 80% capacity | Mobile users miss proactive context window alerts |
| **4. Agent Catalog** | Exposes 5 agents (`claude`, `codex`, `agy`, `grok`, `opencode`) | Dynamically renders from `/v1/providers` | Hardcodes 3 agents in [new_session_sheet.dart](../../mobile/lib/features/fleet/presentation/new_session_sheet.dart#L62-L74) | Mobile cannot spawn Grok or OpenCode sessions |
| **5. Supervisor Title Parsing** | Resolves `⚓` anchor emoji in session names | Formats title with custom regex badge | Strips or incorrectly prefixes anchor symbols | Session grouping breaks in mobile fleet list |
| **6. Speech Sanitization** | [server_speech.go](../../internal/daemon/server_speech.go#L25-L193) regex pipeline | [chat.js](../../web/js/chat.js#L75-L207) custom replace rules | [speech_sanitizer.dart](../../mobile/lib/core/utils/speech_sanitizer.dart#L6-L178) custom pipeline | Spoken transcripts produce divergent audio outputs |
| **7. Tool Call Regex Parsing** | [transcript.go](../../internal/daemon/transcript.go) AST + regex extraction | [chat.js](../../web/js/chat.js) tool card parser | [transcript_screen.dart](../../mobile/lib/features/transcript/presentation/transcript_screen.dart) regex | Complex tool invocations render differently or fail to expand |

## 4. Documentation Accuracy & Link Drift

| Document | Stated Architecture | Actual Codebase Reality | Required Fix |
| :--- | :--- | :--- | :--- |
| [AGENTS.md](../../AGENTS.md) | 19 links point to absolute local path `/Users/dev4u/Work/Ackbar/...` | Absolute developer filesystem paths fail for other agents and environments | Replace with repo-relative Markdown links |
| [docs/dev-workflow.md](../dev-workflow.md#L5) | Line 5 links to absolute `/Users/dev4u/Work/Ackbar/docs/` | Dead link outside local developer machine | Replace with relative `../docs/` path |
| [docs/architecture.md](../architecture.md) | Lists legacy 3-tier daemon layout; specifies `~/.ackbard.db` | Omits `ackbar-relay`, MCP tools, router, Grok/OpenCode; DB is in config dir | Rewrite specification following architecture contract |
| [docs/daemon.md](../daemon.md) | Lists 35 API routes | Daemon exposes 60 active HTTP/WS routes (25 undocumented) | Add missing route specifications and payloads |
| [docs/mobile.md](../mobile.md) | Documents standalone Riverpod providers | Providers live in single monolithic `fleet_providers.dart` | Document actual provider hierarchy and state flows |
| [docs/web.md](../web.md) | Documents modular JavaScript architecture | 12 modules coupled in cyclic Strongly Connected Component | Document planned acyclic event bus architecture |

## 5. Healthy Mobile Patterns to Preserve

* **Design Token Centralization ([app_theme.dart](../../mobile/lib/core/theme/app_theme.dart)):** Cleanly decouples colors, typography, and card decorations into reusable design tokens.
* **Constructor-Injected Test Seams:** Services accept injectable `http.Client`, `SpeechEngine`, and `WebSocketChannelBuilder`, enabling 204 fast, isolated unit and widget tests.
