package agent

import "testing"

func TestIsLegacyAgentFromEnv(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		key     string
		aliases []string
		want    bool
	}{
		{name: "empty", raw: "", key: "antigravity", aliases: []string{"agy"}, want: false},
		{name: "exact_key", raw: "antigravity", key: "antigravity", aliases: []string{"agy"}, want: true},
		{name: "case_alias", raw: " AGY ", key: "antigravity", aliases: []string{"agy"}, want: true},
		{name: "csv_match", raw: "codex, antigravity ,grok", key: "antigravity", aliases: []string{"agy"}, want: true},
		{name: "all_token", raw: "ALL", key: "antigravity", want: true},
		{name: "star_token", raw: "codex, * ", key: "antigravity", want: true},
		{name: "other_agent", raw: "codex,grok", key: "antigravity", aliases: []string{"agy"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLegacyAgentFromEnv(tc.raw, tc.key, tc.aliases...); got != tc.want {
				t.Fatalf("IsLegacyAgentFromEnv(%q, %q, %v) = %v, want %v", tc.raw, tc.key, tc.aliases, got, tc.want)
			}
		})
	}
}

func TestIsLegacyAgentEnvVar(t *testing.T) {
	t.Setenv(LegacyAgentsEnvVar, "agy")
	if !IsLegacyAgent("antigravity", "agy") {
		t.Fatal("expected IsLegacyAgent to detect ACKBAR_LEGACY_AGENTS=agy")
	}
	t.Setenv(LegacyAgentsEnvVar, "codex")
	if IsLegacyAgent("antigravity", "agy") {
		t.Fatal("expected IsLegacyAgent to return false when antigravity is not listed")
	}
}
