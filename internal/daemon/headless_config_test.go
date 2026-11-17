package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultHeadlessIdleTTL(t *testing.T) {
	if DefaultHeadlessIdleTTL != 65*time.Minute {
		t.Fatalf("expected DefaultHeadlessIdleTTL == 65m, got %v", DefaultHeadlessIdleTTL)
	}
	t.Setenv("ACKBAR_HEADLESS_IDLE_TTL", "")
	if got := ResolveHeadlessIdleTTL(0); got != 65*time.Minute {
		t.Fatalf("expected ResolveHeadlessIdleTTL(0) == 65m, got %v", got)
	}
	var nilRunner *HeadlessRunner
	if got := nilRunner.getIdleTTL(); got != 65*time.Minute {
		t.Fatalf("expected nil runner getIdleTTL() == 65m, got %v", got)
	}
}

func TestMCPFingerprint_IgnoresClaudeNonMCPChurnAndCaches(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "proj-a")
	otherCwd := filepath.Join(home, "proj-b")
	_ = os.MkdirAll(cwd, 0755)
	_ = os.MkdirAll(otherCwd, 0755)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0755)

	sess := &Session{ID: "claude-code:local:fp1", Agent: "claude-code", Cwd: cwd}
	claudeJSON := filepath.Join(home, ".claude.json")
	credJSON := filepath.Join(home, ".claude", ".credentials.json")
	authCacheJSON := filepath.Join(home, ".claude", "mcp-needs-auth-cache.json")

	baseClaude := `{
  "numStartups": 10,
  "tipsHistory": {"tip1": 1},
  "mcpServers": {"linear": {"url": "https://mcp.linear.app/sse"}},
  "disabledMcpServers": ["b", "a"],
  "projects": {
    "` + cwd + `": {"lastCost": 0.12, "mcpServers": {"proj-mcp": {"command": "node"}}},
    "` + otherCwd + `": {"mcpServers": {"other-mcp": {"command": "python"}}}
  }
}`
	_ = os.WriteFile(claudeJSON, []byte(baseClaude), 0644)
	_ = os.WriteFile(credJSON, []byte(`{"token":"v1"}`), 0600)
	_ = os.WriteFile(authCacheJSON, []byte(`{"linear":1}`), 0600)

	fp1 := MCPFingerprint(sess, nil, home)
	if fp1 == "" {
		t.Fatalf("expected non-empty MCPFingerprint")
	}

	// Non-MCP churn in .claude.json, credentials/auth-cache churn, other project churn, and disabledMcpServers order
	time.Sleep(5 * time.Millisecond)
	churnClaude := `{
  "numStartups": 999,
  "tipsHistory": {"tip1": 42, "newTip": 9},
  "mcpServers": {"linear": {"url": "https://mcp.linear.app/sse"}},
  "disabledMcpServers": ["a", "b"],
  "projects": {
    "` + cwd + `": {"lastCost": 99.99, "allowedTools": ["Bash"], "mcpServers": {"proj-mcp": {"command": "node"}}},
    "` + otherCwd + `": {"mcpServers": {"other-mcp": {"command": "changed"}}}
  }
}`
	_ = os.WriteFile(claudeJSON, []byte(churnClaude), 0644)
	_ = os.WriteFile(credJSON, []byte(`{"token":"v2-refreshed"}`), 0600)
	_ = os.WriteFile(authCacheJSON, []byte(`{"linear":999,"github":1}`), 0600)

	fp2 := MCPFingerprint(sess, nil, home)
	if fp2 != fp1 {
		t.Fatalf("expected MCPFingerprint to ignore non-MCP churn and auth cache files: %s != %s", fp1, fp2)
	}

	// Project-scoped MCP change for sess.Cwd must change fingerprint
	projChanged := `{
  "numStartups": 999,
  "mcpServers": {"linear": {"url": "https://mcp.linear.app/sse"}},
  "disabledMcpServers": ["a", "b"],
  "projects": {
    "` + cwd + `": {"mcpServers": {"proj-mcp": {"command": "deno"}}}
  }
}`
	_ = os.WriteFile(claudeJSON, []byte(projChanged), 0644)
	fp3 := MCPFingerprint(sess, nil, home)
	if fp3 == fp1 {
		t.Fatalf("expected project-scoped mcpServers change to update MCPFingerprint")
	}

	// Top-level mcpServers change must change fingerprint
	topChanged := `{
  "mcpServers": {"linear": {"url": "https://mcp.linear.app/sse"}, "sentry": {"url": "https://mcp.sentry.dev"}},
  "disabledMcpServers": ["a", "b"],
  "projects": {
    "` + cwd + `": {"mcpServers": {"proj-mcp": {"command": "deno"}}}
  }
}`
	_ = os.WriteFile(claudeJSON, []byte(topChanged), 0644)
	fp4 := MCPFingerprint(sess, nil, home)
	if fp4 == fp3 {
		t.Fatalf("expected top-level mcpServers change to update MCPFingerprint")
	}

	// Invalid JSON in .claude.json -> treated as empty canonical MCP config without flapping
	_ = os.WriteFile(claudeJSON, []byte(`{"mcpServers":`), 0644)
	fpCorrupt := MCPFingerprint(sess, nil, home)
	_ = os.WriteFile(claudeJSON, []byte(`{"numStartups": 1}`), 0644)
	fpEmpty := MCPFingerprint(sess, nil, home)
	if fpCorrupt != fpEmpty {
		t.Fatalf("expected invalid .claude.json to match canonical empty MCP digest: %s != %s", fpCorrupt, fpEmpty)
	}

	// Pure MCP file (.mcp.json) with invalid JSON hashes raw bytes (mtime touch without byte change stays stable)
	mcpJSON := filepath.Join(cwd, ".mcp.json")
	_ = os.WriteFile(mcpJSON, []byte(`{invalid-v1`), 0644)
	fpRaw1 := MCPFingerprint(sess, nil, home)
	future := time.Now().Add(10 * time.Second)
	_ = os.Chtimes(mcpJSON, future, future)
	fpRawSameBytes := MCPFingerprint(sess, nil, home)
	if fpRawSameBytes != fpRaw1 {
		t.Fatalf("expected invalid .mcp.json mtime-only touch not to flap fingerprint: %s != %s", fpRaw1, fpRawSameBytes)
	}
	_ = os.WriteFile(mcpJSON, []byte(`{invalid-v2`), 0644)
	fpRaw2 := MCPFingerprint(sess, nil, home)
	if fpRaw2 == fpRaw1 {
		t.Fatalf("expected invalid .mcp.json byte change to update fingerprint")
	}
}
