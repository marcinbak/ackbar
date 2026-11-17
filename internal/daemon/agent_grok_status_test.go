package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectGrokStatus_HeadlessGuard(t *testing.T) {
	sess := &Session{
		ID:         "grok:local:head-1",
		Agent:      "grok",
		EngineType: EngineHeadless,
		State:      StateIdle,
		NativeID:   "head-1",
	}

	// Headless idle session with no alive PID returns false without pane capture
	changed := InspectGrokStatus(context.Background(), sess)
	if changed {
		t.Errorf("expected no change for idle headless session with no PID")
	}
}

func TestInspectGrokStatus_MetadataAndSubagents(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("GROK_HOME", filepath.Join(tmpHome, ".grok"))

	projectDir := filepath.Join(tmpHome, "my-project")
	_ = os.MkdirAll(projectDir, 0755)

	nativeID := "grok-sess-status-test"
	sessDir := filepath.Join(tmpHome, ".grok", "sessions", EncodeGrokCwd(projectDir), nativeID)
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatalf("failed to create sessDir: %v", err)
	}

	// 1. Write summary.json and signals.json
	summary := map[string]interface{}{
		"title":       "Grok Status Test",
		"head_branch": "feature-test",
	}
	sumBytes, _ := json.Marshal(summary)
	_ = os.WriteFile(filepath.Join(sessDir, "summary.json"), sumBytes, 0644)

	signals := map[string]interface{}{
		"contextTokensUsed":   4096,
		"contextWindowTokens": 16384,
	}
	sigBytes, _ := json.Marshal(signals)
	_ = os.WriteFile(filepath.Join(sessDir, "signals.json"), sigBytes, 0644)

	// 2. Subagents directory with 1 running subagent
	subDir := filepath.Join(sessDir, "subagents", "sub-worker-1")
	_ = os.MkdirAll(subDir, 0755)
	subMeta := map[string]interface{}{
		"session_id": "sub-worker-1",
		"role":       "coder",
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"status":     "running",
	}
	subMetaBytes, _ := json.Marshal(subMeta)
	_ = os.WriteFile(filepath.Join(subDir, "meta.json"), subMetaBytes, 0644)

	sess := &Session{
		ID:         "grok:local:" + nativeID,
		Agent:      "grok",
		EngineType: EngineTmux,
		TmuxName:   "dummy-tmux-pane-not-running",
		State:      StateWorking,
		NativeID:   nativeID,
		Cwd:        projectDir,
	}

	changed := InspectGrokStatus(context.Background(), sess)
	if !changed {
		t.Errorf("expected changed=true when metadata and subagents are updated")
	}

	if sess.ContextPct != 25 {
		t.Errorf("expected ContextPct 25, got %d", sess.ContextPct)
	}
	if sess.RunningSubagents != 1 {
		t.Errorf("expected RunningSubagents 1, got %d", sess.RunningSubagents)
	}
}
