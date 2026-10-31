package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/tmux"
)

func extractTailLines(lines []string, count int) []string {
	lastNonEmpty := len(lines) - 1
	for lastNonEmpty >= 0 && strings.TrimSpace(lines[lastNonEmpty]) == "" {
		lastNonEmpty--
	}
	if lastNonEmpty < 0 {
		return nil
	}
	startIdx := lastNonEmpty - count + 1
	if startIdx < 0 {
		startIdx = 0
	}
	return lines[startIdx : lastNonEmpty+1]
}

func extractTailText(lines []string, count int) string {
	tail := extractTailLines(lines, count)
	if len(tail) == 0 {
		return ""
	}
	return strings.Join(tail, "\n")
}

func effectiveBlockedSince(sess *Session) time.Time {
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

func isWithinBlockedGraceWindow(sess *Session) bool {
	if sess == nil || sess.State != StateBlocked || sess.Blocked == nil {
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

type antigravityStep struct {
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

func resolveFullAntigravityStep(logPath string, stepIndex int) (*antigravityStep, bool) {
	fullPath := filepath.Join(filepath.Dir(logPath), "transcript_full.jsonl")
	data, err := readTail(fullPath, 256*1024)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	lines := strings.Split(string(data), "\n")
	var fallbackPlanner *antigravityStep
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var s antigravityStep
		if err := json.Unmarshal([]byte(line), &s); err == nil {
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

func isNonTurnAntigravityStep(stepType string) bool {
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

func (s *Server) inspectAntigravityStatus(ctx context.Context, sess *Session) bool {
	return InspectAntigravityStatus(ctx, sess)
}

func InspectAntigravityStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "antigravity" || sess.State == StateEnded {
		return false
	}
	if sess.EngineType == EngineHeadless && sess.TmuxName == "" && (sess.PID <= 0 || !isProcessAlive(sess.PID)) {
		return false
	}
	changed := false
	home, _ := os.UserHomeDir()

	// Resolve PID from tmux if missing
	if sess.PID <= 0 && sess.TmuxName != "" {
		if pid, err := tmux.GetPID(ctx, sess.TmuxName); err == nil && pid > 0 {
			sess.PID = pid
			changed = true
		}
	}

	// 1. Structured Subagents on Disk
	if sess.NativeID != "" {
		if subs, err := ExtractSubagents("antigravity", sess.NativeID, sess.Cwd); err == nil {
			runningCount := 0
			var activeRole string
			for _, sub := range subs {
				if sub.State == "running" {
					runningCount++
					if activeRole == "" {
						activeRole = sub.Role
					}
				}
			}
			if sess.RunningSubagents != runningCount {
				sess.RunningSubagents = runningCount
				changed = true
			}
			if runningCount > 0 && sess.State != StateBlocked {
				if sess.State != StateWorking {
					sess.State = StateWorking
					sess.Blocked = nil
					sess.Activity = "Subagent running: " + activeRole
					changed = true
				}
				return changed
			}
		}
	}

	var lines []string
	var nonEmpty []string
	var tailText string
	var bottomText string
	hasIdlePrompt := false
	hasSpinner := false
	hasCancel := false

	// 2. Live Tmux Screen Inspection
	if sess.TmuxName != "" {
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
		if err != nil {
			sess.State = StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			return true
		}

		if sess.PID > 0 && !isProcessAlive(sess.PID) {
			if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs == nil && len(strings.TrimSpace(string(outPs))) > 0 {
				// Child process alive
			} else {
				sess.State = StateEnded
				sess.Activity = "Session ended (process exited)"
				sess.PID = 0
				sess.Blocked = nil
				return true
			}
		}

		paneText := string(out)
		lines = strings.Split(paneText, "\n")
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if t != "" {
				nonEmpty = append(nonEmpty, t)
			}
		}

		for i := len(nonEmpty) - 1; i >= 0 && i >= len(nonEmpty)-4; i-- {
			if isAntigravityPromptLine(nonEmpty[i]) {
				hasIdlePrompt = true
				break
			}
		}

		tailText = extractTailText(lines, 25)
		bottomText = extractTailText(lines, 5)

		hasSpinner = !hasIdlePrompt && (strings.Contains(bottomText, "⠋") || strings.Contains(bottomText, "⠙") ||
			strings.Contains(bottomText, "⠹") || strings.Contains(bottomText, "⠸") ||
			strings.Contains(bottomText, "⠼") || strings.Contains(bottomText, "⠴") ||
			strings.Contains(bottomText, "⠦") || strings.Contains(bottomText, "⠧") ||
			strings.Contains(bottomText, "⠇") || strings.Contains(bottomText, "⠏") ||
			strings.Contains(bottomText, "⢿") ||
			strings.Contains(bottomText, "Thinking...") ||
			strings.Contains(bottomText, "Running command...") ||
			strings.Contains(bottomText, "Running tool:"))

		hasCancel = !hasIdlePrompt && strings.Contains(bottomText, "esc to cancel")

		// 2A. Permission prompts or confirmations
		if !hasIdlePrompt {
			if strings.Contains(tailText, "Requesting permission for:") || strings.Contains(tailText, "Do you want to proceed?") {
				cmdReason := ""
				if idx := strings.Index(tailText, "Requesting permission for:"); idx != -1 {
					sub := tailText[idx+len("Requesting permission for:"):]
					if endIdx := strings.Index(sub, "Do you want to proceed?"); endIdx != -1 {
						cmdReason = strings.TrimSpace(sub[:endIdx])
					}
				}
				if cmdReason == "" {
					cmdReason = "Tool permission requested"
				}

				if sess.State != StateBlocked || sess.Blocked == nil {
					sess.State = StateBlocked
					sess.Blocked = &Blocked{
						Kind:     BlockPermission,
						Reason:   cmdReason,
						Question: "Do you want to proceed with: " + truncateTitle(cmdReason),
						Options:  []string{"1. Yes", "2. Yes, and always allow in this conversation", "4. No"},
						Since:    time.Now(),
					}
					sess.Activity = "Waiting for permission: " + truncateTitle(cmdReason)
					changed = true
				}
				return changed
			} else if strings.Contains(tailText, "Are you sure?") || strings.Contains(tailText, "[y/N]") || strings.Contains(tailText, "[Y/n]") {
				if sess.State != StateBlocked || sess.Blocked == nil {
					sess.State = StateBlocked
					sess.Blocked = &Blocked{
						Kind:     BlockPermission,
						Reason:   "Confirmation required",
						Question: "Confirmation required",
						Options:  []string{"Yes", "No"},
						Since:    time.Now(),
					}
					sess.Activity = "Waiting for confirmation"
					changed = true
				}
				return changed
			}
		}
	}

	// 3. Structured Transcript Inspection (tail up to 256KB)
	transcriptTurnCompletedAfterBlock := false
	if home != "" && sess.NativeID != "" {
		cleanID := filepath.Base(filepath.Clean(sess.NativeID))
		var brainDirs []string
		if cleanID != "" && cleanID != "." && cleanID != ".." && !strings.ContainsAny(cleanID, "*?[") {
			brainDirs = []string{
				filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
				filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
				filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
			}
			if profiles, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl")); len(profiles) > 0 {
				brainDirs = append(brainDirs, profiles...)
			}
			if profiles2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl")); len(profiles2) > 0 {
				brainDirs = append(brainDirs, profiles2...)
			}
		}

		for _, logPath := range brainDirs {
			if data, err := readTail(logPath, 256*1024); err == nil && len(data) > 0 {
				tLines := strings.Split(string(data), "\n")

				for i := len(tLines) - 1; i >= 0; i-- {
					line := strings.TrimSpace(tLines[i])
					if line == "" {
						continue
					}
					var candidate antigravityStep
					if err := json.Unmarshal([]byte(line), &candidate); err == nil && candidate.Type == "USER_INPUT" {
						if userTime := parseStepTimestamp(candidate.CreatedAt); !userTime.IsZero() {
							if sess.LastEventAt.IsZero() || userTime.After(sess.LastEventAt) {
								sess.LastEventAt = userTime
								changed = true
							}
						}
						break
					}
				}

				var lastStep antigravityStep
				lastValidIdx := -1
				for i := len(tLines) - 1; i >= 0; i-- {
					line := strings.TrimSpace(tLines[i])
					if line == "" {
						continue
					}
					var candidate antigravityStep
					if err := json.Unmarshal([]byte(line), &candidate); err == nil {
						if isNonTurnAntigravityStep(candidate.Type) {
							continue
						}
						if hasTruncatedField(candidate.TruncatedFields, "tool_calls") {
							if fullStep, ok := resolveFullAntigravityStep(logPath, candidate.StepIndex); ok {
								candidate.ToolCalls = fullStep.ToolCalls
							}
						}
						lastStep = candidate
						lastValidIdx = i
						break
					}
				}

				if lastValidIdx != -1 {
					// 3A. Check if blocked on ask_question or plan approval (evaluated BEFORE Step 2B spinner/cancel)
					stepsChecked := 0
					for i := lastValidIdx; i >= 0 && stepsChecked < 30; i-- {
						line := strings.TrimSpace(tLines[i])
						if line == "" {
							continue
						}
						var step antigravityStep
						if jerr := json.Unmarshal([]byte(line), &step); jerr == nil {
							if isNonTurnAntigravityStep(step.Type) {
								continue
							}
							stepsChecked++
							if step.Type == "USER_INPUT" || step.Type == "ASK_QUESTION" || step.Type == "TOOL_RESPONSE" {
								break
							}

							if hasTruncatedField(step.TruncatedFields, "tool_calls") {
								if fullStep, ok := resolveFullAntigravityStep(logPath, step.StepIndex); ok {
									step.ToolCalls = fullStep.ToolCalls
								}
							}

							hasOtherTools := false
							hasAskQuestion := false
							for _, tc := range step.ToolCalls {
								if tc.Name == "ask_question" {
									hasAskQuestion = true
								} else if tc.Name != "" {
									hasOtherTools = true
								}
							}
							if hasOtherTools && !hasAskQuestion {
								break
							}

							stepTime, _ := time.Parse(time.RFC3339, step.CreatedAt)
							if stepTime.IsZero() {
								stepTime = time.Now()
							}

							for _, tc := range step.ToolCalls {
								if tc.Name == "ask_question" {
									q, opts := ExtractAntigravityQuestionAndOptions(tc.Args)
									if q == "" && len(opts) == 0 {
										if fullStep, ok := resolveFullAntigravityStep(logPath, step.StepIndex); ok {
											for _, ftc := range fullStep.ToolCalls {
												if ftc.Name == "ask_question" {
													q, opts = ExtractAntigravityQuestionAndOptions(ftc.Args)
													break
												}
											}
										}
									}
									if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Question != q {
										sess.State = StateBlocked
										sess.Blocked = &Blocked{
											Kind:     BlockQuestion,
											Reason:   q,
											Question: q,
											Options:  opts,
											Since:    stepTime,
										}
										if q != "" {
											sess.Activity = "Question: " + truncateTitle(q)
										} else {
											sess.Activity = "Waiting for user response"
										}
										changed = true
									}
									return changed
								}
							}

							if strings.Contains(step.Content, "Note: You have just created an artifact and requested user feedback") ||
								strings.Contains(step.Content, "Stop calling tools to end your turn, and allow the user to review the artifact") {
								if sess.State != StateBlocked || sess.Blocked == nil {
									sess.State = StateBlocked
									sess.Blocked = &Blocked{
										Kind:     BlockQuestion,
										Reason:   "Plan approval required",
										Question: "Please review and approve the implementation plan",
										Options:  []string{"Proceed", "Provide Feedback"},
										Since:    stepTime,
									}
									sess.Activity = "Waiting for plan feedback"
									changed = true
								}
								return changed
							}
						}
					}

					// 3B. Turn completion detection
					if lastStep.Type == "USER_INPUT" {
						if sess.EngineType == EngineHeadless && sess.TmuxName == "" {
							break
						}
						stat, serr := os.Stat(logPath)
						if serr == nil && time.Since(stat.ModTime()) >= 30*time.Minute {
							break
						}
						if serr == nil && time.Since(stat.ModTime()) > 15*time.Second && hasIdlePrompt && !hasSpinner {
							break
						}
						if isWithinBlockedGraceWindow(sess) {
							return changed
						}
						if sess.State != StateWorking {
							sess.State = StateWorking
							sess.Blocked = nil
							sess.Activity = "Thinking..."
							if sess.LastEventAt.IsZero() {
								sess.LastEventAt = time.Now()
							}
							changed = true
						}
						return changed
					} else if lastStep.Type == "PLANNER_RESPONSE" && ((lastStep.Status == "DONE" && len(lastStep.ToolCalls) == 0) ||
						strings.EqualFold(lastStep.Status, "CANCELED") ||
						strings.EqualFold(lastStep.Status, "CANCELLED") ||
						strings.EqualFold(lastStep.Status, "ERROR")) {
						lastStepTime, _ := time.Parse(time.RFC3339, lastStep.CreatedAt)
						blockSince := effectiveBlockedSince(sess)
						if !lastStepTime.IsZero() && (blockSince.IsZero() || !lastStepTime.Before(blockSince)) {
							transcriptTurnCompletedAfterBlock = true
						}
						if isWithinBlockedGraceWindow(sess) && !transcriptTurnCompletedAfterBlock {
							return changed
						}
						if !hasSpinner {
							if sess.State != StateIdle {
								sess.State = StateIdle
								sess.Blocked = nil
								sess.Activity = "Awaiting user prompt"
								changed = true
							}
							return changed
						}
					}
				}
				break
			}
		}
	}

	// 2B. Active generation / tool execution / active spinners in bottom 5 lines (evaluated after Step 3A ask_question)
	if sess.TmuxName != "" && !hasIdlePrompt && (hasSpinner || hasCancel) {
		if isWithinBlockedGraceWindow(sess) && !strings.Contains(bottomText, "Thinking...") {
			return changed
		}
		if sess.State != StateWorking {
			sess.State = StateWorking
			sess.Blocked = nil
			sess.Activity = "Working..."
			changed = true
		}
		return changed
	}

	// 5. Fallback Live Tmux Pane Prompt Detection (if transcript did not determine state)
	if sess.TmuxName != "" && len(nonEmpty) > 0 {
		hasPrompt := hasIdlePrompt
		if !hasPrompt {
			for i := len(nonEmpty) - 1; i >= 0 && i >= len(nonEmpty)-6; i-- {
				if isAntigravityPromptLine(nonEmpty[i]) {
					hasPrompt = true
					break
				}
			}
		}

		if hasPrompt {
			if isWithinBlockedGraceWindow(sess) && !transcriptTurnCompletedAfterBlock {
				return changed
			}
			if sess.State != StateIdle {
				sess.State = StateIdle
				sess.Blocked = nil
				sess.Activity = "Awaiting user prompt"
				changed = true
			}
			return changed
		}
	}

	// 6. If it was blocked, but unblocked and alive
	if sess.State == StateBlocked {
		if sess.TmuxName != "" || isProcessAlive(sess.PID) {
			if isWithinBlockedGraceWindow(sess) && !hasSpinner && !transcriptTurnCompletedAfterBlock {
				return changed
			}
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		} else {
			sess.State = StateEnded
			sess.Blocked = nil
			sess.Activity = "Session ended"
			changed = true
		}
	}

	return changed
}

func isAntigravityPromptLine(trimmed string) bool {
	trimmed = strings.TrimSpace(strings.ReplaceAll(trimmed, "\u00a0", " "))
	if trimmed == ">" || trimmed == "> " {
		return true
	}
	if trimmed == "> Accept-edits" || trimmed == "> Plan mode" || trimmed == "> Auto mode" {
		return true
	}
	return false
}

func hasClaudeSpinner(text string) bool {
	return strings.Contains(text, "⠋") || strings.Contains(text, "⠙") ||
		strings.Contains(text, "⠹") || strings.Contains(text, "⠸") ||
		strings.Contains(text, "⠼") || strings.Contains(text, "⠴") ||
		strings.Contains(text, "⠦") || strings.Contains(text, "⠧") ||
		strings.Contains(text, "⠇") || strings.Contains(text, "⠏") ||
		strings.Contains(text, "Thinking...") ||
		strings.Contains(text, "Running tool:")
}

func isClaudePermissionLine(line string) bool {
	return strings.Contains(line, "Do you want to run") ||
		strings.Contains(line, "Do you want to proceed") ||
		strings.Contains(line, "Do you want to make this edit") ||
		strings.Contains(line, "Do you want to create") ||
		strings.Contains(line, "Do you want to overwrite") ||
		strings.Contains(line, "Would you like to proceed") ||
		strings.Contains(line, "Yes, allow all edits") ||
		strings.Contains(line, "Yes, allow") ||
		strings.Contains(line, "Allow once") ||
		strings.Contains(line, "Allow always") ||
		strings.Contains(line, "[y/N]") ||
		strings.Contains(line, "[Y/n]") ||
		strings.Contains(line, "Permission requested") ||
		strings.Contains(line, "Authorize tool execution") ||
		strings.Contains(line, "Are you sure?")
}

func hasNumberedSelectorOption(lines []string) bool {
	for _, line := range lines {
		clean := strings.Trim(strings.TrimSpace(line), claudeBorderChars)
		clean = strings.TrimPrefix(clean, "❯ ")
		clean = strings.TrimPrefix(clean, "❯")
		clean = strings.TrimSpace(clean)
		if len(clean) > 2 && clean[0] >= '1' && clean[0] <= '9' && clean[1] == '.' {
			return true
		}
	}
	return false
}

func isClaudeQuestionIndicatorLine(line string, hasNumberedOpt bool) bool {
	if strings.Contains(line, "Enter to select") ||
		strings.Contains(line, "Tab/Arrow keys") ||
		strings.Contains(line, "↑/↓ to navigate") ||
		strings.Contains(line, "↑ / ↓ to navigate") ||
		strings.Contains(line, "✔ Submit") ||
		strings.Contains(line, "Waiting for user response") ||
		strings.Contains(line, "Type something") ||
		strings.Contains(line, "Chat about this") {
		return true
	}
	if hasNumberedOpt && (strings.Contains(line, "Esc to cancel") || strings.Contains(line, "esc to cancel")) {
		return true
	}
	return false
}

func (s *Server) inspectClaudeStatus(ctx context.Context, sess *Session) bool {
	return InspectClaudeStatus(ctx, sess)
}

func InspectClaudeStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "claude-code" || sess.State == StateEnded {
		return false
	}
	if sess.EngineType == EngineHeadless && sess.TmuxName == "" && (sess.PID <= 0 || !isProcessAlive(sess.PID)) {
		return false
	}
	changed := false
	home, _ := os.UserHomeDir()

	// Resolve PID from tmux if missing
	if sess.PID <= 0 && sess.TmuxName != "" {
		if pid, err := tmux.GetPID(ctx, sess.TmuxName); err == nil && pid > 0 {
			sess.PID = pid
			changed = true
		}
	}

	var tailText string
	var bottom6Text string
	var lines []string
	var nonEmpty []string
	hasSpinner := false
	hasPrompt := false

	// 1. Live Tmux Pane Check: Priority on Permission and Question Prompts in bottom 8 non-empty lines
	if sess.TmuxName != "" {
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
		if err != nil {
			sess.State = StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			return true
		}

		// Check if Claude process is actually alive under pane PID
		if sess.PID > 0 && !isProcessAlive(sess.PID) {
			if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs == nil && len(strings.TrimSpace(string(outPs))) > 0 {
				// Child process alive
			} else {
				sess.State = StateEnded
				sess.Activity = "Session ended (process exited)"
				sess.PID = 0
				sess.Blocked = nil
				return true
			}
		}

		paneText := string(out)
		lines = strings.Split(paneText, "\n")
		for _, l := range lines {
			t := strings.TrimSpace(strings.ReplaceAll(l, "\u00a0", " "))
			if t != "" {
				nonEmpty = append(nonEmpty, t)
			}
		}

		tailText = extractTailText(lines, 25)
		bottom8Lines := extractTailLines(nonEmpty, 8)
		bottom6Text = strings.Join(extractTailLines(nonEmpty, 6), "\n")

		lastPromptIdx := -1
		lastSpinnerIdx := -1
		lastPromptOrSpinnerIdx := -1
		lastPermIdx := -1
		lastQuestionIdx := -1
		hasNumOpt := hasNumberedSelectorOption(bottom8Lines)

		promptMinIdx := len(bottom8Lines) - 6
		if promptMinIdx < 0 {
			promptMinIdx = 0
		}

		for i, l := range bottom8Lines {
			if isClaudePromptLine(l) {
				if i >= promptMinIdx {
					lastPromptIdx = i
				}
				lastPromptOrSpinnerIdx = i
			}
			if hasClaudeSpinner(l) {
				if i >= promptMinIdx {
					lastSpinnerIdx = i
				}
				lastPromptOrSpinnerIdx = i
			}
			if isClaudePermissionLine(l) {
				lastPermIdx = i
			}
			if isClaudeQuestionIndicatorLine(l, hasNumOpt) {
				lastQuestionIdx = i
			}
		}

		// 1A. Permission prompt / confirmation in bottom 8 lines (not superseded by a lower prompt or spinner)
		if lastPermIdx != -1 && lastPromptOrSpinnerIdx < lastPermIdx {
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockPermission {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockPermission,
					Reason:   "Tool permission requested",
					Question: "Permission required",
					Options:  []string{"Allow", "Deny"},
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for tool authorization"
				changed = true
			}
			return changed
		}

		// 1B. Question / user choice / prompt selection in bottom 8 lines (not superseded by a lower prompt or spinner)
		if lastQuestionIdx != -1 && lastPromptOrSpinnerIdx < lastQuestionIdx {
			q, opts := extractClaudeQuestionAndOptions(tailText)
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Question != q {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockQuestion,
					Reason:   q,
					Question: q,
					Options:  opts,
					Since:    time.Now(),
				}
				if q != "" && q != "Waiting for user response" && q != "Waiting for user input" {
					sess.Activity = "Question: " + truncateTitle(q)
				} else {
					sess.Activity = "Waiting for user input"
				}
				changed = true
			}
			return changed
		}

		hasPrompt = lastPromptIdx != -1 && lastPromptIdx > lastSpinnerIdx && lastPromptIdx > lastPermIdx && lastPromptIdx > lastQuestionIdx
		hasSpinner = lastSpinnerIdx != -1 && lastSpinnerIdx > lastPromptIdx && hasClaudeSpinner(bottom6Text)
	}

	// Inspect Claude .jsonl transcript for blocked tool_use (AskUserQuestion / ExitPlanMode) or completed turn (end_turn / stop_sequence)
	var transcriptEndTurn bool
	var transcriptTurnCompletedAfterBlock bool
	if home != "" && sess.NativeID != "" {
		if transcriptPath := findClaudeTranscriptPath(home, sess.NativeID, sess.Cwd); transcriptPath != "" {
			tInfo, statErr := os.Stat(transcriptPath)
			if data, err := readTail(transcriptPath, 65536); err == nil && len(data) > 0 {
				tLines := strings.Split(strings.TrimSpace(string(data)), "\n")
				minIdx := 0
				if statErr == nil && tInfo.Size() > 65536 && len(tLines) > 1 {
					minIdx = 1
				}
				for i := len(tLines) - 1; i >= minIdx; i-- {
					line := strings.TrimSpace(tLines[i])
					if line == "" {
						continue
					}
					var entry struct {
						Type       string `json:"type"`
						Timestamp  string `json:"timestamp"`
						StopReason string `json:"stop_reason"`
						Message    struct {
							Role       string          `json:"role"`
							StopReason string          `json:"stop_reason"`
							Content    json.RawMessage `json:"content"`
						} `json:"message"`
					}
					if err := json.Unmarshal([]byte(line), &entry); err != nil {
						continue
					}
					if entry.Type != "user" && entry.Type != "assistant" {
						continue
					}

					entryTime, _ := time.Parse(time.RFC3339, entry.Timestamp)
					if entry.Type == "assistant" {
						sr := entry.Message.StopReason
						if sr == "" {
							sr = entry.StopReason
						}
						if sr == "tool_use" && !hasPrompt && !hasSpinner {
							var blocks []struct {
								Type  string                 `json:"type"`
								Name  string                 `json:"name"`
								Input map[string]interface{} `json:"input"`
							}
							if err := json.Unmarshal(entry.Message.Content, &blocks); err == nil {
								for _, b := range blocks {
									if b.Type == "tool_use" && (b.Name == "AskUserQuestion" || b.Name == "ask_question" || b.Name == "ExitPlanMode") {
										var q string
										var opts []string
										if b.Name == "ExitPlanMode" {
											q = "Approve plan?"
											opts = []string{"Yes", "No"}
										} else {
											q, opts = ExtractQuestionAndOptions("AskUserQuestion", b.Input)
											if q == "" {
												q = "Waiting for user response"
											}
										}
										since := entryTime
										if since.IsZero() {
											since = time.Now()
										}
										if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Question != q {
											sess.State = StateBlocked
											sess.Blocked = &Blocked{
												Kind:     BlockQuestion,
												Reason:   q,
												Question: q,
												Options:  opts,
												Since:    since,
											}
											sess.Activity = "Question: " + truncateTitle(q)
											changed = true
										}
										return changed
									}
								}
							}
						} else if sr == "end_turn" || sr == "stop_sequence" {
							transcriptEndTurn = true
							blockSince := effectiveBlockedSince(sess)
							if !entryTime.IsZero() && (blockSince.IsZero() || !entryTime.Before(blockSince)) {
								transcriptTurnCompletedAfterBlock = true
							}
						}
					} else if entry.Type == "user" {
						blockSince := effectiveBlockedSince(sess)
						if !entryTime.IsZero() && !blockSince.IsZero() && entryTime.After(blockSince) {
							transcriptTurnCompletedAfterBlock = true
						}
					}
					break
				}
			}
		}
	}

	// 2. Structured Subagent Discovery from disk (only when not blocked on question/permission)
	if sess.NativeID != "" {
		if subs, err := ExtractSubagents("claude-code", sess.NativeID, sess.Cwd); err == nil {
			runningCount := 0
			var activeSubName string
			for _, sub := range subs {
				if sub.State == "running" {
					runningCount++
					if activeSubName == "" {
						activeSubName = sub.Name
					}
				}
			}
			if sess.RunningSubagents != runningCount {
				sess.RunningSubagents = runningCount
				changed = true
			}
			if runningCount > 0 {
				if sess.State != StateWorking {
					sess.State = StateWorking
					sess.Blocked = nil
					sess.Activity = "Subagent running: " + activeSubName
					changed = true
				}
				return changed
			}
		}
	}

	// 3. Structured Background Task Output Discovery (/tmp/claude-$UID/.../tasks/*.output) when not at idle prompt
	if sess.NativeID != "" && !hasPrompt {
		cleanID := filepath.Base(filepath.Clean(sess.NativeID))
		if cleanID != "" && cleanID != "." && cleanID != ".." && !strings.ContainsAny(cleanID, "*?[") {
			tmpPattern := fmt.Sprintf("/tmp/claude-*/*/%s/tasks/*.output", cleanID)
			if matches, err := filepath.Glob(tmpPattern); err == nil && len(matches) > 0 {
				for _, taskPath := range matches {
					info, err := os.Stat(taskPath)
					if err != nil {
						continue
					}
					if time.Since(info.ModTime()) < 30*time.Second {
						if data, err := readTail(taskPath, 256); err == nil {
							if !strings.Contains(string(data), "[exited with code") {
								if sess.State != StateWorking {
									sess.State = StateWorking
									sess.Blocked = nil
									sess.Activity = "Executing background task..."
									changed = true
								}
								return changed
							}
						}
					}
				}
			}
		}
	}

	if sess.TmuxName != "" {
		// 4. Active generation / tool spinner in bottom 6 lines
		if hasSpinner {
			if sess.State != StateWorking {
				sess.State = StateWorking
				sess.Blocked = nil
				sess.Activity = "Working..."
				changed = true
			}
			return changed
		}

		// 5. Interactive prompt idle (at bottom prompt ❯ in last 6 non-empty lines)
		if hasPrompt {
			if isWithinBlockedGraceWindow(sess) && !transcriptTurnCompletedAfterBlock {
				return changed
			}
			if sess.State != StateIdle {
				sess.State = StateIdle
				sess.Blocked = nil
				sess.Activity = "Awaiting user prompt"
				changed = true
			}
			return changed
		}

		// 7. Explicit agent failure in dead pane
		if strings.Contains(tailText, "No conversation found with session ID") {
			if sess.State != StateEnded {
				sess.State = StateEnded
				sess.Activity = "Agent process exited: No conversation found"
				sess.Blocked = nil
				changed = true
			}
			return changed
		}
	}

	// Transcript fallback: transition Working -> Idle when transcript shows end_turn / stop_sequence
	if transcriptEndTurn && !hasSpinner {
		if isWithinBlockedGraceWindow(sess) && !transcriptTurnCompletedAfterBlock {
			return changed
		}
		if sess.State != StateIdle {
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
		return changed
	}

	// 8. If it was blocked, but tmux pane is unblocked and alive
	if sess.TmuxName != "" && sess.State == StateBlocked {
		if isWithinBlockedGraceWindow(sess) && !hasSpinner && !transcriptTurnCompletedAfterBlock {
			return changed
		}
		sess.State = StateIdle
		sess.Blocked = nil
		sess.Activity = "Awaiting user prompt"
		changed = true
	}

	return changed
}

func isClaudePromptLine(trimmed string) bool {
	normalized := strings.ReplaceAll(trimmed, "\u00a0", " ")
	clean := strings.TrimSpace(normalized)
	clean = strings.Trim(clean, "│")
	clean = strings.TrimSpace(clean)
	if clean == "❯" {
		return true
	}
	if strings.HasPrefix(clean, "❯ Type ") || strings.HasPrefix(clean, "❯ Try ") || strings.HasPrefix(clean, "❯ Press ") {
		return true
	}
	return false
}

const claudeBorderChars = " │┌└─┐┘╭╮╰╯├┤┬┴┼"

func extractClaudeQuestionAndOptions(tailText string) (string, []string) {
	lines := strings.Split(tailText, "\n")
	var options []string
	firstOptIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		clean := strings.Trim(trimmed, claudeBorderChars)
		clean = strings.TrimPrefix(clean, "❯ ")
		clean = strings.TrimPrefix(clean, "❯")
		clean = strings.TrimSpace(clean)

		if len(clean) > 2 && clean[0] >= '1' && clean[0] <= '9' && clean[1] == '.' {
			optText := strings.TrimSpace(clean[2:])
			optText = strings.Trim(optText, claudeBorderChars)
			optText = strings.TrimSpace(optText)
			if optText != "" && !strings.HasPrefix(optText, "Type something") && !strings.HasPrefix(optText, "Chat about this") {
				options = append(options, optText)
				if firstOptIdx == -1 {
					firstOptIdx = i
				}
			}
		}
	}

	question := ""
	if firstOptIdx != -1 {
		for j := firstOptIdx - 1; j >= 0; j-- {
			prev := strings.TrimSpace(lines[j])
			cleanPrev := strings.Trim(prev, claudeBorderChars)
			if cleanPrev == "" || strings.HasPrefix(cleanPrev, "←") || strings.HasPrefix(cleanPrev, "❯") {
				continue
			}
			question = cleanPrev
			break
		}
	}

	if question == "" {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			cleanLine := strings.Trim(trimmed, claudeBorderChars)
			if strings.HasSuffix(cleanLine, "?") && !strings.Contains(cleanLine, "shortcuts") && !strings.Contains(cleanLine, "want to proceed") {
				question = cleanLine
				break
			}
		}
	}

	if question == "" {
		question = "Waiting for user input"
	}

	return question, options
}

