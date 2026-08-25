package provider

import (
	"ackbar/internal/daemon"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
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
					q, opts := extractAntigravityQuestionAndOptions(toolMap)
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
			q, opts := extractAntigravityQuestionAndOptions(v)
			if questionText == "" {
				questionText = q
			}
			if len(optionsList) == 0 {
				optionsList = opts
			}
		}
	}

	// 4. Fallback to Prompt or UserPrompt
	if questionText == "" {
		if p.Prompt != "" {
			questionText = p.Prompt
		} else if p.UserPrompt != "" {
			questionText = p.UserPrompt
		}
	}

	return questionText, optionsList
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

	promptText := ""
	if p.Title != "" && !daemon.IsRawSessionName(p.Title) {
		promptText = p.Title
	} else if p.CustomTitle != "" {
		promptText = p.CustomTitle
	} else if p.Name != "" && !daemon.IsRawSessionName(p.Name) {
		promptText = p.Name
	}
	if promptText == "" {
		promptText = daemon.ReadClaudeSessionTitle(p.Cwd, p.SessionID)
	}
	if promptText == "" {
		promptText = p.Summary
	}
	// Only for initial session start or if no title exists anywhere, use user prompt
	if promptText == "" && (strings.EqualFold(p.HookEventName, "sessionstart") || eventName == "SessionStart") {
		if p.UserPrompt != "" {
			promptText = p.UserPrompt
		} else if p.Prompt != "" {
			promptText = p.Prompt
		}
	}

	if promptText != "" && !strings.HasPrefix(promptText, "<") && !daemon.IsRawSessionName(promptText) {
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
		} else if strings.Contains(notifLower, "permission") || strings.Contains(notifLower, "prompt") || strings.Contains(notifLower, "approval") {
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
	if lookPathInStandardDirs("claude") {
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
