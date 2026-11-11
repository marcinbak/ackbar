package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ackbar/internal/tmux"
)

func (h *HeadlessRunner) pruneCompletedSpoolsForSession(runDir, sessionID, nativeID, keepTurnID string) {
	entries, err := os.ReadDir(runDir)
	if runDir == "" || err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		mp := filepath.Join(runDir, e.Name())
		meta, err := ReadTurnSpoolMeta(mp)
		if err != nil || meta == nil || meta.TurnID == keepTurnID {
			continue
		}
		if (meta.Status == "starting" || meta.Status == "running") && h.isSpoolWorkerLive(meta) {
			continue
		}
		if metaMatchesSession(meta, sessionID, nativeID) {
			removeTurnSpoolFiles(mp, meta)
		}
	}
}

func (h *HeadlessRunner) PruneStaleSpools(maxAge time.Duration) {
	if h == nil || maxAge <= 0 {
		return
	}
	runDir, _ := h.resolveRunDir(false)
	entries, err := os.ReadDir(runDir)
	if runDir == "" || err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".meta.json") {
			mp := filepath.Join(runDir, name)
			meta, err := ReadTurnSpoolMeta(mp)
			if err != nil || meta == nil || ((meta.Status == "starting" || meta.Status == "running") && h.isSpoolWorkerLive(meta)) {
				continue
			}
			ref := meta.FinishedAt
			if ref.IsZero() {
				ref = meta.StartedAt
			}
			if !ref.IsZero() && ref.Before(cutoff) {
				h.mu.Lock()
				for k, cur := range h.sessionSpools {
					if cur != nil && cur.TurnID == meta.TurnID {
						delete(h.sessionSpools, k)
					}
				}
				h.mu.Unlock()
				removeTurnSpoolFiles(mp, meta)
			}
		} else if strings.HasSuffix(name, ".worker.json") {
			wp := filepath.Join(runDir, name)
			wm, err := ReadSessionWorkerMeta(wp)
			if err == nil && wm != nil {
				ref := wm.LastTurnEndedAt
				if ref.IsZero() {
					ref = wm.StartedAt
				}
				if !ref.IsZero() && ref.Before(cutoff) {
					h.mu.RLock()
					isWarm := false
					for _, handle := range h.warmWorkers {
						if handle != nil && handle.WorkerID == wm.WorkerID {
							isWarm = true
							break
						}
					}
					h.mu.RUnlock()
					if !isWarm {
						cleanupWorkerArtifacts(wm)
					}
				}
			}
		}
	}
}

// CleanupSessionRuntime stops any active worker, clears queued prompts, and deletes all spool/socket files for a session without touching provider transcripts.
func (h *HeadlessRunner) CleanupSessionRuntime(sessionID, nativeID string) {
	if h == nil {
		return
	}
	h.ShutdownSessionWorker(sessionID, nativeID, 3*time.Second)
	if sessionID != "" {
		_ = h.CancelTurn(sessionID)
		h.ClearInteractiveBlockAndWait(sessionID, 500*time.Millisecond)
		h.ClearPromptQueue(sessionID)
	}
	if nativeID != "" && nativeID != sessionID {
		h.ClearPromptQueue(nativeID)
	}

	matchesKey := func(k string) bool {
		if sessionID != "" && k == sessionID {
			return true
		}
		if nativeID != "" && (k == nativeID || strings.HasSuffix(k, ":"+nativeID)) {
			return true
		}
		if parts := strings.Split(sessionID, ":"); len(parts) == 3 && parts[2] != "" {
			return k == parts[2] || strings.HasSuffix(k, ":"+parts[2])
		}
		return false
	}

	var toKill []*TurnSpoolMeta
	h.mu.Lock()
	for k, cmd := range h.processes {
		if matchesKey(k) {
			if h.isOwnedProcessLocked(cmd) {
				_ = cmd.Process.Kill()
			}
			delete(h.processes, k)
		}
	}
	for k := range h.activeSessions {
		if matchesKey(k) {
			delete(h.activeSessions, k)
		}
	}
	for k := range h.activeTurns {
		if matchesKey(k) {
			delete(h.activeTurns, k)
		}
	}
	for k := range h.activePrompts {
		if matchesKey(k) {
			delete(h.activePrompts, k)
		}
	}
	for k := range h.interactiveBlocks {
		if matchesKey(k) {
			delete(h.interactiveBlocks, k)
		}
	}
	for k, m := range h.sessionSpools {
		if matchesKey(k) || metaMatchesSession(m, sessionID, nativeID) {
			if m != nil {
				toKill = append(toKill, m)
			}
			delete(h.sessionSpools, k)
		}
	}
	h.mu.Unlock()
	h.syncSleepInhibitor()

	for _, m := range toKill {
		_ = sendWorkerCancel(m)
		h.terminateVerifiedWorker(m, syscall.SIGKILL)
		removeTurnSpoolFiles(m.MetaPath, m)
	}

	runDir, _ := h.resolveRunDir(false)
	entries, err := os.ReadDir(runDir)
	if runDir == "" || err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".meta.json") {
			mp := filepath.Join(runDir, e.Name())
			meta, err := ReadTurnSpoolMeta(mp)
			if err == nil && metaMatchesSession(meta, sessionID, nativeID) {
				if meta.Status == "starting" || meta.Status == "running" {
					_ = sendWorkerCancel(meta)
					h.terminateVerifiedWorker(meta, syscall.SIGKILL)
				}
				removeTurnSpoolFiles(mp, meta)
			}
		} else if strings.HasSuffix(e.Name(), ".worker.json") {
			wp := filepath.Join(runDir, e.Name())
			wm, err := ReadSessionWorkerMeta(wp)
			if err == nil && wm != nil && (wm.SessionID == sessionID || wm.OrigSessionID == sessionID || (nativeID != "" && wm.NativeID == nativeID)) {
				cleanupWorkerArtifacts(wm)
			}
		}
	}
}

