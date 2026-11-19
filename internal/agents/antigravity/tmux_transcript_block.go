package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type transcriptStep struct {
	StepIndex int    `json:"step_index"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Name string                 `json:"name"`
		Args map[string]interface{} `json:"args"`
	} `json:"tool_calls"`
	CreatedAt       string   `json:"created_at"`
	TruncatedFields []string `json:"truncated_fields"`
}

func hasTruncatedField(fields []string, target string) bool {
	for _, f := range fields {
		if strings.EqualFold(f, target) {
			return true
		}
	}
	return false
}

func isNonTurnStep(stepType string) bool {
	t := strings.TrimSpace(stepType)
	return t == "" || strings.EqualFold(t, "CHECKPOINT") || strings.EqualFold(t, "SYSTEM")
}

func parseStepTimestamp(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

func effectiveBlockedSince(sess *agent.Session) time.Time {
	if sess == nil {
		return time.Time{}
	}
	if sess.Blocked != nil && !sess.Blocked.Since.IsZero() {
		return sess.Blocked.Since
	}
	if !sess.UpdatedAt.IsZero() {
		return sess.UpdatedAt
	}
	if !sess.LastStateChangeAt.IsZero() {
		return sess.LastStateChangeAt
	}
	return time.Time{}
}

func isWithinBlockedGraceWindow(sess *agent.Session) bool {
	if sess == nil || sess.State != agent.StateBlocked || sess.Blocked == nil {
		return false
	}
	if !sess.UpdatedAt.IsZero() && time.Since(sess.UpdatedAt) < 15*time.Second {
		return true
	}
	if !sess.Blocked.Since.IsZero() && time.Since(sess.Blocked.Since) < 15*time.Second {
		return true
	}
	if !sess.LastStateChangeAt.IsZero() && time.Since(sess.LastStateChangeAt) < 15*time.Second {
		return true
	}
	return false
}

func applyBlocked(sess *agent.Session, blocked *agent.Blocked, activity string) bool {
	if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != blocked.Kind ||
		sess.Blocked.Question != blocked.Question || sess.Blocked.Reason != blocked.Reason {
		sess.State = agent.StateBlocked
		sess.Blocked = blocked
		sess.Activity = activity
		return true
	}
	return false
}

func resolveFullStep(logPath string, stepIndex int) (*transcriptStep, bool) {
	fullPath := filepath.Join(filepath.Dir(logPath), "transcript_full.jsonl")
	data, err := agentutil.ReadTail(fullPath, 256*1024)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	lines := strings.Split(string(data), "\n")
	var fallbackPlanner *transcriptStep
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var s transcriptStep
		if json.Unmarshal([]byte(line), &s) == nil {
			if stepIndex > 0 {
				if s.StepIndex == stepIndex {
					return &s, true
				}
				continue
			}
			if s.Type == "USER_INPUT" || s.Type == "TOOL_RESPONSE" {
				break
			}
			if fallbackPlanner == nil && s.Type == "PLANNER_RESPONSE" {
				sCopy := s
				fallbackPlanner = &sCopy
				break
			}
		}
	}
	if stepIndex <= 0 && fallbackPlanner != nil {
		return fallbackPlanner, true
	}
	return nil, false
}

func lastValidTranscriptIdx(tLines []string, logPath string, lastStep *transcriptStep, hasLastStep *bool) int {
	for i := len(tLines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(tLines[i])
		if line == "" {
			continue
		}
		var cand transcriptStep
		if json.Unmarshal([]byte(line), &cand) == nil {
			if isNonTurnStep(cand.Type) {
				continue
			}
			if hasTruncatedField(cand.TruncatedFields, "tool_calls") {
				if fullStep, ok := resolveFullStep(logPath, cand.StepIndex); ok {
					cand.ToolCalls = fullStep.ToolCalls
				}
			}
			*lastStep = cand
			*hasLastStep = true
			return i
		}
	}
	return -1
}

func applyTranscriptStepState(sess *agent.Session, lastStep *transcriptStep, activeLogPath string, hasIdlePrompt, hasSpinner bool, blockSince time.Time, turnCompletedAfterBlock *bool, blockAnswered bool) (shouldReturn bool, changed bool) {
	isPlannerConcluded := lastStep.Type == "PLANNER_RESPONSE" && ((lastStep.Status == "DONE" && len(lastStep.ToolCalls) == 0) ||
		strings.EqualFold(lastStep.Status, "CANCELED") ||
		strings.EqualFold(lastStep.Status, "CANCELLED") ||
		strings.EqualFold(lastStep.Status, "ERROR"))

	if lastStep.Type == "USER_INPUT" {
		if sess.EngineType == agent.EngineHeadless && sess.TmuxName == "" {
			return false, false
		}
		stat, serr := os.Stat(activeLogPath)
		if (serr != nil || time.Since(stat.ModTime()) < 30*time.Minute) &&
			!(serr == nil && time.Since(stat.ModTime()) > 15*time.Second && hasIdlePrompt && !hasSpinner) {
			if isWithinBlockedGraceWindow(sess) && !*turnCompletedAfterBlock {
				return true, false
			}
			if sess.State != agent.StateWorking {
				sess.State = agent.StateWorking
				sess.Blocked = nil
				sess.Activity = "Thinking..."
				if sess.LastEventAt.IsZero() {
					sess.LastEventAt = time.Now()
				}
				return true, true
			}
			return true, false
		}
		return false, false
	}
	if isPlannerConcluded {
		lastStepTime := parseStepTimestamp(lastStep.CreatedAt)
		if !lastStepTime.IsZero() && (blockSince.IsZero() || !lastStepTime.Before(blockSince)) {
			*turnCompletedAfterBlock = true
		}
		if isWithinBlockedGraceWindow(sess) && !*turnCompletedAfterBlock {
			return true, false
		}
		if !hasSpinner {
			if sess.State != agent.StateIdle {
				sess.State = agent.StateIdle
				sess.Blocked = nil
				sess.Activity = "Awaiting user prompt"
				return true, true
			}
			return true, false
		}
		return false, false
	}
	if sess.State == agent.StateBlocked && blockAnswered &&
		(lastStep.Type == "TOOL_RESPONSE" || lastStep.Type == "ASK_QUESTION" || lastStep.Type == "PLANNER_RESPONSE" || lastStep.Type == "GENERIC") {
		sess.State = agent.StateWorking
		sess.Blocked = nil
		sess.Activity = "Working..."
		return true, true
	}
	return false, false
}
