package daemon

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// NormalizeGitURL normalizes standard git remote URLs to a canonical form:
// e.g. "git@github.com:mode/app.git" -> "github.com/mode/app"
// e.g. "https://github.com/Mode/app" -> "github.com/mode/app"
func NormalizeGitURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Lowercase the entire URL for canonical comparison
	normalized := strings.ToLower(raw)

	// Remove protocol prefix (https://, http://, ssh://, git://)
	protoReg := regexp.MustCompile(`^(https?|ssh|git|ftp)://`)
	normalized = protoReg.ReplaceAllString(normalized, "")

	// Remove ssh username (e.g. git@)
	userReg := regexp.MustCompile(`^[^@]+@`)
	normalized = userReg.ReplaceAllString(normalized, "")

	// Replace colon separators with slash (common in ssh clone urls: github.com:mode/app)
	normalized = strings.ReplaceAll(normalized, ":", "/")

	// Strip trailing slashes and .git
	normalized = strings.TrimSuffix(normalized, "/")
	normalized = strings.TrimSuffix(normalized, ".git")
	normalized = strings.TrimSuffix(normalized, "/")

	return normalized
}

// GetProjectKey returns the canonical project identifier for a directory.
// If it has a git remote origin, it normalizes that URL.
// Fallback is "path:<basename>".
func GetProjectKey(cwd string) string {
	if cwd == "" {
		return "unknown"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	// Try getting the git remote origin url
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err == nil {
		remote := strings.TrimSpace(string(out))
		if normalized := NormalizeGitURL(remote); normalized != "" {
			return normalized
		}
	}

	// Fallback to directory basename
	base := filepath.Base(cwd)
	if base == "." || base == "/" {
		return "path:root"
	}
	return "path:" + base
}
