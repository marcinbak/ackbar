package daemon

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	readSearchEditTools = map[string]bool{
		"read": true, "read_file": true, "grep": true, "grep_search": true,
		"glob": true, "find_by_name": true, "write": true, "write_file": true,
		"edit": true, "edit_file": true, "multiedit": true, "notebookedit": true,
		"ls": true, "list_dir": true, "todowrite": true, "taskoutput": true,
	}

	shellTools = map[string]bool{
		"bash": true, "run_command": true, "shell": true,
		"execute_command": true, "command_execution": true,
	}

	knownWorkspacePlugins = []string{"gslides", "gdocs", "gdrive", "gsheets"}

	exitZeroRegex        = regexp.MustCompile(`(?m)^Command exited with code 0\.`)
	exitNonZeroRegex     = regexp.MustCompile(`(?m)^Command exited with code [1-9]\d*\.`)
	scope403Regex        = regexp.MustCompile(`(?i)(insufficientPermissions|PERMISSION_DENIED|insufficient authentication scopes|missing permission)`)
	authLoginPromptRegex = regexp.MustCompile(`(?i)\bauth\s+login\b`)
	validServerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	mcpAuthErrorRegex    = regexp.MustCompile(`(?i)(\b401\b|unauthorized|unauthenticated|invalid_token|token\s+expired|authentication\s+required|requires\s+authentication|needs\s+authentication|not\s+authenticated|not\s+logged\s+in|oauth[^\n]*login)`)

	agyLoginRegex     = regexp.MustCompile(`(?i)(agy\s+acp\s+--login|open this link in a browser to log in with google|antigravity.*not logged in|please log in to antigravity)`)
	claudeLoginRegex  = regexp.MustCompile(`(?i)(not logged in.*(?:\/login|please run \/login)|please run \/login|oauth token has expired.*login)`)
	mcpTurnErrorRegex = regexp.MustCompile(`(?i)mcp\s+server\s+['"‘“]?([a-zA-Z0-9][a-zA-Z0-9_-]{0,63})['"’”]?\s+(?:requires\s+authentication|is\s+not\s+authenticated|needs\s+authentication|unauthorized|\b401\b)`)
)

func isShellTool(name string) bool {
	return shellTools[strings.ToLower(strings.TrimSpace(name))]
}

func DetectToolResultAuthBlock(agent, toolName string, toolInput map[string]any, toolOutput string, isError bool) *Blocked {
	toolLower := strings.ToLower(strings.TrimSpace(toolName))
	if readSearchEditTools[toolLower] {
		return nil
	}

	isShell := isShellTool(toolLower)
	if isShell && exitZeroRegex.MatchString(toolOutput) {
		return nil
	}
	effectiveError := isError || (isShell && exitNonZeroRegex.MatchString(toolOutput))
	if !effectiveError {
		return nil
	}

	if scope403Regex.MatchString(toolOutput) && !authLoginPromptRegex.MatchString(toolOutput) {
		return nil
	}

	if isShell {
		for _, plugin := range knownWorkspacePlugins {
			if matchesPluginAuthPrompt(toolOutput, plugin) {
				return &Blocked{
					Kind:       BlockAuth,
					AuthKind:   "plugin",
					AuthTarget: "plugin:" + plugin,
					AuthServer: plugin,
					Reason:     fmt.Sprintf("Google Workspace plugin '%s' requires sign-in.", plugin),
					Question:   fmt.Sprintf("Sign in to %s to continue?", plugin),
					Options:    []string{"Sign in", "Retry", "Dismiss"},
					Since:      time.Now(),
				}
			}
		}
		return nil
	}

	server := extractMCPServerName(toolName, toolInput)
	if server != "" && validServerNameRegex.MatchString(server) {
		if mcpAuthErrorRegex.MatchString(toolOutput) {
			return &Blocked{
				Kind:       BlockAuth,
				AuthKind:   "mcp",
				AuthTarget: "mcp:" + server,
				AuthServer: server,
				Reason:     fmt.Sprintf("MCP server '%s' requires authentication.", server),
				Question:   fmt.Sprintf("Authenticate MCP server '%s'?", server),
				Options:    []string{"Sign in", "Retry", "Dismiss"},
				Since:      time.Now(),
			}
		}
	}

	return nil
}

