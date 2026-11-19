package antigravity

import (
	"fmt"
	"os"
	"path/filepath"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) APIKeyEnvVar() string { return "GEMINI_API_KEY" }
func (*Plugin) LoginCommand() string { return "agy login" }

func (*Plugin) AccountEnvVars(homeDir string, acct *agent.AgentAccount) map[string]string {
	if acct == nil {
		return nil
	}
	envVars := make(map[string]string)
	if acct.ConfigDir != "" {
		envVars["GEMINI_CLI_HOME"] = agentutil.ExpandTildeWithHome(acct.ConfigDir, homeDir)
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
	if acct.Env != nil && acct.Env["GEMINI_API_KEY"] != "" {
		return true
	}
	if acct.ConfigDir != "" {
		cfgDir := agentutil.ExpandTildeWithHome(acct.ConfigDir, homeDir)
		if _, err := os.Stat(cfgDir); err == nil {
			return true
		}
	}
	if homeDir != "" {
		if _, err := os.Stat(filepath.Join(homeDir, ".gemini")); err == nil {
			return true
		}
	}
	return false
}

func (*Plugin) SetupProfileDir(homeDir, name, configDir string) (string, error) {
	home := effectiveHome(homeDir)
	if configDir == "" {
		configDir = filepath.Join(home, ".gemini-profiles", name)
	} else {
		configDir = agentutil.ExpandTildeWithHome(configDir, home)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}
	_ = writeHooksFile(filepath.Join(configDir, "config", "hooks.json"))
	return configDir, nil
}

func (*Plugin) ProfileSkillDirs(homeDir string, acct *agent.AgentAccount) []string {
	if acct == nil || acct.ConfigDir == "" {
		return nil
	}
	cfgDir := agentutil.ExpandTildeWithHome(acct.ConfigDir, effectiveHome(homeDir))
	return []string{
		filepath.Join(cfgDir, "config", "skills"),
		filepath.Join(cfgDir, "antigravity-cli", "skills"),
	}
}
