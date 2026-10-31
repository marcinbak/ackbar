package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/tmux"
)

// AntigravityAgentAdapter implements AgentAdapter for Google Antigravity.
type AntigravityAgentAdapter struct{}

func (a AntigravityAgentAdapter) GetResetCommand() string {
	return "/reset"
}

func (a AntigravityAgentAdapter) GetApprovalKeys(allow bool) []string {
	if allow {
		return []string{"1", "Enter"}
	}
	return []string{"4", "Enter"}
}

func (a AntigravityAgentAdapter) AccountEnvVars(home string, acc *AgentAccount) map[string]string {
	if acc == nil {
		return nil
	}
	envVars := make(map[string]string)
	if acc.ConfigDir != "" {
		cfgDir := acc.ConfigDir
		if strings.HasPrefix(cfgDir, "~/") && home != "" {
			cfgDir = filepath.Join(home, cfgDir[2:])
		}
		envVars["GEMINI_CLI_HOME"] = cfgDir
	}
	for k, v := range acc.Env {
		envVars[k] = v
	}
	if acc.Name != "" {
		envVars["ACKBAR_ACCOUNT"] = acc.Name
	}
	return envVars
}

func (a AntigravityAgentAdapter) IsAccountLoggedIn(home string, acc *AgentAccount) bool {
	if acc == nil {
		return false
	}
	if acc.Env != nil && acc.Env["GEMINI_API_KEY"] != "" {
		return true
	}
	if acc.ConfigDir != "" {
		cfgDir := acc.ConfigDir
		if strings.HasPrefix(cfgDir, "~/") && home != "" {
			cfgDir = filepath.Join(home, cfgDir[2:])
		}
		if _, err := os.Stat(cfgDir); err == nil {
			return true
		}
	}
	if home != "" {
		if _, err := os.Stat(filepath.Join(home, ".gemini")); err == nil {
			return true
		}
	}
	return false
}

func (a AntigravityAgentAdapter) SetupProfileDir(home, name, configDir string) (string, error) {
	if configDir == "" {
		configDir = filepath.Join(home, ".gemini-profiles", name)
	} else if strings.HasPrefix(configDir, "~/") && home != "" {
		configDir = filepath.Join(home, configDir[2:])
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}

	cfgDir := filepath.Join(configDir, "config")
	_ = os.MkdirAll(cfgDir, 0755)
	hookFile := filepath.Join(cfgDir, "hooks.json")
	var existingHooks map[string]interface{}
	if data, err := os.ReadFile(hookFile); err == nil {
		_ = json.Unmarshal(data, &existingHooks)
	}
	if existingHooks == nil {
		existingHooks = make(map[string]interface{})
	}
	delete(existingHooks, "hooks")

	hookCmd := "ackbar-hook --agent=antigravity"
	existingHooks["ackbar"] = map[string]interface{}{
		"SessionStart": []map[string]interface{}{
			{
				"type":    "command",
				"command": hookCmd + " --event=SessionStart",
			},
		},
		"PreInvocation": []map[string]interface{}{
			{
				"type":    "command",
				"command": hookCmd + " --event=PreInvocation",
			},
		},
		"PostInvocation": []map[string]interface{}{
			{
				"type":    "command",
				"command": hookCmd + " --event=PostInvocation",
			},
		},
		"Notification": []map[string]interface{}{
			{
				"type":    "command",
				"command": hookCmd + " --event=Notification",
			},
		},
		"Stop": []map[string]interface{}{
			{
				"type":    "command",
				"command": hookCmd + " --event=Stop",
			},
		},
		"PreToolUse": []map[string]interface{}{
			{
				"matcher": "*",
				"hooks": []map[string]interface{}{
					{
						"type":    "command",
						"command": hookCmd + " --event=PreToolUse",
					},
				},
			},
		},
		"PostToolUse": []map[string]interface{}{
			{
				"matcher": "*",
				"hooks": []map[string]interface{}{
					{
						"type":    "command",
						"command": hookCmd + " --event=PostToolUse",
					},
				},
			},
		},
	}
	if updated, err := json.MarshalIndent(existingHooks, "", "  "); err == nil {
		_ = os.WriteFile(hookFile, updated, 0644)
	}
	return configDir, nil
}

func (a AntigravityAgentAdapter) GetLoginCommand() string {
	return "agy login"
}

func (a AntigravityAgentAdapter) IsValidNativeID(nativeID string) bool {
	return nativeID != ""
}

var antigravityIgnoredSubcommands = map[string]bool{
	"agent": true, "agents": true, "changelog": true, "help": true,
	"install": true, "mcp": true, "models": true, "plugin": true,
	"plugins": true, "update": true,
}

var antigravityIgnoredFlags = map[string]bool{
	"--prompt": true,
}

func (a AntigravityAgentAdapter) IsIgnoredCommand(fullCmd string) bool {
	return checkIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "antigravity" || base == "agy" || strings.HasPrefix(base, "agy")
	}, antigravityIgnoredSubcommands, antigravityIgnoredFlags)
}

func (a AntigravityAgentAdapter) MatchProcessCommand(binLower, cmdLower string) bool {
	if binLower == "antigravity" || binLower == "agy" || strings.Contains(cmdLower, "bin/agy") {
		if !strings.Contains(cmdLower, "ackbar") {
			return true
		}
	}
	return false
}

