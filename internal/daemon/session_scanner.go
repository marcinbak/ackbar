package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ackbar/internal/tmux"
)

// ScanState holds the in-memory session lookup indices during a scanObservedSessions pass.
type ScanState struct {
	hostName         string
	existingSessions []*Session
	knownIDs         map[string]*Session
	knownByPID       map[int]*Session
	knownByNativeID  map[string]*Session
	knownByTmux      map[string]*Session
}

type scanState = ScanState

func (state *ScanState) cleanupGhostProc(s *Server, excludeSessID string, pids ...int) {
	for _, p := range pids {
		if p <= 0 {
			continue
		}
		ghostID := fmt.Sprintf("%s:observed:proc-%d", state.hostName, p)
		if ghostID == excludeSessID {
			continue
		}
		if ghost, ok := state.knownIDs[ghostID]; ok {
			_ = s.db.DeleteSession(ghostID)
			ghost.Deleted = true
			ghost.Activity = "Deleted"
			s.broadcast(ghost)
			delete(state.knownIDs, ghostID)
		} else {
			_ = s.db.DeleteSession(ghostID)
		}
	}
}

func (s *Server) verifyHeadlessLiveness(sessions []*Session) {
	for _, sess := range sessions {
		if sess == nil || sess.State == StateEnded {
			continue
		}
		if !s.isLocalHost(sess.Host) {
			continue
		}
		if sess.EngineType == EngineHeadless && sess.TmuxName == "" {
			isRunning := s.headless != nil && s.headless.IsRunning(sess.ID)
			if isRunning {
				if sess.State != StateWorking {
					sess.State = StateWorking
					if sess.Activity == "" {
						sess.Activity = "Thinking..."
					}
					_ = s.db.SaveSession(sess)
					s.broadcast(sess)
				}
			} else if sess.State == StateWorking {
				sess.State = StateIdle
				sess.Activity = ""
				sess.PID = 0
				sess.Blocked = nil
				sess.RunningSubagents = 0
				_ = s.db.SaveSession(sess)
				s.broadcast(sess)
			}
		}
	}
}

func (s *Server) triggerAsyncLiveness() {
	if s.db == nil {
		return
	}
	lastUnix := s.lastLivenessAt.Load()
	if lastUnix > 0 && time.Since(time.Unix(0, lastUnix)) < 2*time.Second {
		return
	}
	if !s.livenessRunning.CompareAndSwap(false, true) {
		return
	}
	s.lastLivenessAt.Store(time.Now().UnixNano())
	go func() {
		defer s.livenessRunning.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		active, err := s.db.ListActiveSessions()
		if err != nil || len(active) == 0 {
			return
		}
		s.verifySessionLiveness(ctx, active)
	}()
}

