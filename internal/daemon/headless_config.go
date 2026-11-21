package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agent"
)

const DefaultHeadlessIdleTTL = 65 * time.Minute

func defaultSessionPermissionMode(agentName string) string {
	switch strings.ToLower(strings.TrimSpace(agentName)) {
	case "", "claude-code", "claude":
		return "bypassPermissions"
	default:
		return ""
	}
}

func defaultSessionPermissionModeWithRegistry(reg *agent.Registry, agentName string) string {
	if pd, ok := persistentDriverForAgent(reg, agentName); ok {
		return pd.DefaultPermissionMode()
	}
	if reg != nil && strings.TrimSpace(agentName) != "" {
		if _, ok := reg.AsHeadlessDriver(agentName); ok {
			return ""
		}
	}
	return defaultSessionPermissionMode(agentName)
}

func normalizePermissionMode(agentName, mode string) string {
	return normalizePermissionModeWithRegistry(nil, agentName, mode)
}

func normalizePermissionModeWithRegistry(reg *agent.Registry, agentName, mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		return "plan"
	case "auto":
		return "auto"
	case "acceptedits":
		return "acceptEdits"
	case "dontask":
		return "dontAsk"
	case "bypasspermissions":
		return "bypassPermissions"
	default:
		return defaultSessionPermissionModeWithRegistry(reg, agentName)
	}
}

func computeConfigFingerprint(sess *Session, includeModel bool) string {
	return computeConfigFingerprintWithRegistry(nil, sess, includeModel)
}

