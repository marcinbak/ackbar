package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
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
			if err := s.db.UpdateTask(&t); err != nil {
				http.Error(w, "Failed to update task: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			if fresh, err := s.db.GetTaskByID(t.ID); err == nil && fresh != nil {
				t = *fresh
			}
		}

		json.NewEncoder(w).Encode(t)
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

	groupName := strings.TrimSpace(p.GroupName)
	if groupName == "" {
		groupName = "Modemobile"
	}
	projectName := strings.TrimSpace(p.ProjectName)
	if projectName == "" {
		projectName = "General"
	}

	var notesList []string
	if p.Type != "" {
		notesList = append(notesList, fmt.Sprintf("Type: %s", p.Type))
	}
	if p.Severity != "" {
		notesList = append(notesList, fmt.Sprintf("Severity: %s", p.Severity))
	}
	if p.FileReference != "" {
		notesList = append(notesList, fmt.Sprintf("Ref: %s", p.FileReference))
	}
	if p.Rationale != "" {
		notesList = append(notesList, p.Rationale)
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
	if p.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
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
		Kind:     p.Kind,
		Title:    p.Title,
		Host:     hostName,
		FilePath: p.FilePath,
		URL:      p.URL,
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
