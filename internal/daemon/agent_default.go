package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ackbar/internal/agentutil"
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

func (a defaultAgentAdapter) DetectRateLimit(text string, now time.Time) (bool, time.Time, string) {
	return agentutil.DetectDefaultRateLimitAt(text, now)
}

func isAckbarInternalProcess(binLower, cmdLower string) bool {
	return agentutil.IsAckbarProcess(binLower, cmdLower)
}

func checkIgnoredCLIArgs(fullCmd string, matchBin func(field, base string) bool, ignoredSubcommands map[string]bool, extraFlags map[string]bool) bool {
	return agentutil.CheckIgnoredCLIArgs(fullCmd, matchBin, ignoredSubcommands, extraFlags)
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

func ReadTail(path string, maxBytes int64) ([]byte, error) {
	return agentutil.ReadTail(path, maxBytes)
}

func readTail(path string, maxBytes int64) ([]byte, error) {
	return agentutil.ReadTail(path, maxBytes)
}

func readHead(path string, maxBytes int64) ([]byte, error) {
	return agentutil.ReadHead(path, maxBytes)
}

func TruncateTitle(text string) string {
	return agentutil.TruncateTitle(text, 50)
}

func truncateTitle(text string) string {
	return agentutil.TruncateTitle(text, 50)
}

func fileExists(p string) bool {
	return agentutil.FileExists(p)
}

func isValidUUID(u string) bool {
	return agentutil.IsUUID(u)
}

func isUUID(s string) bool {
	return agentutil.IsUUID(s)
}

func IsUUID(s string) bool {
	return agentutil.IsUUID(s)
}

func isRawSessionName(n string) bool {
	return agentutil.IsRawSessionName(n)
}

func IsRawSessionName(n string) bool {
	return agentutil.IsRawSessionName(n)
}
