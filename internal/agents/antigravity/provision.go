package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/version"
)

func (p *Plugin) IsInstalled(homeDir string) bool {
	if _, err := agentutil.LookPathInStandardDirs("agy"); err == nil {
		return true
	}
	if _, err := agentutil.LookPathInStandardDirs("antigravity"); err == nil {
		return true
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return false
	}
	for _, d := range []string{
		filepath.Join(home, ".gemini", "antigravity"),
		filepath.Join(home, ".antigravity"),
		filepath.Join(home, ".gemini"),
	} {
		if _, err := os.Stat(d); err == nil {
			return true
		}
	}
	return false
}

func (p *Plugin) SkillDirs(homeDir string) []string {
	home := effectiveHome(homeDir)
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".gemini", "config", "skills"),
		filepath.Join(home, ".gemini", "antigravity-cli", "skills"),
	}
}

func (p *Plugin) CheckStatus(_ context.Context, homeDir string) agent.AgentStatus {
	home := effectiveHome(homeDir)
	homeSubdir := filepath.Join(home, ".gemini", "antigravity")
	configPath := filepath.Join(homeSubdir, "mcp_config.json")
	skillsDir := filepath.Join(home, ".gemini", "config", "skills")
	rulesFile := filepath.Join(home, ".gemini", "GEMINI.md")

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
		if _, err := exec.LookPath("agy"); err == nil {
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

	for _, sPath := range []string{
		filepath.Join(skillsDir, "ackbar-tasks", "SKILL.md"),
		filepath.Join(home, ".gemini", "antigravity-cli", "skills", "ackbar-tasks", "SKILL.md"),
	} {
		if data, err := os.ReadFile(sPath); err == nil {
			st.SkillInstalled = true
			st.SkillVersion = agentutil.ExtractSkillVersion(string(data))
			break
		}
	}

	for _, rPath := range []string{
		rulesFile,
		filepath.Join(home, ".gemini", "config", "GEMINI.md"),
	} {
		if data, err := os.ReadFile(rPath); err == nil {
			if ver := agentutil.ExtractRulesVersion(string(data)); ver != "" {
				st.RulesInstalled = true
				st.RulesVersion = ver
				break
			}
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

func (p *Plugin) Provision(_ context.Context, homeDir string) error {
	home := effectiveHome(homeDir)
	configPath := filepath.Join(home, ".gemini", "antigravity", "mcp_config.json")
	if err := agentutil.UpsertJSONMCPServer(configPath, "ackbar", map[string]any{
		"command": "ackbar",
		"args":    []string{"mcp"},
	}); err != nil {
		return fmt.Errorf("failed to configure MCP for %s: %w", p.DisplayName(), err)
	}

	for _, dir := range p.SkillDirs(home) {
		if err := agentutil.InstallEmbeddedSkill(filepath.Join(dir, "ackbar-tasks")); err != nil {
			return fmt.Errorf("failed to install skill for %s: %w", p.DisplayName(), err)
		}
	}

	for _, target := range []string{
		filepath.Join(home, ".gemini", "GEMINI.md"),
		filepath.Join(home, ".gemini", "config", "GEMINI.md"),
	} {
		if err := agentutil.UpsertMarkdownRulesBlock(target, version.Version); err != nil {
			return fmt.Errorf("failed to install rules for %s: %w", p.DisplayName(), err)
		}
	}
	return nil
}

func (*Plugin) InstallHooks(homeDir string) error {
	home := effectiveHome(homeDir)
	if home == "" {
		return fmt.Errorf("cannot resolve home directory")
	}
	return writeHooksFile(home, filepath.Join(home, ".gemini", "config", "hooks.json"))
}

func resolveHookCommand(home string) string {
	if home != "" {
		localHookBin := filepath.Join(home, ".local", "bin", "ackbar-hook")
		if st, err := os.Stat(localHookBin); err == nil && !st.IsDir() {
			return localHookBin + " --agent=antigravity"
		}
	}
	return "ackbar-hook --agent=antigravity"
}

func writeHooksFile(home, hookFile string) error {
	if err := os.MkdirAll(filepath.Dir(hookFile), 0o755); err != nil {
		return err
	}
	var existing map[string]interface{}
	if data, err := os.ReadFile(hookFile); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	if existing == nil {
		existing = make(map[string]interface{})
	}
	delete(existing, "hooks")

	hookCmd := resolveHookCommand(home)
	existing["ackbar"] = map[string]interface{}{
		"SessionStart":   []map[string]interface{}{{"type": "command", "command": hookCmd + " --event=SessionStart"}},
		"PreInvocation":  []map[string]interface{}{{"type": "command", "command": hookCmd + " --event=PreInvocation"}},
		"PostInvocation": []map[string]interface{}{{"type": "command", "command": hookCmd + " --event=PostInvocation"}},
		"Notification":   []map[string]interface{}{{"type": "command", "command": hookCmd + " --event=Notification"}},
		"Stop":           []map[string]interface{}{{"type": "command", "command": hookCmd + " --event=Stop"}},
		"PreToolUse": []map[string]interface{}{
			{"matcher": "*", "hooks": []map[string]interface{}{{"type": "command", "command": hookCmd + " --event=PreToolUse"}}},
		},
		"PostToolUse": []map[string]interface{}{
			{"matcher": "*", "hooks": []map[string]interface{}{{"type": "command", "command": hookCmd + " --event=PostToolUse"}}},
		},
	}
	updated, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return agentutil.WriteFileAtomic(hookFile, updated, 0o644)
}
