package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) CheckHookConfig(homeDir string) (bool, string, error) {
	setupCmd := "ackbar-hook --agent=opencode"
	ocDir := HomeDir(homeDir)
	for _, path := range []string{
		filepath.Join(ocDir, "hooks.json"),
		filepath.Join(ocDir, "config.json"),
		filepath.Join(ocDir, "config.toml"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(content, "127.0.0.1:7777") || strings.Contains(content, "localhost:7777") || strings.Contains(content, "ackbar-hook") {
			return true, setupCmd, nil
		}
	}
	return false, setupCmd, nil
}

func (*Plugin) HookAckPayload(eventName string) []byte {
	_ = eventName
	return []byte("{}")
}

func (*Plugin) ParseHook(eventName string, payload []byte) (*agent.Event, error) {
	var raw map[string]interface{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil, fmt.Errorf("failed to unmarshal opencode hook JSON: %w", err)
		}
	} else {
		raw = make(map[string]interface{})
	}

	ev := &agent.Event{
		Agent:       "opencode",
		LastEventAt: time.Now(),
		State:       agent.StateWorking,
	}

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

	if sid, ok := raw["session_id"].(string); ok && sid != "" {
		ev.NativeID = sid
	} else if sid, ok := raw["id"].(string); ok && sid != "" {
		ev.NativeID = sid
	}
	if ev.NativeID == "" {
		ev.NativeID = "default"
	}

	if cwd, ok := raw["cwd"].(string); ok && cwd != "" {
		ev.Cwd = cwd
	}

	lowerEvent := strings.ToLower(resolvedEvent)
	switch {
	case strings.Contains(lowerEvent, "prompt") || lowerEvent == "userpromptsubmit":
		ev.State = agent.StateWorking
		ev.Activity = "Processing user prompt"
		if prompt, ok := raw["prompt"].(string); ok && prompt != "" {
			ev.Activity = "Processing: " + agentutil.TruncateTitle(prompt, 50)
		} else if text, ok := raw["text"].(string); ok && text != "" {
			ev.Activity = "Processing: " + agentutil.TruncateTitle(text, 50)
		}
	case strings.Contains(lowerEvent, "pretool") || strings.Contains(lowerEvent, "tool_start") || strings.Contains(lowerEvent, "tool_use"):
		ev.State = agent.StateWorking
		toolName := "tool"
		if name, ok := raw["tool_name"].(string); ok && name != "" {
			toolName = name
		} else if name, ok := raw["tool"].(string); ok && name != "" {
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
		if toolName != "tool" {
			ev.Activity = "Executing tool: " + toolName
		} else {
			ev.Activity = "Running tool"
		}
	case strings.Contains(lowerEvent, "posttool") || strings.Contains(lowerEvent, "tool_end") || strings.Contains(lowerEvent, "tool_result"):
		ev.State = agent.StateWorking
		ev.Activity = "Tool execution completed"
	case strings.Contains(lowerEvent, "permission") || strings.Contains(lowerEvent, "approval") || strings.Contains(lowerEvent, "confirm"):
		ev.State = agent.StateBlocked
		reason := "Tool permission requested"
		if r, ok := raw["reason"].(string); ok && r != "" {
			reason = r
		}
		question := "Permission required"
		if q, ok := raw["question"].(string); ok && q != "" {
			question = q
		}
		ev.Blocked = &agent.Blocked{
			Kind:     agent.BlockPermission,
			Reason:   reason,
			Question: question,
			Options:  []string{"Allow", "Deny"},
			Since:    time.Now(),
		}
		ev.Activity = "Waiting for permission: " + agentutil.TruncateTitle(reason, 50)
	case strings.Contains(lowerEvent, "question") || strings.Contains(lowerEvent, "input"):
		ev.State = agent.StateBlocked
		question := "Waiting for user input"
		if q, ok := raw["question"].(string); ok && q != "" {
			question = q
		}
		ev.Blocked = &agent.Blocked{
			Kind:     agent.BlockQuestion,
			Reason:   question,
			Question: question,
			Since:    time.Now(),
		}
		ev.Activity = "Waiting for user input"
	case strings.Contains(lowerEvent, "stop") || strings.Contains(lowerEvent, "done") || strings.Contains(lowerEvent, "idle"):
		ev.State = agent.StateIdle
		ev.Activity = "Awaiting user prompt"
	case strings.Contains(lowerEvent, "start") || strings.Contains(lowerEvent, "init"):
		ev.State = agent.StateIdle
		ev.Activity = "Session started"
		ev.StartedAt = time.Now()
	default:
		ev.State = agent.StateWorking
		ev.Activity = "Working"
	}

	return ev, nil
}
