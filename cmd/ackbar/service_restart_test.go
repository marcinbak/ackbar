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

func TestDaemonServiceRestarter_DarwinLaunchAgentFastAndSlow(t *testing.T) {
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

	managed, ready := fast.restart()
	if !managed || !ready {
		t.Fatalf("expected (true, true) for fast launchd restart, got (%v, %v)", managed, ready)
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

	managed, ready = slow.restart()
	if !managed || ready {
		t.Fatalf("expected (true, false) for throttled launchd restart timeout, got (%v, %v)", managed, ready)
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

	managed, ready := brewRestarter.restart()
	if !managed || !ready {
		t.Fatalf("expected (true, true) for homebrew service restart, got (%v, %v)", managed, ready)
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
	if m, r := missingBrew.restart(); !m || r {
		t.Fatalf("expected (true, false) when homebrew.mxcl.ackbar is loaded but brew binary is missing, got (%v, %v)", m, r)
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
	if m, r := unmanaged.restart(); m || r {
		t.Fatalf("expected (false, false) on unmanaged darwin host, got (%v, %v)", m, r)
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

	managed, ready := enabled.restart()
	if !managed || !ready {
		t.Fatalf("expected (true, true) for enabled systemd user service, got (%v, %v)", managed, ready)
	}
	if len(cmds) != 2 || cmds[0] != "systemctl --user is-enabled ackbard.service" || cmds[1] != "systemctl --user restart ackbard.service" {
		t.Fatalf("unexpected systemd commands: %v", cmds)
	}

	disabled := &daemonServiceRestarter{
		goos: "linux",
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			return nil, errors.New("disabled")
		},
		probePort: func(addr string, timeout time.Duration) error {
			return nil
		},
	}
	if m, r := disabled.restart(); m || r {
		t.Fatalf("expected (false, false) when systemd service is disabled, got (%v, %v)", m, r)
	}
}
