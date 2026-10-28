package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ClaudeAgentAdapter implements AgentAdapter for Claude Code.
type ClaudeAgentAdapter struct{}

func (a ClaudeAgentAdapter) GetResetCommand() string {
	return "/clear"
}

func (a ClaudeAgentAdapter) GetApprovalKeys(allow bool) []string {
	if allow {
		return []string{"y", "Enter"}
	}
	return []string{"n", "Enter"}
}

func (a ClaudeAgentAdapter) AccountEnvVars(home string, acc *AgentAccount) map[string]string {
	if acc == nil {
		return nil
	}
	envVars := make(map[string]string)
	if acc.ConfigDir != "" {
		cfgDir := acc.ConfigDir
		if strings.HasPrefix(cfgDir, "~/") && home != "" {
			cfgDir = filepath.Join(home, cfgDir[2:])
		}
		envVars["CLAUDE_CONFIG_DIR"] = cfgDir
	}
	for k, v := range acc.Env {
		envVars[k] = v
	}
	if acc.Name != "" {
		envVars["ACKBAR_ACCOUNT"] = acc.Name
	}
	return envVars
}

func (a ClaudeAgentAdapter) IsAccountLoggedIn(home string, acc *AgentAccount) bool {
	if acc == nil {
		return false
	}
	if acc.Env != nil && acc.Env["ANTHROPIC_API_KEY"] != "" {
		return true
	}
	if acc.ConfigDir != "" {
		cfgDir := acc.ConfigDir
		if strings.HasPrefix(cfgDir, "~/") && home != "" {
			cfgDir = filepath.Join(home, cfgDir[2:])
		}
		if data, err := os.ReadFile(filepath.Join(cfgDir, ".claude.json")); err == nil {
			if strings.Contains(string(data), "oauthAccount") {
				return true
			}
		}
		if _, err := os.Stat(filepath.Join(cfgDir, "settings.json")); err == nil {
			return true
		}
	}
	if acc.Name == "default" || acc.ConfigDir == "" {
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			return true
		}
		if home != "" {
			if data, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil {
				if strings.Contains(string(data), "oauthAccount") {
					return true
				}
			}
			if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
				return true
			}
		}
	}
	return false
}

