package claude

import (
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

const borderChars = " │┌└─┐┘╭╮╰╯├┤┬┴┼"

type paneScanResult struct {
	tailText    string
	bottom6Text string
	hasSpinner  bool
	hasPrompt   bool
	hasPerm     bool
	hasQuestion bool
}

func scanPaneText(paneText string) paneScanResult {
	lines := strings.Split(paneText, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if t := strings.TrimSpace(strings.ReplaceAll(l, "\u00a0", " ")); t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}

	tailLines := agentutil.ExtractTailLinesSlice(lines, 25)
	bottom8 := agentutil.ExtractTailLinesSlice(nonEmpty, 8)
	bottom6 := agentutil.ExtractTailLinesSlice(nonEmpty, 6)
	tailText := strings.Join(tailLines, "\n")
	bottom6Text := strings.Join(bottom6, "\n")

	lastPromptIdx, lastSpinnerIdx, lastPromptOrSpinnerIdx := -1, -1, -1
	lastPermIdx, lastQuestionIdx := -1, -1
	hasNumOpt := hasNumberedSelectorOption(bottom8)
	promptMinIdx := len(bottom8) - 6
	if promptMinIdx < 0 {
		promptMinIdx = 0
	}

	for i, l := range bottom8 {
		if isPromptLine(l) {
			if i >= promptMinIdx {
				lastPromptIdx = i
			}
			lastPromptOrSpinnerIdx = i
		}
		if hasSpinner(l) {
			if i >= promptMinIdx {
				lastSpinnerIdx = i
			}
			lastPromptOrSpinnerIdx = i
		}
		if isPermissionLine(l) {
			lastPermIdx = i
		}
		if isQuestionIndicatorLine(l, hasNumOpt) {
			lastQuestionIdx = i
		}
	}

	return paneScanResult{
		tailText:    tailText,
		bottom6Text: bottom6Text,
		hasPerm:     lastPermIdx != -1 && lastPromptOrSpinnerIdx < lastPermIdx,
		hasQuestion: lastQuestionIdx != -1 && lastPromptOrSpinnerIdx < lastQuestionIdx,
		hasPrompt:   lastPromptIdx != -1 && lastPromptIdx > lastSpinnerIdx && lastPromptIdx > lastPermIdx && lastPromptIdx > lastQuestionIdx,
		hasSpinner:  lastSpinnerIdx != -1 && lastSpinnerIdx > lastPromptIdx && hasSpinner(bottom6Text),
	}
}

func applyPaneImmediateBlock(_ *Plugin, sess *agent.Session, pane paneScanResult) (handled bool, changed bool) {
	if pane.hasPerm {
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockPermission {
			sess.State = agent.StateBlocked
			sess.Blocked = &agent.Blocked{
				Kind: agent.BlockPermission, Reason: "Tool permission requested", Question: "Permission required",
				Options: []string{"Allow", "Deny"}, Since: time.Now(),
			}
			sess.Activity = "Waiting for tool authorization"
			changed = true
		}
		return true, changed
	}
	if pane.hasQuestion {
		q, opts := ExtractPaneQuestionAndOptions(pane.tailText)
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Question != q {
			sess.State = agent.StateBlocked
			sess.Blocked = &agent.Blocked{Kind: agent.BlockQuestion, Reason: q, Question: q, Options: opts, Since: time.Now()}
			if q != "" && q != "Waiting for user response" && q != "Waiting for user input" {
				sess.Activity = "Question: " + agentutil.TruncateTitle(q, 50)
			} else {
				sess.Activity = "Waiting for user input"
			}
			changed = true
		}
		return true, changed
	}
	return false, false
}

func hasSpinner(text string) bool {
	for _, marker := range []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏", "Thinking...", "Running tool:"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func isPermissionLine(line string) bool {
	for _, pat := range []string{
		"Do you want to run", "Do you want to proceed", "Do you want to make this edit",
		"Do you want to create", "Do you want to overwrite", "Would you like to proceed",
		"Yes, allow all edits", "Yes, allow", "Allow once", "Allow always",
		"[y/N]", "[Y/n]", "Permission requested", "Authorize tool execution", "Are you sure?",
	} {
		if strings.Contains(line, pat) {
			return true
		}
	}
	return false
}

func hasNumberedSelectorOption(lines []string) bool {
	for _, line := range lines {
		clean := strings.Trim(strings.TrimSpace(line), borderChars)
		clean = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(clean, "❯ "), "❯"))
		if len(clean) > 2 && clean[0] >= '1' && clean[0] <= '9' && clean[1] == '.' {
			return true
		}
	}
	return false
}

func isQuestionIndicatorLine(line string, hasNumberedOpt bool) bool {
	for _, pat := range []string{
		"Enter to select", "Tab/Arrow keys", "↑/↓ to navigate", "↑ / ↓ to navigate",
		"✔ Submit", "Waiting for user response", "Type something", "Chat about this",
	} {
		if strings.Contains(line, pat) {
			return true
		}
	}
	return hasNumberedOpt && (strings.Contains(line, "Esc to cancel") || strings.Contains(line, "esc to cancel"))
}

func isPromptLine(trimmed string) bool {
	clean := strings.TrimSpace(strings.Trim(strings.TrimSpace(strings.ReplaceAll(trimmed, "\u00a0", " ")), "│"))
	return clean == "❯" || strings.HasPrefix(clean, "❯ Type ") || strings.HasPrefix(clean, "❯ Try ") || strings.HasPrefix(clean, "❯ Press ")
}

func ExtractPaneQuestionAndOptions(tailText string) (string, []string) {
	lines := strings.Split(tailText, "\n")
	var options []string
	firstOptIdx := -1
	for i, line := range lines {
		clean := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), borderChars))
		clean = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(clean, "❯ "), "❯"))
		if len(clean) > 2 && clean[0] >= '1' && clean[0] <= '9' && clean[1] == '.' {
			optText := strings.TrimSpace(strings.Trim(strings.TrimSpace(clean[2:]), borderChars))
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
			cleanPrev := strings.Trim(strings.TrimSpace(lines[j]), borderChars)
			if cleanPrev == "" || strings.HasPrefix(cleanPrev, "←") || strings.HasPrefix(cleanPrev, "❯") {
				continue
			}
			question = cleanPrev
			break
		}
	}
	if question == "" {
		for _, line := range lines {
			cleanLine := strings.Trim(strings.TrimSpace(line), borderChars)
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
