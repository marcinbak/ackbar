package daemon

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var grokIgnoredSubcommands = map[string]bool{
	"login": true, "logout": true, "auth": true, "update": true, "upgrade": true, "models": true,
	"inspect": true, "doctor": true, "du": true, "disk-usage": true, "export": true, "mcp": true,
	"memory": true, "plugin": true, "sessions": true, "setup": true, "trace": true, "usage": true,
	"version": true, "v": true, "completions": true, "clone": true, "worktree": true, "leader": true,
	"worker": true, "agent": true,
}

func realOSUserHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func GrokHomeDir(home string) string {
	realHome := realOSUserHome()
	if envHome := strings.TrimSpace(os.Getenv("GROK_HOME")); envHome != "" && (home == "" || filepath.Clean(home) == filepath.Clean(realHome)) {
		return envHome
	}
	if home == "" {
		home = realHome
	}
	return filepath.Join(home, ".grok")
}

func IsSafeGrokSessionID(id string) bool {
	if id == "" || len(id) > 128 || id == "." || strings.Contains(id, "..") {
		return false
	}
	first := id[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

func EncodeGrokCwd(cwd string) string {
	return url.PathEscape(filepath.Clean(cwd))
}

func FindGrokSessionDir(grokHome, cwd, nativeID string) string {
	if grokHome == "" || !IsSafeGrokSessionID(nativeID) {
		return ""
	}
	if cwd != "" {
		encoded := EncodeGrokCwd(cwd)
		if len(encoded) <= 255 {
			dir := filepath.Join(grokHome, "sessions", encoded, nativeID)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return dir
			}
		}
	}
	sessRoot := filepath.Join(grokHome, "sessions")
	entries, err := os.ReadDir(sessRoot)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				dir := filepath.Join(sessRoot, e.Name(), nativeID)
				if info, err := os.Stat(dir); err == nil && info.IsDir() {
					return dir
				}
			}
		}
	}
	flatDir := filepath.Join(sessRoot, nativeID)
	if info, err := os.Stat(flatDir); err == nil && info.IsDir() {
		return flatDir
	}
	return ""
}

func FindGrokSessionDirForHome(home, cwd, nativeID string) string {
	if !IsSafeGrokSessionID(nativeID) {
		return ""
	}
	if dir := FindGrokSessionDir(GrokHomeDir(home), cwd, nativeID); dir != "" {
		return dir
	}
	userHome := home
	if userHome == "" {
		userHome = realOSUserHome()
	}
	profilesDir := filepath.Join(userHome, ".grok-profiles")
	if entries, err := os.ReadDir(profilesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				if dir := FindGrokSessionDir(filepath.Join(profilesDir, e.Name()), cwd, nativeID); dir != "" {
					return dir
				}
			}
		}
	}
	return ""
}

type GrokAgentAdapter struct {
	defaultAgentAdapter
}

func (a GrokAgentAdapter) SetupProfileDir(home, name, configDir string) (string, error) {
	if configDir == "" {
		configDir = filepath.Join(home, ".grok-profiles", name)
	} else if strings.HasPrefix(configDir, "~/") && home != "" {
		configDir = filepath.Join(home, configDir[2:])
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}
	if err := WriteGrokHooksFile(configDir, name); err != nil {
		return configDir, fmt.Errorf("failed to write grok hooks file: %w", err)
	}
	if err := UpsertGrokTOMLConfig(filepath.Join(configDir, "config.toml"), "ackbar"); err != nil {
		return configDir, fmt.Errorf("failed to upsert grok toml config: %w", err)
	}
	return configDir, nil
}

func (a GrokAgentAdapter) AccountEnvVars(home string, acc *AgentAccount) map[string]string {
	if acc == nil {
		return nil
	}
	envVars := make(map[string]string)
	for k, v := range acc.Env {
		envVars[k] = v
	}
	if acc.Name != "" {
		envVars["ACKBAR_ACCOUNT"] = acc.Name
	}
	if acc.ConfigDir != "" {
		cfgDir := acc.ConfigDir
		if strings.HasPrefix(cfgDir, "~/") && home != "" {
			cfgDir = filepath.Join(home, cfgDir[2:])
		}
		envVars["GROK_HOME"] = cfgDir
	}
	return envVars
}

