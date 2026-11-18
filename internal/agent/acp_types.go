package agent

import (
	"encoding/json"
	"time"
)

type ACPAuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ACPPermissionOption struct {
	OptionID string `json:"option_id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

type ACPCommandInfo struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argument_hint,omitempty"`
}

type ACPRewindPoint struct {
	ID        string    `json:"id"`
	Turn      int       `json:"turn"`
	Preview   string    `json:"preview"`
	Timestamp time.Time `json:"timestamp"`
}

type ACPControlPrompt struct {
	RequestID   string                `json:"request_id"`
	RawID       json.RawMessage       `json:"raw_id,omitempty"`
	Method      string                `json:"method"`
	ToolName    string                `json:"tool_name,omitempty"`
	ToolInput   any                   `json:"tool_input,omitempty"`
	Blocked     *Blocked              `json:"blocked,omitempty"`
	Options     []ACPPermissionOption `json:"options,omitempty"`
	Questions   []BlockedQuestion     `json:"questions,omitempty"`
	PlanContent string                `json:"plan_content,omitempty"`
}

type ACPNotificationResult struct {
	DetectedModel     string
	AvailableCommands []ACPCommandInfo
	SystemEvents      []map[string]any
}
