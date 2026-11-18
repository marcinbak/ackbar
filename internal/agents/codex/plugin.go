package codex

import (
	"os"
	"path/filepath"
	"strings"
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
}

var (
	_ agent.Plugin               = (*Plugin)(nil)
	_ agent.Branding             = (*Plugin)(nil)
	_ agent.InstallationDetector = (*Plugin)(nil)
	_ agent.Provisioner          = (*Plugin)(nil)
	_ agent.Accounts             = (*Plugin)(nil)
	_ agent.TMuxDriver           = (*Plugin)(nil)
	_ agent.Hooks                = (*Plugin)(nil)
	_ agent.Transcripts          = (*Plugin)(nil)
	_ agent.Scanner              = (*Plugin)(nil)
	_ agent.RateLimiter          = (*Plugin)(nil)
	_ agent.TranscriptStatter    = (*Plugin)(nil)
)

func New() *Plugin {
	return &Plugin{}
}

func (*Plugin) Key() string           { return "codex" }
func (*Plugin) DisplayName() string   { return "OpenAI Codex" }
func (*Plugin) BinaryNames() []string { return []string{"codex"} }
func (*Plugin) BrandColor() string    { return "#10A37F" }

func (*Plugin) IconSVG() string {
	return `<svg class="agent-logo-svg codex-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a4.5 4.5 0 0 1 4.2 2.9l.4.9a4.5 4.5 0 0 1 1.8 4.7l-.2 1a4.5 4.5 0 0 1-2.4 4.1l-.8.5a4.5 4.5 0 0 1-4.7 1.8l-1-.2a4.5 4.5 0 0 1-4.1-2.4l-.5-.8a4.5 4.5 0 0 1-1.8-4.7l.2-1a4.5 4.5 0 0 1 2.4-4.1l.8-.5A4.5 4.5 0 0 1 12 2z"/><path d="M12 6.5l3.5 2v4l-3.5 2l-3.5-2v-4z"/><path d="M8.5 8.5L5 10.5M15.5 8.5L19 10.5M12 14.5V18.5"/></svg>`
}

func (*Plugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		SupportsTmux:         true,
		NativeIDStrategy:     agent.NativeIDLazyAdopt,
		SupportsHookShim:     true,
		SupportsMultiAccount: true,
		SupportsTranscript:   true,
	}
}

func HomeDir(home string) string {
	realHome := agentutil.RealUserHome()
	if envHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); envHome != "" && (home == "" || filepath.Clean(home) == filepath.Clean(realHome)) {
		return envHome
	}
	if home == "" {
		home = realHome
	}
	return filepath.Join(home, ".codex")
}

func (*Plugin) DetectRateLimit(text string, now time.Time) (bool, time.Time, string) {
	return agentutil.DetectDefaultRateLimitAt(text, now)
}
