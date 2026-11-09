package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type defaultAgentAdapter struct {
	agent string
}

func (a defaultAgentAdapter) GetResetCommand() string {
	return "/clear"
}

func (a defaultAgentAdapter) GetApprovalKeys(allow bool) []string {
	if allow {
		return []string{"y", "Enter"}
	}
	return []string{"n", "Enter"}
}

func (a defaultAgentAdapter) AccountEnvVars(home string, acc *AgentAccount) map[string]string {
	if acc == nil {
		return nil
	}
	envVars := make(map[string]string)
	for k, v := range acc.Env {
		envVars[k] = v
	}
	if acc.Name != "" {
		envVars["ACKBAR_ACCOUNT"] = acc.Name
	}
	return envVars
}

func (a defaultAgentAdapter) IsAccountLoggedIn(home string, acc *AgentAccount) bool {
	return true
}

func (a defaultAgentAdapter) SetupProfileDir(home, name, configDir string) (string, error) {
	agentSlug := a.agent
	if agentSlug == "" {
		agentSlug = "agent"
	}
	if configDir == "" {
		configDir = filepath.Join(home, "."+agentSlug+"-profiles", name)
	} else if strings.HasPrefix(configDir, "~/") && home != "" {
		configDir = filepath.Join(home, configDir[2:])
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return configDir, fmt.Errorf("failed to create profile directory: %w", err)
	}
	return configDir, nil
}

func (a defaultAgentAdapter) GetLoginCommand() string {
	if a.agent != "" {
		return a.agent + " login"
	}
	return "claude login"
}

func (a defaultAgentAdapter) IsValidNativeID(nativeID string) bool {
	return nativeID != ""
}

func (a defaultAgentAdapter) IsIgnoredCommand(fullCmd string) bool {
	return false
}

func (a defaultAgentAdapter) MatchProcessCommand(binLower, cmdLower string) bool {
	return false
}

func (a defaultAgentAdapter) RequiresResolvedSessionID() bool {
	return false
}

func (a defaultAgentAdapter) ResolveSessionForPID(ctx context.Context, home string, pid int, cmdFields []string) string {
	return ""
}

func (a defaultAgentAdapter) ResolveSessionOwner(home, sessionID string) (pid int, tmuxName string) {
	return 0, ""
}

func (a defaultAgentAdapter) HasConversationArtifacts(home, nativeID string) bool {
	return false
}

func (a defaultAgentAdapter) IsSubagentSession(home, nativeID string, precomputed map[string]bool) bool {
	return false
}

func (a defaultAgentAdapter) ListWorkspaceDocuments(home, cwd, nativeID string, addDoc func(fullPath, title, relPath, category, categoryLabel string, priority int)) {
}

func (a defaultAgentAdapter) ScanDiskSessions(ctx context.Context, s *Server, home string, state *ScanState) {
}

func (a defaultAgentAdapter) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	clean := strings.TrimSpace(content)
	if clean == "" {
		return "", false
	}
	return clean, true
}

func cleanCLIEnvironmentTags(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	xmlTags := []string{
		"environment_context",
		"recommended_plugins",
		"permissions instructions",
		"permissions_instructions",
		"collaboration_mode",
		"apps_instructions",
		"plugins_instructions",
		"skills_instructions",
		"multi_agent_mode",
	}

	clean := trimmed
	for _, tag := range xmlTags {
		startTag := "<" + tag + ">"
		endTag := "</" + tag + ">"
		for {
			sIdx := strings.Index(clean, startTag)
			if sIdx == -1 {
				break
			}
			relEnd := strings.Index(clean[sIdx+len(startTag):], endTag)
			if relEnd == -1 {
				clean = clean[:sIdx]
				break
			}
			eIdx := sIdx + len(startTag) + relEnd
			clean = clean[:sIdx] + clean[eIdx+len(endTag):]
		}
	}

	return strings.TrimSpace(clean)
}

func filterCLIUserEntry(content string) (string, bool) {
	clean := cleanCLIEnvironmentTags(content)
	if clean == "" {
		return "", false
	}
	return clean, true
}

// CodexAgentAdapter implements AgentAdapter for OpenAI Codex.
type CodexAgentAdapter struct {
	defaultAgentAdapter
}

var codexIgnoredSubcommands = map[string]bool{
	"login": true, "logout": true, "auth": true, "mcp": true, "update": true, "upgrade": true,
}

func (a CodexAgentAdapter) SetupProfileDir(home, name, configDir string) (string, error) {
	return (defaultAgentAdapter{agent: "codex"}).SetupProfileDir(home, name, configDir)
}

