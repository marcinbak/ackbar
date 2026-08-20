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

	// 5. Kill session
	err = Kill(ctx, sessionName)
	if err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	// 6. Verify it is gone
	if HasSession(ctx, sessionName) {
		t.Errorf("Expected session %s to be terminated", sessionName)
	}
}