func (a AntigravityAgentAdapter) RequiresResolvedSessionID() bool {
	return true
}

func (a AntigravityAgentAdapter) ResolveSessionForPID(ctx context.Context, home string, pid int, cmdFields []string) string {
	sID := findAntigravitySessionForPID(ctx, pid)
	if sID != "" && home != "" && isAntigravitySubagent(home, sID) {
		return ""
	}
	if sID == "" {
		for i, arg := range cmdFields {
			if (arg == "--conversation" || arg == "-c" || arg == "--conversation-id") && i+1 < len(cmdFields) {
				cID := strings.TrimSpace(cmdFields[i+1])
				if IsUUID(cID) {
					if home != "" && isAntigravitySubagent(home, cID) {
						return ""
					}
					sID = cID
					break
				}
			}
		}
	}
	return sID
}

func (a AntigravityAgentAdapter) ResolveSessionOwner(home, sessionID string) (pid int, tmuxName string) {
	return 0, ""
}

func (a AntigravityAgentAdapter) HasConversationArtifacts(home, nativeID string) bool {
	return hasAntigravityConversationArtifacts(home, nativeID)
}

func (a AntigravityAgentAdapter) IsSubagentSession(home, nativeID string, precomputed map[string]bool) bool {
	return isAntigravitySubagent(home, nativeID, precomputed)
}

func (a AntigravityAgentAdapter) ListWorkspaceDocuments(home, cwd, nativeID string, addDoc func(fullPath, title, relPath, category, categoryLabel string, priority int)) {
	if nativeID == "" || home == "" {
		return
	}
	candidateDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", nativeID),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID),
		filepath.Join(home, ".antigravity", "brain", nativeID),
	}
	for _, convBrainDir := range candidateDirs {
		if dirExists(convBrainDir) {
			if files, ferr := os.ReadDir(convBrainDir); ferr == nil {
				for _, f := range files {
					if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
						full := filepath.Join(convBrainDir, f.Name())
						cat, label, prio := classifyDoc(f.Name())
						if cat == "other" {
							cat, label = "plan", "Antigravity Plan"
						}
						addDoc(full, fmt.Sprintf("⚡ %s", f.Name()), f.Name(), cat, label, prio+5)
					}
				}
			}
		}
	}
}

