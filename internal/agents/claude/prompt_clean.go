package claude

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	systemReminderRegex   = regexp.MustCompile(`(?s)<system-reminder>[\s\S]*?</system-reminder>`)
	localCmdCaveatRegex   = regexp.MustCompile(`(?s)<local-command-caveat>[\s\S]*?</local-command-caveat>`)
	ideOpenedFileRegex    = regexp.MustCompile(`(?s)<ide_opened_file>[\s\S]*?</ide_opened_file>`)
	ideSelectionRegex     = regexp.MustCompile(`(?s)<ide_selection>[\s\S]*?</ide_selection>`)
	commandMsgRegex       = regexp.MustCompile(`(?s)<command-message>[\s\S]*?</command-message>`)
	commandNameRegex      = regexp.MustCompile(`(?s)<command-name>([^<]+)</command-name>`)
	commandArgsRegex      = regexp.MustCompile(`(?s)<command-args>([^<]*)</command-args>`)
	bashInputRegex        = regexp.MustCompile(`(?s)<bash-input>([\s\S]*?)</bash-input>`)
	bashStdoutRegex       = regexp.MustCompile(`(?s)<bash-stdout>([\s\S]*?)</bash-stdout>`)
	bashStderrRegex       = regexp.MustCompile(`(?s)<bash-stderr>([\s\S]*?)</bash-stderr>`)
	localCmdStdoutRegex   = regexp.MustCompile(`(?s)<local-command-stdout>([\s\S]*?)</local-command-stdout>`)
	taskNotificationRegex = regexp.MustCompile(`(?s)<task-notification>([\s\S]*?)</task-notification>`)
	taskSummaryRegex      = regexp.MustCompile(`(?s)<summary>([\s\S]*?)</summary>`)
	taskStatusRegex       = regexp.MustCompile(`(?s)<status>([\s\S]*?)</status>`)
	multiNewlinesRegex    = regexp.MustCompile(`\n{3,}`)
)

func CleanPrompt(raw string) string {
	if raw == "" {
		return ""
	}
	result := systemReminderRegex.ReplaceAllString(raw, "")
	result = localCmdCaveatRegex.ReplaceAllString(result, "")
	result = ideOpenedFileRegex.ReplaceAllString(result, "")
	result = ideSelectionRegex.ReplaceAllString(result, "")
	result = commandMsgRegex.ReplaceAllString(result, "")

	if cmdMatch := commandNameRegex.FindStringSubmatch(result); len(cmdMatch) > 1 {
		cmdName := strings.TrimSpace(cmdMatch[1])
		cmdArgs := ""
		if argsMatch := commandArgsRegex.FindStringSubmatch(result); len(argsMatch) > 1 {
			cmdArgs = strings.TrimSpace(argsMatch[1])
		}
		result = commandNameRegex.ReplaceAllString(result, "")
		result = commandArgsRegex.ReplaceAllString(result, "")

		formattedCmd := "/" + strings.TrimPrefix(cmdName, "/")
		if cmdArgs != "" {
			formattedCmd += " " + cmdArgs
		}
		if trimmedRemaining := strings.TrimSpace(result); trimmedRemaining != "" {
			result = formattedCmd + "\n\n" + trimmedRemaining
		} else {
			result = formattedCmd
		}
	}

	result = bashInputRegex.ReplaceAllStringFunc(result, func(match string) string {
		if sub := bashInputRegex.FindStringSubmatch(match); len(sub) > 1 {
			if input := strings.TrimSpace(sub[1]); input != "" {
				return fmt.Sprintf("```bash\n$ %s\n```", input)
			}
		}
		return ""
	})

	formatOutputBlock := func(match string, re *regexp.Regexp) string {
		if sub := re.FindStringSubmatch(match); len(sub) > 1 {
			if out := strings.TrimSpace(sub[1]); out != "" {
				return fmt.Sprintf("```\n%s\n```", out)
			}
		}
		return ""
	}
	result = bashStdoutRegex.ReplaceAllStringFunc(result, func(m string) string { return formatOutputBlock(m, bashStdoutRegex) })
	result = bashStderrRegex.ReplaceAllStringFunc(result, func(m string) string { return formatOutputBlock(m, bashStderrRegex) })
	result = localCmdStdoutRegex.ReplaceAllStringFunc(result, func(m string) string { return formatOutputBlock(m, localCmdStdoutRegex) })

	result = taskNotificationRegex.ReplaceAllStringFunc(result, func(match string) string {
		if sub := taskNotificationRegex.FindStringSubmatch(match); len(sub) > 1 {
			inner := sub[1]
			var summary, status string
			if sm := taskSummaryRegex.FindStringSubmatch(inner); len(sm) > 1 {
				summary = strings.TrimSpace(sm[1])
			}
			if stm := taskStatusRegex.FindStringSubmatch(inner); len(stm) > 1 {
				status = strings.TrimSpace(stm[1])
			}
			if summary != "" && status != "" {
				return fmt.Sprintf("[Task %s: %s]", status, summary)
			}
			if summary != "" {
				return fmt.Sprintf("[Task: %s]", summary)
			}
		}
		return ""
	})

	result = multiNewlinesRegex.ReplaceAllString(result, "\n\n")
	result = strings.ReplaceAll(result, "```\n```", "```\n\n```")
	return strings.TrimSpace(result)
}

func IsSyntheticUserEntry(raw map[string]interface{}, content string) bool {
	isExplicitHuman := false
	if raw != nil {
		for _, k := range []string{"isMeta", "isCompactSummary", "isVisibleInTranscriptOnly", "turnCompanion"} {
			if b, _ := raw[k].(bool); b {
				return true
			}
		}
		if origin, ok := raw["origin"].(map[string]interface{}); ok {
			if kind, _ := origin["kind"].(string); kind != "" {
				if kind != "human" {
					return true
				}
				isExplicitHuman = true
			}
		}
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || isExplicitHuman {
		return false
	}
	if strings.Contains(trimmed, "<EXTREMELY_IMPORTANT>") {
		return true
	}
	for _, prefix := range []string{
		"<task-notification>",
		"<teammate-message",
		"<cross-session-message",
		"Another Claude session sent a message:",
		"[Image: original ",
		"[Cross-session ",
		"[Request interrupted by user",
		"Background task notification received.",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func (*Plugin) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	if IsSyntheticUserEntry(raw, content) {
		return "", false
	}
	clean := CleanPrompt(content)
	if clean == "" || IsSyntheticUserEntry(raw, clean) {
		return "", false
	}
	return clean, true
}

func extractUserMessageText(raw map[string]interface{}) string {
	if msgObj, ok := raw["message"].(map[string]interface{}); ok {
		if contentStr, ok := msgObj["content"].(string); ok {
			return contentStr
		}
		if contentArr, ok := msgObj["content"].([]interface{}); ok {
			var parts []string
			for _, item := range contentArr {
				if cMap, ok := item.(map[string]interface{}); ok {
					if cType, _ := cMap["type"].(string); cType == "text" {
						if txt, _ := cMap["text"].(string); txt != "" {
							parts = append(parts, txt)
						}
					}
				} else if s, ok := item.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	if s, ok := raw["content"].(string); ok {
		return s
	}
	if s, ok := raw["text"].(string); ok {
		return s
	}
	return ""
}
