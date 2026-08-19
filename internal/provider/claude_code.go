package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"ackbar/internal/daemon"
)

type ClaudeProvider struct{}

func NewClaudeProvider() *ClaudeProvider {
	return &ClaudeProvider{}
}

func (c *ClaudeProvider) Agent() string {
	return "claude-code"
}

// Minimal JSON payload representations for Claude Code hook events
type claudePayload struct {
	SessionID            string `json:"session_id"`
	Cwd                  string `json:"cwd"`
	HookEventName        string `json:"hook_event_name"`
	LastAssistantMessage string `json:"last_assistant_message"`
	PermissionMode       string `json:"permission_mode"`
	ToolName            string `json:"tool_name"`
	RequestedPermission string `json:"requested_permission"`
	// For Notifications
	NotificationType    string `json:"notification_type"`
	Prompt           string `json:"prompt"`
	UserPrompt       string `json:"user_prompt"`
	Title            string `json:"title"`
	Name             string `json:"name"`
	Summary          string `json:"summary"`
	IsSidechain      bool   `json:"is_sidechain"`
	AgentID          string `json:"agent_id"`
}

func (c *ClaudeProvider) ParseHook(eventName string, payload []byte) (*daemon.Event, error) {
	var p claudePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Claude hook payload: %w", err)
	}

	// Filter out subagents (sidechain agents)
	if p.IsSidechain || p.AgentID != "" {
		return nil, nil
	}

	// Default values
	event := &daemon.Event{
		Agent:       "claude-code",
		NativeID:    p.SessionID,
		Cwd:         p.Cwd,
		EventName:   p.HookEventName,
		LastEventAt: time.Now(),
		State:       daemon.StateWorking, // default assumption while active
	}

	promptText := p.Title
	if promptText == "" {
		promptText = p.Name
	}
	if promptText == "" {
		promptText = p.UserPrompt
	}
	if promptText == "" {
		promptText = p.Prompt
	}
	if promptText == "" {
		promptText = p.Summary
	}
	if promptText == "" {
		promptText = ReadClaudeSessionTitle(p.Cwd, p.SessionID)
	}

	if promptText != "" && !strings.HasPrefix(promptText, "<") {
		firstLine := strings.Split(strings.TrimSpace(promptText), "\n")[0]
		if len(firstLine) > 50 {
			firstLine = firstLine[:47] + "..."
		}
		event.Name = firstLine
	}

	if event.NativeID == "" {
		event.NativeID = "default" // fallback if not supplied
	}

	// Normalize hook event name
	evt := strings.ToLower(p.HookEventName)
	if evt == "" {
		evt = strings.ToLower(eventName)
	}

	switch evt {
	case "sessionstart":
		event.State = daemon.StateIdle
		event.Activity = "Session started"
		event.StartedAt = time.Now()

	case "sessionend", "exit":
		event.State = daemon.StateEnded
		event.Activity = "Session ended"

	case "stop":
		// Stop in Claude Code represents completion of the current assistant turn (awaiting next user prompt)
		event.State = daemon.StateIdle
		event.Activity = "Awaiting user prompt"

	case "userpromptsubmit", "usersubmit":
		event.State = daemon.StateWorking
		event.Activity = "Processing user prompt"

	case "permissionrequest", "permission_request", "permission":
		event.State = daemon.StateBlocked
		reason := p.RequestedPermission
		if reason == "" {
			reason = "Authorize tool execution"
		}
		event.Blocked = &daemon.Blocked{
			Kind:   daemon.BlockPermission,
			Reason: reason,
			Since:  time.Now(),
		}
		event.Activity = "Waiting for permission: " + reason

	case "pretooluse", "pre_tool_use":
		tool := p.ToolName
		toolLower := strings.ToLower(tool)
		if tool == "AskUserQuestion" || tool == "ask_question" || tool == "AskFollowupQuestion" ||
			strings.Contains(toolLower, "question") || strings.Contains(toolLower, "askuser") {
			event.State = daemon.StateBlocked
			event.Blocked = &daemon.Blocked{
				Kind:   daemon.BlockQuestion,
				Reason: "Waiting for user response",
				Since:  time.Now(),
			}
			event.Activity = "Waiting for user response"
		} else {
			event.State = daemon.StateWorking
			event.Activity = "Running tool: " + tool
		}

	case "posttooluse", "post_tool_use":
		event.State = daemon.StateWorking
		event.Activity = "Tool execution completed"

	case "notification":
		notifLower := strings.ToLower(p.NotificationType)
		if strings.Contains(notifLower, "input") || strings.Contains(notifLower, "question") {
			event.State = daemon.StateBlocked
			event.Blocked = &daemon.Blocked{
				Kind:   daemon.BlockQuestion,
				Reason: "Agent needs input",
				Since:  time.Now(),
			}
			event.Activity = "Waiting for user input"
		} else if strings.Contains(notifLower, "permission") || strings.Contains(notifLower, "prompt") || strings.Contains(notifLower, "approval") {
			event.State = daemon.StateBlocked
			event.Blocked = &daemon.Blocked{
				Kind:   daemon.BlockPermission,
				Reason: "Permission confirmation required",
				Since:  time.Now(),
			}
			event.Activity = "Waiting for tool authorization"
		} else {
			event.State = daemon.StateWorking
			event.Activity = "Notification: " + p.NotificationType
		}

	default:
		// Fallback activity logging
		if p.LastAssistantMessage != "" {
			event.Activity = p.LastAssistantMessage
			if len(event.Activity) > 80 {
				event.Activity = event.Activity[:77] + "..."
			}
		} else {
			event.Activity = "Event: " + p.HookEventName
		}
	}

	return event, nil
}

