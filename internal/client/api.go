package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"ackbar/internal/daemon"
	"ackbar/internal/version"
)

type HostConfig struct {
	Name        string `json:"name"`
	URL         string `json:"url"`                   // e.g. "http://127.0.0.1:7777"
	ProjectsDir string `json:"projects_dir,omitempty"` // e.g. "~/Projects"
}

func SaveHostConfig(path string, hosts []HostConfig) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = make(map[string]interface{})
	}
	cfg["hosts"] = hosts
	newData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, newData, 0644)
}

type HostStatus struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	ProjectsDir string `json:"projects_dir"`
	Version     string `json:"version"`
	Online      bool   `json:"online"`
	ErrorStage  string `json:"error_stage,omitempty"`
	Error       string `json:"error,omitempty"`
}

// FetchSessions retrieves all sessions from all configured daemons and returns host connection statuses
func FetchSessions(hosts []HostConfig) ([]*daemon.Session, map[string]HostStatus, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allSessions []*daemon.Session
	var errors []string
	hostStatuses := make(map[string]HostStatus)

	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	for _, h := range hosts {
		wg.Add(1)
		go func(host HostConfig) {
			defer wg.Done()

			// Query host daemon version
			verURL := fmt.Sprintf("%s/v1/version", strings.TrimSuffix(host.URL, "/"))
			verResp, verErr := client.Get(verURL)
			hostVer := "unknown"
			if verErr == nil && verResp.StatusCode == http.StatusOK {
				var verMap map[string]string
				if err := json.NewDecoder(verResp.Body).Decode(&verMap); err == nil {
					if v, ok := verMap["version"]; ok && v != "" {
						hostVer = v
					}
				}
				verResp.Body.Close()
			}

			// Auto-upgrade remote daemon if running outdated version
			if hostVer != "unknown" && hostVer != version.Version && host.Name != "local" {
				go func(h HostConfig) {
					shutdownURL := fmt.Sprintf("%s/v1/shutdown", strings.TrimSuffix(h.URL, "/"))
					_, _ = client.Post(shutdownURL, "application/json", nil)
					time.Sleep(200 * time.Millisecond)

					osOut, _ := exec.Command("ssh", "-o", "BatchMode=yes", h.Name, "uname -s").Output()
					archOut, _ := exec.Command("ssh", "-o", "BatchMode=yes", h.Name, "uname -m").Output()
					remoteOS := strings.ToLower(strings.TrimSpace(string(osOut)))
					if remoteOS == "" {
						remoteOS = "linux"
					}
					remoteArchRaw := strings.TrimSpace(string(archOut))
					remoteArch := "amd64"
					if strings.Contains(remoteArchRaw, "aarch64") || strings.Contains(remoteArchRaw, "arm64") {
						remoteArch = "arm64"
					}

					tmpBard := filepath.Join(os.TempDir(), fmt.Sprintf("ackbard-%s-%s", remoteOS, remoteArch))
					tmpHook := filepath.Join(os.TempDir(), fmt.Sprintf("ackbar-hook-%s-%s", remoteOS, remoteArch))

					cmdBard := exec.Command("go", "build", "-o", tmpBard, "./cmd/ackbard")
					cmdBard.Env = append(os.Environ(), "GOOS="+remoteOS, "GOARCH="+remoteArch, "CGO_ENABLED=0")
					_ = cmdBard.Run()

					cmdHook := exec.Command("go", "build", "-o", tmpHook, "./cmd/ackbar-hook")
					cmdHook.Env = append(os.Environ(), "GOOS="+remoteOS, "GOARCH="+remoteArch, "CGO_ENABLED=0")
					_ = cmdHook.Run()

					_ = exec.Command("ssh", "-o", "BatchMode=yes", h.Name, "mkdir -p ~/.local/bin").Run()
					if _, err := os.Stat(tmpBard); err == nil {
						_ = exec.Command("scp", "-o", "BatchMode=yes", tmpBard, fmt.Sprintf("%s:~/.local/bin/ackbard", h.Name)).Run()
						_ = os.Remove(tmpBard)
					}
					if _, err := os.Stat(tmpHook); err == nil {
						_ = exec.Command("scp", "-o", "BatchMode=yes", tmpHook, fmt.Sprintf("%s:~/.local/bin/ackbar-hook", h.Name)).Run()
						_ = os.Remove(tmpHook)
					}

					remoteSetupScript := `export PATH=$PATH:$HOME/.local/bin:$HOME/go/bin
chmod +x ~/.local/bin/ackbard ~/.local/bin/ackbar-hook 2>/dev/null
python3 -c '
import json, os
home = os.path.expanduser("~")

# 1. Claude Code Hooks
claude_hook = os.path.join(home, ".local", "bin", "ackbar-hook") + " claude-code "
claude_events = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Notification", "Stop"]
claude_obj = {ev: [{"matcher": "", "hooks": [{"type": "command", "command": claude_hook + ev}]}] for ev in claude_events}
for p in [os.path.join(home, ".claude", "settings.json"), os.path.join(home, ".claude.json")]:
    os.makedirs(os.path.dirname(p), exist_ok=True)
    cfg = {}
    if os.path.exists(p):
        try:
            with open(p) as f: cfg = json.load(f)
        except: pass
    cfg["hooks"] = claude_obj
    with open(p, "w") as f: json.dump(cfg, f, indent=2)

# 2. Antigravity Hooks
agy_hook = os.path.join(home, ".local", "bin", "ackbar-hook") + " antigravity "
agy_events = ["PreInvocation", "PostToolUse", "PermissionRequest", "PostInvocation"]
agy_obj = {ev: [{"command": agy_hook + ev}] for ev in agy_events}
for p in [os.path.join(home, ".gemini", "config", "hooks.json"), os.path.join(home, ".antigravity", "config", "hooks.json")]:
    os.makedirs(os.path.dirname(p), exist_ok=True)
    cfg = {}
    if os.path.exists(p):
        try:
            with open(p) as f: cfg = json.load(f)
        except: pass
    cfg["hooks"] = agy_obj
    with open(p, "w") as f: json.dump(cfg, f, indent=2)
'
pkill ackbard 2>/dev/null || true
nohup ~/.local/bin/ackbard > ~/.ackbard.log 2>&1 &`
					_ = exec.Command("ssh", "-o", "BatchMode=yes", h.Name, remoteSetupScript).Run()
				}(host)
			}

			url := fmt.Sprintf("%s/v1/sessions", strings.TrimSuffix(host.URL, "/"))
			resp, err := client.Get(url)
			if err != nil {
				errorStage := "HTTP Endpoint"
				if host.Name != "local" {
					sshErr := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=3", host.Name, "echo ok").Run()
					if sshErr != nil {
						errorStage = "Stage 1: Passwordless SSH"
					} else {
						daemonOut, daemonErr := exec.Command("ssh", "-o", "BatchMode=yes", host.Name, "curl -s http://127.0.0.1:7777/v1/version").Output()
						if daemonErr != nil || !strings.Contains(string(daemonOut), "version") {
							errorStage = "Stage 2: Remote ackbard Daemon"
						} else {
							// SSH and remote daemon are active! Self-heal the SSH port-forward tunnel
							var localPort int
							fmt.Sscanf(strings.TrimPrefix(host.URL, "http://127.0.0.1:"), "%d", &localPort)
							if localPort > 0 {
								_ = exec.Command("pkill", "-f", fmt.Sprintf("-L %d:127.0.0.1:7777", localPort)).Run()
								_ = exec.Command("ssh", "-f", "-N", "-o", "ExitOnForwardFailure=yes", "-L", fmt.Sprintf("%d:127.0.0.1:7777", localPort), host.Name).Run()
								time.Sleep(200 * time.Millisecond)
								if resp2, err2 := client.Get(url); err2 == nil {
									resp = resp2
									err = nil
								}
							}
							if err != nil {
								errorStage = "Stage 3: Local SSH Tunnel"
							}
						}
					}
				}

				mu.Lock()
				hostStatuses[host.Name] = HostStatus{
					Name:        host.Name,
					URL:         host.URL,
					ProjectsDir: host.ProjectsDir,
					Version:     hostVer,
					Online:      false,
					ErrorStage:  errorStage,
					Error:       err.Error(),
				}
				errors = append(errors, fmt.Sprintf("[%s] Failed to reach host %s (%s): %v", errorStage, host.Name, host.URL, err))
				mu.Unlock()
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				mu.Lock()
				hostStatuses[host.Name] = HostStatus{
					Name:        host.Name,
					URL:         host.URL,
					ProjectsDir: host.ProjectsDir,
					Version:     hostVer,
					Online:      false,
					Error:       fmt.Sprintf("Status %d", resp.StatusCode),
				}
				errors = append(errors, fmt.Sprintf("Host %s returned status %d", host.Name, resp.StatusCode))
				mu.Unlock()
				return
			}

			var sessions []*daemon.Session
			if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
				mu.Lock()
				hostStatuses[host.Name] = HostStatus{
					Name:        host.Name,
					URL:         host.URL,
					ProjectsDir: host.ProjectsDir,
					Version:     hostVer,
					Online:      false,
					Error:       err.Error(),
				}
				errors = append(errors, fmt.Sprintf("Failed to decode sessions from host %s: %v", host.Name, err))
				mu.Unlock()
				return
			}

			// Ensure the host field is correctly set to our configured host alias
			for _, s := range sessions {
				s.Host = host.Name
			}

			mu.Lock()
			hostStatuses[host.Name] = HostStatus{
				Name:        host.Name,
				URL:         host.URL,
				ProjectsDir: host.ProjectsDir,
				Version:     hostVer,
				Online:      true,
			}
			allSessions = append(allSessions, sessions...)
			mu.Unlock()
		}(h)
	}

	wg.Wait()

	return allSessions, hostStatuses, nil
}

