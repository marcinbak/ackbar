package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
)

type stubBridgePlugin struct {
	agent.BasePlugin
	agent.BaseProvisioner
	agent.BaseAccounts
	agent.BaseTMuxDriver
	agent.BaseHooks
	agent.BaseTranscripts
	agent.BaseScanner

	cleanedNativeID string
	subagents       []agent.SubagentInfo
	activeSubagents []*agent.ActiveSubagent
	docs            []agent.WorkspaceDoc
	scanPruneIDs    []string
}

func (s *stubBridgePlugin) Key() string         { return "plugin-x" }
func (s *stubBridgePlugin) DisplayName() string { return "Plugin X" }
func (s *stubBridgePlugin) Aliases() []string   { return []string{"px"} }
func (s *stubBridgePlugin) BinaryNames() []string {
	return []string{"pluginx"}
}
func (s *stubBridgePlugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		SupportsTmux:          true,
		SupportsTranscript:    true,
		SupportsSubagents:     true,
		SupportsWorkspaceDocs: true,
	}
}
func (s *stubBridgePlugin) BrandColor() string                  { return "#112233" }
func (s *stubBridgePlugin) IconSVG() string                     { return "<svg/>" }
func (s *stubBridgePlugin) IsInstalled(homeDir string) bool     { return true }
func (s *stubBridgePlugin) SpawnCommand(tempUUID string) string { return "pluginx -s " + tempUUID }
func (s *stubBridgePlugin) ResumeCommand(nativeID string) string {
	return "pluginx -r " + nativeID
}
func (s *stubBridgePlugin) CheckHookConfig(homeDir string) (bool, string, error) {
	return true, "ackbar-hook --agent=plugin-x", nil
}
func (s *stubBridgePlugin) ParseHook(eventName string, payload []byte) (*agent.Event, error) {
	return &agent.Event{
		Agent:       "plugin-x",
		NativeID:    "nat-1",
		EventName:   eventName,
		State:       agent.StateWorking,
		Activity:    "hook ok",
		LastEventAt: time.Now(),
	}, nil
}
func (s *stubBridgePlugin) ReadSessionMetadata(homeDir, cwd, nativeID string) *agent.SessionMeta {
	return &agent.SessionMeta{Title: "Bridge Title"}
}
func (s *stubBridgePlugin) ResolveSessionTitle(homeDir, cwd, nativeID string) string {
	return "Bridge Title"
}
func (s *stubBridgePlugin) ExtractTranscript(homeDir, cwd, nativeID string) (*agent.Transcript, error) {
	return &agent.Transcript{
		Agent:    "plugin-x",
		NativeID: nativeID,
		Messages: []agent.TranscriptMessage{{Role: "user", Content: "hi"}},
	}, nil
}
func (s *stubBridgePlugin) CleanSessionFiles(homeDir, cwd, nativeID string) error {
	s.cleanedNativeID = nativeID
	return nil
}
func (s *stubBridgePlugin) ListSubagents(homeDir, cwd, nativeID string) ([]agent.SubagentInfo, error) {
	return s.subagents, nil
}
func (s *stubBridgePlugin) ListActiveSubagents(homeDir, cwd, nativeID string) ([]*agent.ActiveSubagent, error) {
	return s.activeSubagents, nil
}
func (s *stubBridgePlugin) ListWorkspaceDocuments(homeDir, cwd, nativeID string) []agent.WorkspaceDoc {
	return s.docs
}
func (s *stubBridgePlugin) InspectStatus(ctx context.Context, sess *agent.Session) bool {
	return true
}
func (s *stubBridgePlugin) ScanDisk(ctx context.Context, in agent.DiskScanInput) agent.DiskScanResult {
	return agent.DiskScanResult{PruneSessionIDs: s.scanPruneIDs}
}
func (s *stubBridgePlugin) IsValidNativeID(nativeID string) bool {
	return strings.HasPrefix(nativeID, "nat-")
}

