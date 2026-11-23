package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTmuxSupervision(t *testing.T) {
	if !IsTmuxInstalled() {
		t.Skip("tmux not installed on host, skipping test")
	}

	sessionName := "ackbar-test-session"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	deadline := time.Now().Add(10 * time.Second)
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

func TestBuildPaneShellArgs_MultilineCommand(t *testing.T) {
	script := `
echo "hello"
sleep 30
`
	args := buildPaneShellArgs("/tmp/work", script, map[string]string{"FOO": "bar"})
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}
	if strings.Contains(args[3], "\n;") {
		t.Fatalf("expected no '\\n;' sequence in shell command, got %q", args[3])
	}
	if !strings.HasSuffix(args[3], "sleep 30; exec bash -l") {
		t.Fatalf("expected shell command suffix 'sleep 30; exec bash -l', got %q", args[3])
	}

	semiArgs := buildPaneShellArgs("", "echo ok;\n\t;", nil)
	if len(semiArgs) != 4 {
		t.Fatalf("expected 4 args for trailing semicolon command, got %d", len(semiArgs))
	}
	if strings.Contains(semiArgs[3], "\n;") || strings.Contains(semiArgs[3], ";;") {
		t.Fatalf("unexpected trailing delimiter in shell command: %q", semiArgs[3])
	}
	if !strings.HasSuffix(semiArgs[3], "echo ok; exec bash -l") {
		t.Fatalf("expected shell command suffix 'echo ok; exec bash -l', got %q", semiArgs[3])
	}

	if emptyArgs := buildPaneShellArgs("/tmp", " \t\r\n; ", nil); emptyArgs != nil {
		t.Fatalf("expected nil args for whitespace/semicolon-only command, got %v", emptyArgs)
	}
}

func TestEnsureHyperlinksFeature_Unit(t *testing.T) {
	t.Run("sets feature when missing", func(t *testing.T) {
		var calls [][]string
		runner := func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, args)
			if len(args) > 0 && args[0] == "show-options" {
				return []byte("terminal-features[0] xterm*:clipboard:ccolour:cstyle:focus:title\nterminal-features[1] screen*:title\n"), nil
			}
			return nil, nil
		}

		ensureHyperlinksFeature(context.Background(), runner)

		want := [][]string{
			{"show-options", "-s", "terminal-features"},
			{"set-option", "-sa", "terminal-features", ",xterm-256color:hyperlinks"},
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	})

	t.Run("sets feature when only non-xterm entry has hyperlinks", func(t *testing.T) {
		var calls [][]string
		runner := func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, args)
			if len(args) > 0 && args[0] == "show-options" {
				return []byte("terminal-features[0] screen*:hyperlinks\nterminal-features[1] xterm*:clipboard\n"), nil
			}
			return nil, nil
		}

		ensureHyperlinksFeature(context.Background(), runner)

		if len(calls) != 2 || calls[1][0] != "set-option" {
			t.Fatalf("expected set-option when only screen* has hyperlinks, got calls = %v", calls)
		}
	})

	t.Run("noop when xterm hyperlinks already present", func(t *testing.T) {
		existingOutputs := []string{
			"terminal-features[0] xterm*:clipboard:title\nterminal-features[3] xterm-256color:hyperlinks\n",
			"terminal-features[0] xterm*:clipboard:ccolour:hyperlinks\n",
			"terminal-features[0] xterm-256color:RGB:hyperlinks\n",
		}
		for _, out := range existingOutputs {
			var calls [][]string
			runner := func(_ context.Context, args ...string) ([]byte, error) {
				calls = append(calls, args)
				return []byte(out), nil
			}

			ensureHyperlinksFeature(context.Background(), runner)

			want := [][]string{{"show-options", "-s", "terminal-features"}}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("for output %q: calls = %v, want %v", out, calls, want)
			}
		}
	})

	t.Run("tolerates errors on older tmux or missing server", func(t *testing.T) {
		var calls [][]string
		runner := func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, args)
			return nil, errors.New("exit status 1")
		}

		ensureHyperlinksFeature(context.Background(), runner)

		want := [][]string{
			{"show-options", "-s", "terminal-features"},
			{"set-option", "-sa", "terminal-features", ",xterm-256color:hyperlinks"},
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	})
}

func TestEnsureHyperlinksFeature_Integration(t *testing.T) {
	if !IsTmuxInstalled() {
		t.Skip("tmux not installed on host, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sessionName := "ackbar-test-hyperlinks"
	_ = Kill(ctx, sessionName)
	if err := Spawn(ctx, sessionName, os.TempDir(), "sleep 30"); err != nil {
		if strings.Contains(err.Error(), "Operation not permitted") {
			t.Skip("tmux socket creation blocked by sandbox environment, skipping test")
		}
		t.Fatalf("Spawn failed: %v", err)
	}
	defer Kill(ctx, sessionName)

	countMatches := func(out []byte) int {
		count := 0
		for _, line := range strings.Split(string(out), "\n") {
			if hyperlinksFeatureRegex.MatchString(line) {
				count++
			}
		}
		return count
	}

	out1, err := exec.CommandContext(ctx, "tmux", "show-options", "-s", "terminal-features").Output()
	if err != nil {
		t.Skipf("tmux show-options terminal-features not supported on host: %v", err)
	}
	c1 := countMatches(out1)

	EnsureHyperlinksFeature(ctx)

	out2, err := exec.CommandContext(ctx, "tmux", "show-options", "-s", "terminal-features").Output()
	if err != nil {
		t.Fatalf("show-options after EnsureHyperlinksFeature failed: %v", err)
	}
	c2 := countMatches(out2)

	if c1 != c2 {
		t.Fatalf("EnsureHyperlinksFeature was not idempotent: match count changed from %d to %d\n%s", c1, c2, string(out2))
	}

	if verOut, vErr := exec.CommandContext(ctx, "tmux", "-V").Output(); vErr == nil {
		verStr := strings.TrimSpace(string(verOut))
		if strings.HasPrefix(verStr, "tmux 3.4") || strings.HasPrefix(verStr, "tmux 3.5") || strings.HasPrefix(verStr, "tmux 3.6") {
			if c2 < 1 {
				t.Fatalf("expected hyperlinks terminal-feature on %s, got:\n%s", verStr, string(out2))
			}
		}
	}
}
