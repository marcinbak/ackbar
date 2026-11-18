package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/version"
)

type AgentStatus = agent.AgentStatus

// AgentDefinition encapsulates paths and conventions for an agent runtime
type AgentDefinition struct {
	Key         string
	DisplayName string
	CLICommand  string
	HomeSubdir  string // e.g. ".claude"
	ConfigRel   string // e.g. ".claude.json" or ".gemini/antigravity/mcp_config.json"
	SkillsRel   string // e.g. ".claude/skills"
	RulesRel    string // e.g. ".claude/CLAUDE.md"
}

var supportedAgents = []AgentDefinition{
	{
		Key:         "claude-code",
		DisplayName: "Claude Code",
		CLICommand:  "claude",
		HomeSubdir:  ".claude",
		ConfigRel:   ".claude.json",
		SkillsRel:   filepath.Join(".claude", "skills"),
		RulesRel:    filepath.Join(".claude", "CLAUDE.md"),
	},
	{
		Key:         "claude-desktop",
		DisplayName: "Claude Desktop",
		CLICommand:  "",
		HomeSubdir:  "",
		ConfigRel:   "",
		SkillsRel:   "",
		RulesRel:    "",
	},
	{
		Key:         "antigravity",
		DisplayName: "Google Antigravity",
		CLICommand:  "agy",
		HomeSubdir:  filepath.Join(".gemini", "antigravity"),
		ConfigRel:   filepath.Join(".gemini", "antigravity", "mcp_config.json"),
		SkillsRel:   filepath.Join(".gemini", "config", "skills"),
		RulesRel:    filepath.Join(".gemini", "GEMINI.md"),
	},
	{
		Key:         "codex",
		DisplayName: "OpenAI Codex",
		CLICommand:  "codex",
		HomeSubdir:  ".codex",
		ConfigRel:   filepath.Join(".codex", "config.json"),
		SkillsRel:   filepath.Join(".codex", "skills"),
		RulesRel:    filepath.Join(".codex", "AGENTS.md"),
	},
}

// getClaudeDesktopConfigPath resolves the OS-specific path for Claude Desktop config
func getClaudeDesktopConfigPath(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData != "" {
			return filepath.Join(appData, "Claude", "claude_desktop_config.json")
		}
		return filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

func getAgentConfigPath(home string, def AgentDefinition) string {
	if def.Key == "claude-desktop" {
		return getClaudeDesktopConfigPath(home)
	}
	return filepath.Join(home, def.ConfigRel)
}

func getAgentHomeSubdirPath(home string, def AgentDefinition) string {
	if def.Key == "claude-desktop" {
		return filepath.Dir(getClaudeDesktopConfigPath(home))
	}
	return filepath.Join(home, def.HomeSubdir)
}

// GetAgentStatuses inspects the system and returns current status for all supported agents
func GetAgentStatuses(customHome string) ([]AgentStatus, error) {
	return GetAgentStatusesWithRegistry(customHome, nil)
}

func GetAgentStatusesWithRegistry(customHome string, reg *agent.Registry) ([]AgentStatus, error) {
	home := customHome
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("unable to determine user home directory: %w", err)
		}
		home = h
	}

	var results []AgentStatus
	seen := make(map[string]bool)

	for _, def := range supportedAgents {
		seen[def.Key] = true
		status := inspectAgent(home, def)
		results = append(results, status)
	}

	if reg != nil {
		for _, p := range reg.List() {
			if seen[p.Key()] {
				continue
			}
			if prov, ok := reg.AsProvisioner(p.Key()); ok {
				results = append(results, prov.CheckStatus(context.Background(), home))
			}
		}
	}

	return results, nil
}

