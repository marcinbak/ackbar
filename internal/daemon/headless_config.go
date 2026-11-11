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
	pm := "bypassPermissions"
	if strings.EqualFold(sess.PermissionMode, "plan") {
		pm = "plan"
	}
	acc := strings.TrimSpace(sess.AccountID)
	cwd := filepath.Clean(strings.TrimSpace(sess.Cwd))
	sup := strconv.FormatBool(sess.IsSupervisor)

	h := sha256.New()
	parts := []string{ag, m, e, pm, acc, cwd, sup}
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
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
		if strings.EqualFold(sess.PermissionMode, "plan") {
			args = append(args, "--mode", "plan")
		} else {
			args = append(args, "--dangerously-skip-permissions")
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
		if strings.EqualFold(sess.PermissionMode, "plan") {
			args = append(args, "--permission-mode", "plan")
		} else {
			args = append(args, "--dangerously-skip-permissions")
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
	return env
}
