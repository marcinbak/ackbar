package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

	// 3. Test discovery
	subs, err := p.ListSubagents(tmpHome, "", sessID)
	if err != nil {
		t.Fatalf("ListSubagents failed: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subagent, got %d", len(subs))
	}
	if subs[0].ID != "subagent-1" {
		t.Errorf("expected ID 'subagent-1', got %q", subs[0].ID)
	}
	if subs[0].Role != "reviewer" {
		t.Errorf("expected Role 'reviewer', got %q", subs[0].Role)
	}
	if subs[0].Prompt != "review the changes" {
		t.Errorf("expected Prompt 'review the changes', got %q", subs[0].Prompt)
	}
}
