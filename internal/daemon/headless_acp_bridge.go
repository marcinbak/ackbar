package daemon

import (
	"os"
	"time"
)

type workerACPBridgeState struct {
	rateLimitedUntil     *time.Time
	rateLimitEnteredAt   *time.Time
	pendingInitConvID    *string
	pendingDetectedModel *string
	completedTurns       map[string]string
	onIdleTTLExpired     func()
	startTurnLocked      func(string) error
	finalizeTurnMeta     func(*activeTurnState, *boundedTailBuffer, string, int, string)
	exitCleanly          func(int)
}

func buildWorkerACPDeps(rt *workerRuntime, b workerACPBridgeState) workerACPDeps {
	return workerACPDeps{
		saveMeta:         rt.saveMeta,
		finalizeTurnMeta: b.finalizeTurnMeta,
		exitCleanly:      b.exitCleanly,
		completedTurns:   b.completedTurns,
		clearRateLimit: func() {
			*b.rateLimitedUntil = time.Time{}
			*b.rateLimitEnteredAt = time.Time{}
		},
		resetIdleTimer: func(wait time.Duration) {
			if wait <= 0 {
				return
			}
			if *rt.idleTimer == nil {
				*rt.idleTimer = time.AfterFunc(wait, b.onIdleTTLExpired)
			} else {
				(*rt.idleTimer).Reset(wait)
			}
		},
		stopIdleTimer: func() {
			if *rt.idleTimer != nil {
				(*rt.idleTimer).Stop()
			}
		},
		onSessionInit: func(g *workerChildGen, convID, model string) {
			rt.mu.Lock()
			if *rt.curChild == g && !g.replaced {
				rt.workerMeta.NativeID = convID
				rt.workerMeta.IsExisting = true
				rt.saveMeta()
				if turn := *rt.activeTurn; turn != nil {
					turn.meta.NativeID = convID
					_ = WriteTurnSpoolMeta(turn.metaPath, turn.meta)
					_, _ = turn.spool.Append(ChatStreamEvent{SessionID: rt.workerMeta.SessionID, TurnID: turn.turnID, Type: "session_init", NativeID: convID, Model: model})
				} else {
					*b.pendingInitConvID, *b.pendingDetectedModel = convID, model
				}
			}
			rt.mu.Unlock()
		},
		onModelDetected: func(g *workerChildGen, m string) {
			rt.mu.Lock()
			if *rt.curChild == g && !g.replaced && m != "" {
				if turn := *rt.activeTurn; turn != nil && turn.spool != nil && *rt.state != "shutting_down" && *rt.state != "exited" {
					_, _ = turn.spool.Append(ChatStreamEvent{SessionID: rt.workerMeta.SessionID, TurnID: turn.turnID, Type: "model_detected", Model: m})
				} else {
					*b.pendingDetectedModel = m
				}
			}
			rt.mu.Unlock()
		},
		onInteractiveBlock: func(g *workerChildGen, expectedTurn *activeTurnState, toolName string, toolInput any, blocked *Blocked) {
			rt.mu.Lock()
			if *rt.curChild != g || g.replaced || *rt.state == "shutting_down" || *rt.state == "exited" || *rt.state == "cancelling" || *rt.activeTurn == nil || (*rt.activeTurn).cancelled || (expectedTurn != nil && *rt.activeTurn != expectedTurn) {
				rt.mu.Unlock()
				return
			}
			turn := *rt.activeTurn
			turn.hadBlock = true
			_, _ = turn.spool.Append(ChatStreamEvent{SessionID: rt.workerMeta.SessionID, TurnID: turn.turnID, Type: "interactive_block", ToolName: toolName, ToolInput: toolInput, Blocked: blocked})
			*rt.pendingControl, rt.workerMeta.PendingControl = nil, nil
			*rt.state, rt.workerMeta.State = "shutting_down", "shutting_down"
			rt.saveMeta()
			rt.notifyWatchersLocked()
			rt.mu.Unlock()
			go func() {
				g.shutdown(500 * time.Millisecond)
				rt.mu.Lock()
				if *rt.activeTurn == turn {
					b.finalizeTurnMeta(turn, g.stderrTail, "completed", 0, "")
					*rt.activeTurn = nil
				}
				rt.mu.Unlock()
				b.exitCleanly(0)
			}()
		},
		onRateLimitBlock: func(g *workerChildGen, turn *activeTurnState, blocked *Blocked) {
			rt.mu.Lock()
			if *rt.curChild != g || g.replaced || *rt.activeTurn != turn || turn.cancelled || *rt.state == "cancelling" || *rt.state == "shutting_down" || *rt.state == "exited" {
				rt.mu.Unlock()
				return
			}
			_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.turnID, Type: "interactive_block", Blocked: blocked})
			turn.hadBlock = true
			b.finalizeTurnMeta(turn, g.stderrTail, "completed", 0, "")
			*b.rateLimitEnteredAt = time.Now()
			if blocked.ResetAt != nil && !blocked.ResetAt.IsZero() {
				*b.rateLimitedUntil = *blocked.ResetAt
			}
			g.stderrTail.Reset()
			*rt.pendingControl, rt.workerMeta.PendingControl = nil, nil
			rt.workerMeta.IsExisting = true
			*rt.state, rt.workerMeta.State = "idle_warm", "idle_warm"
			rt.workerMeta.ActiveTurnID, rt.workerMeta.ActiveMetaPath = "", ""
			rt.workerMeta.LastTurnEndedAt, rt.workerMeta.UpdatedAt = time.Now().UTC(), time.Now().UTC()
			rt.saveMeta()
			*rt.activeTurn = nil
			rt.bgCtrl.markIdleLocked(time.Now())
			rt.notifyWatchersLocked()
			if rt.idleTTL > 0 {
				if *rt.idleTimer == nil {
					*rt.idleTimer = time.AfterFunc(rt.idleTTL, b.onIdleTTLExpired)
				} else {
					(*rt.idleTimer).Reset(rt.idleTTL)
				}
			}
			rt.mu.Unlock()
		},
		onTurnFailure: func(g *workerChildGen, turn *activeTurnState, errMsg string) {
			rt.mu.Lock()
			if *rt.curChild != g || g.replaced || *rt.activeTurn != turn || turn.cancelled || *rt.state == "cancelling" || *rt.state == "shutting_down" || *rt.state == "exited" {
				rt.mu.Unlock()
				return
			}
			_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.turnID, Type: "error", Text: errMsg, IsError: true})
			*rt.state, rt.workerMeta.State = "shutting_down", "shutting_down"
			rt.saveMeta()
			rt.mu.Unlock()
			go func() {
				g.shutdown(500 * time.Millisecond)
				rt.mu.Lock()
				if *rt.activeTurn == turn {
					b.finalizeTurnMeta(turn, g.stderrTail, "failed", 1, "")
					*rt.activeTurn = nil
				}
				rt.mu.Unlock()
				b.exitCleanly(0)
			}()
		},
	}
}

