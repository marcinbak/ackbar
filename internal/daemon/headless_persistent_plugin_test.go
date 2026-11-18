package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
)

type mockPersistentPlugin struct {
	agent.BasePlugin
	agent.BasePersistentHeadless
	agent.BaseScanner
	agent.BaseModelCatalog
	key       string
	aliases   []string
	stdioMode bool
}

func (p *mockPersistentPlugin) Key() string                    { return p.key }
func (p *mockPersistentPlugin) DisplayName() string            { return p.key }
func (p *mockPersistentPlugin) Aliases() []string              { return p.aliases }
func (p *mockPersistentPlugin) SupportsStdioControl() bool     { return p.stdioMode }
func (p *mockPersistentPlugin) DefaultPermissionMode() string  { return "bypassPermissions" }
func (p *mockPersistentPlugin) ResolveBinary() (string, error) { return exec.LookPath(p.key) }
func (p *mockPersistentPlugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{SupportsHeadless: true, HeadlessMode: agent.HeadlessModePersistent, SupportsModelSelection: true, SupportsEffortLevel: p.stdioMode, SupportsSystemPrompt: true, SupportedPermissionModes: []string{"bypassPermissions", "plan", "auto"}}
}
func (p *mockPersistentPlugin) BuildTurnSpec(in agent.HeadlessTurnInput) (agent.HeadlessTurnSpec, error) {
	bin, err := p.ResolveBinary()
	args := []string{"--stream"}
	if in.Session.PermissionMode != "" {
		args = append(args, "--mode", in.Session.PermissionMode)
	}
	if in.IsExisting && in.Session.NativeID != "" {
		args = append(args, "--resume", in.Session.NativeID)
	}
	if in.Session.Model != "" && !strings.EqualFold(in.Session.Model, "default") {
		args = append(args, "--model", in.Session.Model)
	}
	return agent.HeadlessTurnSpec{Binary: bin, Args: args, Mode: agent.HeadlessModePersistent}, err
}
func (p *mockPersistentPlugin) ParseStreamEvent([]byte) ([]agent.ChatStreamEvent, error) {
	return nil, nil
}
func (p *mockPersistentPlugin) ComputeUsage(string, string, string) (agent.TranscriptUsage, bool) {
	return agent.TranscriptUsage{}, false
}
func (p *mockPersistentPlugin) FormatStdinTurn(in agent.PersistentTurnInput) (string, error) {
	if p.stdioMode {
		b, err := json.Marshal(map[string]string{"type": "user", "prompt": in.Prompt})
		return string(b), err
	}
	content := in.Prompt
	if in.IsSupervisor && in.IsFirstTurn && in.SystemPrompt != "" {
		content = "[SYS:" + in.SystemPrompt + "] " + content
	}
	b, err := json.Marshal(map[string]any{"event": "user", "message": map[string]string{"role": "user", "content": content}})
	return string(b), err
}
func (p *mockPersistentPlugin) FormatSetModel(model string) (string, bool) {
	if !p.stdioMode {
		return "", false
	}
	b, _ := json.Marshal(map[string]string{"type": "set_model_custom", "model": model})
	return string(b), true
}
func (p *mockPersistentPlugin) InspectSubcommandArgs(kind string) ([]string, bool) {
	if !p.stdioMode && kind == "auth_login" {
		return []string{"acp", "--custom-login"}, true
	}
	if p.stdioMode && kind == "mcp_login" {
		return []string{"mcp", "custom-login", "--no-browser"}, true
	}
	return nil, false
}
func (p *mockPersistentPlugin) NewStreamParser(sessionID string) agent.StreamParser {
	return &mockPersistStreamParser{sessionID: sessionID, stdioMode: p.stdioMode}
}

type mockPersistStreamParser struct {
	sessionID string
	stdioMode bool
}

func (s *mockPersistStreamParser) ParseStreamLine(line []byte) ([]agent.ChatStreamEvent, bool, error) {
	var raw map[string]any
	if json.Unmarshal(line, &raw) != nil {
		return nil, false, nil
	}
	str := func(k string) string { v, _ := raw[k].(string); return v }
	if s.stdioMode {
		switch str("type") {
		case "mock_delta":
			return []agent.ChatStreamEvent{{SessionID: s.sessionID, Type: "text_delta", Text: str("text")}}, true, nil
		case "mock_done":
			return []agent.ChatStreamEvent{{SessionID: s.sessionID, Type: "turn_complete", Text: str("result")}}, true, nil
		}
		return nil, false, nil
	}
	switch str("event") {
	case "custom_init":
		return []agent.ChatStreamEvent{{SessionID: s.sessionID, Type: "session_init", NativeID: str("conv_id"), Model: str("model")}}, true, nil
	case "custom_step":
		return []agent.ChatStreamEvent{{SessionID: s.sessionID, Type: "text_delta", Text: str("text")}}, true, nil
	case "custom_done":
		return []agent.ChatStreamEvent{{SessionID: s.sessionID, Type: "turn_complete", Text: str("text")}}, true, nil
	}
	return nil, false, nil
}