func (s *Server) verifySessionLiveness(ctx context.Context, sessions []*Session) {
	s.verifyHeadlessLiveness(sessions)

	// Query all active local tmux sessions once to avoid O(N) subprocess executions
	activeTmux, _ := tmux.ListSessions(ctx)

	for _, sess := range sessions {
		if sess == nil || sess.State == StateEnded {
			continue
		}

		if !s.isLocalHost(sess.Host) {
			continue
		}

		if sess.EngineType == EngineHeadless && sess.TmuxName == "" {
			continue
		}

		if p, ok := s.providers[sess.Agent]; ok {
			if inspector, ok := p.(StatusInspector); ok {
				prevState := sess.State
				if inspector.InspectStatus(ctx, sess) {
					_ = s.db.SaveSession(sess)
					s.maybeNotifySessionBlocked(prevState, sess)
					s.broadcast(sess)
				}
			}
		}

		if sess.State == StateEnded {
			continue
		}

		// Grace period for active sessions that sent hook events recently
		if time.Since(sess.LastEventAt) < 15*time.Minute {
			continue
		}

		alive := true
		if sess.Managed && sess.TmuxName != "" {
			if sess.Host == "local" || sess.Host == "" || sess.Host == s.HostName() {
				alive = activeTmux[sess.TmuxName]
				if alive && sess.PID > 0 && !isProcessAlive(sess.PID) {
					if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs != nil || len(strings.TrimSpace(string(outPs))) == 0 {
						alive = false
					}
				}
			} else {
				alive = tmux.HasSession(ctx, sess.TmuxName)
			}
		} else if !sess.Managed && sess.PID > 0 {
			proc, err := os.FindProcess(sess.PID)
			if err != nil {
				alive = false
			} else {
				err = proc.Signal(syscall.Signal(0))
				if err != nil {
					alive = false
				}
			}
		}

		if !alive {
			sess.State = StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			_ = s.db.SaveSession(sess)
			s.SyncSessionTaskWorker(sess)
			s.broadcast(sess)
		}
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// isIgnoredAgentCommand returns true if the command line represents a utility subcommand,
// non-interactive run, or help/version invocation that should not be tracked as an agentic session.
func isIgnoredAgentCommand(agent string, fullCmd string) bool {
	return getDefaultAgentAdapter(agent).IsIgnoredCommand(fullCmd)
}

func (s *Server) scanObservedSessions(ctx context.Context) {
	hostName := s.HostName()

	existingSessions, err := s.db.ListSessions()
	if err != nil {
		return
	}

	state := &ScanState{
		hostName:         hostName,
		existingSessions: existingSessions,
		knownIDs:         make(map[string]*Session, len(existingSessions)),
		knownByPID:       make(map[int]*Session, len(existingSessions)),
		knownByNativeID:  make(map[string]*Session, len(existingSessions)),
		knownByTmux:      make(map[string]*Session, len(existingSessions)),
	}

	for _, sess := range existingSessions {
		if s.isForeignSession(sess.Host) {
			continue
		}
		state.knownIDs[sess.ID] = sess
		if sess.PID > 0 {
			state.knownByPID[sess.PID] = sess
		}
		if sess.NativeID != "" {
			state.knownByNativeID[sess.NativeID] = sess
		}
		if sess.TmuxName != "" {
			state.knownByTmux[sess.TmuxName] = sess
		}
	}

	s.refreshExistingSessions(ctx, state)
	s.scanTmuxPanes(ctx, state)
	s.scanDiskSessions(ctx, state)
}

// refreshExistingSessions refreshes names/titles/metadata and prunes dead proc-<pid> sessions.
func (s *Server) refreshExistingSessions(ctx context.Context, state *ScanState) {
	for _, sess := range state.existingSessions {
		if s.isForeignSession(sess.Host) {
			continue
		}
		if strings.HasPrefix(sess.NativeID, "proc-") {
			pidStr := strings.TrimPrefix(sess.NativeID, "proc-")
			if pidVal, pErr := strconv.Atoi(pidStr); pErr == nil && pidVal > 0 {
				if !isProcessAlive(pidVal) {
					_ = s.db.DeleteSession(sess.ID)
					_ = s.db.DeleteSession(sess.NativeID)
					delete(state.knownIDs, sess.ID)
					delete(state.knownByPID, pidVal)
					delete(state.knownByNativeID, sess.NativeID)
					sess.Deleted = true
					sess.State = StateEnded
					sess.Activity = "Deleted"
					s.broadcast(sess)
					continue
				}
			}
		}

		// IMMUTABILITY GUARD: Ended sessions with established names are static and do not need disk rescans
		if sess.State == StateEnded && sess.Name != "" && !isRawSessionName(sess.Name) {
			if sess.NodePath == "" && sess.Cwd != "" && !sess.IsSupervisor {
				if np := s.resolveSessionNodePath(sess.Cwd); np != "" {
					sess.NodePath = np
					_ = s.db.SaveSession(sess)
					s.broadcast(sess)
				}
			}
			continue
		}

		prevState := sess.State
		changed := false
		if p, ok := s.providers[sess.Agent]; ok && sess.NativeID != "" {
			if meta := p.ReadSessionMetadata(sess.Cwd, sess.NativeID); meta != nil {
				if sess.CustomTitle != "" {
					if sess.Name != sess.CustomTitle {
						sess.Name = sess.CustomTitle
						changed = true
					}
				} else if meta.Title != "" && meta.Title != sess.Name && !strings.HasPrefix(meta.Title, "<") && !isRawSessionName(meta.Title) {
					sess.Name = meta.Title
					changed = true
				}
				if meta.Entrypoint != "" && meta.Entrypoint != sess.Entrypoint {
					sess.Entrypoint = meta.Entrypoint
					changed = true
				}
				if meta.Kind != "" && meta.Kind != sess.Kind {
					sess.Kind = meta.Kind
					changed = true
				}
				if meta.Version != "" && meta.Version != sess.Version {
					sess.Version = meta.Version
					changed = true
				}
				if meta.ContextPct > 0 && meta.ContextPct != sess.ContextPct {
					sess.ContextPct = meta.ContextPct
					changed = true
				}
				if !meta.LastUserMessageAt.IsZero() && meta.LastUserMessageAt.After(sess.LastEventAt) {
					sess.LastEventAt = meta.LastUserMessageAt
					changed = true
				}
				if !meta.LastMessageAt.IsZero() {
					if sess.LastEventAt.IsZero() {
						if !meta.LastUserMessageAt.IsZero() {
							sess.LastEventAt = meta.LastUserMessageAt
						} else {
							sess.LastEventAt = meta.LastMessageAt
						}
						changed = true
					} else if (sess.State == StateIdle || sess.State == StateEnded) && sess.Blocked == nil && sess.LastEventAt.After(meta.LastMessageAt) {
						sess.LastEventAt = meta.LastMessageAt
						changed = true
					}
				}
			}
			if sess.EngineType == EngineHeadless && sess.TmuxName == "" {
				isRunning := s.headless != nil && s.headless.IsRunning(sess.ID)
				if !isRunning && sess.State == StateWorking {
					sess.State = StateIdle
					sess.Activity = ""
					sess.PID = 0
					sess.Blocked = nil
					sess.RunningSubagents = 0
					changed = true
				} else if isRunning && sess.State != StateWorking {
					sess.State = StateWorking
					changed = true
				}
			} else if inspector, ok := p.(StatusInspector); ok {
				if inspector.InspectStatus(ctx, sess) {
					changed = true
				}
			}
		}
		if sess.NodePath == "" && sess.Cwd != "" && !sess.IsSupervisor {
			if np := s.resolveSessionNodePath(sess.Cwd); np != "" {
				sess.NodePath = np
				changed = true
			}
		}
		if changed {
			_ = s.db.SaveSession(sess)
			s.maybeNotifySessionBlocked(prevState, sess)
			s.broadcast(sess)
		}
	}
}

// scanTmuxPanes scans local tmux panes for running agent CLIs and reconciles managed sessions.
func (s *Server) scanTmuxPanes(ctx context.Context, state *ScanState) {
	hostName := state.hostName
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_pid}\t#{pane_current_path}\t#{session_name}\t#{pane_current_command}").Output()
	if err != nil {
		return
	}

	type procEntry struct {
		pid int
		cmd string
	}
	childrenByPPID := make(map[int][]procEntry)
	if psOut, psErr := exec.CommandContext(ctx, "ps", "-eo", "ppid,pid,command").Output(); psErr == nil {
		for _, pline := range strings.Split(string(psOut), "\n") {
			pline = strings.TrimSpace(pline)
			if pline == "" {
				continue
			}
			fields := strings.Fields(pline)
			if len(fields) >= 3 {
				ppidVal, err1 := strconv.Atoi(fields[0])
				cPid, err2 := strconv.Atoi(fields[1])
				if err1 == nil && err2 == nil && ppidVal > 0 && cPid > 0 {
					cCmd := strings.ToLower(strings.Join(fields[2:], " "))
					childrenByPPID[ppidVal] = append(childrenByPPID[ppidVal], procEntry{pid: cPid, cmd: cCmd})
				}
			}
		}
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		pidStr, cwd, tmuxName, cmdName := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]), strings.TrimSpace(parts[3])
		cwd = strings.TrimSuffix(cwd, " (deleted)")
		cmdLower := strings.ToLower(cmdName)

		if tmuxName == "(deleted)" || tmuxName == "" || strings.HasPrefix(tmuxName, "ackbar-mock-agent-") {
			continue
		}

		agent := ""
		actualPID := 0
		var panePID int
		fmt.Sscanf(pidStr, "%d", &panePID)
		if panePID > 0 {
			actualPID = panePID
		}

		for _, p := range s.providers {
			prefix := fmt.Sprintf("ackbar-%s-", p.Agent())
			if strings.HasPrefix(tmuxName, prefix) {
				agent = p.Agent()
				break
			}
		}

		if panePID > 0 {
			for _, child := range childrenByPPID[panePID] {
				matchedAgent := ""
				for _, p := range s.providers {
					for _, pName := range p.ProcessNames() {
						if strings.Contains(child.cmd, pName) {
							matchedAgent = p.Agent()
							actualPID = child.pid
							break
						}
					}
					if matchedAgent != "" {
						agent = matchedAgent
						break
					}
				}
				if matchedAgent != "" {
					break
				}
			}
		}

		if agent == "" {
			for _, p := range s.providers {
				for _, pName := range p.ProcessNames() {
					if strings.Contains(cmdLower, pName) {
						agent = p.Agent()
						break
					}
				}
				if agent != "" {
					break
				}
			}
		}

		if agent == "" || actualPID <= 0 || cwd == "" {
			continue
		}

		pid := actualPID
		adapter := s.agentAdapter(agent)

		// If no child process found and pane is just a dead shell, mark session as ended
		if actualPID == panePID && (cmdLower == "bash" || cmdLower == "zsh" || cmdLower == "sh" || cmdLower == "fish") {
			targetNativeID := extractNativeIDFromTmuxName(agent, tmuxName)
			var existing *Session
			if targetNativeID != "" && adapter.IsValidNativeID(targetNativeID) {
				if ex, ok := state.knownIDs[fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)]; ok {
					existing = ex
				} else if ex, ok := state.knownByNativeID[targetNativeID]; ok {
					existing = ex
				}
			}
			if existing == nil && tmuxName != "" {
				existing = state.knownByTmux[tmuxName]
			}
			if existing != nil && time.Since(existing.StartedAt) < 15*time.Second {
				continue
			}
			if existing != nil && existing.State != StateEnded {
				existing.State = StateEnded
				existing.Activity = "Session ended (process exited)"
				existing.PID = 0
				existing.Blocked = nil
				_ = s.db.SaveSession(existing)
				s.SyncSessionTaskWorker(existing)
				s.broadcast(existing)
			}
			continue
		}

		targetNativeID := extractNativeIDFromTmuxName(agent, tmuxName)
		home, _ := os.UserHomeDir()

		if agent == "claude-code" && (targetNativeID == "" || !IsUUID(targetNativeID)) {
			targetNativeID = ""
			if home != "" {
				sID, _ := findClaudeSessionForPID(home, pid)
				if sID != "" && IsUUID(sID) {
					targetNativeID = sID
				}
			}
		} else if agent == "antigravity" {
			sID := findAntigravitySessionForPID(ctx, pid)
			if sID != "" && IsUUID(sID) && sID != targetNativeID && (home == "" || !adapter.IsSubagentSession(home, sID, nil)) {
				tmuxName, targetNativeID = s.reconcileAntigravityTmuxSession(ctx, state, agent, tmuxName, targetNativeID, sID, pid)
			}
			if targetNativeID != "" && home != "" && adapter.IsSubagentSession(home, targetNativeID, nil) {
				continue
			}
		}

		if targetNativeID != "" && adapter.IsValidNativeID(targetNativeID) {
			if s.db.IsSessionDeleted(targetNativeID) || s.db.IsSessionDeleted(fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)) {
				_ = tmux.Kill(ctx, tmuxName)
				if pid > 0 {
					_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
				}
				continue
			}
		}

		var existing *Session
		if targetNativeID != "" && adapter.IsValidNativeID(targetNativeID) {
			if ex, ok := state.knownIDs[fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)]; ok {
				existing = ex
			} else if ex, ok := state.knownByNativeID[targetNativeID]; ok {
				existing = ex
			}
		}
		if existing == nil && tmuxName != "" {
			existing = state.knownByTmux[tmuxName]
		}

		if existing != nil {
			s.adoptExistingTmuxSession(ctx, state, existing, agent, tmuxName, cwd, pid, panePID)
		}
	}
}

