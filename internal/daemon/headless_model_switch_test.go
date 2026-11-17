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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWarmWorker_ClaudeLiveSetModelAndDefaultReset(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	stdinLog := filepath.Join(tmpDir, "claude_stdin.log")
	mockClaude := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
while IFS= read -r line; do
  printf '%%s\n' "$line" >> %q
  case "$line" in
    *'"subtype":"set_model"'*)
      model=$(printf '%%s' "$line" | sed -n 's/.*"model":"\([^"]*\)".*/\1/p')
      printf '{"type":"control_response","response":{"subtype":"success","response":{"model":"%%s"}}}\n' "$model"
      ;;
    *)
      echo '{"type":"result","result":"ok","is_error":false}'
      ;;
  esac
done
`, stdinLog)
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("write mock claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{
		ID:         "claude-code:local:live-model-1",
		Agent:      "claude-code",
		NativeID:   "live-model-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
	}
	_ = db.SaveSession(sess)

	runTurnAndWait := func(prompt string) {
		sub, unsub := h.Subscribe(sess.ID)
		done := waitTurnDone(h, sess.ID, sub)
		if err := h.RunTurn(context.Background(), sess, prompt); err != nil {
			t.Fatalf("RunTurn(%q): %v", prompt, err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for turn %q", prompt)
		}
		unsub()
	}

	runTurnAndWait("turn 1")

	h.mu.RLock()
	h1 := h.warmWorkers[sess.ID]
	workerPID1, childPID1 := h1.WorkerPID, h1.ChildPID
	h.mu.RUnlock()
	if h1 == nil || workerPID1 <= 1 || childPID1 <= 1 {
		t.Fatalf("expected valid warm worker after turn 1, got %+v", h1)
	}

	sess.Model = "claude-sonnet-4-6"
	sess.MetadataUpdatedAt = time.Now().UTC()
	_ = db.SaveSession(sess)
	h.NotifyConfigChanged(sess)

	h.mu.RLock()
	hAfterSwitch := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if hAfterSwitch == nil || hAfterSwitch.WorkerPID != workerPID1 || hAfterSwitch.ChildPID != childPID1 {
		t.Fatalf("expected identical WorkerPID=%d and ChildPID=%d after live set_model, got %+v", workerPID1, childPID1, hAfterSwitch)
	}

	runTurnAndWait("turn 2")

	h.mu.RLock()
	h2 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if h2 == nil || h2.WorkerPID != workerPID1 || h2.ChildPID != childPID1 {
		t.Fatalf("expected same child PID after Turn 2, got %+v (want worker=%d child=%d)", h2, workerPID1, childPID1)
	}

	sess.Model = ""
	sess.MetadataUpdatedAt = time.Now().UTC()
	_ = db.SaveSession(sess)
	h.NotifyConfigChanged(sess)

	runTurnAndWait("turn 3")

	h.mu.RLock()
	h3 := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if h3 == nil || h3.WorkerPID != workerPID1 || h3.ChildPID != childPID1 {
		t.Fatalf("expected same child PID after reset to default, got %+v", h3)
	}

	rawStdin, err := os.ReadFile(stdinLog)
	if err != nil {
		t.Fatalf("read stdin log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawStdin)), "\n")
	var foundSonnet, foundDefault bool
	for _, l := range lines {
		var msg map[string]any
		if json.Unmarshal([]byte(l), &msg) != nil {
			continue
		}
		if msg["type"] != "control_request" {
			continue
		}
		req, _ := msg["request"].(map[string]any)
		if req["subtype"] == "set_model" {
			if req["model"] == "claude-sonnet-4-6" {
				foundSonnet = true
			} else if req["model"] == "default" {
				foundDefault = true
			}
		}
	}
	if !foundSonnet || !foundDefault {
		t.Errorf("expected control_request set_model for claude-sonnet-4-6 (%v) and default (%v); stdin:\n%s", foundSonnet, foundDefault, string(rawStdin))
	}
}

func TestWarmWorker_AntigravityIdleRestartAndMidTurnQueuedSwap(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	argsLog := filepath.Join(tmpDir, "agy_args.log")
	gateFile := filepath.Join(tmpDir, "agy_gate")
	mockAgy := filepath.Join(binDir, "agy")
	script := fmt.Sprintf(`#!/bin/sh
printf 'PID=%%s ARGS=%%s\n' "$$" "$*" >> %q
echo '{"event":"init","conversation_id":"agy-conv-1","model":"gemini-4-carbon-high"}'
while IFS= read -r line; do
  case "$line" in
    *"hold turn"*)
      while [ ! -f %q ]; do sleep 0.02; done
      ;;
  esac
  echo '{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"done"}}'
  echo '{"event":"result","result":{"status":"SUCCESS"}}'
