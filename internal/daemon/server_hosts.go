package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ackbar/internal/version"
)

func (s *Server) updateHostHealth(name string, online bool, ver, displayName string, latencyMs int64) {
	s.updateHostHealthWithTelemetry(name, online, ver, displayName, latencyMs, nil, nil, -1, nil)
}

func (s *Server) updateHostHealthWithTelemetry(
	name string,
	online bool,
	ver, displayName string,
	latencyMs int64,
	detectedCaps []string,
	policyTags []string,
	routingWeight int,
	load *HostLoadSnapshot,
) {
	if strings.EqualFold(strings.TrimSpace(displayName), "local") {
		displayName = ""
	}
	s.hostHealthMu.Lock()
	wasOnline := false
	if prev, ok := s.hostHealthMap[name]; ok {
		if prev.Online {
			wasOnline = true
		}
		if online {
			if ver == "" && prev.Version != "" {
				ver = prev.Version
			}
			if displayName == "" && prev.DisplayName != "" && !strings.EqualFold(strings.TrimSpace(prev.DisplayName), "local") {
				displayName = prev.DisplayName
			}
			if len(detectedCaps) == 0 && len(prev.DetectedCapabilities) > 0 {
				detectedCaps = prev.DetectedCapabilities
			}
			if len(policyTags) == 0 && len(prev.PolicyTags) > 0 {
				policyTags = prev.PolicyTags
			}
			if routingWeight < 0 && prev.RoutingWeight > 0 {
				routingWeight = prev.RoutingWeight
			}
			if load == nil && prev.Load != nil {
				load = prev.Load
			}
		}
	}
	s.hostHealthMap[name] = &HostHealth{
		Online:               online,
		Version:              ver,
		DisplayName:          displayName,
		LatencyMs:            latencyMs,
		LastCheck:            time.Now(),
		DetectedCapabilities: detectedCaps,
		PolicyTags:           policyTags,
		RoutingWeight:        routingWeight,
		Load:                 load,
	}
	s.hostHealthMu.Unlock()

	if online && s.db != nil {
		if len(detectedCaps) > 0 {
			if hr, err := s.db.GetHost(name); err == nil && hr != nil {
				hr.DetectedCapabilities = detectedCaps
				_ = s.db.SaveHost(hr)
			}
		}
		if load != nil {
			_ = s.db.RecordHostLoadSample(name, *load, 0, time.Now().UTC())
		}
	}

	// If host transitioned to online, pull/push deltas immediately for fast convergence
	if online && !wasOnline && s.db != nil {
		go func() {
			if hr, err := s.db.GetHost(name); err == nil && hr != nil {
				_ = s.PullFromPeer(context.Background(), hr)
				s.pushSyncStateToPeer(hr)
			}
		}()
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

// ShortRemoteHostIdentifier extracts and validates a safe short hostname ([a-z0-9_-])
// from an SSH host alias or target (e.g. "user@macmini.local" -> "macmini").
func ShortRemoteHostIdentifier(hostName string) string {
	h := strings.TrimSpace(hostName)
	if idx := strings.LastIndexByte(h, '@'); idx != -1 {
		h = h[idx+1:]
	}
	h = normalizeSystemHostName(h)
	for _, r := range h {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return ""
		}
	}
	return h
}

func (s *Server) resolveHost(hostName string) *HostRecord {
	if s.isLocalHost(hostName) || s.db == nil {
		return nil
	}
	hostRec, err := s.db.GetHost(hostName)
	if err == nil && hostRec != nil {
		return hostRec
	}
	if allHosts, err := s.db.ListHosts(); err == nil {
		s.hostHealthMu.RLock()
		healthMap := make(map[string]string)
		for k, v := range s.hostHealthMap {
			if v != nil {
				healthMap[k] = v.DisplayName
			}
		}
		s.hostHealthMu.RUnlock()

		targetExtracted := extractHostname(hostName)

		for _, h := range allHosts {
			if h.Name == hostName || strings.EqualFold(h.Name, hostName) {
				return h
			}
			if h.DisplayName != "" && strings.EqualFold(h.DisplayName, hostName) {
				return h
			}
			if healthDN, ok := healthMap[h.Name]; ok && healthDN != "" && strings.EqualFold(healthDN, hostName) {
				return h
			}
			if h.SSHTarget != "" {
				if strings.EqualFold(h.SSHTarget, hostName) || strings.HasSuffix(h.SSHTarget, "@"+hostName) {
					return h
				}
				if targetExtracted != "" && extractHostname(h.SSHTarget) == targetExtracted {
					return h
				}
			}
		}

		for _, h := range allHosts {
			if strings.HasSuffix(h.Name, "@"+hostName) || strings.Contains(h.Name, hostName) {
				return h
			}
		}
	}
	return nil
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("include_local") == "true" {
			all := s.GetAllFleetHosts()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(all)
			return
		}
		hosts, err := s.db.ListHosts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.hostHealthMu.RLock()
		for _, h := range hosts {
			if health, ok := s.hostHealthMap[h.Name]; ok && health != nil {
				h.Online = health.Online
				if health.Version != "" {
					h.Version = health.Version
				}
				if health.DisplayName != "" {
					h.DisplayName = health.DisplayName
				}
				h.LatencyMs = health.LatencyMs
				if len(health.DetectedCapabilities) > 0 {
					h.DetectedCapabilities = health.DetectedCapabilities
				}
				if len(health.PolicyTags) > 0 && !h.HasExplicitWeight {
					h.PolicyTags = health.PolicyTags
				}
				if health.Load != nil {
					loadCopy := *health.Load
					h.Load = &loadCopy
				}
			}
		}
		s.hostHealthMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hosts)

	case http.MethodPost:
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		rawBytes, _ := json.Marshal(raw)
		var h HostRecord
		_ = json.Unmarshal(rawBytes, &h)
		if _, hasWeight := raw["routing_weight"]; hasWeight {
			h.HasExplicitWeight = true
		}
		if h.Name == "" {
			http.Error(w, "Missing host name", http.StatusBadRequest)
			return
		}
		if s.isLocalHost(h.Name) {
			settingsToSave := make(map[string]string)
			if len(h.PolicyTags) > 0 {
				if b, err := json.Marshal(h.PolicyTags); err == nil {
					settingsToSave["local_policy_tags"] = string(b)
				}
			}
			if h.HasExplicitWeight {
				settingsToSave["local_routing_weight"] = fmt.Sprintf("%d", h.RoutingWeight)
			}
			if len(settingsToSave) > 0 {
				_ = s.db.SetSettings(settingsToSave)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"saved"}`))
			return
		}
		h.CreatedAt = time.Now()
		h.UpdatedAt = time.Now().UTC()
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

type httpStatusError struct {
	code int
	err  error
}

func (e *httpStatusError) Error() string { return e.err.Error() }
func (e *httpStatusError) Unwrap() error { return e.err }

func statusCodeOf(err error, fallback int) int {
	var se *httpStatusError
	if errors.As(err, &se) && se.code > 0 {
		return se.code
	}
	return fallback
}

func readSourceDirVersion(srcDir string) string {
	if srcDir == "" {
		return ""
	}
	for _, rel := range []string{
		filepath.Join("internal", "version", "VERSION"),
		"VERSION",
	} {
		if data, err := os.ReadFile(filepath.Join(srcDir, rel)); err == nil {
			if v := version.CleanVersion(string(data)); v != "" {
				return v
			}
		}
	}
	return ""
}

// findUpToDateSourceDir requires srcDir >= targetVersion and running daemon version to prevent stale checkouts from downgrading hosts.
func findUpToDateSourceDir(targetVersion string) string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "Work/Ackbar"),
		filepath.Join(home, "Personal/ackbar"),
		filepath.Join(home, "src/Ackbar"),
		filepath.Join(home, "Projects/Ackbar"),
		".",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, serr := os.Stat(filepath.Join(c, "cmd/ackbard/main.go")); serr != nil {
			continue
		}
		srcVer := readSourceDirVersion(c)
		if srcVer == "" {
			continue
		}
		if targetVersion != "" && version.IsNewer(targetVersion, srcVer) {
			continue
		}
		if version.IsNewer(version.Version, srcVer) {
			continue
		}
		return c
	}
	return ""
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellQuoteArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellSingleQuote(a)
	}
	return strings.Join(quoted, " ")
}

func detectRemotePlatformSSH(host *HostRecord) (string, string, error) {
	sshTarget := host.SSHTarget
	if sshTarget == "" {
		sshTarget = host.Name
	}
	detectCmd := exec.Command("ssh", "-o", "ConnectTimeout=8", "-o", "BatchMode=yes", sshTarget, "uname -s && uname -m")
	out, err := detectCmd.CombinedOutput()
	if err != nil {
		return "", "", &httpStatusError{
			code: http.StatusBadGateway,
			err:  fmt.Errorf("SSH connection to '%s' failed: %s (%v)", sshTarget, strings.TrimSpace(string(out)), err),
		}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return "", "", &httpStatusError{
			code: http.StatusInternalServerError,
			err:  fmt.Errorf("Could not parse remote OS/architecture: %q", string(out)),
		}
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
	return goos, goarch, nil
}

func preparePlatformBinariesDefault(ctx context.Context, targetVersion, srcDir, goos, goarch, destDir string, onDetail func(string)) (bool, string) {
	emit := func(d string) {
		if onDetail != nil {
			onDetail(d)
		}
	}
	if srcDir != "" {
		buildOK := true
		for _, target := range []struct {
			out string
			pkg string
			bin string
		}{
			{filepath.Join(destDir, "ackbard"), "./cmd/ackbard", "ackbard"},
			{filepath.Join(destDir, "ackbar-hook"), "./cmd/ackbar-hook", "ackbar-hook"},
			{filepath.Join(destDir, "ackbar"), "./cmd/ackbar", "ackbar"},
			{filepath.Join(destDir, "ackbar-relay"), "./cmd/ackbar-relay", "ackbar-relay"},
		} {
			emit("compiling " + target.bin)
			cmdBuild := exec.CommandContext(ctx, "go", "build", "-ldflags=-s -w", "-o", target.out, target.pkg)
			cmdBuild.Dir = srcDir
			cmdBuild.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
			if _, berr := cmdBuild.CombinedOutput(); berr != nil {
				buildOK = false
				break
			}
		}
		if buildOK {
			return true, "compiled"
		}
	}

	emit("downloading release tarball")
	dlCtx, dlCancel := context.WithTimeout(ctx, 90*time.Second)
	_, dlErr := version.DownloadAndInstallForPlatform(dlCtx, targetVersion, goos, goarch, destDir)
	dlCancel()
	if dlErr == nil {
		return true, "downloaded"
	}
	return false, "fallback"
}

func (s *Server) deployRemoteHostDefault(host *HostRecord, targetVersion, goos, goarch, preparedDir string, binariesPrepared bool, onDetail func(string)) (map[string]interface{}, int, error) {
	emit := func(d string) {
		if onDetail != nil {
			onDetail(d)
		}
	}
	hostName := host.Name
	sshTarget := host.SSHTarget
	if sshTarget == "" {
		sshTarget = host.Name
	}

	shortHost := ShortRemoteHostIdentifier(host.Name)
	hostFlagArg := ""
	if shortHost != "" {
		hostFlagArg = " --host-name " + shortHost
	}

	authToken := s.configuredToken
	if authToken == "" {
		authToken = os.Getenv("ACKBAR_TOKEN")
	}
	quotedAuthToken := shellSingleQuote(authToken)

	if binariesPrepared {
		emit("uploading binaries")
		_ = exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "mkdir -p ~/.local/bin").Run()
		for _, binName := range []string{"ackbard", "ackbar", "ackbar-hook", "ackbar-relay"} {
			localPath := filepath.Join(preparedDir, binName)
			if _, serr := os.Stat(localPath); serr != nil {
				if binName == "ackbar-relay" {
					continue
				}
				return nil, http.StatusInternalServerError, fmt.Errorf("Missing prepared binary %s for %s", binName, sshTarget)
			}
			remotePath := fmt.Sprintf("%s:~/.local/bin/%s.new", sshTarget, binName)
			scpCmd := exec.Command("scp", "-o", "BatchMode=yes", localPath, remotePath)
			if sout, serr := scpCmd.CombinedOutput(); serr != nil {
				return nil, http.StatusBadGateway, fmt.Errorf("Failed to upload %s to %s: %s", binName, sshTarget, string(sout))
			}
		}

		emit("restarting ackbard")
		remoteRestartScript := buildRemoteBinaryRestartScript(quotedAuthToken, hostFlagArg)
		restartCmd := exec.Command("ssh", "-o", "ConnectTimeout=15", "-o", "BatchMode=yes", sshTarget, remoteRestartScript)
		if rout, rerr := restartCmd.CombinedOutput(); rerr != nil {
			return nil, http.StatusInternalServerError, fmt.Errorf("Failed to replace binaries or restart ackbard on %s: %s (%v)", sshTarget, strings.TrimSpace(string(rout)), rerr)
		} else if strings.Contains(string(rout), "RESTART_DEFERRED") {
			return map[string]interface{}{
				"status":      "restart_deferred",
				"message":     fmt.Sprintf("Upgraded binaries on '%s' to v%s (daemon restart deferred until active headless turn completes)", hostName, targetVersion),
				"host":        hostName,
				"version":     targetVersion,
				"target_os":   goos,
				"target_arch": goarch,
			}, http.StatusOK, nil
		}
	} else {
		emit("running remote self-update")
		remoteUpdateScript := buildRemoteSelfUpdateScript(quotedAuthToken, hostFlagArg)
		updCmd := exec.Command("ssh", "-o", "ConnectTimeout=30", "-o", "BatchMode=yes", sshTarget, remoteUpdateScript)
		if uout, uerr := updCmd.CombinedOutput(); uerr != nil {
			return nil, http.StatusInternalServerError, fmt.Errorf("Remote 'ackbar update' on %s failed: %s (%v)", sshTarget, strings.TrimSpace(string(uout)), uerr)
		} else if strings.Contains(string(uout), "RESTART_DEFERRED") {
			return map[string]interface{}{
				"status":      "restart_deferred",
				"message":     fmt.Sprintf("Upgraded binaries on '%s' to v%s (daemon restart deferred until active headless turn completes)", hostName, targetVersion),
				"host":        hostName,
				"version":     targetVersion,
				"target_os":   goos,
				"target_arch": goarch,
			}, http.StatusOK, nil
		}
	}

	if targetVersion != "" {
		emit("verifying v" + targetVersion)
	} else {
		emit("verifying daemon")
	}
	remoteVersion := s.verifyRemoteHostVersion(host, sshTarget, targetVersion)
	if targetVersion != "" && version.IsNewer(targetVersion, remoteVersion) {
		return nil, http.StatusInternalServerError, fmt.Errorf("Host '%s' restarted but is still reporting v%s (expected v%s)", hostName, remoteVersion, targetVersion)
	}

	return map[string]interface{}{
		"status":      "success",
		"message":     fmt.Sprintf("Successfully upgraded ackbard on '%s' (%s/%s) to v%s", hostName, goos, goarch, remoteVersion),
		"host":        hostName,
		"version":     remoteVersion,
		"target_os":   goos,
		"target_arch": goarch,
	}, http.StatusOK, nil
}

func (s *Server) verifyRemoteHostVersion(host *HostRecord, sshTarget, targetVersion string) string {
	remoteVersion := ""
	if host.URL != "" {
		client := &http.Client{Timeout: 2 * time.Second}
		for attempt := 0; attempt < 10; attempt++ {
			time.Sleep(500 * time.Millisecond)
			if resp, err := client.Get(strings.TrimRight(host.URL, "/") + "/v1/version"); err == nil {
				var vResp struct {
					Version              string            `json:"version"`
					DisplayName          string            `json:"display_name"`
					DetectedCapabilities []string          `json:"detected_capabilities"`
					PolicyTags           []string          `json:"policy_tags"`
					RoutingWeight        int               `json:"routing_weight"`
					Load                 *HostLoadSnapshot `json:"load"`
				}
				vResp.RoutingWeight = -1
				if err := json.NewDecoder(resp.Body).Decode(&vResp); err == nil && vResp.Version != "" {
					remoteVersion = vResp.Version
					s.updateHostHealthWithTelemetry(host.Name, true, vResp.Version, vResp.DisplayName, 0, vResp.DetectedCapabilities, vResp.PolicyTags, vResp.RoutingWeight, vResp.Load)
					_ = resp.Body.Close()
					if targetVersion == "" || !version.IsNewer(targetVersion, remoteVersion) {
						break
					}
					continue
				}
				_ = resp.Body.Close()
			}
		}
	}
	if remoteVersion == "" {
		if vout, verr := exec.Command("ssh", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", sshTarget, `~/.local/bin/ackbard --version`).CombinedOutput(); verr == nil {
			fields := strings.Fields(strings.TrimSpace(string(vout)))
			if len(fields) > 0 {
				remoteVersion = version.CleanVersion(fields[len(fields)-1])
			}
		}
	}
	if remoteVersion == "" {
		remoteVersion = targetVersion
	}
	return remoteVersion
}

type HostUpdateSummary struct {
	Host            string `json:"host"`
	PreviousVersion string `json:"previous_version"`
	NewVersion      string `json:"new_version"`
	Status          string `json:"status"`
	Message         string `json:"message"`
}

type hostProbeResult struct {
	host      *HostRecord
	status    string
	remoteVer string
	goos      string
	goarch    string
	probeErr  error
}

func (s *Server) probeHostForUpdate(ctx context.Context, h *HostRecord, targetVer string) hostProbeResult {
	remoteVer, isOnline := "", false
	s.hostHealthMu.RLock()
	if hh, ok := s.hostHealthMap[h.Name]; ok && hh != nil {
		isOnline, remoteVer = hh.Online, hh.Version
	}
	s.hostHealthMu.RUnlock()

	if h.URL != "" {
		client := &http.Client{Timeout: 2 * time.Second}
		if req, rErr := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(h.URL, "/")+"/v1/version", nil); rErr == nil {
			if resp, gErr := client.Do(req); gErr == nil {
				var vData struct {
					Version string `json:"version"`
				}
				if json.NewDecoder(resp.Body).Decode(&vData) == nil && vData.Version != "" {
					remoteVer, isOnline = vData.Version, true
				}
				_ = resp.Body.Close()
			}
		}
	}

	if !isOnline {
		return hostProbeResult{host: h, status: "skipped_offline", remoteVer: remoteVer}
	}
	if remoteVer != "" && remoteVer != "online" && !version.IsNewer(targetVer, remoteVer) {
		return hostProbeResult{host: h, status: "up_to_date", remoteVer: remoteVer}
	}
	goos, goarch, pErr := s.detectRemotePlatform(h)
	return hostProbeResult{host: h, status: "outdated", remoteVer: remoteVer, goos: goos, goarch: goarch, probeErr: pErr}
}

type fleetProbePlan struct {
	probes          []hostProbeResult
	outdatedIndices []int
	uniquePlats     []string
	platSet         map[string][2]string
}

func (s *Server) probeFleetHostsForUpdate(ctx context.Context, targetVer string) fleetProbePlan {
	if s.db == nil {
		return fleetProbePlan{}
	}
	hosts, _ := s.db.ListHosts()
	probes := make([]hostProbeResult, len(hosts))
	var wg sync.WaitGroup
	for idx, h := range hosts {
		if h == nil || s.isLocalHost(h.Name) {
			continue
		}
		wg.Add(1)
		go func(i int, hr *HostRecord) {
			defer wg.Done()
			probes[i] = s.probeHostForUpdate(ctx, hr, targetVer)
		}(idx, h)
	}
	wg.Wait()

	var outdatedIndices []int
	platSet := make(map[string][2]string)
	for i, p := range probes {
		if p.status == "outdated" {
			outdatedIndices = append(outdatedIndices, i)
			if p.probeErr == nil {
				platSet[p.goos+"/"+p.goarch] = [2]string{p.goos, p.goarch}
			}
		}
	}
	uniquePlats := make([]string, 0, len(platSet))
	for k := range platSet {
		uniquePlats = append(uniquePlats, k)
	}
	sort.Strings(uniquePlats)
	return fleetProbePlan{probes: probes, outdatedIndices: outdatedIndices, uniquePlats: uniquePlats, platSet: platSet}
}

func (s *Server) lookupUpdateHost(hostName string) *HostRecord {
	if s.db == nil {
		return nil
	}
	if host, err := s.db.GetHost(hostName); err == nil && host != nil {
		return host
	}
	allHosts, err := s.db.ListHosts()
	if err != nil {
		return nil
	}
	lower := strings.ToLower(hostName)
	for _, h := range allHosts {
		if h.Name == hostName || h.SSHTarget == hostName || strings.EqualFold(h.Name, hostName) || strings.Contains(strings.ToLower(h.Name), lower) {
			return h
		}
	}
	return nil
}

func (s *Server) checkLatestRelease(ctx context.Context, force bool) (*version.ReleaseInfo, error) {
	if s.checkLatestReleaseFn != nil {
		return s.checkLatestReleaseFn(ctx, force)
	}
	return version.CheckLatestRelease(ctx, force)
}

func (s *Server) detectRemotePlatform(host *HostRecord) (string, string, error) {
	if s.detectRemotePlatformFn != nil {
		return s.detectRemotePlatformFn(host)
	}
	return detectRemotePlatformSSH(host)
}

func (s *Server) UpdateOutdatedRemoteHosts(targetVersion string) []HostUpdateSummary {
	if s.db == nil {
		return nil
	}
	if outcome, _ := s.runFleetUpdate(context.Background(), false, true, targetVersion, nil); outcome != nil {
		return outcome.hostSummaries
	}
	return nil
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

		type peerVersionPayload struct {
			Version              string            `json:"version"`
			DisplayName          string            `json:"display_name"`
			DetectedCapabilities []string          `json:"detected_capabilities"`
			PolicyTags           []string          `json:"policy_tags"`
			RoutingWeight        int               `json:"routing_weight"`
			Load                 *HostLoadSnapshot `json:"load"`
		}

		// Check if port is already answering
		start := time.Now()
		client := http.Client{Timeout: 1500 * time.Millisecond}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/v1/version", port))
		if err == nil {
			var vResp peerVersionPayload
			vResp.RoutingWeight = -1
			_ = json.NewDecoder(resp.Body).Decode(&vResp)
			resp.Body.Close()
			latency := time.Since(start).Milliseconds()
			s.updateHostHealthWithTelemetry(h.Name, true, vResp.Version, vResp.DisplayName, latency, vResp.DetectedCapabilities, vResp.PolicyTags, vResp.RoutingWeight, vResp.Load)
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
				var vResp peerVersionPayload
				vResp.RoutingWeight = -1
				_ = json.NewDecoder(r2.Body).Decode(&vResp)
				r2.Body.Close()
				latency := time.Since(start).Milliseconds()
				s.updateHostHealthWithTelemetry(h.Name, true, vResp.Version, vResp.DisplayName, latency, vResp.DetectedCapabilities, vResp.PolicyTags, vResp.RoutingWeight, vResp.Load)
			}
		}
	}
}
