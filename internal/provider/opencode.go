package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/daemon"
)

type OpenCodeProvider struct{}

func NewOpenCodeProvider() *OpenCodeProvider {
	return &OpenCodeProvider{}
}

func (o *OpenCodeProvider) Agent() string {
	return "opencode"
}

func (o *OpenCodeProvider) DisplayName() string {
	return "OpenCode"
}

func (o *OpenCodeProvider) BrandColor() string {
	return "#6366F1"
}

func (o *OpenCodeProvider) IconSVG() string {
	return `<svg class="agent-logo-svg opencode-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>`
}

func (o *OpenCodeProvider) ProcessNames() []string {
	return []string{"opencode", "open-code"}
}

func (o *OpenCodeProvider) GetSpawnCommand(tempUUID string) string {
	return "opencode"
}

func (o *OpenCodeProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" && isSafeSessionID(nativeID) {
		return "opencode resume " + nativeID
	}
	return "opencode"
}

func (o *OpenCodeProvider) IsInstalled() bool {
	if lookPathInStandardDirs("opencode") || lookPathInStandardDirs("open-code") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(filepath.Join(home, ".opencode")); err == nil {
			return true
		}
	}
	return false
}

func (o *OpenCodeProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "ackbar-hook --agent=opencode"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	// 1. Check ~/.opencode/hooks.json
	hooksPath := filepath.Join(home, ".opencode", "hooks.json")
	if data, err := os.ReadFile(hooksPath); err == nil {
		content := string(data)
		if strings.Contains(content, "127.0.0.1:7777") || strings.Contains(content, "localhost:7777") || strings.Contains(content, "ackbar-hook") {
			return true, setupCmd, nil
		}
	}

	// 2. Check ~/.opencode/config.json
	configJson := filepath.Join(home, ".opencode", "config.json")
	if data, err := os.ReadFile(configJson); err == nil {
		content := string(data)
		if strings.Contains(content, "127.0.0.1:7777") || strings.Contains(content, "localhost:7777") || strings.Contains(content, "ackbar-hook") {
			return true, setupCmd, nil
		}
	}

	// 3. Check ~/.opencode/config.toml
	configToml := filepath.Join(home, ".opencode", "config.toml")
	if data, err := os.ReadFile(configToml); err == nil {
		content := string(data)
		if strings.Contains(content, "127.0.0.1:7777") || strings.Contains(content, "localhost:7777") || strings.Contains(content, "ackbar-hook") {
			return true, setupCmd, nil
		}
	}

	return false, setupCmd, nil
}

func (o *OpenCodeProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if home == "" || nativeID == "" || !isSafeSessionID(nativeID) {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(home, ".opencode", "sessions", nativeID))
	if logFile := findOpenCodeSessionLog(home, nativeID); logFile != "" {
		_ = os.Remove(logFile)
	}
	return nil
}

func (o *OpenCodeProvider) InspectStatus(ctx context.Context, sess *daemon.Session) bool {
	return daemon.InspectOpenCodeStatus(ctx, sess)
}
