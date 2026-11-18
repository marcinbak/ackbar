package daemon

import (
	"context"
	"strings"
	"sync"
	"time"
)

func (h *HeadlessRunner) setControlBlockState(ids ...string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	for _, id := range ids {
		if id != "" {
			h.blockedControlTurns[id] = true
			if parts := strings.Split(id, ":"); len(parts) == 3 && parts[2] != "" {
				h.blockedControlTurns[parts[2]] = true
			}
		}
	}
	h.mu.Unlock()
	h.syncSleepInhibitor()
}

func (h *HeadlessRunner) clearControlBlockState(ids ...string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	for _, id := range ids {
		if id == "" {
			continue
		}
		delete(h.blockedControlTurns, id)
		delete(h.interactiveBlocks, id)
		if parts := strings.Split(id, ":"); len(parts) == 3 && parts[2] != "" {
			nativeID := parts[2]
			delete(h.blockedControlTurns, nativeID)
			delete(h.interactiveBlocks, nativeID)
			for k := range h.blockedControlTurns {
				if strings.HasSuffix(k, ":"+nativeID) {
					delete(h.blockedControlTurns, k)
				}
			}
			for k := range h.interactiveBlocks {
				if strings.HasSuffix(k, ":"+nativeID) {
					delete(h.interactiveBlocks, k)
				}
			}
		}
	}
	h.mu.Unlock()
	h.syncSleepInhibitor()
}

