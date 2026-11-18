package grok

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func parseGrokTime(val interface{}) time.Time {
	switch v := val.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	case float64:
		if v > 1e11 {
			return time.UnixMilli(int64(v))
		}
		if v > 0 {
			return time.Unix(int64(v), 0)
		}
	}
	return time.Time{}
}

func parseGrokContentText(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return strings.TrimSpace(s)
	}
	if m, ok := content.(map[string]interface{}); ok {
		return strings.TrimSpace(firstMapStr(m, "text", "content"))
	}
	if parts, ok := content.([]interface{}); ok {
		var texts []string
		for _, p := range parts {
			if m, ok := p.(map[string]interface{}); ok {
				if t := firstMapStr(m, "text", "content"); t != "" {
					texts = append(texts, t)
				}
			} else if s, ok := p.(string); ok && s != "" {
				texts = append(texts, s)
			}
		}
		return strings.TrimSpace(strings.Join(texts, "\n"))
	}
	return ""
}

func parseGrokToolInput(raw map[string]interface{}) interface{} {
	for _, k := range []string{"arguments", "input", "args", "tool_input"} {
		if v, exists := raw[k]; exists && v != nil {
			if s, ok := v.(string); ok {
				var parsed map[string]interface{}
				if json.Unmarshal([]byte(s), &parsed) == nil {
					return parsed
				}
				return s
			}
			return v
		}
	}
	return nil
}

func (*Plugin) ReadSessionMetadata(cwd, nativeID string) *agent.SessionMeta {
	return ReadSessionMetadataForHome(agentutil.RealUserHome(), cwd, nativeID)
}

func (*Plugin) ResolveSessionTitle(cwd, nativeID string) string {
	if meta := ReadSessionMetadataForHome(agentutil.RealUserHome(), cwd, nativeID); meta != nil && meta.Title != "" {
		return meta.Title
	}
	return ""
}

func ReadSessionMetadataForHome(home, cwd, nativeID string) *agent.SessionMeta {
	if !agentutil.IsSafeGrokSessionID(nativeID) {
		return nil
	}
	p := &Plugin{}
	sessDir := FindSessionDirForHome(home, cwd, nativeID)
	meta := &agent.SessionMeta{}

	if sessDir != "" {
		if sumData, err := os.ReadFile(filepath.Join(sessDir, "summary.json")); err == nil {
			var sum map[string]interface{}
			if json.Unmarshal(sumData, &sum) == nil {
				cTitle, _ := sum["custom_title"].(string)
				title, _ := sum["title"].(string)
				gTitle, _ := sum["generated_title"].(string)
				sSummary, _ := sum["session_summary"].(string)
				lSummary, _ := sum["last_turn_summary"].(string)
				branch, _ := sum["head_branch"].(string)

				if cTitle != "" {
					meta.CustomTitle, meta.Title = cTitle, cTitle
				} else if title != "" {
					meta.CustomTitle, meta.Title = title, title
				} else if gTitle != "" {
					meta.AITitle, meta.Title = gTitle, gTitle
				} else if sSummary != "" {
					meta.AITitle, meta.Title = sSummary, sSummary
				}
				if lSummary != "" {
					meta.AIDescription = lSummary
				}
				if branch != "" {
					meta.GitBranch = branch
				}
				for _, k := range []string{"last_active_at", "updated_at", "created_at"} {
					if t := parseGrokTime(sum[k]); !t.IsZero() {
						meta.LastMessageAt = t
						break
					}
				}
			}
		}

		if sigData, err := os.ReadFile(filepath.Join(sessDir, "signals.json")); err == nil {
			var sig struct {
				ContextTokensUsed   int `json:"contextTokensUsed"`
				ContextWindowTokens int `json:"contextWindowTokens"`
			}
			if json.Unmarshal(sigData, &sig) == nil && sig.ContextWindowTokens > 0 && sig.ContextTokensUsed > 0 {
				meta.ContextPct = (sig.ContextTokensUsed * 100) / sig.ContextWindowTokens
			}
		}
	}

	if legPath := findLegacyLog(home, nativeID); legPath != "" {
		if data, err := os.ReadFile(legPath); err == nil {
			scanner := bufio.NewScanner(bytes.NewReader(data))
			for scanner.Scan() {
				var entry struct {
					Type       string `json:"type"`
					CliVersion string `json:"cli_version"`
					Version    string `json:"version"`
					RateLimits struct {
						Primary struct {
							UsedPercent float64 `json:"used_percent"`
						} `json:"primary"`
					} `json:"rate_limits"`
				}
				if json.Unmarshal(scanner.Bytes(), &entry) == nil {
					if (entry.Type == "session_meta" || entry.Type == "init") && meta.Version == "" {
						if entry.CliVersion != "" {
							meta.Version = entry.CliVersion
						} else if entry.Version != "" {
							meta.Version = entry.Version
						}
					}
					if (entry.Type == "usage" || entry.Type == "token_count") && entry.RateLimits.Primary.UsedPercent > 0 && meta.ContextPct == 0 {
						meta.ContextPct = int(entry.RateLimits.Primary.UsedPercent)
					}
				}
			}
		}
	}

	if msgs, err := extractTranscriptMessages(p, home, cwd, nativeID); err == nil && len(msgs) > 0 {
		for _, m := range msgs {
			if m.Role == "user" {
				if meta.FirstPrompt == "" {
					meta.FirstPrompt = m.Content
				}
				meta.LastPrompt = m.Content
				if !m.Timestamp.IsZero() {
					meta.LastUserMessageAt = m.Timestamp
					if m.Timestamp.After(meta.LastMessageAt) {
						meta.LastMessageAt = m.Timestamp
					}
				}
			} else if !m.Timestamp.IsZero() && m.Timestamp.After(meta.LastMessageAt) {
				meta.LastMessageAt = m.Timestamp
			}
		}
		if meta.Title == "" && meta.FirstPrompt != "" {
			meta.Title = agentutil.TruncateTitle(meta.FirstPrompt, 50)
		}
	}

	if meta.Title == "" {
		userHome := home
		if userHome == "" {
			userHome = agentutil.RealUserHome()
		}
		for _, baseDir := range []string{HomeDir(home), filepath.Join(userHome, ".grok")} {
			if idxData, err := os.ReadFile(filepath.Join(baseDir, "session_index.jsonl")); err == nil {
				scanner := bufio.NewScanner(strings.NewReader(string(idxData)))
				for scanner.Scan() {
					var row struct {
						ID    string `json:"id"`
						Title string `json:"title"`
					}
					if json.Unmarshal(scanner.Bytes(), &row) == nil && row.ID == nativeID && row.Title != "" {
						meta.Title = row.Title
					}
				}
			}
		}
	}

	if meta.Title == "" && meta.FirstPrompt == "" && meta.LastMessageAt.IsZero() && !p.HasConversationArtifacts(home, nativeID) {
		return nil
	}
	return meta
}
