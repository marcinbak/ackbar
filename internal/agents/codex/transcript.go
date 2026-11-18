package codex

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

func (p *Plugin) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
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
	return findCodexSessionLog(homeDir, nativeID) != ""
}

func (*Plugin) CleanSessionFiles(homeDir, cwd, nativeID string) error {
	_ = cwd
	if !agentutil.IsSafeSessionID(nativeID) {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(HomeDir(homeDir), "sessions", nativeID))
	if logFile := findCodexSessionLog(homeDir, nativeID); logFile != "" {
		_ = os.Remove(logFile)
	}
	return nil
}

func (*Plugin) StatTranscript(homeDir, cwd, nativeID string) (string, int64, int64, error) {
	_ = cwd
	if nativeID == "" {
		return "", 0, 0, fmt.Errorf("nativeID cannot be empty")
	}
	filePath := findCodexSessionLog(homeDir, nativeID)
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
		return nil, fmt.Errorf("codex nativeID cannot be empty")
	}
	logFile := findCodexSessionLog(homeDir, nativeID)
	if logFile == "" {
		return nil, fmt.Errorf("codex session log not found for session %s", nativeID)
	}
	file, err := os.Open(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open codex log file: %w", err)
	}
	defer file.Close()

	var messages []agent.TranscriptMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil || raw.Type != "response_item" {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, raw.Timestamp)
		if ts.IsZero() {
			ts = time.Now()
		}
		var ri struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Name    string `json:"name"`
			Input   string `json:"input"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw.Payload, &ri) != nil {
			continue
		}
		switch {
		case ri.Type == "message" && ri.Role == "user":
			var textParts []string
			for _, part := range ri.Content {
				if cleaned, keep := p.FilterUserEntry(nil, part.Text); keep {
					textParts = append(textParts, cleaned)
				}
			}
			if len(textParts) > 0 {
				messages = append(messages, agent.TranscriptMessage{
					Role:      "user",
					Content:   strings.Join(textParts, "\n\n"),
					Timestamp: ts,
				})
			}
		case ri.Type == "message" && ri.Role == "assistant":
			var textParts []string
			for _, part := range ri.Content {
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			}
			fullText := strings.Join(textParts, "\n\n")
			if fullText == "" {
				continue
			}
			if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
				last := &messages[len(messages)-1]
				if last.Content != "" {
					if !strings.Contains(last.Content, fullText) {
						last.Content += "\n\n" + fullText
					}
				} else {
					last.Content = fullText
				}
				last.Timestamp = ts
			} else {
				messages = append(messages, agent.TranscriptMessage{
					Role:      "assistant",
					Content:   fullText,
					Timestamp: ts,
				})
			}
		case ri.Type == "custom_tool_call" && ri.Name != "":
			toolSummary := ri.Name
			if ri.Input != "" {
				toolSummary += ": " + agentutil.TruncateTitle(ri.Input, 50)
			}
			if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
				messages[len(messages)-1].ToolCalls = append(messages[len(messages)-1].ToolCalls, toolSummary)
			} else {
				messages = append(messages, agent.TranscriptMessage{
					Role:      "assistant",
					ToolCalls: []string{toolSummary},
					Timestamp: ts,
				})
			}
		}
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("no conversation messages found in codex log for %s", nativeID)
	}
	return &agent.Transcript{NativeID: nativeID, Agent: "codex", Cwd: cwd, Messages: messages}, nil
}

func findCodexSessionLog(homeDir, nativeID string) string {
	if !agentutil.IsSafeSessionID(nativeID) {
		return ""
	}
	roots := []string{HomeDir(homeDir)}
	// Account profile dirs passed as homeDir place sessions/ directly under homeDir.
	if homeDir != "" && (agentutil.DirExists(filepath.Join(homeDir, "sessions")) || agentutil.DirExists(filepath.Join(homeDir, "archived_sessions"))) {
		roots = append(roots, homeDir)
	}
	for _, root := range roots {
		for _, pattern := range []string{
			filepath.Join(root, "archived_sessions", "*"+nativeID+"*.jsonl"),
			filepath.Join(root, "sessions", "*", "*", "*", "*"+nativeID+"*.jsonl"),
			filepath.Join(root, "sessions", "*"+nativeID+"*.jsonl"),
		} {
			if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
				return matches[0]
			}
		}
		var found string
		_ = filepath.WalkDir(filepath.Join(root, "sessions"), func(path string, d os.DirEntry, err error) error {
			if err != nil || found != "" {
				return filepath.SkipAll
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") && strings.Contains(d.Name(), nativeID) {
				found = path
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}
