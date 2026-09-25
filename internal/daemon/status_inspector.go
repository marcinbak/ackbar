package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/tmux"
)

var backgroundShellRegex = regexp.MustCompile(`\b\d+\s+shells?\b`)

func (s *Server) inspectAntigravityStatus(ctx context.Context, sess *Session) bool {
	return InspectAntigravityStatus(ctx, sess)
}

func InspectAntigravityStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "antigravity" || sess.State == StateEnded {
		return false
	}
	changed := false
	home, _ := os.UserHomeDir()

	// 1. Check live tmux screen for permission prompts or confirmations
	if sess.TmuxName != "" {
		if out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output(); err == nil {
			paneText := string(out)
			lines := strings.Split(paneText, "\n")
			startIdx := len(lines) - 25
			if startIdx < 0 {
				startIdx = 0
			}
			tailText := strings.Join(lines[startIdx:], "\n")

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
					sess.LastEventAt = time.Now()
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
					sess.LastEventAt = time.Now()
					changed = true
				}
				return changed
			}
		}
	}

	// 2. Check transcript.jsonl for ask_question or plan approval (using tail 64KB read to avoid reading entire file)
	if home != "" && sess.NativeID != "" {
		brainDirs := []string{
			filepath.Join(home, ".gemini", "antigravity", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".gemini", "antigravity-cli", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".antigravity", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
		}
		for _, logPath := range brainDirs {
			if data, err := readTail(logPath, 64*1024); err == nil && len(data) > 0 {
				lines := strings.Split(string(data), "\n")
				stepsChecked := 0
				for i := len(lines) - 1; i >= 0 && stepsChecked < 30; i-- {
					line := strings.TrimSpace(lines[i])
					if line == "" {
						continue
					}
					var step struct {
						Type      string `json:"type"`
						Content   string `json:"content"`
						ToolCalls []struct {
							Name string                 `json:"name"`
							Args map[string]interface{} `json:"args"`
						} `json:"tool_calls"`
						CreatedAt string `json:"created_at"`
					}
					if jerr := json.Unmarshal([]byte(line), &step); jerr == nil {
						stepsChecked++
						// If the user already provided input after this, any prior question in this conversation was answered
						if step.Type == "USER_INPUT" {
							break
						}

						stepTime, _ := time.Parse(time.RFC3339, step.CreatedAt)
						if stepTime.IsZero() {
							stepTime = time.Now()
						}

						for _, tc := range step.ToolCalls {
							if tc.Name == "ask_question" {
								q, opts := ExtractAntigravityQuestionAndOptions(tc.Args)
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
									sess.LastEventAt = stepTime
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
								sess.LastEventAt = stepTime
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

	// 3. Check for active subagents on disk
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
					sess.LastEventAt = time.Now()
					changed = true
				}
			}
		}
	}

	// 4. Check for active child processes
	if sess.State != StateBlocked && sess.PID > 0 {
		if activeChildren := getActiveChildProcesses(ctx, sess.PID); len(activeChildren) > 0 {
			if sess.State != StateWorking {
				sess.State = StateWorking
				sess.Blocked = nil
				sess.Activity = "Running " + activeChildren[0]
				sess.LastEventAt = time.Now()
				changed = true
			}
		}
	}

	// 5. If it was blocked, but live tmux pane and transcript show it is now unblocked
	if sess.State == StateBlocked {
		if sess.TmuxName != "" || isProcessAlive(sess.PID) {
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			sess.LastEventAt = time.Now()
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

func (s *Server) inspectClaudeStatus(ctx context.Context, sess *Session) bool {
	return InspectClaudeStatus(ctx, sess)
}

func InspectClaudeStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "claude-code" || sess.State == StateEnded {
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

	var tailText string
	var lines []string

	// 1. Live Tmux Pane Check: Priority on Permission and Question Prompts
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
		startIdx := len(lines) - 25
		if startIdx < 0 {
			startIdx = 0
		}
		tailText = strings.Join(lines[startIdx:], "\n")

		// 1A. Permission prompt / confirmation
		if strings.Contains(tailText, "Do you want to run") ||
			strings.Contains(tailText, "Do you want to proceed") ||
			strings.Contains(tailText, "Allow once") ||
			strings.Contains(tailText, "Allow always") ||
			strings.Contains(tailText, "[y/N]") ||
			strings.Contains(tailText, "[Y/n]") ||
			strings.Contains(tailText, "Permission requested") ||
			strings.Contains(tailText, "Authorize tool execution") ||
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
				sess.LastEventAt = time.Now()
				changed = true
			}
			return changed
		}

		// 1B. Question / user choice / prompt selection
		if strings.Contains(tailText, "Enter to select") ||
			strings.Contains(tailText, "Tab/Arrow keys to navigate") ||
			strings.Contains(tailText, "↑/↓ to navigate") ||
			strings.Contains(tailText, "↑ / ↓ to navigate") ||
			strings.Contains(tailText, "Esc to cancel") ||
			strings.Contains(tailText, "✔ Submit") ||
			strings.Contains(tailText, "Waiting for user response") ||
			strings.Contains(tailText, "AskUserQuestion") ||
			strings.Contains(tailText, "Type something.") ||
			strings.Contains(tailText, "Chat about this") {
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
				sess.LastEventAt = time.Now()
				changed = true
			}
			return changed
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
					sess.LastEventAt = time.Now()
					changed = true
				}
				return changed
			}
		}
	}

	// 3. Structured Background Task Output Discovery (/tmp/claude-$UID/.../tasks/*.output)
	if sess.NativeID != "" {
		cleanID := filepath.Base(filepath.Clean(sess.NativeID))
		if cleanID != "" && cleanID != "." && cleanID != ".." && !strings.ContainsAny(cleanID, "*?[") {
			tmpPattern := fmt.Sprintf("/tmp/claude-*/*/%s/tasks/*.output", cleanID)
			if matches, err := filepath.Glob(tmpPattern); err == nil && len(matches) > 0 {
				for _, taskPath := range matches {
					info, err := os.Stat(taskPath)
					if err != nil {
						continue
					}
					if time.Since(info.ModTime()) < 15*time.Minute {
						if data, err := readTail(taskPath, 256); err == nil {
							if !strings.Contains(string(data), "[exited with code") {
								if sess.State != StateWorking {
									sess.State = StateWorking
									sess.Blocked = nil
									sess.Activity = "Executing background task..."
									sess.LastEventAt = time.Now()
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
		// 4. Active generation / tool spinner / active background shells
		hasSpinner := strings.Contains(tailText, "⠋") || strings.Contains(tailText, "⠙") ||
			strings.Contains(tailText, "⠹") || strings.Contains(tailText, "⠸") ||
			strings.Contains(tailText, "⠼") || strings.Contains(tailText, "⠴") ||
			strings.Contains(tailText, "⠦") || strings.Contains(tailText, "⠧") ||
			strings.Contains(tailText, "⠇") || strings.Contains(tailText, "⠏") ||
			strings.Contains(tailText, "Thinking...") ||
			strings.Contains(tailText, "Running tool:")

		hasBackgroundShell := backgroundShellRegex.MatchString(tailText)

		if hasSpinner || hasBackgroundShell {
			if sess.State != StateWorking {
				sess.State = StateWorking
				sess.Blocked = nil
				if hasBackgroundShell {
					sess.Activity = "Executing background shell..."
				} else {
					sess.Activity = "Working..."
				}
				sess.LastEventAt = time.Now()
				changed = true
			}
			return changed
		}

		// 5. Interactive prompt idle (strictly at bottom prompt ❯, no background tasks, no active background shells)
		var nonEmpty []string
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if t != "" {
				nonEmpty = append(nonEmpty, t)
			}
		}

		hasPrompt := false
		for i := len(nonEmpty) - 1; i >= 0 && i >= len(nonEmpty)-4; i-- {
			if isClaudePromptLine(nonEmpty[i]) {
				hasPrompt = true
				break
			}
		}

		if hasPrompt {
			if sess.State != StateIdle {
				sess.State = StateIdle
				sess.Blocked = nil
				sess.Activity = "Awaiting user prompt"
				changed = true
			}
			return changed
		}

		// 6. Active OS child processes (when not at interactive prompt)
		activeChildren := getActiveChildProcesses(ctx, sess.PID)
		if len(activeChildren) > 0 {
			if sess.State != StateWorking {
				sess.State = StateWorking
				sess.Blocked = nil
				sess.Activity = "Running " + activeChildren[0]
				sess.LastEventAt = time.Now()
				changed = true
			}
			return changed
		}

		// 7. If it was blocked, but tmux pane is unblocked and alive
		if sess.State == StateBlocked {
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
	}

	return changed
}

func isClaudePromptLine(trimmed string) bool {
	if trimmed == "❯" {
		return true
	}
	if strings.HasPrefix(trimmed, "❯ Type ") || strings.HasPrefix(trimmed, "❯ Try ") {
		return true
	}
	return false
}

func extractClaudeQuestionAndOptions(tailText string) (string, []string) {
	lines := strings.Split(tailText, "\n")
	var options []string
	firstOptIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		clean := strings.Trim(trimmed, " │┌└─")
		clean = strings.TrimPrefix(clean, "❯ ")
		clean = strings.TrimPrefix(clean, "❯")
		clean = strings.TrimSpace(clean)

		if len(clean) > 2 && clean[0] >= '1' && clean[0] <= '9' && clean[1] == '.' {
			optText := strings.TrimSpace(clean[2:])
			optText = strings.Trim(optText, " │┌└─")
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
			cleanPrev := strings.Trim(prev, " │┌└─")
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
			cleanLine := strings.Trim(trimmed, " │┌└─")
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
// It returns non-zombie command names of child processes, prioritizing worker grandchildren.
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

	// Batch query all child processes in a single ps call
	statOut, err := exec.CommandContext(ctx, "ps", "-o", "pid=,stat=,comm=", "-p", strings.Join(pids, ",")).Output()
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
			if strings.HasPrefix(stat, "Z") || strings.HasPrefix(stat, "z") {
				continue
			}
			if comm == "bash" || comm == "sh" || comm == "zsh" || comm == "node" {
				intermediatePIDs = append(intermediatePIDs, pidStr)
			}
			activeCommands = append(activeCommands, comm)
		}
	}

	// Inspect grandchildren under shell/runner processes (e.g. gradle, cargo, npm, clang)
	if len(intermediatePIDs) > 0 {
		var grandPIDs []string
		for _, ipid := range intermediatePIDs {
			if gOut, gErr := exec.CommandContext(ctx, "pgrep", "-P", ipid).Output(); gErr == nil && len(gOut) > 0 {
				grandPIDs = append(grandPIDs, strings.Fields(string(gOut))...)
			}
		}
		if len(grandPIDs) > 0 {
			if gStatOut, gErr := exec.CommandContext(ctx, "ps", "-o", "stat=,comm=", "-p", strings.Join(grandPIDs, ",")).Output(); gErr == nil && len(gStatOut) > 0 {
				var grandCommands []string
				for _, line := range strings.Split(string(gStatOut), "\n") {
					gFields := strings.Fields(line)
					if len(gFields) >= 2 && !strings.HasPrefix(gFields[0], "Z") && !strings.HasPrefix(gFields[0], "z") {
						grandCommands = append(grandCommands, filepath.Base(gFields[1]))
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