func (a CodexAgentAdapter) GetLoginCommand() string {
	return "codex login"
}

func (a CodexAgentAdapter) IsIgnoredCommand(fullCmd string) bool {
	return checkIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "codex"
	}, codexIgnoredSubcommands, nil)
}

func (a CodexAgentAdapter) MatchProcessCommand(binLower, cmdLower string) bool {
	return binLower == "codex" && !isAckbarInternalProcess(binLower, cmdLower)
}

func (a CodexAgentAdapter) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	return filterCLIUserEntry(content)
}

// GrokAgentAdapter implements AgentAdapter for xAI Grok.
type GrokAgentAdapter struct {
	defaultAgentAdapter
}

var grokIgnoredSubcommands = map[string]bool{
	"login": true, "logout": true, "auth": true, "update": true, "upgrade": true, "models": true,
}

func (a GrokAgentAdapter) SetupProfileDir(home, name, configDir string) (string, error) {
	return (defaultAgentAdapter{agent: "grok"}).SetupProfileDir(home, name, configDir)
}

func (a GrokAgentAdapter) GetLoginCommand() string {
	return "grok login"
}

func (a GrokAgentAdapter) IsIgnoredCommand(fullCmd string) bool {
	return checkIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "grok" || base == "grok-cli"
	}, grokIgnoredSubcommands, nil)
}

func (a GrokAgentAdapter) MatchProcessCommand(binLower, cmdLower string) bool {
	return (binLower == "grok" || binLower == "grok-cli") && !isAckbarInternalProcess(binLower, cmdLower)
}

func (a GrokAgentAdapter) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	return filterCLIUserEntry(content)
}

// OpenCodeAgentAdapter implements AgentAdapter for OpenCode.
type OpenCodeAgentAdapter struct {
	defaultAgentAdapter
}

var openCodeIgnoredSubcommands = map[string]bool{
	"login": true, "logout": true, "auth": true, "update": true, "upgrade": true, "models": true, "config": true,
}

func (a OpenCodeAgentAdapter) SetupProfileDir(home, name, configDir string) (string, error) {
	return (defaultAgentAdapter{agent: "opencode"}).SetupProfileDir(home, name, configDir)
}

func (a OpenCodeAgentAdapter) GetLoginCommand() string {
	return "opencode login"
}

func (a OpenCodeAgentAdapter) IsIgnoredCommand(fullCmd string) bool {
	return checkIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "opencode" || base == "open-code"
	}, openCodeIgnoredSubcommands, nil)
}

func (a OpenCodeAgentAdapter) MatchProcessCommand(binLower, cmdLower string) bool {
	return (binLower == "opencode" || binLower == "open-code") && !isAckbarInternalProcess(binLower, cmdLower)
}

func (a OpenCodeAgentAdapter) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	return filterCLIUserEntry(content)
}

func isAckbarInternalProcess(binLower, cmdLower string) bool {
	if strings.HasPrefix(binLower, "ackbar") {
		return true
	}
	fields := strings.Fields(cmdLower)
	for i := 0; i < len(fields) && i < 3; i++ {
		f := fields[i]
		if strings.HasPrefix(f, "-") {
			continue
		}
		base := filepath.Base(f)
		if base == "sh" || base == "bash" || base == "zsh" || base == "env" {
			continue
		}
		if strings.HasPrefix(base, "ackbar") {
			return true
		}
		break
	}
	return false
}

var interactiveSessionValueFlags = map[string]bool{
	"--session-id":      true,
	"-s":                true,
	"--resume":          true,
	"-r":                true,
	"--conversation":    true,
	"-c":                true,
	"--conversation-id": true,
}

var cliValueFlags = map[string]bool{
	"--model":                true,
	"-m":                     true,
	"--permission-mode":      true,
	"--effort":               true,
	"--name":                 true,
	"-n":                     true,
	"--add-dir":              true,
	"--allowedtools":         true,
	"--disallowedtools":      true,
	"--append-system-prompt": true,
	"--system-prompt":        true,
	"--settings":             true,
	"--mcp-config":           true,
	"--agents":               true,
	"--agent":                true,
	"--worktree":             true,
	"-w":                     true,
}

