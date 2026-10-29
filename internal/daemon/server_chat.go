package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ackbar/internal/tmux"
)

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Prompt    string `json:"prompt"`
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" || req.Prompt == "" {
		http.Error(w, "Missing session_id or prompt", http.StatusBadRequest)
		return
	}

	sess, handled := s.resolveSessionOrProxy(w, r.Method, req.SessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return "/v1/sessions/prompt"
	})
	if handled {
		return
	}

	if sess.EngineType == EngineHeadless {
		if s.headless != nil && s.headless.IsRunning(sess.ID) {
			item := s.headless.EnqueuePrompt(sess.ID, req.Prompt)
			s.headless.EmitQueueUpdate(sess.ID)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "queued",
				"id":     item.ID,
			})
			return
		}
		if s.headless != nil {
			go func() {
				_ = s.headless.RunTurn(context.Background(), sess, req.Prompt)
			}()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "running",
		})
		return
	}

	// Tmux session prompt injection
	if sess.TmuxName == "" {
		http.Error(w, "Session has no active tmux process", http.StatusBadRequest)
		return
	}

	if err := tmux.SendInput(r.Context(), sess.TmuxName, req.Prompt, true); err != nil {
		http.Error(w, fmt.Sprintf("Failed to send prompt to tmux: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "sent",
	})
}

