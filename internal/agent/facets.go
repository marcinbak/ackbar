package agent

import (
	"context"
	"encoding/json"
	"io"
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

type InstallationDetector interface {
	IsInstalled(homeDir string) bool
}

type Provisioner interface {
	InstallationDetector
	CheckStatus(ctx context.Context, homeDir string) AgentStatus
	InstallHooks(homeDir string) error
	Provision(ctx context.Context, homeDir string) error
	SkillDirs(homeDir string) []string
}

type Accounts interface {
	APIKeyEnvVar() string
	AccountEnvVars(homeDir string, acct *AgentAccount) map[string]string
	IsAccountLoggedIn(homeDir string, acct *AgentAccount) bool
	SetupProfileDir(homeDir, name, configDir string) (string, error)
	LoginCommand() string
	CaptureCredentials(homeDir string) (string, error)
	ActivateAccount(homeDir string, acct *AgentAccount) error
	RefreshAccount(ctx context.Context, acct *AgentAccount) (bool, error)
	AuthenticateAccount(ctx context.Context, label string, env []string) (*AgentAccount, error)
	ProfileSkillDirs(homeDir string, acct *AgentAccount) []string
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

type PersistentHeadlessDriver interface {
	HeadlessDriver
	SupportsStdioControl() bool
	DefaultPermissionMode() string
	FormatStdinTurn(in PersistentTurnInput) (string, error)
	FormatSetModel(model string) (stdinLine string, supported bool)
}

type Hooks interface {
	CheckHookConfig(homeDir string) (configured bool, setupCmd string, err error)
	ParseHook(eventName string, payload []byte) (*Event, error)
	HookAckPayload(eventName string) []byte
	ResolveSubagentParent(homeDir, nativeID string) (parentID, role, typeName string)
}

type Transcripts interface {
	ReadSessionMetadata(homeDir, cwd, nativeID string) *SessionMeta
	ResolveSessionTitle(homeDir, cwd, nativeID string) string
	ExtractTranscript(homeDir, cwd, nativeID string) (*Transcript, error)
	FilterUserEntry(raw map[string]interface{}, content string) (clean string, keep bool)
	ListSubagents(homeDir, cwd, nativeID string) ([]SubagentInfo, error)
	ListActiveSubagents(homeDir, cwd, nativeID string) ([]*ActiveSubagent, error)
	IsSubagentSession(homeDir, nativeID string, precomputed map[string]bool) bool
	HasConversationArtifacts(homeDir, nativeID string) bool
	CleanSessionFiles(homeDir, cwd, nativeID string) error
	ListWorkspaceDocuments(homeDir, cwd, nativeID string) []WorkspaceDoc
}

type IncrementalTranscripts interface {
	AppendTranscript(r io.Reader, existing []TranscriptMessage) ([]TranscriptMessage, error)
}

type SessionRewinder interface {
	ListRewindTurns(homeDir, cwd, nativeID string) ([]RewindTurn, error)
	CloneTranscriptBeforeTurn(homeDir, cwd, nativeID string, beforeTurn int, newNativeID string) (cloned bool, err error)
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
	NormalizeModelID(raw string) string
}

type RateLimiter interface {
	DetectRateLimit(text string, now time.Time) (limited bool, resetAt time.Time, reason string)
}

type StreamParser interface {
	ParseStreamLine(line []byte) (events []ChatStreamEvent, handled bool, err error)
}

type StreamParserFactory interface {
	NewStreamParser(sessionID string) StreamParser
}

type ModelResolver interface {
	ResolveArtifactModel(homeDir, accConfigDir, cwd, nativeID string) (string, time.Time)
	ResolveConfiguredModel(homeDir string, acct *AgentAccount) string
}

type TranscriptStatter interface {
	StatTranscript(homeDir, cwd, nativeID string) (path string, size int64, modTimeNano int64, err error)
}

type TranscriptUsageInspector interface {
	ComputeTranscriptUsage(homeDir, cwd, nativeID string, defaultLimit int) (TranscriptUsage, bool)
}

type CLIInspector interface {
	InspectSubcommandArgs(kind string) ([]string, bool)
}

type ACPDriver interface {
	HeadlessDriver
	SelectACPAuth(methods []ACPAuthMethod, env []string) (methodID string, meta map[string]any, ok bool)
	HandleACPNotification(homeDir string, env []string, method string, params map[string]any) ACPNotificationResult
	ParseACPServerRequest(rawID json.RawMessage, reqID string, method string, params map[string]any) (*ACPControlPrompt, bool)
	BuildACPServerResponse(prompt *ACPControlPrompt, approved bool, answer string) (result any, err error)
	ACPRewindMethods() (listMethod, execMethod, cancelMethod string, supported bool)
}

type CommandCatalog interface {
	CollectCommands(homeDir string) []CommandSuggestion
}

type SubagentBlockInspector interface {
	InspectSubagentBlock(homeDir, subID string, blockedSince time.Time) (blocked *Blocked, activity string, ok bool)
}