func extractNativeIDFromTmuxName(agent, tmuxName string) string {
	if strings.HasPrefix(tmuxName, fmt.Sprintf("ackbar-%s-", agent)) {
		return strings.TrimPrefix(tmuxName, fmt.Sprintf("ackbar-%s-", agent))
	}
	if strings.HasPrefix(tmuxName, "ackbar-") {
		parts := strings.Split(tmuxName, "-")
		if len(parts) >= 3 {
			return parts[len(parts)-1]
		}
	}
	return ""
}

func (s *Server) reconcileAntigravityTmuxSession(ctx context.Context, state *ScanState, agent, tmuxName, targetNativeID, sID string, pid int) (string, string) {
	hostName := state.hostName
	oldSessID := fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)
	realSessID := fmt.Sprintf("%s:%s:%s", agent, hostName, sID)

	newTmuxName := fmt.Sprintf("ackbar-%s-%s", agent, sID)
	if strings.HasPrefix(tmuxName, "ackbar-") && tmuxName != newTmuxName {
		if rErr := tmux.Rename(ctx, tmuxName, newTmuxName); rErr == nil {
			delete(state.knownByTmux, tmuxName)
			tmuxName = newTmuxName
		} else {
			log.Printf("[daemon] failed to rename tmux session %s to %s: %v", tmuxName, newTmuxName, rErr)
		}
	}

	var existingOld *Session
	if ex, ok := state.knownIDs[oldSessID]; ok {
		existingOld = ex
	} else if ex, ok := state.knownByNativeID[targetNativeID]; ok {
		existingOld = ex
	}

	if existingOld != nil {
		_ = s.db.DeleteSession(existingOld.ID)
		delete(state.knownIDs, existingOld.ID)
		delete(state.knownByNativeID, existingOld.NativeID)

		var targetSess *Session
		if existingReal, ok := state.knownIDs[realSessID]; ok {
			if existingOld.Cwd != "" && (existingReal.Cwd == "" || (sameOrRelatedDir(existingOld.Cwd, existingReal.Cwd) && len(existingOld.Cwd) < len(existingReal.Cwd))) {
				existingReal.Cwd = existingOld.Cwd
				existingReal.ProjectKey = GetProjectKey(existingOld.Cwd)
			}
			if existingOld.NodePath != "" && (existingReal.NodePath == "" || existingReal.Cwd == existingOld.Cwd) {
				existingReal.NodePath = existingOld.NodePath
			}
			if existingReal.CustomTitle == "" && existingOld.CustomTitle != "" {
				existingReal.CustomTitle = existingOld.CustomTitle
				existingReal.Name = existingOld.Name
			}
			if existingOld.IsSupervisor {
				existingReal.IsSupervisor = true
			}
			if existingReal.CurrentTaskID == "" && existingOld.CurrentTaskID != "" {
				existingReal.CurrentTaskID = existingOld.CurrentTaskID
			}
			if existingReal.CustomTitle == "" {
				if title := ReadAntigravitySessionTitle(existingReal.Cwd, sID); title != "" && !isRawSessionName(title) {
					existingReal.Name = title
				}
			}
			existingReal.Managed = true
			existingReal.PID = pid
			existingReal.OldID = oldSessID
			if tmuxName != "" {
				existingReal.TmuxName = tmuxName
			}
			targetSess = existingReal
		} else {
			existingOld.ID = realSessID
			existingOld.NativeID = sID
			existingOld.OldID = oldSessID
			if tmuxName != "" {
				existingOld.TmuxName = tmuxName
			}
			if existingOld.CustomTitle == "" {
				if title := ReadAntigravitySessionTitle(existingOld.Cwd, sID); title != "" && !isRawSessionName(title) {
					existingOld.Name = title
				}
			}
			state.knownIDs[realSessID] = existingOld
			state.knownByNativeID[sID] = existingOld
			targetSess = existingOld
		}
		if targetSess.TmuxName != "" {
			state.knownByTmux[targetSess.TmuxName] = targetSess
		}
		_ = s.db.SaveSession(targetSess)
		s.broadcast(targetSess)
	} else if existingReal, ok := state.knownIDs[realSessID]; ok {
		if tmuxName != "" && existingReal.TmuxName != tmuxName {
			existingReal.TmuxName = tmuxName
			if targetNativeID != "" {
				existingReal.OldID = oldSessID
			}
			state.knownByTmux[tmuxName] = existingReal
			_ = s.db.SaveSession(existingReal)
			s.broadcast(existingReal)
		}
	}
	return tmuxName, sID
}

