package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSupervisorWorkspace_DefaultDirAndPrompt(t *testing.T) {
	// Test DefaultSupervisorDir
	customHome := "/Users/testuser"
	expected := filepath.Join(customHome, ".ackbar", "supervisor")
	if dir := DefaultSupervisorDir(customHome); dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}

	if dir := DefaultSupervisorDir(""); dir != filepath.Join("~", ".ackbar", "supervisor") {
		t.Errorf("expected ~/ fallback, got %q", dir)
	}

	// Test BuildSupervisorSystemPrompt
	prompt := BuildSupervisorSystemPrompt("claude-code:local:123-abc")
	if !strings.Contains(prompt, "session_id: claude-code:local:123-abc") {
		t.Errorf("expected prompt to contain session ID, got: %s", prompt)
	}
	if !strings.Contains(prompt, "HARD RULE 1") || !strings.Contains(prompt, "HARD RULE 2") || !strings.Contains(prompt, "HARD RULE 3") {
		t.Errorf("expected prompt to contain all 3 hard rules, got: %s", prompt)
	}
	if !strings.Contains(prompt, "mcp__ackbar__bind_supervisor_session") || !strings.Contains(prompt, "mcp__ackbar__create_task") {
		t.Errorf("expected prompt to reference supervisor dispatch tools, got: %s", prompt)
	}
}

func TestSupervisorWorkspace_EnsureWorkspace(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "supervisor_test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	srv.SetHostIdentity("test-box", "Test Box")

	// Save tree node and host paths
	_ = db.SaveNode(&TreeNode{
		Path:       "Modemobile/NGL",
		ProjectDir: "/path/to/ngl",
		GitURL:     "git@github.com:modemobile/ngl.git",
		CreatedAt:  time.Now(),
	})
	_ = db.SaveNodeHostPath(&NodeHostPath{
		NodePath:   "Modemobile/NGL",
		Host:       "devbox",
		ProjectDir: "/remote/path/to/ngl",
		GitRemote:  "git@github.com:modemobile/ngl.git",
		UpdatedAt:  time.Now(),
	})

	targetDir := filepath.Join(t.TempDir(), "supervisor")
	srv.ensureSupervisorWorkspace(targetDir)

	for _, fname := range []string{"CLAUDE.md", "AGENTS.md"} {
		path := filepath.Join(targetDir, fname)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", fname, err)
		}
		content := string(data)
		if !strings.Contains(content, "⚓ Ackbar Supervisor Dispatcher Workspace") {
			t.Errorf("expected header in %s", fname)
		}
		if !strings.Contains(content, "Hard Rule 1") || !strings.Contains(content, "Hard Rule 2") || !strings.Contains(content, "Hard Rule 3") {
			t.Errorf("expected hard rules in %s", fname)
		}
		if !strings.Contains(content, "Modemobile/NGL") {
			t.Errorf("expected registered tree node in %s table, got: %s", fname, content)
		}
		if !strings.Contains(content, "devbox") || !strings.Contains(content, "/remote/path/to/ngl") {
			t.Errorf("expected host path details in %s table, got: %s", fname, content)
		}
	}
}
