package grok

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

var ignoredSubcommands = map[string]bool{
	"login": true, "logout": true, "auth": true, "update": true, "upgrade": true, "models": true,
	"inspect": true, "doctor": true, "du": true, "disk-usage": true, "export": true, "mcp": true,
	"memory": true, "plugin": true, "sessions": true, "setup": true, "trace": true, "usage": true,
	"version": true, "v": true, "completions": true, "clone": true, "worktree": true, "leader": true,
	"worker": true, "agent": true,
}

func (*Plugin) SetupProfileDir(homeDir, name, configDir string) (string, error) {
	if configDir == "" {
		configDir = filepath.Join(homeDir, ".grok-profiles", name)
	} else if strings.HasPrefix(configDir, "~/") && homeDir != "" {
		configDir = filepath.Join(homeDir, configDir[2:])
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}
	if err := WriteHooksFile(configDir, name); err != nil {
		return configDir, fmt.Errorf("failed to write grok hooks file: %w", err)
	}
	if err := UpsertTOMLConfig(filepath.Join(configDir, "config.toml"), "ackbar"); err != nil {
		return configDir, fmt.Errorf("failed to upsert grok toml config: %w", err)
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
		envVars["GROK_HOME"] = cfgDir
	}
	return envVars
}

func (*Plugin) IsAccountLoggedIn(homeDir string, acct *agent.AgentAccount) bool {
	if acct != nil {
		if strings.TrimSpace(acct.Env["XAI_API_KEY"]) != "" {
			return true
		}
		cfgDir := acct.ConfigDir
		if cfgDir != "" {
			if strings.HasPrefix(cfgDir, "~/") && homeDir != "" {
				cfgDir = filepath.Join(homeDir, cfgDir[2:])
			}
		} else {
			cfgDir = HomeDir(homeDir)
		}
		return agentutil.FileExists(filepath.Join(cfgDir, "auth.json")) || strings.TrimSpace(os.Getenv("XAI_API_KEY")) != ""
	}
	return agentutil.FileExists(filepath.Join(HomeDir(homeDir), "auth.json")) || strings.TrimSpace(os.Getenv("XAI_API_KEY")) != ""
}

func (*Plugin) LoginCommand() string { return "grok login" }

func (*Plugin) IsValidNativeID(nativeID string) bool {
	return agentutil.IsSafeGrokSessionID(nativeID) && nativeID != "default"
}

func (*Plugin) IsIgnoredCommand(fullCmd string) bool {
	return agentutil.CheckIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "grok" || base == "grok-cli"
	}, ignoredSubcommands, nil)
}

func (*Plugin) MatchProcessCommand(binLower, cmdLower string) bool {
	return (binLower == "grok" || binLower == "grok-cli") && !agentutil.IsAckbarProcess(binLower, cmdLower)
}

func (*Plugin) ResolveSessionOwner(homeDir, sessionID string) (int, string) {
	if !agentutil.IsSafeGrokSessionID(sessionID) {
		return 0, ""
	}
	var candidatePaths []string
	candidatePaths = append(candidatePaths, filepath.Join(HomeDir(homeDir), "active_sessions.json"))
	userHome := homeDir
	if userHome == "" {
		userHome = agentutil.RealUserHome()
	}
	if entries, err := os.ReadDir(filepath.Join(userHome, ".grok-profiles")); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidatePaths = append(candidatePaths, filepath.Join(userHome, ".grok-profiles", e.Name(), "active_sessions.json"))
			}
		}
	}
	type activeSessionEntry struct {
		SessionID string `json:"session_id"`
		PID       int    `json:"pid"`
	}
	for _, cand := range candidatePaths {
		data, err := os.ReadFile(cand)
		if err != nil {
			continue
		}
		var list []activeSessionEntry
		if json.Unmarshal(data, &list) == nil {
			for _, entry := range list {
				if entry.SessionID == sessionID && entry.PID > 0 {
					return entry.PID, ""
				}
			}
		}
	}
	return 0, ""
}
