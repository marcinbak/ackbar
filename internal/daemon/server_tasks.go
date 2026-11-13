package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var checkGitHubPRState = func(ctx context.Context, prURL string) (string, error) {
	prURL = strings.TrimSpace(prURL)
	if prURL == "" || strings.HasPrefix(prURL, "-") || !prURLRegex.MatchString(prURL) {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", prURL, "--json", "state")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var res struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return "", err
	}
	return strings.ToUpper(strings.TrimSpace(res.State)), nil
}

func (s *Server) reconcileOpenPRTasks(ctx context.Context, tasks []Task) {
	rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	for i := range tasks {
		t := &tasks[i]
		if (t.Status == "IN_PROGRESS" || t.Status == "REVIEW") && t.PRURL != "" && t.PRState != "MERGED" && t.PRState != "CLOSED" {
			state, err := checkGitHubPRState(rctx, t.PRURL)
			if err != nil {
				continue
			}
			state = strings.ToUpper(strings.TrimSpace(state))
			if state == "MERGED" {
				t.PRState = "MERGED"
				t.Status = "DONE"
				t.Substatus = "completed"
				now := time.Now().UTC()
				t.CompletedAt = &now
				s.completeAndCloseTaskSession(t, nil)
				_ = s.db.UpdateTask(t)
				s.pushSyncPayload(&SyncStatePayload{Tasks: []Task{*t}, Workstreams: t.Workstreams})
			} else if state == "CLOSED" {
				t.PRState = "CLOSED"
				_ = s.db.UpdateTask(t)
			}
		}
	}
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if r.URL.Query().Get("federated") == "true" {
			s.PullFromPeersWithTimeout(r.Context(), 1500*time.Millisecond)
		}
		tasks, err := s.db.GetTasks()
		if err != nil {
			http.Error(w, "Failed to get tasks: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if r.URL.Query().Get("reconcile_prs") == "true" {
			s.reconcileOpenPRTasks(r.Context(), tasks)
		}

		// Filter by query parameters if present
		groupFilter := strings.TrimSpace(r.URL.Query().Get("group"))
		projFilter := strings.TrimSpace(r.URL.Query().Get("project"))
		statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
		kindFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))

		if groupFilter != "" || projFilter != "" || statusFilter != "" || kindFilter != "" {
			var filtered []Task
			for _, task := range tasks {
				if groupFilter != "" && !strings.EqualFold(task.GroupName, groupFilter) {
					continue
				}
				if projFilter != "" && !strings.EqualFold(task.ProjectName, projFilter) {
					continue
				}
				if statusFilter != "" && !strings.EqualFold(task.Status, statusFilter) {
					continue
				}
				if kindFilter != "" && !strings.EqualFold(task.TaskKind, kindFilter) {
					continue
				}
				filtered = append(filtered, task)
			}
			tasks = filtered
		}

		// Lazy deliverable content_md handling: unless ?include_content=true, omit inline content_md
		includeContent := r.URL.Query().Get("include_content") == "true"
		for i := range tasks {
			for j := range tasks[i].Deliverables {
				tasks[i].Deliverables[j].HasContentMD = strings.TrimSpace(tasks[i].Deliverables[j].ContentMD) != ""
				if !includeContent {
					tasks[i].Deliverables[j].ContentMD = ""
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tasks)
		return
	}

	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1MB limit
		var req struct {
			Task
			WorkflowNode         string   `json:"workflow_node,omitempty"`
			RepoNodePath         string   `json:"repo_node_path,omitempty"`
			TargetHost           string   `json:"target_host,omitempty"`
			RequiredCapabilities []string `json:"required_capabilities,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		t := req.Task
		w.Header().Set("Content-Type", "application/json")
		sessionID := ""
		for _, w := range t.Workers {
			if w.SessionID != "" {
				sessionID = w.SessionID
				break
			}
		}

		var existing *Task
		if t.ID != "" {
			existing, _ = s.db.GetTaskByID(t.ID)
		}

		if t.ID != "" && existing == nil && strings.TrimSpace(t.Title) == "" {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}

		if t.ID == "" || existing == nil {
			if t.ID == "" {
				t.ID = fmt.Sprintf("task_%d", time.Now().UnixNano())
			}
			t.GroupName, t.ProjectName = s.normalizeTaskNodeScope(t.GroupName, t.ProjectName, t.WorktreePath, sessionID)
			t.Status, t.Substatus = normalizeTaskStatusAndSubstatus(t.Status, t.Substatus, t.Substatus != "")
			if strings.EqualFold(t.Status, "DONE") && t.CompletedAt == nil {
				now := time.Now().UTC()
				t.CompletedAt = &now
			}
			if t.TaskKind == "" {
				t.TaskKind = "code"
			}
			for i := range t.Workstreams {
				ws := &t.Workstreams[i]
				targetH := strings.TrimSpace(ws.TargetHost)
				if targetH == "" {
					targetH = strings.TrimSpace(req.TargetHost)
				}
				if targetH == "" || strings.EqualFold(targetH, "auto") {
					dec := s.SelectTargetHost(r.Context(), HostRoutingRequest{
						TargetHost:           "auto",
						RequiredCapabilities: req.RequiredCapabilities,
						RepoNodePath:         ws.RepoNodePath,
						TaskKind:             t.TaskKind,
					})
					ws.TargetHost = dec.SelectedHost
				}
			}
			if req.WorkflowNode != "" {
				canonicalNode := normalizeWorkflowNode(req.WorkflowNode)
				sessState := StateIdle
				for _, w := range t.Workers {
					if w.IsActive {
						if sSess, _ := s.db.GetSession(w.SessionID); sSess != nil && sSess.State == StateWorking {
							sessState = StateWorking
							break
						}
					}
				}
				t.Status, t.Substatus = ClassifyWorkflowNodeState(canonicalNode, sessState, false)
				for i := range t.Workstreams {
					ws := &t.Workstreams[i]
					if req.RepoNodePath == "" || len(t.Workstreams) == 1 || strings.EqualFold(ws.RepoNodePath, req.RepoNodePath) {
						ws.WorkflowNode = canonicalNode
						ws.Status = t.Status
						ws.UpdatedAt = time.Now().UTC()
					}
				}
				if canonicalNode == "N14" || canonicalNode == "N15" {
					now := time.Now().UTC()
					t.CompletedAt = &now
				}
			}
			if err := s.db.CreateTask(&t); err != nil {
				http.Error(w, "Failed to create task: "+err.Error(), http.StatusInternalServerError)
				return
			}
			s.maybeNotifyTaskReview("", &t)
			s.maybeNotifyTaskBlocked("", "", &t)
			targetRepoPath := req.RepoNodePath
			if targetRepoPath == "" && len(t.Workstreams) > 0 {
				for _, ws := range t.Workstreams {
					if ws.Status == "IN_PROGRESS" {
						targetRepoPath = ws.RepoNodePath
						break
					}
				}
				if targetRepoPath == "" {
					targetRepoPath = t.Workstreams[0].RepoNodePath
				}
			}
			canonicalReqNode := normalizeWorkflowNode(req.WorkflowNode)
			if canonicalReqNode == "N14" || canonicalReqNode == "N15" {
				s.unlockSequentialWorkstreams(&t, targetRepoPath)
			}
			if canonicalReqNode == "N15" {
				s.completeAndCloseTaskSession(&t, nil)
			}
			s.pushSyncPayload(&SyncStatePayload{Tasks: []Task{t}, Workstreams: t.Workstreams})
			w.WriteHeader(http.StatusCreated)
		} else {
			oldStatus := existing.Status
			oldSubstatus := existing.Substatus
			oldQuestion := existing.BlockerQuestion

			// Merge fields from t into existing if provided
			if t.Title != "" {
				existing.Title = t.Title
			}
			if t.GroupName != "" || t.ProjectName != "" {
				gn := t.GroupName
				if gn == "" {
					gn = existing.GroupName
				}
				pn := t.ProjectName
				if pn == "" {
					pn = existing.ProjectName
				}
				g, p := s.normalizeTaskNodeScope(gn, pn, t.WorktreePath, sessionID)
				existing.GroupName = g
				existing.ProjectName = p
			}
			if t.SubprojectName != "" {
				existing.SubprojectName = t.SubprojectName
			}
			if t.TaskKind != "" {
				existing.TaskKind = t.TaskKind
			}
			statusChanged := false
			explicitSubstatus := false
			if t.Status != "" {
				statusChanged = !strings.EqualFold(oldStatus, t.Status)
				existing.Status = t.Status
			}
			if t.Substatus != "" {
				explicitSubstatus = t.Substatus != "" && (!statusChanged || !strings.EqualFold(t.Substatus, oldSubstatus))
				existing.Substatus = t.Substatus
				if t.Substatus == "active" {
					existing.BlockerQuestion = ""
				}
			}
			if t.Status != "" {
				existing.Status, existing.Substatus = normalizeTaskStatusAndSubstatus(existing.Status, existing.Substatus, explicitSubstatus)
				if strings.EqualFold(existing.Status, "DONE") {
					existing.Status = "DONE"
					existing.BlockerQuestion = ""
					if existing.CompletedAt == nil {
						now := time.Now().UTC()
						existing.CompletedAt = &now
					}
					for i := range existing.Workers {
						existing.Workers[i].IsActive = false
					}
				} else if strings.EqualFold(oldStatus, "DONE") {
					existing.CompletedAt = nil
				}
				if req.WorkflowNode == "" {
					reopeningFromDone := strings.EqualFold(oldStatus, "DONE") && !strings.EqualFold(existing.Status, "DONE")
					for i := range existing.Workstreams {
						ws := &existing.Workstreams[i]
						if (req.RepoNodePath == "" && len(existing.Workstreams) == 1) ||
							(req.RepoNodePath != "" && strings.EqualFold(ws.RepoNodePath, req.RepoNodePath)) ||
							existing.Status == "DONE" ||
							(reopeningFromDone && strings.EqualFold(ws.Status, "DONE")) {
							ws.Status = existing.Status
							ws.UpdatedAt = time.Now().UTC()
						}
					}
				}
			}
			if t.Notes != "" {
				existing.Notes = t.Notes
			}
			if t.BlockerQuestion != "" {
				existing.BlockerQuestion = t.BlockerQuestion
			}
			if t.Branch != "" {
				existing.Branch = t.Branch
			}
			if t.WorktreePath != "" {
				existing.WorktreePath = t.WorktreePath
			}
			if t.PRURL != "" {
				if t.PRURL == "-" {
					existing.PRURL = ""
					existing.PRNumber = 0
					existing.PRState = ""
				} else {
					existing.PRURL = t.PRURL
				}
			}
			if t.PRNumber != 0 {
				existing.PRNumber = t.PRNumber
			}
			if t.PRState != "" {
				existing.PRState = t.PRState
			}
			if t.CIStatus != "" {
				existing.CIStatus = t.CIStatus
			}
			if !strings.EqualFold(existing.Status, "DONE") {
				existing.CompletedAt = nil
			} else if t.CompletedAt != nil {
				existing.CompletedAt = t.CompletedAt
			}
			if t.Workers != nil {
				existing.Workers = t.Workers
			}
			if t.Workstreams != nil {
				existing.Workstreams = t.Workstreams
			}
			if t.ExternalRefs != nil {
				existing.ExternalRefs = t.ExternalRefs
			}
			if t.Deliverables != nil {
				existing.Deliverables = t.Deliverables
			}

			if req.WorkflowNode != "" {
				canonicalNode := normalizeWorkflowNode(req.WorkflowNode)
				targetRepoPath := req.RepoNodePath
				if targetRepoPath == "" && len(existing.Workstreams) > 0 {
					for _, ws := range existing.Workstreams {
						if ws.Status == "IN_PROGRESS" {
							targetRepoPath = ws.RepoNodePath
							break
						}
					}
					if targetRepoPath == "" {
						targetRepoPath = existing.Workstreams[0].RepoNodePath
					}
				}

				for i := range existing.Workers {
					w := &existing.Workers[i]
					matchesWorker := (sessionID != "" && w.SessionID == sessionID) || (sessionID == "" && w.IsActive)
					if matchesWorker && (targetRepoPath == "" || w.RepoNodePath == "" || strings.EqualFold(w.RepoNodePath, targetRepoPath)) {
						if w.WorkflowNode == "" || shouldAdvanceWorkflowNode(w.WorkflowNode, canonicalNode) {
							w.WorkflowNode = canonicalNode
						}
					}
				}
				sessState := StateIdle
				for _, w := range existing.Workers {
					if w.IsActive {
						if sSess, _ := s.db.GetSession(w.SessionID); sSess != nil {
							if sSess.State == StateWorking {
								sessState = StateWorking
								break
							} else if sSess.State == StateIdle && (sSess.Activity == "" || sSess.Activity == "Session started") {
								sSess.Activity = fmt.Sprintf("Awaiting user prompt (%s)", canonicalNode)
								_ = s.db.SaveSession(sSess)
							}
						}
					}
				}
				newStatus, newSubstatus := ClassifyWorkflowNodeState(canonicalNode, sessState, existing.Substatus == "blocked" && existing.BlockerQuestion != "")
				if newStatus == "NEW" && existing.Status != "" && !strings.EqualFold(existing.Status, "NEW") {
					newStatus = existing.Status
					if existing.Substatus != "" && existing.Substatus != "ready" && existing.Substatus != "queued" {
						newSubstatus = existing.Substatus
					} else {
						newSubstatus = "active"
					}
				}
				if (strings.EqualFold(existing.Status, "REVIEW") || existing.PRURL != "" || existing.PRNumber > 0) && (newStatus == "IN_PROGRESS" || newStatus == "NEW") {
					newStatus = "REVIEW"
					if existing.Substatus == "pr_review" || existing.Substatus == "in_review" || existing.Substatus == "plan_review" {
						newSubstatus = existing.Substatus
					} else {
						newSubstatus = "pr_review"
					}
				}
				existing.Status = newStatus
				existing.Substatus = newSubstatus
				for i := range existing.Workstreams {
					ws := &existing.Workstreams[i]
					if targetRepoPath == "" || strings.EqualFold(ws.RepoNodePath, targetRepoPath) {
						if ws.WorkflowNode == "" || shouldAdvanceWorkflowNode(ws.WorkflowNode, canonicalNode) {
							ws.WorkflowNode = canonicalNode
						}
						wsStatus := existing.Status
						if wsStatus == "NEW" && ws.Status != "" && !strings.EqualFold(ws.Status, "NEW") && !strings.EqualFold(ws.Status, "QUEUED") {
							wsStatus = ws.Status
						}
						ws.Status = wsStatus
						ws.UpdatedAt = time.Now().UTC()
					}
				}
				if existing.Status == "DONE" && existing.CompletedAt == nil {
					now := time.Now().UTC()
					existing.CompletedAt = &now
				}
				if canonicalNode == "N14" || canonicalNode == "N15" {
					s.unlockSequentialWorkstreams(existing, targetRepoPath)
				}
				if canonicalNode == "N15" {
					s.completeAndCloseTaskSession(existing, nil)
				}
			}

			if err := s.db.UpdateTask(existing); err != nil {
				http.Error(w, "Failed to update task: "+err.Error(), http.StatusInternalServerError)
				return
			}
			s.maybeNotifyTaskReview(oldStatus, existing)
			s.maybeNotifyTaskBlocked(oldSubstatus, oldQuestion, existing)
			s.pushSyncPayload(&SyncStatePayload{Tasks: []Task{*existing}, Workstreams: existing.Workstreams})
			w.WriteHeader(http.StatusOK)
			t = *existing
		}

		json.NewEncoder(w).Encode(t)
		return
	}

	if r.Method == http.MethodDelete {
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		taskID := strings.TrimSpace(r.URL.Query().Get("id"))
		if taskID == "" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			taskID = strings.TrimSpace(body.ID)
		}
		if taskID == "" || len(taskID) > 128 {
			http.Error(w, "Valid task ID is required", http.StatusBadRequest)
			return
		}

		if err := s.db.DeleteTask(taskID); err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, "Task not found", http.StatusNotFound)
				return
			}
			log.Printf("error: failed to delete task %s: %v", taskID, err)
			http.Error(w, "Failed to delete task", http.StatusInternalServerError)
			return
		}

		now := time.Now().UTC()
		s.pushSyncPayload(&SyncStatePayload{Tasks: []Task{{ID: taskID, UpdatedAt: now, DeletedAt: &now}}})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "success",
			"message": "Task deleted successfully",
			"id":      taskID,
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

type TaskEventPayload struct {
	TaskID    string `json:"task_id"`
	EventType string `json:"event_type"` // "log", "status", etc.
	Payload   string `json:"payload"`
}

var prRegex = regexp.MustCompile(`(https://github\.com/[^/]+/[^/]+/pull/(\d+))`)
var branchRegex = regexp.MustCompile(`branch ['"]?([\w\-/\.]+)['"]?`)

func (s *Server) handleTaskEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1MB limit
	var event TaskEventPayload
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if event.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	task, err := s.db.GetTaskByID(event.TaskID)
	if err != nil {
		http.Error(w, "Internal Error", http.StatusInternalServerError)
		return
	}

	if task == nil {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	oldStatus := task.Status
	updated := false

	// Ambient ingestion: detect branches
	if m := branchRegex.FindStringSubmatch(event.Payload); len(m) > 1 {
		task.Branch = m[1]
		updated = true
	}

	// Ambient ingestion: detect PRs
	if m := prRegex.FindStringSubmatch(event.Payload); len(m) > 2 {
		task.PRURL = m[1]
		if prNum, err := strconv.Atoi(m[2]); err == nil {
			task.PRNumber = prNum
		}
		task.PRState = "OPEN" // assumption for new PRs
		if task.Status == "IN_PROGRESS" {
			task.Status = "REVIEW"
			task.Substatus = "in_review"
		}
		updated = true
	}

	if updated {
		if err := s.db.UpdateTask(task); err != nil {
			http.Error(w, "Failed to update task", http.StatusInternalServerError)
			return
		}
		s.maybeNotifyTaskReview(oldStatus, task)
		s.pushSyncPayload(&SyncStatePayload{Tasks: []Task{*task}})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

type TaskProposePayload struct {
	Title         string `json:"title"`
	Type          string `json:"type"`     // bug | opportunity | tech_debt
	Severity      string `json:"severity"` // low | medium | high
	Rationale     string `json:"rationale"`
	FileReference string `json:"file_reference"`
	GroupName     string `json:"group_name"`
	ProjectName   string `json:"project_name"`
}

func (s *Server) handleTaskPropose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1MB limit
	var p TaskProposePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(p.Title)
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if len(title) > 256 {
		title = title[:256]
	}

	propType := strings.ToLower(strings.TrimSpace(p.Type))
	if propType != "" && propType != "bug" && propType != "opportunity" && propType != "tech_debt" {
		http.Error(w, "invalid type: must be bug, opportunity, or tech_debt", http.StatusBadRequest)
		return
	}

	severity := strings.ToLower(strings.TrimSpace(p.Severity))
	if severity != "" && severity != "low" && severity != "medium" && severity != "high" {
		http.Error(w, "invalid severity: must be low, medium, or high", http.StatusBadRequest)
		return
	}

	rationale := strings.TrimSpace(p.Rationale)
	if len(rationale) > 4096 {
		rationale = rationale[:4096]
	}
	fileRef := strings.TrimSpace(p.FileReference)
	if len(fileRef) > 1024 {
		fileRef = fileRef[:1024]
	}

	groupName, projectName := s.normalizeTaskNodeScope(p.GroupName, p.ProjectName, "", "")

	// Deduplicate proposed tasks: if an active/new task with identical title already exists for this project, return it
	existingTasks, err := s.db.GetTasks()
	if err != nil {
		log.Printf("warn: failed to fetch tasks for deduplication: %v", err)
	} else {
		for _, et := range existingTasks {
			if strings.EqualFold(strings.TrimSpace(et.Title), title) &&
				strings.EqualFold(strings.TrimSpace(et.ProjectName), projectName) &&
				(et.Status == "NEW" || et.Status == "IN_PROGRESS") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(et)
				return
			}
		}
	}

	var notesList []string
	if propType != "" {
		notesList = append(notesList, fmt.Sprintf("Type: %s", propType))
	}
	if severity != "" {
		notesList = append(notesList, fmt.Sprintf("Severity: %s", severity))
	}
	if fileRef != "" {
		notesList = append(notesList, fmt.Sprintf("Ref: %s", fileRef))
	}
	if rationale != "" {
		notesList = append(notesList, rationale)
	}

	task := &Task{
		ID:          fmt.Sprintf("task_%d", time.Now().UnixNano()),
		Title:       title,
		GroupName:   groupName,
		ProjectName: projectName,
		Status:      "NEW",
		Substatus:   "discovered",
		Notes:       strings.Join(notesList, " | "),
	}

	if err := s.db.CreateTask(task); err != nil {
		http.Error(w, "Failed to create proposed task: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.pushSyncPayload(&SyncStatePayload{Tasks: []Task{*task}})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(task)
}

type TaskDeliverablePayload struct {
	TaskID    string `json:"task_id"`
	Kind      string `json:"kind"` // mockup | plan | retrospective | doc
	Title     string `json:"title"`
	Host      string `json:"host"`
	FilePath  string `json:"file_path"`
	URL       string `json:"url"`
	Provider  string `json:"provider,omitempty"`
	ContentMD string `json:"content_md,omitempty"`
}

func (s *Server) handleTaskDeliverable(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		delID := strings.TrimSpace(r.URL.Query().Get("id"))
		if delID == "" {
			http.Error(w, "deliverable id is required", http.StatusBadRequest)
			return
		}
		del, err := s.db.GetTaskDeliverableByID(delID)
		if err != nil {
			http.Error(w, "Failed to get deliverable: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if del == nil {
			http.Error(w, "Deliverable not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(del)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1MB limit
	var p TaskDeliverablePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if p.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	filePath := strings.TrimSpace(p.FilePath)
	urlStr := strings.TrimSpace(p.URL)
	contentMD := strings.TrimSpace(p.ContentMD)

	if filePath == "" && urlStr == "" && contentMD == "" {
		http.Error(w, "either file_path, url, or content_md is required", http.StatusBadRequest)
		return
	}

	if filePath != "" {
		clean := filepath.Clean(filePath)
		if strings.Contains(clean, "..") {
			http.Error(w, "path traversal detected in file_path", http.StatusBadRequest)
			return
		}
		filePath = clean
	}

	if urlStr != "" {
		lowerURL := strings.ToLower(urlStr)
		if !strings.HasPrefix(lowerURL, "http://") && !strings.HasPrefix(lowerURL, "https://") {
			http.Error(w, "url must begin with http:// or https://", http.StatusBadRequest)
			return
		}
	}

	kind := strings.ToLower(strings.TrimSpace(p.Kind))
	if kind == "" {
		kind = "doc"
	}
	validKinds := map[string]bool{
		"prd":           true,
		"html_ui":       true,
		"slides":        true,
		"plan":          true,
		"learnings":     true,
		"pr":            true,
		"retro":         true,
		"retrospective": true,
		"video":         true,
		"screenshot":    true,
		"mockup":        true,
		"doc":           true,
		"other":         true,
	}
	if !validKinds[kind] {
		http.Error(w, "invalid kind: must be one of prd, html_ui, slides, plan, learnings, pr, retro, retrospective, video, screenshot, mockup, doc, other", http.StatusBadRequest)
		return
	}

	provider := strings.TrimSpace(p.Provider)
	if provider == "" {
		if strings.Contains(urlStr, "docs.google.com/document") {
			provider = "gdocs"
		} else if strings.Contains(urlStr, "docs.google.com/presentation") {
			provider = "gslides"
		} else if strings.Contains(urlStr, "docs.google.com/spreadsheets") {
			provider = "gsheets"
		} else if strings.Contains(urlStr, "drive.google.com") {
			provider = "gdrive"
		} else if strings.Contains(urlStr, "claude.ai") || strings.Contains(urlStr, "claude.site") {
			provider = "claude_artifact"
		} else if strings.Contains(urlStr, "github.com") {
			provider = "github"
		} else if filePath != "" {
			provider = "local"
		}
	}

	task, err := s.db.GetTaskByID(p.TaskID)
	if err != nil {
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	hostName := p.Host
	if hostName == "" {
		hostName = s.HostName()
	}

	del := &TaskDeliverable{
		TaskID:    p.TaskID,
		Kind:      kind,
		Title:     title,
		Host:      hostName,
		FilePath:  filePath,
		URL:       urlStr,
		Provider:  provider,
		ContentMD: p.ContentMD,
	}

	if err := s.db.InsertTaskDeliverable(del); err != nil {
		http.Error(w, "Failed to insert deliverable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.pushSyncPayload(&SyncStatePayload{Deliverables: []TaskDeliverable{*del}})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(del)
}

func (s *Server) handleTaskSyncWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) normalizeTaskNodeScope(groupName, projectName, cwd, sessionID string) (normGroup, normProject string) {
	groupName = strings.TrimSpace(groupName)
	projectName = strings.TrimSpace(projectName)
	cwd = strings.TrimSpace(cwd)
	sessionID = strings.TrimSpace(sessionID)

	// 1. If sessionID != "" and groupName == "" / projectName == "", look up session
	if sessionID != "" && (groupName == "" || projectName == "") {
		if sess, err := s.db.GetSession(sessionID); err == nil && sess != nil {
			if sess.NodePath != "" && !strings.EqualFold(sess.NodePath, "unassigned") {
				parts := strings.Split(sess.NodePath, "/")
				if len(parts) > 1 {
					if groupName == "" {
						groupName = parts[0]
					}
					if projectName == "" {
						projectName = strings.Join(parts[1:], "/")
					}
				} else if len(parts) == 1 && projectName == "" {
					projectName = parts[0]
				}
			}
			if cwd == "" && sess.Cwd != "" {
				cwd = sess.Cwd
			}
		}
	}

	// 2. If projectName is already a full tree_nodes path (e.g. "Modemobile/NGL/ngl-ios" or "Personal/Ackbar"),
	// split on first / so GroupName = "Modemobile", ProjectName = "NGL/ngl-ios"
	// (or if GroupName is already "Modemobile" and ProjectName starts with "Modemobile/", strip duplicate prefix!).
	if groupName != "" && strings.HasPrefix(strings.ToLower(projectName), strings.ToLower(groupName)+"/") {
		projectName = projectName[len(groupName)+1:]
	}

	if strings.Contains(projectName, "/") {
		parts := strings.Split(projectName, "/")
		nodes, _ := s.db.ListNodes()
		isKnownGroup := false
		for _, n := range nodes {
			nP := strings.Split(n.Path, "/")
			if len(nP) > 0 && strings.EqualFold(nP[0], parts[0]) {
				isKnownGroup = true
				break
			}
		}
		if isKnownGroup || strings.EqualFold(parts[0], "Modemobile") || strings.EqualFold(parts[0], "Personal") {
			if groupName == "" {
				groupName = parts[0]
			}
			projectName = strings.Join(parts[1:], "/")
		}
	}

	// 3. Match against registered tree_nodes
	nodes, _ := s.db.ListNodes()
	matchNode := func(query string) (string, string, bool) {
		if query == "" {
			return "", "", false
		}
		qLower := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(query, ".git"), "/"))
		// Match exact node path
		for _, n := range nodes {
			if strings.EqualFold(n.Path, query) {
				parts := strings.Split(n.Path, "/")
				if len(parts) > 1 {
					return parts[0], strings.Join(parts[1:], "/"), true
				}
				return "Personal", parts[0], true
			}
		}
		// Match GitURL, ProjectDir, or Path slug
		for _, n := range nodes {
			gitLower := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(n.GitURL, ".git"), "/"))
			projLower := strings.ToLower(strings.TrimSuffix(n.ProjectDir, "/"))
			pathParts := strings.Split(n.Path, "/")
			slug := ""
			if len(pathParts) > 0 {
				slug = strings.ToLower(pathParts[len(pathParts)-1])
			}

			if (gitLower != "" && strings.HasSuffix(gitLower, qLower)) ||
				(projLower != "" && strings.HasSuffix(projLower, qLower)) ||
				(slug != "" && slug == qLower) {
				if len(pathParts) > 1 {
					return pathParts[0], strings.Join(pathParts[1:], "/"), true
				}
				return "Personal", pathParts[0], true
			}
		}

		// Match node_host_paths
		if s.db != nil {
			hostPaths, err := s.db.ListNodeHostPaths("")
			if err == nil {
				for _, hp := range hostPaths {
					pDir := strings.ToLower(strings.TrimSuffix(hp.ProjectDir, "/"))
					gitR := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(hp.GitRemote, ".git"), "/"))
					if (pDir != "" && strings.HasSuffix(pDir, qLower)) || (gitR != "" && strings.HasSuffix(gitR, qLower)) {
						parts := strings.Split(hp.NodePath, "/")
						if len(parts) > 1 {
							return parts[0], strings.Join(parts[1:], "/"), true
						}
						return "Personal", parts[0], true
					}
				}
			}
		}

		return "", "", false
	}

	if g, p, ok := matchNode(projectName); ok {
		if groupName == "" {
			groupName = g
		}
		projectName = p
	} else if cwd != "" {
		if g, p, ok := matchNode(cwd); ok {
			if groupName == "" {
				groupName = g
			}
			if projectName == "" || projectName == "General" {
				projectName = p
			}
		}
	}

	if projectName == "" {
		projectName = "General"
	}

	return groupName, projectName
}

func (s *Server) handleTaskDeduplicate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	deleted, err := s.db.DeduplicateTasks()
	if err != nil {
		http.Error(w, "Failed to deduplicate tasks: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"deleted": deleted,
		"status":  "success",
	})
}
