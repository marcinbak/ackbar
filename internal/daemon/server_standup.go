package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var reGitHubPRURL = regexp.MustCompile(`^https://github\.com/([a-zA-Z0-9._-]+)/([a-zA-Z0-9._-]+)/pull/(\d+)/?$`)

func resolveGhBinary() string {
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	candidates := []string{
		"/opt/homebrew/bin/gh",
		"/usr/local/bin/gh",
		"/usr/bin/gh",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "gh"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "gh"
}

// handleStandup handles GET /v1/standup
func (s *Server) handleStandup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	group := strings.TrimSpace(r.URL.Query().Get("group"))
	if len(group) > 64 {
		group = group[:64]
	}
	group = strings.ReplaceAll(group, "\n", "")
	group = strings.ReplaceAll(group, "\r", "")

	days := 1
	if dStr := r.URL.Query().Get("days"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil && d > 0 {
			days = d
			if days > 60 {
				days = 60
			}
		}
	}

	report, err := s.BuildStandupReport(group, days)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate standup: %v", err), http.StatusInternalServerError)
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	accept := strings.ToLower(r.Header.Get("Accept"))

	// Return JSON if requested via format=json or Accept: application/json
	if format == "json" || strings.Contains(accept, "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
		return
	}

	// Default to Markdown for easy curl and terminal usage
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(report.Markdown))
}

// handleBriefingSynthesize handles POST /v1/briefings/synthesize
func (s *Server) handleBriefingSynthesize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req struct {
		Group string `json:"group"`
		Mode  string `json:"mode"`
		Days  int    `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		http.Error(w, fmt.Sprintf("Invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}

	days := req.Days
	if days <= 0 {
		days = 1
	} else if days > 60 {
		days = 60
	}

	group := strings.TrimSpace(req.Group)
	if len(group) > 64 {
		group = group[:64]
	}

	report, err := s.BuildStandupReport(group, days)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to synthesize briefing: %v", err), http.StatusInternalServerError)
		return
	}

	// Estimate spoken duration based on average speech rate of 130 words per minute
	words := len(strings.Fields(report.SpokenBriefing))
	durationEstimateSec := (words * 60) / 130
	if durationEstimateSec < 5 {
		durationEstimateSec = 5
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"group":                 report.Group,
		"date":                  report.Date,
		"days":                  report.Days,
		"spoken_text":           report.SpokenBriefing,
		"duration_estimate_sec": durationEstimateSec,
		"metrics": map[string]int{
			"shipped":     report.TotalShipped,
			"in_progress": report.TotalInProgress,
			"blocked":     report.TotalBlocked,
			"discovered":  report.TotalDiscovered,
		},
	})
}

// handleTaskMergePR handles POST /v1/tasks/merge-pr
func (s *Server) handleTaskMergePR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req struct {
		TaskID string `json:"task_id"`
		Method string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}

	req.TaskID = strings.TrimSpace(req.TaskID)
	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	method := strings.ToLower(strings.TrimSpace(req.Method))
	if method == "" {
		method = "squash"
	}
	if method != "squash" && method != "merge" && method != "rebase" {
		http.Error(w, "Invalid merge method; must be squash, merge, or rebase", http.StatusBadRequest)
		return
	}

	task, err := s.db.GetTaskByID(req.TaskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch task: %v", err), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, fmt.Sprintf("Task not found: %s", req.TaskID), http.StatusNotFound)
		return
	}

	if task.PRURL == "" {
		http.Error(w, "Task does not have an associated PR URL", http.StatusBadRequest)
		return
	}

	matches := reGitHubPRURL.FindStringSubmatch(task.PRURL)
	if len(matches) < 4 {
		http.Error(w, fmt.Sprintf("Task PR URL is not a valid GitHub PR URL: %s", task.PRURL), http.StatusBadRequest)
		return
	}
	cleanPRURL := fmt.Sprintf("https://github.com/%s/%s/pull/%s", matches[1], matches[2], matches[3])

	// Validate task lifecycle and merge preconditions
	if task.Status == "DONE" || strings.ToUpper(task.PRState) == "MERGED" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": "Task is already marked DONE or PR is already merged",
		})
		return
	}
	if task.Status != "REVIEW" && task.Substatus != "approved" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("Task is not ready for merge (status=%s, substatus=%s)", task.Status, task.Substatus),
		})
		return
	}

	// Execute gh pr merge with a 45-second timeout
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	ghBin := resolveGhBinary()
	cmd := exec.CommandContext(ctx, ghBin, "pr", "merge", cleanPRURL, "--"+method)
	if task.WorktreePath != "" {
		cleanPath := filepath.Clean(task.WorktreePath)
		if filepath.IsAbs(cleanPath) && !strings.Contains(cleanPath, "..") {
			if stat, err := os.Stat(cleanPath); err == nil && stat.IsDir() {
				cmd.Dir = cleanPath
			}
		}
	}

	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("gh pr merge failed: %s (%v)", outStr, err),
			"output":  outStr,
		})
		return
	}

	// Update task state upon successful merge
	task.Status = "DONE"
	task.Substatus = "completed"
	task.PRState = "MERGED"
	task.BlockerQuestion = ""
	now := time.Now()
	task.CompletedAt = &now

	if err := s.db.UpdateTask(task); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("PR was merged successfully, but task database update failed: %v", err),
			"output":  outStr,
		})
		return
	}

	// Broadcast update to all connected clients
	if len(task.Workers) > 0 {
		if sess, _ := s.db.GetSession(task.Workers[0].SessionID); sess != nil {
			s.broadcast(sess)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "success",
		"message": "Pull request merged successfully",
		"task":    task,
		"output":  outStr,
	})
}