func (a ClaudeAgentAdapter) SetupProfileDir(home, name, configDir string) (string, error) {
	if configDir == "" {
		configDir = filepath.Join(home, ".claude-profiles", name)
	} else if strings.HasPrefix(configDir, "~/") && home != "" {
		configDir = filepath.Join(home, configDir[2:])
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}

	settingsPath := filepath.Join(configDir, "settings.json")
	var settings map[string]interface{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if settings == nil {
		settings = make(map[string]interface{})
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}
	hookURL := fmt.Sprintf("http://127.0.0.1:7777/v1/hooks/claude-code?account=%s", name)
	correctHookEntry := map[string]interface{}{
		"matcher": "",
		"hooks": []map[string]string{
			{
				"type": "http",
				"url":  hookURL,
			},
		},
	}
	eventKeys := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Notification", "Stop"}
	for _, evtKey := range eventKeys {
		existingList, _ := hooks[evtKey].([]interface{})
		var validList []interface{}
		hasHook := false
		for _, item := range existingList {
			if m, ok := item.(map[string]interface{}); ok {
				if innerHooks, ok := m["hooks"].([]interface{}); ok {
					for _, h := range innerHooks {
						if hm, ok := h.(map[string]interface{}); ok {
							if hm["url"] == hookURL {
								hasHook = true
							}
						}
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
	if out, err := json.MarshalIndent(settings, "", "  "); err == nil {
		_ = os.WriteFile(settingsPath, out, 0644)
	}
	return configDir, nil
}

func (a ClaudeAgentAdapter) GetLoginCommand() string {
	return "claude login"
}

func (a ClaudeAgentAdapter) IsValidNativeID(nativeID string) bool {
	return IsUUID(nativeID)
}

var claudeIgnoredSubcommands = map[string]bool{
	"mcp": true, "doctor": true, "update": true, "upgrade": true,
	"auth": true, "login": true, "logout": true, "install": true,
	"plugin": true, "plugins": true, "project": true, "auto-mode": true,
	"gateway": true, "import": true, "logs": true, "respawn": true,
	"rm": true, "setup-token": true, "stop": true, "kill": true,
	"ultrareview": true, "agents": true, "attach": true,
}

func (a ClaudeAgentAdapter) IsIgnoredCommand(fullCmd string) bool {
	return checkIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "claude" || strings.HasPrefix(base, "claude") || strings.HasSuffix(field, "/claude.js") || strings.Contains(field, "claude-code")
	}, claudeIgnoredSubcommands, nil)
}

func (a ClaudeAgentAdapter) MatchProcessCommand(binLower, cmdLower string) bool {
	if binLower == "claude" || ((binLower == "node" || binLower == "bun") && (strings.Contains(cmdLower, "@anthropic-ai/claude-code") || strings.Contains(cmdLower, "claude-code") || strings.Contains(cmdLower, "/claude.js") || strings.Contains(cmdLower, "bin/claude"))) {
		if !strings.Contains(cmdLower, "ackbar") && !strings.Contains(cmdLower, "jest-worker") && !strings.Contains(cmdLower, "react-native") && !strings.Contains(cmdLower, "yarn") {
			return true
		}
	}
	return false
}

func (a ClaudeAgentAdapter) RequiresResolvedSessionID() bool {
	return true
}

func (a ClaudeAgentAdapter) ResolveSessionForPID(ctx context.Context, home string, pid int, cmdFields []string) string {
	var sID string
	if home != "" {
		sID, _ = findClaudeSessionForPID(home, pid)
	}
	if sID == "" {
		for i, arg := range cmdFields {
			if (arg == "--session-id" || arg == "-s" || arg == "--resume" || arg == "-r") && i+1 < len(cmdFields) {
				cID := strings.TrimSpace(cmdFields[i+1])
				if IsUUID(cID) {
					sID = cID
					break
				}
			}
		}
	}
	if sID != "" && IsUUID(sID) {
		return sID
	}
	return ""
}

func (a ClaudeAgentAdapter) ResolveSessionOwner(home, sessionID string) (pid int, tmuxName string) {
	return findClaudeSessionOwner(home, sessionID)
}

func (a ClaudeAgentAdapter) HasConversationArtifacts(home, nativeID string) bool {
	return false
}

func (a ClaudeAgentAdapter) IsSubagentSession(home, nativeID string, precomputed map[string]bool) bool {
	return strings.HasPrefix(nativeID, "agent-")
}

func (a ClaudeAgentAdapter) ListWorkspaceDocuments(home, cwd, nativeID string, addDoc func(fullPath, title, relPath, category, categoryLabel string, priority int)) {
	if cwd == "" {
		return
	}
	claudeDir := filepath.Join(cwd, ".claude")
	if dirExists(claudeDir) {
		_ = filepath.WalkDir(claudeDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if !d.IsDir() {
				nameLower := strings.ToLower(d.Name())
				if strings.HasSuffix(nameLower, ".md") {
					rel, _ := filepath.Rel(cwd, p)
					cat, label, prio := classifyDoc(d.Name())
					if cat == "other" {
						cat, label = "plan", "Claude Plan"
					}
					addDoc(p, d.Name(), rel, cat, label, prio)
				}
			}
			return nil
		})
	}
}

func (a ClaudeAgentAdapter) ScanDiskSessions(ctx context.Context, s *Server, home string, state *ScanState) {
	if home == "" {
		return
	}
	hostName := state.hostName
	claudeProjectsDir := filepath.Join(home, ".claude", "projects")
	projDirs, err := os.ReadDir(claudeProjectsDir)
	if err != nil {
		return
	}
	for _, pDir := range projDirs {
		if !pDir.IsDir() {
			continue
		}
		projPath := filepath.Join(claudeProjectsDir, pDir.Name())
		files, err := os.ReadDir(projPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".jsonl") || strings.HasPrefix(f.Name(), "agent-") {
				continue
			}
			sessionUUID := strings.TrimSuffix(f.Name(), ".jsonl")
			sessID := fmt.Sprintf("claude-code:%s:%s", hostName, sessionUUID)
			if s.db.IsSessionDeleted(sessID) || s.db.IsSessionDeleted(sessionUUID) {
				continue
			}
			// Skip expensive disk reads if session is already known in DB
			if state.knownIDs[sessID] != nil || state.knownByNativeID[sessionUUID] != nil {
				continue
			}

			baseDir, wtDir := decodeClaudeProjectDirInfo(pDir.Name())
			cwd := baseDir
			if cwd == "" && wtDir != "" {
				cwd = wtDir
			}

			meta := ReadClaudeSessionMeta(cwd, sessionUUID)
			title := ""
			if meta != nil {
				if meta.CustomTitle != "" {
					title = meta.CustomTitle
				} else if meta.AITitle != "" {
					title = meta.AITitle
				} else if meta.FirstPrompt != "" && !strings.HasPrefix(meta.FirstPrompt, "/") && meta.FirstPrompt != "config" && meta.FirstPrompt != "claude" {
					title = truncateTitle(meta.FirstPrompt)
				} else if meta.Title != "" && !strings.HasPrefix(meta.Title, "/") && meta.Title != "config" && meta.Title != "claude" {
					title = meta.Title
				}
			}
			if title == "" {
				rawTitle := ReadClaudeSessionTitle(cwd, sessionUUID)
				if rawTitle != "" && !strings.HasPrefix(rawTitle, "/") && rawTitle != "config" && rawTitle != "claude" {
					title = rawTitle
				}
			}
			if title == "" {
				if meta != nil && meta.LastPrompt != "" && !strings.HasPrefix(meta.LastPrompt, "/") {
					title = truncateTitle(meta.LastPrompt)
				}
			}
			if title == "" {
				title = fmt.Sprintf("Claude Code (%s)", sessionUUID[:8])
			}

			stat, _ := f.Info()
			modTime := time.Now()
			isOld := false
			if stat != nil {
				modTime = stat.ModTime()
			}

			lastEvent := modTime
			if meta != nil && !meta.LastMessageAt.IsZero() {
				lastEvent = meta.LastMessageAt
			}
			if time.Since(lastEvent) > 7*24*time.Hour {
				isOld = true
			}

			branch := ""
			if meta != nil && meta.GitBranch != "" {
				branch = meta.GitBranch
			} else if wtDir != "" {
				branch = ResolveGitBranch(wtDir)
			} else if cwd != "" {
				branch = ResolveGitBranch(cwd)
			}

			newSess := &Session{
				ID:          sessID,
				Name:        title,
				Agent:       "claude-code",
				Host:        hostName,
				NativeID:    sessionUUID,
				Cwd:         cwd,
				ProjectKey:  GetProjectKey(cwd),
				NodePath:    s.resolveSessionNodePath(cwd),
				State:       StateEnded,
				Managed:     false,
				Activity:    "Session ended",
				StartedAt:   modTime,
				LastEventAt: lastEvent,
				ContextPct:  ReadClaudeContextUsage(cwd, sessionUUID),
				GitBranch:   branch,
				Archived:    isOld,
			}
			if meta != nil {
				newSess.Entrypoint = meta.Entrypoint
				newSess.Kind = meta.Kind
				newSess.Version = meta.Version
				newSess.CustomTitle = meta.CustomTitle
				newSess.AITitle = meta.AITitle
				newSess.AIDescription = meta.AIDescription
				newSess.FirstPrompt = meta.FirstPrompt
				newSess.LastPrompt = meta.LastPrompt
			}
			_ = s.db.SaveSession(newSess)
			state.knownIDs[sessID] = newSess
			s.broadcast(newSess)
		}
	}
}

func decodeClaudeProjectDirInfo(raw string) (baseRepoDir string, worktreeDir string) {
	if strings.HasPrefix(raw, "-") {
		raw = raw[1:]
	}

	// Handle explicit worktree split patterns (e.g. "--claude-worktrees-", "--worktrees-", "-claude-worktrees-")
	wtTokens := []struct {
		token string
		sub   []string
	}{
		{"--claude-worktrees-", []string{".claude", "worktrees"}},
		{"-claude-worktrees-", []string{".claude", "worktrees"}},
		{"--worktrees-", []string{".worktrees"}},
		{"-worktrees-", []string{".worktrees"}},
	}
	for _, wt := range wtTokens {
		if idx := strings.Index(raw, wt.token); idx != -1 {
			prefix := raw[:idx]
			wtName := raw[idx+len(wt.token):]
			parent := decodeClaudeProjectDir(prefix)
			parts := append([]string{parent}, wt.sub...)
			parts = append(parts, wtName)
			return parent, filepath.Join(parts...)
		}
	}

	decoded := decodeClaudeProjectDir(raw)
	return decoded, ""
}

func decodeClaudeProjectDir(raw string) string {
	if strings.HasPrefix(raw, "-") {
		raw = raw[1:]
	}

	// Handle explicit worktree split patterns (e.g. "--claude-worktrees-", "--worktrees-", "-claude-worktrees-")
	wtTokens := []struct {
		token string
		sub   []string
	}{
		{"--claude-worktrees-", []string{".claude", "worktrees"}},
		{"-claude-worktrees-", []string{".claude", "worktrees"}},
		{"--worktrees-", []string{".worktrees"}},
		{"-worktrees-", []string{".worktrees"}},
	}
	for _, wt := range wtTokens {
		if idx := strings.Index(raw, wt.token); idx != -1 {
			prefix := raw[:idx]
			wtName := raw[idx+len(wt.token):]
			parent := decodeClaudeProjectDir(prefix)
			parts := append([]string{parent}, wt.sub...)
			parts = append(parts, wtName)
			return filepath.Join(parts...)
		}
	}

	current := "/"
	remaining := raw
	for remaining != "" {
		if !dirExists(current) {
			break
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			break
		}

		var matchedEntry string
		var matchedLen int

		for _, e := range entries {
			eName := e.Name()
			cleanName := strings.TrimPrefix(eName, ".")
			if strings.HasPrefix(remaining, cleanName) {
				if len(cleanName) > matchedLen {
					matchedEntry = eName
					matchedLen = len(cleanName)
				}
			}
		}

		if matchedEntry != "" {
			current = filepath.Join(current, matchedEntry)
			remaining = strings.TrimPrefix(strings.TrimPrefix(remaining[matchedLen:], "-"), "-")
		} else {
			parts := strings.SplitN(remaining, "-", 2)
			candidate := filepath.Join(current, parts[0])
			hiddenCandidate := filepath.Join(current, "."+parts[0])
			if dirExists(hiddenCandidate) {
				current = hiddenCandidate
			} else {
				current = candidate
			}
			if len(parts) > 1 {
				remaining = parts[1]
			} else {
				remaining = ""
			}
		}
	}
	return current
}

func findClaudeSessionOwner(home, sessionID string) (pid int, tmuxName string) {
	if home == "" || sessionID == "" {
		return 0, ""
	}
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0, ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
			if err != nil {
				continue
			}
			var meta struct {
				PID       int    `json:"pid"`
				SessionID string `json:"sessionId"`
				Tmux      string `json:"tmux"`
			}
			if err := json.Unmarshal(data, &meta); err == nil && meta.SessionID == sessionID {
				tName := meta.Tmux
				if idx := strings.Index(tName, ":"); idx != -1 {
					tName = tName[:idx]
				}
				p := meta.PID
				if p == 0 {
					pStr := strings.TrimSuffix(e.Name(), ".json")
					p, _ = strconv.Atoi(pStr)
				}
				return p, tName
			}
		}
	}
	return 0, ""
}

