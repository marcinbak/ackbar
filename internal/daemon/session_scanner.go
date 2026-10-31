package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

func (s *Server) verifySessionLiveness(ctx context.Context, sessions []*Session) {
	// Query all active local tmux sessions once to avoid O(N) subprocess executions
	activeTmux, _ := tmux.ListSessions(ctx)

	for _, sess := range sessions {
		if sess.State == StateEnded {
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
			continue
		}

		if p, ok := s.providers[sess.Agent]; ok {
			if inspector, ok := p.(StatusInspector); ok {
				if inspector.InspectStatus(ctx, sess) {
					_ = s.db.SaveSession(sess)
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
	s.scanOSProcessTable(ctx, state)
	s.scanDiskSessions(ctx, state)
}

// refreshExistingSessions refreshes names/titles/metadata and prunes dead proc-<pid> sessions.
func (s *Server) refreshExistingSessions(ctx context.Context, state *ScanState) {
	for _, sess := range state.existingSessions {
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
			if sess.NodePath == "" && sess.Cwd != "" {
				if np := s.resolveSessionNodePath(sess.Cwd); np != "" {
					sess.NodePath = np
					_ = s.db.SaveSession(sess)
					s.broadcast(sess)
				}
			}
			continue
		}

		changed := false
		if p, ok := s.providers[sess.Agent]; ok && sess.NativeID != "" {
			if meta := p.ReadSessionMetadata(sess.Cwd, sess.NativeID); meta != nil {
				if meta.Title != "" && meta.Title != sess.Name && !strings.HasPrefix(meta.Title, "<") {
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
		if sess.NodePath == "" && sess.Cwd != "" {
			if np := s.resolveSessionNodePath(sess.Cwd); np != "" {
				sess.NodePath = np
				changed = true
			}
		}
		if changed {
			_ = s.db.SaveSession(sess)
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
			continue
		}

		nativeID := fmt.Sprintf("proc-%d", pid)
		sessID := fmt.Sprintf("%s:observed:%s", hostName, nativeID)
		if targetNativeID != "" && isUUID(targetNativeID) {
			nativeID = targetNativeID
			sessID = fmt.Sprintf("%s:%s:%s", agent, hostName, targetNativeID)
		}

		existingObs := state.knownIDs[sessID]
		if existingObs == nil {
			sessionName := ""
			lastTime := time.Now()
			if p, ok := s.providers[agent]; ok && targetNativeID != "" {
				sessionName = p.ResolveSessionTitle(cwd, targetNativeID)
				if meta := p.ReadSessionMetadata(cwd, targetNativeID); meta != nil && !meta.LastMessageAt.IsZero() {
					lastTime = meta.LastMessageAt
				}
			}
			if sessionName == "" {
				if targetNativeID != "" && len(targetNativeID) >= 8 {
					sessionName = fmt.Sprintf("%s (%s)", agent, targetNativeID[:8])
				} else {
					sessionName = fmt.Sprintf("%s (%s)", agent, nativeID)
				}
			}
			newSess := &Session{
				ID:          sessID,
				Name:        sessionName,
				Agent:       agent,
				Host:        hostName,
				NativeID:    nativeID,
				Cwd:         cwd,
				NodePath:    s.resolveSessionNodePath(cwd),
				ProjectKey:  GetProjectKey(cwd),
				State:       StateIdle,
				Managed:     true,
				TmuxName:    tmuxName,
				PID:         pid,
				Activity:    fmt.Sprintf("Observed running agent in tmux '%s' (PID %d)", tmuxName, pid),
				StartedAt:   lastTime,
				LastEventAt: lastTime,
			}
			if p, ok := s.providers[agent]; ok {
				if inspector, ok := p.(StatusInspector); ok {
					inspector.InspectStatus(ctx, newSess)
				}
			}
			_ = s.db.SaveSession(newSess)
			state.knownIDs[sessID] = newSess
			state.knownByPID[pid] = newSess
			if panePID > 0 {
				state.knownByPID[panePID] = newSess
			}
			s.broadcast(newSess)
			state.cleanupGhostProc(s, sessID, pid, panePID)
		} else {
			obsChanged := false
			if (existingObs.TmuxName != tmuxName || !existingObs.Managed) && tmuxName != "(deleted)" && tmuxName != "" {
				existingObs.TmuxName = tmuxName
				existingObs.Managed = true
				obsChanged = true
			}
			if existingObs.PID != pid {
				existingObs.PID = pid
				obsChanged = true
			}
			if existingObs.NodePath == "" && existingObs.Cwd != "" {
				if np := s.resolveSessionNodePath(existingObs.Cwd); np != "" {
					existingObs.NodePath = np
					obsChanged = true
				}
			}
			state.knownIDs[sessID] = existingObs
			state.knownByPID[pid] = existingObs
			if panePID > 0 {
				state.knownByPID[panePID] = existingObs
			}
			if obsChanged {
				_ = s.db.SaveSession(existingObs)
				s.broadcast(existingObs)
			}
			state.cleanupGhostProc(s, sessID, pid, panePID)
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
	if existing.NodePath == "" && existing.Cwd != "" {
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

// scanOSProcessTable scans the OS process table for running agent processes.
func (s *Server) scanOSProcessTable(ctx context.Context, state *ScanState) {
	hostName := state.hostName
	psOut, err := exec.CommandContext(ctx, "ps", "-eo", "pid,ppid,command").Output()
	if err != nil {
		return
	}

	lines := strings.Split(string(psOut), "\n")
	parentOf := make(map[int]int, len(lines))
	type procEntry struct {
		pid     int
		ppid    int
		fullCmd string
	}
	var procEntries []procEntry

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		pVal, pErr := strconv.Atoi(parts[0])
		ppVal, ppErr := strconv.Atoi(parts[1])
		if pErr != nil || ppErr != nil || pVal <= 0 {
			continue
		}
		parentOf[pVal] = ppVal
		procEntries = append(procEntries, procEntry{
			pid:     pVal,
			ppid:    ppVal,
			fullCmd: strings.Join(parts[2:], " "),
		})
	}

	isDescendantOfKnownSession := func(startPid int) bool {
		curr := startPid
		for depth := 0; depth < 35; depth++ {
			p, ok := parentOf[curr]
			if !ok || p <= 1 {
				break
			}
			if p == os.Getpid() {
				return true
			}
			if sess, ok := state.knownByPID[p]; ok && sess != nil {
				return true
			}
			curr = p
		}
		return false
	}

	home, _ := os.UserHomeDir()
	candidateAgents := []string{"claude-code", "antigravity", "codex", "grok", "opencode"}

	for _, pe := range procEntries {
		pid := pe.pid
		pidStr := strconv.Itoa(pid)
		fullCmd := pe.fullCmd
		cmdLower := strings.ToLower(fullCmd)

		agent := ""
		cmdFields := strings.Fields(fullCmd)
		if len(cmdFields) > 0 {
			binName := filepath.Base(cmdFields[0])
			binLower := strings.ToLower(binName)
			for _, ag := range candidateAgents {
				if s.agentAdapter(ag).MatchProcessCommand(binLower, cmdLower) {
					agent = ag
					break
				}
			}
		}

		if agent == "" || pidStr == "" || pid <= 0 || pid == os.Getpid() {
			continue
		}

		adapter := s.agentAdapter(agent)
		if adapter.IsIgnoredCommand(fullCmd) {
			continue
		}
		if isDescendantOfKnownSession(pid) {
			continue
		}
		if exSess, ok := state.knownByPID[pid]; ok && exSess.ID != fmt.Sprintf("%s:observed:proc-%d", hostName, pid) {
			continue
		}

		sID := adapter.ResolveSessionForPID(ctx, home, pid, cmdFields)
		if adapter.RequiresResolvedSessionID() && sID == "" {
			continue
		}

		hasExistingMatch := false
		if sID != "" {
			if sObj, ok := state.knownByNativeID[sID]; ok {
				if sObj.PID != pid {
					sObj.PID = pid
					_ = s.db.SaveSession(sObj)
					_ = s.db.DeleteSession(fmt.Sprintf("%s:observed:proc-%d", hostName, pid))
					state.knownByPID[pid] = sObj
				}
				hasExistingMatch = true
			}
		}
		if hasExistingMatch {
			continue
		}

		if sID != "" {
			if s.db.IsSessionDeleted(sID) || s.db.IsSessionDeleted(fmt.Sprintf("%s:%s:%s", agent, hostName, sID)) {
				continue
			}
		}

		nativeID := fmt.Sprintf("proc-%d", pid)
		sessID := fmt.Sprintf("%s:observed:%s", hostName, nativeID)
		if sID != "" && isUUID(sID) {
			nativeID = sID
			sessID = fmt.Sprintf("%s:%s:%s", agent, hostName, sID)
		}

		if state.knownIDs[sessID] != nil {
			continue
		}

		cwd := resolveProcessCwd(ctx, pid, pidStr)
		if cwd == "" {
			continue
		}

		title := fmt.Sprintf("%s (%s)", agent, nativeID)
		lastTime := time.Now()
		if agent == "claude-code" {
			if meta := ReadClaudeSessionMeta(cwd, nativeID); meta != nil && !meta.LastMessageAt.IsZero() {
				lastTime = meta.LastMessageAt
			}
		}
		newSess := &Session{
			ID:          sessID,
			Name:        title,
			Agent:       agent,
			Host:        hostName,
			NativeID:    nativeID,
			Cwd:         cwd,
			ProjectKey:  GetProjectKey(cwd),
			State:       StateIdle,
			Managed:     false,
			PID:         pid,
			Activity:    fmt.Sprintf("Observed running agent process (PID %d)", pid),
			StartedAt:   lastTime,
			LastEventAt: lastTime,
		}
		if p, ok := s.providers[agent]; ok {
			meta := p.ReadSessionMetadata(cwd, nativeID)
			if meta != nil {
				if meta.CustomTitle != "" {
					newSess.Name = meta.CustomTitle
				} else if meta.AITitle != "" {
					newSess.Name = meta.AITitle
				} else if meta.FirstPrompt != "" {
					newSess.Name = truncateTitle(meta.FirstPrompt)
				}
				newSess.Entrypoint = meta.Entrypoint
				newSess.Kind = meta.Kind
				newSess.Version = meta.Version
				newSess.CustomTitle = meta.CustomTitle
				newSess.AITitle = meta.AITitle
				newSess.AIDescription = meta.AIDescription
				newSess.FirstPrompt = meta.FirstPrompt
				newSess.LastPrompt = meta.LastPrompt
				newSess.GitBranch = meta.GitBranch
				newSess.ContextPct = meta.ContextPct
			}
		}
		_ = s.db.SaveSession(newSess)
		state.knownIDs[sessID] = newSess
		state.knownByPID[pid] = newSess
		s.broadcast(newSess)
	}
}

func resolveProcessCwd(ctx context.Context, pid int, pidStr string) string {
	if link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil && link != "" {
		return strings.TrimSuffix(link, " (deleted)")
	}
	lsofOut, lerr := exec.CommandContext(ctx, "lsof", "-a", "-p", pidStr, "-d", "cwd", "-fn").Output()
	if lerr == nil {
		for _, lline := range strings.Split(string(lsofOut), "\n") {
			if idx := strings.LastIndex(lline, " "); idx != -1 {
				candidate := strings.TrimSpace(lline[idx:])
				if strings.HasPrefix(candidate, "/") {
					return strings.TrimSuffix(candidate, " (deleted)")
				}
			}
		}
	}
	return ""
}

// scanDiskSessions delegates disk-backed session restoration to each agent's DiskSessionDiscoverer.
func (s *Server) scanDiskSessions(ctx context.Context, state *ScanState) {
	home, _ := os.UserHomeDir()
	if home == "" {
		return
	}
	s.agentAdapter("claude-code").ScanDiskSessions(ctx, s, home, state)
	s.agentAdapter("antigravity").ScanDiskSessions(ctx, s, home, state)
}
