package grok

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) ParseACPServerRequest(rawID json.RawMessage, reqID, method string, params map[string]any) (*agent.ACPControlPrompt, bool) {
	rawCopy := append([]byte(nil), bytes.TrimSpace(rawID)...)
	switch method {
	case "session/request_permission":
		return agentutil.ParseACPPermissionRequest(rawCopy, reqID, params)

	case "_x.ai/exit_plan_mode", "x.ai/exit_plan_mode":
		plan := firstMapStr(params, "planContent", "plan", "content")
		reason := plan
		if strings.TrimSpace(reason) == "" {
			reason = "Approve implementation plan?"
		}
		return &agent.ACPControlPrompt{
			RequestID:   reqID,
			RawID:       rawCopy,
			Method:      method,
			ToolName:    "ExitPlanMode",
			ToolInput:   params,
			PlanContent: plan,
			Blocked: &agent.Blocked{
				Kind:      agent.BlockApproval,
				ToolName:  "ExitPlanMode",
				Reason:    reason,
				Question:  "Approve implementation plan?",
				Options:   []string{"Approve Plan", "Request Changes"},
				RequestID: reqID,
				Since:     time.Now(),
			},
		}, true

	case "_x.ai/ask_user_question", "x.ai/ask_user_question":
		qs := agentutil.ExtractQuestions(params)
		firstQ, firstOpts := agentutil.ExtractQuestionAndOptions(params)
		if firstQ == "" && len(qs) > 0 {
			firstQ = qs[0].Question
		}
		if len(firstOpts) == 0 && len(qs) > 0 {
			firstOpts = qs[0].Options
		}
		reason := firstQ
		if reason == "" {
			reason = "Waiting for user response"
		}
		return &agent.ACPControlPrompt{
			RequestID: reqID,
			RawID:     rawCopy,
			Method:    method,
			ToolName:  "AskUserQuestion",
			ToolInput: params,
			Questions: qs,
			Blocked: &agent.Blocked{
				Kind:      agent.BlockQuestion,
				ToolName:  "AskUserQuestion",
				Reason:    reason,
				Question:  firstQ,
				Questions: qs,
				Options:   firstOpts,
				RequestID: reqID,
				Since:     time.Now(),
			},
		}, true

	default:
		return nil, false
	}
}

func (*Plugin) BuildACPServerResponse(prompt *agent.ACPControlPrompt, approved bool, answer string) (any, error) {
	if prompt == nil {
		return nil, fmt.Errorf("nil acp control prompt")
	}
	switch prompt.Method {
	case "session/request_permission":
		return agentutil.BuildACPPermissionResponse(prompt, approved, answer), nil

	case "_x.ai/exit_plan_mode", "x.ai/exit_plan_mode":
		if approved {
			return map[string]any{"outcome": "approved"}, nil
		}
		low := strings.ToLower(strings.TrimSpace(answer))
		if low == "abandon" || low == "abandoned" {
			return map[string]any{"outcome": "abandoned"}, nil
		}
		return map[string]any{"outcome": "cancelled"}, nil

	case "_x.ai/ask_user_question", "x.ai/ask_user_question":
		if !approved {
			return map[string]any{"outcome": "cancelled", "answers": []any{}}, nil
		}
		return map[string]any{"outcome": "accepted", "answers": buildGrokQuestionAnswers(prompt, answer)}, nil

	default:
		return nil, fmt.Errorf("unsupported acp server request method: %s", prompt.Method)
	}
}

type rawQuestionMeta struct {
	id          string
	question    string
	options     []string
	multiSelect bool
}

