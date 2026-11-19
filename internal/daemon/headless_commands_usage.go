package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) computeTranscriptContextAndCost(home string, sess *Session) map[string]interface{} {
	resolvedModel := s.resolveSessionModel(sess)
	effectiveModel := sess.Model
	if effectiveModel == "" {
		effectiveModel = resolvedModel
	}
	limitTokens := getModelContextLimit(effectiveModel)

	sumInputTokens := 0
	outputTokens := 0
	cacheRead := 0
	cacheWrite := 0
	thinkingTokens := 0
	turnCount := 0
	lastTurnContextTokens := 0
	customCostUSD := 0.0
	hasCustomCost := false

	if tui, ok := s.PluginRegistry().AsTranscriptUsageInspector(sess.Agent); ok {
		if u, uOk := tui.ComputeTranscriptUsage(home, sess.Cwd, sess.NativeID, limitTokens); uOk {
			sumInputTokens = u.InputTokens
			outputTokens = u.OutputTokens
			cacheRead = u.CacheReadTokens
			lastTurnContextTokens = u.LastTurnContextTokens
			limitTokens = u.ContextLimitTokens
			turnCount = u.TurnCount
			customCostUSD = u.CostUSD
			hasCustomCost = true
		}
	} else if sess.Agent == "antigravity" {
		if limitTokens < 1000000 {
			limitTokens = 1000000
		}
		logFile := filepath.Join(home, ".gemini", "antigravity-cli", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl")
		if !fileExists(logFile) {
			logFile = filepath.Join(home, ".gemini", "antigravity", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl")
		}
		if fileExists(logFile) {
			if f, err := os.Open(logFile); err == nil {
				defer f.Close()
				sc := bufio.NewScanner(f)
				for sc.Scan() {
					var raw map[string]interface{}
					if err := json.Unmarshal([]byte(sc.Text()), &raw); err == nil {
						msgType, _ := raw["type"].(string)
						if inT, ok := raw["input_tokens"].(float64); ok && inT > 0 {
							if msgType == "PLANNER_RESPONSE" {
								lastTurnContextTokens = int(inT)
							}
							crT, _ := raw["cache_read_tokens"].(float64)
							uncached := int(inT) - int(crT)
							if uncached > 0 {
								sumInputTokens += uncached
							}
							cacheRead += int(crT)
						}
						if outT, ok := raw["output_tokens"].(float64); ok && outT > 0 {
							outputTokens += int(outT)
						}
						if raw["source"] == "USER" || raw["type"] == "USER_INPUT" {
							turnCount++
						}
					}
				}
			}
		}
	} else {
		targetFile := findClaudeTranscriptPath(home, sess.NativeID, sess.Cwd)
		if targetFile != "" && fileExists(targetFile) {
			if f, err := os.Open(targetFile); err == nil {
				defer f.Close()
				sc := bufio.NewScanner(f)
				adapter := ClaudeAgentAdapter{}
				for sc.Scan() {
					var raw map[string]interface{}
					if err := json.Unmarshal([]byte(sc.Text()), &raw); err == nil {
						if raw["type"] == "user" {
							var fullText string
							if msgObj, ok := raw["message"].(map[string]interface{}); ok {
								if contentStr, ok := msgObj["content"].(string); ok {
									fullText = contentStr
								} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
									for _, cItem := range contentArr {
										if cMap, ok := cItem.(map[string]interface{}); ok {
											if cType, ok := cMap["type"].(string); ok && cType == "text" {
												if txt, ok := cMap["text"].(string); ok {
													fullText += txt
												}
											}
										}
									}
								}
							}
							cleanPrompt, keep := adapter.FilterUserEntry(raw, fullText)
							if keep && strings.TrimSpace(cleanPrompt) != "" {
								turnCount++
							}
						}
						if raw["type"] == "assistant" {
							if msg, ok := raw["message"].(map[string]interface{}); ok {
								if usage, ok := msg["usage"].(map[string]interface{}); ok {
									inT, _ := usage["input_tokens"].(float64)
									outT, _ := usage["output_tokens"].(float64)
									cIn, _ := usage["cache_creation_input_tokens"].(float64)
									cRd, _ := usage["cache_read_input_tokens"].(float64)

									sumInputTokens += int(inT)
									outputTokens += int(outT)
									cacheWrite += int(cIn)
									cacheRead += int(cRd)

									lastTurnContextTokens = int(inT) + int(cIn) + int(cRd)
								}
								if outDet, ok := msg["output_tokens_details"].(map[string]interface{}); ok {
									if thT, ok := outDet["thinking_tokens"].(float64); ok {
										thinkingTokens += int(thT)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	usedTokens := lastTurnContextTokens
	if usedTokens == 0 && sess.ContextPct > 0 {
		usedTokens = (sess.ContextPct * limitTokens) / 100
	}
	pct := 0
	if limitTokens > 0 {
		pct = (usedTokens * 100) / limitTokens
		if pct > 100 {
			pct = 100
		}
	}

	// Sonnet 3.5 API rates: $3/M in, $15/M out, $0.30/M cache read, $3.75/M cache write
	costUSD := (float64(sumInputTokens)*3.0 + float64(outputTokens)*15.0 + float64(cacheRead)*0.30 + float64(cacheWrite)*3.75) / 1000000.0
	if hasCustomCost {
		costUSD = customCostUSD
	}

	extraDirs := sess.ExtraDirs
	if extraDirs == nil {
		extraDirs = []string{}
	}

	return map[string]interface{}{
		"agent":                sess.Agent,
		"model":                sess.Model,
		"resolved_model":       resolvedModel,
		"effort":               sess.Effort,
		"permission_mode":      sess.PermissionMode,
		"turn_count":           turnCount,
		"context_used_tokens":  usedTokens,
		"context_limit_tokens": limitTokens,
		"context_pct":          pct,
		"input_tokens":         sumInputTokens,
		"output_tokens":        outputTokens,
		"cache_read_tokens":    cacheRead,
		"cache_write_tokens":   cacheWrite,
		"thinking_tokens":      thinkingTokens,
		"estimated_usd":        fmt.Sprintf("$%.4f", costUSD),
		"billing_note":         "Informational estimate based on standard API token pricing. Billed through your active account plan or API keys.",
		"cwd":                  sess.Cwd,
		"extra_dirs":           extraDirs,
	}
}
