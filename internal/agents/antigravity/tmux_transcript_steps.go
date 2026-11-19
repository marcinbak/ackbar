package antigravity

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (p *Plugin) InspectSubagentBlock(homeDir, subID string, blockedSince time.Time) (*agent.Blocked, string, bool) {
	home := effectiveHome(homeDir)
	if home == "" || !agentutil.IsSafeSessionID(subID) {
		return nil, "", false
	}
	cleanID := filepath.Base(filepath.Clean(subID))
	for _, bDir := range brainDirs(home) {
		subLogPath := filepath.Join(bDir, cleanID, ".system_generated", "logs", "transcript.jsonl")
		if blocked, activity, ok, _ := inspectTranscriptFileBlocked(subLogPath, blockedSince); ok {
			return blocked, activity, true
		}
	}
	return nil, "", false
}

func inspectTranscriptFileBlocked(logPath string, since time.Time) (blocked *agent.Blocked, activity string, blockedOk bool, blockAnswered bool) {
	data, err := agentutil.ReadTail(logPath, 256*1024)
	if err != nil || len(data) == 0 {
		return nil, "", false, false
	}
	tLines := strings.Split(string(data), "\n")
	lastValidIdx := -1
	for i := len(tLines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(tLines[i])
		if line == "" {
			continue
		}
		var cand transcriptStep
		if json.Unmarshal([]byte(line), &cand) == nil && !isNonTurnStep(cand.Type) {
			lastValidIdx = i
			break
		}
	}
	if lastValidIdx == -1 {
		return nil, "", false, false
	}
	return inspectTranscriptBlocked(logPath, tLines, lastValidIdx, since)
}

func inspectTranscriptBlocked(logPath string, tLines []string, lastValidIdx int, since time.Time) (blocked *agent.Blocked, activity string, blockedOk bool, blockAnswered bool) {
	stepsChecked := 0
	sawBoundary := false
	sawLaterNonBlocking := false
	var latestCompletion time.Time

	for i := lastValidIdx; i >= 0 && stepsChecked < 30; i-- {
		line := strings.TrimSpace(tLines[i])
		if line == "" {
			continue
		}
		var step transcriptStep
		if json.Unmarshal([]byte(line), &step) != nil || isNonTurnStep(step.Type) {
			continue
		}
		stepsChecked++
		stepTime := parseStepTimestamp(step.CreatedAt)

		if step.Type == "USER_INPUT" || step.Type == "ASK_QUESTION" || step.Type == "TOOL_RESPONSE" {
			sawBoundary = true
			if !stepTime.IsZero() && latestCompletion.IsZero() {
				latestCompletion = stepTime
			}
			continue
		}
		if hasTruncatedField(step.TruncatedFields, "tool_calls") {
			if fullStep, ok := resolveFullStep(logPath, step.StepIndex); ok {
				step.ToolCalls = fullStep.ToolCalls
			}
		}

		hasOtherTools, hasBlockingTool := false, false
		for _, tc := range step.ToolCalls {
			if tc.Name == "ask_question" || tc.Name == "ask_permission" || tc.Name == "ask_custom_permission" {
				hasBlockingTool = true
			} else if tc.Name != "" {
				hasOtherTools = true
			}
		}

		if sawBoundary || sawLaterNonBlocking {
			if hasBlockingTool {
				ansTime := latestCompletion
				if ansTime.IsZero() {
					ansTime = stepTime
				}
				if !since.IsZero() && !ansTime.IsZero() && ansTime.Before(since.Add(-500*time.Millisecond)) {
					return nil, "", false, false
				}
				return nil, "", false, true
			}
			if !stepTime.IsZero() && latestCompletion.IsZero() {
				latestCompletion = stepTime
			}
			continue
		}
		if hasOtherTools && !hasBlockingTool {
			sawLaterNonBlocking = true
			if !stepTime.IsZero() && latestCompletion.IsZero() {
				latestCompletion = stepTime
			}
			continue
		}
		if stepTime.IsZero() {
			stepTime = time.Now()
		}

		if b, act, ok := blockFromTranscriptStep(logPath, &step, stepTime); ok {
			return b, act, true, false
		}
	}
	return nil, "", false, false
}

func blockFromTranscriptStep(logPath string, step *transcriptStep, stepTime time.Time) (*agent.Blocked, string, bool) {
	for _, tc := range step.ToolCalls {
		if tc.Name == "ask_question" {
			q, opts := agentutil.ExtractQuestionAndOptions(tc.Args)
			if q == "" && len(opts) == 0 {
				if fullStep, ok := resolveFullStep(logPath, step.StepIndex); ok {
					for _, ftc := range fullStep.ToolCalls {
						if ftc.Name == "ask_question" {
							q, opts = agentutil.ExtractQuestionAndOptions(ftc.Args)
							break
						}
					}
				}
			}
			act := "Waiting for user response"
			if q != "" {
				act = "Question: " + agentutil.TruncateTitle(q, 50)
			}
			return &agent.Blocked{Kind: agent.BlockQuestion, Reason: q, Question: q, Options: opts, Since: stepTime}, act, true
		}
		if tc.Name == "ask_permission" || tc.Name == "ask_custom_permission" {
			args := tc.Args
			if len(args) == 0 {
				if fullStep, ok := resolveFullStep(logPath, step.StepIndex); ok {
					for _, ftc := range fullStep.ToolCalls {
						if ftc.Name == tc.Name {
							args = ftc.Args
							break
						}
					}
				}
			}
			q := "Tool requires permission"
			if tc.Name == "ask_custom_permission" {
				q = "Grant requested permission?"
			}
			return &agent.Blocked{
				Kind: agent.BlockPermission, Reason: extractPermissionReason(args),
				Question: q, Options: []string{"1. Yes", "2. Yes, and always allow", "3. No"}, Since: stepTime,
			}, "Waiting for user permission", true
		}
	}
	if strings.Contains(step.Content, "Note: You have just created an artifact and requested user feedback") ||
		strings.Contains(step.Content, "Stop calling tools to end your turn, and allow the user to review the artifact") {
		return &agent.Blocked{
			Kind: agent.BlockQuestion, Reason: "Plan approval required",
			Question: "Please review and approve the implementation plan", Options: []string{"Proceed", "Provide Feedback"}, Since: stepTime,
		}, "Waiting for plan feedback", true
	}
	return nil, "", false
}
