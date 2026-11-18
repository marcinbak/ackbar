package daemon

import (
	"fmt"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

var validServerNameRegex = agentutil.ValidServerNameRegex

func DetectToolResultAuthBlock(agent, toolName string, toolInput map[string]any, toolOutput string, isError bool) *Blocked {
	return agentutil.DetectToolResultAuthBlock(agent, toolName, toolInput, toolOutput, isError)
}

func DetectTurnAuthBlock(agent, text string) *Blocked {
	return agentutil.DetectTurnAuthBlock(agent, text)
}

func detectTurnFailureBlock(agentName, text string) *Blocked {
	return detectTurnFailureBlockWithRegistry(nil, agentName, text)
}

func detectTurnFailureBlockWithRegistry(reg *agent.Registry, agentName, text string) *Blocked {
	if b := DetectTurnAuthBlock(agentName, text); b != nil {
		return b
	}
	if reg != nil {
		if rl, ok := reg.AsRateLimiter(agentName); ok {
			if limited, resetAt, reason := rl.DetectRateLimit(text, time.Now()); limited {
				return newRateLimitBlocked(resetAt, reason)
			}
		}
	}
	if adapter := getDefaultAgentAdapter(agentName); adapter != nil {
		if limited, resetAt, reason := adapter.DetectRateLimit(text, time.Now()); limited {
			return newRateLimitBlocked(resetAt, reason)
		}
	}
	return nil
}

func newRateLimitBlocked(resetAt time.Time, reason string) *Blocked {
	return &Blocked{
		Kind:     BlockRateLimit,
		Reason:   reason,
		Question: reason,
		ResetAt:  &resetAt,
		Options:  []string{fmt.Sprintf("Resume at %s", resetAt.Local().Format("15:04")), "Resume in 1h"},
		Since:    time.Now(),
	}
}
