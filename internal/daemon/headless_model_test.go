package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agents/antigravity"
)

func TestNormalizeModelIdentifier(t *testing.T) {
	cases := []struct {
		agent    string
		raw      string
		expected string
	}{
		{"claude-code", "", ""},
		{"claude-code", "   ", ""},
		{"claude-code", "default", ""},
		{"claude-code", "DEFAULT", ""},
		{"claude-code", "synthetic", ""},
		{"claude-code", "<synthetic>", ""},
		{"claude-code", "<local-command>", ""},
		{"claude-code", "claude-fable-5-1[1m]", "claude-fable-5-1"},
		{"claude-code", "claude-opus-5-5[200k]", "claude-opus-5-5"},
		{"claude-code", "opus", "opus"},
		{"claude-code", "sonnet", "sonnet"},
		{"claude-code", "haiku", "haiku"},
		{"claude-code", "opusplan", "opusplan"},
		{"antigravity", "Gemini 4 Carbon (Medium)", "gemini-4-carbon-medium"},
		{"antigravity", "gemini-4-carbon-medium", "gemini-4-carbon-medium"},
		{"antigravity", "GEMINI 4 HYDROGEN (XHIGH)", "gemini-4-hydrogen-xhigh"},
		{"antigravity", "Orchestrator (Carbon)", "gemini-pro-orchestrator"},
		{"antigravity", "custom-experimental-model", "custom-experimental-model"},
		{"other", "gpt-4o", "gpt-4o"},
	}

	reg := agent.NewRegistry()
	_ = reg.Register(antigravity.New())

	for _, tc := range cases {
		got := normalizeModelIdentifierWithRegistry(reg, tc.agent, tc.raw)
		if got != tc.expected {
			t.Errorf("normalizeModelIdentifierWithRegistry(%q, %q) = %q; want %q", tc.agent, tc.raw, got, tc.expected)
		}
	}
}

func TestResolveSessionModel_ExplicitOverride(t *testing.T) {
	srv, _ := setupTestServer(t)

	sess := &Session{
		ID:         "claude:local:sess1",
		Agent:      "claude-code",
		Host:       "local",
		EngineType: EngineHeadless,
		Model:      "claude-fable-5-1[1m]",
	}

	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "claude-fable-5-1" {
		t.Fatalf("expected resolved model 'claude-fable-5-1', got %q", sess.ResolvedModel)
	}

	sess.Model = "default"
	srv.populateResolvedModel(sess)
	if sess.ResolvedModel == "default" {
		t.Fatalf("expected 'default' override to fall through, got %q", sess.ResolvedModel)
	}
}

func TestResolveSessionModel_RuntimeModel(t *testing.T) {
	srv, _ := setupTestServer(t)

	sess := &Session{
		ID:         "claude:local:rt-sess",
		Agent:      "claude-code",
		Host:       "local",
		EngineType: EngineHeadless,
	}

	srv.headless.SetRuntimeModel(sess.ID, sess.Agent, "claude-opus-5-5[1m]")
	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "claude-opus-5-5" {
		t.Fatalf("expected runtime model 'claude-opus-5-5', got %q", sess.ResolvedModel)
	}

	srv.headless.NotifyConfigChanged(sess)
	srv.populateResolvedModel(sess)
	if sess.ResolvedModel == "claude-opus-5-5" {
		t.Fatalf("expected runtime model to be cleared after NotifyConfigChanged, got %q", sess.ResolvedModel)
	}
}

