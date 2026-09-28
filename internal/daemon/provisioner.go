package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ackbar/internal/skills"
	"ackbar/internal/version"
)

// AgentStatus represents the installation and configuration state for an agent
type AgentStatus struct {
	Key            string `json:"key"`             // e.g. "claude-code", "antigravity", "codex"
	DisplayName    string `json:"display_name"`    // e.g. "Claude Code"
	Detected       bool   `json:"detected"`        // Whether agent was detected on system
	ConfigFile     string `json:"config_file"`     // Path to MCP configuration file
	SkillsDir      string `json:"skills_dir"`      // Path to skills directory
	MCPInstalled   bool   `json:"mcp_installed"`   // Whether ackbar MCP server is configured
	MCPCommand     string `json:"mcp_command"`     // Registered command string (e.g. "ackbar mcp")
	SkillInstalled bool   `json:"skill_installed"` // Whether ackbar-tasks skill is installed
	SkillVersion   string `json:"skill_version"`   // Installed skill version (e.g. "20260929.02")
	DriftDetected  bool   `json:"drift_detected"`  // Whether configuration or skill version is out of date
}

// AgentDefinition encapsulates paths and conventions for an agent runtime
type AgentDefinition struct {
	Key         string
	DisplayName string
	CLICommand  string
	HomeSubdir  string // e.g. ".claude"
	ConfigRel   string // e.g. ".claude.json" or ".gemini/antigravity/mcp_config.json"
	SkillsRel   string // e.g. ".claude/skills"
}

var supportedAgents = []AgentDefinition{
	{
		Key:         "claude-code",
		DisplayName: "Claude Code",
		CLICommand:  "claude",
		HomeSubdir:  ".claude",
		ConfigRel:   ".claude.json",
		SkillsRel:   filepath.Join(".claude", "skills"),
	},
	{
		Key:         "antigravity",
		DisplayName: "Google Antigravity",
		CLICommand:  "agy",
		HomeSubdir:  filepath.Join(".gemini", "antigravity"),
		ConfigRel:   filepath.Join(".gemini", "antigravity", "mcp_config.json"),
		SkillsRel:   filepath.Join(".gemini", "antigravity", "skills"),
	},
	{
		Key:         "codex",
		DisplayName: "OpenAI Codex",
		CLICommand:  "codex",
		HomeSubdir:  ".codex",
		ConfigRel:   filepath.Join(".codex", "config.json"),
		SkillsRel:   filepath.Join(".codex", "skills"),
	},
}

// GetAgentStatuses inspects the system and returns current status for all supported agents
func GetAgentStatuses(customHome string) ([]AgentStatus, error) {
	home := customHome
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("unable to determine user home directory: %w", err)
		}
		home = h
	}

	var results []AgentStatus

	for _, def := range supportedAgents {
		status := inspectAgent(home, def)
		results = append(results, status)
	}

	return results, nil
}

func inspectAgent(home string, def AgentDefinition) AgentStatus {
	configPath := filepath.Join(home, def.ConfigRel)
	skillsDirPath := filepath.Join(home, def.SkillsRel)
	homeSubdirPath := filepath.Join(home, def.HomeSubdir)

	// Check if agent is detected on machine
	detected := false
	if _, err := os.Stat(homeSubdirPath); err == nil {
		detected = true
	} else if _, err := os.Stat(configPath); err == nil {
		detected = true
	} else if _, err := exec.LookPath(def.CLICommand); err == nil {
		detected = true
	}

	status := AgentStatus{
		Key:         def.Key,
		DisplayName: def.DisplayName,
		Detected:    detected,
		ConfigFile:  configPath,
		SkillsDir:   skillsDirPath,
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
	skillMDPath := filepath.Join(skillsDirPath, "ackbar-tasks", "SKILL.md")
	if data, err := os.ReadFile(skillMDPath); err == nil {
		status.SkillInstalled = true
		status.SkillVersion = extractSkillVersion(string(data))
	}

	// 3. Evaluate Drift
	// Drift occurs if detected but MCP or Skill is missing, or Skill version is not current
	if detected {
		if !status.MCPInstalled || !status.SkillInstalled || status.SkillVersion != version.Version {
			status.DriftDetected = true
		}
	}

	return status
}

// ProvisionAgents installs the Ackbar Tasks MCP server and Skill into the specified agents
func ProvisionAgents(customHome string, targetAgentKeys []string) ([]AgentStatus, error) {
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

	for _, def := range supportedAgents {
		if !provisionAll && !targetMap[def.Key] {
			continue
		}

		// 1. Configure MCP server in agent's config file
		if err := configureAgentMCP(home, def); err != nil {
			return nil, fmt.Errorf("failed to configure MCP for %s: %w", def.DisplayName, err)
		}

		// 2. Install versioned skill into agent's skill directory
		if err := installAgentSkill(home, def); err != nil {
			return nil, fmt.Errorf("failed to install skill for %s: %w", def.DisplayName, err)
		}
	}

	// Return updated statuses
	return GetAgentStatuses(home)
}

func configureAgentMCP(home string, def AgentDefinition) error {
	configPath := filepath.Join(home, def.ConfigRel)

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

	// Set ackbar entry
	mcpServers["ackbar"] = map[string]any{
		"command": "ackbar",
		"args":    []string{"mcp"},
	}

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
	targetSkillDir := filepath.Join(home, def.SkillsRel, "ackbar-tasks")
	if err := os.MkdirAll(targetSkillDir, 0755); err != nil {
		return fmt.Errorf("failed to create skill directory %s: %w", targetSkillDir, err)
	}

	skillFiles, err := skills.GetSkillFiles()
	if err != nil {
		return fmt.Errorf("failed to retrieve embedded skill files: %w", err)
	}

	for relPath, content := range skillFiles {
		filePath := filepath.Join(targetSkillDir, relPath)
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", filePath, err)
		}
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			return fmt.Errorf("failed to write skill file %s: %w", filePath, err)
		}
	}

	return nil
}

func extractSkillVersion(content string) string {
	lines := strings.Split(content, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "version:") {
			val := strings.TrimPrefix(l, "version:")
			val = strings.Trim(strings.TrimSpace(val), "\"'")
			return val
		}
	}
	return ""
}