func extractMCPServerName(toolName string, toolInput map[string]any) string {
	if strings.HasPrefix(toolName, "mcp__") {
		parts := strings.Split(toolName, "__")
		if len(parts) >= 3 && parts[1] != "" {
			return parts[1]
		}
	}
	if strings.HasPrefix(toolName, "mcp_") {
		parts := strings.Split(toolName, "_")
		if len(parts) >= 3 && parts[1] != "" {
			return parts[1]
		}
	}
	if strings.EqualFold(strings.TrimSpace(toolName), "call_mcp_tool") && toolInput != nil {
		for _, k := range []string{"ServerName", "server_name", "server"} {
			if v, ok := toolInput[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func matchesPluginAuthPrompt(output, plugin string) bool {
	pLow := strings.ToLower(plugin)
	outLow := strings.ToLower(output)
	if !strings.Contains(outLow, pLow) {
		return false
	}
	hasLogin := strings.Contains(outLow, "auth login")
	hasNoCreds := strings.Contains(outLow, "no credentials found")
	hasNotAuth := strings.Contains(outLow, "not authenticated")
	hasExpired := strings.Contains(outLow, "token expired") || strings.Contains(outLow, "token has expired")
	return hasLogin || hasNoCreds || hasNotAuth || hasExpired
}

func DetectTurnAuthBlock(agent, text string) *Blocked {
	if agyLoginRegex.MatchString(text) {
		return &Blocked{
			Kind:       BlockAuth,
			AuthKind:   "agent",
			AuthTarget: "agent:agy",
			AuthServer: "antigravity",
			Reason:     "Antigravity CLI requires sign-in.",
			Question:   "Sign in to Antigravity to continue?",
			Options:    []string{"Sign in", "Retry", "Dismiss"},
			Since:      time.Now(),
		}
	}

	if claudeLoginRegex.MatchString(text) {
		return &Blocked{
			Kind:       BlockAuth,
			AuthKind:   "agent",
			AuthTarget: "agent:claude",
			AuthServer: "claude-code",
			AuthHint:   "Claude CLI requires interactive terminal sign-in.",
			Reason:     "Claude CLI requires interactive sign-in.",
			Question:   "Authenticate Claude CLI in terminal?",
			Options:    []string{"Take Wheel", "Retry", "Dismiss"},
			Since:      time.Now(),
		}
	}

	if m := mcpTurnErrorRegex.FindStringSubmatch(text); len(m) > 1 {
		server := m[1]
		if validServerNameRegex.MatchString(server) {
			return &Blocked{
				Kind:       BlockAuth,
				AuthKind:   "mcp",
				AuthTarget: "mcp:" + server,
				AuthServer: server,
				Reason:     fmt.Sprintf("MCP server '%s' requires authentication.", server),
				Question:   fmt.Sprintf("Authenticate MCP server '%s'?", server),
				Options:    []string{"Sign in", "Retry", "Dismiss"},
				Since:      time.Now(),
			}
		}
	}

	for _, plugin := range knownWorkspacePlugins {
		if matchesPluginAuthPrompt(text, plugin) {
			return &Blocked{
				Kind:       BlockAuth,
				AuthKind:   "plugin",
				AuthTarget: "plugin:" + plugin,
				AuthServer: plugin,
				Reason:     fmt.Sprintf("Google Workspace plugin '%s' requires sign-in.", plugin),
				Question:   fmt.Sprintf("Sign in to %s to continue?", plugin),
				Options:    []string{"Sign in", "Retry", "Dismiss"},
				Since:      time.Now(),
			}
		}
	}

	return nil
}

func detectTurnFailureBlock(agent, text string) *Blocked {
	if b := DetectTurnAuthBlock(agent, text); b != nil {
		return b
	}
	adapter := getDefaultAgentAdapter(agent)
	if adapter != nil {
		if limited, resetAt, reason := adapter.DetectRateLimit(text, time.Now()); limited {
			return &Blocked{
				Kind:     BlockRateLimit,
				Reason:   reason,
				Question: reason,
				ResetAt:  &resetAt,
				Options:  []string{fmt.Sprintf("Resume at %s", resetAt.Local().Format("15:04")), "Resume in 1h"},
				Since:    time.Now(),
			}
		}
	}
	return nil
}
