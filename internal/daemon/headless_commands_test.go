package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setupTestCommandsServer(t *testing.T) (*Server, *DB, string) {
	t.Helper()
	srv, db := setupTestServer(t)
	tmpDir := t.TempDir()
	srv.homeDir = tmpDir
	return srv, db, tmpDir
}

func TestNormalizeAndValidateExtraDirs(t *testing.T) {
	tmpDir := t.TempDir()
	cwd := filepath.Join(tmpDir, "project")
	dirA := filepath.Join(tmpDir, "dirA")
	dirB := filepath.Join(tmpDir, "dirB")
	_ = os.MkdirAll(cwd, 0755)
	_ = os.MkdirAll(dirA, 0755)
	_ = os.MkdirAll(dirB, 0755)

	// 1. Basic add relative and absolute
	addDirs := []string{dirA, "subdir"}
	_ = os.MkdirAll(filepath.Join(cwd, "subdir"), 0755)
	dirs, err := normalizeAndValidateExtraDirs(cwd, nil, nil, addDirs, nil, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 dirs, got %d: %v", len(dirs), dirs)
	}

	// 2. Reject flag injection
	_, err = normalizeAndValidateExtraDirs(cwd, nil, nil, []string{"--dangerously-skip-permissions"}, nil, tmpDir)
	if err == nil {
		t.Fatalf("expected error for flag hyphen")
	}

	// 3. Reject control chars
	_, err = normalizeAndValidateExtraDirs(cwd, nil, nil, []string{"foo\nbar"}, nil, tmpDir)
	if err == nil {
		t.Fatalf("expected error for newline in path")
	}

	// 4. Reject root /
	_, err = normalizeAndValidateExtraDirs(cwd, nil, nil, []string{"/"}, nil, tmpDir)
	if err == nil {
		t.Fatalf("expected error for filesystem root")
	}

	// 5. Expand ~/
	homeSubdir := filepath.Join(tmpDir, "homeDirX")
	_ = os.MkdirAll(homeSubdir, 0755)
	dirs, err = normalizeAndValidateExtraDirs(cwd, nil, nil, []string{"~/homeDirX"}, nil, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	canonicalHomeSubdir, _ := filepath.EvalSymlinks(homeSubdir)
	if len(dirs) != 1 || (dirs[0] != homeSubdir && dirs[0] != canonicalHomeSubdir) {
		t.Fatalf("expected %s, got %v", homeSubdir, dirs)
	}

	// 6. RemoveDirs removes existing
	existing := []string{dirA, dirB}
	dirs, err = normalizeAndValidateExtraDirs(cwd, existing, nil, nil, []string{dirA}, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	canonicalDirB, _ := filepath.EvalSymlinks(dirB)
	if len(dirs) != 1 || (dirs[0] != dirB && dirs[0] != canonicalDirB) {
		t.Fatalf("expected [dirB], got %v", dirs)
	}

	// 7. Non-existent addDirs returns error
	_, err = normalizeAndValidateExtraDirs(cwd, nil, nil, []string{"non_existent_dir_123"}, nil, tmpDir)
	if err == nil {
		t.Fatalf("expected error for non-existent directory")
	}
}

func TestHandleSessionShell(t *testing.T) {
	srv, db, tmpDir := setupTestCommandsServer(t)
	defer db.Close()

	cwd := filepath.Join(tmpDir, "shell_work")
	_ = os.MkdirAll(cwd, 0755)

	sess := &Session{
		ID:         "claude-code:local:shell-test-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "shell-test-1",
		Cwd:        cwd,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sess)

	// 1. Missing Content-Type -> 415
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/shell", bytes.NewReader([]byte(`{"command": "echo hi"}`)))
	w := httptest.NewRecorder()
	srv.handleSessionShell(w, req, sess.ID)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415 for missing Content-Type, got %d", w.Code)
	}

	// 2. Missing / empty command -> 400
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/shell", bytes.NewReader([]byte(`{"command": "  "}`)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.handleSessionShell(w, req, sess.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty command, got %d", w.Code)
	}

	// 3. Successful shell command
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/shell", bytes.NewReader([]byte(`{"command": "echo 'hello from shell'"}`)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.handleSessionShell(w, req, sess.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to unmarshal shell response: %v", err)
	}
	stdout, _ := res["stdout"].(string)
	if !bytes.Contains([]byte(stdout), []byte("hello from shell")) {
		t.Fatalf("expected stdout to contain 'hello from shell', got %q", stdout)
	}
	if code, _ := res["exit_code"].(float64); code != 0 {
		t.Fatalf("expected exit_code 0, got %v", code)
	}

	// 4. Invalid cwd -> 400
	sessBadCwd := &Session{
		ID:         "claude-code:local:shell-test-bad",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "shell-test-bad",
		Cwd:        filepath.Join(tmpDir, "non_existent_path_xyz"),
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sessBadCwd)
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessBadCwd.ID+"/shell", bytes.NewReader([]byte(`{"command": "echo hi"}`)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.handleSessionShell(w, req, sessBadCwd.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad cwd, got %d", w.Code)
	}

	// 5. CSRF protection in tokenless mode: Origin: https://evil.example -> 403 Forbidden
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/shell", bytes.NewReader([]byte(`{"command": "echo pwn"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for untrusted origin, got %d", w.Code)
	}
}