func (s *Server) handlePromptQueue(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("id")
	}
	if sessionID == "" {
		http.Error(w, "Missing session_id query parameter", http.StatusBadRequest)
		return
	}

	sess := s.resolveSession(sessionID)
	if sess == nil {
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 2 {
			if hostRec := s.resolveHost(parts[1]); hostRec != nil && hostRec.URL != "" {
				targetURL := fmt.Sprintf("%s/v1/sessions/prompt/queue?%s", strings.TrimSuffix(hostRec.URL, "/"), r.URL.RawQuery)
				fwdReq, err := http.NewRequest(r.Method, targetURL, r.Body)
				if err == nil {
					fwdReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))
					if resp, err := http.DefaultClient.Do(fwdReq); err == nil {
						defer resp.Body.Close()
						w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
						w.WriteHeader(resp.StatusCode)
						_, _ = io.Copy(w, resp.Body)
						return
					}
				}
			}
		}
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if hostRec := s.resolveHost(sess.Host); hostRec != nil && hostRec.URL != "" {
		targetURL := fmt.Sprintf("%s/v1/sessions/prompt/queue?%s", strings.TrimSuffix(hostRec.URL, "/"), r.URL.RawQuery)
		fwdReq, err := http.NewRequest(r.Method, targetURL, r.Body)
		if err == nil {
			fwdReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))
			if resp, err := http.DefaultClient.Do(fwdReq); err == nil {
				defer resp.Body.Close()
				w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
				w.WriteHeader(resp.StatusCode)
				_, _ = io.Copy(w, resp.Body)
				return
			}
		}
	}

	if s.headless == nil {
		http.Error(w, "Headless engine not available", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, paused := s.headless.GetPromptQueue(sess.ID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"items":  items,
			"paused": paused,
		})

	case http.MethodDelete:
		itemID := r.URL.Query().Get("item_id")
		if itemID != "" {
			s.headless.DeletePromptQueueItem(sess.ID, itemID)
		} else {
			s.headless.ClearPromptQueue(sess.ID)
		}
		s.headless.EmitQueueUpdate(sess.ID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePromptQueueResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("id")
	}
	if sessionID == "" {
		var req struct {
			SessionID string `json:"session_id"`
		}
		if bodyBytes, err := io.ReadAll(r.Body); err == nil && len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &req)
			sessionID = req.SessionID
		}
	}
	if sessionID == "" {
		http.Error(w, "Missing session_id", http.StatusBadRequest)
		return
	}

	sess := s.resolveSession(sessionID)
	if sess == nil {
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 2 {
			if hostRec := s.resolveHost(parts[1]); hostRec != nil && hostRec.URL != "" {
				targetURL := fmt.Sprintf("%s/v1/sessions/prompt/queue/resume?session_id=%s", strings.TrimSuffix(hostRec.URL, "/"), url.QueryEscape(sessionID))
				fwdReq, err := http.NewRequest(r.Method, targetURL, nil)
				if err == nil {
					if resp, err := http.DefaultClient.Do(fwdReq); err == nil {
						defer resp.Body.Close()
						w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
						w.WriteHeader(resp.StatusCode)
						_, _ = io.Copy(w, resp.Body)
						return
					}
				}
			}
		}
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if hostRec := s.resolveHost(sess.Host); hostRec != nil && hostRec.URL != "" {
		targetURL := fmt.Sprintf("%s/v1/sessions/prompt/queue/resume?session_id=%s", strings.TrimSuffix(hostRec.URL, "/"), url.QueryEscape(sess.ID))
		fwdReq, err := http.NewRequest(r.Method, targetURL, nil)
		if err == nil {
			if resp, err := http.DefaultClient.Do(fwdReq); err == nil {
				defer resp.Body.Close()
				w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
				w.WriteHeader(resp.StatusCode)
				_, _ = io.Copy(w, resp.Body)
				return
			}
		}
	}

	if s.headless == nil {
		http.Error(w, "Headless engine not available", http.StatusInternalServerError)
		return
	}

	s.headless.SetQueuePaused(sess.ID, false)
	s.headless.EmitQueueUpdate(sess.ID)

	// If runner is not currently running, trigger next queued prompt
	if !s.headless.IsRunning(sess.ID) {
		nextItem := s.headless.DequeuePrompt(sess.ID)
		if nextItem != nil {
			s.headless.EmitQueueUpdate(sess.ID)
			go func() {
				_ = s.headless.RunTurn(context.Background(), sess, nextItem.Text)
			}()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "resumed"})
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("id")
	}
	if sessionID == "" {
		http.Error(w, "Missing session_id query parameter", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush()

	if s.headless == nil {
		return
	}

	targetID := sessionID
	if sess := s.resolveSession(sessionID); sess != nil {
		targetID = sess.ID
	}

	ch, cleanup := s.headless.Subscribe(targetID)
	defer cleanup()

	// Initial connected heartbeat
	initEvt, _ := json.Marshal(ChatStreamEvent{
		SessionID: targetID,
		Type:      "status",
		Text:      "connected",
		Timestamp: time.Now(),
	})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", initEvt)
	flusher.Flush()

	// Initial prompt queue state snapshot
	items, paused := s.headless.GetPromptQueue(targetID)
	if len(items) > 0 || paused {
		qEvt, _ := json.Marshal(ChatStreamEvent{
			SessionID:   targetID,
			Type:        "queue_update",
			QueueItems:  items,
			QueuePaused: paused,
			Timestamp:   time.Now(),
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", qEvt)
		flusher.Flush()
	}

	notify := r.Context().Done()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-notify:
			return
		case <-heartbeat.C:
			// Standard SSE comment keepalive to prevent NAT / proxy / client timeout
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleCancelTurn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		req.SessionID = r.URL.Query().Get("session_id")
		if req.SessionID == "" {
			req.SessionID = r.URL.Query().Get("id")
		}
	}
	if req.SessionID == "" {
		http.Error(w, "Missing session_id", http.StatusBadRequest)
		return
	}

	sess, handled := s.resolveSessionOrProxy(w, r.Method, req.SessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return "/v1/sessions/cancel"
	})
	if handled {
		return
	}

	if sess.EngineType == EngineHeadless {
		if s.headless != nil {
			_ = s.headless.CancelTurn(sess.ID)
		}
		sess.State = StateIdle
		sess.Activity = "Turn cancelled"
		_ = s.db.SaveSession(sess)
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
		return
	}

	if sess.TmuxName != "" {
		_ = tmux.SendKeys(r.Context(), sess.TmuxName, "C-c")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
}

func (s *Server) handleSessionHandover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID                string `json:"id"`
		SessionID         string `json:"session_id"`
		Strategy          string `json:"strategy"` // "in_place" (default) or "new_session"
		CustomInstruction string `json:"custom_instruction"`
		Sync              bool   `json:"sync"`
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err == nil && len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, &req)
	}

	if req.ID == "" {
		req.ID = req.SessionID
	}
	if req.ID == "" {
		req.ID = r.URL.Query().Get("id")
		if req.ID == "" {
			req.ID = r.URL.Query().Get("session_id")
		}
	}
	if req.Strategy == "" {
		req.Strategy = r.URL.Query().Get("strategy")
	}
	if req.Strategy == "" {
		req.Strategy = "in_place"
	}
	if req.CustomInstruction == "" {
		req.CustomInstruction = r.URL.Query().Get("custom_instruction")
	}
	if !req.Sync && r.URL.Query().Get("sync") == "true" {
		req.Sync = true
	}

	if req.ID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	payload, _ := json.Marshal(req)
	sess, handled := s.resolveSessionOrProxy(w, r.Method, req.ID, "application/json", bytes.NewReader(payload), func(_ *Session) string {
		return "/v1/sessions/handover"
	})
	if handled {
		return
	}

	// Validation constraints
	if !sess.Managed && sess.TmuxName == "" && sess.EngineType != EngineHeadless {
		http.Error(w, "Cannot perform handover on unmanaged session without process supervisor", http.StatusBadRequest)
		return
	}

	if sess.State == StateWorking {
		http.Error(w, "Cannot perform handover while session is actively working. Wait for turn to complete or cancel it first.", http.StatusConflict)
		return
	}

	if sess.State == StateEnded || sess.Deleted {
		http.Error(w, "Cannot perform handover on ended or deleted session", http.StatusBadRequest)
		return
	}

	briefingPrompt := "Please generate a concise, structured handover briefing for a fresh session continuing this work:\n" +
		"1. **Core Objective:** What is the primary task?\n" +
		"2. **Completed Milestones:** What has been implemented, verified, or tested?\n" +
		"3. **Current State:** Active git branch, modified files, uncommitted changes.\n" +
		"4. **Immediate Next Steps:** Exact next steps for the incoming turn to pick up.\n" +
		"Format your response clearly so it can be ingested by the next agent turn."

	if req.CustomInstruction != "" {
		briefingPrompt += "\n\nAdditional instructions for the next session:\n" + req.CustomInstruction
	}

	if req.Sync {
		if err := s.executeSessionHandover(r.Context(), sess, req.Strategy, req.CustomInstruction, briefingPrompt); err != nil {
			http.Error(w, fmt.Sprintf("Handover execution failed: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "completed",
			"id":       sess.ID,
			"strategy": req.Strategy,
			"message":  "Handover executed successfully",
		})
		return
	}

	// Asynchronous background execution
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = s.executeSessionHandover(ctx, sess, req.Strategy, req.CustomInstruction, briefingPrompt)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "handover_initiated",
		"id":       sess.ID,
		"strategy": req.Strategy,
		"message":  "Generating handover briefing and rotating context...",
	})
}

