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

	"ackbar/internal/tmux"
)

func TestGrokSpawnCoercionAndFlags(t *testing.T) {
	srv, db := setupTestServer(t)
	cwd := filepath.Join(t.TempDir(), "grok-project")
	_ = os.MkdirAll(cwd, 0755)

	extraDir := filepath.Join(t.TempDir(), "grok-extra")
	_ = os.MkdirAll(extraDir, 0755)

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

	if resp["engine_type"] != EngineTmux {
		t.Errorf("Expected engine_type coerced to 'tmux', got %v", resp["engine_type"])
	}

	sessID, ok := resp["id"].(string)
	if !ok || sessID == "" {
		t.Fatalf("Expected session id in response")
	}

	sess, err := db.GetSession(sessID)
	if err != nil || sess == nil {
		t.Fatalf("Failed to fetch session from DB: %v", err)
	}

	if sess.EngineType != EngineTmux {
		t.Errorf("Expected sess.EngineType == EngineTmux, got %s", sess.EngineType)
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
	if len(sess.ExtraDirs) != 1 || sess.ExtraDirs[0] != extraDir {
		t.Errorf("Expected sess.ExtraDirs == [%s], got %+v", extraDir, sess.ExtraDirs)
	}
}

func TestAppendGrokCLIArgs(t *testing.T) {
	base := "grok -s 1234"

	// 1. Clean valid arguments
	res := appendGrokCLIArgs(base, "grok-2", "high", "bypass", []string{"/path/one", "/path/it's a path"})
	expected := "grok -s 1234 -m grok-2 --effort high --permission-mode bypass --add-dir '/path/one' --add-dir '/path/it'\\''s a path'"
	if res != expected {
		t.Errorf("appendGrokCLIArgs failed:\ngot:  %q\nwant: %q", res, expected)
	}

	// 2. Shell metacharacters in model/effort/permission rejected safely
	resUnsafe := appendGrokCLIArgs(base, "grok-2;rm -rf /", "high && echo bad", "bypass|cat", nil)
	if strings.Contains(resUnsafe, ";") || strings.Contains(resUnsafe, "&&") || strings.Contains(resUnsafe, "|") {
		t.Errorf("Dangerous metacharacters leaked into command: %s", resUnsafe)
	}
	if resUnsafe != base {
		t.Errorf("Expected unchanged base on invalid args, got %q", resUnsafe)
	}

	// 3. Newline control chars in extraDirs rejected
	resControl := appendGrokCLIArgs(base, "", "", "", []string{"/safe/dir", "/bad\ndir"})
	if strings.Contains(resControl, "\n") || !strings.Contains(resControl, "/safe/dir") {
		t.Errorf("Expected newline dir skipped: %s", resControl)
	}
}

func TestGrokModelResolution_SummaryAndConfig(t *testing.T) {
	srv, _ := setupTestServer(t)
	tmpHome := t.TempDir()
	srv.homeDir = tmpHome

	cwd := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(cwd, 0755)

	nativeID := "grok-sess-model"
	encodedCwd := EncodeGrokCwd(cwd)
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
	srv, _ := setupTestServer(t)
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

	base := "grok -s 1234"
	if res := appendGrokCLIArgs(base, "-flag", "--effort", "-p", nil); res != base {
		t.Errorf("appendGrokCLIArgs should reject leading hyphen flags, got %q", res)
	}

	initCmd := appendCLIInitPrompt("grok", base)
	expectedInit := base + ` -- "$ACKBAR_INIT_PROMPT"`
	if initCmd != expectedInit {
		t.Errorf("appendCLIInitPrompt(grok) = %q, want %q", initCmd, expectedInit)
	}
}
