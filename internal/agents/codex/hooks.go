package codex

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

var ignoredSubcommands = map[string]bool{
	"login": true, "logout": true, "auth": true, "mcp": true, "update": true, "upgrade": true,
}

type codexPayload struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Model          string `json:"model"`
	PermissionMode string `json:"permission_mode"`
	ToolName       string `json:"tool_name"`
	ToolInput      string `json:"tool_input"`
}

func (*Plugin) CheckHookConfig(homeDir string) (bool, string, error) {
	setupCmd := "ackbar-hook --agent=codex"
	codexDir := HomeDir(homeDir)
	for _, path := range []string{
		filepath.Join(codexDir, "hooks.json"),
		filepath.Join(codexDir, "config.toml"),
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
	var p codexPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Codex hook payload: %w", err)
	}

	event := &agent.Event{
		Agent:       "codex",
		NativeID:    p.SessionID,
		Cwd:         p.Cwd,
		EventName:   p.HookEventName,
		LastEventAt: time.Now(),
		State:       agent.StateWorking,
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
		event.State = agent.StateIdle
		event.Activity = "Session started"
		event.StartedAt = time.Now()
	case "sessionend", "stop":
		event.State = agent.StateEnded
		event.Activity = "Session ended"
	case "userpromptsubmit":
		event.State = agent.StateWorking
		event.Activity = "Processing user prompt"
	case "permissionrequest":
		event.State = agent.StateBlocked
		reason := "Authorize tool execution"
		if p.ToolName != "" {
			reason = "Allow running tool: " + p.ToolName
		}
		event.Blocked = &agent.Blocked{
			Kind:     agent.BlockPermission,
			Reason:   reason,
			Question: reason,
			Options:  []string{"Allow", "Deny"},
			Since:    time.Now(),
		}
		event.Activity = "Waiting for permission"
	case "pretooluse":
		if p.ToolName == "request_user_input" || p.ToolName == "ask_question" {
			event.State = agent.StateBlocked
			var q string
			var opts []string
			if p.ToolInput != "" {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(p.ToolInput), &data); err == nil {
					q, opts = agentutil.ExtractQuestionAndOptions(data)
				} else {
					q = p.ToolInput
				}
			}
			reason := "Waiting for user response"
			if q != "" {
				reason = q
			}
			event.Blocked = &agent.Blocked{
				Kind:     agent.BlockQuestion,
				Reason:   reason,
				Question: q,
				Options:  opts,
				Since:    time.Now(),
			}
			event.Activity = "Waiting for user input"
		} else {
			event.State = agent.StateWorking
			event.Activity = "Running tool: " + p.ToolName
		}
	case "posttooluse":
		event.State = agent.StateWorking
		event.Activity = "Tool execution completed"
	default:
		event.Activity = "Event: " + p.HookEventName
	}

	return event, nil
}

func (*Plugin) IsValidNativeID(nativeID string) bool {
	return agentutil.IsSafeSessionID(nativeID)
}

func (*Plugin) IsIgnoredCommand(fullCmd string) bool {
	return agentutil.CheckIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "codex"
	}, ignoredSubcommands, nil)
}

func (*Plugin) MatchProcessCommand(binLower, cmdLower string) bool {
	return binLower == "codex" && !agentutil.IsAckbarProcess(binLower, cmdLower)
}
