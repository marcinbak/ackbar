package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultHostPolicy(t *testing.T) {
	tags, weight := DefaultHostPolicy("legion")
	if weight != 75 || len(tags) != 1 || tags[0] != "24/7" {
		t.Fatalf("expected legion => [24/7], 75; got %v, %d", tags, weight)
	}

	tags, weight = DefaultHostPolicy("macmini")
	if weight != 25 || len(tags) != 1 || tags[0] != "24/7" {
		t.Fatalf("expected macmini => [24/7], 25; got %v, %d", tags, weight)
	}

	tags, weight = DefaultHostPolicy("macbook-pro")
	if weight != 0 || len(tags) != 1 || tags[0] != "ephemeral" {
		t.Fatalf("expected macbook-pro => [ephemeral], 0; got %v, %d", tags, weight)
	}
}

func TestSelectTargetHost_CapabilityAndWeighted75_25(t *testing.T) {
	origDetect := DetectCapabilitiesFunc
	origProbe := ProbeSystemStatsFunc
	defer func() {
		DetectCapabilitiesFunc = origDetect
		ProbeSystemStatsFunc = origProbe
	}()

	DetectCapabilitiesFunc = func(home string) []string {
		return []string{"macos", "ios", "android", "agent:claude-code", "agent:antigravity"}
	}
	ProbeSystemStatsFunc = func() (float64, float64, float64, float64) {
		return 15.0, 12.0, 32.0, 200.0
	}

	dbPath := filepath.Join(t.TempDir(), "test_routing.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	srv.SetHostIdentity("macbook", "MacBook Pro")

	// Register remote 24/7 hosts: legion (75%, linux + android + mcp:gdrive) and macmini (25%, macos + ios + android)
	_ = db.SaveHost(&HostRecord{
		Name:                 "legion",
		URL:                  "http://legion:7777",
		PolicyTags:           []string{"24/7"},
		RoutingWeight:        75,
		HasExplicitWeight:    true,
		DetectedCapabilities: []string{"linux", "android", "docker", "gpu", "agent:claude-code", "mcp:gdrive"},
	})
	_ = db.SaveHost(&HostRecord{
		Name:                 "macmini",
		URL:                  "http://macmini:7777",
		PolicyTags:           []string{"24/7"},
		RoutingWeight:        25,
		HasExplicitWeight:    true,
		DetectedCapabilities: []string{"macos", "ios", "android", "agent:claude-code"},
	})

	legionLoad := &HostLoadSnapshot{
		WorkingSessions: 0,
		CPULoadPct:      10.0,
		RAMFreeGB:       24.0,
		RAMTotalGB:      32.0,
		DiskFreeGB:      500.0,
	}
	macminiLoad := &HostLoadSnapshot{
		WorkingSessions: 0,
		CPULoadPct:      10.0,
		RAMFreeGB:       12.0,
		RAMTotalGB:      16.0,
		DiskFreeGB:      250.0,
	}

	srv.updateHostHealthWithTelemetry("legion", true, "1.0.0", "Legion", 5,
		[]string{"linux", "android", "docker", "gpu", "agent:claude-code", "mcp:gdrive"},
		[]string{"24/7"}, 75, legionLoad)
	srv.updateHostHealthWithTelemetry("macmini", true, "1.0.0", "Mac Mini", 5,
		[]string{"macos", "ios", "android", "agent:claude-code"},
		[]string{"24/7"}, 25, macminiLoad)

	ctx := context.Background()

	// 1. iOS task MUST route to macmini (even though legion has weight 75 and macbook has ios, macbook is ephemeral)
	resIOS := srv.SelectTargetHost(ctx, HostRoutingRequest{
		TargetHost:           "auto",
		Agent:                "claude-code",
		RequiredCapabilities: []string{"ios"},
	})
	if resIOS.SelectedHost != "macmini" {
		t.Fatalf("expected iOS task to route to macmini, got %s (reason: %s)", resIOS.SelectedHost, resIOS.Reason)
	}

	// 2. Non-iOS tasks (e.g. Android / backend) across 4 sequential dispatches should distribute 3 to legion (75%) and 1 to macmini (25%)
	counts := map[string]int{}
	for i := 0; i < 4; i++ {
		res := srv.SelectTargetHost(ctx, HostRoutingRequest{
			TargetHost:           "auto",
			Agent:                "claude-code",
			RequiredCapabilities: []string{"android"},
		})
		counts[res.SelectedHost]++
		if res.SelectedHost == "legion" {
			legionLoad.WorkingSessions++
			srv.updateHostHealthWithTelemetry("legion", true, "1.0.0", "Legion", 5, nil, []string{"24/7"}, 75, legionLoad)
		} else if res.SelectedHost == "macmini" {
			macminiLoad.WorkingSessions++
			srv.updateHostHealthWithTelemetry("macmini", true, "1.0.0", "Mac Mini", 5, nil, []string{"24/7"}, 25, macminiLoad)
		}
	}
	if counts["legion"] != 3 || counts["macmini"] != 1 {
		t.Fatalf("expected 75/25 distribution (legion=3, macmini=1), got %+v", counts)
	}
	if counts["macbook"] != 0 {
		t.Fatalf("expected ephemeral macbook to receive 0 auto-routed tasks while 24/7 hosts are online, got %d", counts["macbook"])
	}

	// 3. Dynamic CPU/RAM headroom tilting: if legion has 2 working sessions AND high CPU/low RAM, next task tilts to idle macmini
	legionLoad.WorkingSessions = 2
	legionLoad.CPULoadPct = 82.0
	legionLoad.RAMFreeGB = 3.0
	macminiLoad.WorkingSessions = 0
	macminiLoad.CPULoadPct = 10.0
	macminiLoad.RAMFreeGB = 12.0
	srv.updateHostHealthWithTelemetry("legion", true, "1.0.0", "Legion", 5, nil, []string{"24/7"}, 75, legionLoad)
	srv.updateHostHealthWithTelemetry("macmini", true, "1.0.0", "Mac Mini", 5, nil, []string{"24/7"}, 25, macminiLoad)

	resTilt := srv.SelectTargetHost(ctx, HostRoutingRequest{
		TargetHost:           "auto",
		Agent:                "claude-code",
		RequiredCapabilities: []string{"android"},
	})
	if resTilt.SelectedHost != "macmini" {
		t.Fatalf("expected headroom tilt to select macmini when legion is under heavy CPU/RAM pressure, got %s", resTilt.SelectedHost)
	}

	// 4. Overload is non-blocking and emits LoadWarning
	macminiLoad.Overloaded = true
	macminiLoad.CPULoadPct = 95.0
	srv.updateHostHealthWithTelemetry("macmini", true, "1.0.0", "Mac Mini", 5, nil, []string{"24/7"}, 25, macminiLoad)
	resOverload := srv.SelectTargetHost(ctx, HostRoutingRequest{
		TargetHost:           "auto",
		Agent:                "claude-code",
		RequiredCapabilities: []string{"ios"},
	})
	if resOverload.SelectedHost != "macmini" {
		t.Fatalf("expected overloaded macmini to still be selected for iOS without blocking, got %s", resOverload.SelectedHost)
	}
	if resOverload.LoadWarning == "" {
		t.Fatalf("expected non-empty LoadWarning when selected host is overloaded")
	}

	// 5. Fallback to ephemeral macbook only when all 24/7 hosts are offline
	srv.updateHostHealthWithTelemetry("legion", false, "", "", 0, nil, nil, 0, nil)
	srv.updateHostHealthWithTelemetry("macmini", false, "", "", 0, nil, nil, 0, nil)
	resFallback := srv.SelectTargetHost(ctx, HostRoutingRequest{
		TargetHost:           "auto",
		Agent:                "claude-code",
		RequiredCapabilities: []string{"ios"},
	})
	if resFallback.SelectedHost != "macbook" {
		t.Fatalf("expected fallback to local macbook when all 24/7 hosts are offline, got %s", resFallback.SelectedHost)
	}
}

func TestHostLoadMetrics_RollupAndUtilizationAPI(t *testing.T) {
	origProbe := ProbeSystemStatsFunc
	defer func() { ProbeSystemStatsFunc = origProbe }()
	ProbeSystemStatsFunc = func() (float64, float64, float64, float64) {
		return 20.0, 24.0, 32.0, 400.0
	}

	dbPath := filepath.Join(t.TempDir(), "test_metrics.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	srv.SetHostIdentity("legion", "Legion Server")

	now := time.Now().UTC().Truncate(time.Hour)
	// Record recent 5m samples for legion and macmini
	if err := db.RecordHostLoadSample("legion", HostLoadSnapshot{WorkingSessions: 3, QueuedWorkstreams: 1, CPULoadPct: 40.0, RAMFreeGB: 20.0}, 3, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("RecordHostLoadSample legion failed: %v", err)
	}
	if err := db.RecordHostLoadSample("macmini", HostLoadSnapshot{WorkingSessions: 1, QueuedWorkstreams: 0, CPULoadPct: 25.0, RAMFreeGB: 12.0}, 1, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("RecordHostLoadSample macmini failed: %v", err)
	}

	// Record 8-day-old 5m samples that should be compacted into 1h rollup
	oldTime := now.Add(-8 * 24 * time.Hour)
	if err := db.RecordHostLoadSample("legion", HostLoadSnapshot{WorkingSessions: 2, CPULoadPct: 30.0, RAMFreeGB: 22.0}, 2, oldTime); err != nil {
		t.Fatalf("RecordHostLoadSample old 1 failed: %v", err)
	}
	if err := db.RecordHostLoadSample("legion", HostLoadSnapshot{WorkingSessions: 4, CPULoadPct: 50.0, RAMFreeGB: 18.0}, 1, oldTime.Add(5*time.Minute)); err != nil {
		t.Fatalf("RecordHostLoadSample old 2 failed: %v", err)
	}

	if err := db.RollupAndPruneHostLoadMetrics(now); err != nil {
		t.Fatalf("RollupAndPruneHostLoadMetrics failed: %v", err)
	}

	metrics, err := db.GetHostLoadMetrics("", now.Add(-14*24*time.Hour))
	if err != nil {
		t.Fatalf("GetHostLoadMetrics failed: %v", err)
	}

	found1h := false
	for _, m := range metrics {
		if m.Host == "legion" && m.BucketSec == 3600 {
			found1h = true
			if m.TasksSpawnedCount != 3 {
				t.Errorf("expected compacted 1h tasks_spawned_count=3, got %d", m.TasksSpawnedCount)
			}
			if m.MaxWorkingSessions != 4 {
				t.Errorf("expected compacted 1h max_working_sessions=4, got %d", m.MaxWorkingSessions)
			}
		}
	}
	if !found1h {
		t.Errorf("expected 8-day-old 5m buckets to be rolled up into a 1h bucket (3600s), got %+v", metrics)
	}

	// Verify GET /v1/hosts/utilization?days=14
	req := httptest.NewRequest(http.MethodGet, "/v1/hosts/utilization?days=14", nil)
	w := httptest.NewRecorder()
	srv.handleHostUtilization(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/hosts/utilization returned %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Days          int `json:"days"`
		HostSummaries []struct {
			Host           string  `json:"host"`
			RoutingWeight  int     `json:"routing_weight"`
			TasksSpawned   int     `json:"tasks_spawned"`
			ActualSharePct float64 `json:"actual_share_pct"`
		} `json:"host_summaries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode utilization response: %v", err)
	}
	totalSpawned := 0
	for _, hs := range resp.HostSummaries {
		totalSpawned += hs.TasksSpawned
	}
	if totalSpawned != 7 {
		t.Errorf("expected total tasks_spawned=7 across summaries, got %d (%+v)", totalSpawned, resp.HostSummaries)
	}

	// Verify GET /v1/version returns capabilities, policy_tags, routing_weight, and load
	reqVer := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	wVer := httptest.NewRecorder()
	srv.handleVersion(wVer, reqVer)
	if wVer.Code != http.StatusOK {
		t.Fatalf("GET /v1/version returned %d", wVer.Code)
	}
	if !strings.Contains(wVer.Body.String(), `"routing_weight":75`) {
		t.Errorf("expected /v1/version for legion to include routing_weight:75, got %s", wVer.Body.String())
	}

	// Verify POST /v1/hosts for local host updates local policy & weight in settings without polluting remote hosts table
	bodyLocal, _ := json.Marshal(map[string]any{
		"name":                "legion",
		"policy_tags":         []string{"24/7", "priority"},
		"routing_weight":      80,
		"has_explicit_weight": true,
	})
	reqPost := httptest.NewRequest(http.MethodPost, "/v1/hosts", bytes.NewReader(bodyLocal))
	wPost := httptest.NewRecorder()
	srv.handleHosts(wPost, reqPost)
	if wPost.Code != http.StatusOK {
		t.Fatalf("POST /v1/hosts for local host returned %d: %s", wPost.Code, wPost.Body.String())
	}
	tags, weight := srv.LocalPolicyAndWeight()
	if weight != 80 || len(tags) != 2 {
		t.Errorf("expected updated local policy [24/7 priority] and weight 80, got %v, %d", tags, weight)
	}

	// Verify UpdateOutdatedRemoteHosts skips offline and already up-to-date remote hosts
	_ = db.SaveHost(&HostRecord{Name: "macmini", URL: "http://127.0.0.1:59991"})
	_ = db.SaveHost(&HostRecord{Name: "devbox", URL: "http://127.0.0.1:59992"})
	srv.updateHostHealth("macmini", true, "20261104.08", "Mac Mini", 5)
	srv.updateHostHealth("devbox", false, "20261104.01", "Dev Box", 0)

	updSummaries := srv.UpdateOutdatedRemoteHosts("20261104.08")
	if len(updSummaries) != 2 {
		t.Fatalf("expected 2 host update summaries, got %d: %+v", len(updSummaries), updSummaries)
	}
	for _, us := range updSummaries {
		switch us.Host {
		case "macmini":
			if us.Status != "up_to_date" {
				t.Errorf("expected macmini status=up_to_date, got %s", us.Status)
			}
		case "devbox":
			if us.Status != "skipped_offline" {
				t.Errorf("expected devbox status=skipped_offline, got %s", us.Status)
			}
		}
	}
}

func TestCLIInitPromptAndProcessScannerCompatibility(t *testing.T) {
	claudeCmd := appendCLIInitPrompt("claude-code", "claude --session-id 550e8400-e29b-41d4-a716-446655440000")
	if claudeCmd != `claude --session-id 550e8400-e29b-41d4-a716-446655440000 "$ACKBAR_INIT_PROMPT"` {
		t.Fatalf("unexpected claude init prompt cmd: %s", claudeCmd)
	}

	agyCmd := appendCLIInitPrompt("antigravity", "agy")
	if agyCmd != `agy -i "$ACKBAR_INIT_PROMPT"` {
		t.Fatalf("unexpected antigravity init prompt cmd: %s", agyCmd)
	}

	if other := appendCLIInitPrompt("mock-agent", "sleep 5"); other != "" {
		t.Fatalf("expected empty CLI init prompt for mock-agent, got %s", other)
	}

	claudeAdapter := ClaudeAgentAdapter{}
	agyAdapter := AntigravityAgentAdapter{}

	// Interactive Claude session whose prompt mentions "ackbar", "ackbard", "update", "mcp", and "-p"
	claudePS := "claude --session-id 550e8400-e29b-41d4-a716-446655440000 update the mcp server in ackbard and test -p flag"
	if !claudeAdapter.MatchProcessCommand("claude", strings.ToLower(claudePS)) {
		t.Errorf("expected ClaudeAgentAdapter.MatchProcessCommand to match interactive claude session with ackbard in prompt")
	}
	if claudeAdapter.IsIgnoredCommand(claudePS) {
		t.Errorf("expected ClaudeAgentAdapter.IsIgnoredCommand to be false for interactive claude session with prompt")
	}

	// Interactive Antigravity session with -i whose prompt starts with an ignored subcommand keyword ("update") and mentions "ackbar-hook"
	agyPS := "agy -i update the mcp server in ackbar-hook and check --prompt handling"
	if !agyAdapter.MatchProcessCommand("agy", strings.ToLower(agyPS)) {
		t.Errorf("expected AntigravityAgentAdapter.MatchProcessCommand to match interactive agy session with ackbar-hook in prompt")
	}
	if agyAdapter.IsIgnoredCommand(agyPS) {
		t.Errorf("expected AntigravityAgentAdapter.IsIgnoredCommand to be false for interactive agy -i session")
	}

	// Genuine subcommands and non-interactive invocations MUST still be ignored
	for _, ignored := range []string{
		"claude mcp serve",
		"claude update",
		"claude -p summarize this",
		"claude --version",
	} {
		if !claudeAdapter.IsIgnoredCommand(ignored) {
			t.Errorf("expected claude command %q to be ignored", ignored)
		}
	}
	for _, ignored := range []string{
		"agy update",
		"agy mcp",
		"agy --prompt summarize this",
		"agy --help",
	} {
		if !agyAdapter.IsIgnoredCommand(ignored) {
			t.Errorf("expected agy command %q to be ignored", ignored)
		}
	}

	// Internal ackbar binaries must NOT match agent process commands
	if claudeAdapter.MatchProcessCommand("ackbar-hook", "ackbar-hook --agent=claude-code --event=Stop") {
		t.Errorf("expected ackbar-hook to be excluded from ClaudeAgentAdapter.MatchProcessCommand")
	}
	if agyAdapter.MatchProcessCommand("ackbar-hook", "ackbar-hook --agent=antigravity --event=Stop") {
		t.Errorf("expected ackbar-hook to be excluded from AntigravityAgentAdapter.MatchProcessCommand")
	}
}

func TestRepoPathCapabilityInferenceAndTaskActivationOnSpawn(t *testing.T) {
	origDetect := DetectCapabilitiesFunc
	origProbe := ProbeSystemStatsFunc
	defer func() {
		DetectCapabilitiesFunc = origDetect
		ProbeSystemStatsFunc = origProbe
	}()

	DetectCapabilitiesFunc = func(home string) []string {
		return []string{"linux", "android", "agent:claude-code", "agent:antigravity"}
	}
	ProbeSystemStatsFunc = func() (float64, float64, float64, float64) {
		return 10.0, 20.0, 32.0, 200.0
	}

	dbPath := filepath.Join(t.TempDir(), "test_spawn_task.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	srv.SetHostIdentity("legion", "Legion")

	// Mock remote macmini server to receive proxied /v1/sessions/spawn for iOS repo
	var remoteSpawnReceived map[string]any
	remoteMacmini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sessions/spawn" {
			_ = json.NewDecoder(r.Body).Decode(&remoteSpawnReceived)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":     "spawning",
				"id":         "claude-code:macmini:550e8400-e29b-41d4-a716-446655440000",
				"session_id": "550e8400-e29b-41d4-a716-446655440000",
				"host":       "macmini",
			})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer remoteMacmini.Close()

	_ = db.SaveHost(&HostRecord{
		Name:                 "macmini",
		URL:                  remoteMacmini.URL,
		PolicyTags:           []string{"24/7"},
		RoutingWeight:        25,
		HasExplicitWeight:    true,
		DetectedCapabilities: []string{"macos", "ios", "android", "agent:claude-code"},
	})
	srv.updateHostHealthWithTelemetry("macmini", true, "1.0.0", "Mac Mini", 5,
		[]string{"macos", "ios", "android", "agent:claude-code"},
		[]string{"24/7"}, 25, &HostLoadSnapshot{RAMFreeGB: 12.0, RAMTotalGB: 16.0})

	// Create a NEW task on legion
	now := time.Now().UTC()
	task := &Task{
		ID:          "task_ios_test_1",
		Title:       "Fix iOS push notifications",
		ProjectName: "NGL",
		GroupName:   "Modemobile",
		Status:      "NEW",
		Substatus:   "ready",
		TaskKind:    "code",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Spawn a worker session for Modemobile/NGL/ngl-ios WITHOUT explicit host or required_capabilities
	spawnBody, _ := json.Marshal(map[string]any{
		"agent":          "claude-code",
		"node_path":      "Modemobile/NGL/ngl-ios",
		"repo_node_path": "Modemobile/NGL/ngl-ios",
		"task_id":        "task_ios_test_1",
		"prompt":         "Fix APNs token registration",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(spawnBody))
	w := httptest.NewRecorder()
	srv.handleSpawn(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleSpawn returned %d: %s", w.Code, w.Body.String())
	}
	if remoteSpawnReceived == nil {
		t.Fatalf("expected Modemobile/NGL/ngl-ios spawn to auto-infer ios capability and route to remote macmini")
	}

	// Verify task on legion transitioned from NEW to IN_PROGRESS (active) and recorded the worker
	updatedTask, err := db.GetTaskByID("task_ios_test_1")
	if err != nil || updatedTask == nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if updatedTask.Status != "IN_PROGRESS" || updatedTask.Substatus != "active" {
		t.Fatalf("expected task status=IN_PROGRESS and substatus=active after worker spawn, got status=%s substatus=%s",
			updatedTask.Status, updatedTask.Substatus)
	}
	if len(updatedTask.Workers) != 1 || updatedTask.Workers[0].Host != "macmini" || updatedTask.Workers[0].RepoNodePath != "Modemobile/NGL/ngl-ios" {
		t.Fatalf("expected 1 active worker on macmini with RepoNodePath=Modemobile/NGL/ngl-ios recorded on task, got %+v", updatedTask.Workers)
	}
	if caps := inferCapabilitiesFromRepoPath("Mobile/ios/client"); len(caps) != 1 || caps[0] != "ios" {
		t.Fatalf("expected Mobile/ios/client to infer ios capability, got %v", caps)
	}
}

func TestSourceDirVersionGuard(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "cmd", "ackbard"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "internal", "version"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "cmd", "ackbard", "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "internal", "version", "VERSION"), []byte("20261106.01\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := readSourceDirVersion(tmp); got != "20261106.01" {
		t.Fatalf("expected readSourceDirVersion=20261106.01, got %q", got)
	}

	// Ensure findUpToDateSourceDir rejects any local repo when targetVersion is far in the future
	if dir := findUpToDateSourceDir("20991231.99"); dir != "" {
		t.Fatalf("expected findUpToDateSourceDir to reject stale local checkouts when targetVersion=20991231.99, got %q", dir)
	}
}
