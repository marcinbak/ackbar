package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/version"
)

func (p *Plugin) IsInstalled(homeDir string) bool {
	if _, err := os.Stat(HomeDir(homeDir)); err == nil {
		return true
	}
	_, err := agentutil.LookPathInStandardDirs("codex")
	return err == nil
}

func (p *Plugin) CheckStatus(ctx context.Context, homeDir string) agent.AgentStatus {
	_ = ctx
	codexDir := HomeDir(homeDir)
	configPath := filepath.Join(codexDir, "config.json")
	skillsDir := filepath.Join(codexDir, "skills")
	rulesFile := filepath.Join(codexDir, "AGENTS.md")

	detected := p.IsInstalled(homeDir)
	if !detected {
		if _, err := os.Stat(configPath); err == nil {
			detected = true
		}
	}

	status := agent.AgentStatus{
		Key:         p.Key(),
		DisplayName: p.DisplayName(),
		Detected:    detected,
		ConfigFile:  configPath,
		SkillsDir:   skillsDir,
		RulesFile:   rulesFile,
	}

	if cfgData, err := os.ReadFile(configPath); err == nil {
		status.MCPInstalled, status.MCPCommand = agentutil.InspectJSONMCPServer(cfgData, "ackbar")
	}

	skillMDPath := filepath.Join(skillsDir, "ackbar-tasks", "SKILL.md")
	if data, err := os.ReadFile(skillMDPath); err == nil {
		status.SkillInstalled = true
		status.SkillVersion = agentutil.ExtractSkillVersion(string(data))
	}

	if data, err := os.ReadFile(rulesFile); err == nil {
		if ver := agentutil.ExtractRulesVersion(string(data)); ver != "" {
			status.RulesInstalled = true
			status.RulesVersion = ver
		}
	}

	if detected {
		if !status.MCPInstalled || !status.SkillInstalled || status.SkillVersion != version.Version ||
			!status.RulesInstalled || status.RulesVersion != version.Version {
			status.DriftDetected = true
		}
	}
	return status
}

func (*Plugin) InstallHooks(homeDir string) error {
	_ = homeDir
	return nil
}

func (*Plugin) Provision(ctx context.Context, homeDir string) error {
	_ = ctx
	codexDir := HomeDir(homeDir)
	if err := agentutil.UpsertJSONMCPServer(filepath.Join(codexDir, "config.json"), "ackbar", map[string]any{
		"command": "ackbar",
		"args":    []string{"mcp"},
	}); err != nil {
		return err
	}
	if err := agentutil.InstallEmbeddedSkill(filepath.Join(codexDir, "skills", "ackbar-tasks")); err != nil {
		return err
	}
	return agentutil.UpsertMarkdownRulesBlock(filepath.Join(codexDir, "AGENTS.md"), version.Version)
}

func (*Plugin) APIKeyEnvVar() string { return "OPENAI_API_KEY" }

func (*Plugin) SetupProfileDir(homeDir, name, configDir string) (string, error) {
	if configDir == "" {
		configDir = filepath.Join(homeDir, ".codex-profiles", name)
	} else if strings.HasPrefix(configDir, "~/") && homeDir != "" {
		configDir = filepath.Join(homeDir, configDir[2:])
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}
	return configDir, nil
}

func (*Plugin) AccountEnvVars(homeDir string, acct *agent.AgentAccount) map[string]string {
	if acct == nil {
		return nil
	}
	envVars := make(map[string]string, len(acct.Env)+2)
	for k, v := range acct.Env {
		envVars[k] = v
	}
	if acct.Name != "" {
		envVars["ACKBAR_ACCOUNT"] = acct.Name
	}
	if acct.ConfigDir != "" {
		cfgDir := acct.ConfigDir
		if strings.HasPrefix(cfgDir, "~/") && homeDir != "" {
			cfgDir = filepath.Join(homeDir, cfgDir[2:])
		}
		envVars["CODEX_HOME"] = cfgDir
	}
	return envVars
}

func (*Plugin) LoginCommand() string { return "codex login" }