func findClaudeSessionForPID(home string, pid int) (sessionID, name string) {
	if home == "" || pid <= 0 {
		return "", ""
	}
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	// 1. Direct PID match
	metaFile := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", pid))
	if data, err := os.ReadFile(metaFile); err == nil {
		var meta struct {
			SessionID string `json:"sessionId"`
			Name      string `json:"name"`
		}
		if err := json.Unmarshal(data, &meta); err == nil && meta.SessionID != "" {
			return meta.SessionID, meta.Name
		}
	}
	// 2. Child PID match via pgrep -P
	if out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output(); err == nil {
		for _, cpStr := range strings.Fields(string(out)) {
			if cp, err := strconv.Atoi(cpStr); err == nil && cp > 0 {
				cMetaFile := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", cp))
				if data, err := os.ReadFile(cMetaFile); err == nil {
					var meta struct {
						SessionID string `json:"sessionId"`
						Name      string `json:"name"`
					}
					if err := json.Unmarshal(data, &meta); err == nil && meta.SessionID != "" {
						return meta.SessionID, meta.Name
					}
				}
			}
		}
	}
	return "", ""
}

func getModelContextLimit(modelName string) int {
	modelLower := strings.ToLower(modelName)
	if strings.Contains(modelLower, "fable") || strings.Contains(modelLower, "opus") || strings.Contains(modelLower, "1m") || strings.Contains(modelLower, "gemini") || strings.Contains(modelLower, "sonnet-4") || strings.Contains(modelLower, "sonnet-5") || strings.Contains(modelLower, "claude-4") || strings.Contains(modelLower, "claude-5") {
		return 1000000
	}
	if strings.Contains(modelLower, "gpt-4") || strings.Contains(modelLower, "codex") {
		return 128000
	}
	return 200000
}

