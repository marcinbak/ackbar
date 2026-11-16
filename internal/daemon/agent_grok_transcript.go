package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ExtractGrokTranscriptMessages(home, cwd, nativeID string) ([]TranscriptMessage, error) {
	if !IsSafeGrokSessionID(nativeID) {
		return nil, fmt.Errorf("invalid grok session ID: %q", nativeID)
	}
	sessDir := FindGrokSessionDirForHome(home, cwd, nativeID)
	if sessDir != "" {
		chatFile := filepath.Join(sessDir, "chat_history.jsonl")
		if stat, err := os.Stat(chatFile); err == nil && !stat.IsDir() {
			return extractGrokChatHistoryMessages(chatFile, sessDir)
		}
		updatesFile := filepath.Join(sessDir, "updates.jsonl")
		if stat, err := os.Stat(updatesFile); err == nil && !stat.IsDir() {
			return extractGrokUpdatesMessages(updatesFile)
		}
	}

	legacyFile := findGrokLegacyLog(home, nativeID)
	if legacyFile != "" {
		return extractGrokLegacyMessages(legacyFile)
	}

	return nil, fmt.Errorf("grok transcript not found for session %q", nativeID)
}

func extractGrokChatHistoryMessages(chatFile, sessDir string) ([]TranscriptMessage, error) {
	f, err := os.Open(chatFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

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
		tVal, _ := raw["type"].(string)
		roleVal, _ := raw["role"].(string)
		ts := parseGrokTime(raw["timestamp"])
		if ts.IsZero() {
			ts = parseGrokTime(raw["created_at"])
		}

		if tVal == "system" || roleVal == "system" {
			continue
		}
		if tVal == "function_call_output" || tVal == "tool_result" {
			continue
		}

		if tVal == "user" || roleVal == "user" {
			text := parseGrokContentText(raw["content"])
			if text == "" {
				text, _ = raw["text"].(string)
			}
			if cleaned, keep := adapter.FilterUserEntry(raw, text); keep {
				flushAssistant()
				messages = append(messages, TranscriptMessage{
					Role:      "user",
					Content:   cleaned,
					Timestamp: ts,
				})
			}
			continue
		}

		if tVal == "reasoning" {
			thinking := parseGrokContentText(raw["summary"])
			if thinking == "" {
				thinking = parseGrokContentText(raw["content"])
			}
			if thinking != "" {
				if pendingAssistant == nil {
					pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
				}
				if pendingAssistant.Thinking != "" {
					pendingAssistant.Thinking += "\n" + thinking
				} else {
					pendingAssistant.Thinking = thinking
				}
			}
			continue
		}

		if tVal == "function_call" || tVal == "tool_call" || tVal == "custom_tool_call" {
			toolName, _ := raw["name"].(string)
			if toolName == "" {
				toolName, _ = raw["tool"].(string)
			}
			toolInput := parseGrokToolInput(raw)
			display := FormatToolDisplay(toolName, toolInput)
			if pendingAssistant == nil {
				pendingAssistant = &TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, display)
			continue
		}

		if tVal == "assistant" || roleVal == "assistant" {
			content := parseGrokContentText(raw["content"])
			if content == "" {
				content, _ = raw["text"].(string)
			}
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
			if !ts.IsZero() && pendingAssistant.Timestamp.IsZero() {
				pendingAssistant.Timestamp = ts
			}
			if tCalls, ok := raw["tool_calls"].([]interface{}); ok {
				for _, tc := range tCalls {
					if tcMap, ok := tc.(map[string]interface{}); ok {
						tName, _ := tcMap["name"].(string)
						tInput := parseGrokToolInput(tcMap)
						pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, FormatToolDisplay(tName, tInput))
					}
				}
			}
		}
	}
	flushAssistant()

	return messages, nil
}
