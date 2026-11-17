package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHasLaunchctlLabel(t *testing.T) {
	sample := strings.Join([]string{
		"PID\tStatus\tLabel",
		"-\t0\tcom.marcinbak.ackbard.other",
		"-\t0\tprefix.com.marcinbak.ackbard",
		"812\t0\tcom.marcinbak.ackbard",
		"-\t0\thomebrew.mxcl.ackbar.helper",
	}, "\n")

	if !hasLaunchctlLabel(sample, "com.marcinbak.ackbard") {
		t.Fatal("expected exact label com.marcinbak.ackbard to match")
	}
	if hasLaunchctlLabel(sample, "homebrew.mxcl.ackbar") {
		t.Fatal("expected homebrew.mxcl.ackbar not to match .helper suffix")
	}
	if hasLaunchctlLabel(sample, "Label") {
		t.Fatal("expected header column Label not to match when searching for another label")
	}
	if hasLaunchctlLabel(sample, "") {
		t.Fatal("expected empty label to return false")
	}

	onlyOther := "PID\tStatus\tLabel\n-\t0\tcom.marcinbak.ackbard.other\n"
	if hasLaunchctlLabel(onlyOther, "com.marcinbak.ackbard") {
		t.Fatal("expected com.marcinbak.ackbard.other not to false-match com.marcinbak.ackbard")
	}
}

func TestDefaultDaemonServiceRestarter_CapturesCombinedOutput(t *testing.T) {
	def := newDefaultDaemonServiceRestarter()
	out, err := def.runCmd(nil, "/bin/sh", "-c", "echo launchd-stderr-detail >&2; exit 2")
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if !strings.Contains(string(out), "launchd-stderr-detail") {
		t.Fatalf("expected stderr to be captured via CombinedOutput, got %q", string(out))
	}
}

func TestDaemonServiceRestarter_DarwinLaunchAgentFastSlowAndError(t *testing.T) {
	var invoked [][]string
	probes := 0
	fast := &daemonServiceRestarter{
		goos:             "darwin",
		uid:              501,
		readinessTimeout: 200 * time.Millisecond,
		pollInterval:     5 * time.Millisecond,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			invoked = append(invoked, append([]string{name}, args...))
			if name == "launchctl" && len(args) == 1 && args[0] == "list" {
				return []byte("PID\tStatus\tLabel\n-\t0\tcom.marcinbak.ackbard\n"), nil
			}
			return nil, nil
		},
		probePort: func(addr string, timeout time.Duration) error {
			probes++
			if probes < 3 {
				return errors.New("connection refused")
			}
			return nil
		},
	}

	managed, ready, err := fast.restart()
	if !managed || !ready || err != nil {
		t.Fatalf("expected (true, true, nil) for fast launchd restart, got (%v, %v, %v)", managed, ready, err)
	}
	if len(invoked) != 2 || strings.Join(invoked[1], " ") != "launchctl kickstart -k gui/501/com.marcinbak.ackbard" {
		t.Fatalf("unexpected commands invoked: %v", invoked)
	}

	slow := &daemonServiceRestarter{
		goos:             "darwin",
		uid:              501,
		readinessTimeout: 25 * time.Millisecond,
		pollInterval:     5 * time.Millisecond,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			if name == "launchctl" && len(args) == 1 && args[0] == "list" {
				return []byte("PID\tStatus\tLabel\n-\t0\tcom.marcinbak.ackbard\n"), nil
			}
			return nil, nil
		},
		probePort: func(addr string, timeout time.Duration) error {
			return errors.New("connection refused")
		},
	}

	managed, ready, err = slow.restart()
	if !managed || ready || err != nil {
		t.Fatalf("expected (true, false, nil) for throttled launchd restart timeout, got (%v, %v, %v)", managed, ready, err)
	}

	failing := &daemonServiceRestarter{
		goos: "darwin",
		uid:  501,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			if name == "launchctl" && len(args) == 1 && args[0] == "list" {
				return []byte("PID\tStatus\tLabel\n-\t0\tcom.marcinbak.ackbard\n"), nil
			}
			return []byte("Could not find service \"com.marcinbak.ackbard\" in domain\n"), errors.New("exit status 113")
		},
		probePort: func(addr string, timeout time.Duration) error {
			return nil
		},
	}

	managed, ready, err = failing.restart()
	if !managed || ready || err == nil {
		t.Fatalf("expected (true, false, non-nil err) when kickstart fails, got (%v, %v, %v)", managed, ready, err)
	}
	if !strings.Contains(err.Error(), "Could not find service") {
		t.Fatalf("expected stderr detail in error, got: %v", err)
	}
}

