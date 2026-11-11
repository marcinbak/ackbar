package daemon

import (
	"testing"
)

func TestDetectToolResultAuthBlock_Denylist(t *testing.T) {
	deniedTools := []string{
		"read", "read_file", "grep", "grep_search", "glob", "find_by_name",
		"write", "write_file", "edit", "edit_file", "multiedit", "notebookedit",
		"ls", "list_dir", "todowrite", "taskoutput",
	}
	output := "no credentials found: run 'gslides auth login'"
	for _, tool := range deniedTools {
		b := DetectToolResultAuthBlock("antigravity", tool, nil, output, true)
		if b != nil {
			t.Errorf("tool %q should be denied by denylist, got non-nil: %+v", tool, b)
		}
	}
}

func TestDetectToolResultAuthBlock_ExitCodeHeaders(t *testing.T) {
	outWithZeroExit := "Command exited with code 0.\nno credentials found: run 'gslides auth login'"
	b := DetectToolResultAuthBlock("antigravity", "run_command", nil, outWithZeroExit, false)
	if b != nil {
		t.Errorf("expected nil on code 0 header, got: %+v", b)
	}

	outWithNonZeroExit := "Command exited with code 1.\nno credentials found: run 'gslides auth login'"
	b2 := DetectToolResultAuthBlock("antigravity", "run_command", nil, outWithNonZeroExit, false)
	if b2 == nil {
		t.Fatalf("expected BlockAuth on non-zero exit code header, got nil")
	}
	if b2.Kind != BlockAuth || b2.AuthServer != "gslides" {
		t.Errorf("unexpected block: %+v", b2)
	}

	b3 := DetectToolResultAuthBlock("antigravity", "run_command", nil, "hello", false)
	if b3 != nil {
		t.Errorf("expected nil when no error, got: %+v", b3)
	}
}

func TestDetectToolResultAuthBlock_Scope403Exclusion(t *testing.T) {
	out403 := "Error 403: PERMISSION_DENIED: insufficient authentication scopes"
	b := DetectToolResultAuthBlock("antigravity", "run_command", nil, "Command exited with code 1.\n"+out403, true)
	if b != nil {
		t.Errorf("expected nil for 403 scope error without login prompt, got: %+v", b)
	}

	out403WithLogin := "Error 403: PERMISSION_DENIED\nplease run 'gdrive auth login' to re-authenticate"
	b2 := DetectToolResultAuthBlock("antigravity", "run_command", nil, "Command exited with code 1.\n"+out403WithLogin, true)
	if b2 == nil {
		t.Fatalf("expected BlockAuth when auth login is explicitly prompted alongside 403")
	}
	if b2.AuthServer != "gdrive" {
		t.Errorf("expected gdrive auth target, got: %s", b2.AuthServer)
	}
}

func TestDetectToolResultAuthBlock_ShellToolPlugins(t *testing.T) {
	plugins := []string{"gslides", "gdocs", "gdrive", "gsheets"}
	for _, p := range plugins {
		out := "Command exited with code 1.\nno credentials found: run '" + p + " auth login'"
		b := DetectToolResultAuthBlock("antigravity", "bash", nil, out, true)
		if b == nil {
			t.Fatalf("expected BlockAuth for plugin %s, got nil", p)
		}
		if b.Kind != BlockAuth || b.AuthKind != "plugin" || b.AuthServer != p || b.AuthTarget != "plugin:"+p {
			t.Errorf("mismatched block for %s: %+v", p, b)
		}
	}

	genericCurl401 := "Command exited with code 1.\nHTTP/1.1 401 Unauthorized\n{\"error\": \"invalid_token\"}"
	bGeneric := DetectToolResultAuthBlock("antigravity", "bash", nil, genericCurl401, true)
	if bGeneric != nil {
		t.Errorf("expected nil for generic curl 401 in shell tool, got: %+v", bGeneric)
	}
}