func ReadClaudeContextUsage(cwd, sessionID string) int {
	home, err := os.UserHomeDir()
	if err != nil || cwd == "" {
		return 0
	}

	targetID := sessionID
	if strings.HasPrefix(sessionID, "proc-") {
		targetID = ""
	}

	encodedCwd := encodeClaudeProjectDir(cwd)
	projDir := filepath.Join(home, ".claude", "projects", encodedCwd)

	files, err := os.ReadDir(projDir)
	if err != nil {
		return 0
	}

	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			if targetID == "" || strings.Contains(f.Name(), targetID) {
				filePath := filepath.Join(projDir, f.Name())
				file, err := os.Open(filePath)
				if err != nil {
					continue
				}

				stat, err := file.Stat()
				if err != nil || stat.Size() == 0 {
					file.Close()
					continue
				}

				offset := stat.Size() - 65536
				if offset < 0 {
					offset = 0
				}
				_, _ = file.Seek(offset, 0)
				buf, _ := io.ReadAll(file)
				file.Close()

				lines := strings.Split(string(buf), "\n")
				for i := len(lines) - 1; i >= 0; i-- {
					line := strings.TrimSpace(lines[i])
					if line == "" {
						continue
					}
					if strings.Contains(line, `"usage"`) {
						var wrapper struct {
							Message *struct {
								Model string `json:"model"`
								Usage *struct {
									InputTokens              int `json:"input_tokens"`
									CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
									CacheReadInputTokens     int `json:"cache_read_input_tokens"`
								} `json:"usage"`
							} `json:"message"`
						}
						if err := json.Unmarshal([]byte(line), &wrapper); err == nil && wrapper.Message != nil && wrapper.Message.Usage != nil {
							u := wrapper.Message.Usage
							totalTokens := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
							if totalTokens > 0 {
								limit := getModelContextLimit(wrapper.Message.Model)
								pct := (totalTokens * 100) / limit
								if pct > 100 {
									pct = 100
								}
								return pct
							}
						}
					}
				}
			}
		}
	}

	return 0
}