// SubscribeEvents connects to SSE v1/events endpoint for each host
// and writes received session updates to the channel.
func SubscribeEvents(ctx context.Context, hosts []HostConfig, ch chan<- *daemon.Session) {
	for _, h := range hosts {
		go func(host HostConfig) {
			url := fmt.Sprintf("%s/v1/events", strings.TrimSuffix(host.URL, "/"))
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
				if err != nil {
					time.Sleep(2 * time.Second)
					continue
				}

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					// wait and retry connection
					time.Sleep(2 * time.Second)
					continue
				}

				reader := bufio.NewReader(resp.Body)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						resp.Body.Close()
						break
					}

					line = strings.TrimSpace(line)
					if !strings.HasPrefix(line, "data: ") {
						continue
					}

					dataStr := strings.TrimPrefix(line, "data: ")
					var s daemon.Session
					if err := json.Unmarshal([]byte(dataStr), &s); err == nil {
						s.Host = host.Name
						select {
						case ch <- &s:
						case <-ctx.Done():
							resp.Body.Close()
							return
						}
					}
				}
				time.Sleep(1 * time.Second)
			}
		}(h)
	}
}

// RunAction executes session control endpoints on the daemon
func RunAction(hostURL, action, sessionID string) error {
	reqURL := fmt.Sprintf("%s/v1/sessions/control?id=%s&action=%s",
		strings.TrimSuffix(hostURL, "/"),
		url.QueryEscape(sessionID),
		action)
	resp, err := http.Post(reqURL, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("action %s failed (%d): %s", action, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// FetchDocuments gets the list of relevant workspace documents for a session
func FetchDocuments(hostURL, sessionID string) ([]string, error) {
	nativeID := sessionID
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		nativeID = parts[2]
	}

	url := fmt.Sprintf("%s/v1/sessions/%s/documents", strings.TrimSuffix(hostURL, "/"), nativeID)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch documents failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var docs []string
	if err := json.NewDecoder(resp.Body).Decode(&docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func FetchAgentDiscovery(hostURL string) ([]daemon.AgentDiscoveryResult, error) {
	reqURL := fmt.Sprintf("%s/v1/agents/discovery", strings.TrimSuffix(hostURL, "/"))
	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("discovery request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var results []daemon.AgentDiscoveryResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, err
	}
	return results, nil
}

func FetchAllAgentDiscovery(hosts []HostConfig) map[string][]daemon.AgentDiscoveryResult {
	res := make(map[string][]daemon.AgentDiscoveryResult)
	var wg sync.WaitGroup
	var mu sync.Mutex

	client := &http.Client{Timeout: 3 * time.Second}

	for _, h := range hosts {
		wg.Add(1)
		go func(host HostConfig) {
			defer wg.Done()
			reqURL := fmt.Sprintf("%s/v1/agents/discovery", strings.TrimSuffix(host.URL, "/"))
			resp, err := client.Get(reqURL)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var list []daemon.AgentDiscoveryResult
				if err := json.NewDecoder(resp.Body).Decode(&list); err == nil {
					mu.Lock()
					res[host.Name] = list
					mu.Unlock()
				}
			}
		}(h)
	}
	wg.Wait()
	return res
}

func FetchNodes(hostURL string) ([]*daemon.TreeNode, error) {
	reqURL := fmt.Sprintf("%s/v1/nodes", strings.TrimSuffix(hostURL, "/"))
	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch nodes failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var nodes []*daemon.TreeNode
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func CreateProject(hostURL, path, name, gitURL, baseDir string) error {
	payload := map[string]string{
		"path":     path,
		"name":     name,
		"git_url":  gitURL,
		"base_dir": baseDir,
	}
	body, _ := json.Marshal(payload)
	reqURL := fmt.Sprintf("%s/v1/projects/create", strings.TrimSuffix(hostURL, "/"))
	resp, err := http.Post(reqURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create project failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func MoveNode(hostURL, oldPath, newPath string) error {
	payload := map[string]string{
		"old_path": oldPath,
		"new_path": newPath,
	}
	body, _ := json.Marshal(payload)
	reqURL := fmt.Sprintf("%s/v1/nodes/move", strings.TrimSuffix(hostURL, "/"))
	resp, err := http.Post(reqURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("move node failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func MoveSession(hostURL, sessionID, newPath string) error {
	reqURL := fmt.Sprintf("%s/v1/sessions/control?id=%s&action=move&node_path=%s",
		strings.TrimSuffix(hostURL, "/"),
		url.QueryEscape(sessionID),
		url.QueryEscape(newPath),
	)
	resp, err := http.Post(reqURL, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("move session failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func DeleteNode(hostURL, path string) error {
	reqURL := fmt.Sprintf("%s/v1/nodes?path=%s",
		strings.TrimSuffix(hostURL, "/"),
		url.QueryEscape(path))
	req, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete node failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func PurgeDaemonSessions(hostURL string) error {
	reqURL := fmt.Sprintf("%s/v1/maintenance/purge", strings.TrimSuffix(hostURL, "/"))
	resp, err := http.Post(reqURL, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("purge failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func SaveGroupConfig(path string, groups map[string][]string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = make(map[string]interface{})
	}
	cfg["groups"] = groups
	newData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, newData, 0644)
}
