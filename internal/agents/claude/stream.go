package claude

import (
	"encoding/json"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (p *Plugin) ParseStreamEvent(line []byte) ([]agent.ChatStreamEvent, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, nil
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, nil
	}

	evtType, _ := raw["type"].(string)
	now := time.Now()
	var events []agent.ChatStreamEvent

	switch evtType {
	case "keep_alive":
		return nil, nil

	case "system":
		if m, _ := raw["model"].(string); m != "" {
			events = append(events, agent.ChatStreamEvent{Type: "model_detected", Model: m, Timestamp: now})
		}

	case "content_block_delta":
		if delta, ok := raw["delta"].(map[string]interface{}); ok {
			switch delta["type"] {
			case "text_delta":
				if txt, _ := delta["text"].(string); txt != "" {
					events = append(events, agent.ChatStreamEvent{Type: "text_delta", Text: txt, Timestamp: now})
				}
			case "thinking_delta":
				if th, _ := delta["thinking"].(string); th != "" {
					events = append(events, agent.ChatStreamEvent{Type: "thought_delta", Thinking: th, Timestamp: now})
				}
			}
		}

	case "content_block_start":
		if cb, ok := raw["content_block"].(map[string]interface{}); ok && cb["type"] == "tool_use" {
			tName, _ := cb["name"].(string)
			tInput := cb["input"]
			events = append(events, agent.ChatStreamEvent{Type: "tool_start", ToolName: tName, ToolInput: tInput, Timestamp: now})
			if blocked := agentutil.ParseInteractiveToolBlock(tName, tInput); blocked != nil {
				events = append(events, agent.ChatStreamEvent{Type: "blocked", Blocked: blocked, ToolName: tName, ToolInput: tInput, Timestamp: now})
			}
		}

	case "tool_result":
		outStr := extractToolResultOutput(raw["content"])
		isErr, _ := raw["is_error"].(bool)
		tName, _ := raw["tool_name"].(string)
		events = append(events, agent.ChatStreamEvent{Type: "tool_result", ToolName: tName, ToolOutput: outStr, IsError: isErr, Timestamp: now})
		if authBlk := agentutil.DetectToolResultAuthBlock("", tName, nil, outStr, isErr); authBlk != nil {
			events = append(events, agent.ChatStreamEvent{Type: "blocked", Blocked: authBlk, ToolName: tName, Timestamp: now})
		}

	case "user":
		events = append(events, parseStreamUserEvents(raw, now)...)

	case "assistant":
		events = append(events, parseStreamAssistantEvents(raw, now)...)

	case "error":
		msg := extractStreamErrorText(raw)
		if msg == "" {
			msg = "Stream error"
		}
		events = append(events, agent.ChatStreamEvent{Type: "error", Text: msg, IsError: true, Timestamp: now})

	case "result":
		resText, _ := raw["result"].(string)
		isErr, _ := raw["is_error"].(bool)
		if isErr {
			events = append(events, agent.ChatStreamEvent{Type: "error", Text: strings.TrimSpace(resText), IsError: true, Timestamp: now})
			if limited, _, reason := p.DetectRateLimit(resText, now); limited {
				events = append(events, agent.ChatStreamEvent{Type: "blocked", Text: reason, Timestamp: now})
			} else if authBlk := agentutil.DetectTurnAuthBlock("claude-code", resText); authBlk != nil {
				events = append(events, agent.ChatStreamEvent{Type: "blocked", Blocked: authBlk, Timestamp: now})
			}
		} else {
			events = append(events, agent.ChatStreamEvent{Type: "turn_complete", Text: resText, Timestamp: now})
		}
	}
	return events, nil
}

