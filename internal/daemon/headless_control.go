package daemon

import (
	"errors"
	"os"
	"sync"
	"time"

	"ackbar/internal/agent"
)

var ErrStaleControlRequest = errors.New("stale or already-resolved control request")

type PendingControlRequest struct {
	RequestID      string                  `json:"request_id"`
	Subtype        string                  `json:"subtype"`
	ToolName       string                  `json:"tool_name"`
	ToolUseID      string                  `json:"tool_use_id,omitempty"`
	DecisionReason string                  `json:"decision_reason,omitempty"`
	RawInput       map[string]interface{}  `json:"raw_input,omitempty"`
	Blocked        *Blocked                `json:"blocked,omitempty"`
	ACP            *agent.ACPControlPrompt `json:"acp,omitempty"`
}

type workerRuntime struct {
	mu                 *sync.Mutex
	reg                *agent.Registry
	state              *string
	workerMeta         *SessionWorkerMeta
	workerMetaPath     string
	activeTurn         **activeTurnState
	curChild           **workerChildGen
	idleTimer          **time.Timer
	pendingControl     **PendingControlRequest
	idleTTL            time.Duration
	isStdioControl     bool
	saveMeta           func()
	getChildPGIDLocked func() int
	writeStdin         func(string)
	watchHub           *workerWatchHub
	bgCtrl             *workerBgController
}

func isActiveTurnStatus(status string) bool {
	return status == "starting" || status == "running" || status == "blocked"
}

func (rt *workerRuntime) snapshotLocked() workerIPCResp {
	turnID := ""
	if *rt.activeTurn != nil {
		turnID = (*rt.activeTurn).turnID
	}
	bgCount := rt.workerMeta.BgTaskCount
	if rt.bgCtrl != nil {
		bgCount = len(rt.bgCtrl.tasks)
	}
	var blocked *Blocked
	if *rt.pendingControl != nil {
		blocked = (*rt.pendingControl).Blocked
	}
	return workerIPCResp{
		OK:                       true,
		WorkerPID:                os.Getpid(),
		ChildPID:                 rt.getChildPGIDLocked(),
		TurnID:                   turnID,
		State:                    *rt.state,
		ConfigFingerprint:        rt.workerMeta.ConfigFingerprint,
		ConfigFingerprintNoModel: rt.workerMeta.ConfigFingerprintNoModel,
		MCPFingerprint:           rt.workerMeta.MCPFingerprint,
		BgTaskCount:              bgCount,
		Blocked:                  blocked,
	}
}

func (rt *workerRuntime) notifyWatchersLocked() {
	if rt != nil && rt.watchHub != nil {
		rt.watchHub.notify(rt.snapshotLocked())
	}
}

