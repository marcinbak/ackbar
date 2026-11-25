package claude

import (
	"ackbar/internal/agent"
)

type Plugin struct {
	agent.BaseAccounts
}

// StreamParserFactory is omitted because headless.go parseHeadlessStream owns Claude's stateful
// control_request/control_cancel_request/pendingTool stdio-control buffering and bgCtrl state machine.
var (
	_ agent.Identity                 = (*Plugin)(nil)
	_ agent.Plugin                   = (*Plugin)(nil)
	_ agent.Branding                 = (*Plugin)(nil)
	_ agent.InstallationDetector     = (*Plugin)(nil)
	_ agent.Provisioner              = (*Plugin)(nil)
	_ agent.Accounts                 = (*Plugin)(nil)
	_ agent.TMuxDriver               = (*Plugin)(nil)
	_ agent.HeadlessDriver           = (*Plugin)(nil)
	_ agent.PersistentHeadlessDriver = (*Plugin)(nil)
	_ agent.CLIInspector             = (*Plugin)(nil)
	_ agent.SessionRewinder          = (*Plugin)(nil)
	_ agent.Hooks                    = (*Plugin)(nil)
	_ agent.Transcripts              = (*Plugin)(nil)
	_ agent.IncrementalTranscripts   = (*Plugin)(nil)
	_ agent.TranscriptStatter        = (*Plugin)(nil)
	_ agent.TranscriptUsageInspector = (*Plugin)(nil)
	_ agent.Scanner                  = (*Plugin)(nil)
	_ agent.ModelCatalog             = (*Plugin)(nil)
	_ agent.ModelResolver            = (*Plugin)(nil)
	_ agent.WorkspaceModelResolver   = (*Plugin)(nil)
	_ agent.RateLimiter              = (*Plugin)(nil)
	_ agent.ChecklistExtractor       = (*Plugin)(nil)
)

func New() *Plugin { return &Plugin{} }

func (*Plugin) Key() string           { return "claude-code" }
func (*Plugin) DisplayName() string   { return "Claude Code" }
func (*Plugin) Aliases() []string     { return []string{"claude"} }
func (*Plugin) BinaryNames() []string { return []string{"claude", "@anthropic-ai/claude-code"} }
func (*Plugin) BrandColor() string    { return "#D97706" }
func (*Plugin) IconSVG() string {
	return `<svg class="agent-logo-svg claude-logo" viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><path clip-rule="evenodd" fill-rule="evenodd" d="M20.998 10.949H24v3.102h-3v3.028h-1.487V20H18v-2.921h-1.487V20H15v-2.921H9V20H7.488v-2.921H6V20H4.487v-2.921H3V14.05H0V10.95h3V5h17.998v5.949zM6 10.949h1.488V8.102H6v2.847zm10.51 0H18V8.102h-1.49v2.847z"/></svg>`
}

func (*Plugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		SupportsTmux:             true,
		SupportsHeadless:         true,
		HeadlessMode:             agent.HeadlessModePersistent,
		NativeIDStrategy:         agent.NativeIDEagerUUID,
		SupportsNativeHooks:      true,
		SupportsHookShim:         false,
		SupportsMultiAccount:     true,
		SupportsOAuth:            true,
		SupportsTranscript:       true,
		SupportsSubagents:        true,
		SupportsWorkspaceDocs:    true,
		SupportsModelSelection:   true,
		SupportsEffortLevel:      true,
		SupportsYoloMode:         true,
		SupportsSystemPrompt:     true,
		SupportsExtraDirs:        true,
		SupportedPermissionModes: []string{"bypassPermissions", "auto", "acceptEdits", "dontAsk", "plan"},
	}
}
