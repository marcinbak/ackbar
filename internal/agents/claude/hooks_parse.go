package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type claudePayload struct {
	SessionID            string      `json:"session_id"`
	Cwd                  string      `json:"cwd"`
	HookEventName        string      `json:"hook_event_name"`
	Source               string      `json:"source"`
	LastAssistantMessage string      `json:"last_assistant_message"`
	PermissionMode       string      `json:"permission_mode"`
	ToolName             string      `json:"tool_name"`
	ToolInput            interface{} `json:"tool_input"`
	RequestedPermission  string      `json:"requested_permission"`
	NotificationType     string      `json:"notification_type"`
	Prompt               string      `json:"prompt"`
	UserPrompt           string      `json:"user_prompt"`
	Title                string      `json:"title"`
	CustomTitle          string      `json:"custom_title"`
	AITitle              string      `json:"ai_title"`
	Name                 string      `json:"name"`
	Summary              string      `json:"summary"`
	IsSidechain          bool        `json:"is_sidechain"`
	IsSidechainCamel     bool        `json:"isSidechain"`
	AgentID              string      `json:"agent_id"`
	AgentIDCamel         string      `json:"agentId"`
	AgentType            string      `json:"agent_type"`
	Question             string      `json:"question"`
	Questions            []struct {
		Question      string      `json:"question"`
		Header        string      `json:"header"`
		Options       interface{} `json:"options"`
		MultiSelect   bool        `json:"multiSelect"`
		IsMultiSelect bool        `json:"is_multi_select"`
	} `json:"questions"`
	Options []string `json:"options"`
}

func extractPayloadQuestions(p *claudePayload) []agent.BlockedQuestion {
	var questions []agent.BlockedQuestion
	for _, qItem := range p.Questions {
		var bq agent.BlockedQuestion
		bq.Question = qItem.Question
		bq.Header = qItem.Header
		bq.MultiSelect = qItem.MultiSelect || qItem.IsMultiSelect
		switch v := qItem.Options.(type) {
		case []string:
			bq.Options = v
		case []interface{}:
			for _, opt := range v {
				if s, ok := opt.(string); ok && s != "" {
					bq.Options = append(bq.Options, s)
				} else if m, ok := opt.(map[string]interface{}); ok {
					if label, ok := m["label"].(string); ok && label != "" {
						bq.Options = append(bq.Options, label)
					} else if text, ok := m["text"].(string); ok && text != "" {
						bq.Options = append(bq.Options, text)
					}
				}
			}
		}
		if bq.Question != "" || len(bq.Options) > 0 {
			questions = append(questions, bq)
		}
	}
	if len(questions) == 0 && p.ToolInput != nil {
		switch v := p.ToolInput.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				var toolMap map[string]interface{}
				if json.Unmarshal([]byte(v), &toolMap) == nil {
					questions = agentutil.ExtractQuestions(toolMap)
				}
			}
		case map[string]interface{}:
			questions = agentutil.ExtractQuestions(v)
		}
	}
	if len(questions) == 0 && (p.Question != "" || len(p.Options) > 0) {
		questions = append(questions, agent.BlockedQuestion{Question: p.Question, Options: p.Options})
	}
	return questions
}

func extractPayloadQuestionAndOptions(p *claudePayload) (string, []string) {
	if questions := extractPayloadQuestions(p); len(questions) > 0 {
		return questions[0].Question, questions[0].Options
	}
	return p.Question, append([]string(nil), p.Options...)
}

func extractPlanText(toolInput interface{}) string {
	switch v := toolInput.(type) {
	case string:
		var m map[string]interface{}
		if json.Unmarshal([]byte(v), &m) == nil {
			if pStr, ok := m["plan"].(string); ok {
				return pStr
			}
			return ""
		}
		return v
	case map[string]interface{}:
		if pStr, ok := v["plan"].(string); ok {
			return pStr
		}
	}
	return ""
}

func (*Plugin) DetectRateLimit(text string, now time.Time) (bool, time.Time, string) {
	if text == "" {
		return false, time.Time{}, ""
	}
	lower := strings.ToLower(text)
	var prefix string
	switch {
	case strings.Contains(lower, "5-hour limit") || strings.Contains(lower, "5h limit"):
		prefix = "5-hour limit reached"
	case strings.Contains(lower, "weekly limit reached"):
		prefix = "Weekly limit reached"
	case strings.Contains(lower, "session limit reached"):
		prefix = "Session limit reached"
	case strings.Contains(lower, "claude ai usage limit") || strings.Contains(lower, "usage limit reached"):
		prefix = "Usage limit reached"
	case strings.Contains(lower, "rate limit reached") || strings.Contains(lower, "rate limit exceeded"):
		prefix = "Rate limit reached"
	case strings.Contains(lower, "limit reached") && (strings.Contains(lower, "resets") || strings.Contains(lower, "retry")):
		prefix = "Limit reached"
	default:
		return false, time.Time{}, ""
	}
	resetAt, _ := agentutil.ParseRateLimitResetTime(text, now)
	return true, resetAt, fmt.Sprintf("%s · resets %s", prefix, resetAt.Local().Format("15:04"))
}

func (p *Plugin) rateLimitBlocked(text string) (*agent.Blocked, string, bool) {
	if text == "" {
		return nil, "", false
	}
	limited, resetAt, reason := p.DetectRateLimit(text, time.Now())
	if !limited {
		return nil, "", false
	}
	return &agent.Blocked{
		Kind: agent.BlockRateLimit, Reason: reason, Question: reason, ResetAt: &resetAt,
		Options: []string{fmt.Sprintf("Resume at %s", resetAt.Local().Format("15:04")), "Resume in 1h"}, Since: time.Now(),
	}, "Rate limit: " + reason, true
}
