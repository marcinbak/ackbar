package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

type antigravityPayload struct {
	ConversationID string   `json:"conversationId"`
	WorkspacePaths []string `json:"workspacePaths"`
	StepIdx        int      `json:"stepIdx"`
	ToolCall       *struct {
		Name string                 `json:"name"`
		Args map[string]interface{} `json:"args"`
	} `json:"toolCall"`
}

func (a *AntigravityProvider) ParseHook(eventName string, payload []byte) (*daemon.Event, error) {
	var p antigravityPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Antigravity hook payload: %w", err)
	}

	cwd := ""
	if len(p.WorkspacePaths) > 0 {
		cwd = p.WorkspacePaths[0]
	}

	event := &daemon.Event{
		Agent:       "antigravity",
		NativeID:    p.ConversationID,
		Cwd:         cwd,
		EventName:   eventName,
		LastEventAt: time.Now(),
		State:       daemon.StateWorking, // default assumption
	}
	if p.ToolCall != nil {
		event.ToolName = p.ToolCall.Name
		event.ToolInput = p.ToolCall.Args
	}

	if event.NativeID == "" {
		event.NativeID = "default"
	}

	evt := strings.ToLower(eventName)

	switch evt {
	case "sessionstart":
		event.State = daemon.StateIdle
		event.Activity = "Session started"
		event.StartedAt = time.Now()

	case "notification":
		event.State = daemon.StateIdle
		event.Activity = "Awaiting user prompt"

	case "stop":
		event.State = daemon.StateEnded
		event.Activity = "Session ended"

	case "pretooluse":
		if p.ToolCall != nil {
			tool := p.ToolCall.Name
			if tool == "ask_question" {
				event.State = daemon.StateBlocked
				question, options := daemon.ExtractAntigravityQuestionAndOptions(p.ToolCall.Args)
				reason := "Waiting for user response to prompt"
				if question != "" {
					reason = question
				}
				event.Blocked = &daemon.Blocked{
					Kind:     daemon.BlockQuestion,
					Reason:   reason,
					Question: question,
					Options:  options,
					Since:    time.Now(),
				}
				if question != "" {
					event.Activity = "Question: " + daemon.TruncateTitle(question)
				} else {
					event.Activity = "Waiting for user response"
				}
			} else if tool == "ask_permission" || tool == "ask_custom_permission" {
				event.State = daemon.StateBlocked
				reason := daemon.ExtractAntigravityPermissionReason(p.ToolCall.Args)
				event.Blocked = &daemon.Blocked{
					Kind:     daemon.BlockPermission,
					Reason:   reason,
					Question: "Tool requires permission",
					Options:  []string{"1. Yes", "2. Yes, and always allow", "3. No"},
					Since:    time.Now(),
				}
				event.Activity = "Waiting for user permission"
			} else {
				event.State = daemon.StateWorking
				event.Activity = "Running tool: " + tool
			}
		} else {
			event.State = daemon.StateWorking
			event.Activity = "Running tool step"
		}

	case "posttooluse":
		event.State = daemon.StateWorking
		event.Activity = "Tool execution completed"

	case "preinvocation":
		event.State = daemon.StateWorking
		event.Activity = "Thinking..."

	case "postinvocation":
		event.State = daemon.StateIdle
		event.Activity = "Awaiting user prompt"

	default:
		event.Activity = "Event: " + eventName
	}

	return event, nil
}
