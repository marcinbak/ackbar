package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	ToolName  string    `json:"-"`
	Synthetic bool      `json:"-"`
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
	msgs, err := ExtractAntigravityTranscriptMessages(home, convID)
	if err != nil {
		return err
	}
	t.Messages = append(t.Messages, msgs...)
	return nil
}

type antigravityTranscriptEntry struct {
	StepIndex       int      `json:"step_index"`
	Source          string   `json:"source"`
	Type            string   `json:"type"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	Content         string   `json:"content"`
	Thinking        string   `json:"thinking"`
	TruncatedFields []string `json:"truncated_fields"`
	Media           []struct {
		MimeType string `json:"mime_type"`
		URI      string `json:"uri"`
	} `json:"media"`
	ToolCalls []struct {
		Name string                 `json:"name"`
		Args map[string]interface{} `json:"args"`
	} `json:"tool_calls"`
}

func hasRelevantTruncatedField(fields []string) bool {
	for _, f := range fields {
		switch f {
		case "content", "thinking", "tool_calls", "media":
			return true
		}
	}
	return false
}

var (
	claudeSystemReminderRegex   = regexp.MustCompile(`(?s)<system-reminder>[\s\S]*?</system-reminder>`)
	claudeLocalCmdCaveatRegex   = regexp.MustCompile(`(?s)<local-command-caveat>[\s\S]*?</local-command-caveat>`)
	claudeIdeOpenedFileRegex    = regexp.MustCompile(`(?s)<ide_opened_file>[\s\S]*?</ide_opened_file>`)
	claudeIdeSelectionRegex     = regexp.MustCompile(`(?s)<ide_selection>[\s\S]*?</ide_selection>`)
	claudeCommandMsgRegex       = regexp.MustCompile(`(?s)<command-message>[\s\S]*?</command-message>`)
	claudeCommandNameRegex      = regexp.MustCompile(`(?s)<command-name>([^<]+)</command-name>`)
	claudeCommandArgsRegex      = regexp.MustCompile(`(?s)<command-args>([^<]*)</command-args>`)
	claudeBashInputRegex        = regexp.MustCompile(`(?s)<bash-input>([\s\S]*?)</bash-input>`)
	claudeBashStdoutRegex       = regexp.MustCompile(`(?s)<bash-stdout>([\s\S]*?)</bash-stdout>`)
	claudeBashStderrRegex       = regexp.MustCompile(`(?s)<bash-stderr>([\s\S]*?)</bash-stderr>`)
	claudeLocalCmdStdoutRegex   = regexp.MustCompile(`(?s)<local-command-stdout>([\s\S]*?)</local-command-stdout>`)
	claudeTaskNotificationRegex = regexp.MustCompile(`(?s)<task-notification>([\s\S]*?)</task-notification>`)
	claudeTaskSummaryRegex      = regexp.MustCompile(`(?s)<summary>([\s\S]*?)</summary>`)
	claudeTaskStatusRegex       = regexp.MustCompile(`(?s)<status>([\s\S]*?)</status>`)
	consecutiveNewlinesRegex    = regexp.MustCompile(`\n{3,}`)
)

// CleanClaudePrompt normalizes raw Claude Code transcripts and prompts by stripping
// internal XML tags (<system-reminder>, <local-command-caveat>, <ide_opened_file>),
// formatting slash commands (/cmd args), bash executions, and task notifications.
func CleanClaudePrompt(raw string) string {
	if raw == "" {
		return ""
	}

	result := raw

	// 1. Strip internal system noise tags
	result = claudeSystemReminderRegex.ReplaceAllString(result, "")
	result = claudeLocalCmdCaveatRegex.ReplaceAllString(result, "")
	result = claudeIdeOpenedFileRegex.ReplaceAllString(result, "")
	result = claudeIdeSelectionRegex.ReplaceAllString(result, "")
	result = claudeCommandMsgRegex.ReplaceAllString(result, "")

	// 2. Handle slash command formatting: <command-name>foo</command-name> <command-args>bar</command-args>
	if cmdMatch := claudeCommandNameRegex.FindStringSubmatch(result); len(cmdMatch) > 1 {
		cmdName := strings.TrimSpace(cmdMatch[1])
		cmdArgs := ""
		if argsMatch := claudeCommandArgsRegex.FindStringSubmatch(result); len(argsMatch) > 1 {
			cmdArgs = strings.TrimSpace(argsMatch[1])
		}
		result = claudeCommandNameRegex.ReplaceAllString(result, "")
		result = claudeCommandArgsRegex.ReplaceAllString(result, "")

		formattedCmd := "/" + strings.TrimPrefix(cmdName, "/")
		if cmdArgs != "" {
			formattedCmd += " " + cmdArgs
		}

		trimmedRemaining := strings.TrimSpace(result)
		if trimmedRemaining != "" {
			result = formattedCmd + "\n\n" + trimmedRemaining
		} else {
			result = formattedCmd
		}
	}

	// 3. Format <bash-input> as ```bash\n$ <input>\n```
	result = claudeBashInputRegex.ReplaceAllStringFunc(result, func(match string) string {
		sub := claudeBashInputRegex.FindStringSubmatch(match)
		if len(sub) > 1 {
			input := strings.TrimSpace(sub[1])
			if input != "" {
				return fmt.Sprintf("```bash\n$ %s\n```", input)
			}
		}
		return ""
	})

	// 4. Format stdout / stderr blocks (or strip if empty)
	formatOutputBlock := func(match string, re *regexp.Regexp) string {
		sub := re.FindStringSubmatch(match)
		if len(sub) > 1 {
			out := strings.TrimSpace(sub[1])
			if out != "" {
				return fmt.Sprintf("```\n%s\n```", out)
			}
		}
		return ""
	}
	result = claudeBashStdoutRegex.ReplaceAllStringFunc(result, func(m string) string {
		return formatOutputBlock(m, claudeBashStdoutRegex)
	})
	result = claudeBashStderrRegex.ReplaceAllStringFunc(result, func(m string) string {
		return formatOutputBlock(m, claudeBashStderrRegex)
	})
	result = claudeLocalCmdStdoutRegex.ReplaceAllStringFunc(result, func(m string) string {
		return formatOutputBlock(m, claudeLocalCmdStdoutRegex)
	})

	// 5. Format <task-notification> blocks
	result = claudeTaskNotificationRegex.ReplaceAllStringFunc(result, func(match string) string {
		sub := claudeTaskNotificationRegex.FindStringSubmatch(match)
		if len(sub) > 1 {
			inner := sub[1]
			summary := ""
			status := ""
			if sm := claudeTaskSummaryRegex.FindStringSubmatch(inner); len(sm) > 1 {
				summary = strings.TrimSpace(sm[1])
			}
			if stm := claudeTaskStatusRegex.FindStringSubmatch(inner); len(stm) > 1 {
				status = strings.TrimSpace(stm[1])
			}
			if summary != "" && status != "" {
				return fmt.Sprintf("[Task %s: %s]", status, summary)
			} else if summary != "" {
				return fmt.Sprintf("[Task: %s]", summary)
			}
		}
		return ""
	})

	result = consecutiveNewlinesRegex.ReplaceAllString(result, "\n\n")
	result = strings.ReplaceAll(result, "```\n```", "```\n\n```")
	return strings.TrimSpace(result)
}

// IsSyntheticClaudeUserEntry reports whether a raw Claude Code JSONL entry of type "user"
// was injected by the runtime (e.g. image coordinate hints, skill expansions, compaction
// summaries, background task notifications, or cross-session/teammate messages) rather
// than sent by the human user.
func IsSyntheticClaudeUserEntry(raw map[string]interface{}, content string) bool {
	isExplicitHuman := false
	if raw != nil {
		if isMeta, _ := raw["isMeta"].(bool); isMeta {
			return true
		}
		if isCompact, _ := raw["isCompactSummary"].(bool); isCompact {
			return true
		}
		if isTranscriptOnly, _ := raw["isVisibleInTranscriptOnly"].(bool); isTranscriptOnly {
			return true
		}
		if isTurnCompanion, _ := raw["turnCompanion"].(bool); isTurnCompanion {
			return true
		}
		if origin, ok := raw["origin"].(map[string]interface{}); ok {
			if kind, _ := origin["kind"].(string); kind != "" {
				if kind != "human" {
					return true
				}
				isExplicitHuman = true
			}
		}
	}

	trimmed := strings.TrimSpace(content)
	if trimmed == "" || isExplicitHuman {
		return false
	}
	if strings.Contains(trimmed, "<EXTREMELY_IMPORTANT>") ||
		strings.HasPrefix(trimmed, "<task-notification>") ||
		strings.HasPrefix(trimmed, "<teammate-message") ||
		strings.HasPrefix(trimmed, "<cross-session-message") ||
		strings.HasPrefix(trimmed, "Another Claude session sent a message:") ||
		strings.HasPrefix(trimmed, "[Image: original ") ||
		strings.HasPrefix(trimmed, "[Cross-session ") ||
		strings.HasPrefix(trimmed, "[Request interrupted by user") {
		return true
	}
	return false
}

// ExtractAntigravityTranscriptMessages reads Antigravity conversation logs from disk,
// hydrating any truncated steps from transcript_full.jsonl when available.
func ExtractAntigravityTranscriptMessages(home, convID string) ([]TranscriptMessage, error) {
	cleanID := filepath.Base(filepath.Clean(convID))
	if cleanID == "" || cleanID == "." || cleanID == ".." || strings.ContainsAny(cleanID, "*?[") {
		return nil, fmt.Errorf("invalid conversation ID %q", convID)
	}
	candidateDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "logs"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "logs"),
		filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "logs"),
	}
	if p1, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "logs")); len(p1) > 0 {
		candidateDirs = append(candidateDirs, p1...)
	}
	if p2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "logs")); len(p2) > 0 {
		candidateDirs = append(candidateDirs, p2...)
	}
	if p3, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity-cli", "brain", cleanID, ".system_generated", "logs")); len(p3) > 0 {
		candidateDirs = append(candidateDirs, p3...)
	}

	var file *os.File
	var openedDir string
	var err error
	for _, dir := range candidateDirs {
		for _, name := range []string{"transcript.jsonl", "transcript_full.jsonl"} {
			file, err = os.Open(filepath.Join(dir, name))
			if err == nil {
				openedDir = dir
				break
			}
		}
		if file != nil {
			break
		}
	}

	if file == nil {
		return nil, fmt.Errorf("antigravity log not found for conversation %s: %w", convID, err)
	}
	defer file.Close()

	var fullSteps map[int]string
	fullStepsLoaded := false
	getFullLine := func(stepIdx int) (string, bool) {
		if !fullStepsLoaded {
			fullStepsLoaded = true
			fullSteps = make(map[int]string)
			if fullFile, ferr := os.Open(filepath.Join(openedDir, "transcript_full.jsonl")); ferr == nil {
				defer fullFile.Close()
				fScanner := bufio.NewScanner(fullFile)
				fBuf := make([]byte, 1024*1024)
				fScanner.Buffer(fBuf, 20*1024*1024)
				for fScanner.Scan() {
					fLine := strings.TrimSpace(fScanner.Text())
					if fLine == "" {
						continue
					}
					var idxOnly struct {
						StepIndex int `json:"step_index"`
					}
					if json.Unmarshal([]byte(fLine), &idxOnly) == nil {
						fullSteps[idxOnly.StepIndex] = fLine
					}
				}
			}
		}
		l, ok := fullSteps[stepIdx]
		return l, ok
	}

	messages := make([]TranscriptMessage, 0)
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024) // 1MB buffer for large entries
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry antigravityTranscriptEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if hasRelevantTruncatedField(entry.TruncatedFields) {
			if fullLine, ok := getFullLine(entry.StepIndex); ok {
				var fullEntry antigravityTranscriptEntry
				if json.Unmarshal([]byte(fullLine), &fullEntry) == nil {
					entry = fullEntry
				}
			}
		}

		ts, err := time.Parse(time.RFC3339Nano, entry.CreatedAt)
		if err != nil {
			ts, _ = time.Parse(time.RFC3339, entry.CreatedAt)
		}
		if ts.IsZero() {
			ts = time.Now()
		}

		switch entry.Type {
		case "USER_INPUT":
			cleanContent := cleanAntigravityPrompt(entry.Content)
			for _, m := range entry.Media {
				if m.URI != "" {
					cleanURI := strings.TrimPrefix(m.URI, "file://")
					attachmentLine := fmt.Sprintf("[Attached file: %q]", cleanURI)
					if cleanContent != "" {
						cleanContent += "\n\n" + attachmentLine
					} else {
						cleanContent = attachmentLine
					}
				}
			}
			if cleanContent != "" {
				messages = append(messages, TranscriptMessage{
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
				} else if tc.Name != "" {
					toolSummaries = append(toolSummaries, tc.Name)
				}
			}

			content := entry.Content
			for _, m := range entry.Media {
				if m.URI != "" {
					attachmentMarkdown := fmt.Sprintf("![Attachment](%s)", m.URI)
					if content != "" {
						content += "\n\n" + attachmentMarkdown
					} else {
						content = attachmentMarkdown
					}
				}
			}

			if content != "" || entry.Thinking != "" || len(toolSummaries) > 0 {
				if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
					last := &messages[len(messages)-1]
					if content != "" {
						if last.Content != "" {
							if !strings.Contains(last.Content, content) {
								last.Content += "\n\n" + content
							}
						} else {
							last.Content = content
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
					messages = append(messages, TranscriptMessage{
						Role:      "assistant",
						Content:   content,
						Thinking:  entry.Thinking,
						ToolCalls: toolSummaries,
						Timestamp: ts,
					})
				}
			}

		case "CHECKPOINT":
			// Truncation summaries
			if strings.Contains(entry.Content, "CHECKPOINT") {
				messages = append(messages, TranscriptMessage{
					Role:      "system",
					Content:   strings.TrimSpace(entry.Content),
					Timestamp: ts,
				})
			}
		}
	}

	return messages, nil
}

func findClaudeTranscriptPath(home, sessionID, cwd string) string {
	cleanID := filepath.Base(filepath.Clean(sessionID))
	if cleanID == "" || cleanID == "." || cleanID == ".." || strings.ContainsAny(cleanID, "*?[") {
		return ""
	}
	projectsDir := filepath.Join(home, ".claude", "projects")

	if cwd != "" {
		encodedCwd := encodeClaudeProjectDir(cwd)
		cand := filepath.Join(projectsDir, encodedCwd, cleanID+".jsonl")
		if fileExists(cand) {
			return cand
		}
		slug := strings.ReplaceAll(cwd, "/", "-")
		if slug != encodedCwd {
			cand2 := filepath.Join(projectsDir, slug, cleanID+".jsonl")
			if fileExists(cand2) {
				return cand2
			}
		}
	}

	if dirExists(projectsDir) {
		if entries, err := os.ReadDir(projectsDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					cand := filepath.Join(projectsDir, e.Name(), cleanID+".jsonl")
					if fileExists(cand) {
						return cand
					}
				}
			}
		}
	}

	if matches, err := filepath.Glob(filepath.Join(home, ".claude-profiles", "*", "projects", "*", cleanID+".jsonl")); err == nil && len(matches) > 0 {
		return matches[0]
	}

	return ""
}

func loadClaudeTranscript(t *Transcript, home, sessionID, cwd string) error {
	targetFile := findClaudeTranscriptPath(home, sessionID, cwd)
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

			if IsSyntheticClaudeUserEntry(raw, fullText) {
				continue
			}

			fullText = CleanClaudePrompt(fullText)
			fullText = strings.TrimSpace(fullText)
			if fullText != "" && !IsSyntheticClaudeUserEntry(raw, fullText) {
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

// CleanAntigravityPrompt strips Antigravity system/metadata XML wrappers and extracts the clean user request text.
func CleanAntigravityPrompt(raw string) string {
	s := raw

	// If contains <USER_REQUEST>...</USER_REQUEST>, extract the user prompt up to the last closing tag.
	// Returning directly when closed preserves any inner quoted metadata/XML tags inside the user's message verbatim.
	if startIdx := strings.Index(s, "<USER_REQUEST>"); startIdx != -1 {
		start := startIdx + len("<USER_REQUEST>")
		if end := strings.LastIndex(s, "</USER_REQUEST>"); end > start {
			return strings.TrimSpace(s[start:end])
		}
		s = s[start:]
	}

	// Remove system/metadata blocks when <USER_REQUEST> was not present or not closed
	for _, tag := range []string{"<CONTEXT_SUMMARY>", "<ADDITIONAL_METADATA>", "<USER_SETTINGS_CHANGE>", "<SYSTEM_MESSAGE>", "<EPHEMERAL_MESSAGE>"} {
		endTag := "</" + strings.Trim(tag, "<>") + ">"
		for {
			start := strings.Index(s, tag)
			if start == -1 {
				break
			}
			if relEnd := strings.Index(s[start+len(tag):], endTag); relEnd != -1 {
				end := start + len(tag) + relEnd + len(endTag)
				s = s[:start] + s[end:]
			} else {
				s = s[:start]
				break
			}
		}
	}

	return strings.TrimSpace(s)
}

func cleanAntigravityPrompt(raw string) string {
	return CleanAntigravityPrompt(raw)
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

func loadAntigravitySubagentsFromDir(home, convID string) []SubagentInfo {
	cleanID := filepath.Base(filepath.Clean(convID))
	if cleanID == "" || cleanID == "." || cleanID == ".." || strings.ContainsAny(cleanID, "*?[") {
		return nil
	}
	candidateDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "subagents"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "subagents"),
		filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "subagents"),
	}
	if profiles, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "subagents")); len(profiles) > 0 {
		candidateDirs = append(candidateDirs, profiles...)
	}
	if profiles2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "subagents")); len(profiles2) > 0 {
		candidateDirs = append(candidateDirs, profiles2...)
	}

	var results []SubagentInfo
	seen := make(map[string]bool)

	for _, dir := range candidateDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		brainBaseDir := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			filePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}
			var payload struct {
				ConversationID     string `json:"conversationId"`
				SubagentDescriptor struct {
					TypeName string `json:"typeName"`
					Role     string `json:"role"`
				} `json:"subagentDescriptor"`
				State string `json:"state"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				continue
			}
			subID := payload.ConversationID
			if subID == "" {
				subID = strings.TrimSuffix(entry.Name(), ".json")
			}
			if seen[subID] {
				continue
			}
			seen[subID] = true

			info, _ := entry.Info()
			startTime := time.Now()
			if info != nil {
				startTime = info.ModTime()
			}

			stateNorm := "completed"
			var prompt string
			stUpper := strings.ToUpper(payload.State)
			if strings.Contains(stUpper, "KILLED") || strings.Contains(stUpper, "CANCEL") {
				stateNorm = "killed"
				_, prompt, startTime = inspectAntigravitySubagentTranscriptDaemon(brainBaseDir, subID, startTime)
			} else if strings.Contains(stUpper, "RUNNING") || strings.Contains(stUpper, "ALIVE") {
				stateNorm, prompt, startTime = inspectAntigravitySubagentTranscriptDaemon(brainBaseDir, subID, startTime)
			} else {
				_, prompt, startTime = inspectAntigravitySubagentTranscriptDaemon(brainBaseDir, subID, startTime)
			}

			role := payload.SubagentDescriptor.Role
			if role == "" {
				role = payload.SubagentDescriptor.TypeName
			}
			if role == "" {
				role = "Subagent"
			}
			name := payload.SubagentDescriptor.TypeName
			if name == "" {
				name = role
			}

			results = append(results, SubagentInfo{
				ID:        subID,
				Name:      name,
				Role:      role,
				Prompt:    prompt,
				State:     stateNorm,
				StartedAt: startTime,
			})
		}
	}
	return results
}

