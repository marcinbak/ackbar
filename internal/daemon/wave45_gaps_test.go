package daemon

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ackbar/internal/agent"
)

type mockWave45Plugin struct {
	agent.BasePlugin
	agent.BaseProvisioner
	agent.BaseAccounts
	agent.BaseTranscripts
	agent.BaseModelCatalog
	agent.BaseSessionRewinder
	key          string
	txPath       string
	extractCalls atomic.Int32
	appendCalls  atomic.Int32
	cloneResult  bool
	cloneDestID  string
	rewindTurns  []agent.RewindTurn
	cfgModel     string
}

func (p *mockWave45Plugin) Key() string         { return p.key }
func (p *mockWave45Plugin) DisplayName() string { return p.key }
func (p *mockWave45Plugin) Capabilities() agent.Capabilities {
	return agent.Capabilities{SupportsTranscript: true, SupportsModelSelection: true, SupportsMultiAccount: true}
}
func (p *mockWave45Plugin) SkillDirs(homeDir string) []string {
	return []string{filepath.Join(homeDir, ".mockwave", "skills")}
}
func (p *mockWave45Plugin) NormalizeModelID(raw string) string {
	return strings.TrimSuffix(p.BaseModelCatalog.NormalizeModelID(raw), "@latest")
}
func (p *mockWave45Plugin) ResolveArtifactModel(string, string, string, string) (string, time.Time) {
	return "", time.Time{}
}
func (p *mockWave45Plugin) ResolveConfiguredModel(string, *agent.AgentAccount) string {
	return p.cfgModel
}
func (p *mockWave45Plugin) FilterUserEntry(_ map[string]interface{}, content string) (string, bool) {
	c := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(content), "[WRAP]"), "[/WRAP]"))
	return c, c != ""
}
func (p *mockWave45Plugin) StatTranscript(string, string, string) (string, int64, int64, error) {
	st, err := os.Stat(p.txPath)
	if err != nil {
		return "", 0, 0, err
	}
	return p.txPath, st.Size(), st.ModTime().UnixNano(), nil
}
func (p *mockWave45Plugin) ExtractTranscript(_ string, cwd, nativeID string) (*agent.Transcript, error) {
	p.extractCalls.Add(1)
	f, err := os.Open(p.txPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	msgs, err := p.readLines(f, nil)
	return &agent.Transcript{Agent: p.key, NativeID: nativeID, Cwd: cwd, Messages: msgs}, err
}
func (p *mockWave45Plugin) AppendTranscript(r io.Reader, existing []agent.TranscriptMessage) ([]agent.TranscriptMessage, error) {
	p.appendCalls.Add(1)
	saved := make([]agent.TranscriptMessage, len(existing))
	for i, m := range existing {
		saved[i] = m
		saved[i].ToolCalls = append([]string(nil), m.ToolCalls...)
		existing[i].Content = "POISON"
		if len(existing[i].ToolCalls) > 0 {
			existing[i].ToolCalls[0] = "POISON_TOOL"
		}
	}
	return p.readLines(r, saved)
}
func (p *mockWave45Plugin) readLines(r io.Reader, base []agent.TranscriptMessage) ([]agent.TranscriptMessage, error) {
	msgs := append([]agent.TranscriptMessage(nil), base...)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		msgs = append(msgs, agent.TranscriptMessage{Role: "user", Content: line, ToolCalls: []string{"t1"}, Timestamp: time.Now().UTC()})
	}
	return msgs, sc.Err()
}
func (p *mockWave45Plugin) ListRewindTurns(string, string, string) ([]agent.RewindTurn, error) {
	return p.rewindTurns, nil
}
func (p *mockWave45Plugin) CloneTranscriptBeforeTurn(_, _, _ string, _ int, newNativeID string) (bool, error) {
	if !p.cloneResult {
		return false, nil
	}
	p.cloneDestID = newNativeID
	return true, nil
}