done
`, argsLog, gateFile)
	if err := os.WriteFile(mockAgy, []byte(script), 0755); err != nil {
		t.Fatalf("write mock agy: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{
		ID:         "antigravity:local:agy-temp-1",
		Agent:      "antigravity",
		Host:       "local",
		NativeID:   "agy-temp-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
	}
	_ = db.SaveSession(sess)

	sub1, unsub1 := h.Subscribe(sess.ID)
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		for e := range sub1 {
			if e.Type == "turn_complete" {
				return
			}
		}
	}()
	if err := h.RunTurn(context.Background(), sess, "turn 1"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for agy turn 1")
	}
	unsub1()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && h.IsRunning(sess.ID) {
		time.Sleep(15 * time.Millisecond)
	}

	h.mu.RLock()
	handle1 := h.warmWorkers[sess.ID]
	workerPID1, childPID1 := handle1.WorkerPID, handle1.ChildPID
	h.mu.RUnlock()
	if handle1 == nil || childPID1 <= 1 {
		t.Fatalf("expected warm worker after agy turn 1, got %+v", handle1)
	}

	sess.Model = "gemini-4-argon-medium"
	sess.MetadataUpdatedAt = time.Now().UTC()
	_ = db.SaveSession(sess)
	h.NotifyConfigChanged(sess)

	h.mu.RLock()
	handleIdle := h.warmWorkers[sess.ID]
	workerPIDIdle, childPIDIdle := handleIdle.WorkerPID, handleIdle.ChildPID
	h.mu.RUnlock()
	if handleIdle == nil || workerPIDIdle != workerPID1 || childPIDIdle == childPID1 {
		t.Fatalf("expected agy idle switch to restart child in-place: worker %d->%d child %d->%d", workerPID1, workerPIDIdle, childPID1, childPIDIdle)
	}

	sub2, unsub2 := h.Subscribe(sess.ID)
	turnCount := 0
	bothDone := make(chan struct{})
	go func() {
		defer close(bothDone)
		for e := range sub2 {
			if e.Type == "turn_complete" {
				turnCount++
				if turnCount >= 2 {
					return
				}
			}
		}
	}()
	if err := h.RunTurn(context.Background(), sess, "hold turn for switch"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}

	sess.Model = "gemini-3.8-flash-high"
	sess.MetadataUpdatedAt = time.Now().UTC()
	_ = db.SaveSession(sess)
	h.NotifyConfigChanged(sess)

	h.mu.RLock()
	midHandle := h.warmWorkers[sess.ID]
	midRotate := midHandle != nil && midHandle.rotateAfterTurn
	h.mu.RUnlock()
	if !midRotate {
		t.Fatalf("expected rotateAfterTurn=true for mid-turn agy model switch, got %+v", midHandle)
	}

	h.EnqueuePrompt(sess.ID, "queued turn after switch")
	_ = os.WriteFile(gateFile, []byte("release"), 0644)

	select {
	case <-bothDone:
	case <-time.After(8 * time.Second):
		t.Fatalf("timed out waiting for hold turn and queued turn (completed=%d)", turnCount)
	}
	unsub2()

	deadline2 := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline2) && h.IsRunning(sess.ID) {
		time.Sleep(15 * time.Millisecond)
	}

	h.mu.RLock()
	finalHandle := h.warmWorkers[sess.ID]
	h.mu.RUnlock()
	if finalHandle == nil || finalHandle.rotateAfterTurn || finalHandle.ConfigFingerprint != ConfigFingerprint(sess) {
		t.Fatalf("expected active swapped worker after queued turn, got %+v", finalHandle)
	}

	argsData, _ := os.ReadFile(argsLog)
	argsText := string(argsData)
	if !strings.Contains(argsText, "--conversation agy-conv-1") {
		t.Errorf("expected restarted child to include --conversation agy-conv-1, args:\n%s", argsText)
	}
	if !strings.Contains(argsText, "--model gemini-4-argon-medium") || !strings.Contains(argsText, "--model gemini-3.8-flash-high") {
		t.Errorf("expected args log to record restarted child models, got:\n%s", argsText)
	}
}

func TestBuildArgs_ClaudeResumeDefaultModel(t *testing.T) {
	h := NewHeadlessRunner(nil, nil)

	newSess := &Session{ID: "c1", Agent: "claude-code", NativeID: "n1"}
	_, newArgs, err := h.buildArgs(newSess, "hi", t.TempDir())
	if err != nil {
		t.Fatalf("buildArgs new: %v", err)
	}
	if slices.Contains(newArgs, "--model") {
		t.Errorf("new claude session without model must omit --model, got %v", newArgs)
	}

	_, newPArgs, err := buildPersistentArgs(newSess, false)
	if err != nil {
		t.Fatalf("buildPersistentArgs new: %v", err)
	}
	if slices.Contains(newPArgs, "--model") {
		t.Errorf("new persistent claude session without model must omit --model, got %v", newPArgs)
	}

	resumedSess := &Session{ID: "c2", Agent: "claude-code", NativeID: "n2", FirstPrompt: "prev", Model: ""}
	_, resArgs, err := h.buildArgs(resumedSess, "follow up", t.TempDir())
	if err != nil {
		t.Fatalf("buildArgs resumed: %v", err)
	}
	idx := slices.Index(resArgs, "--model")
	if idx == -1 || idx >= len(resArgs)-1 || resArgs[idx+1] != "default" {
		t.Errorf("expected resumed claude session with empty model to pass --model default, got %v", resArgs)
	}

	_, resPArgs, err := buildPersistentArgs(resumedSess, true)
	if err != nil {
		t.Fatalf("buildPersistentArgs resumed: %v", err)
	}
	pIdx := slices.Index(resPArgs, "--model")
	if pIdx == -1 || pIdx >= len(resPArgs)-1 || resPArgs[pIdx+1] != "default" {
		t.Errorf("expected resumed persistent claude session with empty model to pass --model default, got %v", resPArgs)
	}

	explicitSess := &Session{ID: "c3", Agent: "claude-code", NativeID: "n3", FirstPrompt: "prev", Model: "claude-sonnet-4-6"}
	_, expPArgs, _ := buildPersistentArgs(explicitSess, true)
	eIdx := slices.Index(expPArgs, "--model")
	if eIdx == -1 || eIdx >= len(expPArgs)-1 || expPArgs[eIdx+1] != "claude-sonnet-4-6" {
		t.Errorf("expected explicit model on resume, got %v", expPArgs)
	}
}

func TestParseHeadlessStream_ModelDetectedRuntimeModel(t *testing.T) {
	var detected []string
	input := strings.Join([]string{
		`{"event":"init","conversation_id":"c1","init":{"model":"gemini-4-argon-high"}}`,
		`{"event":"init","conversation_id":"c2","model":"gemini-4-carbon-medium"}`,
		`{"type":"control_response","response":{"subtype":"success","response":{"model":"claude-sonnet-4-6"}}}`,
	}, "\n")
	parseHeadlessStream("sess-md", strings.NewReader(input), streamParseHooks{
		onModelDetected: func(m string) {
			detected = append(detected, m)
		},
	})
	want := []string{"gemini-4-argon-high", "gemini-4-carbon-medium", "claude-sonnet-4-6"}
	if !slices.Equal(detected, want) {
		t.Fatalf("detected models = %v, want %v", detected, want)
	}

	// Verify worker spool emits model_detected into daemon GetRuntimeModel without forwarding to SSE subscribers
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  echo '{"type":"control_response","response":{"subtype":"success","response":{"model":"claude-sonnet-4-6"}}}'
  echo '{"type":"result","result":"ok","is_error":false}'
done
`
	_ = os.WriteFile(mockClaude, []byte(script), 0755)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	sess := &Session{ID: "claude-code:local:rt-detect-1", Agent: "claude-code", NativeID: "rt-detect-1", Cwd: tmpDir, EngineType: EngineHeadless}
	_ = db.SaveSession(sess)

	sub, unsub := h.Subscribe(sess.ID)
	var seenTypes []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range sub {
			seenTypes = append(seenTypes, e.Type)
			if e.Type == "turn_complete" {
				return
			}
		}
	}()
	if err := h.RunTurn(context.Background(), sess, "check runtime model"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for turn_complete")
	}
	unsub()

	if slices.Contains(seenTypes, "model_detected") {
		t.Errorf("model_detected must not be emitted to SSE chat subscribers, got %v", seenTypes)
	}
	if got := h.GetRuntimeModel(sess.ID); got != "claude-sonnet-4-6" {
		t.Errorf("GetRuntimeModel = %q, want claude-sonnet-4-6", got)
	}
}

