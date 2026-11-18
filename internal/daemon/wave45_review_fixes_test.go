package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ackbar/internal/agent"
)

type boundedIncPlugin struct {
	agent.BasePlugin
	agent.BaseTranscripts
	curPath      string
	overrideSize int64
	overrideMod  int64
	extractCalls atomic.Int32
	appendCalls  atomic.Int32
}

func (p *boundedIncPlugin) Key() string         { return "bounded-inc" }
func (p *boundedIncPlugin) DisplayName() string { return "bounded-inc" }
func (p *boundedIncPlugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{SupportsTranscript: true}
}
func (p *boundedIncPlugin) StatTranscript(string, string, string) (string, int64, int64, error) {
	st, err := os.Stat(p.curPath)
	if err != nil {
		return "", 0, 0, err
	}
	sz, mod := st.Size(), st.ModTime().UnixNano()
	if p.overrideSize > 0 {
		sz = p.overrideSize
	}
	if p.overrideMod > 0 {
		mod = p.overrideMod
	}
	return p.curPath, sz, mod, nil
}
func (p *boundedIncPlugin) ExtractTranscript(_ string, cwd, nativeID string) (*agent.Transcript, error) {
	p.extractCalls.Add(1)
	f, err := os.Open(p.curPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	msgs, err := p.parse(f, nil)
	return &agent.Transcript{Agent: p.Key(), NativeID: nativeID, Cwd: cwd, Messages: msgs}, err
}
func (p *boundedIncPlugin) AppendTranscript(r io.Reader, existing []agent.TranscriptMessage) ([]agent.TranscriptMessage, error) {
	p.appendCalls.Add(1)
	return p.parse(r, existing)
}
func (p *boundedIncPlugin) parse(r io.Reader, base []agent.TranscriptMessage) ([]agent.TranscriptMessage, error) {
	msgs := append([]agent.TranscriptMessage(nil), base...)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l != "" && !strings.HasPrefix(l, "#") {
			msgs = append(msgs, agent.TranscriptMessage{Role: "user", Content: l, Timestamp: time.Now().UTC()})
		}
	}
	return msgs, sc.Err()
}

