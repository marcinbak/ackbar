package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

type warmWorkerHandle struct {
	WorkerID, SessionID, NativeID, SocketPath, WorkerMetaPath string
	ConfigFingerprint, ConfigFingerprintNoModel               string
	MCPFingerprint, Bin                                       string
	Args                                                      []string
	WorkerPID, ChildPID                                       int
	rotateAfterTurn                                           bool
	restartChildAfterTurn                                     bool
}

func (h *HeadlessRunner) findWarmHandleLocked(sess *Session) (string, *warmWorkerHandle) {
	if h == nil || sess == nil {
		return "", nil
	}
	if sess.ID != "" {
		if wh := h.warmWorkers[sess.ID]; wh != nil {
			return sess.ID, wh
		}
	}
	if sess.OldID != "" {
		if wh := h.warmWorkers[sess.OldID]; wh != nil {
			return sess.OldID, wh
		}
	}
	if sess.NativeID != "" {
		if wh := h.warmWorkers[sess.NativeID]; wh != nil {
			return sess.NativeID, wh
		}
		for k, wh := range h.warmWorkers {
			if wh != nil && (strings.HasSuffix(k, ":"+sess.NativeID) || wh.NativeID == sess.NativeID) {
				return k, wh
			}
		}
	}
	return "", nil
}

func (h *HeadlessRunner) SetIdleTTL(d time.Duration) {
	if h != nil {
		h.mu.Lock()
		h.idleTTL = d
		h.mu.Unlock()
	}
}