func TestResolveSessionModel_ClaudeTranscriptTail(t *testing.T) {
	srv, _ := setupTestServer(t)
	tmpHome := t.TempDir()
	srv.homeDir = tmpHome

	testCwd := "/workspace/my-app"
	nativeID := "claude-uuid-transcript"
	encodedCwd := encodeClaudeProjectDir(testCwd)
	projDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	transcriptPath := filepath.Join(projDir, nativeID+".jsonl")
	content := `{"type":"assistant","message":{"model":"<synthetic>","content":"skip me"}}` + "\n" +
		`{"type":"assistant","message":{"model":"claude-fable-5-1[1m]","content":"final answer"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	sess := &Session{
		ID:         "claude:local:" + nativeID,
		NativeID:   nativeID,
		Agent:      "claude-code",
		Host:       "local",
		Cwd:        testCwd,
		EngineType: EngineHeadless,
	}

	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "claude-fable-5-1" {
		t.Fatalf("expected resolved model 'claude-fable-5-1', got %q", sess.ResolvedModel)
	}
}

func TestResolveSessionModel_AntigravityTranscriptTail(t *testing.T) {
	srv, _ := setupTestServer(t)
	tmpHome := t.TempDir()
	srv.homeDir = tmpHome

	nativeID := "agy-uuid-transcript"
	logsDir := filepath.Join(tmpHome, ".gemini", "antigravity-cli", "brain", nativeID, ".system_generated", "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatal(err)
	}

	transcriptPath := filepath.Join(logsDir, "transcript.jsonl")
	content := `{"step_index":1,"type":"USER_INPUT","content":"Fix bug"}` + "\n" +
		`{"step_index":2,"type":"PLANNER_RESPONSE","model":"Gemini 4 Carbon (Medium)","content":"working"}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	sess := &Session{
		ID:         "antigravity:local:" + nativeID,
		NativeID:   nativeID,
		Agent:      "antigravity",
		Host:       "local",
		EngineType: EngineHeadless,
	}

	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "gemini-4-carbon-medium" {
		t.Fatalf("expected canonical model 'gemini-4-carbon-medium', got %q", sess.ResolvedModel)
	}
}

