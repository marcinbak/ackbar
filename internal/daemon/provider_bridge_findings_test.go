package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ackbar/internal/agent"
)

type bareNoSubPlugin struct {
	agent.BasePlugin
	agent.BaseTranscripts
}

func (p *bareNoSubPlugin) Key() string         { return "plugin-nosub" }
func (p *bareNoSubPlugin) DisplayName() string { return "NoSub" }
func (p *bareNoSubPlugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{SupportsTmux: true, SupportsSubagents: false}
}

type scannerPlugin struct {
	agent.BasePlugin
	agent.BaseAccounts
	agent.BaseScanner
	key        string
	aliases    []string
	scanCalls  int
	setupCalls int
	updates    []agent.DiskSessionMetadataUpdate
}

func (p *scannerPlugin) Key() string         { return p.key }
func (p *scannerPlugin) DisplayName() string { return p.key }
func (p *scannerPlugin) Aliases() []string   { return p.aliases }
func (p *scannerPlugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{SupportsTmux: true}
}
func (p *scannerPlugin) SetupProfileDir(homeDir, name, configDir string) (string, error) {
	p.setupCalls++
	return filepath.Join(homeDir, "custom-profile-"+p.key, name), nil
}
func (p *scannerPlugin) ScanDisk(ctx context.Context, in agent.DiskScanInput) agent.DiskScanResult {
	p.scanCalls++
	return agent.DiskScanResult{MetadataUpdates: p.updates}
}

func TestServer_ConcurrentPluginRegistryAndCollision(t *testing.T) {
	srv := &Server{}
	srv.RegisterProvider(&MockProvider{})

	colliding := &scannerPlugin{key: "new-agent", aliases: []string{"MOCK-AGENT"}}
	if err := srv.RegisterPlugin(colliding); err == nil {
		t.Fatal("expected collision with existing legacy provider")
	}
	if _, ok := srv.PluginRegistry().Get("new-agent"); ok {
		t.Fatal("colliding plugin should not enter registry")
	}

	okPlugin := &scannerPlugin{key: "plugin-y", aliases: []string{"py"}}
	if err := srv.RegisterPlugin(okPlugin); err != nil {
		t.Fatalf("RegisterPlugin: %v", err)
	}

	// RegisterProvider must not overwrite an existing registered plugin alias.
	srv.RegisterProvider(&namedMockProvider{agentName: "py"})
	if got, ok := srv.resolveProvider("py"); !ok || got.Agent() != "plugin-y" {
		t.Fatalf("resolveProvider('py') = (%v, %v), want plugin-y", got, ok)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := &scannerPlugin{key: fmt.Sprintf("conc-%d", idx), aliases: []string{fmt.Sprintf("ca-%d", idx)}}
			_ = srv.RegisterPlugin(p)
			_, _ = srv.resolveProvider(p.key)
			_ = srv.snapshotProviders()
			rec := httptest.NewRecorder()
			srv.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/v1/providers", nil))
			rec2 := httptest.NewRecorder()
			srv.handleAgentDiscovery(rec2, httptest.NewRequest(http.MethodGet, "/v1/agents/discovery", nil))
		}(i)
	}
	wg.Wait()
}

func TestPluginBridge_SubagentDiscoveryGateAndInspectNoop(t *testing.T) {
	srv := NewServer(nil)
	p := &bareNoSubPlugin{}
	if err := srv.RegisterPlugin(p); err != nil {
		t.Fatalf("RegisterPlugin: %v", err)
	}

	prov, ok := srv.resolveProvider("plugin-nosub")
	if !ok {
		t.Fatal("expected resolved provider")
	}
	bridge := prov.(*PluginBridge)
	if bridge.SupportsSubagentDiscovery() {
		t.Fatal("expected SupportsSubagentDiscovery == false when SupportsSubagents is false")
	}

	sess := &Session{ID: "plugin-nosub:local:s1", Agent: "plugin-nosub", State: StateWorking, Activity: "Busy"}
	if bridge.InspectStatus(context.Background(), sess) {
		t.Fatal("expected InspectStatus == false when TMuxDriver is absent")
	}
	if sess.State != StateWorking || sess.Activity != "Busy" {
		t.Fatalf("InspectStatus mutated session: %+v", sess)
	}

	// Subagent started 5m ago (>2m disk threshold, <15m fallback TTL) must not be pruned.
	srv.activeSubagents[sess.ID] = []*SubagentInfo{{
		ID:        "sub-old",
		Name:      "Worker",
		State:     "running",
		StartedAt: time.Now().Add(-5 * time.Minute),
	}}
	got := srv.getRunningSubagents(sess.ID, "s1", "plugin-nosub", t.TempDir())
	if len(got) != 1 || got[0].ID != "sub-old" {
		t.Fatalf("expected 5m subagent preserved under 15m TTL, got %+v", got)
	}
}

