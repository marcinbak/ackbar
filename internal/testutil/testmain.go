package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mkdirShortTempDir(pattern string) (string, error) {
	if runtime.GOOS != "windows" {
		// Keep Unix socket paths under the 104-byte sun_path limit on Darwin.
		if dir, err := os.MkdirTemp("/tmp", pattern); err == nil {
			return dir, nil
		}
	}
	return os.MkdirTemp("", pattern)
}

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

const grokACPStubPOSIX = `#!/bin/sh
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    case "$line" in
      *'"method":"'*'"id":'*|*'"id":'*'"method":"'*)
        id=$(printf '%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
        [ -z "$id" ] && continue
        case "$line" in
          *'"method":"initialize"'*)
            printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cached_token","name":"Cached"}]}}\n' "$id"
            ;;
          *'"method":"authenticate"'*|*'"method":"session/set_model"'*|*'"method":"session/set_mode"'*|*'"method":"session/set_config_option"'*)
            printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
            ;;
          *'"method":"session/new"'*|*'"method":"session/load"'*)
            printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"stub-session"}}\n' "$id"
            ;;
          *'"method":"session/prompt"'*)
            printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
            ;;
          *)
            printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id"
            ;;
        esac
        ;;
    esac
  done
fi
exit 0
`

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
		"TMPDIR",
		"TMUX",
		"TMUX_TMPDIR",
		"ACKBAR_BREW_OPT_BIN_DIRS",
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
		"PATH",
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

	tmpHome, err := mkdirShortTempDir("ackbar-test-home-*")
	if err != nil {
		return "", nil, err
	}
	shortTmpDir, err := mkdirShortTempDir("akt-*")
	if err != nil {
		_ = os.RemoveAll(tmpHome)
		return "", nil, err
	}

	subdirs := []string{
		".config",
		".cache",
		filepath.Join(".local", "share"),
		filepath.Join(".local", "state"),
		".test-bin",
	}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(tmpHome, sub), 0o700); err != nil {
			_ = os.RemoveAll(shortTmpDir)
			_ = os.RemoveAll(tmpHome)
			return "", nil, err
		}
	}

	testBinDir := filepath.Join(tmpHome, ".test-bin")
	// Prepend no-op agent CLI stubs so tests never execute host binaries or fail when CLIs are absent.
	agentBins := []string{"claude", "agy", "antigravity", "codex", "grok", "grok-cli", "opencode"}
	for _, name := range agentBins {
		content := "#!/bin/sh\nexit 0\n"
		if name == "grok" || name == "grok-cli" {
			content = grokACPStubPOSIX
		}
		if err := os.WriteFile(filepath.Join(testBinDir, name), []byte(content), 0o755); err != nil {
			_ = os.RemoveAll(shortTmpDir)
			_ = os.RemoveAll(tmpHome)
			return "", nil, err
		}
		if runtime.GOOS == "windows" {
			if err := os.WriteFile(filepath.Join(testBinDir, name+".bat"), []byte("@echo off\r\nexit /b 0\r\n"), 0o755); err != nil {
				_ = os.RemoveAll(shortTmpDir)
				_ = os.RemoveAll(tmpHome)
				return "", nil, err
			}
		}
	}
	if origPath := snapshot["PATH"]; origPath.set && origPath.val != "" {
		_ = os.Setenv("PATH", testBinDir+string(os.PathListSeparator)+origPath.val)
	} else {
		_ = os.Setenv("PATH", testBinDir)
	}

	_ = os.Setenv("HOME", tmpHome)
	_ = os.Setenv("USERPROFILE", tmpHome)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpHome, ".config"))
	_ = os.Setenv("XDG_CACHE_HOME", filepath.Join(tmpHome, ".cache"))
	_ = os.Setenv("XDG_DATA_HOME", filepath.Join(tmpHome, ".local", "share"))
	_ = os.Setenv("XDG_STATE_HOME", filepath.Join(tmpHome, ".local", "state"))
	_ = os.Setenv("APPDATA", filepath.Join(tmpHome, ".config"))
	_ = os.Setenv("LOCALAPPDATA", filepath.Join(tmpHome, ".cache"))
	_ = os.Setenv("TMPDIR", shortTmpDir)
	if runtime.GOOS != "windows" {
		_ = os.Setenv("TMUX_TMPDIR", shortTmpDir)
	}
	_ = os.Unsetenv("TMUX")
	_ = os.Setenv("ACKBAR_BREW_OPT_BIN_DIRS", "")

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

	widenAndRemoveAll := func(root string) {
		if root == "" {
			return
		}
		// Permissions must be widened before deletion to remove read-only fixtures without EPERM.
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
		_ = os.RemoveAll(root)
	}

	cleanup := func() {
		if runtime.GOOS != "windows" {
			tmuxDir := filepath.Join(shortTmpDir, fmt.Sprintf("tmux-%d", os.Getuid()))
			if info, err := os.Stat(tmuxDir); err == nil && info.IsDir() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				cmd := exec.CommandContext(ctx, "tmux", "kill-server")
				env := make([]string, 0, len(os.Environ())+1)
				for _, e := range os.Environ() {
					if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_TMPDIR=") {
						continue
					}
					env = append(env, e)
				}
				cmd.Env = append(env, "TMUX_TMPDIR="+shortTmpDir)
				_ = cmd.Run()
				cancel()
			}
		}
		widenAndRemoveAll(shortTmpDir)
		widenAndRemoveAll(tmpHome)

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