func (h *HeadlessRunner) GetInteractiveBlock(sessionID string) *Blocked {
	if h == nil || sessionID == "" {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if b := h.interactiveBlocks[sessionID]; b != nil {
		return b
	}
	if parts := strings.Split(sessionID, ":"); len(parts) == 3 && parts[2] != "" {
		nativeID := parts[2]
		if b := h.interactiveBlocks[nativeID]; b != nil {
			return b
		}
		for k, b := range h.interactiveBlocks {
			if b != nil && strings.HasSuffix(k, ":"+nativeID) {
				return b
			}
		}
	}
	return nil
}

func (h *HeadlessRunner) HasInteractiveBlock(sessionID string) bool {
	return h.GetInteractiveBlock(sessionID) != nil
}

func (h *HeadlessRunner) IsBlockedControl(sessionID string) bool {
	if h == nil || sessionID == "" {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.blockedControlTurns[sessionID] {
		return true
	}
	if parts := strings.Split(sessionID, ":"); len(parts) == 3 && parts[2] != "" {
		nativeID := parts[2]
		if h.blockedControlTurns[nativeID] {
			return true
		}
		for k, blocked := range h.blockedControlTurns {
			if blocked && strings.HasSuffix(k, ":"+nativeID) {
				return true
			}
		}
	}
	return false
}

func (h *HeadlessRunner) restoreBlockedControlOnReattach(sess *Session, meta *TurnSpoolMeta, pingResp *workerIPCResp) bool {
	if h == nil || sess == nil {
		return false
	}
	isBlocked := (pingResp != nil && pingResp.State == "blocked_control") || (meta != nil && meta.Status == "blocked")
	if !isBlocked {
		return false
	}
	var blocked *Blocked
	if pingResp != nil && pingResp.Blocked != nil {
		blocked = pingResp.Blocked
	} else if meta != nil && meta.PendingControl != nil && meta.PendingControl.Blocked != nil {
		blocked = meta.PendingControl.Blocked
	} else {
		h.mu.RLock()
		blocked = h.interactiveBlocks[sess.ID]
		h.mu.RUnlock()
	}
	if blocked == nil {
		blocked = &Blocked{Kind: BlockPermission, Reason: "Waiting for user response", Options: []string{"Allow", "Deny"}, Since: time.Now()}
	}
	origID := ""
	if meta != nil {
		origID = meta.OrigSessionID
	}
	h.setInteractiveBlock(sess.ID, blocked)
	h.setControlBlockState(sess.ID, origID)
	h.SetQueuePaused(sess.ID, true)
	sess.State = StateBlocked
	sess.Blocked = blocked
	sess.Activity = blockActivityText(blocked)
	return true
}

func (h *HeadlessRunner) TryAnswerControlRequest(ctx context.Context, sess *Session, approved bool, answerOrFeedback string) (bool, error) {
	action := "deny"
	if approved {
		action = "allow"
	}
	return h.TryAnswerControlAction(ctx, sess, action, answerOrFeedback, "")
}

func (h *HeadlessRunner) TryAnswerControlAction(ctx context.Context, sess *Session, actionLower, answerOrFeedback, reqRequestID string) (bool, error) {
	_ = ctx
	if h == nil || sess == nil || (!strings.EqualFold(sess.Agent, "claude-code") && !isACPHeadlessAgent(h.pluginRegistry(), sess.Agent)) {
		return false, nil
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

	h.mu.RLock()
	_, handle := h.findWarmHandleLocked(sess)
	if handle == nil || handle.SocketPath == "" {
		h.mu.RUnlock()
		return false, nil
	}
	sockPath, workerID := handle.SocketPath, handle.WorkerID
	h.mu.RUnlock()

	req := workerIPCReq{Op: "answer_control", Action: strings.ToLower(strings.TrimSpace(actionLower)), Answer: answerOrFeedback}
	if reqRequestID != "" {
		req.RequestID = reqRequestID
	} else if sess.Blocked != nil && sess.Blocked.RequestID != "" {
		req.RequestID = sess.Blocked.RequestID
	}
	targetPerm := defaultSessionPermissionMode(sess.Agent)
	if sess.PermissionMode != targetPerm {
		nextSess := *sess
		nextSess.PermissionMode = targetPerm
		req.ConfigFingerprint = ConfigFingerprint(&nextSess)
		req.ConfigFingerprintNoModel = ConfigFingerprintWithoutModel(&nextSess)
	}

	resp, err := callWorkerSocket(sockPath, req, 2*time.Second)
	if err != nil || resp == nil {
		return false, nil
	}
	if !resp.OK {
		if resp.ErrCode == "request_id_mismatch" || (resp.ErrCode == "not_blocked" && resp.State != "" && resp.State != "exited" && resp.State != "shutting_down") {
			return true, ErrStaleControlRequest
		}
		return false, nil
	}

	approved := resp.Approved
	h.mu.Lock()
	if _, cur := h.findWarmHandleLocked(sess); cur != nil && cur.WorkerID == workerID {
		if resp.ConfigFingerprint != "" {
			cur.ConfigFingerprint = resp.ConfigFingerprint
		}
		if resp.ConfigFingerprintNoModel != "" {
			cur.ConfigFingerprintNoModel = resp.ConfigFingerprintNoModel
		}
		if resp.ChildPID > 1 {
			cur.ChildPID = resp.ChildPID
		}
	}
	h.mu.Unlock()
	h.clearControlBlockState(sess.ID, sess.OldID, sess.NativeID)
	h.SetQueuePaused(sess.ID, false)

	now := time.Now()
	sess.Blocked = nil
	sess.State = StateWorking
	sess.IsUnread = false
	sess.LastEventAt = now
	sess.LastStateChangeAt = now
	if resp.PlanApproved {
		sess.PermissionMode = resp.PermissionMode
		ts := now.UTC()
		if !ts.After(sess.MetadataUpdatedAt) {
			ts = sess.MetadataUpdatedAt.Add(time.Nanosecond)
		}
		sess.MetadataUpdatedAt = ts
	}
	if approved {
		if resp.PlanApproved {
			sess.Activity = "Approved plan"
		} else if strings.TrimSpace(answerOrFeedback) != "" {
			sess.Activity = "Answered: " + strings.TrimSpace(answerOrFeedback)
		} else {
			sess.Activity = "Permission allowed"
		}
	} else if strings.TrimSpace(answerOrFeedback) != "" {
		sess.Activity = "Rejected: " + strings.TrimSpace(answerOrFeedback)
	} else {
		sess.Activity = "Permission denied"
	}

	if h.db != nil {
		if dbErr := h.db.SaveSession(sess); dbErr != nil {
			return true, dbErr
		}
	}
	unlockSessionMu()
	if h.broadcast != nil {
		h.broadcast(sess)
	}
	return true, nil
}