func inspectAntigravitySubagentTranscriptDaemon(brainBaseDir, subID string, fallbackStart time.Time) (state string, prompt string, startTime time.Time) {
	startTime = fallbackStart
	cleanSubID := filepath.Base(filepath.Clean(subID))
	logPath := filepath.Join(brainBaseDir, cleanSubID, ".system_generated", "logs", "transcript.jsonl")

	stat, statErr := os.Stat(logPath)
	if statErr != nil {
		if time.Since(fallbackStart) < 2*time.Minute {
			return "running", "", fallbackStart
		}
		return "completed", "", fallbackStart
	}

	if headData, err := readHead(logPath, 16*1024); err == nil && len(headData) > 0 {
		for _, line := range strings.Split(string(headData), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var step struct {
				StepIndex int    `json:"step_index"`
				Type      string `json:"type"`
				CreatedAt string `json:"created_at"`
				Content   string `json:"content"`
			}
			if json.Unmarshal([]byte(line), &step) == nil {
				if step.CreatedAt != "" && startTime.Equal(fallbackStart) {
					if t, pErr := time.Parse(time.RFC3339Nano, step.CreatedAt); pErr == nil {
						startTime = t
					} else if t, pErr := time.Parse(time.RFC3339, step.CreatedAt); pErr == nil {
						startTime = t
					}
				}
				if prompt == "" && (step.StepIndex == 0 || step.Type == "SYSTEM_MESSAGE" || step.Type == "USER_INPUT") {
					text := strings.TrimSpace(step.Content)
					if !strings.EqualFold(step.Type, "USER_INPUT") && strings.Contains(text, "<SYSTEM_MESSAGE>") && strings.Contains(text, "content=") {
						idx := strings.Index(text, "content=")
						sub := text[idx+len("content="):]
						if endIdx := strings.Index(sub, "</SYSTEM_MESSAGE>"); endIdx != -1 {
							sub = sub[:endIdx]
						}
						text = strings.TrimSpace(sub)
					} else if strings.EqualFold(step.Type, "USER_INPUT") {
						text = CleanAntigravityPrompt(text)
					} else {
						text = ""
					}
					if len(text) > 240 {
						text = strings.TrimSpace(text[:240]) + "..."
					}
					prompt = text
				}
				if prompt != "" {
					break
				}
			}
		}
	}

	if time.Since(stat.ModTime()) > 15*time.Minute {
		return "completed", prompt, startTime
	}

	tailData, tailErr := readTail(logPath, 64*1024)
	if tailErr != nil || len(tailData) == 0 {
		return "running", prompt, startTime
	}

	lines := strings.Split(string(tailData), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var step struct {
			StepIndex       int                      `json:"step_index"`
			Type            string                   `json:"type"`
			Status          string                   `json:"status"`
			ToolCalls       []map[string]interface{} `json:"tool_calls"`
			TruncatedFields []string                 `json:"truncated_fields"`
		}
		if json.Unmarshal([]byte(line), &step) != nil {
			continue
		}
		stType := strings.TrimSpace(step.Type)
		if stType == "" || strings.EqualFold(stType, "CHECKPOINT") || (strings.EqualFold(stType, "SYSTEM") && step.StepIndex > 0) {
			continue
		}
		if strings.EqualFold(stType, "PLANNER_RESPONSE") {
			stStatus := strings.ToUpper(strings.TrimSpace(step.Status))
			if stStatus == "DONE" || stStatus == "CANCELED" || stStatus == "CANCELLED" || stStatus == "ERROR" {
				hasToolCalls := len(step.ToolCalls) > 0 || hasTruncatedField(step.TruncatedFields, "tool_calls")
				if !hasToolCalls {
					return "completed", prompt, startTime
				}
			}
			return "running", prompt, startTime
		}
		return "running", prompt, startTime
	}

	return "running", prompt, startTime
}

