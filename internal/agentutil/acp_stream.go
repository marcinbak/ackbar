package agentutil

import (
	"encoding/json"
	"strings"

	"ackbar/internal/agent"
)

type ACPStreamState struct {
	lastText                strings.Builder
	needsSeparation         bool
	hasEmittedAssistantText bool
	emittedAuthBlock        bool
	toolUses                map[string]string
	toolInputs              map[string]map[string]any
}

func (s *ACPStreamState) ResetTurn() {
	s.lastText.Reset()
	s.needsSeparation = false
	s.hasEmittedAssistantText = false
	s.emittedAuthBlock = false
}

func (s *ACPStreamState) LastText() string {
	return s.lastText.String()
}

func (s *ACPStreamState) ParseACPSessionUpdate(agentKey, sessionID string, params map[string]any) ([]agent.ChatStreamEvent, *agent.ACPNotificationResult) {
	if s.toolUses == nil {
		s.toolUses = make(map[string]string)
	}
	if s.toolInputs == nil {
		s.toolInputs = make(map[string]map[string]any)
	}
	res := &agent.ACPNotificationResult{}
	if params == nil {
		return nil, res
	}
	update, _ := params["update"].(map[string]any)
	if update == nil && acpFirstStr(params, "sessionUpdate", "update_type") != "" {
		update = params
	}
	if update == nil {
		return nil, res
	}

	var events []agent.ChatStreamEvent
	switch acpFirstStr(update, "sessionUpdate", "update_type", "type") {
	case "agent_thought_chunk", "thought":
		if txt := extractACPText(update, "thought", "content", "text"); txt != "" {
			events = append(events, agent.ChatStreamEvent{SessionID: sessionID, Type: "thought_delta", Thinking: txt})
		}

	case "agent_message_chunk", "message", "text":
		if txt := extractACPText(update, "content", "text"); txt != "" {
			if s.needsSeparation && s.hasEmittedAssistantText {
				txt = EnsureDoubleNewlineSeparation(s.lastText.String(), txt)
				s.needsSeparation = false
			}
			s.hasEmittedAssistantText = true
			s.lastText.WriteString(txt)
			events = append(events, agent.ChatStreamEvent{SessionID: sessionID, Type: "text_delta", Text: txt})
		}

	case "tool_call":
		callID := acpFirstStr(update, "toolCallId", "tool_call_id", "id")
		toolName := acpFirstStr(update, "toolName", "title", "name", "kind")
		rawInput := firstMapAny(update, "rawInput", "input", "arguments")
		if callID != "" {
			if toolName != "" {
				s.toolUses[callID] = toolName
			}
			if mInp, ok := rawInput.(map[string]any); ok {
				s.toolInputs[callID] = mInp
			} else if sInp, ok := rawInput.(string); ok {
				var parsed map[string]any
				if json.Unmarshal([]byte(sInp), &parsed) == nil {
					s.toolInputs[callID] = parsed
				}
			}
		}
		s.needsSeparation = true
		events = append(events, agent.ChatStreamEvent{SessionID: sessionID, Type: "tool_start", ToolName: toolName, ToolInput: rawInput})

	case "tool_call_update":
		callID := acpFirstStr(update, "toolCallId", "tool_call_id", "id")
		if title := acpFirstStr(update, "toolName", "title", "name"); title != "" && callID != "" && s.toolUses[callID] == "" {
			s.toolUses[callID] = title
		}
		status := strings.ToLower(acpFirstStr(update, "status"))
		if status == "completed" || status == "failed" || status == "error" {
			s.needsSeparation = true
			isErr := status == "failed" || status == "error"
			outStr := stringifyACPValue(firstMapAny(update, "rawOutput", "output", "result", "content"))
			tName := s.toolUses[callID]
			if tName == "" {
				tName = acpFirstStr(update, "toolName", "title", "name")
			}
			tInput := s.toolInputs[callID]
			events = append(events, agent.ChatStreamEvent{
				SessionID: sessionID, Type: "tool_result", ToolName: tName, ToolInput: tInput, ToolOutput: outStr, IsError: isErr,
			})
			if !s.emittedAuthBlock {
				if authBlock := DetectToolResultAuthBlock(agentKey, tName, tInput, outStr, isErr); authBlock != nil {
					s.emittedAuthBlock = true
					events = append(events, agent.ChatStreamEvent{
						SessionID: sessionID, Type: "interactive_block", ToolName: tName, ToolInput: tInput, Blocked: authBlock,
					})
				}
			}
		}

	case "available_commands_update":
		rawCmds := update["availableCommands"]
		if rawCmds == nil {
			rawCmds = update["commands"]
		}
		res.AvailableCommands = ParseACPCommands(rawCmds)

	case "current_mode_update", "config_option_update":
		res.DetectedModel = extractACPModelFromMap(update)
	}
	return events, res
}

func acpFirstStr(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func firstMapAny(m map[string]any, keys ...string) any {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func firstNonNilMapVal(primary map[string]any, key string, fallback map[string]any) any {
	if primary != nil {
		if v, ok := primary[key]; ok && v != nil {
			return v
		}
	}
	if fallback != nil {
		return fallback[key]
	}
	return nil
}
