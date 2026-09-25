package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

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
	IsSidechainCamel bool   `json:"isSidechain"`
	AgentID          string `json:"agent_id"`
	AgentIDCamel     string `json:"agentId"`
	AgentType        string `json:"agent_type"`
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

	// Allow SubagentStart and SubagentStop through so daemon can track active subagents
	isSubagentLifecycle := strings.EqualFold(p.HookEventName, "SubagentStart") || strings.EqualFold(p.HookEventName, "SubagentStop") ||
		strings.EqualFold(eventName, "SubagentStart") || strings.EqualFold(eventName, "SubagentStop")

	// Filter out child subagents and sidechain hook events per docs/providers.md §4
	if !isSubagentLifecycle {
		if p.IsSidechain || p.IsSidechainCamel || (p.AgentID != "" && p.AgentID != "default") || (p.AgentIDCamel != "" && p.AgentIDCamel != "default") {
			return nil, nil
		}
	}

	// Filter out events with invalid or non-UUID session IDs (e.g. mock/test hooks like "test", "default")
	if !isValidUUID(p.SessionID) {
		return nil, nil
	}

	toolName := p.ToolName
	if isSubagentLifecycle && toolName == "" {
		if p.AgentType != "" {
			toolName = p.AgentType
		} else if p.AgentID != "" {
			toolName = p.AgentID
		}
	}

	event := &daemon.Event{
		Agent:       "claude-code",
		NativeID:    p.SessionID,
		Cwd:         p.Cwd,
		EventName:   p.HookEventName,
		ToolName:    toolName,
		ToolInput:   p.ToolInput,
		LastEventAt: time.Now(),
		State:       daemon.StateWorking, // default assumption
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