// getActiveChildProcesses inspects active direct or indirect child processes using pgrep and ps.
// It returns non-zombie command names of child processes, prioritizing worker grandchildren
// while ignoring intermediate shells, agent runners, and long-running MCP servers.
func getActiveChildProcesses(ctx context.Context, parentPID int) []string {
	if parentPID <= 0 {
		return nil
	}
	out, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(parentPID)).Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	pids := strings.Fields(string(out))
	if len(pids) == 0 {
		return nil
	}

	// Batch query all child processes in a single ps call with pid, stat, comm, and full command arguments
	statOut, err := exec.CommandContext(ctx, "ps", "-o", "pid=,stat=,comm=,args=", "-p", strings.Join(pids, ",")).Output()
	if err != nil || len(statOut) == 0 {
		return nil
	}

	var activeCommands []string
	var intermediatePIDs []string

	for _, line := range strings.Split(string(statOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			pidStr := fields[0]
			stat := fields[1]
			comm := filepath.Base(fields[2])
			args := strings.Join(fields[2:], " ")
			if strings.HasPrefix(stat, "Z") || strings.HasPrefix(stat, "z") {
				continue
			}
			if isIntermediateRunner(comm) {
				intermediatePIDs = append(intermediatePIDs, pidStr)
				continue
			}
			if isIgnoredMCPOrDaemon(comm, args) {
				continue
			}
			activeCommands = append(activeCommands, comm)
		}
	}

	// Inspect grandchildren under shell/runner processes (e.g. gradle, cargo, npm, clang, go)
	if len(intermediatePIDs) > 0 {
		var grandPIDs []string
		for _, ipid := range intermediatePIDs {
			if gOut, gErr := exec.CommandContext(ctx, "pgrep", "-P", ipid).Output(); gErr == nil && len(gOut) > 0 {
				grandPIDs = append(grandPIDs, strings.Fields(string(gOut))...)
			}
		}
		if len(grandPIDs) > 0 {
			if gStatOut, gErr := exec.CommandContext(ctx, "ps", "-o", "pid=,stat=,comm=,args=", "-p", strings.Join(grandPIDs, ",")).Output(); gErr == nil && len(gStatOut) > 0 {
				var grandCommands []string
				for _, line := range strings.Split(string(gStatOut), "\n") {
					gFields := strings.Fields(line)
					if len(gFields) >= 3 && !strings.HasPrefix(gFields[1], "Z") && !strings.HasPrefix(gFields[1], "z") {
						gComm := filepath.Base(gFields[2])
						gArgs := strings.Join(gFields[2:], " ")
						if isIntermediateRunner(gComm) || isIgnoredMCPOrDaemon(gComm, gArgs) {
							continue
						}
						grandCommands = append(grandCommands, gComm)
					}
				}
				if len(grandCommands) > 0 {
					// Prioritize worker grandchildren over shell parents
					activeCommands = append(grandCommands, activeCommands...)
				}
			}
		}
	}

	return activeCommands
}

