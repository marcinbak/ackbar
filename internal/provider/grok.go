package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/daemon"
)

type GrokProvider struct {
	daemon.GrokAgentAdapter
}

var _ daemon.AgentAdapter = (*GrokProvider)(nil)

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
	if tempUUID != "" && daemon.IsUUID(tempUUID) {
		return "grok -s " + tempUUID
	}
	return "grok"
}

func (g *GrokProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" && isSafeSessionID(nativeID) {
		return "grok -r " + nativeID
	}
	return ""
}

func (g *GrokProvider) IsInstalled() bool {
	if lookPathInStandardDirs("grok") || lookPathInStandardDirs("grok-cli") {
		return true
	}
	home, _ := os.UserHomeDir()
	grokHome := daemon.GrokHomeDir(home)
	if _, err := os.Stat(grokHome); err == nil {
		return true
	}
	return false
}

func (g *GrokProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "ackbar-hook --agent=grok"
	home, _ := os.UserHomeDir()
	grokHome := daemon.GrokHomeDir(home)

	checkContent := func(p string) bool {
		data, err := os.ReadFile(p)
		if err != nil {
			return false
		}
		c := string(data)
		return strings.Contains(c, "127.0.0.1:7777") || strings.Contains(c, "localhost:7777") || strings.Contains(c, "ackbar-hook")
	}

	if checkContent(filepath.Join(grokHome, "hooks", "ackbar.json")) {
		return true, setupCmd, nil
	}
	if matches, err := filepath.Glob(filepath.Join(grokHome, "hooks", "*.json")); err == nil {
		for _, m := range matches {
			if checkContent(m) {
				return true, setupCmd, nil
			}
		}
	}
	if checkContent(filepath.Join(grokHome, "hooks.json")) ||
		checkContent(filepath.Join(grokHome, "config.toml")) ||
		checkContent(filepath.Join(grokHome, "config.json")) {
		return true, setupCmd, nil
	}
	return false, setupCmd, nil
}

func (g *GrokProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if nativeID == "" || !isSafeSessionID(nativeID) {
		return nil
	}
	if sessDir := daemon.FindGrokSessionDirForHome(home, cwd, nativeID); sessDir != "" {
		_ = os.RemoveAll(sessDir)
	}
	grokHome := daemon.GrokHomeDir(home)
	_ = os.RemoveAll(filepath.Join(grokHome, "sessions", nativeID))
	_ = os.Remove(filepath.Join(grokHome, "sessions", nativeID+".jsonl"))
	return nil
}

func (g *GrokProvider) InspectStatus(ctx context.Context, sess *daemon.Session) bool {
	return daemon.InspectGrokStatus(ctx, sess)
}