func TestWave45Gaps_IncrementalTranscriptAndCustomHome(t *testing.T) {
	tmpDir := t.TempDir()
	customHome := filepath.Join(tmpDir, "srv-home")
	_ = os.MkdirAll(customHome, 0o755)
	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	srv.homeDir = customHome

	txFile := filepath.Join(customHome, "conv.log")
	header := "# " + strings.Repeat("H", 140) + "\n"
	_ = os.WriteFile(txFile, []byte(header+"hello-1\n"), 0o600)
	plug := &mockWave45Plugin{key: "wave-inc", txPath: txFile}
	_ = srv.PluginRegistry().Register(plug)

	t1, err := srv.ExtractTranscript("wave-inc", "n1", tmpDir)
	if err != nil || len(t1.Messages) != 1 || plug.extractCalls.Load() != 1 || plug.appendCalls.Load() != 0 {
		t.Fatalf("initial extract mismatch: %+v err=%v", t1, err)
	}
	t1.Messages[0].Content = "CALLER_MUTATED"
	t1.Messages[0].ToolCalls[0] = "CALLER_TOOL"

	tHit, _ := srv.ExtractTranscript("wave-inc", "n1", tmpDir)
	if tHit.Messages[0].Content != "hello-1" || tHit.Messages[0].ToolCalls[0] != "t1" {
		t.Fatalf("caller mutation leaked into cache: %+v", tHit.Messages[0])
	}

	f, _ := os.OpenFile(txFile, os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString("hello-2\n")
	_ = f.Close()
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(txFile, future, future)

	t2, err := srv.ExtractTranscript("wave-inc", "n1", tmpDir)
	if err != nil || len(t2.Messages) != 2 || t2.Messages[0].Content != "hello-1" || t2.Messages[1].Content != "hello-2" || plug.appendCalls.Load() != 1 || plug.extractCalls.Load() != 1 {
		t.Fatalf("incremental append mismatch: msgs=%+v extract=%d append=%d err=%v", t2.Messages, plug.extractCalls.Load(), plug.appendCalls.Load(), err)
	}

	_ = os.WriteFile(txFile, []byte("# "+strings.Repeat("Z", 140)+"\nhello-1\nhello-2\nhello-3\n"), 0o600)
	future2 := future.Add(2 * time.Second)
	_ = os.Chtimes(txFile, future2, future2)
	if t3, err := srv.ExtractTranscript("wave-inc", "n1", tmpDir); err != nil || len(t3.Messages) != 3 || plug.extractCalls.Load() != 2 {
		t.Fatalf("expected header change to trigger full re-extract: extract=%d err=%v", plug.extractCalls.Load(), err)
	}

	claudeDir := filepath.Join(customHome, ".claude", "projects", encodeClaudeProjectDir(tmpDir))
	_ = os.MkdirAll(claudeDir, 0o755)
	_ = os.WriteFile(filepath.Join(claudeDir, "c-home-1.jsonl"), []byte(`{"type":"user","message":{"role":"user","content":"from-custom-home"}}`+"\n"), 0o600)
	if ct, err := srv.ExtractTranscript("claude-code", "c-home-1", tmpDir); err != nil || ct == nil || len(ct.Messages) != 1 || ct.Messages[0].Content != "from-custom-home" {
		t.Fatalf("expected legacy ExtractTranscript to honor Server.homeDir: ct=%+v err=%v", ct, err)
	}
}

func TestWave45Gaps_RewindModelNormalizeSkillsAndPromptCleaner(t *testing.T) {
	tmpHome := t.TempDir()
	db, _ := InitDB(filepath.Join(tmpHome, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	srv.homeDir = tmpHome

	now := time.Now().UTC().Truncate(time.Second)
	plug := &mockWave45Plugin{
		key: "wave-full", cloneResult: true, cfgModel: "wave-cfg@latest",
		rewindTurns: []agent.RewindTurn{{Turn: 1, Timestamp: now, Preview: "p1", RawPrompt: "first prompt"}, {Turn: 2, Timestamp: now.Add(time.Second), Preview: "p2", RawPrompt: "second prompt"}},
	}
	_ = srv.PluginRegistry().Register(plug)
	sess := &Session{ID: "wave-full:local:r1", Agent: "wave-full", Host: srv.HostName(), NativeID: "r1", Cwd: tmpHome, EngineType: EngineHeadless, Managed: true, State: StateIdle}
	_ = db.SaveSession(sess)

	doRewind := func(method, id, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.Mux().ServeHTTP(w, httptest.NewRequest(method, "/v1/sessions/"+id+"/rewind", bytes.NewBufferString(body)))
		return w
	}
	if wGet := doRewind(http.MethodGet, sess.ID, ""); wGet.Code != http.StatusOK || !strings.Contains(wGet.Body.String(), `"total_turns":2`) {
		t.Fatalf("rewind GET mismatch: %d %s", wGet.Code, wGet.Body.String())
	}
	if wClone := doRewind(http.MethodPost, sess.ID, `{"before_turn":2}`); wClone.Code != http.StatusOK || !strings.Contains(wClone.Body.String(), `"cloned_transcript"`) || plug.cloneDestID == "" {
		t.Fatalf("rewind cloned POST mismatch: %d %s", wClone.Code, wClone.Body.String())
	}
	plug.cloneResult = false
	sess2 := &Session{ID: "wave-full:local:r2", Agent: "wave-full", Host: srv.HostName(), NativeID: "r2", Cwd: tmpHome, EngineType: EngineHeadless, Managed: true, State: StateIdle}
	_ = db.SaveSession(sess2)
	if wSeed := doRewind(http.MethodPost, sess2.ID, `{"before_turn":2}`); wSeed.Code != http.StatusOK || !strings.Contains(wSeed.Body.String(), `"seeded_history"`) {
		t.Fatalf("rewind seeded POST mismatch: %d %s", wSeed.Code, wSeed.Body.String())
	}

	if got := srv.resolveSessionModel(sess); got != "wave-cfg" {
		t.Fatalf("resolveSettingsModel normalized = %q, want wave-cfg", got)
	}
	srv.headless.SetRuntimeModel(sess.ID, "wave-full", "wave-rt@latest")
	if got := srv.resolveSessionModel(sess); got != "wave-rt" {
		t.Fatalf("SetRuntimeModel normalized = %q, want wave-rt", got)
	}

	_ = db.SaveAccount(&AgentAccount{ID: "wave-full:team", Agent: "wave-full", Name: "team", ConfigDir: "~/.wave-profiles/team"})
	repoDir := filepath.Join(tmpHome, "Projects", "my-skills")
	_ = os.MkdirAll(filepath.Join(repoDir, "skills", "demo-skill"), 0o755)
	_ = os.WriteFile(filepath.Join(repoDir, "skills", "demo-skill", "SKILL.md"), []byte("# Demo\n"), 0o644)
	_ = exec.Command("git", "init", repoDir).Run()
	if _, err := SyncSkillsRepoWithRegistry(tmpHome, db, srv.PluginRegistry(), SyncSkillsRepoOptions{RepoPath: repoDir}); err != nil {
		t.Fatalf("SyncSkillsRepoWithRegistry fallback: %v", err)
	}
	for _, want := range []string{filepath.Join(tmpHome, ".mockwave", "skills", "demo-skill", "SKILL.md"), filepath.Join(tmpHome, ".wave-profiles", "team", "skills", "demo-skill", "SKILL.md")} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("missing synced skill %s: %v", want, err)
		}
	}
	envOut := filepath.Join(tmpHome, "skill_dirs.txt")
	_ = os.WriteFile(filepath.Join(repoDir, "install.sh"), []byte("#!/bin/bash\nprintf '%s' \"$ACKBAR_SKILL_DIRS\" > \""+envOut+"\"\n"), 0o755)
	if _, err := SyncSkillsRepoWithRegistry(tmpHome, db, srv.PluginRegistry(), SyncSkillsRepoOptions{RepoPath: repoDir}); err != nil {
		t.Fatalf("SyncSkillsRepoWithRegistry installer: %v", err)
	}
	envBytes, _ := os.ReadFile(envOut)
	if !strings.Contains(string(envBytes), filepath.Join(tmpHome, ".mockwave", "skills")) || !strings.Contains(string(envBytes), filepath.Join(tmpHome, ".wave-profiles", "team", "skills")) {
		t.Fatalf("ACKBAR_SKILL_DIRS missing dirs: %s", string(envBytes))
	}

	spoolPath, metaPath := filepath.Join(tmpHome, "t1.ndjson"), filepath.Join(tmpHome, "t1.meta.json")
	sw, _ := OpenTurnSpoolWriter(spoolPath, "t1")
	_, _ = sw.Append(ChatStreamEvent{SessionID: sess.ID, TurnID: "t1", Type: "turn_start", Text: "[WRAP]clean me[/WRAP]"})
	_ = sw.Close()
	tMeta := &TurnSpoolMeta{TurnID: "t1", SessionID: sess.ID, NativeID: sess.NativeID, Agent: "wave-full", Prompt: "[WRAP]clean me[/WRAP]", SpoolPath: spoolPath, MetaPath: metaPath, Status: "completed", StartedAt: now}
	_ = WriteTurnSpoolMeta(metaPath, tMeta)
	if snap, _ := SynthesizeInFlightTurnWithRegistry(srv.PluginRegistry(), tMeta, "wave-full"); snap == nil || snap.UserPrompt != "clean me" || srv.cleanActivePromptForAgent("wave-full", "[WRAP]hi[/WRAP]") != "hi" {
		t.Fatalf("prompt cleaner mismatch: %+v", snap)
	}
	srv.headless.mu.Lock()
	srv.headless.sessionSpools[sess.ID] = tMeta
	srv.headless.mu.Unlock()
	ch, cleanup := srv.headless.SubscribeWithReplay(sess.ID, "t1", 0, "wave-full")
	defer cleanup()
	if evt := <-ch; evt.Type != "turn_start" || evt.Text != "clean me" {
		t.Fatalf("SubscribeWithReplay turn_start = %+v", evt)
	}
}