func TestDetectToolResultAuthBlock_MCPTools(t *testing.T) {
	b1 := DetectToolResultAuthBlock("claude-code", "mcp__github__create_issue", nil, "401 Unauthorized: token expired", true)
	if b1 == nil {
		t.Fatalf("expected BlockAuth for mcp__github__create_issue, got nil")
	}
	if b1.AuthKind != "mcp" || b1.AuthServer != "github" || b1.AuthTarget != "mcp:github" {
		t.Errorf("unexpected mcp block: %+v", b1)
	}

	b2 := DetectToolResultAuthBlock("claude-code", "mcp_stripe_charge", nil, "requires authentication: oauth login needed", true)
	if b2 == nil {
		t.Fatalf("expected BlockAuth for mcp_stripe_charge, got nil")
	}
	if b2.AuthServer != "stripe" {
		t.Errorf("expected stripe, got %s", b2.AuthServer)
	}

	input := map[string]any{"ServerName": "slack"}
	b3 := DetectToolResultAuthBlock("claude-code", "call_mcp_tool", input, "error: not logged in", true)
	if b3 == nil {
		t.Fatalf("expected BlockAuth for call_mcp_tool, got nil")
	}
	if b3.AuthServer != "slack" {
		t.Errorf("expected slack, got %s", b3.AuthServer)
	}

	badInput := map[string]any{"ServerName": "invalid/server;name"}
	bBad := DetectToolResultAuthBlock("claude-code", "call_mcp_tool", badInput, "401 Unauthorized", true)
	if bBad != nil {
		t.Errorf("expected nil for invalid server name, got: %+v", bBad)
	}
}

func TestDetectTurnAuthBlock(t *testing.T) {
	bAgy := DetectTurnAuthBlock("antigravity", "Please run: agy acp --login to continue")
	if bAgy == nil || bAgy.AuthKind != "agent" || bAgy.AuthTarget != "agent:agy" {
		t.Fatalf("failed to detect antigravity turn block: %+v", bAgy)
	}

	bClaude := DetectTurnAuthBlock("claude-code", "You are not logged in. Please run /login to authenticate.")
	if bClaude == nil || bClaude.AuthKind != "agent" || bClaude.AuthTarget != "agent:claude" || bClaude.AuthHint == "" {
		t.Fatalf("failed to detect claude turn block: %+v", bClaude)
	}

	bMCP := DetectTurnAuthBlock("claude-code", "MCP server 'postgres' requires authentication to proceed")
	if bMCP == nil || bMCP.AuthKind != "mcp" || bMCP.AuthServer != "postgres" {
		t.Fatalf("failed to detect MCP turn block: %+v", bMCP)
	}

	bPlugin := DetectTurnAuthBlock("antigravity", "gdocs error: token expired, run gdocs auth login")
	if bPlugin == nil || bPlugin.AuthKind != "plugin" || bPlugin.AuthServer != "gdocs" {
		t.Fatalf("failed to detect plugin turn block: %+v", bPlugin)
	}
}

func TestDetectTurnFailureBlock(t *testing.T) {
	bAuth := detectTurnFailureBlock("antigravity", "open this link in a browser to log in with google: https://accounts.google.com/...")
	if bAuth == nil || bAuth.Kind != BlockAuth {
		t.Fatalf("expected BlockAuth, got: %+v", bAuth)
	}

	bRate := detectTurnFailureBlock("claude-code", "Claude 5-hour limit reached · resets at 2:00pm")
	if bRate == nil || bRate.Kind != BlockRateLimit {
		t.Fatalf("expected BlockRateLimit, got: %+v", bRate)
	}

	bNil := detectTurnFailureBlock("claude-code", "All done successfully.")
	if bNil != nil {
		t.Errorf("expected nil for normal turn text, got: %+v", bNil)
	}
}

func TestDetectToolResultAuthBlock_LeadingHyphenRejected(t *testing.T) {
	b := DetectToolResultAuthBlock("claude-code", "mcp__--browser__tool", nil, "401 Unauthorized", true)
	if b != nil {
		t.Errorf("expected nil for leading-hyphen server name, got: %+v", b)
	}

	bTurn := DetectTurnAuthBlock("claude-code", "MCP server '--browser' requires authentication")
	if bTurn != nil {
		t.Errorf("expected nil for leading-hyphen turn error, got: %+v", bTurn)
	}

	_, _, _, err := resolveAllowlistedAuthCommand(nil, "mcp:--browser", nil, "/tmp")
	if err == nil {
		t.Errorf("expected error from resolveAllowlistedAuthCommand for leading-hyphen mcp target")
	}
}