func (c *ClaudeProvider) IsInstalled() bool {
	if _, err := exec.LookPath("claude"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(home + "/.claude"); err == nil {
			return true
		}
	}
	return false
}

func (c *ClaudeProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "go run ./cmd/ackbar setup-hooks  # (or edit ~/.claude/settings.json)"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	paths := []string{
		home + "/.claude/settings.json",
		home + "/.claude.json",
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			if strings.Contains(string(data), "127.0.0.1:7777") || strings.Contains(string(data), "localhost:7777") || strings.Contains(string(data), "ackbar-hook") {
				return true, setupCmd, nil
			}
		}
	}

	return false, setupCmd, nil
}

type SessionMeta struct {
	Title         string
	Entrypoint    string
	Kind          string
	Version       string
	ContextPct    int
	LastMessageAt time.Time
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

	encodedCwd := strings.ReplaceAll(cwd, "/", "-")
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
	if strings.HasPrefix(sessionID, "proc-") {
		targetID = ""
	}

	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if files, err := os.ReadDir(sessionsDir); err == nil {
		found := false
		if targetID != "" {
			for _, f := range files {
				if strings.HasSuffix(f.Name(), ".json") {
					metaPath := filepath.Join(sessionsDir, f.Name())
					if data, err := os.ReadFile(metaPath); err == nil {
						var meta struct {
							SessionID  string `json:"sessionId"`
							Entrypoint string `json:"entrypoint"`
							Kind       string `json:"kind"`
							Version    string `json:"version"`
						}
						if err := json.Unmarshal(data, &meta); err == nil && meta.SessionID == targetID {
							if meta.Entrypoint != "" {
								metaInfo.Entrypoint = meta.Entrypoint
							}
							if meta.Kind != "" {
								metaInfo.Kind = meta.Kind
							}
							if meta.Version != "" {
								metaInfo.Version = meta.Version
							}
							found = true
							break
						}
					}
				}
			}
		}
		if !found && cwd != "" {
			for _, f := range files {
				if strings.HasSuffix(f.Name(), ".json") {
					metaPath := filepath.Join(sessionsDir, f.Name())
					if data, err := os.ReadFile(metaPath); err == nil {
						var meta struct {
							Cwd        string `json:"cwd"`
							Entrypoint string `json:"entrypoint"`
							Kind       string `json:"kind"`
							Version    string `json:"version"`
						}
						if err := json.Unmarshal(data, &meta); err == nil && (meta.Cwd == cwd || strings.HasSuffix(cwd, meta.Cwd) || strings.HasSuffix(meta.Cwd, cwd)) {
							if meta.Entrypoint != "" {
								metaInfo.Entrypoint = meta.Entrypoint
							}
							if meta.Kind != "" {
								metaInfo.Kind = meta.Kind
							}
							if meta.Version != "" {
								metaInfo.Version = meta.Version
							}
							break
						}
					}
				}
			}
		}
	}

	if cwd != "" {
		encodedCwd := strings.ReplaceAll(cwd, "/", "-")
		projDir := filepath.Join(home, ".claude", "projects", encodedCwd)
		if files, err := os.ReadDir(projDir); err == nil {
			for _, f := range files {
				if strings.HasSuffix(f.Name(), ".jsonl") {
					if targetID == "" || strings.Contains(f.Name(), targetID) {
						if info, err := f.Info(); err == nil {
							if metaInfo.LastMessageAt.IsZero() || info.ModTime().After(metaInfo.LastMessageAt) {
								metaInfo.LastMessageAt = info.ModTime()
							}
						}
					}
				}
			}
		}
	}

	return metaInfo
}

