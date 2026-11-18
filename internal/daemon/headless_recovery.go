package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const crashedWorkerErrorText = "Turn interrupted: worker process exited unexpectedly"

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
		if evt.Model != "" && sess != nil {
			h.SetRuntimeModel(sess.ID, sess.Agent, evt.Model)
		}
		if evt.NativeID != "" {
			h.handleAntigravityInit(sess, evt.NativeID, meta.Prompt)
		}
		if evt.Model != "" && sess != nil {
			h.SetRuntimeModel(sess.ID, sess.Agent, evt.Model)
		}
	case "model_detected":
		if evt.Model != "" && sess != nil {
			h.SetRuntimeModel(sess.ID, sess.Agent, evt.Model)
		}
		return
	case "interactive_block":
		if evt.Blocked == nil {
			return
		}
		h.setInteractiveBlock(sess.ID, evt.Blocked)
		if origSessionID != "" && origSessionID != sess.ID {
			h.setInteractiveBlock(origSessionID, evt.Blocked)
		}
		h.setControlBlockState(sess.ID, origSessionID)
		h.SetQueuePaused(sess.ID, true)
		if emitLive {
			if h.db != nil && sess != nil {
				var sessMu *sync.Mutex
				if sess.ID != "" {
					sessMu = h.getSessionTurnMu(sess.ID)
					sessMu.Lock()
				}
				preserveLatestSessionConfigFromDB(h.db, sess, origSessionID)
				sess.State, sess.Blocked, sess.Activity = StateBlocked, evt.Blocked, blockActivityText(evt.Blocked)
				sess.IsUnread, sess.LastStateChangeAt, sess.LastEventAt = true, time.Now(), time.Now()
				_ = h.db.SaveSession(sess)
				if sessMu != nil {
					sessMu.Unlock()
				}
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
	case "control_resolved":
		h.clearControlBlockState(sess.ID, origSessionID)
		h.SetQueuePaused(sess.ID, false)
		if emitLive {
			if h.db != nil && sess != nil {
				var sessMu *sync.Mutex
				if sess.ID != "" {
					sessMu = h.getSessionTurnMu(sess.ID)
					sessMu.Lock()
				}
				preserveLatestSessionConfigFromDB(h.db, sess, origSessionID)
				if sess.State == StateBlocked {
					sess.State = StateWorking
				}
				sess.Blocked = nil
				if sess.Activity == "" || strings.HasPrefix(sess.Activity, "Waiting") {
					sess.Activity = "Thinking..."
				}
				sess.IsUnread, sess.LastStateChangeAt, sess.LastEventAt = false, time.Now(), time.Now()
				_ = h.db.SaveSession(sess)
				if sessMu != nil {
					sessMu.Unlock()
				}
				if h.broadcast != nil {
					h.broadcast(sess)
				}
			}
			if sess != nil && sess.ID != "" {
				evt.SessionID = sess.ID
			}
			h.Emit(sess.ID, evt, origSessionID)
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

func preserveLatestSessionConfigFromDB(db *DB, sess *Session, lookupIDs ...string) {
	if db == nil || sess == nil {
		return
	}
	var candidates []string
	if sess.ID != "" {
		candidates = append(candidates, sess.ID)
	}
	if sess.OldID != "" {
		candidates = append(candidates, sess.OldID)
	}
	candidates = append(candidates, lookupIDs...)

	seen := make(map[string]bool, len(candidates))
	var best *Session
	for _, id := range candidates {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if dbSess, err := db.GetSession(id); err == nil && dbSess != nil {
			if best == nil || (!dbSess.MetadataUpdatedAt.IsZero() && (best.MetadataUpdatedAt.IsZero() || dbSess.MetadataUpdatedAt.After(best.MetadataUpdatedAt))) {
				best = dbSess
			}
		}
	}
	if best == nil {
		return
	}
	if (sess.MetadataUpdatedAt.IsZero() && !best.MetadataUpdatedAt.IsZero()) || best.MetadataUpdatedAt.After(sess.MetadataUpdatedAt) {
		sess.Model = best.Model
		sess.Effort = best.Effort
		sess.PermissionMode = best.PermissionMode
		sess.YoloMode = best.YoloMode
		if best.ExtraDirs != nil {
			sess.ExtraDirs = append([]string(nil), best.ExtraDirs...)
		} else {
			sess.ExtraDirs = nil
		}
		sess.MetadataUpdatedAt = best.MetadataUpdatedAt
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

func isInactiveOrNonHeadless(sess *Session) bool {
	return sess == nil || sess.Deleted || sess.IsDone || sess.TmuxName != "" || sess.EngineType != EngineHeadless
}

func (h *HeadlessRunner) adoptLiveWorkers(runDir string, entries []os.DirEntry) []*warmWorkerHandle {
	var adopted []*warmWorkerHandle
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".worker.json") {
			continue
		}
		wp := filepath.Join(runDir, e.Name())
		wm, err := ReadSessionWorkerMeta(wp)
		if err != nil || wm == nil {
			continue
		}
		resp, err := callWorkerSocket(wm.SocketPath, workerIPCReq{Op: "status"}, 250*time.Millisecond)
		alive := err == nil && resp != nil && resp.OK
		sess := h.resolveSession(wm.SessionID, wm.OrigSessionID, wm.NativeID)
		fpMismatch := sess != nil && wm.ConfigFingerprint != ConfigFingerprint(sess)
		inActiveTurn := alive && (resp.State == "running_turn" || resp.State == "blocked_control")
		if !alive || resp.State == "shutting_down" || resp.State == "exited" || isInactiveOrNonHeadless(sess) || (fpMismatch && !inActiveTurn) {
			if alive {
				h.killWorkerMeta(wm, 300*time.Millisecond)
			} else {
				cleanupWorkerArtifacts(wm)
			}
			continue
		}
		handle := &warmWorkerHandle{
			WorkerID: wm.WorkerID, SessionID: sess.ID, NativeID: sess.NativeID,
			SocketPath: wm.SocketPath, WorkerMetaPath: wm.WorkerMetaPath,
			ConfigFingerprint: wm.ConfigFingerprint, ConfigFingerprintNoModel: wm.ConfigFingerprintNoModel,
			ImmutableFingerprint: wm.ImmutableFingerprint, MCPFingerprint: wm.MCPFingerprint, Bin: wm.Bin, Args: wm.Args,
			WorkerPID: resp.WorkerPID, ChildPID: resp.ChildPID,
			rotateAfterTurn: fpMismatch,
		}
		h.mu.Lock()
		h.warmWorkers[sess.ID] = handle
		h.mu.Unlock()
		h.setBgTaskCount(sess.ID, sess.NativeID, resp.BgTaskCount)
		adopted = append(adopted, handle)
		if !inActiveTurn {
			if !(sess.State == StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == BlockRateLimit) {
				sess.State, sess.Blocked, sess.PID = StateIdle, nil, 0
			} else {
				sess.PID = 0
			}
			if h.db != nil {
				_ = h.db.SaveSession(sess)
			}
			if h.broadcast != nil {
				h.broadcast(sess)
			}
		}
	}
	return adopted
}

func (h *HeadlessRunner) reattachLiveTurn(sess *Session, meta *TurnSpoolMeta, pingResp *workerIPCResp, clearInterrupted func(*Session, *TurnSpoolMeta)) {
	if isInactiveOrNonHeadless(sess) {
		_ = sendWorkerCancel(meta)
		h.terminateVerifiedWorker(meta, syscall.SIGKILL)
		removeTurnSpoolFiles(meta.MetaPath, meta)
		clearInterrupted(sess, meta)
		return
	}
	clearInterrupted(sess, meta)
	events, maxSeq, _ := ReadTurnSpoolAfter(meta.SpoolPath, 0)
	for _, evt := range events {
		h.applyWorkerEvent(sess, meta.OrigSessionID, meta, evt, false)
	}
	livePID := meta.WorkerPID
	if pingResp != nil && pingResp.WorkerPID > 1 {
		livePID = pingResp.WorkerPID
	}
	if !h.restoreBlockedControlOnReattach(sess, meta, pingResp) {
		sess.State, sess.Blocked, sess.PID = StateWorking, nil, livePID
		if sess.Activity == "" {
			sess.Activity = "Thinking..."
		}
	} else {
		sess.PID = livePID
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
	if meta.TurnID != "" {
		h.monitoredTurns[meta.TurnID] = true
	}
	if !meta.IsSynthetic && strings.TrimSpace(meta.Prompt) != "" {
		h.activePrompts[sess.ID] = TranscriptMessage{
			Role:      "user",
			Content:   meta.Prompt,
			Timestamp: meta.StartedAt,
			TurnID:    meta.TurnID,
			InFlight:  true,
		}
	}
	h.sessionSpools[sess.ID] = meta
	h.activeMonitors++
	h.mu.Unlock()
	h.syncSleepInhibitor()
	go h.monitorWorkerTurn(sess, meta.OrigSessionID, meta, trackedCmd, maxSeq)
}

func (h *HeadlessRunner) finalizeCrashedTurnSpool(sess *Session, meta *TurnSpoolMeta, clearInterrupted func(*Session, *TurnSpoolMeta)) {
	if isInactiveOrNonHeadless(sess) {
		removeTurnSpoolFiles(meta.MetaPath, meta)
		clearInterrupted(sess, meta)
		return
	}
	if meta.WorkerMetaPath == "" || !fileExists(meta.WorkerMetaPath) {
		cleanupTurnSocketArtifacts(meta)
	}
	if meta.SpoolPath != "" {
		if w, err := OpenTurnSpoolWriter(meta.SpoolPath, meta.TurnID); err == nil {
			_, _ = w.Append(ChatStreamEvent{
				SessionID: sess.ID,
				TurnID:    meta.TurnID,
				Type:      "error",
				Text:      crashedWorkerErrorText,
				IsError:   true,
			})
			_ = w.Close()
		}
	}
	meta.Status = "failed"
	meta.ExitCode = -1
	meta.FinishedAt = time.Now().UTC()
	meta.FinalizedByDaemon = true
	if meta.MetaPath != "" {
		_ = WriteTurnSpoolMeta(meta.MetaPath, meta)
	}
	if meta.SpoolPath != "" {
		events, _, _ := ReadTurnSpoolAfter(meta.SpoolPath, 0)
		for _, evt := range events {
			h.applyWorkerEvent(sess, meta.OrigSessionID, meta, evt, false)
		}
	}
	_ = h.takeInteractiveBlock(sess.ID)

	h.mu.Lock()
	h.sessionSpools[sess.ID] = meta
	h.mu.Unlock()

	sess.State = StateIdle
	sess.PID = 0
	sess.Blocked = nil
	sess.Activity = "Turn error: " + crashedWorkerErrorText
	sess.LastEventAt = time.Now()
	h.SetQueuePaused(sess.ID, true)
	if h.db != nil {
		_ = h.db.SaveSession(sess)
	}
	if h.broadcast != nil {
		h.broadcast(sess)
	}
	h.EmitQueueUpdate(sess.ID)
	clearInterrupted(sess, meta)
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

	adopted := h.adoptLiveWorkers(runDir, entries)

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
		isActiveStatus := isActiveTurnStatus(meta.Status)
		var pingResp *workerIPCResp
		alive := false
		if isActiveStatus {
			pingResp, alive = probeLiveWorker(meta, 250*time.Millisecond)
		}
		sess := h.resolveSessionForSpool(meta)
		if alive {
			h.reattachLiveTurn(sess, meta, pingResp, clearInterrupted)
		} else if isActiveStatus {
			h.finalizeCrashedTurnSpool(sess, meta, clearInterrupted)
		} else if !meta.FinalizedByDaemon {
			clearInterrupted(sess, meta)
			if isInactiveOrNonHeadless(sess) {
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
		} else if !isInactiveOrNonHeadless(sess) {
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
	for _, handle := range adopted {
		h.ensureWarmWatcher(handle)
	}
	h.PruneStaleSpools(24 * time.Hour)
}
