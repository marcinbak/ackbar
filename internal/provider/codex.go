package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/daemon"
)

type CodexProvider struct{}

func NewCodexProvider() *CodexProvider {
	return &CodexProvider{}
}

func (c *CodexProvider) Agent() string {
	return "codex"
}

func (c *CodexProvider) DisplayName() string {
	return "OpenAI Codex"
}

func (c *CodexProvider) BrandColor() string {
	return "#10A37F"
}

func (c *CodexProvider) IconSVG() string {
	return `<svg class="agent-logo-svg codex-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a4.5 4.5 0 0 1 4.2 2.9l.4.9a4.5 4.5 0 0 1 1.8 4.7l-.2 1a4.5 4.5 0 0 1-2.4 4.1l-.8.5a4.5 4.5 0 0 1-4.7 1.8l-1-.2a4.5 4.5 0 0 1-4.1-2.4l-.5-.8a4.5 4.5 0 0 1-1.8-4.7l.2-1a4.5 4.5 0 0 1 2.4-4.1l.8-.5A4.5 4.5 0 0 1 12 2z"/><path d="M12 6.5l3.5 2v4l-3.5 2l-3.5-2v-4z"/><path d="M8.5 8.5L5 10.5M15.5 8.5L19 10.5M12 14.5V18.5"/></svg>`
}

func (c *CodexProvider) ProcessNames() []string {
	return []string{"codex"}
}

func (c *CodexProvider) GetSpawnCommand(tempUUID string) string {
	return "codex exec"
}

func (c *CodexProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" {
		return "codex exec resume " + nativeID
	}
	return "codex exec"
}

func (c *CodexProvider) IsInstalled() bool {
	if lookPathInStandardDirs("codex") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
			return true
		}
	}
	return false
}

func (c *CodexProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "ackbar-hook --agent=codex"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, setupCmd, nil
	}

	if strings.Contains(string(data), "127.0.0.1:7777") || strings.Contains(string(data), "localhost:7777") || strings.Contains(string(data), "ackbar-hook") {
		return true, setupCmd, nil
	}

	return false, setupCmd, nil
}

func (c *CodexProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	return nil
}

func (c *CodexProvider) ResolveSessionTitle(cwd, nativeID string) string {
	return ""
}

func (c *CodexProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	return nil, fmt.Errorf("codex native transcript extraction not supported")
}

func (c *CodexProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if nativeID == "" {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(home, ".codex", "sessions", nativeID))
	return nil
}
