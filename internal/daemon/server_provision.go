package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"ackbar/internal/version"
)

func checkSameOriginRequest(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil // Non-browser / CLI direct requests allowed
	}

	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("invalid Origin header: %w", err)
	}

	origHost := strings.ToLower(u.Hostname())
	if origHost == "127.0.0.1" || origHost == "localhost" || origHost == "::1" {
		return nil
	}

	reqHost := strings.ToLower(extractHostname(r.Host))
	if reqHost != "" && origHost == reqHost {
		return nil
	}

	return fmt.Errorf("cross-origin requests are forbidden from %s", origin)
}

// handleAgentsStatus handles GET /v1/agents/status
func (s *Server) handleAgentsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	statuses, err := GetAgentStatusesWithRegistry(s.homeDir, s.PluginRegistry())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query agent statuses: %v", err), http.StatusInternalServerError)
		return
	}

	fetchRemote := r.URL.Query().Get("fetch") == "true"
	skillsRepoStatus := GetSkillsRepoStatusWithRegistry(s.homeDir, s.db, s.PluginRegistry(), fetchRemote)

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
	if err := checkSameOriginRequest(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
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

	updated, err := ProvisionAgentsWithRegistry(s.homeDir, req.Agents, s.PluginRegistry())
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
	currentRepoStatus := GetSkillsRepoStatusWithRegistry(s.homeDir, s.db, s.PluginRegistry(), false)
	if currentRepoStatus.Configured || req.SkillsRepoPath != "" || req.SkillsRepoURL != "" {
		syncOpts := SyncSkillsRepoOptions{
			RepoPath:       req.SkillsRepoPath,
			RepoURL:        req.SkillsRepoURL,
			CloneIfMissing: req.CloneIfMissing,
		}
		resStatus, _ := SyncSkillsRepoWithRegistry(s.homeDir, s.db, s.PluginRegistry(), syncOpts)
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
	if err := checkSameOriginRequest(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
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

	syncStatus, err := SyncSkillsRepoWithRegistry(s.homeDir, s.db, s.PluginRegistry(), opts)
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

	agents, _ := GetAgentStatusesWithRegistry(s.homeDir, s.PluginRegistry())

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "success",
		"message":     "Skills repository synced successfully",
		"skills_repo": syncStatus,
		"agents":      agents,
	})
}
