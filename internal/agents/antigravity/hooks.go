package antigravity

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type hookPayload struct {
	ConversationID string   `json:"conversationId"`
	WorkspacePaths []string `json:"workspacePaths"`
	StepIdx        int      `json:"stepIdx"`
	ToolCall       *struct {
		Name string                 `json:"name"`
		Args map[string]interface{} `json:"args"`
	} `json:"toolCall"`
}

func (*Plugin) CheckHookConfig(homeDir string) (bool, string, error) {
	setupCmd := "go run ./cmd/ackbar setup-hooks  # (or edit ~/.gemini/settings.json)"
	home := effectiveHome(homeDir)
	if home == "" {
		return false, setupCmd, nil
	}
	for _, p := range hookConfigCandidatePaths(home) {
		if data, err := os.ReadFile(p); err == nil {
			s := string(data)
			if strings.Contains(s, "127.0.0.1:7777") || strings.Contains(s, "localhost:7777") || strings.Contains(s, "ackbar-hook") {
				return true, setupCmd, nil
			}
		}
	}
	return false, setupCmd, nil
}

func (*Plugin) HookAckPayload(eventName string) []byte {
	if strings.EqualFold(eventName, "PreToolUse") {
		return []byte(`{"decision":"allow"}`)
	}
	return []byte("{}")
}

func (*Plugin) ParseHook(eventName string, payload []byte) (*agent.Event, error) {
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Antigravity hook payload: %w", err)
	}

	if p.ConversationID != "" && !agentutil.IsSafeSessionID(p.ConversationID) {
		return nil, fmt.Errorf("invalid conversationId")
	}

	cwd := ""
	if len(p.WorkspacePaths) > 0 {
		cwd = p.WorkspacePaths[0]
	}

	now := time.Now()
	event := &agent.Event{
		Agent:       "antigravity",
		NativeID:    p.ConversationID,
		Cwd:         cwd,
		EventName:   eventName,
		LastEventAt: now,
		State:       agent.StateWorking,
	}
	if p.ToolCall != nil {
		event.ToolName = p.ToolCall.Name
		event.ToolInput = p.ToolCall.Args
	}
	if event.NativeID == "" {
		event.NativeID = "default"
	}

	switch strings.ToLower(eventName) {
	case "sessionstart":
		event.State = agent.StateIdle
		event.Activity = "Session started"
		event.StartedAt = now
	case "notification":
		event.State = agent.StateIdle
		event.Activity = "Awaiting user prompt"
	case "stop":
		event.State = agent.StateEnded
		event.Activity = "Session ended"
	case "pretooluse":
		if p.ToolCall != nil {
			tool := p.ToolCall.Name
			toolLower := strings.ToLower(tool)
			if tool == "ask_question" || tool == "AskUserQuestion" {
				event.State = agent.StateBlocked
				question, options := agentutil.ExtractQuestionAndOptions(p.ToolCall.Args)
				questions := agentutil.ExtractQuestions(p.ToolCall.Args)
				reason := "Waiting for user response to prompt"
				if question != "" {
					reason = question
				}
				event.Blocked = &agent.Blocked{
					Kind:      agent.BlockQuestion,
					Reason:    reason,
					Question:  question,
					Options:   options,
					Questions: questions,
					Since:     now,
				}
				if question != "" {
					event.Activity = "Question: " + agentutil.TruncateTitle(question, 50)
				} else {
					event.Activity = "Waiting for user response"
				}
			} else if toolLower == "exitplanmode" || toolLower == "exit_plan_mode" {
				event.State = agent.StateBlocked
				planText, _ := p.ToolCall.Args["plan"].(string)
				event.Blocked = &agent.Blocked{
					Kind:     agent.BlockApproval,
					Reason:   planText,
					Question: "Approve implementation plan?",
					Options:  []string{"Approve Plan", "Request Changes"},
					Since:    now,
				}
				event.Activity = "Waiting for plan approval"
			} else if tool == "ask_permission" || tool == "ask_custom_permission" {
				event.State = agent.StateBlocked
				event.Blocked = &agent.Blocked{
					Kind:     agent.BlockPermission,
					Reason:   extractPermissionReason(p.ToolCall.Args),
					Question: "Tool requires permission",
					Options:  []string{"1. Yes", "2. Yes, and always allow", "3. No"},
					Since:    now,
				}
				event.Activity = "Waiting for user permission"
			} else {
				event.State = agent.StateWorking
				event.Activity = "Running tool: " + tool
			}
		} else {
			event.State = agent.StateWorking
			event.Activity = "Running tool step"
		}
	case "posttooluse":
		event.State = agent.StateWorking
		event.Activity = "Tool execution completed"
	case "preinvocation":
		event.State = agent.StateWorking
		event.Activity = "Thinking..."
	case "postinvocation":
		event.State = agent.StateIdle
		event.Activity = "Awaiting user prompt"
	default:
		event.Activity = "Event: " + eventName
	}

	return event, nil
}
