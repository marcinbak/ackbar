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
)

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		return "plan"
	case "auto":
		return "auto"
	case "acceptedits":
		return "acceptEdits"
	case "dontask":
		return "dontAsk"
	default:
		return "bypassPermissions"
	}
}

func ConfigFingerprint(sess *Session) string {
	if sess == nil {
		return ""
	}
	ag := strings.ToLower(strings.TrimSpace(sess.Agent))
	m := strings.TrimSpace(sess.Model)
	if strings.EqualFold(m, "default") {
		m = ""
	}
	e := strings.TrimSpace(sess.Effort)
	if ag != "claude-code" || strings.EqualFold(e, "default") {
		e = ""
	}
	pm := normalizePermissionMode(sess.PermissionMode)
	acc := strings.TrimSpace(sess.AccountID)
	cwd := filepath.Clean(strings.TrimSpace(sess.Cwd))
	sup := strconv.FormatBool(sess.IsSupervisor)

	h := sha256.New()
	parts := []string{ag, m, e, pm, acc, cwd, sup}
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

func MCPFingerprint(sess *Session, db *DB, home string) string {
	if sess == nil {
		return ""
	}
	var candidates []string
	if sess.Cwd != "" {
		candidates = append(candidates, filepath.Join(sess.Cwd, ".mcp.json"))
	}
	if accDir := resolveSessionAccountConfigDir(sess, home, db); accDir != "" {
		candidates = append(candidates,
			filepath.Join(accDir, ".claude.json"),
			filepath.Join(accDir, ".credentials.json"),
			filepath.Join(accDir, "mcp_config.json"),
			filepath.Join(accDir, "mcp-needs-auth-cache.json"),
		)
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".claude.json"),
			filepath.Join(home, ".claude", ".credentials.json"),
			filepath.Join(home, ".claude", "mcp_config.json"),
			filepath.Join(home, ".claude", "mcp-needs-auth-cache.json"),
			filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			filepath.Join(home, ".gemini", "config", "mcp_config.json"),
			filepath.Join(home, ".codex", "config.json"),
		)
	}
	h := sha256.New()
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			_, _ = fmt.Fprintf(h, "%s:%d:%d\n", p, st.Size(), st.ModTime().UnixNano())
		}
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
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
	return 20 * time.Minute
}

func buildPersistentArgs(sess *Session, isExistingConversation bool) (string, []string, error) {
	if sess == nil {
		return "", nil, fmt.Errorf("nil session")
	}
	if sess.Agent == "antigravity" {
		bin, err := resolveAntigravityBinary()
		if err != nil {
			return "", nil, fmt.Errorf("failed to locate agy binary: %w", err)
		}
		// Antigravity's -p requires an immediate value and consumes --input-format if bare.
		args := []string{"--input-format", "stream-json", "--output-format", "stream-json"}
		switch strings.ToLower(strings.TrimSpace(sess.PermissionMode)) {
		case "plan":
			args = append(args, "--mode", "plan")
		case "auto":
			args = append(args, "--mode", "auto")
		case "acceptedits":
			args = append(args, "--mode", "accept-edits")
		default:
			args = append(args, "--dangerously-skip-permissions")
		}
		for _, d := range sess.ExtraDirs {
			if cleaned := filepath.Clean(strings.TrimSpace(d)); cleaned != "" && cleaned != "." {
				args = append(args, "--add-dir", cleaned)
			}
		}
		if isExistingConversation && sess.NativeID != "" {
			args = append(args, "--conversation", sess.NativeID)
		}
		if sess.Model != "" && !strings.EqualFold(sess.Model, "default") {
			args = append(args, "--model", sess.Model)
		}
		return bin, args, nil
	}

	if sess.Agent == "claude-code" {
		bin, err := resolveClaudeBinary()
		if err != nil {
			return "", nil, fmt.Errorf("failed to locate claude binary: %w", err)
		}
		args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}
		switch strings.ToLower(strings.TrimSpace(sess.PermissionMode)) {
		case "plan":
			args = append(args, "--permission-mode", "plan")
		case "auto":
			args = append(args, "--permission-mode", "auto")
		case "acceptedits":
			args = append(args, "--permission-mode", "acceptEdits")
		case "dontask":
			args = append(args, "--permission-mode", "dontAsk")
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

type agyStdinTurnMsg struct {
	Event   string             `json:"event"`
	Message stdinTurnMsgDetail `json:"message"`
}

type stdinTurnMsgDetail struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
	case "antigravity":
		effectivePrompt := prompt
		if isSupervisor && isFirstTurnOfNewConv {
			effectivePrompt = "[System Instruction: " + BuildSupervisorSystemPrompt(sessID) + "]\n\n" + prompt
		}
		payload := agyStdinTurnMsg{
			Event: "user",
			Message: stdinTurnMsgDetail{
				Role:    "user",
				Content: effectivePrompt,
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
	env := StripBilledCredentials(os.Environ())
	if sess == nil {
		return env
	}
	if sess.ID != "" {
		env = append(env, "ACKBAR_SESSION_ID="+sess.ID)
	}
	if sess.IsSupervisor {
		env = append(env, "ACKBAR_IS_SUPERVISOR=1")
	}
	if sess.AccountID != "" && db != nil {
		if acc, _ := db.GetAccount(sess.AccountID); acc != nil {
			cfgDir := acc.ConfigDir
			if cfgDir != "" {
				if strings.HasPrefix(cfgDir, "~/") && home != "" {
					cfgDir = filepath.Join(home, cfgDir[2:])
				}
				if sess.Agent == "claude-code" {
					env = append(env, "CLAUDE_CONFIG_DIR="+cfgDir)
				} else if sess.Agent == "antigravity" {
					env = append(env, "GEMINI_CLI_HOME="+cfgDir)
				}
			}
			for k, v := range acc.Env {
				env = append(env, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}
	return augmentWorkerPluginEnv(env, home)
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
