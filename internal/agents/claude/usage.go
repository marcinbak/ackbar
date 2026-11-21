package claude

import (
	"bufio"
	"encoding/json"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func ModelContextLimit(modelName string) int {
	modelLower := strings.ToLower(modelName)
	if strings.Contains(modelLower, "fable") || strings.Contains(modelLower, "opus") || strings.Contains(modelLower, "1m") ||
		strings.Contains(modelLower, "gemini") || strings.Contains(modelLower, "sonnet-4") || strings.Contains(modelLower, "sonnet-5") ||
		strings.Contains(modelLower, "claude-4") || strings.Contains(modelLower, "claude-5") {
		return 1000000
	}
	if strings.Contains(modelLower, "gpt-4") || strings.Contains(modelLower, "codex") {
		return 128000
	}
	return 200000
}

func readContextUsage(home, cwd, sessionID string) int {
	targetID := sessionID
	if strings.HasPrefix(sessionID, "proc-") || !agentutil.IsUUID(targetID) {
		return 0
	}
	targetFile := FindTranscriptPath(home, cwd, targetID)
	if targetFile == "" {
		return 0
	}
	data, _, _, err := readRegularFileSlice(targetFile, -1, 65536)
	if err != nil || len(data) == 0 {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.Contains(line, `"usage"`) {
			continue
		}
		var wrapper struct {
			Message *struct {
				Model string `json:"model"`
				Usage *struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &wrapper) == nil && wrapper.Message != nil && wrapper.Message.Usage != nil {
			u := wrapper.Message.Usage
			total := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
			if total > 0 {
				limit := ModelContextLimit(wrapper.Message.Model)
				pct := (total * 100) / limit
				if pct > 100 {
					pct = 100
				}
				return pct
			}
		}
	}
	return 0
}

func (p *Plugin) ComputeUsage(sessionID, cwd, model string) (agent.TranscriptUsage, bool) {
	limit := ModelContextLimit(model)
	return p.ComputeTranscriptUsage("", cwd, sessionID, limit)
}

func (p *Plugin) ComputeTranscriptUsage(homeDir, cwd, nativeID string, defaultLimit int) (agent.TranscriptUsage, bool) {
	u := agent.TranscriptUsage{ContextLimitTokens: defaultLimit}
	targetFile := FindTranscriptPath(homeDir, cwd, nativeID)
	if targetFile == "" {
		return u, false
	}
	f, err := openRegularFileNonBlocking(targetFile)
	if err != nil {
		return u, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 256*1024), 10*1024*1024)
	for sc.Scan() {
		var raw map[string]interface{}
		if json.Unmarshal([]byte(sc.Text()), &raw) != nil {
			continue
		}
		if raw["type"] == "user" {
			fullText := extractUserTextOnly(raw)
			if clean, keep := p.FilterUserEntry(raw, fullText); keep && strings.TrimSpace(clean) != "" {
				u.TurnCount++
			}
			continue
		}
		if raw["type"] == "assistant" {
			if msg, ok := raw["message"].(map[string]interface{}); ok {
				if usage, ok := msg["usage"].(map[string]interface{}); ok {
					inT, _ := usage["input_tokens"].(float64)
					outT, _ := usage["output_tokens"].(float64)
					cIn, _ := usage["cache_creation_input_tokens"].(float64)
					cRd, _ := usage["cache_read_input_tokens"].(float64)
					u.InputTokens += int(inT)
					u.OutputTokens += int(outT)
					u.CacheWriteTokens += int(cIn)
					u.CacheReadTokens += int(cRd)
					u.LastTurnContextTokens = int(inT) + int(cIn) + int(cRd)
				}
				if outDet, ok := msg["output_tokens_details"].(map[string]interface{}); ok {
					if thT, ok := outDet["thinking_tokens"].(float64); ok {
						u.ThinkingTokens += int(thT)
					}
				}
			}
		}
	}
	u.CostUSD = (float64(u.InputTokens)*3.0 + float64(u.OutputTokens)*15.0 +
		float64(u.CacheReadTokens)*0.30 + float64(u.CacheWriteTokens)*3.75) / 1000000.0
	return u, true
}

func extractUserTextOnly(raw map[string]interface{}) string {
	msgObj, ok := raw["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	if s, ok := msgObj["content"].(string); ok {
		return s
	}
	arr, ok := msgObj["content"].([]interface{})
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, cItem := range arr {
		if cMap, ok := cItem.(map[string]interface{}); ok {
			if cType, _ := cMap["type"].(string); cType == "text" {
				if txt, _ := cMap["text"].(string); txt != "" {
					out.WriteString(txt)
				}
			}
		}
	}
	return out.String()
}
