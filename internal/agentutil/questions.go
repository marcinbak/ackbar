package agentutil

import (
	"encoding/json"
	"strings"
	"time"

	"ackbar/internal/agent"
)

func parseInterfaceSlice(raw interface{}) []interface{} {
	switch v := raw.(type) {
	case []interface{}:
		return v
	case []string:
		out := make([]interface{}, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out
	case []map[string]interface{}:
		out := make([]interface{}, len(v))
		for i, m := range v {
			out[i] = m
		}
		return out
	case string:
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			var parsed []interface{}
			if json.Unmarshal([]byte(trimmed), &parsed) == nil {
				return parsed
			}
		}
	}
	return nil
}

func extractOptionStrings(raw interface{}) []string {
	var options []string
	for _, opt := range parseInterfaceSlice(raw) {
		switch o := opt.(type) {
		case string:
			if o != "" {
				options = append(options, o)
			}
		case map[string]interface{}:
			for _, key := range []string{"label", "text", "title"} {
				if val, ok := o[key].(string); ok && val != "" {
					options = append(options, val)
					break
				}
			}
		}
	}
	return options
}

func parseBlockedQuestion(m map[string]interface{}) (agent.BlockedQuestion, bool) {
	var bq agent.BlockedQuestion
	for _, key := range []string{"question", "prompt", "text"} {
		if qStr, ok := m[key].(string); ok && qStr != "" {
			bq.Question = qStr
			break
		}
	}
	if hStr, ok := m["header"].(string); ok {
		bq.Header = hStr
	}
	for _, key := range []string{"multiSelect", "multi_select", "is_multi_select"} {
		if ms, ok := m[key].(bool); ok {
			bq.MultiSelect = ms
			break
		}
	}
	bq.Options = extractOptionStrings(m["options"])
	return bq, bq.Question != "" || len(bq.Options) > 0
}

func ExtractQuestionAndOptions(args map[string]interface{}) (string, []string) {
	if args == nil {
		return "", nil
	}
	var questionText string
	var optionsList []string
	for _, qItem := range parseInterfaceSlice(args["questions"]) {
		if qMap, ok := qItem.(map[string]interface{}); ok {
			if bq, ok := parseBlockedQuestion(qMap); ok {
				if questionText == "" {
					questionText = bq.Question
				}
				optionsList = append(optionsList, bq.Options...)
			}
		}
	}
	if questionText == "" {
		if qStr, ok := args["question"].(string); ok {
			questionText = qStr
		} else if promptStr, ok := args["prompt"].(string); ok {
			questionText = promptStr
		}
	}
	if len(optionsList) == 0 {
		optionsList = extractOptionStrings(args["options"])
	}
	return questionText, optionsList
}

func ExtractQuestions(args map[string]interface{}) []agent.BlockedQuestion {
	if args == nil {
		return nil
	}
	var questions []agent.BlockedQuestion
	for _, qItem := range parseInterfaceSlice(args["questions"]) {
		if qMap, ok := qItem.(map[string]interface{}); ok {
			if bq, ok := parseBlockedQuestion(qMap); ok {
				questions = append(questions, bq)
			}
		}
	}
	if len(questions) == 0 {
		if bq, ok := parseBlockedQuestion(args); ok {
			questions = append(questions, bq)
		}
	}
	return questions
}

func ParseInteractiveToolBlock(toolName string, toolInput interface{}) *agent.Blocked {
	if toolName == "" {
		return nil
	}
	tLower := strings.ToLower(toolName)
	if tLower == "exitplanmode" || tLower == "exit_plan_mode" {
		planMarkdown := ""
		switch v := toolInput.(type) {
		case string:
			var m map[string]interface{}
			if json.Unmarshal([]byte(v), &m) == nil {
				planMarkdown, _ = m["plan"].(string)
			} else {
				planMarkdown = v
			}
		case map[string]interface{}:
			planMarkdown, _ = v["plan"].(string)
		}
		return &agent.Blocked{
			Kind:     agent.BlockApproval,
			Reason:   planMarkdown,
			Question: "Approve implementation plan?",
			Options:  []string{"Approve Plan", "Request Changes"},
			Since:    time.Now(),
		}
	}

	if toolName == "AskUserQuestion" || toolName == "ask_question" || toolName == "AskFollowupQuestion" ||
		strings.Contains(tLower, "question") || strings.Contains(tLower, "askuser") {
		var inputMap map[string]interface{}
		switch v := toolInput.(type) {
		case string:
			_ = json.Unmarshal([]byte(v), &inputMap)
		case map[string]interface{}:
			inputMap = v
		}
		questions := ExtractQuestions(inputMap)
		questionText, optionsList := ExtractQuestionAndOptions(inputMap)
		if questionText == "" && len(questions) > 0 {
			questionText = questions[0].Question
		}
		if len(optionsList) == 0 && len(questions) > 0 {
			optionsList = questions[0].Options
		}
		reason := "Waiting for user response"
		if questionText != "" {
			reason = questionText
		}
		return &agent.Blocked{
			Kind:      agent.BlockQuestion,
			Reason:    reason,
			Question:  questionText,
			Options:   optionsList,
			Questions: questions,
			Since:     time.Now(),
		}
	}
	return nil
}