func TestHandleSessionClear(t *testing.T) {
	srv, db, tmpDir := setupTestCommandsServer(t)
	defer db.Close()

	sess := &Session{
		ID:            "claude-code:local:clear-test-1",
		Agent:         "claude-code",
		Host:          "local",
		NativeID:      "clear-test-1",
		Cwd:           tmpDir,
		EngineType:    EngineHeadless,
		Managed:       true,
		State:         StateIdle,
		CurrentTaskID: "task_clr_1",
		ExtraDirs:     []string{tmpDir},
	}
	_ = db.SaveSession(sess)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/clear", nil)
	w := httptest.NewRecorder()
	srv.handleSessionClear(w, req, sess.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		Status       string   `json:"status"`
		OldSessionID string   `json:"old_session_id"`
		Session      *Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if res.Status != "ok" || res.Session == nil {
		t.Fatalf("expected status ok and non-nil session, got %+v", res)
	}
	if res.Session.ID == sess.ID {
		t.Fatalf("expected rotated session ID, got same %s", res.Session.ID)
	}
	if res.Session.CurrentTaskID != "task_clr_1" {
		t.Fatalf("expected CurrentTaskID to be preserved, got %q", res.Session.CurrentTaskID)
	}
	if len(res.Session.ExtraDirs) != 1 || res.Session.ExtraDirs[0] != tmpDir {
		t.Fatalf("expected ExtraDirs preserved, got %v", res.Session.ExtraDirs)
	}

	// Check old session marked ended
	oldSess, _ := db.GetSession(sess.ID)
	if oldSess == nil || !oldSess.IsDone || oldSess.State != StateEnded {
		t.Fatalf("expected old session marked done/ended, got %+v", oldSess)
	}
}

func TestHandleSessionConfigExtended(t *testing.T) {
	srv, db, tmpDir := setupTestCommandsServer(t)
	defer db.Close()

	sess := &Session{
		ID:         "claude-code:local:cfg-test-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "cfg-test-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sess)

	// 1. Valid config update
	payload := `{"model": "claude-3-7-sonnet", "effort": "xhigh", "permission_mode": "acceptEdits"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/config", bytes.NewReader([]byte(payload)))
	w := httptest.NewRecorder()
	srv.handleSessionConfig(w, req, sess.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, _ := db.GetSession(sess.ID)
	if updated.Model != "claude-3-7-sonnet" || updated.Effort != "xhigh" || updated.PermissionMode != "acceptEdits" {
		t.Fatalf("unexpected session config: %+v", updated)
	}

	// 2. Reject invalid model name starting with hyphen
	badModel := `{"model": "--dangerously-skip-permissions"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/config", bytes.NewReader([]byte(badModel)))
	w = httptest.NewRecorder()
	srv.handleSessionConfig(w, req, sess.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for model with hyphen, got %d", w.Code)
	}

	// 3. Reject invalid effort
	badEffort := `{"effort": "extreme"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/config", bytes.NewReader([]byte(badEffort)))
	w = httptest.NewRecorder()
	srv.handleSessionConfig(w, req, sess.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid effort, got %d", w.Code)
	}

	// 4. Reject invalid permission_mode
	badPerm := `{"permission_mode": "godMode"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/config", bytes.NewReader([]byte(badPerm)))
	w = httptest.NewRecorder()
	srv.handleSessionConfig(w, req, sess.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid permission_mode, got %d", w.Code)
	}

	// 5. AddDirs via config
	subDir := filepath.Join(tmpDir, "extra_sub")
	_ = os.MkdirAll(subDir, 0755)
	addPayload, _ := json.Marshal(map[string]interface{}{
		"add_dirs": []string{subDir},
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/config", bytes.NewReader(addPayload))
	w = httptest.NewRecorder()
	srv.handleSessionConfig(w, req, sess.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	updated, _ = db.GetSession(sess.ID)
	canonicalSubDir, _ := filepath.EvalSymlinks(subDir)
	if len(updated.ExtraDirs) != 1 || (updated.ExtraDirs[0] != subDir && updated.ExtraDirs[0] != canonicalSubDir) {
		t.Fatalf("expected ExtraDirs to contain %s, got %v", subDir, updated.ExtraDirs)
	}

	// 6. Non-existent add_dirs via config -> 400
	badDirPayload := `{"add_dirs": ["non_existent_folder_xyz"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/config", bytes.NewReader([]byte(badDirPayload)))
	w = httptest.NewRecorder()
	srv.handleSessionConfig(w, req, sess.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-existent add_dirs, got %d", w.Code)
	}

	// 7. dontAsk on antigravity -> 400 Bad Request
	sessAgy := &Session{
		ID:         "antigravity:local:cfg-agy-1",
		Agent:      "antigravity",
		Host:       "local",
		NativeID:   "cfg-agy-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sessAgy)
	dontAskPayload := `{"permission_mode": "dontAsk"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessAgy.ID+"/config", bytes.NewReader([]byte(dontAskPayload)))
	w = httptest.NewRecorder()
	srv.handleSessionConfig(w, req, sessAgy.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for dontAsk on antigravity, got %d", w.Code)
	}
}

func TestHandleSessionCompact(t *testing.T) {
	srv, db, tmpDir := setupTestCommandsServer(t)
	defer db.Close()

	// 1. Antigravity delegates to executeHeadlessCompaction
	sessAgy := &Session{
		ID:         "antigravity:local:compact-test-1",
		Agent:      "antigravity",
		Host:       "local",
		NativeID:   "compact-test-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sessAgy)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessAgy.ID+"/compact", bytes.NewReader([]byte(`{"instructions": "Focus on auth"}`)))
	w := httptest.NewRecorder()
	srv.handleSessionCompact(w, req, sessAgy.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for antigravity compact, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if res["mode"] != "rotated_session" {
		t.Fatalf("expected rotated_session mode, got %v", res["mode"])
	}

	// 2. Claude Code native_stream
	sessClaude := &Session{
		ID:         "claude-code:local:compact-test-2",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "compact-test-2",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sessClaude)

	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessClaude.ID+"/compact", bytes.NewReader([]byte(`{"instructions": "Summarize"}`)))
	w = httptest.NewRecorder()
	srv.handleSessionCompact(w, req, sessClaude.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for claude compact, got %d: %s", w.Code, w.Body.String())
	}
	var res2 map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &res2)
	if res2["mode"] != "native_stream" {
		t.Fatalf("expected native_stream mode, got %v", res2["mode"])
	}
}
