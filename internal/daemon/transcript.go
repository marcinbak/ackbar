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

// SubagentInfo represents an active or completed subagent spawned by an AI agent
type SubagentInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty"`
	Prompt    string    `json:"prompt"`
	State     string    `json:"state"` // "running", "completed"
	StartedAt time.Time `json:"started_at"`
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
	candidatePaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", convID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".antigravity", "brain", convID, ".system_generated", "logs", "transcript.jsonl"),
	}

	var file *os.File
	var err error
	for _, path := range candidatePaths {
		file, err = os.Open(path)
		if err == nil {
			break
		}
	}

	if file == nil {
		return fmt.Errorf("antigravity log not found for conversation %s: %w", convID, err)
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
				if len(t.Messages) > 0 && t.Messages[len(t.Messages)-1].Role == "assistant" {
					last := &t.Messages[len(t.Messages)-1]
					if entry.Content != "" {
						if last.Content != "" {
							if !strings.Contains(last.Content, entry.Content) {
								last.Content += "\n\n" + entry.Content
							}
						} else {
							last.Content = entry.Content
						}
					}
					if entry.Thinking != "" {
						if last.Thinking == "" {
							last.Thinking = entry.Thinking
						} else if !strings.Contains(last.Thinking, entry.Thinking) {
							last.Thinking += "\n\n" + entry.Thinking
						}
					}
					if len(toolSummaries) > 0 {
						last.ToolCalls = append(last.ToolCalls, toolSummaries...)
					}
					last.Timestamp = ts
				} else {
					t.Messages = append(t.Messages, TranscriptMessage{
						Role:      "assistant",
						Content:   entry.Content,
						Thinking:  entry.Thinking,
						ToolCalls: toolSummaries,
						Timestamp: ts,
					})
				}
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
			var fullText string
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentStr, ok := msgObj["content"].(string); ok {
					fullText = contentStr
				} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
					var textParts []string
					for _, cItem := range contentArr {
						if cMap, ok := cItem.(map[string]interface{}); ok {
							if cType, ok := cMap["type"].(string); ok && cType == "text" {
								if txt, ok := cMap["text"].(string); ok && txt != "" {
									textParts = append(textParts, txt)
								}
							}
						} else if cStr, ok := cItem.(string); ok && cStr != "" {
							textParts = append(textParts, cStr)
						}
					}
					fullText = strings.Join(textParts, "\n")
				}
			} else if contentStr, ok := raw["content"].(string); ok {
				fullText = contentStr
			} else if textStr, ok := raw["text"].(string); ok {
				fullText = textStr
			}

			fullText = strings.TrimSpace(fullText)
			if fullText != "" && !strings.Contains(fullText, "<EXTREMELY_IMPORTANT>") {
				t.Messages = append(t.Messages, TranscriptMessage{
					Role:      "user",
					Content:   fullText,
					Timestamp: ts,
				})
			}
		} else if msgType == "assistant" {
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentStr, ok := msgObj["content"].(string); ok && contentStr != "" {
					if len(t.Messages) > 0 && t.Messages[len(t.Messages)-1].Role == "assistant" {
						last := &t.Messages[len(t.Messages)-1]
						if last.Content != "" {
							if !strings.Contains(last.Content, contentStr) {
								last.Content += "\n\n" + contentStr
							}
						} else {
							last.Content = contentStr
						}
						last.Timestamp = ts
					} else {
						t.Messages = append(t.Messages, TranscriptMessage{
							Role:      "assistant",
							Content:   contentStr,
							Timestamp: ts,
						})
					}
				} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
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
										detail := ""
										if inputMap, ok := cMap["input"].(map[string]interface{}); ok {
											if cmd, ok := inputMap["command"].(string); ok && cmd != "" {
												detail = strings.TrimSpace(cmd)
											} else if desc, ok := inputMap["description"].(string); ok && desc != "" {
												detail = strings.TrimSpace(desc)
											} else if fp, ok := inputMap["file_path"].(string); ok && fp != "" {
												detail = strings.TrimSpace(fp)
											} else if p, ok := inputMap["path"].(string); ok && p != "" {
												detail = strings.TrimSpace(p)
											} else if pat, ok := inputMap["pattern"].(string); ok && pat != "" {
												detail = strings.TrimSpace(pat)
											} else if q, ok := inputMap["query"].(string); ok && q != "" {
												detail = strings.TrimSpace(q)
											}
										}
										if detail != "" {
											tools = append(tools, fmt.Sprintf("%s: %s", tName, detail))
										} else {
											tools = append(tools, tName)
										}
									}
								}
							}
						}
					}
					fullText := strings.Join(textParts, "\n")
					if fullText != "" || len(tools) > 0 {
						if len(t.Messages) > 0 && t.Messages[len(t.Messages)-1].Role == "assistant" {
							last := &t.Messages[len(t.Messages)-1]
							if fullText != "" {
								if last.Content != "" {
									if !strings.Contains(last.Content, fullText) {
										last.Content += "\n\n" + fullText
									}
								} else {
									last.Content = fullText
								}
							}
							if len(tools) > 0 {
								last.ToolCalls = append(last.ToolCalls, tools...)
							}
							last.Timestamp = ts
						} else {
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
	}

	return nil
}