func inspectAgent(home string, def AgentDefinition) AgentStatus {
	configPath := getAgentConfigPath(home, def)
	skillsDirPath := ""
	if def.SkillsRel != "" {
		skillsDirPath = filepath.Join(home, def.SkillsRel)
	}
	homeSubdirPath := getAgentHomeSubdirPath(home, def)

	// Check if agent is detected on machine
	detected := false
	if homeSubdirPath != "" {
		if _, err := os.Stat(homeSubdirPath); err == nil {
			detected = true
		}
	}
	if !detected && configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			detected = true
		}
	}
	if !detected && def.CLICommand != "" {
		if _, err := exec.LookPath(def.CLICommand); err == nil {
			detected = true
		}
	}

	rulesFilePath := ""
	if def.RulesRel != "" {
		rulesFilePath = filepath.Join(home, def.RulesRel)
	}

	status := AgentStatus{
		Key:         def.Key,
		DisplayName: def.DisplayName,
		Detected:    detected,
		ConfigFile:  configPath,
		SkillsDir:   skillsDirPath,
		RulesFile:   rulesFilePath,
	}

	// 1. Inspect MCP config
	if cfgData, err := os.ReadFile(configPath); err == nil {
		var root map[string]any
		if err := json.Unmarshal(cfgData, &root); err == nil {
			if mcpServers, ok := root["mcpServers"].(map[string]any); ok {
				if ackbarEntry, ok := mcpServers["ackbar"].(map[string]any); ok {
					status.MCPInstalled = true
					cmd, _ := ackbarEntry["command"].(string)
					argsList, _ := ackbarEntry["args"].([]any)
					var argsStr []string
					for _, a := range argsList {
						argsStr = append(argsStr, fmt.Sprint(a))
					}
					status.MCPCommand = strings.TrimSpace(cmd + " " + strings.Join(argsStr, " "))
				}
			}
		}
	}

	// 2. Inspect Skill installation
	if def.SkillsRel == "" {
		status.SkillInstalled = true
		status.SkillVersion = version.Version
	} else {
		skillMDPath := filepath.Join(skillsDirPath, "ackbar-tasks", "SKILL.md")
		if data, err := os.ReadFile(skillMDPath); err == nil {
			status.SkillInstalled = true
			status.SkillVersion = agentutil.ExtractSkillVersion(string(data))
		} else if def.Key == "antigravity" {
			cliSkillMDPath := filepath.Join(home, ".gemini", "antigravity-cli", "skills", "ackbar-tasks", "SKILL.md")
			if data, err := os.ReadFile(cliSkillMDPath); err == nil {
				status.SkillInstalled = true
				status.SkillVersion = agentutil.ExtractSkillVersion(string(data))
			}
		}
	}

	// 3. Inspect Rules installation
	if def.RulesRel == "" {
		status.RulesInstalled = true
		status.RulesVersion = version.Version
	} else {
		if data, err := os.ReadFile(rulesFilePath); err == nil {
			if ver := agentutil.ExtractRulesVersion(string(data)); ver != "" {
				status.RulesInstalled = true
				status.RulesVersion = ver
			}
		} else if def.Key == "antigravity" {
			cliRulesPath := filepath.Join(home, ".gemini", "config", "GEMINI.md")
			if data, err := os.ReadFile(cliRulesPath); err == nil {
				if ver := agentutil.ExtractRulesVersion(string(data)); ver != "" {
					status.RulesInstalled = true
					status.RulesVersion = ver
				}
			}
		}
	}

	// 4. Evaluate Drift
	// Drift occurs if detected but MCP, Skill, or Rules is missing, or versions are out of date
	if detected {
		if !status.MCPInstalled || !status.SkillInstalled || status.SkillVersion != version.Version ||
			!status.RulesInstalled || status.RulesVersion != version.Version {
			status.DriftDetected = true
		}
	}

	return status
}

// ProvisionAgents installs the Ackbar Tasks MCP server, Skill, and global rules into the specified agents
func ProvisionAgents(customHome string, targetAgentKeys []string) ([]AgentStatus, error) {
	return ProvisionAgentsWithRegistry(customHome, targetAgentKeys, nil)
}

