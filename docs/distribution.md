# Distributing & Upgrading Ackbar

This document outlines the automated strategies for distributing, packaging, and managing updates for Project Ackbar via **Homebrew**, **GoReleaser**, and **GitHub Actions**.

---

## 1. Homebrew Tap (Recommended for macOS & Linux)

The official Homebrew tap provides the cleanest user experience for installation, daemon background service management, and seamless upgrades.

### Installation

```bash
# Add official Homebrew tap
brew tap marcinbak/ackbar

# Install ackbard daemon, ackbar TUI, ackbar-hook, and ackbar-relay
brew install ackbar

# Start daemon as a background service managed by brew services
brew services start ackbar
```

### Managing the Background Daemon

```bash
# Check service status
brew services list

# Restart daemon
brew services restart ackbar

# Stop background service
brew services stop ackbar
```

### Upgrading to the Latest Release

```bash
brew update
brew upgrade ackbar
brew services restart ackbar
```

---

## 2. GitHub Releases & GoReleaser Automation

We use **[GoReleaser v2](https://goreleaser.com/)** (`.goreleaser.yaml`) orchestrated via GitHub Actions (`.github/workflows/release.yml`) to automatically compile, package, and publish releases whenever a git tag (`v*`) is pushed to `origin`.

### Release Matrix

Every release automatically cross-compiles 4 standalone CGO-free binaries for 4 target architectures:

| Platform | Architecture | Target Users |
| :--- | :--- | :--- |
| **macOS (`darwin`)** | `arm64` | Apple Silicon (M1/M2/M3/M4) |
| **macOS (`darwin`)** | `amd64` | Intel Macs |
| **Linux (`linux`)** | `amd64` | Ubuntu, Debian, Arch, RHEL x86_64 servers |
| **Linux (`linux`)** | `arm64` | Raspberry Pi, AWS Graviton, ARM VPSs |

### Binaries Bundled in Every Archive

1. **`ackbard`:** Control plane daemon engine with SQLite persistence, PTY multiplexing, and SSE streams.
2. **`ackbar`:** Bubble Tea terminal dashboard and tmux attachment interface.
3. **`ackbar-hook`:** Universal stdin-to-HTTP hook shim for AI agents.
4. **`ackbar-relay`:** Standalone reverse WebSocket tunnel relay server.

---

## 3. Triggering a New Release

To publish a new official release across GitHub Releases and Homebrew:

```bash
# 1. Update VERSION file on main (e.g. 20260822.01)
echo "20260822.01" > VERSION

# 2. Tag and push to origin
git tag v20260822.01
git push origin v20260822.01
```

GitHub Actions will automatically:
1. Run full unit and integration test suites.
2. Compile and package release tarballs (`.tar.gz`) and SHA-256 checksums (`checksums.txt`).
3. Create the GitHub Release with automated changelog.
4. Update the Homebrew formula in `marcinbak/homebrew-ackbar`.

---

## 4. Direct `go install` (Go Toolchain Users)

For developers with the Go toolchain installed who prefer compiling from source:

```bash
go install github.com/marcinbak/ackbar/cmd/ackbard@latest
go install github.com/marcinbak/ackbar/cmd/ackbar@latest
go install github.com/marcinbak/ackbar/cmd/ackbar-hook@latest
go install github.com/marcinbak/ackbar/cmd/ackbar-relay@latest
```

---

## 5. Linux Systemd User Service Setup

For Linux workstations and remote GPU servers running `ackbard` in the background:

### Create Service Unit (`~/.config/systemd/user/ackbard.service`)
```ini
[Unit]
Description=Ackbar Agent Control Plane Daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/ackbard
Restart=always
RestartSec=3s
Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

### Enable & Start
```bash
# Enable systemd linger so daemon runs even when not logged in via SSH
loginctl enable-linger $USER

# Reload and enable user service
systemctl --user daemon-reload
systemctl --user enable --now ackbard

# Inspect live logs
journalctl --user -u ackbard -f
```
