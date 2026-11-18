package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/version"
)

func (p *Plugin) IsInstalled(homeDir string) bool {
	if _, err := agentutil.LookPathInStandardDirs("grok"); err == nil {
		return true
	}
	if _, err := agentutil.LookPathInStandardDirs("grok-cli"); err == nil {
		return true
	}
	_, err := os.Stat(HomeDir(homeDir))
	return err == nil
}

func (p *Plugin) CheckStatus(ctx context.Context, homeDir string) agent.AgentStatus {
	_ = ctx
	home := homeDir
	if home == "" {
		home = agentutil.RealUserHome()
	}
	grokDir := filepath.Join(home, ".grok")
	configPath := filepath.Join(grokDir, "config.toml")
	skillsDir := filepath.Join(grokDir, "skills")
	rulesFile := filepath.Join(grokDir, "rules", "ackbar.md")

	detected := false
	if _, err := os.Stat(grokDir); err == nil {
		detected = true
	} else if _, err := os.Stat(configPath); err == nil {
		detected = true
	} else if _, err := exec.LookPath("grok"); err == nil {
		detected = true
	}

	status := agent.AgentStatus{
		Key:         p.Key(),
		DisplayName: p.DisplayName(),
		Detected:    detected,
		ConfigFile:  configPath,
		SkillsDir:   skillsDir,
		RulesFile:   rulesFile,
	}

	claudeHooksDisabled := false
	if cfgData, err := os.ReadFile(configPath); err == nil {
		status.MCPInstalled, status.MCPCommand, claudeHooksDisabled = inspectTOMLConfig(cfgData)
	}
	hasHooks := hasAckbarHook(grokDir)

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
		if !status.MCPInstalled || !claudeHooksDisabled || !hasHooks ||
			!status.SkillInstalled || status.SkillVersion != version.Version ||
			!status.RulesInstalled || status.RulesVersion != version.Version {
			status.DriftDetected = true
		}
	}
	return status
}

func (p *Plugin) InstallHooks(homeDir string) error {
	return WriteHooksFile(HomeDir(homeDir), "")
}

func (p *Plugin) Provision(ctx context.Context, homeDir string) error {
	_ = ctx
	home := homeDir
	if home == "" {
		home = agentutil.RealUserHome()
	}
	grokDir := filepath.Join(home, ".grok")
	if err := UpsertTOMLConfig(filepath.Join(grokDir, "config.toml"), "ackbar"); err != nil {
		return err
	}
	if err := WriteHooksFile(grokDir, ""); err != nil {
		return err
	}
	if err := agentutil.InstallEmbeddedSkill(filepath.Join(grokDir, "skills", "ackbar-tasks")); err != nil {
		return err
	}
	return agentutil.UpsertMarkdownRulesBlock(filepath.Join(grokDir, "rules", "ackbar.md"), version.Version)
}

func WriteHooksFile(grokDir, accountName string) error {
	hooksDir := filepath.Join(grokDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return fmt.Errorf("failed to create hooks directory: %w", err)
	}
	_ = os.Chmod(grokDir, 0o700)
	targetFile := filepath.Join(hooksDir, "ackbar.json")
	hookURL := "http://127.0.0.1:7777/v1/hooks/grok"
	if accountName != "" && accountName != "default" {
		hookURL += "?account=" + url.QueryEscape(accountName)
	}

	root := make(map[string]interface{})
	if data, err := os.ReadFile(targetFile); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &root)
	}
	hooksMap, _ := root["hooks"].(map[string]interface{})
	if hooksMap == nil {
		hooksMap = make(map[string]interface{})
	}

	events := []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
		"PostToolUseFailure", "PermissionDenied", "Notification",
		"SubagentStart", "SubagentStop", "Stop", "StopCancelled",
		"StopFailure", "SessionEnd",
	}

	for _, evt := range events {
		var filteredEntries []interface{}
		if existing, ok := hooksMap[evt].([]interface{}); ok {
			for _, entry := range existing {
				entryMap, ok := entry.(map[string]interface{})
				if !ok {
					continue
				}
				hooksList, ok := entryMap["hooks"].([]interface{})
				if !ok {
					filteredEntries = append(filteredEntries, entry)
					continue
				}
				var keepHooks []interface{}
				for _, h := range hooksList {
					hMap, ok := h.(map[string]interface{})
					if !ok {
						keepHooks = append(keepHooks, h)
						continue
					}
					u, _ := hMap["url"].(string)
					c, _ := hMap["command"].(string)
					if strings.Contains(u, "127.0.0.1:7777/v1/hooks/grok") ||
						strings.Contains(u, "localhost:7777/v1/hooks/grok") ||
						strings.Contains(c, "ackbar-hook") {
						continue
					}
					keepHooks = append(keepHooks, h)
				}
				if len(keepHooks) > 0 {
					entryMap["hooks"] = keepHooks
					filteredEntries = append(filteredEntries, entryMap)
				}
			}
		}
		filteredEntries = append(filteredEntries, map[string]interface{}{
			"hooks": []map[string]interface{}{
				{"type": "http", "url": hookURL, "timeout": 5},
			},
		})
		hooksMap[evt] = filteredEntries
	}
	root["hooks"] = hooksMap

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize grok hooks: %w", err)
	}
	data = append(data, '\n')
	return agentutil.WriteFileAtomic(targetFile, data, 0o600)
}

func hasAckbarHook(grokDir string) bool {
	data, err := os.ReadFile(filepath.Join(grokDir, "hooks", "ackbar.json"))
	if err != nil {
		return false
	}
	var root map[string]interface{}
	if json.Unmarshal(data, &root) != nil {
		return false
	}
	hooksMap, ok := root["hooks"].(map[string]interface{})
	if !ok || len(hooksMap) == 0 {
		return false
	}
	for _, entries := range hooksMap {
		list, ok := entries.([]interface{})
		if !ok {
			continue
		}
		for _, rawItem := range list {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				continue
			}
			innerList, _ := item["hooks"].([]interface{})
			for _, inner := range innerList {
				hMap, _ := inner.(map[string]interface{})
				u, _ := hMap["url"].(string)
				c, _ := hMap["command"].(string)
				if strings.Contains(u, "127.0.0.1:7777/v1/hooks/grok") ||
					strings.Contains(u, "localhost:7777/v1/hooks/grok") ||
					strings.Contains(c, "ackbar-hook") {
					return true
				}
			}
		}
	}
	return false
}
