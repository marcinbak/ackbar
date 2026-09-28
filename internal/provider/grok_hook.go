package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

// ParseHook parses incoming hook events from the Grok CLI into canonical Ackbar Events.
func (g *GrokProvider) ParseHook(eventName string, payload []byte) (*daemon.Event, error) {
	var raw map[string]interface{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil, fmt.Errorf("failed to unmarshal grok hook JSON: %w", err)
		}
	} else {
		raw = make(map[string]interface{})
	}

	ev := &daemon.Event{
		Agent:       "grok",
		LastEventAt: time.Now(),
		State:       daemon.StateWorking,
	}

	// Resolve event name (from argument or payload)
	resolvedEvent := eventName
	if resolvedEvent == "" {
		if evVal, ok := raw["event"].(string); ok && evVal != "" {
			resolvedEvent = evVal
		} else if evVal, ok := raw["type"].(string); ok && evVal != "" {
			resolvedEvent = evVal
		} else if hookVal, ok := raw["hook"].(string); ok && hookVal != "" {
			resolvedEvent = hookVal
		}
	}
	ev.EventName = resolvedEvent

	// Extract session ID
	if sid, ok := raw["session_id"].(string); ok && sid != "" {
		ev.NativeID = sid
	} else if sid, ok := raw["id"].(string); ok && sid != "" {
		ev.NativeID = sid
	}
	if ev.NativeID == "" {
		ev.NativeID = "default"
	}

	// Extract cwd
	if cwd, ok := raw["cwd"].(string); ok && cwd != "" {
		ev.Cwd = cwd
	}

	// Event-specific mapping
	lowerEvent := strings.ToLower(resolvedEvent)
	switch {
	case strings.Contains(lowerEvent, "prompt") || lowerEvent == "userpromptsubmit":
		ev.State = daemon.StateWorking
		ev.Activity = "Processing prompt"
		if prompt, ok := raw["prompt"].(string); ok && prompt != "" {
			ev.Activity = "Processing: " + daemon.TruncateTitle(prompt)
		} else if text, ok := raw["text"].(string); ok && text != "" {
			ev.Activity = "Processing: " + daemon.TruncateTitle(text)
		}

	case strings.Contains(lowerEvent, "pretool") || strings.Contains(lowerEvent, "tool_start") || strings.Contains(lowerEvent, "tool_use"):
		ev.State = daemon.StateWorking
		toolName := "tool"
		if name, ok := raw["tool"].(string); ok && name != "" {
			toolName = name
		} else if name, ok := raw["name"].(string); ok && name != "" {
			toolName = name
		} else if toolObj, ok := raw["tool"].(map[string]interface{}); ok {
			if name, ok := toolObj["name"].(string); ok && name != "" {
				toolName = name
			}
		}
		ev.ToolName = toolName
		ev.ToolInput = raw["input"]
		ev.Activity = "Running " + toolName

	case strings.Contains(lowerEvent, "posttool") || strings.Contains(lowerEvent, "tool_end") || strings.Contains(lowerEvent, "tool_result"):
		ev.State = daemon.StateWorking
		ev.Activity = "Tool execution completed"

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

	case strings.Contains(lowerEvent, "question") || strings.Contains(lowerEvent, "input"):
		ev.State = daemon.StateBlocked
		question := "Waiting for user input"
		if q, ok := raw["question"].(string); ok && q != "" {
			question = q
		}
		ev.Blocked = &daemon.Blocked{
			Kind:     daemon.BlockQuestion,
			Reason:   question,
			Question: question,
			Options:  nil,
			Since:    time.Now(),
		}
		ev.Activity = "Waiting for user input"

	case strings.Contains(lowerEvent, "stop") || strings.Contains(lowerEvent, "done") || strings.Contains(lowerEvent, "idle"):
		ev.State = daemon.StateIdle
		ev.Activity = "Awaiting user prompt"

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
