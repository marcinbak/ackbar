package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"ackbar/internal/version"
)

// handleAgentsStatus handles GET /v1/agents/status
func (s *Server) handleAgentsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	statuses, err := GetAgentStatuses(s.homeDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query agent statuses: %v", err), http.StatusInternalServerError)
		return
	}

	fetchRemote := r.URL.Query().Get("fetch") == "true"
	skillsRepoStatus := GetSkillsRepoStatus(s.homeDir, s.db, fetchRemote)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "success",
		"version":     version.Version,
		"agents":      statuses,
		"skills_repo": skillsRepoStatus,
	})
}

// handleAgentsProvision handles POST /v1/agents/provision
func (s *Server) handleAgentsProvision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req struct {
		Agents         []string `json:"agents"`
		SkillsRepoPath string   `json:"skills_repo_path"`
		SkillsRepoURL  string   `json:"skills_repo_url"`
		CloneIfMissing bool     `json:"clone_if_missing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, fmt.Sprintf("Invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}

	if len(req.Agents) == 0 {
		req.Agents = []string{"all"}
	}

	updated, err := ProvisionAgents(s.homeDir, req.Agents)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("Failed to provision agents: %v", err),
		})
		return
	}

	var skillsRepoStatus *SkillsRepoStatus
	currentRepoStatus := GetSkillsRepoStatus(s.homeDir, s.db, false)
	if currentRepoStatus.Configured || req.SkillsRepoPath != "" || req.SkillsRepoURL != "" {
		syncOpts := SyncSkillsRepoOptions{
			RepoPath:       req.SkillsRepoPath,
			RepoURL:        req.SkillsRepoURL,
			CloneIfMissing: req.CloneIfMissing,
		}
		resStatus, _ := SyncSkillsRepo(s.homeDir, s.db, syncOpts)
		if resStatus != nil {
			skillsRepoStatus = resStatus
		} else {
			skillsRepoStatus = &currentRepoStatus
		}
	} else if currentRepoStatus.RepoPath != "" || currentRepoStatus.RepoURL != "" {
		skillsRepoStatus = &currentRepoStatus
	}

	respMap := map[string]any{
		"status":  "success",
		"message": "Connected agents configured successfully with Ackbar MCP server and Skill",
		"version": version.Version,
		"agents":  updated,
	}
	if skillsRepoStatus != nil {
		respMap["skills_repo"] = skillsRepoStatus
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(respMap)
}

// handleSkillsRepoSync handles POST /v1/agents/skills-repo/sync
func (s *Server) handleSkillsRepoSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req struct {
		RepoPath       string `json:"repo_path"`
		RepoURL        string `json:"repo_url"`
		CloneIfMissing bool   `json:"clone_if_missing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, fmt.Sprintf("Invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}

	opts := SyncSkillsRepoOptions{
		RepoPath:       req.RepoPath,
		RepoURL:        req.RepoURL,
		CloneIfMissing: req.CloneIfMissing,
	}

	syncStatus, err := SyncSkillsRepo(s.homeDir, s.db, opts)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "error",
			"message":     fmt.Sprintf("Failed to sync skills repository: %v", err),
			"skills_repo": syncStatus,
		})
		return
	}

	agents, _ := GetAgentStatuses(s.homeDir)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "success",
		"message":     "Skills repository synced successfully",
		"skills_repo": syncStatus,
		"agents":      agents,
	})
}