func ReadAntigravitySessionTitle(cwd, sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	targetID := sessionID
	if strings.HasPrefix(sessionID, "proc-") {
		targetID = ""
	}

	// 1. Direct Lookup: Check ~/.gemini/antigravity/annotations/<sessionID>.pbtxt
	if targetID != "" {
		annoPath := filepath.Join(home, ".gemini", "antigravity", "annotations", targetID+".pbtxt")
		if data, err := os.ReadFile(annoPath); err == nil {
			content := string(data)
			if idx := strings.Index(content, `title:"`); idx != -1 {
				sub := content[idx+len(`title:"`):]
				if endIdx := strings.Index(sub, `"`); endIdx != -1 {
					return sub[:endIdx]
				}
			}
		}
	}

	// 2. Check brain task summary fallback
	if targetID != "" {
		metaPath := filepath.Join(home, ".gemini", "antigravity", "brain", targetID, "task.md.metadata.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(data, &meta); err == nil && meta.Summary != "" {
				return truncateTitle(meta.Summary)
			}
		}
	}

	return ""
}

var (
	titleCacheMutex sync.RWMutex
	titleCache      = make(map[string]string)
)

func ReadClaudeSessionTitle(cwd, sessionID string) string {
	cacheKey := fmt.Sprintf("%s:%s", cwd, sessionID)
	titleCacheMutex.RLock()
	if cached, ok := titleCache[cacheKey]; ok && cached != "" {
		titleCacheMutex.RUnlock()
		return cached
	}
	titleCacheMutex.RUnlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	targetID := sessionID
	if strings.HasPrefix(sessionID, "proc-") {
		targetID = ""
	}

	var title string

	// Direct Lookup 0: Check ~/.claude/sessions/*.json session registry files
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if files, err := os.ReadDir(sessionsDir); err == nil {
		// Pass 0a: Exact SessionID match first
		if targetID != "" {
			for _, f := range files {
				if strings.HasSuffix(f.Name(), ".json") {
					metaPath := filepath.Join(sessionsDir, f.Name())
					if data, err := os.ReadFile(metaPath); err == nil {
						var meta struct {
							SessionID string `json:"sessionId"`
							Name      string `json:"name"`
						}
						if err := json.Unmarshal(data, &meta); err == nil && meta.SessionID == targetID && meta.Name != "" {
							title = meta.Name
							break
						}
					}
				}
			}
		}
		// Pass 0b: CWD fallback only if no exact SessionID match
		if title == "" && cwd != "" {
			for _, f := range files {
				if strings.HasSuffix(f.Name(), ".json") {
					metaPath := filepath.Join(sessionsDir, f.Name())
					if data, err := os.ReadFile(metaPath); err == nil {
						var meta struct {
							Cwd  string `json:"cwd"`
							Name string `json:"name"`
						}
						if err := json.Unmarshal(data, &meta); err == nil && meta.Name != "" {
							if meta.Cwd == cwd || strings.HasSuffix(cwd, meta.Cwd) || strings.HasSuffix(meta.Cwd, cwd) {
								title = meta.Name
								break
							}
						}
					}
				}
			}
		}
	}

	if title == "" {
		var firstPrompt string
		var aiTitle string

		// Pass 1: Check ~/.claude/history.jsonl for explicit display_name or prompts
		historyPath := filepath.Join(home, ".claude", "history.jsonl")
		if data, err := os.ReadFile(historyPath); err == nil {
			lines := strings.Split(string(data), "\n")
			// 1a. First pass: look specifically for custom DisplayName
			for i := len(lines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(lines[i])
				if line == "" {
					continue
				}
				var entry struct {
					SessionID      string `json:"sessionId"`
					SessionIDOld   string `json:"session_id"`
					DisplayName    string `json:"displayName"`
					DisplayNameOld string `json:"display_name"`
					CustomTitle    string `json:"customTitle"`
					Title          string `json:"title"`
					ProjectPath    string `json:"project"`
					ProjectPathOld string `json:"project_path"`
				}
				if err := json.Unmarshal([]byte(line), &entry); err == nil {
					sID := entry.SessionID
					if sID == "" {
						sID = entry.SessionIDOld
					}
					pPath := entry.ProjectPath
					if pPath == "" {
						pPath = entry.ProjectPathOld
					}
					dName := entry.DisplayName
					if dName == "" {
						dName = entry.DisplayNameOld
					}
					if dName == "" {
						dName = entry.CustomTitle
					}
					if dName == "" {
						dName = entry.Title
					}

					matched := false
					if targetID != "" && sID == targetID {
						matched = true
					} else if cwd != "" && pPath != "" && (pPath == cwd || strings.HasSuffix(cwd, pPath) || strings.HasSuffix(pPath, cwd)) {
						matched = true
					}
					if matched && dName != "" {
						title = dName
						break
					}
				}
			}

			// 1b. Top-to-bottom pass for initial prompt if no custom title
			if title == "" {
				for i := 0; i < len(lines); i++ {
					line := strings.TrimSpace(lines[i])
					if line == "" {
						continue
					}
					var entry struct {
						SessionID      string `json:"sessionId"`
						SessionIDOld   string `json:"session_id"`
						ProjectPath    string `json:"project"`
						ProjectPathOld string `json:"project_path"`
						Display        string `json:"display"`
						Prompt         string `json:"prompt"`
					}
					if err := json.Unmarshal([]byte(line), &entry); err == nil {
						sID := entry.SessionID
						if sID == "" {
							sID = entry.SessionIDOld
						}
						pPath := entry.ProjectPath
						if pPath == "" {
							pPath = entry.ProjectPathOld
						}
						promptStr := entry.Display
						if promptStr == "" {
							promptStr = entry.Prompt
						}

						matched := false
						if targetID != "" && sID == targetID {
							matched = true
						} else if cwd != "" && pPath != "" && (pPath == cwd || strings.HasSuffix(cwd, pPath) || strings.HasSuffix(pPath, cwd)) {
							matched = true
						}
						if matched && promptStr != "" {
							firstPrompt = promptStr
							break
						}
					}
				}
			}
		}

		// Pass 2: Check target project directory in ~/.claude/projects/ using bounded 32KB read
		if title == "" && cwd != "" {
			encodedCwd := strings.ReplaceAll(cwd, "/", "-")
			projDir := filepath.Join(home, ".claude", "projects", encodedCwd)
			if files, err := os.ReadDir(projDir); err == nil {
				for _, f := range files {
					if strings.HasSuffix(f.Name(), ".jsonl") {
						if targetID == "" || strings.Contains(f.Name(), targetID) {
							filePath := filepath.Join(projDir, f.Name())
							file, err := os.Open(filePath)
							if err == nil {
								buf := make([]byte, 32768)
								n, _ := io.ReadFull(file, buf)
								file.Close()
								jsonLines := strings.Split(string(buf[:n]), "\n")
								for i := 0; i < len(jsonLines); i++ {
									line := strings.TrimSpace(jsonLines[i])
									if line == "" {
										continue
									}
									var entry struct {
										Type        string `json:"type"`
										Name        string `json:"name"`
										DisplayName string `json:"display_name"`
										AITitle     string `json:"aiTitle"`
										LastPrompt  string `json:"lastPrompt"`
										Prompt      string `json:"prompt"`
										Display     string `json:"display"`
										Message     *struct {
											Text string `json:"text"`
										} `json:"message"`
									}
									if err := json.Unmarshal([]byte(line), &entry); err == nil {
										if entry.Type == "rename" && entry.Name != "" {
											title = entry.Name
											break
										}
										if entry.DisplayName != "" {
											title = entry.DisplayName
											break
										}
										if entry.Type == "ai-title" && entry.AITitle != "" && aiTitle == "" {
											aiTitle = entry.AITitle
										}
										if firstPrompt == "" {
											if entry.LastPrompt != "" {
												firstPrompt = entry.LastPrompt
											} else if entry.Display != "" {
												firstPrompt = entry.Display
											} else if entry.Message != nil && entry.Message.Text != "" {
												firstPrompt = entry.Message.Text
											} else if entry.Prompt != "" {
												firstPrompt = entry.Prompt
											}
										}
									}
								}
							}
						}
					}
					if title != "" {
						break
					}
				}
			}
		}

		if title == "" {
			if aiTitle != "" {
				title = aiTitle
			} else if firstPrompt != "" {
				title = truncateTitle(firstPrompt)
			}
		}
	}

	if title != "" {
		titleCacheMutex.Lock()
		titleCache[cacheKey] = title
		titleCacheMutex.Unlock()
	}

	return title
}

func truncateTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	cleanLines := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			cleanLines = append(cleanLines, l)
		}
	}
	if len(cleanLines) == 0 {
		return ""
	}
	res := cleanLines[0]
	if len(res) < 6 && len(cleanLines) > 1 {
		res = res + " " + cleanLines[1]
	}
	if len(res) > 50 {
		return res[:47] + "..."
	}
	return res
}
