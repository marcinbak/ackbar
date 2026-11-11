package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type MCPServerHealth struct {
	Name          string `json:"name"`
	Transport     string `json:"transport"`
	Scope         string `json:"scope"`
	Status        string `json:"status"`
	AuthSupported bool   `json:"auth_supported"`
	Target        string `json:"target"`
}

type WorkspacePluginHealth struct {
	Name          string `json:"name"`
	Installed     bool   `json:"installed"`
	Authenticated bool   `json:"authenticated"`
	AuthSupported bool   `json:"auth_supported"`
	Target        string `json:"target"`
}

type SessionMCPStatusResponse struct {
	Servers []MCPServerHealth       `json:"servers"`
	Plugins []WorkspacePluginHealth `json:"plugins"`
}

type mcpStatusCacheEntry struct {
	cachedAt time.Time
	data     *SessionMCPStatusResponse
}

var (
	mcpStatusCacheMu sync.RWMutex
	mcpStatusCache   = make(map[string]*mcpStatusCacheEntry)
)

func (s *Server) invalidateMCPStatusCache(sessionID string) {
	mcpStatusCacheMu.Lock()
	delete(mcpStatusCache, sessionID)
	mcpStatusCacheMu.Unlock()
}

func (s *Server) handleSessionMCPStatus(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess, proxied := s.resolveSessionOrProxy(w, http.MethodGet, sessionID, "", nil, func(_ *Session) string {
		return r.URL.RequestURI()
	})
	if proxied {
		return
	}
	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	bypassCache := r.URL.Query().Get("refresh") == "1"
	if !bypassCache {
		mcpStatusCacheMu.RLock()
		entry, found := mcpStatusCache[sessionID]
		if found && entry != nil && time.Since(entry.cachedAt) < 10*time.Second {
			data := entry.data
			mcpStatusCacheMu.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(data)
			return
		}
		mcpStatusCacheMu.RUnlock()
	}

	home, _ := os.UserHomeDir()
	servers, _ := discoverConfiguredMCPServers(sess, s.db, home)
	plugins := probeWorkspacePlugins(home, sess, s.db)

	resp := &SessionMCPStatusResponse{
		Servers: servers,
		Plugins: plugins,
	}

	mcpStatusCacheMu.Lock()
	mcpStatusCache[sessionID] = &mcpStatusCacheEntry{
		cachedAt: time.Now(),
		data:     resp,
	}
	mcpStatusCacheMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type rawMCPFileConfig struct {
	MCPServers          map[string]rawMCPServer `json:"mcpServers"`
	Projects            map[string]rawProject   `json:"projects"`
	DisabledMCPServers  []string                `json:"disabledMcpServers"`
	DisabledMCPServers2 []string                `json:"disabled_mcp_servers"`
}

type rawProject struct {
	MCPServers map[string]rawMCPServer `json:"mcpServers"`
}

type rawMCPServer struct {
	Type      string `json:"type"`
	Command   string `json:"command"`
	URL       string `json:"url"`
	ServerURL string `json:"serverUrl"`
	Disabled  bool   `json:"disabled"`
}

func discoverConfiguredMCPServers(sess *Session, db *DB, home string) ([]MCPServerHealth, error) {
	var results []MCPServerHealth
	seen := make(map[string]bool)

	addServer := func(name string, raw rawMCPServer, scope string) {
		name = strings.TrimSpace(name)
		if name == "" || raw.Disabled || seen[name] {
			return
		}
		seen[name] = true
		transport := "stdio"
		checkURL := raw.URL
		if checkURL == "" {
			checkURL = raw.ServerURL
		}
		if checkURL != "" {
			if strings.Contains(strings.ToLower(checkURL), "/sse") {
				transport = "sse"
			} else {
				transport = "http"
			}
		} else if strings.EqualFold(raw.Type, "sse") {
			transport = "sse"
		} else if strings.EqualFold(raw.Type, "http") {
			transport = "http"
		}

		status := "configured"
		if sess != nil && sess.Blocked != nil && sess.Blocked.Kind == BlockAuth && sess.Blocked.AuthTarget == "mcp:"+name {
			status = "needs_auth"
		}
		authSupported := sess != nil && sess.Agent == "claude-code" && (transport == "http" || transport == "sse")

		results = append(results, MCPServerHealth{
			Name:          name,
			Transport:     transport,
			Scope:         scope,
			Status:        status,
			AuthSupported: authSupported,
			Target:        "mcp:" + name,
		})
	}

	loadConfigFile := func(path, defaultScope string, isClaude bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		var cfg rawMCPFileConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return
		}
		disabled := make(map[string]bool)
		for _, d := range cfg.DisabledMCPServers {
			disabled[d] = true
		}
		for _, d := range cfg.DisabledMCPServers2 {
			disabled[d] = true
		}
		if sess != nil && sess.Cwd != "" && cfg.Projects != nil {
			if p, ok := cfg.Projects[sess.Cwd]; ok && p.MCPServers != nil {
				for name, srv := range p.MCPServers {
					if !disabled[name] {
						addServer(name, srv, "project")
					}
				}
			}
		}
		for name, srv := range cfg.MCPServers {
			if !disabled[name] {
				addServer(name, srv, defaultScope)
			}
		}
	}

	if sess != nil && sess.Cwd != "" {
		loadConfigFile(filepath.Join(sess.Cwd, ".mcp.json"), "project", false)
	}

	var claudeConfigDir string
	if sess != nil && sess.AccountID != "" && db != nil {
		if acc, _ := db.GetAccount(sess.AccountID); acc != nil && acc.ConfigDir != "" {
			claudeConfigDir = acc.ConfigDir
			if strings.HasPrefix(claudeConfigDir, "~/") && home != "" {
				claudeConfigDir = filepath.Join(home, claudeConfigDir[2:])
			}
		}
	}
	if claudeConfigDir != "" {
		loadConfigFile(filepath.Join(claudeConfigDir, ".claude.json"), "profile", true)
	}
	if home != "" {
		loadConfigFile(filepath.Join(home, ".claude.json"), "user", true)
		loadConfigFile(filepath.Join(home, ".claude", "mcp_config.json"), "user", false)
		loadConfigFile(filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json"), "user", false)
		loadConfigFile(filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"), "user", false)
		loadConfigFile(filepath.Join(home, ".gemini", "config", "mcp_config.json"), "user", false)
		loadConfigFile(filepath.Join(home, ".codex", "config.json"), "user", false)
	}

	return results, nil
}

func probeWorkspacePlugins(home string, sess *Session, db *DB) []WorkspacePluginHealth {
	var wg sync.WaitGroup
	plugins := []string{"gslides", "gdocs", "gdrive", "gsheets"}
	results := make([]WorkspacePluginHealth, len(plugins))

	for i, tool := range plugins {
		wg.Add(1)
		go func(idx int, toolName string) {
			defer wg.Done()
			results[idx] = probeSinglePlugin(toolName, home, sess, db)
		}(i, tool)
	}
	wg.Wait()
	return results
}

func probeSinglePlugin(tool, home string, sess *Session, db *DB) WorkspacePluginHealth {
	launcherScript := filepath.Join(home, ".gemini", "config", "plugins", tool, "skills", tool, "scripts", tool)
	if _, err := os.Stat(launcherScript); err != nil {
		return WorkspacePluginHealth{
			Name:          tool,
			Installed:     false,
			Authenticated: false,
			AuthSupported: true,
			Target:        "plugin:" + tool,
		}
	}

	compiledBin := findCompiledPluginBinary(tool, home)
	if compiledBin == "" {
		tokenPath := filepath.Join(home, ".gemini", "antigravity", "plugin_data", tool, "token.json")
		if _, err := os.Stat(tokenPath); err != nil {
			return WorkspacePluginHealth{
				Name:          tool,
				Installed:     true,
				Authenticated: false,
				AuthSupported: true,
				Target:        "plugin:" + tool,
			}
		}
		compiledBin = launcherScript
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, compiledBin, "auth", "status", "--json")
	cmd.Env = append(buildWorkerEnv(sess, db, home), "DISPLAY=")

	out, err := cmd.Output()
	if err != nil {
		return WorkspacePluginHealth{
			Name:          tool,
			Installed:     true,
			Authenticated: false,
			AuthSupported: true,
			Target:        "plugin:" + tool,
		}
	}

	var status struct {
		Authenticated   bool `json:"authenticated"`
		IsExpired       bool `json:"is_expired"`
		HasRefreshToken bool `json:"has_refresh_token"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return WorkspacePluginHealth{
			Name:          tool,
			Installed:     true,
			Authenticated: false,
			AuthSupported: true,
			Target:        "plugin:" + tool,
		}
	}

	authenticated := status.Authenticated && !status.IsExpired
	return WorkspacePluginHealth{
		Name:          tool,
		Installed:     true,
		Authenticated: authenticated,
		AuthSupported: true,
		Target:        "plugin:" + tool,
	}
}

func findCompiledPluginBinary(tool, home string) string {
	candidates := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "plugin_data", tool, "bin", tool+"-*"),
		filepath.Join(home, ".gemini", "antigravity", "plugin_data", tool, "bin", tool+"-*"),
	}
	for _, pattern := range candidates {
		matches, err := filepath.Glob(pattern)
		if err == nil {
			for _, m := range matches {
				if info, err := os.Stat(m); err == nil && !info.IsDir() {
					return m
				}
			}
		}
	}
	return ""
}
