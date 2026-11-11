package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerMCPStatus_DiscoveryAndNoSecrets(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	cwd := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}

	mcpJSON := `{
		"mcpServers": {
			"project-http": {
				"type": "http",
				"url": "https://mcp.internal/api",
				"env": {"SECRET_KEY": "supersecret"},
				"headers": {"Authorization": "Bearer 12345"}
			},
			"project-stdio": {
				"command": "node",
				"args": ["server.js", "--secret=foo"]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(cwd, ".mcp.json"), []byte(mcpJSON), 0644); err != nil {
		t.Fatal(err)
	}

	claudeJSON := `{
		"mcpServers": {
			"user-sse": {
				"type": "sse",
				"url": "https://sse.internal/sse"
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claudeJSON), 0644); err != nil {
		t.Fatal(err)
	}

	sess := &Session{
		ID:        "claude-code:local:mcp-test",
		Agent:     "claude-code",
		Cwd:       cwd,
		StartedAt: time.Now(),
	}

	servers, err := discoverConfiguredMCPServers(sess, nil, home)
	if err != nil {
		t.Fatalf("discoverConfiguredMCPServers failed: %v", err)
	}

	if len(servers) < 3 {
		t.Fatalf("expected at least 3 servers, got %d: %+v", len(servers), servers)
	}

	raw, err := json.Marshal(servers)
	if err != nil {
		t.Fatal(err)
	}
	rawStr := string(raw)

	forbidden := []string{"supersecret", "SECRET_KEY", "Authorization", "Bearer 12345", "--secret=foo"}
	for _, f := range forbidden {
		if strings.Contains(rawStr, f) {
			t.Errorf("secret %q leaked in MCP status response: %s", f, rawStr)
		}
	}

	var foundHTTP, foundSSE, foundStdio bool
	for _, s := range servers {
		if s.Name == "project-http" {
			foundHTTP = true
			if s.Transport != "http" || !s.AuthSupported || s.Target != "mcp:project-http" {
				t.Errorf("unexpected project-http health: %+v", s)
			}
		}
		if s.Name == "user-sse" {
			foundSSE = true
			if s.Transport != "sse" || !s.AuthSupported {
				t.Errorf("unexpected user-sse health: %+v", s)
			}
		}
		if s.Name == "project-stdio" {
			foundStdio = true
			if s.Transport != "stdio" || s.AuthSupported {
				t.Errorf("unexpected project-stdio health: %+v", s)
			}
		}
	}
	if !foundHTTP || !foundSSE || !foundStdio {
		t.Errorf("missing expected servers: http=%v, sse=%v, stdio=%v", foundHTTP, foundSSE, foundStdio)
	}
}

func TestServerMCPStatus_EndpointAndCache(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(db)

	sess := &Session{
		ID:        "claude-code:local:cache-test",
		Agent:     "claude-code",
		Host:      s.HostName(),
		StartedAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatal(err)
	}

	req1 := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/mcp-status", nil)
	w1 := httptest.NewRecorder()
	s.handleSessionMCPStatus(w1, req1, sess.ID)

	if w1.Code != http.StatusOK {
		t.Fatalf("mcp-status failed (%d): %s", w1.Code, w1.Body.String())
	}

	var resp1 SessionMCPStatusResponse
	if err := json.Unmarshal(w1.Body.Bytes(), &resp1); err != nil {
		t.Fatal(err)
	}

	// Verify caching
	mcpStatusCacheMu.RLock()
	entry, found := mcpStatusCache[sess.ID]
	mcpStatusCacheMu.RUnlock()
	if !found || entry == nil {
		t.Fatalf("expected cached entry for %s", sess.ID)
	}

	// Invalidate
	s.invalidateMCPStatusCache(sess.ID)
	mcpStatusCacheMu.RLock()
	_, foundAfter := mcpStatusCache[sess.ID]
	mcpStatusCacheMu.RUnlock()
	if foundAfter {
		t.Fatalf("cache entry not invalidated")
	}
}

func TestServerMCPStatus_ProbeRefreshTokenAndNoNetworkFallback(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	tool := "gslides"

	launcherDir := filepath.Join(home, ".gemini", "config", "plugins", tool, "skills", tool, "scripts")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		t.Fatal(err)
	}
	launcherPath := filepath.Join(launcherDir, tool)
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	tokenDir := filepath.Join(home, ".gemini", "antigravity", "plugin_data", tool)
	if err := os.MkdirAll(tokenDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tokenDir, "token.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	healthNoBin := probeSinglePlugin(tool, home, nil, nil)
	if healthNoBin.Authenticated {
		t.Errorf("expected Authenticated: false when compiledBin is missing")
	}
	if !healthNoBin.Installed {
		t.Errorf("expected Installed: true when launcherScript exists")
	}

	binDir := filepath.Join(home, ".gemini", "antigravity", "plugin_data", tool, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, tool+"-linux-amd64")
	mockScript := `#!/bin/sh
echo '{"authenticated": true, "is_expired": true, "has_refresh_token": true}'
exit 0
`
	if err := os.WriteFile(binPath, []byte(mockScript), 0755); err != nil {
		t.Fatal(err)
	}

	healthWithRefresh := probeSinglePlugin(tool, home, nil, nil)
	if !healthWithRefresh.Authenticated {
		t.Errorf("expected Authenticated: true when has_refresh_token: true, got %+v", healthWithRefresh)
	}
}
