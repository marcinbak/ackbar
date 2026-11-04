package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/tmux"
)

var (
	// Issue key pattern: e.g. NGL-409, ENG-12, PROJ-101 (strictly uppercase letters and numbers)
	issueKeyRegex = regexp.MustCompile(`\b([A-Z]{2,10}-[0-9]+)\b`)
	// PR URL pattern: e.g. https://github.com/owner/repo/pull/123
	prURLRegex = regexp.MustCompile(`(https://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+))`)
	// Git branch checkout / switch / worktree creation pattern
	branchCommandRegex = regexp.MustCompile(`(?:git\s+(?:checkout|switch|worktree\s+add)\s+(?:-b|-B|-c|-C)\s+['"]?)([\w\-/\.]+)`)
	// Test command execution pattern
	testCmdRegex = regexp.MustCompile(`\b(go\s+test|flutter\s+test|npm\s+test|pnpm\s+test|yarn\s+test|pytest|cargo\s+test)\b`)
)

// ExtractIssueKey extracts the first Jira/Linear issue key from text, e.g. "feat/NGL-409-ws" -> "NGL-409"
func ExtractIssueKey(text string) string {
	m := issueKeyRegex.FindStringSubmatch(text)
	if len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return ""
}

// ExtractPRURL extracts GitHub pull request URL and number from text
func ExtractPRURL(text string) (string, int) {
	m := prURLRegex.FindStringSubmatch(text)
	if len(m) > 4 {
		num, _ := strconv.Atoi(m[4])
		return m[1], num
	}
	return "", 0
}

