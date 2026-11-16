package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

func (g *GrokProvider) ParseHook(eventName string, payload []byte) (*daemon.Event, error) {
	var raw map[string]interface{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil, fmt.Errorf("failed to unmarshal grok hook JSON: %w", err)
		}
	} else {
		raw = make(map[string]interface{})
	}

	resolvedEvent := eventName
	if resolvedEvent == "" {
		for _, k := range []string{"hookEventName", "hook_event_name", "event", "type", "hook"} {
			if s, ok := raw[k].(string); ok && s != "" {
				resolvedEvent = s
				break
			}
		}
	}

	subType := ""
	if s, ok := raw["subagentType"].(string); ok && s != "" {
		subType = s
	} else if s, ok := raw["subagent_type"].(string); ok && s != "" {
		subType = s
	}

	lowerEvent := strings.ToLower(resolvedEvent)
	if subType != "" {
		switch lowerEvent {
		case "subagent_start", "subagentstart", "subagent_stop", "subagentstop", "subagent_end", "subagentend":
		default:
			return nil, nil
		}
	}

	sid := ""
	for _, k := range []string{"sessionId", "session_id", "id"} {
		if s, ok := raw[k].(string); ok && s != "" {
			sid = s
			break
		}
	}
	if sid == "" || !daemon.IsSafeGrokSessionID(sid) {
		return nil, nil
	}

	ev := &daemon.Event{
		Agent:       "grok",
		NativeID:    sid,
		EventName:   resolvedEvent,
		LastEventAt: time.Now(),
		State:       daemon.StateWorking,
	}

	if cwd, ok := raw["cwd"].(string); ok && cwd != "" {
		ev.Cwd = cwd
	}
	if wsRoot, ok := raw["workspaceRoot"].(string); ok && wsRoot != "" {
		ev.Roots = []string{wsRoot}
		if ev.Cwd == "" {
			ev.Cwd = wsRoot
		}
	}

	switch {
	case lowerEvent == "subagent_start" || lowerEvent == "subagentstart":
		ev.EventName = "SubagentStart"
		ev.State = daemon.StateWorking
		ev.ToolName = subType
		ev.Activity = "Subagent started: " + subType

	case lowerEvent == "subagent_stop" || lowerEvent == "subagentstop" || lowerEvent == "subagent_end" || lowerEvent == "subagentend":
		ev.EventName = "SubagentStop"
		ev.State = daemon.StateWorking
		ev.ToolName = subType
		ev.Activity = "Subagent stopped"

	case lowerEvent == "session_end" || lowerEvent == "sessionend":
		ev.State = daemon.StateEnded
		ev.Activity = "Session ended"

	case strings.Contains(lowerEvent, "prompt") || lowerEvent == "userpromptsubmit":
		ev.State = daemon.StateWorking
		ev.Activity = "Processing prompt"
		if prompt, ok := raw["prompt"].(string); ok && prompt != "" {
			ev.Activity = "Processing: " + daemon.TruncateTitle(prompt)
		} else if text, ok := raw["text"].(string); ok && text != "" {
			ev.Activity = "Processing: " + daemon.TruncateTitle(text)
		}

	case strings.Contains(lowerEvent, "pretool") || strings.Contains(lowerEvent, "tool_start") || strings.Contains(lowerEvent, "tool_use"):
		toolName := "tool"
		for _, k := range []string{"toolName", "tool_name", "tool", "name"} {
			if s, ok := raw[k].(string); ok && s != "" {
				toolName = s
				break
			}
		}
		if toolName == "tool" {
			if tObj, ok := raw["tool"].(map[string]interface{}); ok {
				if s, ok := tObj["name"].(string); ok && s != "" {
					toolName = s
				}
			}
		}
		toolInput := raw["toolInput"]
		if toolInput == nil {
			toolInput = raw["tool_input"]
		}
		if toolInput == nil {
			toolInput = raw["input"]
		}

		if strings.EqualFold(toolName, "AskUserQuestion") || strings.EqualFold(toolName, "AskQuestion") ||
			strings.EqualFold(toolName, "ask_question") || strings.EqualFold(toolName, "ask_user") {
			ev.State = daemon.StateBlocked
			qText := "Waiting for user input"
			if s, ok := toolInput.(string); ok && s != "" {
				qText = s
			} else if m, ok := toolInput.(map[string]interface{}); ok {
				if q, ok := m["question"].(string); ok && q != "" {
					qText = q
				}
			}
			ev.Blocked = &daemon.Blocked{
				Kind:     daemon.BlockQuestion,
				Reason:   qText,
				Question: qText,
				Since:    time.Now(),
			}
			ev.Activity = "Waiting for user input"
		} else {
			ev.State = daemon.StateWorking
			ev.ToolName = toolName
			ev.ToolInput = toolInput
			ev.Activity = "Running " + daemon.FormatToolDisplay(toolName, toolInput)
		}

	case strings.Contains(lowerEvent, "posttool") || strings.Contains(lowerEvent, "tool_end") || strings.Contains(lowerEvent, "tool_result"):
		ev.State = daemon.StateWorking
		ev.Activity = "Tool execution completed"

	case lowerEvent == "notification":
		notifType := ""
		if s, ok := raw["notificationType"].(string); ok && s != "" {
			notifType = s
		} else if s, ok := raw["notification_type"].(string); ok && s != "" {
			notifType = s
		}
		if notifType == "" {
			msg, _ := raw["message"].(string)
			if strings.Contains(strings.ToLower(msg), "permission") {
				notifType = "permission_prompt"
			}
		}
		switch notifType {
		case "permission_prompt":
			ev.State = daemon.StateBlocked
			reason := "Tool permission requested"
			for _, k := range []string{"reason", "message", "question"} {
				if r, ok := raw[k].(string); ok && r != "" {
					reason = r
					break
				}
			}
			ev.Blocked = &daemon.Blocked{
				Kind:     daemon.BlockPermission,
				Reason:   reason,
				Question: "Permission required",
				Options:  []string{"Allow", "Deny"},
				Since:    time.Now(),
			}
			ev.Activity = "Waiting for permission: " + daemon.TruncateTitle(reason)
		case "idle_prompt", "task_complete":
			ev.State = daemon.StateIdle
			ev.Activity = "Awaiting user prompt"
		default:
			return nil, nil
		}

	case strings.Contains(lowerEvent, "permission") || strings.Contains(lowerEvent, "approval") || strings.Contains(lowerEvent, "confirm"):
		ev.State = daemon.StateBlocked
		reason := "Tool permission requested"
		if r, ok := raw["reason"].(string); ok && r != "" {
			reason = r
		}
		question := "Permission required"
		if q, ok := raw["question"].(string); ok && q != "" {
			question = q
		}
		ev.Blocked = &daemon.Blocked{
			Kind:     daemon.BlockPermission,
			Reason:   reason,
			Question: question,
			Options:  []string{"Allow", "Deny"},
			Since:    time.Now(),
		}
		ev.Activity = "Waiting for permission: " + daemon.TruncateTitle(reason)

	case lowerEvent == "stop_failure" || lowerEvent == "stopfailure":
		errType := ""
		for _, k := range []string{"error", "errorType", "error_type"} {
			if s, ok := raw[k].(string); ok && s != "" {
				errType = s
				break
			}
		}
		errLower := strings.ToLower(errType)
		if strings.Contains(errLower, "rate_limit") {
			reason := "Rate limit reached"
			for _, k := range []string{"errorDetails", "message", "error_details"} {
				if s, ok := raw[k].(string); ok && s != "" {
					reason = s
					break
				}
			}
			ev.State = daemon.StateBlocked
			ev.Blocked = &daemon.Blocked{
				Kind:   daemon.BlockRateLimit,
				Reason: reason,
				Since:  time.Now(),
			}
			ev.Activity = "Rate limit reached"
		} else if strings.Contains(errLower, "auth") {
			ev.State = daemon.StateBlocked
			ev.Blocked = &daemon.Blocked{
				Kind:   daemon.BlockAuth,
				Reason: "Authentication failed — run grok login",
				Since:  time.Now(),
			}
			ev.Activity = "Authentication failed — run grok login"
		} else {
			ev.State = daemon.StateIdle
			ev.Activity = "Turn stopped with error"
		}

	case strings.Contains(lowerEvent, "stop") || strings.Contains(lowerEvent, "done") || strings.Contains(lowerEvent, "idle"):
		reason := ""
		for _, k := range []string{"reason", "stopReason", "stop_reason"} {
			if s, ok := raw[k].(string); ok && s != "" {
				reason = s
				break
			}
		}
		rLower := strings.ToLower(reason)
		if rLower == "channel_closed" || rLower == "shutdown" {
			ev.State = daemon.StateEnded
			ev.Activity = "Session ended"
		} else {
			ev.State = daemon.StateIdle
			ev.Activity = "Awaiting user prompt"
		}

	case strings.Contains(lowerEvent, "start") || strings.Contains(lowerEvent, "init"):
		ev.State = daemon.StateIdle
		ev.Activity = "Session started"
		ev.StartedAt = time.Now()

	default:
		ev.State = daemon.StateWorking
		ev.Activity = "Working"
	}

	return ev, nil
}
