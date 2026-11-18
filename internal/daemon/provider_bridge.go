package daemon

import (
	"context"
	"fmt"
	"os"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type PluginBridge struct {
	plugin   agent.Plugin
	registry *agent.Registry
	server   *Server
}

var (
	_ Provider           = (*PluginBridge)(nil)
	_ FullProvider       = (*PluginBridge)(nil)
	_ SubagentDiscoverer = (*PluginBridge)(nil)
	_ StatusInspector    = (*PluginBridge)(nil)
	_ AgentAdapter       = (*PluginBridge)(nil)
)

func NewPluginBridge(p agent.Plugin, reg *agent.Registry, s *Server) *PluginBridge {
	return &PluginBridge{plugin: p, registry: reg, server: s}
}

func (b *PluginBridge) Plugin() agent.Plugin { return b.plugin }

func (b *PluginBridge) fallback() defaultAgentAdapter {
	return defaultAgentAdapter{agent: b.plugin.Key()}
}

func (b *PluginBridge) installationDetector() (agent.InstallationDetector, bool) {
	if b.registry != nil {
		return b.registry.AsInstallationDetector(b.plugin.Key())
	}
	f, ok := b.plugin.(agent.InstallationDetector)
	return f, ok
}

func (b *PluginBridge) provisioner() (agent.Provisioner, bool) {
	if b.registry != nil {
		return b.registry.AsProvisioner(b.plugin.Key())
	}
	f, ok := b.plugin.(agent.Provisioner)
	return f, ok
}

func (b *PluginBridge) accounts() (agent.Accounts, bool) {
	if b.registry != nil {
		return b.registry.AsAccounts(b.plugin.Key())
	}
	f, ok := b.plugin.(agent.Accounts)
	return f, ok
}

func (b *PluginBridge) tmuxDriver() (agent.TMuxDriver, bool) {
	if b.registry != nil {
		return b.registry.AsTMuxDriver(b.plugin.Key())
	}
	f, ok := b.plugin.(agent.TMuxDriver)
	return f, ok
}

func (b *PluginBridge) hooks() (agent.Hooks, bool) {
	if b.registry != nil {
		return b.registry.AsHooks(b.plugin.Key())
	}
	f, ok := b.plugin.(agent.Hooks)
	return f, ok
}

func (b *PluginBridge) transcripts() (agent.Transcripts, bool) {
	if b.registry != nil {
		return b.registry.AsTranscript(b.plugin.Key())
	}
	f, ok := b.plugin.(agent.Transcripts)
	return f, ok
}

func (b *PluginBridge) scanner() (agent.Scanner, bool) {
	if b.registry != nil {
		return b.registry.AsScanner(b.plugin.Key())
	}
	f, ok := b.plugin.(agent.Scanner)
	return f, ok
}

func (b *PluginBridge) rateLimiter() (agent.RateLimiter, bool) {
	if b.registry != nil {
		return b.registry.AsRateLimiter(b.plugin.Key())
	}
	f, ok := b.plugin.(agent.RateLimiter)
	return f, ok
}

func (b *PluginBridge) Agent() string          { return b.plugin.Key() }
func (b *PluginBridge) DisplayName() string    { return b.plugin.DisplayName() }
func (b *PluginBridge) ProcessNames() []string { return b.plugin.BinaryNames() }

func (b *PluginBridge) BrandColor() string {
	if v, ok := b.plugin.(agent.Branding); ok {
		return v.BrandColor()
	}
	return ""
}

func (b *PluginBridge) IconSVG() string {
	if v, ok := b.plugin.(agent.Branding); ok {
		return v.IconSVG()
	}
	return ""
}

func (b *PluginBridge) IsInstalled() bool {
	if det, ok := b.installationDetector(); ok {
		return det.IsInstalled(b.server.effectiveHomeDir())
	}
	for _, bin := range b.plugin.BinaryNames() {
		if _, err := agentutil.LookPathInStandardDirs(bin); err == nil {
			return true
		}
	}
	return false
}

func (b *PluginBridge) CheckHookConfig() (bool, string, error) {
	if h, ok := b.hooks(); ok {
		home, _ := os.UserHomeDir()
		return h.CheckHookConfig(home)
	}
	return false, "", nil
}

func (b *PluginBridge) ParseHook(eventName string, payload []byte) (*Event, error) {
	if h, ok := b.hooks(); ok {
		return h.ParseHook(eventName, payload)
	}
	return nil, fmt.Errorf("plugin %q does not implement Hooks", b.plugin.Key())
}

func (b *PluginBridge) ResolveSubagentParent(home, nativeID string) (string, string, string) {
	if h, ok := b.hooks(); ok {
		return h.ResolveSubagentParent(home, nativeID)
	}
	return "", "", ""
}

func (b *PluginBridge) BuildLaunchSpec(in agent.LaunchInput) (agent.LaunchSpec, error) {
	if td, ok := b.tmuxDriver(); ok {
		return td.BuildLaunchSpec(in)
	}
	return agent.LaunchSpec{}, fmt.Errorf("plugin %q does not implement TMuxDriver", b.plugin.Key())
}

func (b *PluginBridge) GetSpawnCommand(tempUUID string) string {
	if td, ok := b.tmuxDriver(); ok {
		return td.SpawnCommand(tempUUID)
	}
	return ""
}

func (b *PluginBridge) GetResumeCommand(nativeID string) string {
	if td, ok := b.tmuxDriver(); ok {
		return td.ResumeCommand(nativeID)
	}
	return ""
}

func (b *PluginBridge) ReadSessionMetadata(cwd, nativeID string) *SessionMeta {
	if tr, ok := b.transcripts(); ok {
		return tr.ReadSessionMetadata(b.server.effectiveHomeDir(), cwd, nativeID)
	}
	return nil
}

func (b *PluginBridge) ResolveSessionTitle(cwd, nativeID string) string {
	if tr, ok := b.transcripts(); ok {
		return tr.ResolveSessionTitle(b.server.effectiveHomeDir(), cwd, nativeID)
	}
	return ""
}

func (b *PluginBridge) ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error) {
	if tr, ok := b.transcripts(); ok {
		t, err := tr.ExtractTranscript(home, cwd, nativeID)
		if err != nil || t == nil {
			return nil, err
		}
		return t.Messages, nil
	}
	return nil, nil
}

