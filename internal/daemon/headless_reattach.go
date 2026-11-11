package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

func blockActivityText(b *Blocked) string {
	if b.Kind == BlockApproval {
		return "Waiting for plan approval"
	}
	if b.Kind == BlockRateLimit {
		return "Rate limit: " + b.Reason
	}
	if b.Kind == BlockAuth {
		return "Authentication required: " + b.AuthServer
	}
	return "Waiting for user response"
}

func (h *HeadlessRunner) applyWorkerEvent(sess *Session, origSessionID string, meta *TurnSpoolMeta, evt ChatStreamEvent, emitLive bool) {
	switch evt.Type {
	case "session_init":
		if evt.NativeID != "" {
			h.handleAntigravityInit(sess, evt.NativeID, meta.Prompt)
		}
	case "interactive_block":
		if evt.Blocked == nil {
			return
		}
		h.setInteractiveBlock(sess.ID, evt.Blocked)
		if origSessionID != "" && origSessionID != sess.ID {
			h.setInteractiveBlock(origSessionID, evt.Blocked)
		}
		h.SetQueuePaused(sess.ID, true)
		if emitLive {
			if h.db != nil && sess != nil {
				sess.State, sess.Blocked, sess.Activity = StateBlocked, evt.Blocked, blockActivityText(evt.Blocked)
				sess.IsUnread, sess.LastStateChangeAt, sess.LastEventAt = true, time.Now(), time.Now()
				_ = h.db.SaveSession(sess)
				if h.broadcast != nil {
					h.broadcast(sess)
				}
			}
			evt.SessionID = sess.ID
			h.Emit(sess.ID, evt, origSessionID)
			if evt.Blocked.Kind == BlockRateLimit || evt.Blocked.Kind == BlockAuth {
				h.EmitQueueUpdate(sess.ID)
			}
		}
	default:
		if emitLive {
			if sess != nil && sess.ID != "" {
				evt.SessionID = sess.ID
			}
			h.Emit(sess.ID, evt, origSessionID)
		}
	}
}

