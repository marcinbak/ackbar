package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

type AntigravityProvider struct{}

func NewAntigravityProvider() *AntigravityProvider {
	return &AntigravityProvider{}
}

func (a *AntigravityProvider) Agent() string {
	return "antigravity"
}

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

	if event.NativeID == "" {
		event.NativeID = "default"
	}

	evt := strings.ToLower(eventName)

	switch evt {
	case "stop":
		event.State = daemon.StateEnded
		event.Activity = "Session ended"

	case "pretooluse":
		if p.ToolCall != nil {
			tool := p.ToolCall.Name
			if tool == "ask_question" {
				event.State = daemon.StateBlocked
				event.Blocked = &daemon.Blocked{
					Kind:   daemon.BlockQuestion,
					Reason: "Waiting for user response to prompt",
					Since:  time.Now(),
				}
				event.Activity = "Waiting for user response"
			} else if tool == "ask_permission" {
				event.State = daemon.StateBlocked
				event.Blocked = &daemon.Blocked{
					Kind:   daemon.BlockPermission,
					Reason: "Waiting for tool permission approval",
					Since:  time.Now(),
				}
				event.Activity = "Waiting for permission"
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
		event.State = daemon.StateWorking
		event.Activity = "Completed turn"

	default:
		event.Activity = "Event: " + eventName
	}

	return event, nil
}

func (a *AntigravityProvider) IsInstalled() bool {
	if _, err := exec.LookPath("agy"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(home + "/.gemini/antigravity"); err == nil {
			return true
		}
	}
	return false
}

func (a *AntigravityProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "ackbar-hook --agent=antigravity"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	hooksPath := home + "/.gemini/config/hooks.json"
	data, err := os.ReadFile(hooksPath)
	if err == nil && (strings.Contains(string(data), "ackbar-hook") || strings.Contains(string(data), "127.0.0.1:7777")) {
		return true, setupCmd, nil
	}

	return false, setupCmd, nil
}
