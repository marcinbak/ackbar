package claude

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/version"
)

var HookEventKeys = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PermissionRequest",
	"Notification",
	"SubagentStart",
	"SubagentStop",
	"Stop",
}

func (*Plugin) IsInstalled(homeDir string) bool {
	if _, err := agentutil.LookPathInStandardDirs("claude"); err == nil {
		return true
	}
	home := effectiveHome(homeDir)
	return home != "" && agentutil.DirExists(filepath.Join(home, ".claude"))
}

func (*Plugin) SkillDirs(homeDir string) []string {
	home := effectiveHome(homeDir)
	if home == "" {
		return nil
	}
	return []string{filepath.Join(home, ".claude", "skills")}
}

func (p *Plugin) CheckStatus(_ context.Context, homeDir string) agent.AgentStatus {
	home := effectiveHome(homeDir)
	homeSubdir := filepath.Join(home, ".claude")
	configPath := filepath.Join(home, ".claude.json")
	skillsDir := filepath.Join(homeSubdir, "skills")
	rulesFile := filepath.Join(homeSubdir, "CLAUDE.md")

	detected := false
	if _, err := os.Stat(homeSubdir); err == nil {
		detected = true
	}
	if !detected {
		if _, err := os.Stat(configPath); err == nil {
			detected = true
		}
	}
	if !detected {
		if _, err := exec.LookPath("claude"); err == nil {
			detected = true
		}
	}

	st := agent.AgentStatus{
		Key:         p.Key(),
		DisplayName: p.DisplayName(),
		Detected:    detected,
		ConfigFile:  configPath,
		SkillsDir:   skillsDir,
		RulesFile:   rulesFile,
	}

	if cfgData, err := os.ReadFile(configPath); err == nil {
		st.MCPInstalled, st.MCPCommand = agentutil.InspectJSONMCPServer(cfgData, "ackbar")
	}
	if data, err := os.ReadFile(filepath.Join(skillsDir, "ackbar-tasks", "SKILL.md")); err == nil {
		st.SkillInstalled = true
		st.SkillVersion = agentutil.ExtractSkillVersion(string(data))
	}
	if data, err := os.ReadFile(rulesFile); err == nil {
		if ver := agentutil.ExtractRulesVersion(string(data)); ver != "" {
			st.RulesInstalled = true
			st.RulesVersion = ver
		}
	}
	if detected {
		if !st.MCPInstalled || !st.SkillInstalled || st.SkillVersion != version.Version ||
			!st.RulesInstalled || st.RulesVersion != version.Version {
			st.DriftDetected = true
		}
	}
	return st
}

func (*Plugin) Provision(_ context.Context, homeDir string) error {
	home := effectiveHome(homeDir)
	configPath := filepath.Join(home, ".claude.json")
	if err := agentutil.UpsertJSONMCPServer(configPath, "ackbar", map[string]any{
		"command": "ackbar",
		"args":    []string{"mcp"},
	}); err != nil {
		return err
	}
	if err := agentutil.InstallEmbeddedSkill(filepath.Join(home, ".claude", "skills", "ackbar-tasks")); err != nil {
		return err
	}
	return agentutil.UpsertMarkdownRulesBlock(filepath.Join(home, ".claude", "CLAUDE.md"), version.Version)
}

func ConfigureHookSettings(settingsPath, hookURL, rawOverride string, createIfMissing bool) error {
	var settings map[string]interface{}
	raw := rawOverride
	if raw == "" {
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			if !createIfMissing || !os.IsNotExist(err) {
				return err
			}
		} else {
			raw = string(data)
		}
	}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return err
		}
	}
	if settings == nil {
		settings = make(map[string]interface{})
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}
	correctHookEntry := map[string]interface{}{
		"matcher": "",
		"hooks": []map[string]string{
			{
				"type": "http",
				"url":  hookURL,
			},
		},
	}
	for _, evtKey := range HookEventKeys {
		existingList, _ := hooks[evtKey].([]interface{})
		var validList []interface{}
		hasHook := false
		for _, item := range existingList {
			if m, ok := item.(map[string]interface{}); ok {
				if innerHooks, ok := m["hooks"].([]interface{}); ok {
					isAckbarHook := false
					for _, h := range innerHooks {
						if hm, ok := h.(map[string]interface{}); ok {
							u, _ := hm["url"].(string)
							cmdStr, _ := hm["command"].(string)
							if u == hookURL || strings.Contains(u, "/v1/hooks/claude-code") || strings.Contains(cmdStr, "ackbar-hook") {
								isAckbarHook = true
							}
						}
					}
					if isAckbarHook {
						if hasHook {
							continue
						}
						hasHook = true
					}
					validList = append(validList, item)
				}
			}
		}
		if !hasHook {
			validList = append(validList, correctHookEntry)
		}
		hooks[evtKey] = validList
	}
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, out, 0o644)
}

func (*Plugin) InstallHooks(homeDir string) error {
	home := effectiveHome(homeDir)
	if home == "" {
		return os.ErrNotExist
	}
	legacyClaudePath := filepath.Join(home, ".claude.json")
	if data, err := os.ReadFile(legacyClaudePath); err == nil {
		var legMap map[string]interface{}
		if err := json.Unmarshal(data, &legMap); err == nil {
			if _, exists := legMap["hooks"]; exists {
				delete(legMap, "hooks")
				if updated, err := json.MarshalIndent(legMap, "", "  "); err == nil {
					_ = os.WriteFile(legacyClaudePath, updated, 0o644)
				}
			}
		}
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}
	content := string(data)
	content = strings.ReplaceAll(content, "Write(~/.memories/log/**)", "Edit(~/.memories/log/**)")
	content = strings.ReplaceAll(content, "Write("+home+"/.memories/log/**)", "Edit("+home+"/.memories/log/**)")
	return ConfigureHookSettings(settingsPath, "http://127.0.0.1:7777/v1/hooks/claude-code", content, false)
}

func (*Plugin) CheckHookConfig(homeDir string) (bool, string, error) {
	setupCmd := "go run ./cmd/ackbar setup-hooks  # (or edit ~/.claude/settings.json)"
	home := effectiveHome(homeDir)
	if home == "" {
		return false, setupCmd, nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err == nil {
		s := string(data)
		if strings.Contains(s, "127.0.0.1:7777") || strings.Contains(s, "localhost:7777") || strings.Contains(s, "ackbar-hook") {
			return true, setupCmd, nil
		}
	}
	return false, setupCmd, nil
}

func (*Plugin) HookAckPayload(_ string) []byte { return []byte("{}") }

func (*Plugin) ResolveSubagentParent(_, _ string) (string, string, string) {
	return "", "", ""
}