func TestStaleSessionConfigPreservation(t *testing.T) {
	srv, db := setupTestServer(t)
	h := srv.headless

	// 1. handleAntigravityInit preserves newer config written to oldID in DB before DeleteSession(oldID)
	oldID := "antigravity:local:temp-stale-1"
	newConv := "real-conv-99"
	newID := "antigravity:local:" + newConv
	t0 := time.Now().UTC().Add(-10 * time.Second)
	t1 := time.Now().UTC()

	staleSess := &Session{
		ID: oldID, Agent: "antigravity", Host: "local", NativeID: "temp-stale-1",
		Cwd: t.TempDir(), EngineType: EngineHeadless, Model: "gemini-4-carbon-high", MetadataUpdatedAt: t0,
	}
	dbSess := *staleSess
	dbSess.Model = "gemini-4-argon-medium"
	dbSess.PermissionMode = "plan"
	dbSess.MetadataUpdatedAt = t1
	_ = db.SaveSession(&dbSess)

	h.handleAntigravityInit(staleSess, newConv, "first prompt")
	saved, err := db.GetSession(newID)
	if err != nil || saved == nil {
		t.Fatalf("expected migrated session in DB: %v", err)
	}
	if saved.Model != "gemini-4-argon-medium" || saved.PermissionMode != "plan" {
		t.Errorf("handleAntigravityInit clobbered newer DB config: %+v", saved)
	}

	// 2. applyWorkerEvent interactive_block preserves newer DB config
	t2 := t1.Add(5 * time.Second)
	dbLatest := *saved
	dbLatest.Model = "gemini-3.8-flash-high"
	dbLatest.MetadataUpdatedAt = t2
	_ = db.SaveSession(&dbLatest)

	h.applyWorkerEvent(staleSess, oldID, &TurnSpoolMeta{Prompt: "first prompt"}, ChatStreamEvent{
		Type:    "interactive_block",
		Blocked: &Blocked{Kind: BlockApproval, Question: "Proceed?"},
	}, true)
	afterBlock, _ := db.GetSession(newID)
	if afterBlock.Model != "gemini-3.8-flash-high" {
		t.Errorf("interactive_block clobbered newer DB config: got %q, want gemini-3.8-flash-high", afterBlock.Model)
	}

	// 3. resolveOrAdoptHookSession preserves Model, Effort, PermissionMode, ExtraDirs, MetadataUpdatedAt
	cwd := t.TempDir()
	spawning := &Session{
		ID: "spawning-1", Agent: "claude-code", Host: "local", Managed: true,
		EngineType: EngineTmux, State: StateUnknown, TmuxName: "test-tmux-1", Cwd: cwd,
		Model: "claude-sonnet-4-6", Effort: "high", PermissionMode: "plan",
		ExtraDirs: []string{"/tmp/dir-a"}, MetadataUpdatedAt: t1,
		StartedAt: t1, LastEventAt: t1,
	}
	_ = db.SaveSession(spawning)

	adopted, ok := srv.resolveOrAdoptHookSession(&Event{
		Agent: "claude-code", NativeID: "hook-native-1", Cwd: cwd,
	}, "local", "claude-code:local:hook-native-1", "")
	if !ok || adopted == nil {
		t.Fatalf("expected spawning session adoption to succeed")
	}
	if adopted.Model != "claude-sonnet-4-6" || adopted.Effort != "high" || adopted.PermissionMode != "plan" || !slices.Equal(adopted.ExtraDirs, []string{"/tmp/dir-a"}) || !adopted.MetadataUpdatedAt.Equal(t1) {
		t.Errorf("adopted session missed config fields: %+v", adopted)
	}
	// Mutating spawning.ExtraDirs must not mutate adopted.ExtraDirs
	spawning.ExtraDirs[0] = "/mutated"
	if adopted.ExtraDirs[0] != "/tmp/dir-a" {
		t.Errorf("expected deep-copied ExtraDirs on adopted session, got %v", adopted.ExtraDirs)
	}

	// Rotation of activeManaged
	adopted.State = StateWorking
	_ = db.SaveSession(adopted)
	rotated, ok := srv.resolveOrAdoptHookSession(&Event{
		Agent: "claude-code", NativeID: "hook-native-2", Cwd: cwd,
	}, "local", "claude-code:local:hook-native-2", "")
	if !ok || rotated == nil {
		t.Fatalf("expected activeManaged rotation to succeed")
	}
	if rotated.Model != "claude-sonnet-4-6" || rotated.Effort != "high" || rotated.PermissionMode != "plan" || !slices.Equal(rotated.ExtraDirs, []string{"/tmp/dir-a"}) || !rotated.MetadataUpdatedAt.Equal(t1) {
		t.Errorf("rotated session missed config fields: %+v", rotated)
	}
}

