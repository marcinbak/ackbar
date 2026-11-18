package daemon

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"
)

func isExitPlanTool(toolName string) bool {
	t := strings.ToLower(strings.TrimSpace(toolName))
	return t == "exitplanmode" || t == "exit_plan_mode"
}

func isQuestionTool(toolName string) bool {
	t := strings.ToLower(strings.TrimSpace(toolName))
	return toolName == "AskUserQuestion" || toolName == "ask_question" || toolName == "AskFollowupQuestion" ||
		strings.Contains(t, "question") || strings.Contains(t, "askuser")
}

func parseStdioControlRequest(raw map[string]interface{}, fallbackText string) *PendingControlRequest {
	requestID, _ := raw["request_id"].(string)
	reqObj, ok := raw["request"].(map[string]interface{})
	if requestID == "" || !ok {
		return nil
	}
	subtype, _ := reqObj["subtype"].(string)
	if subtype != "can_use_tool" {
		return nil
	}
	toolName, _ := reqObj["tool_name"].(string)
	toolUseID, _ := reqObj["tool_use_id"].(string)
	decisionReason, _ := reqObj["decision_reason"].(string)
	rawInput, _ := reqObj["input"].(map[string]interface{})

	var blocked *Blocked
	switch {
	case isExitPlanTool(toolName):
		blocked = parseInteractiveToolBlock("ExitPlanMode", rawInput)
		if blocked != nil && strings.TrimSpace(blocked.Reason) == "" && strings.TrimSpace(fallbackText) != "" {
			blocked.Reason = strings.TrimSpace(fallbackText)
		}
	case isQuestionTool(toolName):
		blocked = parseInteractiveToolBlock(toolName, rawInput)
	default:
		reason, question := formatPermissionBlockSummary(toolName, decisionReason, rawInput)
		blocked = &Blocked{
			Kind:     BlockPermission,
			Reason:   reason,
			Question: question,
			Options:  []string{"Allow", "Deny"},
			Since:    time.Now(),
		}
	}
	if blocked == nil {
		blocked = &Blocked{Kind: BlockPermission, Reason: toolName, Question: "Allow tool?", Options: []string{"Allow", "Deny"}, Since: time.Now()}
	}
	blocked.RequestID = requestID
	blocked.ToolName = toolName
	blocked.ToolUseID = toolUseID
	blocked.DecisionReason = decisionReason

	return &PendingControlRequest{
		RequestID:      requestID,
		Subtype:        subtype,
		ToolName:       toolName,
		ToolUseID:      toolUseID,
		DecisionReason: decisionReason,
		RawInput:       rawInput,
		Blocked:        blocked,
	}
}

func formatPermissionBlockSummary(toolName, decisionReason string, rawInput map[string]interface{}) (string, string) {
	question := "Allow tool execution?"
	if toolName != "" {
		question = "Allow " + toolName + "?"
	}
	preview := ""
	if len(rawInput) > 0 {
		for _, k := range []string{"command", "file_path", "path", "pattern", "query", "url"} {
			if s, ok := rawInput[k].(string); ok && strings.TrimSpace(s) != "" {
				preview = strings.TrimSpace(s)
				break
			}
		}
		if preview == "" {
			if b, err := json.Marshal(rawInput); err == nil {
				preview = string(b)
			}
		}
		if len(preview) > 160 {
			preview = preview[:157] + "..."
		}
	}
	parts := make([]string, 0, 3)
	if toolName != "" {
		parts = append(parts, toolName)
	}
	if preview != "" {
		parts = append(parts, preview)
	}
	if strings.TrimSpace(decisionReason) != "" {
		parts = append(parts, strings.TrimSpace(decisionReason))
	}
	if len(parts) == 0 {
		return "Tool permission required", question
	}
	return strings.Join(parts, " — "), question
}

func cloneInputMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return map[string]interface{}{}
	}
	return maps.Clone(in)
}

func extractRawQuestionKeys(rawInput map[string]interface{}, blocked *Blocked) []string {
	var keys []string
	if rawInput != nil {
		for _, item := range parseInterfaceSlice(rawInput["questions"]) {
			if qMap, ok := item.(map[string]interface{}); ok {
				for _, k := range []string{"question", "prompt", "text"} {
					if q, ok := qMap[k].(string); ok && q != "" {
						keys = append(keys, q)
						break
					}
				}
			}
		}
		if len(keys) == 0 {
			for _, k := range []string{"question", "prompt"} {
				if q, ok := rawInput[k].(string); ok && q != "" {
					keys = append(keys, q)
					break
				}
			}
		}
	}
	if len(keys) == 0 && blocked != nil {
		for _, q := range blocked.Questions {
			if q.Question != "" {
				keys = append(keys, q.Question)
			}
		}
		if len(keys) == 0 && blocked.Question != "" {
			keys = append(keys, blocked.Question)
		}
	}
	return keys
}