func (b *PluginBridge) CleanSessionFiles(home, cwd, nativeID string) error {
	if tr, ok := b.transcripts(); ok {
		return tr.CleanSessionFiles(home, cwd, nativeID)
	}
	return nil
}

func (b *PluginBridge) InspectStatus(ctx context.Context, sess *Session) bool {
	if td, ok := b.tmuxDriver(); ok {
		return td.InspectStatus(ctx, sess)
	}
	return false
}

func (b *PluginBridge) SupportsSubagentDiscovery() bool {
	if !b.plugin.Capabilities().SupportsSubagents {
		return false
	}
	_, ok := b.transcripts()
	return ok
}

func (b *PluginBridge) ListSubagents(home, cwd, nativeID string) ([]*ActiveSubagent, error) {
	tr, ok := b.transcripts()
	if !ok {
		return nil, nil
	}
	active, err := tr.ListActiveSubagents(home, cwd, nativeID)
	if err != nil {
		return nil, err
	}
	if len(active) > 0 {
		return active, nil
	}
	subs, err := tr.ListSubagents(home, cwd, nativeID)
	if err != nil || len(subs) == 0 {
		return nil, err
	}
	out := make([]*ActiveSubagent, 0, len(subs))
	for _, s := range subs {
		out = append(out, &ActiveSubagent{
			ID: s.ID, Name: s.Name, Role: s.Role,
			Prompt: s.Prompt, State: s.State, StartedAt: s.StartedAt,
		})
	}
	return out, nil
}
