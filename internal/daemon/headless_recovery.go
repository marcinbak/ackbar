package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const crashedWorkerErrorText = "Turn interrupted: worker process exited unexpectedly"

func (h *HeadlessRunner) adoptLiveWorkers(runDir string, entries []os.DirEntry) map[string]bool {
	adoptedIdle := make(map[string]bool)
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
		if !alive || resp.State == "shutting_down" || resp.State == "exited" || sess == nil || sess.Deleted || sess.IsDone || sess.TmuxName != "" || (fpMismatch && !inActiveTurn) {
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
			ConfigFingerprint: wm.ConfigFingerprint, MCPFingerprint: wm.MCPFingerprint,
			Bin: wm.Bin, Args: wm.Args,
			WorkerPID: resp.WorkerPID, ChildPID: resp.ChildPID,
			rotateAfterTurn: fpMismatch,
		}
		h.mu.Unlock()
		if !inActiveTurn {
			adoptedIdle[sess.ID] = true
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
	return adoptedIdle
}

func (h *HeadlessRunner) reattachLiveTurn(sess *Session, meta *TurnSpoolMeta, pingResp *workerIPCResp, clearInterrupted func(*Session, *TurnSpoolMeta)) {
	if sess == nil || sess.Deleted || sess.IsDone {
		_ = sendWorkerCancel(meta)
		h.terminateVerifiedWorker(meta, syscall.SIGKILL)
		removeTurnSpoolFiles(meta.MetaPath, meta)
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
	h.activeMonitors++
	h.mu.Unlock()
	h.syncSleepInhibitor()
	go h.monitorWorkerTurn(sess, meta.OrigSessionID, meta, trackedCmd, maxSeq)
}

func (h *HeadlessRunner) finalizeCrashedTurnSpool(sess *Session, meta *TurnSpoolMeta, clearInterrupted func(*Session, *TurnSpoolMeta)) {
	if sess == nil || sess.Deleted || sess.IsDone {
		removeTurnSpoolFiles(meta.MetaPath, meta)
		clearInterrupted(sess, meta)
		return
	}
	cleanupTurnSocketArtifacts(meta)
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
