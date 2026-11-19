package agent

import (
	"os"
	"strings"
)

// LegacyAgentsEnvVar lists comma-separated agent keys or aliases routed to legacy adapters.
const LegacyAgentsEnvVar = "ACKBAR_LEGACY_AGENTS"

// IsLegacyAgentFromEnv reports whether rawEnv selects key, any alias, "all", or "*".
func IsLegacyAgentFromEnv(rawEnv string, key string, aliases ...string) bool {
	if rawEnv == "" {
		return false
	}
	for _, tok := range strings.Split(rawEnv, ",") {
		trimmed := strings.TrimSpace(tok)
		if trimmed == "" {
			continue
		}
		if strings.EqualFold(trimmed, "all") || trimmed == "*" {
			return true
		}
		if key != "" && strings.EqualFold(trimmed, key) {
			return true
		}
		for _, alias := range aliases {
			if alias != "" && strings.EqualFold(trimmed, alias) {
				return true
			}
		}
	}
	return false
}

// IsLegacyAgent reads LegacyAgentsEnvVar from the process environment.
func IsLegacyAgent(key string, aliases ...string) bool {
	return IsLegacyAgentFromEnv(os.Getenv(LegacyAgentsEnvVar), key, aliases...)
}