func ProvisionAgentsWithRegistry(customHome string, targetAgentKeys []string, reg *agent.Registry) ([]AgentStatus, error) {
	home := customHome
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("unable to determine user home directory: %w", err)
		}
		home = h
	}

	targetMap := make(map[string]bool)
	provisionAll := len(targetAgentKeys) == 0 || (len(targetAgentKeys) == 1 && targetAgentKeys[0] == "all")
	for _, k := range targetAgentKeys {
		targetMap[strings.ToLower(strings.TrimSpace(k))] = true
	}

	seen := make(map[string]bool)
	for _, def := range supportedAgents {
		seen[def.Key] = true
		if !provisionAll && !targetMap[def.Key] {
			continue
		}

		// When provisioning all, claude-desktop is only provisioned if its parent directory exists
		if def.Key == "claude-desktop" && provisionAll && !targetMap["claude-desktop"] {
			claudeParent := filepath.Dir(getClaudeDesktopConfigPath(home))
			if _, err := os.Stat(claudeParent); err != nil {
				continue
			}
		}

		// 1. Configure MCP server in agent's config file
		if err := configureAgentMCP(home, def); err != nil {
			return nil, fmt.Errorf("failed to configure MCP for %s: %w", def.DisplayName, err)
		}

		// 2. Install versioned skill into agent's skill directory
		if err := installAgentSkill(home, def); err != nil {
			return nil, fmt.Errorf("failed to install skill for %s: %w", def.DisplayName, err)
		}

		// 3. Install global communication and brevity rules
		if err := installAgentRules(home, def); err != nil {
			return nil, fmt.Errorf("failed to install rules for %s: %w", def.DisplayName, err)
		}
	}

	if reg != nil {
		for _, p := range reg.List() {
			if seen[p.Key()] {
				continue
			}
			targeted := provisionAll || targetMap[strings.ToLower(p.Key())]
			if !targeted {
				for _, a := range p.Aliases() {
					if targetMap[strings.ToLower(strings.TrimSpace(a))] {
						targeted = true
						break
					}
				}
			}
			if !targeted {
				continue
			}
			if prov, ok := reg.AsProvisioner(p.Key()); ok {
				if err := prov.Provision(context.Background(), home); err != nil {
					return nil, fmt.Errorf("failed to provision %s: %w", p.DisplayName(), err)
				}
			}
		}
	}

	// Return updated statuses
	return GetAgentStatusesWithRegistry(home, reg)
}

func configureAgentMCP(home string, def AgentDefinition) error {
	configPath := getAgentConfigPath(home, def)
	return upsertJSONMCPServer(configPath, "ackbar", map[string]any{
		"command": "ackbar",
		"args":    []string{"mcp"},
	})
}

// UpsertJSONMCPServer merges an MCP server configuration into a target JSON file
func UpsertJSONMCPServer(configPath string, serverName string, serverConfig map[string]any) error {
	return upsertJSONMCPServer(configPath, serverName, serverConfig)
}

func upsertJSONMCPServer(configPath string, serverName string, serverConfig map[string]any) error {
	// If configPath is a symlink, resolve destination to preserve symlink
	targetPath := configPath
	if resolved, err := filepath.EvalSymlinks(configPath); err == nil {
		targetPath = resolved
	}

	var root map[string]any
	if data, err := os.ReadFile(targetPath); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return fmt.Errorf("existing configuration %s contains malformed JSON, aborting to prevent data loss: %w", targetPath, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing config %s: %w", targetPath, err)
	}

	if root == nil {
		root = make(map[string]any)
	}

	// Ensure mcpServers exists
	mcpServers, ok := root["mcpServers"].(map[string]any)
	if !ok || mcpServers == nil {
		mcpServers = make(map[string]any)
		root["mcpServers"] = mcpServers
	}

	// Set entry
	mcpServers[serverName] = serverConfig

	// Ensure parent directory exists with restrictive permissions
	parentDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(parentDir, 0700); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", parentDir, err)
	}

	// Format JSON nicely with 2 spaces
	updatedData, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize MCP config: %w", err)
	}
	updatedData = append(updatedData, '\n')

	// Write atomically via temporary file with 0600 permissions (contains developer credentials)
	tmpPath := targetPath + ".tmp"
	if err := os.WriteFile(tmpPath, updatedData, 0600); err != nil {
		return fmt.Errorf("failed to write temp MCP config %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to atomically update MCP config %s: %w", targetPath, err)
	}

	return nil
}

func installAgentSkill(home string, def AgentDefinition) error {
	if def.SkillsRel == "" {
		return nil
	}

	targetDirs := []string{filepath.Join(home, def.SkillsRel, "ackbar-tasks")}
	if def.Key == "antigravity" {
		targetDirs = append(targetDirs, filepath.Join(home, ".gemini", "antigravity-cli", "skills", "ackbar-tasks"))
	}

	for _, targetSkillDir := range targetDirs {
		if err := agentutil.InstallEmbeddedSkill(targetSkillDir); err != nil {
			return err
		}
	}

	return nil
}

func installAgentRules(home string, def AgentDefinition) error {
	if def.RulesRel == "" {
		return nil
	}

	targets := []string{filepath.Join(home, def.RulesRel)}
	if def.Key == "antigravity" {
		targets = append(targets, filepath.Join(home, ".gemini", "config", "GEMINI.md"))
	}

	for _, target := range targets {
		if err := agentutil.UpsertMarkdownRulesBlock(target, version.Version); err != nil {
			return err
		}
	}

	return nil
}
