# Cross-Host Retrospective Synthesis (2026-11-13)

This document synthesizes empirical performance metrics across 159 retrospectives and 181 `dev-workflow` run-state records from 2026-09-07 through 2026-11-13 (135 retrospectives [84.9%] from 2026-10-23 through 2026-11-13).

For prioritized remediations, see [2026-11-13-workflow-action-items.md](2026-11-13-workflow-action-items.md).

---

## 1. Fleet Scorecard

| Metric | `legion` | `macmini` | `macbook` | Fleet Total |
| :--- | :--- | :--- | :--- | :--- |
| Dispatch weight / 30d task share | 75 (58.4%) | 25 (25.7%) | 0 (15.8%) | 100 (100%, 101 tasks; rounding 99.9%) |
| 30d Avg CPU / Free RAM | 6.4% / 22.9 GB | 15.4% / 6.3 GB | 50.3% / 3.3 GB (stuck session `5d8cf32d`) | — |
| Run-state JSON records | 106 | 27 (+1 sidecar) | 48 | **181** |
| `.retro.md` in `~/.claude/dev-workflow-runs` | 82 (73 active + 9 archive) | 21 | 29 (1 orphan) | **132** |
| Unindexed retros in `~/.gemini/antigravity-cli/brain/` | 0 | **27** (Atria PRs) | 0 | **27** (**159** total retrospectives) |
| Missing retrospectives on run-state JSONs | 33 | 6 | 20 | **59 (32.6%)** |
| 3-way review (`N6`/`N8`) kept / dropped | 354 / 25 (93.4%) | 265 / 4 (98.5%, Atria 43 retros = 100%) | 155 / 4 (97.5%) | **774 / 33 (95.9% precision)** |
| `review_budget_left <= 0` exhaustions | 23 | 6 | 4 | **33 (18.2%)** |
| Human interventions | — | — | — | **122** (40 [32.8%] preventable; 80 [65.6%] product/scope; 2 [1.6%] untyped) |
| Token usage reports present | 22 | 5 | 4 | **31 / 159 (19.5% Claude Code; 128 [80.5%] unmeasured)** |

---

## 2. Reviewer Precision Breakdown (`N6`/`N8`)

| Reviewer Role | Kept Findings | Dropped Findings | Precision |
| :--- | :--- | :--- | :--- |
| `security-reviewer` | 109 | 4 | 96.5% (99.1% on active runs: 108 / 1) |
| `context-reviewer` | 186 | 7 | 96.4% |
| `clean-reviewer` | 370 | 22 | 94.4% |
| Pooled 3-way reviewer | 109 | 0 | 100.0% |
| **Total** | **774** | **33** | **95.9%** |

---

## 3. Architectural & Workflow Diagnostics

### 3.1 Fragmented Retro Storage & Cross-Host Collection

| Storage Location | Ingestion State | Defect Mode |
| :--- | :--- | :--- |
| `~/.claude/dev-workflow-runs` | Local filesystem only | Isolated per node; no peer sync across hosts. |
| `~/.gemini/antigravity-cli/brain/` | Local subagent store | 27 Atria retrospectives unindexed by dev-workflow tooling. |
| Work Board Deliverables | Partial database attachment | 9 of 26 recent completed tasks attached retrospectives. |
| Daemon `get_standup_report` | Ephemeral metadata | Omits full retrospective text and token usage. |

* Collection across hosts required a 6-shard Federated Map-Reduce using payloads under 8.5 KB.
* The daemon `get_session_output` API truncates session output silently at 10 KB without offset or cursor controls.
* Headless Antigravity bootstrap re-keys session identifiers (`old_id -> id`), dropping input routed to `old_id`.
* Terminal title updates on macOS emit `<uuid>(` OSC sequences that overwrite custom session titles.
* Peer daemons lack Last-Write-Wins (LWW) retrospective synchronization across `legion`, `macmini`, and `macbook`.

### 3.2 Code Review Calibration (`N6`/`N8`/`N12`)

* The N6 three-way review caught 774 confirmed code defects across all active repositories.
* Caught defects include cloud metadata/MCP SSRF, loopback SSRF, path traversal, tokenless CORS/CSRF, OAuth hijack, deadlocks, and panics.
* Reviewing diffs under 10 LOC consumes 148k to 800k weighted tokens without generating actionable findings.
* `clean-reviewer` caused 65% of dropped findings by flagging approved N2 non-goals or uncommitted N10 doc updates.
* `review_budget_left <= 0` starvation caused unreviewed state edits during N12 and introduced regressions (`NGL-1174`).
* Reviewers accepted speculative sub-second UI timing race findings that failed physical reproducibility (`NGL-1168`).

### 3.3 Token & Context Hygiene

| Profile / Metric | Empirical Range | Waste Source |
| :--- | :--- | :--- |
| Claude Code Weighted Tokens (31 measured runs) | 4.85M – 7.4M median, 42.8M main max, 50.4M subagent max | Unbounded multi-round subagent reuse (551 turns = 27.6M wt). |
| Peak Context Window | 512k – 600.6k tokens | Unsplit post-PR N12 review sessions. |
| Prompt Cache-Write Ratio | 41% – 47.6% cache-write share | Redundant context re-ingestion across multi-turn sessions. |
| Antigravity Telemetry Coverage | 0 of 128 runs measured (80.5% blind) | `scripts/token-report.py` lacks `transcript.jsonl` parser. |

* Whole-file reads of raw PNG, XML, and logcat files consumed 127k to 338k tokens per call across 24+ retrospectives.
* Post-PR N12 feedback loops remained in primary orchestrator context exceeding 250k tokens instead of splitting into fresh sessions.
* Repository `AGENTS.md` files exceed size targets: `ngl-android` (316 lines / 50.7 KB), `skip2q` (265 lines), `ngl-ios` (199 lines / 39.3 KB).

### 3.4 On-Device Verification (`N9`) Efficacy & Friction Modes

* On-device verification caught 15+ production blockers missed by static analysis and unit tests.
* Caught blockers include cold-start push token races, stale Gradle JS bundles, 24 missing dSYMs, German locale clipping, and SW redirects.
* Friction Mode 1: Silent background execution hangs (`5h44m` Maestro `flutter --version` hang, `3h` Gradle lock stall).
* Friction Mode 2: Verification subagents hit the 80-turn conversation limit across 10+ runs.
* Friction Mode 3: Hardware emulator `-gpu host` crashes and subagent-blind `ListAgents` contention across 10 runs.
* Friction Mode 4: False-positive test passes caused by overlay modal occlusion (`NGL-1054`) and un-scrolled sheet clipping (`NGL-1172`).
* Friction Mode 5: CPU starvation caused by concurrent Gradle and Jest test execution on `legion`.
