package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agents/grok"
	"ackbar/internal/tmux"
)

func TestGrokSpawnCoercionAndFlags(t *testing.T) {
	srv, db := setupTestServer(t)
	cwd := filepath.Join(t.TempDir(), "grok-project")
	_ = os.MkdirAll(cwd, 0755)

	extraDir := filepath.Join(t.TempDir(), "grok-extra")
	_ = os.MkdirAll(extraDir, 0755)

	// 1. Grok preserves EngineHeadless
	payload := map[string]interface{}{
		"agent":           "grok",
		"cwd":             cwd,
		"engine_type":     EngineHeadless,
		"model":           "grok-2",
		"effort":          "medium",
		"permission_mode": "plan",
		"extra_dirs":      []string{extraDir},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(body))
	w := httptest.NewRecorder()

	srv.handleSpawn(w, req)
	defer func() {
		if sessions, err := db.ListSessions(); err == nil {
			for _, s := range sessions {
				if s.TmuxName != "" {
					_ = tmux.Kill(context.Background(), s.TmuxName)
				}
			}
		}
	}()

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["engine_type"] != EngineHeadless {
		t.Errorf("Expected engine_type preserved as 'headless', got %v", resp["engine_type"])
	}

	sessID, ok := resp["id"].(string)
	if !ok || sessID == "" {
		t.Fatalf("Expected session id in response")
	}

	sess, err := db.GetSession(sessID)
	if err != nil || sess == nil {
		t.Fatalf("Failed to fetch session from DB: %v", err)
	}

	if sess.EngineType != EngineHeadless {
		t.Errorf("Expected sess.EngineType == EngineHeadless, got %s", sess.EngineType)
	}
	if sess.Model != "grok-2" {
		t.Errorf("Expected sess.Model == 'grok-2', got %q", sess.Model)
	}
	if sess.Effort != "medium" {
		t.Errorf("Expected sess.Effort == 'medium', got %q", sess.Effort)
	}
	if sess.PermissionMode != "plan" {
		t.Errorf("Expected sess.PermissionMode == 'plan', got %q", sess.PermissionMode)
	}
	canonicalExtra, _ := filepath.EvalSymlinks(extraDir)
	if len(sess.ExtraDirs) != 1 || (sess.ExtraDirs[0] != extraDir && sess.ExtraDirs[0] != canonicalExtra) {
		t.Errorf("Expected sess.ExtraDirs == [%s], got %+v", extraDir, sess.ExtraDirs)
	}

	// 2. Unsupported agent (codex) still coerces to EngineTmux
	codexPayload := map[string]interface{}{
		"agent":       "codex",
		"cwd":         cwd,
		"engine_type": EngineHeadless,
	}
	codexBody, _ := json.Marshal(codexPayload)
	codexReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(codexBody))
	codexW := httptest.NewRecorder()

	srv.handleSpawn(codexW, codexReq)
	if codexW.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for codex spawn, got %d: %s", codexW.Code, codexW.Body.String())
	}
	var codexResp map[string]interface{}
	if err := json.Unmarshal(codexW.Body.Bytes(), &codexResp); err != nil {
		t.Fatalf("Failed to decode codex response: %v", err)
	}
	_ = codexBody
	if codexResp["engine_type"] != EngineTmux {
		t.Errorf("Expected codex engine_type coerced to 'tmux', got %v", codexResp["engine_type"])
	}
}

