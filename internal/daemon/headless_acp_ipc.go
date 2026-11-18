package daemon

import (
	"context"
	"os"
	"strings"
	"time"

	"ackbar/internal/agentutil"
)

func (s *acpChildSession) handleCancelIPC(req workerIPCReq) workerIPCResp {
	s.rt.mu.Lock()
	curChildPID := s.rt.getChildPGIDLocked()
	if *s.rt.state == "shutting_down" || *s.rt.state == "exited" {
		s.rt.mu.Unlock()
		return workerIPCResp{OK: false, ErrCode: *s.rt.state, WorkerPID: os.Getpid(), ChildPID: curChildPID}
	}
	turn := *s.rt.activeTurn
	if turn == nil {
		st := *s.rt.state
		s.rt.mu.Unlock()
		return workerIPCResp{OK: true, KeptWarm: st == "idle_warm" || st == "cancelling", WorkerPID: os.Getpid(), ChildPID: curChildPID, State: st}
	}
	if req.TurnID != "" && turn.turnID != req.TurnID {
		s.rt.mu.Unlock()
		return workerIPCResp{OK: false, ErrCode: "turn_mismatch", WorkerPID: os.Getpid(), ChildPID: curChildPID}
	}
	cancelledTID := turn.turnID
	turn.cancelled = true
	_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: cancelledTID, Type: "turn_cancelled", Text: "Turn was cancelled by user."})
	turn.meta.Status, turn.meta.PendingControl, turn.meta.FinishedAt = "cancelled", nil, time.Now().UTC()
	_ = WriteTurnSpoolMeta(turn.metaPath, turn.meta)
	_ = turn.spool.Close()
	if s.deps.completedTurns != nil {
		s.deps.completedTurns[cancelledTID] = turn.meta.SpoolPath
	}
	ctrl := *s.rt.pendingControl
	*s.rt.pendingControl = nil
	s.rt.workerMeta.PendingControl = nil
	*s.rt.activeTurn = nil
	s.rt.workerMeta.ActiveTurnID, s.rt.workerMeta.ActiveMetaPath = "", ""
	*s.rt.state, s.rt.workerMeta.State = "cancelling", "cancelling"
	s.deps.saveMeta()
	s.rt.notifyWatchersLocked()
	s.rt.mu.Unlock()

	if ctrl != nil && ctrl.ACP != nil {
		if res, err := s.driver.BuildACPServerResponse(ctrl.ACP, false, "cancel"); err == nil {
			_ = s.conn.SendResponse(ctrl.ACP.RawID, res)
		}
	}

	s.mu.Lock()
	sid := s.sessionID
	inFlightRew := s.inFlightRewind
	promptDone := s.promptDone
	if promptDone != nil && s.cancelSettledCh == nil {
		s.cancelSettledCh = make(chan struct{})
	}
	if promptDone != nil {
		if s.cancelTimer != nil {
			s.cancelTimer.Stop()
		}
		s.cancelTimer = time.AfterFunc(1500*time.Millisecond, func() {
			s.rt.mu.Lock()
			if *s.rt.curChild != s.gen || s.gen.replaced || *s.rt.state != "cancelling" {
				s.rt.mu.Unlock()
				return
			}
			*s.rt.state, s.rt.workerMeta.State = "shutting_down", "shutting_down"
			s.deps.saveMeta()
			s.rt.notifyWatchersLocked()
			s.rt.mu.Unlock()
			s.gen.shutdown(300 * time.Millisecond)
			s.deps.exitCleanly(0)
		})
	} else {
		s.rt.mu.Lock()
		if *s.rt.curChild == s.gen && !s.gen.replaced && *s.rt.state == "cancelling" {
			*s.rt.state, s.rt.workerMeta.State = "idle_warm", "idle_warm"
			s.deps.saveMeta()
			s.rt.notifyWatchersLocked()
		}
		s.rt.mu.Unlock()
	}
	s.mu.Unlock()

	_ = s.conn.SendNotification("session/cancel", map[string]any{"sessionId": sid})
	if inFlightRew {
		if _, _, cancelM, ok := s.driver.ACPRewindMethods(); ok && cancelM != "" {
			_ = s.conn.SendNotification(cancelM, map[string]any{"sessionId": sid})
		}
	}
	return workerIPCResp{OK: true, KeptWarm: true, WorkerPID: os.Getpid(), ChildPID: curChildPID, TurnID: cancelledTID, State: "cancelling"}
}