func TestServer_NormalizeModelLabelInConfigAndSpawn(t *testing.T) {
	srv, db := setupTestServer(t)

	agySess := &Session{
		ID:         "antigravity:local:norm-1",
		Agent:      "antigravity",
		Host:       "local",
		Cwd:        t.TempDir(),
		State:      StateIdle,
		EngineType: EngineHeadless,
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_ = db.SaveSession(agySess)

	req := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+agySess.ID+"/config", bytes.NewBufferString(`{"model":"Gemini 4 Argon (Medium)"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH config status = %d: %s", w.Code, w.Body.String())
	}
	updated, _ := db.GetSession(agySess.ID)
	if updated.Model != "gemini-4-argon-medium" {
		t.Errorf("expected normalized model 'gemini-4-argon-medium', got %q", updated.Model)
	}

	reqDefault := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+agySess.ID+"/config", bytes.NewBufferString(`{"model":"default"}`))
	reqDefault.Header.Set("Content-Type", "application/json")
	wDefault := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wDefault, reqDefault)
	if wDefault.Code != http.StatusOK {
		t.Fatalf("PATCH config default status = %d: %s", wDefault.Code, wDefault.Body.String())
	}
	updatedDefault, _ := db.GetSession(agySess.ID)
	if updatedDefault.Model != "" {
		t.Errorf("expected 'default' to clear model to empty, got %q", updatedDefault.Model)
	}

	spawnBody, _ := json.Marshal(map[string]any{
		"agent":       "antigravity",
		"cwd":         t.TempDir(),
		"engine_type": "headless",
		"model":       "Gemini 4 Argon (Medium)",
	})
	spawnReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(spawnBody))
	spawnReq.Header.Set("Content-Type", "application/json")
	wSpawn := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wSpawn, spawnReq)
	if wSpawn.Code != http.StatusOK && wSpawn.Code != http.StatusCreated {
		t.Fatalf("spawn status = %d: %s", wSpawn.Code, wSpawn.Body.String())
	}
	var spawnResp struct {
		Session *Session `json:"session"`
	}
	if err := json.Unmarshal(wSpawn.Body.Bytes(), &spawnResp); err != nil {
		t.Fatalf("unmarshal spawn resp: %v", err)
	}
	if spawnResp.Session == nil || spawnResp.Session.Model != "gemini-4-argon-medium" {
		t.Errorf("expected spawn to normalize model to 'gemini-4-argon-medium', got %+v", spawnResp.Session)
	}
}

func TestWarmWorker_ConcurrentRunTurnAndNotifyConfigChangedRace(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockClaude := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"subtype":"set_model"'*)
      model=$(printf '%s' "$line" | sed -n 's/.*"model":"\([^"]*\)".*/\1/p')
      printf '{"type":"control_response","response":{"subtype":"success","response":{"model":"%s"}}}\n' "$model"
      ;;
    *)
      echo '{"type":"result","result":"ok","is_error":false}'
      ;;
  esac
done
`
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("write mock claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	h := NewHeadlessRunner(db, nil)
	defer h.ShutdownAllWorkers(time.Second)

	newSess := func(model, effort string) *Session {
		return &Session{
			ID:         "claude-code:local:race-conc-1",
			Agent:      "claude-code",
			Host:       "local",
			NativeID:   "race-conc-1",
			Cwd:        tmpDir,
			EngineType: EngineHeadless,
			Model:      model,
			Effort:     effort,
		}
	}

	initSess := newSess("", "")
	_ = db.SaveSession(initSess)
	sub0, unsub0 := h.Subscribe(initSess.ID)
	done0 := waitTurnDone(h, initSess.ID, sub0)
	if err := h.RunTurn(context.Background(), initSess, "warmup"); err != nil {
		t.Fatalf("warmup RunTurn: %v", err)
	}
	select {
	case <-done0:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for warmup turn")
	}
	unsub0()

	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	models := []string{"", "claude-sonnet-4-6", "claude-opus-4-6"}
	efforts := []string{"", "high"}

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 8; i++ {
			s := newSess(models[i%len(models)], efforts[i%len(efforts)])
			s.FirstPrompt = "warmup"
			for {
				err := h.RunTurn(context.Background(), s, fmt.Sprintf("turn-%d", i))
				if err == ErrTurnInProgress {
					time.Sleep(5 * time.Millisecond)
					continue
				}
				if err != nil {
					t.Errorf("RunTurn(%d) failed: %v", i, err)
				}
				break
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && h.IsRunning(s.ID) {
				time.Sleep(5 * time.Millisecond)
			}
		}
		close(stopCh)
	}()

	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				s := newSess(models[i%len(models)], efforts[(i/3)%len(efforts)])
				s.FirstPrompt = "warmup"
				h.NotifyConfigChanged(s)
				i++
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	wg.Wait()
}
