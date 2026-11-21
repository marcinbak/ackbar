package agents

import (
	"testing"

	"ackbar/internal/agent"
)

func TestDefaultRegistry_UnconditionalBuiltinPlugins(t *testing.T) {
	reg := agent.NewRegistry()
	if err := RegisterDefaultsFromEnv(reg, ""); err != nil {
		t.Fatalf("RegisterDefaultsFromEnv empty failed: %v", err)
	}
	for _, want := range []string{"grok", "codex", "opencode", "antigravity", "agy"} {
		if _, ok := reg.Get(want); !ok {
			t.Errorf("expected %q to be registered in default registry", want)
		}
	}

	for _, envVal := range []string{"antigravity", "agy", "codex,AGY", "all", "*"} {
		legacyReg := agent.NewRegistry()
		if err := RegisterDefaultsFromEnv(legacyReg, envVal); err != nil {
			t.Fatalf("RegisterDefaultsFromEnv(%q) failed: %v", envVal, err)
		}
		for _, want := range []string{"grok", "codex", "opencode", "antigravity", "agy"} {
			if _, ok := legacyReg.Get(want); !ok {
				t.Errorf("expected %q to remain registered when %s=%q", want, agent.LegacyAgentsEnvVar, envVal)
			}
		}
	}

	t.Setenv(agent.LegacyAgentsEnvVar, "antigravity")
	envReg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}
	for _, want := range []string{"antigravity", "agy"} {
		if _, ok := envReg.Get(want); !ok {
			t.Errorf("expected NewDefaultRegistry to keep %q registered when ACKBAR_LEGACY_AGENTS=antigravity", want)
		}
	}
}