func (a AntigravityAgentAdapter) ScanDiskSessions(ctx context.Context, s *Server, home string, state *ScanState) {
	if home == "" {
		return
	}
	hostName := state.hostName
	brainDirs := antigravityBrainDirs(home)

	subagentIDs := make(map[string]bool)
	for _, bDir := range brainDirs {
		if matches, err := filepath.Glob(filepath.Join(bDir, "*", ".system_generated", "subagents", "*.json")); err == nil {
			for _, m := range matches {
				id := strings.TrimSuffix(filepath.Base(m), ".json")
				if isUUID(id) {
					subagentIDs[strings.ToLower(id)] = true
				}
			}
		}
	}

	for _, brainDir := range brainDirs {
		bDirs, err := os.ReadDir(brainDir)
		if err != nil {
			continue
		}
		for _, bDir := range bDirs {
			convPath := filepath.Join(brainDir, bDir.Name())
			logPath := filepath.Join(convPath, ".system_generated", "logs", "transcript.jsonl")
			// Only process real conversation directories with valid transcript logs
			if !bDir.IsDir() || !fileExists(logPath) || !isUUID(bDir.Name()) {
				continue
			}
			convID := bDir.Name()
			sessID := fmt.Sprintf("antigravity:%s:%s", hostName, convID)
			if s.db.IsSessionDeleted(sessID) || s.db.IsSessionDeleted(convID) {
				continue
			}

			// Check if this is an internal subagent conversation
			if a.IsSubagentSession(home, convID, subagentIDs) {
				if existing := state.knownIDs[sessID]; existing != nil {
					if existing.CustomTitle != "" || !IsUUID(existing.NativeID) {
						// Protect user-customized or placeholder spawning session
					} else {
						_ = s.db.DeleteSession(sessID)
						existing.Deleted = true
						existing.State = StateEnded
						existing.Activity = "Deleted"
						s.broadcast(existing)
						delete(state.knownIDs, sessID)
						delete(state.knownByNativeID, convID)
					}
				}
				continue
			}

			existing := state.knownIDs[sessID]
			if existing == nil && convID != "" {
				existing = state.knownByNativeID[convID]
			}
			if existing == nil {
				title := ReadAntigravitySessionTitle("", convID)
				cwd := extractAntigravityWorkspace(logPath, home)
				modTime := time.Now()
				convStartTime := modTime

				if stat, serr := os.Stat(convPath); serr == nil {
					modTime = stat.ModTime()
					convStartTime = modTime
				}
				if lstat, lerr := os.Stat(logPath); lerr == nil && lstat.ModTime().After(modTime) {
					modTime = lstat.ModTime()
				}
				convStartTime = extractAntigravityStartTime(logPath, convStartTime)

				if title == "" {
					title = fmt.Sprintf("Antigravity (%s)", convID[:8])
				}

				// Check if a newly spawned managed Antigravity session in this workspace should adopt convID
				if cwd != "" && time.Since(convStartTime) < 15*time.Minute {
					if spawningSess, err := s.findSpawningSession("antigravity", cwd); err == nil && spawningSess != nil && spawningSess.NativeID != convID && convStartTime.After(spawningSess.StartedAt.Add(-5*time.Second)) {
						oldSessID := spawningSess.ID
						_ = s.db.DeleteSession(oldSessID)
						delete(state.knownIDs, oldSessID)
						delete(state.knownByNativeID, spawningSess.NativeID)

						tmuxName := spawningSess.TmuxName
						newTmuxName := fmt.Sprintf("ackbar-antigravity-%s", convID)
						if strings.HasPrefix(tmuxName, "ackbar-") && tmuxName != newTmuxName {
							if rErr := tmux.Rename(ctx, tmuxName, newTmuxName); rErr == nil {
								delete(state.knownByTmux, tmuxName)
								tmuxName = newTmuxName
							} else {
								log.Printf("[daemon] failed to rename tmux session %s to %s in brain scan adoption: %v", tmuxName, newTmuxName, rErr)
							}
						}

						adoptedCwd := spawningSess.Cwd
						if adoptedCwd == "" {
							adoptedCwd = cwd
						}
						adoptedName := title
						if spawningSess.CustomTitle != "" {
							adoptedName = spawningSess.CustomTitle
						}
						adoptedNodePath := spawningSess.NodePath
						if adoptedNodePath == "" && adoptedCwd != "" {
							adoptedNodePath = s.resolveSessionNodePath(adoptedCwd)
						}

						spawningSess.ID = sessID
						spawningSess.OldID = oldSessID
						spawningSess.NativeID = convID
						spawningSess.Name = adoptedName
						spawningSess.Cwd = adoptedCwd
						spawningSess.ProjectKey = GetProjectKey(adoptedCwd)
						spawningSess.NodePath = adoptedNodePath
						spawningSess.TmuxName = tmuxName
						spawningSess.LastEventAt = modTime

						if p, ok := s.providers["antigravity"]; ok {
							if inspector, ok := p.(StatusInspector); ok {
								inspector.InspectStatus(ctx, spawningSess)
							}
						}
						_ = s.db.SaveSession(spawningSess)
						state.knownIDs[sessID] = spawningSess
						state.knownByNativeID[convID] = spawningSess
						if tmuxName != "" {
							state.knownByTmux[tmuxName] = spawningSess
						}
						s.broadcast(spawningSess)
						continue
					}
				}

				isOld := time.Since(modTime) > 7*24*time.Hour

				newSess := &Session{
					ID:          sessID,
					Name:        title,
					Agent:       "antigravity",
					Host:        hostName,
					NativeID:    convID,
					Cwd:         cwd,
					ProjectKey:  GetProjectKey(cwd),
					NodePath:    s.resolveSessionNodePath(cwd),
					State:       StateEnded,
					Managed:     false,
					Activity:    "Session ended",
					StartedAt:   convStartTime,
					LastEventAt: modTime,
					ContextPct:  0,
					Archived:    isOld,
				}
				_ = s.db.SaveSession(newSess)
				state.knownIDs[sessID] = newSess
				s.broadcast(newSess)
			} else {
				changed := false
				if existing.Cwd == "" {
					if cwd := extractAntigravityWorkspace(logPath, home); cwd != "" {
						existing.Cwd = cwd
						existing.ProjectKey = GetProjectKey(cwd)
						changed = true
					}
				}
				if !existing.Managed && existing.Cwd != "" {
					convStartTime := existing.StartedAt
					if stat, serr := os.Stat(convPath); serr == nil && convStartTime.IsZero() {
						convStartTime = stat.ModTime()
					}
					convStartTime = extractAntigravityStartTime(logPath, convStartTime)
					if time.Since(convStartTime) < 15*time.Minute {
						if spawningSess, err := s.findSpawningSession("antigravity", existing.Cwd); err == nil && spawningSess != nil && spawningSess.NativeID != convID && convStartTime.After(spawningSess.StartedAt.Add(-5*time.Second)) {
							oldSessID := spawningSess.ID
							_ = s.db.DeleteSession(oldSessID)
							delete(state.knownIDs, oldSessID)
							delete(state.knownByNativeID, spawningSess.NativeID)

							tmuxName := spawningSess.TmuxName
							newTmuxName := fmt.Sprintf("ackbar-antigravity-%s", convID)
							if strings.HasPrefix(tmuxName, "ackbar-") && tmuxName != newTmuxName {
								if rErr := tmux.Rename(ctx, tmuxName, newTmuxName); rErr == nil {
									delete(state.knownByTmux, tmuxName)
									tmuxName = newTmuxName
								} else {
									log.Printf("[daemon] failed to rename tmux session %s to %s in deferred brain scan adoption: %v", tmuxName, newTmuxName, rErr)
								}
							}

							existing.OldID = oldSessID
							existing.Managed = true
							existing.PID = spawningSess.PID
							existing.EngineType = spawningSess.EngineType
							existing.TmuxName = tmuxName
							if spawningSess.Cwd != "" {
								existing.Cwd = spawningSess.Cwd
								existing.ProjectKey = GetProjectKey(spawningSess.Cwd)
							}
							if spawningSess.NodePath != "" {
								existing.NodePath = spawningSess.NodePath
							}
							if spawningSess.CustomTitle != "" {
								existing.CustomTitle = spawningSess.CustomTitle
								existing.Name = spawningSess.CustomTitle
							}
							if tmuxName != "" {
								state.knownByTmux[tmuxName] = existing
							}
							if p, ok := s.providers["antigravity"]; ok {
								if inspector, ok := p.(StatusInspector); ok {
									inspector.InspectStatus(ctx, existing)
								}
							}
							changed = true
						}
					}
				}
				if existing.CustomTitle == "" {
					if isRawSessionName(existing.Name) {
						if title := ReadAntigravitySessionTitle(existing.Cwd, convID); title != "" && !isRawSessionName(title) {
							existing.Name = title
							changed = true
						}
					} else if anno := ReadAntigravityAnnotationTitle(convID); anno != "" && anno != existing.Name {
						existing.Name = anno
						changed = true
					}
				}
				if existing.NodePath == "" && existing.Cwd != "" {
					if np := s.resolveSessionNodePath(existing.Cwd); np != "" {
						existing.NodePath = np
						changed = true
					}
				}
				if changed {
					_ = s.db.SaveSession(existing)
					s.broadcast(existing)
				}
			}
		}
	}

	// Database Sanitation: Clean up any obsolete/orphaned subagents in DB
	for _, sObj := range state.existingSessions {
		if sObj.Deleted {
			continue
		}
		if !s.isLocalHost(sObj.Host) {
			continue
		}
		if sObj.Agent == "antigravity" && sObj.NativeID != "" && IsUUID(sObj.NativeID) {
			if sObj.CustomTitle != "" {
				continue
			}
			if a.IsSubagentSession(home, sObj.NativeID, subagentIDs) {
				_ = s.db.DeleteSession(sObj.ID)
				sObj.Deleted = true
				sObj.State = StateEnded
				sObj.Activity = "Deleted"
				s.broadcast(sObj)
				delete(state.knownIDs, sObj.ID)
				delete(state.knownByNativeID, sObj.NativeID)
			}
		}
	}
}