func (s *Server) executeSessionHandover(ctx context.Context, sess *Session, strategy, customInstruction, prompt string) error {
	// 1. Mark session as generating briefing
	sess.Activity = "Handover: Generating briefing..."
	sess.State = StateWorking
	sess.LastEventAt = time.Now()
	_ = s.db.SaveSession(sess)
	s.broadcast(sess)

	// 2. Dispatch briefing generation prompt
	if sess.EngineType == EngineHeadless && s.headless != nil {
		go func() {
			_ = s.headless.RunTurn(ctx, sess, prompt)
		}()
	} else if sess.TmuxName != "" {
		if err := tmux.SendInput(ctx, sess.TmuxName, prompt, true); err != nil {
			sess.Activity = fmt.Sprintf("Handover failed: %v", err)
			sess.State = StateIdle
			_ = s.db.SaveSession(sess)
			s.broadcast(sess)
			return err
		}
	}

	// 3. Wait for briefing turn completion (up to 90s)
	pollTicker := time.NewTicker(500 * time.Millisecond)
	defer pollTicker.Stop()

	briefingDone := false
	startTime := time.Now()
	for !briefingDone {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pollTicker.C:
			current := s.resolveSession(sess.ID)
			if current == nil {
				return fmt.Errorf("session disappeared during handover")
			}
			if (current.State == StateIdle || current.State == StateBlocked) && current.Activity != "Handover: Generating briefing..." {
				briefingDone = true
				sess = current
				break
			}
			if time.Since(startTime) > 90*time.Second {
				briefingDone = true
				sess = current
				break
			}
		}
	}

	// 4. Extract generated briefing from transcript
	var briefingContent string
	if transcript, err := s.ExtractTranscript(sess.Agent, sess.NativeID, sess.Cwd); err == nil && transcript != nil && len(transcript.Messages) > 0 {
		for i := len(transcript.Messages) - 1; i >= 0; i-- {
			if transcript.Messages[i].Role == "assistant" && strings.TrimSpace(transcript.Messages[i].Content) != "" {
				briefingContent = strings.TrimSpace(transcript.Messages[i].Content)
				break
			}
		}
	}

	if briefingContent == "" {
		briefingContent = fmt.Sprintf("Handover briefing for %s in %s (branch: %s).\nPlease inspect latest changes and continue with pending tasks.",
			sess.Name, sess.Cwd, sess.GitBranch)
	}

	// 5. Build continuation / reseeding prompt
	var reseedingPrompt strings.Builder
	reseedingPrompt.WriteString("Continuing work from previous session handover:\n\n")
	reseedingPrompt.WriteString(briefingContent)
	if customInstruction != "" {
		reseedingPrompt.WriteString("\n\n**Additional instructions for this turn:**\n")
		reseedingPrompt.WriteString(customInstruction)
	}
	reseedingPrompt.WriteString("\n\nPlease review the briefing above and continue with the immediate next steps.")

	// 6. Context Rotation based on strategy
	resetCmd := s.agentAdapter(sess.Agent).GetResetCommand()
	if resetCmd == "" {
		resetCmd = "/clear"
	}

	if strategy == "new_session" {
		sess.Managed = false
		sess.State = StateEnded
		sess.Activity = "Handover complete (rotated to new session)"
		_ = s.db.SaveSession(sess)
		s.broadcast(sess)

		newUUID := generateUUID()
		newTmuxName := fmt.Sprintf("ackbar-%s-%s", sess.Agent, newUUID)
		launchCmd := s.getSpawnCmd(sess.Agent, newUUID)
		if err := tmux.Spawn(ctx, newTmuxName, sess.Cwd, launchCmd); err != nil {
			return fmt.Errorf("failed to spawn new handover session: %w", err)
		}

		newSess := &Session{
			ID:          fmt.Sprintf("%s:%s:%s", sess.Agent, sess.Host, newUUID),
			Agent:       sess.Agent,
			Host:        sess.Host,
			NativeID:    newUUID,
			Cwd:         sess.Cwd,
			NodePath:    sess.NodePath,
			ProjectKey:  sess.ProjectKey,
			Name:        fmt.Sprintf("%s (Handover)", sess.Name),
			Managed:     true,
			TmuxName:    newTmuxName,
			State:       StateWorking,
			Activity:    "Seeding fresh handover turn...",
			StartedAt:   time.Now(),
			LastEventAt: time.Now(),
			AccountID:   sess.AccountID,
		}
		_ = s.db.SaveSession(newSess)
		s.broadcast(newSess)

		time.Sleep(1500 * time.Millisecond)
		_ = tmux.SendInput(ctx, newTmuxName, reseedingPrompt.String(), true)
		newSess.Activity = "Handover briefing reseeded"
		_ = s.db.SaveSession(newSess)
		s.broadcast(newSess)
		return nil
	}

	// In-place turn rotation (/clear or /reset)
	if sess.TmuxName != "" {
		sess.Activity = "Handover: Resetting context window..."
		_ = s.db.SaveSession(sess)
		s.broadcast(sess)

		_ = tmux.SendInput(ctx, sess.TmuxName, resetCmd, true)

		time.Sleep(1500 * time.Millisecond)

		_ = tmux.SendInput(ctx, sess.TmuxName, reseedingPrompt.String(), true)

		sess.Activity = "Handover: Briefing reseeded into fresh turn"
		_ = s.db.SaveSession(sess)
		s.broadcast(sess)
	}

	return nil
}

