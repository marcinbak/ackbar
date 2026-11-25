package daemon

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	agentpkg "ackbar/internal/agent"
)

func parseChatStreamReplayCursor(r *http.Request) (string, int64) {
	reqTurnID := strings.TrimSpace(r.URL.Query().Get("turn_id"))
	var afterSeq int64
	if rawAfter := strings.TrimSpace(r.URL.Query().Get("after_seq")); rawAfter != "" {
		if parsed, err := strconv.ParseInt(rawAfter, 10, 64); err == nil && parsed >= 0 {
			afterSeq = parsed
		}
	}
	if lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID")); afterSeq == 0 && lastEventID != "" {
		if idx := strings.LastIndexByte(lastEventID, ':'); idx >= 0 {
			if parsed, err := strconv.ParseInt(strings.TrimSpace(lastEventID[idx+1:]), 10, 64); err == nil && parsed >= 0 {
				afterSeq = parsed
				if reqTurnID == "" && idx > 0 {
					reqTurnID = strings.TrimSpace(lastEventID[:idx])
				}
			}
		} else if parsed, err := strconv.ParseInt(lastEventID, 10, 64); err == nil && parsed >= 0 {
			afterSeq = parsed
		}
	}
	return reqTurnID, afterSeq
}

// SubscribeWithReplay subscribes to live events BEFORE reading the turn spool and deduplicates by Seq so reconnects never miss or duplicate deltas.
func (h *HeadlessRunner) SubscribeWithReplay(sessionID, reqTurnID string, afterSeq int64, streamAgent string) (chan ChatStreamEvent, func()) {
	liveCh, liveCleanup := h.Subscribe(sessionID)
	reg := h.pluginRegistry()

	h.mu.RLock()
	meta := h.findSpoolForSessionLocked(sessionID, "")
	h.mu.RUnlock()
	if meta != nil && meta.MetaPath != "" {
		if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil {
			meta = dm
		}
	}

	var replayEvents []ChatStreamEvent
	var replayTurnID string
	var maxReplayedSeq int64

	if meta != nil {
		isActive := (isActiveTurnStatus(meta.Status) && h.isSpoolWorkerLive(meta)) || h.IsRunning(sessionID)
		shouldReplay := false
		readAfter := afterSeq
		if reqTurnID != "" {
			if meta.TurnID == reqTurnID {
				shouldReplay = true
			} else if isActive {
				shouldReplay = true
				readAfter = 0
			}
		} else if afterSeq > 0 || isActive {
			shouldReplay = true
		}
		if shouldReplay {
			replayTurnID = meta.TurnID
			if events, maxSeq, err := ReadTurnSpoolAfter(meta.SpoolPath, readAfter); err == nil {
				replayEvents = events
				if maxSeq > maxReplayedSeq {
					maxReplayedSeq = maxSeq
				}
			}
		}
	} else if reqTurnID == "" && afterSeq == 0 {
		if activeMsg, ok := h.GetActiveTurnPrompt(sessionID); ok && strings.TrimSpace(activeMsg.Content) != "" {
			replayEvents = []ChatStreamEvent{{
				SessionID: sessionID,
				TurnID:    activeMsg.TurnID,
				Type:      "turn_start",
				Text:      cleanActivePromptWithRegistry(reg, streamAgent, activeMsg.Content),
				Timestamp: activeMsg.Timestamp,
			}}
		}
	}

	outCh := make(chan ChatStreamEvent, 2048)
	done := make(chan struct{})
	var closeOnce sync.Once
	cleanup := func() {
		closeOnce.Do(func() {
			close(done)
			liveCleanup()
		})
	}

	go func() {
		defer close(outCh)
		for _, evt := range replayEvents {
			if evt.Type == "session_init" || evt.Type == "model_detected" {
				continue
			}
			if evt.Type == "turn_start" && evt.Text != "" {
				evt.Text = cleanActivePromptWithRegistry(reg, streamAgent, evt.Text)
			}
			select {
			case outCh <- evt:
			case <-done:
				return
			}
		}
		for {
			select {
			case <-done:
				return
			case evt, ok := <-liveCh:
				if !ok {
					return
				}
				if evt.Type == "session_init" || evt.Type == "model_detected" {
					continue
				}
				if replayTurnID != "" && evt.TurnID == replayTurnID && evt.Seq > 0 && evt.Seq <= maxReplayedSeq {
					continue
				}
				if evt.TurnID != "" && evt.Seq > 0 {
					if evt.TurnID != replayTurnID {
						replayTurnID, maxReplayedSeq = evt.TurnID, evt.Seq
					} else if evt.Seq > maxReplayedSeq {
						maxReplayedSeq = evt.Seq
					}
				}
				if evt.Type == "turn_start" && evt.Text != "" {
					evt.Text = cleanActivePromptWithRegistry(reg, streamAgent, evt.Text)
				}
				select {
				case outCh <- evt:
				case <-done:
					return
				}
			}
		}
	}()
	return outCh, cleanup
}

