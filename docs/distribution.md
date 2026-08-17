# Distributing & Upgrading Ackbar

This document outlines the recommended strategies for distributing, packaging, and managing updates for Project Ackbar.

---

## 1. Homebrew Tap (Recommended for macOS & Linux)

Creating a custom Homebrew tap provides the cleanest user experience for installation, daemon service management, and automatic upgrades.

### Setup Steps
1. Create a public repository named `github.com/<org>/homebrew-ackbar`.
2. Add a formula file `ackbar.rb`:

```ruby
class Ackbar < Formula
  desc "Control plane & session multiplexer for agentic programming sessions"
  homepage "https://github.com/<org>/ackbar"
  url "https://github.com/<org>/ackbar/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "<release-tarball-sha256>"
  license "MIT"

  depends_on "go" => :build
  depends_on "tmux"

  def install
    system "go", "build", *std_go_args(output: bin/"ackbard"), "./cmd/ackbard"
    system "go", "build", *std_go_args(output: bin/"ackbar"), "./cmd/ackbar"
    system "go", "build", *std_go_args(output: bin/"ackbar-hook"), "./cmd/ackbar-hook"
  end

  service do
    run [opt_bin/"ackbard"]
    keep_alive true
    log_path var/"log/ackbard.log"
    error_log_path var/"log/ackbard.err.log"
  end

  test do
    system "#{bin}/ackbar", "--version"
  end
end
```

### End-User Commands
```bash
# Installation
brew tap <org>/ackbar
brew install ackbar

# Start daemon as background service
brew services start ackbar

# Upgrade to latest version
brew upgrade ackbar
```

---

## 2. Direct `go install` (Go Toolchain Users)

For developers with Go installed, users can install binaries directly from the repository:

```bash
# Initial installation / Upgrade
go install github.com/<org>/ackbar/cmd/ackbard@latest
go install github.com/<org>/ackbar/cmd/ackbar@latest
go install github.com/<org>/ackbar/cmd/ackbar-hook@latest
```

---

## 3. GitHub Releases & GoReleaser

For pre-compiled binary distribution without a local Go compiler:

1. Use **[GoReleaser](https://goreleaser.com/)** configured via `.goreleaser.yaml`.
2. Set up GitHub Actions CI/CD to build binaries on tag push for:
   * `darwin/arm64` (Apple Silicon)
   * `darwin/amd64` (Intel Mac)
   * `linux/amd64` (Linux x86_64)
   * `linux/arm64` (Linux ARM)
3. GoReleaser can automatically update the Homebrew tap formula on release publication.