func (s *Server) markSessionDoneAndCleanup(ctx context.Context, sess *Session, activity string) error {
	if sess == nil || s.db == nil {
		return errors.New("nil session or database")
	}
	wasRunning := s.headless != nil && s.headless.IsRunning(sess.ID)
	hadTmux := sess.TmuxName != "" && sess.TmuxName != "(deleted)"

	if s.isLocalHost(sess.Host) {
		if s.headless != nil {
			s.headless.CleanupSessionRuntime(sess.ID, sess.NativeID)
		}
		s.clearSubagents(sess.ID, sess.NativeID)
		if hadTmux {
			_ = tmux.Kill(ctx, sess.TmuxName)
		}
	}

	now := time.Now().UTC()
	if hadTmux || wasRunning || sess.State == StateWorking || sess.State == StateBlocked || activity != "" {
		sess.State = StateEnded
	}
	sess.IsDone = true
	sess.IsLater = false
	sess.PID = 0
	sess.Blocked = nil
	sess.RunningSubagents = 0
	sess.TmuxName = ""
	if sess.EngineType != EngineHeadless {
		sess.Managed = false
	}
	if activity != "" {
		sess.Activity = activity
	} else if hadTmux || wasRunning {
		sess.Activity = ""
	}
	sess.MetadataUpdatedAt = now

	if err := s.db.SaveSession(sess); err != nil {
		return err
	}
	s.broadcast(sess)
	return nil
}

func sessionLastActivityAt(sess *Session) time.Time {
	var latest time.Time
	for _, t := range []time.Time{sess.LastEventAt, sess.UpdatedAt, sess.MetadataUpdatedAt, sess.StartedAt} {
		if t.After(latest) {
			latest = t
		}
	}
	return latest
}

func (s *Server) sweepAutoDoneSessions(ctx context.Context) {
	if s == nil || s.db == nil {
		return
	}
	rawSettings, err := s.db.GetAllSettings()
	if err != nil {
		return
	}
	settings := s.populateDefaultSettings(rawSettings)
	if strings.EqualFold(strings.TrimSpace(settings["auto_done_enabled"]), "false") {
		return
	}
	hours, err := strconv.ParseFloat(strings.TrimSpace(settings["auto_done_hours"]), 64)
	if err != nil || hours <= 0 {
		return
	}
	threshold := time.Duration(hours * float64(time.Hour))

	sessions, err := s.db.ListActiveSessions()
	if err != nil || len(sessions) == 0 {
		return
	}
	now := time.Now()
	for _, sess := range sessions {
		if ctx.Err() != nil {
			return
		}
		if sess == nil || sess.Deleted || sess.Archived || sess.IsDone || sess.IsLater {
			continue
		}
		if !s.isLocalHost(sess.Host) {
			continue
		}
		if sess.State == StateWorking || sess.State == StateBlocked {
			continue
		}
		if s.headless != nil {
			if s.headless.IsRunning(sess.ID) {
				continue
			}
			if q, _ := s.headless.GetPromptQueue(sess.ID); len(q) > 0 {
				continue
			}
		}
		lastAt := sessionLastActivityAt(sess)
		if lastAt.IsZero() || now.Sub(lastAt) <= threshold {
			continue
		}
		_ = s.markSessionDoneAndCleanup(ctx, sess, "")
	}
}
