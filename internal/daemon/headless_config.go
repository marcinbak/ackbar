package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultHeadlessIdleTTL = 65 * time.Minute

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
	if (ag != "claude-code" && ag != "grok") || strings.EqualFold(e, "default") {
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

type mcpFingerprintCacheKey struct {
	path      string
	cwdScope  string
	size      int64
	mtimeNano int64
}

type normalizedProjectMCP struct {
	MCPServers             map[string]any `json:"mcpServers,omitempty"`
	DisabledMCPServers     []string       `json:"disabledMcpServers,omitempty"`
	EnabledMCPJSONServers  []string       `json:"enabledMcpjsonServers,omitempty"`
	DisabledMCPJSONServers []string       `json:"disabledMcpjsonServers,omitempty"`
}

type normalizedMCPConfig struct {
	MCPServers         map[string]any        `json:"mcpServers,omitempty"`
	DisabledMCPServers []string              `json:"disabledMcpServers,omitempty"`
	DisabledMCPSnake   []string              `json:"disabled_mcp_servers,omitempty"`
	Project            *normalizedProjectMCP `json:"project,omitempty"`
}

var (
	mcpFingerprintCacheMu sync.RWMutex
	mcpFingerprintCache   = make(map[mcpFingerprintCacheKey][32]byte)
	emptyMCPDigest        = sha256.Sum256([]byte("{}"))
)

func asStringAnyMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sortedStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return out
}

func computeMCPFileDigest(path, rawCwd string) ([32]byte, bool) {
	data, err := readBoundedJSONFile(path, false)
	if err != nil {
		return emptyMCPDigest, false
	}
	if strings.HasSuffix(path, ".toml") {
		return sha256.Sum256(data), true
	}
	isClaudeJSON := filepath.Base(path) == ".claude.json"
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		if isClaudeJSON {
			// Treat transient/mid-write invalid JSON in .claude.json as empty so warm workers do not flap.
			return emptyMCPDigest, false
		}
		return sha256.Sum256(data), true
	}
	norm := normalizedMCPConfig{
		MCPServers:         asStringAnyMap(root["mcpServers"]),
		DisabledMCPServers: sortedStringSlice(root["disabledMcpServers"]),
		DisabledMCPSnake:   sortedStringSlice(root["disabled_mcp_servers"]),
	}
	if isClaudeJSON && rawCwd != "" {
		if projects := asStringAnyMap(root["projects"]); projects != nil {
			proj := asStringAnyMap(projects[rawCwd])
			if proj == nil {
				if cleanCwd := filepath.Clean(rawCwd); cleanCwd != rawCwd {
					proj = asStringAnyMap(projects[cleanCwd])
				}
			}
			if proj != nil {
				pNorm := &normalizedProjectMCP{
					MCPServers:             asStringAnyMap(proj["mcpServers"]),
					DisabledMCPServers:     sortedStringSlice(proj["disabledMcpServers"]),
					EnabledMCPJSONServers:  sortedStringSlice(proj["enabledMcpjsonServers"]),
					DisabledMCPJSONServers: sortedStringSlice(proj["disabledMcpjsonServers"]),
				}
				if len(pNorm.MCPServers) > 0 || len(pNorm.DisabledMCPServers) > 0 ||
					len(pNorm.EnabledMCPJSONServers) > 0 || len(pNorm.DisabledMCPJSONServers) > 0 {
					norm.Project = pNorm
				}
			}
		}
	}
	encoded, err := json.Marshal(norm)
	if err != nil {
		return emptyMCPDigest, false
	}
	return sha256.Sum256(encoded), true
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
			filepath.Join(accDir, "mcp_config.json"),
			filepath.Join(accDir, "config.toml"),
		)
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".claude.json"),
			filepath.Join(home, ".claude", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			filepath.Join(home, ".gemini", "config", "mcp_config.json"),
			filepath.Join(home, ".codex", "config.json"),
			filepath.Join(home, ".grok", "config.toml"),
		)
	}
	cleanCwd := ""
	if sess.Cwd != "" {
		cleanCwd = filepath.Clean(sess.Cwd)
	}
	seen := make(map[string]bool, len(candidates))
	h := sha256.New()
	for _, rawPath := range candidates {
		p := filepath.Clean(rawPath)
		if seen[p] {
			continue
		}
		seen[p] = true
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		cwdScope := ""
		if filepath.Base(p) == ".claude.json" {
			cwdScope = cleanCwd
		}
		key := mcpFingerprintCacheKey{
			path:      p,
			cwdScope:  cwdScope,
			size:      st.Size(),
			mtimeNano: st.ModTime().UnixNano(),
		}
		mcpFingerprintCacheMu.RLock()
		digest, cached := mcpFingerprintCache[key]
		mcpFingerprintCacheMu.RUnlock()
		if !cached {
			var cacheable bool
			digest, cacheable = computeMCPFileDigest(p, sess.Cwd)
			if cacheable {
				mcpFingerprintCacheMu.Lock()
				if len(mcpFingerprintCache) >= 2048 {
					clear(mcpFingerprintCache)
				}
				mcpFingerprintCache[key] = digest
				mcpFingerprintCacheMu.Unlock()
			}
		}
		_, _ = fmt.Fprintf(h, "%s:%x\n", p, digest)
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
	return DefaultHeadlessIdleTTL
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
				} else if sess.Agent == "grok" {
					env = append(env, "GROK_HOME="+cfgDir)
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
