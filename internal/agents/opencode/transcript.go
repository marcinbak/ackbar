package opencode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	_ = raw
	clean := agentutil.CleanCLIEnvironmentTags(content)
	if clean == "" {
		return "", false
	}
	return clean, true
}

func (*Plugin) HasConversationArtifacts(homeDir, nativeID string) bool {
	if homeDir == "" || !agentutil.IsSafeSessionID(nativeID) {
		return false
	}
	return findOpenCodeSessionLog(homeDir, nativeID) != ""
}

func (*Plugin) CleanSessionFiles(homeDir, cwd, nativeID string) error {
	_ = cwd
	if homeDir == "" || !agentutil.IsSafeSessionID(nativeID) {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(HomeDir(homeDir), "sessions", nativeID))
	if logFile := findOpenCodeSessionLog(homeDir, nativeID); logFile != "" {
		_ = os.Remove(logFile)
	}
	return nil
}

func (*Plugin) StatTranscript(homeDir, cwd, nativeID string) (string, int64, int64, error) {
	_ = cwd
	if nativeID == "" {
		return "", 0, 0, fmt.Errorf("nativeID cannot be empty")
	}
	filePath := findOpenCodeSessionLog(homeDir, nativeID)
	if filePath == "" {
		return "", 0, 0, os.ErrNotExist
	}
	st, err := os.Stat(filePath)
	if err != nil {
		return "", 0, 0, err
	}
	return filePath, st.Size(), st.ModTime().UnixNano(), nil
}

func (p *Plugin) ExtractTranscript(homeDir, cwd, nativeID string) (*agent.Transcript, error) {
	if nativeID == "" {
		return nil, fmt.Errorf("opencode nativeID cannot be empty")
	}
	logFile := findOpenCodeSessionLog(homeDir, nativeID)
	if logFile == "" {
		return nil, fmt.Errorf("opencode session log not found for session %s", nativeID)
	}
	file, err := os.Open(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open opencode log file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var messages []agent.TranscriptMessage
	var pendingAssistant *agent.TranscriptMessage

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
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}

		tsStr := raw.Timestamp
		if tsStr == "" {
			tsStr = raw.Time
		}
		ts := parseOpenCodeTime(tsStr)

		if raw.Type == "tool_call" || raw.Type == "tool_use" || raw.Type == "custom_tool_call" {
			toolName := raw.Name
			if toolName == "" {
				toolName = "tool"
			}
			callDesc := toolName
			if len(raw.Input) > 0 {
				callDesc = fmt.Sprintf("%s(%s)", toolName, agentutil.TruncateTitle(string(raw.Input), 50))
			}
			if pendingAssistant == nil {
				pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, callDesc)
			if !ts.IsZero() {
				pendingAssistant.Timestamp = ts
			}
			continue
		}

		role, content := extractUserMessageContent(raw.Type, raw.Role, raw.Content, raw.Payload, nil)

		if role == "user" {
			flushPendingAssistant()
			if cleaned, keep := p.FilterUserEntry(nil, content); keep {
				messages = append(messages, agent.TranscriptMessage{
					Role:      "user",
					Content:   cleaned,
					Timestamp: ts,
				})
			}
		} else if role == "assistant" {
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
		}
	}

	flushPendingAssistant()
	return &agent.Transcript{NativeID: nativeID, Agent: "opencode", Cwd: cwd, Messages: messages}, nil
}

func findOpenCodeSessionLog(homeDir, nativeID string) string {
	if !agentutil.IsSafeSessionID(nativeID) {
		return ""
	}
	ocDir := HomeDir(homeDir)
	if ocDir == ".opencode" {
		return ""
	}
	for _, cand := range []string{
		filepath.Join(ocDir, "sessions", nativeID+".jsonl"),
		filepath.Join(ocDir, "sessions", nativeID, "transcript.jsonl"),
	} {
		if agentutil.FileExists(cand) {
			return cand
		}
	}
	for _, pattern := range []string{
		filepath.Join(ocDir, "sessions", "*"+nativeID+"*.jsonl"),
		filepath.Join(ocDir, "sessions", "*", "*", "*", "*"+nativeID+"*.jsonl"),
	} {
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

func parseContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return strings.TrimSpace(str)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var collected []string
		for _, p := range parts {
			if p.Text != "" {
				collected = append(collected, p.Text)
			}
		}
		return strings.TrimSpace(strings.Join(collected, " "))
	}
	return ""
}

func parseOpenCodeTime(tsStr string) time.Time {
	if tsStr == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
		return t
	}
	return time.Time{}
}
