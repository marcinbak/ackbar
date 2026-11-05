package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func readWorkflowFileBounded(path string, maxBytes int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	if fi.Size() > maxBytes {
		return nil, fmt.Errorf("file %s exceeds maximum size limit (%d > %d)", path, fi.Size(), maxBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

type DevWorkflowRun struct {
	Ticket             *string          `json:"ticket"`
	Repo               string           `json:"repo"`
	Branch             string           `json:"branch"`
	Worktree           string           `json:"worktree"`
	Node               string           `json:"node"`
	Started            string           `json:"started"`
	Completed          []string         `json:"completed"`
	Lite               bool             `json:"lite"`
	ImplAttempts       int              `json:"impl_attempts"`
	PlanRevisions      int              `json:"plan_revisions"`
	ReviewBudgetLeft   int              `json:"review_budget_left"`
	ReviewRounds       int              `json:"review_rounds"`
	HumanInterventions []map[string]any `json:"human_interventions"`
	ImplementerAgent   any              `json:"implementer_agent"`
	Tier               string           `json:"tier"`
	Artifacts          []string         `json:"artifacts"`
	PR                 *DevWorkflowPR   `json:"pr"`
	Notes              string           `json:"notes"`
}

type DevWorkflowPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

var rePullNumber = regexp.MustCompile(`/pull/(\d+)`)

func (p *DevWorkflowPR) UnmarshalJSON(data []byte) error {
	str := strings.TrimSpace(string(data))
	if str == "" || str == "null" {
		return nil
	}
	// 1. Structured object
	type rawPR DevWorkflowPR
	var obj rawPR
	if err := json.Unmarshal(data, &obj); err == nil && (obj.Number != 0 || obj.URL != "") {
		p.Number = obj.Number
		p.URL = obj.URL
		return nil
	}
	// 2. Integer number
	var num int
	if err := json.Unmarshal(data, &num); err == nil {
		p.Number = num
		return nil
	}
	// 3. String URL
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		p.URL = s
		if m := rePullNumber.FindStringSubmatch(s); len(m) > 1 {
			p.Number, _ = strconv.Atoi(m[1])
		}
		return nil
	}
	return nil
}

// IngestDevWorkflowRuns scans ~/.claude/dev-workflow-runs/ in update-only mode and synchronizes
// workflow node, branch, worktree, PR, and deliverables onto existing non-DONE tasks.
func (s *Server) IngestDevWorkflowRuns() error {
	if s == nil || s.db == nil {
		return nil
	}

	home := s.homeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || home == "" {
			return nil
		}
	}

	runsDir := filepath.Join(home, ".claude", "dev-workflow-runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read dev-workflow-runs dir: %w", err)
	}

	tasks, err := s.db.GetTasks()
	if err != nil {
		return err
	}

	var activeTasks []*Task
	for i := range tasks {
		if !strings.EqualFold(tasks[i].Status, "DONE") {
			activeTasks = append(activeTasks, &tasks[i])
		}
	}
	if len(activeTasks) == 0 {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(runsDir, entry.Name())
		var modTime time.Time
		if info, infoErr := entry.Info(); infoErr == nil && info != nil {
			modTime = info.ModTime()
		}
		if err := s.ingestSingleWorkflowRun(filePath, modTime, activeTasks); err != nil {
			log.Printf("[DevWorkflowWatcher] Error ingesting %s: %v", entry.Name(), err)
		}
	}

	return nil
}

func taskMatchesWorkflowRepo(task *Task, runRepo string) bool {
	if task == nil {
		return false
	}
	repoClean := strings.ToLower(strings.TrimSpace(filepath.Base(runRepo)))
	if repoClean == "" || repoClean == "." {
		return true
	}
	if strings.Contains(strings.ToLower(task.ProjectName), repoClean) ||
		strings.Contains(strings.ToLower(task.SubprojectName), repoClean) {
		return true
	}
	for _, ws := range task.Workstreams {
		if strings.Contains(strings.ToLower(ws.RepoNodePath), repoClean) {
			return true
		}
	}
	for _, w := range task.Workers {
		if strings.Contains(strings.ToLower(w.RepoNodePath), repoClean) {
			return true
		}
	}
	return false
}

