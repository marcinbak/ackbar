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
			return cmd.Output()
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

// tryRestartManagedDaemonService restarts ackbard via its OS supervisor if one owns the service.
// When managed is true, callers must never spawn an unmanaged process because KeepAlive/Restart=always
// will collide on 127.0.0.1:7777.
func tryRestartManagedDaemonService() (managed bool, ready bool) {
	return newDefaultDaemonServiceRestarter().restart()
}

func (r *daemonServiceRestarter) restart() (managed bool, ready bool) {
	var restartErr error

	switch r.goos {
	case "darwin":
		out, err := r.runCmd(nil, "launchctl", "list")
		if err != nil {
			return false, false
		}
		outStr := string(out)
		if hasLaunchctlLabel(outStr, "com.marcinbak.ackbard") {
			managed = true
			_, restartErr = r.runCmd(nil, "launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/com.marcinbak.ackbard", r.uid))
		} else if hasLaunchctlLabel(outStr, "homebrew.mxcl.ackbar") {
			managed = true
			brewBin := ""
			if r.resolveBrew != nil {
				brewBin = r.resolveBrew()
			}
			if brewBin == "" {
				return true, false
			}
			env := append(os.Environ(), "PATH=/opt/homebrew/bin:/usr/local/bin:"+os.Getenv("PATH"))
			_, restartErr = r.runCmd(env, brewBin, "services", "restart", "ackbar")
		} else {
			return false, false
		}
	case "linux":
		if _, err := r.runCmd(nil, "systemctl", "--user", "is-enabled", "ackbard.service"); err != nil {
			return false, false
		}
		managed = true
		_, restartErr = r.runCmd(nil, "systemctl", "--user", "restart", "ackbard.service")
	default:
		return false, false
	}

	if restartErr != nil {
		return true, false
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
		if err := r.probePort("127.0.0.1:7777", 100*time.Millisecond); err == nil {
			return true, true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return true, false
		}
		if remaining < interval {
			time.Sleep(remaining)
		} else {
			time.Sleep(interval)
		}
	}
}
