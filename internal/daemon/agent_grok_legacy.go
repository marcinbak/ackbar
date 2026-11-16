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
			if cleaned, keep := adapter.FilterUserEntry(nil, fullPrompt); keep {
				messages = append(messages, TranscriptMessage{
					Role:      "user",
					Content:   cleaned,
					Timestamp: currentPromptTS,
				})
			}
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
		uType, _ := raw["sessionUpdate"].(string)
		if uType == "" {
			uType, _ = raw["update_type"].(string)
		}
		if uType == "" {
			uType, _ = raw["type"].(string)
		}
		ts := parseGrokTime(raw["timestamp"])
		if ts.IsZero() {
			if metaMap, ok := raw["_meta"].(map[string]interface{}); ok {
				ts = parseGrokTime(metaMap["agentTimestampMs"])
			}
		}

		switch uType {
		case "user_message_chunk":
			flushAssistant()
			chunk, _ := raw["content"].(string)
			if chunk == "" {
				chunk, _ = raw["text"].(string)
			}
			currentPromptChunks = append(currentPromptChunks, chunk)
			if currentPromptTS.IsZero() {
				currentPromptTS = ts
			}
		case "agent_thought_chunk":
			flushUser()
			thought, _ := raw["thought"].(string)
			if thought == "" {
				thought, _ = raw["content"].(string)
			}
			if thought != "" {
				if pendingAssistant == nil {
					pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
				}
				pendingAssistant.Thinking += thought
			}
		case "tool_call":
			flushUser()
			toolName, _ := raw["name"].(string)
			toolInput := parseGrokToolInput(raw)
			if pendingAssistant == nil {
				pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, FormatToolDisplay(toolName, toolInput))
		case "agent_message_chunk":
			flushUser()
			chunk, _ := raw["content"].(string)
			if chunk == "" {
				chunk, _ = raw["text"].(string)
			}
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
		if matches, err := filepath.Glob(filepath.Join(baseDir, "sessions", "*"+nativeID+"*.jsonl")); err == nil && len(matches) > 0 {
			return matches[0]
		}
		if matches, err := filepath.Glob(filepath.Join(baseDir, "sessions", "*", "*", "*", "*"+nativeID+"*.jsonl")); err == nil && len(matches) > 0 {
			return matches[0]
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