func hasAntigravityConversationArtifacts(home, nativeID string) bool {
	if home == "" || nativeID == "" || !IsUUID(nativeID) {
		return false
	}
	candidates := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", nativeID),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID),
		filepath.Join(home, ".antigravity", "brain", nativeID),
		filepath.Join(home, ".gemini", "antigravity-cli", "conversations", nativeID+".db"),
		filepath.Join(home, ".gemini", "antigravity", "conversations", nativeID+".db"),
		filepath.Join(home, ".antigravity", "conversations", nativeID+".db"),
		filepath.Join(home, ".gemini", "antigravity-cli", "presence", nativeID+".lock"),
		filepath.Join(home, ".gemini", "antigravity", "presence", nativeID+".lock"),
		filepath.Join(home, ".antigravity", "presence", nativeID+".lock"),
		filepath.Join(home, ".gemini", "antigravity", "annotations", nativeID+".pbtxt"),
		filepath.Join(home, ".gemini", "antigravity-cli", "annotations", nativeID+".pbtxt"),
		filepath.Join(home, ".antigravity", "annotations", nativeID+".pbtxt"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func extractConvIDFromPath(path string) string {
	clean := filepath.ToSlash(path)
	markers := []string{"/presence/", "/brain/", "/conversations/", "/annotations/"}
	for _, m := range markers {
		if idx := strings.Index(clean, m); idx != -1 {
			sub := clean[idx+len(m):]
			seg := sub
			if slashIdx := strings.Index(sub, "/"); slashIdx != -1 {
				seg = sub[:slashIdx]
			}
			seg = strings.TrimSuffix(seg, ".lock")
			seg = strings.TrimSuffix(seg, ".db")
			seg = strings.TrimSuffix(seg, ".pbtxt")
			if IsUUID(seg) {
				return seg
			}
		}
	}
	return ""
}

func findAntigravitySessionForPID(ctx context.Context, pid int) string {
	return findAntigravitySessionForPIDWithDepth(ctx, pid, 0)
}

func findAntigravitySessionForPIDWithDepth(ctx context.Context, pid int, depth int) string {
	if pid <= 0 || depth > 2 {
		return ""
	}

	home, _ := os.UserHomeDir()
	var fallbackCandidate string

	// 1. Linux: scan /proc/<pid>/fd/
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	if entries, err := os.ReadDir(fdDir); err == nil {
		for _, e := range entries {
			link, err := os.Readlink(filepath.Join(fdDir, e.Name()))
			if err == nil && link != "" {
				cleanLink := filepath.ToSlash(link)
				// Prioritize direct presence lock held by the process
				if strings.Contains(cleanLink, "/presence/") && strings.HasSuffix(cleanLink, ".lock") {
					if convID := extractConvIDFromPath(cleanLink); convID != "" {
						if home == "" || !isAntigravitySubagent(home, convID) {
							return convID
						}
					}
				}
				if fallbackCandidate == "" {
					if convID := extractConvIDFromPath(cleanLink); convID != "" {
						if home == "" || !isAntigravitySubagent(home, convID) {
							fallbackCandidate = convID
						}
					}
				}
			}
		}
	}
	if fallbackCandidate != "" {
		return fallbackCandidate
	}

	// 2. Cross-platform / macOS / fallback: lsof -a -n -P -p <pid> -Fn
	// Bounded with 1-second timeout to avoid any hang on blocked mounts
	subCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	cmd := exec.CommandContext(subCtx, "lsof", "-a", "-n", "-P", "-p", strconv.Itoa(pid), "-Fn")
	out, err := cmd.Output()
	if err == nil || len(out) > 0 {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "n") {
				path := line[1:]
				cleanPath := filepath.ToSlash(path)
				if strings.Contains(cleanPath, "/presence/") && strings.HasSuffix(cleanPath, ".lock") {
					if convID := extractConvIDFromPath(cleanPath); convID != "" {
						if home == "" || !isAntigravitySubagent(home, convID) {
							return convID
						}
					}
				}
				if fallbackCandidate == "" {
					if convID := extractConvIDFromPath(cleanPath); convID != "" {
						if home == "" || !isAntigravitySubagent(home, convID) {
							fallbackCandidate = convID
						}
					}
				}
			}
		}
	}
	if fallbackCandidate != "" {
		return fallbackCandidate
	}

	// 3. Inspect child processes if wrapper process (capped at depth 2)
	// Only inspect children if the process itself has no conversation ID
	pgrepCmd := exec.CommandContext(subCtx, "pgrep", "-P", strconv.Itoa(pid))
	if pgrepOut, pErr := pgrepCmd.Output(); pErr == nil {
		for _, cpStr := range strings.Fields(string(pgrepOut)) {
			if cp, err := strconv.Atoi(cpStr); err == nil && cp > 0 {
				if sID := findAntigravitySessionForPIDWithDepth(ctx, cp, depth+1); sID != "" {
					if home == "" || !isAntigravitySubagent(home, sID) {
						return sID
					}
				}
			}
		}
	}

	return ""
}