func (a GrokAgentAdapter) IsAccountLoggedIn(home string, acc *AgentAccount) bool {
	if acc != nil {
		if strings.TrimSpace(acc.Env["XAI_API_KEY"]) != "" {
			return true
		}
		cfgDir := acc.ConfigDir
		if cfgDir != "" {
			if strings.HasPrefix(cfgDir, "~/") && home != "" {
				cfgDir = filepath.Join(home, cfgDir[2:])
			}
		} else {
			cfgDir = GrokHomeDir(home)
		}
		return fileExists(filepath.Join(cfgDir, "auth.json")) || strings.TrimSpace(os.Getenv("XAI_API_KEY")) != ""
	}
	return fileExists(filepath.Join(GrokHomeDir(home), "auth.json")) || strings.TrimSpace(os.Getenv("XAI_API_KEY")) != ""
}

func (a GrokAgentAdapter) GetLoginCommand() string {
	return "grok login"
}

func (a GrokAgentAdapter) IsValidNativeID(nativeID string) bool {
	return IsSafeGrokSessionID(nativeID) && nativeID != "default"
}

func (a GrokAgentAdapter) IsIgnoredCommand(fullCmd string) bool {
	return checkIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "grok" || base == "grok-cli"
	}, grokIgnoredSubcommands, nil)
}

func (a GrokAgentAdapter) MatchProcessCommand(binLower, cmdLower string) bool {
	return (binLower == "grok" || binLower == "grok-cli") && !isAckbarInternalProcess(binLower, cmdLower)
}

func (a GrokAgentAdapter) ResolveSessionOwner(home, sessionID string) (int, string) {
	if !IsSafeGrokSessionID(sessionID) {
		return 0, ""
	}
	var candidatePaths []string
	candidatePaths = append(candidatePaths, filepath.Join(GrokHomeDir(home), "active_sessions.json"))
	userHome := home
	if userHome == "" {
		userHome = realOSUserHome()
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
		if err := json.Unmarshal(data, &list); err == nil {
			for _, entry := range list {
				if entry.SessionID == sessionID && entry.PID > 0 {
					return entry.PID, ""
				}
			}
		}
	}
	return 0, ""
}

func (a GrokAgentAdapter) HasConversationArtifacts(home, nativeID string) bool {
	if sessDir := FindGrokSessionDirForHome(home, "", nativeID); sessDir != "" {
		for _, f := range []string{"chat_history.jsonl", "updates.jsonl", "summary.json"} {
			if stat, err := os.Stat(filepath.Join(sessDir, f)); err == nil && stat.Size() > 0 {
				return true
			}
		}
	}
	userHome := home
	if userHome == "" {
		userHome = realOSUserHome()
	}
	for _, baseDir := range []string{GrokHomeDir(home), filepath.Join(userHome, ".grok")} {
		if stat, err := os.Stat(filepath.Join(baseDir, "sessions", nativeID+".jsonl")); err == nil && stat.Size() > 0 {
			return true
		}
		if stat, err := os.Stat(filepath.Join(baseDir, "sessions", nativeID, "transcript.jsonl")); err == nil && stat.Size() > 0 {
			return true
		}
	}
	return false
}

func stripXMLBlock(s, tag string) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	for {
		start := strings.Index(s, startTag)
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], endTag)
		if end == -1 {
			s = s[:start]
			break
		}
		s = s[:start] + s[start+end+len(endTag):]
	}
	return s
}

func (a GrokAgentAdapter) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	if raw != nil {
		if sr, ok := raw["synthetic_reason"].(string); ok && strings.TrimSpace(sr) != "" {
			return "", false
		}
		if t, _ := raw["type"].(string); t == "user" {
			if _, hasIdx := raw["prompt_index"]; !hasIdx &&
				!strings.Contains(content, "<user_query>") &&
				(strings.Contains(content, "<user_info>") || strings.Contains(content, "<system-reminder>")) {
				return "", false
			}
		}
	}
	if start := strings.Index(content, "<user_query>"); start != -1 {
		end := strings.LastIndex(content, "</user_query>")
		if end > start+len("<user_query>") {
			clean := strings.TrimSpace(content[start+len("<user_query>") : end])
			return clean, clean != ""
		}
	}
	clean := stripXMLBlock(content, "user_info")
	if raw != nil {
		clean = stripXMLBlock(clean, "system-reminder")
	}
	clean = cleanCLIEnvironmentTags(clean)
	trimmed := strings.TrimSpace(clean)
	return trimmed, trimmed != ""
}
