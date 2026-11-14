# Workflow & Fleet Action Items (2026-11-13)

This document details prioritized action items derived from the cross-host retrospective synthesis across `legion`, `macmini`, and `macbook`.

For empirical metrics and diagnostic conclusions, see [2026-11-13-cross-host-retro-synthesis.md](2026-11-13-cross-host-retro-synthesis.md).

---

## 1. Ackbar Platform, LWW Retro Sync & Fleet Supervision (`Personal/Ackbar`)

- [x] **[P0] Auto-ingest Retrospectives into Ackbar Deliverables & LWW Replication**
  * **Owner:** `Personal/Ackbar` (`N15` / LWW mesh)
  * **Evidence:** 59 of 181 missing retrospectives, 9 of 26 tasks with deliverables, 27 unindexed Atria retrospectives.
  * **Acceptance Criteria:** Auto-ingest N15 `.retro.md` (`content_md` populated) and structured run metrics into Ackbar deliverables; replicate deliverables across the LWW peer mesh (`legion` <-> `macmini` <-> `macbook`); expose `list_retros` and `get_deliverable` MCP tools.

- [x] **[P0] Daemon `get_session_output` Pagination & Cursor Controls**
  * **Owner:** `Personal/Ackbar` (`get_session_output`)
  * **Evidence:** Cross-host Map-Reduce truncation requiring an 8.5 KB shard workaround.
  * **Acceptance Criteria:** Add `max_chars` parameter (up to 50,000), `offset`/`cursor` pagination, and `"truncated": true` response metadata to the `get_session_output` endpoint.

- [x] **[P0] Headless Antigravity Session Re-keying & Title Lock**
  * **Owner:** `Personal/Ackbar` (headless `antigravity` & session titles)
  * **Evidence:** Headless boot `old_id -> id` re-key drops and macOS `<uuid>(` OSC title overwrites.
  * **Acceptance Criteria:** Resolve `old_id -> id` transparently across `get_session_output`, `send_session_input`, and `control_session`; lock explicit `custom_title` against macOS `<uuid>(` OSC title escapes.

- [x] **[P0] Standardize `verify-t0.sh` Harness & Manifest Alignments**
  * **Owner:** `Personal/Ackbar` (`scripts/verify-t0.sh` & [docs/dev-workflow.md](../dev-workflow.md))
  * **Evidence:** Host environment bleed (`ACKBAR_HOST`/`$HOME`), `rm ackbar-hook` binary breaks, and false `✅ CI` manifest tags.
  * **Acceptance Criteria:** Ship `scripts/verify-t0.sh` (`HOME=$(mktemp -d)` + unset `ACKBAR_HOST` `go test -race ./...`, `GOOS=darwin GOARCH=arm64 go build ./...`, `node --test web/js/*.test.mjs`, conditional `flutter analyze --no-pub` + diff-scoped `dart format` only on non-whitespace `mobile/` diffs); update [docs/dev-workflow.md](../dev-workflow.md) to remove false `✅ CI` tags, add Linux `systemctl --user restart ackbard`, document `:7778` mobile N9 daemon isolation, enforce atomic `mv` binary install instead of `rm ackbar-hook`, and retain `app_version.dart` sync in [scripts/bump-version.sh](../../scripts/bump-version.sh).

- [x] **[P1] Automatic Session Archival on Work Board Task Completion**
  * **Owner:** `Personal/Ackbar` (Work Board lifecycle)
  * **Evidence:** 165+ stale sessions degrading Web UI performance.
  * **Acceptance Criteria:** Automatically archive bound worker sessions when their associated Ackbar task transitions to `DONE`.

---

## 2. Global `dev-workflow` Skill & Helper Tooling (`Personal/PersonalConfig`)

- [ ] **[P0] Rewrite `scripts/guard.py` with Argument-Aware AST/Shlex Parsing**
  * **Owner:** `Personal/PersonalConfig` (`scripts/guard.py`, `N3`/`N14`)
  * **Evidence:** 20+ false-positive hook blocks across runs.
  * **Acceptance Criteria:** Rewrite `scripts/guard.py` with `shlex` per-command-segment parsing: enforce `--force`/`-f` and default-branch checks on `git push` argv only; allow `git branch -D <br>` when `gh pr view <br> --json state` equals `MERGED`; enforce stepping out to main repo and running `git worktree remove` before deleting a branch.

- [ ] **[P0] Canonical `scripts/run-state.py` Helper with Atomic State Transitions**
  * **Owner:** `Personal/PersonalConfig` (`scripts/run-state.py`, `N0`–`N15`)
  * **Evidence:** 50+ ad-hoc inline Python mutations, schema drift, and 59 incomplete terminal states.
  * **Acceptance Criteria:** Ship `scripts/run-state.py {init,set,note,intervention,complete}` enforcing canonical JSON schema, atomic `.tmp` rename writes, and `node="done"` terminal transitions.

- [ ] **[P0] Extend `scripts/token-report.py` for Antigravity & Subagent Transcripts**
  * **Owner:** `Personal/PersonalConfig` (`scripts/token-report.py`, `N15`)
  * **Evidence:** 128 of 159 (80.5%) Antigravity/manual runs unmeasured.
  * **Acceptance Criteria:** Extend `scripts/token-report.py` to parse Antigravity `transcript.jsonl` files and spawned subagent transcripts.