func parsePbtxtTitle(content string) string {
	re := regexp.MustCompile(`title\s*:\s*"([^"]+)"`)
	m := re.FindStringSubmatch(content)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func ReadAntigravityAnnotationTitle(sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	targetID := sessionID
	if strings.HasPrefix(sessionID, "proc-") {
		return ""
	}
	if targetID == "" {
		return ""
	}
	annotationDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "annotations"),
		filepath.Join(home, ".gemini", "antigravity-cli", "annotations"),
		filepath.Join(home, ".antigravity", "annotations"),
	}
	for _, aDir := range annotationDirs {
		annoPath := filepath.Join(aDir, targetID+".pbtxt")
		if data, err := os.ReadFile(annoPath); err == nil {
			if title := parsePbtxtTitle(string(data)); title != "" {
				return title
			}
		}
	}
	return ""
}

func ReadAntigravitySessionTitle(cwd, sessionID string) string {
	targetID := sessionID
	if strings.HasPrefix(sessionID, "proc-") {
		targetID = ""
	}

	if targetID == "" {
		return ""
	}

	// 1. Direct Lookup: Check annotations in all Antigravity dirs (.gemini/antigravity, .gemini/antigravity-cli, .antigravity)
	if annoTitle := ReadAntigravityAnnotationTitle(targetID); annoTitle != "" {
		return annoTitle
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// 2. Check conversation_metadata.json cache
	metadataPaths := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".gemini", "antigravity", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".antigravity", "cache", "conversation_metadata.json"),
	}
	for _, mPath := range metadataPaths {
		if data, err := os.ReadFile(mPath); err == nil {
			var meta struct {
				Conversations map[string]struct {
					Summary struct {
						Title   string `json:"Title"`
						Preview string `json:"Preview"`
					} `json:"summary"`
				} `json:"conversations"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				if c, exists := meta.Conversations[targetID]; exists {
					if c.Summary.Title != "" {
						return c.Summary.Title
					}
					if c.Summary.Preview != "" && c.Summary.Preview != "Session Exit Command" {
						return truncateTitle(c.Summary.Preview)
					}
				}
			}
		}
	}

	// 3. Check brain task summary fallback
	brainDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain"),
		filepath.Join(home, ".antigravity", "brain"),
	}
	for _, bDir := range brainDirs {
		metaPath := filepath.Join(bDir, targetID, "task.md.metadata.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(data, &meta); err == nil && meta.Summary != "" {
				return truncateTitle(meta.Summary)
			}
		}
	}

	// 4. Scan transcript for CHECKPOINT objective or first user prompt (using bounded head read)
	for _, bDir := range brainDirs {
		logPath := filepath.Join(bDir, targetID, ".system_generated", "logs", "transcript.jsonl")
		if data, err := readHead(logPath, 64*1024); err == nil && len(data) > 0 {
			lines := strings.Split(string(data), "\n")
			firstPromptTitle := ""
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var step struct {
					Type    string `json:"type"`
					Content string `json:"content"`
				}
				if json.Unmarshal([]byte(line), &step) == nil {
					if step.Type == "CHECKPOINT" && strings.Contains(step.Content, "# USER Objective:") {
						idx := strings.Index(step.Content, "# USER Objective:")
						sub := strings.TrimSpace(step.Content[idx+len("# USER Objective:"):])
						if end := strings.Index(sub, "\n"); end != -1 {
							sub = strings.TrimSpace(sub[:end])
						}
						if sub != "" && !strings.HasPrefix(sub, "<") && len(sub) >= 4 && !IsRawSessionName(sub) {
							return truncateTitle(sub)
						}
					}
					if step.Type == "USER_INPUT" && step.Content != "" && firstPromptTitle == "" {
						clean := cleanAntigravityPrompt(step.Content)
						if clean != "" && !strings.HasPrefix(clean, "/") && !strings.HasPrefix(clean, "<") && !IsRawSessionName(clean) {
							firstPromptTitle = truncateTitle(clean)
						}
					}
				}
			}
			if firstPromptTitle != "" {
				return firstPromptTitle
			}
		}
	}

	// 5. Check central Antigravity proto registry fallback: ~/.gemini/antigravity/agyhub_summaries_proto.pb
	protoPaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "agyhub_summaries_proto.pb"),
		filepath.Join(home, ".gemini", "antigravity-cli", "agyhub_summaries_proto.pb"),
		filepath.Join(home, ".antigravity", "agyhub_summaries_proto.pb"),
	}
	for _, protoPath := range protoPaths {
		if data, err := readHead(protoPath, 256*1024); err == nil && len(data) > 0 {
			str := string(data)
			if idx := strings.Index(str, targetID); idx != -1 {
				sub := str[idx+len(targetID):]
				if len(sub) > 250 {
					sub = sub[:250]
				}
				var words []string
				var cur strings.Builder
				for _, r := range sub {
					if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' || r == '-' || r == '_' || r == ':' || r == '/' || r == '(' || r == ')' {
						cur.WriteRune(r)
					} else {
						if cur.Len() >= 4 {
							words = append(words, strings.TrimSpace(cur.String()))
						}
						cur.Reset()
					}
				}
				if cur.Len() >= 4 {
					words = append(words, strings.TrimSpace(cur.String()))
				}
				for _, w := range words {
					if !strings.HasPrefix(w, "file:") && !strings.HasPrefix(w, "git@") && !strings.Contains(w, "Users/") && !strings.Contains(w, "home/") && len(w) >= 5 && !IsRawSessionName(w) {
						return truncateTitle(w)
					}
				}
			}
		}
	}

	return ""
}

func normalizeToGitRoot(dir, home string) string {
	if dir == "" {
		return ""
	}
	cleanHome := ""
	if home != "" {
		cleanHome = filepath.Clean(home)
	}
	curr := filepath.Clean(dir)
	for curr != "" && curr != "/" && curr != "." && curr != cleanHome {
		gitPath := filepath.Join(curr, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return curr
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return dir
}

func extractAntigravityStartTime(logPath string, fallback time.Time) time.Time {
	data, err := readHead(logPath, 8*1024)
	if err != nil {
		return fallback
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var step struct {
			CreatedAt string `json:"created_at"`
		}
		if err := json.Unmarshal([]byte(line), &step); err == nil && step.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, step.CreatedAt); err == nil {
				return t
			}
			if t, err := time.Parse(time.RFC3339, step.CreatedAt); err == nil {
				return t
			}
		}
		break
	}
	return fallback
}

func extractAntigravityWorkspace(logPath, home string) string {
	data, err := readHead(logPath, 64*1024)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")

	// 1. Scan for user_information mapping: "/path -> corpus"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, " -> ") {
			idx := strings.Index(line, " -> ")
			startIdx := strings.LastIndexAny(line[:idx], " \t\n\r\"'[]")
			if startIdx == -1 {
				startIdx = 0
			} else {
				startIdx++
			}
			cand := strings.TrimSpace(line[startIdx:idx])
			cand = strings.TrimPrefix(cand, "file://")
			if dirExists(cand) && cand != home && !strings.Contains(cand, "/.gemini") {
				return normalizeToGitRoot(cand, home)
			}
		}
	}

	// 2. Scan tool calls in first 50 steps for authoritative workspace paths
	maxLines := len(lines)
	if maxLines > 50 {
		maxLines = 50
	}
	for i := 0; i < maxLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var step struct {
			ToolCalls []struct {
				Name string                 `json:"name"`
				Args map[string]interface{} `json:"args"`
			} `json:"tool_calls"`
		}
		if err := json.Unmarshal([]byte(line), &step); err == nil {
			for _, tc := range step.ToolCalls {
				for _, k := range []string{"Cwd", "DirectoryPath", "SearchPath"} {
					if v, ok := tc.Args[k]; ok {
						if s, ok := v.(string); ok && s != "" {
							s = strings.Trim(s, "\"")
							if dirExists(s) && s != home && !strings.Contains(s, "/.gemini") {
								return normalizeToGitRoot(s, home)
							}
						}
					}
				}
				for _, k := range []string{"AbsolutePath", "Path", "TargetFile"} {
					if v, ok := tc.Args[k]; ok {
						if s, ok := v.(string); ok && s != "" {
							s = strings.Trim(s, "\"")
							dir := filepath.Dir(s)
							if dirExists(dir) && dir != home && !strings.Contains(dir, "/.gemini") {
								return normalizeToGitRoot(dir, home)
							}
						}
					}
				}
			}
		}
	}

	return ""
}

func antigravityBrainDirs(home string) []string {
	if home == "" {
		return nil
	}
	dirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain"),
		filepath.Join(home, ".antigravity", "brain"),
	}
	if m1, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain")); err == nil {
		dirs = append(dirs, m1...)
	}
	if m2, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain")); err == nil {
		dirs = append(dirs, m2...)
	}
	return dirs
}

func hasAntigravityUserAnnotation(home, convID string) bool {
	if home == "" || convID == "" {
		return false
	}
	annoPaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "annotations", convID+".pbtxt"),
		filepath.Join(home, ".gemini", "antigravity-cli", "annotations", convID+".pbtxt"),
		filepath.Join(home, ".antigravity", "annotations", convID+".pbtxt"),
	}
	if m1, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "annotations", convID+".pbtxt")); err == nil {
		annoPaths = append(annoPaths, m1...)
	}
	if m2, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "annotations", convID+".pbtxt")); err == nil {
		annoPaths = append(annoPaths, m2...)
	}
	for _, p := range annoPaths {
		if fileExists(p) {
			return true
		}
	}
	return false
}

func extractSenderUUIDFromSystemMessage(text, selfID string) string {
	lower := strings.ToLower(text)
	search := lower
	for {
		idx := strings.Index(search, "sender=")
		if idx == -1 {
			break
		}
		rest := strings.TrimSpace(text[len(text)-len(search)+idx+len("sender="):])
		rest = strings.TrimLeft(rest, `"'`)
		end := strings.IndexAny(rest, " \t\r\n\"'\\>]")
		cand := rest
		if end != -1 {
			cand = rest[:end]
		}
		cand = strings.Trim(cand, `"'`)
		if IsUUID(cand) && (selfID == "" || !strings.EqualFold(cand, selfID)) {
			return cand
		}
		search = search[idx+len("sender="):]
	}
	return ""
}

// ResolveAntigravitySubagentParent resolves the parent conversation UUID, role, and typeName
// for a given Antigravity subagent conversation ID.
func ResolveAntigravitySubagentParent(home, convID string) (parentID, role, typeName string) {
	if home == "" || convID == "" || !IsUUID(convID) {
		return "", "", ""
	}
	convID = filepath.Base(filepath.Clean(convID))
	if hasAntigravityUserAnnotation(home, convID) {
		return "", "", ""
	}

	brainDirs := antigravityBrainDirs(home)
	for _, bDir := range brainDirs {
		matches, err := filepath.Glob(filepath.Join(bDir, "*", ".system_generated", "subagents", convID+".json"))
		if err == nil && len(matches) > 0 {
			for _, match := range matches {
				parentCandidate := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(match))))
				if IsUUID(parentCandidate) {
					if data, rErr := os.ReadFile(match); rErr == nil {
						var payload struct {
							Role               string `json:"role"`
							TypeName           string `json:"typeName"`
							SubagentDescriptor struct {
								Role     string `json:"role"`
								TypeName string `json:"typeName"`
							} `json:"subagentDescriptor"`
						}
						if json.Unmarshal(data, &payload) == nil {
							role = payload.SubagentDescriptor.Role
							if role == "" {
								role = payload.Role
							}
							typeName = payload.SubagentDescriptor.TypeName
							if typeName == "" {
								typeName = payload.TypeName
							}
						}
					}
					return parentCandidate, role, typeName
				}
			}
		}
	}

	for _, bDir := range brainDirs {
		logPath := filepath.Join(bDir, convID, ".system_generated", "logs", "transcript.jsonl")
		if data, err := readHead(logPath, 64*1024); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var step struct {
					StepIndex int    `json:"step_index"`
					Source    string `json:"source"`
					Type      string `json:"type"`
					Content   string `json:"content"`
				}
				if json.Unmarshal([]byte(line), &step) == nil {
					if step.StepIndex == 0 {
						if strings.EqualFold(step.Type, "SYSTEM_MESSAGE") || strings.EqualFold(step.Source, "SYSTEM") {
							if strings.Contains(strings.ToLower(step.Content), "<system_message>") {
								if senderID := extractSenderUUIDFromSystemMessage(step.Content, convID); senderID != "" {
									return senderID, "", ""
								}
							}
						}
						if strings.EqualFold(step.Type, "USER_INPUT") || strings.EqualFold(step.Source, "USER_EXPLICIT") || strings.EqualFold(step.Source, "USER") {
							return "", "", ""
						}
					}
				} else if strings.Contains(strings.ToLower(line), "<system_message>") {
					if senderID := extractSenderUUIDFromSystemMessage(line, convID); senderID != "" {
						return senderID, "", ""
					}
				}
				break
			}
		}

		msgDir := filepath.Join(bDir, convID, ".system_generated", "messages")
		if entries, err := os.ReadDir(msgDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && e.Name() != "read.json" {
					if mData, mErr := readHead(filepath.Join(msgDir, e.Name()), 8*1024); mErr == nil {
						mStr := string(mData)
						search := mStr
						for {
							idx := strings.Index(search, `"conversationId":`)
							if idx == -1 {
								break
							}
							rest := strings.TrimSpace(search[idx+len(`"conversationId":`):])
							if strings.HasPrefix(rest, `"`) {
								rest = rest[1:]
								if qEnd := strings.Index(rest, `"`); qEnd != -1 {
									otherUUID := rest[:qEnd]
									if IsUUID(otherUUID) && !strings.EqualFold(otherUUID, convID) {
										return otherUUID, "", ""
									}
								}
							}
							search = search[idx+len(`"conversationId":`):]
						}
					}
				}
			}
		}
	}

	return "", "", ""
}

