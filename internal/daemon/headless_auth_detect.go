package daemon

import (
	"fmt"
	"time"

	"ackbar/internal/agentutil"
)

var validServerNameRegex = agentutil.ValidServerNameRegex

func DetectToolResultAuthBlock(agent, toolName string, toolInput map[string]any, toolOutput string, isError bool) *Blocked {
	return agentutil.DetectToolResultAuthBlock(agent, toolName, toolInput, toolOutput, isError)
}

func DetectTurnAuthBlock(agent, text string) *Blocked {
	return agentutil.DetectTurnAuthBlock(agent, text)
}

func detectTurnFailureBlock(agent, text string) *Blocked {
	if b := DetectTurnAuthBlock(agent, text); b != nil {
		return b
	}
	adapter := getDefaultAgentAdapter(agent)
	if adapter != nil {
		if limited, resetAt, reason := adapter.DetectRateLimit(text, time.Now()); limited {
			return &Blocked{
				Kind:     BlockRateLimit,
				Reason:   reason,
				Question: reason,
				ResetAt:  &resetAt,
				Options:  []string{fmt.Sprintf("Resume at %s", resetAt.Local().Format("15:04")), "Resume in 1h"},
				Since:    time.Now(),
			}
		}
	}
	return nil
}