func (s *Server) mergeInFlightSpoolIntoTranscript(transcript *Transcript, sess *Session, sessionID, nativeID, agent, cwd string) (*Transcript, map[string]interface{}) {
	if s.headless == nil {
		return transcript, nil
	}
	snap := s.headless.GetInFlightTurnSnapshot(sess, sessionID, nativeID, agent)
	if transcript == nil {
		if snap != nil || (sess != nil && sess.EngineType == EngineHeadless) {
			transcript = &Transcript{
				SessionID: sessionID,
				NativeID:  nativeID,
				Agent:     agent,
				Cwd:       cwd,
				Messages:  make([]TranscriptMessage, 0),
			}
		} else {
			return nil, nil
		}
	}
	if snap == nil || (!snap.IsSynthetic && strings.TrimSpace(snap.UserPrompt) == "") {
		return transcript, nil
	}

	msgs := transcript.Messages
	n := len(msgs)
	hasError := snap.StreamHadError || strings.TrimSpace(snap.LastErrorText) != ""
	errText := strings.TrimSpace(snap.LastErrorText)
	hasStreamedAssistant := strings.TrimSpace(snap.AssistantText) != "" || strings.TrimSpace(snap.ThinkingText) != "" || len(snap.ToolCalls) > 0 || snap.HasChecklist || errText != ""
	assistantContent := snap.AssistantText
	if errText != "" {
		if strings.TrimSpace(assistantContent) == "" {
			assistantContent = errText
		} else if !strings.Contains(assistantContent, errText) {
			assistantContent = strings.TrimRight(assistantContent, "\n") + "\n\n" + errText
		}
	}

	if snap.IsSynthetic && strings.TrimSpace(snap.UserPrompt) == "" {
		if !hasStreamedAssistant {
			return transcript, nil
		}
		sameSyntheticAssistant := n > 0 && msgs[n-1].Role == "assistant" &&
			((snap.TurnID != "" && msgs[n-1].TurnID == snap.TurnID) ||
				(msgs[n-1].TurnID == "" && !snap.UserTimestamp.IsZero() &&
					(!msgs[n-1].Timestamp.Before(snap.UserTimestamp) ||
						(!msgs[n-1].Timestamp.Before(snap.UserTimestamp.Add(-100*time.Millisecond)) &&
							strings.TrimSpace(assistantContent) != "" &&
							strings.TrimSpace(msgs[n-1].Content) == strings.TrimSpace(assistantContent)))))
		if !snap.IsActive {
			if !snap.FinishedAt.IsZero() && time.Since(snap.FinishedAt) > 10*time.Second {
				return transcript, nil
			}
			if sameSyntheticAssistant {
				return transcript, nil
			}
		}
		if sameSyntheticAssistant && len(msgs[n-1].Content) >= len(assistantContent) {
			msgs[n-1].InFlight = snap.IsActive
			if snap.TurnID != "" {
				msgs[n-1].TurnID = snap.TurnID
			}
			if snap.ChecklistCleared {
				msgs[n-1].Checklist = nil
				msgs[n-1].ChecklistCleared = true
			} else if snap.HasChecklist {
				msgs[n-1].Checklist = agentpkg.CloneChecklist(snap.Checklist)
				msgs[n-1].ChecklistCleared = false
			}
		} else {
			msgs = append(msgs, TranscriptMessage{
				Role:             "assistant",
				Content:          assistantContent,
				Thinking:         snap.ThinkingText,
				ToolCalls:        snap.ToolCalls,
				Checklist:        agentpkg.CloneChecklist(snap.Checklist),
				ChecklistCleared: snap.ChecklistCleared,
				Timestamp:        snap.UpdatedAt,
				TurnID:           snap.TurnID,
				InFlight:         snap.IsActive,
			})
		}
		transcript.Messages = msgs
		payload := map[string]interface{}{
			"turn_id":             snap.TurnID,
			"last_seq":            snap.LastSeq,
			"active":              snap.IsActive,
			"assistant_text":      assistantContent,
			"thinking":            snap.ThinkingText,
			"tool_calls":          snap.ToolCalls,
			"timestamp":           snap.UserTimestamp,
			"activity_started_at": snap.ActivityStartedAt,
			"activity_kind":       snap.ActivityKind,
		}
		if snap.ChecklistCleared {
			payload["checklist_cleared"] = true
		} else if snap.HasChecklist {
			payload["checklist"] = agentpkg.CloneChecklist(snap.Checklist)
		}
		return transcript, payload
	}

	threshold := snap.UserTimestamp.Add(-5 * time.Second)
	matchedUserIdx := -1
	for i := n - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			if promptsMatchActiveTurn(msgs[i].Content, snap.UserPrompt) &&
				(i == n-1 || !msgs[i].Timestamp.Before(threshold) || !msgs[n-1].Timestamp.Before(threshold)) {
				matchedUserIdx = i
			}
			break
		}
	}

	asstIdx := -1
	if matchedUserIdx >= 0 {
		for i := n - 1; i > matchedUserIdx; i-- {
			if msgs[i].Role == "assistant" {
				asstIdx = i
				break
			}
		}
	}
	hasUnhandledError := errText != "" && (asstIdx <= matchedUserIdx || !strings.Contains(msgs[asstIdx].Content, errText))

	// Suppress spool synthesis once the provider transcript on disk covers the completed turn.
	if !snap.IsActive {
		diskHasChecklist := asstIdx > matchedUserIdx && (len(msgs[asstIdx].Checklist) > 0 || msgs[asstIdx].ChecklistCleared)
		diskHasContent := asstIdx > matchedUserIdx && (strings.TrimSpace(msgs[asstIdx].Content) != "" || diskHasChecklist)
		diskMatchesChecklist := !snap.HasChecklist ||
			(asstIdx > matchedUserIdx && ((snap.ChecklistCleared && msgs[asstIdx].ChecklistCleared) || (!snap.ChecklistCleared && len(msgs[asstIdx].Checklist) > 0)))
		if matchedUserIdx >= 0 && !hasUnhandledError && ((diskHasContent && diskMatchesChecklist) || !hasStreamedAssistant) {
			if snap.TurnID != "" {
				msgs[matchedUserIdx].TurnID = snap.TurnID
				if asstIdx > matchedUserIdx {
					msgs[asstIdx].TurnID = snap.TurnID
				}
			}
			transcript.Messages = msgs
			return transcript, nil
		}
		if matchedUserIdx < 0 && !hasError && (snap.WasCancelled || (!snap.FinishedAt.IsZero() && time.Since(snap.FinishedAt) > 10*time.Second)) {
			return transcript, nil
		}
		if matchedUserIdx < 0 && hasError && n > 0 && !snap.FinishedAt.IsZero() && msgs[n-1].Timestamp.After(snap.FinishedAt) {
			return transcript, nil
		}
	}

	if matchedUserIdx < 0 {
		msgs = append(msgs, TranscriptMessage{
			Role:      "user",
			Content:   snap.UserPrompt,
			Timestamp: snap.UserTimestamp,
			TurnID:    snap.TurnID,
			InFlight:  snap.IsActive,
		})
		if hasStreamedAssistant {
			msgs = append(msgs, TranscriptMessage{
				Role:             "assistant",
				Content:          assistantContent,
				Thinking:         snap.ThinkingText,
				ToolCalls:        snap.ToolCalls,
				Checklist:        agentpkg.CloneChecklist(snap.Checklist),
				ChecklistCleared: snap.ChecklistCleared,
				Timestamp:        snap.UpdatedAt,
				TurnID:           snap.TurnID,
				InFlight:         snap.IsActive,
			})
		}
	} else {
		if snap.TurnID != "" {
			msgs[matchedUserIdx].TurnID = snap.TurnID
		}
		if asstIdx > matchedUserIdx {
			if snap.TurnID != "" {
				msgs[asstIdx].TurnID = snap.TurnID
			}
			if snap.IsActive {
				msgs[asstIdx].InFlight = true
			}
			if len(snap.AssistantText) > len(msgs[asstIdx].Content) {
				msgs[asstIdx].Content = snap.AssistantText
			}
			if errText != "" && !strings.Contains(msgs[asstIdx].Content, errText) {
				trimmed := strings.TrimRight(msgs[asstIdx].Content, "\n")
				if trimmed == "" {
					msgs[asstIdx].Content = errText
				} else {
					msgs[asstIdx].Content = trimmed + "\n\n" + errText
				}
			}
			if len(snap.ThinkingText) > len(msgs[asstIdx].Thinking) {
				msgs[asstIdx].Thinking = snap.ThinkingText
			}
			if len(msgs[asstIdx].ToolCalls) == 0 && len(snap.ToolCalls) > 0 {
				msgs[asstIdx].ToolCalls = snap.ToolCalls
			}
			if snap.ChecklistCleared {
				msgs[asstIdx].Checklist = nil
				msgs[asstIdx].ChecklistCleared = true
			} else if snap.HasChecklist {
				msgs[asstIdx].Checklist = agentpkg.CloneChecklist(snap.Checklist)
				msgs[asstIdx].ChecklistCleared = false
			}
		} else if hasStreamedAssistant {
			msgs = append(msgs, TranscriptMessage{
				Role:             "assistant",
				Content:          assistantContent,
				Thinking:         snap.ThinkingText,
				ToolCalls:        snap.ToolCalls,
				Checklist:        agentpkg.CloneChecklist(snap.Checklist),
				ChecklistCleared: snap.ChecklistCleared,
				Timestamp:        snap.UpdatedAt,
				TurnID:           snap.TurnID,
				InFlight:         snap.IsActive,
			})
		}
	}
	transcript.Messages = msgs

	payload := map[string]interface{}{
		"turn_id":             snap.TurnID,
		"last_seq":            snap.LastSeq,
		"active":              snap.IsActive,
		"prompt":              snap.UserPrompt,
		"assistant_text":      assistantContent,
		"thinking":            snap.ThinkingText,
		"tool_calls":          snap.ToolCalls,
		"timestamp":           snap.UserTimestamp,
		"activity_started_at": snap.ActivityStartedAt,
		"activity_kind":       snap.ActivityKind,
	}
	if snap.ChecklistCleared {
		payload["checklist_cleared"] = true
	} else if snap.HasChecklist {
		payload["checklist"] = agentpkg.CloneChecklist(snap.Checklist)
	}
	return transcript, payload
}
