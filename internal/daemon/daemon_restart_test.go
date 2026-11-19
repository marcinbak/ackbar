package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonRestartScripts_SyntaxAndManagedIsolation(t *testing.T) {
	cases := []struct {
		name     string
		isRemote bool
		script   string
	}{
		{
			name:     "remote_binary_restart",
			isRemote: true,
			script:   buildRemoteBinaryRestartScript(shellSingleQuote("tok-secret"), " --host-name macmini"),
		},
		{
			name:     "remote_self_update",
			isRemote: true,
			script:   buildRemoteSelfUpdateScript(shellSingleQuote("tok-secret"), " --host-name macmini"),
		},
		{
			name:     "local_async_restart",
			isRemote: false,
			script:   buildLocalAsyncRestartScript(4242, "/opt/homebrew/opt/ackbar/bin/ackbard", []string{"--host-name", "macmini"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := exec.Command("sh", "-n", "-c", tc.script).CombinedOutput(); err != nil {
				t.Fatalf("sh -n syntax check failed: %v\noutput: %s\nscript:\n%s", err, string(out), tc.script)
			}

			parts := strings.Split(tc.script, "\nelse\n")
			if len(parts) != 2 {
				t.Fatalf("expected exactly one top-level '\\nelse\\n' separator, got %d parts in:\n%s", len(parts), tc.script)
			}
			managedSection := parts[0]
			unmanagedSection := parts[1]

			if strings.Contains(managedSection, "nohup") {
				t.Fatalf("managed section must contain zero 'nohup' fallbacks:\n%s", managedSection)
			}
			if !strings.Contains(unmanagedSection, "nohup") {
				t.Fatalf("unmanaged else section must retain 'nohup' launch:\n%s", unmanagedSection)
			}

			idxSystemd := strings.Index(managedSection, "systemctl --user is-enabled ackbard.service")
			idxLaunchAgent := strings.Index(managedSection, `com\.marcinbak\.ackbard`)
			idxHomebrew := strings.Index(managedSection, `homebrew\.mxcl\.ackbar`)
			if idxSystemd == -1 || idxLaunchAgent == -1 || idxHomebrew == -1 {
				t.Fatalf("missing service manager check(s): systemd=%d launchAgent=%d homebrew=%d", idxSystemd, idxLaunchAgent, idxHomebrew)
			}
			if !(idxSystemd < idxLaunchAgent && idxLaunchAgent < idxHomebrew) {
				t.Fatalf("expected service manager order systemd -> com.marcinbak.ackbard -> homebrew.mxcl.ackbar, got %d, %d, %d", idxSystemd, idxLaunchAgent, idxHomebrew)
			}
			if !strings.Contains(managedSection, "linked*|static") || !strings.Contains(managedSection, "active|activating|reloading") {
				t.Fatalf("expected systemd check to cover linked/static and active/activating/reloading states:\n%s", managedSection)
			}

			if gotPolls := strings.Count(managedSection, `while [ "$i" -lt 25 ]; do has_non_worker_ackbard && break; sleep 0.2; i=$((i + 1)); done`); gotPolls != 3 {
				t.Fatalf("expected 3 worker-safe POSIX 5s readiness poll loops in managed branches, got %d", gotPolls)
			}
			if strings.Contains(tc.script, "pkill") {
				t.Fatalf("restart script must never invoke indiscriminate pkill (would SIGKILL --headless-worker sidecars):\n%s", tc.script)
			}
			if strings.Contains(tc.script, "pgrep -x ackbard") {
				t.Fatalf("restart script must use has_non_worker_ackbard rather than raw 'pgrep -x ackbard':\n%s", tc.script)
			}
			if !strings.Contains(managedSection, "\nself_heal_ackbard_service\n") || !strings.Contains(managedSection, "systemctl --user daemon-reload") {
				t.Fatalf("expected self_heal_ackbard_service and systemctl --user daemon-reload before service restart:\n%s", managedSection)
			}

			if tc.isRemote {
				for _, line := range strings.Split(managedSection, "\n") {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "systemctl --user restart") ||
						strings.Contains(trimmed, "launchctl kickstart") ||
						strings.HasPrefix(trimmed, "brew services restart") {
						if strings.Contains(trimmed, "|| true") {
							t.Fatalf("remote restart command must not swallow exit status with '|| true': %s", trimmed)
						}
						if strings.Contains(trimmed, "2>/dev/null") {
							t.Fatalf("remote restart command must preserve stderr (no '2>/dev/null'): %s", trimmed)
						}
					}
				}
			}
		})
	}
}

