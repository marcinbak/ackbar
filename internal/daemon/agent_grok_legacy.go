package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func extractGrokUpdatesMessages(updatesFile string) ([]TranscriptMessage, error) {
	f, err := os.Open(updatesFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []TranscriptMessage
	var pendingAssistant *TranscriptMessage
	var currentPromptChunks []string
	var currentPromptTS time.Time
	adapter := GrokAgentAdapter{}

	flushUser := func() {
		if len(currentPromptChunks) > 0 {
			fullPrompt := strings.Join(currentPromptChunks, "")
			currentPromptChunks = nil
			ts := currentPromptTS
			currentPromptTS = time.Time{}
			if cleaned, keep := adapter.FilterUserEntry(nil, fullPrompt); keep {
				messages = append(messages, TranscriptMessage{
					Role:      "user",
					Content:   cleaned,
					Timestamp: ts,
				})
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
			if t, ok := m["text"].(string); ok {
				return t
			}
			if t, ok := m["content"].(string); ok {
				return t
			}
		}
		return ""
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		params, _ := raw["params"].(map[string]interface{})
		upd, _ := params["update"].(map[string]interface{})
		target := upd
		if target == nil {
			target = raw
		}

		uType, _ := target["sessionUpdate"].(string)
		if uType == "" {
			uType, _ = target["update_type"].(string)
		}
		if uType == "" {
			uType, _ = target["type"].(string)
		}

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
			chunk := extractChunk(target, "content")
			currentPromptChunks = append(currentPromptChunks, chunk)
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
					pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
				}
				pendingAssistant.Thinking += thought
			}
		case "tool_call":
			flushUser()
			toolName := ""
			for _, k := range []string{"title", "name", "toolName"} {
				if s, ok := target[k].(string); ok && s != "" {
					toolName = s
					break
				}
			}
			var toolInput interface{}
			if rawIn, exists := target["rawInput"]; exists && rawIn != nil {
				toolInput = rawIn
			} else {
				toolInput = parseGrokToolInput(target)
			}
			if pendingAssistant == nil {
				pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, FormatToolDisplay(toolName, toolInput))
		case "agent_message_chunk":
			flushUser()
			chunk := extractChunk(target, "content")
			if chunk != "" {
				if pendingAssistant == nil {
					pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
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

func findGrokLegacyLog(home, nativeID string) string {
	if !IsSafeGrokSessionID(nativeID) {
		return ""
	}
	if home == "" {
		home = realOSUserHome()
	}
	for _, baseDir := range []string{GrokHomeDir(home), filepath.Join(home, ".grok")} {
		cand1 := filepath.Join(baseDir, "sessions", nativeID+".jsonl")
		if stat, err := os.Stat(cand1); err == nil && !stat.IsDir() {
			return cand1
		}
		cand2 := filepath.Join(baseDir, "sessions", nativeID, "transcript.jsonl")
		if stat, err := os.Stat(cand2); err == nil && !stat.IsDir() {
			return cand2
		}
		sessRoot := filepath.Join(baseDir, "sessions")
		if entries, err := os.ReadDir(sessRoot); err == nil {
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() && strings.Contains(name, nativeID) && strings.HasSuffix(name, ".jsonl") {
					return filepath.Join(sessRoot, name)
				}
			}
		}
	}
	return ""
}

func extractGrokLegacyMessages(logPath string) ([]TranscriptMessage, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var messages []TranscriptMessage
	var pendingAssistant *TranscriptMessage
	adapter := GrokAgentAdapter{}

	flushAssistant := func() {
		if pendingAssistant != nil {
			if pendingAssistant.Content != "" || len(pendingAssistant.ToolCalls) > 0 || pendingAssistant.Thinking != "" {
				messages = append(messages, *pendingAssistant)
			}
			pendingAssistant = nil
		}
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
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
			content := parseGrokContentText(raw["content"])
			if cleaned, keep := adapter.FilterUserEntry(raw, content); keep {
				messages = append(messages, TranscriptMessage{
					Role:      "user",
					Content:   cleaned,
					Timestamp: ts,
				})
			}
		} else if role == "assistant" || tVal == "assistant" {
			content := parseGrokContentText(raw["content"])
			if pendingAssistant == nil {
				pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
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
			input := parseGrokToolInput(raw)
			display := FormatToolDisplay(name, input)
			if pendingAssistant == nil {
				pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, display)
		}
	}
	flushAssistant()

	return messages, nil
}
