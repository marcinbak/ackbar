package antigravity

import (
	"fmt"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type Plugin struct {
	agent.BaseAccounts
}

// Compile-time assertions; IncrementalTranscripts is omitted because StatTranscript sums transcript_full.jsonl and truncated steps require full hydration.
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
	_ agent.StreamParserFactory      = (*Plugin)(nil)
	_ agent.CLIInspector             = (*Plugin)(nil)
	_ agent.SessionRewinder          = (*Plugin)(nil)
	_ agent.Hooks                    = (*Plugin)(nil)
	_ agent.SubagentBlockInspector   = (*Plugin)(nil)
	_ agent.Transcripts              = (*Plugin)(nil)
	_ agent.TranscriptStatter        = (*Plugin)(nil)
	_ agent.TranscriptUsageInspector = (*Plugin)(nil)
	_ agent.Scanner                  = (*Plugin)(nil)
	_ agent.ModelCatalog             = (*Plugin)(nil)
	_ agent.ModelResolver            = (*Plugin)(nil)
	_ agent.RateLimiter              = (*Plugin)(nil)
)

func New() *Plugin { return &Plugin{} }

func (*Plugin) Key() string           { return "antigravity" }
func (*Plugin) DisplayName() string   { return "Google Antigravity" }
func (*Plugin) Aliases() []string     { return []string{"agy"} }
func (*Plugin) BinaryNames() []string { return []string{"antigravity", "agy", "bin/agy"} }
func (*Plugin) BrandColor() string    { return "#4285F4" }
func (*Plugin) IconSVG() string {
	return `<svg class="agent-logo-svg antigravity-logo" viewBox="0 0 24 24" width="12" height="12"><defs><linearGradient id="agGrad" x1="0%" y1="0%" x2="100%" y2="100%"><stop offset="0%" stop-color="#EA4335"/><stop offset="35%" stop-color="#FBBC04"/><stop offset="70%" stop-color="#34A853"/><stop offset="100%" stop-color="#4285F4"/></linearGradient></defs><path fill="url(#agGrad)" d="M12 0.8 C8.2 0.8 5.6 3.2 4.1 7.8 C2.6 12.5 1.2 18.2 0.3 22.4 C-0.1 23.6 0.8 24.2 1.6 23.6 C4.8 21.2 7.8 14.2 12 12.4 C16.2 14.2 19.2 21.2 22.4 23.6 C23.2 24.2 24.1 23.6 23.7 22.4 C22.8 18.2 21.4 12.5 19.9 7.8 C18.4 3.2 15.8 0.8 12 0.8 Z"/></svg>`
}

func (*Plugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		SupportsTmux:             true,
		SupportsHeadless:         true,
		HeadlessMode:             agent.HeadlessModePersistent,
		NativeIDStrategy:         agent.NativeIDLazyAdopt,
		SupportsHookShim:         true,
		SupportsMultiAccount:     true,
		SupportsOAuth:            true,
		SupportsTranscript:       true,
		SupportsSubagents:        true,
		SupportsWorkspaceDocs:    true,
		SupportsModelSelection:   true,
		SupportsEffortLevel:      false,
		SupportsYoloMode:         true,
		SupportsSystemPrompt:     true,
		SupportsExtraDirs:        true,
		LocksExtraDirsOnResume:   false,
		SupportedPermissionModes: []string{"bypassPermissions", "auto", "acceptEdits", "plan"},
	}
}

func (*Plugin) DetectRateLimit(text string, now time.Time) (bool, time.Time, string) {
	if text == "" {
		return false, time.Time{}, ""
	}
	lower := strings.ToLower(text)
	var prefix string
	switch {
	case strings.Contains(lower, "quota exceeded") || strings.Contains(lower, "exhausted your capacity"):
		prefix = "Quota exceeded"
	case strings.Contains(lower, "resourceexhausted") || strings.Contains(lower, "resource_exhausted"):
		prefix = "Resource exhausted"
	case strings.Contains(lower, "rate limit exceeded") || strings.Contains(lower, "rate limit reached") ||
		strings.Contains(lower, "rate_limit_exceeded") || strings.Contains(lower, "429 too many requests"):
		prefix = "Rate limit exceeded"
	case strings.Contains(lower, "usage limit reached"):
		prefix = "Usage limit reached"
	default:
		return false, time.Time{}, ""
	}
	resetAt, _ := agentutil.ParseRateLimitResetTime(text, now)
	return true, resetAt, fmt.Sprintf("%s · resets %s", prefix, resetAt.Local().Format("15:04"))
}
