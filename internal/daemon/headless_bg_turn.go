package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func boolField(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	v, _ := m[key].(bool)
	return v
}

func stringField(m map[string]interface{}, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func isTerminalBgTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "killed", "stopped", "cancelled", "canceled", "done", "error", "errored":
		return true
	}
	return false
}

func (c *workerBgController) openSyntheticBgTurnLocked(prompt string) error {
	runDir := filepath.Dir(c.rt.workerMeta.SocketPath)
	if c.rt.workerMetaPath != "" {
		runDir = filepath.Dir(c.rt.workerMetaPath)
	}
	now := time.Now().UTC()
	turnID := "turn_bg_" + strconv.FormatInt(now.UnixNano(), 10) + "_" + generateUUID()[:8]
	spoolPath := filepath.Join(runDir, turnID+".ndjson")
	metaPath := filepath.Join(runDir, turnID+".meta.json")
	tMeta := &TurnSpoolMeta{
		TurnID:         turnID,
		SessionID:      c.rt.workerMeta.SessionID,
		OrigSessionID:  c.rt.workerMeta.OrigSessionID,
		NativeID:       c.rt.workerMeta.NativeID,
		Agent:          c.rt.workerMeta.Agent,
		Cwd:            c.rt.workerMeta.Cwd,
		Prompt:         prompt,
		Bin:            c.rt.workerMeta.Bin,
		Args:           c.rt.workerMeta.Args,
		SocketPath:     c.rt.workerMeta.SocketPath,
		SocketTempDir:  c.rt.workerMeta.SocketTempDir,
		SpoolPath:      spoolPath,
		MetaPath:       metaPath,
		WorkerID:       c.rt.workerMeta.WorkerID,
		WorkerMetaPath: c.rt.workerMetaPath,
		WorkerPID:      os.Getpid(),
		ChildPID:       c.rt.getChildPGIDLocked(),
		StartedAt:      now,
		Status:         "running",
		IsSynthetic:    true,
	}
	spool, err := OpenTurnSpoolWriter(spoolPath, turnID)
	if err != nil {
		return err
	}
	_, _ = spool.Append(ChatStreamEvent{
		SessionID: tMeta.SessionID,
		TurnID:    turnID,
		Type:      "turn_start",
		Timestamp: now,
	})
	if err := WriteTurnSpoolMeta(metaPath, tMeta); err != nil {
		_ = spool.Close()
		removeTurnSpoolFiles(metaPath, tMeta)
		return err
	}
	if *c.rt.idleTimer != nil {
		(*c.rt.idleTimer).Stop()
	}
	*c.rt.pendingControl = nil
	c.rt.workerMeta.PendingControl = nil
	*c.rt.state = "running_turn"
	c.rt.workerMeta.State = "running_turn"
	c.rt.workerMeta.ActiveTurnID = turnID
	c.rt.workerMeta.ActiveMetaPath = metaPath
	c.rt.workerMeta.UpdatedAt = now
	c.rt.saveMeta()
	*c.rt.activeTurn = &activeTurnState{turnID: turnID, metaPath: metaPath, meta: tMeta, spool: spool}
	c.rt.notifyWatchersLocked()
	return nil
}

