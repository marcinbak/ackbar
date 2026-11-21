package agents

import (
	"os"

	"ackbar/internal/agent"
	"ackbar/internal/agents/antigravity"
	"ackbar/internal/agents/codex"
	"ackbar/internal/agents/grok"
	"ackbar/internal/agents/opencode"
)

func DefaultPluginsFromEnv(rawLegacyEnv string) []agent.Plugin {
	_ = rawLegacyEnv
	return []agent.Plugin{
		grok.New(),
		codex.New(),
		opencode.New(),
		antigravity.New(),
	}
}

func RegisterDefaultsFromEnv(reg *agent.Registry, rawLegacyEnv string) error {
	if reg == nil {
		return nil
	}
	for _, p := range DefaultPluginsFromEnv(rawLegacyEnv) {
		if err := reg.Register(p); err != nil {
			return err
		}
	}
	return nil
}

func DefaultPlugins() []agent.Plugin {
	return DefaultPluginsFromEnv(os.Getenv(agent.LegacyAgentsEnvVar))
}

func RegisterDefaults(reg *agent.Registry) error {
	return RegisterDefaultsFromEnv(reg, os.Getenv(agent.LegacyAgentsEnvVar))
}

func NewDefaultRegistry() (*agent.Registry, error) {
	reg := agent.NewRegistry()
	if err := RegisterDefaults(reg); err != nil {
		return nil, err
	}
	return reg, nil
}
