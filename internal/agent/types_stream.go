package agent

import "time"

type ProviderDTO struct {
	Agent          string   `json:"agent"`
	DisplayName    string   `json:"display_name"`
	BrandColor     string   `json:"brand_color"`
	IconSVG        string   `json:"icon_svg"`
	IsInstalled    bool     `json:"is_installed"`
	HookConfigured bool     `json:"hook_configured"`
	SetupCmd       string   `json:"setup_cmd"`
	ProcessNames   []string `json:"process_names"`
}

type AgentStatus struct {
	Key            string `json:"key"`
	DisplayName    string `json:"display_name"`
	Detected       bool   `json:"detected"`
	ConfigFile     string `json:"config_file"`
	SkillsDir      string `json:"skills_dir"`
	RulesFile      string `json:"rules_file"`
	MCPInstalled   bool   `json:"mcp_installed"`
	MCPCommand     string `json:"mcp_command"`
	SkillInstalled bool   `json:"skill_installed"`
	SkillVersion   string `json:"skill_version"`
	RulesInstalled bool   `json:"rules_installed"`
	RulesVersion   string `json:"rules_version"`
	DriftDetected  bool   `json:"drift_detected"`
}

type TranscriptMessage struct {
	Role               string          `json:"role"`
	Content            string          `json:"content"`
	Thinking           string          `json:"thinking,omitempty"`
	ToolCalls          []string        `json:"tool_calls,omitempty"`
	Checklist          []ChecklistItem `json:"checklist,omitempty"`
	ChecklistCleared   bool            `json:"checklist_cleared,omitempty"`
	ChecklistHighWater int             `json:"-"`
	TurnID             string          `json:"turn_id,omitempty"`
	InFlight           bool            `json:"in_flight,omitempty"`
	Timestamp          time.Time       `json:"timestamp"`
}

type Transcript struct {
	SessionID string              `json:"session_id"`
	NativeID  string              `json:"native_id"`
	Agent     string              `json:"agent"`
	Title     string              `json:"title,omitempty"`
	Cwd       string              `json:"cwd,omitempty"`
	Messages  []TranscriptMessage `json:"messages"`
}

type TranscriptUsage struct {
	InputTokens           int     `json:"input_tokens,omitempty"`
	OutputTokens          int     `json:"output_tokens,omitempty"`
	CacheReadTokens       int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens      int     `json:"cache_write_tokens,omitempty"`
	ThinkingTokens        int     `json:"thinking_tokens,omitempty"`
	LastTurnContextTokens int     `json:"last_turn_context_tokens,omitempty"`
	ContextLimitTokens    int     `json:"context_limit_tokens,omitempty"`
	TurnCount             int     `json:"turn_count,omitempty"`
	CostUSD               float64 `json:"cost_usd,omitempty"`
}

type Event struct {
	Agent       string
	NativeID    string
	Cwd         string
	Roots       []string
	EventName   string
	State       State
	Blocked     *Blocked
	Activity    string
	Name        string
	Entrypoint  string
	Kind        string
	Version     string
	ContextPct  int
	ToolName    string
	ToolInput   any
	StartedAt   time.Time
	LastEventAt time.Time
}

type ChatStreamEvent struct {
	Seq              int64              `json:"seq,omitempty"`
	SessionID        string             `json:"session_id"`
	TurnID           string             `json:"turn_id,omitempty"`
	NativeID         string             `json:"native_id,omitempty"`
	Model            string             `json:"model,omitempty"`
	Type             string             `json:"type"`
	Text             string             `json:"text,omitempty"`
	Thinking         string             `json:"thinking,omitempty"`
	ToolName         string             `json:"tool_name,omitempty"`
	ToolInput        any                `json:"tool_input,omitempty"`
	ToolOutput       string             `json:"tool_output,omitempty"`
	Checklist        []ChecklistItem    `json:"checklist,omitempty"`
	ChecklistCleared bool               `json:"checklist_cleared,omitempty"`
	Blocked          *Blocked           `json:"blocked,omitempty"`
	IsError          bool               `json:"is_error,omitempty"`
	QueueItems       []*PromptQueueItem `json:"queue_items,omitempty"`
	QueuePaused      bool               `json:"queue_paused,omitempty"`
	Subagents        []*SubagentInfo    `json:"subagents,omitempty"`
	Timestamp        time.Time          `json:"timestamp"`
}

type PromptQueueItem struct {
	ID        string     `json:"id"`
	SessionID string     `json:"session_id"`
	Text      string     `json:"text"`
	DeliverAt *time.Time `json:"deliver_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type ModelSuggestion struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type WorkspaceDoc struct {
	FullPath      string `json:"full_path"`
	Title         string `json:"title"`
	RelPath       string `json:"rel_path"`
	Category      string `json:"category"`
	CategoryLabel string `json:"category_label"`
	Priority      int    `json:"priority"`
}

type LaunchInput struct {
	SessionID      string
	NativeID       string
	TempUUID       string
	Resume         bool
	Cwd            string
	Prompt         string
	Model          string
	Effort         string
	PermissionMode string
	YoloMode       bool
	ExtraDirs      []string
	Env            map[string]string
	SystemPrompt   string
}

type LaunchSpec struct {
	Command         string
	Args            []string
	Env             map[string]string
	CommandLine     string
	PromptInCommand bool
}

type HeadlessTurnInput struct {
	Session      *Session
	Prompt       string
	HomeDir      string
	IsExisting   bool
	SystemPrompt string
}

type HeadlessTurnSpec struct {
	Binary string
	Args   []string
	Env    []string
	Cwd    string
	Mode   HeadlessExecMode
}

type DiskScanInput struct {
	HomeDir          string
	HostName         string
	ExistingSessions []*Session
	IsSessionDeleted func(id string) bool
}

type DiskSessionAdoption struct {
	OldSessionID string
	NewNativeID  string
	Title        string
	Cwd          string
	UpdatedAt    time.Time
}

type DiskSessionMetadataUpdate struct {
	SessionID string
	Cwd       string
	Title     string
}

type DiskScanResult struct {
	AdoptUpdates    []DiskSessionAdoption
	MetadataUpdates []DiskSessionMetadataUpdate
	PruneSessionIDs []string
}
