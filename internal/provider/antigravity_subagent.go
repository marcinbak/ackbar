package provider

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

func (a *AntigravityProvider) ListSubagents(home, cwd, nativeID string) ([]*daemon.ActiveSubagent, error) {
	if nativeID == "" {
		return nil, nil
	}
	cleanID := filepath.Base(filepath.Clean(nativeID))
	if cleanID == "" || cleanID == "." || cleanID == ".." || strings.ContainsAny(cleanID, "*?[") {
		return nil, nil
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return nil, nil
	}

	candidateDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "subagents"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "subagents"),
		filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "subagents"),
	}

	if profiles, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "subagents")); len(profiles) > 0 {
		candidateDirs = append(candidateDirs, profiles...)
	}
	if profiles2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "subagents")); len(profiles2) > 0 {
		candidateDirs = append(candidateDirs, profiles2...)
	}

	var results []*daemon.ActiveSubagent
	seen := make(map[string]bool)

	for _, dir := range candidateDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		brainBaseDir := filepath.Dir(filepath.Dir(filepath.Dir(dir)))

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			filePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			var payload struct {
				ConversationID     string `json:"conversationId"`
				SubagentDescriptor struct {
					TypeName string `json:"typeName"`
					Role     string `json:"role"`
				} `json:"subagentDescriptor"`
				State          string `json:"state"`
				SpawnStepIndex int    `json:"spawnStepIndex"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				continue
			}

			subID := payload.ConversationID
			if subID == "" {
				subID = strings.TrimSuffix(entry.Name(), ".json")
			}
			if seen[subID] {
				continue
			}
			seen[subID] = true

			info, _ := entry.Info()
			startTime := time.Now()
			if info != nil {
				startTime = info.ModTime()
			}

			stateNorm := "completed"
			var prompt string
			stUpper := strings.ToUpper(payload.State)
			if strings.Contains(stUpper, "KILLED") || strings.Contains(stUpper, "CANCEL") {
				stateNorm = "killed"
				_, prompt, startTime = inspectAntigravitySubagentTranscript(brainBaseDir, subID, startTime)
			} else if strings.Contains(stUpper, "RUNNING") || strings.Contains(stUpper, "ALIVE") {
				stateNorm, prompt, startTime = inspectAntigravitySubagentTranscript(brainBaseDir, subID, startTime)
			} else {
				_, prompt, startTime = inspectAntigravitySubagentTranscript(brainBaseDir, subID, startTime)
			}

			role := payload.SubagentDescriptor.Role
			if role == "" {
				role = payload.SubagentDescriptor.TypeName
			}
			if role == "" {
				role = "Subagent"
			}

			name := payload.SubagentDescriptor.TypeName
			if name == "" {
				name = role
			}

			results = append(results, &daemon.ActiveSubagent{
				ID:        subID,
				Name:      name,
				Role:      role,
				Prompt:    prompt,
				State:     stateNorm,
				StartedAt: startTime,
			})
		}
	}

	return results, nil
}

func inspectAntigravitySubagentTranscript(brainBaseDir, subID string, fallbackStart time.Time) (state string, prompt string, startTime time.Time) {
	startTime = fallbackStart
	cleanSubID := filepath.Base(filepath.Clean(subID))
	logPath := filepath.Join(brainBaseDir, cleanSubID, ".system_generated", "logs", "transcript.jsonl")

	f, err := os.Open(logPath)
	if err != nil {
		if time.Since(fallbackStart) < 2*time.Minute {
			return "running", "", fallbackStart
		}
		return "completed", "", fallbackStart
	}
	defer f.Close()

	stat, statErr := f.Stat()
	headBuf := make([]byte, 16*1024)
	nHead, _ := f.Read(headBuf)
	if nHead > 0 {
		lines := strings.Split(string(headBuf[:nHead]), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var step struct {
				StepIndex int    `json:"step_index"`
				Type      string `json:"type"`
				CreatedAt string `json:"created_at"`
				Content   string `json:"content"`
			}
			if json.Unmarshal([]byte(line), &step) == nil {
				if step.CreatedAt != "" && startTime.Equal(fallbackStart) {
					if t, pErr := time.Parse(time.RFC3339Nano, step.CreatedAt); pErr == nil {
						startTime = t
					} else if t, pErr := time.Parse(time.RFC3339, step.CreatedAt); pErr == nil {
						startTime = t
					}
				}
				if prompt == "" && (step.StepIndex == 0 || step.Type == "SYSTEM_MESSAGE" || step.Type == "USER_INPUT") {
					prompt = extractSubagentPromptText(step.Type, step.Content)
				}
				if prompt != "" {
					break
				}
			}
		}
	}

	if statErr == nil && time.Since(stat.ModTime()) > 15*time.Minute {
		return "completed", prompt, startTime
	}

	var tailData []byte
	if statErr == nil && stat.Size() > 0 {
		tailSize := int64(64 * 1024)
		offset := stat.Size() - tailSize
		if offset < 0 {
			offset = 0
			tailSize = stat.Size()
		}
		if _, sErr := f.Seek(offset, io.SeekStart); sErr == nil {
			buf := make([]byte, tailSize)
			if nTail, rErr := io.ReadFull(f, buf); rErr == nil || nTail > 0 {
				tailData = buf[:nTail]
			}
		}
	}

	if len(tailData) == 0 {
		return "running", prompt, startTime
	}

	lines := strings.Split(string(tailData), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var step struct {
			StepIndex       int                      `json:"step_index"`
			Type            string                   `json:"type"`
			Status          string                   `json:"status"`
			ToolCalls       []map[string]interface{} `json:"tool_calls"`
			TruncatedFields []string                 `json:"truncated_fields"`
		}
		if json.Unmarshal([]byte(line), &step) != nil {
			continue
		}
		stType := strings.TrimSpace(step.Type)
		if stType == "" || strings.EqualFold(stType, "CHECKPOINT") || (strings.EqualFold(stType, "SYSTEM") && step.StepIndex > 0) {
			continue
		}
		if strings.EqualFold(stType, "PLANNER_RESPONSE") {
			stStatus := strings.ToUpper(strings.TrimSpace(step.Status))
			if stStatus == "DONE" || stStatus == "CANCELED" || stStatus == "CANCELLED" || stStatus == "ERROR" {
				hasToolCalls := len(step.ToolCalls) > 0
				if !hasToolCalls {
					for _, tf := range step.TruncatedFields {
						if strings.EqualFold(tf, "tool_calls") {
							hasToolCalls = true
							break
						}
					}
				}
				if !hasToolCalls {
					return "completed", prompt, startTime
				}
			}
			return "running", prompt, startTime
		}
		return "running", prompt, startTime
	}

	return "running", prompt, startTime
}

func extractSubagentPromptText(stepType, content string) string {
	text := strings.TrimSpace(content)
	if text == "" {
		return ""
	}
	if strings.Contains(text, "<SYSTEM_MESSAGE>") {
		if idx := strings.Index(text, "content="); idx != -1 {
			sub := text[idx+len("content="):]
			if endIdx := strings.Index(sub, "</SYSTEM_MESSAGE>"); endIdx != -1 {
				sub = sub[:endIdx]
			}
			text = strings.TrimSpace(sub)
		}
	} else if !strings.EqualFold(stepType, "USER_INPUT") {
		return ""
	}
	if len(text) > 240 {
		text = strings.TrimSpace(text[:240]) + "..."
	}
	return text
}