func TestDaemonServiceRestarter_DarwinHomebrewAndUnmanaged(t *testing.T) {
	var capturedEnv []string
	var capturedCmd []string
	brewRestarter := &daemonServiceRestarter{
		goos:             "darwin",
		uid:              501,
		readinessTimeout: 50 * time.Millisecond,
		pollInterval:     5 * time.Millisecond,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			if name == "launchctl" {
				return []byte("PID\tStatus\tLabel\n123\t0\thomebrew.mxcl.ackbar\n"), nil
			}
			capturedEnv = env
			capturedCmd = append([]string{name}, args...)
			return nil, nil
		},
		probePort: func(addr string, timeout time.Duration) error {
			return nil
		},
		resolveBrew: func() string {
			return "/opt/homebrew/bin/brew"
		},
	}

	managed, ready, err := brewRestarter.restart()
	if !managed || !ready || err != nil {
		t.Fatalf("expected (true, true, nil) for homebrew service restart, got (%v, %v, %v)", managed, ready, err)
	}
	if strings.Join(capturedCmd, " ") != "/opt/homebrew/bin/brew services restart ackbar" {
		t.Fatalf("unexpected brew command: %v", capturedCmd)
	}
	hasBrewPath := false
	for _, kv := range capturedEnv {
		if strings.HasPrefix(kv, "PATH=/opt/homebrew/bin:/usr/local/bin:") {
			hasBrewPath = true
		}
	}
	if !hasBrewPath {
		t.Fatalf("expected brew PATH prefix in env, got %v", capturedEnv)
	}

	missingBrew := *brewRestarter
	missingBrew.resolveBrew = func() string { return "" }
	if m, r, bErr := missingBrew.restart(); !m || r || bErr == nil || !strings.Contains(bErr.Error(), "homebrew executable not found") {
		t.Fatalf("expected (true, false, missing brew err) when brew binary is missing, got (%v, %v, %v)", m, r, bErr)
	}

	unmanaged := &daemonServiceRestarter{
		goos: "darwin",
		uid:  501,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			return []byte("PID\tStatus\tLabel\n-\t0\tcom.marcinbak.ackbard.other\n"), nil
		},
		probePort: func(addr string, timeout time.Duration) error {
			return nil
		},
	}
	if m, r, uErr := unmanaged.restart(); m || r || uErr != nil {
		t.Fatalf("expected (false, false, nil) on unmanaged darwin host, got (%v, %v, %v)", m, r, uErr)
	}
}

func TestDaemonServiceRestarter_LinuxSystemd(t *testing.T) {
	var cmds []string
	enabled := &daemonServiceRestarter{
		goos:             "linux",
		readinessTimeout: 50 * time.Millisecond,
		pollInterval:     5 * time.Millisecond,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			cmds = append(cmds, name+" "+strings.Join(args, " "))
			return []byte("enabled\n"), nil
		},
		probePort: func(addr string, timeout time.Duration) error {
			return nil
		},
	}

	managed, ready, err := enabled.restart()
	if !managed || !ready || err != nil {
		t.Fatalf("expected (true, true, nil) for enabled systemd user service, got (%v, %v, %v)", managed, ready, err)
	}
	if len(cmds) != 2 || cmds[0] != "systemctl --user is-enabled ackbard.service" || cmds[1] != "systemctl --user restart ackbard.service" {
		t.Fatalf("unexpected systemd commands: %v", cmds)
	}

	linked := &daemonServiceRestarter{
		goos:             "linux",
		readinessTimeout: 50 * time.Millisecond,
		pollInterval:     5 * time.Millisecond,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[1] == "is-enabled" {
				return []byte("linked\n"), errors.New("exit status 1")
			}
			return nil, nil
		},
		probePort: func(addr string, timeout time.Duration) error {
			return nil
		},
	}
	if m, r, lErr := linked.restart(); !m || !r || lErr != nil {
		t.Fatalf("expected dotfile-linked systemd unit to be treated as managed, got (%v, %v, %v)", m, r, lErr)
	}

	activating := &daemonServiceRestarter{
		goos:             "linux",
		readinessTimeout: 50 * time.Millisecond,
		pollInterval:     5 * time.Millisecond,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[1] == "is-enabled" {
				return []byte("disabled\n"), errors.New("exit status 1")
			}
			if len(args) >= 2 && args[1] == "is-active" {
				return []byte("activating\n"), errors.New("exit status 3")
			}
			return nil, nil
		},
		probePort: func(addr string, timeout time.Duration) error {
			return nil
		},
	}
	if m, r, aErr := activating.restart(); !m || !r || aErr != nil {
		t.Fatalf("expected activating systemd unit to be treated as managed, got (%v, %v, %v)", m, r, aErr)
	}

	disabled := &daemonServiceRestarter{
		goos: "linux",
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[1] == "is-active" {
				return []byte("inactive\n"), errors.New("exit status 3")
			}
			return []byte("disabled\n"), errors.New("exit status 1")
		},
		probePort: func(addr string, timeout time.Duration) error {
			return nil
		},
	}
	if m, r, dErr := disabled.restart(); m || r || dErr != nil {
		t.Fatalf("expected (false, false, nil) when systemd service is disabled and inactive, got (%v, %v, %v)", m, r, dErr)
	}
}
