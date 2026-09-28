package daemon

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	acc := strings.ToLower(sess.AccountID)
	node := strings.ToLower(sess.NodePath)
	proj := strings.ToLower(sess.ProjectKey)
	cwd := strings.ToLower(sess.Cwd)

	if strings.Contains(acc, "work") || strings.Contains(acc, "mode") ||
		strings.Contains(node, "work") || strings.Contains(node, "modemobile") ||
		strings.Contains(proj, "modemobile") || strings.Contains(proj, "ngl") || strings.Contains(proj, "mea") ||
		strings.Contains(cwd, "work") || strings.Contains(cwd, "modemobile") {
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
		if task.PRURL != prURL {
			task.PRURL = prURL
			task.PRNumber = prNum
			task.PRState = "OPEN"
			task.Status = "REVIEW"
			task.Substatus = "in_review"
			taskUpdated = true
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
	if issueKey == "" && cmd != "" {
		if strings.Contains(cmd, "git") || strings.Contains(cmd, "gh") {
			issueKey = ExtractIssueKey(cmd)
		}
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
			newRef := TaskExternalRef{
				TaskID:  task.ID,
				Tracker: "jira",
				RefKey:  issueKey,
			}
			_ = s.db.InsertTaskExternalRef(&newRef)
			task.ExternalRefs = append(task.ExternalRefs, newRef)
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

	// 2. Ensure worker is registered in task_workers
	worker := &TaskWorker{
		TaskID:     task.ID,
		SessionID:  sess.ID,
		Agent:      sess.Agent,
		Host:       sess.Host,
		IsActive:   sess.State != StateEnded,
		AssignedAt: time.Now(),
	}
	_ = s.db.InsertTaskWorker(worker)

	// 3. Reflect blocked / active state
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
	}

	if taskNeedsUpdate {
		_ = s.db.UpdateTask(task)
	}
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
	// 2. Try by worktree
	if sess.Cwd != "" {
		if t, err := s.db.GetTaskByWorktree(sess.Cwd); err == nil && t != nil && t.Status != "DONE" {
			return t, nil
		}
	}
	// 3. Fall back to active session worker record (only if active / not DONE)
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

	// Do not auto-create tasks for main/master branches or empty session
	branch := sess.GitBranch
	if branch == "" || branch == "main" || branch == "master" {
		return nil, nil
	}

	issueKey := ExtractIssueKey(branch)
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

	newTask := &Task{
		ID:           fmt.Sprintf("task_%d", time.Now().UnixNano()),
		Title:        title,
		GroupName:    groupName,
		ProjectName:  projectName,
		Status:       "IN_PROGRESS",
		Substatus:    "active",
		Branch:       branch,
		WorktreePath: sess.Cwd,
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
