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
	claudeDesktopDir := filepath.Dir(getClaudeDesktopConfigPath(tempHome))
	_ = os.MkdirAll(claudeDesktopDir, 0700)

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
		if !s.RulesInstalled {
			t.Errorf("Expected agent %s to have rules installed", s.Key)
		}
		if s.RulesVersion != version.Version {
			t.Errorf("Expected agent %s rules version to be %s, got %s", s.Key, version.Version, s.RulesVersion)
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

	// Verify Antigravity skills in both config and antigravity-cli
	agyConfigSkill := filepath.Join(tempHome, ".gemini", "config", "skills", "ackbar-tasks", "SKILL.md")
	if _, err := os.Stat(agyConfigSkill); err != nil {
		t.Errorf("Expected Antigravity config skill at %s: %v", agyConfigSkill, err)
	}
	agyCliSkill := filepath.Join(tempHome, ".gemini", "antigravity-cli", "skills", "ackbar-tasks", "SKILL.md")
	if _, err := os.Stat(agyCliSkill); err != nil {
		t.Errorf("Expected Antigravity CLI skill at %s: %v", agyCliSkill, err)
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

func TestClaudeDesktopProvisioning(t *testing.T) {
	tempHome := t.TempDir()

	// 1. Without Claude desktop directory, "all" skips claude-desktop
	statuses, err := ProvisionAgents(tempHome, []string{"all"})
	if err != nil {
		t.Fatalf("ProvisionAgents failed: %v", err)
	}
	var cdStatus *AgentStatus
	for i := range statuses {
		if statuses[i].Key == "claude-desktop" {
			cdStatus = &statuses[i]
		}
	}
	if cdStatus == nil {
		t.Fatalf("expected claude-desktop in statuses")
	}
	if cdStatus.Detected || cdStatus.MCPInstalled {
		t.Errorf("expected claude-desktop not detected or installed without parent directory")
	}

	// 2. Explicit provisioning of claude-desktop
	statuses, err = ProvisionAgents(tempHome, []string{"claude-desktop"})
	if err != nil {
		t.Fatalf("Explicit ProvisionAgents for claude-desktop failed: %v", err)
	}
	for i := range statuses {
		if statuses[i].Key == "claude-desktop" {
			cdStatus = &statuses[i]
		}
	}
	if cdStatus == nil || !cdStatus.MCPInstalled {
		t.Fatalf("expected claude-desktop to be installed after explicit provisioning")
	}

	// Verify config contents
	cfgPath := getClaudeDesktopConfigPath(tempHome)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read claude_desktop_config.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON in claude_desktop_config.json: %v", err)
	}
	mcpServers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcpServers map in claude desktop config")
	}
	if _, ok := mcpServers["ackbar"]; !ok {
		t.Errorf("expected ackbar in mcpServers of claude desktop config")
	}

	// 3. Test non-destructive upsert preserves other servers
	err = UpsertJSONMCPServer(cfgPath, "custom-tool", map[string]any{
		"command": "custom",
		"args":    []any{"run"},
	})
	if err != nil {
		t.Fatalf("UpsertJSONMCPServer failed: %v", err)
	}

	data2, _ := os.ReadFile(cfgPath)
	var root2 map[string]any
	_ = json.Unmarshal(data2, &root2)
	mcpServers2 := root2["mcpServers"].(map[string]any)
	if _, ok := mcpServers2["ackbar"]; !ok {
		t.Errorf("expected ackbar to remain in mcpServers")
	}
	if _, ok := mcpServers2["custom-tool"]; !ok {
		t.Errorf("expected custom-tool to be in mcpServers")
	}
}

func TestStampSkillVersion(t *testing.T) {
	// Case 1: version line exists
	raw1 := []byte("---\nname: test\nversion: \"old\"\n---\nBody")
	out1 := stampSkillVersion(raw1, "20261104.05")
	if !strings.Contains(string(out1), `version: "20261104.05"`) || strings.Contains(string(out1), "old") {
		t.Errorf("Case 1 failed, got:\n%s", string(out1))
	}

	// Case 2: argument-hint exists, no version
	raw2 := []byte("---\nname: test\nargument-hint: [foo]\n---\nBody")
	out2 := stampSkillVersion(raw2, "20261104.05")
	if !strings.Contains(string(out2), `version: "20261104.05"`) || !strings.Contains(string(out2), "argument-hint:") {
		t.Errorf("Case 2 failed, got:\n%s", string(out2))
	}

	// Case 3: standard frontmatter without version
	raw3 := []byte("---\nname: test\n---\nBody")
	out3 := stampSkillVersion(raw3, "20261104.05")
	if !strings.Contains(string(out3), `version: "20261104.05"`) {
		t.Errorf("Case 3 failed, got:\n%s", string(out3))
	}

	// Case 4: no frontmatter at all
	raw4 := []byte("# Just a header\nBody text")
	out4 := stampSkillVersion(raw4, "20261104.05")
	if !strings.Contains(string(out4), `version: "20261104.05"`) {
		t.Errorf("Case 4 failed, got:\n%s", string(out4))
	}
}

