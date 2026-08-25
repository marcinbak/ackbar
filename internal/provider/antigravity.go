package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
		event.State = daemon.StateWorking
		event.Activity = "Completed turn"

	default:
		event.Activity = "Event: " + eventName
	}

	return event, nil
}

func lookPathInStandardDirs(binName string) bool {
	if _, err := exec.LookPath(binName); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidates := []string{
			filepath.Join(home, ".local", "bin", binName),
			filepath.Join(home, ".npm-global", "bin", binName),
			filepath.Join(home, "bin", binName),
			filepath.Join(home, ".cargo", "bin", binName),
			"/opt/homebrew/bin/" + binName,
			"/usr/local/bin/" + binName,
			"/usr/bin/" + binName,
			"/bin/" + binName,
		}
		for _, c := range candidates {
			if stat, err := os.Stat(c); err == nil && !stat.IsDir() {
				return true
			}
		}
	}
	return false
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
	setupCmd := "ackbar-hook --agent=antigravity"
	home, err := os.UserHomeDir()
	if err != nil {
		return false, setupCmd, nil
	}

	paths := []string{
		filepath.Join(home, ".gemini", "config", "hooks.json"),
		filepath.Join(home, ".antigravity", "config", "hooks.json"),
		filepath.Join(home, ".gemini", "hooks.json"),
		filepath.Join(home, ".antigravity", "hooks.json"),
	}
	for _, hooksPath := range paths {
		data, err := os.ReadFile(hooksPath)
		if err == nil && (strings.Contains(string(data), "ackbar-hook") || strings.Contains(string(data), "127.0.0.1:7777") || strings.Contains(string(data), "PreInvocation")) {
			return true, setupCmd, nil
		}
	}

	return false, setupCmd, nil
}

func extractAntigravityQuestionAndOptions(args map[string]interface{}) (string, []string) {
	if args == nil {
		return "", nil
	}

	var questionText string
	var optionsList []string

	// 1. Array of questions (standard ask_question schema)
	if questionsRaw, ok := args["questions"].([]interface{}); ok && len(questionsRaw) > 0 {
		for _, qItem := range questionsRaw {
			if qMap, ok := qItem.(map[string]interface{}); ok {
				if qStr, ok := qMap["question"].(string); ok && questionText == "" {
					questionText = qStr
				}
				if optsRaw, ok := qMap["options"].([]interface{}); ok {
					for _, opt := range optsRaw {
						if s, ok := opt.(string); ok {
							optionsList = append(optionsList, s)
						} else if optMap, ok := opt.(map[string]interface{}); ok {
							if label, ok := optMap["label"].(string); ok {
								optionsList = append(optionsList, label)
							} else if text, ok := optMap["text"].(string); ok {
								optionsList = append(optionsList, text)
							}
						}
					}
				}
			}
		}
	}

	// 2. Direct question / prompt field
	if questionText == "" {
		if qStr, ok := args["question"].(string); ok {
			questionText = qStr
		} else if promptStr, ok := args["prompt"].(string); ok {
			questionText = promptStr
		}
	}

	// 3. Direct options field
	if len(optionsList) == 0 {
		if optsRaw, ok := args["options"].([]interface{}); ok {
			for _, opt := range optsRaw {
				if s, ok := opt.(string); ok {
					optionsList = append(optionsList, s)
				} else if optMap, ok := opt.(map[string]interface{}); ok {
					if label, ok := optMap["label"].(string); ok {
						optionsList = append(optionsList, label)
					} else if text, ok := optMap["text"].(string); ok {
						optionsList = append(optionsList, text)
					}
				}
			}
		}
	}

	return questionText, optionsList
}
