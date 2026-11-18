package opencode

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (p *Plugin) ResolveSessionTitle(homeDir, cwd, nativeID string) string {
	_ = cwd
	if !agentutil.IsSafeSessionID(nativeID) {
		return ""
	}
	if homeDir == "" {
		homeDir = agentutil.RealUserHome()
	}

	indexPath := filepath.Join(HomeDir(homeDir), "session_index.jsonl")
	if file, err := os.Open(indexPath); err == nil {
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
			if json.Unmarshal([]byte(line), &item) == nil && item.ID == nativeID {
				if t := firstNonEmpty(item.Title, item.ThreadName, item.Name); t != "" {
					latestTitle = t
				}
			}
		}
		if latestTitle != "" {
			return latestTitle
		}
	}

	if logFile := findOpenCodeSessionLog(homeDir, nativeID); logFile != "" {
		if f, err := os.Open(logFile); err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			var customTitle string
			var firstUserPrompt string
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				var item struct {
					Title   string          `json:"title"`
					Role    string          `json:"role"`
					Type    string          `json:"type"`
					Content json.RawMessage `json:"content"`
					Message string          `json:"message"`
				}
				if json.Unmarshal([]byte(line), &item) == nil {
					if item.Title != "" {
						customTitle = item.Title
					}
					if firstUserPrompt == "" {
						if item.Role == "user" {
							var text string
							if json.Unmarshal(item.Content, &text) == nil && text != "" {
								firstUserPrompt = text
							} else if item.Message != "" {
								firstUserPrompt = item.Message
							}
						} else if item.Type == "user_message" && item.Message != "" {
							firstUserPrompt = item.Message
						}
					}
				}
			}
			if customTitle != "" {
				return customTitle
			}
			if firstUserPrompt != "" {
				if cleaned, keep := p.FilterUserEntry(nil, firstUserPrompt); keep {
					return agentutil.TruncateTitle(cleaned, 50)
				}
			}
		}
	}

	return ""
}

func (p *Plugin) ReadSessionMetadata(homeDir, cwd, nativeID string) *agent.SessionMeta {
	if homeDir == "" {
		homeDir = agentutil.RealUserHome()
	}
	if homeDir == "" {
		return nil
	}

	title := p.ResolveSessionTitle(homeDir, cwd, nativeID)
	logFile := findOpenCodeSessionLog(homeDir, nativeID)
	if logFile == "" {
		if title != "" {
			return &agent.SessionMeta{Title: title}
		}
		return nil
	}

	file, err := os.Open(logFile)
	if err != nil {
		if title != "" {
			return &agent.SessionMeta{Title: title}
		}
		return nil
	}
	defer file.Close()

	meta := &agent.SessionMeta{Title: title}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

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
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}

		tsStr := raw.Timestamp
		if tsStr == "" {
			tsStr = raw.Time
		}
		msgTime := parseOpenCodeTime(tsStr)
		if !msgTime.IsZero() && msgTime.After(meta.LastMessageAt) {
			meta.LastMessageAt = msgTime
		}

		if raw.Type == "session_meta" || raw.Type == "init" || raw.Type == "system" {
			var sm struct {
				CliVersion string `json:"cli_version"`
				Version    string `json:"version"`
			}
			data := raw.Payload
			if len(data) == 0 {
				data = []byte(line)
			}
			if json.Unmarshal(data, &sm) == nil {
				if sm.CliVersion != "" {
					meta.Version = sm.CliVersion
				} else if sm.Version != "" {
					meta.Version = sm.Version
				}
			}
		}

		role, promptText := extractUserMessageContent(raw.Type, raw.Role, raw.Content, raw.Payload, []byte(line))
		if role != "user" {
			promptText = ""
		}

		if promptText != "" {
			if cleaned, keep := p.FilterUserEntry(nil, promptText); keep {
				if firstPrompt == "" {
					firstPrompt = cleaned
				}
				lastPrompt = cleaned
				if !msgTime.IsZero() && msgTime.After(meta.LastUserMessageAt) {
					meta.LastUserMessageAt = msgTime
				}
			}
		}

		if raw.Type == "usage" || raw.Type == "token_count" || strings.Contains(line, "tokens") {
			if pct := parseOpenCodeContextPct(raw.Payload, line); pct > 0 {
				meta.ContextPct = pct
			}
		}
	}

	meta.FirstPrompt = firstPrompt
	meta.LastPrompt = lastPrompt
	if meta.Title == "" && firstPrompt != "" {
		meta.Title = agentutil.TruncateTitle(firstPrompt, 50)
	}
	return meta
}

func parseOpenCodeContextPct(payload json.RawMessage, line string) int {
	var usage struct {
		TotalTokens        int `json:"total_tokens"`
		ModelContextWindow int `json:"model_context_window"`
		ContextWindow      int `json:"context_window"`
		RateLimits         struct {
			Primary struct {
				UsedPercent float64 `json:"used_percent"`
			} `json:"primary"`
		} `json:"rate_limits"`
	}
	data := payload
	if len(data) == 0 {
		data = []byte(line)
	}
	if json.Unmarshal(data, &usage) != nil {
		return 0
	}
	if usage.RateLimits.Primary.UsedPercent > 0 {
		return int(usage.RateLimits.Primary.UsedPercent)
	}
	window := usage.ModelContextWindow
	if window == 0 {
		window = usage.ContextWindow
	}
	if window > 0 && usage.TotalTokens > 0 {
		return (usage.TotalTokens * 100) / window
	}
	return 0
}
