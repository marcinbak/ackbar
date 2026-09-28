package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ackbar/internal/version"
)

func TestProvisioner_FullLifecycle(t *testing.T) {
	tempHome := t.TempDir()

	// Setup fake existing Claude Code and Antigravity directories
	claudeDir := filepath.Join(tempHome, ".claude")
	_ = os.MkdirAll(claudeDir, 0700)

	// Create an existing .claude.json with an existing third-party MCP server
	existingClaudeCfg := map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"stitch": map[string]any{
				"command": "npx",
				"args":    []any{"stitch-mcp"},
			},
		},
	}
	cfgBytes, _ := json.MarshalIndent(existingClaudeCfg, "", "  ")
	_ = os.WriteFile(filepath.Join(tempHome, ".claude.json"), cfgBytes, 0600)

	// 1. Inspect initial statuses (Claude should be detected, with drift detected because ackbar is not installed)
	statuses, err := GetAgentStatuses(tempHome)
	if err != nil {
		t.Fatalf("GetAgentStatuses failed: %v", err)
	}

	var claudeStatus *AgentStatus
	for i := range statuses {
		if statuses[i].Key == "claude-code" {
			claudeStatus = &statuses[i]
		}
	}
	if claudeStatus == nil {
		t.Fatalf("Expected claude-code in statuses")
	}
	if !claudeStatus.Detected {
		t.Errorf("Expected claude-code to be detected")
	}
	if claudeStatus.MCPInstalled {
		t.Errorf("Expected MCP not to be installed initially")
	}
	if !claudeStatus.DriftDetected {
		t.Errorf("Expected drift detected initially")
	}

	// 2. Provision all agents
	updatedStatuses, err := ProvisionAgents(tempHome, []string{"all"})
	if err != nil {
		t.Fatalf("ProvisionAgents failed: %v", err)
	}

	for _, s := range updatedStatuses {
		if !s.MCPInstalled {
			t.Errorf("Expected agent %s to have MCP installed", s.Key)
		}
		if !s.SkillInstalled {
			t.Errorf("Expected agent %s to have skill installed", s.Key)
		}
		if s.SkillVersion != version.Version {
			t.Errorf("Expected agent %s skill version to be %s, got %s", s.Key, version.Version, s.SkillVersion)
		}
		if s.DriftDetected {
			t.Errorf("Expected no drift for agent %s after provisioning", s.Key)
		}
	}

	// 3. Verify non-destructive merge and secure 0600 permissions in ~/.claude.json
	claudeCfgPath := filepath.Join(tempHome, ".claude.json")
	fi, err := os.Stat(claudeCfgPath)
	if err != nil {
		t.Fatalf("Failed to stat .claude.json: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("Expected .claude.json permissions to be 0600, got: %o", fi.Mode().Perm())
	}

	claudeCfgData, err := os.ReadFile(claudeCfgPath)
	if err != nil {
		t.Fatalf("Failed to read updated .claude.json: %v", err)
	}
	var mergedClaudeCfg map[string]any
	if err := json.Unmarshal(claudeCfgData, &mergedClaudeCfg); err != nil {
		t.Fatalf("Failed to parse merged config: %v", err)
	}

	// Verify original key "theme" is preserved
	if mergedClaudeCfg["theme"] != "dark" {
		t.Errorf("Expected 'theme': 'dark' to be preserved, got: %v", mergedClaudeCfg["theme"])
	}

	// Verify original MCP server "stitch" is preserved
	mcpServers := mergedClaudeCfg["mcpServers"].(map[string]any)
	if _, ok := mcpServers["stitch"]; !ok {
		t.Errorf("Expected existing 'stitch' MCP server to be preserved")
	}

	// Verify new "ackbar" MCP server is added
	ackbarEntry, ok := mcpServers["ackbar"].(map[string]any)
	if !ok {
		t.Fatalf("Expected 'ackbar' entry in mcpServers")
	}
	if ackbarEntry["command"] != "ackbar" {
		t.Errorf("Expected command 'ackbar', got: %v", ackbarEntry["command"])
	}

	// 4. Verify skill files written and versioned
	skillPath := filepath.Join(tempHome, ".claude", "skills", "ackbar-tasks", "SKILL.md")
	skillContent, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("Failed to read deployed SKILL.md: %v", err)
	}
	if !strings.Contains(string(skillContent), "version: \""+version.Version+"\"") {
		t.Errorf("Expected version tag in deployed SKILL.md, got:\n%s", string(skillContent))
	}
	if !strings.Contains(string(skillContent), "mcp__ackbar__report_blocker") {
		t.Errorf("Expected report_blocker tool usage in deployed SKILL.md")
	}
}

func TestProvisioner_MalformedJSONRefusesOverwrite(t *testing.T) {
	tempHome := t.TempDir()
	claudeDir := filepath.Join(tempHome, ".claude")
	_ = os.MkdirAll(claudeDir, 0700)

	// Write invalid JSON with trailing syntax error
	badJSON := []byte(`{"theme": "dark", "mcpServers": { broken `)
	cfgPath := filepath.Join(tempHome, ".claude.json")
	_ = os.WriteFile(cfgPath, badJSON, 0600)

	_, err := ProvisionAgents(tempHome, []string{"claude-code"})
	if err == nil {
		t.Fatalf("Expected error when provisioning agent with malformed JSON, got nil")
	}

	// Verify original file was not overwritten or destroyed
	content, _ := os.ReadFile(cfgPath)
	if string(content) != string(badJSON) {
		t.Errorf("Expected bad JSON file to remain untouched, got: %s", string(content))
	}
}

func TestProvisioner_NullJSONHandling(t *testing.T) {
	tempHome := t.TempDir()
	cfgPath := filepath.Join(tempHome, ".claude.json")
	_ = os.WriteFile(cfgPath, []byte("null"), 0600)

	// Should not panic on null root
	_, err := ProvisionAgents(tempHome, []string{"claude-code"})
	if err != nil {
		t.Fatalf("Expected successful provision on null JSON, got: %v", err)
	}

	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		t.Fatalf("Expected valid JSON after null handling: %v", err)
	}
	if _, ok := root["mcpServers"]; !ok {
		t.Errorf("Expected mcpServers in parsed config")
	}
}