func isIntermediateRunner(comm string) bool {
	base := strings.ToLower(comm)
	switch base {
	case "bash", "sh", "zsh", "fish", "node", "agy", "antigravity", "claude", "codex", "opencode", "grok":
		return true
	}
	return false
}

func isIgnoredMCPOrDaemon(comm, args string) bool {
	lowerComm := strings.ToLower(comm)
	lowerArgs := strings.ToLower(args)
	if lowerComm == "ackbar-hook" ||
		strings.Contains(lowerComm, "ackbar-hook") ||
		strings.Contains(lowerArgs, "ackbar-hook") ||
		strings.Contains(lowerArgs, "mcp-remote") ||
		strings.Contains(lowerArgs, "mcp-server") ||
		strings.Contains(lowerArgs, "modelcontextprotocol") ||
		strings.Contains(lowerArgs, "@modelcontextprotocol") ||
		strings.Contains(lowerArgs, "ackbar mcp") ||
		(lowerComm == "ackbar" && strings.Contains(lowerArgs, "mcp")) {
		return true
	}
	return false
}

func (s *Server) inspectCodexStatus(ctx context.Context, sess *Session) bool {
	return InspectCodexStatus(ctx, sess)
}

func InspectCodexStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "codex" || sess.State == StateEnded {
		return false
	}
	changed := false

	// Resolve PID from tmux if missing
	if sess.PID <= 0 && sess.TmuxName != "" {
		if pid, err := tmux.GetPID(ctx, sess.TmuxName); err == nil && pid > 0 {
			sess.PID = pid
			changed = true
		}
	}

	if sess.TmuxName != "" {
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
		if err != nil {
			sess.State = StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			return true
		}

		if sess.PID > 0 && !isProcessAlive(sess.PID) {
			if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs == nil && len(strings.TrimSpace(string(outPs))) > 0 {
				// Child process alive
			} else {
				sess.State = StateEnded
				sess.Activity = "Session ended (process exited)"
				sess.PID = 0
				sess.Blocked = nil
				return true
			}
		}

		paneText := string(out)
		lines := strings.Split(paneText, "\n")
		tailText := extractTailText(lines, 25)

		// 1. Permission request / confirmation
		if strings.Contains(tailText, "requires approval") ||
			strings.Contains(tailText, "Approval required") ||
			strings.Contains(tailText, "Authorize execution") ||
			strings.Contains(tailText, "[y/N]") ||
			strings.Contains(tailText, "[Y/n]") ||
			strings.Contains(tailText, "Do you want to proceed") {
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockPermission {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockPermission,
					Reason:   "Tool permission requested",
					Question: "Permission required",
					Options:  []string{"Allow", "Deny"},
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for tool authorization"
				changed = true
			}
			return changed
		}

		// 2. Question / user response
		if strings.Contains(tailText, "Waiting for user response") ||
			strings.Contains(tailText, "request_user_input") {
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockQuestion {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockQuestion,
					Reason:   "Waiting for user response",
					Question: "Waiting for user response",
					Options:  nil,
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for user response"
				changed = true
			}
			return changed
		}

		// 3. If it was blocked, but live tmux pane is now unblocked and alive
		if sess.State == StateBlocked {
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
	}

	return changed
}