func attachACPWorkerIPC(ext *workerIPCExtensions, rt *workerRuntime, startTurnLocked func(string) error) {
	ext.onACPStartTurn = func(req workerIPCReq) workerIPCResp {
		rt.mu.Lock()
		g := *rt.curChild
		if g != nil && g.acp != nil && *rt.state == "cancelling" {
			acpRef := g.acp
			rt.mu.Unlock()
			acpRef.waitCancelSettled(time.Second)
			rt.mu.Lock()
			g = *rt.curChild
		}
		curChildPID := rt.getChildPGIDLocked()
		if *rt.state == "shutting_down" || *rt.state == "exited" || *rt.state == "restarting_child" {
			st := *rt.state
			rt.mu.Unlock()
			return workerIPCResp{OK: false, ErrCode: st, WorkerPID: os.Getpid(), ChildPID: curChildPID}
		}
		if *rt.state == "running_turn" || *rt.state == "blocked_control" || *rt.state == "cancelling" || *rt.activeTurn != nil {
			activeTID := ""
			if *rt.activeTurn != nil {
				activeTID = (*rt.activeTurn).turnID
			}
			st := *rt.state
			rt.mu.Unlock()
			return workerIPCResp{OK: false, ErrCode: "busy", State: st, TurnID: activeTID, WorkerPID: os.Getpid(), ChildPID: curChildPID}
		}
		if g == nil || g.acp == nil {
			rt.mu.Unlock()
			return workerIPCResp{OK: false, ErrCode: "acp_inactive", WorkerPID: os.Getpid(), ChildPID: curChildPID}
		}
		if req.SessionID != "" {
			rt.workerMeta.SessionID = req.SessionID
		}
		if req.NativeID != "" {
			rt.workerMeta.NativeID = req.NativeID
		}
		if startErr := startTurnLocked(req.MetaPath); startErr != nil {
			rt.mu.Unlock()
			return workerIPCResp{OK: false, ErrCode: startErr.Error()}
		}
		turn, acpRef := *rt.activeTurn, g.acp
		rt.mu.Unlock()
		go acpRef.runPromptTurn(turn, req.Prompt)
		return workerIPCResp{OK: true, WorkerPID: os.Getpid(), ChildPID: curChildPID, TurnID: turn.turnID, State: "running_turn"}
	}
	ext.onACPSetConfig = func(req workerIPCReq) workerIPCResp {
		rt.mu.Lock()
		g, st := *rt.curChild, *rt.state
		rt.mu.Unlock()
		if g == nil || g.acp == nil || st == "shutting_down" || st == "exited" || st == "restarting_child" {
			return workerIPCResp{OK: false, ErrCode: "unsupported_or_inactive", WorkerPID: os.Getpid()}
		}
		return g.acp.handleSetConfigIPC(req)
	}
	ext.onACPCancel = func(req workerIPCReq) workerIPCResp {
		rt.mu.Lock()
		g := *rt.curChild
		rt.mu.Unlock()
		if g == nil || g.acp == nil {
			return workerIPCResp{OK: false, ErrCode: "acp_inactive", WorkerPID: os.Getpid()}
		}
		return g.acp.handleCancelIPC(req)
	}
	ext.onACPRewind = func(req workerIPCReq) workerIPCResp {
		rt.mu.Lock()
		g, st := *rt.curChild, *rt.state
		rt.mu.Unlock()
		if g == nil || g.acp == nil || st == "shutting_down" || st == "exited" || st == "restarting_child" {
			return workerIPCResp{OK: false, ErrCode: "unsupported_or_inactive", WorkerPID: os.Getpid()}
		}
		return g.acp.handleRewindIPC(req)
	}
}