func computeConfigFingerprintWithRegistry(reg *agent.Registry, sess *Session, includeModel bool) string {
	if sess == nil {
		return ""
	}
	ag := strings.ToLower(strings.TrimSpace(sess.Agent))
	supportsEffort := ag == "claude-code" || ag == "grok"
	if p, ok := reg.Get(ag); ok {
		ag = strings.ToLower(strings.TrimSpace(p.Key()))
		supportsEffort = p.Capabilities().SupportsEffortLevel
	}
	m := ""
	if includeModel {
		m = strings.TrimSpace(sess.Model)
		if strings.EqualFold(m, "default") {
			m = ""
		}
	}
	e := strings.TrimSpace(sess.Effort)
	if !supportsEffort || strings.EqualFold(e, "default") {
		e = ""
	}
	pm := normalizePermissionModeWithRegistry(reg, sess.Agent, sess.PermissionMode)
	acc := strings.TrimSpace(sess.AccountID)
	cwd := filepath.Clean(strings.TrimSpace(sess.Cwd))
	sup := strconv.FormatBool(sess.IsSupervisor)

	h := sha256.New()
	parts := []string{ag, m, e, pm, acc, cwd, sup}
	if sess.YoloMode {
		parts = append(parts, "yolo:1")
	}
	for _, d := range sess.ExtraDirs {
		if cleaned := filepath.Clean(strings.TrimSpace(d)); cleaned != "" && cleaned != "." {
			parts = append(parts, "extra:"+cleaned)
		}
	}
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

func ConfigFingerprint(sess *Session) string {
	return computeConfigFingerprint(sess, true)
}

func ConfigFingerprintWithoutModel(sess *Session) string {
	return computeConfigFingerprint(sess, false)
}

func ConfigFingerprintWithRegistry(reg *agent.Registry, sess *Session) string {
	return computeConfigFingerprintWithRegistry(reg, sess, true)
}

func ConfigFingerprintWithoutModelWithRegistry(reg *agent.Registry, sess *Session) string {
	return computeConfigFingerprintWithRegistry(reg, sess, false)
}

func ResolveHeadlessIdleTTL(override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	if env := os.Getenv("ACKBAR_HEADLESS_IDLE_TTL"); env != "" {
		if d, err := time.ParseDuration(env); err == nil && d > 0 {
			return d
		}
	}
	return DefaultHeadlessIdleTTL
}

func buildPersistentArgs(sess *Session, isExistingConversation bool) (string, []string, error) {
	if sess == nil {
		return "", nil, fmt.Errorf("nil session")
	}

	if sess.Agent == "claude-code" {
		bin, err := resolveClaudeBinary()
		if err != nil {
			return "", nil, fmt.Errorf("failed to locate claude binary: %w", err)
		}
		args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--permission-prompt-tool", "stdio"}
		switch strings.ToLower(strings.TrimSpace(sess.PermissionMode)) {
		case "plan":
			args = append(args, "--permission-mode", "plan", "--allow-dangerously-skip-permissions")
		case "auto":
			args = append(args, "--permission-mode", "auto", "--allow-dangerously-skip-permissions")
		case "acceptedits":
			args = append(args, "--permission-mode", "acceptEdits", "--allow-dangerously-skip-permissions")
		case "dontask":
			args = append(args, "--permission-mode", "dontAsk", "--allow-dangerously-skip-permissions")
		default:
			args = append(args, "--dangerously-skip-permissions")
		}
		for _, d := range sess.ExtraDirs {
			if cleaned := filepath.Clean(strings.TrimSpace(d)); cleaned != "" && cleaned != "." {
				args = append(args, "--add-dir", cleaned)
			}
		}
		if isExistingConversation {
			args = append(args, "--resume", sess.NativeID)
		} else {
			args = append(args, "--session-id", sess.NativeID)
		}
		if sess.Model != "" && !strings.EqualFold(sess.Model, "default") {
			args = append(args, "--model", sess.Model)
		} else if isExistingConversation {
			// Pass --model default on resume so Claude does not remain pinned to the prior turn's transcript model.
			args = append(args, "--model", "default")
		}
		if sess.Effort != "" && !strings.EqualFold(sess.Effort, "default") {
			args = append(args, "--effort", sess.Effort)
		}
		if sess.IsSupervisor {
			args = append(args, "--append-system-prompt", BuildSupervisorSystemPrompt(sess.ID))
		}
		return bin, args, nil
	}

	return "", nil, ErrAgentUnsupported
}

type claudeStdinTurnMsg struct {
	Type    string             `json:"type"`
	Message stdinTurnMsgDetail `json:"message"`
}

type stdinTurnMsgDetail struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func formatPersistentStdinTurnWithRegistry(reg *agent.Registry, in agent.PersistentTurnInput) (string, error) {
	if pd, ok := persistentDriverForAgent(reg, in.Agent); ok {
		if in.IsSupervisor && in.SystemPrompt == "" && in.SessionID != "" {
			in.SystemPrompt = BuildSupervisorSystemPrompt(in.SessionID)
		}
		return pd.FormatStdinTurn(in)
	}
	return formatPersistentStdinTurn(in.Agent, in.SessionID, in.IsSupervisor, in.IsFirstTurn, in.Prompt)
}

func formatPersistentStdinTurn(agent, sessID string, isSupervisor, isFirstTurnOfNewConv bool, prompt string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude-code":
		payload := claudeStdinTurnMsg{
			Type: "user",
			Message: stdinTurnMsgDetail{
				Role:    "user",
				Content: prompt,
			},
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return "", ErrAgentUnsupported
	}
}

func buildWorkerEnv(sess *Session, db *DB, home string) []string {
	return buildWorkerEnvWithRegistry(nil, sess, db, home)
}

func augmentWorkerPluginEnv(env []string, home string) []string {
	geminiHome := ""
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], "GEMINI_CLI_HOME=") {
			geminiHome = strings.TrimPrefix(env[i], "GEMINI_CLI_HOME=")
			break
		}
	}

	var candidates []string
	if geminiHome != "" {
		candidates = append(candidates,
			filepath.Join(geminiHome, "antigravity-cli", "bin"),
			filepath.Join(geminiHome, ".gemini", "antigravity-cli", "bin"),
			filepath.Join(geminiHome, "antigravity", "bin"),
			filepath.Join(geminiHome, ".gemini", "antigravity", "bin"),
		)
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".grok", "bin"),
			filepath.Join(home, ".gemini", "antigravity-cli", "bin"),
			filepath.Join(home, ".gemini", "antigravity", "bin"),
		)
	}

	var validDirs []string
	seenDirs := make(map[string]bool)
	for _, dir := range candidates {
		if dir != "" && !seenDirs[dir] {
			seenDirs[dir] = true
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				validDirs = append(validDirs, dir)
			}
		}
	}

	pathIdx, hasAppDataDir := -1, false
	var existingParts []string
	existingSet := make(map[string]bool)
	for i, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			pathIdx = i
			for _, part := range strings.Split(strings.TrimPrefix(entry, "PATH="), string(os.PathListSeparator)) {
				if part != "" {
					existingParts, existingSet[part] = append(existingParts, part), true
				}
			}
		} else if strings.HasPrefix(entry, "ANTIGRAVITY_APP_DATA_DIR=") {
			hasAppDataDir = true
		}
	}

	var toPrepend []string
	for _, dir := range validDirs {
		if !existingSet[dir] {
			toPrepend, existingSet[dir] = append(toPrepend, dir), true
		}
	}
	if len(toPrepend) > 0 {
		newPath := strings.Join(append(toPrepend, existingParts...), string(os.PathListSeparator))
		if pathIdx >= 0 {
			env[pathIdx] = "PATH=" + newPath
		} else {
			env = append(env, "PATH="+newPath)
		}
	}

	if !hasAppDataDir && home != "" {
		_, errAntigravity := os.Stat(filepath.Join(home, ".gemini", "antigravity", "plugin_data"))
		statCliPlugin, errCliPlugin := os.Stat(filepath.Join(home, ".gemini", "antigravity-cli", "plugin_data"))
		statCliBin, errCliBin := os.Stat(filepath.Join(home, ".gemini", "antigravity-cli", "bin"))
		if os.IsNotExist(errAntigravity) && ((errCliPlugin == nil && statCliPlugin.IsDir()) || (errCliBin == nil && statCliBin.IsDir())) {
			env = append(env, "ANTIGRAVITY_APP_DATA_DIR="+filepath.Join(home, ".gemini", "antigravity-cli"))
		}
	}
	return env
}
