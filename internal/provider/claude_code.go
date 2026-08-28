package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func (c *ClaudeProvider) DisplayName() string {
	return "Claude Code"
}

func (c *ClaudeProvider) BrandColor() string {
	return "#D97706"
}

func (c *ClaudeProvider) IconSVG() string {
	return `<svg class="agent-logo-svg claude-logo" viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><path clip-rule="evenodd" fill-rule="evenodd" d="M20.998 10.949H24v3.102h-3v3.028h-1.487V20H18v-2.921h-1.487V20H15v-2.921H9V20H7.488v-2.921H6V20H4.487v-2.921H3V14.05H0V10.95h3V5h17.998v5.949zM6 10.949h1.488V8.102H6v2.847zm10.51 0H18V8.102h-1.49v2.847z"/></svg>`
}

func (c *ClaudeProvider) ProcessNames() []string {
	return []string{"claude", "@anthropic-ai/claude-code"}
}

func (c *ClaudeProvider) GetSpawnCommand(tempUUID string) string {
	if tempUUID != "" && isValidUUID(tempUUID) {
		return "claude --session-id " + tempUUID
	}
	return "claude"
}

func (c *ClaudeProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" && isValidUUID(nativeID) {
		return "claude --resume " + nativeID
	}
	return "claude"
}

// Minimal JSON payload representations for Claude Code hook events
type claudePayload struct {
	SessionID            string      `json:"session_id"`
	Cwd                  string      `json:"cwd"`
	HookEventName        string      `json:"hook_event_name"`
	LastAssistantMessage string      `json:"last_assistant_message"`
	PermissionMode       string      `json:"permission_mode"`
	ToolName             string      `json:"tool_name"`
	ToolInput            interface{} `json:"tool_input"`
	RequestedPermission  string      `json:"requested_permission"`
	// For Notifications
	NotificationType string `json:"notification_type"`
	Prompt           string `json:"prompt"`
	UserPrompt       string `json:"user_prompt"`
	Title            string `json:"title"`
	CustomTitle      string `json:"custom_title"`
	AITitle          string `json:"ai_title"`
	Name             string `json:"name"`
	Summary          string `json:"summary"`
	IsSidechain      bool   `json:"is_sidechain"`
	AgentID          string `json:"agent_id"`
	Question         string `json:"question"`
	Questions        []struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	} `json:"questions"`
	Options []string `json:"options"`
}

func extractClaudeQuestionAndOptions(p *claudePayload) (string, []string) {
	var questionText string
	var optionsList []string

	// 1. Direct Question / Options fields
	if p.Question != "" {
		questionText = p.Question
	}
	if len(p.Options) > 0 {
		optionsList = append(optionsList, p.Options...)
	}

	// 2. Direct Questions array
	if len(p.Questions) > 0 {
		if questionText == "" && p.Questions[0].Question != "" {
			questionText = p.Questions[0].Question
		}
		if len(optionsList) == 0 && len(p.Questions[0].Options) > 0 {
			optionsList = append(optionsList, p.Questions[0].Options...)
		}
	}

	// 3. Inspect ToolInput
	if p.ToolInput != nil {
		switch v := p.ToolInput.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				var toolMap map[string]interface{}
				if err := json.Unmarshal([]byte(v), &toolMap); err == nil {
					q, opts := daemon.ExtractAntigravityQuestionAndOptions(toolMap)
					if questionText == "" {
						questionText = q
					}
					if len(optionsList) == 0 {
						optionsList = opts
					}
				} else if questionText == "" {
					questionText = v
				}
			}
		case map[string]interface{}:
			q, opts := daemon.ExtractAntigravityQuestionAndOptions(v)
			if questionText == "" {
				questionText = q
			}
			if len(optionsList) == 0 {
				optionsList = opts
			}
		}
	}

	return questionText, optionsList
}