func (h *HeadlessRunner) getIdleTTL() time.Duration {
	if h == nil {
		return DefaultHeadlessIdleTTL
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.idleTTL > 0 {
		return h.idleTTL
	}
	return ResolveHeadlessIdleTTL(0)
}

func (h *HeadlessRunner) getSessionTurnMu(sessionID string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessionTurnMu == nil {
		h.sessionTurnMu = make(map[string]*sync.Mutex)
	}
	mu, ok := h.sessionTurnMu[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		h.sessionTurnMu[sessionID] = mu
	}
	return mu
}

func (h *HeadlessRunner) initTurnSpool(runDir, turnID, sessID, nativeID, agent, cwd, prompt, bin string, args []string, sockPath, sockTempDir, workerID, workerMetaPath string, turnStart time.Time) (*TurnSpoolMeta, ChatStreamEvent, error) {
	spoolPath, metaPath := filepath.Join(runDir, turnID+".ndjson"), filepath.Join(runDir, turnID+".meta.json")
	meta := &TurnSpoolMeta{
		TurnID: turnID, SessionID: sessID, OrigSessionID: sessID, NativeID: nativeID,
		Agent: agent, Cwd: cwd, Prompt: prompt, Bin: bin, Args: args,
		SocketPath: sockPath, SocketTempDir: sockTempDir, SpoolPath: spoolPath, MetaPath: metaPath,
		WorkerID: workerID, WorkerMetaPath: workerMetaPath, StartedAt: turnStart, Status: "starting",
	}
	initSpool, err := OpenTurnSpoolWriter(spoolPath, turnID)
	if err != nil {
		return nil, ChatStreamEvent{}, err
	}
	evt, aErr := initSpool.Append(ChatStreamEvent{SessionID: sessID, TurnID: turnID, Type: "turn_start", Text: prompt, Timestamp: turnStart})
	_ = initSpool.Close()
	if aErr != nil || WriteTurnSpoolMeta(metaPath, meta) != nil {
		removeTurnSpoolFiles(metaPath, meta)
		return nil, ChatStreamEvent{}, errors.New("failed to initialize turn spool files")
	}
	return meta, evt, nil
}

func (h *HeadlessRunner) recordTurnActive(sess *Session, prompt, turnID string, turnStart time.Time, meta *TurnSpoolMeta, pid int, startEvt ChatStreamEvent, cmd *exec.Cmd) {
	trackedCmd := cmd
	if trackedCmd == nil {
		trackedCmd = &exec.Cmd{}
	}
	h.mu.Lock()
	h.activeTurns[sess.ID] = true
	if turnID != "" {
		h.monitoredTurns[turnID] = true
	}
	h.activeSessions[sess.ID] = sess
	if prev := h.sessionSpools[sess.ID]; prev != nil && prev.TurnID != "" && prev.TurnID != turnID && !prev.FinalizedByDaemon {
		h.supersededTurns[prev.TurnID] = true
	}
	h.sessionSpools[sess.ID] = meta
	h.activePrompts[sess.ID] = TranscriptMessage{Role: "user", Content: prompt, Timestamp: turnStart, TurnID: turnID, InFlight: true}
	h.processes[sess.ID] = trackedCmd
	if cmd != nil {
		h.ownedWorkers[turnID] = cmd
	}
	h.activeMonitors++
	h.mu.Unlock()
	h.syncSleepInhibitor()

	sess.State, sess.Blocked, sess.Activity, sess.LastEventAt, sess.PID = StateWorking, nil, "Thinking...", turnStart, pid
	if sess.FirstPrompt == "" {
		sess.FirstPrompt = prompt
	}
	sess.LastPrompt = prompt
	if h.db != nil {
		_ = h.db.SaveSession(sess)
	}
	if h.broadcast != nil {
		h.broadcast(sess)
	}
	h.Emit(sess.ID, startEvt)
	go h.monitorWorkerTurn(sess, sess.ID, meta, trackedCmd, startEvt.Seq)
}

func (h *HeadlessRunner) RunTurn(ctx context.Context, sess *Session, prompt string) error {
	_ = ctx
	if sess.Agent != "claude-code" && sess.Agent != "antigravity" && sess.Agent != "grok" {
		return ErrAgentUnsupported
	}
	if sess.Agent == "grok" {
		return h.runGrokOneShotTurn(sess, prompt)
	}
	sessionMu := h.getSessionTurnMu(sess.ID)
	sessionMu.Lock()
	defer sessionMu.Unlock()

	runDir, err := h.resolveRunDir(true)
	exePath, eErr := os.Executable()
	if err != nil || eErr != nil {
		return fmt.Errorf("init error: %v %v", err, eErr)
	}

	var (
		hCfgFP, hMCPFP, hBin, hSock, hWorkerID, hWorkerMetaPath string
		hRotate, hRestartChild                                  bool
		hArgs                                                   []string
	)
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
	sess.IsDone = false
	handleKey, handle := h.findWarmHandleLocked(sess)
	if handle != nil {
		if handleKey != sess.ID && sess.ID != "" {
			delete(h.warmWorkers, handleKey)
			handle.SessionID = sess.ID
			if sess.NativeID != "" {
				handle.NativeID = sess.NativeID
			}
			h.warmWorkers[sess.ID] = handle
		}
		hCfgFP = handle.ConfigFingerprint
		hRotate = handle.rotateAfterTurn
		hMCPFP = handle.MCPFingerprint
		hRestartChild = handle.restartChildAfterTurn
		hBin = handle.Bin
		hArgs = slices.Clone(handle.Args)
		hSock = handle.SocketPath
		hWorkerID = handle.WorkerID
		hWorkerMetaPath = handle.WorkerMetaPath
	}
	h.mu.Unlock()

	home, _ := os.UserHomeDir()
	isExisting := sess.FirstPrompt != "" || sess.LastPrompt != "" || hasConversationOnDisk(sess, home, h.db)
	fp := ConfigFingerprint(sess)
	fpNoModel := ConfigFingerprintWithoutModel(sess)
	mcpFP := MCPFingerprint(sess, h.db, home)

	if handle != nil {
		if hCfgFP != fp || hRotate {
			h.ShutdownSessionWorker(sess.ID, sess.NativeID, 3*time.Second)
			handle, isExisting = nil, true
		} else {
			if (hMCPFP != "" && hMCPFP != mcpFP) || hRestartChild {
				if ok := h.restartHandleChild(sess, handle, isExisting, fp, mcpFP, home, 3*time.Second); !ok {
					h.ShutdownSessionWorker(sess.ID, sess.NativeID, 500*time.Millisecond)
					handle, isExisting = nil, true
				} else {
					h.mu.RLock()
					hBin = handle.Bin
					hArgs = slices.Clone(handle.Args)
					h.mu.RUnlock()
				}
			}
			if handle != nil {
				if stdinLine, fErr := formatPersistentStdinTurn(sess.Agent, sess.ID, sess.IsSupervisor, false, prompt); fErr == nil {
					turnStart := time.Now().UTC()
					turnID := fmt.Sprintf("turn_%d_%s", turnStart.UnixNano(), generateUUID()[:8])
					meta, startEvt, sErr := h.initTurnSpool(runDir, turnID, sess.ID, sess.NativeID, sess.Agent, sess.Cwd, prompt, hBin, hArgs, hSock, "", hWorkerID, hWorkerMetaPath, turnStart)
					if sErr == nil {
						h.mu.Lock()
						h.monitoredTurns[turnID] = true
						h.mu.Unlock()
						resp, cErr := callWorkerSocket(hSock, workerIPCReq{
							Op: "send_turn", MetaPath: meta.MetaPath, TurnID: turnID, StdinLine: stdinLine,
							SessionID: sess.ID, NativeID: sess.NativeID,
						}, 500*time.Millisecond)
						if cErr == nil && resp != nil && resp.OK {
							h.mu.Lock()
							handle.ChildPID = resp.ChildPID
							h.mu.Unlock()
							meta.WorkerPID, meta.ChildPID, meta.Status = resp.WorkerPID, resp.ChildPID, "running"
							h.recordTurnActive(sess, prompt, turnID, turnStart, meta, resp.ChildPID, startEvt, nil)
							return nil
						}
						h.mu.Lock()
						delete(h.monitoredTurns, turnID)
						h.mu.Unlock()
						removeTurnSpoolFiles(meta.MetaPath, meta)
					}
					h.ShutdownSessionWorker(sess.ID, sess.NativeID, 300*time.Millisecond)
					handle, isExisting = nil, true
				}
			}
		}
	}

	cleanEnv := buildWorkerEnv(sess, h.db, home)
	turnStart := time.Now().UTC()
	workerID, turnID := fmt.Sprintf("w_%d_%s", turnStart.UnixNano(), generateUUID()[:8]), fmt.Sprintf("turn_%d_%s", turnStart.UnixNano(), generateUUID()[:8])
	h.pruneCompletedSpoolsForSession(runDir, sess.ID, sess.NativeID, turnID)

	sockPath, sockTempDir, err := allocateWorkerSocketPath(runDir, workerID)
	if err != nil {
		return fmt.Errorf("failed to allocate worker socket: %w", err)
	}
	workerMetaPath := filepath.Join(runDir, workerID+".worker.json")
	bin, args, bErr := buildPersistentArgs(sess, isExisting)
	if bErr != nil {
		_ = os.Remove(sockPath)
		if sockTempDir != "" {
			_ = os.RemoveAll(sockTempDir)
		}
		return bErr
	}
	meta, startEvt, sErr := h.initTurnSpool(runDir, turnID, sess.ID, sess.NativeID, sess.Agent, sess.Cwd, prompt, bin, args, sockPath, sockTempDir, workerID, workerMetaPath, turnStart)
	if sErr != nil {
		_ = os.Remove(sockPath)
		if sockTempDir != "" {
			_ = os.RemoveAll(sockTempDir)
		}
		return sErr
	}

	dur := h.getIdleTTL()
	workerMeta := &SessionWorkerMeta{
		WorkerID: workerID, SessionID: sess.ID, OrigSessionID: sess.ID, NativeID: sess.NativeID,
		Agent: sess.Agent, Cwd: sess.Cwd, IsSupervisor: sess.IsSupervisor, ConfigFingerprint: fp, ConfigFingerprintNoModel: fpNoModel, MCPFingerprint: mcpFP,
		Bin: bin, Args: args, SocketPath: sockPath, SocketTempDir: sockTempDir, WorkerMetaPath: workerMetaPath,
		State: "starting", ActiveTurnID: turnID, ActiveMetaPath: meta.MetaPath, IdleTTLNs: dur, StartedAt: turnStart, UpdatedAt: turnStart,
	}
	if err := WriteSessionWorkerMeta(workerMetaPath, workerMeta); err != nil {
		removeTurnSpoolFiles(meta.MetaPath, meta)
		return fmt.Errorf("failed to write worker meta: %w", err)
	}

	workerCmd := exec.Command(exePath, "--headless-worker", "--worker-spec", workerMetaPath, "--spec", meta.MetaPath, "--idle-ttl", dur.String())
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
	h.mu.Lock()
	h.monitoredTurns[turnID] = true
	h.mu.Unlock()
	startErr := workerCmd.Start()
	if startErr != nil {
		h.mu.Lock()
		delete(h.monitoredTurns, turnID)
		h.mu.Unlock()
		removeTurnSpoolFiles(meta.MetaPath, meta)
		cleanupWorkerArtifacts(workerMeta)
		return fmt.Errorf("failed to start headless worker: %w", startErr)
	}

	meta.WorkerPID = workerCmd.Process.Pid

	handle = &warmWorkerHandle{
		WorkerID: workerID, SessionID: sess.ID, NativeID: sess.NativeID, SocketPath: sockPath,
		WorkerMetaPath: workerMetaPath, ConfigFingerprint: fp, ConfigFingerprintNoModel: fpNoModel, MCPFingerprint: mcpFP, Bin: bin, Args: args, WorkerPID: workerCmd.Process.Pid,
	}
	h.mu.Lock()
	h.warmWorkers[sess.ID] = handle
	h.mu.Unlock()
	h.ensureWarmWatcher(handle)

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

func (h *HeadlessRunner) killWorkerMeta(m *SessionWorkerMeta, timeout time.Duration) {
	if m == nil {
		return
	}
	var verifiedResp *workerIPCResp
	if m.SocketPath != "" {
		if resp, err := callWorkerSocket(m.SocketPath, workerIPCReq{Op: "shutdown"}, timeout); err == nil && resp != nil {
			verifiedResp = resp
		}
	}
	isOwned := false
	if m.WorkerPID > 1 {
		h.mu.RLock()
		for _, cmd := range h.ownedWorkers {
			if cmd != nil && cmd.Process != nil && cmd.Process.Pid == m.WorkerPID {
				isOwned = true
				break
			}
		}
		h.mu.RUnlock()
	}
	killPIDs := func(child, worker int) {
		if child > 1 && isProcessAlive(child) {
			_ = syscall.Kill(-child, syscall.SIGKILL)
			_ = syscall.Kill(child, syscall.SIGKILL)
		}
		if worker > 1 && isProcessAlive(worker) {
			_ = syscall.Kill(worker, syscall.SIGKILL)
			for d := time.Now().Add(250 * time.Millisecond); time.Now().Before(d) && isProcessAlive(worker); {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}
	if verifiedResp != nil {
		killPIDs(verifiedResp.ChildPID, verifiedResp.WorkerPID)
	} else if isOwned {
		killPIDs(m.ChildPID, m.WorkerPID)
	}
	cleanupWorkerArtifacts(m)
}

func (h *HeadlessRunner) forEachWorkerMeta(sessID, nativeID string, fn func(*SessionWorkerMeta)) {
	runDir, _ := h.resolveRunDir(false)
	if runDir == "" {
		return
	}
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".worker.json") {
			if wm, err := ReadSessionWorkerMeta(filepath.Join(runDir, e.Name())); err == nil && wm != nil {
				if sessID == "" || wm.SessionID == sessID || wm.OrigSessionID == sessID || (nativeID != "" && wm.NativeID == nativeID) {
					fn(wm)
				}
			}
		}
	}
}

func (h *HeadlessRunner) ShutdownSessionWorker(sessionID, nativeID string, timeout time.Duration) {
	if h == nil {
		return
	}
	h.ClearRuntimeModel(sessionID)
	h.stopWarmWatchersForSession(sessionID, nativeID)
	h.mu.Lock()
	for k := range h.warmWorkers {
		if k == sessionID || (nativeID != "" && (k == nativeID || strings.HasSuffix(k, ":"+nativeID))) {
			delete(h.warmWorkers, k)
		}
	}
	delete(h.bgTaskCounts, sessionID)
	h.mu.Unlock()
	h.forEachWorkerMeta(sessionID, nativeID, func(m *SessionWorkerMeta) { h.killWorkerMeta(m, timeout) })
}

func (h *HeadlessRunner) ShutdownSpecificWorker(workerID string, timeout time.Duration) {
	if h == nil || workerID == "" {
		return
	}
	h.mu.Lock()
	if c := h.warmWatchCancels[workerID]; c != nil {
		delete(h.warmWatchCancels, workerID)
		c()
	}
	for k, wh := range h.warmWorkers {
		if wh != nil && wh.WorkerID == workerID {
			delete(h.warmWorkers, k)
			delete(h.bgTaskCounts, wh.SessionID)
		}
	}
	h.mu.Unlock()
	h.forEachWorkerMeta("", "", func(m *SessionWorkerMeta) {
		if m != nil && m.WorkerID == workerID {
			h.killWorkerMeta(m, timeout)
		}
	})
}

func (h *HeadlessRunner) ShutdownAllWorkers(timeout time.Duration) {
	if h == nil {
		return
	}
	waitDur := timeout
	if waitDur < time.Second {
		waitDur = time.Second
	}
	h.stopAllWarmWatchers()
	h.mu.Lock()
	clear(h.warmWorkers)
	clear(h.bgTaskCounts)
	var owned []*exec.Cmd
	for _, cmd := range h.ownedWorkers {
		if cmd != nil && cmd.Process != nil && cmd.Process.Pid > 1 {
			owned = append(owned, cmd)
		}
	}
	h.mu.Unlock()
	h.forEachWorkerMeta("", "", func(m *SessionWorkerMeta) { h.killWorkerMeta(m, timeout) })
	deadline := time.Now().Add(waitDur)
	for _, cmd := range owned {
		pid := cmd.Process.Pid
		if isProcessAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	watchDone := make(chan struct{})
	go func() {
		h.watchWG.Wait()
		close(watchDone)
	}()
	for time.Now().Before(deadline) {
		allDead := true
		for _, cmd := range owned {
			if isProcessAlive(cmd.Process.Pid) {
				allDead = false
				break
			}
		}
		h.mu.RLock()
		monitors := h.activeMonitors
		h.mu.RUnlock()
		watchExited := false
		select {
		case <-watchDone:
			watchExited = true
		default:
		}
		if allDead && monitors == 0 && watchExited {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *HeadlessRunner) setHandleModelLive(sess *Session, handle *warmWorkerHandle, isExisting bool, fp, home string, timeout time.Duration) bool {
	if h == nil || sess == nil || handle == nil || sess.Agent != "claude-code" {
		return false
	}
	h.mu.RLock()
	sockPath := handle.SocketPath
	h.mu.RUnlock()
	if sockPath == "" {
		return false
	}
	_ = home
	_, nextArgs, err := buildPersistentArgs(sess, isExisting)
	if err != nil {
		return false
	}
	fpNoModel := ConfigFingerprintWithoutModel(sess)
	resp, cErr := callWorkerSocket(sockPath, workerIPCReq{
		Op:                       "set_model",
		Model:                    sess.Model,
		Args:                     nextArgs,
		ConfigFingerprint:        fp,
		ConfigFingerprintNoModel: fpNoModel,
	}, timeout)
	if cErr != nil || resp == nil || !resp.OK {
		return false
	}
	h.mu.Lock()
	handle.Args = nextArgs
	handle.ConfigFingerprint = fp
	handle.ConfigFingerprintNoModel = fpNoModel
	handle.rotateAfterTurn = false
	handle.ChildPID = resp.ChildPID
	h.mu.Unlock()
	return true
}

func (h *HeadlessRunner) NotifyConfigChanged(sess *Session) {
	if h == nil || sess == nil {
		return
	}
	if sess.ID != "" {
		// Match RunTurn's lock order (sessionMu -> h.mu) so config updates never race RunTurn's startup window.
		sessionMu := h.getSessionTurnMu(sess.ID)
		sessionMu.Lock()
		defer sessionMu.Unlock()
	}
	h.ClearRuntimeModel(sess.ID)
	if sess.OldID != "" {
		h.ClearRuntimeModel(sess.OldID)
	}

	home, _ := os.UserHomeDir()
	isExisting := sess.FirstPrompt != "" || sess.LastPrompt != "" || hasConversationOnDisk(sess, home, h.db)
	newFP := ConfigFingerprint(sess)
	newNoModelFP := ConfigFingerprintWithoutModel(sess)
	mcpFP := MCPFingerprint(sess, h.db, home)

	h.mu.Lock()
	handleKey, handle := h.findWarmHandleLocked(sess)
	if handle != nil && handleKey != sess.ID && sess.ID != "" {
		delete(h.warmWorkers, handleKey)
		handle.SessionID = sess.ID
		if sess.NativeID != "" {
			handle.NativeID = sess.NativeID
		}
		h.warmWorkers[sess.ID] = handle
	}
	isActive := h.activeTurns[sess.ID] || (sess.OldID != "" && h.activeTurns[sess.OldID])
	if handle != nil {
		if handle.ConfigFingerprint == newFP {
			handle.rotateAfterTurn = false
			h.mu.Unlock()
			return
		}
		canLiveSetModel := sess.Agent == "claude-code" && handle.ConfigFingerprintNoModel != "" && handle.ConfigFingerprintNoModel == newNoModelFP
		workerID := handle.WorkerID
		h.mu.Unlock()

		if canLiveSetModel && h.setHandleModelLive(sess, handle, isExisting, newFP, home, 2*time.Second) {
			return
		}

		h.mu.Lock()
		stillActive := h.activeTurns[sess.ID] || (sess.OldID != "" && h.activeTurns[sess.OldID])
		if stillActive {
			handle.rotateAfterTurn = true
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()

		if h.restartHandleChild(sess, handle, isExisting, newFP, mcpFP, home, 3*time.Second) {
			return
		}
		h.ShutdownSpecificWorker(workerID, 3*time.Second)
		return
	}
	h.mu.Unlock()

	seenWorkers := make(map[string]bool)
	inspectMeta := func(wm *SessionWorkerMeta) {
		if wm == nil || wm.WorkerID == "" || seenWorkers[wm.WorkerID] {
			return
		}
		seenWorkers[wm.WorkerID] = true
		if wm.ConfigFingerprint == newFP {
			return
		}
		tmpHandle := &warmWorkerHandle{
			WorkerID: wm.WorkerID, SessionID: sess.ID, NativeID: sess.NativeID,
			SocketPath: wm.SocketPath, WorkerMetaPath: wm.WorkerMetaPath,
			ConfigFingerprint: wm.ConfigFingerprint, ConfigFingerprintNoModel: wm.ConfigFingerprintNoModel,
			MCPFingerprint: wm.MCPFingerprint, Bin: wm.Bin, Args: wm.Args,
			WorkerPID: wm.WorkerPID, ChildPID: wm.ChildPID,
		}
		if sess.Agent == "claude-code" && wm.ConfigFingerprintNoModel != "" && wm.ConfigFingerprintNoModel == newNoModelFP {
			if h.setHandleModelLive(sess, tmpHandle, isExisting, newFP, home, 2*time.Second) {
				h.mu.Lock()
				if h.warmWorkers[sess.ID] == nil {
					h.warmWorkers[sess.ID] = tmpHandle
				}
				h.mu.Unlock()
				h.ensureWarmWatcher(tmpHandle)
				return
			}
		}
		if !isActive {
			if h.restartHandleChild(sess, tmpHandle, isExisting, newFP, mcpFP, home, 3*time.Second) {
				h.mu.Lock()
				if h.warmWorkers[sess.ID] == nil {
					h.warmWorkers[sess.ID] = tmpHandle
				}
				h.mu.Unlock()
				h.ensureWarmWatcher(tmpHandle)
				return
			}
			h.killWorkerMeta(wm, 3*time.Second)
		}
	}
	h.forEachWorkerMeta(sess.ID, sess.NativeID, inspectMeta)
	if sess.OldID != "" {
		h.forEachWorkerMeta(sess.OldID, sess.NativeID, inspectMeta)
	}
}

func (h *HeadlessRunner) ScheduleChildRestartAfterTurn(sessionID string) {
	if h == nil || sessionID == "" {
		return
	}
	h.mu.Lock()
	if handle := h.warmWorkers[sessionID]; handle != nil {
		handle.restartChildAfterTurn = true
	}
	h.mu.Unlock()
}

func (h *HeadlessRunner) restartHandleChild(sess *Session, handle *warmWorkerHandle, isExisting bool, fp, mcpFP, home string, timeout time.Duration) bool {
	if h == nil || sess == nil || handle == nil {
		return false
	}
	h.mu.RLock()
	sockPath := handle.SocketPath
	h.mu.RUnlock()
	if sockPath == "" {
		return false
	}
	bin, args, err := buildPersistentArgs(sess, isExisting)
	if err != nil {
		return false
	}
	fpNoModel := ConfigFingerprintWithoutModel(sess)
	cleanEnv := buildWorkerEnv(sess, h.db, home)
	resp, cErr := callWorkerSocket(sockPath, workerIPCReq{
		Op:                       "restart_child",
		SessionID:                sess.ID,
		NativeID:                 sess.NativeID,
		Bin:                      bin,
		Args:                     args,
		Env:                      cleanEnv,
		ConfigFingerprint:        fp,
		ConfigFingerprintNoModel: fpNoModel,
		MCPFingerprint:           mcpFP,
	}, timeout)
	if cErr != nil || resp == nil || !resp.OK {
		return false
	}
	h.mu.Lock()
	handle.Bin = bin
	handle.Args = args
	handle.ChildPID = resp.ChildPID
	handle.ConfigFingerprint = fp
	handle.ConfigFingerprintNoModel = fpNoModel
	handle.MCPFingerprint = mcpFP
	handle.rotateAfterTurn = false
	handle.restartChildAfterTurn = false
	h.mu.Unlock()
	return true
}

func (h *HeadlessRunner) RestartSessionChild(sess *Session, timeout time.Duration) bool {
	if h == nil || sess == nil {
		return false
	}
	if sess.ID != "" {
		// Match RunTurn's lock order (sessionMu -> h.mu) so restarts serialize cleanly with RunTurn and finalization.
		sessionMu := h.getSessionTurnMu(sess.ID)
		sessionMu.Lock()
		defer sessionMu.Unlock()
	}
	home, _ := os.UserHomeDir()
	isExisting := sess.FirstPrompt != "" || sess.LastPrompt != "" || hasConversationOnDisk(sess, home, h.db)
	fp := ConfigFingerprint(sess)
	mcpFP := MCPFingerprint(sess, h.db, home)

	h.mu.Lock()
	delete(h.interactiveBlocks, sess.ID)
	if sess.NativeID != "" {
		delete(h.interactiveBlocks, sess.NativeID)
	}
	if curSpool := h.findSpoolForSessionLocked(sess.ID, sess.NativeID); curSpool != nil && curSpool.TurnID != "" && !curSpool.FinalizedByDaemon {
		h.supersededTurns[curSpool.TurnID] = true
	}
	if h.activeTurns[sess.ID] || h.processes[sess.ID] != nil {
		h.respondedWhileStopping[sess.ID] = true
		delete(h.activeTurns, sess.ID)
		delete(h.activePrompts, sess.ID)
		delete(h.processes, sess.ID)
	}
	handle := h.warmWorkers[sess.ID]
	if handle == nil && sess.NativeID != "" {
		for k, cand := range h.warmWorkers {
			if k == sess.NativeID || strings.HasSuffix(k, ":"+sess.NativeID) {
				handle = cand
				break
			}
		}
	}
	h.mu.Unlock()
	h.SetQueuePaused(sess.ID, false)
	h.syncSleepInhibitor()

	if handle != nil {
		if h.restartHandleChild(sess, handle, isExisting, fp, mcpFP, home, timeout) {
			return true
		}
		h.ShutdownSessionWorker(sess.ID, sess.NativeID, 500*time.Millisecond)
		return false
	}

	var restarted bool
	h.forEachWorkerMeta(sess.ID, sess.NativeID, func(m *SessionWorkerMeta) {
		if m == nil || m.SocketPath == "" {
			return
		}
		tmpHandle := &warmWorkerHandle{
			WorkerID: m.WorkerID, SessionID: sess.ID, NativeID: sess.NativeID,
			SocketPath: m.SocketPath, WorkerMetaPath: m.WorkerMetaPath, WorkerPID: m.WorkerPID,
		}
		if h.restartHandleChild(sess, tmpHandle, isExisting, fp, mcpFP, home, timeout) {
			h.mu.Lock()
			h.warmWorkers[sess.ID] = tmpHandle
			h.mu.Unlock()
			h.ensureWarmWatcher(tmpHandle)
			restarted = true
		} else {
			h.killWorkerMeta(m, 500*time.Millisecond)
		}
	})
	return restarted
}

func callWorkerTurnSocket(meta *TurnSpoolMeta, op string, timeout time.Duration) (*workerIPCResp, error) {
	if meta == nil || meta.TurnID == "" {
		return nil, errors.New("nil meta")
	}
	canWait, deadline := timeout > 0 && meta.Status == "starting" && !meta.StartedAt.IsZero() && time.Since(meta.StartedAt) < 3*time.Second, time.Now().Add(timeout)
	for {
		cur := meta
		if meta.MetaPath != "" {
			if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil && dm.TurnID == meta.TurnID {
				cur = dm
			}
		}
		if cur.SocketPath != "" {
			if resp, err := callWorkerSocket(cur.SocketPath, workerIPCReq{Op: op, TurnID: meta.TurnID}, 250*time.Millisecond); err == nil && resp != nil && resp.OK && resp.TurnID == meta.TurnID {
				return resp, nil
			}
		}
		if !canWait || time.Now().After(deadline) || !isActiveTurnStatus(cur.Status) {
			return nil, errors.New("worker socket unreachable")
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func probeLiveWorker(meta *TurnSpoolMeta, waitIfStarting time.Duration) (*workerIPCResp, bool) {
	resp, err := callWorkerTurnSocket(meta, "ping", waitIfStarting)
	return resp, err == nil
}

func pingWorkerSocket(meta *TurnSpoolMeta) bool {
	_, ok := probeLiveWorker(meta, 0)
	return ok
}

func sendWorkerCancel(meta *TurnSpoolMeta) error {
	_, err := callWorkerTurnSocket(meta, "cancel", 350*time.Millisecond)
	return err
}

func (h *HeadlessRunner) isOwnedProcessLocked(cmd *exec.Cmd) bool {
	if h == nil || cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 1 {
		return false
	}
	for _, oc := range h.ownedWorkers {
		if oc == cmd {
			return true
		}
	}
	return false
}

func (h *HeadlessRunner) isOwnedProcess(cmd *exec.Cmd) bool {
	if h == nil || cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 1 {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.isOwnedProcessLocked(cmd)
}

func (h *HeadlessRunner) isTurnWorkerOwnedAlive(turnID string) bool {
	if h == nil || turnID == "" {
		return false
	}
	h.mu.RLock()
	oc := h.ownedWorkers[turnID]
	h.mu.RUnlock()
	return oc != nil && oc.Process != nil && oc.Process.Pid > 1
}

func (h *HeadlessRunner) isSpoolWorkerLive(meta *TurnSpoolMeta) bool {
	if meta == nil || meta.TurnID == "" {
		return false
	}
	if h.isTurnWorkerOwnedAlive(meta.TurnID) {
		return true
	}
	_, ok := probeLiveWorker(meta, 150*time.Millisecond)
	return ok
}

func (h *HeadlessRunner) terminateVerifiedWorker(meta *TurnSpoolMeta, sig syscall.Signal) {
	if meta == nil || meta.TurnID == "" {
		return
	}
	if resp, ok := probeLiveWorker(meta, 150*time.Millisecond); ok {
		if resp.ChildPID > 1 {
			_ = syscall.Kill(-resp.ChildPID, sig)
			_ = syscall.Kill(resp.ChildPID, sig)
		}
		if resp.WorkerPID > 1 {
			_ = syscall.Kill(resp.WorkerPID, sig)
		}
		return
	}
	if h != nil {
		h.mu.RLock()
		ownedCmd := h.ownedWorkers[meta.TurnID]
		h.mu.RUnlock()
		if ownedCmd != nil && ownedCmd.Process != nil && ownedCmd.Process.Pid > 1 {
			_ = ownedCmd.Process.Signal(sig)
		}
	}
}
