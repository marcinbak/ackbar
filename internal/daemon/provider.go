package daemon

import (
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

// Provider defines the complete contract for AI agent integrations in Ackbar
type Provider interface {
	// 1. Identity & UI Presentation
	Agent() string
	DisplayName() string
	BrandColor() string
	IconSVG() string

	// 2. Binary & Process Discovery
	IsInstalled() bool
	ProcessNames() []string
	CheckHookConfig() (configured bool, setupCmd string, err error)

	// 3. Telemetry & Hooks
	ParseHook(eventName string, payload []byte) (*Event, error)

	// 4. Lifecycle Commands
	GetSpawnCommand(tempUUID string) string
	GetResumeCommand(nativeID string) string

	// 5. Workspace Metadata & Transcripts
	ReadSessionMetadata(cwd, nativeID string) *SessionMeta
	ResolveSessionTitle(cwd, nativeID string) string
	ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error)
	CleanSessionFiles(home, cwd, nativeID string) error
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