func buildQuestionAnswersMap(qKeys []string, answer string) map[string]string {
	trimmed := strings.TrimSpace(answer)
	if strings.HasPrefix(trimmed, "{") {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil && len(parsed) > 0 {
			if len(qKeys) == 0 {
				return parsed
			}
			for _, qk := range qKeys {
				if _, ok := parsed[qk]; ok {
					return parsed
				}
			}
		}
	}
	answers := make(map[string]string, len(qKeys))
	if len(qKeys) == 1 {
		answers[qKeys[0]] = answer
		return answers
	}
	if len(qKeys) > 1 {
		parts := strings.SplitN(answer, "; ", len(qKeys))
		for i, qk := range qKeys {
			if i < len(parts) {
				answers[qk] = strings.TrimSpace(parts[i])
			} else {
				answers[qk] = ""
			}
		}
	}
	return answers
}

func buildControlResponseJSON(ctrl *PendingControlRequest, approved bool, answerOrFeedback string) ([]byte, error) {
	if ctrl == nil || ctrl.RequestID == "" {
		return nil, errors.New("missing pending control request")
	}
	var inner map[string]interface{}
	switch {
	case isExitPlanTool(ctrl.ToolName):
		if approved {
			inner = map[string]interface{}{
				"behavior":     "allow",
				"updatedInput": cloneInputMap(ctrl.RawInput),
				"updatedPermissions": []map[string]interface{}{
					{"type": "setMode", "mode": "bypassPermissions", "destination": "session"},
				},
			}
		} else {
			msg := strings.TrimSpace(answerOrFeedback)
			if msg == "" {
				msg = "Plan rejected by user. Revise the plan."
			}
			inner = map[string]interface{}{"behavior": "deny", "message": msg}
		}
	case isQuestionTool(ctrl.ToolName):
		if !approved {
			msg := strings.TrimSpace(answerOrFeedback)
			if msg == "" {
				msg = "Question dismissed by user."
			}
			inner = map[string]interface{}{"behavior": "deny", "message": msg}
		} else {
			updated := cloneInputMap(ctrl.RawInput)
			qKeys := extractRawQuestionKeys(ctrl.RawInput, ctrl.Blocked)
			updated["answers"] = buildQuestionAnswersMap(qKeys, answerOrFeedback)
			inner = map[string]interface{}{"behavior": "allow", "updatedInput": updated}
		}
	default:
		if approved {
			inner = map[string]interface{}{"behavior": "allow", "updatedInput": cloneInputMap(ctrl.RawInput)}
		} else {
			msg := strings.TrimSpace(answerOrFeedback)
			if msg == "" {
				msg = "Permission denied by user."
			}
			inner = map[string]interface{}{"behavior": "deny", "message": msg}
		}
	}
	payload := map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    "success",
			"request_id": ctrl.RequestID,
			"response":   inner,
		},
	}
	return json.Marshal(payload)
}

func resolveRespondApproval(blocked *Blocked, actionLower, value string) bool {
	switch strings.ToLower(strings.TrimSpace(actionLower)) {
	case "allow", "yes", "proceed", "1", "approve":
		return true
	case "deny", "no", "4", "reject", "dismiss", "cancel", "2", "3":
		return false
	case "answer", "input", "option", "respond":
		if blocked == nil {
			return false
		}
		valTrim := strings.TrimSpace(value)
		if blocked.Kind == BlockQuestion || isQuestionTool(blocked.ToolName) {
			return valTrim != ""
		}
		if blocked.Kind == BlockApproval || isExitPlanTool(blocked.ToolName) {
			return strings.EqualFold(valTrim, "Approve Plan") || strings.EqualFold(valTrim, "approve") || strings.EqualFold(valTrim, "yes") || strings.EqualFold(valTrim, "allow")
		}
		if blocked.Kind == BlockPermission {
			valLower := strings.ToLower(valTrim)
			return valLower == "allow" || valLower == "yes" || valLower == "approve" || strings.HasPrefix(valLower, "allow ") || strings.HasPrefix(valLower, "allow_") || strings.Contains(valLower, "always allow")
		}
		return false
	default:
		return false
	}
}
