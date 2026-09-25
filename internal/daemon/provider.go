package daemon

import (
	"context"
	"time"
)

// SessionMeta contains rich session discovery metadata resolved from workspace files
type SessionMeta struct {
	Title         string    `json:"title,omitempty"`
	CustomTitle   string    `json:"custom_title,omitempty"`
	AITitle       string    `json:"ai_title,omitempty"`
	AIDescription string    `json:"ai_description,omitempty"`
	FirstPrompt   string    `json:"first_prompt,omitempty"`
	LastPrompt    string    `json:"last_prompt,omitempty"`
	Entrypoint    string    `json:"entrypoint,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	Version       string    `json:"version,omitempty"`
	GitBranch     string    `json:"git_branch,omitempty"`
	ContextPct    int       `json:"context_pct,omitempty"`
	LastMessageAt time.Time `json:"last_message_at,omitempty"`
}

// ActiveSubagent represents a structured subagent discovered on disk or via hooks
type ActiveSubagent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Prompt    string    `json:"prompt,omitempty"`
	State     string    `json:"state"` // "running", "completed", "killed"
	Model     string    `json:"model,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// AgentIdentity provides agent naming, branding, and UI presentation metadata.
type AgentIdentity interface {
	Agent() string
	DisplayName() string
	BrandColor() string
	IconSVG() string
}

// ProcessDetector provides binary existence, process name matching, and hook configuration checks.
type ProcessDetector interface {
	IsInstalled() bool
	ProcessNames() []string
	CheckHookConfig() (configured bool, setupCmd string, err error)
}

// HookParser translates incoming agent-specific telemetry webhooks or stdin payloads into canonical Ackbar Events.
type HookParser interface {
	ParseHook(eventName string, payload []byte) (*Event, error)
}

// SessionLifecycle provides CLI launch and resume command templates for tmux or terminal sessions.
type SessionLifecycle interface {
	GetSpawnCommand(tempUUID string) string
	GetResumeCommand(nativeID string) string
}

// TranscriptReader extracts past conversation messages, session titles, and cleans agent storage.
type TranscriptReader interface {
	ReadSessionMetadata(cwd, nativeID string) *SessionMeta
	ResolveSessionTitle(cwd, nativeID string) string
	ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error)
	CleanSessionFiles(home, cwd, nativeID string) error
}

// StatusInspector is an optional capability interface for agents that perform custom pane,
// process, or output inspection to detect working/idle/blocked states.
type StatusInspector interface {
	InspectStatus(ctx context.Context, sess *Session) bool
}

// SubagentDiscoverer is an optional capability interface for agents that maintain structured
// subagent state on disk or across teammate processes.
type SubagentDiscoverer interface {
	ListSubagents(home, cwd, nativeID string) ([]*ActiveSubagent, error)
}

// Provider defines the core contract required for an AI agent integration in Ackbar.
type Provider interface {
	AgentIdentity
	ProcessDetector
	HookParser
	SessionLifecycle
	TranscriptReader
}

// FullProvider represents an agent integration supporting all core and optional capabilities.
type FullProvider interface {
	Provider
	StatusInspector
	SubagentDiscoverer
}

// ProviderDTO represents provider discovery metadata exposed over the REST API
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
