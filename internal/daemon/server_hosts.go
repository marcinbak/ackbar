package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ackbar/internal/version"
)

func (s *Server) updateHostHealth(name string, online bool, ver, displayName string, latencyMs int64) {
	s.hostHealthMu.Lock()
	defer s.hostHealthMu.Unlock()
	s.hostHealthMap[name] = &HostHealth{
		Online:      online,
		Version:     ver,
		DisplayName: displayName,
		LatencyMs:   latencyMs,
		LastCheck:   time.Now(),
	}
}

// resolveSSHTarget resolves a host identifier to its configured SSH target or alias
func (s *Server) resolveSSHTarget(host string) string {
	if s.db != nil {
		if hosts, err := s.db.ListHosts(); err == nil {
			for _, h := range hosts {
				if h.Name == host || h.SSHTarget == host {
					if h.SSHTarget != "" {
						return h.SSHTarget
					}
					return h.Name
				}
				parts := strings.Split(h.Name, "@")
				if parts[len(parts)-1] == host {
					if h.SSHTarget != "" {
						return h.SSHTarget
					}
					return h.Name
				}
			}
		}
	}
	return host
}

func (s *Server) resolveHost(hostName string) *HostRecord {
	if hostName == "" || hostName == "local" || hostName == s.HostName() || s.db == nil {
		return nil
	}
	hostRec, err := s.db.GetHost(hostName)
	if err == nil && hostRec != nil {
		return hostRec
	}
	if allHosts, err := s.db.ListHosts(); err == nil {
		for _, h := range allHosts {
			if h.Name == hostName || strings.HasSuffix(h.Name, "@"+hostName) || strings.Contains(h.Name, hostName) {
				return h
			}
		}
	}
	return nil
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		hosts, err := s.db.ListHosts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.hostHealthMu.RLock()
		for _, h := range hosts {
			if health, ok := s.hostHealthMap[h.Name]; ok {
				h.Online = health.Online
				h.Version = health.Version
				h.DisplayName = health.DisplayName
				h.LatencyMs = health.LatencyMs
			}
		}
		s.hostHealthMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hosts)

	case http.MethodPost:
		var h HostRecord
		if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		if h.Name == "" {
			http.Error(w, "Missing host name", http.StatusBadRequest)
			return
		}
		h.CreatedAt = time.Now()
		if err := s.db.SaveHost(&h); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"saved"}`))

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "Missing name parameter", http.StatusBadRequest)
			return
		}
		if err := s.db.DeleteHost(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"deleted"}`))

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHostUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hostName := r.URL.Query().Get("name")
	if hostName == "" {
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		hostName = req.Name
	}
	if hostName == "" {
		http.Error(w, "Missing host name", http.StatusBadRequest)
		return
	}

	if hostName == "local" || hostName == s.HostName() {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "Local host daemon is managed directly on this machine (run 'ackbar setup-hooks')",
		})
		return
	}

	host, err := s.db.GetHost(hostName)
	if err != nil || host == nil {
		if allHosts, lerr := s.db.ListHosts(); lerr == nil {
			for _, h := range allHosts {
				if h.Name == hostName || h.SSHTarget == hostName || strings.EqualFold(h.Name, hostName) || strings.Contains(strings.ToLower(h.Name), strings.ToLower(hostName)) {
					host = h
					break
				}
			}
		}
	}
	if host == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Host '%s' not registered", hostName),
		})
		return
	}

	sshTarget := host.SSHTarget
	if sshTarget == "" {
		sshTarget = host.Name
	}

	// 1. Detect target OS and Architecture via SSH
	detectCmd := exec.Command("ssh", "-o", "ConnectTimeout=8", "-o", "BatchMode=yes", sshTarget, "uname -s && uname -m")
	out, err := detectCmd.CombinedOutput()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("SSH connection to '%s' failed: %s (%v)", sshTarget, strings.TrimSpace(string(out)), err),
		})
		return
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Could not parse remote OS/architecture: %q", string(out)),
		})
		return
	}

	targetOSRaw := strings.ToLower(strings.TrimSpace(lines[0]))
	targetArchRaw := strings.ToLower(strings.TrimSpace(lines[1]))

	goos := "linux"
	if strings.Contains(targetOSRaw, "darwin") {
		goos = "darwin"
	} else if strings.Contains(targetOSRaw, "freebsd") {
		goos = "freebsd"
	}

	goarch := "amd64"
	if strings.Contains(targetArchRaw, "aarch64") || strings.Contains(targetArchRaw, "arm64") {
		goarch = "arm64"
	} else if strings.Contains(targetArchRaw, "arm") {
		goarch = "arm"
	} else if strings.Contains(targetArchRaw, "i386") || strings.Contains(targetArchRaw, "686") {
		goarch = "386"
	}

	// 2. Find Go project source directory
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "Work/Ackbar"),
		filepath.Join(home, "src/Ackbar"),
		filepath.Join(home, "Projects/Ackbar"),
		".",
	}
	srcDir := ""
	for _, c := range candidates {
		if _, serr := os.Stat(filepath.Join(c, "cmd/ackbard/main.go")); serr == nil {
			srcDir = c
			break
		}
	}
	if srcDir == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "Ackbar source directory not found to compile binaries",
		})
		return
	}

	// 3. Create temporary directory and cross-compile
	tmpDir, err := os.MkdirTemp("", "ackbar-remote-update-*")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create temp build dir: %v", err), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	ackbardPath := filepath.Join(tmpDir, "ackbard")
	hookPath := filepath.Join(tmpDir, "ackbar-hook")

	cmdBuild1 := exec.Command("go", "build", "-ldflags=-s -w", "-o", ackbardPath, "./cmd/ackbard")
	cmdBuild1.Dir = srcDir
	cmdBuild1.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if bout, berr := cmdBuild1.CombinedOutput(); berr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Failed to cross-compile ackbard for %s/%s: %s", goos, goarch, string(bout)),
		})
		return
	}

	cmdBuild2 := exec.Command("go", "build", "-ldflags=-s -w", "-o", hookPath, "./cmd/ackbar-hook")
	cmdBuild2.Dir = srcDir
	cmdBuild2.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if bout, berr := cmdBuild2.CombinedOutput(); berr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Failed to cross-compile ackbar-hook for %s/%s: %s", goos, goarch, string(bout)),
		})
		return
	}

	// 4. Deploy binaries to remote host via SCP using atomic staging files
	_ = exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "mkdir -p ~/.local/bin").Run()

	scpCmd1 := exec.Command("scp", "-o", "BatchMode=yes", ackbardPath, fmt.Sprintf("%s:~/.local/bin/ackbard.new", sshTarget))
	if sout, serr := scpCmd1.CombinedOutput(); serr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Failed to upload ackbard to %s: %s", sshTarget, string(sout)),
		})
		return
	}

	scpCmd2 := exec.Command("scp", "-o", "BatchMode=yes", hookPath, fmt.Sprintf("%s:~/.local/bin/ackbar-hook.new", sshTarget))
	if sout, serr := scpCmd2.CombinedOutput(); serr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Failed to upload ackbar-hook to %s: %s", sshTarget, string(sout)),
		})
		return
	}

	// 5. Atomically replace binaries, set permissions, and restart daemon on remote host
	remoteRestartScript := `chmod +x ~/.local/bin/ackbard.new ~/.local/bin/ackbar-hook.new && mv -f ~/.local/bin/ackbard.new ~/.local/bin/ackbard && mv -f ~/.local/bin/ackbar-hook.new ~/.local/bin/ackbar-hook && pkill -9 -x ackbard || true; sleep 0.5; nohup ~/.local/bin/ackbard >/dev/null 2>&1 < /dev/null &`
	restartCmd := exec.Command("ssh", "-n", "-f", "-o", "BatchMode=yes", sshTarget, remoteRestartScript)
	_ = restartCmd.Run()

	// 6. Verify remote daemon responsiveness
	time.Sleep(1 * time.Second)
	remoteVersion := version.Version
	if host.URL != "" {
		client := &http.Client{Timeout: 3 * time.Second}
		if resp, err := client.Get(strings.TrimRight(host.URL, "/") + "/v1/version"); err == nil {
			var vResp struct {
				Version string `json:"version"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&vResp); err == nil && vResp.Version != "" {
				remoteVersion = vResp.Version
			}
			_ = resp.Body.Close()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "success",
		"message":     fmt.Sprintf("Successfully upgraded ackbard on '%s' (%s/%s) to v%s", hostName, goos, goarch, remoteVersion),
		"host":        hostName,
		"version":     remoteVersion,
		"target_os":   goos,
		"target_arch": goarch,
	})
}

var tunnelMu sync.Mutex

// killTunnelOnPort terminates any dead/stale SSH tunnel listening on the given local port
// and waits until the port is confirmed released by the operating system.
func killTunnelOnPort(port string) error {
	if port == "" {
		return nil
	}

	myPID := fmt.Sprintf("%d", os.Getpid())

	// 1. Identify specifically listening PIDs on this port (avoiding client sockets like Chrome or ackbard)
	lsofOut, _ := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%s", port), "-sTCP:LISTEN", "-t").Output()
	pids := strings.Fields(strings.TrimSpace(string(lsofOut)))
	for _, pid := range pids {
		if pid != "" && pid != myPID {
			_ = exec.Command("kill", "-9", pid).Run()
		}
	}

	// 2. Also terminate any ssh forwarder process targeting this local port
	_ = exec.Command("pkill", "-9", "-f", fmt.Sprintf("ssh.*-L %s:", port)).Run()

	// 3. Poll net.DialTimeout until the port is freed or timeout after 1.5s
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 50*time.Millisecond)
		if err != nil {
			// Port is successfully freed!
			return nil
		}
		conn.Close()
		time.Sleep(50 * time.Millisecond)
	}

	return nil
}

// spawnSSHTunnel safely clears any stale tunnel on port and spawns a new resilient SSH tunnel with forward failure detection.
func spawnSSHTunnel(port, sshTarget string) error {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()

	var lastErr error
	var lastOut string

	for attempt := 1; attempt <= 3; attempt++ {
		_ = killTunnelOnPort(port)
		time.Sleep(200 * time.Millisecond)

		tmp, err := os.CreateTemp("", "ssh-tunnel-*")
		if err != nil {
			return fmt.Errorf("create temp file for ssh tunnel: %w", err)
		}
		tmpName := tmp.Name()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "ssh",
			"-f",
			"-o", "ExitOnForwardFailure=yes",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=15",
			"-o", "ServerAliveInterval=15",
			"-o", "ServerAliveCountMax=3",
			"-N",
			"-L", fmt.Sprintf("%s:127.0.0.1:7777", port),
			sshTarget,
		)
		cmd.Stdout = tmp
		cmd.Stderr = tmp

		runErr := cmd.Run()
		cancel()
		_ = tmp.Close()

		outBytes, _ := os.ReadFile(tmpName)
		_ = os.Remove(tmpName)

		if runErr == nil {
			return nil
		}
		lastErr = runErr
		lastOut = strings.TrimSpace(string(outBytes))
		time.Sleep(500 * time.Millisecond)
	}

	if lastOut != "" {
		return fmt.Errorf("%v (%s)", lastErr, lastOut)
	}
	return lastErr
}

func (s *Server) handleHostReconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hostName := r.URL.Query().Get("name")
	if hostName == "" {
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		hostName = req.Name
	}
	if hostName == "" {
		http.Error(w, "Missing host name", http.StatusBadRequest)
		return
	}

	host, err := s.db.GetHost(hostName)
	if err != nil || host == nil {
		if allHosts, lerr := s.db.ListHosts(); lerr == nil {
			for _, h := range allHosts {
				if h.Name == hostName || strings.HasSuffix(h.Name, "@"+hostName) || strings.Contains(h.Name, hostName) {
					host = h
					break
				}
			}
		}
	}
	if host == nil {
		http.Error(w, "Host not found", http.StatusNotFound)
		return
	}

	sshTarget := host.SSHTarget
	if sshTarget == "" {
		sshTarget = host.Name
	}

	// Determine target local port from host.URL (e.g. http://127.0.0.1:7778 -> 7778)
	port := "7778"
	if u, err := url.Parse(host.URL); err == nil && u.Port() != "" {
		port = u.Port()
	}

	// Launch resilient SSH tunnel
	if err := spawnSSHTunnel(port, sshTarget); err != nil {
		http.Error(w, fmt.Sprintf("Failed to launch SSH tunnel: %v", err), http.StatusInternalServerError)
		return
	}

	// Poll remote daemon health over the newly spawned tunnel (allowing up to ~4-5s for wake-from-sleep network re-association)
	testURL := fmt.Sprintf("http://127.0.0.1:%s/v1/version", port)
	client := http.Client{Timeout: 1000 * time.Millisecond}
	var vData map[string]interface{}
	var lastCheckErr error

	for attempt := 0; attempt < 8; attempt++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := client.Get(testURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&vData)
			resp.Body.Close()
			lastCheckErr = nil
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		lastCheckErr = err
	}

	if lastCheckErr != nil || vData == nil {
		s.updateHostHealth(host.Name, false, "", "", 0)
		http.Error(w, fmt.Sprintf("SSH tunnel spawned but remote daemon is unreachable: %v", lastCheckErr), http.StatusBadGateway)
		return
	}

	var dispName string
	if d, ok := vData["display_name"].(string); ok {
		dispName = d
	}
	s.updateHostHealth(host.Name, true, fmt.Sprintf("%v", vData["version"]), dispName, 0)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Successfully reconnected SSH tunnel to '%s' (port %s)", host.Name, port),
		"version": vData["version"],
	})
}

// ensureHostTunnels verifies that local SSH tunnels for registered remote hosts are active, and revives them if down.
func (s *Server) ensureHostTunnels(ctx context.Context) {
	hosts, err := s.db.ListHosts()
	if err != nil || len(hosts) == 0 {
		return
	}
	for _, h := range hosts {
		if h.SSHTarget == "" && h.Name == "" {
			continue
		}
		sshTarget := h.SSHTarget
		if sshTarget == "" {
			sshTarget = h.Name
		}
		if h.URL == "" {
			continue
		}
		u, err := url.Parse(h.URL)
		if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
			continue
		}
		port := u.Port()
		if port == "" {
			continue
		}

		// Check if port is already answering
		start := time.Now()
		client := http.Client{Timeout: 1500 * time.Millisecond}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/v1/version", port))
		if err == nil {
			var vResp struct {
				Version     string `json:"version"`
				DisplayName string `json:"display_name"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&vResp)
			resp.Body.Close()
			latency := time.Since(start).Milliseconds()
			s.updateHostHealth(h.Name, true, vResp.Version, vResp.DisplayName, latency)
			continue // tunnel is healthy!
		}

		// Tunnel is down or not established; attempt background reconnection
		log.Printf("[Host Tunnel Manager] SSH tunnel for host %q (port %s) is down. Reconnecting...", h.Name, port)
		if err := spawnSSHTunnel(port, sshTarget); err != nil {
			log.Printf("[Host Tunnel Manager] Failed to revive tunnel for %q (port %s): %v", h.Name, port, err)
			s.updateHostHealth(h.Name, false, "", "", 0)
		} else {
			start = time.Now()
			if r2, err2 := client.Get(fmt.Sprintf("http://127.0.0.1:%s/v1/version", port)); err2 == nil {
				var vResp struct {
					Version     string `json:"version"`
					DisplayName string `json:"display_name"`
				}
				_ = json.NewDecoder(r2.Body).Decode(&vResp)
				r2.Body.Close()
				latency := time.Since(start).Milliseconds()
				s.updateHostHealth(h.Name, true, vResp.Version, vResp.DisplayName, latency)
			}
		}
	}
}
