package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TranscriptMessage struct {
	Role      string    `json:"role"` // "user", "assistant", "system", "tool"
	Content   string    `json:"content"`
	Thinking  string    `json:"thinking,omitempty"`
	ToolCalls []string  `json:"tool_calls,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type Transcript struct {
	SessionID string              `json:"session_id"`
	NativeID  string              `json:"native_id"`
	Agent     string              `json:"agent"`
	Title     string              `json:"title,omitempty"`
	Cwd       string              `json:"cwd,omitempty"`
	Messages  []TranscriptMessage `json:"messages"`
}

// ExtractTranscript loads and parses conversation logs for a session from disk
func ExtractTranscript(agent, nativeID, cwd string) (*Transcript, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	t := &Transcript{
		NativeID: nativeID,
		Agent:    agent,
		Cwd:      cwd,
		Messages: make([]TranscriptMessage, 0),
	}

	switch agent {
	case "antigravity":
		if err := loadAntigravityTranscript(t, home, nativeID); err != nil {
			return nil, err
		}
	case "claude-code":
		if err := loadClaudeTranscript(t, home, nativeID, cwd); err != nil {
			return nil, err
		}
	default:
		// Attempt Antigravity then Claude
		if err := loadAntigravityTranscript(t, home, nativeID); err != nil {
			if err2 := loadClaudeTranscript(t, home, nativeID, cwd); err2 != nil {
				return nil, fmt.Errorf("transcript not found for agent %s (%s)", agent, nativeID)
			}
		}
	}

	return t, nil
}

func loadAntigravityTranscript(t *Transcript, home, convID string) error {
	logPath := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs", "transcript.jsonl")
	file, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("antigravity log not found at %s: %w", logPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024) // 1MB buffer for large entries
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry struct {
			StepIndex int    `json:"step_index"`
			Source    string `json:"source"`
			Type      string `json:"type"`
			Status    string `json:"status"`
			CreatedAt string `json:"created_at"`
			Content   string `json:"content"`
			Thinking  string `json:"thinking"`
			ToolCalls []struct {
				Name string                 `json:"name"`
				Args map[string]interface{} `json:"args"`
			} `json:"tool_calls"`
		}

		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339, entry.CreatedAt)
		if ts.IsZero() {
			ts = time.Now()
		}

		switch entry.Type {
		case "USER_INPUT":
			cleanContent := cleanAntigravityPrompt(entry.Content)
			if cleanContent != "" {
				t.Messages = append(t.Messages, TranscriptMessage{
					Role:      "user",
					Content:   cleanContent,
					Timestamp: ts,
				})
			}

		case "PLANNER_RESPONSE":
			var toolSummaries []string
			for _, tc := range entry.ToolCalls {
				action := ""
				if tc.Args != nil {
					if act, ok := tc.Args["toolAction"].(string); ok && act != "" {
						action = strings.Trim(act, "\"")
					} else if sum, ok := tc.Args["toolSummary"].(string); ok && sum != "" {
						action = strings.Trim(sum, "\"")
					}
				}
				if action != "" {
					toolSummaries = append(toolSummaries, fmt.Sprintf("%s (%s)", tc.Name, action))
				} else {
					toolSummaries = append(toolSummaries, tc.Name)
				}
			}

			if entry.Content != "" || entry.Thinking != "" || len(toolSummaries) > 0 {
				t.Messages = append(t.Messages, TranscriptMessage{
					Role:      "assistant",
					Content:   entry.Content,
					Thinking:  entry.Thinking,
					ToolCalls: toolSummaries,
					Timestamp: ts,
				})
			}

		case "CHECKPOINT":
			// Truncation summaries
			if strings.Contains(entry.Content, "CHECKPOINT") {
				t.Messages = append(t.Messages, TranscriptMessage{
					Role:      "system",
					Content:   strings.TrimSpace(entry.Content),
					Timestamp: ts,
				})
			}
		}
	}

	return nil
}

func loadClaudeTranscript(t *Transcript, home, sessionID, cwd string) error {
	var targetFile string
	projectsDir := filepath.Join(home, ".claude", "projects")

	// Look directly in projects dir if cwd encoded
	if cwd != "" {
		encodedCwd := encodeClaudeProjectDir(cwd)
		cand := filepath.Join(projectsDir, encodedCwd, sessionID+".jsonl")
		if fileExists(cand) {
			targetFile = cand
		}
	}

	// Fallback: search all project dirs for sessionID.jsonl
	if targetFile == "" && dirExists(projectsDir) {
		entries, err := os.ReadDir(projectsDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					cand := filepath.Join(projectsDir, e.Name(), sessionID+".jsonl")
					if fileExists(cand) {
						targetFile = cand
						break
					}
				}
			}
		}
	}

	if targetFile == "" {
		return fmt.Errorf("claude log not found for session %s", sessionID)
	}

	file, err := os.Open(targetFile)
	if err != nil {
		return err
	}
	defer file.Close()

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

		msgType, _ := raw["type"].(string)
		tsStr, _ := raw["timestamp"].(string)
		ts, _ := time.Parse(time.RFC3339, tsStr)
		if ts.IsZero() {
			ts = time.Now()
		}

		if msgType == "user" {
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentArr, ok := msgObj["content"].([]interface{}); ok {
					var textParts []string
					for _, cItem := range contentArr {
						if cMap, ok := cItem.(map[string]interface{}); ok {
							if cType, ok := cMap["type"].(string); ok && cType == "text" {
								if txt, ok := cMap["text"].(string); ok && txt != "" {
									textParts = append(textParts, txt)
								}
							}
						}
					}
					fullText := strings.Join(textParts, "\n")
					if fullText != "" && !strings.Contains(fullText, "<EXTREMELY_IMPORTANT>") {
						t.Messages = append(t.Messages, TranscriptMessage{
							Role:      "user",
							Content:   fullText,
							Timestamp: ts,
						})
					}
				}
			}
		} else if msgType == "assistant" {
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentArr, ok := msgObj["content"].([]interface{}); ok {
					var textParts []string
					var tools []string
					for _, cItem := range contentArr {
						if cMap, ok := cItem.(map[string]interface{}); ok {
							if cType, ok := cMap["type"].(string); ok {
								if cType == "text" {
									if txt, ok := cMap["text"].(string); ok && txt != "" {
										textParts = append(textParts, txt)
									}
								} else if cType == "tool_use" {
									tName, _ := cMap["name"].(string)
									if tName != "" {
										tools = append(tools, tName)
									}
								}
							}
						}
					}
					fullText := strings.Join(textParts, "\n")
					if fullText != "" || len(tools) > 0 {
						t.Messages = append(t.Messages, TranscriptMessage{
							Role:      "assistant",
							Content:   fullText,
							ToolCalls: tools,
							Timestamp: ts,
						})
					}
				}
			}
		}
	}

	return nil
}

func encodeClaudeProjectDir(p string) string {
	clean := filepath.Clean(p)
	return strings.ReplaceAll(clean, string(filepath.Separator), "-")
}

func cleanAntigravityPrompt(raw string) string {
	s := raw
	if strings.Contains(s, "<USER_REQUEST>") && strings.Contains(s, "</USER_REQUEST>") {
		start := strings.Index(s, "<USER_REQUEST>") + len("<USER_REQUEST>")
		end := strings.Index(s, "</USER_REQUEST>")
		if end > start {
			s = s[start:end]
		}
	}
	// Strip metadata / setting tags
	for _, tag := range []string{"<ADDITIONAL_METADATA>", "<USER_SETTINGS_CHANGE>", "<SYSTEM_MESSAGE>"} {
		if idx := strings.Index(s, tag); idx != -1 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}

// FormatTranscriptANSI converts messages into colorized terminal output
func FormatTranscriptANSI(t *Transcript) string {
	if t == nil || len(t.Messages) == 0 {
		return "\r\n\x1b[90m(No prior transcript messages found)\x1b[0m\r\n\r\n"
	}

	var sb strings.Builder
	sb.WriteString("\r\n\x1b[1;36m╭──────────────────────────────────────────────────────────────╮\x1b[0m\r\n")
	sb.WriteString(fmt.Sprintf("\x1b[1;36m│ 📜 Session Transcript History (%d steps)                     │\x1b[0m\r\n", len(t.Messages)))
	sb.WriteString("\x1b[1;36m╰──────────────────────────────────────────────────────────────╯\x1b[0m\r\n\r\n")

	for i, m := range t.Messages {
		timeLabel := m.Timestamp.Format("15:04:05")
		switch m.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("\x1b[1;32m┌── 👤 User [%s] ───────────────────────────────\x1b[0m\r\n", timeLabel))
			lines := strings.Split(strings.TrimSpace(m.Content), "\n")
			for _, l := range lines {
				sb.WriteString(fmt.Sprintf("\x1b[32m│\x1b[0m %s\r\n", l))
			}
			sb.WriteString("\x1b[1;32m└──────────────────────────────────────────────────────────\x1b[0m\r\n\r\n")

		case "assistant":
			sb.WriteString(fmt.Sprintf("\x1b[1;34m┌── 🤖 Assistant [%s] ──────────────────────────\x1b[0m\r\n", timeLabel))
			if len(m.ToolCalls) > 0 {
				sb.WriteString(fmt.Sprintf("\x1b[34m│\x1b[0m \x1b[33m⚡ Tools:\x1b[0m %s\r\n", strings.Join(m.ToolCalls, ", ")))
			}
			if m.Thinking != "" {
				firstLine := strings.Split(strings.TrimSpace(m.Thinking), "\n")[0]
				if len(firstLine) > 80 {
					firstLine = firstLine[:80] + "..."
				}
				sb.WriteString(fmt.Sprintf("\x1b[34m│\x1b[0m \x1b[90m💭 Thought: %s\x1b[0m\r\n", firstLine))
			}
			if m.Content != "" {
				lines := strings.Split(strings.TrimSpace(m.Content), "\n")
				// Print up to 15 lines if long to keep terminal responsive
				maxLines := 15
				if i == len(t.Messages)-1 {
					maxLines = 40
				}
				for li, l := range lines {
					if li >= maxLines {
						sb.WriteString(fmt.Sprintf("\x1b[34m│\x1b[0m \x1b[90m... [%d more lines] ...\x1b[0m\r\n", len(lines)-maxLines))
						break
					}
					sb.WriteString(fmt.Sprintf("\x1b[34m│\x1b[0m %s\r\n", l))
				}
			}
			sb.WriteString("\x1b[1;34m└──────────────────────────────────────────────────────────\x1b[0m\r\n\r\n")

		case "system":
			sb.WriteString(fmt.Sprintf("\x1b[90m── ℹ️ System [%s]: %s ──\x1b[0m\r\n\r\n", timeLabel, truncateTitle(m.Content)))
		}
	}

	sb.WriteString("\x1b[1;33m────────────────────────────────────────────────────────────\x1b[0m\r\n")
	sb.WriteString("\x1b[1;33m▶ Resuming Live Session Terminal Below...\x1b[0m\r\n")
	sb.WriteString("\x1b[1;33m────────────────────────────────────────────────────────────\x1b[0m\r\n\r\n")

	return sb.String()
}

// FormatTranscriptMarkdown converts messages into GitHub-flavored Markdown
func FormatTranscriptMarkdown(t *Transcript) string {
	if t == nil || len(t.Messages) == 0 {
		return "*(No transcript messages recorded for this session)*\n"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Session Transcript — %s\n\n", t.Title))
	if t.Cwd != "" {
		sb.WriteString(fmt.Sprintf("> **Workspace:** `%s` • **Agent:** `%s` • **Steps:** %d\n\n", t.Cwd, t.Agent, len(t.Messages)))
	}

	for _, m := range t.Messages {
		timeLabel := m.Timestamp.Format("2006-01-02 15:04:05")
		switch m.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("### 👤 User (`%s`)\n\n", timeLabel))
			sb.WriteString(m.Content)
			sb.WriteString("\n\n---\n\n")

		case "assistant":
			sb.WriteString(fmt.Sprintf("### 🤖 Assistant (`%s`)\n\n", timeLabel))
			if len(m.ToolCalls) > 0 {
				sb.WriteString("**Tools Invoked:**\n")
				for _, tc := range m.ToolCalls {
					sb.WriteString(fmt.Sprintf("- 🛠️ `%s`\n", tc))
				}
				sb.WriteString("\n")
			}
			if m.Thinking != "" {
				sb.WriteString("<details><summary>💭 Thought Process</summary>\n\n")
				sb.WriteString(m.Thinking)
				sb.WriteString("\n\n</details>\n\n")
			}
			if m.Content != "" {
				sb.WriteString(m.Content)
				sb.WriteString("\n\n")
			}
			sb.WriteString("---\n\n")

		case "system":
			sb.WriteString(fmt.Sprintf("> ℹ️ **System (%s):** %s\n\n", timeLabel, m.Content))
		}
	}

	return sb.String()
}
