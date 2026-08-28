package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

type CodexProvider struct{}

func NewCodexProvider() *CodexProvider {
	return &CodexProvider{}
}

func (c *CodexProvider) Agent() string {
	return "codex"
}

func (c *CodexProvider) DisplayName() string {
	return "OpenAI Codex"
}

func (c *CodexProvider) BrandColor() string {
	return "#10A37F"
}

func (c *CodexProvider) IconSVG() string {
	return `<svg class="agent-logo-svg codex-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a4.5 4.5 0 0 1 4.2 2.9l.4.9a4.5 4.5 0 0 1 1.8 4.7l-.2 1a4.5 4.5 0 0 1-2.4 4.1l-.8.5a4.5 4.5 0 0 1-4.7 1.8l-1-.2a4.5 4.5 0 0 1-4.1-2.4l-.5-.8a4.5 4.5 0 0 1-1.8-4.7l.2-1a4.5 4.5 0 0 1 2.4-4.1l.8-.5A4.5 4.5 0 0 1 12 2z"/><path d="M12 6.5l3.5 2v4l-3.5 2l-3.5-2v-4z"/><path d="M8.5 8.5L5 10.5M15.5 8.5L19 10.5M12 14.5V18.5"/></svg>`
}

func (c *CodexProvider) ProcessNames() []string {
	return []string{"codex"}
}

func (c *CodexProvider) GetSpawnCommand(tempUUID string) string {
	return "codex exec"
}

func (c *CodexProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" {
		return "codex exec resume " + nativeID
	}
	return "codex exec"
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

func (c *CodexProvider) IsInstalled() bool {
	if lookPathInStandardDirs("codex") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
			return true
		}
	}
	return false
}

func (c *CodexProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "ackbar-hook --agent=codex"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, setupCmd, nil
	}

	if strings.Contains(string(data), "127.0.0.1:7777") || strings.Contains(string(data), "localhost:7777") || strings.Contains(string(data), "ackbar-hook") {
		return true, setupCmd, nil
	}

	return false, setupCmd, nil
}

func (c *CodexProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	return nil
}

func (c *CodexProvider) ResolveSessionTitle(cwd, nativeID string) string {
	return ""
}

func (c *CodexProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	return nil, fmt.Errorf("codex native transcript extraction not supported")
}

func (c *CodexProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if nativeID == "" {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(home, ".codex", "sessions", nativeID))
	return nil
}

func (c *CodexProvider) InspectStatus(ctx context.Context, sess *daemon.Session) bool {
	return false
}