func ReadClaudeSessionMeta(cwd, sessionID string) *SessionMeta {
	title := ReadClaudeSessionTitle(cwd, sessionID)
	metaInfo := &SessionMeta{
		Title:      title,
		ContextPct: ReadClaudeContextUsage(cwd, sessionID),
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return metaInfo
	}

	targetID := sessionID
	var targetPID int
	if strings.HasPrefix(sessionID, "proc-") {
		pidStr := strings.TrimPrefix(sessionID, "proc-")
		targetPID, _ = strconv.Atoi(pidStr)
		targetID = ""
	}

	// 1. Resolve metadata and sessionId from ~/.claude/sessions/ if PID is known or targetID is provided
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if files, err := os.ReadDir(sessionsDir); err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				metaPath := filepath.Join(sessionsDir, f.Name())
				if data, err := os.ReadFile(metaPath); err == nil {
					var meta struct {
						PID        int    `json:"pid"`
						SessionID  string `json:"sessionId"`
						Name       string `json:"name"`
						NameSource string `json:"nameSource"`
						Entrypoint string `json:"entrypoint"`
						Kind       string `json:"kind"`
						Version    string `json:"version"`
						Cwd        string `json:"cwd"`
					}
					if err := json.Unmarshal(data, &meta); err == nil {
						if (targetPID > 0 && meta.PID == targetPID) || (targetID != "" && meta.SessionID == targetID) {
							if meta.Name != "" && meta.NameSource == "custom" && metaInfo.CustomTitle == "" {
								metaInfo.CustomTitle = meta.Name
							}
							if meta.Entrypoint != "" {
								metaInfo.Entrypoint = meta.Entrypoint
							}
							if meta.Kind != "" {
								metaInfo.Kind = meta.Kind
							}
							if meta.Version != "" {
								metaInfo.Version = meta.Version
							}
							if targetID == "" && meta.SessionID != "" {
								targetID = meta.SessionID
							}
							break
						}
					}
				}
			}
		}
	}

	// 2. Scan transcript file for prompts, summaries, and titles
	encodedCwd := encodeClaudeProjectDir(cwd)
	var targetFiles []string
	claudeProjectsDir := filepath.Join(home, ".claude", "projects")

	if encodedCwd != "" && encodedCwd != "-" {
		projDir := filepath.Join(claudeProjectsDir, encodedCwd)
		if targetID != "" {
			targetFiles = append(targetFiles, filepath.Join(projDir, targetID+".jsonl"))
		}
	}

	if len(targetFiles) == 0 || !fileExists(targetFiles[0]) {
		if pDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
			for _, pDir := range pDirs {
				if pDir.IsDir() {
					projPath := filepath.Join(claudeProjectsDir, pDir.Name())
					if targetID != "" {
						tf := filepath.Join(projPath, targetID+".jsonl")
						if fileExists(tf) {
							targetFiles = append(targetFiles, tf)
							break
						}
					}
				}
			}
		}
	}

	for _, filePath := range targetFiles {
		if data, rerr := readTail(filePath, 64*1024); rerr == nil && len(data) > 0 {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var obj struct {
					Type    string `json:"type"`
					Message struct {
						Role    string `json:"role"`
						Content any    `json:"content"`
					} `json:"message"`
					CustomTitle string `json:"customTitle"`
					Title       string `json:"title"`
					AITitle     string `json:"aiTitle"`
					Summary     string `json:"summary"`
					Cwd         string `json:"cwd"`
					GitBranch   string `json:"gitBranch"`
					LastPrompt  string `json:"lastPrompt"`
					Prompt      string `json:"prompt"`
					Timestamp   string `json:"timestamp"`
				}
				if jerr := json.Unmarshal([]byte(line), &obj); jerr == nil {
					if obj.Timestamp != "" {
						if t, terr := time.Parse(time.RFC3339Nano, obj.Timestamp); terr == nil {
							if metaInfo.LastMessageAt.IsZero() || t.After(metaInfo.LastMessageAt) {
								metaInfo.LastMessageAt = t
							}
						} else if t, terr := time.Parse(time.RFC3339, obj.Timestamp); terr == nil {
							if metaInfo.LastMessageAt.IsZero() || t.After(metaInfo.LastMessageAt) {
								metaInfo.LastMessageAt = t
							}
						}
					}
					if obj.GitBranch != "" && metaInfo.GitBranch == "" {
						metaInfo.GitBranch = obj.GitBranch
					}
					if obj.CustomTitle != "" {
						metaInfo.CustomTitle = obj.CustomTitle
					}
					if obj.AITitle != "" {
						metaInfo.AITitle = obj.AITitle
					}
					if obj.Title != "" && metaInfo.AITitle == "" {
						metaInfo.AITitle = obj.Title
					}
					if obj.Summary != "" {
						metaInfo.AIDescription = obj.Summary
					}

					promptText := ""
					if obj.LastPrompt != "" {
						promptText = obj.LastPrompt
					} else if obj.Prompt != "" {
						promptText = obj.Prompt
					} else if obj.Type == "user" || obj.Message.Role == "user" {
						if s, ok := obj.Message.Content.(string); ok {
							promptText = s
						} else if arr, ok := obj.Message.Content.([]any); ok && len(arr) > 0 {
							for _, itm := range arr {
								if firstObj, ok := itm.(map[string]any); ok {
									if text, ok := firstObj["text"].(string); ok && text != "" {
										promptText = text
										break
									}
								}
							}
						}
					}

					if promptText != "" {
						clean := cleanPromptText(promptText)
						if clean != "" {
							if metaInfo.FirstPrompt == "" {
								metaInfo.FirstPrompt = clean
							}
							metaInfo.LastPrompt = clean
						}
					}
				}
			}
			break
		}
	}

	return metaInfo
}