func TestDaemonRestartScripts_RemoteErrorPropagationAndSystemdLinkedState(t *testing.T) {
	mockBin := t.TempDir()
	mockHome := t.TempDir()

	writeHelper := func(name, body string) {
		p := filepath.Join(mockBin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	writeHelper("curl", "exit 0")
	writeHelper("pkill", "exit 0")
	writeHelper("pgrep", "exit 0")
	writeHelper("sleep", "exit 0")

	// Case 1: dotfile-linked unit (is-enabled exits 1 with "linked") whose restart fails -> exits non-zero and surfaces stderr.
	writeHelper("systemctl", `
if [ "$2" = "is-enabled" ]; then
  echo "linked"
  exit 1
fi
if [ "$2" = "restart" ]; then
  echo "Failed to restart ackbard.service: Unit broken" >&2
  exit 1
fi
exit 1`)

	script := buildRemoteBinaryRestartScript(shellSingleQuote(""), "")
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{
		"HOME=" + mockHome,
		"PATH=" + mockBin + ":/usr/bin:/bin",
		"ACKBAR_BREW_OPT_BIN_DIRS=",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when remote systemctl restart fails, got nil (output: %s)", string(out))
	}
	if !strings.Contains(string(out), "Failed to restart ackbard.service: Unit broken") {
		t.Fatalf("expected preserved systemctl stderr in output, got: %s", string(out))
	}

	// Case 2: disabled unit currently in "activating" state -> treated as managed and restarted cleanly.
	marker := filepath.Join(mockHome, "restarted.ok")
	writeHelper("systemctl", `
if [ "$2" = "is-enabled" ]; then
  echo "disabled"
  exit 1
fi
if [ "$2" = "is-active" ]; then
  echo "activating"
  exit 3
fi
if [ "$2" = "restart" ]; then
  touch "`+marker+`"
  exit 0
fi
exit 1`)

	cmd2 := exec.Command("sh", "-c", script)
	cmd2.Env = []string{
		"HOME=" + mockHome,
		"PATH=" + mockBin + ":/usr/bin:/bin",
		"ACKBAR_BREW_OPT_BIN_DIRS=",
	}
	if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
		t.Fatalf("expected activating systemd unit restart to succeed, got %v (%s)", err2, string(out2))
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("expected systemctl --user restart to be invoked for activating unit: %v", statErr)
	}

	// Case 3: buildRemoteSelfUpdateScript preserves ackbar update diagnostic output when ackbar exits non-zero under set -e.
	writeHelper("ackbar", `
echo "-e GitHub API rate limit exceeded while fetching release" >&2
exit 42`)

	selfUpdScript := buildRemoteSelfUpdateScript(shellSingleQuote("tok-123"), "")
	cmd3 := exec.Command("sh", "-c", selfUpdScript)
	cmd3.Env = []string{
		"HOME=" + mockHome,
		"PATH=" + mockBin + ":/usr/bin:/bin",
		"ACKBAR_BREW_OPT_BIN_DIRS=",
	}
	out3, err3 := cmd3.CombinedOutput()
	if err3 == nil {
		t.Fatalf("expected non-zero exit when ackbar update --force fails, got nil (output: %s)", string(out3))
	}
	if !strings.Contains(string(out3), "-e GitHub API rate limit exceeded while fetching release") {
		t.Fatalf("expected diagnostic output from failing ackbar update to be preserved, got: %s", string(out3))
	}
}

func TestBuildLocalAsyncRestartScript_SingleQuotesHazardousExePath(t *testing.T) {
	exe := "/tmp/My Dir/$PWN/`id`/o'hare/ackbard"
	rawArgs := []string{"--display-name", "Mac $HOME `uname`"}
	script := buildLocalAsyncRestartScript(9999, exe, rawArgs)

	if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("sh -n failed for hazardous path: %v (%s)", err, string(out))
	}

	expectedCmd := "nohup '/tmp/My Dir/$PWN/`id`/o'\\''hare/ackbard' '--display-name' 'Mac $HOME `uname`' >/dev/null 2>&1 < /dev/null &"
	if !strings.Contains(script, expectedCmd) {
		t.Fatalf("expected single-quoted command:\n%s\nin script:\n%s", expectedCmd, script)
	}
}

