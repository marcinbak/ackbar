package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func handleGrokStreamEvent(
	raw map[string]interface{},
	sessionID string,
	seenGrok *bool,
	lastTextBuilder *strings.Builder,
	resultText *string,
	streamHadError *bool,
	needsSeparation *bool,
	hasEmittedAssistantText *bool,
	toolUses map[string]string,
	toolInputs map[string]map[string]any,
	emittedAuthBlock *bool,
	emitStreamEvt func(ChatStreamEvent),
	hooks streamParseHooks,
) bool {
	evtType, _ := raw["type"].(string)

	isGrokEvt := false
	switch evtType {
	case "thought", "tool_call", "tool_call_update", "end":
		isGrokEvt = true
	case "text":
		_, hasData := raw["data"]
		_, hasText := raw["text"]
		isGrokEvt = hasData || hasText
	case "usage":
		_, isGrokEvt = raw["model"].(string)
	case "error":
		isGrokEvt = (seenGrok != nil && *seenGrok) || strings.HasPrefix(sessionID, "grok")
	}

	if !isGrokEvt {
		return false
	}
	if seenGrok != nil {
		*seenGrok = true
	}

	switch evtType {
	case "thought":
		data, _ := raw["data"].(string)
		if data == "" {
			data, _ = raw["text"].(string)
		}
		if data != "" {
			emitStreamEvt(ChatStreamEvent{SessionID: sessionID, Type: "thought_delta", Thinking: data})
		}

	case "text":
		data, _ := raw["data"].(string)
		if data == "" {
			data, _ = raw["text"].(string)
		}
		if data != "" {
			if *needsSeparation && *hasEmittedAssistantText {
				data = ensureDoubleNewlineSeparation(lastTextBuilder.String(), data)
				*needsSeparation = false
			}
			*hasEmittedAssistantText = true
			lastTextBuilder.WriteString(data)
			emitStreamEvt(ChatStreamEvent{SessionID: sessionID, Type: "text_delta", Text: data})
		}

	case "tool_call":
		toolCallID, _ := raw["toolCallId"].(string)
		if toolCallID == "" {
			toolCallID, _ = raw["tool_call_id"].(string)
		}
		if toolCallID == "" {
			toolCallID, _ = raw["id"].(string)
		}
		toolName, _ := raw["toolName"].(string)
		if toolName == "" {
			toolName, _ = raw["title"].(string)
		}
		if toolName == "" {
			toolName, _ = raw["name"].(string)
		}
		rawInput := raw["rawInput"]
		if rawInput == nil {
			rawInput = raw["input"]
		}
		if rawInput == nil {
			rawInput = raw["arguments"]
		}

		if toolCallID != "" {
			if toolName != "" {
				toolUses[toolCallID] = toolName
			}
			if mInp, ok := rawInput.(map[string]any); ok {
				toolInputs[toolCallID] = mInp
			} else if sInp, ok := rawInput.(string); ok {
				var parsed map[string]any
				if json.Unmarshal([]byte(sInp), &parsed) == nil {
					toolInputs[toolCallID] = parsed
				}
			}
		}

		*needsSeparation = true
		emitStreamEvt(ChatStreamEvent{SessionID: sessionID, Type: "tool_start", ToolName: toolName, ToolInput: rawInput})
		if rawInput != nil {
			if blocked := parseInteractiveToolBlock(toolName, rawInput); blocked != nil && hooks.onInteractiveBlock != nil {
				hooks.onInteractiveBlock(toolName, rawInput, blocked)
			}
		}

	case "tool_call_update":
		toolCallID, _ := raw["toolCallId"].(string)
		if toolCallID == "" {
			toolCallID, _ = raw["tool_call_id"].(string)
		}
		if toolCallID == "" {
			toolCallID, _ = raw["id"].(string)
		}
		status, _ := raw["status"].(string)
		statusLower := strings.ToLower(status)
		if statusLower == "completed" || statusLower == "failed" || statusLower == "error" {
			*needsSeparation = true
			isErr := statusLower == "failed" || statusLower == "error"
			rawOutput := raw["rawOutput"]
			if rawOutput == nil {
				rawOutput = raw["output"]
			}
			if rawOutput == nil {
				rawOutput = raw["result"]
			}
			outStr := ""
			if s, ok := rawOutput.(string); ok {
				outStr = s
			} else if rawOutput != nil {
				if b, err := json.Marshal(rawOutput); err == nil {
					outStr = string(b)
				}
			}

			tName := toolUses[toolCallID]
			tInput := toolInputs[toolCallID]
			emitStreamEvt(ChatStreamEvent{
				SessionID: sessionID, Type: "tool_result", ToolName: tName, ToolInput: tInput, ToolOutput: outStr, IsError: isErr,
			})
			if !*emittedAuthBlock {
				if authBlock := DetectToolResultAuthBlock("grok", tName, tInput, outStr, isErr); authBlock != nil && hooks.onInteractiveBlock != nil {
					*emittedAuthBlock = true
					hooks.onInteractiveBlock(tName, tInput, authBlock)
				}
			}
		}

	case "usage":
		if m, ok := raw["model"].(string); ok && m != "" && hooks.onModelDetected != nil {
			hooks.onModelDetected(m)
		}

	case "error":
		msg, _ := raw["message"].(string)
		if msg == "" {
			msg, _ = raw["error"].(string)
		}
		if msg == "" {
			msg = "Grok stream error"
		}
		*streamHadError = true
		lastTextBuilder.WriteString("\n" + msg)
		emitStreamEvt(ChatStreamEvent{SessionID: sessionID, Type: "error", Text: msg, IsError: true})

	case "end":
		*resultText = lastTextBuilder.String()
		if hooks.emitTurnComplete {
			emitStreamEvt(ChatStreamEvent{SessionID: sessionID, Type: "turn_complete", Text: *resultText})
		}
		if hooks.onTurnResult != nil {
			hooks.onTurnResult(lastTextBuilder.String(), *resultText, *streamHadError)
		}
	}

	return true
}

