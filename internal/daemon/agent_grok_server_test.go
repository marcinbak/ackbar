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

func TestHeadlessRunner_GrokOneShotWorkerE2E(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)

	mockGrokPath := filepath.Join(binDir, "grok")
	script := `#!/bin/sh
echo "$@" >> "` + filepath.Join(tmpDir, "grok.log") + `"
echo '{"type":"thought","data":"analyzing"}'
echo '{"type":"text","data":"pong from mock grok"}'
echo '{"type":"end","stopReason":"stop"}'
`
	if err := os.WriteFile(mockGrokPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock grok: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)

	srv, db := setupTestServer(t)
	cwd := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(cwd, 0755)

	sess := &Session{
		ID:         "grok:local:grok-e2e-1",
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
	if err := hr.RunTurn(context.Background(), sess, "ping turn 1"); err != nil {
		t.Fatalf("RunTurn Turn 1 failed: %v", err)
	}

	var logStr string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(tmpDir, "grok.log")); err == nil && len(b) > 0 {
			if !hr.IsRunning(sess.ID) {
				logStr = string(b)
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if logStr == "" {
		t.Fatalf("timed out waiting for Turn 1 completion and grok.log")
	}
	if !strings.Contains(logStr, "-s grok-e2e-1") {
		t.Errorf("expected -s grok-e2e-1 in Turn 1 run, got: %s", logStr)
	}

	sess.LastPrompt = "ping turn 1"
	if err := hr.RunTurn(context.Background(), sess, "ping turn 2"); err != nil {
		t.Fatalf("RunTurn Turn 2 failed: %v", err)
	}

	var logStr2 string
	deadline2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline2) {
		if b, err := os.ReadFile(filepath.Join(tmpDir, "grok.log")); err == nil {
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			if len(lines) >= 2 && !hr.IsRunning(sess.ID) {
				logStr2 = string(b)
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if logStr2 == "" {
		t.Fatalf("timed out waiting for Turn 2 completion and grok.log")
	}
	lines := strings.Split(strings.TrimSpace(logStr2), "\n")
	if !strings.Contains(lines[len(lines)-1], "-r grok-e2e-1") {
		t.Errorf("expected -r grok-e2e-1 in Turn 2 run, got: %s", lines[len(lines)-1])
	}
}
