package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ackbar/internal/agent"
)

func (h *HeadlessRunner) SetPluginRegistry(reg *agent.Registry) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.registry = reg
	h.mu.Unlock()
}

func (h *HeadlessRunner) pluginRegistry() *agent.Registry {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	reg := h.registry
	h.mu.RUnlock()
	return reg
}

func (h *HeadlessRunner) runOneShotPluginTurn(sess *Session, prompt string, hd agent.HeadlessDriver) error {
	if sess == nil || hd == nil {
		return ErrAgentUnsupported
	}
	sessionMu := h.getSessionTurnMu(sess.ID)
	sessionMu.Lock()
	defer sessionMu.Unlock()

	runDir, err := h.resolveRunDir(true)
	exePath, eErr := os.Executable()
	if err != nil || eErr != nil {
		return fmt.Errorf("init error: %v %v", err, eErr)
	}

	reg := h.pluginRegistry()

	h.mu.Lock()
	if _, running := h.processes[sess.ID]; running || h.activeTurns[sess.ID] {
		h.mu.Unlock()
		return ErrTurnInProgress
	}
	delete(h.respondedWhileStopping, sess.ID)
	if sess.OldID != "" {
		delete(h.respondedWhileStopping, sess.OldID)
	}
	if sess.NativeID == "" {
		sess.NativeID = generateUUID()
	}
	if sc, ok := reg.AsScanner(sess.Agent); ok && !sc.IsValidNativeID(sess.NativeID) {
		h.mu.Unlock()
		return fmt.Errorf("unsafe %s session ID: %q", sess.Agent, sess.NativeID)
	}
	sess.IsDone = false
	h.mu.Unlock()

	home, _ := os.UserHomeDir()
	isExisting := sess.FirstPrompt != "" || sess.LastPrompt != "" || hasConversationOnDiskWithRegistry(reg, sess, home, h.db)
	var sysPrompt string
	if sess.IsSupervisor {
		sysPrompt = BuildSupervisorSystemPrompt(sess.ID)
	}
	spec, bErr := hd.BuildTurnSpec(agent.HeadlessTurnInput{
		Session:      sess,
		Prompt:       prompt,
		HomeDir:      home,
		IsExisting:   isExisting,
		SystemPrompt: sysPrompt,
	})
	if bErr != nil {
		return bErr
	}

	cleanEnv := buildWorkerEnvWithRegistry(reg, sess, h.db, home)
	if len(spec.Env) > 0 {
		cleanEnv = append(cleanEnv, spec.Env...)
	}

	turnStart := time.Now().UTC()
	turnID := fmt.Sprintf("turn_%d_%s", turnStart.UnixNano(), generateUUID()[:8])
	h.pruneCompletedSpoolsForSession(runDir, sess.ID, sess.NativeID, turnID)

	sockPath, sockTempDir, aErr := allocateWorkerSocketPath(runDir, turnID)
	if aErr != nil {
		return fmt.Errorf("failed to allocate worker socket: %w", aErr)
	}

	meta, startEvt, sErr := h.initTurnSpool(runDir, turnID, sess.ID, sess.NativeID, sess.Agent, sess.Cwd, prompt, spec.Binary, spec.Args, sockPath, sockTempDir, "", "", turnStart)
	if sErr != nil {
		_ = os.Remove(sockPath)
		if sockTempDir != "" {
			_ = os.RemoveAll(sockTempDir)
		}
		return sErr
	}

	workerCmd := exec.Command(exePath, "--headless-worker", "--spec", meta.MetaPath)
	workerCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	workerCmd.Env = cleanEnv
	if sess.Cwd != "" {
		workerCmd.Dir = sess.Cwd
	}
	devNullR, _ := os.Open(os.DevNull)
	devNullW, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if devNullR != nil {
		workerCmd.Stdin = devNullR
		defer devNullR.Close()
	}
	if devNullW != nil {
		workerCmd.Stdout, workerCmd.Stderr = devNullW, devNullW
		defer devNullW.Close()
	}

	if startErr := workerCmd.Start(); startErr != nil {
		removeTurnSpoolFiles(meta.MetaPath, meta)
		return fmt.Errorf("failed to start headless worker: %w", startErr)
	}

	meta.WorkerPID = workerCmd.Process.Pid
	go func(tid string, c *exec.Cmd) {
		_ = c.Wait()
		h.mu.Lock()
		if h.ownedWorkers[tid] == c {
			delete(h.ownedWorkers, tid)
		}
		h.mu.Unlock()
	}(turnID, workerCmd)

	h.recordTurnActive(sess, prompt, turnID, turnStart, meta, workerCmd.Process.Pid, startEvt, workerCmd)
	return nil
}