func (s *Server) handleSessionTranscript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("session_id")
	}
	if sessionID == "" {
		http.Error(w, "Missing id parameter", http.StatusBadRequest)
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format")) // "json", "ansi", "markdown"
	if format == "" {
		format = "json"
	}

	sess := s.resolveSession(sessionID)
	if sess == nil {
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 2 {
			if hostRec := s.resolveHost(parts[1]); hostRec != nil && hostRec.URL != "" {
				targetURL := fmt.Sprintf("%s/v1/sessions/transcript?%s", strings.TrimSuffix(hostRec.URL, "/"), r.URL.RawQuery)
				resp, err := http.Get(targetURL)
				if err == nil {
					defer resp.Body.Close()
					w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
					w.WriteHeader(resp.StatusCode)
					_, _ = io.Copy(w, resp.Body)
					return
				}
			}
		}
	} else if hostRec := s.resolveHost(sess.Host); hostRec != nil && hostRec.URL != "" {
		targetURL := fmt.Sprintf("%s/v1/sessions/transcript?%s", strings.TrimSuffix(hostRec.URL, "/"), r.URL.RawQuery)
		resp, err := http.Get(targetURL)
		if err == nil {
			defer resp.Body.Close()
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
	}

	agent := "claude-code"
	nativeID := sessionID
	cwd := ""
	title := ""

	if sess != nil {
		agent = sess.Agent
		nativeID = sess.NativeID
		cwd = sess.Cwd
		title = sess.Name
	} else {
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 3 {
			agent = parts[0]
			nativeID = parts[2]
		}
	}

	transcript, err := s.ExtractTranscript(agent, nativeID, cwd)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to extract transcript: %v", err), http.StatusNotFound)
		return
	}
	if title != "" {
		transcript.Title = title
	} else if sess != nil {
		transcript.Title = sess.Name
	}

	switch format {
	case "ansi":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(FormatTranscriptANSI(transcript)))
	case "markdown", "md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(FormatTranscriptMarkdown(transcript)))
	default:
		runningSubs := s.getRunningSubagents(sessionID, nativeID, agent, cwd)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"session_id":        sessionID,
			"native_id":         nativeID,
			"agent":             agent,
			"title":             transcript.Title,
			"cwd":               transcript.Cwd,
			"messages":          transcript.Messages,
			"running_subagents": runningSubs,
			"subagents":         runningSubs,
			"ansi":              FormatTranscriptANSI(transcript),
			"markdown":          FormatTranscriptMarkdown(transcript),
		})
	}
}

func (s *Server) ExtractTranscript(agent, nativeID, cwd string) (*Transcript, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	t := &Transcript{
		NativeID: nativeID,
		Agent:    agent,
		Cwd:      cwd,
		Messages: make([]TranscriptMessage, 0),
	}

	if p, ok := s.providers[agent]; ok {
		msgs, err := p.ExtractTranscript(home, cwd, nativeID)
		if err == nil && len(msgs) > 0 {
			t.Messages = msgs
			return t, nil
		}
	}

	for _, p := range s.providers {
		msgs, err := p.ExtractTranscript(home, cwd, nativeID)
		if err == nil && len(msgs) > 0 {
			t.Messages = msgs
			t.Agent = p.Agent()
			return t, nil
		}
	}

	// Legacy fallback
	return ExtractTranscript(agent, nativeID, cwd)
}
