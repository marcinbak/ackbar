package grok

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func extractUpdatesMessages(p *Plugin, updatesFile string) ([]agent.TranscriptMessage, error) {
	f, err := os.Open(updatesFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []agent.TranscriptMessage
	var pendingAssistant *agent.TranscriptMessage
	var currentPromptChunks []string
	var currentPromptTS time.Time

	flushUser := func() {
		if len(currentPromptChunks) > 0 {
			fullPrompt := strings.Join(currentPromptChunks, "")
			currentPromptChunks = nil
			ts := currentPromptTS
			currentPromptTS = time.Time{}
			if cleaned, keep := p.FilterUserEntry(nil, fullPrompt); keep {
				messages = append(messages, agent.TranscriptMessage{Role: "user", Content: cleaned, Timestamp: ts})
			}
		} else {
			currentPromptTS = time.Time{}
		}
	}
	flushAssistant := func() {
		if pendingAssistant != nil {
			if pendingAssistant.Content != "" || len(pendingAssistant.ToolCalls) > 0 || pendingAssistant.Thinking != "" {
				messages = append(messages, *pendingAssistant)
			}
			pendingAssistant = nil
		}
	}
	extractChunk := func(obj map[string]interface{}, primaryKey string) string {
		val := obj[primaryKey]
		if val == nil {
			val = obj["text"]
		}
		if s, ok := val.(string); ok {
			return s
		}
		if m, ok := val.(map[string]interface{}); ok {
			return firstMapStr(m, "text", "content")
		}
		return ""
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		params, _ := raw["params"].(map[string]interface{})
		upd, _ := params["update"].(map[string]interface{})
		target := upd
		if target == nil {
			target = raw
		}

		uType := firstMapStr(target, "sessionUpdate", "update_type", "type")
		var ts time.Time
		if metaMap, ok := params["_meta"].(map[string]interface{}); ok {
			ts = parseGrokTime(metaMap["agentTimestampMs"])
		}
		if ts.IsZero() {
			if metaMap, ok := raw["_meta"].(map[string]interface{}); ok {
				ts = parseGrokTime(metaMap["agentTimestampMs"])
			}
		}
		if ts.IsZero() {
			ts = parseGrokTime(raw["timestamp"])
		}

		switch uType {
		case "user_message_chunk":
			flushAssistant()
			currentPromptChunks = append(currentPromptChunks, extractChunk(target, "content"))
			if currentPromptTS.IsZero() {
				currentPromptTS = ts
			}
		case "agent_thought_chunk":
			flushUser()
			thought := extractChunk(target, "thought")
			if thought == "" {
				thought = extractChunk(target, "content")
			}
			if thought != "" {
				if pendingAssistant == nil {
					pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
				}
				pendingAssistant.Thinking += thought
			}
		case "tool_call":
			flushUser()
			toolName := firstMapStr(target, "title", "name", "toolName")
			toolInput := target["rawInput"]
			if toolInput == nil {
				toolInput = parseGrokToolInput(target)
			}
			if pendingAssistant == nil {
				pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, agentutil.FormatToolDisplay(toolName, toolInput))
		case "agent_message_chunk":
			flushUser()
			if chunk := extractChunk(target, "content"); chunk != "" {
				if pendingAssistant == nil {
					pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
				}
				pendingAssistant.Content += chunk
			}
		case "turn_completed":
			flushUser()
			flushAssistant()
		}
	}
	flushUser()
	flushAssistant()
	return messages, nil
}

func extractLegacyMessages(p *Plugin, logPath string) ([]agent.TranscriptMessage, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var messages []agent.TranscriptMessage
	var pendingAssistant *agent.TranscriptMessage
	flushAssistant := func() {
		if pendingAssistant != nil {
			if pendingAssistant.Content != "" || len(pendingAssistant.ToolCalls) > 0 || pendingAssistant.Thinking != "" {
				messages = append(messages, *pendingAssistant)
			}
			pendingAssistant = nil
		}
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		role, _ := raw["role"].(string)
		tVal, _ := raw["type"].(string)
		ts := parseGrokTime(raw["timestamp"])
		if ts.IsZero() {
			ts = parseGrokTime(raw["time"])
		}

		if role == "user" || tVal == "user" {
			flushAssistant()
			if cleaned, keep := p.FilterUserEntry(raw, parseGrokContentText(raw["content"])); keep {
				messages = append(messages, agent.TranscriptMessage{Role: "user", Content: cleaned, Timestamp: ts})
			}
		} else if role == "assistant" || tVal == "assistant" {
			content := parseGrokContentText(raw["content"])
			if pendingAssistant == nil {
				pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			if content != "" {
				if pendingAssistant.Content != "" {
					pendingAssistant.Content += "\n\n" + content
				} else {
					pendingAssistant.Content = content
				}
			}
			if !ts.IsZero() {
				pendingAssistant.Timestamp = ts
			}
		} else if tVal == "tool_call" || tVal == "function_call" {
			name, _ := raw["name"].(string)
			display := agentutil.FormatToolDisplay(name, parseGrokToolInput(raw))
			if pendingAssistant == nil {
				pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, display)
		}
	}
	flushAssistant()
	return messages, nil
}