func TestWave45ReviewFixes_IncrementalTranscriptBoundsAndPathSwitch(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	srv.homeDir = tmpDir

	hdr := "# " + strings.Repeat("A", 140) + "\n"
	pathA := filepath.Join(tmpDir, "a.log")
	pathB := filepath.Join(tmpDir, "b.log")
	_ = os.WriteFile(pathA, []byte(hdr+"a1\n"), 0o600)
	_ = os.WriteFile(pathB, []byte(hdr+"b1\nb2\n"), 0o600)

	plug := &boundedIncPlugin{curPath: pathA}
	_ = srv.PluginRegistry().Register(plug)

	if tA, err := srv.ExtractTranscript("bounded-inc", "n1", tmpDir); err != nil || len(tA.Messages) != 1 || tA.Messages[0].Content != "a1" {
		t.Fatalf("pathA extract = %+v, err=%v", tA, err)
	}
	plug.curPath = pathB
	tB, err := srv.ExtractTranscript("bounded-inc", "n1", tmpDir)
	if err != nil || len(tB.Messages) != 2 || tB.Messages[0].Content != "b1" || plug.appendCalls.Load() != 0 || plug.extractCalls.Load() != 2 {
		t.Fatalf("path switch should miss incremental cache: msgs=%+v extract=%d append=%d err=%v", tB.Messages, plug.extractCalls.Load(), plug.appendCalls.Load(), err)
	}

	stB, _ := os.Stat(pathB)
	plug.overrideSize = stB.Size() + 64
	plug.overrideMod = stB.ModTime().UnixNano() + 100
	if _, err := srv.ExtractTranscript("bounded-inc", "n1", tmpDir); err != nil || plug.appendCalls.Load() != 0 || plug.extractCalls.Load() != 3 {
		t.Fatalf("composite size > f.Stat().Size() must skip incremental append: extract=%d append=%d err=%v", plug.extractCalls.Load(), plug.appendCalls.Load(), err)
	}
	plug.overrideSize, plug.overrideMod = 0, 0
	_, _ = srv.ExtractTranscript("bounded-inc", "n1", tmpDir)
	baseExtracts := plug.extractCalls.Load()

	f, _ := os.OpenFile(pathB, os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString("b3\n")
	_ = f.Close()
	t3Time := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(pathB, t3Time, t3Time)
	stSnap, _ := os.Stat(pathB)
	plug.overrideSize = stSnap.Size()
	plug.overrideMod = stSnap.ModTime().UnixNano()

	f, _ = os.OpenFile(pathB, os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString("b4\n")
	_ = f.Close()

	tBounded, err := srv.ExtractTranscript("bounded-inc", "n1", tmpDir)
	if err != nil || len(tBounded.Messages) != 3 || tBounded.Messages[2].Content != "b3" || plug.appendCalls.Load() != 1 || plug.extractCalls.Load() != baseExtracts {
		t.Fatalf("post-stat growth must stop at curSize: msgs=%+v extract=%d append=%d err=%v", tBounded.Messages, plug.extractCalls.Load(), plug.appendCalls.Load(), err)
	}

	plug.overrideSize, plug.overrideMod = 0, 0
	t4Time := t3Time.Add(2 * time.Second)
	_ = os.Chtimes(pathB, t4Time, t4Time)
	tNext, err := srv.ExtractTranscript("bounded-inc", "n1", tmpDir)
	if err != nil || len(tNext.Messages) != 4 || tNext.Messages[2].Content != "b3" || tNext.Messages[3].Content != "b4" || plug.appendCalls.Load() != 2 {
		t.Fatalf("subsequent delta should read b4 without duplicates: msgs=%+v append=%d err=%v", tNext.Messages, plug.appendCalls.Load(), err)
	}

	f, _ = os.OpenFile(pathB, os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString("partial-line")
	_ = f.Close()
	t5Time := t4Time.Add(2 * time.Second)
	_ = os.Chtimes(pathB, t5Time, t5Time)
	if _, ok := srv.getCachedTranscript("bounded-inc", "n1", tmpDir); ok || plug.appendCalls.Load() != 2 {
		t.Fatalf("unterminated trailing line must not run incremental append")
	}
}

type autoDefaultPermPlugin struct {
	mockPersistentPlugin
}

func (p *autoDefaultPermPlugin) DefaultPermissionMode() string { return "auto" }

type errTurnCompleteParser struct{}

func (errTurnCompleteParser) ParseStreamLine(line []byte) ([]agent.ChatStreamEvent, bool, error) {
	var m map[string]string
	if json.Unmarshal(line, &m) != nil {
		return nil, false, nil
	}
	if m["kind"] == "fatal_done" {
		return []agent.ChatStreamEvent{{SessionID: "s1", TurnID: "t1", Type: "turn_complete", Text: m["msg"], IsError: true}}, true, nil
	}
	if m["kind"] == "err_then_empty_done" {
		return []agent.ChatStreamEvent{
			{SessionID: "s1", TurnID: "t1", Type: "error", Text: m["msg"], IsError: true},
			{SessionID: "s1", TurnID: "t1", Type: "turn_complete", IsError: true},
		}, true, nil
	}
	return nil, false, nil
}

func TestWave45ReviewFixes_PermDefaultsErrorSpoolModelAdoptAndAuthGuard(t *testing.T) {
	tmpDir := t.TempDir()
	installMockAgentBin(t, "auto-perm-agent", "exit 0")
	installMockAgentBin(t, "mock-persist-stdio", "exit 0")
	installMockAgentBin(t, "mock-persist-agy", "exit 0")
	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	srv.homeDir = tmpDir
	_ = srv.PluginRegistry().Register(&autoDefaultPermPlugin{mockPersistentPlugin: mockPersistentPlugin{key: "auto-perm-agent", stdioMode: true}})
	registerMockPersistentPlugins(srv.PluginRegistry())

	wSpawn := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wSpawn, httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewBufferString(`{"agent":"auto-perm-agent","engine_type":"headless","cwd":"`+tmpDir+`","permission_mode":"default"}`)))
	if wSpawn.Code != http.StatusOK {
		t.Fatalf("spawn status=%d body=%s", wSpawn.Code, wSpawn.Body.String())
	}
	var spResp map[string]any
	_ = json.Unmarshal(wSpawn.Body.Bytes(), &spResp)
	sessID, _ := spResp["id"].(string)
	if s1, _ := db.GetSession(sessID); s1 == nil || s1.PermissionMode != "auto" {
		t.Fatalf("spawn default permission_mode = %+v, want 'auto'", s1)
	}
	sPlan, _ := db.GetSession(sessID)
	sPlan.PermissionMode = "plan"
	_ = db.SaveSession(sPlan)
	wCfg := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wCfg, httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+sessID+"/config", bytes.NewBufferString(`{"permission_mode":"default"}`)))
	if wCfg.Code != http.StatusOK {
		t.Fatalf("config PATCH status=%d body=%s", wCfg.Code, wCfg.Body.String())
	}
	if s2, _ := db.GetSession(sessID); s2 == nil || s2.PermissionMode != "auto" {
		t.Fatalf("config default permission_mode = %+v, want 'auto'", s2)
	}
	sBlk, _ := db.GetSession(sessID)
	sBlk.PermissionMode, sBlk.State, sBlk.Blocked = "plan", StateBlocked, &Blocked{Kind: BlockApproval, Reason: "plan"}
	_ = db.SaveSession(sBlk)
	if wResp := postSessionRespond(t, srv, map[string]string{"id": sessID, "action": "allow"}); wResp.Code != http.StatusOK {
		t.Fatalf("respond status=%d body=%s", wResp.Code, wResp.Body.String())
	}
	if s3, _ := db.GetSession(sessID); s3 == nil || s3.PermissionMode != "auto" {
		t.Fatalf("respond plan approval permission_mode = %+v, want 'auto'", s3)
	}
	var emitted []ChatStreamEvent
	_, res, hadErr := parseHeadlessStream("s1", strings.NewReader(`{"kind":"fatal_done","msg":"API key invalid"}`+"\n"), streamParseHooks{
		emitTurnComplete: false, streamParser: errTurnCompleteParser{},
		emit: func(e ChatStreamEvent) { emitted = append(emitted, e) },
	})
	if !hadErr || res != "API key invalid" || len(emitted) != 1 || emitted[0].Type != "error" || emitted[0].Text != "API key invalid" {
		t.Fatalf("turn_complete IsError spool mismatch: hadErr=%v res=%q emitted=%+v", hadErr, res, emitted)
	}
	emitted = nil
	parseHeadlessStream("s1", strings.NewReader(`{"kind":"err_then_empty_done","msg":"rate fault"}`+"\n"), streamParseHooks{
		emitTurnComplete: false, streamParser: errTurnCompleteParser{},
		emit: func(e ChatStreamEvent) { emitted = append(emitted, e) },
	})
	if len(emitted) != 1 || emitted[0].Text != "rate fault" {
		t.Fatalf("expected single error event without duplicate on empty turn_complete: %+v", emitted)
	}
	preSess := &Session{ID: "mock-persist-agy:local:pre1", Agent: "mock-persist-agy", Host: "local", NativeID: "pre1", Cwd: tmpDir, EngineType: EngineHeadless, State: StateWorking}
	_ = db.SaveSession(preSess)
	srv.headless.processStreamWithPrompt(preSess.ID, strings.NewReader(`{"event":"custom_init","conv_id":"post9","model":"agy-stream-v4"}`+"\n"), "hi", preSess)
	if preSess.ID != "mock-persist-agy:local:post9" || srv.headless.GetRuntimeModel(preSess.ID) != "agy-stream-v4" {
		t.Fatalf("processStreamWithPrompt adoption/model = (%q, %q)", preSess.ID, srv.headless.GetRuntimeModel(preSess.ID))
	}

	stdioSess := &Session{ID: "mock-persist-stdio:local:x", Agent: "mock-persist-stdio", Cwd: tmpDir}
	if _, _, _, err := resolveAllowlistedAuthCommandWithRegistry(srv.PluginRegistry(), stdioSess, "agent:mock-agy", db, tmpDir); err == nil {
		t.Fatal("expected error for mismatched registered agent auth target")
	}
	claudeSess := &Session{ID: "claude-code:local:x", Agent: "claude-code", Cwd: tmpDir}
	if _, _, _, err := resolveAllowlistedAuthCommandWithRegistry(srv.PluginRegistry(), claudeSess, "agent:agy", db, tmpDir); err == nil {
		t.Fatal("expected error for mismatched legacy agent:agy target")
	}
}