func TestGrokModelResolution_SummaryAndConfig(t *testing.T) {
	srv, _ := setupTestServer(t)
	tmpHome := t.TempDir()
	srv.homeDir = tmpHome

	cwd := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(cwd, 0755)

	nativeID := "grok-sess-model"
	encodedCwd := grok.EncodeCwd(cwd)
	sessDir := filepath.Join(tmpHome, ".grok", "sessions", encodedCwd, nativeID)
	_ = os.MkdirAll(sessDir, 0755)

	sess := &Session{
		ID:         "grok:local:" + nativeID,
		Agent:      "grok",
		Host:       "local",
		NativeID:   nativeID,
		Cwd:        cwd,
		EngineType: EngineTmux,
	}

	// 1. Resolve from summary.json
	_ = os.WriteFile(filepath.Join(sessDir, "summary.json"), []byte(`{"current_model_id":"grok-2-1212"}`), 0644)
	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "grok-2-1212" {
		t.Errorf("Expected model 'grok-2-1212' from summary.json, got %q", sess.ResolvedModel)
	}

	// 2. When summary.json is absent, fallback to ~/.grok/config.toml
	_ = os.Remove(filepath.Join(sessDir, "summary.json"))
	grokHome := filepath.Join(tmpHome, ".grok")
	_ = os.WriteFile(filepath.Join(grokHome, "config.toml"), []byte("[models]\ndefault = \"grok-3\"\n"), 0644)

	sess.ResolvedModel = ""
	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "grok-3" {
		t.Errorf("Expected model 'grok-3' from config.toml, got %q", sess.ResolvedModel)
	}
}

func TestGrokSpawnValidation(t *testing.T) {
	srv, db := setupTestServer(t)
	cwd := t.TempDir()

	payload := map[string]interface{}{
		"agent": "grok",
		"cwd":   cwd,
		"model": "--yolo",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleSpawn(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for model with leading hyphen, got %d", w.Code)
	}

	payload["model"] = "grok-2"
	payload["effort"] = "--invalid"
	body, _ = json.Marshal(payload)
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handleSpawn(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for invalid effort, got %d", w.Code)
	}

	delete(payload, "effort")
	payload["permission_mode"] = "invalid_mode"
	body, _ = json.Marshal(payload)
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handleSpawn(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for invalid permission_mode, got %d", w.Code)
	}

	resumed := &Session{
		ID:          "grok:local:resumed-1",
		Agent:       "grok",
		Host:        "local",
		NativeID:    "resumed-1",
		Cwd:         cwd,
		FirstPrompt: "hello",
	}
	_ = db.SaveSession(resumed)
	extraDir := t.TempDir()
	cfgBody, _ := json.Marshal(map[string]any{"extra_dirs": []string{extraDir}})
	cfgReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+resumed.ID+"/config", bytes.NewReader(cfgBody))
	cfgW := httptest.NewRecorder()
	srv.handleSessionConfig(cfgW, cfgReq, resumed.ID)
	if cfgW.Code != http.StatusBadRequest || !strings.Contains(cfgW.Body.String(), "Grok resumed sessions cannot modify extra_dirs") {
		t.Errorf("expected HTTP 400 locking extra_dirs on resume, got %d: %s", cfgW.Code, cfgW.Body.String())
	}
}

func TestHeadlessRunner_GrokACPWorkerE2E(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)

	argsLog := filepath.Join(tmpDir, "grok_args.log")
	callsLog := filepath.Join(tmpDir, "grok_calls.log")
	mockGrokPath := filepath.Join(binDir, "grok")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = "agent" ] && [ "$2" = "stdio" ]; then
  while IFS= read -r line; do
    printf '%%s\n' "$line" >> %q
    id=$(printf '%%s\n' "$line" | sed -n 's/,"params":.*//; s/.*"id": *\([^,}]*\).*/\1/p')
    [ -z "$id" ] && continue
    case "$line" in
      *'"method":"initialize"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cached_token","name":"Cached"}]}}\n' "$id"
        ;;
      *'"method":"authenticate"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{}}\n' "$id"
        ;;
      *'"method":"session/new"'*|*'"method":"session/load"'*)
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"sessionId":"grok-e2e-1"}}\n' "$id"
        ;;
      *'"method":"session/prompt"'*)
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"grok-e2e-1","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"analyzing"}}}}\n'
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"grok-e2e-1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"pong from mock grok"}}}}\n'
        printf '{"jsonrpc":"2.0","id":%%s,"result":{"stopReason":"end_turn"}}\n' "$id"
        ;;
      *)
        printf '{"jsonrpc":"2.0","id":%%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id"
        ;;
    esac
  done
