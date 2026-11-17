package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ackbar/internal/daemon"
	"ackbar/internal/version"
)

const (
	localDaemonBaseURL     = "http://127.0.0.1:7777"
	daemonProbeTimeout     = 2 * time.Second
	daemonReadyWaitTimeout = 5 * time.Second
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

func localDaemonAuthToken() string {
	return strings.TrimSpace(os.Getenv("ACKBAR_TOKEN"))
}

func newDaemonRequest(method, urlStr, token string) (*http.Request, error) {
	req, err := http.NewRequest(method, urlStr, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func probeDaemonVersion(client *http.Client, baseURL, token string) (ver, host, disp string, ok bool) {
	req, err := newDaemonRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/version", token)
	if err != nil {
		return "", "", "", false
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", false
	}
	var verRes map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&verRes); err != nil {
		return "", "", "", false
	}
	runningVer, _ := verRes["version"].(string)
	if h, ok := verRes["host"].(string); ok && validHostNameRegex.MatchString(h) {
		host = h
	}
	if d, ok := verRes["display_name"].(string); ok && isValidDisplayName(d) {
		disp = d
	}
	return runningVer, host, disp, runningVer != ""
}

func requestDaemonDrainShutdown(client *http.Client, baseURL, token string) (deferred bool, unauthorized bool, err error) {
	req, err := newDaemonRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/shutdown?drain=true", token)
	if err != nil {
		return false, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, true, nil
	}
	if resp.StatusCode == http.StatusOK {
		var sData struct {
			RestartDeferred bool `json:"restart_deferred"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&sData); err == nil {
			return sData.RestartDeferred, false, nil
		}
	}
	return false, false, nil
}

func waitForDaemonReady(baseURL, token, expectedVersion string, timeout time.Duration) bool {
	client := &http.Client{Timeout: daemonProbeTimeout}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ver, _, _, ok := probeDaemonVersion(client, baseURL, token); ok {
			if expectedVersion == "" || ver == expectedVersion {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func spawnLocalAckbardDaemon(runningHost, runningDisplayName string) {
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

func ensureDaemonRunning() {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		_, _ = daemon.EnsureServiceProcessIsolation(home)
	}
	ensureDaemonRunningWith(localDaemonBaseURL, localDaemonAuthToken(), version.Version, tryRestartManagedDaemonService, spawnLocalAckbardDaemon)
}

func ensureDaemonRunningWith(baseURL, token, targetVersion string, restartManaged func() (managed bool, ready bool, err error), spawnFn func(runningHost, runningDisplayName string)) {
	httpClient := &http.Client{Timeout: daemonProbeTimeout}
	runningVer, runningHost, runningDisplayName, ok := probeDaemonVersion(httpClient, baseURL, token)
	if ok {
		if runningVer == targetVersion {
			return
		}
		fmt.Fprintf(os.Stderr, "🔄 Outdated daemon detected (running v%s, client v%s). Restarting daemon...\n", runningVer, targetVersion)
		deferred, unauth, _ := requestDaemonDrainShutdown(httpClient, baseURL, token)
		if unauth {
			fmt.Fprintln(os.Stderr, "⚠️ Local ackbard requires ACKBAR_TOKEN to restart automatically; restart ackbard manually.")
			return
		}
		if deferred {
			fmt.Fprintln(os.Stderr, "⏳ Active headless session turn(s) in progress — ackbard will restart automatically once the current turn finishes.")
			return
		}
		time.Sleep(300 * time.Millisecond)
	}

	fmt.Fprintln(os.Stderr, "🚀 Local ackbard daemon starting in background...")

	managed, ready, restartErr := restartManaged()
	if !managed {
		spawnFn(runningHost, runningDisplayName)
		_ = waitForDaemonReady(baseURL, token, targetVersion, 3*time.Second)
		return
	}
	if restartErr != nil {
		fmt.Fprintf(os.Stderr, "⚠️ Managed ackbard service restart failed: %v\n", restartErr)
		return
	}
	if !ready {
		fmt.Fprintln(os.Stderr, "⚠️ Managed ackbard service (launchd/systemd) is still starting on 127.0.0.1:7777...")
		_ = waitForDaemonReady(baseURL, token, targetVersion, 3*time.Second)
	}
}