// ExtractBranchCommand extracts branch name from git checkout/switch/worktree command
func ExtractBranchCommand(text string) string {
	m := branchCommandRegex.FindStringSubmatch(text)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// Helper to extract command string from ToolInput
func extractCommandFromToolInput(toolInput any) string {
	if toolInput == nil {
		return ""
	}
	switch v := toolInput.(type) {
	case string:
		return v
	case map[string]any:
		for _, k := range []string{"command", "cmd", "CommandLine"} {
			if s, ok := v[k].(string); ok && s != "" {
				return s
			}
		}
	case map[string]string:
		for _, k := range []string{"command", "cmd", "CommandLine"} {
			if s, ok := v[k]; ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// Helper to extract file path from ToolInput
func extractFilePathFromToolInput(toolInput any) string {
	if toolInput == nil {
		return ""
	}
	if m, ok := toolInput.(map[string]any); ok {
		for _, k := range []string{"file_path", "path", "target_file", "TargetFile", "FilePath", "file"} {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func resolveTaskGroupName(sess *Session) string {
	if sess == nil {
		return "Personal"
	}

	// 1. If session has an assigned NodePath (e.g. "Personal/Ackbar" or "Modemobile/NGL/ngl-ios")
	if sess.NodePath != "" && !strings.EqualFold(sess.NodePath, "unassigned") {
		parts := strings.Split(sess.NodePath, "/")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}

	// 2. Specific known project keys or workspace path patterns
	proj := strings.ToLower(sess.ProjectKey)
	cwd := strings.ToLower(sess.Cwd)
	acc := strings.ToLower(sess.AccountID)

	if proj == "ackbar" || strings.Contains(cwd, "/work/ackbar") || strings.HasSuffix(cwd, "/ackbar") {
		return "Personal"
	}

	if strings.Contains(acc, "modemobile") || strings.Contains(acc, "mode") ||
		strings.Contains(proj, "modemobile") || strings.Contains(proj, "ngl") || strings.Contains(proj, "mea") ||
		strings.Contains(cwd, "/modemobile") || strings.Contains(cwd, "/ngl") || strings.HasSuffix(cwd, "/ngl") {
		return "Modemobile"
	}

	return "Personal"
}

func formatTaskTitleFromBranch(branch string, issueKey string) string {
	if issueKey != "" {
		cleaned := strings.TrimPrefix(branch, "feat/")
		cleaned = strings.TrimPrefix(cleaned, "fix/")
		cleaned = strings.TrimPrefix(cleaned, "refactor/")
		lowerCleaned := strings.ToLower(cleaned)
		lowerKey := strings.ToLower(issueKey)
		if idx := strings.Index(lowerCleaned, lowerKey); idx != -1 {
			cleaned = cleaned[:idx] + cleaned[idx+len(lowerKey):]
		}
		cleaned = strings.ReplaceAll(cleaned, "-", " ")
		cleaned = strings.ReplaceAll(cleaned, "_", " ")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			return fmt.Sprintf("%s: %s", issueKey, strings.Title(cleaned))
		}
		return issueKey
	}
	cleaned := strings.TrimPrefix(branch, "feat/")
	cleaned = strings.TrimPrefix(cleaned, "fix/")
	cleaned = strings.TrimPrefix(cleaned, "refactor/")
	cleaned = strings.ReplaceAll(cleaned, "-", " ")
	cleaned = strings.ReplaceAll(cleaned, "_", " ")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned != "" {
		return strings.Title(cleaned)
	}
	return branch
}

// ClassifyWorkflowNodeState maps dev-workflow nodes (N0-N15) and session state to Ackbar task status/substatus
func ClassifyWorkflowNodeState(node string, sessState State, hasExplicitBlocker bool) (status string, substatus string) {
	n := strings.ToUpper(strings.TrimSpace(node))
	switch n {
	case "N0":
		if sessState == StateWorking {
			return "IN_PROGRESS", "active"
		}
		return "NEW", "ready"
	case "N1":
		if sessState == StateIdle || sessState == StateBlocked || hasExplicitBlocker {
			return "IN_PROGRESS", "question"
		}
		return "IN_PROGRESS", "active"
	case "N2":
		if sessState == StateIdle || sessState == StateBlocked || hasExplicitBlocker {
			return "REVIEW", "plan_review"
		}
		return "IN_PROGRESS", "active"
	case "N3", "N4", "N5", "N6", "N7", "N8", "N9", "N10":
		if hasExplicitBlocker || sessState == StateBlocked {
			return "IN_PROGRESS", "blocked"
		}
		if sessState == StateIdle {
			return "IN_PROGRESS", "stalled"
		}
		return "IN_PROGRESS", "active"
	case "N11", "N12", "N13":
		return "REVIEW", "pr_review"
	case "N14", "N15", "DONE", "COMPLETED":
		return "DONE", "completed"
	default:
		if hasExplicitBlocker || sessState == StateBlocked {
			return "IN_PROGRESS", "blocked"
		}
		if sessState == StateWorking {
			return "IN_PROGRESS", "active"
		}
		return "IN_PROGRESS", "idle"
	}
}

// unlockSequentialWorkstreams transitions downstream workstreams from QUEUED/NEW to IN_PROGRESS upon upstream completion
func (s *Server) unlockSequentialWorkstreams(task *Task, completedRepoPath string) bool {
	if task == nil || len(task.Workstreams) == 0 {
		return false
	}

	normPath := func(p string) string {
		return strings.TrimSpace(filepath.Clean(p))
	}

	completedNorm := ""
	if completedRepoPath != "" {
		completedNorm = normPath(completedRepoPath)
	}

	changed := false
	// 1. Mark completed repo workstream as DONE if specified
	for i := range task.Workstreams {
		ws := &task.Workstreams[i]
		if completedNorm != "" && (normPath(ws.RepoNodePath) == completedNorm || strings.EqualFold(ws.RepoNodePath, completedRepoPath)) {
			if ws.Status != "DONE" {
				ws.Status = "DONE"
				ws.UpdatedAt = time.Now().UTC()
				changed = true
			}
		}
	}

	// Helper to check if an upstream dependency is DONE
	isUpstreamDone := func(dep string) bool {
		depNorm := normPath(dep)
		for _, ws := range task.Workstreams {
			if normPath(ws.RepoNodePath) == depNorm || strings.EqualFold(ws.RepoNodePath, dep) {
				return ws.Status == "DONE"
			}
		}
		return false
	}

	// 2. Find any workstreams in QUEUED or NEW that depend on completed workstream or upstream done
	unlockedAny := false
	for i := range task.Workstreams {
		ws := &task.Workstreams[i]
		if (ws.Status == "QUEUED" || ws.Status == "NEW") && ws.DependsOn != "" {
			depMatchesCompleted := completedNorm != "" && (normPath(ws.DependsOn) == completedNorm || strings.EqualFold(ws.DependsOn, completedRepoPath))
			if depMatchesCompleted || isUpstreamDone(ws.DependsOn) {
				ws.Status = "IN_PROGRESS"
				ws.WorkflowNode = "N0"
				ws.UpdatedAt = time.Now().UTC()
				unlockedAny = true
			}
		}
	}

	if changed || unlockedAny {
		// If task was previously marked DONE, revert it so it stays active on the board
		if unlockedAny && task.Status == "DONE" {
			task.Status = "IN_PROGRESS"
			task.Substatus = "active"
			task.CompletedAt = nil
		}

		if len(task.Deliverables) == 0 && s.db != nil && task.ID != "" {
			if delsMap, err := s.db.getDeliverablesForTasks([]string{task.ID}); err == nil {
				task.Deliverables = delsMap[task.ID]
			}
		}

		if unlockedAny {
			// Check for learnings (or plan/prd) deliverables to construct a handoff note for newly unlocked workstreams
			for _, ws := range task.Workstreams {
				if ws.Status == "IN_PROGRESS" && ws.DependsOn != "" {
					handoffPrefix := fmt.Sprintf("[Handoff to %s]", ws.RepoNodePath)
					if !strings.Contains(task.Notes, handoffPrefix) {
						var handoffDetails []string
						for _, del := range task.Deliverables {
							if del.Kind == "learnings" || del.Kind == "plan" || del.Kind == "prd" {
								detail := del.Title
								if del.ContentMD != "" {
									contentSummary := strings.TrimSpace(del.ContentMD)
									if len(contentSummary) > 200 {
										contentSummary = contentSummary[:200] + "..."
									}
									detail += fmt.Sprintf(": %s", contentSummary)
								} else if del.URL != "" {
									detail += fmt.Sprintf(" (%s)", del.URL)
								}
								handoffDetails = append(handoffDetails, detail)
							}
						}
						if len(handoffDetails) > 0 {
							handoffNote := fmt.Sprintf("\n%s Upstream handoff context:\n- %s", handoffPrefix, strings.Join(handoffDetails, "\n- "))
							if task.Notes == "" {
								task.Notes = strings.TrimSpace(handoffNote)
							} else {
								task.Notes += handoffNote
							}
						}
					}
				}
			}
		}

		_ = s.db.UpdateTask(task)
		s.pushSyncPayload(&SyncStatePayload{Tasks: []Task{*task}, Workstreams: task.Workstreams})
	}

	return changed || unlockedAny
}

// completeAndCloseTaskSession terminates Tmux sessions and updates metadata when task reaches N15
func (s *Server) completeAndCloseTaskSession(task *Task, sess *Session) {
	if task == nil {
		return
	}

	now := time.Now().UTC()

	// If a specific session was provided:
	if sess != nil {
		if sess.TmuxName != "" {
			_ = tmux.Kill(context.Background(), sess.TmuxName)
		}
		sess.IsDone = true
		sess.State = StateEnded
		sess.Activity = "Completed (N15)"
		sess.MetadataUpdatedAt = now
		_ = s.db.SaveSession(sess)
		s.broadcast(sess)
	} else {
		// Close all active worker sessions for this task
		for _, w := range task.Workers {
			if w.SessionID != "" && w.IsActive {
				if sSess, _ := s.db.GetSession(w.SessionID); sSess != nil && sSess.State != StateEnded {
					if sSess.TmuxName != "" {
						_ = tmux.Kill(context.Background(), sSess.TmuxName)
					}
					sSess.IsDone = true
					sSess.State = StateEnded
					sSess.Activity = "Completed (N15)"
					sSess.MetadataUpdatedAt = now
					_ = s.db.SaveSession(sSess)
					s.broadcast(sSess)
				}
			}
		}
	}

	// Deactivate workers in task
	for i := range task.Workers {
		task.Workers[i].IsActive = false
	}
	_ = s.db.UpdateTask(task)
}

// SyncSessionTaskWorker associates a session with an explicitly bound active task and syncs substatus
func (s *Server) SyncSessionTaskWorker(sess *Session) {
	if sess == nil || sess.ID == "" {
		return
	}

	// 1. Look up an existing non-DONE task that this session is explicitly bound to:
	//    a) If sess.CurrentTaskID != "", look up s.db.GetTaskByID(sess.CurrentTaskID)
	//    b) Else look up s.db.GetActiveTaskForSession(sess.ID)
	//    c) If no explicitly bound task is found, return immediately!
	var task *Task
	if sess.CurrentTaskID != "" {
		if t, err := s.db.GetTaskByID(sess.CurrentTaskID); err == nil && t != nil && t.Status != "DONE" {
			task = t
		}
	}
	if task == nil {
		if t, err := s.db.GetActiveTaskForSession(sess.ID); err == nil && t != nil && t.Status != "DONE" {
			task = t
		}
	}
	if task == nil {
		return
	}

	var repoNodePath string
	var workflowNode string
	for _, w := range task.Workers {
		if w.SessionID == sess.ID {
			repoNodePath = w.RepoNodePath
			workflowNode = w.WorkflowNode
			break
		}
	}
	if repoNodePath == "" {
		for _, ws := range task.Workstreams {
			if (ws.TargetHost == sess.Host || ws.TargetHost == "") && ws.Status == "IN_PROGRESS" {
				repoNodePath = ws.RepoNodePath
				if workflowNode == "" {
					workflowNode = ws.WorkflowNode
				}
				break
			}
		}
	}
	if workflowNode == "" {
		for _, ws := range task.Workstreams {
			if ws.WorkflowNode != "" {
				workflowNode = ws.WorkflowNode
				break
			}
		}
	}

	// 2. Ensure worker is registered in task_workers
	worker := &TaskWorker{
		TaskID:       task.ID,
		SessionID:    sess.ID,
		Agent:        sess.Agent,
		Host:         sess.Host,
		IsActive:     sess.State != StateEnded,
		RepoNodePath: repoNodePath,
		WorkflowNode: workflowNode,
		AssignedAt:   time.Now(),
	}
	_ = s.db.InsertTaskWorker(worker)

	if sess.CurrentTaskID != task.ID {
		sess.CurrentTaskID = task.ID
		sess.MetadataUpdatedAt = time.Now().UTC()
		_ = s.db.SaveSession(sess)
	}

	// Update task.Workers in-memory
	workerUpdated := false
	for i := range task.Workers {
		if task.Workers[i].SessionID == sess.ID {
			task.Workers[i].IsActive = sess.State != StateEnded
			task.Workers[i].Agent = sess.Agent
			task.Workers[i].Host = sess.Host
			if repoNodePath != "" {
				task.Workers[i].RepoNodePath = repoNodePath
			}
			if workflowNode != "" {
				task.Workers[i].WorkflowNode = workflowNode
			}
			workerUpdated = true
			break
		}
	}
	if !workerUpdated {
		task.Workers = append(task.Workers, *worker)
	}

	// When a worker session ends, check if any active worker remains
	if sess.State == StateEnded {
		anyActiveWorker := false
		for _, w := range task.Workers {
			if w.IsActive {
				anyActiveWorker = true
				break
			}
		}
		if !anyActiveWorker && task.Status == "IN_PROGRESS" {
			task.Substatus = "stalled"
			_ = s.db.UpdateTask(task)
		}
		return
	}

	// 3. Reflect blocked / active / idle workflow state
	oldStatus := task.Status
	taskNeedsUpdate := false
	if sess.State == StateBlocked {
		if task.Substatus != "blocked" {
			task.Substatus = "blocked"
			taskNeedsUpdate = true
		}
		reason := "Waiting for user response"
		if sess.Blocked != nil {
			if sess.Blocked.Question != "" {
				reason = sess.Blocked.Question
			} else if sess.Blocked.Reason != "" {
				reason = sess.Blocked.Reason
			}
		}
		if task.BlockerQuestion != reason {
			task.BlockerQuestion = reason
			taskNeedsUpdate = true
		}
	} else if sess.State == StateWorking && (task.Status == "NEW" || task.Substatus == "blocked" || task.Substatus == "stalled" || task.Substatus == "question") {
		task.Status = "IN_PROGRESS"
		task.Substatus = "active"
		task.BlockerQuestion = ""
		taskNeedsUpdate = true
	} else if sess.State == StateIdle && task.TaskKind != "design" && workflowNode != "" {
		newStatus, newSubstatus := ClassifyWorkflowNodeState(workflowNode, sess.State, task.Substatus == "blocked" && task.BlockerQuestion != "")
		if task.Status != newStatus || task.Substatus != newSubstatus {
			task.Status = newStatus
			task.Substatus = newSubstatus
			taskNeedsUpdate = true
		}
		for i := range task.Workstreams {
			ws := &task.Workstreams[i]
			if repoNodePath == "" || strings.EqualFold(ws.RepoNodePath, repoNodePath) {
				if ws.WorkflowNode != workflowNode || ws.Status != newStatus {
					ws.WorkflowNode = workflowNode
					ws.Status = newStatus
					ws.UpdatedAt = time.Now().UTC()
					taskNeedsUpdate = true
				}
			}
		}
		if workflowNode == "N14" || workflowNode == "N15" || workflowNode == "DONE" || workflowNode == "COMPLETED" {
			if s.unlockSequentialWorkstreams(task, repoNodePath) {
				taskNeedsUpdate = true
			}
		}
		if workflowNode == "N15" || workflowNode == "DONE" || workflowNode == "COMPLETED" {
			s.completeAndCloseTaskSession(task, sess)
		}
	}

	if taskNeedsUpdate {
		if err := s.db.UpdateTask(task); err == nil {
			s.maybeNotifyTaskReview(oldStatus, task)
		}
	}
}

func isIsolatedWorktree(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	if strings.Contains(clean, ".worktree") || strings.Contains(clean, "worktrees") {
		return true
	}
	// In git worktrees, .git is a file referencing the parent repository's gitdir.
	// In primary root checkouts, .git is a directory.
	fi, err := os.Stat(filepath.Join(clean, ".git"))
	if err == nil && !fi.IsDir() {
		return true
	}
	return false
}