func (h *HeadlessRunner) monitorWorkerTurn(sess *Session, origSessionID string, meta *TurnSpoolMeta, trackedCmd *exec.Cmd, afterSeq int64) {
	lastSeq := afterSeq
	dialDeadline := time.Now().Add(3 * time.Second)
	for {
		curMeta := meta
		if meta.MetaPath != "" {
			if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil {
				curMeta = dm
			}
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
						h.applyWorkerEvent(sess, origSessionID, curMeta, evt, true)
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
		_, liveOnSocket := probeLiveWorker(curMeta, 0)
		stillRunning := (curMeta.Status == "starting" || curMeta.Status == "running") &&
			(liveOnSocket || (time.Now().Before(dialDeadline) && h.isTurnWorkerOwnedAlive(curMeta.TurnID)))
		if !stillRunning {
			events, _, _ := ReadTurnSpoolAfter(curMeta.SpoolPath, lastSeq)
			for _, evt := range events {
				if evt.Seq > lastSeq {
					lastSeq = evt.Seq
					h.applyWorkerEvent(sess, origSessionID, curMeta, evt, true)
				}
			}
			h.finalizeTurnFromSpool(sess, origSessionID, curMeta, trackedCmd)
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func (h *HeadlessRunner) finalizeTurnFromSpool(sess *Session, origSessionID string, meta *TurnSpoolMeta, trackedCmd *exec.Cmd) {
	h.mu.Lock()
	responded := h.respondedWhileStopping[sess.ID] || h.respondedWhileStopping[origSessionID]
	delete(h.respondedWhileStopping, sess.ID)
	delete(h.respondedWhileStopping, origSessionID)
	for _, id := range []string{origSessionID, sess.ID} {
		if id != "" {
			if cur, exists := h.processes[id]; !exists || cur == nil || cur == trackedCmd {
				delete(h.processes, id)
				delete(h.activeSessions, id)
				delete(h.activeTurns, id)
				delete(h.activePrompts, id)
			}
		}
	}
	h.mu.Unlock()
	h.syncSleepInhibitor()

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
		checkDrain()
		return
	}
	if h.db != nil {
		currentSess, err := h.db.GetSession(sess.ID)
		if err != nil || currentSess == nil {
			checkDrain()
			return
		}
		if currentSess.Deleted || currentSess.IsDone {
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
	prompt := meta.Prompt

	if interactiveBlock != nil {
		sess.State, sess.Blocked, sess.Activity = StateBlocked, interactiveBlock, blockActivityText(interactiveBlock)
		sess.PID, sess.IsUnread, sess.LastStateChangeAt, sess.LastEventAt = 0, true, time.Now(), time.Now()
		if sess.FirstPrompt == "" {
			sess.FirstPrompt = prompt
		}
		sess.LastPrompt = prompt
	} else if wasCancelled {
		sess.State, sess.Blocked, sess.Activity, sess.PID, sess.LastEventAt = StateIdle, nil, "Turn cancelled", 0, time.Now()
		h.SetQueuePaused(sess.ID, true)
	} else {
		sess.State, sess.Blocked, sess.Activity, sess.PID, sess.LastEventAt = StateIdle, nil, "", 0, time.Now()
		if meta.ExitCode == 0 && meta.Status != "failed" {
			if sess.FirstPrompt == "" {
				sess.FirstPrompt = prompt
			}
			sess.LastPrompt = prompt
		} else {
			if meta.StderrTail != "" {
				sess.Activity = "Turn error: " + meta.StderrTail
			} else {
				sess.Activity = fmt.Sprintf("Turn completed with status: exit status %d", meta.ExitCode)
			}
			h.SetQueuePaused(sess.ID, true)
			h.EmitQueueUpdate(sess.ID)
		}
	}

	if home, _ := os.UserHomeDir(); sess.Agent == "antigravity" {
		if sess.CustomTitle != "" {
			sess.Name = sess.CustomTitle
		} else if title := ReadAntigravitySessionTitle(sess.Cwd, sess.NativeID); title != "" && !isRawSessionName(title) {
			sess.Name = title
		} else if sess.Name == "" || isRawSessionName(sess.Name) {
			sess.Name = TruncateTitle(prompt)
		}
	} else if home != "" {
		if cm := ReadClaudeSessionMeta(sess.Cwd, sess.NativeID); cm != nil {
			sess.ContextPct = cm.ContextPct
			if cm.CustomTitle != "" {
				sess.CustomTitle = cm.CustomTitle
			}
		}
		if sess.Name == "" || isRawSessionName(sess.Name) {
			if sess.CustomTitle != "" {
				sess.Name = sess.CustomTitle
			} else {
				sess.Name = TruncateTitle(prompt)
			}
		}
	}

	markFinalized()
	var rotateWorker bool
	h.mu.Lock()
	if handle, ok := h.warmWorkers[sess.ID]; ok && handle != nil {
		if (interactiveBlock != nil || wasCancelled) && !h.activeTurns[sess.ID] {
			delete(h.warmWorkers, sess.ID)
			if sess.NativeID != "" {
				delete(h.warmWorkers, sess.NativeID)
			}
		} else if handle.rotateAfterTurn && !h.activeTurns[sess.ID] {
			rotateWorker = true
		}
	}
	h.mu.Unlock()
	if rotateWorker {
		go h.ShutdownSessionWorker(sess.ID, sess.NativeID, 2*time.Second)
	}

	if h.db != nil {
		_ = h.db.SaveSession(sess)
	}
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
	if !h.IsRestartPending() && interactiveBlock == nil && !wasCancelled && !h.IsQueuePaused(sess.ID) {
		if nextItem := h.DequeuePrompt(sess.ID); nextItem != nil {
			h.EmitQueueUpdate(sess.ID)
			go func(nextPrompt string) {
				time.Sleep(50 * time.Millisecond)
				targetSess := sess
				if h.db != nil {
					if cs, err := h.db.GetSession(sess.ID); err == nil && cs != nil {
						targetSess = cs
					}
				}
				_ = h.RunTurn(context.Background(), targetSess, nextPrompt)
			}(nextItem.Text)
		}
	}
}

func (h *HeadlessRunner) resolveSession(id, origID, nativeID string) *Session {
	if h == nil || h.db == nil {
		return nil
	}
	for _, sid := range []string{id, origID} {
		if sid != "" {
			if s, err := h.db.GetSession(sid); err == nil && s != nil {
				return s
			}
		}
	}
	if nativeID != "" {
		if list, err := h.db.ListSessions(); err == nil {
			for _, s := range list {
				if s != nil && s.NativeID == nativeID && s.EngineType == EngineHeadless {
					return s
				}
			}
		}
	}
	return nil
}

func (h *HeadlessRunner) resolveSessionForSpool(meta *TurnSpoolMeta) *Session {
	if meta == nil {
		return nil
	}
	return h.resolveSession(meta.SessionID, meta.OrigSessionID, meta.NativeID)
}

func (h *HeadlessRunner) ReattachLiveWorkers() {
	if h == nil || h.db == nil {
		return
	}
	runDir, _ := h.resolveRunDir(false)
	entries, err := os.ReadDir(runDir)
	if runDir == "" || err != nil {
		return
	}

	adoptedTurns := make(map[string]bool)
	adoptedIdle := make(map[string]bool)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".worker.json") {
			wp := filepath.Join(runDir, e.Name())
			wm, err := ReadSessionWorkerMeta(wp)
			if err != nil || wm == nil {
				continue
			}
			resp, err := callWorkerSocket(wm.SocketPath, workerIPCReq{Op: "status"}, 250*time.Millisecond)
			alive := err == nil && resp != nil && resp.OK
			sess := h.resolveSession(wm.SessionID, wm.OrigSessionID, wm.NativeID)
			if !alive || sess == nil || sess.Deleted || sess.IsDone || sess.TmuxName != "" || wm.ConfigFingerprint != ConfigFingerprint(sess) {
				if alive {
					h.killWorkerMeta(wm, 300*time.Millisecond)
				} else {
					cleanupWorkerArtifacts(wm)
				}
				continue
			}
			h.mu.Lock()
			h.warmWorkers[sess.ID] = &warmWorkerHandle{
				WorkerID: wm.WorkerID, SessionID: sess.ID, NativeID: sess.NativeID,
				SocketPath: wm.SocketPath, WorkerMetaPath: wm.WorkerMetaPath,
				ConfigFingerprint: wm.ConfigFingerprint, Bin: wm.Bin, Args: wm.Args,
				WorkerPID: resp.WorkerPID, ChildPID: resp.ChildPID,
			}
			h.mu.Unlock()

			if resp.State == "running_turn" && wm.ActiveTurnID != "" {
				adoptedTurns[wm.ActiveTurnID] = true
			} else {
				adoptedIdle[sess.ID] = true
				sess.State, sess.Blocked, sess.PID = StateIdle, nil, 0
				if h.db != nil {
					_ = h.db.SaveSession(sess)
				}
				if h.broadcast != nil {
					h.broadcast(sess)
				}
			}
		}
	}

	var metas []*TurnSpoolMeta
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".meta.json") {
			if m, err := ReadTurnSpoolMeta(filepath.Join(runDir, e.Name())); err == nil && m != nil {
				metas = append(metas, m)
			}
		}
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].StartedAt.Before(metas[j].StartedAt) })

	clearInterrupted := func(sess *Session, m *TurnSpoolMeta) {
		if sess != nil {
			h.db.RemoveInterruptedHeadlessSessionID(sess.ID)
		}
		h.db.RemoveInterruptedHeadlessSessionID(m.SessionID)
		h.db.RemoveInterruptedHeadlessSessionID(m.OrigSessionID)
	}

	for _, meta := range metas {
		isActiveStatus := meta.Status == "starting" || meta.Status == "running"
		var pingResp *workerIPCResp
		alive := false
		if isActiveStatus {
			pingResp, alive = probeLiveWorker(meta, 250*time.Millisecond)
		}
		sess := h.resolveSessionForSpool(meta)
		if alive {
			if sess == nil || sess.Deleted || sess.IsDone {
				_ = sendWorkerCancel(meta)
				h.terminateVerifiedWorker(meta, syscall.SIGKILL)
				removeTurnSpoolFiles(meta.MetaPath, meta)
				continue
			}
			clearInterrupted(sess, meta)
			livePID := meta.WorkerPID
			if pingResp != nil && pingResp.WorkerPID > 1 {
				livePID = pingResp.WorkerPID
			}
			sess.State, sess.Blocked, sess.PID = StateWorking, nil, livePID
			if sess.Activity == "" {
				sess.Activity = "Thinking..."
			}
			_ = h.db.SaveSession(sess)
			if h.broadcast != nil {
				h.broadcast(sess)
			}
			trackedCmd := &exec.Cmd{}
			h.mu.Lock()
			h.processes[sess.ID] = trackedCmd
			h.activeSessions[sess.ID] = sess
			h.activeTurns[sess.ID] = true
			h.activePrompts[sess.ID] = TranscriptMessage{
				Role:      "user",
				Content:   meta.Prompt,
				Timestamp: meta.StartedAt,
				TurnID:    meta.TurnID,
				InFlight:  true,
			}
			h.sessionSpools[sess.ID] = meta
			h.mu.Unlock()
			h.syncSleepInhibitor()
			go h.monitorWorkerTurn(sess, meta.OrigSessionID, meta, trackedCmd, 0)
		} else if isActiveStatus {
			removeTurnSpoolFiles(meta.MetaPath, meta)
		} else if !meta.FinalizedByDaemon {
			clearInterrupted(sess, meta)
			if sess == nil || sess.Deleted || sess.IsDone {
				removeTurnSpoolFiles(meta.MetaPath, meta)
				continue
			}
			h.mu.Lock()
			h.sessionSpools[sess.ID] = meta
			h.mu.Unlock()
			events, _, _ := ReadTurnSpoolAfter(meta.SpoolPath, 0)
			for _, evt := range events {
				h.applyWorkerEvent(sess, meta.OrigSessionID, meta, evt, false)
			}
			h.finalizeTurnFromSpool(sess, meta.OrigSessionID, meta, nil)
			if adoptedIdle[sess.ID] {
				sess.State = StateIdle
				if h.db != nil {
					_ = h.db.SaveSession(sess)
				}
			}
		} else if sess != nil && !sess.Deleted && !sess.IsDone {
			clearInterrupted(sess, meta)
			if sess.State == StateWorking {
				sess.State, sess.Blocked, sess.PID = StateIdle, nil, 0
				if h.db != nil {
					_ = h.db.SaveSession(sess)
				}
			}
			h.mu.Lock()
			h.sessionSpools[sess.ID] = meta
			h.mu.Unlock()
		}
	}
	h.PruneStaleSpools(24 * time.Hour)
}