func registerMockPersistentPlugins(reg *agent.Registry) {
	_ = reg.Register(&mockPersistentPlugin{key: "mock-persist-stdio", stdioMode: true})
	_ = reg.Register(&mockPersistentPlugin{key: "mock-persist-agy", aliases: []string{"mock-agy"}, stdioMode: false})
}

func installMockAgentBin(t *testing.T, binName, script string) string {
	t.Helper()
	binDir := t.TempDir()
	p := filepath.Join(binDir, binName)
	if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", binName, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return p
}

func warmHandlePID(srv *Server, sess *Session) int {
	srv.headless.mu.RLock()
	defer srv.headless.mu.RUnlock()
	if _, wh := srv.headless.findWarmHandleLocked(sess); wh != nil {
		return wh.ChildPID
	}
	return 0
}

func setupPersistTestServer(t *testing.T, tmpDir string) (*Server, *DB) {
	t.Helper()
	t.Setenv("ACKBAR_TEST_MOCK_PERSISTENT_PLUGIN", "1")
	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	srv := NewServer(db)
	registerMockPersistentPlugins(srv.PluginRegistry())
	t.Cleanup(func() { srv.headless.ShutdownAllWorkers(time.Second); _ = db.Close() })
	return srv, db
}

func TestPersistentPlugin_StdioMultiTurnControlSetModelAndCancel(t *testing.T) {
	tmpDir := t.TempDir()
	stdinLog := filepath.Join(tmpDir, "stdio.log")
	binPath := installMockAgentBin(t, "mock-persist-stdio", `read -r t1; echo "$t1" >> "`+stdinLog+`"; echo '{"type":"mock_delta","text":"turn1-ok"}'; echo '{"type":"mock_done","result":"turn1-ok"}'
read -r mline; echo "$mline" >> "`+stdinLog+`"; read -r t2; echo "$t2" >> "`+stdinLog+`"
echo '{"type":"control_request","request_id":"req_p1","request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode","tool_use_id":"tu_p1","input":{"plan":"Ship it"}}}'
read -r ctrl; echo "$ctrl" >> "`+stdinLog+`"; echo '{"type":"mock_delta","text":"turn2-clean"}'; echo '{"type":"mock_done","result":""}'; read -r t3; echo "$t3" >> "`+stdinLog+`"; sleep 30`)
	srv, db := setupPersistTestServer(t, tmpDir)

	_ = os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(`{"mcpServers":{"corp":{"type":"http","url":"https://mcp.example.com"}}}`), 0o600)
	sess := &Session{ID: "mock-persist-stdio:local:s1", Agent: "mock-persist-stdio", Host: "local", NativeID: "s1", Cwd: tmpDir, EngineType: EngineHeadless, Model: "v1", PermissionMode: "plan", Managed: true, State: StateIdle}
	_ = db.SaveSession(sess)

	if argv, _, _, err := resolveAllowlistedAuthCommandWithRegistry(srv.PluginRegistry(), sess, "mcp:corp", db, tmpDir); err != nil || len(argv) != 5 || argv[0] != binPath || argv[4] != "corp" {
		t.Fatalf("mcp_login mismatch: argv=%v err=%v", argv, err)
	}
	if _, _, _, err := resolveAllowlistedAuthCommandWithRegistry(srv.PluginRegistry(), sess, "mcp:--bad", db, tmpDir); err == nil {
		t.Fatal("expected leading-hyphen mcp rejection")
	}

	if err := srv.headless.RunTurn(context.Background(), sess, "first"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "turn 1 idle", func() bool { return !srv.headless.IsRunning(sess.ID) })
	pidBefore := warmHandlePID(srv, sess)
	sess.Model = "v2"
	_ = db.SaveSession(sess)
	srv.headless.NotifyConfigChanged(sess)
	if pidAfter := warmHandlePID(srv, sess); pidBefore <= 1 || pidAfter != pidBefore {
		t.Fatalf("expected live set_model without child restart: %d -> %d", pidBefore, pidAfter)
	}

	if err := srv.headless.RunTurn(context.Background(), sess, "second"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "blocked ExitPlanMode", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateBlocked && cur.Blocked != nil && cur.Blocked.Kind == BlockApproval
	})
	if handled, err := srv.headless.TryAnswerControlAction(context.Background(), sess, "allow", "", ""); !handled || err != nil {
		t.Fatalf("TryAnswerControlAction = (%v, %v)", handled, err)
	}
	waitForWorkerCond(t, 5*time.Second, "turn 2 idle", func() bool { return !srv.headless.IsRunning(sess.ID) })
	if snap := srv.headless.GetInFlightTurnSnapshot(sess, sess.ID, sess.NativeID, sess.Agent); snap == nil || snap.AssistantText != "turn2-clean" {
		t.Fatalf("expected isolated turn 2 output, got %+v", snap)
	}

	_ = srv.headless.RunTurn(context.Background(), sess, "third")
	waitForWorkerCond(t, 5*time.Second, "turn 3 active", func() bool { return srv.headless.IsRunning(sess.ID) })
	_ = srv.headless.CancelTurn(sess.ID)
	waitForWorkerCond(t, 5*time.Second, "turn 3 cancelled", func() bool { return !srv.headless.IsRunning(sess.ID) })

	data, _ := os.ReadFile(stdinLog)
	if !strings.Contains(string(data), `"type":"set_model_custom"`) || !strings.Contains(string(data), `"control_response"`) {
		t.Fatalf("unexpected stdin log:\n%s", string(data))
	}
}