func TestResolveSessionModel_PostResetGuard(t *testing.T) {
	srv, _ := setupTestServer(t)
	tmpHome := t.TempDir()
	srv.homeDir = tmpHome

	testCwd := "/workspace/post-reset"
	nativeID := "claude-uuid-post-reset"
	encodedCwd := encodeClaudeProjectDir(testCwd)
	projDir := filepath.Join(tmpHome, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	transcriptPath := filepath.Join(projDir, nativeID+".jsonl")
	content := `{"type":"assistant","message":{"model":"claude-opus-5-5","content":"earlier turn"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	homeSettingsDir := filepath.Join(tmpHome, ".claude")
	_ = os.WriteFile(filepath.Join(homeSettingsDir, "settings.json"), []byte(`{"model":"claude-fable-5-1"}`), 0644)

	sess := &Session{
		ID:                "claude:local:" + nativeID,
		NativeID:          nativeID,
		Agent:             "claude-code",
		Host:              "local",
		Cwd:               testCwd,
		EngineType:        EngineHeadless,
		MetadataUpdatedAt: time.Now().Add(5 * time.Second),
	}

	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "claude-fable-5-1" {
		t.Fatalf("expected post-reset guard to prefer settings.json 'claude-fable-5-1', got %q", sess.ResolvedModel)
	}
}

func TestResolveSessionModel_ClaudeSettingsPrecedence(t *testing.T) {
	srv, _ := setupTestServer(t)
	tmpHome := t.TempDir()
	srv.homeDir = tmpHome
	cwd := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(filepath.Join(cwd, ".claude"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpHome, ".claude"), 0755)

	sess := &Session{
		ID:         "claude:local:settings-test",
		Agent:      "claude-code",
		Host:       "local",
		Cwd:        cwd,
		EngineType: EngineHeadless,
	}

	_ = os.WriteFile(filepath.Join(tmpHome, ".claude", "settings.json"), []byte(`{"model":"claude-haiku-4-5"}`), 0644)
	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "claude-haiku-4-5" {
		t.Fatalf("expected fallback to home settings 'claude-haiku-4-5', got %q", sess.ResolvedModel)
	}

	_ = os.WriteFile(filepath.Join(cwd, ".claude", "settings.json"), []byte(`{"model":"claude-sonnet-5-5"}`), 0644)
	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "claude-sonnet-5-5" {
		t.Fatalf("expected project settings 'claude-sonnet-5-5', got %q", sess.ResolvedModel)
	}

	_ = os.WriteFile(filepath.Join(cwd, ".claude", "settings.local.json"), []byte(`{"model":"claude-fable-5-1"}`), 0644)
	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "claude-fable-5-1" {
		t.Fatalf("expected local settings 'claude-fable-5-1', got %q", sess.ResolvedModel)
	}
}

func TestResolveSessionModel_RemoteSessionPreserved(t *testing.T) {
	srv, _ := setupTestServer(t)

	sess := &Session{
		ID:            "claude:remote-host:sess1",
		Agent:         "claude-code",
		Host:          "remote-host",
		EngineType:    EngineHeadless,
		ResolvedModel: "remote-injected-model",
	}

	srv.populateResolvedModel(sess)
	if sess.ResolvedModel != "remote-injected-model" {
		t.Fatalf("expected remote session to keep its resolved model, got %q", sess.ResolvedModel)
	}
}

func TestConcurrentBroadcastAndListSessionsRace(t *testing.T) {
	srv, db := setupTestServer(t)
	sess := &Session{
		ID:         "claude:local:race-sess",
		Agent:      "claude-code",
		Host:       "local",
		EngineType: EngineHeadless,
		Model:      "claude-fable-5-1",
	}
	_ = db.SaveSession(sess)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			sCopy := *sess
			if idx%2 == 0 {
				sCopy.Model = fmt.Sprintf("claude-opus-5-5-%d", idx)
			} else {
				sCopy.Model = ""
			}
			srv.broadcast(&sCopy)
		}(i)

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
			rec := httptest.NewRecorder()
			srv.handleSessions(rec, req)
			var list []*Session
			_ = json.Unmarshal(rec.Body.Bytes(), &list)
		}()
	}
	wg.Wait()
}

func TestResolveSessionModel_NonRegularFileAndFIFO(t *testing.T) {
	srv, _ := setupTestServer(t)
	srv.homeDir = t.TempDir()

	// Test 1: directory at .claude/settings.json
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	_ = os.MkdirAll(filepath.Join(claudeDir, "settings.json"), 0755)

	sess := &Session{
		ID:         "claude:local:dir-sess",
		Agent:      "claude-code",
		Host:       "local",
		EngineType: EngineHeadless,
		Cwd:        tmpDir,
	}

	done := make(chan string, 1)
	go func() {
		done <- srv.resolveSessionModel(sess)
	}()

	select {
	case res := <-done:
		if res != "" {
			t.Fatalf("expected empty model for directory settings, got %q", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resolveSessionModel hung on directory settings file")
	}

	// Test 2: FIFO / named pipe at .claude/settings.local.json with fallback to settings.json
	fifoDir := t.TempDir()
	fifoClaudeDir := filepath.Join(fifoDir, ".claude")
	_ = os.MkdirAll(fifoClaudeDir, 0755)
	fifoPath := filepath.Join(fifoClaudeDir, "settings.local.json")
	_ = os.WriteFile(filepath.Join(fifoClaudeDir, "settings.json"), []byte(`{"model":"claude-sonnet-4-6"}`), 0644)

	if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
		t.Skipf("skipping FIFO test: mkfifo not supported: %v", err)
	}

	fifoSess := &Session{
		ID:         "claude:local:fifo-sess",
		Agent:      "claude-code",
		Host:       "local",
		EngineType: EngineHeadless,
		Cwd:        fifoDir,
	}

	fifoDone := make(chan string, 1)
	go func() {
		fifoDone <- srv.resolveSessionModel(fifoSess)
	}()

	select {
	case res := <-fifoDone:
		if res != "claude-sonnet-4-6" {
			t.Fatalf("expected fallback to settings.json 'claude-sonnet-4-6' when local is FIFO, got %q", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resolveSessionModel hung on FIFO settings file")
	}
}
