package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"ackbar/internal/tmux"
)

type AgentDiscoveryResult struct {
	Agent          string `json:"agent"`
	DisplayName    string `json:"display_name,omitempty"`
	Installed      bool   `json:"installed"`
	HookConfigured bool   `json:"hook_configured"`
	SetupCmd       string `json:"setup_cmd"`
}

func (s *Server) handleAgentDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var results []AgentDiscoveryResult
	for name, p := range s.providers {
		installed := p.IsInstalled()
		configured, setupCmd, _ := p.CheckHookConfig()

		results = append(results, AgentDiscoveryResult{
			Agent:          name,
			DisplayName:    p.DisplayName(),
			Installed:      installed,
			HookConfigured: configured,
			SetupCmd:       setupCmd,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Agent == "claude-code" {
			return true
		}
		if results[j].Agent == "claude-code" {
			return false
		}
		return results[i].Agent < results[j].Agent
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var results []ProviderDTO
	for _, p := range s.providers {
		configured, setupCmd, _ := p.CheckHookConfig()
		results = append(results, ProviderDTO{
			Agent:          p.Agent(),
			DisplayName:    p.DisplayName(),
			BrandColor:     p.BrandColor(),
			IconSVG:        p.IconSVG(),
			IsInstalled:    p.IsInstalled(),
			HookConfigured: configured,
			SetupCmd:       setupCmd,
			ProcessNames:   p.ProcessNames(),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Agent == "claude-code" {
			return true
		}
		if results[j].Agent == "claude-code" {
			return false
		}
		return results[i].Agent < results[j].Agent
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

// resolveAccountEnv resolves the account record and environment variables for a session or spawn request.
func (s *Server) resolveAccountEnv(agent, accountID, home string, fallbackToDefault bool) (string, map[string]string) {
	if s.db == nil {
		return accountID, nil
	}
	adapter := s.agentAdapter(agent)
	if accountID != "" && accountID != "default" {
		acc, err := s.db.GetAccount(accountID)
		if (err != nil || acc == nil) && !strings.Contains(accountID, ":") {
			acc, _ = s.db.GetAccount(fmt.Sprintf("%s:%s", agent, accountID))
		}
		if acc != nil {
			return accountID, adapter.AccountEnvVars(home, acc)
		}
	} else if accountID == "" && fallbackToDefault {
		if accList, err := s.db.ListAccounts(agent); err == nil {
			for _, acc := range accList {
				if acc.IsDefault && acc.Name != "default" {
					return acc.ID, adapter.AccountEnvVars(home, acc)
				}
			}
		}
	}
	return accountID, nil
}

func (s *Server) checkAccountLoggedIn(a *AgentAccount) bool {
	if a == nil {
		return false
	}
	home, _ := os.UserHomeDir()
	return s.agentAdapter(a.Agent).IsAccountLoggedIn(home, a)
}

func setupProfileDirectory(agent, name, configDir string) (string, error) {
	home, _ := os.UserHomeDir()
	return getDefaultAgentAdapter(agent).SetupProfileDir(home, name, configDir)
}

func (s *Server) propagateAccountToFleet(acc *AgentAccount) {
	allHosts, err := s.db.ListHosts()
	if err != nil || len(allHosts) == 0 {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	for _, h := range allHosts {
		if h.Name == "local" || h.Name == s.HostName() || h.URL == "" {
			continue
		}

		discURL := fmt.Sprintf("%s/v1/agents/discovery", strings.TrimSuffix(h.URL, "/"))
		resp, err := client.Get(discURL)
		if err != nil {
			continue
		}
		var discResults []AgentDiscoveryResult
		_ = json.NewDecoder(resp.Body).Decode(&discResults)
		resp.Body.Close()

		agentInstalled := false
		for _, d := range discResults {
			if d.Agent == acc.Agent && d.Installed {
				agentInstalled = true
				break
			}
		}

		if !agentInstalled {
			continue
		}

		postURL := fmt.Sprintf("%s/v1/accounts", strings.TrimSuffix(h.URL, "/"))
		payload, _ := json.Marshal(map[string]interface{}{
			"agent":         acc.Agent,
			"name":          acc.Name,
			"display_name":  acc.DisplayName,
			"config_dir":    acc.ConfigDir,
			"env":           acc.Env,
			"is_default":    acc.IsDefault,
			"propagate_all": false,
		})
		pResp, err := client.Post(postURL, "application/json", bytes.NewBuffer(payload))
		if err == nil {
			pResp.Body.Close()
		}
	}
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agentFilter := r.URL.Query().Get("agent")
		accounts, err := s.db.ListAccounts(agentFilter)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to list accounts: %v", err), http.StatusInternalServerError)
			return
		}

		// Ensure default account exists for agents
		agentsToCheck := []string{"claude-code", "antigravity", "codex"}
		if agentFilter != "" {
			agentsToCheck = []string{agentFilter}
		}
		for _, ag := range agentsToCheck {
			hasDefault := false
			for _, a := range accounts {
				if a.Agent == ag && a.Name == "default" {
					hasDefault = true
					break
				}
			}
			if !hasDefault {
				hasAnyDefault := false
				for _, a := range accounts {
					if a.Agent == ag && a.IsDefault {
						hasAnyDefault = true
						break
					}
				}
				accounts = append(accounts, &AgentAccount{
					ID:          ag + ":default",
					Agent:       ag,
					Name:        "default",
					DisplayName: "Default",
					IsDefault:   !hasAnyDefault,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				})
			}
		}

		for _, a := range accounts {
			a.IsLoggedIn = s.checkAccountLoggedIn(a)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(accounts)

	case http.MethodPost:
		var req struct {
			Agent        string            `json:"agent"`
			Name         string            `json:"name"`
			DisplayName  string            `json:"display_name"`
			ConfigDir    string            `json:"config_dir"`
			Env          map[string]string `json:"env"`
			IsDefault    bool              `json:"is_default"`
			PropagateAll bool              `json:"propagate_all"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		if req.Agent == "" || req.Name == "" {
			http.Error(w, "Missing agent or name", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(strings.ToLower(req.Name))
		validName := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
		if !validName.MatchString(req.Name) {
			http.Error(w, "Invalid account name (must start with alphanumeric and contain only [a-z0-9_-])", http.StatusBadRequest)
			return
		}

		configDir := req.ConfigDir
		if req.Name != "default" {
			var err error
			configDir, err = setupProfileDirectory(req.Agent, req.Name, req.ConfigDir)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to setup profile directory: %v", err), http.StatusInternalServerError)
				return
			}
		}

		displayName := req.DisplayName
		if displayName == "" {
			displayName = strings.Title(req.Name)
		}

		acc := &AgentAccount{
			ID:          fmt.Sprintf("%s:%s", req.Agent, req.Name),
			Agent:       req.Agent,
			Name:        req.Name,
			DisplayName: displayName,
			ConfigDir:   configDir,
			Env:         req.Env,
			IsDefault:   req.IsDefault,
		}

		if err := s.db.SaveAccount(acc); err != nil {
			http.Error(w, fmt.Sprintf("Failed to save account: %v", err), http.StatusInternalServerError)
			return
		}

		acc.IsLoggedIn = s.checkAccountLoggedIn(acc)

		if req.PropagateAll {
			go s.propagateAccountToFleet(acc)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(acc)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAccountOps(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/v1/accounts/")
	parts := strings.Split(subPath, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Missing account id", http.StatusBadRequest)
		return
	}
	id := parts[0]

	if len(parts) == 1 {
		if r.Method == http.MethodDelete {
			if strings.HasSuffix(id, ":default") {
				http.Error(w, "Cannot delete default account", http.StatusBadRequest)
				return
			}
			if err := s.db.DeleteAccount(id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
			return
		} else if r.Method == http.MethodGet {
			acc, err := s.db.GetAccount(id)
			if err != nil || acc == nil {
				http.Error(w, "Account not found", http.StatusNotFound)
				return
			}
			acc.IsLoggedIn = s.checkAccountLoggedIn(acc)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(acc)
			return
		}
	} else if len(parts) == 2 {
		action := parts[1]
		if action == "default" && r.Method == http.MethodPost {
			acc, err := s.db.GetAccount(id)
			if err != nil || acc == nil {
				http.Error(w, "Account not found", http.StatusNotFound)
				return
			}
			if err := s.db.SetDefaultAccount(acc.Agent, id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "default_updated"})
			return
		} else if action == "login" && r.Method == http.MethodPost {
			acc, err := s.db.GetAccount(id)
			if err != nil || acc == nil {
				http.Error(w, "Account not found", http.StatusNotFound)
				return
			}
			home, _ := os.UserHomeDir()
			adapter := s.agentAdapter(acc.Agent)
			envVars := adapter.AccountEnvVars(home, acc)
			delete(envVars, "ACKBAR_ACCOUNT")
			tmuxName := fmt.Sprintf("ackbar-login-%s-%s", acc.Agent, acc.Name)
			launchCmd := adapter.GetLoginCommand()
			if launchCmd == "" {
				launchCmd = "claude login"
			}
			if err := tmux.SpawnWithEnv(r.Context(), tmuxName, "", launchCmd, envVars); err != nil {
				http.Error(w, fmt.Sprintf("Failed to spawn login session: %v", err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":    "login_spawned",
				"tmux_name": tmuxName,
			})
			return
		}
	}

	http.Error(w, "Not found or method not allowed", http.StatusNotFound)
}
