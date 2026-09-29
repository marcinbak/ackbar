package daemon

import (
	"bufio"
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

var reBulletNumbered = regexp.MustCompile(`^\d+\.\s+`)

func readWorkflowFileBounded(path string, maxBytes int64) ([]byte, error) {
	fi, err := os.Stat(path)
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

// IngestDevWorkflowRuns scans ~/.claude/dev-workflow-runs/ and synchronizes run states to tasks
func (s *Server) IngestDevWorkflowRuns() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home dir: %w", err)
	}

	runsDir := filepath.Join(home, ".claude", "dev-workflow-runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read dev-workflow-runs dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(runsDir, entry.Name())
		if err := s.ingestSingleWorkflowRun(filePath); err != nil {
			log.Printf("[DevWorkflowWatcher] Error ingesting %s: %v", entry.Name(), err)
		}
	}

	// Run periodic deduplication pass to consolidate any newly orphaned planning tasks
	if n, err := s.db.DeduplicateTasks(); err == nil && n > 0 {
		log.Printf("[DevWorkflowWatcher] Deduplicated %d redundant task(s)", n)
	}

	return nil
}

func (s *Server) ingestSingleWorkflowRun(jsonPath string) error {
	data, err := readWorkflowFileBounded(jsonPath, 2*1024*1024)
	if err != nil {
		return err
	}

	var run DevWorkflowRun
	if err := json.Unmarshal(data, &run); err != nil {
		return fmt.Errorf("invalid json in %s: %w", jsonPath, err)
	}

	if run.Branch == "" && (run.Ticket == nil || *run.Ticket == "") {
		return nil
	}

	// Determine status and substatus based on workflow node
	status, substatus := mapWorkflowNodeToTaskStatus(run.Node)

	// Check for companion brief and retro files
	basePrefix := strings.TrimSuffix(jsonPath, ".json")
	briefPath := basePrefix + ".brief.md"
	retroPath := basePrefix + ".retro.md"
	briefTitle, briefNotes := extractBriefMetadata(briefPath)

	ticketStr := ""
	if run.Ticket != nil && *run.Ticket != "" {
		ticketStr = strings.TrimSpace(*run.Ticket)
	}

	// Resolve task by external reference ticket key, branch, worktree, or title
	var task *Task
	if ticketStr != "" {
		task, _ = s.db.GetTaskByExternalRef(ticketStr)
	}
	if task == nil && run.Branch != "" {
		task, _ = s.db.GetTaskByBranch(run.Branch)
	}
	if task == nil && run.Worktree != "" {
		task, _ = s.db.GetTaskByWorktree(run.Worktree)
	}
	if task == nil && briefTitle != "" {
		task, _ = s.db.GetTaskByTitle(briefTitle)
	}
	if task == nil && ticketStr != "" {
		task, _ = s.db.GetTaskByTitle(ticketStr)
	}

	taskUpdated := false

	if task == nil {
		// Create new task
		title := briefTitle
		if title == "" {
			title = formatTaskTitleFromBranch(run.Branch, ticketStr)
		}

		projectName := filepath.Base(run.Repo)
		if projectName == "" || projectName == "." {
			projectName = "General"
		} else if strings.Contains(strings.ToLower(projectName), "ackbar") {
			projectName = "Ackbar"
		}

		groupName := "Modemobile"
		repoLower := strings.ToLower(run.Repo)
		if strings.Contains(repoLower, "ackbar") || projectName == "Ackbar" {
			groupName = "Personal"
		} else if repoLower != "" && !strings.Contains(repoLower, "modemobile") && !strings.Contains(repoLower, "ngl") && !strings.Contains(repoLower, "mea") {
			groupName = "Personal"
		}

		notes := run.Notes
		if notes == "" {
			notes = briefNotes
		}

		task = &Task{
			ID:           fmt.Sprintf("task_%d", time.Now().UnixNano()),
			Title:        title,
			GroupName:    groupName,
			ProjectName:  projectName,
			Status:       status,
			Substatus:    substatus,
			Notes:        notes,
			Branch:       run.Branch,
			WorktreePath: run.Worktree,
		}

		if status == "DONE" {
			now := time.Now()
			task.CompletedAt = &now
		}

		if run.PR != nil && run.PR.URL != "" {
			task.PRURL = run.PR.URL
			task.PRNumber = run.PR.Number
			task.PRState = "OPEN"
		}

		if ticketStr != "" {
			task.ExternalRefs = []TaskExternalRef{
				{
					Tracker: "jira",
					RefKey:  ticketStr,
				},
			}
		}

		if err := s.db.CreateTask(task); err != nil {
			return fmt.Errorf("failed to create task from workflow run: %w", err)
		}
		taskUpdated = true
	} else {
		// Existing task: synchronize status and PR if run is further along
		if status == "DONE" {
			if task.CompletedAt == nil {
				now := time.Now()
				task.CompletedAt = &now
			}
			if task.Status != "DONE" {
				task.Status = status
				task.Substatus = substatus
				taskUpdated = true
			}
		} else if task.Status != "DONE" && status != "" {
			task.Status = status
			task.Substatus = substatus
			taskUpdated = true
		}

		if run.PR != nil && run.PR.URL != "" && task.PRURL != run.PR.URL {
			task.PRURL = run.PR.URL
			task.PRNumber = run.PR.Number
			task.PRState = "OPEN"
			if task.Status == "IN_PROGRESS" {
				task.Status = "REVIEW"
				task.Substatus = "in_review"
			}
			taskUpdated = true
		}

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

		if len(task.ExternalRefs) == 0 && ticketStr != "" {
			_ = s.db.InsertTaskExternalRef(&TaskExternalRef{
				TaskID:  task.ID,
				Tracker: "jira",
				RefKey:  ticketStr,
			})
			task.ExternalRefs = append(task.ExternalRefs, TaskExternalRef{
				TaskID:  task.ID,
				Tracker: "jira",
				RefKey:  ticketStr,
			})
		}

		if taskUpdated {
			_ = s.db.UpdateTask(task)
		}
	}

	// Attach companion deliverables
	if _, err := os.Stat(briefPath); err == nil {
		_ = s.db.InsertTaskDeliverable(&TaskDeliverable{
			TaskID:   task.ID,
			Kind:     "plan",
			Title:    "Change Brief",
			FilePath: briefPath,
		})
	}

	if _, err := os.Stat(retroPath); err == nil {
		_ = s.db.InsertTaskDeliverable(&TaskDeliverable{
			TaskID:   task.ID,
			Kind:     "retrospective",
			Title:    "Retrospective",
			FilePath: retroPath,
		})

		// Parse retro proposals into NEW discovered inbox tasks
		s.parseRetroProposalsIntoNewTasks(retroPath, task.GroupName, task.ProjectName)
	}

	for _, art := range run.Artifacts {
		if art != "" {
			_ = s.db.InsertTaskDeliverable(&TaskDeliverable{
				TaskID:   task.ID,
				Kind:     "mockup",
				Title:    filepath.Base(art),
				FilePath: art,
			})
		}
	}

	return nil
}

