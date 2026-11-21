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

	"ackbar/internal/agentutil"
	"ackbar/internal/tmux"
)

func extractTailLines(lines []string, count int) []string {
	return agentutil.ExtractTailLinesSlice(lines, count)
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

		// 1C. Rate limit detection in live pane text (when not actively spinning)
		if sess.State == StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == BlockRateLimit {
			if sess.Blocked.ResetAt == nil || sess.Blocked.ResetAt.After(time.Now()) {
				return changed
			}
			// Reset time has passed; do not re-detect old static rate-limit in pane, fall through to unblock
		} else if !hasSpinner {
			adapter := ClaudeAgentAdapter{}
			if limited, resetAt, reason := adapter.DetectRateLimit(bottom6Text, time.Now()); limited {
				if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockRateLimit || sess.Blocked.Reason != reason {
					sess.State = StateBlocked
					sess.Blocked = &Blocked{
						Kind:     BlockRateLimit,
						Reason:   reason,
						Question: reason,
						ResetAt:  &resetAt,
						Options:  []string{fmt.Sprintf("Resume at %s", resetAt.Local().Format("15:04")), "Resume in 1h"},
						Since:    time.Now(),
					}
					sess.Activity = "Rate limit: " + reason
					changed = true
				}
				return changed
			}
		}
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
		if sess.Blocked != nil && sess.Blocked.Kind == BlockRateLimit && sess.Blocked.ResetAt != nil && sess.Blocked.ResetAt.After(time.Now()) {
			return changed
		}
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