func TestServer_ScanDiskSessionsAndSetupProfileDirectory(t *testing.T) {
	srv, db := setupTestServer(t)
	plg := &scannerPlugin{
		key:     "plugin-scan",
		aliases: []string{"ps"},
		updates: []agent.DiskSessionMetadataUpdate{{SessionID: "plugin-scan:local:1", Title: "Updated From Scan"}},
	}
	if err := srv.RegisterPlugin(plg); err != nil {
		t.Fatalf("RegisterPlugin: %v", err)
	}

	sess := &Session{ID: "plugin-scan:local:1", NativeID: "1", Agent: "plugin-scan", Host: "local", Name: "Initial"}
	st := &ScanState{
		hostName:         "local",
		existingSessions: []*Session{sess},
		knownIDs:         map[string]*Session{sess.ID: sess},
	}
	srv.scanDiskSessions(context.Background(), st)
	if plg.scanCalls != 1 || sess.Name != "Updated From Scan" {
		t.Fatalf("scanDiskSessions calls=%d name=%q", plg.scanCalls, sess.Name)
	}

	body := []byte(`{"agent":"ps","name":"work"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts", bytes.NewReader(body))
	srv.handleAccounts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/accounts status=%d body=%s", rec.Code, rec.Body.String())
	}
	var acc AgentAccount
	if err := json.NewDecoder(rec.Body).Decode(&acc); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	if plg.setupCalls != 1 || filepath.Base(filepath.Dir(acc.ConfigDir)) != "custom-profile-plugin-scan" {
		t.Fatalf("setupCalls=%d configDir=%q db=%v", plg.setupCalls, acc.ConfigDir, db != nil)
	}
}

func TestApplyDiskScanResult_AdoptionGuardsAndTaskReassignment(t *testing.T) {
	srv, db := setupTestServer(t)

	oldID := "plugin-x:local:temp-1"
	newNative := "real-1"
	newID := "plugin-x:local:" + newNative
	oldSess := &Session{ID: oldID, NativeID: "temp-1", Agent: "plugin-x", Host: "local", CurrentTaskID: "task-adopt-1"}
	if err := db.SaveSession(oldSess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := db.CreateTask(&Task{
		ID: "task-adopt-1", Title: "Adopt", Status: "IN_PROGRESS",
		Workers:   []TaskWorker{{SessionID: oldID, IsActive: true}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	liveCollision := &Session{ID: "plugin-x:local:occupied", NativeID: "occupied", Agent: "plugin-x", Host: "local"}
	st := &ScanState{
		hostName: "local",
		knownIDs: map[string]*Session{oldID: oldSess, liveCollision.ID: liveCollision},
		// Intentionally nil knownByNativeID to verify nil-map safety.
	}

	// Empty NewNativeID and collision must not delete oldSess or overwrite liveCollision.
	applyDiskScanResult(srv, st, agent.DiskScanResult{
		AdoptUpdates: []agent.DiskSessionAdoption{
			{OldSessionID: oldID, NewNativeID: "   "},
			{OldSessionID: oldID, NewNativeID: "occupied"},
		},
	})
	if st.knownIDs[oldID] != oldSess || st.knownIDs[liveCollision.ID] != liveCollision {
		t.Fatal("empty/colliding adoption altered sessions unexpectedly")
	}

	applyDiskScanResult(srv, st, agent.DiskScanResult{
		AdoptUpdates: []agent.DiskSessionAdoption{{OldSessionID: oldID, NewNativeID: newNative, Title: "Adopted"}},
	})
	if _, ok := st.knownIDs[oldID]; ok {
		t.Fatal("oldSessionID should be removed after adoption")
	}
	if st.knownIDs[newID] != oldSess || st.knownByNativeID[newNative] != oldSess || oldSess.ID != newID {
		t.Fatalf("adoption map state mismatch: id=%q", oldSess.ID)
	}
	task, err := db.GetTaskByID("task-adopt-1")
	if err != nil || task == nil || len(task.Workers) == 0 || task.Workers[0].SessionID != newID {
		t.Fatalf("expected task worker reassigned to %q, got %+v err=%v", newID, task, err)
	}
}

type detectOnlyBridgePlugin struct {
	agent.BasePlugin
	agent.BaseInstallationDetector
	installed bool
	gotHome   string
}

func (p *detectOnlyBridgePlugin) Key() string         { return "detect-bridge" }
func (p *detectOnlyBridgePlugin) DisplayName() string { return "Detect Bridge" }
func (p *detectOnlyBridgePlugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{SupportsTmux: true}
}
func (p *detectOnlyBridgePlugin) IsInstalled(homeDir string) bool {
	p.gotHome = homeDir
	return p.installed
}

func TestPluginBridge_InstallationDetectorWithoutProvisioner(t *testing.T) {
	srv := NewServer(nil)
	srv.homeDir = "/tmp/custom-ackbar-home"
	plg := &detectOnlyBridgePlugin{installed: true}
	if err := srv.RegisterPlugin(plg); err != nil {
		t.Fatalf("RegisterPlugin: %v", err)
	}
	prov, ok := srv.resolveProvider("detect-bridge")
	if !ok {
		t.Fatal("expected resolved provider")
	}
	if !prov.IsInstalled() || plg.gotHome != srv.homeDir {
		t.Fatalf("IsInstalled() = %v home=%q, want true / %q", prov.IsInstalled(), plg.gotHome, srv.homeDir)
	}
	if _, isProv := srv.PluginRegistry().AsProvisioner("detect-bridge"); isProv {
		t.Fatal("detectOnlyBridgePlugin must not satisfy Provisioner")
	}
}

func TestServer_CodexAndOpenCodePluginWiring(t *testing.T) {
	srv, _ := setupTestServer(t)

	// OpenCode alias resolves cleanly and exposes InstallationDetector without Provisioner.
	if p, ok := srv.resolveProvider("open-code"); !ok || p.Agent() != "opencode" {
		t.Fatalf("resolveProvider('open-code') = (%v, %v)", p, ok)
	}
	if _, ok := srv.PluginRegistry().AsInstallationDetector("opencode"); !ok {
		t.Error("expected opencode InstallationDetector")
	}
	if _, ok := srv.PluginRegistry().AsProvisioner("opencode"); ok {
		t.Error("opencode must not satisfy Provisioner")
	}

	// Codex is included dynamically in /v1/accounts via SupportsMultiAccount and resolves CODEX_HOME.
	accRec := httptest.NewRecorder()
	srv.handleAccounts(accRec, httptest.NewRequest(http.MethodGet, "/v1/accounts", nil))
	if accRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/accounts = %d", accRec.Code)
	}
	var accts []AgentAccount
	if err := json.Unmarshal(accRec.Body.Bytes(), &accts); err != nil {
		t.Fatal(err)
	}
	seenCodexDefault := false
	for _, a := range accts {
		if a.ID == "codex:default" {
			seenCodexDefault = true
		}
		if a.Agent == "opencode" {
			t.Errorf("opencode should not get default multi-account entry: %+v", a)
		}
	}
	if !seenCodexDefault {
		t.Errorf("expected codex:default in GET /v1/accounts, got %+v", accts)
	}

	postRec := httptest.NewRecorder()
	srv.handleAccounts(postRec, httptest.NewRequest(http.MethodPost, "/v1/accounts", bytes.NewBufferString(`{"agent":"codex","name":"work"}`)))
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /v1/accounts codex = %d: %s", postRec.Code, postRec.Body.String())
	}
	var created AgentAccount
	if err := json.Unmarshal(postRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	_, env := srv.resolveAccountEnv("codex", "work", "/tmp/home", false)
	if env["CODEX_HOME"] == "" || env["CODEX_HOME"] != created.ConfigDir {
		t.Fatalf("expected CODEX_HOME == %q in account env, got %+v", created.ConfigDir, env)
	}

	// LaunchSpec PromptInCommand differentiation: grok=true (when prompt non-empty), codex=false, opencode=false.
	for _, tc := range []struct {
		agent string
		want  bool
	}{
		{"grok", true},
		{"codex", false},
		{"opencode", false},
	} {
		td, ok := srv.PluginRegistry().AsTMuxDriver(tc.agent)
		if !ok {
			t.Fatalf("missing TMuxDriver for %s", tc.agent)
		}
		spec, err := td.BuildLaunchSpec(agent.LaunchInput{Prompt: "run tests"})
		if err != nil || spec.PromptInCommand != tc.want {
			t.Errorf("%s BuildLaunchSpec PromptInCommand = %v (err=%v), want %v", tc.agent, spec.PromptInCommand, err, tc.want)
		}
	}
}
