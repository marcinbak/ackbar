# Dev Workflow Manifest

Project facts for the `dev-workflow` skill. The skill owns the process; this
file owns the facts. Architectural conventions and core system constraints live in
[AGENTS.md](../AGENTS.md) and the subsystem guides in [docs/](file:///Users/dev4u/Work/Ackbar/docs/) — this file points to them, never copies them.

---

## Tracker

| Key | Value |
|---|---|
| tracker | none — project uses [docs/backlog.md](backlog.md) and branch-keyed runs |
| project_key | none |
| jira_base_url | none |
| ready_statuses | none |
| in_progress_status | none |
| pr_open_status | none |

## Naming

| Key | Pattern |
|---|---|
| branch_pattern | `{type}/{slug}` (e.g. `feat/dispatcher-jev`, `fix/test-headless`, `docs/dev-workflow`) |
| pr_title_pattern | `{type}({subsystem}): {brief description}` |
| commit_pattern | `{type}({subsystem}): {brief description}` |

---

## Commands

`ci` = CI already gates this on every PR.

`ci` values: `✅ <workflow>` (hard gate), `⚠️ warning-only` (runs but cannot
fail the job), `❌ not gated`, or `—` (not run in CI).

| Capability | Command | T0 | CI | Notes |
|---|---|---|---|---|
| install | `go mod tidy && (cd mobile && flutter pub get)` | | ✅ CI | Installs Go module dependencies and Flutter packages. |
| format | `gofmt -w . && (cd mobile && dart format .)` | | ✅ CI | Formats all Go files and Dart mobile code. |
| lint | `if [ -n "$(gofmt -l .)" ]; then echo "Unformatted Go code:"; gofmt -l .; exit 1; fi && (cd mobile && flutter analyze --no-fatal-infos)` | ✅ | ✅ CI | Verified in CI via Go Test Suite and Flutter Analyzer. |
| typecheck | `go vet ./... && (cd mobile && flutter analyze --no-fatal-infos)` | ✅ | ✅ CI | Catches type, formatting, and static analysis defects across backend and mobile. |
| unit test | `go test -v ./... && (cd mobile && flutter test)` | ✅ | ✅ CI | Runs complete Go test suite (daemon, provider, relay, router, tmux) and Flutter unit/widget tests. |
| compile/bundle gate | `CGO_ENABLED=0 go build -o /dev/null ./cmd/ackbard ./cmd/ackbar ./cmd/ackbar-hook ./cmd/ackbar-relay` | ✅ | ✅ CI | **Catches broken imports, package syntax errors, and missing symbols across all four binaries.** |
| build artifact | `go build -o bin/ackbard ./cmd/ackbard && go build -o bin/ackbar ./cmd/ackbar && go build -o bin/ackbar-hook ./cmd/ackbar-hook && go build -o bin/ackbar-relay ./cmd/ackbar-relay` | | — | Produces standalone CGO-free executables in `bin/`. Local installation into `~/.local/bin/`. |
| install to device | `cd mobile && flutter run` | | — | Installs and runs the Flutter companion app on an attached device or simulator. |
| e2e | `none — integration covered by internal/daemon and internal/relay end-to-end Go test suites and mobile widget tests` | | — | `TestServer_Integration` and `TestRelay_EndToEnd` run full HTTP/WebSocket loops in unit tests. |

**T0 set:** lint, typecheck, unit test, compile/bundle gate.

---

## Runtime

| Key | Value |
|---|---|
| worktree_location | `.worktrees/<branch-name>` (gitignored), branched from `origin/main` |
| dependency_install_rule | Run `go mod tidy` in the worktree root. Run `flutter pub get` in `mobile/` if mobile files are touched. |
| device_acquisition | Desktop daemon / TUI runs locally on macOS (`127.0.0.1:7777`). Mobile companion app runs on booted iOS Simulator (`xcrun simctl boot <udid>`) or Android Emulator. |
| build_flags | Always compile pure Go with `CGO_ENABLED=0` to ensure compatibility with pure Go SQLite (`modernc.org/sqlite`). |
| install_caveats | The daemon `ackbard` is managed locally via `launchctl` (`com.marcinbak.ackbard`). Restart after local installation with `launchctl kickstart -k gui/$(id -u)/com.marcinbak.ackbard`. |
| device_automation | Use the `agent-device`, `ios-simulator`, or `android-emulator` skills for companion app automation. |
| design_tool | none — desktop web UI and mobile companion app follow the cyberpunk/glassmorphic terminal design system documented in `docs/web.md` and `docs/mobile.md`. |
| design_link_location | none |

### E2E flows

Integration flows are built into automated test suites (`internal/daemon/server_test.go`, `internal/relay/relay_test.go`, and `mobile/test/`).

Excluded flows and why:
- **Production tunnel relay connections** — automated tests spin up ephemeral local WebSocket listeners; never connect tests to public relay endpoints.
- **External agent hook execution** — real `claude`, `codex`, or `agy` CLI processes require interactive login; mock hooks and stdin shims are tested instead.

---

## Ship

| Key | Value |
|---|---|
| announce_channel | none — personal open-source project; PRs reviewed and merged directly on GitHub by `@marcinbak` |
| cc_resolution | none |
| doc_update_targets | `AGENTS.md` (conventions, package layout), `docs/*.md` (architecture, daemon, providers, web, mobile, tui, backlog) |

---

## Skip rules

N9 (on-device companion verification) is skipped when the change matches:
- Only backend daemon (`internal/daemon/**`, `cmd/ackbard/**`, `internal/relay/**`, `internal/tmux/**`, `internal/router/**`) or TUI (`internal/client/**`, `cmd/ackbar/**`) or web GUI (`web/**`) changed (runtime surface is desktop / web browser).
- Only documentation (`docs/**`, `AGENTS.md`, `README.md`) changed.
- Only CI workflows (`.github/**`) or GoReleaser config (`.goreleaser.yaml`) changed.

---

## Security Addendum

Project-specific risks for the N6 security reviewer, beyond generic review:
- **Security Constraint (§9):** The `ackbard` HTTP server must bind strictly to `127.0.0.1` by default or require token authentication (`ACKBAR_TOKEN`) / outbound reverse WebSocket tunnel relay.
- **CGO-Free SQLite:** Always maintain pure Go database drivers (`modernc.org/sqlite`). Never introduce CGO.
- **PTY & Command Injection:** Process supervision via tmux must pass commands and prompts through sanitized arguments (`tmux.SendInput` with literal mode `-l`) to prevent shell escape or code injection.
- **In-Place Attachment:** Attachment MUST suspend the Bubble Tea app using `tea.ExecProcess`, run `tmux attach` or `ssh -t host tmux attach`, and resume/redraw cleanly upon detach.
- **No Hardcoded Secrets:** Never hardcode API keys or credentials (e.g. `TYPESAFE_API_KEY`, Cloudflare tokens) into binaries or git commits; persist user settings in SQLite or retrieve from environment variables.

---

## Model Overrides

Optional. Overrides the skill's routing rubric:
- Daemon networking, process supervision (`internal/tmux`, `internal/relay`), SQLite schema migrations, and authentication changes are planned and reviewed on large tier (`opus` / `pro`) regardless of file count.