func ReadClaudeSessionTitle(cwd, sessionID string) string {
	cacheKey := fmt.Sprintf("%s:%s", cwd, sessionID)
	titleCacheMutex.RLock()
	if cached, ok := titleCache[cacheKey]; ok && cached.Title != "" {
		if cached.Source == "custom" {
			titleCacheMutex.RUnlock()
			return cached.Title
		}
	}
	titleCacheMutex.RUnlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	targetID := sessionID
	var targetPID int
	if strings.HasPrefix(sessionID, "proc-") {
		pidStr := strings.TrimPrefix(sessionID, "proc-")
		targetPID, _ = strconv.Atoi(pidStr)
		targetID = ""
	}

	var title string
	var source string = "fallback"
	var derivedTitle string

	// 1. Check ~/.claude/sessions/*.json session registry files strictly for exact PID or SessionID match
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if files, err := os.ReadDir(sessionsDir); err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				metaPath := filepath.Join(sessionsDir, f.Name())
				if data, err := os.ReadFile(metaPath); err == nil {
					var meta struct {
						PID        int    `json:"pid"`
						SessionID  string `json:"sessionId"`
						Name       string `json:"name"`
						NameSource string `json:"nameSource"`
					}
					if err := json.Unmarshal(data, &meta); err == nil {
						if (targetPID > 0 && meta.PID == targetPID) || (targetID != "" && meta.SessionID == targetID) {
							if meta.Name != "" && meta.NameSource == "custom" {
								title = meta.Name
								source = "custom"
							} else if meta.Name != "" && derivedTitle == "" {
								derivedTitle = meta.Name
							}
							if targetID == "" && meta.SessionID != "" {
								targetID = meta.SessionID
							}
							break
						}
					}
				}
			}
		}
	}

	if title == "" && targetID != "" {
		var firstPrompt string
		var aiTitle string

		// 2. Check ~/.claude/projects/ strictly for targetID transcript
		claudeProjectsDir := filepath.Join(home, ".claude", "projects")
		var targetFiles []string
		encodedCwd := encodeClaudeProjectDir(cwd)
		if encodedCwd != "" && encodedCwd != "-" {
			projDir := filepath.Join(claudeProjectsDir, encodedCwd)
			tf := filepath.Join(projDir, targetID+".jsonl")
			if fileExists(tf) {
				targetFiles = append(targetFiles, tf)
			}
		}
		if len(targetFiles) == 0 {
			if pDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
				for _, pDir := range pDirs {
					if pDir.IsDir() {
						tf := filepath.Join(claudeProjectsDir, pDir.Name(), targetID+".jsonl")
						if fileExists(tf) {
							targetFiles = append(targetFiles, tf)
							break
						}
					}
				}
			}
		}

		for _, filePath := range targetFiles {
			file, err := os.Open(filePath)
			if err == nil {
				stat, _ := file.Stat()
				fileSize := stat.Size()

				// Tail read (last 64KB) for customTitle, rename, or aiTitle
				if fileSize > 0 {
					offset := fileSize - 65536
					if offset < 0 {
						offset = 0
					}
					_, _ = file.Seek(offset, 0)
					tailBuf, _ := io.ReadAll(file)
					tailLines := strings.Split(string(tailBuf), "\n")
					for i := len(tailLines) - 1; i >= 0; i-- {
						line := strings.TrimSpace(tailLines[i])
						if line == "" {
							continue
						}
						var entry struct {
							Type        string `json:"type"`
							CustomTitle string `json:"customTitle"`
							AgentName   string `json:"agentName"`
							AITitle     string `json:"aiTitle"`
							Title       string `json:"title"`
							Name        string `json:"name"`
						}
						if err := json.Unmarshal([]byte(line), &entry); err == nil {
							if entry.CustomTitle != "" {
								title = entry.CustomTitle
								source = "custom"
								break
							}
							if entry.AgentName != "" {
								title = entry.AgentName
								source = "custom"
								break
							}
							if entry.Title != "" && title == "" {
								title = entry.Title
								source = "ai"
							}
							if entry.AITitle != "" && aiTitle == "" {
								aiTitle = entry.AITitle
							}
							if entry.Type == "rename" && entry.Name != "" {
								title = entry.Name
								source = "custom"
								break
							}
						}
					}
				}

				// Head read (first 64KB) for prompt or ai-title if no title
				if title == "" && fileSize > 0 {
					_, _ = file.Seek(0, 0)
					buf := make([]byte, 65536)
					n, _ := io.ReadFull(file, buf)
					jsonLines := strings.Split(string(buf[:n]), "\n")
					for i := 0; i < len(jsonLines); i++ {
						line := strings.TrimSpace(jsonLines[i])
						if line == "" {
							continue
						}
						var entry struct {
							Type    string `json:"type"`
							Message struct {
								Role    string `json:"role"`
								Content any    `json:"content"`
							} `json:"message"`
							AITitle     string `json:"aiTitle"`
							Title       string `json:"title"`
							CustomTitle string `json:"customTitle"`
							LastPrompt  string `json:"lastPrompt"`
							Prompt      string `json:"prompt"`
						}
						if err := json.Unmarshal([]byte(line), &entry); err == nil {
							if entry.CustomTitle != "" && title == "" {
								title = entry.CustomTitle
								source = "custom"
							}
							if entry.AITitle != "" && aiTitle == "" {
								aiTitle = entry.AITitle
							}
							if entry.Title != "" && aiTitle == "" {
								aiTitle = entry.Title
							}
							pText := entry.LastPrompt
							if pText == "" {
								pText = entry.Prompt
							}
							if pText == "" && (entry.Type == "user" || entry.Message.Role == "user") {
								if s, ok := entry.Message.Content.(string); ok {
									pText = s
								}
							}
							if pText != "" && firstPrompt == "" {
								firstPrompt = cleanPromptText(pText)
							}
						}
					}
				}
				file.Close()
			}
			if title != "" {
				break
			}
		}

		if title == "" && aiTitle != "" {
			title = aiTitle
			source = "ai"
		}
		if title == "" && firstPrompt != "" {
			title = truncateTitle(firstPrompt)
			source = "prompt"
		}
		if title == "" && derivedTitle != "" {
			title = derivedTitle
			source = "fallback"
		}
	}

	// 3. Fallback to history.jsonl strictly matching targetID
	if title == "" && targetID != "" {
		historyPath := filepath.Join(home, ".claude", "history.jsonl")
		if data, err := os.ReadFile(historyPath); err == nil {
			lines := strings.Split(string(data), "\n")
			for i := len(lines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(lines[i])
				if line == "" {
					continue
				}
				var entry struct {
					SessionID    string `json:"sessionId"`
					SessionIDOld string `json:"session_id"`
					DisplayName  string `json:"displayName"`
					CustomTitle  string `json:"customTitle"`
					Title        string `json:"title"`
					Display      string `json:"display"`
					Prompt       string `json:"prompt"`
				}
				if err := json.Unmarshal([]byte(line), &entry); err == nil {
					sID := entry.SessionID
					if sID == "" {
						sID = entry.SessionIDOld
					}
					if sID == targetID {
						if entry.DisplayName != "" {
							title = entry.DisplayName
							source = "custom"
							break
						}
						if entry.CustomTitle != "" {
							title = entry.CustomTitle
							source = "custom"
							break
						}
						if entry.Title != "" {
							title = entry.Title
							source = "ai"
							break
						}
						p := entry.Display
						if p == "" {
							p = entry.Prompt
						}
						if p != "" {
							title = truncateTitle(cleanPromptText(p))
							source = "prompt"
							break
						}
					}
				}
			}
		}
	}

	if title == "" && targetID != "" && len(targetID) >= 8 {
		title = fmt.Sprintf("Claude Code (%s)", targetID[:8])
	}

	if title != "" {
		titleCacheMutex.Lock()
		titleCache[cacheKey] = TitleCacheEntry{
			Title:     title,
			Source:    source,
			UpdatedAt: time.Now(),
		}
		titleCacheMutex.Unlock()
	}

	return title
}
