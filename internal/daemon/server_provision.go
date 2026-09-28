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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "success",
		"version": version.Version,
		"agents":  statuses,
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
		Agents []string `json:"agents"`
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "success",
		"message": "Connected agents configured successfully with Ackbar MCP server and Skill",
		"version": version.Version,
		"agents":  updated,
	})
}
