package agentutil

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"ackbar/internal/agent"
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
	ValidServerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	mcpAuthErrorRegex    = regexp.MustCompile(`(?i)(\b401\b|unauthorized|unauthenticated|invalid_token|token\s+expired|authentication\s+required|requires\s+authentication|needs\s+authentication|not\s+authenticated|not\s+logged\s+in|oauth[^\n]*login)`)

	agyLoginRegex     = regexp.MustCompile(`(?i)(agy\s+acp\s+--login|open this link in a browser to log in with google|antigravity.*not logged in|please log in to antigravity)`)
	claudeLoginRegex  = regexp.MustCompile(`(?i)(not logged in.*(?:\/login|please run \/login)|please run \/login|oauth token has expired.*login)`)
	grokLoginRegex    = regexp.MustCompile(`(?i)(grok\s+login|XAI_API_KEY|authentication_failed|not\s+logged\s+in)`)
	mcpTurnErrorRegex = regexp.MustCompile(`(?i)mcp\s+server\s+['"‘“]?([a-zA-Z0-9][a-zA-Z0-9_-]{0,63})['"’”]?\s+(?:requires\s+authentication|is\s+not\s+authenticated|needs\s+authentication|unauthorized|\b401\b)`)
)

func DetectToolResultAuthBlock(agentName, toolName string, toolInput map[string]any, toolOutput string, isError bool) *agent.Blocked {
	toolLower := strings.ToLower(strings.TrimSpace(toolName))
	if readSearchEditTools[toolLower] {
		return nil
	}

	isShell := shellTools[toolLower]
	if isShell && exitZeroRegex.MatchString(toolOutput) {
		return nil
	}
	if !(isError || (isShell && exitNonZeroRegex.MatchString(toolOutput))) {
		return nil
	}
	if scope403Regex.MatchString(toolOutput) && !authLoginPromptRegex.MatchString(toolOutput) {
		return nil
	}

	if isShell {
		for _, plugin := range knownWorkspacePlugins {
			if matchesPluginAuthPrompt(toolOutput, plugin) {
				return newPluginAuthBlocked(plugin)
			}
		}
		return nil
	}

	server := extractMCPServerName(toolName, toolInput)
	if server != "" && ValidServerNameRegex.MatchString(server) && mcpAuthErrorRegex.MatchString(toolOutput) {
		return newMCPAuthBlocked(server)
	}
	return nil
}

func DetectTurnAuthBlock(agentName, text string) *agent.Blocked {
	if agyLoginRegex.MatchString(text) {
		return &agent.Blocked{
			Kind:       agent.BlockAuth,
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
		return &agent.Blocked{
			Kind:       agent.BlockAuth,
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
	if (agentName == "grok" || strings.Contains(agentName, "grok")) && grokLoginRegex.MatchString(text) {
		return &agent.Blocked{
			Kind:       agent.BlockAuth,
			AuthKind:   "agent",
			AuthTarget: "agent:grok",
			AuthServer: "grok",
			AuthHint:   "Grok CLI requires sign-in via 'grok login' or XAI_API_KEY.",
			Reason:     "Grok CLI requires sign-in.",
			Question:   "Authenticate Grok CLI in terminal?",
			Options:    []string{"Take Wheel", "Retry", "Dismiss"},
			Since:      time.Now(),
		}
	}
	if m := mcpTurnErrorRegex.FindStringSubmatch(text); len(m) > 1 {
		if server := m[1]; ValidServerNameRegex.MatchString(server) {
			return newMCPAuthBlocked(server)
		}
	}
	for _, plugin := range knownWorkspacePlugins {
		if matchesPluginAuthPrompt(text, plugin) {
			return newPluginAuthBlocked(plugin)
		}
	}
	return nil
}

func newPluginAuthBlocked(plugin string) *agent.Blocked {
	return &agent.Blocked{
		Kind:       agent.BlockAuth,
		AuthKind:   "plugin",
		AuthTarget: "plugin:" + plugin,
		AuthServer: plugin,
		Reason:     fmt.Sprintf("Google Workspace plugin '%s' requires sign-in.", plugin),
		Question:   fmt.Sprintf("Sign in to %s to continue?", plugin),
		Options:    []string{"Sign in", "Retry", "Dismiss"},
		Since:      time.Now(),
	}
}

func newMCPAuthBlocked(server string) *agent.Blocked {
	return &agent.Blocked{
		Kind:       agent.BlockAuth,
		AuthKind:   "mcp",
		AuthTarget: "mcp:" + server,
		AuthServer: server,
		Reason:     fmt.Sprintf("MCP server '%s' requires authentication.", server),
		Question:   fmt.Sprintf("Authenticate MCP server '%s'?", server),
		Options:    []string{"Sign in", "Retry", "Dismiss"},
		Since:      time.Now(),
	}
}

func extractMCPServerName(toolName string, toolInput map[string]any) string {
	if strings.HasPrefix(toolName, "mcp__") {
		if parts := strings.Split(toolName, "__"); len(parts) >= 3 && parts[1] != "" {
			return parts[1]
		}
	}
	if strings.HasPrefix(toolName, "mcp_") {
		if parts := strings.Split(toolName, "_"); len(parts) >= 3 && parts[1] != "" {
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
	outLow := strings.ToLower(output)
	if !strings.Contains(outLow, strings.ToLower(plugin)) {
		return false
	}
	return strings.Contains(outLow, "auth login") ||
		strings.Contains(outLow, "no credentials found") ||
		strings.Contains(outLow, "not authenticated") ||
		strings.Contains(outLow, "token expired") ||
		strings.Contains(outLow, "token has expired")
}
