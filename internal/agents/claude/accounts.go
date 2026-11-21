package claude

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) APIKeyEnvVar() string { return "ANTHROPIC_API_KEY" }
func (*Plugin) LoginCommand() string { return "claude login" }

func (*Plugin) AccountEnvVars(homeDir string, acct *agent.AgentAccount) map[string]string {
	if acct == nil {
		return nil
	}
	home := effectiveHome(homeDir)
	envVars := make(map[string]string, len(acct.Env)+2)
	if acct.ConfigDir != "" {
		envVars["CLAUDE_CONFIG_DIR"] = agentutil.ExpandTildeWithHome(acct.ConfigDir, home)
	}
	for k, v := range acct.Env {
		envVars[k] = v
	}
	if acct.Name != "" {
		envVars["ACKBAR_ACCOUNT"] = acct.Name
	}
	return envVars
}

func (*Plugin) IsAccountLoggedIn(homeDir string, acct *agent.AgentAccount) bool {
	if acct == nil {
		return false
	}
	home := effectiveHome(homeDir)
	if acct.Env != nil && acct.Env["ANTHROPIC_API_KEY"] != "" {
		return true
	}
	if acct.ConfigDir != "" {
		cfgDir := agentutil.ExpandTildeWithHome(acct.ConfigDir, home)
		if data, err := os.ReadFile(filepath.Join(cfgDir, ".claude.json")); err == nil && strings.Contains(string(data), "oauthAccount") {
			return true
		}
		if _, err := os.Stat(filepath.Join(cfgDir, "settings.json")); err == nil {
			return true
		}
	}
	if acct.Name == "default" || acct.ConfigDir == "" {
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			return true
		}
		if home != "" {
			if data, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil && strings.Contains(string(data), "oauthAccount") {
				return true
			}
			if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
				return true
			}
		}
	}
	return false
}

func (*Plugin) SetupProfileDir(homeDir, name, configDir string) (string, error) {
	home := effectiveHome(homeDir)
	if configDir == "" {
		configDir = filepath.Join(home, ".claude-profiles", name)
	} else {
		configDir = agentutil.ExpandTildeWithHome(configDir, home)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}
	settingsPath := filepath.Join(configDir, "settings.json")
	hookURL := fmt.Sprintf("http://127.0.0.1:7777/v1/hooks/claude-code?account=%s", url.QueryEscape(name))
	_ = ConfigureHookSettings(settingsPath, hookURL, "", true)
	return configDir, nil
}