func hasSystemStepSubagentMarker(lower string) bool {
	return strings.Contains(lower, "<subagent_invocation>") ||
		strings.Contains(lower, "you are a subagent") ||
		strings.Contains(lower, "subagent task") ||
		strings.Contains(lower, "invoked by parent") ||
		strings.Contains(lower, "role contract (") ||
		strings.Contains(lower, "advisory restriction:")
}

func hasLegacySubagentPromptPrefix(lower string) bool {
	trimmed := strings.TrimSpace(lower)
	return strings.Contains(trimmed, "<subagent_invocation>") ||
		strings.HasPrefix(trimmed, "you are a subagent") ||
		strings.HasPrefix(trimmed, "you are reviewer") ||
		strings.HasPrefix(trimmed, "you are code reviewer") ||
		strings.HasPrefix(trimmed, "you are a code reviewer") ||
		strings.HasPrefix(trimmed, "you are the dedicated security reviewer") ||
		strings.HasPrefix(trimmed, "you are the security reviewer") ||
		strings.HasPrefix(trimmed, "you are the security auditor") ||
		strings.HasPrefix(trimmed, "you are the security-reviewer") ||
		strings.HasPrefix(trimmed, "you are the clean-reviewer") ||
		strings.HasPrefix(trimmed, "you are the context-reviewer") ||
		strings.HasPrefix(trimmed, "advisory: you are read-only") ||
		((strings.HasPrefix(trimmed, "you are the ") || strings.HasPrefix(trimmed, "you are a ")) && strings.Contains(trimmed, "-reviewer for project"))
}

