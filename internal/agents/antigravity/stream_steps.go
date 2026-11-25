package antigravity

import (
	"fmt"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type turnErrorTracker struct {
	pending []string
}

func (t *turnErrorTracker) hasPending() bool { return len(t.pending) > 0 }
func (t *turnErrorTracker) clear()           { t.pending = nil }

func (t *turnErrorTracker) add(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	for i, p := range t.pending {
		if strings.Contains(p, msg) {
			return
		}
		if strings.Contains(msg, p) {
			t.pending[i] = msg
			var pruned []string
			for j, item := range t.pending {
				if j == i || !strings.Contains(msg, item) {
					pruned = append(pruned, item)
				}
			}
			t.pending = pruned
			return
		}
	}
	t.pending = append(t.pending, msg)
}

func (t *turnErrorTracker) resolveFinal(finalErr string) string {
	t.add(finalErr)
	if len(t.pending) == 0 {
		return "Turn failed"
	}
	joined := strings.Join(t.pending, "\n")
	t.pending = nil
	return joined
}

func extractToolInfoInput(info map[string]interface{}) any {
	if info == nil {
		return nil
	}
	for _, k := range []string{"parameters", "arguments", "args"} {
		if v, ok := info[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func (p *streamParser) parseStepUpdate(su map[string]interface{}) []agent.ChatStreamEvent {
	var evts []agent.ChatStreamEvent
	if m, ok := su["model"].(string); ok && m != "" {
		evts = append(evts, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "model_detected", Model: m})
	}
	stepType, _ := su["step_type"].(string)
	state, _ := su["state"].(string)
	stepIdx := -1
	switch v := su["step_index"].(type) {
	case float64:
		stepIdx = int(v)
	case int:
		stepIdx = v
	}

	switch stepType {
	case "agent_response":
		hasProgress := false
		if textDelta, ok := su["text_delta"].(string); ok && textDelta != "" {
			isDupDone := stepIdx >= 0 && state == "DONE" && p.agStepText[stepIdx] != "" && strings.Contains(p.agStepText[stepIdx], textDelta)
			if !isDupDone {
				hasProgress = true
				isCont := stepIdx >= 0 && p.agStepText[stepIdx] != ""
				isNew := (stepIdx >= 0 && p.agStepText[stepIdx] == "") || (stepIdx < 0 && p.agHadToolSinceLastText)
				if stepIdx >= 0 {
					p.agStepText[stepIdx] += textDelta
				}
				emitted := textDelta
				if !isCont && isNew && p.hasEmittedAssistantText {
					emitted = agentutil.EnsureDoubleNewlineSeparation(p.lastTextBuilder.String(), textDelta)
				}
				p.lastTextBuilder.WriteString(emitted)
				evts = append(evts, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "text_delta", Text: emitted})
				p.hasEmittedAssistantText = true
				if !isCont {
					p.agHadToolSinceLastText = false
				}
			}
		}
		if thoughtDelta, ok := su["thought_delta"].(string); ok && thoughtDelta != "" {
			hasProgress = true
			evts = append(evts, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "thought_delta", Thinking: thoughtDelta})
		} else if thinking, ok := su["thinking"].(string); ok && thinking != "" {
			hasProgress = true
			evts = append(evts, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "thought_delta", Thinking: thinking})
		}
		if hasProgress {
			p.errTracker.clear()
		}

	case "tool":
		if state == "ACTIVE" {
			p.errTracker.clear()
		}
		p.agHadToolSinceLastText = true
		toolName, _ := su["tool_name"].(string)
		toolInfo, _ := su["tool_info"].(map[string]interface{})
		key := fmt.Sprintf("ag_%d", stepIdx)

		if state == "ACTIVE" {
			toolInput := extractToolInfoInput(toolInfo)
			if params, ok := toolInput.(map[string]any); ok {
				p.toolInputs[key] = params
			}
			var evtChecklist []agent.ChecklistItem
			var evtCleared bool
			if p.tracker == nil {
				p.tracker = (*Plugin)(nil).NewChecklistTracker()
			}
			if items, cleared, matched := p.tracker.Apply(toolName, toolInput); matched {
				evtChecklist = items
				evtCleared = cleared
			}
			evts = append(evts, agent.ChatStreamEvent{
				SessionID: p.sessionID, Type: "tool_start", ToolName: toolName, ToolInput: toolInput, Checklist: evtChecklist, ChecklistCleared: evtCleared,
			})
			if blocked := agentutil.ParseInteractiveToolBlock(toolName, toolInput); blocked != nil {
				evts = append(evts, agent.ChatStreamEvent{
					SessionID: p.sessionID, Type: "interactive_block", ToolName: toolName, ToolInput: toolInput, Blocked: blocked,
				})
			}
		} else if state == "DONE" || state == "ERROR" {
			toolOutput := ""
			if toolInfo != nil {
				toolOutput, _ = toolInfo["output"].(string)
			}
			curInput := p.toolInputs[key]
			if curInput == nil {
				if inp := extractToolInfoInput(toolInfo); inp != nil {
					if params, ok := inp.(map[string]any); ok {
						curInput = params
						p.toolInputs[key] = params
					}
				}
			}
			var resInput any
			if curInput != nil {
				resInput = curInput
			}
			evts = append(evts, agent.ChatStreamEvent{
				SessionID: p.sessionID, Type: "tool_result", ToolName: toolName, ToolInput: resInput, ToolOutput: toolOutput, IsError: state == "ERROR",
			})
			if !p.emittedAuthBlock {
				if authBlock := agentutil.DetectToolResultAuthBlock("", toolName, curInput, toolOutput, state == "ERROR"); authBlock != nil {
					p.emittedAuthBlock = true
					evts = append(evts, agent.ChatStreamEvent{
						SessionID: p.sessionID, Type: "interactive_block", ToolName: toolName, ToolInput: curInput, Blocked: authBlock,
					})
				}
			}
		}

	case "subagent":
		if state == "ACTIVE" {
			p.errTracker.clear()
		}
		p.agHadToolSinceLastText = true
		role, _ := su["role"].(string)
		taskName, _ := su["task_name"].(string)
		subName := role
		if subName == "" {
			subName = taskName
		}
		if subName == "" {
			subName = "subagent"
		}
		if state == "ACTIVE" {
			evts = append(evts, agent.ChatStreamEvent{
				SessionID: p.sessionID, Type: "tool_start", ToolName: "Task: " + subName, ToolInput: su["description"],
			})
		} else if state == "DONE" || state == "ERROR" {
			evts = append(evts, agent.ChatStreamEvent{
				SessionID: p.sessionID, Type: "tool_result", ToolName: "Task: " + subName, ToolOutput: fmt.Sprintf("Subagent completed (%s)", state), IsError: state == "ERROR",
			})
		}

	case "error_message":
		if errMsg, ok := su["error"].(string); ok && errMsg != "" {
			p.errTracker.add(errMsg)
		}
	}
	return evts
}