fi
`, argsLog, callsLog)
	if err := os.WriteFile(mockGrokPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock grok: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	srv, db := setupTestServer(t)
	cwd := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(cwd, 0755)

	sess := &Session{
		ID:         "grok:local:grok-e2e-1",
		Host:       "local",
		Agent:      "grok",
		EngineType: EngineHeadless,
		NativeID:   "grok-e2e-1",
		Cwd:        cwd,
		State:      StateIdle,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	hr := srv.headless
	runTurnAndWait := func(prompt string) []ChatStreamEvent {
		sub, unsub := hr.Subscribe(sess.ID)
		defer unsub()
		if err := hr.RunTurn(context.Background(), sess, prompt); err != nil {
			t.Fatalf("RunTurn(%q) failed: %v", prompt, err)
		}
		var evts []ChatStreamEvent
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-sub:
				evts = append(evts, ev)
				if ev.Type == "turn_complete" || ev.Type == "error" || ev.Type == "turn_cancelled" {
					waitEnd := time.Now().Add(2 * time.Second)
					for time.Now().Before(waitEnd) && hr.IsRunning(sess.ID) {
						time.Sleep(10 * time.Millisecond)
					}
					return evts
				}
			case <-deadline:
				t.Fatalf("timed out waiting for turn %q", prompt)
			}
		}
	}

	evts1 := runTurnAndWait("ping turn 1")
	if !hr.HasWarmWorker(sess.ID) {
		t.Fatalf("expected HasWarmWorker true after Turn 1")
	}
	hr.mu.RLock()
	h1 := hr.warmWorkers[sess.ID]
	wpid1, cpid1 := h1.WorkerPID, h1.ChildPID
	hr.mu.RUnlock()

	evts2 := runTurnAndWait("ping turn 2")
	hr.mu.RLock()
	h2 := hr.warmWorkers[sess.ID]
	wpid2, cpid2 := h2.WorkerPID, h2.ChildPID
	hr.mu.RUnlock()
	if wpid2 != wpid1 || cpid2 != cpid1 || cpid1 <= 1 {
		t.Fatalf("expected same WorkerPID/ChildPID across ACP turns, got (%d,%d) vs (%d,%d)", wpid1, cpid1, wpid2, cpid2)
	}

	argsBytes, _ := os.ReadFile(argsLog)
	argLines := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	if len(argLines) != 1 || !strings.Contains(argLines[0], "agent stdio --no-auto-update") {
		t.Fatalf("expected single persistent agent stdio invocation, got %q", string(argsBytes))
	}
	callsBytes, _ := os.ReadFile(callsLog)
	if !strings.Contains(string(callsBytes), `"method":"authenticate"`) || !strings.Contains(string(callsBytes), `"method":"session/new"`) {
		t.Errorf("expected initialize/authenticate/session/new sequence, got:\n%s", string(callsBytes))
	}
	var gotThought, gotText bool
	for _, ev := range append(evts1, evts2...) {
		if ev.Type == "thought_delta" && strings.Contains(ev.Thinking, "analyzing") {
			gotThought = true
		}
		if ev.Type == "text_delta" && strings.Contains(ev.Text, "pong from mock grok") {
			gotText = true
		}
	}
	if !gotThought || !gotText {
		t.Errorf("expected thought_delta and text_delta from ACP turns, got thought=%v text=%v", gotThought, gotText)
	}
}

func TestGrokBuildCommandEnv_MultiAccountGrokHome(t *testing.T) {
	srv, db := setupTestServer(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	acc := &AgentAccount{
		ID:        "grok:work",
		Agent:     "grok",
		Name:      "work",
		ConfigDir: "~/.grok-profiles/work",
	}
	if err := db.SaveAccount(acc); err != nil {
		t.Fatalf("SaveAccount failed: %v", err)
	}

	sess := &Session{
		ID:        "grok:local:cmd-env-1",
		Agent:     "grok",
		AccountID: "grok:work",
		Cwd:       tmpHome,
	}
	want := "GROK_HOME=" + filepath.Join(tmpHome, ".grok-profiles", "work")
	env := srv.headless.buildCommandEnv(sess)
	found := false
	for _, entry := range env {
		if entry == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("buildCommandEnv missing %q in %+v", want, env)
	}

	_, resolvedEnv := srv.resolveAccountEnv("grok-cli", "work", tmpHome, false)
	if resolvedEnv["GROK_HOME"] != filepath.Join(tmpHome, ".grok-profiles", "work") {
		t.Fatalf("resolveAccountEnv('grok-cli') GROK_HOME = %q", resolvedEnv["GROK_HOME"])
	}
}

func TestGrokSpawnAndAccounts_CanonicalizesGrokCLIAlias(t *testing.T) {
	srv, db := setupTestServer(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	cwd := filepath.Join(tmpHome, "repo")
	_ = os.MkdirAll(cwd, 0755)

	body, _ := json.Marshal(map[string]any{
		"agent":       "grok-cli",
		"cwd":         cwd,
		"engine_type": EngineHeadless,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleSpawn(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("spawn with grok-cli failed: %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode spawn response: %v", err)
	}
	sessResp, _ := resp["session"].(map[string]any)
	if sessResp["agent"] != "grok" {
		t.Fatalf("expected canonical response session.agent 'grok', got %v", resp["session"])
	}
	sessID, _ := resp["id"].(string)
	sess, err := db.GetSession(sessID)
	if err != nil || sess == nil {
		t.Fatalf("GetSession(%q): %v", sessID, err)
	}
	if sess.Agent != "grok" || !strings.HasPrefix(sess.ID, "grok:") {
		t.Fatalf("expected canonicalized session Agent=grok, got id=%q agent=%q", sess.ID, sess.Agent)
	}

	accBody, _ := json.Marshal(map[string]any{"agent": "grok-cli", "name": "work"})
	accReq := httptest.NewRequest(http.MethodPost, "/v1/accounts", bytes.NewReader(accBody))
	accW := httptest.NewRecorder()
	srv.handleAccounts(accW, accReq)
	if accW.Code != http.StatusOK {
		t.Fatalf("POST /v1/accounts with grok-cli failed: %d: %s", accW.Code, accW.Body.String())
	}
	var created AgentAccount
	if err := json.Unmarshal(accW.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode account response: %v", err)
	}
	if created.Agent != "grok" || created.ID != "grok:work" {
		t.Fatalf("expected canonical account grok:work, got id=%q agent=%q", created.ID, created.Agent)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/accounts?agent=grok-cli", nil)
	listW := httptest.NewRecorder()
	srv.handleAccounts(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("GET /v1/accounts?agent=grok-cli failed: %d: %s", listW.Code, listW.Body.String())
	}
	var listed []AgentAccount
	if err := json.Unmarshal(listW.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode accounts list: %v", err)
	}
	seenWork := false
	for _, a := range listed {
		if a.Agent != "grok" {
			t.Errorf("expected canonical agent 'grok', got %+v", a)
		}
		if a.ID == "grok:work" {
			seenWork = true
		}
	}
	if !seenWork {
		t.Fatalf("expected grok:work in GET /v1/accounts?agent=grok-cli, got %+v", listed)
	}
}

func TestGrokTranscriptCache_UsesEffectiveHomeDir(t *testing.T) {
	srv, _ := setupTestServer(t)
	tmpHome := t.TempDir()
	srv.homeDir = tmpHome

	cwd := filepath.Join(tmpHome, "proj")
	nativeID := "grok-cache-1"
	sessDir := filepath.Join(tmpHome, ".grok", "sessions", grok.EncodeCwd(cwd), nativeID)
	_ = os.MkdirAll(sessDir, 0755)
	historyPath := filepath.Join(sessDir, "chat_history.jsonl")
	_ = os.WriteFile(historyPath, []byte(`{"role":"user","content":"hi"}`+"\n"), 0644)

	statPath, size, modNano, err := srv.resolveTranscriptStat(tmpHome, "grok", nativeID, cwd)
	if err != nil {
		t.Fatalf("resolveTranscriptStat: %v", err)
	}
	srv.setCachedTranscript("grok", nativeID, cwd, statPath, size, modNano, []TranscriptMessage{{Role: "user", Content: "hi"}})

	cached, ok := srv.getCachedTranscript("grok", nativeID, cwd)
	if !ok || len(cached) != 1 || cached[0].Content != "hi" {
		t.Fatalf("expected cache hit using effectiveHomeDir, got ok=%v msgs=%+v", ok, cached)
	}
}