func (s *acpChildSession) handleSetConfigIPC(req workerIPCReq) workerIPCResp {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-s.bootDone:
	case <-ctx.Done():
		return workerIPCResp{OK: false, ErrCode: "bootstrap_timeout", WorkerPID: os.Getpid()}
	}
	s.mu.Lock()
	if s.bootErr != nil {
		s.mu.Unlock()
		return workerIPCResp{OK: false, ErrCode: s.bootErr.Error(), WorkerPID: os.Getpid()}
	}
	sid, prevModel, prevEffort, prevPerm := s.sessionID, s.curModel, s.curEffort, s.curPermMode
	s.mu.Unlock()

	targetModel, targetEffort := normalizeACPConfigValue(req.Model), normalizeACPConfigValue(req.Effort)
	if (prevModel != "" && targetModel == "") || (prevEffort != "" && targetEffort == "") {
		return workerIPCResp{OK: false, ErrCode: "requires_restart", WorkerPID: os.Getpid()}
	}
	if targetModel != "" {
		if err := agentutil.SwitchACPModel(ctx, s.conn, sid, targetModel, targetEffort); err != nil {
			return workerIPCResp{OK: false, ErrCode: err.Error(), WorkerPID: os.Getpid()}
		}
	} else if targetEffort != "" {
		if _, rpcErr, err := s.conn.Call(ctx, "session/set_config_option", map[string]any{
			"sessionId": sid, "configId": "reasoning_effort", "value": targetEffort,
		}); err != nil {
			return workerIPCResp{OK: false, ErrCode: err.Error(), WorkerPID: os.Getpid()}
		} else if rpcErr != nil {
			return workerIPCResp{OK: false, ErrCode: rpcErr.Error(), WorkerPID: os.Getpid()}
		}
	}
	targetPerm := strings.TrimSpace(req.PermissionMode)
	if targetPerm != prevPerm {
		modeArg := targetPerm
		if modeArg == "" {
			modeArg = "default"
		}
		if err := agentutil.SwitchACPMode(ctx, s.conn, sid, modeArg); err != nil {
			return workerIPCResp{OK: false, ErrCode: err.Error(), WorkerPID: os.Getpid()}
		}
	}

	s.mu.Lock()
	s.curModel, s.curEffort, s.curPermMode = targetModel, targetEffort, targetPerm
	s.mu.Unlock()

	s.rt.mu.Lock()
	if len(req.Args) > 0 {
		s.rt.workerMeta.Args = req.Args
	}
	if req.ConfigFingerprint != "" {
		s.rt.workerMeta.ConfigFingerprint = req.ConfigFingerprint
	}
	if req.ConfigFingerprintNoModel != "" {
		s.rt.workerMeta.ConfigFingerprintNoModel = req.ConfigFingerprintNoModel
	}
	s.rt.workerMeta.Model = targetModel
	s.rt.workerMeta.Effort = targetEffort
	s.rt.workerMeta.PermissionMode = targetPerm
	s.rt.workerMeta.UpdatedAt = time.Now().UTC()
	s.deps.saveMeta()
	resp := s.rt.snapshotLocked()
	s.rt.mu.Unlock()
	return resp
}

func (s *acpChildSession) handleRewindIPC(req workerIPCReq) workerIPCResp {
	listM, execM, cancelM, sup := s.driver.ACPRewindMethods()
	if !sup {
		return workerIPCResp{OK: false, ErrCode: "method_not_found", WorkerPID: os.Getpid()}
	}
	s.mu.Lock()
	sid := s.sessionID
	s.mu.Unlock()

	if req.Op == "acp_rewind_cancel" {
		if cancelM != "" {
			_ = s.conn.SendNotification(cancelM, map[string]any{"sessionId": sid})
		}
		return workerIPCResp{OK: true, WorkerPID: os.Getpid()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	select {
	case <-s.bootDone:
	case <-ctx.Done():
		return workerIPCResp{OK: false, ErrCode: "bootstrap_timeout", WorkerPID: os.Getpid()}
	}
	s.rt.mu.Lock()
	st := *s.rt.state
	s.rt.mu.Unlock()
	if st != "idle_warm" {
		return workerIPCResp{OK: false, ErrCode: "not_idle", State: st, WorkerPID: os.Getpid()}
	}

	method, params := listM, map[string]any{"sessionId": sid}
	if req.Op == "acp_rewind_exec" {
		method = execM
		params["targetId"] = req.TargetID
		params["mode"] = "all"
		s.mu.Lock()
		s.inFlightRewind = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.inFlightRewind = false
			s.mu.Unlock()
		}()
	} else if req.Op != "acp_rewind_list" {
		return workerIPCResp{OK: false, ErrCode: "unsupported", WorkerPID: os.Getpid()}
	}

	resRaw, rpcErr, err := s.conn.Call(ctx, method, params)
	if err != nil {
		return workerIPCResp{OK: false, ErrCode: err.Error(), WorkerPID: os.Getpid()}
	}
	if rpcErr != nil {
		if rpcErr.Code == -32601 {
			return workerIPCResp{OK: false, ErrCode: "method_not_found", WorkerPID: os.Getpid()}
		}
		return workerIPCResp{OK: false, ErrCode: rpcErr.Error(), WorkerPID: os.Getpid()}
	}
	if req.Op == "acp_rewind_list" {
		return workerIPCResp{OK: true, WorkerPID: os.Getpid(), RewindPoints: parseACPRewindPoints(resRaw)}
	}
	return workerIPCResp{OK: true, WorkerPID: os.Getpid(), RewindResult: resRaw}
}
