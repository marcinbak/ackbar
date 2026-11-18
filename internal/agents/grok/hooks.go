package grok

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
	setupCmd := "ackbar-hook --agent=grok"
	grokHome := HomeDir(homeDir)
	checkContent := func(p string) bool {
		data, err := os.ReadFile(p)
		if err != nil {
			return false
		}
		c := string(data)
		return strings.Contains(c, "127.0.0.1:7777") || strings.Contains(c, "localhost:7777") || strings.Contains(c, "ackbar-hook")
	}

	if checkContent(filepath.Join(grokHome, "hooks", "ackbar.json")) {
		return true, setupCmd, nil
	}
	if matches, err := filepath.Glob(filepath.Join(grokHome, "hooks", "*.json")); err == nil {
		for _, m := range matches {
			if checkContent(m) {
				return true, setupCmd, nil
			}
		}
	}
	if checkContent(filepath.Join(grokHome, "hooks.json")) ||
		checkContent(filepath.Join(grokHome, "config.toml")) ||
		checkContent(filepath.Join(grokHome, "config.json")) {
		return true, setupCmd, nil
	}
	return false, setupCmd, nil
}

func (*Plugin) HookAckPayload(eventName string) []byte {
	_ = eventName
	return []byte("{}")
}

func (*Plugin) ResolveSubagentParent(homeDir, nativeID string) (string, string, string) {
	return ResolveSubagentParent(homeDir, nativeID)
}

func (*Plugin) ParseHook(eventName string, payload []byte) (*agent.Event, error) {
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
		resolvedEvent = firstMapStr(raw, "hookEventName", "hook_event_name", "event", "type", "hook")
	}

	subType := firstMapStr(raw, "subagentType", "subagent_type")
	lowerEvent := strings.ToLower(resolvedEvent)
	if subType != "" {
		switch lowerEvent {
		case "subagent_start", "subagentstart", "subagent_stop", "subagentstop", "subagent_end", "subagentend":
		default:
			return nil, nil
		}
	}

	sid := firstMapStr(raw, "sessionId", "session_id", "id")
	if sid == "" || !agentutil.IsSafeGrokSessionID(sid) {
		return nil, nil
	}

	ev := &agent.Event{
		Agent:       "grok",
		NativeID:    sid,
		EventName:   resolvedEvent,
		LastEventAt: time.Now(),
		State:       agent.StateWorking,
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
		ev.EventName, ev.State, ev.ToolName, ev.Activity = "SubagentStart", agent.StateWorking, subType, "Subagent started: "+subType
	case lowerEvent == "subagent_stop" || lowerEvent == "subagentstop" || lowerEvent == "subagent_end" || lowerEvent == "subagentend":
		ev.EventName, ev.State, ev.ToolName, ev.Activity = "SubagentStop", agent.StateWorking, subType, "Subagent stopped"
	case lowerEvent == "session_end" || lowerEvent == "sessionend":
		ev.State, ev.Activity = agent.StateEnded, "Session ended"
	case strings.Contains(lowerEvent, "prompt") || lowerEvent == "userpromptsubmit":
		ev.State, ev.Activity = agent.StateWorking, "Processing prompt"
		if prompt := firstMapStr(raw, "prompt", "text"); prompt != "" {
			ev.Activity = "Processing: " + agentutil.TruncateTitle(prompt, 50)
		}
	case strings.Contains(lowerEvent, "pretool") || strings.Contains(lowerEvent, "tool_start") || strings.Contains(lowerEvent, "tool_use"):
		applyPreToolHook(ev, raw)
	case strings.Contains(lowerEvent, "posttool") || strings.Contains(lowerEvent, "tool_end") || strings.Contains(lowerEvent, "tool_result"):
		ev.State, ev.Activity = agent.StateWorking, "Tool execution completed"
	case lowerEvent == "notification":
		if !applyNotificationHook(ev, raw) {
			return nil, nil
		}
	case strings.Contains(lowerEvent, "permission") || strings.Contains(lowerEvent, "approval") || strings.Contains(lowerEvent, "confirm"):
		reason := firstMapStr(raw, "reason")
		if reason == "" {
			reason = "Tool permission requested"
		}
		question := firstMapStr(raw, "question")
		if question == "" {
			question = "Permission required"
		}
		ev.State = agent.StateBlocked
		ev.Blocked = &agent.Blocked{
			Kind: agent.BlockPermission, Reason: reason, Question: question, Options: []string{"Allow", "Deny"}, Since: time.Now(),
		}
		ev.Activity = "Waiting for permission: " + agentutil.TruncateTitle(reason, 50)
	case lowerEvent == "stop_failure" || lowerEvent == "stopfailure":
		applyStopFailureHook(ev, raw)
	case strings.Contains(lowerEvent, "stop") || strings.Contains(lowerEvent, "done") || strings.Contains(lowerEvent, "idle"):
		rLower := strings.ToLower(firstMapStr(raw, "reason", "stopReason", "stop_reason"))
		if rLower == "channel_closed" || rLower == "shutdown" {
			ev.State, ev.Activity = agent.StateEnded, "Session ended"
		} else {
			ev.State, ev.Activity = agent.StateIdle, "Awaiting user prompt"
		}
	case strings.Contains(lowerEvent, "start") || strings.Contains(lowerEvent, "init"):
		ev.State, ev.Activity, ev.StartedAt = agent.StateIdle, "Session started", time.Now()
	default:
		ev.State, ev.Activity = agent.StateWorking, "Working"
	}
	return ev, nil
}