func (s *Server) matchWorkflowRunToTask(run *DevWorkflowRun, modTime time.Time, activeTasks []*Task) *Task {
	if run == nil || len(activeTasks) == 0 {
		return nil
	}

	ticketStr := ""
	if run.Ticket != nil && *run.Ticket != "" {
		ticketStr = strings.ToUpper(strings.TrimSpace(*run.Ticket))
	}
	if ticketStr == "" && run.Branch != "" {
		ticketStr = ExtractIssueKey(run.Branch)
	}

	cleanRunWorktree := ""
	if strings.TrimSpace(run.Worktree) != "" {
		cleanRunWorktree = filepath.Clean(strings.TrimSpace(run.Worktree))
	}
	cleanRunBranch := strings.TrimSpace(run.Branch)
	if cleanRunBranch == "main" || cleanRunBranch == "master" {
		cleanRunBranch = ""
	}

	isFreshForTask := func(t *Task) bool {
		if t == nil {
			return false
		}
		if !t.CreatedAt.IsZero() && !modTime.IsZero() && modTime.Before(t.CreatedAt.Add(-24*time.Hour)) {
			return false
		}
		return true
	}

	// 1. Exact worktree or branch match against task or its bound worker sessions
	for _, t := range activeTasks {
		if !isFreshForTask(t) {
			continue
		}
		if cleanRunWorktree != "" && t.WorktreePath != "" && filepath.Clean(t.WorktreePath) == cleanRunWorktree {
			return t
		}
		if cleanRunBranch != "" && t.Branch != "" && strings.EqualFold(t.Branch, cleanRunBranch) {
			return t
		}
		for _, w := range t.Workers {
			if w.SessionID == "" {
				continue
			}
			if sess, _ := s.db.GetSession(w.SessionID); sess != nil {
				if cleanRunWorktree != "" && sess.Cwd != "" && filepath.Clean(sess.Cwd) == cleanRunWorktree {
					return t
				}
				if cleanRunBranch != "" && sess.GitBranch != "" && strings.EqualFold(sess.GitBranch, cleanRunBranch) {
					return t
				}
			}
		}
	}

	// 2. Ticket key match (with repo disambiguation)
	if ticketStr != "" {
		var ticketMatches []*Task
		for _, t := range activeTasks {
			if !isFreshForTask(t) {
				continue
			}
			matchedKey := false
			for _, ref := range t.ExternalRefs {
				if strings.EqualFold(strings.TrimSpace(ref.RefKey), ticketStr) {
					matchedKey = true
					break
				}
			}
			if !matchedKey && strings.EqualFold(ExtractIssueKey(t.Title), ticketStr) {
				matchedKey = true
			}
			if !matchedKey && strings.EqualFold(ExtractIssueKey(t.Branch), ticketStr) {
				matchedKey = true
			}
			if matchedKey {
				ticketMatches = append(ticketMatches, t)
			}
		}

		if len(ticketMatches) == 1 {
			if run.Repo == "" || taskMatchesWorkflowRepo(ticketMatches[0], run.Repo) {
				return ticketMatches[0]
			}
		} else if len(ticketMatches) > 1 {
			for _, t := range ticketMatches {
				if taskMatchesWorkflowRepo(t, run.Repo) {
					return t
				}
			}
			return ticketMatches[0]
		}
	}

	return nil
}

