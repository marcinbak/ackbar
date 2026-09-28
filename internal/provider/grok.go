package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/daemon"
)

type GrokProvider struct{}

func NewGrokProvider() *GrokProvider {
	return &GrokProvider{}
}

func (g *GrokProvider) Agent() string {
	return "grok"
}

func (g *GrokProvider) DisplayName() string {
	return "xAI Grok"
}

func (g *GrokProvider) BrandColor() string {
	return "#E5E7EB"
}

func (g *GrokProvider) IconSVG() string {
	return `<svg class="agent-logo-svg grok-logo" viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/></svg>`
}

func (g *GrokProvider) ProcessNames() []string {
	return []string{"grok", "grok-cli"}
}

func (g *GrokProvider) GetSpawnCommand(tempUUID string) string {
	return "grok"
}

func (g *GrokProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" && isSafeSessionID(nativeID) {
		return "grok resume " + nativeID
	}
	return "grok"
}

func (g *GrokProvider) IsInstalled() bool {
	if lookPathInStandardDirs("grok") || lookPathInStandardDirs("grok-cli") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(filepath.Join(home, ".grok")); err == nil {
			return true
		}
	}
	return false
}

func (g *GrokProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "ackbar-hook --agent=grok"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	// 1. Check ~/.grok/hooks.json
	hooksPath := filepath.Join(home, ".grok", "hooks.json")
	if data, err := os.ReadFile(hooksPath); err == nil {
		content := string(data)
		if strings.Contains(content, "127.0.0.1:7777") || strings.Contains(content, "localhost:7777") || strings.Contains(content, "ackbar-hook") {
			return true, setupCmd, nil
		}
	}

	// 2. Check ~/.grok/config.toml
	configToml := filepath.Join(home, ".grok", "config.toml")
	if data, err := os.ReadFile(configToml); err == nil {
		content := string(data)
		if strings.Contains(content, "127.0.0.1:7777") || strings.Contains(content, "localhost:7777") || strings.Contains(content, "ackbar-hook") {
			return true, setupCmd, nil
		}
	}

	// 3. Check ~/.grok/config.json
	configJson := filepath.Join(home, ".grok", "config.json")
	if data, err := os.ReadFile(configJson); err == nil {
		content := string(data)
		if strings.Contains(content, "127.0.0.1:7777") || strings.Contains(content, "localhost:7777") || strings.Contains(content, "ackbar-hook") {
			return true, setupCmd, nil
		}
	}

	return false, setupCmd, nil
}

func (g *GrokProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if home == "" || nativeID == "" || !isSafeSessionID(nativeID) {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(home, ".grok", "sessions", nativeID))
	if logFile := findGrokSessionLog(home, nativeID); logFile != "" {
		_ = os.Remove(logFile)
	}
	return nil
}

func (g *GrokProvider) InspectStatus(ctx context.Context, sess *daemon.Session) bool {
	return daemon.InspectGrokStatus(ctx, sess)
}
