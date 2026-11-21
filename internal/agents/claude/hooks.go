package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (plug *Plugin) ParseHook(eventName string, payload []byte) (*agent.Event, error) {
	var p claudePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Claude Code hook payload: %w", err)
	}
	if p.SessionID == "" && bytes.Contains(payload, []byte(`"sessionId"`)) {
		return nil, nil
	}

	isSubagentLifecycle := strings.EqualFold(p.HookEventName, "SubagentStart") || strings.EqualFold(p.HookEventName, "SubagentStop") ||
		strings.EqualFold(eventName, "SubagentStart") || strings.EqualFold(eventName, "SubagentStop")
	if !isSubagentLifecycle {
		if p.IsSidechain || p.IsSidechainCamel || (p.AgentID != "" && p.AgentID != "default") || (p.AgentIDCamel != "" && p.AgentIDCamel != "default") {
			return nil, nil
		}
	}
	if !agentutil.IsUUID(p.SessionID) {
		return nil, nil
	}

	toolName := p.ToolName
	if isSubagentLifecycle && toolName == "" {
		toolName = firstNonEmpty(p.AgentType, p.AgentID)
	}
	evtName := firstNonEmpty(p.HookEventName, eventName)

	event := &agent.Event{
		Agent:       "claude-code",
		NativeID:    p.SessionID,
		Cwd:         p.Cwd,
		EventName:   evtName,
		ToolName:    toolName,
		ToolInput:   p.ToolInput,
		LastEventAt: time.Now(),
		State:       agent.StateWorking,
		Name:        firstNonEmpty(p.CustomTitle, p.AITitle, p.Title, p.Summary, p.Name),
	}

	switch strings.ToLower(evtName) {
	case "sessionstart":
		event.State = agent.StateIdle
		switch strings.ToLower(strings.TrimSpace(p.Source)) {
		case "compact":
			event.Activity = "Context compacted"
		case "resume":
			event.Activity = "Session resumed"
		default:
			event.Activity = "Session started"
			event.StartedAt = time.Now()
		}

	case "sessionend", "stop":
		if b, act, ok := plug.rateLimitBlocked(p.LastAssistantMessage); ok {
			event.State, event.Blocked, event.Activity = agent.StateBlocked, b, act
			break
		}
		event.State = agent.StateIdle
		event.Activity = "Awaiting user prompt"

	case "userpromptsubmit", "usersubmit":
		event.State = agent.StateWorking
		event.Activity = "Processing user prompt"

	case "permissionrequest", "permission_request", "permission":
		reason := firstNonEmpty(p.RequestedPermission, "Authorize tool execution")
		event.State = agent.StateBlocked
		event.Blocked = &agent.Blocked{Kind: agent.BlockPermission, Reason: reason, Question: reason, Options: []string{"Allow", "Deny"}, Since: time.Now()}
		event.Activity = "Waiting for permission: " + reason

	case "pretooluse", "pre_tool_use":
		plug.applyPreToolUseHook(event, &p)

	case "posttooluse", "post_tool_use":
		event.State = agent.StateWorking
		event.Activity = "Tool execution completed"

	case "notification":
		if b, act, ok := plug.rateLimitBlocked(p.LastAssistantMessage); ok {
			event.State, event.Blocked, event.Activity = agent.StateBlocked, b, act
			break
		}
		applyNotificationHook(event, &p)

	default:
		if p.LastAssistantMessage != "" {
			event.Activity = p.LastAssistantMessage
			if len(event.Activity) > 80 {
				event.Activity = event.Activity[:77] + "..."
			}
		} else {
			event.Activity = "Event: " + evtName
		}
	}
	return event, nil
}

func (*Plugin) applyPreToolUseHook(event *agent.Event, p *claudePayload) {
	toolLower := strings.ToLower(p.ToolName)
	if toolLower == "exitplanmode" {
		event.State = agent.StateBlocked
		event.Blocked = &agent.Blocked{
			Kind: agent.BlockApproval, Reason: extractPlanText(p.ToolInput), Question: "Approve implementation plan?",
			Options: []string{"Approve Plan", "Request Changes"}, Since: time.Now(),
		}
		event.Activity = "Waiting for plan approval"
		return
	}
	if p.ToolName == "AskUserQuestion" || p.ToolName == "ask_question" || p.ToolName == "AskFollowupQuestion" ||
		strings.Contains(toolLower, "question") || strings.Contains(toolLower, "askuser") {
		q, opts := extractPayloadQuestionAndOptions(p)
		reason := "Waiting for user response"
		event.Activity = "Waiting for user response"
		if q != "" {
			reason = q
			event.Activity = "Question: " + agentutil.TruncateTitle(q, 50)
		}
		event.State = agent.StateBlocked
		event.Blocked = &agent.Blocked{
			Kind: agent.BlockQuestion, Reason: reason, Question: q, Options: opts, Questions: extractPayloadQuestions(p), Since: time.Now(),
		}
		return
	}
	event.State = agent.StateWorking
	event.Activity = "Running tool: " + p.ToolName
}

func applyNotificationHook(event *agent.Event, p *claudePayload) {
	notifLower := strings.ToLower(p.NotificationType)
	switch {
	case strings.Contains(notifLower, "input") || strings.Contains(notifLower, "question"):
		q, opts := extractPayloadQuestionAndOptions(p)
		reason := "Agent needs input"
		event.Activity = "Waiting for user input"
		if q != "" {
			reason = q
			event.Activity = "Input: " + agentutil.TruncateTitle(q, 50)
		}
		event.State = agent.StateBlocked
		event.Blocked = &agent.Blocked{Kind: agent.BlockQuestion, Reason: reason, Question: q, Options: opts, Questions: extractPayloadQuestions(p), Since: time.Now()}
	case strings.Contains(notifLower, "permission") || strings.Contains(notifLower, "approval") || notifLower == "permission_prompt" || notifLower == "permission_request":
		reason := firstNonEmpty(p.Prompt, p.RequestedPermission, "Permission confirmation required")
		event.State = agent.StateBlocked
		event.Blocked = &agent.Blocked{Kind: agent.BlockPermission, Reason: reason, Question: reason, Options: []string{"Allow", "Deny"}, Since: time.Now()}
		event.Activity = "Waiting for tool authorization"
	case strings.Contains(notifLower, "idle") || strings.Contains(notifLower, "prompt") || strings.Contains(notifLower, "ready") || strings.Contains(notifLower, "done") || strings.Contains(notifLower, "complete"):
		event.State = agent.StateIdle
		event.Activity = "Awaiting user prompt"
	default:
		event.State = agent.StateIdle
		event.Activity = "Notification: " + p.NotificationType
	}
}
