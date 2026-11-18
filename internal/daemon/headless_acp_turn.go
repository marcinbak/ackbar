package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
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

func (s *acpChildSession) runPromptTurn(turn *activeTurnState, prompt string) {
	if turn == nil {
		return
	}
	s.waitCancelSettled(time.Second)
	<-s.bootDone

	s.rt.mu.Lock()
	cancelledBeforeCall := turn.cancelled || *s.rt.curChild != s.gen || s.gen.replaced || *s.rt.state == "shutting_down" || *s.rt.state == "exited"
	s.rt.mu.Unlock()
	if cancelledBeforeCall {
		return
	}

	s.mu.Lock()
	bootErr, bootBlock := s.bootErr, s.bootBlock
	if bootErr != nil {
		s.mu.Unlock()
		if bootBlock != nil && s.deps.onInteractiveBlock != nil {
			s.deps.onInteractiveBlock(s.gen, "", nil, bootBlock)
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
	doneCh := make(chan struct{})
	s.promptDone = doneCh
	s.mu.Unlock()

	s.streamMu.Lock()
	s.streamState.ResetTurn()
	s.streamMu.Unlock()

	resRaw, rpcErr, callErr := s.conn.Call(context.Background(), "session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": effectivePrompt}},
	})
	close(doneCh)

	s.mu.Lock()
	if s.promptDone == doneCh {
		s.promptDone = nil
	}
	settledCh := s.cancelSettledCh
	s.cancelSettledCh = nil
	if s.cancelTimer != nil {
		s.cancelTimer.Stop()
		s.cancelTimer = nil
	}
	s.mu.Unlock()

	s.rt.mu.Lock()
	if *s.rt.curChild != s.gen || s.gen.replaced || *s.rt.state == "shutting_down" || *s.rt.state == "exited" {
		s.rt.mu.Unlock()
		if settledCh != nil {
			close(settledCh)
		}
		return
	}
	if *s.rt.state == "cancelling" || turn.cancelled {
		s.stderrTail.Reset()
		*s.rt.pendingControl = nil
		s.rt.workerMeta.PendingControl = nil
		*s.rt.state, s.rt.workerMeta.State = "idle_warm", "idle_warm"
		s.rt.workerMeta.ActiveTurnID, s.rt.workerMeta.ActiveMetaPath = "", ""
		s.rt.workerMeta.LastTurnEndedAt, s.rt.workerMeta.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		s.deps.saveMeta()
		s.rt.bgCtrl.markIdleLocked(time.Now())
		s.rt.notifyWatchersLocked()
		s.deps.resetIdleTimer(s.rt.idleTTL)
		s.rt.mu.Unlock()
		if settledCh != nil {
			close(settledCh)
		}
		return
	}
	s.rt.mu.Unlock()

	s.streamMu.Lock()
	lastText := s.streamState.LastText()
	s.streamMu.Unlock()

	if callErr != nil || rpcErr != nil {
		errMsg := ""
		if rpcErr != nil {
			errMsg = rpcErr.Error()
		} else {
			errMsg = callErr.Error()
		}
		combined := strings.TrimSpace(errMsg + "\n" + lastText + "\n" + s.stderrTail.String())
		if failBlock := detectTurnFailureBlockWithRegistry(s.reg, s.rt.workerMeta.Agent, combined); failBlock != nil {
			if failBlock.Kind == BlockRateLimit && s.deps.onRateLimitBlock != nil {
				s.deps.onRateLimitBlock(s.gen, turn, failBlock)
				return
			}
			if s.deps.onInteractiveBlock != nil {
				s.deps.onInteractiveBlock(s.gen, "", nil, failBlock)
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
	_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.turnID, Type: "turn_complete", Text: resText})

	s.rt.mu.Lock()
	if *s.rt.curChild != s.gen || s.gen.replaced || *s.rt.state == "shutting_down" || *s.rt.state == "exited" {
		s.rt.mu.Unlock()
		return
	}
	s.deps.finalizeTurnMeta(turn, s.stderrTail, "completed", 0, resText)
	s.deps.clearRateLimit()
	s.stderrTail.Reset()
	*s.rt.pendingControl = nil
	s.rt.workerMeta.PendingControl = nil
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