func parseStreamUserEvents(raw map[string]interface{}, now time.Time) []agent.ChatStreamEvent {
	var events []agent.ChatStreamEvent
	msg, ok := raw["message"].(map[string]interface{})
	if !ok {
		return nil
	}
	switch c := msg["content"].(type) {
	case string:
		if out := ExtractLocalCommandOutput(c); out != "" {
			events = append(events, agent.ChatStreamEvent{Type: "text_delta", Text: out, Timestamp: now})
		}
	case []interface{}:
		for _, item := range c {
			if s, ok := item.(string); ok {
				if out := ExtractLocalCommandOutput(s); out != "" {
					events = append(events, agent.ChatStreamEvent{Type: "text_delta", Text: out, Timestamp: now})
				}
				continue
			}
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if txt, ok := m["text"].(string); ok {
				if out := ExtractLocalCommandOutput(txt); out != "" {
					events = append(events, agent.ChatStreamEvent{Type: "text_delta", Text: out, Timestamp: now})
				}
			}
			if m["type"] == "tool_result" {
				outStr := extractToolResultOutput(m["content"])
				isErr, _ := m["is_error"].(bool)
				events = append(events, agent.ChatStreamEvent{Type: "tool_result", ToolOutput: outStr, IsError: isErr, Timestamp: now})
			}
		}
	}
	return events
}

func parseStreamAssistantEvents(raw map[string]interface{}, now time.Time) []agent.ChatStreamEvent {
	var events []agent.ChatStreamEvent
	msg, ok := raw["message"].(map[string]interface{})
	if !ok {
		return nil
	}
	if m, _ := msg["model"].(string); m != "" {
		events = append(events, agent.ChatStreamEvent{Type: "model_detected", Model: m, Timestamp: now})
	}
	switch c := msg["content"].(type) {
	case string:
		if c != "" {
			events = append(events, agent.ChatStreamEvent{Type: "text_delta", Text: c, Timestamp: now})
		}
	case []interface{}:
		for _, item := range c {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			switch m["type"] {
			case "thinking":
				if th, _ := m["thinking"].(string); th != "" {
					events = append(events, agent.ChatStreamEvent{Type: "thought_delta", Thinking: th, Timestamp: now})
				}
			case "text":
				if txt, _ := m["text"].(string); txt != "" {
					events = append(events, agent.ChatStreamEvent{Type: "text_delta", Text: txt, Timestamp: now})
				}
			case "tool_use":
				tName, _ := m["name"].(string)
				tInput := m["input"]
				events = append(events, agent.ChatStreamEvent{Type: "tool_start", ToolName: tName, ToolInput: tInput, Timestamp: now})
				if blocked := agentutil.ParseInteractiveToolBlock(tName, tInput); blocked != nil {
					events = append(events, agent.ChatStreamEvent{Type: "blocked", Blocked: blocked, ToolName: tName, ToolInput: tInput, Timestamp: now})
				}
			}
		}
	}
	return events
}

func extractToolResultOutput(content interface{}) string {
	if s, ok := content.(string); ok {
		return s
	}
	arr, ok := content.([]interface{})
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, part := range arr {
		if m, ok := part.(map[string]interface{}); ok {
			if txt, ok := m["text"].(string); ok {
				out.WriteString(txt)
			}
		}
	}
	return out.String()
}

func extractStreamErrorText(raw map[string]interface{}) string {
	switch e := raw["error"].(type) {
	case string:
		return strings.TrimSpace(e)
	case map[string]interface{}:
		if msg, ok := e["message"].(string); ok {
			return strings.TrimSpace(msg)
		}
	}
	if msg, ok := raw["message"].(string); ok {
		return strings.TrimSpace(msg)
	}
	return ""
}

func ExtractLocalCommandOutput(content string) string {
	var out strings.Builder
	extractTag := func(open, close string) {
		start := 0
		for {
			i := strings.Index(content[start:], open)
			if i == -1 {
				break
			}
			i += start + len(open)
			j := strings.Index(content[i:], close)
			if j == -1 {
				out.WriteString(content[i:])
				break
			}
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			out.WriteString(content[i : i+j])
			start = i + j + len(close)
		}
	}
	extractTag("<local-command-stdout>", "</local-command-stdout>")
	extractTag("<local-command-stderr>", "</local-command-stderr>")
	return out.String()
}
