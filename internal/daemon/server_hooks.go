package daemon

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ackbar/internal/tmux"
)

type sessionHookQueue struct {
	mu   sync.Mutex
	tail chan struct{}
}

func hookLockKey(agent, nativeID, sessionID string) string {
	if nativeID != "" {
		return agent + ":" + nativeID
	}
	return sessionID
}

func (s *Server) reserveSessionHook(lockKey string) (func(), func()) {
	val, _ := s.sessionHookLocks.LoadOrStore(lockKey, &sessionHookQueue{})
	q := val.(*sessionHookQueue)
	q.mu.Lock()
	prev := q.tail
	curr := make(chan struct{})
	q.tail = curr
	q.mu.Unlock()
	var once sync.Once
	return func() {
			if prev != nil {
				<-prev
			}
		}, func() {
			once.Do(func() {
				close(curr)
			})
		}
}

func (s *Server) lockSessionHook(lockKey string) func() {
	wait, release := s.reserveSessionHook(lockKey)
	wait()
	return release
}

func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Route is /v1/hooks/{agent}
	parts := stringsSplit(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid hook path", http.StatusBadRequest)
		return
	}
	agentName := parts[3]

	p, exists := s.providers[agentName]
	if !exists {
		http.Error(w, fmt.Sprintf("Unsupported agent provider: %s", agentName), http.StatusBadRequest)
		return
	}

	// Read hook payload
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}

	urlEventName := r.URL.Query().Get("event")
	headerHost := r.Header.Get("X-Ackbar-Host")
	accountParam := r.URL.Query().Get("account")

	event, parseErr := p.ParseHook(urlEventName, body)
	var wait, release func()
	if parseErr == nil && event != nil {
		if event.EventName == "" && urlEventName != "" {
			event.EventName = urlEventName
		}
		if s.agentAdapter(event.Agent).IsValidNativeID(event.NativeID) {
			host := headerHost
			if host == "" || host == "local" {
				host = s.HostName()
			}
			sessionID := fmt.Sprintf("%s:%s:%s", event.Agent, host, event.NativeID)
			lockKey := hookLockKey(event.Agent, event.NativeID, sessionID)
			wait, release = s.reserveSessionHook(lockKey)
		}
	}

	// Fast response to prevent blocking the agent loop
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"enqueued"}`))

	// Process in a goroutine so ingest is single-digit milliseconds while preserving arrival order
	go func() {
		if release != nil {
			defer release()
		}
		if parseErr != nil {
			log.Printf("Error parsing hook payload: %v", parseErr)
			return
		}
		if event == nil {
			return
		}
		if wait != nil {
			wait()
		}
		s.processParsedHookEvent(event, urlEventName, headerHost, accountParam, release != nil)
	}()
}

func (s *Server) processHookEvent(p Provider, urlEventName string, headerHost string, body []byte) {
	s.processHookEventWithAccount(p, urlEventName, headerHost, body, "")
}

func (s *Server) processHookEventWithAccount(p Provider, urlEventName string, headerHost string, body []byte, accountParam string) {
	event, err := p.ParseHook(urlEventName, body)
	if err != nil {
		log.Printf("Error parsing hook payload: %v", err)
		return
	}
	if event == nil {
		return
	}
	s.processParsedHookEvent(event, urlEventName, headerHost, accountParam, false)
}

func (s *Server) processParsedHookEvent(event *Event, urlEventName string, headerHost string, accountParam string, preLocked bool) {
	if event.EventName == "" && urlEventName != "" {
		event.EventName = urlEventName
	}

	if !s.agentAdapter(event.Agent).IsValidNativeID(event.NativeID) {
		log.Printf("Dropping %s hook event with invalid native ID %q", event.Agent, event.NativeID)
		return
	}

	host := headerHost
	if host == "" || host == "local" {
		host = s.HostName()
	}

	// ID is "{agent}:{host}:{nativeID}"
	sessionID := fmt.Sprintf("%s:%s:%s", event.Agent, host, event.NativeID)
	lockKey := hookLockKey(event.Agent, event.NativeID, sessionID)

	if !preLocked {
		unlock := s.lockSessionHook(lockKey)
		defer unlock()
	}

	sess, ok := s.resolveOrAdoptHookSession(event, host, sessionID, accountParam)
	if !ok {
		return
	}

	s.updateHookSessionMetadataAndTitle(sess, event)

	if sess.ID != "" && sess.ID != sessionID {
		sessionID = sess.ID
		secondKey := hookLockKey(sess.Agent, sess.NativeID, sess.ID)
		if secondKey != lockKey {
			unlock2 := s.lockSessionHook(secondKey)
			defer unlock2()
		}
	}

	isPureHeadless := sess.EngineType == EngineHeadless && sess.TmuxName == ""
	headlessRunning := isPureHeadless && s.headless != nil && s.headless.IsRunning(sess.ID)

	s.applyHookStateTransition(sess, event, sessionID, isPureHeadless, headlessRunning)
	s.trackHookSubagentEvent(sessionID, event)

	// Update running subagents count on session
	if isPureHeadless && !headlessRunning {
		s.clearInMemorySubagents(sessionID, event.NativeID)
		sess.RunningSubagents = 0
	} else {
		sess.RunningSubagents = len(s.getRunningSubagents(sessionID, event.NativeID, event.Agent, sess.Cwd))
	}
	sess.UpdatedAt = time.Now()

	// Save to SQLite
	if err := s.db.SaveSession(sess); err != nil {
		log.Printf("Error saving session: %v", err)
		return
	}

	// Synchronize session with task board and ingest ambient tool telemetry
	s.SyncSessionTaskWorker(sess)
	s.IngestToolTelemetry(sess, event)

	// Broadcast update
	s.broadcast(sess)
}

func (s *Server) resolveOrAdoptHookSession(event *Event, host, sessionID, accountParam string) (*Session, bool) {
	sess, err := s.db.GetSession(sessionID)
	if err != nil {
		log.Printf("Error retrieving session %s: %v", sessionID, err)
		return nil, false
	}

	if sess == nil || !sess.Managed {
		// Look for a spawning managed session to adopt
		if spawningSess, err := s.findSpawningSession(event.Agent, event.Cwd); err == nil && spawningSess != nil && spawningSess.ID != sessionID {
			// Adopt! Delete the spawning temp record
			_ = s.db.DeleteSession(spawningSess.ID)

			tmuxName := spawningSess.TmuxName
			if strings.HasPrefix(tmuxName, "ackbar-") && event.NativeID != "" {
				newTmuxName := fmt.Sprintf("ackbar-%s-%s", event.Agent, event.NativeID)
				if tmuxName != newTmuxName {
					if rErr := tmux.Rename(context.Background(), tmuxName, newTmuxName); rErr == nil {
						tmuxName = newTmuxName
					} else {
						log.Printf("[daemon] failed to rename tmux session %s to %s on hook adoption: %v", tmuxName, newTmuxName, rErr)
					}
				}
			}

			sess = &Session{
				ID:          sessionID,
				OldID:       spawningSess.ID,
				Agent:       event.Agent,
				Host:        host,
				NativeID:    event.NativeID,
				Managed:     true,
				PID:         spawningSess.PID,
				EngineType:  spawningSess.EngineType,
				TmuxName:    tmuxName,
				Cwd:         spawningSess.Cwd,
				ProjectKey:  spawningSess.ProjectKey,
				NodePath:    spawningSess.NodePath,
				CustomTitle: spawningSess.CustomTitle,
				StartedAt:   spawningSess.StartedAt,
				LastEventAt: spawningSess.LastEventAt,
				AccountID:   spawningSess.AccountID,
			}
			if spawningSess.CustomTitle != "" {
				sess.Name = spawningSess.CustomTitle
			} else if spawningSess.Name != "" && !isRawSessionName(spawningSess.Name) {
				sess.Name = spawningSess.Name
			}
		}
	}

	if sess == nil {
		if activeManaged, err := s.findActiveManagedSessionInCwd(event.Agent, host, event.Cwd); err == nil && activeManaged != nil && activeManaged.NativeID != event.NativeID && s.isSameSupervisor(activeManaged, event) {
			// An active managed tmux session rotated its internal conversation ID (e.g. via /clear or /reset)
			reConv := regexp.MustCompile(`\s*\(Conv\s*(\d+)\)$`)
			var newTurnName string
			baseTitle := activeManaged.CustomTitle
			if baseTitle == "" {
				baseTitle = activeManaged.Name
			}
			if baseTitle == "" || isRawSessionName(baseTitle) {
				baseTitle = activeManaged.Agent
			}

			if m := reConv.FindStringSubmatch(baseTitle); len(m) == 2 {
				num, _ := strconv.Atoi(m[1])
				cleanBase := strings.TrimSpace(reConv.ReplaceAllString(baseTitle, ""))
				newTurnName = fmt.Sprintf("%s (Conv %d)", cleanBase, num+1)
			} else {
				activeManaged.Name = fmt.Sprintf("%s (Conv 1)", baseTitle)
				newTurnName = fmt.Sprintf("%s (Conv 2)", baseTitle)
			}

			// Archive previous conversation turn
			activeManaged.Managed = false
			activeManaged.State = StateEnded
			activeManaged.Activity = "Cleared (context reset)"
			activeManaged.LastEventAt = time.Now()
			_ = s.db.SaveSession(activeManaged)
			s.broadcast(activeManaged)

			// Adopt the live tmux supervisor for the new conversation turn
			sess = &Session{
				ID:         sessionID,
				Agent:      event.Agent,
				Host:       host,
				NativeID:   event.NativeID,
				Managed:    true,
				TmuxName:   activeManaged.TmuxName,
				NodePath:   activeManaged.NodePath,
				ProjectKey: activeManaged.ProjectKey,
				Name:       newTurnName,
				StartedAt:  time.Now(),
				AccountID:  activeManaged.AccountID,
			}
		} else {
			accountID := ""
			if accountParam != "" {
				accountID = fmt.Sprintf("%s:%s", event.Agent, accountParam)
			}
			sess = &Session{
				ID:        sessionID,
				Agent:     event.Agent,
				Host:      host,
				NativeID:  event.NativeID,
				StartedAt: time.Now(),
				AccountID: accountID,
			}
		}
	}

	if sess.AccountID == "" && accountParam != "" {
		sess.AccountID = fmt.Sprintf("%s:%s", event.Agent, accountParam)
	}

	return sess, true
}

func (s *Server) updateHookSessionMetadataAndTitle(sess *Session, event *Event) {
	if event.Cwd != "" {
		if sess.Cwd == "" || !sameOrRelatedDir(sess.Cwd, event.Cwd) || len(event.Cwd) < len(sess.Cwd) {
			sess.Cwd = event.Cwd
		}
	}
	if sess.NodePath == "" && sess.Cwd != "" {
		sess.NodePath = s.resolveSessionNodePath(sess.Cwd)
	}
	if len(event.Roots) > 0 {
		sess.Roots = event.Roots
	}
	// Respect Custom Title priority (never overwrite user-given custom title)
	if sess.CustomTitle != "" {
		sess.Name = sess.CustomTitle
	} else if event.Name != "" && !isRawSessionName(event.Name) {
		// Only adopt event.Name if session doesn't already have an established non-raw name,
		// or if current name is raw/empty
		if sess.Name == "" || isRawSessionName(sess.Name) {
			sess.Name = event.Name
		}
	} else if isRawSessionName(sess.Name) {
		// Attempt title resolution from disk
		if sess.Agent == "antigravity" {
			if title := ReadAntigravitySessionTitle(sess.Cwd, sess.NativeID); title != "" && !isRawSessionName(title) {
				sess.Name = title
			}
		} else if sess.Agent == "claude-code" {
			if meta := ReadClaudeSessionMeta(sess.Cwd, sess.NativeID); meta != nil && meta.Title != "" {
				sess.Name = meta.Title
				if meta.CustomTitle != "" {
					sess.CustomTitle = meta.CustomTitle
				}
				if meta.AITitle != "" {
					sess.AITitle = meta.AITitle
				}
			}
		}
	} else if sess.Agent == "antigravity" && sess.CustomTitle == "" {
		if anno := ReadAntigravityAnnotationTitle(sess.NativeID); anno != "" && anno != sess.Name {
			sess.Name = anno
		}
	}
}

func (s *Server) applyHookStateTransition(sess *Session, event *Event, sessionID string, isPureHeadless, headlessRunning bool) {
	evtLower := strings.ToLower(event.EventName)

	if !strings.EqualFold(event.EventName, "SubagentStop") && (strings.EqualFold(event.EventName, "stop") || strings.EqualFold(event.EventName, "sessionend")) {
		s.clearInMemorySubagents(sessionID, event.NativeID)
		if !isPureHeadless {
			if diskSubs, err := ExtractSubagents(event.Agent, event.NativeID, sess.Cwd); err == nil {
				for _, sub := range diskSubs {
					if sub.State == "running" && time.Since(sub.StartedAt) < 2*time.Minute {
						event.State = StateWorking
						event.Activity = "Subagent running: " + sub.Name
						break
					}
				}
			}
		}
	}

	if isPureHeadless {
		if !headlessRunning && (event.State == StateWorking || event.State == StateEnded) {
			event.State = StateIdle
			event.Activity = ""
		} else if headlessRunning && (event.State == StateIdle || event.State == StateEnded) {
			event.State = StateWorking
			if sess.Activity != "" {
				event.Activity = sess.Activity
			} else if event.Activity == "" {
				event.Activity = "Thinking..."
			}
		}
	}

	prevState := sess.State
	// Guard against out-of-order PostToolUse overwriting a session that just transitioned to StateIdle via Stop/SessionEnd
	if prevState == StateIdle && !sess.LastStateChangeAt.IsZero() && time.Since(sess.LastStateChangeAt) < 2*time.Second &&
		(evtLower == "posttooluse" || evtLower == "post_tool_use") {
		event.State = StateIdle
		event.Blocked = nil
		if sess.Activity != "" {
			event.Activity = sess.Activity
		}
	}
	// Guard against out-of-order PreToolUse (for non-question tools) overwriting a StateBlocked permission prompt just set by PermissionRequest
	if prevState == StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == BlockPermission &&
		!sess.LastStateChangeAt.IsZero() && time.Since(sess.LastStateChangeAt) < 2*time.Second &&
		(evtLower == "pretooluse" || evtLower == "pre_tool_use") && event.State == StateWorking {
		event.State = StateBlocked
		event.Blocked = sess.Blocked
		if sess.Activity != "" {
			event.Activity = sess.Activity
		}
	}

	isStateChange := sess.State != event.State
	if isStateChange {
		if event.State != StateWorking {
			sess.IsUnread = true
			sess.LastStateChangeAt = time.Now()
		} else {
			sess.IsUnread = false
		}
	}
	sess.State = event.State
	if event.State != StateBlocked {
		sess.Blocked = nil
	} else {
		sess.Blocked = event.Blocked
	}
	sess.Activity = event.Activity

	// Only update LastEventAt on initial session creation or genuine user interaction:
	// - Brand new session initialization (sess.LastEventAt is zero)
	// - User prompt submission ("userpromptsubmit", "usersubmit", "user_prompt")
	// Do NOT update LastEventAt on passive process boot / reconnect, agent tool execution, subagent events, or state transitions.
	shouldUpdateLastEvent := false

	if sess.LastEventAt.IsZero() {
		shouldUpdateLastEvent = true
	} else if evtLower == "userpromptsubmit" || evtLower == "usersubmit" || evtLower == "user_prompt" {
		shouldUpdateLastEvent = true
	}

	if shouldUpdateLastEvent {
		sess.LastEventAt = event.LastEventAt
		sess.IsDone = false
	}

	// Resolve project key if not populated or if Cwd changed
	if sess.ProjectKey == "" || (event.Cwd != "" && event.Cwd != sess.Cwd) {
		sess.ProjectKey = GetProjectKey(sess.Cwd)
	}
}

func (s *Server) trackHookSubagentEvent(sessionID string, event *Event) {
	evtNameLower := strings.ToLower(event.EventName)
	isSubagentTool := event.ToolName == "Agent" || event.ToolName == "Task" || event.ToolName == "invoke_subagent" || strings.EqualFold(event.ToolName, "subagent")
	if strings.EqualFold(event.EventName, "SubagentStart") {
		subName := event.ToolName
		if subName == "" {
			subName = "Subagent"
		}
		s.addSubagent(sessionID, event.NativeID, &SubagentInfo{
			ID:        generateUUID(),
			Name:      subName,
			Role:      subName,
			State:     "running",
			StartedAt: time.Now(),
		})
	} else if strings.EqualFold(event.EventName, "SubagentStop") {
		s.markSessionSubagentLifecycleActive(sessionID, event.NativeID)
		s.removeSubagent(sessionID, event.NativeID, event.ToolName)
	} else if (evtNameLower == "posttooluse" || evtNameLower == "post_tool_use") && isSubagentTool {
		targetSub := event.ToolName
		if event.ToolInput != nil {
			if m, ok := event.ToolInput.(map[string]interface{}); ok {
				if st, ok := m["subagent_type"].(string); ok && st != "" {
					targetSub = st
				} else if d, ok := m["description"].(string); ok && d != "" {
					targetSub = d
				}
			}
		}
		s.removeSubagent(sessionID, event.NativeID, targetSub)
	} else if evtNameLower == "userpromptsubmit" || evtNameLower == "sessionend" || evtNameLower == "stop" {
		s.clearInMemorySubagents(sessionID, event.NativeID)
	} else if (evtNameLower == "pretooluse" || evtNameLower == "pre_tool_use") && isSubagentTool {
		var prompt, desc, subType string
		if event.ToolInput != nil {
			if m, ok := event.ToolInput.(map[string]interface{}); ok {
				if pr, ok := m["prompt"].(string); ok {
					prompt = pr
				}
				if d, ok := m["description"].(string); ok {
					desc = d
				}
				if st, ok := m["subagent_type"].(string); ok {
					subType = st
				}
			}
		}
		name := subType
		if name == "" {
			name = desc
		}
		if name == "" {
			name = event.ToolName
		}
		s.addSubagent(sessionID, event.NativeID, &SubagentInfo{
			ID:        generateUUID(),
			Name:      name,
			Role:      subType,
			Prompt:    prompt,
			State:     "running",
			StartedAt: time.Now(),
			ToolName:  event.ToolName,
			Synthetic: true,
		})
	}
}

func (s *Server) findSpawningSession(agent, cwd string) (*Session, error) {
	sessions, err := s.db.ListSessions()
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	adapter := s.agentAdapter(agent)
	// 1. Exact CWD + StateUnknown match
	for _, sess := range sessions {
		if sess.EngineType == EngineHeadless && sess.TmuxName == "" {
			continue
		}
		if sess.Agent == agent && sess.Cwd == cwd && sess.Managed && sess.State == StateUnknown && sess.OldID == "" {
			if adapter.HasConversationArtifacts(home, sess.NativeID) {
				continue
			}
			return sess, nil
		}
	}
	// 2. Subdirectory / parent CWD (within same project/worktree root) + StateUnknown match
	for _, sess := range sessions {
		if sess.EngineType == EngineHeadless && sess.TmuxName == "" {
			continue
		}
		if sess.Agent == agent && sameOrRelatedDir(sess.Cwd, cwd) && sess.Managed && sess.State == StateUnknown && sess.OldID == "" {
			if adapter.HasConversationArtifacts(home, sess.NativeID) {
				continue
			}
			return sess, nil
		}
	}
	// 3. For Antigravity: match a managed placeholder session whose State was updated by scanObservedSessions
	// before the user submitted their first prompt (still holds synthetic tempUUID with no brain artifacts).
	if agent == "antigravity" {
		var best *Session
		for _, sess := range sessions {
			if sess.Agent != "antigravity" || !sess.Managed || sess.State == StateEnded || sess.OldID != "" {
				continue
			}
			if !sameOrRelatedDir(sess.Cwd, cwd) {
				continue
			}
			expectedTmux := fmt.Sprintf("ackbar-antigravity-%s", sess.NativeID)
			if sess.TmuxName != expectedTmux {
				continue
			}
			if sess.FirstPrompt != "" || (sess.CustomTitle == "" && !isRawSessionName(sess.Name)) {
				continue
			}
			if adapter.HasConversationArtifacts(home, sess.NativeID) {
				continue
			}
			if best == nil || sess.StartedAt.After(best.StartedAt) {
				best = sess
			}
		}
		if best != nil {
			return best, nil
		}
	}
	return nil, nil
}

func (s *Server) findActiveManagedSessionInCwd(agent, host, cwd string) (*Session, error) {
	if cwd == "" {
		return nil, nil
	}
	sessions, err := s.db.ListSessions()
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if sess.Agent == agent && sess.Host == host && sess.Cwd == cwd && sess.Managed && sess.State != StateEnded && sess.TmuxName != "" {
			return sess, nil
		}
	}
	return nil, nil
}

func (s *Server) isSameSupervisor(activeManaged *Session, event *Event) bool {
	if activeManaged == nil || event == nil {
		return false
	}
	if host := activeManaged.Host; host == "" || host == "local" || host == s.HostName() {
		home, _ := os.UserHomeDir()
		adapter := s.agentAdapter(event.Agent)
		eventPID, eventTmux := adapter.ResolveSessionOwner(home, event.NativeID)
		if eventTmux != "" && activeManaged.TmuxName != "" {
			return eventTmux == activeManaged.TmuxName
		}
		if eventPID > 0 && activeManaged.PID > 0 {
			if eventPID == activeManaged.PID {
				return true
			}
			if isProcessAlive(activeManaged.PID) {
				return false
			}
		}

		// If activeManaged has a live process that is running, do not adopt unless proven to be the same
		if activeManaged.PID > 0 && isProcessAlive(activeManaged.PID) {
			return false
		}

		// If activeManaged has an active tmux session, check if the pane process is still running
		if activeManaged.TmuxName != "" && tmux.HasSession(context.Background(), activeManaged.TmuxName) {
			if panePID, err := tmux.GetPID(context.Background(), activeManaged.TmuxName); err == nil && panePID > 0 {
				if isProcessAlive(panePID) {
					if eventPID > 0 && eventPID != panePID {
						return false
					}
				}
			}
		}
	}
	return true
}