// isAntigravitySubagent determines whether a given conversation UUID is an internal
// subagent (such as code reviewer, security auditor, or background worker) rather
// than a primary interactive user session.
func isAntigravitySubagent(home, convID string, precomputedSubagents ...map[string]bool) bool {
	if convID == "" || !IsUUID(convID) {
		return false
	}
	convID = filepath.Base(filepath.Clean(convID))
	// 1. Check if user annotation exists (annotations are ONLY created by the IDE/CLI for real user root sessions)
	if hasAntigravityUserAnnotation(home, convID) {
		return false
	}

	brainDirs := antigravityBrainDirs(home)

	// 2. Direct parent subagent registration check:
	// Every subagent created via invoke_subagent is recorded in <parent_brain>/.system_generated/subagents/<convID>.json
	if len(precomputedSubagents) > 0 && precomputedSubagents[0] != nil {
		if precomputedSubagents[0][strings.ToLower(convID)] {
			return true
		}
	} else {
		for _, bDir := range brainDirs {
			matches, err := filepath.Glob(filepath.Join(bDir, "*", ".system_generated", "subagents", convID+".json"))
			if err == nil && len(matches) > 0 {
				return true
			}
		}
	}

	// 3. Check conversation_metadata.json if present
	metaPaths := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".gemini", "antigravity", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".antigravity", "cache", "conversation_metadata.json"),
	}
	for _, mp := range metaPaths {
		if data, err := os.ReadFile(mp); err == nil {
			var meta struct {
				Conversations map[string]struct {
					IsInternal bool `json:"is_internal"`
				} `json:"conversations"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				if c, exists := meta.Conversations[convID]; exists && c.IsInternal {
					return true
				}
			}
		}
	}

	// 4. Check transcript file first step (step_index == 0) for explicit subagent invocation markers
	for _, bDir := range brainDirs {
		logPath := filepath.Join(bDir, convID, ".system_generated", "logs", "transcript.jsonl")
		if data, err := readHead(logPath, 64*1024); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var step struct {
					StepIndex int    `json:"step_index"`
					Source    string `json:"source"`
					Type      string `json:"type"`
					Content   string `json:"content"`
				}
				if json.Unmarshal([]byte(line), &step) == nil {
					if step.StepIndex == 0 {
						contentLower := strings.ToLower(step.Content)
						if strings.EqualFold(step.Type, "SYSTEM_MESSAGE") || strings.EqualFold(step.Source, "SYSTEM") {
							if (strings.Contains(contentLower, "<system_message>") && extractSenderUUIDFromSystemMessage(step.Content, convID) != "") ||
								hasSystemStepSubagentMarker(contentLower) ||
								hasLegacySubagentPromptPrefix(contentLower) {
								return true
							}
						}
						if hasLegacySubagentPromptPrefix(contentLower) {
							return true
						}
						if strings.EqualFold(step.Type, "USER_INPUT") || strings.EqualFold(step.Source, "USER_EXPLICIT") || strings.EqualFold(step.Source, "USER") {
							return false
						}
					}
				} else {
					lineLower := strings.ToLower(line)
					if (strings.Contains(lineLower, "<system_message>") && extractSenderUUIDFromSystemMessage(line, convID) != "") ||
						hasSystemStepSubagentMarker(lineLower) ||
						hasLegacySubagentPromptPrefix(lineLower) {
						return true
					}
				}
				break
			}
		}

		// 5. Check .system_generated/messages for dispatched parent-to-subagent invocation messages
		msgDir := filepath.Join(bDir, convID, ".system_generated", "messages")
		if entries, err := os.ReadDir(msgDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && e.Name() != "read.json" {
					if mData, mErr := readHead(filepath.Join(msgDir, e.Name()), 8*1024); mErr == nil {
						mStr := string(mData)
						if strings.Contains(mStr, "invoke_subagent") ||
							strings.Contains(mStr, "Message from Parent Agent") ||
							strings.Contains(mStr, "Message from Root Agent") {
							return true
						}
					}
				}
			}
		}
	}

	// 6. Default to false: A conversation is a primary/user conversation unless proven to be a subagent!
	return false
}
