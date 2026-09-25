package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/daemon"
)

type AntigravityProvider struct{}

func NewAntigravityProvider() *AntigravityProvider {
	return &AntigravityProvider{}
}

func (a *AntigravityProvider) Agent() string {
	return "antigravity"
}

func (a *AntigravityProvider) DisplayName() string {
	return "Google Antigravity"
}

func (a *AntigravityProvider) BrandColor() string {
	return "#4285F4"
}

func (a *AntigravityProvider) IconSVG() string {
	return `<svg class="agent-logo-svg antigravity-logo" viewBox="0 0 24 24" width="12" height="12"><defs><linearGradient id="agGrad" x1="0%" y1="0%" x2="100%" y2="100%"><stop offset="0%" stop-color="#EA4335"/><stop offset="35%" stop-color="#FBBC04"/><stop offset="70%" stop-color="#34A853"/><stop offset="100%" stop-color="#4285F4"/></linearGradient></defs><path fill="url(#agGrad)" d="M12 0.8 C8.2 0.8 5.6 3.2 4.1 7.8 C2.6 12.5 1.2 18.2 0.3 22.4 C-0.1 23.6 0.8 24.2 1.6 23.6 C4.8 21.2 7.8 14.2 12 12.4 C16.2 14.2 19.2 21.2 22.4 23.6 C23.2 24.2 24.1 23.6 23.7 22.4 C22.8 18.2 21.4 12.5 19.9 7.8 C18.4 3.2 15.8 0.8 12 0.8 Z"/></svg>`
}

func (a *AntigravityProvider) ProcessNames() []string {
	return []string{"antigravity", "agy", "bin/agy"}
}

func (a *AntigravityProvider) GetSpawnCommand(tempUUID string) string {
	return "agy"
}

func (a *AntigravityProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" {
		return "agy --conversation " + nativeID
	}
	return "agy"
}

func (a *AntigravityProvider) IsInstalled() bool {
	if lookPathInStandardDirs("agy") || lookPathInStandardDirs("antigravity") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		dirs := []string{
			filepath.Join(home, ".gemini", "antigravity"),
			filepath.Join(home, ".antigravity"),
			filepath.Join(home, ".gemini"),
		}
		for _, d := range dirs {
			if _, err := os.Stat(d); err == nil {
				return true
			}
		}
	}
	return false
}

func (a *AntigravityProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "go run ./cmd/ackbar setup-hooks  # (or edit ~/.gemini/settings.json)"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	paths := []string{
		filepath.Join(home, ".gemini", "settings.json"),
		filepath.Join(home, ".gemini", "antigravity", "settings.json"),
		filepath.Join(home, ".antigravity", "settings.json"),
		filepath.Join(home, ".antigravity", "config", "hooks.json"),
		filepath.Join(home, ".gemini", "antigravity", "config", "hooks.json"),
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			if strings.Contains(string(data), "127.0.0.1:7777") || strings.Contains(string(data), "localhost:7777") || strings.Contains(string(data), "ackbar-hook") {
				return true, setupCmd, nil
			}
		}
	}

	return false, setupCmd, nil
}

func (a *AntigravityProvider) InspectStatus(ctx context.Context, sess *daemon.Session) bool {
	return daemon.InspectAntigravityStatus(ctx, sess)
}