func TestProvisioner_RulesNonDestructiveUpsertAndUpgrade(t *testing.T) {
	tempHome := t.TempDir()

	// Pre-populate ~/.claude/CLAUDE.md with custom machine notes
	claudeDir := filepath.Join(tempHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}

	claudeMDPath := filepath.Join(claudeDir, "CLAUDE.md")
	userNotes := "# Machine notes — legion\nKeep X11 running.\n"
	if err := os.WriteFile(claudeMDPath, []byte(userNotes), 0644); err != nil {
		t.Fatalf("failed to write initial CLAUDE.md: %v", err)
	}

	// 1. Initial inspection: RulesInstalled == false and DriftDetected == true
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
	if claudeStatus.RulesInstalled {
		t.Errorf("Expected RulesInstalled == false initially")
	}
	if !claudeStatus.DriftDetected {
		t.Errorf("Expected DriftDetected == true initially")
	}

	// 2. Provision claude-code
	updatedStatuses, err := ProvisionAgents(tempHome, []string{"claude-code"})
	if err != nil {
		t.Fatalf("ProvisionAgents failed: %v", err)
	}

	for i := range updatedStatuses {
		if updatedStatuses[i].Key == "claude-code" {
			claudeStatus = &updatedStatuses[i]
		}
	}
	if !claudeStatus.RulesInstalled {
		t.Errorf("Expected RulesInstalled == true after provisioning")
	}
	if claudeStatus.RulesVersion != version.Version {
		t.Errorf("Expected RulesVersion == %s, got %s", version.Version, claudeStatus.RulesVersion)
	}
	if claudeStatus.DriftDetected {
		t.Errorf("Expected DriftDetected == false after provisioning")
	}

	// 3. Verify user notes preserved and ackbar rules present
	contentBytes, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("failed to read CLAUDE.md: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "# Machine notes — legion\nKeep X11 running.") {
		t.Errorf("Expected original machine notes to be preserved, got:\n%s", content)
	}
	expectedMarker := "<!-- ackbar-rules:v" + version.Version + " -->"
	if !strings.Contains(content, expectedMarker) {
		t.Errorf("Expected marker %s in CLAUDE.md, got:\n%s", expectedMarker, content)
	}
	if !strings.Contains(content, "<!-- /ackbar-rules -->") {
		t.Errorf("Expected closing marker in CLAUDE.md, got:\n%s", content)
	}

	// 4. Simulate older version by replacing marker
	oldContent := strings.Replace(content, expectedMarker, "<!-- ackbar-rules:v20250101.01 -->", 1)
	if err := os.WriteFile(claudeMDPath, []byte(oldContent), 0644); err != nil {
		t.Fatalf("failed to write simulated old CLAUDE.md: %v", err)
	}

	statuses, err = GetAgentStatuses(tempHome)
	if err != nil {
		t.Fatalf("GetAgentStatuses failed: %v", err)
	}
	for i := range statuses {
		if statuses[i].Key == "claude-code" {
			claudeStatus = &statuses[i]
		}
	}
	if !claudeStatus.RulesInstalled {
		t.Errorf("Expected RulesInstalled == true for old version")
	}
	if claudeStatus.RulesVersion != "20250101.01" {
		t.Errorf("Expected RulesVersion == 20250101.01, got %s", claudeStatus.RulesVersion)
	}
	if !claudeStatus.DriftDetected {
		t.Errorf("Expected DriftDetected == true for old version")
	}

	// 5. Re-run ProvisionAgents and verify in-place update without duplication
	updatedStatuses, err = ProvisionAgents(tempHome, []string{"claude-code"})
	if err != nil {
		t.Fatalf("Re-provisioning failed: %v", err)
	}

	for i := range updatedStatuses {
		if updatedStatuses[i].Key == "claude-code" {
			claudeStatus = &updatedStatuses[i]
		}
	}
	if !claudeStatus.RulesInstalled {
		t.Errorf("Expected RulesInstalled == true after re-provisioning")
	}
	if claudeStatus.RulesVersion != version.Version {
		t.Errorf("Expected RulesVersion == %s after upgrade, got %s", version.Version, claudeStatus.RulesVersion)
	}
	if claudeStatus.DriftDetected {
		t.Errorf("Expected DriftDetected == false after upgrade")
	}

	upgradedBytes, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("failed to read upgraded CLAUDE.md: %v", err)
	}
	upgradedContent := string(upgradedBytes)
	if !strings.Contains(upgradedContent, "# Machine notes — legion\nKeep X11 running.") {
		t.Errorf("Expected user machine notes to be preserved after upgrade")
	}
	if !strings.Contains(upgradedContent, expectedMarker) {
		t.Errorf("Expected current marker %s after upgrade", expectedMarker)
	}
	if strings.Contains(upgradedContent, "20250101.01") {
		t.Errorf("Expected old version tag to be replaced")
	}
	if strings.Count(upgradedContent, "<!-- /ackbar-rules -->") != 1 {
		t.Errorf("Expected exactly 1 closing marker block, found %d", strings.Count(upgradedContent, "<!-- /ackbar-rules -->"))
	}
}