func mapWorkflowNodeToTaskStatus(node string) (string, string) {
	n := strings.ToUpper(strings.TrimSpace(node))
	switch n {
	case "N0", "N1":
		return "NEW", "ready"
	case "N2", "N3", "N4", "N5":
		return "IN_PROGRESS", "active"
	case "N6", "N7", "N8":
		return "REVIEW", "task_review"
	case "N9", "N10", "N11", "N12", "N13":
		return "REVIEW", "in_review"
	case "N14", "N15":
		return "DONE", "completed"
	default:
		if strings.HasPrefix(n, "N") {
			return "IN_PROGRESS", "active"
		}
		return "IN_PROGRESS", "active"
	}
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

	// Capture first paragraph under background or acceptance criteria as notes
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

func (s *Server) parseRetroProposalsIntoNewTasks(retroPath, groupName, projectName string) {
	data, err := readWorkflowFileBounded(retroPath, 1*1024*1024)
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inProposals := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineLower := strings.ToLower(line)

		if strings.HasPrefix(lineLower, "## proposal") || strings.HasPrefix(lineLower, "## future work") || strings.HasPrefix(lineLower, "### proposal") {
			inProposals = true
			continue
		}
		if inProposals {
			if strings.HasPrefix(line, "## ") {
				break
			}
			var bullet string
			if strings.HasPrefix(line, "- ") {
				bullet = strings.TrimPrefix(line, "- ")
			} else if strings.HasPrefix(line, "* ") {
				bullet = strings.TrimPrefix(line, "* ")
			} else if reBulletNumbered.MatchString(line) {
				bullet = reBulletNumbered.ReplaceAllString(line, "")
			}
			bullet = strings.TrimSpace(bullet)
			if len(bullet) > 5 {
				// Check if task with this title already exists in any status
				existingTasks, err := s.db.GetTasks()
				exists := false
				if err == nil {
					for _, et := range existingTasks {
						if strings.EqualFold(et.Title, bullet) {
							exists = true
							break
						}
					}
				}
				if !exists {
					newTask := &Task{
						ID:          fmt.Sprintf("task_%d", time.Now().UnixNano()),
						Title:       bullet,
						GroupName:   groupName,
						ProjectName: projectName,
						Status:      "NEW",
						Substatus:   "discovered",
						Notes:       fmt.Sprintf("Proposed in retrospective: %s", filepath.Base(retroPath)),
					}
					_ = s.db.CreateTask(newTask)
				}
			}
		}
	}
}
