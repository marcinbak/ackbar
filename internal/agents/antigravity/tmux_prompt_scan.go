package antigravity

import (
	"strings"

	"ackbar/internal/agentutil"
)

func scanPermissionWindow(window []string, subagentAlertReason string, hasSubagentApprovalBanner bool, requireHeaderWithChoiceOrHint bool) (question string, reason string, options []string, matched bool) {
	headerIdx := -1
	matchedHeader := ""
	for i, cLine := range window {
		if cLine == "" || isPromptLine(cLine) {
			continue
		}
		for _, hdr := range permissionHeaders {
			if strings.Contains(cLine, hdr) {
				if matchedHeader == "" || matchedHeader == "⚠ Approval Required" || (matchedHeader == "Requesting permission for:" && hdr != "Do you want to proceed?") {
					prevHeader := matchedHeader
					matchedHeader = hdr
					if headerIdx == -1 || prevHeader != "Requesting permission for:" {
						headerIdx = i
					}
				}
				break
			}
		}
	}

	var numberedOpts []string
	firstOptionIdx := -1
	hasPermChoice, hasConfirmHint := false, false
	optSearchStart := 0
	if headerIdx != -1 {
		optSearchStart = headerIdx
	}
	for i := optSearchStart; i < len(window); i++ {
		cLine := window[i]
		if cLine == "" || isPromptLine(cLine) {
			continue
		}
		if isHintLine(cLine) {
			hasConfirmHint = true
			continue
		}
		if opt, ok := parseNumberedOption(cLine); ok {
			if firstOptionIdx == -1 {
				firstOptionIdx = i
			}
			numberedOpts = append(numberedOpts, opt)
			for _, phrase := range permissionChoices {
				if strings.Contains(opt, phrase) {
					hasPermChoice = true
					break
				}
			}
		}
	}

	var isMatch bool
	if requireHeaderWithChoiceOrHint {
		isMatch = headerIdx != -1 && (hasPermChoice || (hasConfirmHint && len(numberedOpts) > 0))
	} else {
		isMatch = headerIdx != -1 || hasPermChoice || (hasConfirmHint && len(numberedOpts) > 0)
	}
	if !isMatch {
		return "", "", nil, false
	}

	if matchedHeader != "" {
		question = matchedHeader
	} else if hasSubagentApprovalBanner {
		question = "Subagent requires permission"
	} else {
		question = "Tool requires permission"
	}
	if len(numberedOpts) > 0 {
		options = numberedOpts
	} else {
		options = []string{"1. Yes", "2. Yes, and always allow", "3. No"}
	}

	reason = extractPanePermissionReason(window, headerIdx, firstOptionIdx, subagentAlertReason)
	return question, reason, options, true
}

func extractPanePermissionReason(window []string, headerIdx, firstOptionIdx int, subagentAlertReason string) string {
	reasonStart, reasonEnd := 0, len(window)
	if headerIdx != -1 {
		reasonStart = headerIdx
	}
	if firstOptionIdx != -1 {
		reasonEnd = firstOptionIdx
	}
	var kvDetails, ctxDetails []string
	for i := reasonStart; i < reasonEnd; i++ {
		cLine := window[i]
		if cLine == "" || isHintLine(cLine) || isPromptLine(cLine) {
			continue
		}
		if _, isOpt := parseNumberedOption(cLine); isOpt {
			continue
		}
		if strings.HasPrefix(cLine, "Requesting permission for:") {
			if after := strings.TrimSpace(strings.TrimPrefix(cLine, "Requesting permission for:")); after != "" {
				ctxDetails = append(ctxDetails, after)
			}
			continue
		}
		isHdr := false
		for _, hdr := range permissionHeaders {
			if cLine == hdr || cLine == "🔓 "+hdr || strings.TrimSpace(strings.TrimPrefix(cLine, "🔓")) == hdr {
				isHdr = true
				break
			}
		}
		if isHdr {
			continue
		}
		if isPermKeyValueLine(cLine) {
			kvDetails = append(kvDetails, cLine)
			continue
		}
		ctxDetails = append(ctxDetails, cLine)
	}
	switch {
	case len(kvDetails) > 0:
		return strings.Join(kvDetails, ", ")
	case len(ctxDetails) > 0:
		if len(ctxDetails) > 2 {
			ctxDetails = ctxDetails[:2]
		}
		return strings.Join(ctxDetails, " ")
	case subagentAlertReason != "":
		return subagentAlertReason
	default:
		return "Permission prompt active in terminal"
	}
}

func detectPermissionPrompt(lines []string, runningSubs bool) (question string, reason string, options []string, matched bool) {
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	tailRaw := agentutil.ExtractTailLinesSlice(nonEmpty, 25)
	if len(tailRaw) == 0 {
		return "", "", nil, false
	}
	cleaned := make([]string, len(tailRaw))
	for i, l := range tailRaw {
		cleaned[i] = cleanPaneLine(l)
	}

	lastIdlePromptIdx := -1
	minSearch := len(cleaned) - 6
	if minSearch < 0 {
		minSearch = 0
	}
	for i := len(cleaned) - 1; i >= minSearch; i-- {
		if isPromptLine(cleaned[i]) || isPromptLine(strings.TrimSpace(tailRaw[i])) {
			lastIdlePromptIdx = i
			break
		}
	}

	subagentAlertReason := ""
	hasSubagentApprovalBanner := false
	alertStart := len(cleaned) - 12
	if alertStart < 0 {
		alertStart = 0
	}
	for i := alertStart; i < len(cleaned); i++ {
		cLine := cleaned[i]
		aboveIdle := lastIdlePromptIdx != -1 && i <= lastIdlePromptIdx
		if strings.Contains(cLine, " needs approval for ") {
			if !aboveIdle || runningSubs || isSubagentAlertPrefix(cLine) {
				hasSubagentApprovalBanner = true
				subagentAlertReason = strings.TrimSpace(strings.TrimLeft(cLine, "●•⚠*>- "))
			}
		} else if strings.Contains(cLine, "⚠ Approval Required") && lastIdlePromptIdx != -1 && i < lastIdlePromptIdx {
			if runningSubs || isSubagentAlertPrefix(cLine) {
				hasSubagentApprovalBanner = true
				if subagentAlertReason == "" {
					subagentAlertReason = "Subagent approval required"
				}
			}
		}
	}

	postLines := cleaned
	if lastIdlePromptIdx != -1 {
		postLines = cleaned[lastIdlePromptIdx+1:]
	}
	if q, r, opts, ok := scanPermissionWindow(postLines, subagentAlertReason, hasSubagentApprovalBanner, false); ok {
		return q, r, opts, true
	}
	if lastIdlePromptIdx != -1 {
		bottomWindow := agentutil.ExtractTailLinesSlice(cleaned, 15)
		if q, r, opts, ok := scanPermissionWindow(bottomWindow, subagentAlertReason, hasSubagentApprovalBanner, true); ok {
			return q, r, opts, true
		}
	}
	if hasSubagentApprovalBanner {
		r := subagentAlertReason
		if r == "" {
			r = "Permission prompt active in terminal"
		}
		return "Subagent requires permission", r, []string{"1. Yes", "2. Yes, and always allow", "3. No"}, true
	}
	if lastIdlePromptIdx == -1 {
		for _, cLine := range postLines {
			if strings.Contains(cLine, "Are you sure?") || strings.Contains(cLine, "[y/N]") || strings.Contains(cLine, "[Y/n]") {
				return "Confirmation required", "Confirmation required", []string{"Yes", "No"}, true
			}
		}
	}
	return "", "", nil, false
}
