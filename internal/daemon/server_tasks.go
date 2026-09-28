package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
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
		var t Task
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

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
		}

		w.Header().Set("Content-Type", "application/json")
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

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