func (h *HeadlessRunner) maybeAttachObservedTurn(sessID, workerMetaPath string, frame workerIPCResp) {
	if h == nil || sessID == "" || frame.TurnID == "" {
		return
	}
	h.mu.RLock()
	already := h.monitoredTurns[frame.TurnID] || h.supersededTurns[frame.TurnID] || h.activeTurns[sessID]
	h.mu.RUnlock()
	if already {
		return
	}

	runDir := ""
	if workerMetaPath != "" {
		runDir = filepath.Dir(workerMetaPath)
	} else {
		runDir, _ = h.resolveRunDir(false)
	}
	if runDir == "" {
		return
	}
	meta, err := ReadTurnSpoolMeta(filepath.Join(runDir, frame.TurnID+".meta.json"))
	if err != nil || meta == nil || meta.FinalizedByDaemon {
		return
	}
	sess := h.resolveSession(sessID, meta.OrigSessionID, meta.NativeID)
	if sess == nil {
		if h.db != nil {
			return
		}
		sess = &Session{ID: sessID, NativeID: meta.NativeID, Agent: meta.Agent, Cwd: meta.Cwd, EngineType: EngineHeadless}
	}

	trackedCmd := &exec.Cmd{}
	h.mu.Lock()
	if h.monitoredTurns[frame.TurnID] || h.supersededTurns[frame.TurnID] || h.activeTurns[sess.ID] {
		h.mu.Unlock()
		return
	}
	h.monitoredTurns[frame.TurnID] = true
	h.activeTurns[sess.ID] = true
	h.activeSessions[sess.ID] = sess
	h.sessionSpools[sess.ID] = meta
	h.processes[sess.ID] = trackedCmd
	if !meta.IsSynthetic && strings.TrimSpace(meta.Prompt) != "" {
		h.activePrompts[sess.ID] = TranscriptMessage{Role: "user", Content: meta.Prompt, Timestamp: meta.StartedAt, TurnID: frame.TurnID, InFlight: true}
	}
	if frame.State == "blocked_control" {
		h.blockedControlTurns[sess.ID] = true
	}
	h.activeMonitors++
	h.mu.Unlock()
	h.syncSleepInhibitor()

	var sessMu *sync.Mutex
	if sess.ID != "" {
		sessMu = h.getSessionTurnMu(sess.ID)
		sessMu.Lock()
	}
	preserveLatestSessionConfigFromDB(h.db, sess, meta.OrigSessionID)
	if frame.State == "blocked_control" {
		h.restoreBlockedControlOnReattach(sess, meta, &frame)
	} else {
		pid := frame.ChildPID
		if pid <= 1 {
			pid = frame.WorkerPID
		}
		sess.State, sess.Blocked, sess.Activity, sess.LastEventAt, sess.PID = StateWorking, nil, "Thinking...", meta.StartedAt, pid
	}
	if h.db != nil {
		_ = h.db.SaveSession(sess)
	}
	if sessMu != nil {
		sessMu.Unlock()
	}
	if h.broadcast != nil {
		h.broadcast(sess)
	}
	go h.monitorWorkerTurn(sess, sess.ID, meta, trackedCmd, 0)
}

func (h *HeadlessRunner) SetOnBgTaskCountChange(fn func(sessionID, nativeID string, count int)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.onBgTaskCountChange = fn
	h.mu.Unlock()
}

func (h *HeadlessRunner) SetOnWorkerExit(fn func(sessionID, nativeID string)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.onWorkerExit = fn
	h.mu.Unlock()
}

func (h *HeadlessRunner) GetBgTaskCount(sessionID string) int {
	if h == nil || sessionID == "" {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if cnt, ok := h.bgTaskCounts[sessionID]; ok {
		return cnt
	}
	if parts := strings.Split(sessionID, ":"); len(parts) == 3 && parts[2] != "" {
		nativeID := parts[2]
		if cnt, ok := h.bgTaskCounts[nativeID]; ok {
			return cnt
		}
		for k, cnt := range h.bgTaskCounts {
			if strings.HasSuffix(k, ":"+nativeID) {
				return cnt
			}
		}
	}
	return 0
}

func (h *HeadlessRunner) HasWarmWorker(sessionID string) bool {
	if h == nil || sessionID == "" {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.warmWorkers[sessionID] != nil {
		return true
	}
	if parts := strings.Split(sessionID, ":"); len(parts) == 3 && parts[2] != "" {
		nativeID := parts[2]
		if h.warmWorkers[nativeID] != nil {
			return true
		}
		for k, wh := range h.warmWorkers {
			if wh != nil && (strings.HasSuffix(k, ":"+nativeID) || wh.NativeID == nativeID) {
				return true
			}
		}
	}
	return false
}

func (h *HeadlessRunner) setBgTaskCount(sessID, nativeID string, count int) {
	if h == nil || sessID == "" {
		return
	}
	if count < 0 {
		count = 0
	}
	h.mu.Lock()
	prev, had := h.bgTaskCounts[sessID]
	if count == 0 {
		delete(h.bgTaskCounts, sessID)
	} else {
		h.bgTaskCounts[sessID] = count
	}
	cb := h.onBgTaskCountChange
	h.mu.Unlock()
	if cb != nil && ((!had && count > 0) || (had && prev != count)) {
		cb(sessID, nativeID, count)
	}
}

func (h *HeadlessRunner) handleWarmWorkerExited(workerID, sessID, nativeID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	for k, wh := range h.warmWorkers {
		if wh != nil && wh.WorkerID == workerID {
			delete(h.warmWorkers, k)
		}
	}
	delete(h.bgTaskCounts, sessID)
	onExit := h.onWorkerExit
	h.mu.Unlock()
	if onExit != nil {
		onExit(sessID, nativeID)
	}
}