func firstMapStr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func applyPreToolHook(ev *agent.Event, raw map[string]interface{}) {
	toolName := firstMapStr(raw, "toolName", "tool_name", "tool", "name")
	if toolName == "" {
		if tObj, ok := raw["tool"].(map[string]interface{}); ok {
			toolName = firstMapStr(tObj, "name")
		}
	}
	if toolName == "" {
		toolName = "tool"
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
		ev.State = agent.StateBlocked
		qText := "Waiting for user input"
		if s, ok := toolInput.(string); ok && s != "" {
			qText = s
		} else if m, ok := toolInput.(map[string]interface{}); ok {
			if q := firstMapStr(m, "question"); q != "" {
				qText = q
			}
		}
		ev.Blocked = &agent.Blocked{Kind: agent.BlockQuestion, Reason: qText, Question: qText, Since: time.Now()}
		ev.Activity = "Waiting for user input"
		return
	}
	ev.State, ev.ToolName, ev.ToolInput = agent.StateWorking, toolName, toolInput
	ev.Activity = "Running " + agentutil.FormatToolDisplay(toolName, toolInput)
}

func applyNotificationHook(ev *agent.Event, raw map[string]interface{}) bool {
	notifType := firstMapStr(raw, "notificationType", "notification_type")
	if notifType == "" {
		if msg, _ := raw["message"].(string); strings.Contains(strings.ToLower(msg), "permission") {
			notifType = "permission_prompt"
		}
	}
	switch notifType {
	case "permission_prompt":
		reason := firstMapStr(raw, "reason", "message", "question")
		if reason == "" {
			reason = "Tool permission requested"
		}
		ev.State = agent.StateBlocked
		ev.Blocked = &agent.Blocked{
			Kind: agent.BlockPermission, Reason: reason, Question: "Permission required", Options: []string{"Allow", "Deny"}, Since: time.Now(),
		}
		ev.Activity = "Waiting for permission: " + agentutil.TruncateTitle(reason, 50)
		return true
	case "idle_prompt", "task_complete":
		ev.State, ev.Activity = agent.StateIdle, "Awaiting user prompt"
		return true
	default:
		return false
	}
}

func applyStopFailureHook(ev *agent.Event, raw map[string]interface{}) {
	errLower := strings.ToLower(firstMapStr(raw, "error", "errorType", "error_type"))
	if strings.Contains(errLower, "rate_limit") {
		reason := firstMapStr(raw, "errorDetails", "message", "error_details")
		if reason == "" {
			reason = "Rate limit reached"
		}
		ev.State = agent.StateBlocked
		ev.Blocked = &agent.Blocked{Kind: agent.BlockRateLimit, Reason: reason, Since: time.Now()}
		ev.Activity = "Rate limit reached"
	} else if strings.Contains(errLower, "auth") {
		ev.State = agent.StateBlocked
		ev.Blocked = &agent.Blocked{Kind: agent.BlockAuth, Reason: "Authentication failed — run grok login", Since: time.Now()}
		ev.Activity = "Authentication failed — run grok login"
	} else {
		ev.State, ev.Activity = agent.StateIdle, "Turn stopped with error"
	}
}
