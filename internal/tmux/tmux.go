package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Spawn creates a new detached tmux session with the given name, running the command in the specified directory.
func Spawn(ctx context.Context, sessionName, cwd, command string) error {
	return SpawnWithEnv(ctx, sessionName, cwd, command, nil)
}

func buildPaneShellArgs(cwd, command string, env map[string]string) []string {
	// Trim trailing newlines and semicolons so "; exec bash -l" never forms an invalid "\n;" token.
	trimmedCmd := strings.TrimRight(command, " \t\r\n;")
	if trimmedCmd == "" {
		return nil
	}
	cdPrefix := ""
	if cwd != "" {
		cdPrefix = fmt.Sprintf("cd %s 2>/dev/null || true; ", shellSingleQuote(cwd))
	}
	envExports := ""
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			if isValidEnvKey(k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := env[k]
			envExports += fmt.Sprintf("export %s=%s; ", k, shellSingleQuote(v))
		}
	}
	return []string{
		"bash", "-l", "-c",
		fmt.Sprintf("%sexport PATH=\"/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$PATH\"; %s%s; exec bash -l", cdPrefix, envExports, trimmedCmd),
	}
}

// SpawnWithEnv creates a new detached tmux session with custom environment variables injected before command execution.
func SpawnWithEnv(ctx context.Context, sessionName, cwd, command string, env map[string]string) error {
	// check if session already exists to avoid duplicate spawning
	if HasSession(ctx, sessionName) {
		return fmt.Errorf("tmux session %s already exists", sessionName)
	}

	args := []string{"new-session", "-d", "-s", sessionName}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, buildPaneShellArgs(cwd, command, env)...)

	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to spawn tmux session: %w (stderr: %s)", err, stderr.String())
	}

	// Configure window-size latest so new sessions adapt dynamically to client geometries
	_ = exec.CommandContext(ctx, "tmux", "set-option", "-t", sessionName, "window-size", "latest").Run()
	_ = exec.CommandContext(ctx, "tmux", "set-window-option", "-t", sessionName, "window-size", "latest").Run()

	return nil
}

// RespawnWithEnv restarts the active pane process in-place inside an existing tmux session,
// preserving attached PTY clients and scrollback. Falls back to SpawnWithEnv if the session does not exist.
func RespawnWithEnv(ctx context.Context, sessionName, cwd, command string, env map[string]string) error {
	if !HasSession(ctx, sessionName) {
		return SpawnWithEnv(ctx, sessionName, cwd, command, env)
	}

	paneTarget := exactSessionTarget(sessionName) + ":"
	args := []string{"respawn-pane", "-k", "-t", paneTarget}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, buildPaneShellArgs(cwd, command, env)...)

	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to respawn tmux pane: %w (stderr: %s)", err, stderr.String())
	}

	_ = exec.CommandContext(ctx, "tmux", "set-option", "-t", exactSessionTarget(sessionName), "window-size", "latest").Run()
	_ = exec.CommandContext(ctx, "tmux", "set-window-option", "-t", paneTarget, "window-size", "latest").Run()

	return nil
}

// Kill terminates the tmux session with the given name.
func exactSessionTarget(sessionName string) string {
	if sessionName == "" || strings.HasPrefix(sessionName, "=") {
		return sessionName
	}
	return "=" + sessionName
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func isValidEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') {
				return false
			}
		} else {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
				return false
			}
		}
	}
	return true
}

func Kill(ctx context.Context, sessionName string) error {
	cmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", exactSessionTarget(sessionName))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If session is already gone, don't return error
		if strings.Contains(strings.ToLower(stderr.String()), "can't find session") {
			return nil
		}
		return fmt.Errorf("failed to kill tmux session: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}

// Rename renames an existing tmux session to newSessionName.
func Rename(ctx context.Context, oldSessionName, newSessionName string) error {
	if oldSessionName == "" || newSessionName == "" || oldSessionName == newSessionName {
		return nil
	}
	cmd := exec.CommandContext(ctx, "tmux", "rename-session", "-t", exactSessionTarget(oldSessionName), newSessionName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to rename tmux session %s to %s: %w (stderr: %s)", oldSessionName, newSessionName, err, stderr.String())
	}
	return nil
}

// GetPID returns the PID of the main process running in the tmux session's active pane.
func GetPID(ctx context.Context, sessionName string) (int, error) {
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", sessionName, "#{pane_pid}")
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get tmux pane PID: %w", err)
	}

	pidStr := strings.TrimSpace(string(out))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID %q: %w", pidStr, err)
	}

	return pid, nil
}

// HasSession checks if a tmux session with the given name currently exists.
func HasSession(ctx context.Context, sessionName string) bool {
	if sessionName == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", exactSessionTarget(sessionName))
	err := cmd.Run()
	return err == nil
}

// ListSessions returns a map of all currently active tmux session names.
func ListSessions(ctx context.Context) (map[string]bool, error) {
	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		// If tmux is not running or has no sessions, return an empty map without error
		return make(map[string]bool), nil
	}
	sessions := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			sessions[trimmed] = true
		}
	}
	return sessions, nil
}

// IsTmuxInstalled checks if the tmux binary is present in the system's PATH.
func IsTmuxInstalled() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// SendKeys sends one or more key names (e.g. "y", "n", "Enter", "C-c") to the tmux session.
func SendKeys(ctx context.Context, sessionName string, keys ...string) error {
	args := append([]string{"send-keys", "-t", sessionName}, keys...)
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to send keys to tmux session %s: %w (stderr: %s)", sessionName, err, stderr.String())
	}
	return nil
}

// SendInput sends text literally to the tmux session, followed by an optional Enter key.
func SendInput(ctx context.Context, sessionName string, text string, pressEnter bool) error {
	if text != "" {
		cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", sessionName, "-l", text)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to send literal input to tmux session %s: %w (stderr: %s)", sessionName, err, stderr.String())
		}
		if pressEnter {
			select {
			case <-time.After(150 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if pressEnter {
		return SendKeys(ctx, sessionName, "Enter")
	}
	return nil
}
