package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/agentutil"
)

func (s *acpChildSession) waitCancelSettled(timeout time.Duration) {
	s.mu.Lock()
	ch := s.cancelSettledCh
	s.mu.Unlock()
	if ch == nil {
		return
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-ch:
	case <-t.C:
	}
}

func (s *acpChildSession) handleNotification(method string, params map[string]any) {
	notif := s.driver.HandleACPNotification(s.homeDir, s.env, method, params)
	if notif.DetectedModel != "" && s.deps.onModelDetected != nil {
		s.deps.onModelDetected(s.gen, notif.DetectedModel)
	}
	for _, sysEvt := range notif.SystemEvents {
		s.rt.bgCtrl.handleSystemEvent(s.gen, sysEvt)
	}
	if method != "session/update" && method != "_x.ai/session/update" {
		return
	}
	s.mu.Lock()
	activePrompt := s.inPromptTurn
	s.mu.Unlock()
	if !activePrompt {
		return
	}

	s.streamMu.Lock()
	evts, _ := s.streamState.ParseACPSessionUpdate(s.rt.workerMeta.Agent, s.rt.workerMeta.SessionID, params)
	s.streamMu.Unlock()
	for _, ev := range evts {
		if ev.Type == "interactive_block" && ev.Blocked != nil {
			if s.deps.onInteractiveBlock != nil {
				s.deps.onInteractiveBlock(s.gen, nil, ev.ToolName, ev.ToolInput, ev.Blocked)
			}
			continue
		}
		s.rt.mu.Lock()
		valid := *s.rt.curChild == s.gen && !s.gen.replaced && *s.rt.state != "shutting_down" && *s.rt.state != "exited" && *s.rt.state != "cancelling"
		if turn := *s.rt.activeTurn; valid && turn != nil && !turn.cancelled && turn.spool != nil {
			_, _ = turn.spool.Append(ev)
		}
		s.rt.mu.Unlock()
	}
}

func (s *acpChildSession) handleServerRequest(rawID json.RawMessage, reqID, method string, params map[string]any) bool {
	prompt, ok := s.driver.ParseACPServerRequest(rawID, reqID, method, params)
	if !ok || prompt == nil {
		return false
	}
	rawMap, _ := prompt.ToolInput.(map[string]any)
	ctrl := &PendingControlRequest{
		RequestID: prompt.RequestID, Subtype: "acp_control", ToolName: prompt.ToolName,
		RawInput: rawMap, Blocked: prompt.Blocked, ACP: prompt,
	}
	if !s.rt.handleControlRequest(s.gen, ctrl) {
		// Unblock child immediately if no active turn can service the prompt.
		if res, bErr := s.driver.BuildACPServerResponse(prompt, false, "cancel"); bErr == nil {
			_ = s.conn.SendResponse(prompt.RawID, res)
		} else {
			_ = s.conn.SendError(prompt.RawID, -32000, "turn inactive")
		}
	}
	return true
}

func (s *acpChildSession) sendControlResponse(rawID json.RawMessage, result any) {
	_ = s.conn.SendResponse(rawID, result)
}

func (s *acpChildSession) notifyModeAfterPlanApproval(modeID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.mu.Lock()
	sid := s.sessionID
	s.curPermMode = ""
	s.mu.Unlock()
	_ = agentutil.SwitchACPMode(ctx, s.conn, sid, modeID)
}

func (s *acpChildSession) settleCancelledTurnLocked() {
	s.stderrTail.Reset()
	*s.rt.pendingControl = nil
	s.rt.workerMeta.PendingControl = nil
	*s.rt.activeTurn = nil
	s.rt.workerMeta.IsExisting = true
	*s.rt.state, s.rt.workerMeta.State = "idle_warm", "idle_warm"
	s.rt.workerMeta.ActiveTurnID, s.rt.workerMeta.ActiveMetaPath = "", ""
	s.rt.workerMeta.LastTurnEndedAt, s.rt.workerMeta.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	s.deps.saveMeta()
	s.rt.bgCtrl.markIdleLocked(time.Now())
	s.rt.notifyWatchersLocked()
	s.deps.resetIdleTimer(s.rt.idleTTL)
}

