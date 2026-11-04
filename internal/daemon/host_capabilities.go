package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ackbar/internal/version"
)

type HostRoutingRequest struct {
	TargetHost           string   `json:"target_host"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	RepoNodePath         string   `json:"repo_node_path,omitempty"`
	NodePath             string   `json:"node_path,omitempty"`
	TaskKind             string   `json:"task_kind,omitempty"`
	Agent                string   `json:"agent,omitempty"`
	IsSupervisor         bool     `json:"is_supervisor,omitempty"`
}

type HostRoutingDecision struct {
	SelectedHost string            `json:"selected_host"`
	Reason       string            `json:"reason"`
	LoadWarning  string            `json:"load_warning,omitempty"`
	Load         *HostLoadSnapshot `json:"load,omitempty"`
}

// ProbeSystemStatsFunc allows deterministic overriding in unit tests.
var ProbeSystemStatsFunc = defaultProbeSystemStats

// DetectCapabilitiesFunc allows deterministic overriding in unit tests.
var DetectCapabilitiesFunc = defaultDetectHostCapabilities

func (s *Server) GetDetectedCapabilities() []string {
	s.hostHealthMu.RLock()
	if len(s.cachedCapabilities) > 0 && time.Since(s.capabilitiesCachedAt) < 5*time.Minute {
		out := make([]string, len(s.cachedCapabilities))
		copy(out, s.cachedCapabilities)
		s.hostHealthMu.RUnlock()
		return out
	}
	s.hostHealthMu.RUnlock()

	home := s.homeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	caps := DetectCapabilitiesFunc(home)

	s.hostHealthMu.Lock()
	s.cachedCapabilities = caps
	s.capabilitiesCachedAt = time.Now()
	s.hostHealthMu.Unlock()

	return caps
}

func defaultDetectHostCapabilities(home string) []string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	capSet := make(map[string]bool)
	addCap := func(c string) {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" {
			capSet[c] = true
		}
	}

	// 1. OS & Hardware
	switch runtime.GOOS {
	case "darwin":
		addCap("macos")
		if runtime.GOARCH == "arm64" {
			addCap("gpu")
			addCap("apple-silicon")
		}
		// Check iOS / Xcode simulator support
		if _, err := os.Stat("/Applications/Xcode.app"); err == nil {
			addCap("ios")
		} else if _, err := os.Stat("/Library/Developer/CommandLineTools/usr/bin/xcrun"); err == nil {
			if _, sErr := os.Stat("/Applications/Xcode-beta.app"); sErr == nil {
				addCap("ios")
			}
		}
		if !capSet["ios"] {
			if p, err := exec.LookPath("xcrun"); err == nil && p != "" {
				// Quick check if simctl exists in Developer dir
				if _, err := os.Stat("/Library/Developer/CoreSimulator"); err == nil {
					addCap("ios")
				}
			}
		}
	case "linux":
		addCap("linux")
		if _, err := os.Stat("/dev/nvidia0"); err == nil {
			addCap("gpu")
		} else if _, err := exec.LookPath("nvidia-smi"); err == nil {
			addCap("gpu")
		}
	}

	// 2. Android SDK / Emulator / ADB
	if _, err := exec.LookPath("adb"); err == nil {
		addCap("android")
	} else if _, err := exec.LookPath("emulator"); err == nil {
		addCap("android")
	} else if os.Getenv("ANDROID_HOME") != "" || os.Getenv("ANDROID_SDK_ROOT") != "" {
		addCap("android")
	} else if home != "" {
		for _, p := range []string{
			filepath.Join(home, "Library", "Android", "sdk"),
			filepath.Join(home, "Android", "Sdk"),
		} {
			if _, err := os.Stat(p); err == nil {
				addCap("android")
				break
			}
		}
	}

	// 3. Chrome / Playwright UI testing
	for _, bin := range []string{"google-chrome", "chromium", "chromium-browser", "playwright"} {
		if _, err := exec.LookPath(bin); err == nil {
			addCap("chrome")
			break
		}
	}
	if !capSet["chrome"] {
		if _, err := os.Stat("/Applications/Google Chrome.app"); err == nil {
			addCap("chrome")
		} else if home != "" {
			if _, err := os.Stat(filepath.Join(home, ".cache", "ms-playwright")); err == nil {
				addCap("chrome")
			}
		}
	}

	// 4. Docker
	if _, err := exec.LookPath("docker"); err == nil {
		addCap("docker")
	}

	// 5. Installed & Authenticated AI Agents
	if statuses, err := GetAgentStatuses(home); err == nil {
		for _, st := range statuses {
			if st.Detected && st.Key != "claude-desktop" {
				addCap("agent:" + st.Key)
			}
		}
	}

	// 6. Configured MCP Plugins (gdrive, gdocs, gslides, gsheets, linear, etc.)
	if home != "" {
		mcpConfigPaths := []string{
			filepath.Join(home, ".claude.json"),
			filepath.Join(home, ".gemini", "config", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			filepath.Join(home, ".codex", "config.json"),
		}
		for _, cfgPath := range mcpConfigPaths {
			if data, err := os.ReadFile(cfgPath); err == nil {
				var root map[string]any
				if err := json.Unmarshal(data, &root); err == nil {
					if servers, ok := root["mcpServers"].(map[string]any); ok {
						for srvName := range servers {
							norm := normalizeMCPCapabilityName(srvName)
							if norm != "" && norm != "ackbar" {
								addCap("mcp:" + norm)
							}
						}
					}
				}
			}
		}
		// Check Antigravity plugins directory (~/.gemini/config/plugins/<plugin>)
		pluginsDir := filepath.Join(home, ".gemini", "config", "plugins")
		if entries, err := os.ReadDir(pluginsDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					norm := normalizeMCPCapabilityName(e.Name())
					if norm != "" {
						addCap("mcp:" + norm)
					}
				}
			}
		}
	}

	out := make([]string, 0, len(capSet))
	for c := range capSet {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func normalizeMCPCapabilityName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimSuffix(s, "-mcp-server")
	s = strings.TrimSuffix(s, "-plugin")
	s = strings.TrimSuffix(s, "-mcp")
	s = strings.TrimSuffix(s, "mcp")
	return strings.TrimSpace(s)
}

func (s *Server) LocalPolicyAndWeight() ([]string, int) {
	hostName := s.HostName()
	defTags, defWeight := DefaultHostPolicy(hostName)

	if s.db == nil {
		return defTags, defWeight
	}

	var tags []string
	if rawTags, err := s.db.GetSetting("local_policy_tags"); err == nil && rawTags != "" {
		_ = json.Unmarshal([]byte(rawTags), &tags)
	}
	if len(tags) == 0 {
		if hr, err := s.db.GetHost(hostName); err == nil && hr != nil && len(hr.PolicyTags) > 0 {
			tags = hr.PolicyTags
		} else {
			tags = defTags
		}
	}

	weight := defWeight
	if rawWeight, err := s.db.GetSetting("local_routing_weight"); err == nil && rawWeight != "" {
		if w, pErr := strconv.Atoi(strings.TrimSpace(rawWeight)); pErr == nil && w >= 0 {
			weight = w
		}
	} else if hr, err := s.db.GetHost(hostName); err == nil && hr != nil && hr.HasExplicitWeight {
		weight = hr.RoutingWeight
	} else {
		isEphemeral := false
		has247 := false
		for _, t := range tags {
			if strings.EqualFold(t, "ephemeral") {
				isEphemeral = true
			}
			if strings.EqualFold(t, "24/7") || strings.EqualFold(t, "always_on") || strings.EqualFold(t, "always-on") {
				has247 = true
			}
		}
		if isEphemeral && !has247 {
			weight = 0
		}
	}

	return tags, weight
}

func (s *Server) LocalHostRecord() *HostRecord {
	tags, weight := s.LocalPolicyAndWeight()
	caps := s.GetDetectedCapabilities()
	load := s.CollectLocalHostLoad()
	return &HostRecord{
		Name:                 s.HostName(),
		URL:                  "http://127.0.0.1:7777",
		PolicyTags:           tags,
		RoutingWeight:        weight,
		HasExplicitWeight:    true,
		DetectedCapabilities: caps,
		Online:               true,
		Version:              version.Version,
		DisplayName:          s.DisplayName(),
		LatencyMs:            0,
		Load:                 &load,
		IsLocal:              true,
	}
}

func (s *Server) CollectLocalHostLoad() HostLoadSnapshot {
	var working, idle, queued int
	if s.db != nil {
		if sessions, err := s.db.ListActiveSessions(); err == nil {
			for _, sess := range sessions {
				if sess == nil || !s.isLocalHost(sess.Host) || sess.Archived || sess.IsDone || sess.IsLater {
					continue
				}
				switch sess.State {
				case StateWorking:
					working++
				case StateIdle, StateBlocked:
					idle++
				}
			}
		}
		if tasks, err := s.db.GetTasks(); err == nil {
			for _, t := range tasks {
				if t.Status == "DONE" {
					continue
				}
				for _, ws := range t.Workstreams {
					if ws.Status == "QUEUED" && (ws.TargetHost == "" || s.isLocalHost(ws.TargetHost)) {
						queued++
					}
				}
			}
		}
	}

	cpuPct, ramFreeGB, ramTotalGB, diskFreeGB := ProbeSystemStatsFunc()
	overloaded := cpuPct > 85.0 || (ramTotalGB > 0 && ramFreeGB < 2.0)

	return HostLoadSnapshot{
		WorkingSessions:   working,
		QueuedWorkstreams: queued,
		IdleSessions:      idle,
		CPULoadPct:        math.Round(cpuPct*10) / 10,
		RAMFreeGB:         math.Round(ramFreeGB*10) / 10,
		RAMTotalGB:        math.Round(ramTotalGB*10) / 10,
		DiskFreeGB:        math.Round(diskFreeGB*10) / 10,
		Overloaded:        overloaded,
	}
}

func defaultProbeSystemStats() (cpuPct, ramFreeGB, ramTotalGB, diskFreeGB float64) {
	numCPU := float64(runtime.NumCPU())
	if numCPU <= 0 {
		numCPU = 1
	}

	// 1. Disk free space via syscall.Statfs
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		wd = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(wd, &stat); err == nil {
		diskFreeGB = float64(stat.Bavail) * float64(stat.Bsize) / (1024 * 1024 * 1024)
	}

	// 2. Linux /proc/loadavg & /proc/meminfo
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/loadavg"); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) > 0 {
				if load1, pErr := strconv.ParseFloat(fields[0], 64); pErr == nil {
					cpuPct = math.Min(100.0, (load1/numCPU)*100.0)
				}
			}
		}
		if data, err := os.ReadFile("/proc/meminfo"); err == nil {
			var memTotalKB, memAvailKB, memFreeKB, cachedKB float64
			for _, line := range strings.Split(string(data), "\n") {
				parts := strings.Fields(line)
				if len(parts) < 2 {
					continue
				}
				val, _ := strconv.ParseFloat(parts[1], 64)
				switch strings.TrimSuffix(parts[0], ":") {
				case "MemTotal":
					memTotalKB = val
				case "MemAvailable":
					memAvailKB = val
				case "MemFree":
					memFreeKB = val
				case "Cached":
					cachedKB = val
				}
			}
			if memTotalKB > 0 {
				ramTotalGB = memTotalKB / (1024 * 1024)
				if memAvailKB > 0 {
					ramFreeGB = memAvailKB / (1024 * 1024)
				} else {
					ramFreeGB = (memFreeKB + cachedKB) / (1024 * 1024)
				}
			}
		}
		return cpuPct, ramFreeGB, ramTotalGB, diskFreeGB
	}

	// 3. macOS sysctl & vm_stat (fast <10ms execution)
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "sysctl", "-n", "vm.loadavg", "hw.memsize").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) >= 1 {
				// e.g. "{ 2.45 2.10 1.95 }"
				cleaned := strings.Trim(lines[0], "{} ")
				fields := strings.Fields(cleaned)
				if len(fields) > 0 {
					if load1, pErr := strconv.ParseFloat(fields[0], 64); pErr == nil {
						cpuPct = math.Min(100.0, (load1/numCPU)*100.0)
					}
				}
			}
			if len(lines) >= 2 {
				if memBytes, pErr := strconv.ParseFloat(strings.TrimSpace(lines[1]), 64); pErr == nil && memBytes > 0 {
					ramTotalGB = memBytes / (1024 * 1024 * 1024)
				}
			}
		}
		if out, err := exec.CommandContext(ctx, "vm_stat").Output(); err == nil {
			pageSize := 16384.0
			var freePages, inactivePages, speculativePages float64
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "page size of") {
					for _, tok := range strings.Fields(line) {
						if ps, pErr := strconv.ParseFloat(tok, 64); pErr == nil && ps >= 4096 {
							pageSize = ps
							break
						}
					}
					continue
				}
				parts := strings.Split(line, ":")
				if len(parts) != 2 {
					continue
				}
				key := strings.TrimSpace(parts[0])
				valStr := strings.TrimSuffix(strings.TrimSpace(parts[1]), ".")
				val, _ := strconv.ParseFloat(valStr, 64)
				switch key {
				case "Pages free":
					freePages = val
				case "Pages inactive":
					inactivePages = val
				case "Pages speculative":
					speculativePages = val
				}
			}
			availBytes := (freePages + inactivePages + speculativePages) * pageSize
			if availBytes > 0 {
				ramFreeGB = availBytes / (1024 * 1024 * 1024)
			}
		}
	}

	return cpuPct, ramFreeGB, ramTotalGB, diskFreeGB
}

func inferCapabilitiesFromRepoPath(repoPath string) []string {
	lower := strings.ToLower(strings.Trim(strings.TrimSpace(repoPath), "/"))
	if lower == "" {
		return nil
	}
	var caps []string
	if lower == "ios" || strings.HasSuffix(lower, "-ios") || strings.HasSuffix(lower, "/ios") || strings.Contains(lower, "-ios/") || strings.Contains(lower, "/ios/") {
		caps = append(caps, "ios")
	}
	if lower == "android" || strings.HasSuffix(lower, "-android") || strings.HasSuffix(lower, "/android") || strings.Contains(lower, "-android/") || strings.Contains(lower, "/android/") {
		caps = append(caps, "android")
	}
	return caps
}

func hostHasCapability(h *HostRecord, reqCap string, repoCaps []string) bool {
	reqCap = strings.ToLower(strings.TrimSpace(reqCap))
	if reqCap == "" {
		return true
	}
	checkList := func(list []string) bool {
		for _, item := range list {
			it := strings.ToLower(strings.TrimSpace(item))
			if it == reqCap || it == "agent:"+reqCap || it == "mcp:"+reqCap {
				return true
			}
			if strings.TrimPrefix(it, "agent:") == reqCap || strings.TrimPrefix(it, "mcp:") == reqCap {
				return true
			}
			if (reqCap == "24/7" || reqCap == "always_on" || reqCap == "always-on") &&
				(it == "24/7" || it == "always_on" || it == "always-on") {
				return true
			}
		}
		return false
	}
	return checkList(h.DetectedCapabilities) || checkList(h.PolicyTags) || checkList(repoCaps)
}

func hostHasTag(h *HostRecord, tag string) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	for _, t := range h.PolicyTags {
		tl := strings.ToLower(strings.TrimSpace(t))
		if tl == tag {
			return true
		}
		if (tag == "24/7" || tag == "always_on" || tag == "always-on") &&
			(tl == "24/7" || tl == "always_on" || tl == "always-on") {
			return true
		}
	}
	return false
}

func formatOverloadWarning(h *HostRecord) string {
	if h == nil || h.Load == nil || !h.Load.Overloaded {
		return ""
	}
	return fmt.Sprintf("Host '%s' is currently under heavy load (CPU %.0f%%, %.1f GB free RAM, %d working sessions); spawned immediately anyway.",
		h.Name, h.Load.CPULoadPct, h.Load.RAMFreeGB, h.Load.WorkingSessions)
}

func computeEffectiveLoad(snap *HostLoadSnapshot) float64 {
	if snap == nil {
		return 0
	}
	eff := float64(snap.WorkingSessions) + 0.5*float64(snap.QueuedWorkstreams)
	if snap.CPULoadPct > 60.0 {
		eff += (snap.CPULoadPct - 60.0) / 20.0
	}
	if snap.RAMTotalGB > 0 && snap.RAMFreeGB < 4.0 {
		eff += (4.0 - snap.RAMFreeGB)
	}
	return eff
}

// GetAllFleetHosts returns the local host record plus all registered remote hosts with live health/load hydrated.
func (s *Server) GetAllFleetHosts() []*HostRecord {
	var all []*HostRecord
	localRec := s.LocalHostRecord()
	all = append(all, localRec)

	if s.db == nil {
		return all
	}
	remoteHosts, err := s.db.ListHosts()
	if err != nil {
		return all
	}

	s.hostHealthMu.RLock()
	for _, h := range remoteHosts {
		if s.isLocalHost(h.Name) {
			continue
		}
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
		all = append(all, h)
	}
	s.hostHealthMu.RUnlock()
	return all
}

// SelectTargetHost deterministically selects the optimal host based on capabilities, 24/7 policy, and 75/25 weighted deficit scoring.
func (s *Server) SelectTargetHost(ctx context.Context, req HostRoutingRequest) HostRoutingDecision {
	target := strings.TrimSpace(req.TargetHost)
	allHosts := s.GetAllFleetHosts()

	// 1. Explicit target host requested (not "" and not "auto")
	if target != "" && !strings.EqualFold(target, "auto") {
		if s.isLocalHost(target) {
			localRec := allHosts[0]
			return HostRoutingDecision{
				SelectedHost: localRec.Name,
				Reason:       "explicit local host selected",
				LoadWarning:  formatOverloadWarning(localRec),
				Load:         localRec.Load,
			}
		}
		for _, h := range allHosts {
			if strings.EqualFold(h.Name, target) || strings.EqualFold(h.SSHTarget, target) || strings.HasSuffix(strings.ToLower(h.Name), "@"+strings.ToLower(target)) {
				return HostRoutingDecision{
					SelectedHost: h.Name,
					Reason:       fmt.Sprintf("explicit host '%s' selected", h.Name),
					LoadWarning:  formatOverloadWarning(h),
					Load:         h.Load,
				}
			}
		}
		return HostRoutingDecision{
			SelectedHost: target,
			Reason:       fmt.Sprintf("explicit host '%s' passthrough", target),
		}
	}

	// 2. Build required capabilities list
	reqCaps := make([]string, 0, len(req.RequiredCapabilities)+1)
	for _, c := range req.RequiredCapabilities {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" {
			reqCaps = append(reqCaps, c)
		}
	}
	if req.Agent != "" && req.Agent != "mock-agent" {
		// Only enforce agent capability on hosts that have already reported detected_capabilities
		reqAgent := strings.ToLower(strings.TrimSpace(req.Agent))
		anyReportsAgent := false
		for _, h := range allHosts {
			if h.Online && hostHasCapability(h, "agent:"+reqAgent, nil) {
				anyReportsAgent = true
				break
			}
		}
		if anyReportsAgent {
			reqCaps = append(reqCaps, "agent:"+reqAgent)
		}
	}

	// 3. Check node_host_paths if a repo node path is specified
	repoPath := strings.TrimSpace(req.RepoNodePath)
	if repoPath == "" && req.TaskKind != "design" && !req.IsSupervisor {
		repoPath = strings.TrimSpace(req.NodePath)
	}
	if repoPath != "" && req.TaskKind != "design" && !req.IsSupervisor {
		for _, inferred := range inferCapabilitiesFromRepoPath(repoPath) {
			alreadyPresent := false
			for _, existing := range reqCaps {
				if existing == inferred {
					alreadyPresent = true
					break
				}
			}
			if !alreadyPresent {
				reqCaps = append(reqCaps, inferred)
			}
		}
	}
	repoHostCaps := make(map[string][]string)
	repoHostExists := make(map[string]bool)
	if repoPath != "" && s.db != nil {
		if nhps, err := s.db.ListNodeHostPaths(repoPath); err == nil {
			for _, nhp := range nhps {
				repoHostExists[strings.ToLower(nhp.Host)] = true
				repoHostCaps[strings.ToLower(nhp.Host)] = nhp.Capabilities
			}
		}
	}

	matchHostRepo := func(h *HostRecord) ([]string, bool) {
		names := []string{strings.ToLower(h.Name)}
		if idx := strings.LastIndex(h.Name, "@"); idx != -1 {
			names = append(names, strings.ToLower(h.Name[idx+1:]))
		}
		for _, n := range names {
			if repoHostExists[n] {
				return repoHostCaps[n], true
			}
		}
		return nil, false
	}

	// Check if at least one online host has the repo registered
	enforceRepoPresence := false
	if len(repoHostExists) > 0 && req.TaskKind != "design" && !req.IsSupervisor {
		for _, h := range allHosts {
			if h.Online && !hostHasTag(h, "drain") {
				if _, ok := matchHostRepo(h); ok {
					enforceRepoPresence = true
					break
				}
			}
		}
	}

	// 4. Filter online, non-drained candidates matching required capabilities
	var alwaysOnCandidates []*HostRecord
	var ephemeralCandidates []*HostRecord

	for _, h := range allHosts {
		if !h.Online || hostHasTag(h, "drain") {
			continue
		}
		rCaps, hasRepo := matchHostRepo(h)
		if enforceRepoPresence && !hasRepo {
			continue
		}
		matchesAllCaps := true
		for _, rc := range reqCaps {
			if !hostHasCapability(h, rc, rCaps) {
				matchesAllCaps = false
				break
			}
		}
		if !matchesAllCaps {
			continue
		}

		is247 := hostHasTag(h, "24/7") && !hostHasTag(h, "ephemeral") && h.RoutingWeight > 0
		if is247 {
			alwaysOnCandidates = append(alwaysOnCandidates, h)
		} else {
			ephemeralCandidates = append(ephemeralCandidates, h)
		}
	}

	// Fallback if strict capability filter yielded 0 candidates: relax capability filter across online 24/7 hosts
	if len(alwaysOnCandidates) == 0 && len(ephemeralCandidates) == 0 {
		for _, h := range allHosts {
			if !h.Online || hostHasTag(h, "drain") {
				continue
			}
			if hostHasTag(h, "24/7") && h.RoutingWeight > 0 {
				alwaysOnCandidates = append(alwaysOnCandidates, h)
			} else {
				ephemeralCandidates = append(ephemeralCandidates, h)
			}
		}
	}

	pool := alwaysOnCandidates
	poolKind := "24/7 weighted pool"
	if len(pool) == 0 {
		pool = ephemeralCandidates
		poolKind = "ephemeral fallback (all capable 24/7 hosts offline)"
	}
	if len(pool) == 0 {
		localRec := allHosts[0]
		return HostRoutingDecision{
			SelectedHost: localRec.Name,
			Reason:       "fallback to local daemon",
			LoadWarning:  formatOverloadWarning(localRec),
			Load:         localRec.Load,
		}
	}

	// 5. Deterministic Capacity-Adjusted Weighted Deficit Scoring
	totalWeight := 0.0
	for _, h := range pool {
		w := float64(h.RoutingWeight)
		if w <= 0 {
			w = 1.0
		}
		totalWeight += w
	}

	var bestHost *HostRecord
	bestScore := -1.0
	for _, h := range pool {
		w := float64(h.RoutingWeight)
		if w <= 0 {
			w = 1.0
		}
		normWeight := w / totalWeight
		effLoad := computeEffectiveLoad(h.Load)
		score := normWeight / (effLoad + 1.0)
		if score > bestScore || (score == bestScore && bestHost != nil && h.RoutingWeight > bestHost.RoutingWeight) {
			bestScore = score
			bestHost = h
		}
	}

	reason := fmt.Sprintf("auto-selected '%s' from %s (weight=%d, working=%d)",
		bestHost.Name, poolKind, bestHost.RoutingWeight, func() int {
			if bestHost.Load != nil {
				return bestHost.Load.WorkingSessions
			}
			return 0
		}())
	if len(reqCaps) > 0 {
		reason += fmt.Sprintf(" matching capabilities %v", reqCaps)
	}

	return HostRoutingDecision{
		SelectedHost: bestHost.Name,
		Reason:       reason,
		LoadWarning:  formatOverloadWarning(bestHost),
		Load:         bestHost.Load,
	}
}

func (s *Server) recordFleetLoadSamples(now time.Time) {
	if s.db == nil {
		return
	}
	for _, h := range s.GetAllFleetHosts() {
		if h.Online && h.Load != nil {
			_ = s.db.RecordHostLoadSample(h.Name, *h.Load, 0, now)
		}
	}
}

func (s *Server) handleHostUtilization(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	days := 14
	if dStr := r.URL.Query().Get("days"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil && d > 0 && d <= 90 {
			days = d
		}
	}
	hostFilter := strings.TrimSpace(r.URL.Query().Get("host"))
	since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)

	// Record a fresh sample so immediate queries always have current bucket data
	s.recordFleetLoadSamples(time.Now().UTC())

	metrics, err := s.db.GetHostLoadMetrics(hostFilter, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hosts := s.GetAllFleetHosts()

	type HourlyCell struct {
		Host               string  `json:"host"`
		HourUTC            int     `json:"hour_utc"`
		AvgWorkingSessions float64 `json:"avg_working_sessions"`
		MaxWorkingSessions int     `json:"max_working_sessions"`
		AvgCPUPct          float64 `json:"avg_cpu_pct"`
		TasksSpawned       int     `json:"tasks_spawned"`
		BucketsCount       int     `json:"buckets_count"`
	}

	type HostSummary struct {
		Host                 string   `json:"host"`
		DisplayName          string   `json:"display_name"`
		Online               bool     `json:"online"`
		PolicyTags           []string `json:"policy_tags"`
		DetectedCapabilities []string `json:"detected_capabilities"`
		RoutingWeight        int      `json:"routing_weight"`
		TasksSpawned         int      `json:"tasks_spawned"`
		ActualSharePct       float64  `json:"actual_share_pct"`
		AvgWorkingSessions   float64  `json:"avg_working_sessions"`
		PeakWorkingSessions  int      `json:"peak_working_sessions"`
		AvgCPUPct            float64  `json:"avg_cpu_pct"`
		PeakCPUPct           float64  `json:"peak_cpu_pct"`
		MinRAMFreeGB         float64  `json:"min_ram_free_gb"`
	}

	type cellKey struct {
		host string
		hour int
	}
	cellMap := make(map[cellKey]*HourlyCell)
	summaryMap := make(map[string]*HostSummary)
	totalSpawnedAllHosts := 0

	for _, h := range hosts {
		if hostFilter != "" && !strings.EqualFold(h.Name, hostFilter) {
			continue
		}
		summaryMap[h.Name] = &HostSummary{
			Host:                 h.Name,
			DisplayName:          h.DisplayName,
			Online:               h.Online,
			PolicyTags:           h.PolicyTags,
			DetectedCapabilities: h.DetectedCapabilities,
			RoutingWeight:        h.RoutingWeight,
		}
	}

	for _, m := range metrics {
		hr := m.BucketStart.UTC().Hour()
		ck := cellKey{host: m.Host, hour: hr}
		cell, ok := cellMap[ck]
		if !ok {
			cell = &HourlyCell{
				Host:               m.Host,
				HourUTC:            hr,
				AvgWorkingSessions: m.AvgWorkingSessions,
				MaxWorkingSessions: m.MaxWorkingSessions,
				AvgCPUPct:          m.AvgCPUPct,
				TasksSpawned:       m.TasksSpawnedCount,
				BucketsCount:       1,
			}
			cellMap[ck] = cell
		} else {
			n := float64(cell.BucketsCount)
			cell.AvgWorkingSessions = (cell.AvgWorkingSessions*n + m.AvgWorkingSessions) / (n + 1)
			if m.MaxWorkingSessions > cell.MaxWorkingSessions {
				cell.MaxWorkingSessions = m.MaxWorkingSessions
			}
			cell.AvgCPUPct = (cell.AvgCPUPct*n + m.AvgCPUPct) / (n + 1)
			cell.TasksSpawned += m.TasksSpawnedCount
			cell.BucketsCount++
		}

		sum, sOk := summaryMap[m.Host]
		if !sOk {
			defTags, defWeight := DefaultHostPolicy(m.Host)
			sum = &HostSummary{
				Host:          m.Host,
				DisplayName:   m.Host,
				PolicyTags:    defTags,
				RoutingWeight: defWeight,
			}
			summaryMap[m.Host] = sum
		}
		sum.TasksSpawned += m.TasksSpawnedCount
		totalSpawnedAllHosts += m.TasksSpawnedCount
		if m.MaxWorkingSessions > sum.PeakWorkingSessions {
			sum.PeakWorkingSessions = m.MaxWorkingSessions
		}
		if m.MaxCPUPct > sum.PeakCPUPct {
			sum.PeakCPUPct = m.MaxCPUPct
		}
		if sum.MinRAMFreeGB <= 0 || (m.MinRAMFreeGB > 0 && m.MinRAMFreeGB < sum.MinRAMFreeGB) {
			sum.MinRAMFreeGB = m.MinRAMFreeGB
		}
		sum.AvgWorkingSessions += m.AvgWorkingSessions
		sum.AvgCPUPct += m.AvgCPUPct
	}

	// Finalize averages and shares
	bucketCountsPerHost := make(map[string]int)
	for _, m := range metrics {
		bucketCountsPerHost[m.Host]++
	}
	var summaries []HostSummary
	for _, sum := range summaryMap {
		if cnt := bucketCountsPerHost[sum.Host]; cnt > 0 {
			sum.AvgWorkingSessions = math.Round((sum.AvgWorkingSessions/float64(cnt))*100) / 100
			sum.AvgCPUPct = math.Round((sum.AvgCPUPct/float64(cnt))*10) / 10
		}
		if totalSpawnedAllHosts > 0 {
			sum.ActualSharePct = math.Round((float64(sum.TasksSpawned)/float64(totalSpawnedAllHosts))*1000) / 10
		}
		summaries = append(summaries, *sum)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].RoutingWeight > summaries[j].RoutingWeight
	})

	var heatmap []HourlyCell
	for _, c := range cellMap {
		c.AvgWorkingSessions = math.Round(c.AvgWorkingSessions*100) / 100
		c.AvgCPUPct = math.Round(c.AvgCPUPct*10) / 10
		heatmap = append(heatmap, *c)
	}
	sort.Slice(heatmap, func(i, j int) bool {
		if heatmap[i].Host != heatmap[j].Host {
			return heatmap[i].Host < heatmap[j].Host
		}
		return heatmap[i].HourUTC < heatmap[j].HourUTC
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"days":           days,
		"hosts":          hosts,
		"host_summaries": summaries,
		"hourly_heatmap": heatmap,
		"metrics":        metrics,
	})
}