func (s *Server) resolveGitHubPRURLForTask(task *Task, prNumber int) string {
	if task == nil || prNumber <= 0 {
		return ""
	}
	if task.PRURL != "" && strings.Contains(task.PRURL, "/pull/") {
		return task.PRURL
	}
	for _, w := range task.Workers {
		if w.SessionID == "" {
			continue
		}
		if sess, _ := s.db.GetSession(w.SessionID); sess != nil {
			pk := strings.TrimSpace(sess.ProjectKey)
			if strings.HasPrefix(strings.ToLower(pk), "github.com/") {
				parts := strings.Split(strings.TrimSuffix(pk, ".git"), "/")
				if len(parts) >= 3 && parts[1] != "" && parts[2] != "" {
					return fmt.Sprintf("https://github.com/%s/%s/pull/%d", parts[1], parts[2], prNumber)
				}
			}
		}
	}
	return ""
}

func (s *Server) ingestSingleWorkflowRun(jsonPath string, modTime time.Time, activeTasks []*Task) error {
	data, err := readWorkflowFileBounded(jsonPath, 2*1024*1024)
	if err != nil {
		return err
	}

	var run DevWorkflowRun
	if err := json.Unmarshal(data, &run); err != nil {
		return fmt.Errorf("invalid json in %s: %w", jsonPath, err)
	}

	if run.Branch == "" && (run.Ticket == nil || *run.Ticket == "") && run.Worktree == "" {
		return nil
	}

	// Strictly match against existing non-DONE tasks; NEVER auto-create new tasks.
	task := s.matchWorkflowRunToTask(&run, modTime, activeTasks)
	if task == nil {
		return nil
	}

	oldStatus := task.Status
	taskUpdated := false

	canonicalNode := normalizeWorkflowNode(run.Node)

	// Determine current workflow node on task
	currentNode := ""
	for _, w := range task.Workers {
		if w.IsActive && shouldAdvanceWorkflowNode(currentNode, w.WorkflowNode) {
			currentNode = normalizeWorkflowNode(w.WorkflowNode)
		}
	}
	for _, ws := range task.Workstreams {
		if (run.Repo == "" || taskMatchesWorkflowRepo(&Task{Workstreams: []TaskWorkstream{ws}}, run.Repo)) &&
			shouldAdvanceWorkflowNode(currentNode, ws.WorkflowNode) {
			currentNode = normalizeWorkflowNode(ws.WorkflowNode)
		}
	}

	effectiveNode := currentNode
	if shouldAdvanceWorkflowNode(currentNode, canonicalNode) {
		effectiveNode = canonicalNode
	}

	// Determine active session state for this task
	sessState := StateIdle
	for _, w := range task.Workers {
		if w.IsActive && w.SessionID != "" {
			if sess, _ := s.db.GetSession(w.SessionID); sess != nil && sess.State == StateWorking {
				sessState = StateWorking
				break
			}
		}
	}

	hasExplicitBlocker := task.Substatus == "blocked" && task.BlockerQuestion != ""

	// Resolve PR URL & Number
	prURL := ""
	prNum := 0
	if run.PR != nil {
		var extractedNum int
		prURL, extractedNum = ExtractPRURL(strings.TrimSpace(run.PR.URL))
		prNum = run.PR.Number
		if prNum == 0 && extractedNum > 0 {
			prNum = extractedNum
		}
		if prURL == "" && prNum > 0 {
			prURL = s.resolveGitHubPRURLForTask(task, prNum)
		}
	}
	if prURL != "" && task.PRURL != prURL {
		task.PRURL = prURL
		if prNum > 0 {
			task.PRNumber = prNum
		}
		if task.PRState == "" {
			task.PRState = "OPEN"
		}
		taskUpdated = true
	} else if prNum > 0 && task.PRNumber != prNum {
		task.PRNumber = prNum
		if task.PRState == "" {
			task.PRState = "OPEN"
		}
		taskUpdated = true
	}

	if (task.PRURL != "" || task.PRNumber > 0) && shouldAdvanceWorkflowNode(effectiveNode, "N11") {
		effectiveNode = "N11"
	}

	if effectiveNode != "" {
		newStatus, newSubstatus := ClassifyWorkflowNodeState(effectiveNode, sessState, hasExplicitBlocker)

		// Monotonic Guard 1: Never snap an IN_PROGRESS/REVIEW task back to NEW
		if newStatus == "NEW" && task.Status != "" && !strings.EqualFold(task.Status, "NEW") {
			newStatus = task.Status
			if task.Substatus != "" && task.Substatus != "ready" && task.Substatus != "queued" {
				newSubstatus = task.Substatus
			} else {
				newSubstatus = "active"
			}
		}

		// Monotonic Guard 2: Never downgrade REVIEW (or open PR) back to IN_PROGRESS/NEW
		if (strings.EqualFold(task.Status, "REVIEW") || task.PRURL != "" || task.PRNumber > 0) && (newStatus == "IN_PROGRESS" || newStatus == "NEW") {
			newStatus = "REVIEW"
			if task.Substatus == "pr_review" || task.Substatus == "in_review" || task.Substatus == "plan_review" {
				newSubstatus = task.Substatus
			} else {
				newSubstatus = "pr_review"
			}
		}

		// Monotonic Guard 3: If workflow file reached N14/N15/done while PR is still open (unmerged), keep in REVIEW until PR merges
		if newStatus == "DONE" && (task.PRURL != "" || task.PRNumber > 0) && task.PRState != "MERGED" {
			prMerged := false
			if task.PRURL != "" {
				prCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				prState, err := checkGitHubPRState(prCtx, task.PRURL)
				cancel()
				if err == nil && prState == "MERGED" {
					task.PRState = "MERGED"
					prMerged = true
				}
			}
			if !prMerged {
				newStatus = "REVIEW"
				newSubstatus = "pr_review"
				if shouldAdvanceWorkflowNode("N13", effectiveNode) {
					effectiveNode = "N13"
				}
			}
		}

		if newStatus == "DONE" {
			if task.CompletedAt == nil {
				now := time.Now().UTC()
				task.CompletedAt = &now
			}
		}

		if task.Status != newStatus || task.Substatus != newSubstatus {
			task.Status = newStatus
			task.Substatus = newSubstatus
			taskUpdated = true
		}

		for i := range task.Workers {
			w := &task.Workers[i]
			if w.IsActive && shouldAdvanceWorkflowNode(w.WorkflowNode, effectiveNode) {
				w.WorkflowNode = effectiveNode
				_ = s.db.InsertTaskWorker(w)
				taskUpdated = true
			}
		}
		for i := range task.Workstreams {
			ws := &task.Workstreams[i]
			if run.Repo == "" || len(task.Workstreams) == 1 || taskMatchesWorkflowRepo(&Task{Workstreams: []TaskWorkstream{*ws}}, run.Repo) {
				wsStatus := task.Status
				if wsStatus == "NEW" && ws.Status != "" && !strings.EqualFold(ws.Status, "NEW") && !strings.EqualFold(ws.Status, "QUEUED") {
					wsStatus = ws.Status
				}
				if shouldAdvanceWorkflowNode(ws.WorkflowNode, effectiveNode) || (ws.Status != wsStatus && wsStatus != "") {
					if shouldAdvanceWorkflowNode(ws.WorkflowNode, effectiveNode) {
						ws.WorkflowNode = effectiveNode
					}
					if wsStatus != "" {
						ws.Status = wsStatus
					}
					ws.UpdatedAt = time.Now().UTC()
					taskUpdated = true
				}
			}
		}

		if task.Status == "DONE" && (effectiveNode == "N14" || effectiveNode == "N15") {
			if s.unlockSequentialWorkstreams(task, run.Repo) {
				taskUpdated = true
			}
		}
		if task.Status == "DONE" && effectiveNode == "N15" {
			s.completeAndCloseTaskSession(task, nil)
		}
	}

	// Check for companion brief and retro files
	basePrefix := strings.TrimSuffix(jsonPath, ".json")
	briefPath := basePrefix + ".brief.md"
	retroPath := basePrefix + ".retro.md"
	_, briefNotes := extractBriefMetadata(briefPath)

	if task.Notes == "" && (run.Notes != "" || briefNotes != "") {
		if run.Notes != "" {
			task.Notes = run.Notes
		} else {
			task.Notes = briefNotes
		}
		taskUpdated = true
	}

	if task.Branch == "" && run.Branch != "" {
		task.Branch = run.Branch
		taskUpdated = true
	}

	if task.WorktreePath == "" && run.Worktree != "" {
		task.WorktreePath = run.Worktree
		taskUpdated = true
	}

	ticketStr := ""
	if run.Ticket != nil && *run.Ticket != "" {
		ticketStr = strings.ToUpper(strings.TrimSpace(*run.Ticket))
	}
	if len(task.ExternalRefs) == 0 && ticketStr != "" {
		ref := TaskExternalRef{
			TaskID:  task.ID,
			Tracker: "jira",
			RefKey:  ticketStr,
		}
		_ = s.db.InsertTaskExternalRef(&ref)
		task.ExternalRefs = append(task.ExternalRefs, ref)
		taskUpdated = true
	}

	// Attach companion deliverables onto the matched task only when missing or modified
	hasDeliverableContent := func(filePath, content string) bool {
		for _, d := range task.Deliverables {
			if d.FilePath == filePath && d.ContentMD == content {
				return true
			}
		}
		return false
	}
	deliverableAdded := false
	if briefData, err := readWorkflowFileBounded(briefPath, 1*1024*1024); err == nil {
		content := string(briefData)
		if !hasDeliverableContent(briefPath, content) {
			del := TaskDeliverable{
				TaskID:    task.ID,
				Kind:      "plan",
				Title:     "Change Brief",
				FilePath:  briefPath,
				Provider:  "local",
				ContentMD: content,
			}
			if err := s.db.InsertTaskDeliverable(&del); err == nil {
				deliverableAdded = true
			}
		}
	}
	if retroData, err := readWorkflowFileBounded(retroPath, 1*1024*1024); err == nil {
		content := string(retroData)
		if !hasDeliverableContent(retroPath, content) {
			del := TaskDeliverable{
				TaskID:    task.ID,
				Kind:      "retrospective",
				Title:     "Retrospective",
				FilePath:  retroPath,
				Provider:  "local",
				ContentMD: content,
			}
			if err := s.db.InsertTaskDeliverable(&del); err == nil {
				deliverableAdded = true
			}
		}
	}

	if deliverableAdded {
		if delsMap, err := s.db.getDeliverablesForTasks([]string{task.ID}); err == nil {
			task.Deliverables = delsMap[task.ID]
		}
		taskUpdated = true
	}

	if taskUpdated {
		if err := s.db.UpdateTask(task); err == nil {
			s.pushSyncPayload(&SyncStatePayload{
				Tasks:        []Task{*task},
				Workstreams:  task.Workstreams,
				Deliverables: task.Deliverables,
			})
			s.maybeNotifyTaskReview(oldStatus, task)
		}
	}

	return nil
}

func extractBriefMetadata(briefPath string) (title string, notes string) {
	data, err := readWorkflowFileBounded(briefPath, 1*1024*1024)
	if err != nil {
		return "", ""
	}

	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "# Change brief —") || strings.HasPrefix(trimmed, "# Change Brief —") {
			title = strings.TrimPrefix(trimmed, "# Change brief —")
			title = strings.TrimPrefix(title, "# Change Brief —")
			title = strings.TrimSpace(title)
			break
		} else if strings.HasPrefix(trimmed, "# ") && title == "" {
			title = strings.TrimPrefix(trimmed, "# ")
			title = strings.TrimSpace(title)
		}
	}

	inSection := false
	var noteLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "## Background") || strings.HasPrefix(trimmed, "## Acceptance") {
			inSection = true
			continue
		}
		if inSection {
			if strings.HasPrefix(trimmed, "## ") {
				break
			}
			if trimmed != "" {
				noteLines = append(noteLines, trimmed)
				if len(noteLines) >= 3 {
					break
				}
			}
		}
	}

	if len(noteLines) > 0 {
		notes = strings.Join(noteLines, " ")
	}

	return title, notes
}
