package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscriptCache_HitWithoutDiskRead(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	tempHome := t.TempDir()
	server.homeDir = tempHome

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", origHome)

	convID := "conv-cache-test"
	logDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	jsonl := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-12T00:00:00Z","content":"<USER_REQUEST>Initial prompt</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-12T00:00:01Z","content":"Initial reply"}
`
	logPath := filepath.Join(logDir, "transcript.jsonl")
	if err := os.WriteFile(logPath, []byte(jsonl), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// First extraction: cold load, populates cache
	t1, err := server.ExtractTranscript("antigravity", convID, "/workspace")
	if err != nil {
		t.Fatalf("ExtractTranscript cold failed: %v", err)
	}
	if len(t1.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(t1.Messages))
	}

	// Verify it was placed into server transcript cache
	cached, ok := server.getCachedTranscript("antigravity", convID, "/workspace")
	if !ok || len(cached) != 2 {
		t.Fatalf("expected 2 cached messages, got ok=%v len=%d", ok, len(cached))
	}

	// Overwrite underlying file with corrupt content while preserving exact file size and modTime
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	corruptBytes := make([]byte, info.Size())
	for i := range corruptBytes {
		corruptBytes[i] = 'X'
	}
	if err := os.WriteFile(logPath, corruptBytes, 0644); err != nil {
		t.Fatalf("Write corrupt file failed: %v", err)
	}
	_ = os.Chtimes(logPath, info.ModTime(), info.ModTime())

	// Second extraction: should hit cache and succeed without reading corrupt disk content
	t2, err := server.ExtractTranscript("antigravity", convID, "/workspace")
	if err != nil {
		t.Fatalf("ExtractTranscript warm failed: %v", err)
	}
	if len(t2.Messages) != 2 || t2.Messages[0].Content != "Initial prompt" {
		t.Fatalf("expected cached transcript, got: %+v", t2.Messages)
	}
}

func TestTranscriptCache_IncrementalClaudeAppend(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	tempHome := t.TempDir()
	server.homeDir = tempHome

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", origHome)

	nativeID := "claude-sess-incr-1"
	cwd := "/Users/test/project"
	sanitizedDir := "-Users-test-project"
	logDir := filepath.Join(tempHome, ".claude", "projects", sanitizedDir)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	logPath := filepath.Join(logDir, nativeID+".jsonl")

	initialLines := `{"type":"user","message":{"role":"user","content":"First question"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"First answer"}]}}
