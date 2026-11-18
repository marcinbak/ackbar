package grok

import (
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type Plugin struct {
	agent.BasePlugin
	agent.BaseProvisioner
	agent.BaseAccounts
	agent.BaseTMuxDriver
	agent.BaseHooks
	agent.BaseTranscripts
	agent.BaseScanner
	agent.BaseModelCatalog
}

var (
	_ agent.Plugin                   = (*Plugin)(nil)
	_ agent.Branding                 = (*Plugin)(nil)
	_ agent.InstallationDetector     = (*Plugin)(nil)
	_ agent.Provisioner              = (*Plugin)(nil)
	_ agent.Accounts                 = (*Plugin)(nil)
	_ agent.TMuxDriver               = (*Plugin)(nil)
	_ agent.HeadlessDriver           = (*Plugin)(nil)
	_ agent.Hooks                    = (*Plugin)(nil)
	_ agent.Transcripts              = (*Plugin)(nil)
	_ agent.Scanner                  = (*Plugin)(nil)
	_ agent.ModelCatalog             = (*Plugin)(nil)
	_ agent.RateLimiter              = (*Plugin)(nil)
	_ agent.StreamParserFactory      = (*Plugin)(nil)
	_ agent.ModelResolver            = (*Plugin)(nil)
	_ agent.TranscriptStatter        = (*Plugin)(nil)
	_ agent.TranscriptUsageInspector = (*Plugin)(nil)
	_ agent.CLIInspector             = (*Plugin)(nil)
	_ agent.ACPDriver                = (*Plugin)(nil)
	_ agent.CommandCatalog           = (*Plugin)(nil)
)

func New() *Plugin {
	return &Plugin{}
}

func (*Plugin) Key() string           { return "grok" }
func (*Plugin) DisplayName() string   { return "xAI Grok" }
func (*Plugin) Aliases() []string     { return []string{"grok-cli"} }
func (*Plugin) BinaryNames() []string { return []string{"grok", "grok-cli"} }
func (*Plugin) BrandColor() string    { return "#E5E7EB" }

func (*Plugin) IconSVG() string {
	return `<svg class="agent-logo-svg grok-logo" viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/></svg>`
}

func (*Plugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		SupportsTmux:             true,
		SupportsHeadless:         true,
		HeadlessMode:             agent.HeadlessModeACP,
		NativeIDStrategy:         agent.NativeIDEagerUUID,
		SupportsNativeHooks:      true,
		SupportsHookShim:         true,
		SupportsMultiAccount:     true,
		SupportsTranscript:       true,
		SupportsSubagents:        true,
		SupportsModelSelection:   true,
		SupportsEffortLevel:      true,
		SupportsYoloMode:         true,
		SupportsSystemPrompt:     true,
		SupportsExtraDirs:        true,
		LocksExtraDirsOnResume:   true,
		SupportedPermissionModes: []string{"bypassPermissions", "auto", "acceptEdits", "dontAsk", "plan"},
	}
}

func (*Plugin) DetectRateLimit(text string, now time.Time) (bool, time.Time, string) {
	return agentutil.DetectDefaultRateLimitAt(text, now)
}