func getGrokDefaultModels() []ModelSuggestion {
	return []ModelSuggestion{
		{ID: "grok-4.7", Label: "Grok 4.7"},
		{ID: "grok-4.6", Label: "Grok 4.6"},
		{ID: "grok-build", Label: "Grok Build"},
	}
}

func sortGrokModels(models []ModelSuggestion) {
	priority := func(id string) int {
		lower := strings.ToLower(id)
		if strings.HasPrefix(lower, "grok-4.7") {
			return 1
		}
		if strings.HasPrefix(lower, "grok-4.6") {
			return 2
		}
		if strings.HasPrefix(lower, "grok-build") {
			return 3
		}
		return 10
	}
	sort.Slice(models, func(i, j int) bool {
		pi, pj := priority(models[i].ID), priority(models[j].ID)
		if pi != pj {
			return pi < pj
		}
		return models[i].ID < models[j].ID
	})
}

func (s *Server) collectGrokModels() []ModelSuggestion {
	home := s.effectiveHomeDir()
	cachePath := filepath.Join(GrokHomeDir(home), "models_cache.json")
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		var cache struct {
			Models map[string]struct {
				ID, Name, Description string
				Hidden                bool
			} `json:"models"`
		}
		if err := json.Unmarshal(data, &cache); err == nil && len(cache.Models) > 0 {
			var models []ModelSuggestion
			for k, v := range cache.Models {
				if v.Hidden {
					continue
				}
				id := v.ID
				if id == "" {
					id = k
				}
				label := v.Name
				if label == "" {
					label = id
				}
				models = append(models, ModelSuggestion{ID: id, Label: label})
			}
			if len(models) > 0 {
				sortGrokModels(models)
				return models
			}
		}
	}
	return getGrokDefaultModels()
}

func computeGrokTranscriptUsage(home string, sess *Session, defaultLimit int) (int, int, int, int, int, int, float64) {
	sumIn, sumOut, cacheRead, lastTurnContext, limitTokens, turnCount, costUSD := 0, 0, 0, 0, defaultLimit, 0, 0.0

	sessDir := FindGrokSessionDirForHome(home, sess.Cwd, sess.NativeID)
	if sessDir != "" {
		if sigData, err := os.ReadFile(filepath.Join(sessDir, "signals.json")); err == nil {
			var sig struct {
				ContextTokensUsed, ContextWindowTokens int
			}
			if json.Unmarshal(sigData, &sig) == nil {
				if sig.ContextWindowTokens > 0 {
					limitTokens = sig.ContextWindowTokens
				}
				if sig.ContextTokensUsed > 0 {
					lastTurnContext = sig.ContextTokensUsed
				}
			}
		}

		if usageData, err := os.ReadFile(filepath.Join(sessDir, "usage.json")); err == nil {
			var usg struct {
				CostUsdTicks                                float64 `json:"costUsdTicks"`
				InputTokens, OutputTokens, CachedReadTokens int
			}
			if json.Unmarshal(usageData, &usg) == nil {
				sumIn, sumOut, cacheRead = usg.InputTokens, usg.OutputTokens, usg.CachedReadTokens
				if usg.CostUsdTicks > 0 {
					costUSD = usg.CostUsdTicks / 1e10
				}
			}
		}
	}

	if msgs, err := ExtractGrokTranscriptMessages(home, sess.Cwd, sess.NativeID); err == nil {
		for _, m := range msgs {
			if m.Role == "user" {
				turnCount++
			}
		}
	}

	return sumIn, sumOut, cacheRead, lastTurnContext, limitTokens, turnCount, costUSD
}
