package agent

import (
	"context"
	"time"
)

type Identity interface {
	Key() string
	DisplayName() string
	Aliases() []string
	BinaryNames() []string
}

type Branding interface {
	BrandColor() string
	IconSVG() string
}

type Plugin interface {
	Identity
	Capabilities() Capabilities
}

type Provisioner interface {
	IsInstalled(homeDir string) bool
	CheckStatus(ctx context.Context, homeDir string) AgentStatus
	InstallHooks(homeDir string) error
	Provision(ctx context.Context, homeDir string) error
}

type Accounts interface {
	AccountEnvVars(homeDir string, acct *AgentAccount) map[string]string
	IsAccountLoggedIn(homeDir string, acct *AgentAccount) bool
	SetupProfileDir(homeDir, name, configDir string) (string, error)
	LoginCommand() string
	CaptureCredentials(homeDir string) (string, error)
	ActivateAccount(homeDir string, acct *AgentAccount) error
	RefreshAccount(ctx context.Context, acct *AgentAccount) (bool, error)
	AuthenticateAccount(ctx context.Context, label string, env []string) (*AgentAccount, error)
}

type TMuxDriver interface {
	BuildLaunchSpec(in LaunchInput) (LaunchSpec, error)
	SpawnCommand(tempUUID string) string
	ResumeCommand(nativeID string) string
	ResetCommand() string
	ApprovalKeys(allow bool) []string
	InspectStatus(ctx context.Context, sess *Session) bool
}

type HeadlessDriver interface {
	Mode() HeadlessExecMode
	ResolveBinary() (string, error)
	BuildTurnSpec(in HeadlessTurnInput) (HeadlessTurnSpec, error)
	ParseStreamEvent(line []byte) ([]ChatStreamEvent, error)
	ComputeUsage(sessionID, cwd, model string) (TranscriptUsage, bool)
}

type Hooks interface {
	CheckHookConfig(homeDir string) (configured bool, setupCmd string, err error)
	ParseHook(eventName string, payload []byte) (*Event, error)
	HookAckPayload(eventName string) []byte
	ResolveSubagentParent(homeDir, nativeID string) (parentID, role, typeName string)
}

type Transcripts interface {
	ReadSessionMetadata(cwd, nativeID string) *SessionMeta
	ResolveSessionTitle(cwd, nativeID string) string
	ExtractTranscript(homeDir, cwd, nativeID string) (*Transcript, error)
	FilterUserEntry(raw map[string]interface{}, content string) (clean string, keep bool)
	ListSubagents(homeDir, cwd, nativeID string) ([]SubagentInfo, error)
	ListActiveSubagents(homeDir, cwd, nativeID string) ([]*ActiveSubagent, error)
	IsSubagentSession(homeDir, nativeID string, precomputed map[string]bool) bool
	HasConversationArtifacts(homeDir, nativeID string) bool
	CleanSessionFiles(homeDir, cwd, nativeID string) error
	ListWorkspaceDocuments(homeDir, cwd, nativeID string) []WorkspaceDoc
}

type Scanner interface {
	IsValidNativeID(nativeID string) bool
	IsIgnoredCommand(fullCmd string) bool
	MatchProcessCommand(binLower, cmdLower string) bool
	RequiresResolvedSessionID() bool
	ResolveSessionForPID(ctx context.Context, homeDir string, pid int, cmdFields []string) string
	ResolveSessionOwner(homeDir, sessionID string) (pid int, tmuxName string)
	ScanDisk(ctx context.Context, in DiskScanInput) DiskScanResult
}

type ModelCatalog interface {
	DefaultModels() []ModelSuggestion
	CollectModels(homeDir string) []ModelSuggestion
	ValidateModelID(model string) bool
}

type RateLimiter interface {
	DetectRateLimit(text string, now time.Time) (limited bool, resetAt time.Time, reason string)
}
