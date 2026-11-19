package antigravity

import (
	"fmt"
	"strings"
)

const borderChars = " │┃┌┍┎┏┐┑┒┓└┕┖┗┘┙┚┛├┝┞┟┠┣┤┥┦┧┨┫┬┭┮┯┰┱┲┳┴┵┶┷┸┹┺┻┼┽┾┿╀╁╂╃╄╅╆╇╈╉╊╋─━╭╮╰╯"

var permissionHeaders = []string{
	"Run this command?",
	"Accept this file edit?",
	"Allow creation of this file?",
	"Allow access to this file?",
	"Allow access to this URL?",
	"Allow sandbox bypass for command execution?",
	"Allow administrator elevation?",
	"Grant requested permission?",
	"Expanded Tool Permission Editor",
	"Requested Permission (Editable):",
	"Requesting permission for:",
	"Do you want to proceed?",
	"⚠ Approval Required",
}

var permissionChoices = []string{
	"Yes, run command",
	"Yes, accept this change",
	"No, reject this change",
	"Yes, allow access",
	"No, deny access",
	"Yes, allow creation",
	"No, deny creation",
	"Yes, allow tool call",
	"No, deny tool call",
	"Yes, grant permission",
	"No, deny permission",
	"Yes, elevate",
	"Yes, approve",
	"Yes, proceed",
	"No, and tell Antigravity CLI what to do differently",
}

func isPromptLine(trimmed string) bool {
	trimmed = strings.TrimSpace(strings.ReplaceAll(trimmed, "\u00a0", " "))
	return trimmed == ">" || trimmed == "> " ||
		trimmed == "> Accept-edits" || trimmed == "> Plan mode" || trimmed == "> Auto mode"
}

func hasPromptInTail(nonEmpty []string, n int) bool {
	for i := len(nonEmpty) - 1; i >= 0 && i >= len(nonEmpty)-n; i-- {
		if isPromptLine(nonEmpty[i]) {
			return true
		}
	}
	return false
}

func hasSpinnerText(text string) bool {
	for _, tok := range []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏", "⢿", "Thinking...", "Running command...", "Running tool:"} {
		if strings.Contains(text, tok) {
			return true
		}
	}
	return false
}

func cleanPaneLine(line string) string {
	s := strings.ReplaceAll(line, "\u00a0", " ")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, borderChars)
	return strings.TrimSpace(s)
}

func parseNumberedOption(cleanLine string) (string, bool) {
	s := strings.TrimSpace(cleanLine)
	for _, prefix := range []string{"> ", "❯ ", ">", "❯"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
			break
		}
	}
	if len(s) > 3 && s[0] >= '1' && s[0] <= '9' && s[1] == '.' && s[2] == ' ' {
		return s, true
	}
	return "", false
}

func isHintLine(cleanLine string) bool {
	return strings.Contains(cleanLine, "Enter Submit") || strings.Contains(cleanLine, "Tab Amend")
}

func isSubagentAlertPrefix(cLine string) bool {
	return strings.HasPrefix(cLine, "●") || strings.HasPrefix(cLine, "•") || strings.HasPrefix(cLine, "⚠")
}

func isPermKeyValueLine(cLine string) bool {
	for _, pfx := range []string{"Edit:", "New file:", "File:", "Path:", "URL:", "Action:", "Target:", "Command:", "Reason:"} {
		if strings.HasPrefix(cLine, pfx) {
			return true
		}
	}
	return false
}

func getStringArg(args map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		for k, v := range args {
			if strings.EqualFold(k, key) {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}

func extractPermissionReason(args map[string]interface{}) string {
	if len(args) == 0 {
		return "Waiting for tool permission approval"
	}
	action := getStringArg(args, "action", "Action")
	target := getStringArg(args, "target", "Target")
	if action != "" && target != "" {
		return fmt.Sprintf("%s (%s)", action, target)
	}
	if cmd := getStringArg(args, "command", "CommandLine", "command_line", "cmd"); cmd != "" {
		return cmd
	}
	if reason := getStringArg(args, "reason", "Reason", "description", "Description", "message", "Message"); reason != "" {
		return reason
	}
	if toolName := getStringArg(args, "tool", "Tool", "tool_name", "toolName"); toolName != "" {
		if target != "" {
			return fmt.Sprintf("%s (%s)", toolName, target)
		}
		return toolName
	}
	if action != "" {
		return action
	}
	if target != "" {
		return target
	}
	if path := getStringArg(args, "path", "Path", "AbsolutePath", "file_path", "filePath", "TargetFile"); path != "" {
		return path
	}
	if url := getStringArg(args, "url", "Url", "URL"); url != "" {
		return url
	}
	return "Waiting for tool permission approval"
}
