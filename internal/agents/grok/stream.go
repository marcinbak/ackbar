package grok

import (
	"encoding/json"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type streamParser struct {
	sessionID               string
	seenGrok                bool
	lastText                strings.Builder
	hadError                bool
	needsSeparation         bool
	hasEmittedAssistantText bool
	emittedAuthBlock        bool
	toolUses                map[string]string
	toolInputs              map[string]map[string]any
}

func (*Plugin) NewStreamParser(sessionID string) agent.StreamParser {
	return &streamParser{
		sessionID:  sessionID,
		seenGrok:   strings.HasPrefix(sessionID, "grok"),
		toolUses:   make(map[string]string),
		toolInputs: make(map[string]map[string]any),
	}
}

func (*Plugin) ParseStreamEvent(line []byte) ([]agent.ChatStreamEvent, error) {
	p := &streamParser{
		seenGrok:   true,
		toolUses:   make(map[string]string),
		toolInputs: make(map[string]map[string]any),
	}
	events, _, err := p.ParseStreamLine(line)
	return events, err
}

func (p *streamParser) ParseStreamLine(line []byte) ([]agent.ChatStreamEvent, bool, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, false, nil
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, false, nil
	}

	evtType, _ := raw["type"].(string)
	isGrokEvt := false
	switch evtType {
	case "thought", "tool_call", "tool_call_update", "end":
		isGrokEvt = true
	case "text":
		_, hasData := raw["data"]
		_, hasText := raw["text"]
		isGrokEvt = hasData || hasText
	case "usage":
		_, isGrokEvt = raw["model"].(string)
	case "error":
		isGrokEvt = p.seenGrok || strings.HasPrefix(p.sessionID, "grok")
	}
	if !isGrokEvt {
		return nil, false, nil
	}
	p.seenGrok = true

	var out []agent.ChatStreamEvent
	switch evtType {
	case "thought":
		data := firstMapStr(raw, "data", "text")
		if data != "" {
			out = append(out, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "thought_delta", Thinking: data})
		}

	case "text":
		data := firstMapStr(raw, "data", "text")
		if data != "" {
			if p.needsSeparation && p.hasEmittedAssistantText {
				data = agentutil.EnsureDoubleNewlineSeparation(p.lastText.String(), data)
				p.needsSeparation = false
			}
			p.hasEmittedAssistantText = true
			p.lastText.WriteString(data)
			out = append(out, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "text_delta", Text: data})
		}

	case "tool_call":
		toolCallID := firstMapStr(raw, "toolCallId", "tool_call_id", "id")
		toolName := firstMapStr(raw, "toolName", "title", "name")
		rawInput := raw["rawInput"]
		if rawInput == nil {
			rawInput = raw["input"]
		}
		if rawInput == nil {
			rawInput = raw["arguments"]
		}
		if toolCallID != "" {
			if toolName != "" {
				p.toolUses[toolCallID] = toolName
			}
			if mInp, ok := rawInput.(map[string]any); ok {
				p.toolInputs[toolCallID] = mInp
			} else if sInp, ok := rawInput.(string); ok {
				var parsed map[string]any
				if json.Unmarshal([]byte(sInp), &parsed) == nil {
					p.toolInputs[toolCallID] = parsed
				}
			}
		}
		p.needsSeparation = true
		out = append(out, agent.ChatStreamEvent{
			SessionID: p.sessionID, Type: "tool_start", ToolName: toolName, ToolInput: rawInput,
		})
		if rawInput != nil {
			if blocked := agentutil.ParseInteractiveToolBlock(toolName, rawInput); blocked != nil {
				out = append(out, agent.ChatStreamEvent{
					SessionID: p.sessionID, Type: "interactive_block", ToolName: toolName, ToolInput: rawInput, Blocked: blocked,
				})
			}
		}

	case "tool_call_update":
		toolCallID := firstMapStr(raw, "toolCallId", "tool_call_id", "id")
		statusLower := strings.ToLower(firstMapStr(raw, "status"))
		if statusLower == "completed" || statusLower == "failed" || statusLower == "error" {
			p.needsSeparation = true
			isErr := statusLower == "failed" || statusLower == "error"
			rawOutput := raw["rawOutput"]
			if rawOutput == nil {
				rawOutput = raw["output"]
			}
			if rawOutput == nil {
				rawOutput = raw["result"]
			}
			outStr := ""
			if s, ok := rawOutput.(string); ok {
				outStr = s
			} else if rawOutput != nil {
				if b, err := json.Marshal(rawOutput); err == nil {
					outStr = string(b)
				}
			}
			tName := p.toolUses[toolCallID]
			tInput := p.toolInputs[toolCallID]
			out = append(out, agent.ChatStreamEvent{
				SessionID: p.sessionID, Type: "tool_result", ToolName: tName, ToolInput: tInput, ToolOutput: outStr, IsError: isErr,
			})
			if !p.emittedAuthBlock {
				if authBlock := agentutil.DetectToolResultAuthBlock("grok", tName, tInput, outStr, isErr); authBlock != nil {
					p.emittedAuthBlock = true
					out = append(out, agent.ChatStreamEvent{
						SessionID: p.sessionID, Type: "interactive_block", ToolName: tName, ToolInput: tInput, Blocked: authBlock,
					})
				}
			}
		}

	case "usage":
		if m, ok := raw["model"].(string); ok && m != "" {
			out = append(out, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "model_detected", Model: m})
		}

	case "error":
		msg := firstMapStr(raw, "message", "error")
		if msg == "" {
			msg = "Grok stream error"
		}
		p.hadError = true
		p.lastText.WriteString("\n" + msg)
		out = append(out, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "error", Text: msg, IsError: true})

	case "end":
		out = append(out, agent.ChatStreamEvent{SessionID: p.sessionID, Type: "turn_complete", Text: p.lastText.String()})
	}

	return out, true, nil
}
