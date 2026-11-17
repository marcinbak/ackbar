package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"ackbar/internal/daemon"
	"ackbar/internal/version"
)

var validHostNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func isValidDisplayName(d string) bool {
	if d == "" {
		return false
	}
	for _, r := range d {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func hasPersistedHostIdentity(home string) bool {
	if home == "" {
		return false
	}
	dbPath := filepath.Join(home, ".config", "ackbar", "ackbard.db")
	if _, err := os.Stat(dbPath); err != nil {
		return false
	}
	val := daemon.ReadPersistedHostName(dbPath)
	return val != "" && !strings.EqualFold(val, "local")
}

func buildDaemonRestartArgs(home, runningHost, runningDisplayName string) []string {
	if hasPersistedHostIdentity(home) {
		return nil
	}
	var args []string
	if runningHost != "" && !strings.EqualFold(runningHost, "local") && validHostNameRegex.MatchString(runningHost) {
		args = append(args, "--host-name", runningHost)
	}
	if runningDisplayName != "" && !strings.EqualFold(runningDisplayName, "local") && isValidDisplayName(runningDisplayName) {
		args = append(args, "--display-name", runningDisplayName)
	}
	return args
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

func tryRestartDarwinLaunchdService() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return false
	}
	outStr := string(out)
	var started bool
	if strings.Contains(outStr, "com.marcinbak.ackbard") {
		err := exec.Command("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/com.marcinbak.ackbard", os.Getuid())).Run()
		started = err == nil
	} else if strings.Contains(outStr, "homebrew.mxcl.ackbar") {
		brewBin := resolveBrewExecutable()
		if brewBin == "" {
			return false
		}
		cmd := exec.Command(brewBin, "services", "restart", "ackbar")
		cmd.Env = append(os.Environ(), "PATH=/opt/homebrew/bin:/usr/local/bin:"+os.Getenv("PATH"))
		err := cmd.Run()
		started = err == nil
	}
	if !started {
		return false
	}
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if conn, err := net.DialTimeout("tcp", "127.0.0.1:7777", 100*time.Millisecond); err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

func ensureDaemonRunning() {
	token := os.Getenv("ACKBAR_TOKEN")
	httpClient := &http.Client{Timeout: 500 * time.Millisecond}
	vReq, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:7777/v1/version", nil)
	if token != "" {
		vReq.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(vReq)
	var runningHost string
	var runningDisplayName string
	if err == nil {
		var verRes map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&verRes); err == nil {
			resp.Body.Close()
			runningVer, _ := verRes["version"].(string)
			if h, ok := verRes["host"].(string); ok && validHostNameRegex.MatchString(h) {
				runningHost = h
			}
			if d, ok := verRes["display_name"].(string); ok && isValidDisplayName(d) {
				runningDisplayName = d
			}
			if runningVer == version.Version {
				return
			}
			fmt.Printf("🔄 Outdated daemon detected (running v%s, client v%s). Restarting daemon...\n", runningVer, version.Version)
			sReq, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:7777/v1/shutdown", nil)
			sReq.Header.Set("Content-Type", "application/json")
			if token != "" {
				sReq.Header.Set("Authorization", "Bearer "+token)
			}
			_, _ = httpClient.Do(sReq)
			time.Sleep(300 * time.Millisecond)
		} else {
			resp.Body.Close()
		}
	}

	fmt.Println("🚀 Local ackbard daemon starting in background...")

	if !tryRestartDarwinLaunchdService() {
		home, _ := os.UserHomeDir()
		ackbardBin := "ackbard"
		if home != "" {
			localBin := filepath.Join(home, ".local", "bin", "ackbard")
			if _, err := os.Stat(localBin); err == nil {
				ackbardBin = localBin
			}
		}

		args := buildDaemonRestartArgs(home, runningHost, runningDisplayName)
		cmd := exec.Command(ackbardBin, args...)
		if err := cmd.Start(); err != nil {
			goArgs := append([]string{"run", "./cmd/ackbard"}, args...)
			cmdRun := exec.Command("go", goArgs...)
			_ = cmdRun.Start()
		}
	}

	// Poll up to 3 seconds for daemon port 7777 to become ready
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		resp, err := httpClient.Get("http://127.0.0.1:7777/v1/version")
		if err == nil {
			var verRes map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&verRes); err == nil {
				resp.Body.Close()
				if verRes["version"] == version.Version {
					return
				}
			} else {
				resp.Body.Close()
			}
		}
	}
}