func loadAntigravitySubagents(home, convID string) ([]SubagentInfo, error) {
	cleanID := filepath.Base(filepath.Clean(convID))
	if cleanID == "" || cleanID == "." || cleanID == ".." || strings.ContainsAny(cleanID, "*?[") {
		return nil, fmt.Errorf("invalid conversation ID %q", convID)
	}

	// 1. Structured files in .system_generated/subagents/
	if diskSubs := loadAntigravitySubagentsFromDir(home, cleanID); len(diskSubs) > 0 {
		return diskSubs, nil
	}

	candidatePaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
	}
	if profiles, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl")); len(profiles) > 0 {
		candidatePaths = append(candidatePaths, profiles...)
	}
	if profiles2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl")); len(profiles2) > 0 {
		candidatePaths = append(candidatePaths, profiles2...)
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

	var fileModTime time.Time
	if fStat, serr := file.Stat(); serr == nil {
		fileModTime = fStat.ModTime()
	}

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

		// Check for invoke_subagent tool call or turn completion
		if entry.Type == "PLANNER_RESPONSE" {
			hasInvoke := false
			for _, tc := range entry.ToolCalls {
				if tc.Name == "invoke_subagent" && tc.Args != nil {
					hasInvoke = true
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

			if !hasInvoke && len(entry.ToolCalls) == 0 && (entry.Status == "DONE" || entry.Status == "CANCELED" || entry.Status == "CANCELLED" || entry.Status == "ERROR") {
				for _, sub := range activeSubagents {
					if sub != nil {
						sub.State = "completed"
					}
				}
				latestPendingBatch = nil
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

	if !fileModTime.IsZero() && time.Since(fileModTime) > 2*time.Minute {
		for _, s := range activeSubagents {
			if s != nil {
				s.State = "completed"
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

func loadClaudeSubagentsFromDir(home, sessionID, cwd string) []SubagentInfo {
	cleanID := filepath.Base(filepath.Clean(sessionID))
	if cleanID == "" || cleanID == "." || cleanID == ".." || strings.ContainsAny(cleanID, "*?[") {
		return nil
	}
	var candidateDirs []string
	if cwd != "" {
		slug := strings.ReplaceAll(cwd, "/", "-")
		candidateDirs = append(candidateDirs, filepath.Join(home, ".claude", "projects", slug, cleanID, "subagents"))
	}
	if matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", cleanID, "subagents")); err == nil {
		for _, m := range matches {
			already := false
			for _, cd := range candidateDirs {
				if cd == m {
					already = true
					break
				}
			}
			if !already {
				candidateDirs = append(candidateDirs, m)
			}
		}
	}

	var results []SubagentInfo
	seen := make(map[string]bool)

	for _, subDir := range candidateDirs {
		entries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
				continue
			}
			baseName := strings.TrimSuffix(entry.Name(), ".meta.json")
			if seen[baseName] {
				continue
			}
			seen[baseName] = true

			metaPath := filepath.Join(subDir, entry.Name())
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta struct {
				Name            string `json:"name"`
				AgentType       string `json:"agentType"`
				CustomAgentType string `json:"customAgentType"`
				Description     string `json:"description"`
				Model           string `json:"model"`
				TaskKind        string `json:"taskKind"`
				Color           string `json:"color"`
				JoinedAt        int64  `json:"joinedAt"`
			}
			if err := json.Unmarshal(data, &meta); err != nil {
				continue
			}
			name := meta.Name
			if name == "" {
				name = meta.AgentType
			}
			if name == "" {
				name = baseName
			}
			role := meta.CustomAgentType
			if role == "" {
				role = meta.AgentType
			}
			if role == "" {
				role = name
			}

			startTime := time.Now()
			modTime := startTime
			if info, err := entry.Info(); err == nil {
				modTime = info.ModTime()
				startTime = modTime
			}
			if meta.JoinedAt > 0 {
				startTime = time.UnixMilli(meta.JoinedAt)
			}

			jsonlPath := filepath.Join(subDir, baseName+".jsonl")
			jInfo, statErr := os.Stat(jsonlPath)
			if statErr == nil {
				modTime = jInfo.ModTime()
			}

			state := "completed"
			if time.Since(modTime) < 30*time.Minute {
				state = "running"
			}
			if jsonlData, err := readTail(jsonlPath, 65536); err == nil && len(jsonlData) > 0 {
				lines := strings.Split(strings.TrimSpace(string(jsonlData)), "\n")
				minIdx := 0
				if statErr == nil && jInfo.Size() > 65536 && len(lines) > 1 {
					minIdx = 1
				}
				for i := len(lines) - 1; i >= minIdx; i-- {
					line := strings.TrimSpace(lines[i])
					if line == "" {
						continue
					}
					var step struct {
						Type       string `json:"type"`
						StopReason string `json:"stop_reason"`
						Message    struct {
							StopReason string `json:"stop_reason"`
						} `json:"message"`
					}
					if jerr := json.Unmarshal([]byte(line), &step); jerr == nil {
						sr := step.StopReason
						if sr == "" {
							sr = step.Message.StopReason
						}
						if sr != "" {
							if sr == "end_turn" || sr == "stop_sequence" || sr == "stop" || time.Since(modTime) >= 30*time.Minute {
								state = "completed"
							} else {
								state = "running"
							}
							break
						}
					}
				}
			}

			results = append(results, SubagentInfo{
				ID:        baseName,
				Name:      name,
				Role:      role,
				Prompt:    meta.Description,
				State:     state,
				StartedAt: startTime,
			})
		}
	}
	return results
}

func loadClaudeSubagents(home, sessionID, cwd string) ([]SubagentInfo, error) {
	// 1. Structured files in ~/.claude/projects/*/<sessionID>/subagents/
	if diskSubs := loadClaudeSubagentsFromDir(home, sessionID, cwd); len(diskSubs) > 0 {
		return diskSubs, nil
	}

	targetFile := findClaudeTranscriptPath(home, sessionID, cwd)
	if targetFile == "" {
		return nil, fmt.Errorf("claude log not found for session %s", sessionID)
	}

	file, err := os.Open(targetFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var fileModTime time.Time
	if fStat, serr := file.Stat(); serr == nil {
		fileModTime = fStat.ModTime()
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var activeSubagents []*SubagentInfo
	toolUseMap := make(map[string]*SubagentInfo)
	lastAssistantStopReason := ""

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
			lastAssistantStopReason = ""
			isToolResult := false
			var textParts []string
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentStr, ok := msgObj["content"].(string); ok {
					textParts = append(textParts, contentStr)
				} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
					for _, item := range contentArr {
						if block, ok := item.(map[string]interface{}); ok {
							bType, _ := block["type"].(string)
							if bType == "tool_result" {
								isToolResult = true
								toolID, _ := block["tool_use_id"].(string)
								if sub, found := toolUseMap[toolID]; found {
									resStr := fmt.Sprintf("%v", block["content"])
									if !strings.Contains(resStr, "teammate_spawned") && !strings.Contains(resStr, "Spawned successfully") {
										sub.State = "completed"
									}
								}
							} else if bType == "text" {
								if txt, ok := block["text"].(string); ok && txt != "" {
									textParts = append(textParts, txt)
								}
							}
						}
					}
				}
			}
			if !isToolResult && !IsSyntheticClaudeUserEntry(raw, strings.Join(textParts, "\n")) {
				activeSubagents = nil
				toolUseMap = make(map[string]*SubagentInfo)
			}
		} else if msgType == "assistant" {
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if sr, ok := msgObj["stop_reason"].(string); ok && sr != "" {
					lastAssistantStopReason = sr
				}
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

	if lastAssistantStopReason == "end_turn" || lastAssistantStopReason == "stop_sequence" || (!fileModTime.IsZero() && time.Since(fileModTime) > 2*time.Minute) {
		for _, s := range activeSubagents {
			if s != nil {
				s.State = "completed"
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
