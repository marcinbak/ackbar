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

// IngestToolTelemetry analyzes tool execution parameters and outputs to update tasks
func (s *Server) IngestToolTelemetry(sess *Session, event *Event) {
	if sess == nil || event == nil {
		return
	}

	cmd := extractCommandFromToolInput(event.ToolInput)
	filePath := extractFilePathFromToolInput(event.ToolInput)
	toolName := strings.ToLower(event.ToolName)

	// Check if branch was changed via command
	if cmd != "" {
		if newBranch := ExtractBranchCommand(cmd); newBranch != "" {
			sess.GitBranch = newBranch
			_ = s.db.SaveSession(sess)
		}
	}

	// Active task lookup
	task, err := s.findActiveTaskForSession(sess)
	if err != nil || task == nil {
		return
	}

	taskUpdated := false

	// 1. Check for PR creation or PR URL in command / activity
	var prURL string
	var prNum int
	if cmd != "" {
		prURL, prNum = ExtractPRURL(cmd)
	}
	if prURL == "" && event.Activity != "" {
		prURL, prNum = ExtractPRURL(event.Activity)
	}
	if prURL != "" {
		// Only attach PR if task has no PR or if session branch matches task branch
		branchMatches := task.Branch == "" || sess.GitBranch == "" || task.Branch == sess.GitBranch
		if branchMatches || task.PRURL == "" {
			if task.PRURL != prURL {
				task.PRURL = prURL
				task.PRNumber = prNum
				task.PRState = "OPEN"
				task.Status = "REVIEW"
				task.Substatus = "in_review"
				taskUpdated = true
			}
		}
	} else if strings.Contains(cmd, "gh pr create") {
		// Even if stdout hasn't arrived, gh pr create means we are entering review
		if task.Status == "IN_PROGRESS" {
			task.Status = "REVIEW"
			task.Substatus = "in_review"
			taskUpdated = true
		}
	}

	// 2. Check for issue key: prioritize branch name over raw commands
	issueKey := ExtractIssueKey(sess.GitBranch)
	if issueKey == "" {
		issueKey = ExtractIssueKey(sess.Name)
	}
	if issueKey != "" {
		hasRef := false
		for _, r := range task.ExternalRefs {
			if strings.EqualFold(r.RefKey, issueKey) {
				hasRef = true
				break
			}
		}
		if !hasRef {
			// Invariant: Do not contaminate an existing task with foreign issue keys from shell commands!
			// Only attach issueKey if task has no refs yet, or if session branch explicitly matches the key
			canAttach := len(task.ExternalRefs) == 0 || (sess.GitBranch != "" && strings.Contains(strings.ToUpper(sess.GitBranch), strings.ToUpper(issueKey)))
			if canAttach {
				newRef := TaskExternalRef{
					TaskID:  task.ID,
					Tracker: "jira",
					RefKey:  issueKey,
				}
				_ = s.db.InsertTaskExternalRef(&newRef)
				task.ExternalRefs = append(task.ExternalRefs, newRef)
			}
		}
	}

	// 3. Check file writes / deliverable creations (only for write/edit tools)
	isWriteTool := false
	for _, wt := range []string{"write_to_file", "replace_file_content", "create_file", "file_writer", "write", "edit"} {
		if strings.EqualFold(toolName, wt) {
			isWriteTool = true
			break
		}
	}

	if isWriteTool && filePath != "" {
		clean := filepath.Clean(filePath)
		if !strings.Contains(clean, "..") {
			baseName := filepath.Base(clean)
			ext := strings.ToLower(filepath.Ext(clean))
			var newDel *TaskDeliverable

			if strings.HasSuffix(baseName, ".retro.md") {
				newDel = &TaskDeliverable{
					TaskID:   task.ID,
					Kind:     "retrospective",
					Title:    "Retrospective",
					Host:     sess.Host,
					FilePath: clean,
				}
			} else if ext == ".html" || ext == ".png" || ext == ".jpg" || ext == ".svg" {
				newDel = &TaskDeliverable{
					TaskID:   task.ID,
					Kind:     "mockup",
					Title:    baseName,
					Host:     sess.Host,
					FilePath: clean,
				}
			} else if strings.Contains(baseName, "plan") || strings.Contains(baseName, "brief") {
				newDel = &TaskDeliverable{
					TaskID:   task.ID,
					Kind:     "plan",
					Title:    baseName,
					Host:     sess.Host,
					FilePath: clean,
				}
			}

			if newDel != nil {
				_ = s.db.InsertTaskDeliverable(newDel)
				task.Deliverables = append(task.Deliverables, *newDel)
			}
		}
	}

	if taskUpdated {
		_ = s.db.UpdateTask(task)
	}
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
	case "N14", "N15":
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

	// 1. Mark completed repo workstream as DONE if specified
	for i := range task.Workstreams {
		ws := &task.Workstreams[i]
		if completedNorm != "" && (normPath(ws.RepoNodePath) == completedNorm || strings.EqualFold(ws.RepoNodePath, completedRepoPath)) {
			if ws.Status != "DONE" {
				ws.Status = "DONE"
				ws.UpdatedAt = time.Now().UTC()
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

	if unlockedAny {
		// If task was previously marked DONE, revert it so it stays active on the board
		if task.Status == "DONE" {
			task.Status = "IN_PROGRESS"
			task.Substatus = "active"
			task.CompletedAt = nil
		}

		if len(task.Deliverables) == 0 && s.db != nil && task.ID != "" {
			if delsMap, err := s.db.getDeliverablesForTasks([]string{task.ID}); err == nil {
				task.Deliverables = delsMap[task.ID]
			}
		}

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

		_ = s.db.UpdateTask(task)
		s.pushSyncPayload(&SyncStatePayload{Tasks: []Task{*task}, Workstreams: task.Workstreams})
	}

	return unlockedAny
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
		s.syncSessionMeta(sess)
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
					s.syncSessionMeta(sSess)
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

// SyncSessionTaskWorker associates a session with an active task and syncs substatus
func (s *Server) SyncSessionTaskWorker(sess *Session) {
	if sess == nil || sess.ID == "" {
		return
	}

	// 1. Locate or create active task
	task, err := s.findOrCreateTaskForSession(sess)
	if err != nil || task == nil {
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
		s.syncSessionMeta(sess)
	}

	// 3. Reflect blocked / active / idle workflow state
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
	} else if sess.State == StateWorking && task.Substatus == "blocked" {
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
		if workflowNode == "N14" || workflowNode == "N15" {
			if s.unlockSequentialWorkstreams(task, repoNodePath) {
				taskNeedsUpdate = true
			}
		}
		if workflowNode == "N15" {
			s.completeAndCloseTaskSession(task, sess)
		}
	}

	if taskNeedsUpdate {
		_ = s.db.UpdateTask(task)
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

func (s *Server) findActiveTaskForSession(sess *Session) (*Task, error) {
	if sess == nil {
		return nil, nil
	}
	// 1. Try by branch (highest priority so switching branches switches tasks)
	if sess.GitBranch != "" && sess.GitBranch != "main" && sess.GitBranch != "master" {
		if t, err := s.db.GetTaskByBranch(sess.GitBranch); err == nil && t != nil && t.Status != "DONE" {
			return t, nil
		}
	}
	// 2. Try by issueKey if branch or session title contains ticket key (e.g. NGL-1041, NGL-993, NGL-1040)
	// Prioritize issueKey before worktree so separate tickets always bind to their own tasks!
	issueKey := ExtractIssueKey(sess.GitBranch)
	if issueKey == "" {
		issueKey = ExtractIssueKey(sess.Name)
	}
	if issueKey != "" {
		if t, err := s.db.GetTaskByExternalRef(issueKey); err == nil && t != nil && t.Status != "DONE" {
			return t, nil
		}
	}
	// 3. Try by worktree (only if branch matches or session is on same branch)
	if sess.Cwd != "" {
		if t, err := s.db.GetTaskByWorktree(sess.Cwd); err == nil && t != nil && t.Status != "DONE" {
			// Invariant: Do NOT match by CWD if git branches explicitly differ!
			// A session on branch X or main cannot be matched to a task on branch Y just because both share repo CWD.
			branchMatches := t.Branch == "" || sess.GitBranch == "" || t.Branch == sess.GitBranch
			if branchMatches {
				return t, nil
			}
		}
	}
	// 4. Fall back to active session worker record (only if active / not DONE)
	if t, err := s.db.GetActiveTaskForSession(sess.ID); err == nil && t != nil && t.Status != "DONE" {
		return t, nil
	}
	return nil, nil
}

func (s *Server) findOrCreateTaskForSession(sess *Session) (*Task, error) {
	t, err := s.findActiveTaskForSession(sess)
	if err == nil && t != nil {
		return t, nil
	}

	// Do not auto-create tasks for main/master branches or empty session unless session title has ticket key
	branch := sess.GitBranch
	issueKey := ExtractIssueKey(branch)
	if issueKey == "" {
		issueKey = ExtractIssueKey(sess.Name)
	}

	if (branch == "" || branch == "main" || branch == "master") && issueKey == "" {
		return nil, nil
	}

	title := formatTaskTitleFromBranch(branch, issueKey)
	if sess.Name != "" && !isRawSessionName(sess.Name) {
		title = sess.Name
	}

	groupName := resolveTaskGroupName(sess)
	projectName := sess.ProjectKey
	if projectName == "" && sess.Cwd != "" {
		projectName = filepath.Base(sess.Cwd)
	}
	if projectName == "" {
		projectName = "General"
	}

	// Only store worktree_path if it's an actual isolated worktree path or non-main branch
	worktreePath := sess.Cwd
	if !isIsolatedWorktree(worktreePath) && (branch == "main" || branch == "master") {
		worktreePath = ""
	}

	newTask := &Task{
		ID:           fmt.Sprintf("task_%d", time.Now().UnixNano()),
		Title:        title,
		GroupName:    groupName,
		ProjectName:  projectName,
		Status:       "IN_PROGRESS",
		Substatus:    "active",
		Branch:       branch,
		WorktreePath: worktreePath,
		Workers: []TaskWorker{
			{
				SessionID:  sess.ID,
				Agent:      sess.Agent,
				Host:       sess.Host,
				IsActive:   sess.State != StateEnded,
				AssignedAt: time.Now(),
			},
		},
	}

	if issueKey != "" {
		newTask.ExternalRefs = []TaskExternalRef{
			{
				Tracker: "jira",
				RefKey:  issueKey,
			},
		}
	}

	if err := s.db.CreateTask(newTask); err != nil {
		return nil, err
	}

	return newTask, nil
}