func (rt *workerRuntime) handleControlRequest(gen *workerChildGen, ctrl *PendingControlRequest) bool {
	if rt == nil || ctrl == nil {
		return false
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if *rt.curChild != gen || gen.replaced || *rt.state == "shutting_down" || *rt.state == "exited" {
		return false
	}
	turn := *rt.activeTurn
	if turn == nil || turn.cancelled {
		return false
	}
	*rt.pendingControl = ctrl
	rt.workerMeta.PendingControl = ctrl
	rt.workerMeta.State = "blocked_control"
	rt.workerMeta.UpdatedAt = time.Now().UTC()
	*rt.state = "blocked_control"
	turn.meta.PendingControl = ctrl
	turn.meta.Status = "blocked"
	_ = WriteTurnSpoolMeta(turn.metaPath, turn.meta)
	rt.saveMeta()
	_, _ = turn.spool.Append(ChatStreamEvent{
		SessionID: rt.workerMeta.SessionID,
		TurnID:    turn.turnID,
		Type:      "interactive_block",
		ToolName:  ctrl.ToolName,
		ToolInput: ctrl.RawInput,
		Blocked:   ctrl.Blocked,
	})
	rt.notifyWatchersLocked()
	return true
}

func (rt *workerRuntime) handleControlCancel(gen *workerChildGen, requestID string) {
	if rt == nil || requestID == "" {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if *rt.curChild != gen || gen.replaced || *rt.state != "blocked_control" || *rt.pendingControl == nil {
		return
	}
	if (*rt.pendingControl).RequestID != requestID {
		return
	}
	*rt.pendingControl = nil
	rt.workerMeta.PendingControl = nil
	*rt.state = "running_turn"
	rt.workerMeta.State = "running_turn"
	rt.workerMeta.UpdatedAt = time.Now().UTC()
	if turn := *rt.activeTurn; turn != nil {
		turn.meta.PendingControl = nil
		turn.meta.Status = "running"
		_ = WriteTurnSpoolMeta(turn.metaPath, turn.meta)
		_, _ = turn.spool.Append(ChatStreamEvent{
			SessionID: rt.workerMeta.SessionID,
			TurnID:    turn.turnID,
			Type:      "control_resolved",
		})
	}
	rt.saveMeta()
	rt.notifyWatchersLocked()
}

func (rt *workerRuntime) handleAnswerControlIPC(req workerIPCReq) workerIPCResp {
	rt.mu.Lock()
	curChildPID := rt.getChildPGIDLocked()
	if *rt.state != "blocked_control" || *rt.activeTurn == nil || *rt.pendingControl == nil {
		curState := *rt.state
		rt.mu.Unlock()
		return workerIPCResp{OK: false, ErrCode: "not_blocked", State: curState, WorkerPID: os.Getpid(), ChildPID: curChildPID}
	}
	ctrl := *rt.pendingControl
	if req.RequestID != "" && ctrl.RequestID != req.RequestID {
		curState := *rt.state
		rt.mu.Unlock()
		return workerIPCResp{OK: false, ErrCode: "request_id_mismatch", State: curState, WorkerPID: os.Getpid(), ChildPID: curChildPID}
	}
	approved := req.Approved
	if req.Action != "" {
		evalBlocked := ctrl.Blocked
		if evalBlocked == nil && ctrl.ToolName != "" {
			evalBlocked = &Blocked{ToolName: ctrl.ToolName}
		} else if evalBlocked != nil && evalBlocked.ToolName == "" && ctrl.ToolName != "" {
			cp := *evalBlocked
			cp.ToolName = ctrl.ToolName
			evalBlocked = &cp
		}
		approved = resolveRespondApproval(evalBlocked, req.Action, req.Answer)
	}
	g := *rt.curChild
	if g == nil || g.pgid <= 1 {
		curState := *rt.state
		rt.mu.Unlock()
		return workerIPCResp{OK: false, ErrCode: "inactive_child", State: curState, WorkerPID: os.Getpid(), ChildPID: curChildPID}
	}
	var acpRes any
	var ctrlLine []byte
	if ctrl.ACP != nil {
		drv, ok := acpDriverForAgent(rt.reg, rt.workerMeta.Agent)
		if !ok || g.acp == nil {
			curState := *rt.state
			rt.mu.Unlock()
			return workerIPCResp{OK: false, ErrCode: "acp_inactive", State: curState, WorkerPID: os.Getpid(), ChildPID: curChildPID}
		}
		var aErr error
		acpRes, aErr = drv.BuildACPServerResponse(ctrl.ACP, approved, req.Answer)
		if aErr != nil {
			curState := *rt.state
			rt.mu.Unlock()
			return workerIPCResp{OK: false, ErrCode: aErr.Error(), State: curState, WorkerPID: os.Getpid(), ChildPID: curChildPID}
		}
	} else {
		var bErr error
		ctrlLine, bErr = buildControlResponseJSON(ctrl, approved, req.Answer)
		if bErr != nil {
			curState := *rt.state
			rt.mu.Unlock()
			return workerIPCResp{OK: false, ErrCode: bErr.Error(), State: curState, WorkerPID: os.Getpid(), ChildPID: curChildPID}
		}
	}
	planApproved := isExitPlanTool(ctrl.ToolName) && approved
	updatedPermMode := ""
	if planApproved {
		updatedPermMode = defaultSessionPermissionMode(rt.workerMeta.Agent)
		rt.workerMeta.PermissionMode = updatedPermMode
		if req.ConfigFingerprint != "" {
			rt.workerMeta.ConfigFingerprint = req.ConfigFingerprint
		}
		if req.ConfigFingerprintNoModel != "" {
			rt.workerMeta.ConfigFingerprintNoModel = req.ConfigFingerprintNoModel
		}
	}
	turn := *rt.activeTurn
	*rt.pendingControl = nil
	rt.workerMeta.PendingControl = nil
	*rt.state = "running_turn"
	rt.workerMeta.State = "running_turn"
	rt.workerMeta.UpdatedAt = time.Now().UTC()
	turn.meta.PendingControl = nil
	turn.meta.Status = "running"
	_ = WriteTurnSpoolMeta(turn.metaPath, turn.meta)
	rt.saveMeta()
	_, _ = turn.spool.Append(ChatStreamEvent{
		SessionID: rt.workerMeta.SessionID,
		TurnID:    turn.turnID,
		Type:      "control_resolved",
	})
	rt.notifyWatchersLocked()
	resp := workerIPCResp{
		OK:                       true,
		Approved:                 approved,
		PlanApproved:             planApproved,
		WorkerPID:                os.Getpid(),
		ChildPID:                 g.pgid,
		TurnID:                   turn.turnID,
		State:                    "running_turn",
		ConfigFingerprint:        rt.workerMeta.ConfigFingerprint,
		ConfigFingerprintNoModel: rt.workerMeta.ConfigFingerprintNoModel,
		MCPFingerprint:           rt.workerMeta.MCPFingerprint,
		PermissionMode:           updatedPermMode,
	}
	rt.mu.Unlock()

	if ctrl.ACP != nil && g.acp != nil {
		g.acp.sendControlResponse(ctrl.ACP.RawID, acpRes)
		if planApproved {
			go g.acp.notifyModeAfterPlanApproval("auto")
		}
	} else {
		g.writeLine(string(ctrlLine))
	}
	return resp
}