func (c *ClaudeProvider) ParseHook(eventName string, payload []byte) (*daemon.Event, error) {
	var p claudePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Claude Code hook payload: %w", err)
	}

	event := &daemon.Event{
		Agent:       "claude-code",
		NativeID:    p.SessionID,
		Cwd:         p.Cwd,
		EventName:   p.HookEventName,
		LastEventAt: time.Now(),
		State:       daemon.StateWorking, // default assumption
	}

	if event.NativeID == "" {
		event.NativeID = "default"
	}

	evt := strings.ToLower(p.HookEventName)
	if evt == "" {
		evt = strings.ToLower(eventName)
	}

	// Parse custom / AI session names
	if p.CustomTitle != "" {
		event.Name = p.CustomTitle
	} else if p.AITitle != "" {
		event.Name = p.AITitle
	} else if p.Title != "" {
		event.Name = p.Title
	} else if p.Summary != "" {
		event.Name = p.Summary
	} else if p.Name != "" {
		event.Name = p.Name
	}

	switch evt {
	case "sessionstart":
		event.State = daemon.StateIdle
		event.Activity = "Session started"
		event.StartedAt = time.Now()

	case "sessionend", "stop":
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
			Kind:     daemon.BlockPermission,
			Reason:   reason,
			Question: reason,
			Options:  []string{"Allow", "Deny"},
			Since:    time.Now(),
		}
		event.Activity = "Waiting for permission: " + reason

	case "pretooluse", "pre_tool_use":
		tool := p.ToolName
		toolLower := strings.ToLower(tool)
		if tool == "AskUserQuestion" || tool == "ask_question" || tool == "AskFollowupQuestion" ||
			strings.Contains(toolLower, "question") || strings.Contains(toolLower, "askuser") {
			event.State = daemon.StateBlocked
			q, opts := extractClaudeQuestionAndOptions(&p)
			reason := "Waiting for user response"
			if q != "" {
				reason = q
			}
			event.Blocked = &daemon.Blocked{
				Kind:     daemon.BlockQuestion,
				Reason:   reason,
				Question: q,
				Options:  opts,
				Since:    time.Now(),
			}
			if q != "" {
				event.Activity = "Question: " + daemon.TruncateTitle(q)
			} else {
				event.Activity = "Waiting for user response"
			}
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
			q, opts := extractClaudeQuestionAndOptions(&p)
			reason := "Agent needs input"
			if q != "" {
				reason = q
			}
			event.Blocked = &daemon.Blocked{
				Kind:     daemon.BlockQuestion,
				Reason:   reason,
				Question: q,
				Options:  opts,
				Since:    time.Now(),
			}
			if q != "" {
				event.Activity = "Input: " + daemon.TruncateTitle(q)
			} else {
				event.Activity = "Waiting for user input"
			}
		} else if strings.Contains(notifLower, "permission") || strings.Contains(notifLower, "approval") || notifLower == "permission_prompt" || notifLower == "permission_request" {
			event.State = daemon.StateBlocked
			reason := "Permission confirmation required"
			if p.Prompt != "" {
				reason = p.Prompt
			} else if p.RequestedPermission != "" {
				reason = p.RequestedPermission
			}
			event.Blocked = &daemon.Blocked{
				Kind:     daemon.BlockPermission,
				Reason:   reason,
				Question: reason,
				Options:  []string{"Allow", "Deny"},
				Since:    time.Now(),
			}
			event.Activity = "Waiting for tool authorization"
		} else if strings.Contains(notifLower, "idle") || strings.Contains(notifLower, "prompt") || strings.Contains(notifLower, "ready") || strings.Contains(notifLower, "done") || strings.Contains(notifLower, "complete") {
			event.State = daemon.StateIdle
			event.Activity = "Awaiting user prompt"
		} else {
			event.State = daemon.StateIdle
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
	if lookPathInStandardDirs("claude") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
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
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude.json"),
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

func (c *ClaudeProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	return daemon.ReadClaudeSessionMeta(cwd, nativeID)
}

func (c *ClaudeProvider) ResolveSessionTitle(cwd, nativeID string) string {
	meta := c.ReadSessionMetadata(cwd, nativeID)
	if meta != nil {
		if meta.CustomTitle != "" {
			return meta.CustomTitle
		}
		if meta.AITitle != "" {
			return meta.AITitle
		}
		if meta.Title != "" {
			return meta.Title
		}
		if meta.FirstPrompt != "" {
			return daemon.TruncateTitle(meta.FirstPrompt)
		}
	}
	return ""
}

func (c *ClaudeProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	var targetFile string
	projectsDir := filepath.Join(home, ".claude", "projects")

	if cwd != "" {
		encodedCwd := strings.ReplaceAll(cwd, "/", "-")
		cand := filepath.Join(projectsDir, encodedCwd, nativeID+".jsonl")
		if fileExists(cand) {
			targetFile = cand
		}
	}

	if targetFile == "" && dirExists(projectsDir) {
		entries, err := os.ReadDir(projectsDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					cand := filepath.Join(projectsDir, e.Name(), nativeID+".jsonl")
					if fileExists(cand) {
						targetFile = cand
						break
					}
				}
			}
		}
	}

	if targetFile == "" {
		return nil, fmt.Errorf("claude log not found for session %s", nativeID)
	}

	file, err := os.Open(targetFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var messages []daemon.TranscriptMessage
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		msgType, _ := raw["type"].(string)
		tsStr, _ := raw["timestamp"].(string)
		ts, _ := time.Parse(time.RFC3339, tsStr)
		if ts.IsZero() {
			ts = time.Now()
		}

		if msgType == "user" {
			var fullText string
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentStr, ok := msgObj["content"].(string); ok {
					fullText = contentStr
				} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
					var textParts []string
					for _, cItem := range contentArr {
						if cMap, ok := cItem.(map[string]interface{}); ok {
							if cType, ok := cMap["type"].(string); ok && cType == "text" {
								if txt, ok := cMap["text"].(string); ok && txt != "" {
									textParts = append(textParts, txt)
								}
							}
						} else if cStr, ok := cItem.(string); ok && cStr != "" {
							textParts = append(textParts, cStr)
						}
					}
					fullText = strings.Join(textParts, "\n")
				}
			} else if contentStr, ok := raw["content"].(string); ok {
				fullText = contentStr
			} else if textStr, ok := raw["text"].(string); ok {
				fullText = textStr
			}

			fullText = strings.TrimSpace(fullText)
			if fullText != "" && !strings.Contains(fullText, "<EXTREMELY_IMPORTANT>") {
				messages = append(messages, daemon.TranscriptMessage{
					Role:      "user",
					Content:   fullText,
					Timestamp: ts,
				})
			}
		} else if msgType == "assistant" {
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentStr, ok := msgObj["content"].(string); ok && contentStr != "" {
					messages = append(messages, daemon.TranscriptMessage{
						Role:      "assistant",
						Content:   contentStr,
						Timestamp: ts,
					})
				} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
					var textParts []string
					var tools []string
					for _, cItem := range contentArr {
						if cMap, ok := cItem.(map[string]interface{}); ok {
							if cType, ok := cMap["type"].(string); ok {
								if cType == "text" {
									if txt, ok := cMap["text"].(string); ok && txt != "" {
										textParts = append(textParts, txt)
									}
								} else if cType == "tool_use" {
									tName, _ := cMap["name"].(string)
									if tName != "" {
										tools = append(tools, tName)
									}
								}
							}
						}
					}
					fullText := strings.Join(textParts, "\n")
					if fullText != "" || len(tools) > 0 {
						messages = append(messages, daemon.TranscriptMessage{
							Role:      "assistant",
							Content:   fullText,
							ToolCalls: tools,
							Timestamp: ts,
						})
					}
				}
			}
		}
	}

	return messages, nil
}

func (c *ClaudeProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if nativeID == "" {
		return nil
	}
	claudeDir := filepath.Join(home, ".claude")
	claudeProjectsDir := filepath.Join(claudeDir, "projects")
	if projDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
		for _, pDir := range projDirs {
			if pDir.IsDir() {
				projPath := filepath.Join(claudeProjectsDir, pDir.Name())
				_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("%s.jsonl", nativeID)))
				_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("agent-%s.jsonl", nativeID)))
				_ = os.RemoveAll(filepath.Join(projPath, nativeID))
			}
		}
	}

	sessionsDir := filepath.Join(claudeDir, "sessions")
	if sFiles, err := os.ReadDir(sessionsDir); err == nil {
		for _, sf := range sFiles {
			if strings.HasSuffix(sf.Name(), ".json") {
				metaPath := filepath.Join(sessionsDir, sf.Name())
				if data, err := os.ReadFile(metaPath); err == nil {
					if strings.Contains(string(data), nativeID) {
						_ = os.Remove(metaPath)
					}
				}
			}
		}
	}

	_ = os.RemoveAll(filepath.Join(claudeDir, "tasks", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "session-env", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "file-history", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "shell-snapshots", nativeID))

	return nil
}
