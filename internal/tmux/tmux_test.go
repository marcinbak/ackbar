package tmux

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTmuxSupervision(t *testing.T) {
	if !IsTmuxInstalled() {
		t.Skip("tmux not installed on host, skipping test")
	}

	sessionName := "ackbar-test-session"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ensure cleanup if previous test runs crashed
	_ = Kill(ctx, sessionName)

	// 1. Spawn a sleep session
	err := Spawn(ctx, sessionName, os.TempDir(), "sleep 5")
	if err != nil {
		if strings.Contains(err.Error(), "Operation not permitted") {
			t.Skip("tmux socket creation blocked by sandbox environment, skipping test")
		}
		t.Fatalf("Spawn failed: %v", err)
	}
	defer Kill(ctx, sessionName)

	// 2. Check existence
	if !HasSession(ctx, sessionName) {
		t.Errorf("Expected session %s to exist", sessionName)
	}

	// 2b. Verify ListSessions finds it in batch
	activeMap, err := ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if !activeMap[sessionName] {
		t.Errorf("Expected ListSessions to contain %s, got: %v", sessionName, activeMap)
	}

	// 3. Get PID
	pid, err := GetPID(ctx, sessionName)
	if err != nil {
		t.Fatalf("GetPID failed: %v", err)
	}
	if pid <= 0 {
		t.Errorf("Expected valid PID, got %d", pid)
	}

	// 4. Test SendKeys and SendInput
	if err := SendInput(ctx, sessionName, "echo test", true); err != nil {
		t.Errorf("SendInput failed: %v", err)
	}
	if err := SendKeys(ctx, sessionName, "Enter"); err != nil {
		t.Errorf("SendKeys failed: %v", err)
	}

	// 5. Test Rename
	renamedName := "ackbar-test-session-renamed"
	_ = Kill(ctx, renamedName)
	if err := Rename(ctx, sessionName, renamedName); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	defer Kill(ctx, renamedName)

	if HasSession(ctx, sessionName) {
		t.Errorf("Expected old session name %s to no longer exist", sessionName)
	}
	if !HasSession(ctx, renamedName) {
		t.Errorf("Expected renamed session %s to exist", renamedName)
	}

	// 6. Kill session
	err = Kill(ctx, renamedName)
	if err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	// 7. Verify it is gone
	if HasSession(ctx, renamedName) {
		t.Errorf("Expected session %s to be terminated", renamedName)
	}
}

func TestTmuxRename_EdgeCases(t *testing.T) {
	if !IsTmuxInstalled() {
		t.Skip("tmux not installed on host, skipping test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. Empty strings are no-ops
	if err := Rename(ctx, "", "new"); err != nil {
		t.Errorf("Expected nil error for empty old name, got: %v", err)
	}
	if err := Rename(ctx, "old", ""); err != nil {
		t.Errorf("Expected nil error for empty new name, got: %v", err)
	}
	if err := Rename(ctx, "same", "same"); err != nil {
		t.Errorf("Expected nil error for identical names, got: %v", err)
	}

	// 2. Renaming a non-existent session should return error
	if err := Rename(ctx, "non-existent-session-12345", "new-name"); err == nil {
		t.Errorf("Expected error when renaming non-existent session, got nil")
	}
}

func TestTmuxRespawnWithEnv_InPlacePreservesSessionAndInjectsEnv(t *testing.T) {
	if !IsTmuxInstalled() {
		t.Skip("tmux not installed on host, skipping test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionName := "ackbar-test-respawn-inplace"
	_ = Kill(ctx, sessionName)
	defer Kill(ctx, sessionName)

	tmpDir := t.TempDir()
	envOutFile := tmpDir + "/env.txt"

	if err := SpawnWithEnv(ctx, sessionName, tmpDir, "sleep 30", map[string]string{"ACKBAR_GEN": "1"}); err != nil {
		if strings.Contains(err.Error(), "Operation not permitted") {
			t.Skip("tmux socket creation blocked by sandbox environment, skipping test")
		}
		t.Fatalf("SpawnWithEnv failed: %v", err)
	}

	pid1, err := GetPID(ctx, sessionName)
	if err != nil || pid1 <= 0 {
		t.Fatalf("GetPID initial failed: pid=%d err=%v", pid1, err)
	}

	respawnCmd := "printf '%s|%s' \"$ACKBAR_GEN\" \"$CLAUDE_CONFIG_DIR\" > " + shellSingleQuote(envOutFile) + "; sleep 30"
	if err := RespawnWithEnv(ctx, sessionName, tmpDir, respawnCmd, map[string]string{
		"ACKBAR_GEN":        "2",
		"CLAUDE_CONFIG_DIR": "/custom/claude-profile",
	}); err != nil {
		t.Fatalf("RespawnWithEnv failed: %v", err)
	}

	if !HasSession(ctx, sessionName) {
		t.Fatalf("expected tmux session %s to stay alive across RespawnWithEnv", sessionName)
	}

	pid2, err := GetPID(ctx, sessionName)
	if err != nil || pid2 <= 0 {
		t.Fatalf("GetPID after respawn failed: pid=%d err=%v", pid2, err)
	}
	if pid2 == pid1 {
		t.Errorf("expected pane PID to change after RespawnWithEnv, got same PID %d", pid2)
	}

	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if data, rErr := os.ReadFile(envOutFile); rErr == nil && len(data) > 0 {
			got = string(data)
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got != "2|/custom/claude-profile" {
		t.Fatalf("expected respawned pane env '2|/custom/claude-profile', got %q", got)
	}
}