func (s *Server) inspectGrokStatus(ctx context.Context, sess *Session) bool {
	return InspectGrokStatus(ctx, sess)
}

func InspectGrokStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "grok" || sess.State == StateEnded {
		return false
	}
	changed := false

	// Resolve PID from tmux if missing
	if sess.PID <= 0 && sess.TmuxName != "" {
		if pid, err := tmux.GetPID(ctx, sess.TmuxName); err == nil && pid > 0 {
			sess.PID = pid
			changed = true
		}
	}

	if sess.TmuxName != "" {
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
		if err != nil {
			sess.State = StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			return true
		}

		if sess.PID > 0 && !isProcessAlive(sess.PID) {
			if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs == nil && len(strings.TrimSpace(string(outPs))) > 0 {
				// Child process alive
			} else {
				sess.State = StateEnded
				sess.Activity = "Session ended (process exited)"
				sess.PID = 0
				sess.Blocked = nil
				return true
			}
		}

		paneText := string(out)
		lines := strings.Split(paneText, "\n")
		tailText := extractTailText(lines, 25)

		// 1. Permission request / confirmation
		if strings.Contains(tailText, "requires approval") ||
			strings.Contains(tailText, "Approval required") ||
			strings.Contains(tailText, "Authorize execution") ||
			strings.Contains(tailText, "[y/N]") ||
			strings.Contains(tailText, "[Y/n]") ||
			strings.Contains(tailText, "Do you want to proceed") {
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockPermission {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockPermission,
					Reason:   "Tool permission requested",
					Question: "Permission required",
					Options:  []string{"Allow", "Deny"},
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for tool authorization"
				changed = true
			}
			return changed
		}

		// 2. Question / user response
		if strings.Contains(tailText, "Waiting for user response") ||
			strings.Contains(tailText, "Waiting for input") ||
			strings.Contains(tailText, "request_user_input") {
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockQuestion {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockQuestion,
					Reason:   "Waiting for user response",
					Question: "Waiting for user response",
					Options:  nil,
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for user response"
				changed = true
			}
			return changed
		}

		// 3. If it was blocked, but live tmux pane is now unblocked and alive
		if sess.State == StateBlocked {
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
	}

	return changed
}

