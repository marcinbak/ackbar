package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		tasks, err := s.db.GetTasks()
		if err != nil {
			http.Error(w, "Failed to get tasks: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Filter by query parameters if present
		groupFilter := strings.TrimSpace(r.URL.Query().Get("group"))
		projFilter := strings.TrimSpace(r.URL.Query().Get("project"))
		statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

		if groupFilter != "" || projFilter != "" || statusFilter != "" {
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
				filtered = append(filtered, task)
			}
			tasks = filtered
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tasks)
		return
	}

	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1MB limit
		var t Task
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if t.ID == "" {
			t.ID = fmt.Sprintf("task_%d", time.Now().UnixNano())
			if err := s.db.CreateTask(&t); err != nil {
				http.Error(w, "Failed to create task: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		} else {
			existing, err := s.db.GetTaskByID(t.ID)
			if err != nil {
				http.Error(w, "Failed to query existing task: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if existing == nil {
				http.Error(w, "Task not found", http.StatusNotFound)
				return
			}

			// Merge fields from t into existing if provided
			if t.Title != "" {
				existing.Title = t.Title
			}
			if t.GroupName != "" {
				existing.GroupName = t.GroupName
			}
			if t.ProjectName != "" {
				existing.ProjectName = t.ProjectName
			}
			if t.SubprojectName != "" {
				existing.SubprojectName = t.SubprojectName
			}
			if t.Status != "" {
				existing.Status = t.Status
			}
			if t.Substatus != "" {
				existing.Substatus = t.Substatus
				if t.Substatus == "active" {
					existing.BlockerQuestion = ""
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
			if t.CompletedAt != nil {
				existing.CompletedAt = t.CompletedAt
			}
			if t.Workers != nil {
				existing.Workers = t.Workers
			}
			if t.ExternalRefs != nil {
				existing.ExternalRefs = t.ExternalRefs
			}
			if t.Deliverables != nil {
				existing.Deliverables = t.Deliverables
			}

			if err := s.db.UpdateTask(existing); err != nil {
				http.Error(w, "Failed to update task: "+err.Error(), http.StatusInternalServerError)
				return
			}
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

	projectName := strings.TrimSpace(p.ProjectName)
	if projectName == "" {
		projectName = "General"
	}

	groupName := strings.TrimSpace(p.GroupName)
	if groupName == "" {
		if strings.EqualFold(projectName, "Ackbar") {
			groupName = "Personal"
		} else {
			nodes, err := s.db.ListNodes()
			if err != nil {
				log.Printf("warn: failed to list tree nodes for group inference: %v", err)
			} else {
				for _, n := range nodes {
					parts := strings.Split(n.Path, "/")
					if len(parts) > 1 && strings.EqualFold(parts[len(parts)-1], projectName) {
						groupName = parts[0]
						break
					}
				}
			}
		}
		if groupName == "" {
			groupName = "Personal"
		}
	}

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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(task)
}

type TaskDeliverablePayload struct {
	TaskID   string `json:"task_id"`
	Kind     string `json:"kind"` // mockup | plan | retrospective | doc
	Title    string `json:"title"`
	Host     string `json:"host"`
	FilePath string `json:"file_path"`
	URL      string `json:"url"`
}

func (s *Server) handleTaskDeliverable(w http.ResponseWriter, r *http.Request) {
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

	if filePath == "" && urlStr == "" {
		http.Error(w, "either file_path or url is required", http.StatusBadRequest)
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
	if kind != "mockup" && kind != "plan" && kind != "retrospective" && kind != "doc" {
		http.Error(w, "invalid kind: must be mockup, plan, retrospective, or doc", http.StatusBadRequest)
		return
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
		TaskID:   p.TaskID,
		Kind:     kind,
		Title:    title,
		Host:     hostName,
		FilePath: filePath,
		URL:      urlStr,
	}

	if err := s.db.InsertTaskDeliverable(del); err != nil {
		http.Error(w, "Failed to insert deliverable: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(del)
}

func (s *Server) handleTaskSyncWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := s.IngestDevWorkflowRuns(); err != nil {
		http.Error(w, "Failed to sync workflow runs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
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
