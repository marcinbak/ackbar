package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Spawn creates a new detached tmux session with the given name, running the command in the specified directory.
func Spawn(ctx context.Context, sessionName, cwd, command string) error {
	// check if session already exists to avoid duplicate spawning
	if HasSession(ctx, sessionName) {
		return fmt.Errorf("tmux session %s already exists", sessionName)
	}

	args := []string{"new-session", "-d", "-s", sessionName}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	if command != "" {
		cdPrefix := ""
		if cwd != "" {
			cdPrefix = fmt.Sprintf("cd %q 2>/dev/null || true; ", cwd)
		}
		args = append(args, "bash", "-l", "-c", fmt.Sprintf("%sexport PATH=\"$HOME/.local/bin:$HOME/.npm-global/bin:$PATH\"; %s; exec bash -l", cdPrefix, command))
	}

	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to spawn tmux session: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}

// Kill terminates the tmux session with the given name.
func Kill(ctx context.Context, sessionName string) error {
	cmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionName)
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
	cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", sessionName)
	err := cmd.Run()
	return err == nil
}

// IsTmuxInstalled checks if the tmux binary is present in the system's PATH.
func IsTmuxInstalled() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}
