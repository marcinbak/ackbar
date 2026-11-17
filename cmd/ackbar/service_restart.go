package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type daemonServiceRestarter struct {
	goos             string
	uid              int
	readinessTimeout time.Duration
	pollInterval     time.Duration
	runCmd           func(env []string, name string, args ...string) ([]byte, error)
	probePort        func(addr string, timeout time.Duration) error
	resolveBrew      func() string
}

func newDefaultDaemonServiceRestarter() *daemonServiceRestarter {
	return &daemonServiceRestarter{
		goos:             runtime.GOOS,
		uid:              os.Getuid(),
		readinessTimeout: 5 * time.Second,
		pollInterval:     100 * time.Millisecond,
		runCmd: func(env []string, name string, args ...string) ([]byte, error) {
			cmd := exec.Command(name, args...)
			if len(env) > 0 {
				cmd.Env = env
			}
			return cmd.CombinedOutput()
		},
		probePort: func(addr string, timeout time.Duration) error {
			conn, err := net.DialTimeout("tcp", addr, timeout)
			if err != nil {
				return err
			}
			_ = conn.Close()
			return nil
		},
		resolveBrew: resolveBrewExecutable,
	}
}

func resolveBrewExecutable() string {
	for _, candidate := range []string{
		"/opt/homebrew/bin/brew",
		"/usr/local/bin/brew",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if p, err := exec.LookPath("brew"); err == nil {
		return p
	}
	return ""
}

func hasLaunchctlLabel(outStr, label string) bool {
	if label == "" {
		return false
	}
	for _, line := range strings.Split(outStr, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			if fields[0] == "PID" && fields[1] == "Status" && fields[2] == "Label" {
				continue
			}
			if fields[2] == label {
				return true
			}
			continue
		}
		for _, tok := range fields {
			if tok == label {
				return true
			}
		}
	}
	return false
}

func isManagedSystemdEnabledState(out string) bool {
	switch strings.TrimSpace(out) {
	case "enabled", "enabled-runtime", "linked", "linked-runtime", "static":
		return true
	}
	return false
}

func isManagedSystemdActiveState(out string) bool {
	switch strings.TrimSpace(out) {
	case "active", "activating", "reloading":
		return true
	}
	return false
}

func (r *daemonServiceRestarter) isLinuxSystemdManaged() bool {
	out, err := r.runCmd(nil, "systemctl", "--user", "is-enabled", "ackbard.service")
	if err == nil || isManagedSystemdEnabledState(string(out)) {
		return true
	}
	activeOut, _ := r.runCmd(nil, "systemctl", "--user", "is-active", "ackbard.service")
	return isManagedSystemdActiveState(string(activeOut))
}

// tryRestartManagedDaemonService restarts ackbard via its OS supervisor if one owns the service.
// When managed is true, callers must never spawn an unmanaged process because KeepAlive/Restart=always
// will collide on 127.0.0.1:7777.
func tryRestartManagedDaemonService() (managed bool, ready bool, err error) {
	return newDefaultDaemonServiceRestarter().restart()
}

func (r *daemonServiceRestarter) restart() (managed bool, ready bool, err error) {
	var restartOut []byte
	var restartErr error

	switch r.goos {
	case "darwin":
		out, listErr := r.runCmd(nil, "launchctl", "list")
		if listErr != nil {
			return false, false, nil
		}
		outStr := string(out)
		if hasLaunchctlLabel(outStr, "com.marcinbak.ackbard") {
			managed = true
			restartOut, restartErr = r.runCmd(nil, "launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/com.marcinbak.ackbard", r.uid))
			if restartErr != nil {
				if userOut, userErr := r.runCmd(nil, "launchctl", "kickstart", "-k", fmt.Sprintf("user/%d/com.marcinbak.ackbard", r.uid)); userErr == nil {
					restartOut, restartErr = nil, nil
				} else {
					restartOut = []byte(strings.TrimSpace(strings.TrimSpace(string(restartOut)) + "; " + strings.TrimSpace(string(userOut))))
					restartErr = userErr
				}
			}
		} else if hasLaunchctlLabel(outStr, "homebrew.mxcl.ackbar") {
			managed = true
			brewBin := ""
			if r.resolveBrew != nil {
				brewBin = r.resolveBrew()
			}
			if brewBin == "" {
				return true, false, fmt.Errorf("homebrew executable not found to restart homebrew.mxcl.ackbar")
			}
			env := append(os.Environ(), "PATH=/opt/homebrew/bin:/usr/local/bin:"+os.Getenv("PATH"))
			restartOut, restartErr = r.runCmd(env, brewBin, "services", "restart", "ackbar")
		} else {
			return false, false, nil
		}
	case "linux":
		if !r.isLinuxSystemdManaged() {
			return false, false, nil
		}
		managed = true
		restartOut, restartErr = r.runCmd(nil, "systemctl", "--user", "restart", "ackbard.service")
	default:
		return false, false, nil
	}

	if restartErr != nil {
		if msg := strings.TrimSpace(string(restartOut)); msg != "" && msg != ";" {
			return true, false, fmt.Errorf("service restart failed: %w (%s)", restartErr, msg)
		}
		return true, false, fmt.Errorf("service restart failed: %w", restartErr)
	}

	timeout := r.readinessTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	interval := r.pollInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}

	deadline := time.Now().Add(timeout)
	for {
		if probeErr := r.probePort("127.0.0.1:7777", 100*time.Millisecond); probeErr == nil {
			return true, true, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return true, false, nil
		}
		if remaining < interval {
			time.Sleep(remaining)
		} else {
			time.Sleep(interval)
		}
	}
}
