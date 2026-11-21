package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ackbar/internal/agents"
	"ackbar/internal/agents/antigravity"
)

func TestAntigravityPlugin_E2E(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	srv, db := setupTestServer(t)
	if _, hasPlugin := srv.PluginRegistry().Get("antigravity"); !hasPlugin {
		t.Fatal("PluginRegistry.Get(antigravity) = false, want true")
	}

	proj := filepath.Join(home, "proj")
	_ = os.MkdirAll(proj, 0o755)
	convID := "11111111-2222-3333-4444-555555555555"
	logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Ship it","created_at":"`+time.Now().UTC().Format(time.RFC3339)+`","input_tokens":500,"cache_read_tokens":100}`+"\n"+
			proj+" -> corpus\n"+
			`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","content":"Shipped","input_tokens":2000,"output_tokens":80}`+"\n",
	), 0o644)

	// 1. Spawn headless rejects dontAsk with canonical error and accepts plan.
	badBody, _ := json.Marshal(map[string]any{"agent": "antigravity", "cwd": proj, "engine_type": EngineHeadless, "permission_mode": "dontAsk"})
	wBad := httptest.NewRecorder()
	srv.handleSpawn(wBad, httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(badBody)))
	if wBad.Code != http.StatusBadRequest || !strings.Contains(wBad.Body.String(), "permission_mode 'dontAsk' is not supported for antigravity") {
		t.Fatalf("dontAsk reject = %d %q", wBad.Code, wBad.Body.String())
	}

	okBody, _ := json.Marshal(map[string]any{"agent": "antigravity", "cwd": proj, "engine_type": EngineHeadless, "permission_mode": "plan"})
	wSpawn := httptest.NewRecorder()
	srv.handleSpawn(wSpawn, httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(okBody)))
	if wSpawn.Code != http.StatusOK {
		t.Fatalf("spawn failed: %d %s", wSpawn.Code, wSpawn.Body.String())
	}
	var spawnResp map[string]any
	_ = json.Unmarshal(wSpawn.Body.Bytes(), &spawnResp)
	sessID := spawnResp["session_id"].(string)
	sess, _ := db.GetSession(sessID)
	inspectID := "antigravity:" + srv.HostName() + ":" + convID
	sess.ID = inspectID
	sess.NativeID = convID
	_ = db.SaveSession(sess)

	// 2. Inspect context, cost, doctor, plugins, agents, rewind.
	for _, kind := range []string{"context", "cost", "doctor", "plugins", "agents"} {
		rec := httptest.NewRecorder()
		srv.handleSessionInspect(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+inspectID+"/inspect?kind="+kind, nil), inspectID)
		if rec.Code != http.StatusOK {
			t.Fatalf("inspect %s = %d %s", kind, rec.Code, rec.Body.String())
		}
		if kind == "context" && !strings.Contains(rec.Body.String(), `"context_limit_tokens":1000000`) {
			t.Fatalf("expected 1M context limit, got %s", rec.Body.String())
		}
		if kind == "doctor" && !strings.Contains(rec.Body.String(), "Antigravity Diagnostic Check") {
			t.Fatalf("expected Antigravity Diagnostic Check in doctor, got %s", rec.Body.String())
		}
	}
	ocID := "opencode:" + srv.HostName() + ":oc-doc-1"
	_ = db.SaveSession(&Session{ID: ocID, Agent: "opencode", Host: srv.HostName(), Cwd: proj, Managed: true, State: StateIdle})
	recOC := httptest.NewRecorder()
	srv.handleSessionInspect(recOC, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+ocID+"/inspect?kind=doctor", nil), ocID)
	if recOC.Code != http.StatusOK || strings.Contains(recOC.Body.String(), "Antigravity Diagnostic Check") {
		t.Fatalf("unexpected non-Antigravity doctor output (%d): %s", recOC.Code, recOC.Body.String())
	}

	wRew := httptest.NewRecorder()
	srv.handleSessionRewind(wRew, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+inspectID+"/rewind", nil), inspectID)
	if wRew.Code != http.StatusOK || !strings.Contains(wRew.Body.String(), `"total_turns":1`) {
		t.Fatalf("rewind = %d %s", wRew.Code, wRew.Body.String())
	}

	// 3. Suggestions: models + commands (effort omitted).
	cmds := srv.collectCommands(proj, "antigravity")
	for _, c := range cmds {
		if c.Name == "effort" {
			t.Fatal("antigravity commands must omit effort")
		}
	}
	if len(srv.collectModelsForAgent("antigravity")) == 0 {
		t.Fatal("expected antigravity model suggestions")
	}

	// 4. Provisioning status & action.
	statuses, err := ProvisionAgentsWithRegistry(home, []string{"antigravity"}, srv.PluginRegistry())
	if err != nil {
		t.Fatalf("ProvisionAgentsWithRegistry failed: %v", err)
	}
	foundAgy := false
	for _, st := range statuses {
		if st.Key == "antigravity" && st.MCPInstalled && st.SkillInstalled && st.RulesInstalled {
			foundAgy = true
		}
	}
	if !foundAgy {
		t.Fatalf("expected provisioned antigravity status, got %+v", statuses)
	}

	// 5. ScanDiskSessions adopts spawning tmux session & skips deleted IDs.
	convID2 := "33333333-3333-3333-3333-333333333333"
	logDir2 := filepath.Join(home, ".gemini", "antigravity", "brain", convID2, ".system_generated", "logs")
	_ = os.MkdirAll(logDir2, 0o755)
	_ = os.WriteFile(filepath.Join(logDir2, "transcript.jsonl"), []byte(
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Adopt me","created_at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`+"\n"+proj+" -> corpus\n",
	), 0o644)
	tempID := "99999999-9999-9999-9999-999999999999"
	spawningID := "antigravity:" + srv.HostName() + ":" + tempID
	spawning := &Session{
		ID: spawningID, Agent: "antigravity", Host: srv.HostName(),
		NativeID: tempID, Cwd: proj, Managed: true, State: StateUnknown, StartedAt: time.Now().Add(-time.Minute),
	}
	_ = db.SaveSession(spawning)
	st := &ScanState{
		hostName:         srv.HostName(),
		existingSessions: []*Session{sess, spawning},
		knownIDs:         map[string]*Session{sess.ID: sess, spawningID: spawning},
		knownByNativeID:  map[string]*Session{convID: sess, tempID: spawning},
		knownByTmux:      map[string]*Session{},
		knownByPID:       map[int]*Session{},
	}
	srv.agentAdapter("antigravity").ScanDiskSessions(context.Background(), srv, home, st)
	adopted, _ := db.GetSession("antigravity:" + srv.HostName() + ":" + convID2)
	if adopted == nil || adopted.OldID != spawningID || adopted.NativeID != convID2 {
		t.Fatalf("expected spawning session adopted to %s, got %+v", convID2, adopted)
	}

	// 6. Plugin-mode hook ingestion & annotation rename check.
	annoDir := filepath.Join(home, ".gemini", "antigravity", "annotations")
	_ = os.MkdirAll(annoDir, 0o755)
	_ = os.WriteFile(filepath.Join(annoDir, convID2+".pbtxt"), []byte(`title: "Hook Upgraded Title"`), 0o644)
	hookBody := `{"conversationId":"` + convID2 + `","workspacePaths":["` + proj + `"]}`
	wHook := httptest.NewRecorder()
	srv.handleHook(wHook, httptest.NewRequest(http.MethodPost, "/v1/hooks/agy?event=Notification", strings.NewReader(hookBody)))
	if wHook.Code != http.StatusOK {
		t.Fatalf("hook POST = %d %s", wHook.Code, wHook.Body.String())
	}
	time.Sleep(80 * time.Millisecond)
	updated, _ := db.GetSession("antigravity:" + srv.HostName() + ":" + convID2)
	if updated == nil || updated.Name != "Hook Upgraded Title" {
		t.Fatalf("expected hook annotation title upgrade, got %+v", updated)
	}
}

func TestAntigravityPlugin_StartupReattachWithRegisteredPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	proj := filepath.Join(home, "proj")
	_ = os.MkdirAll(proj, 0o755)
	convID := "44444444-4444-4444-4444-444444444444"
	logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"<USER_REQUEST>\nRecover startup title\n</USER_REQUEST>","created_at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`+"\n",
	), 0o644)

	db, err := InitDB(filepath.Join(t.TempDir(), "startup_reattach.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	sess := &Session{
		ID:             "antigravity:local:" + convID,
		Agent:          "antigravity",
		Host:           "local",
		NativeID:       convID,
		Cwd:            proj,
		EngineType:     EngineHeadless,
		State:          StateWorking,
		PermissionMode: "",
		Name:           "antigravity",
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	runDir := DefaultHeadlessRunDir(db.Path())
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("MkdirAll runDir failed: %v", err)
	}

	spoolPath := filepath.Join(runDir, "turn-n1.spool.jsonl")
	if err := os.WriteFile(spoolPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile spool failed: %v", err)
	}
	metaPath := filepath.Join(runDir, "turn-n1.meta.json")
	if err := WriteTurnSpoolMeta(metaPath, &TurnSpoolMeta{
		TurnID:            "turn-n1",
		SessionID:         sess.ID,
		OrigSessionID:     sess.ID,
		NativeID:          convID,
		Agent:             "antigravity",
		Cwd:               proj,
		Prompt:            "unrelated prompt fallback",
		SpoolPath:         spoolPath,
		MetaPath:          metaPath,
		Status:            "completed",
		FinalizedByDaemon: false,
		StartedAt:         time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("WriteTurnSpoolMeta failed: %v", err)
	}

	// Start a mock warm worker IPC socket expecting canonical PermissionMode "bypassPermissions".
	sockDir, err := os.MkdirTemp("", "agy-reattach-*")
	if err != nil {
		t.Fatalf("MkdirTemp sockDir failed: %v", err)
	}
	defer os.RemoveAll(sockDir)
	sockPath := filepath.Join(sockDir, "w.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen unix failed: %v", err)
	}
	defer ln.Close()
	stopSock := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stopSock) }) }
	defer closeStop()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var req workerIPCReq
					if json.Unmarshal(sc.Bytes(), &req) != nil {
						continue
					}
					switch req.Op {
					case "status":
						_ = json.NewEncoder(c).Encode(workerIPCResp{OK: true, State: "idle_warm"})
					case "shutdown":
						_ = json.NewEncoder(c).Encode(workerIPCResp{OK: true, State: "exited"})
						closeStop()
						return
					case "watch_worker":
						_ = json.NewEncoder(c).Encode(workerIPCResp{OK: true, State: "idle_warm"})
						<-stopSock
						return
					}
				}
			}(conn)
		}
	}()

	preReg, err := agents.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}
	bypassSess := &Session{ID: sess.ID, Agent: "antigravity", Cwd: proj, PermissionMode: "bypassPermissions"}
	workerMetaPath := filepath.Join(runDir, "w_n1.worker.json")
	if err := WriteSessionWorkerMeta(workerMetaPath, &SessionWorkerMeta{
		WorkerID:          "w_n1",
		SessionID:         sess.ID,
		OrigSessionID:     sess.ID,
		NativeID:          convID,
		Agent:             "antigravity",
		Cwd:               proj,
		ConfigFingerprint: ConfigFingerprintWithRegistry(preReg, bypassSess),
		Bin:               "agy",
		SocketPath:        sockPath,
		WorkerMetaPath:    workerMetaPath,
		State:             "idle_warm",
		StartedAt:         time.Now().Add(-time.Minute),
		UpdatedAt:         time.Now(),
	}); err != nil {
		t.Fatalf("WriteSessionWorkerMeta failed: %v", err)
	}

	srv, err := NewServerWithPlugins(db, agents.DefaultPlugins()...)
	if err != nil {
		t.Fatalf("NewServerWithPlugins failed: %v", err)
	}
	defer srv.headless.ShutdownAllWorkers(2 * time.Second)

	reloadedMeta, err := ReadTurnSpoolMeta(metaPath)
	if err != nil || reloadedMeta == nil || !reloadedMeta.FinalizedByDaemon {
		t.Fatalf("expected spool FinalizedByDaemon=true after startup reattach, got meta=%+v err=%v", reloadedMeta, err)
	}
	updated, err := db.GetSession(sess.ID)
	if err != nil || updated == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if updated.Name != "Recover startup title" || updated.State != StateIdle {
		t.Fatalf("expected Antigravity transcript title 'Recover startup title' and StateIdle, got name=%q state=%v", updated.Name, updated.State)
	}
	if !srv.headless.HasWarmWorker(sess.ID) {
		t.Fatalf("expected warm Antigravity worker adopted during NewServerWithPlugins")
	}

	fpBypass := ConfigFingerprintWithRegistry(srv.PluginRegistry(), bypassSess)
	if fpEmpty := ConfigFingerprintWithRegistry(srv.PluginRegistry(), &Session{ID: sess.ID, Agent: "antigravity", Cwd: proj, PermissionMode: ""}); fpEmpty != fpBypass {
		t.Fatalf("empty PermissionMode fingerprint = %s, want %s", fpEmpty, fpBypass)
	}
	if fpAlias := ConfigFingerprintWithRegistry(srv.PluginRegistry(), &Session{ID: sess.ID, Agent: "agy", Cwd: proj, PermissionMode: ""}); fpAlias != fpBypass {
		t.Fatalf("agy alias fingerprint = %s, want %s", fpAlias, fpBypass)
	}

	if dupSrv, dupErr := NewServerWithPlugins(db, antigravity.New(), antigravity.New()); dupErr == nil || dupSrv != nil {
		t.Fatalf("expected error on duplicate plugin registration, got srv=%v err=%v", dupSrv, dupErr)
	}
}