func dispatchPluginStreamLine(
	parser agent.StreamParser,
	line []byte,
	lastTextBuilder *strings.Builder,
	resultText *string,
	streamHadError *bool,
	hooks streamParseHooks,
) bool {
	if parser == nil {
		return false
	}
	events, handled, err := parser.ParseStreamLine(line)
	if err != nil || !handled {
		return handled
	}
	for _, ev := range events {
		switch ev.Type {
		case "thought_delta", "tool_start", "tool_result":
			if hooks.emit != nil {
				hooks.emit(ev)
			}
		case "text_delta":
			if ev.Text != "" {
				lastTextBuilder.WriteString(ev.Text)
			}
			if hooks.emit != nil {
				hooks.emit(ev)
			}
		case "interactive_block":
			if ev.Blocked != nil && hooks.onInteractiveBlock != nil {
				hooks.onInteractiveBlock(ev.ToolName, ev.ToolInput, ev.Blocked)
			}
		case "model_detected":
			if ev.Model != "" && hooks.onModelDetected != nil {
				hooks.onModelDetected(ev.Model)
			}
		case "error":
			*streamHadError = true
			lastTextBuilder.WriteString("\n" + ev.Text)
			if hooks.emit != nil {
				hooks.emit(ev)
			}
		case "turn_complete":
			*resultText = lastTextBuilder.String()
			if hooks.emitTurnComplete && hooks.emit != nil {
				ev.Text = *resultText
				hooks.emit(ev)
			}
			if hooks.onTurnResult != nil {
				hooks.onTurnResult(lastTextBuilder.String(), *resultText, *streamHadError)
			}
		}
	}
	return true
}

func hasConversationOnDiskWithRegistry(reg *agent.Registry, sess *Session, home string, db *DB) bool {
	if sess == nil || sess.NativeID == "" {
		return false
	}
	accCfgDir := resolveSessionAccountConfigDir(sess, home, db)
	if reg != nil {
		if tr, ok := reg.AsTranscript(sess.Agent); ok {
			if accCfgDir != "" {
				if tr.HasConversationArtifacts(accCfgDir, sess.NativeID) ||
					tr.HasConversationArtifacts(filepath.Dir(accCfgDir), sess.NativeID) {
					return true
				}
			}
			return tr.HasConversationArtifacts(home, sess.NativeID)
		}
	}
	return hasConversationOnDisk(sess, home, db)
}

func buildWorkerEnvWithRegistry(reg *agent.Registry, sess *Session, db *DB, home string) []string {
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
		acc, _ := db.GetAccount(sess.AccountID)
		if acc == nil && !strings.Contains(sess.AccountID, ":") {
			acc, _ = db.GetAccount(sess.Agent + ":" + sess.AccountID)
		}
		if acc != nil {
			if ac, ok := reg.AsAccounts(sess.Agent); ok {
				for k, v := range ac.AccountEnvVars(home, acc) {
					if k != "ACKBAR_ACCOUNT" {
						env = append(env, fmt.Sprintf("%s=%s", k, v))
					}
				}
			} else {
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
	}
	return augmentWorkerPluginEnv(env, home)
}