func extractRawQuestionMetas(prompt *agent.ACPControlPrompt) []rawQuestionMeta {
	params, _ := prompt.ToolInput.(map[string]any)
	rawQs := agentutil.ParseInterfaceSlice(params["questions"])
	if len(rawQs) == 0 {
		out := make([]rawQuestionMeta, 0, len(prompt.Questions))
		for _, q := range prompt.Questions {
			out = append(out, rawQuestionMeta{question: q.Question, options: q.Options, multiSelect: q.MultiSelect})
		}
		return out
	}
	out := make([]rawQuestionMeta, 0, len(rawQs))
	for i, item := range rawQs {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var bq agent.BlockedQuestion
		if i < len(prompt.Questions) {
			bq = prompt.Questions[i]
		}
		qText := firstMapStr(m, "question", "prompt", "text")
		if qText == "" {
			qText = bq.Question
		}
		ms, _ := m["multiSelect"].(bool)
		if !ms {
			ms = bq.MultiSelect
		}
		out = append(out, rawQuestionMeta{
			id:          firstMapStr(m, "id", "questionId"),
			question:    qText,
			options:     bq.Options,
			multiSelect: ms,
		})
	}
	return out
}

func buildGrokQuestionAnswers(prompt *agent.ACPControlPrompt, rawAnswer string) []map[string]any {
	metas := extractRawQuestionMetas(prompt)
	trimmed := strings.TrimSpace(rawAnswer)

	var jsonAnswers map[string]any
	if strings.HasPrefix(trimmed, "{") {
		_ = json.Unmarshal([]byte(trimmed), &jsonAnswers)
	}
	var semiParts []string
	if len(jsonAnswers) == 0 && len(metas) > 1 {
		semiParts = strings.SplitN(rawAnswer, "; ", len(metas))
	}

	if len(metas) == 0 {
		return []map[string]any{formatSingleQuestionAnswer(rawQuestionMeta{}, trimmed)}
	}
	answers := make([]map[string]any, 0, len(metas))
	for i, meta := range metas {
		var val any
		if len(jsonAnswers) > 0 {
			for _, key := range []string{meta.id, meta.question} {
				if key != "" {
					if v, ok := jsonAnswers[key]; ok {
						val = v
						break
					}
				}
			}
		} else if len(semiParts) > 0 {
			if i < len(semiParts) {
				val = strings.TrimSpace(semiParts[i])
			}
		} else {
			val = trimmed
		}
		answers = append(answers, formatSingleQuestionAnswer(meta, val))
	}
	return answers
}

func formatSingleQuestionAnswer(meta rawQuestionMeta, val any) map[string]any {
	entry := map[string]any{"selectedOptions": []string{}}
	if meta.id != "" {
		entry["questionId"] = meta.id
	}
	if m, ok := val.(map[string]any); ok {
		if sel := agentutil.ParseInterfaceSlice(m["selectedOptions"]); len(sel) > 0 {
			var opts []string
			for _, s := range sel {
				if str, ok := s.(string); ok && str != "" {
					opts = append(opts, str)
				}
			}
			entry["selectedOptions"] = opts
		}
		if tr, ok := m["textResponse"].(string); ok && tr != "" {
			entry["textResponse"] = tr
		}
		return entry
	}

	if arr, ok := val.([]any); ok {
		var opts []string
		for _, item := range arr {
			if s, ok := item.(string); ok && s != "" {
				opts = append(opts, s)
			}
		}
		if opts != nil {
			entry["selectedOptions"] = opts
		}
		return entry
	}
	ansStr, _ := val.(string)
	ansStr = strings.TrimSpace(ansStr)
	if ansStr == "" {
		return entry
	}
	matchOpt := func(cand string) (string, bool) {
		for _, opt := range meta.options {
			if strings.EqualFold(opt, cand) {
				return opt, true
			}
		}
		return "", false
	}

	if meta.multiSelect && strings.Contains(ansStr, ",") {
		var selected []string
		var extra []string
		for _, part := range strings.Split(ansStr, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if m, ok := matchOpt(p); ok {
				selected = append(selected, m)
			} else {
				extra = append(extra, p)
			}
		}
		if selected == nil {
			selected = []string{}
		}
		entry["selectedOptions"] = selected
		if len(extra) > 0 {
			entry["textResponse"] = strings.Join(extra, ", ")
		}
		return entry
	}
	if m, ok := matchOpt(ansStr); ok {
		entry["selectedOptions"] = []string{m}
		return entry
	}
	entry["textResponse"] = ansStr
	return entry
}
