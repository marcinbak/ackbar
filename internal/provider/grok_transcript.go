package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

// ReadSessionMetadata reads session metadata (title, first prompt, last prompt, version, context pct, LastMessageAt) from Grok files.
func (g *GrokProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	title := g.ResolveSessionTitle(cwd, nativeID)
	logFile := findGrokSessionLog(home, nativeID)
	if logFile == "" {
		if title != "" {
			return &daemon.SessionMeta{
				Title: title,
			}
		}
		return nil
	}

	file, err := os.Open(logFile)
	if err != nil {
		if title != "" {
			return &daemon.SessionMeta{
				Title: title,
			}
		}
		return nil
	}
	defer file.Close()

	meta := &daemon.SessionMeta{
		Title: title,
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var firstPrompt, lastPrompt string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw struct {
			Timestamp string          `json:"timestamp"`
			Time      string          `json:"time"`
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		tsStr := raw.Timestamp
		if tsStr == "" {
			tsStr = raw.Time
		}
		if tsStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
				if t.After(meta.LastMessageAt) {
					meta.LastMessageAt = t
				}
			} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
				if t.After(meta.LastMessageAt) {
					meta.LastMessageAt = t
				}
			}
		}

		// Check for session_meta or version info
		if raw.Type == "session_meta" || raw.Type == "init" {
			var sm struct {
				CliVersion string `json:"cli_version"`
				Version    string `json:"version"`
			}
			data := raw.Payload
			if len(data) == 0 {
				data = []byte(line)
			}
			if err := json.Unmarshal(data, &sm); err == nil {
				if sm.CliVersion != "" {
					meta.Version = sm.CliVersion
				} else if sm.Version != "" {
					meta.Version = sm.Version
				}
			}
		}

		// Check for user prompt text
		promptText := ""
		if raw.Role == "user" {
			promptText = parseContentText(raw.Content)
		} else if raw.Type == "user" || raw.Type == "user_prompt" {
			promptText = parseContentText(raw.Content)
			if promptText == "" && len(raw.Payload) > 0 {
				promptText = parseContentText(raw.Payload)
			}
		} else if raw.Type == "message" || raw.Type == "response_item" {
			var msg struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			src := raw.Payload
			if len(src) == 0 {
				src = []byte(line)
			}
			if err := json.Unmarshal(src, &msg); err == nil && msg.Role == "user" {
				promptText = parseContentText(msg.Content)
			}
		}

		if promptText != "" {
			promptText = cleanInjectedXMLTags(promptText)
			if promptText != "" {
				if firstPrompt == "" {
					firstPrompt = promptText
				}
				lastPrompt = promptText
			}
		}

		// Token usage and context percentage
		if raw.Type == "usage" || raw.Type == "token_count" || strings.Contains(line, "token_usage") {
			var usage struct {
				TotalTokens        int `json:"total_tokens"`
				ModelContextWindow int `json:"model_context_window"`
				ContextWindow      int `json:"context_window"`
				Info               struct {
					TotalTokenUsage struct {
						TotalTokens int `json:"total_tokens"`
					} `json:"total_token_usage"`
					ModelContextWindow int `json:"model_context_window"`
				} `json:"info"`
				RateLimits struct {
					Primary struct {
						UsedPercent float64 `json:"used_percent"`
					} `json:"primary"`
				} `json:"rate_limits"`
			}
			data := raw.Payload
			if len(data) == 0 {
				data = []byte(line)
			}
			if err := json.Unmarshal(data, &usage); err == nil {
				if usage.RateLimits.Primary.UsedPercent > 0 {
					meta.ContextPct = int(usage.RateLimits.Primary.UsedPercent)
				} else {
					tokens := usage.TotalTokens
					if tokens == 0 {
						tokens = usage.Info.TotalTokenUsage.TotalTokens
					}
					window := usage.ModelContextWindow
					if window == 0 {
						window = usage.ContextWindow
					}
					if window == 0 {
						window = usage.Info.ModelContextWindow
					}
					if window > 0 && tokens > 0 {
						meta.ContextPct = (tokens * 100) / window
					}
				}
			}
		}
	}

	meta.FirstPrompt = firstPrompt
	meta.LastPrompt = lastPrompt
	if meta.Title == "" && firstPrompt != "" {
		meta.Title = daemon.TruncateTitle(firstPrompt)
	}

	return meta
}

// ResolveSessionTitle resolves session thread name from ~/.grok/session_index.jsonl
func (g *GrokProvider) ResolveSessionTitle(cwd, nativeID string) string {
	if nativeID == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	indexPath := filepath.Join(home, ".grok", "session_index.jsonl")
	file, err := os.Open(indexPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var latestTitle string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			ThreadName string `json:"thread_name"`
			Name       string `json:"name"`
		}
		if err := json.Unmarshal([]byte(line), &item); err == nil {
			if item.ID == nativeID {
				if item.Title != "" {
					latestTitle = item.Title
				} else if item.ThreadName != "" {
					latestTitle = item.ThreadName
				} else if item.Name != "" {
					latestTitle = item.Name
				}
			}
		}
	}
	return latestTitle
}