func (s *acpChildSession) runPromptTurn(turn *activeTurnState, prompt string) {
	if turn == nil {
		return
	}
	s.waitCancelSettled(time.Second)
	<-s.bootDone

	doneCh := make(chan struct{})
	s.mu.Lock()
	s.rt.mu.Lock()
	active := !turn.cancelled && *s.rt.curChild == s.gen && !s.gen.replaced && *s.rt.activeTurn == turn && *s.rt.state == "running_turn"
	s.rt.mu.Unlock()
	if !active {
		s.mu.Unlock()
		return
	}
	bootErr, bootBlock := s.bootErr, s.bootBlock
	if bootErr != nil {
		s.mu.Unlock()
		if bootBlock != nil && s.deps.onInteractiveBlock != nil {
			s.deps.onInteractiveBlock(s.gen, turn, "", nil, bootBlock)
			return
		}
		if s.deps.onTurnFailure != nil {
			s.deps.onTurnFailure(s.gen, turn, bootErr.Error())
		}
		return
	}
	effectivePrompt := prompt
	if !s.sentFirstTurn && !s.isExisting && s.sysPrompt != "" {
		effectivePrompt = fmt.Sprintf("[System Instruction: %s]\n\n%s", s.sysPrompt, prompt)
	}
	s.sentFirstTurn = true
	sid := s.sessionID
	s.streamMu.Lock()
	s.streamState.ResetTurn()
	s.streamMu.Unlock()
	s.promptDone = doneCh
	s.inPromptTurn = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.inPromptTurn = false
		if s.promptDone == doneCh {
			s.promptDone = nil
		}
		if s.cancelTimer != nil {
			s.cancelTimer.Stop()
			s.cancelTimer = nil
		}
		settledCh := s.cancelSettledCh
		s.cancelSettledCh = nil
		s.mu.Unlock()
		close(doneCh)
		if settledCh != nil {
			close(settledCh)
		}
	}()

	resRaw, rpcErr, callErr := s.conn.Call(context.Background(), "session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": effectivePrompt}},
	})

	s.mu.Lock()
	s.inPromptTurn = false
	if s.cancelTimer != nil {
		s.cancelTimer.Stop()
		s.cancelTimer = nil
	}
	s.mu.Unlock()

	s.rt.mu.Lock()
	if *s.rt.curChild != s.gen || s.gen.replaced || *s.rt.state == "shutting_down" || *s.rt.state == "exited" {
		s.rt.mu.Unlock()
		return
	}
	if *s.rt.state == "cancelling" || turn.cancelled {
		s.settleCancelledTurnLocked()
		s.rt.mu.Unlock()
		return
	}
	if *s.rt.activeTurn != turn {
		s.rt.mu.Unlock()
		return
	}
	s.rt.mu.Unlock()

	if callErr != nil && !errors.Is(callErr, context.DeadlineExceeded) {
		s.waitStderrDrained(500 * time.Millisecond)
	}

	s.streamMu.Lock()
	lastText := s.streamState.LastText()
	s.streamMu.Unlock()

	if callErr != nil || rpcErr != nil {
		errMsg := ""
		if rpcErr != nil {
			errMsg = rpcErr.Error()
		} else {
			errMsg = callErr.Error()
			if tail := strings.TrimSpace(s.stderrTail.String()); tail != "" && !strings.Contains(errMsg, tail) {
				errMsg = tail
			}
		}
		combined := strings.TrimSpace(errMsg + "\n" + lastText + "\n" + s.stderrTail.String())
		if failBlock := detectTurnFailureBlockWithRegistry(s.reg, s.rt.workerMeta.Agent, combined); failBlock != nil {
			if failBlock.Kind == BlockRateLimit && s.deps.onRateLimitBlock != nil {
				s.deps.onRateLimitBlock(s.gen, turn, failBlock)
				return
			}
			if s.deps.onInteractiveBlock != nil {
				s.deps.onInteractiveBlock(s.gen, turn, "", nil, failBlock)
				return
			}
		}
		if s.deps.onTurnFailure != nil {
			s.deps.onTurnFailure(s.gen, turn, errMsg)
		}
		return
	}

	resText := lastText
	if resText == "" && len(resRaw) > 0 {
		var m map[string]any
		if json.Unmarshal(resRaw, &m) == nil {
			if t, ok := m["text"].(string); ok {
				resText = t
			}
		}
	}

	s.rt.mu.Lock()
	if *s.rt.curChild != s.gen || s.gen.replaced || *s.rt.state == "shutting_down" || *s.rt.state == "exited" {
		s.rt.mu.Unlock()
		return
	}
	if *s.rt.state == "cancelling" || turn.cancelled {
		s.settleCancelledTurnLocked()
		s.rt.mu.Unlock()
		return
	}
	if *s.rt.activeTurn != turn {
		s.rt.mu.Unlock()
		return
	}
	_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.turnID, Type: "turn_complete", Text: resText})
	s.deps.finalizeTurnMeta(turn, s.stderrTail, "completed", 0, resText)
	s.deps.clearRateLimit()
	s.stderrTail.Reset()
	*s.rt.pendingControl = nil
	s.rt.workerMeta.PendingControl = nil
	s.rt.workerMeta.IsExisting = true
	*s.rt.state, s.rt.workerMeta.State = "idle_warm", "idle_warm"
	s.rt.workerMeta.ActiveTurnID, s.rt.workerMeta.ActiveMetaPath = "", ""
	s.rt.workerMeta.LastTurnEndedAt, s.rt.workerMeta.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	s.deps.saveMeta()
	*s.rt.activeTurn = nil
	s.rt.bgCtrl.markIdleLocked(time.Now())
	s.rt.notifyWatchersLocked()
	s.deps.resetIdleTimer(s.rt.idleTTL)
	s.rt.mu.Unlock()
}