func (s *Server) inspectOpenCodeStatus(ctx context.Context, sess *Session) bool {
	return InspectOpenCodeStatus(ctx, sess)
}

func InspectOpenCodeStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "opencode" || sess.State == StateEnded {
		return false
	}
	changed := false

	// Resolve PID from tmux if missing
	if sess.PID <= 0 && sess.TmuxName != "" {
		if pid, err := tmux.GetPID(ctx, sess.TmuxName); err == nil && pid > 0 {
			sess.PID = pid
			changed = true
		}
	}

	if sess.TmuxName != "" {
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
		if err != nil {
			sess.State = StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			return true
		}

		if sess.PID > 0 && !isProcessAlive(sess.PID) {
			if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs == nil && len(strings.TrimSpace(string(outPs))) > 0 {
				// Child process alive
			} else {
				sess.State = StateEnded
				sess.Activity = "Session ended (process exited)"
				sess.PID = 0
				sess.Blocked = nil
				return true
			}
		}

		paneText := string(out)
		lines := strings.Split(paneText, "\n")
		tailText := extractTailText(lines, 25)

		// 1. Permission request / confirmation
		if strings.Contains(tailText, "requires approval") ||
			strings.Contains(tailText, "Approval required") ||
			strings.Contains(tailText, "Permission requested") ||
			strings.Contains(tailText, "Authorize execution") ||
			strings.Contains(tailText, "Do you want to run") ||
			strings.Contains(tailText, "Do you want to proceed") ||
			strings.Contains(tailText, "Allow once") ||
			strings.Contains(tailText, "[y/N]") ||
			strings.Contains(tailText, "[Y/n]") ||
			strings.Contains(tailText, "Are you sure?") {
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockPermission {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockPermission,
					Reason:   "Tool permission requested",
					Question: "Permission required",
					Options:  []string{"Allow", "Deny"},
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for tool authorization"
				changed = true
			}
			return changed
		}

		// 2. Question / user response
		if strings.Contains(tailText, "Waiting for user response") ||
			strings.Contains(tailText, "Waiting for input") ||
			strings.Contains(tailText, "request_user_input") ||
			strings.Contains(tailText, "Enter to select") ||
			strings.Contains(tailText, "Type something") {
			if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockQuestion {
				sess.State = StateBlocked
				sess.Blocked = &Blocked{
					Kind:     BlockQuestion,
					Reason:   "Waiting for user response",
					Question: "Waiting for user response",
					Options:  nil,
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for user response"
				changed = true
			}
			return changed
		}

		// 3. If it was blocked, but live tmux pane is now unblocked and alive
		if sess.State == StateBlocked {
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
	}

	return changed
}
