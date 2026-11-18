package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

func (h *HeadlessRunner) monitorWorkerTurn(sess *Session, origSessionID string, meta *TurnSpoolMeta, trackedCmd *exec.Cmd, afterSeq int64) {
	defer func() { h.mu.Lock(); h.activeMonitors--; h.mu.Unlock() }()
	syncChildPID := func(workerID string, pid int) {
		if pid <= 1 {
			return
		}
		h.mu.Lock()
		if cur, exists := h.processes[sess.ID]; exists && cur != nil && cur != trackedCmd {
			h.mu.Unlock()
			return
		}
		if _, handle := h.findWarmHandleLocked(sess); handle != nil && handle.ChildPID <= 1 && (workerID == "" || handle.WorkerID == "" || handle.WorkerID == workerID) {
			handle.ChildPID = pid
		}
		h.mu.Unlock()
	}
	lastSeq := afterSeq
	dialDeadline := time.Now().Add(3 * time.Second)
	for {
		curMeta := meta
		if meta.MetaPath != "" {
			if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil {
				curMeta = dm
			}
		}
		if curMeta != nil {
			syncChildPID(curMeta.WorkerID, curMeta.ChildPID)
		}
		conn, err := net.DialTimeout("unix", curMeta.SocketPath, 250*time.Millisecond)
		if err == nil {
			b, _ := json.Marshal(workerIPCReq{Op: "subscribe", AfterSeq: lastSeq, TurnID: curMeta.TurnID})
			if _, wErr := conn.Write(append(b, '\n')); wErr == nil {
				sc := bufio.NewScanner(conn)
				sc.Buffer(make([]byte, 64*1024), 10*1024*1024)
				for sc.Scan() {
					var evt ChatStreamEvent
					if json.Unmarshal(sc.Bytes(), &evt) == nil && evt.Seq > lastSeq {
						lastSeq = evt.Seq
						if !h.isTurnSuperseded(curMeta.TurnID) {
							h.applyWorkerEvent(sess, origSessionID, curMeta, evt, true)
						}
					}
				}
			}
			_ = conn.Close()
		}
		if meta.MetaPath != "" {
			if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil {
				curMeta = dm
			}
		}
		if curMeta != nil {
			syncChildPID(curMeta.WorkerID, curMeta.ChildPID)
		}
		probeResp, liveOnSocket := probeLiveWorker(curMeta, 0)
		if probeResp != nil {
			wid := ""
			if curMeta != nil {
				wid = curMeta.WorkerID
			}
			syncChildPID(wid, probeResp.ChildPID)
		}
		stillRunning := isActiveTurnStatus(curMeta.Status) &&
			(liveOnSocket || (time.Now().Before(dialDeadline) && h.isTurnWorkerOwnedAlive(curMeta.TurnID)))
		if !stillRunning {
			events, _, _ := ReadTurnSpoolAfter(curMeta.SpoolPath, lastSeq)
			superseded := h.isTurnSuperseded(curMeta.TurnID)
			for _, evt := range events {
				if evt.Seq > lastSeq {
					lastSeq = evt.Seq
					if !superseded {
						h.applyWorkerEvent(sess, origSessionID, curMeta, evt, true)
					}
				}
			}
			h.finalizeTurnFromSpool(sess, origSessionID, curMeta, trackedCmd)
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func (h *HeadlessRunner) isTurnSuperseded(turnID string) bool {
	if h == nil || turnID == "" {
		return false
	}
	h.mu.RLock()
	superseded := h.supersededTurns[turnID]
	h.mu.RUnlock()
	return superseded
}

func (h *HeadlessRunner) finalizeTurnFromSpool(sess *Session, origSessionID string, meta *TurnSpoolMeta, trackedCmd *exec.Cmd) {
	if meta != nil {
		// Work on a local copy so markFinalized() never mutates h.sessionSpools[id] prematurely or outside h.mu.
		mCopy := *meta
		meta = &mCopy
	}
	turnID := ""
	if meta != nil {
		turnID = meta.TurnID
	}
	if turnID != "" {
		defer func() {
			h.mu.Lock()
			delete(h.supersededTurns, turnID)
			h.mu.Unlock()
		}()
	}

	isSupersededLocked := func() bool {
		if turnID != "" && h.supersededTurns[turnID] {
			return true
		}
		if turnID != "" {
			if curSpool := h.sessionSpools[sess.ID]; curSpool != nil && curSpool.TurnID != "" && curSpool.TurnID != turnID {
				return true
			}
			if origSessionID != "" && origSessionID != sess.ID {
				if curSpool := h.sessionSpools[origSessionID]; curSpool != nil && curSpool.TurnID != "" && curSpool.TurnID != turnID {
					return true
				}
			}
		}
		if cur, exists := h.processes[sess.ID]; exists && cur != nil && cur != trackedCmd {
			return true
		}
		if turnID == "" && (h.respondedWhileStopping[sess.ID] || h.respondedWhileStopping[origSessionID]) {
			return true
		}
		return false
	}

	h.mu.Lock()
	responded := isSupersededLocked()
	if turnID == "" && responded {
		delete(h.respondedWhileStopping, sess.ID)
		delete(h.respondedWhileStopping, origSessionID)
	}
	h.mu.Unlock()

	cleared := false
	clearActiveTurn := func() {
		h.mu.Lock()
		if cleared {
			h.mu.Unlock()
			return
		}
		cleared = true
		for _, id := range []string{origSessionID, sess.ID} {
			if id == "" {
				continue
			}
			curSpool := h.sessionSpools[id]
			if curSpool != nil && (turnID == "" || curSpool.TurnID == turnID) {
				cp := *curSpool
				if meta != nil {
					cp = *meta
				}
				cp.FinalizedByDaemon = true
				h.sessionSpools[id] = &cp
			}
			if curSpool == nil || turnID == "" || curSpool.TurnID == turnID {
				if cur, exists := h.processes[id]; !exists || cur == nil || cur == trackedCmd {
					delete(h.processes, id)
					delete(h.activeSessions, id)
					delete(h.activeTurns, id)
					delete(h.activePrompts, id)
					delete(h.blockedControlTurns, id)
				}
			}
		}
		if turnID != "" {
			delete(h.monitoredTurns, turnID)
		}
		h.mu.Unlock()
		h.syncSleepInhibitor()
	}
	defer clearActiveTurn()

	markFinalized := func() {
		if meta != nil && meta.MetaPath != "" && fileExists(meta.MetaPath) {
			if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil {
				*meta = *dm
			}
			meta.FinalizedByDaemon = true
			_ = WriteTurnSpoolMeta(meta.MetaPath, meta)
		}
	}
	markFinalized()
	checkDrain := func() bool {
		var drainFn func()
		h.mu.Lock()
		hasReplaced := h.processes[sess.ID] != nil && h.processes[sess.ID] != trackedCmd
		if !hasReplaced && h.pendingRestart && h.activeTurnCountLocked() == 0 {
			h.pendingRestart, drainFn, h.onAllTurnsDrained = false, h.onAllTurnsDrained, nil
		}
		h.mu.Unlock()
		if drainFn != nil {
			drainFn()
			return true
		}
		return false
	}

	if responded {
		markFinalized()
		clearActiveTurn()
		checkDrain()
		return
	}
	if h.db != nil {
		currentSess, err := h.db.GetSession(sess.ID)
		if err != nil || currentSess == nil {
			clearActiveTurn()
			checkDrain()
			return
		}
		if currentSess.Deleted {
			removeTurnSpoolFiles(meta.MetaPath, meta)
			clearActiveTurn()
			checkDrain()
			return
		}
		if currentSess.IsDone {
			currentSess.State = StateEnded
			currentSess.PID = 0
			currentSess.Blocked = nil
			currentSess.Activity = ""
			currentSess.MetadataUpdatedAt = time.Now().UTC()
			_ = h.db.SaveSession(currentSess)
			clearActiveTurn()
			if h.broadcast != nil {
				h.broadcast(currentSess)
			}
			removeTurnSpoolFiles(meta.MetaPath, meta)
			checkDrain()
			return
		}
		if sess.OldID != "" && currentSess.OldID == "" {
			currentSess.OldID = sess.OldID
		}
		sess = currentSess
	}

	snap, _ := SynthesizeInFlightTurn(meta, sess.Agent)
	interactiveBlock := h.takeInteractiveBlock(sess.ID)
	if interactiveBlock == nil && origSessionID != sess.ID {
		interactiveBlock = h.takeInteractiveBlock(origSessionID)
	}
	if interactiveBlock == nil && snap != nil {
		interactiveBlock = snap.Blocked
	}
	wasCancelled := (meta != nil && meta.Status == "cancelled") || (snap != nil && snap.WasCancelled)
	wasFailed := (meta != nil && (meta.ExitCode != 0 || meta.Status == "failed")) || (snap != nil && snap.StreamHadError)
	if wasCancelled || (meta != nil && meta.Status == "failed") || (interactiveBlock != nil && interactiveBlock.RequestID != "" && wasFailed) {
		interactiveBlock = nil
	}
	prompt := ""
	if meta != nil && !meta.IsSynthetic {
		prompt = meta.Prompt
	}

	if interactiveBlock != nil {
		sess.State, sess.Blocked, sess.Activity = StateBlocked, interactiveBlock, blockActivityText(interactiveBlock)
		sess.PID, sess.IsUnread, sess.LastStateChangeAt, sess.LastEventAt = 0, true, time.Now(), time.Now()
		if prompt != "" {
			if sess.FirstPrompt == "" {
				sess.FirstPrompt = prompt
			}
			sess.LastPrompt = prompt
		}
	} else if wasCancelled {
		sess.State, sess.Blocked, sess.Activity, sess.PID, sess.LastEventAt = StateIdle, nil, "Turn cancelled", 0, time.Now()
	} else {
		sess.State, sess.Blocked, sess.Activity, sess.PID, sess.LastEventAt = StateIdle, nil, "", 0, time.Now()
		if !wasFailed {
			if prompt != "" {
				if sess.FirstPrompt == "" {
					sess.FirstPrompt = prompt
				}
				sess.LastPrompt = prompt
			}
		} else {
			sess.Activity = formatFailedTurnActivity(meta, snap)
		}
	}

	if home, _ := os.UserHomeDir(); sess.Agent == "antigravity" {
		if sess.CustomTitle != "" {
			sess.Name = sess.CustomTitle
		} else if title := ReadAntigravitySessionTitle(sess.Cwd, sess.NativeID); title != "" && !isRawSessionName(title) {
			sess.Name = title
		} else if (sess.Name == "" || isRawSessionName(sess.Name)) && prompt != "" {
			sess.Name = TruncateTitle(prompt)
		}
	} else if tr, ok := h.pluginRegistry().AsTranscript(sess.Agent); ok {
		if gm := tr.ReadSessionMetadata(sess.Cwd, sess.NativeID); gm != nil {
			if gm.ContextPct > 0 {
				sess.ContextPct = gm.ContextPct
			}
			if gm.CustomTitle != "" {
				sess.CustomTitle = gm.CustomTitle
			}
			if gm.AITitle != "" {
				sess.AITitle = gm.AITitle
			}
			if gm.GitBranch != "" {
				sess.GitBranch = gm.GitBranch
			}
			if sess.CustomTitle != "" {
				sess.Name = sess.CustomTitle
			} else if sess.AITitle != "" {
				sess.Name = sess.AITitle
			} else if gm.Title != "" {
				sess.Name = gm.Title
			}
		}
		if (sess.Name == "" || isRawSessionName(sess.Name)) && prompt != "" {
			sess.Name = TruncateTitle(prompt)
		}
	} else if home != "" {
		if cm := ReadClaudeSessionMeta(sess.Cwd, sess.NativeID); cm != nil {
			sess.ContextPct = cm.ContextPct
			if cm.CustomTitle != "" && sess.CustomTitle == "" {
				sess.CustomTitle = cm.CustomTitle
			}
		}
		if sess.CustomTitle != "" {
			sess.Name = sess.CustomTitle
		} else if (sess.Name == "" || isRawSessionName(sess.Name)) && prompt != "" {
			sess.Name = TruncateTitle(prompt)
		}
	}

	markFinalized()
	keepRateLimitWarm := interactiveBlock != nil && interactiveBlock.Kind == BlockRateLimit && !wasCancelled && (meta == nil || (meta.Status != "failed" && meta.ExitCode == 0))
	if keepRateLimitWarm && meta != nil && meta.WorkerMetaPath != "" {
		if wm, err := ReadSessionWorkerMeta(meta.WorkerMetaPath); err != nil || wm == nil || (wm.State != "idle_warm" && wm.State != "running_turn") {
			keepRateLimitWarm = false
		}
	}
	resolvedChildPID := 0
	if meta != nil {
		resolvedChildPID = meta.ChildPID
		if resolvedChildPID <= 1 && meta.WorkerMetaPath != "" {
			if wm, wErr := ReadSessionWorkerMeta(meta.WorkerMetaPath); wErr == nil && wm != nil && wm.ChildPID > 1 {
				resolvedChildPID = wm.ChildPID
			}
		}
	}

	var sessionMu *sync.Mutex
	if sess.ID != "" {
		sessionMu = h.getSessionTurnMu(sess.ID)
		sessionMu.Lock()
	}
	unlockSessionMu := func() {
		if sessionMu != nil {
			sessionMu.Unlock()
			sessionMu = nil
		}
	}
	defer unlockSessionMu()

	var staleHandle *warmWorkerHandle
	var staleWorkerID string
	h.mu.Lock()
	if isSupersededLocked() {
		if turnID == "" {
			delete(h.respondedWhileStopping, sess.ID)
			delete(h.respondedWhileStopping, origSessionID)
		}
		h.mu.Unlock()
		clearActiveTurn()
		unlockSessionMu()
		checkDrain()
		return
	}
	if (interactiveBlock != nil && !keepRateLimitWarm) || wasCancelled || (wasFailed && !keepRateLimitWarm) {
		delete(h.warmWorkers, sess.ID)
		if sess.NativeID != "" {
			delete(h.warmWorkers, sess.NativeID)
		}
	} else if _, handle := h.findWarmHandleLocked(sess); handle != nil {
		if resolvedChildPID > 1 && handle.ChildPID <= 1 {
			handle.ChildPID = resolvedChildPID
		}
		if handle.rotateAfterTurn {
			staleHandle = handle
			staleWorkerID = handle.WorkerID
		}
	}
	h.mu.Unlock()

	if interactiveBlock == nil {
		if wasCancelled {
			h.SetQueuePaused(sess.ID, true)
		} else if wasFailed {
			h.SetQueuePaused(sess.ID, true)
			h.EmitQueueUpdate(sess.ID)
		}
	}

	if wasFailed && !keepRateLimitWarm && meta != nil && meta.WorkerMetaPath != "" {
		if wm, err := ReadSessionWorkerMeta(meta.WorkerMetaPath); err == nil && wm != nil && wm.State == "idle_warm" {
			h.killWorkerMeta(wm, 300*time.Millisecond)
		}
	}
	preserveLatestSessionConfigFromDB(h.db, sess, origSessionID)
	if staleHandle != nil {
		home, _ := os.UserHomeDir()
		isExisting := sess.FirstPrompt != "" || sess.LastPrompt != "" || hasConversationOnDiskWithRegistry(h.pluginRegistry(), sess, home, h.db)
		fp := ConfigFingerprint(sess)
		mcpFP := MCPFingerprint(sess, h.db, home)
		if !h.restartHandleChild(sess, staleHandle, isExisting, fp, mcpFP, home, 3*time.Second) {
			h.ShutdownSpecificWorker(staleWorkerID, 2*time.Second)
		}
	}

	if h.db != nil {
		_ = h.db.SaveSession(sess)
	}
	// Clear active turn state only after ChildPID, worker rotation, and DB save finish so IsRunning() waiters never observe unfinalized state.
	clearActiveTurn()
	unlockSessionMu()

	if h.broadcast != nil {
		h.broadcast(sess)
	}
	h.mu.RLock()
	onDone := h.onTurnComplete
	h.mu.RUnlock()
	if onDone != nil {
		onDone(sess.ID, sess.NativeID)
	}
	if checkDrain() {
		return
	}
	if !h.IsRestartPending() && interactiveBlock == nil && !wasCancelled && !h.IsQueuePaused(sess.ID) && h.HasReadyPrompt(sess.ID) {
		go func(sessID string, fallbackSess *Session) {
			time.Sleep(50 * time.Millisecond)
			if h.IsRestartPending() || h.IsQueuePaused(sessID) || h.IsRunning(sessID) {
				return
			}
			targetSess := fallbackSess
			if h.db != nil {
				if cs, err := h.db.GetSession(sessID); err == nil && cs != nil {
					targetSess = cs
				}
			}
			if targetSess == nil || targetSess.Deleted || targetSess.IsDone || targetSess.State != StateIdle {
				return
			}
			_, _ = h.DispatchNextQueuedPrompt(context.Background(), targetSess)
		}(sess.ID, sess)
	}
}