- [ ] **[P0] Token Hygiene & On-Device Verification Execution Guards**
  * **Owner:** `Personal/PersonalConfig` (`dev-workflow` context & `N9` rules)
  * **Evidence:** 24+ raw PNG/XML/logcat reads (127k–338k tokens), 5h44m/3h background hangs, 10+ 80-turn cap hits, 10 device contentions, and `NGL-1054`/`NGL-1172` UI misses.
  * **Acceptance Criteria:** Enforce in `dev-workflow`: ban raw full-resolution PNG/XML/logcat `Read`s (orchestrator reads `results.md` only; downscale images to 1024px via `sips`/`convert` before reading; use `ui-snapshot` filter for UI dumps); spawn a fresh `<80t` `device-verifier` per round using pre-built binary; require wall-clock `timeout` and `CI=true MAESTRO_CLI_NO_ANALYTICS=1`; enforce `~/.claude/device-locks/<serial>.json` and `~/.claude/locks/heavy-build.lock`; treat overlay occlusion as FAIL and require scroll-to-last-item assertion.

- [ ] **[P1] Calibrate Review Profiles & Post-PR Feedback Budgets**
  * **Owner:** `Personal/PersonalConfig` (`dev-workflow` `N2`/`N6`/`N8`/`N11`–`N13` calibration)
  * **Evidence:** 148k–800k weighted tokens on `<=10 LOC` literals, 65% `clean-reviewer` drops on N2 non-goals/N10 docs, 33 (18.2%) budget exhaustions (`NGL-1174`), `NGL-1168` speculative race churn, and `>250k` N12 context bloat.
  * **Acceptance Criteria:** Add `<=10 LOC` `trivial-literal` profile (1 `clean-reviewer` at `medium`); pass N2 non-goals and decision table to reviewers; reset `review_budget_left=2` on N2 plan revision and grant +1 scoped `clean-reviewer` slot for stateful N12 edits (`>10 LOC`); reject non-reproducible sub-second N12 UI race nits; split sessions at N11 when peak context exceeds 250k; require sibling-repo survey at N2 (`ngl-web`/`ngl-ios`/`ngl-android`); gate N13 strictly after N12 closes (10m Copilot cap); document Antigravity `.worktrees/` sandbox patterns and `send_message` resume rules.

---

## 3. Downstream Repository & Fleet Hygiene Backlog

- [ ] **[P1] Stabilize Android Build Tooling & Environment Constraints**
  * **Owner:** `Modemobile/NGL/ngl-android`
  * **Evidence:** `--reviewer Copilot` failure (12x), `verify-release-build.sh` manual loop (11x), `AGENTS.md` at 316 lines (50.7 KB), and API 36/Xiaomi traps.
  * **Acceptance Criteria:** Remove broken `--reviewer Copilot`; document and fix `DISPLAY=:0`, `API36_sw`, Xiaomi `220733SG`, no `adb install -d`, and API 36 `input text` pitfalls; ship `scripts/verify-release-build.sh` and `scripts/device/ui.sh`; trim `AGENTS.md` to `<=150` lines.

- [ ] **[P1] Resolve iOS Test Suite Failures & dSYM Verification**
  * **Owner:** `Modemobile/NGL/ngl-ios`
  * **Evidence:** `pod install` UTF-8 failures, 517 red `main` unit tests (`#604`), 24 missing framework dSYMs, and `AGENTS.md` at 199 lines (39.3 KB).
  * **Acceptance Criteria:** Enforce `LANG=en_US.UTF-8` for `pod install`; fix or quarantine and document 517 red `main` unit tests (`#604`); ship `scripts/run-maestro-local.sh`; document 24 framework dSYMs verification; trim `AGENTS.md` to `<=150` lines.

- [ ] **[P1] Standardize Atria Verification Script & Environment Resolution**
  * **Owner:** `Personal/Atria`
  * **Evidence:** 43 PR retrospectives repeating 8 manual T0 checks, JDK 25 drift, and worktree `esbuild` path drift.
  * **Acceptance Criteria:** Ship `scripts/verify-t0.sh` covering all 8 gates, JDK 25 `JAVA_HOME`, and worktree `esbuild` resolution; align `docs/dev-workflow.md` gate counts and flags.

- [ ] **[P1] Author Architecture Documentation & Fix Skip2Q Tooling**
  * **Owner:** `EnuiaLabs/Skip2Q`
  * **Evidence:** Missing `docs/architecture.md`, `AGENTS.md` at 265 lines, and worktree `npm ci` / E2E artifact path failures.
  * **Acceptance Criteria:** Author `docs/architecture.md`; trim `AGENTS.md` to `<=150` lines; fix worktree `npm ci` and E2E build/artifact paths.

- [ ] **[P2] Bootstrap Governance Across Remaining Repositories**
  * **Owner:** `Modemobile/NGL/ngl-web`, `Personal/PersonalConfig`, `Personal/release-monitor`
  * **Evidence:** Unbootstrapped repositories lacking manifests and verification scripts.
  * **Acceptance Criteria:** Run `/project-setup` governance bootstrap (`docs/dev-workflow.md`, `docs/architecture.md`, T0 scripts) across `ngl-web`, `personal-config`, and `release-monitor`.

- [ ] **[P0] Fleet Ops: Terminate Stuck MacBook Process & Clear Blocked Tasks**
  * **Owner:** Fleet Ops (`macbook`, `macmini`)
  * **Evidence:** `macbook` 50.3% 30-day average CPU and 3.3 GB free RAM caused by 10-day stuck session `5d8cf32d`; stalled `macmini` tasks.
  * **Acceptance Criteria:** Kill 10-day stuck `macbook` session `5d8cf32d`; resolve blocked `macmini` sessions (`NGL-1167`, `NGL-1171`).
