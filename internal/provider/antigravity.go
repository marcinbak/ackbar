package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func (a *AntigravityProvider) DisplayName() string {
	return "Google Antigravity"
}

func (a *AntigravityProvider) BrandColor() string {
	return "#4285F4"
}

func (a *AntigravityProvider) IconSVG() string {
	return `<svg class="agent-logo-svg antigravity-logo" viewBox="0 0 24 24" width="12" height="12"><defs><linearGradient id="agGrad" x1="0%" y1="0%" x2="100%" y2="100%"><stop offset="0%" stop-color="#EA4335"/><stop offset="35%" stop-color="#FBBC04"/><stop offset="70%" stop-color="#34A853"/><stop offset="100%" stop-color="#4285F4"/></linearGradient></defs><path fill="url(#agGrad)" d="M12 0.8 C8.2 0.8 5.6 3.2 4.1 7.8 C2.6 12.5 1.2 18.2 0.3 22.4 C-0.1 23.6 0.8 24.2 1.6 23.6 C4.8 21.2 7.8 14.2 12 12.4 C16.2 14.2 19.2 21.2 22.4 23.6 C23.2 24.2 24.1 23.6 23.7 22.4 C22.8 18.2 21.4 12.5 19.9 7.8 C18.4 3.2 15.8 0.8 12 0.8 Z"/></svg>`
}

func (a *AntigravityProvider) ProcessNames() []string {
	return []string{"antigravity", "agy", "bin/agy"}
}

func (a *AntigravityProvider) GetSpawnCommand(tempUUID string) string {
	return "agy"
}

func (a *AntigravityProvider) GetResumeCommand(nativeID string) string {
	if nativeID != "" {
		return "agy --conversation " + nativeID
	}
	return "agy"
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
			} else if tool == "ask_permission" {
				event.State = daemon.StateBlocked
				reason := "Waiting for tool permission approval"
				if p.ToolCall.Args != nil {
					if cmd, ok := p.ToolCall.Args["command"].(string); ok && cmd != "" {
						reason = fmt.Sprintf("Allow command: %s", cmd)
					} else if toolName, ok := p.ToolCall.Args["tool"].(string); ok && toolName != "" {
						reason = fmt.Sprintf("Allow tool: %s", toolName)
					} else if r, ok := p.ToolCall.Args["reason"].(string); ok && r != "" {
						reason = r
					}
				}
				event.Blocked = &daemon.Blocked{
					Kind:     daemon.BlockPermission,
					Reason:   reason,
					Question: reason,
					Options:  []string{"Allow", "Deny"},
					Since:    time.Now(),
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
		event.State = daemon.StateIdle
		event.Activity = "Awaiting user prompt"

	default:
		event.Activity = "Event: " + eventName
	}

	return event, nil
}

func (a *AntigravityProvider) IsInstalled() bool {
	if lookPathInStandardDirs("agy") || lookPathInStandardDirs("antigravity") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		dirs := []string{
			filepath.Join(home, ".gemini", "antigravity"),
			filepath.Join(home, ".antigravity"),
			filepath.Join(home, ".gemini"),
		}
		for _, d := range dirs {
			if _, err := os.Stat(d); err == nil {
				return true
			}
		}
	}
	return false
}

func (a *AntigravityProvider) CheckHookConfig() (bool, string, error) {
	setupCmd := "go run ./cmd/ackbar setup-hooks  # (or edit ~/.gemini/settings.json)"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	paths := []string{
		filepath.Join(home, ".gemini", "settings.json"),
		filepath.Join(home, ".gemini", "antigravity", "settings.json"),
		filepath.Join(home, ".antigravity", "settings.json"),
		filepath.Join(home, ".antigravity", "config", "hooks.json"),
		filepath.Join(home, ".gemini", "antigravity", "config", "hooks.json"),
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			if strings.Contains(string(data), "127.0.0.1:7777") || strings.Contains(string(data), "localhost:7777") || strings.Contains(string(data), "ackbar-hook") {
				return true, setupCmd, nil
			}
		}
	}

	return false, setupCmd, nil
}

func (a *AntigravityProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	title := a.ResolveSessionTitle(cwd, nativeID)
	if title != "" {
		return &daemon.SessionMeta{
			Title: title,
		}
	}
	return nil
}

func (a *AntigravityProvider) ResolveSessionTitle(cwd, nativeID string) string {
	return daemon.ReadAntigravitySessionTitle(cwd, nativeID)
}

func (a *AntigravityProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	candidatePaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".antigravity", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
	}

	var file *os.File
	var err error
	for _, path := range candidatePaths {
		file, err = os.Open(path)
		if err == nil {
			break
		}
	}

	if file == nil {
		return nil, fmt.Errorf("antigravity log not found for conversation %s: %w", nativeID, err)
	}
	defer file.Close()

	var messages []daemon.TranscriptMessage
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry struct {
			StepIndex int    `json:"step_index"`
			Source    string `json:"source"`
			Type      string `json:"type"`
			Status    string `json:"status"`
			CreatedAt string `json:"created_at"`
			Content   string `json:"content"`
			Thinking  string `json:"thinking"`
			ToolCalls []struct {
				Name string                 `json:"name"`
				Args map[string]interface{} `json:"args"`
			} `json:"tool_calls"`
		}

		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339, entry.CreatedAt)
		if ts.IsZero() {
			ts = time.Now()
		}

		if entry.Type == "USER_INPUT" {
			messages = append(messages, daemon.TranscriptMessage{
				Role:      "user",
				Content:   entry.Content,
				Timestamp: ts,
			})
		} else if entry.Type == "PLANNER_RESPONSE" {
			var tools []string
			for _, tc := range entry.ToolCalls {
				if tc.Name != "" {
					tools = append(tools, tc.Name)
				}
			}
			messages = append(messages, daemon.TranscriptMessage{
				Role:      "assistant",
				Content:   entry.Content,
				Thinking:  entry.Thinking,
				ToolCalls: tools,
				Timestamp: ts,
			})
		}
	}

	return messages, nil
}

func (a *AntigravityProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if nativeID == "" {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(home, ".gemini", "antigravity", "brain", nativeID))
	_ = os.RemoveAll(filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID))
	_ = os.RemoveAll(filepath.Join(home, ".antigravity", "brain", nativeID))
	return nil
}
