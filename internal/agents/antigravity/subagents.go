package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func subagentRegistryDirs(home, cleanID string) []string {
	dirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "subagents"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "subagents"),
		filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "subagents"),
	}
	if p1, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "subagents")); err == nil {
		dirs = append(dirs, p1...)
	}
	if p2, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "subagents")); err == nil {
		dirs = append(dirs, p2...)
	}
	return dirs
}

func (p *Plugin) collectSubagents(homeDir, nativeID string) []agent.SubagentInfo {
	home := effectiveHome(homeDir)
	if home == "" || !agentutil.IsSafeSessionID(nativeID) {
		return nil
	}
	cleanID := filepath.Base(filepath.Clean(nativeID))

	var results []agent.SubagentInfo
	seen := make(map[string]bool)
	for _, dir := range subagentRegistryDirs(home, cleanID) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		brainBaseDir := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			var payload struct {
				ConversationID     string `json:"conversationId"`
				SubagentDescriptor struct {
					TypeName string `json:"typeName"`
					Role     string `json:"role"`
				} `json:"subagentDescriptor"`
				State string `json:"state"`
			}
			if json.Unmarshal(data, &payload) != nil {
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
			stUpper := strings.ToUpper(payload.State)
			inspectedState, prompt, resolvedStart := p.inspectSubagentTranscript(brainBaseDir, subID, startTime)
			startTime = resolvedStart
			if strings.Contains(stUpper, "KILLED") || strings.Contains(stUpper, "CANCEL") {
				stateNorm = "killed"
			} else if strings.Contains(stUpper, "RUNNING") || strings.Contains(stUpper, "ALIVE") {
				stateNorm = inspectedState
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
			results = append(results, agent.SubagentInfo{
				ID:        subID,
				Name:      name,
				Role:      role,
				Prompt:    prompt,
				State:     stateNorm,
				StartedAt: startTime,
			})
		}
	}
	return results
}

func (p *Plugin) ListSubagents(homeDir, _, nativeID string) ([]agent.SubagentInfo, error) {
	return p.collectSubagents(homeDir, nativeID), nil
}

func (p *Plugin) ListActiveSubagents(homeDir, _, nativeID string) ([]*agent.ActiveSubagent, error) {
	subs := p.collectSubagents(homeDir, nativeID)
	out := make([]*agent.ActiveSubagent, 0, len(subs))
	for _, s := range subs {
		out = append(out, &agent.ActiveSubagent{
			ID:        s.ID,
			Name:      s.Name,
			Role:      s.Role,
			Prompt:    s.Prompt,
			State:     s.State,
			StartedAt: s.StartedAt,
		})
	}
	return out, nil
}

func (p *Plugin) inspectSubagentTranscript(brainBaseDir, subID string, fallbackStart time.Time) (state string, prompt string, startTime time.Time) {
	startTime = fallbackStart
	cleanSubID := filepath.Base(filepath.Clean(subID))
	logPath := filepath.Join(brainBaseDir, cleanSubID, ".system_generated", "logs", "transcript.jsonl")

	stat, statErr := os.Stat(logPath)
	if statErr != nil {
		if time.Since(fallbackStart) < 2*time.Minute {
			return "running", "", fallbackStart
		}
		return "completed", "", fallbackStart
	}

	if headData, err := agentutil.ReadHead(logPath, 16*1024); err == nil && len(headData) > 0 {
		for _, line := range strings.Split(string(headData), "\n") {
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
					if t := parseStepTimestamp(step.CreatedAt); !t.IsZero() {
						startTime = t
					}
				}
				if prompt == "" && (step.StepIndex == 0 || step.Type == "SYSTEM_MESSAGE" || step.Type == "USER_INPUT") {
					prompt = p.extractSubagentPromptText(step.Type, step.Content)
				}
				if prompt != "" {
					break
				}
			}
		}
	}

	if time.Since(stat.ModTime()) > 15*time.Minute {
		return "completed", prompt, startTime
	}

	tailData, tailErr := agentutil.ReadTail(logPath, 64*1024)
	if tailErr != nil || len(tailData) == 0 {
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
				if len(step.ToolCalls) == 0 && !hasTruncatedField(step.TruncatedFields, "tool_calls") {
					return "completed", prompt, startTime
				}
			}
			return "running", prompt, startTime
		}
		return "running", prompt, startTime
	}
	return "running", prompt, startTime
}

func (p *Plugin) extractSubagentPromptText(stepType, content string) string {
	text := strings.TrimSpace(content)
	if text == "" {
		return ""
	}
	if !strings.EqualFold(stepType, "USER_INPUT") && strings.Contains(text, "<SYSTEM_MESSAGE>") && strings.Contains(text, "content=") {
		sub := text[strings.Index(text, "content=")+len("content="):]
		if endIdx := strings.Index(sub, "</SYSTEM_MESSAGE>"); endIdx != -1 {
			sub = sub[:endIdx]
		}
		text = strings.TrimSpace(sub)
	} else if strings.EqualFold(stepType, "USER_INPUT") {
		text, _ = p.FilterUserEntry(nil, text)
	} else {
		return ""
	}
	if len(text) > 240 {
		text = strings.TrimSpace(text[:240]) + "..."
	}
	return text
}