// ExtractTranscript extracts user and assistant messages along with tool calls from Grok session logs.
func (g *GrokProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	if nativeID == "" {
		return nil, fmt.Errorf("grok nativeID cannot be empty")
	}

	logFile := findGrokSessionLog(home, nativeID)
	if logFile == "" {
		return nil, fmt.Errorf("grok session log not found for session %s", nativeID)
	}

	file, err := os.Open(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open grok log file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var messages []daemon.TranscriptMessage
	var pendingAssistant *daemon.TranscriptMessage

	flushPendingAssistant := func() {
		if pendingAssistant != nil {
			if pendingAssistant.Content != "" || len(pendingAssistant.ToolCalls) > 0 {
				messages = append(messages, *pendingAssistant)
			}
			pendingAssistant = nil
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw struct {
			Timestamp string          `json:"timestamp"`
			Time      string          `json:"time"`
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			Content   json.RawMessage `json:"content"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		tsStr := raw.Timestamp
		if tsStr == "" {
			tsStr = raw.Time
		}
		var ts time.Time
		if tsStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
				ts = t
			} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
				ts = t
			}
		}

		// Tool call event
		if raw.Type == "tool_call" || raw.Type == "custom_tool_call" {
			toolName := raw.Name
			if toolName == "" {
				toolName = "tool"
			}
			callDesc := toolName
			if len(raw.Input) > 0 {
				callDesc = fmt.Sprintf("%s(%s)", toolName, daemon.TruncateTitle(string(raw.Input)))
			}
			if pendingAssistant == nil {
				pendingAssistant = &daemon.TranscriptMessage{
					Role:      "assistant",
					Timestamp: ts,
				}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, callDesc)
			if !ts.IsZero() {
				pendingAssistant.Timestamp = ts
			}
			continue
		}

		// User message
		role := raw.Role
		content := parseContentText(raw.Content)
		if role == "" && (raw.Type == "user" || raw.Type == "user_prompt") {
			role = "user"
			if content == "" {
				content = parseContentText(raw.Payload)
			}
		} else if (raw.Type == "message" || raw.Type == "response_item") && len(raw.Payload) > 0 {
			var msg struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(raw.Payload, &msg); err == nil && msg.Role != "" {
				role = msg.Role
				content = parseContentText(msg.Content)
			}
		}

		if role == "user" {
			flushPendingAssistant()
			cleaned := cleanInjectedXMLTags(content)
			if cleaned != "" {
				messages = append(messages, daemon.TranscriptMessage{
					Role:      "user",
					Content:   cleaned,
					Timestamp: ts,
				})
			}
		} else if role == "assistant" {
			if pendingAssistant == nil {
				pendingAssistant = &daemon.TranscriptMessage{
					Role:      "assistant",
					Timestamp: ts,
				}
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
		}
	}

	flushPendingAssistant()
	return messages, nil
}

// parseContentText parses string or array of text blocks from JSON content.
func parseContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return strings.TrimSpace(str)
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var collected []string
		for _, p := range parts {
			if p.Text != "" {
				collected = append(collected, p.Text)
			}
		}
		return strings.TrimSpace(strings.Join(collected, " "))
	}

	var singlePart struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &singlePart); err == nil && singlePart.Text != "" {
		return strings.TrimSpace(singlePart.Text)
	}

	return ""
}

// findGrokSessionLog searches for Grok session log files safely without path traversal.
func findGrokSessionLog(home, nativeID string) string {
	if home == "" || nativeID == "" || !isSafeSessionID(nativeID) {
		return ""
	}

	// 1. Direct file ~/.grok/sessions/<nativeID>.jsonl
	cand1 := filepath.Join(home, ".grok", "sessions", nativeID+".jsonl")
	if fileExists(cand1) {
		return cand1
	}

	// 2. Directory file ~/.grok/sessions/<nativeID>/transcript.jsonl
	cand2 := filepath.Join(home, ".grok", "sessions", nativeID, "transcript.jsonl")
	if fileExists(cand2) {
		return cand2
	}

	// 3. Glob match in sessions/
	pattern := filepath.Join(home, ".grok", "sessions", "*"+nativeID+"*.jsonl")
	if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
		return matches[0]
	}

	// 4. Glob match in subdirectories YYYY/MM/DD/
	patternSub := filepath.Join(home, ".grok", "sessions", "*", "*", "*", "*"+nativeID+"*.jsonl")
	if matches, err := filepath.Glob(patternSub); err == nil && len(matches) > 0 {
		return matches[0]
	}

	return ""
}