func (s *Server) adoptExistingTmuxSession(ctx context.Context, state *ScanState, existing *Session, agent, tmuxName, cwd string, pid, panePID int) {
	if existing == nil || existing.IsDone {
		return
	}
	existing.Managed = true
	existing.PID = pid
	if tmuxName != "" && tmuxName != "(deleted)" {
		existing.TmuxName = tmuxName
		state.knownByTmux[tmuxName] = existing
	}
	if existing.Cwd == "" {
		if existing.NodePath != "" {
			if node, err := s.db.GetNode(existing.NodePath); err == nil && node != nil && node.ProjectDir != "" {
				existing.Cwd = node.ProjectDir
			}
		}
		if existing.Cwd == "" && cwd != "" {
			existing.Cwd = cwd
		}
	}
	if existing.NodePath == "" && existing.Cwd != "" && !existing.IsSupervisor {
		existing.NodePath = s.resolveSessionNodePath(existing.Cwd)
	}
	if p, ok := s.providers[agent]; ok {
		if inspector, ok := p.(StatusInspector); ok {
			inspector.InspectStatus(ctx, existing)
		}
	}
	if existing.State == StateEnded || existing.State == StateUnknown {
		existing.State = StateIdle
		existing.Activity = "Awaiting user prompt"
	}
	if agent == "antigravity" && existing.CustomTitle == "" {
		if isRawSessionName(existing.Name) {
			if title := ReadAntigravitySessionTitle(existing.Cwd, existing.NativeID); title != "" && !isRawSessionName(title) {
				existing.Name = title
			}
		} else if anno := ReadAntigravityAnnotationTitle(existing.NativeID); anno != "" && anno != existing.Name {
			existing.Name = anno
		}
	}
	_ = s.db.SaveSession(existing)
	state.knownIDs[existing.ID] = existing
	state.knownByPID[pid] = existing
	if panePID > 0 {
		state.knownByPID[panePID] = existing
	}
	s.broadcast(existing)

	state.cleanupGhostProc(s, "", pid, panePID)

	// Clean up any stale duplicate sessions in DB/memory sharing this TmuxName or PID
	for kID, kSess := range state.knownIDs {
		if kSess != nil && kID != existing.ID && s.isLocalHost(kSess.Host) {
			if (tmuxName != "" && kSess.TmuxName == tmuxName) || (pid > 0 && kSess.PID == pid) {
				_ = s.db.DeleteSession(kID)
				kSess.Deleted = true
				kSess.Activity = "Deleted"
				s.broadcast(kSess)
				delete(state.knownIDs, kID)
				if kSess.NativeID != "" && kSess.NativeID != existing.NativeID {
					delete(state.knownByNativeID, kSess.NativeID)
				}
			}
		}
	}
}

// scanDiskSessions delegates disk-backed session reconciliation and sanitation to each agent's DiskSessionDiscoverer.
func (s *Server) scanDiskSessions(ctx context.Context, state *ScanState) {
	home, _ := os.UserHomeDir()
	if home == "" {
		return
	}
	s.agentAdapter("claude-code").ScanDiskSessions(ctx, s, home, state)
	s.agentAdapter("antigravity").ScanDiskSessions(ctx, s, home, state)
}
