package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BasePlugin implements only Identity/Branding defaults so optional facet type
// assertions fail unless the concrete plugin embeds a facet base or implements it.
type BasePlugin struct{}

func (BasePlugin) Aliases() []string     { return nil }
func (BasePlugin) BinaryNames() []string { return nil }
func (BasePlugin) BrandColor() string    { return "" }
func (BasePlugin) IconSVG() string       { return "" }

type BaseInstallationDetector struct{}

func (BaseInstallationDetector) IsInstalled(homeDir string) bool { return false }

type BaseProvisioner struct {
	BaseInstallationDetector
}

func (BaseProvisioner) CheckStatus(ctx context.Context, homeDir string) AgentStatus {
	return AgentStatus{}
}
func (BaseProvisioner) InstallHooks(homeDir string) error                   { return nil }
func (BaseProvisioner) Provision(ctx context.Context, homeDir string) error { return nil }

type BaseAccounts struct{}

func (BaseAccounts) APIKeyEnvVar() string { return "" }

func (BaseAccounts) AccountEnvVars(homeDir string, acct *AgentAccount) map[string]string {
	if acct == nil {
		return nil
	}
	envVars := make(map[string]string, len(acct.Env)+1)
	for k, v := range acct.Env {
		envVars[k] = v
	}
	if acct.Name != "" {
		envVars["ACKBAR_ACCOUNT"] = acct.Name
	}
	return envVars
}

func (BaseAccounts) IsAccountLoggedIn(homeDir string, acct *AgentAccount) bool { return true }
func (BaseAccounts) SetupProfileDir(homeDir, name, configDir string) (string, error) {
	return configDir, nil
}
func (BaseAccounts) LoginCommand() string                              { return "" }
func (BaseAccounts) CaptureCredentials(homeDir string) (string, error) { return "", nil }
func (BaseAccounts) ActivateAccount(homeDir string, acct *AgentAccount) error {
	return nil
}
func (BaseAccounts) RefreshAccount(ctx context.Context, acct *AgentAccount) (bool, error) {
	return false, nil
}
func (BaseAccounts) AuthenticateAccount(ctx context.Context, label string, env []string) (*AgentAccount, error) {
	return nil, fmt.Errorf("account authentication not supported")
}

type BaseTMuxDriver struct{}

func (BaseTMuxDriver) BuildLaunchSpec(in LaunchInput) (LaunchSpec, error) {
	return LaunchSpec{}, fmt.Errorf("launch spec not implemented")
}
func (BaseTMuxDriver) SpawnCommand(tempUUID string) string  { return "" }
func (BaseTMuxDriver) ResumeCommand(nativeID string) string { return "" }
func (BaseTMuxDriver) ResetCommand() string                 { return "/clear" }
func (BaseTMuxDriver) ApprovalKeys(allow bool) []string {
	if allow {
		return []string{"y", "Enter"}
	}
	return []string{"n", "Enter"}
}
func (BaseTMuxDriver) InspectStatus(ctx context.Context, sess *Session) bool { return false }

type BaseHooks struct{}

func (BaseHooks) CheckHookConfig(homeDir string) (bool, string, error) { return false, "", nil }
func (BaseHooks) ParseHook(eventName string, payload []byte) (*Event, error) {
	return nil, fmt.Errorf("hooks not implemented")
}
func (BaseHooks) HookAckPayload(eventName string) []byte { return []byte("{}") }
func (BaseHooks) ResolveSubagentParent(homeDir, nativeID string) (string, string, string) {
	return "", "", ""
}

type BaseTranscripts struct{}

func (BaseTranscripts) ReadSessionMetadata(homeDir, cwd, nativeID string) *SessionMeta { return nil }
func (BaseTranscripts) ResolveSessionTitle(homeDir, cwd, nativeID string) string       { return "" }
func (BaseTranscripts) ExtractTranscript(homeDir, cwd, nativeID string) (*Transcript, error) {
	return nil, nil
}
func (BaseTranscripts) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	clean := strings.TrimSpace(content)
	if clean == "" {
		return "", false
	}
	return clean, true
}
func (BaseTranscripts) ListSubagents(homeDir, cwd, nativeID string) ([]SubagentInfo, error) {
	return nil, nil
}
func (BaseTranscripts) ListActiveSubagents(homeDir, cwd, nativeID string) ([]*ActiveSubagent, error) {
	return nil, nil
}
func (BaseTranscripts) IsSubagentSession(homeDir, nativeID string, precomputed map[string]bool) bool {
	return false
}
func (BaseTranscripts) HasConversationArtifacts(homeDir, nativeID string) bool { return false }
func (BaseTranscripts) CleanSessionFiles(homeDir, cwd, nativeID string) error  { return nil }
func (BaseTranscripts) ListWorkspaceDocuments(homeDir, cwd, nativeID string) []WorkspaceDoc {
	return nil
}

type BaseScanner struct{}

func (BaseScanner) IsValidNativeID(nativeID string) bool               { return nativeID != "" }
func (BaseScanner) IsIgnoredCommand(fullCmd string) bool               { return false }
func (BaseScanner) MatchProcessCommand(binLower, cmdLower string) bool { return false }
func (BaseScanner) RequiresResolvedSessionID() bool                    { return false }
func (BaseScanner) ResolveSessionForPID(ctx context.Context, homeDir string, pid int, cmdFields []string) string {
	return ""
}
func (BaseScanner) ResolveSessionOwner(homeDir, sessionID string) (int, string) { return 0, "" }
func (BaseScanner) ScanDisk(ctx context.Context, in DiskScanInput) DiskScanResult {
	return DiskScanResult{}
}

type BaseModelCatalog struct{}

func (BaseModelCatalog) DefaultModels() []ModelSuggestion               { return nil }
func (BaseModelCatalog) CollectModels(homeDir string) []ModelSuggestion { return nil }
func (BaseModelCatalog) ValidateModelID(model string) bool              { return strings.TrimSpace(model) != "" }

type BaseRateLimiter struct{}

func (BaseRateLimiter) DetectRateLimit(text string, now time.Time) (bool, time.Time, string) {
	return false, time.Time{}, ""
}