`
	if err := os.WriteFile(logPath, []byte(initialLines), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	t1, err := server.ExtractTranscript("claude-code", nativeID, cwd)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}
	if len(t1.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(t1.Messages))
	}

	// Sleep briefly so file modtime strictly advances
	time.Sleep(20 * time.Millisecond)

	appendLines := `{"type":"user","message":{"role":"user","content":"Second question"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Second answer"}]}}
`
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile append failed: %v", err)
	}
	if _, err := f.WriteString(appendLines); err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	_ = f.Close()

	t2, err := server.ExtractTranscript("claude-code", nativeID, cwd)
	if err != nil {
		t.Fatalf("ExtractTranscript incremental failed: %v", err)
	}
	if len(t2.Messages) != 4 {
		t.Fatalf("expected 4 messages after append, got %d", len(t2.Messages))
	}
	if t2.Messages[2].Content != "Second question" || t2.Messages[3].Content != "Second answer" {
		t.Fatalf("unexpected appended messages: %+v", t2.Messages[2:])
	}
}

type transcriptMockProvider struct {
	MockProvider
	agentName string
	messages  []TranscriptMessage
}

func (p *transcriptMockProvider) Agent() string {
	return p.agentName
}

func (p *transcriptMockProvider) ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error) {
	return p.messages, nil
}

func TestHandleSessionTranscript_NDJSONReverseOrder(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	mockMsgs := []TranscriptMessage{
		{Role: "user", Content: "Prompt 0"},
		{Role: "assistant", Content: "Answer 1"},
		{Role: "user", Content: "Prompt 2"},
	}
	server.RegisterProvider(&transcriptMockProvider{
		agentName: "mock-stream",
		messages:  mockMsgs,
	})

	sessID := "mock-stream:local:stream-sess-1"
	if err := db.SaveSession(&Session{
		ID:       sessID,
		Agent:    "mock-stream",
		Host:     "local",
		NativeID: "stream-sess-1",
		Cwd:      "/tmp",
	}); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sessID+"&format=ndjson", nil)
	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/x-ndjson") {
		t.Fatalf("expected application/x-ndjson content type, got %s", contentType)
	}

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 NDJSON lines (1 meta + 3 msgs), got %d: %s", len(lines), w.Body.String())
	}

	// Line 1: Meta
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &meta); err != nil {
		t.Fatalf("failed to unmarshal meta: %v", err)
	}
	if meta["type"] != "meta" || int(meta["total"].(float64)) != 3 {
		t.Fatalf("unexpected meta: %+v", meta)
	}

	// Lines 2-4: Messages in reverse order (msg 2, msg 1, msg 0)
	expectedIndices := []int{2, 1, 0}
	for i, expIdx := range expectedIndices {
		var item struct {
			Type    string            `json:"type"`
			Index   int               `json:"index"`
			Message TranscriptMessage `json:"message"`
		}
		if err := json.Unmarshal([]byte(lines[i+1]), &item); err != nil {
			t.Fatalf("failed to unmarshal item %d: %v", i, err)
		}
		if item.Type != "msg" || item.Index != expIdx {
			t.Fatalf("line %d: expected msg index %d, got %d (%+v)", i+1, expIdx, item.Index, item)
		}
		if item.Message.Content != mockMsgs[expIdx].Content {
			t.Fatalf("line %d: expected content %q, got %q", i+1, mockMsgs[expIdx].Content, item.Message.Content)
		}
	}
}

func TestHandleSessionTranscript_IncludeFormats(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.RegisterProvider(&transcriptMockProvider{
		agentName: "mock-formats",
		messages: []TranscriptMessage{
			{Role: "user", Content: "Hello world"},
		},
	})

	sessID := "mock-formats:local:sess-formats"
	_ = db.SaveSession(&Session{
		ID:       sessID,
		Agent:    "mock-formats",
		Host:     "local",
		NativeID: "sess-formats",
		Cwd:      "/tmp",
	})

	// include_formats=false
	req1 := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sessID+"&format=json&include_formats=false", nil)
	w1 := httptest.NewRecorder()
	server.Mux().ServeHTTP(w1, req1)

	var payload1 map[string]interface{}
	if err := json.Unmarshal(w1.Body.Bytes(), &payload1); err != nil {
		t.Fatalf("Unmarshal payload1 failed: %v", err)
	}
	if ansi, _ := payload1["ansi"].(string); ansi != "" {
		t.Fatalf("expected empty ansi when include_formats=false, got %q", ansi)
	}
	if md, _ := payload1["markdown"].(string); md != "" {
		t.Fatalf("expected empty markdown when include_formats=false, got %q", md)
	}

	// include_formats omitted (default true)
	req2 := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sessID+"&format=json", nil)
	w2 := httptest.NewRecorder()
	server.Mux().ServeHTTP(w2, req2)

	var payload2 map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &payload2); err != nil {
		t.Fatalf("Unmarshal payload2 failed: %v", err)
	}
	if ansi, _ := payload2["ansi"].(string); !strings.Contains(ansi, "Hello world") {
		t.Fatalf("expected formatted ansi, got %q", ansi)
	}
}

func TestResolveGitBranch_PureGo(t *testing.T) {
	// 1. Missing directory -> empty string immediately
	missingDir := filepath.Join(t.TempDir(), "does-not-exist-dir")
	if branch := ResolveGitBranch(missingDir); branch != "" {
		t.Fatalf("expected empty branch for missing dir, got %q", branch)
	}

	// 2. Non-git directory -> empty string
	nonGitDir := t.TempDir()
	if branch := ResolveGitBranch(nonGitDir); branch != "" {
		t.Fatalf("expected empty branch for non-git dir, got %q", branch)
	}

	// 3. Regular git directory with ref: refs/heads/feature-alpha
	gitRepoDir := t.TempDir()
	dotGit := filepath.Join(gitRepoDir, ".git")
	if err := os.MkdirAll(dotGit, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dotGit, "HEAD"), []byte("ref: refs/heads/feature-alpha\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	subDir := filepath.Join(gitRepoDir, "sub", "pkg")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("MkdirAll sub failed: %v", err)
	}
	if branch := ResolveGitBranch(subDir); branch != "feature-alpha" {
		t.Fatalf("expected feature-alpha from ancestor .git walk, got %q", branch)
	}

	// 4. Git worktree with gitdir reference
	wtDir := t.TempDir()
	adminGitDir := filepath.Join(t.TempDir(), "main-repo", ".git", "worktrees", "wt-1")
	if err := os.MkdirAll(adminGitDir, 0755); err != nil {
		t.Fatalf("MkdirAll adminGitDir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adminGitDir, "HEAD"), []byte("ref: refs/heads/perf/wt-test\n"), 0644); err != nil {
		t.Fatalf("WriteFile admin HEAD failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+adminGitDir+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile worktree .git failed: %v", err)
	}
	if branch := ResolveGitBranch(wtDir); branch != "perf/wt-test" {
		t.Fatalf("expected perf/wt-test from worktree gitdir, got %q", branch)
	}
}

func TestGetSessionByAlias(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "alias-test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	uuidPart := "88887777-6666-5555-4444-333322221111"
	fullID := "antigravity:local:" + uuidPart
	s := &Session{
		ID:       fullID,
		Agent:    "antigravity",
		Host:     "local",
		NativeID: "native-conv-999",
		OldID:    "old-conv-555",
	}
	if err := db.SaveSession(s); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Lookup by exact ID
	s1, err := db.GetSessionByAlias(fullID)
	if err != nil || s1 == nil || s1.ID != fullID {
		t.Fatalf("lookup by full ID failed: %v, %+v", err, s1)
	}

	// Lookup by NativeID
	s2, err := db.GetSessionByAlias("native-conv-999")
	if err != nil || s2 == nil || s2.ID != fullID {
		t.Fatalf("lookup by NativeID failed: %v, %+v", err, s2)
	}

	// Lookup by OldID
	s3, err := db.GetSessionByAlias("old-conv-555")
	if err != nil || s3 == nil || s3.ID != fullID {
		t.Fatalf("lookup by OldID failed: %v, %+v", err, s3)
	}

	// Lookup by suffix UUID
	s4, err := db.GetSessionByAlias(uuidPart)
	if err != nil || s4 == nil || s4.ID != fullID {
		t.Fatalf("lookup by UUID suffix failed: %v, %+v", err, s4)
	}
}
