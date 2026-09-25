package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/daemon"
)

type ClaudeProvider struct{}

func NewClaudeProvider() *ClaudeProvider {
	return &ClaudeProvider{}
}

func (c *ClaudeProvider) Agent() string {
	return "claude-code"
}

func (c *ClaudeProvider) DisplayName() string {
	return "Claude Code"
}

func (c *ClaudeProvider) BrandColor() string {
	return "#D97706"
}

func (c *ClaudeProvider) IconSVG() string {
	return `<svg class="agent-logo-svg claude-logo" viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><path clip-rule="evenodd" fill-rule="evenodd" d="M20.998 10.949H24v3.102h-3v3.028h-1.487V20H18v-2.921h-1.487V20H15v-2.921H9V20H7.488v-2.921H6V20H4.487v-2.921H3V14.05H0V10.95h3V5h17.998v5.949zM6 10.949h1.488V8.102H6v2.847zm10.51 0H18V8.102h-1.49v2.847z"/></svg>`
}

func (c *ClaudeProvider) ProcessNames() []string {
	return []string{"claude", "@anthropic-ai/claude-code"}
}

func (c *ClaudeProvider) GetSpawnCommand(tempUUID string) string {
	if tempUUID != "" && isValidUUID(tempUUID) {
		return "claude --session-id " + tempUUID
	}
	return "claude"
}

func (c *ClaudeProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" && isValidUUID(nativeID) {
		return "claude --resume " + nativeID
	}
	return ""
}

func (c *ClaudeProvider) IsInstalled() bool {
	if lookPathInStandardDirs("claude") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
			return true
		}
	}
	return false
}

func (c *ClaudeProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "go run ./cmd/ackbar setup-hooks  # (or edit ~/.claude/settings.json)"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		if strings.Contains(string(data), "127.0.0.1:7777") || strings.Contains(string(data), "localhost:7777") || strings.Contains(string(data), "ackbar-hook") {
			return true, setupCmd, nil
		}
	}

	return false, setupCmd, nil
}

func (c *ClaudeProvider) InspectStatus(ctx context.Context, sess *daemon.Session) bool {
	return daemon.InspectClaudeStatus(ctx, sess)
}
