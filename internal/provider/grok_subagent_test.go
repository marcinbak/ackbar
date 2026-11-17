package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ackbar/internal/daemon"
)

func TestGrokProvider_ListSubagents(t *testing.T) {
	p := NewGrokProvider()

	// 1. Empty or invalid native ID
	if subs, err := p.ListSubagents("", "", ""); err != nil || len(subs) != 0 {
		t.Fatalf("expected nil for empty native ID, got subs=%v err=%v", subs, err)
	}
	if subs, err := p.ListSubagents("", "", "../bad/path"); err != nil || len(subs) != 0 {
		t.Fatalf("expected nil for unsafe native ID, got subs=%v err=%v", subs, err)
	}

	// 2. Mock grok session structure
	tmpHome := t.TempDir()
	sessID := "test-grok-session-123"
	subDir := filepath.Join(tmpHome, ".grok", "sessions", sessID, "subagents", "subagent-1")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create mock subagents dir: %v", err)
	}

	meta := map[string]interface{}{
		"session_id": "subagent-1",
		"role":       "reviewer",
		"prompt":     "review the changes",
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"status":     "running",
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(subDir, "meta.json"), metaBytes, 0644); err != nil {
		t.Fatalf("failed to write subagent meta.json: %v", err)
	}

	// 3. Test initial running discovery
	subs, err := p.ListSubagents(tmpHome, "", sessID)
	if err != nil {
		t.Fatalf("ListSubagents failed: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subagent, got %d", len(subs))
	}
	if subs[0].ID != "subagent-1" || subs[0].State != "running" {
		t.Errorf("expected running subagent, got ID=%q State=%q", subs[0].ID, subs[0].State)
	}

	// 4. ACP-wrapped updates.jsonl line transitions state to completed
	childSessionDir := filepath.Join(tmpHome, ".grok", "sessions", "subagent-1")
	if err := os.MkdirAll(childSessionDir, 0755); err != nil {
		t.Fatalf("failed to create child session dir: %v", err)
	}
	acpLine := `{"params": {"update": {"sessionUpdate": "turn_completed", "stop_reason": "end_turn"}}}` + "\n"
	if err := os.WriteFile(filepath.Join(childSessionDir, "updates.jsonl"), []byte(acpLine), 0644); err != nil {
		t.Fatalf("failed to write updates.jsonl: %v", err)
	}

	subsAfter, err := p.ListSubagents(tmpHome, "", sessID)
	if err != nil || len(subsAfter) != 1 {
		t.Fatalf("expected 1 subagent after update, got len=%d err=%v", len(subsAfter), err)
	}
	if subsAfter[0].State != "completed" {
		t.Errorf("expected state completed after ACP turn_completed, got %q", subsAfter[0].State)
	}
}

func TestResolveGrokSubagentParent_DeadlockAndValidation(t *testing.T) {
	tmpHome := t.TempDir()
	childID := "child-grok-123"
	childDir := filepath.Join(tmpHome, ".grok", "sessions", childID)
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("failed to create childDir: %v", err)
	}

	// 1. Self-parent (parent_session_id == nativeID) must return empty string
	selfParentSummary := map[string]interface{}{
		"parent_session_id": childID,
		"role":              "self-reviewer",
	}
	b, _ := json.Marshal(selfParentSummary)
	_ = os.WriteFile(filepath.Join(childDir, "summary.json"), b, 0644)

	pid, _, _ := daemon.ResolveGrokSubagentParent(tmpHome, childID)
	if pid != "" {
		t.Errorf("expected empty string for self-parent to prevent deadlock, got %q", pid)
	}

	// 2. Unsafe parent ID must return empty string
	unsafeSummary := map[string]interface{}{
		"parent_session_id": "../unsafe/parent",
		"role":              "hacker",
	}
	b2, _ := json.Marshal(unsafeSummary)
	_ = os.WriteFile(filepath.Join(childDir, "summary.json"), b2, 0644)

	pid2, _, _ := daemon.ResolveGrokSubagentParent(tmpHome, childID)
	if pid2 != "" {
		t.Errorf("expected empty string for unsafe parent, got %q", pid2)
	}

	// 3. Safe external parent ID returned properly
	safeSummary := map[string]interface{}{
		"parent_session_id": "parent-root-456",
		"role":              "subagent",
	}
	b3, _ := json.Marshal(safeSummary)
	_ = os.WriteFile(filepath.Join(childDir, "summary.json"), b3, 0644)

	pid3, role, _ := daemon.ResolveGrokSubagentParent(tmpHome, childID)
	if pid3 != "parent-root-456" || role != "subagent" {
		t.Errorf("expected parent 'parent-root-456' and role 'subagent', got pid=%q role=%q", pid3, role)
	}
}
