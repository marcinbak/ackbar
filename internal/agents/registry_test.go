package agents

import (
	"testing"

	"ackbar/internal/agent"
)

func TestDefaultRegistry_HonorLegacyEnv(t *testing.T) {
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
		if _, ok := legacyReg.Get("antigravity"); ok {
			t.Errorf("expected antigravity omitted when %s=%q", agent.LegacyAgentsEnvVar, envVal)
		}
		if _, ok := legacyReg.Get("agy"); ok {
			t.Errorf("expected agy alias omitted when %s=%q", agent.LegacyAgentsEnvVar, envVal)
		}
		if _, ok := legacyReg.Get("grok"); !ok {
			t.Errorf("expected grok to remain registered when %s=%q", agent.LegacyAgentsEnvVar, envVal)
		}
	}

	t.Setenv(agent.LegacyAgentsEnvVar, "antigravity")
	envReg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}
	if _, ok := envReg.Get("antigravity"); ok {
		t.Error("expected NewDefaultRegistry to omit antigravity when ACKBAR_LEGACY_AGENTS=antigravity")
	}
}
