package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		if json.Unmarshal([]byte(line), &item) == nil && item.ID == nativeID && item.ThreadName != "" {
			latestTitle = item.ThreadName
		}
	}
	return latestTitle
}

func (p *Plugin) ReadSessionMetadata(homeDir, cwd, nativeID string) *agent.SessionMeta {
	if homeDir == "" {
		homeDir = agentutil.RealUserHome()
	}
	if homeDir == "" {
		return nil
	}

	title := p.ResolveSessionTitle(homeDir, cwd, nativeID)
	logFile := findCodexSessionLog(homeDir, nativeID)
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
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}

		var msgTime time.Time
		if raw.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
				msgTime = t
			} else if t, err := time.Parse(time.RFC3339, raw.Timestamp); err == nil {
				msgTime = t
			}
			if !msgTime.IsZero() && msgTime.After(meta.LastMessageAt) {
				meta.LastMessageAt = msgTime
			}
		}

		switch raw.Type {
		case "session_meta":
			if meta.Version == "" {
				var sm struct {
					CliVersion string `json:"cli_version"`
				}
				if json.Unmarshal(raw.Payload, &sm) == nil && sm.CliVersion != "" {
					meta.Version = sm.CliVersion
				}
			}
		case "response_item":
			var ri struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(raw.Payload, &ri) == nil && ri.Type == "message" && ri.Role == "user" {
				var texts []string
				for _, part := range ri.Content {
					if cleaned, keep := p.FilterUserEntry(nil, part.Text); keep {
						texts = append(texts, cleaned)
					}
				}
				if len(texts) > 0 {
					joined := strings.Join(texts, " ")
					if firstPrompt == "" {
						firstPrompt = joined
					}
					lastPrompt = joined
					if !msgTime.IsZero() && msgTime.After(meta.LastUserMessageAt) {
						meta.LastUserMessageAt = msgTime
					}
				}
			}
		case "event_msg":
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
			if json.Unmarshal(raw.Payload, &em) == nil && em.Type == "token_count" {
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
		meta.Title = agentutil.TruncateTitle(firstPrompt, 50)
	}
	return meta
}
