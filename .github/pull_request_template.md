## Summary of Changes

A concise description of the problem solved or feature introduced by this Pull Request.

---

## Type of Change

- [ ] 🐛 Bug fix (non-breaking change resolving an issue)
- [ ] ✨ New feature (non-breaking change adding functionality)
- [ ] ♻️ Refactoring / Code cleanup
- [ ] 📝 Documentation update
- [ ] 🧪 Tests / CI update

---

## Subsystems Affected

- [ ] `cmd/ackbard` / `internal/daemon` (Daemon engine, SQLite, SSE, PTY)
- [ ] `cmd/ackbar` / `internal/client` (Bubble Tea TUI)
- [ ] `internal/provider` (Claude Code, Antigravity, Codex adapters)
- [ ] `internal/relay` / `cmd/ackbar-relay` (Outbound reverse tunnel relay)
- [ ] `internal/web` / `web/` (Embedded Web GUI, xterm.js)
- [ ] `mobile/` (Flutter companion application)
- [ ] `docs/` (Architecture & guides)

---

## Verification & Testing

Please describe how you verified your changes:

- [ ] Ran Go test suite (`go test -v ./...`)
- [ ] Ran Flutter test suite (`cd mobile && flutter test`)
- [ ] Verified CGO-free pure Go compilation (`CGO_ENABLED=0 go build ./...`)
- [ ] Verified local/remote daemon connectivity

---

## Checklist

- [ ] Code follows project formatting standards (`go fmt`, `flutter analyze`)
- [ ] Preserved default localhost (`127.0.0.1`) binding constraint
- [ ] Added or updated relevant unit/widget tests
- [ ] Updated documentation in `docs/`, `AGENTS.md`, or `README.md` if applicable
