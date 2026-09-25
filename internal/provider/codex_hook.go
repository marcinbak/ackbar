package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

type codexPayload struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Model          string `json:"model"`
	PermissionMode string `json:"permission_mode"`
	ToolName       string `json:"tool_name"`
	ToolInput      string `json:"tool_input"`
}

func (c *CodexProvider) ParseHook(eventName string, payload []byte) (*daemon.Event, error) {
	var p codexPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Codex hook payload: %w", err)
	}

	event := &daemon.Event{
		Agent:       "codex",
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

	switch evt {
	case "sessionstart":
		event.State = daemon.StateIdle
		event.Activity = "Session started"
		event.StartedAt = time.Now()

	case "sessionend", "stop":
		event.State = daemon.StateEnded
		event.Activity = "Session ended"

	case "userpromptsubmit":
		event.State = daemon.StateWorking
		event.Activity = "Processing user prompt"

	case "permissionrequest":
		event.State = daemon.StateBlocked
		reason := "Authorize tool execution"
		if p.ToolName != "" {
			reason = "Allow running tool: " + p.ToolName
		}
		event.Blocked = &daemon.Blocked{
			Kind:     daemon.BlockPermission,
			Reason:   reason,
			Question: reason,
			Options:  []string{"Allow", "Deny"},
			Since:    time.Now(),
		}
		event.Activity = "Waiting for permission"

	case "pretooluse":
		if p.ToolName == "request_user_input" || p.ToolName == "ask_question" {
			event.State = daemon.StateBlocked
			var q string
			var opts []string
			if p.ToolInput != "" {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(p.ToolInput), &data); err == nil {
					q, opts = daemon.ExtractAntigravityQuestionAndOptions(data)
				} else {
					q = p.ToolInput
				}
			}
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
			event.Activity = "Waiting for user input"
		} else {
			event.State = daemon.StateWorking
			event.Activity = "Running tool: " + p.ToolName
		}

	case "posttooluse":
		event.State = daemon.StateWorking
		event.Activity = "Tool execution completed"

	default:
		event.Activity = "Event: " + p.HookEventName
	}

	return event, nil
}