func TestCodesignDarwinStagedBinaryShellHelper(t *testing.T) {
	const sha = "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678"

	setupMockEnv := func(t *testing.T, unameOut string, keychainOK bool, logPath string) (mockHome, mockBin string) {
		t.Helper()
		mockHome = t.TempDir()
		mockBin = t.TempDir()
		_ = os.MkdirAll(filepath.Join(mockHome, "Library", "Keychains"), 0700)
		_ = os.WriteFile(filepath.Join(mockHome, "Library", "Keychains", "login.keychain-db"), []byte("kc"), 0600)

		writeStub := func(name, body string) {
			p := filepath.Join(mockBin, name)
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}

		writeStub("uname", "echo "+unameOut)
		if keychainOK {
			writeStub("security", `
if [ "$1" = "show-keychain-info" ]; then
  exit 0
fi
if [ "$1" = "find-identity" ]; then
  echo '  1) `+strings.ToLower(sha)+` "Ackbar Local Code Signing"'
  exit 0
fi
exit 1`)
		} else {
			writeStub("security", `exit 1`)
		}
		writeStub("codesign", `
echo "codesign $*" >> "`+logPath+`"
if [ "$1" = "-d" ]; then
  echo 'designated => identifier "com.marcinbak.ackbard" and cdhash H"deadbeef"'
  exit 0
fi
exit 0`)
		return mockHome, mockBin
	}

	t.Run("darwin_unlocked_signs_staged_new_before_mv", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "codesign.log")
		mockHome, mockBin := setupMockEnv(t, "Darwin", true, logFile)
		localBin := filepath.Join(mockHome, ".local", "bin")
		_ = os.MkdirAll(localBin, 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbard.new"), []byte("new-bin"), 0644)

		// Stub curl/systemctl/launchctl/pgrep so buildRemoteBinaryRestartScript completes cleanly after mv
		for _, tool := range []string{"curl", "pgrep", "sleep"} {
			_ = os.WriteFile(filepath.Join(mockBin, tool), []byte("#!/bin/sh\nexit 0\n"), 0755)
		}
		_ = os.WriteFile(filepath.Join(mockBin, "systemctl"), []byte("#!/bin/sh\nif [ \"$2\" = \"is-enabled\" ]; then echo enabled; exit 0; fi\nexit 0\n"), 0755)

		script := buildRemoteBinaryRestartScript(shellSingleQuote(""), "")
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = []string{"HOME=" + mockHome, "PATH=" + mockBin + ":/usr/bin:/bin", "ACKBAR_BREW_OPT_BIN_DIRS="}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote restart script failed: %v (%s)", err, string(out))
		}

		logBytes, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("expected codesign log: %v", err)
		}
		logStr := string(logBytes)
		if !strings.Contains(logStr, "--sign "+sha+" --identifier com.marcinbak.ackbard "+filepath.Join(localBin, "ackbard.new")) {
			t.Fatalf("expected initial codesign on ackbard.new, got:\n%s", logStr)
		}
		wantReq := `-r=designated => identifier "com.marcinbak.ackbard" and certificate root = H"` + strings.ToLower(sha) + `"`
		if !strings.Contains(logStr, wantReq) {
			t.Fatalf("expected cdhash replacement requirement, got:\n%s", logStr)
		}
		if _, err := os.Stat(filepath.Join(localBin, "ackbard")); err != nil {
			t.Fatalf("expected ackbard moved into place: %v", err)
		}
		if _, err := os.Stat(filepath.Join(localBin, "ackbard.new")); !os.IsNotExist(err) {
			t.Fatalf("expected ackbard.new moved away")
		}
	})

	t.Run("locked_keychain_skips_codesign_without_error", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "codesign.log")
		mockHome, mockBin := setupMockEnv(t, "Darwin", false, logFile)
		staged := filepath.Join(mockHome, "ackbard.new")
		_ = os.WriteFile(staged, []byte("bin"), 0755)

		cmd := exec.Command("sh", "-c", DaemonSafeShellHelpers()+"\ncodesign_darwin_staged_binary \"$1\" \"$2\"", "sh", staged, "ackbard")
		cmd.Env = []string{"HOME=" + mockHome, "PATH=" + mockBin + ":/usr/bin:/bin"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("expected clean 0 exit on locked keychain, got %v (%s)", err, string(out))
		}
		if _, err := os.Stat(logFile); !os.IsNotExist(err) {
			t.Fatalf("expected codesign not invoked when keychain locked")
		}
	})

	t.Run("non_darwin_skips_codesign", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "codesign.log")
		mockHome, mockBin := setupMockEnv(t, "Linux", true, logFile)
		staged := filepath.Join(mockHome, "ackbard.new")
		_ = os.WriteFile(staged, []byte("bin"), 0755)

		cmd := exec.Command("sh", "-c", DaemonSafeShellHelpers()+"\ncodesign_darwin_staged_binary \"$1\" \"$2\"", "sh", staged, "ackbard")
		cmd.Env = []string{"HOME=" + mockHome, "PATH=" + mockBin + ":/usr/bin:/bin"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("expected clean 0 exit on Linux, got %v (%s)", err, string(out))
		}
		if _, err := os.Stat(logFile); !os.IsNotExist(err) {
			t.Fatalf("expected codesign not invoked on Linux")
		}
	})

	t.Run("disabled_by_env_skips_codesign", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "codesign.log")
		mockHome, mockBin := setupMockEnv(t, "Darwin", true, logFile)
		staged := filepath.Join(mockHome, "ackbard.new")
		_ = os.WriteFile(staged, []byte("bin"), 0755)

		cmd := exec.Command("sh", "-c", DaemonSafeShellHelpers()+"\ncodesign_darwin_staged_binary \"$1\" \"$2\"", "sh", staged, "ackbard")
		cmd.Env = []string{"HOME=" + mockHome, "PATH=" + mockBin + ":/usr/bin:/bin", "ACKBAR_CODESIGN_IDENTITY=none"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("expected clean 0 exit when disabled, got %v (%s)", err, string(out))
		}
		if _, err := os.Stat(logFile); !os.IsNotExist(err) {
			t.Fatalf("expected codesign not invoked when disabled")
		}
	})
}
