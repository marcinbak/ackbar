package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSessionInspect(t *testing.T) {
	srv, db, tmpDir := setupTestCommandsServer(t)
	defer db.Close()

	sess := &Session{
		ID:         "claude-code:local:inspect-test-1",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "inspect-test-1",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sess)

	// 1. Invalid kind -> 400
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/inspect?kind=unknown", nil)
	w := httptest.NewRecorder()
	srv.handleSessionInspect(w, req, sess.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown kind, got %d", w.Code)
	}

	// 2. Kind plugins -> 200
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/inspect?kind=plugins", nil)
	w = httptest.NewRecorder()
	srv.handleSessionInspect(w, req, sess.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for plugins, got %d", w.Code)
	}
	var resPlugins map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resPlugins); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resPlugins["kind"] != "plugins" {
		t.Fatalf("expected kind plugins, got %v", resPlugins["kind"])
	}

	// 3. Kind agents -> 200
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/inspect?kind=agents", nil)
	w = httptest.NewRecorder()
	srv.handleSessionInspect(w, req, sess.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for agents, got %d", w.Code)
	}
	var resAgents map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resAgents)
	builtins, _ := resAgents["builtins"].([]interface{})
	if len(builtins) < 3 {
		t.Fatalf("expected at least 3 builtin agents, got %d", len(builtins))
	}

	// 4. Kind doctor -> 200
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/inspect?kind=doctor", nil)
	w = httptest.NewRecorder()
	srv.handleSessionInspect(w, req, sess.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for doctor, got %d", w.Code)
	}

	// 5. Kind context & cost -> 200
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/inspect?kind=context", nil)
	w = httptest.NewRecorder()
	srv.handleSessionInspect(w, req, sess.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for context, got %d", w.Code)
	}
	var resCtx map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resCtx)
	if resCtx["kind"] != "context" || resCtx["context_limit_tokens"] == nil {
		t.Fatalf("expected valid context response, got %+v", resCtx)
	}
}

func TestHandleSessionDiff(t *testing.T) {
	srv, db, tmpDir := setupTestCommandsServer(t)
	defer db.Close()

	// 1. Non-git directory
	sessNonGit := &Session{
		ID:         "claude-code:local:diff-test-nongit",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "diff-test-nongit",
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sessNonGit)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessNonGit.ID+"/diff", nil)
	w := httptest.NewRecorder()
	srv.handleSessionDiff(w, req, sessNonGit.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resNonGit map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resNonGit)
	if isGit, _ := resNonGit["is_git_repo"].(bool); isGit {
		t.Fatalf("expected is_git_repo false for empty dir")
	}

	// 2. Real git directory with changes
	gitDir := filepath.Join(tmpDir, "git_repo")
	_ = os.MkdirAll(gitDir, 0755)
	_ = exec.Command("git", "-C", gitDir, "init").Run()
	_ = exec.Command("git", "-C", gitDir, "config", "user.email", "test@test.local").Run()
	_ = exec.Command("git", "-C", gitDir, "config", "user.name", "Test User").Run()
	_ = os.WriteFile(filepath.Join(gitDir, "file1.txt"), []byte("hello world\n"), 0644)
	_ = exec.Command("git", "-C", gitDir, "add", "file1.txt").Run()
	_ = exec.Command("git", "-C", gitDir, "commit", "-m", "init").Run()

	// Modify file
	_ = os.WriteFile(filepath.Join(gitDir, "file1.txt"), []byte("hello modified\n"), 0644)

	sessGit := &Session{
		ID:         "claude-code:local:diff-test-git",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "diff-test-git",
		Cwd:        gitDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sessGit)

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessGit.ID+"/diff", nil)
	w = httptest.NewRecorder()
	srv.handleSessionDiff(w, req, sessGit.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resGit map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resGit); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if isGit, _ := resGit["is_git_repo"].(bool); !isGit {
		t.Fatalf("expected is_git_repo true")
	}
	diffStr, _ := resGit["diff"].(string)
	if !strings.Contains(diffStr, "hello modified") {
		t.Fatalf("expected diff to contain 'hello modified', got %q", diffStr)
	}
	if clean, _ := resGit["clean"].(bool); clean {
		t.Fatalf("expected clean to be false with modifications")
	}

	// 3. Unborn HEAD (fresh git init with modified file before first commit)
	unbornDir := filepath.Join(tmpDir, "git_unborn")
	_ = os.MkdirAll(unbornDir, 0755)
	_ = exec.Command("git", "-C", unbornDir, "init").Run()
	_ = os.WriteFile(filepath.Join(unbornDir, "unborn.txt"), []byte("fresh unborn content\n"), 0644)

	sessUnborn := &Session{
		ID:         "claude-code:local:diff-test-unborn",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "diff-test-unborn",
		Cwd:        unbornDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sessUnborn)

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessUnborn.ID+"/diff", nil)
	w = httptest.NewRecorder()
	srv.handleSessionDiff(w, req, sessUnborn.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for unborn repo, got %d: %s", w.Code, w.Body.String())
	}
	var resUnborn map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resUnborn)
	if isGit, _ := resUnborn["is_git_repo"].(bool); !isGit {
		t.Fatalf("expected is_git_repo true for unborn repo")
	}
}