func encodeClaudeProjectDir(p string) string {
	clean := filepath.Clean(p)
	var b strings.Builder
	for i := 0; i < len(clean); i++ {
		c := clean[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func cleanAntigravityPrompt(raw string) string {
	s := raw

	// If contains <USER_REQUEST>...</USER_REQUEST>, extract the user prompt
	if strings.Contains(s, "<USER_REQUEST>") {
		start := strings.Index(s, "<USER_REQUEST>") + len("<USER_REQUEST>")
		if end := strings.Index(s, "</USER_REQUEST>"); end > start {
			s = s[start:end]
		} else {
			s = s[start:]
		}
	}

	// Remove <CONTEXT_SUMMARY>...</CONTEXT_SUMMARY>
	if start := strings.Index(s, "<CONTEXT_SUMMARY>"); start != -1 {
		if end := strings.Index(s, "</CONTEXT_SUMMARY>"); end != -1 {
			s = s[:start] + s[end+len("</CONTEXT_SUMMARY>"):]
		}
	}

	// Remove system/metadata tags
	for _, tag := range []string{"<ADDITIONAL_METADATA>", "<USER_SETTINGS_CHANGE>", "<SYSTEM_MESSAGE>"} {
		if idx := strings.Index(s, tag); idx != -1 {
			if endTag := "</" + strings.Trim(tag, "<>"); strings.Contains(s, endTag) {
				endIdx := strings.Index(s, endTag) + len(endTag)
				s = s[:idx] + s[endIdx:]
			} else {
				s = s[:idx]
			}
		}
	}

	return strings.TrimSpace(s)
}

// formatToolSummaryCounts summarizes tool calls by tool name (e.g. "Bash x12" or "Bash x3, Grep x2")
func formatToolSummaryCounts(tools []string) string {
	if len(tools) == 0 {
		return ""
	}
	counts := make(map[string]int)
	var orderedNames []string
	for _, t := range tools {
		name := strings.TrimSpace(t)
		if idx := strings.Index(name, ":"); idx != -1 {
			name = strings.TrimSpace(name[:idx])
		} else if idx := strings.Index(name, "("); idx != -1 {
			name = strings.TrimSpace(name[:idx])
		}
		if name == "" {
			name = "tool"
		}
		if counts[name] == 0 {
			orderedNames = append(orderedNames, name)
		}
		counts[name]++
	}

	if len(orderedNames) == 1 {
		name := orderedNames[0]
		count := counts[name]
		if count > 1 {
			return fmt.Sprintf("%s x%d", name, count)
		}
		return name
	}

	var parts []string
	for _, name := range orderedNames {
		parts = append(parts, fmt.Sprintf("%s x%d", name, counts[name]))
	}
	return strings.Join(parts, ", ")
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
				toolCountLabel := formatToolSummaryCounts(m.ToolCalls)
				sb.WriteString(fmt.Sprintf("\x1b[34m│\x1b[0m \x1b[33m⚡ Tools (%s):\x1b[0m %s\r\n", toolCountLabel, strings.Join(m.ToolCalls, ", ")))
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
				toolCountLabel := formatToolSummaryCounts(m.ToolCalls)
				sb.WriteString(fmt.Sprintf("**Tools Invoked (%s):**\n", toolCountLabel))
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

// ExtractSubagents extracts currently running subagents for a session
func ExtractSubagents(agent, nativeID, cwd string) ([]SubagentInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	switch agent {
	case "antigravity":
		return loadAntigravitySubagents(home, nativeID)
	case "claude-code":
		return loadClaudeSubagents(home, nativeID, cwd)
	default:
		subs, err := loadAntigravitySubagents(home, nativeID)
		if err == nil && len(subs) > 0 {
			return subs, nil
		}
		return loadClaudeSubagents(home, nativeID, cwd)
	}
}

func loadAntigravitySubagents(home, convID string) ([]SubagentInfo, error) {
	candidatePaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", convID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".antigravity", "brain", convID, ".system_generated", "logs", "transcript.jsonl"),
	}

	var file *os.File
	var err error
	for _, path := range candidatePaths {
		file, err = os.Open(path)
		if err == nil {
			break
		}
	}

	if file == nil {
		return nil, fmt.Errorf("antigravity log not found for conversation %s: %w", convID, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var activeSubagents []*SubagentInfo
	convMap := make(map[string]*SubagentInfo)
	var latestPendingBatch []*SubagentInfo

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

		// When a new user prompt begins, previous turn's subagents are done
		if entry.Type == "USER_INPUT" {
			activeSubagents = nil
			convMap = make(map[string]*SubagentInfo)
			latestPendingBatch = nil
			continue
		}

		// Check for invoke_subagent tool call
		if entry.Type == "PLANNER_RESPONSE" {
			for _, tc := range entry.ToolCalls {
				if tc.Name == "invoke_subagent" && tc.Args != nil {
					subagentsRaw := tc.Args["Subagents"]
					var subList []map[string]interface{}

					switch v := subagentsRaw.(type) {
					case string:
						var parsed []map[string]interface{}
						if err := json.Unmarshal([]byte(v), &parsed); err == nil {
							subList = parsed
						}
					case []interface{}:
						for _, item := range v {
							if m, ok := item.(map[string]interface{}); ok {
								subList = append(subList, m)
							}
						}
					}

					var batch []*SubagentInfo
					for i, subItem := range subList {
						role, _ := subItem["Role"].(string)
						typeName, _ := subItem["TypeName"].(string)
						prompt, _ := subItem["Prompt"].(string)
						name := role
						if name == "" {
							name = typeName
						}
						if name == "" {
							name = fmt.Sprintf("Subagent #%d", i+1)
						}

						sub := &SubagentInfo{
							ID:        fmt.Sprintf("subagent-%d-%d", entry.StepIndex, i),
							Name:      name,
							Role:      role,
							Prompt:    prompt,
							State:     "running",
							StartedAt: ts,
						}
						activeSubagents = append(activeSubagents, sub)
						batch = append(batch, sub)
					}
					latestPendingBatch = batch
				}
			}
		}

		// Check if this step declares the created subagent IDs
		if len(latestPendingBatch) > 0 && strings.Contains(entry.Content, "Created the following subagents:") {
			lines := strings.Split(entry.Content, "\n")
			batchIdx := 0
			for _, l := range lines {
				if strings.Contains(l, `"conversationId":`) {
					parts := strings.Split(l, `"conversationId":`)
					if len(parts) >= 2 {
						idVal := strings.Trim(parts[1], " \t\r\n,\"} ")
						idVal = strings.Trim(idVal, `"`)
						if idVal != "" && batchIdx < len(latestPendingBatch) {
							latestPendingBatch[batchIdx].ID = idVal
							convMap[idVal] = latestPendingBatch[batchIdx]
							batchIdx++
						}
					}
				}
			}
			latestPendingBatch = nil
		}

		// Check if a subagent sent a response back or completed
		if len(activeSubagents) > 0 {
			for cId, sub := range convMap {
				if strings.Contains(entry.Content, fmt.Sprintf("sender=%s", cId)) ||
					strings.Contains(entry.Content, fmt.Sprintf(`sender="%s"`, cId)) ||
					(strings.Contains(line, fmt.Sprintf(`"conversationId":"%s"`, cId)) && strings.Contains(line, `"state":"idle"`)) {
					sub.State = "completed"
				}
			}
		}
	}

	var running []SubagentInfo
	for _, s := range activeSubagents {
		if s != nil && s.State == "running" {
			running = append(running, *s)
		}
	}

	return running, nil
}

func loadClaudeSubagents(home, sessionID, cwd string) ([]SubagentInfo, error) {
	var targetFile string
	projectsDir := filepath.Join(home, ".claude", "projects")

	if cwd != "" {
		encodedCwd := encodeClaudeProjectDir(cwd)
		cand := filepath.Join(projectsDir, encodedCwd, sessionID+".jsonl")
		if fileExists(cand) {
			targetFile = cand
		}
	}

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
		return nil, fmt.Errorf("claude log not found for session %s", sessionID)
	}

	file, err := os.Open(targetFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var activeSubagents []*SubagentInfo
	toolUseMap := make(map[string]*SubagentInfo)

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
			isToolResult := false
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentArr, ok := msgObj["content"].([]interface{}); ok {
					for _, item := range contentArr {
						if block, ok := item.(map[string]interface{}); ok {
							if bType, _ := block["type"].(string); bType == "tool_result" {
								isToolResult = true
								toolID, _ := block["tool_use_id"].(string)
								if sub, found := toolUseMap[toolID]; found {
									sub.State = "completed"
								}
							}
						}
					}
				}
			}
			if !isToolResult {
				activeSubagents = nil
				toolUseMap = make(map[string]*SubagentInfo)
			}
		} else if msgType == "assistant" {
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentArr, ok := msgObj["content"].([]interface{}); ok {
					for _, item := range contentArr {
						if block, ok := item.(map[string]interface{}); ok {
							bType, _ := block["type"].(string)
							toolName, _ := block["name"].(string)
							if bType == "tool_use" && (toolName == "Agent" || toolName == "Task") {
								toolID, _ := block["id"].(string)
								var prompt, desc, subType string
								if inputMap, ok := block["input"].(map[string]interface{}); ok {
									if pr, ok := inputMap["prompt"].(string); ok {
										prompt = pr
									}
									if d, ok := inputMap["description"].(string); ok {
										desc = d
									}
									if st, ok := inputMap["subagent_type"].(string); ok {
										subType = st
									}
								}
								name := subType
								if name == "" {
									name = desc
								}
								if name == "" {
									name = toolName
								}
								sub := &SubagentInfo{
									ID:        toolID,
									Name:      name,
									Role:      subType,
									Prompt:    prompt,
									State:     "running",
									StartedAt: ts,
								}
								activeSubagents = append(activeSubagents, sub)
								if toolID != "" {
									toolUseMap[toolID] = sub
								}
							}
						}
					}
				}
			}
		}
	}

	var running []SubagentInfo
	for _, s := range activeSubagents {
		if s != nil && s.State == "running" {
			running = append(running, *s)
		}
	}
	return running, nil
}
