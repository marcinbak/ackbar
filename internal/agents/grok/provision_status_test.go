package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/tmux"
	"ackbar/internal/version"
)

func TestProvision_TOMLConfigHooksAndStatus(t *testing.T) {
	tempHome := t.TempDir()
	grokDir := filepath.Join(tempHome, ".grok")
	_ = os.MkdirAll(grokDir, 0o700)

	configTOMLPath := filepath.Join(grokDir, "config.toml")
	initialTOML := "# Grok User Config\n[user_settings]\ntheme = \"dark\"\n"
	_ = os.WriteFile(configTOMLPath, []byte(initialTOML), 0o600)

	if err := UpsertTOMLConfig(configTOMLPath, "ackbar"); err != nil {
		t.Fatalf("UpsertTOMLConfig failed: %v", err)
	}
	contentBytes, _ := os.ReadFile(configTOMLPath)
	content := string(contentBytes)
	if !strings.Contains(content, `theme = "dark"`) || !strings.Contains(content, "[compat.claude]") || !strings.Contains(content, "[mcp_servers.ackbar]") {
		t.Fatalf("unexpected config.toml:\n%s", content)
	}

	p := New()
	if err := p.Provision(context.Background(), tempHome); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}

	hooksBytes, err := os.ReadFile(filepath.Join(grokDir, "hooks", "ackbar.json"))
	if err != nil {
		t.Fatalf("read ackbar.json: %v", err)
	}
	var rootConfig map[string]interface{}
	if err := json.Unmarshal(hooksBytes, &rootConfig); err != nil {
		t.Fatalf("parse ackbar.json: %v", err)
	}
	hooksMap := rootConfig["hooks"].(map[string]interface{})
	for evtName, entries := range hooksMap {
		for _, rawItem := range entries.([]interface{}) {
			item := rawItem.(map[string]interface{})
			if _, hasMatcher := item["matcher"]; hasMatcher {
				t.Errorf("event %s contains forbidden matcher key", evtName)
			}
		}
	}

	st := p.CheckStatus(context.Background(), tempHome)
	if !st.Detected || !st.MCPInstalled || !st.SkillInstalled || st.SkillVersion != version.Version || !st.RulesInstalled || st.DriftDetected {
		t.Fatalf("expected clean CheckStatus after Provision, got %+v", st)
	}
}

func TestInspectStatus_GuardsAndMetadata(t *testing.T) {
	p := New()
	headlessSess := &agent.Session{
		ID: "grok:local:head-1", Agent: "grok", EngineType: agent.EngineHeadless, State: agent.StateIdle, NativeID: "head-1",
	}
	if p.InspectStatus(context.Background(), headlessSess) {
		t.Errorf("expected no change for idle headless session with no PID")
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("GROK_HOME", filepath.Join(tmpHome, ".grok"))
	projectDir := filepath.Join(tmpHome, "my-project")
	_ = os.MkdirAll(projectDir, 0o755)

	nativeID := "grok-sess-status-test"
	sessDir := filepath.Join(tmpHome, ".grok", "sessions", EncodeCwd(projectDir), nativeID)
	_ = os.MkdirAll(filepath.Join(sessDir, "subagents", "sub-worker-1"), 0o755)
	_ = os.WriteFile(filepath.Join(sessDir, "summary.json"), []byte(`{"title":"Grok Status Test","head_branch":"feature-test"}`), 0o644)
	_ = os.WriteFile(filepath.Join(sessDir, "signals.json"), []byte(`{"contextTokensUsed":4096,"contextWindowTokens":16384}`), 0o644)
	subMeta, _ := json.Marshal(map[string]interface{}{
		"session_id": "sub-worker-1", "role": "coder", "created_at": time.Now().UTC().Format(time.RFC3339), "status": "running",
	})
	_ = os.WriteFile(filepath.Join(sessDir, "subagents", "sub-worker-1", "meta.json"), subMeta, 0o644)

	sess := &agent.Session{
		ID: "grok:local:" + nativeID, Agent: "grok", EngineType: agent.EngineTmux,
		TmuxName: "dummy-tmux-pane-not-running", State: agent.StateWorking, NativeID: nativeID, Cwd: projectDir,
	}
	if !p.InspectStatus(context.Background(), sess) {
		t.Fatalf("expected changed=true on metadata and subagent update")
	}
	if sess.ContextPct != 25 || sess.RunningSubagents != 1 {
		t.Errorf("unexpected ContextPct=%d RunningSubagents=%d", sess.ContextPct, sess.RunningSubagents)
	}
}

func TestInspectStatus_TmuxIntegration(t *testing.T) {
	if !tmux.IsTmuxInstalled() {
		t.Skip("tmux not installed, skipping TestInspectStatus_TmuxIntegration")
	}
	p := New()
	ctx := context.Background()
	tmuxName := fmt.Sprintf("test-grok-%d", time.Now().UnixNano())
	defer tmux.Kill(ctx, tmuxName)

	cmd := "sh -c 'echo \"Tool execution requires approval [y/N]: \"; sleep 1; for i in $(seq 1 30); do echo \"\"; done; echo \"Task done\"; sleep 30'"
	if err := tmux.Spawn(ctx, tmuxName, os.TempDir(), cmd); err != nil {
		t.Fatalf("Failed to spawn test tmux session: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	pid, _ := tmux.GetPID(ctx, tmuxName)

	sess := &agent.Session{
		ID: "grok:local:test-sess-grok-1", Agent: "grok", Host: "local", NativeID: "test-sess-grok-1",
		State: agent.StateWorking, TmuxName: tmuxName, PID: pid, StartedAt: time.Now(), LastEventAt: time.Now(),
	}

	changed := false
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		if p.InspectStatus(ctx, sess) {
			changed = true
			break
		}
	}
	if !changed || sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockPermission {
		t.Fatalf("expected StateBlocked(permission), got changed=%v state=%v blocked=%+v", changed, sess.State, sess.Blocked)
	}

	time.Sleep(1100 * time.Millisecond)
	unblocked := false
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		if p.InspectStatus(ctx, sess) {
			unblocked = true
			break
		}
	}
	if !unblocked || sess.State != agent.StateIdle || sess.Blocked != nil {
		t.Fatalf("expected recovery to StateIdle, got unblocked=%v state=%v blocked=%+v", unblocked, sess.State, sess.Blocked)
	}
}