func checkIgnoredCLIArgs(fullCmd string, matchBin func(field, base string) bool, ignoredSubcommands map[string]bool, extraFlags map[string]bool) bool {
	fields := strings.Fields(fullCmd)
	if len(fields) == 0 {
		return false
	}

	argIdx := -1
	for i, f := range fields {
		base := strings.ToLower(filepath.Base(f))
		if matchBin(f, base) {
			argIdx = i + 1
			break
		}
	}

	if argIdx == -1 || argIdx >= len(fields) {
		return false
	}

	args := fields[argIdx:]
	hasSessionFlag := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		a := strings.ToLower(strings.TrimSpace(arg))
		if strings.HasPrefix(a, "-") {
			if a == "-v" || a == "--version" || a == "-h" || a == "--help" || a == "-p" || a == "--print" {
				return true
			}
			if extraFlags != nil && extraFlags[a] {
				return true
			}
			if a == "-i" || a == "--prompt-interactive" {
				// Interactive prompt flag: subsequent tokens belong to the initial prompt string.
				return false
			}
			if interactiveSessionValueFlags[a] {
				hasSessionFlag = true
				i++
				continue
			}
			if cliValueFlags[a] {
				i++
				continue
			}
			continue
		}

		if !hasSessionFlag {
			if ignoredSubcommands != nil && ignoredSubcommands[a] {
				return true
			}
		}
		// Once the first positional argument (interactive prompt) starts, remaining tokens are prompt text.
		return false
	}

	return false
}

type TitleCacheEntry struct {
	Title     string
	Source    string // "custom", "ai", "prompt", "fallback"
	UpdatedAt time.Time
}

var (
	titleCacheMutex sync.RWMutex
	titleCache      = make(map[string]TitleCacheEntry)
)

// ReadTail reads up to maxBytes from the end of a file to prevent high memory allocations
func ReadTail(path string, maxBytes int64) ([]byte, error) {
	return readTail(path, maxBytes)
}

// readTail reads up to maxBytes from the end of a file to prevent high memory allocations
func readTail(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	size := stat.Size()
	offset := int64(0)
	if size > maxBytes {
		offset = size - maxBytes
	}

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	return io.ReadAll(file)
}

// readHead reads up to maxBytes from the beginning of a file to prevent reading multi-megabyte files
func readHead(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := make([]byte, maxBytes)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

func TruncateTitle(text string) string {
	return truncateTitle(text)
}

func truncateTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	cleanLines := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			cleanLines = append(cleanLines, l)
		}
	}
	if len(cleanLines) == 0 {
		return ""
	}
	res := cleanLines[0]
	if len(res) < 6 && len(cleanLines) > 1 {
		res = res + " " + cleanLines[1]
	}
	if len(res) > 50 {
		return res[:47] + "..."
	}
	return res
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func isValidUUID(u string) bool {
	return IsUUID(u)
}

func isUUID(s string) bool {
	return IsUUID(s)
}

// IsUUID returns true if s matches standard 36-character hyphenated UUID format
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

func isRawSessionName(n string) bool {
	return IsRawSessionName(n)
}

// IsRawSessionName returns true if n is empty, agent identifier, raw UUID, or generic placeholder
func IsRawSessionName(n string) bool {
	n = strings.TrimSpace(n)
	n = strings.TrimSuffix(n, ":")
	n = strings.TrimSpace(n)
	if n == "" || n == "antigravity" || n == "claude-code" || n == "codex" || n == "grok" || n == "opencode" || n == "cli" || n == "mock-agent" {
		return true
	}
	if strings.HasPrefix(n, "ackbar-") || strings.HasPrefix(n, "proc-") || IsUUID(n) {
		return true
	}
	if strings.HasPrefix(n, "antigravity (") || strings.HasPrefix(n, "claude-code (") || strings.HasPrefix(n, "codex (") || strings.HasPrefix(n, "grok (") || strings.HasPrefix(n, "opencode (") || strings.HasPrefix(n, "Claude Code (") || strings.HasPrefix(n, "Antigravity (") || strings.HasPrefix(n, "xAI Grok (") || strings.HasPrefix(n, "OpenCode (") {
		return true
	}
	if isGenericDirSlug(n) {
		return true
	}
	return false
}

func isGenericDirSlug(n string) bool {
	lastHyphen := strings.LastIndex(n, "-")
	if lastHyphen <= 0 || lastHyphen == len(n)-1 {
		return false
	}
	numPart := n[lastHyphen+1:]
	for _, c := range numPart {
		if c < '0' || c > '9' {
			return false
		}
	}
	prefix := n[:lastHyphen]
	// Match lowercase directory slugs with no spaces (e.g. ngl-android-23, modemobile-1, skip2q-4)
	if prefix == strings.ToLower(prefix) && !strings.Contains(prefix, " ") && !strings.Contains(prefix, "_") {
		return true
	}
	return false
}
