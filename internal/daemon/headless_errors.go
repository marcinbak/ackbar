package daemon

import (
	"fmt"
	"strings"
)

type turnErrorTracker struct {
	pending []string
}

func (t *turnErrorTracker) hasPending() bool {
	return len(t.pending) > 0
}

func (t *turnErrorTracker) add(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	for i, p := range t.pending {
		if strings.Contains(p, msg) {
			return
		}
		if strings.Contains(msg, p) {
			t.pending[i] = msg
			var pruned []string
			for j, item := range t.pending {
				if j == i || !strings.Contains(msg, item) {
					pruned = append(pruned, item)
				}
			}
			t.pending = pruned
			return
		}
	}
	t.pending = append(t.pending, msg)
}

func (t *turnErrorTracker) clear() {
	t.pending = nil
}

func (t *turnErrorTracker) resolveFinal(finalErr string) string {
	t.add(finalErr)
	if len(t.pending) == 0 {
		return "Turn failed"
	}
	joined := strings.Join(t.pending, "\n")
	t.pending = nil
	return joined
}

func (t *turnErrorTracker) flushRemaining() (string, bool) {
	if len(t.pending) == 0 {
		return "", false
	}
	joined := strings.Join(t.pending, "\n")
	t.pending = nil
	return joined, true
}

func extractClaudeStreamError(raw map[string]interface{}) string {
	if raw == nil {
		return ""
	}
	if errVal, ok := raw["error"]; ok && errVal != nil {
		switch v := errVal.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case map[string]interface{}:
			for _, key := range []string{"message", "error"} {
				if msg, ok := v[key].(string); ok {
					if s := strings.TrimSpace(msg); s != "" {
						return s
					}
				}
			}
		}
	}
	if msgVal, ok := raw["message"].(string); ok {
		return strings.TrimSpace(msgVal)
	}
	return ""
}

func formatFailedTurnActivity(meta *TurnSpoolMeta, snap *InFlightTurnSnapshot) string {
	if snap != nil && strings.TrimSpace(snap.LastErrorText) != "" {
		return "Turn error: " + compactDetail(snap.LastErrorText)
	}
	if meta != nil && strings.TrimSpace(meta.StderrTail) != "" {
		return "Turn error: " + compactDetail(meta.StderrTail)
	}
	if meta != nil && meta.ExitCode != 0 {
		return fmt.Sprintf("Turn failed (exit status %d)", meta.ExitCode)
	}
	return "Turn failed"
}
