package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type Plugin struct {
	agent.BasePlugin
	agent.BaseInstallationDetector
	agent.BaseAccounts
	agent.BaseTMuxDriver
	agent.BaseHooks
	agent.BaseTranscripts
	agent.BaseScanner
}

var (
	_ agent.Plugin               = (*Plugin)(nil)
	_ agent.Branding             = (*Plugin)(nil)
	_ agent.InstallationDetector = (*Plugin)(nil)
	_ agent.Accounts             = (*Plugin)(nil)
	_ agent.TMuxDriver           = (*Plugin)(nil)
	_ agent.Hooks                = (*Plugin)(nil)
	_ agent.Transcripts          = (*Plugin)(nil)
	_ agent.Scanner              = (*Plugin)(nil)
	_ agent.RateLimiter          = (*Plugin)(nil)
	_ agent.TranscriptStatter    = (*Plugin)(nil)
)

var ignoredSubcommands = map[string]bool{
	"login": true, "logout": true, "auth": true, "update": true, "upgrade": true, "models": true, "config": true,
}

func New() *Plugin {
	return &Plugin{}
}

func (*Plugin) Key() string           { return "opencode" }
func (*Plugin) DisplayName() string   { return "OpenCode" }
func (*Plugin) Aliases() []string     { return []string{"open-code"} }
func (*Plugin) BinaryNames() []string { return []string{"opencode", "open-code"} }
func (*Plugin) BrandColor() string    { return "#6366F1" }

func (*Plugin) IconSVG() string {
	return `<svg class="agent-logo-svg opencode-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>`
}

func (*Plugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		SupportsTmux:       true,
		NativeIDStrategy:   agent.NativeIDLazyAdopt,
		SupportsHookShim:   true,
		SupportsTranscript: true,
	}
}

func HomeDir(home string) string {
	if home == "" {
		home = agentutil.RealUserHome()
	}
	return filepath.Join(home, ".opencode")
}

func (*Plugin) IsInstalled(homeDir string) bool {
	if _, err := os.Stat(HomeDir(homeDir)); err == nil {
		return true
	}
	if _, err := agentutil.LookPathInStandardDirs("opencode"); err == nil {
		return true
	}
	_, err := agentutil.LookPathInStandardDirs("open-code")
	return err == nil
}

func (*Plugin) SetupProfileDir(homeDir, name, configDir string) (string, error) {
	if configDir == "" {
		configDir = filepath.Join(homeDir, ".opencode-profiles", name)
	} else if strings.HasPrefix(configDir, "~/") && homeDir != "" {
		configDir = filepath.Join(homeDir, configDir[2:])
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}
	return configDir, nil
}

func (*Plugin) LoginCommand() string { return "opencode login" }

func (*Plugin) IsValidNativeID(nativeID string) bool {
	return agentutil.IsSafeSessionID(nativeID)
}

func (*Plugin) IsIgnoredCommand(fullCmd string) bool {
	return agentutil.CheckIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "opencode" || base == "open-code"
	}, ignoredSubcommands, nil)
}

func (*Plugin) MatchProcessCommand(binLower, cmdLower string) bool {
	return (binLower == "opencode" || binLower == "open-code") && !agentutil.IsAckbarProcess(binLower, cmdLower)
}

func (*Plugin) DetectRateLimit(text string, now time.Time) (bool, time.Time, string) {
	return agentutil.DetectDefaultRateLimitAt(text, now)
}

func extractUserMessageContent(rawType, rawRole string, content, payload json.RawMessage, fallbackLine []byte) (role, text string) {
	role = rawRole
	text = parseContentText(content)
	if role == "" && (rawType == "user" || rawType == "user_prompt") {
		role = "user"
		if text == "" {
			text = parseContentText(payload)
		}
	} else if rawType == "message" || rawType == "response_item" {
		src := payload
		if len(src) == 0 {
			src = fallbackLine
		}
		if len(src) > 0 {
			var msg struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(src, &msg) == nil && msg.Role != "" {
				role = msg.Role
				text = parseContentText(msg.Content)
			}
		}
	}
	return role, text
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