func TestPersistentPlugin_AgyEventStreamSupervisorInitAndModelSwap(t *testing.T) {
	tmpDir := t.TempDir()
	stdinLog := filepath.Join(tmpDir, "agy.log")
	binPath := installMockAgentBin(t, "mock-persist-agy", `echo "ARGS:$*" >> "`+stdinLog+`"
while IFS= read -r line; do echo "IN:$line" >> "`+stdinLog+`"; echo '{"event":"custom_init","conv_id":"conv-adopted-7","model":"agy-pro-1"}'; echo '{"event":"custom_step","text":"agy-reply"}'; echo '{"event":"custom_done","text":"agy-reply"}'; done`)
	srv, db := setupPersistTestServer(t, tmpDir)

	sess := &Session{ID: "mock-persist-agy:local:init1", Agent: "mock-persist-agy", Host: "local", NativeID: "init1", Cwd: tmpDir, EngineType: EngineHeadless, IsSupervisor: true, Model: "m1", Managed: true, State: StateIdle}
	_ = db.SaveSession(sess)

	if argv, _, _, err := resolveAllowlistedAuthCommandWithRegistry(srv.PluginRegistry(), sess, "agent:mock-agy", db, tmpDir); err != nil || len(argv) != 3 || argv[0] != binPath {
		t.Fatalf("auth_login alias mismatch: argv=%v err=%v", argv, err)
	}
	if err := srv.headless.RunTurn(context.Background(), sess, "hello-sup"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "agy turn 1 idle", func() bool { return !srv.headless.IsRunning(sess.ID) })
	if got := srv.headless.GetRuntimeModel(sess.ID); got != "agy-pro-1" {
		t.Fatalf("GetRuntimeModel = %q, want agy-pro-1", got)
	}
	_ = srv.headless.RunTurn(context.Background(), sess, "followup")
	waitForWorkerCond(t, 5*time.Second, "agy turn 2 idle", func() bool { return !srv.headless.IsRunning(sess.ID) })

	pid1 := warmHandlePID(srv, sess)
	sess.Model = "m2"
	_ = db.SaveSession(sess)
	srv.headless.NotifyConfigChanged(sess)
	if pid2 := warmHandlePID(srv, sess); pid1 <= 1 || pid2 <= 1 || pid1 == pid2 {
		t.Fatalf("expected child restart on model change: %d -> %d", pid1, pid2)
	}
	logBytes, _ := os.ReadFile(stdinLog)
	if !strings.Contains(string(logBytes), "[SYS:") || strings.Count(string(logBytes), "[SYS:") != 1 || !strings.Contains(string(logBytes), "--model m2") {
		t.Fatalf("unexpected agy log:\n%s", string(logBytes))
	}
}
