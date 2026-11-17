package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func RunIsolatedTestMain(m *testing.M) {
	os.Exit(runIsolatedTestMain(m))
}

func runIsolatedTestMain(m *testing.M) int {
	_, cleanup, err := IsolateTestEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to isolate test environment: %v\n", err)
		return 1
	}
	defer cleanup()
	return m.Run()
}

func IsolateTestEnv() (string, func(), error) {
	preserveGoEnv()

	managedVars := []string{
		"HOME",
		"USERPROFILE",
		"XDG_CONFIG_HOME",
		"XDG_CACHE_HOME",
		"XDG_DATA_HOME",
		"XDG_STATE_HOME",
		"APPDATA",
		"LOCALAPPDATA",
		"ACKBAR_HOST",
		"ACKBAR_DISPLAY_NAME",
		"ACKBAR_TOKEN",
		"ACKBAR_DAEMON_URL",
		"ACKBAR_SESSION_ID",
		"ACKBAR_IS_SUPERVISOR",
		"CLAUDE_SESSION_ID",
		"CLAUDE_CONFIG_DIR",
		"GEMINI_CLI_HOME",
		"GROK_HOME",
		"CODEX_HOME",
		"ACKBAR_NTFY_TOPIC",
		"ACKBAR_NTFY_SERVER",
		"ACKBAR_NTFY_TOKEN",
		"ACKBAR_RELAY_URL",
		"ACKBAR_RELAY_SECRET",
		"GIT_CONFIG_GLOBAL",
	}

	type envVal struct {
		val string
		set bool
	}
	snapshot := make(map[string]envVal, len(managedVars))
	for _, k := range managedVars {
		val, ok := os.LookupEnv(k)
		snapshot[k] = envVal{val: val, set: ok}
	}

	tmpHome, err := os.MkdirTemp("", "ackbar-test-home-*")
	if err != nil {
		return "", nil, err
	}

	subdirs := []string{
		".config",
		".cache",
		filepath.Join(".local", "share"),
		filepath.Join(".local", "state"),
	}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(tmpHome, sub), 0o700); err != nil {
			_ = os.RemoveAll(tmpHome)
			return "", nil, err
		}
	}

	_ = os.Setenv("HOME", tmpHome)
	_ = os.Setenv("USERPROFILE", tmpHome)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpHome, ".config"))
	_ = os.Setenv("XDG_CACHE_HOME", filepath.Join(tmpHome, ".cache"))
	_ = os.Setenv("XDG_DATA_HOME", filepath.Join(tmpHome, ".local", "share"))
	_ = os.Setenv("XDG_STATE_HOME", filepath.Join(tmpHome, ".local", "state"))
	_ = os.Setenv("APPDATA", filepath.Join(tmpHome, ".config"))
	_ = os.Setenv("LOCALAPPDATA", filepath.Join(tmpHome, ".cache"))

	ambientVars := []string{
		"ACKBAR_HOST",
		"ACKBAR_DISPLAY_NAME",
		"ACKBAR_TOKEN",
		"ACKBAR_DAEMON_URL",
		"ACKBAR_SESSION_ID",
		"ACKBAR_IS_SUPERVISOR",
		"CLAUDE_SESSION_ID",
		"CLAUDE_CONFIG_DIR",
		"GEMINI_CLI_HOME",
		"GROK_HOME",
		"CODEX_HOME",
		"ACKBAR_NTFY_TOPIC",
		"ACKBAR_NTFY_SERVER",
		"ACKBAR_NTFY_TOKEN",
		"ACKBAR_RELAY_URL",
		"ACKBAR_RELAY_SECRET",
		"GIT_CONFIG_GLOBAL",
	}
	for _, v := range ambientVars {
		_ = os.Unsetenv(v)
	}

	cleanup := func() {
		// Permissions must be widened before deletion to remove read-only fixtures without EPERM.
		_ = filepath.WalkDir(tmpHome, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				_ = os.Chmod(path, 0o700)
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			if d.IsDir() {
				_ = os.Chmod(path, fi.Mode()|0o700)
			} else if fi.Mode().IsRegular() {
				_ = os.Chmod(path, fi.Mode()|0o600)
			}
			return nil
		})
		_ = os.RemoveAll(tmpHome)

		for k, v := range snapshot {
			if v.set {
				_ = os.Setenv(k, v.val)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}

	return tmpHome, cleanup, nil
}

func preserveGoEnv() {
	vars := []string{"GOCACHE", "GOPATH", "GOMODCACHE"}
	var missing bool
	for _, v := range vars {
		if os.Getenv(v) == "" {
			missing = true
			break
		}
	}
	if !missing {
		return
	}

	// Toolchain caches must be resolved before changing HOME to prevent cold rebuilds.
	out, err := exec.Command("go", "env", "GOCACHE", "GOPATH", "GOMODCACHE").Output()
	if err != nil {
		return
	}

	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	for i, v := range vars {
		if i < len(lines) && os.Getenv(v) == "" {
			val := strings.TrimSpace(lines[i])
			if val != "" {
				_ = os.Setenv(v, val)
			}
		}
	}
}
