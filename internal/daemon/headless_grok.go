package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func resolveGrokBinary() string {
	if p, err := exec.LookPath("grok"); err == nil && p != "" {
		return p
	}
	if p, err := exec.LookPath("grok-cli"); err == nil && p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/usr/local/bin/grok",
		"/opt/homebrew/bin/grok",
	}
	if home != "" {
		candidates = append([]string{
			filepath.Join(home, ".local", "bin", "grok"),
			filepath.Join(home, ".grok", "bin", "grok"),
		}, candidates...)
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
			return c
		}
	}
	return "grok"
}

func buildGrokHeadlessArgs(sess *Session, prompt string, isExisting bool) []string {
	effectivePrompt := prompt
	if !isExisting && sess.IsSupervisor {
		effectivePrompt = fmt.Sprintf("[System Instruction: %s]\n\n%s", BuildSupervisorSystemPrompt(sess.ID), prompt)
	}

	var args []string
	if !isExisting {
		args = []string{"-p", effectivePrompt, "-s", sess.NativeID, "--output-format", "streaming-json"}
		for _, dir := range sess.ExtraDirs {
			dir = strings.TrimSpace(dir)
			if dir != "" && !strings.ContainsAny(dir, "\x00\r\n") {
				args = append(args, "--add-dir", filepath.Clean(dir))
			}
		}
	} else {
		// Resumed Grok sessions inherit workspace directories; passing --add-dir triggers an error.
		args = []string{"-p", effectivePrompt, "-r", sess.NativeID, "--output-format", "streaming-json"}
	}

	switch strings.ToLower(strings.TrimSpace(sess.PermissionMode)) {
	case "plan":
		args = append(args, "--permission-mode", "plan")
	case "acceptedits":
		args = append(args, "--permission-mode", "acceptEdits")
	case "dontask":
		args = append(args, "--permission-mode", "dontAsk")
	default:
		// Headless execution requires pre-approval to prevent blocking on interactive stdin prompts.
		args = append(args, "--always-approve")
	}

	if m := strings.TrimSpace(sess.Model); m != "" && !strings.EqualFold(m, "default") && safeCLIArgRegex.MatchString(m) {
		args = append(args, "-m", m)
	}
	if e := strings.TrimSpace(sess.Effort); e != "" && !strings.EqualFold(e, "default") && safeCLIArgRegex.MatchString(e) {
		args = append(args, "--effort", e)
	}

	return args
}

func (h *HeadlessRunner) runGrokOneShotTurn(sess *Session, prompt string) error {
	if sess == nil || sess.Agent != "grok" {
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

	h.mu.Lock()
	if _, running := h.processes[sess.ID]; running || h.activeTurns[sess.ID] {
		h.mu.Unlock()
		return ErrTurnInProgress
	}
	if sess.NativeID == "" {
		sess.NativeID = generateUUID()
	}
	sess.IsDone = false
	h.mu.Unlock()

	home, _ := os.UserHomeDir()
	isExisting := sess.FirstPrompt != "" || sess.LastPrompt != "" || hasConversationOnDisk(sess, home, h.db)
	bin := resolveGrokBinary()
	args := buildGrokHeadlessArgs(sess, prompt, isExisting)
	cleanEnv := buildWorkerEnv(sess, h.db, home)

	turnStart := time.Now().UTC()
	turnID := fmt.Sprintf("turn_%d_%s", turnStart.UnixNano(), generateUUID()[:8])
	h.pruneCompletedSpoolsForSession(runDir, sess.ID, sess.NativeID, turnID)

	sockPath, sockTempDir, aErr := allocateWorkerSocketPath(runDir, turnID)
	if aErr != nil {
		return fmt.Errorf("failed to allocate worker socket: %w", aErr)
	}

	meta, startEvt, sErr := h.initTurnSpool(runDir, turnID, sess.ID, sess.NativeID, sess.Agent, sess.Cwd, prompt, bin, args, sockPath, sockTempDir, "", "", turnStart)
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
