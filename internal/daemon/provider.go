package daemon

import (
	"context"
	"strings"
	"time"

	"ackbar/internal/agent"
)

type SessionMeta = agent.SessionMeta
type ActiveSubagent = agent.ActiveSubagent

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

// InteractiveController provides agent-specific interactive terminal commands and keystrokes.
type InteractiveController interface {
	GetResetCommand() string
	GetApprovalKeys(allow bool) []string
}

// AccountConfigurator provides agent-specific multi-account profile setup, login, and environment injection.
type AccountConfigurator interface {
	AccountEnvVars(home string, acc *AgentAccount) map[string]string
	IsAccountLoggedIn(home string, acc *AgentAccount) bool
	SetupProfileDir(home, name, configDir string) (string, error)
	GetLoginCommand() string
}

// ProcessInspector provides agent-specific process matching, command filtering, and PID-to-session resolution.
type ProcessInspector interface {
	IsValidNativeID(nativeID string) bool
	IsIgnoredCommand(fullCmd string) bool
	MatchProcessCommand(binLower, cmdLower string) bool
	RequiresResolvedSessionID() bool
	ResolveSessionForPID(ctx context.Context, home string, pid int, cmdFields []string) string
	ResolveSessionOwner(home, sessionID string) (pid int, tmuxName string)
}

// DiskSessionDiscoverer provides agent-specific disk artifact checks, workspace doc listing, and past session discovery.
type DiskSessionDiscoverer interface {
	HasConversationArtifacts(home, nativeID string) bool
	IsSubagentSession(home, nativeID string, precomputed map[string]bool) bool
	ListWorkspaceDocuments(home, cwd, nativeID string, addDoc func(fullPath, title, relPath, category, categoryLabel string, priority int))
	ScanDiskSessions(ctx context.Context, s *Server, home string, state *ScanState)
}

// UserEntryFilter filters synthetic runtime entries and normalizes user prompt text for a specific agent.
type UserEntryFilter interface {
	FilterUserEntry(raw map[string]interface{}, content string) (clean string, keep bool)
}

// RateLimitDetector detects agent-specific rate, usage, or quota limit errors.
type RateLimitDetector interface {
	DetectRateLimit(text string, now time.Time) (limited bool, resetAt time.Time, reason string)
}

// AgentAdapter combines all agent-specific operational capabilities used by the daemon server and session scanner.
type AgentAdapter interface {
	InteractiveController
	AccountConfigurator
	ProcessInspector
	DiskSessionDiscoverer
	UserEntryFilter
	RateLimitDetector
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

type ProviderDTO = agent.ProviderDTO

func getDefaultAgentAdapter(agent string) AgentAdapter {
	switch agent {
	case "claude-code":
		return &ClaudeAgentAdapter{}
	case "antigravity":
		return &AntigravityAgentAdapter{}
	case "codex":
		return &CodexAgentAdapter{}
	case "grok", "grok-cli":
		return &GrokAgentAdapter{}
	case "opencode", "open-code":
		return &OpenCodeAgentAdapter{}
	default:
		lower := strings.ToLower(agent)
		if strings.Contains(lower, "antigravity") || strings.Contains(lower, "agy") {
			return &AntigravityAgentAdapter{}
		}
		return &defaultAgentAdapter{agent: agent}
	}
}

func (s *Server) resolveProvider(agentName string) (Provider, bool) {
	if s == nil {
		return nil, false
	}
	s.registryMu.RLock()
	defer s.registryMu.RUnlock()
	if p, ok := s.providers[agentName]; ok {
		return p, true
	}
	if s.registry != nil {
		if plugin, ok := s.registry.Get(agentName); ok {
			if p, ok := s.providers[plugin.Key()]; ok {
				return p, true
			}
		}
	}
	return nil, false
}

func (s *Server) agentAdapter(agent string) AgentAdapter {
	if p, ok := s.resolveProvider(agent); ok {
		if adapter, ok := p.(AgentAdapter); ok {
			return adapter
		}
	}
	return getDefaultAgentAdapter(agent)
}