func TestServer_RegisterPluginAndBridge(t *testing.T) {
	srv := &Server{}
	p := &stubBridgePlugin{
		subagents: []agent.SubagentInfo{{ID: "sub-1", Name: "Worker", Role: "coder", State: "running"}},
		docs: []agent.WorkspaceDoc{{
			FullPath: "/tmp/plan.md", Title: "plan.md", RelPath: "plan.md",
			Category: "plan", CategoryLabel: "Active Plan", Priority: 10,
		}},
		scanPruneIDs: []string{"plugin-x:local:orphan"},
	}

	if err := srv.RegisterPlugin(p); err != nil {
		t.Fatalf("RegisterPlugin: %v", err)
	}
	if err := srv.RegisterPlugin(p); err == nil {
		t.Fatal("expected duplicate plugin registration to fail")
	}

	if got, ok := srv.PluginRegistry().Get("px"); !ok || got.Key() != "plugin-x" {
		t.Fatalf("PluginRegistry alias lookup failed: (%v, %v)", got, ok)
	}

	prov, ok := srv.resolveProvider("px")
	if !ok {
		t.Fatal("expected resolveProvider alias hit")
	}

	var _ FullProvider = prov.(FullProvider)
	var _ SubagentDiscoverer = prov.(SubagentDiscoverer)
	var _ AgentAdapter = prov.(AgentAdapter)

	adapter := srv.agentAdapter("px")
	if _, ok := adapter.(*PluginBridge); !ok {
		t.Fatalf("expected PluginBridge adapter, got %T", adapter)
	}

	if prov.GetSpawnCommand("u1") != "pluginx -s u1" || prov.GetResumeCommand("n1") != "pluginx -r n1" {
		t.Error("lifecycle commands mismatch")
	}
	if msgs, err := prov.ExtractTranscript("/home", "/cwd", "n1"); err != nil || len(msgs) != 1 || msgs[0].Content != "hi" {
		t.Fatalf("ExtractTranscript mismatch: %+v err=%v", msgs, err)
	}
	if err := prov.CleanSessionFiles("/home", "/cwd", "n9"); err != nil || p.cleanedNativeID != "n9" {
		t.Fatalf("CleanSessionFiles mismatch: %q err=%v", p.cleanedNativeID, err)
	}

	subs, err := prov.(SubagentDiscoverer).ListSubagents("/home", "/cwd", "n1")
	if err != nil || len(subs) != 1 || subs[0].ID != "sub-1" || subs[0].Role != "coder" {
		t.Fatalf("ListSubagents conversion mismatch: %+v err=%v", subs, err)
	}

	var gotDocs []string
	adapter.ListWorkspaceDocuments("/home", "/cwd", "n1", func(fullPath, title, relPath, category, categoryLabel string, priority int) {
		gotDocs = append(gotDocs, fullPath+":"+category)
	})
	if len(gotDocs) != 1 || gotDocs[0] != "/tmp/plan.md:plan" {
		t.Fatalf("ListWorkspaceDocuments = %v", gotDocs)
	}

	if limited, _, _ := adapter.DetectRateLimit("rate limit reached · retry in 5m", time.Now()); !limited {
		t.Error("expected default rate limit fallback to match")
	}
	if ev, err := prov.ParseHook("PreToolUse", []byte("{}")); err != nil || ev == nil || ev.NativeID != "nat-1" {
		t.Fatalf("ParseHook = (%+v, %v)", ev, err)
	}
	if !adapter.IsValidNativeID("nat-42") || adapter.IsValidNativeID("bad") {
		t.Error("IsValidNativeID mismatch")
	}
	if env := adapter.AccountEnvVars("/home", &AgentAccount{Name: "work"}); env["ACKBAR_ACCOUNT"] != "work" {
		t.Fatalf("AccountEnvVars = %v", env)
	}

	// ListActiveSubagents takes precedence over ListSubagents when populated.
	p.activeSubagents = []*ActiveSubagent{{ID: "act-1", Name: "Active", Role: "runner", State: "running"}}
	if actSubs, err := prov.(SubagentDiscoverer).ListSubagents("/home", "/cwd", "n1"); err != nil || len(actSubs) != 1 || actSubs[0].ID != "act-1" {
		t.Fatalf("active subagents precedence mismatch: %+v err=%v", actSubs, err)
	}

	// Ensure /v1/providers exposes primary key once without duplicate alias entries.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/providers", nil)
	srv.handleProviders(rec, req)
	var dtos []ProviderDTO
	if err := json.NewDecoder(rec.Body).Decode(&dtos); err != nil {
		t.Fatalf("decode /v1/providers: %v", err)
	}
	if len(dtos) != 1 || dtos[0].Agent != "plugin-x" || dtos[0].BrandColor != "#112233" {
		t.Fatalf("/v1/providers = %+v", dtos)
	}

	// Verify ScanDiskSessions applies prune ids to ScanState safely without DB.
	orphan := &Session{ID: "plugin-x:local:orphan", NativeID: "orphan"}
	st := &ScanState{
		hostName:        "local",
		knownIDs:        map[string]*Session{orphan.ID: orphan},
		knownByNativeID: map[string]*Session{orphan.NativeID: orphan},
	}
	adapter.ScanDiskSessions(context.Background(), srv, "/home", st)
	if _, exists := st.knownIDs[orphan.ID]; exists || !orphan.Deleted || !strings.EqualFold(orphan.Activity, "Deleted") {
		t.Fatalf("expected ScanDiskSessions prune on state, got exists=%v sess=%+v", exists, orphan)
	}
}
