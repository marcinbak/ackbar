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

// ReadSessionMetadata reads session metadata (title, first prompt, last prompt, version, context pct) from Codex files
func (c *CodexProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	title := c.ResolveSessionTitle(cwd, nativeID)
	logFile := findCodexSessionLog(home, nativeID)
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
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		if raw.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
				if t.After(meta.LastMessageAt) {
					meta.LastMessageAt = t
				}
			} else if t, err := time.Parse(time.RFC3339, raw.Timestamp); err == nil {
				if t.After(meta.LastMessageAt) {
					meta.LastMessageAt = t
				}
			}
		}

		if raw.Type == "session_meta" && meta.Version == "" {
			var sm struct {
				CliVersion string `json:"cli_version"`
			}
			if err := json.Unmarshal(raw.Payload, &sm); err == nil && sm.CliVersion != "" {
				meta.Version = sm.CliVersion
			}
		} else if raw.Type == "response_item" {
			var ri struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(raw.Payload, &ri); err == nil && ri.Type == "message" && ri.Role == "user" {
				var texts []string
				for _, part := range ri.Content {
					cleaned := cleanCodexUserPrompt(part.Text)
					if cleaned != "" {
						texts = append(texts, cleaned)
					}
				}
				if len(texts) > 0 {
					joined := strings.Join(texts, " ")
					if firstPrompt == "" {
						firstPrompt = joined
					}
					lastPrompt = joined
				}
			}
		} else if raw.Type == "event_msg" {
			var em struct {
				Type string `json:"type"`
				Info struct {
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
			if err := json.Unmarshal(raw.Payload, &em); err == nil && em.Type == "token_count" {
				if em.RateLimits.Primary.UsedPercent > 0 {
					meta.ContextPct = int(em.RateLimits.Primary.UsedPercent)
				} else if em.Info.ModelContextWindow > 0 && em.Info.TotalTokenUsage.TotalTokens > 0 {
					meta.ContextPct = (em.Info.TotalTokenUsage.TotalTokens * 100) / em.Info.ModelContextWindow
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

// ResolveSessionTitle resolves session thread name from ~/.codex/session_index.jsonl
func (c *CodexProvider) ResolveSessionTitle(cwd, nativeID string) string {
	if nativeID == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	indexPath := filepath.Join(home, ".codex", "session_index.jsonl")
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
			ThreadName string `json:"thread_name"`
		}
		if err := json.Unmarshal([]byte(line), &item); err == nil {
			if item.ID == nativeID && item.ThreadName != "" {
				latestTitle = item.ThreadName
			}
		}
	}
	return latestTitle
}

// ExtractTranscript extracts user and assistant messages along with tool calls from rollout logs
func (c *CodexProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	if nativeID == "" {
		return nil, fmt.Errorf("codex nativeID cannot be empty")
	}

	logFile := findCodexSessionLog(home, nativeID)
	if logFile == "" {
		return nil, fmt.Errorf("codex session log not found for session %s", nativeID)
	}

	file, err := os.Open(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open codex log file: %w", err)
	}
	defer file.Close()

	var messages []daemon.TranscriptMessage
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

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
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339, raw.Timestamp)
		if ts.IsZero() {
			ts = time.Now()
		}

		if raw.Type == "response_item" {
			var ri struct {
				Type    string `json:"type"` // "message", "reasoning", "custom_tool_call"
				Role    string `json:"role"` // "user", "assistant", "developer"
				Name    string `json:"name"` // tool name
				Input   string `json:"input"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(raw.Payload, &ri); err != nil {
				continue
			}

			if ri.Type == "message" {
				if ri.Role == "user" {
					var textParts []string
					for _, part := range ri.Content {
						cleaned := cleanCodexUserPrompt(part.Text)
						if cleaned != "" {
							textParts = append(textParts, cleaned)
						}
					}
					if len(textParts) > 0 {
						messages = append(messages, daemon.TranscriptMessage{
							Role:      "user",
							Content:   strings.Join(textParts, "\n\n"),
							Timestamp: ts,
						})
					}
				} else if ri.Role == "assistant" {
					var textParts []string
					for _, part := range ri.Content {
						if part.Text != "" {
							textParts = append(textParts, part.Text)
						}
					}
					fullText := strings.Join(textParts, "\n\n")
					if fullText != "" {
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
							messages = append(messages, daemon.TranscriptMessage{
								Role:      "assistant",
								Content:   fullText,
								Timestamp: ts,
							})
						}
					}
				}
			} else if ri.Type == "custom_tool_call" && ri.Name != "" {
				toolSummary := ri.Name
				if ri.Input != "" {
					toolSummary += ": " + daemon.TruncateTitle(ri.Input)
				}
				if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
					messages[len(messages)-1].ToolCalls = append(messages[len(messages)-1].ToolCalls, toolSummary)
				} else {
					messages = append(messages, daemon.TranscriptMessage{
						Role:      "assistant",
						Content:   "",
						ToolCalls: []string{toolSummary},
						Timestamp: ts,
					})
				}
			}
		}
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("no conversation messages found in codex log for %s", nativeID)
	}

	return messages, nil
}

func isSafeSessionID(id string) bool {
	if id == "" {
		return false
	}
	if isValidUUID(id) {
		return true
	}
	if strings.ContainsAny(id, "/\\*?[]~`$\";|&<>'") || strings.Contains(id, "..") || filepath.Base(id) != id {
		return false
	}
	return true
}

func findCodexSessionLog(home, nativeID string) string {
	if !isSafeSessionID(nativeID) {
		return ""
	}

	// 1. Direct glob in archived sessions
	archivedGlob := filepath.Join(home, ".codex", "archived_sessions", "*"+nativeID+"*.jsonl")
	if matches, err := filepath.Glob(archivedGlob); err == nil && len(matches) > 0 {
		return matches[0]
	}

	// 2. Glob in sessions/YYYY/MM/DD/
	sessionsGlob := filepath.Join(home, ".codex", "sessions", "*", "*", "*", "*"+nativeID+"*.jsonl")
	if matches, err := filepath.Glob(sessionsGlob); err == nil && len(matches) > 0 {
		return matches[0]
	}

	// 3. Glob in top-level sessions/
	topGlob := filepath.Join(home, ".codex", "sessions", "*"+nativeID+"*.jsonl")
	if matches, err := filepath.Glob(topGlob); err == nil && len(matches) > 0 {
		return matches[0]
	}

	// 4. Filepath walk fallback
	sessionsDir := filepath.Join(home, ".codex", "sessions")
	var found string
	_ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipAll
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") && strings.Contains(d.Name(), nativeID) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})

	return found
}

func cleanCodexUserPrompt(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	xmlTags := []string{
		"environment_context",
		"recommended_plugins",
		"permissions instructions",
		"permissions_instructions",
		"collaboration_mode",
		"apps_instructions",
		"plugins_instructions",
		"skills_instructions",
		"multi_agent_mode",
	}

	clean := trimmed
	for _, tag := range xmlTags {
		startTag := "<" + tag + ">"
		endTag := "</" + tag + ">"
		for {
			sIdx := strings.Index(clean, startTag)
			if sIdx == -1 {
				break
			}
			relEnd := strings.Index(clean[sIdx+len(startTag):], endTag)
			if relEnd == -1 {
				clean = clean[:sIdx]
				break
			}
			eIdx := sIdx + len(startTag) + relEnd
			clean = clean[:sIdx] + clean[eIdx+len(endTag):]
		}
	}

	return strings.TrimSpace(clean)
}
