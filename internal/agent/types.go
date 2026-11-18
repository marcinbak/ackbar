package agent

import (
	"encoding/json"
	"strings"
	"time"
)

type State int

const (
	StateUnknown State = iota
	StateWorking
	StateBlocked
	StateIdle
	StateEnded
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateWorking:
		return "working"
	case StateBlocked:
		return "blocked"
	case StateIdle:
		return "idle"
	case StateEnded:
		return "ended"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type BlockKind string

const (
	BlockPermission BlockKind = "permission"
	BlockQuestion   BlockKind = "question"
	BlockApproval   BlockKind = "approval"
	BlockRateLimit  BlockKind = "rate_limit"
	BlockAuth       BlockKind = "auth"
)

type BlockedQuestion struct {
	Question    string   `json:"question"`
	Header      string   `json:"header,omitempty"`
	Options     []string `json:"options,omitempty"`
	MultiSelect bool     `json:"multi_select,omitempty"`
}

type Blocked struct {
	Kind           BlockKind         `json:"kind"`
	Reason         string            `json:"reason"`
	Since          time.Time         `json:"since"`
	Question       string            `json:"question,omitempty"`
	Options        []string          `json:"options,omitempty"`
	Questions      []BlockedQuestion `json:"questions,omitempty"`
	ResetAt        *time.Time        `json:"reset_at,omitempty"`
	AuthTarget     string            `json:"auth_target,omitempty"`
	AuthKind       string            `json:"auth_kind,omitempty"`
	AuthServer     string            `json:"auth_server,omitempty"`
	AuthURL        string            `json:"auth_url,omitempty"`
	AuthHint       string            `json:"auth_hint,omitempty"`
	FlowID         string            `json:"flow_id,omitempty"`
	RequestID      string            `json:"request_id,omitempty"`
	ToolName       string            `json:"tool_name,omitempty"`
	ToolUseID      string            `json:"tool_use_id,omitempty"`
	DecisionReason string            `json:"decision_reason,omitempty"`
}

type blockedMetaJSON struct {
	AuthTarget     string `json:"auth_target,omitempty"`
	AuthKind       string `json:"auth_kind,omitempty"`
	AuthServer     string `json:"auth_server,omitempty"`
	AuthURL        string `json:"auth_url,omitempty"`
	AuthHint       string `json:"auth_hint,omitempty"`
	FlowID         string `json:"flow_id,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	ToolUseID      string `json:"tool_use_id,omitempty"`
	DecisionReason string `json:"decision_reason,omitempty"`
}

func MarshalBlockedMeta(b *Blocked) string {
	if b == nil {
		return ""
	}
	if b.AuthTarget == "" && b.AuthKind == "" && b.AuthServer == "" && b.AuthURL == "" && b.AuthHint == "" && b.FlowID == "" &&
		b.RequestID == "" && b.ToolName == "" && b.ToolUseID == "" && b.DecisionReason == "" {
		return ""
	}
	meta := blockedMetaJSON{
		AuthTarget:     b.AuthTarget,
		AuthKind:       b.AuthKind,
		AuthServer:     b.AuthServer,
		AuthURL:        b.AuthURL,
		AuthHint:       b.AuthHint,
		FlowID:         b.FlowID,
		RequestID:      b.RequestID,
		ToolName:       b.ToolName,
		ToolUseID:      b.ToolUseID,
		DecisionReason: b.DecisionReason,
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(raw)
}

func ApplyBlockedMeta(b *Blocked, raw string) {
	if b == nil || strings.TrimSpace(raw) == "" {
		return
	}
	var meta blockedMetaJSON
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return
	}
	b.AuthTarget = meta.AuthTarget
	b.AuthKind = meta.AuthKind
	b.AuthServer = meta.AuthServer
	b.AuthURL = meta.AuthURL
	b.AuthHint = meta.AuthHint
	b.FlowID = meta.FlowID
	b.RequestID = meta.RequestID
	b.ToolName = meta.ToolName
	b.ToolUseID = meta.ToolUseID
	b.DecisionReason = meta.DecisionReason
}

const (
	EngineTmux     = "tmux"
	EngineHeadless = "headless"
)

type Session struct {
	ID                string    `json:"id"`
	Agent             string    `json:"agent"`
	Host              string    `json:"host"`
	NativeID          string    `json:"native_id"`
	Cwd               string    `json:"cwd"`
	Roots             []string  `json:"roots"`
	ProjectKey        string    `json:"project_key"`
	State             State     `json:"state"`
	Blocked           *Blocked  `json:"blocked,omitempty"`
	Activity          string    `json:"activity"`
	StartedAt         time.Time `json:"started_at"`
	LastEventAt       time.Time `json:"last_event_at"`
	Managed           bool      `json:"managed"`
	TmuxName          string    `json:"tmux_name"`
	PID               int       `json:"pid"`
	Archived          bool      `json:"archived"`
	NodePath          string    `json:"node_path"`
	Name              string    `json:"name,omitempty"`
	Entrypoint        string    `json:"entrypoint,omitempty"`
	Kind              string    `json:"kind,omitempty"`
	Version           string    `json:"version,omitempty"`
	ContextPct        int       `json:"context_pct,omitempty"`
	GitBranch         string    `json:"git_branch,omitempty"`
	Deleted           bool      `json:"deleted,omitempty"`
	CustomTitle       string    `json:"custom_title,omitempty"`
	AITitle           string    `json:"ai_title,omitempty"`
	AIDescription     string    `json:"ai_description,omitempty"`
	FirstPrompt       string    `json:"first_prompt,omitempty"`
	LastPrompt        string    `json:"last_prompt,omitempty"`
	IsUnread          bool      `json:"is_unread,omitempty"`
	IsDone            bool      `json:"is_done,omitempty"`
	IsLater           bool      `json:"is_later,omitempty"`
	IsSupervisor      bool      `json:"is_supervisor,omitempty"`
	CurrentTaskID     string    `json:"current_task_id,omitempty"`
	LastStateChangeAt time.Time `json:"last_state_change_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
	AccountID         string    `json:"account_id,omitempty"`
	EngineType        string    `json:"engine_type,omitempty"`
	Model             string    `json:"model,omitempty"`
	ResolvedModel     string    `json:"resolved_model,omitempty"`
	Effort            string    `json:"effort,omitempty"`
	PermissionMode    string    `json:"permission_mode,omitempty"`
	YoloMode          bool      `json:"yolo_mode,omitempty"`
	ExtraDirs         []string  `json:"extra_dirs,omitempty"`
	RunningSubagents  int       `json:"running_subagents,omitempty"`
	HostOffline       bool      `json:"host_offline,omitempty"`
	MetadataUpdatedAt time.Time `json:"metadata_updated_at,omitempty"`
	OldID             string    `json:"old_id,omitempty"`
	MigratedTo        string    `json:"migrated_to,omitempty"`
}

type AgentAccount struct {
	ID          string            `json:"id"`
	Agent       string            `json:"agent"`
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	ConfigDir   string            `json:"config_dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	IsDefault   bool              `json:"is_default"`
	IsLoggedIn  bool              `json:"is_logged_in,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type SessionMeta struct {
	Title             string    `json:"title,omitempty"`
	CustomTitle       string    `json:"custom_title,omitempty"`
	AITitle           string    `json:"ai_title,omitempty"`
	AIDescription     string    `json:"ai_description,omitempty"`
	FirstPrompt       string    `json:"first_prompt,omitempty"`
	LastPrompt        string    `json:"last_prompt,omitempty"`
	Entrypoint        string    `json:"entrypoint,omitempty"`
	Kind              string    `json:"kind,omitempty"`
	Version           string    `json:"version,omitempty"`
	GitBranch         string    `json:"git_branch,omitempty"`
	ContextPct        int       `json:"context_pct,omitempty"`
	LastMessageAt     time.Time `json:"last_message_at,omitempty"`
	LastUserMessageAt time.Time `json:"last_user_message_at,omitempty"`
}

type ActiveSubagent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Prompt    string    `json:"prompt,omitempty"`
	State     string    `json:"state"`
	Model     string    `json:"model,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

type SubagentInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty"`
	Prompt    string    `json:"prompt"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"started_at"`
	ToolName  string    `json:"-"`
	Synthetic bool      `json:"-"`
}

type CommandSuggestion struct {
	Command      string `json:"command"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argument_hint,omitempty"`
}
