package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlugin_ListSubagentsAndParentResolution(t *testing.T) {
	p := New()
	if subs, err := p.ListSubagents("", "", ""); err != nil || len(subs) != 0 {
		t.Fatalf("expected empty for empty ID, got %v err=%v", subs, err)
	}
	if subs, err := p.ListSubagents("", "", "../bad/path"); err != nil || len(subs) != 0 {
		t.Fatalf("expected empty for unsafe ID, got %v err=%v", subs, err)
	}

	tmpHome := t.TempDir()
	sessID := "test-grok-session-123"
	subDir := filepath.Join(tmpHome, ".grok", "sessions", sessID, "subagents", "subagent-1")
	_ = os.MkdirAll(subDir, 0o755)
	metaBytes, _ := json.Marshal(map[string]interface{}{
		"session_id": "subagent-1", "role": "reviewer", "prompt": "review",
		"created_at": time.Now().UTC().Format(time.RFC3339), "status": "running",
	})
	_ = os.WriteFile(filepath.Join(subDir, "meta.json"), metaBytes, 0o644)

	subs, err := p.ListSubagents(tmpHome, "", sessID)
	if err != nil || len(subs) != 1 || subs[0].State != "running" {
		t.Fatalf("expected 1 running subagent, got %+v err=%v", subs, err)
	}

	childDir := filepath.Join(tmpHome, ".grok", "sessions", "subagent-1")
	_ = os.MkdirAll(childDir, 0o755)
	_ = os.WriteFile(filepath.Join(childDir, "updates.jsonl"), []byte(`{"params":{"update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn"}}}`+"\n"), 0o644)
	subsAfter, err := p.ListSubagents(tmpHome, "", sessID)
	if err != nil || len(subsAfter) != 1 || subsAfter[0].State != "completed" {
		t.Fatalf("expected completed subagent, got %+v err=%v", subsAfter, err)
	}

	childID := "child-grok-123"
	cDir := filepath.Join(tmpHome, ".grok", "sessions", childID)
	_ = os.MkdirAll(cDir, 0o755)
	_ = os.WriteFile(filepath.Join(cDir, "summary.json"), []byte(`{"parent_session_id":"child-grok-123"}`), 0o644)
	if pid, _, _ := p.ResolveSubagentParent(tmpHome, childID); pid != "" {
		t.Errorf("self-parent should return empty, got %q", pid)
	}
	_ = os.WriteFile(filepath.Join(cDir, "summary.json"), []byte(`{"parent_session_id":"../unsafe"}`), 0o644)
	if pid, _, _ := p.ResolveSubagentParent(tmpHome, childID); pid != "" {
		t.Errorf("unsafe parent should return empty, got %q", pid)
	}
	_ = os.WriteFile(filepath.Join(cDir, "summary.json"), []byte(`{"parent_session_id":"parent-root-456","role":"subagent"}`), 0o644)
	if pid, role, _ := p.ResolveSubagentParent(tmpHome, childID); pid != "parent-root-456" || role != "subagent" {
		t.Errorf("expected parent-root-456 / subagent, got %q / %q", pid, role)
	}
	if !p.IsSubagentSession(tmpHome, childID, nil) {
		t.Errorf("expected IsSubagentSession true")
	}
}

func TestPlugin_ResolveSessionTitleAndLegacyTranscript(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	p := New()

	if title := p.ResolveSessionTitle("/tmp", "session-123"); title != "" {
		t.Errorf("expected empty title without index, got %q", title)
	}
	grokDir := filepath.Join(tmpHome, ".grok")
	_ = os.MkdirAll(grokDir, 0o755)
	indexLines := `{"id":"session-123","title":"Add search filters"}` + "\n" +
		`{"id":"session-123","title":"Add search filters - Updated"}` + "\n"
	_ = os.WriteFile(filepath.Join(grokDir, "session_index.jsonl"), []byte(indexLines), 0o644)
	if got := p.ResolveSessionTitle("/tmp", "session-123"); got != "Add search filters - Updated" {
		t.Errorf("ResolveSessionTitle = %q", got)
	}

	sessionID := "grok-sess-999"
	sessDir := filepath.Join(grokDir, "sessions")
	_ = os.MkdirAll(sessDir, 0o755)
	logLines := []string{
		`{"timestamp":"2026-09-28T09:00:00Z","type":"init","version":"1.2.0"}`,
		`{"timestamp":"2026-09-28T09:00:01Z","role":"user","content":"Optimize database performance"}`,
		`{"timestamp":"2026-09-28T09:00:02Z","type":"tool_call","name":"explain","input":{"query":"SELECT * FROM users"}}`,
		`{"timestamp":"2026-09-28T09:00:03Z","role":"assistant","content":"I analyzed the query plan and added indexes."}`,
		`{"timestamp":"2026-09-28T09:00:04Z","type":"usage","rate_limits":{"primary":{"used_percent":40.0}}}`,
	}
	_ = os.WriteFile(filepath.Join(sessDir, sessionID+".jsonl"), []byte(strings.Join(logLines, "\n")), 0o644)

	tr, err := p.ExtractTranscript(tmpHome, "/work", sessionID)
	if err != nil || len(tr.Messages) != 2 || len(tr.Messages[1].ToolCalls) != 1 {
		t.Fatalf("legacy transcript mismatch: %+v err=%v", tr, err)
	}
	meta := p.ReadSessionMetadata("/work", sessionID)
	if meta == nil || meta.Version != "1.2.0" || meta.ContextPct != 40 || meta.FirstPrompt != "Optimize database performance" {
		t.Fatalf("legacy meta mismatch: %+v", meta)
	}
}

func TestPlugin_1077TranscriptACPAndStat(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	p := New()
	cwd := "/work/project"
	nativeID := "grok-sess-v1077"
	sessDir := filepath.Join(tmpHome, ".grok", "sessions", EncodeCwd(cwd), nativeID)
	_ = os.MkdirAll(sessDir, 0o755)

	_ = os.WriteFile(filepath.Join(sessDir, "summary.json"), []byte(`{"custom_title":"Fix memory leak","last_turn_summary":"Closed channels","head_branch":"feat/fix-leak","updated_at":"2026-11-16T15:04:05Z"}`), 0o644)
	_ = os.WriteFile(filepath.Join(sessDir, "signals.json"), []byte(`{"contextTokensUsed":45000,"contextWindowTokens":100000}`), 0o644)
	chatLines := []string{
		`{"type":"system","content":"You are Grok."}`,
		`{"type":"user","synthetic_reason":"init","content":"system context"}`,
		`{"type":"user","content":"<user_query>Investigate high memory usage</user_query>","timestamp":"2026-11-16T15:00:00Z"}`,
		`{"type":"reasoning","summary":[{"type":"summary_text","text":"Analyzing heap dump..."}]}`,
		`{"type":"tool_call","name":"bash","arguments":{"command":"go tool pprof"}}`,
		`{"type":"assistant","content":"Found unclosed goroutines in event loop."}`,
	}
	chatPath := filepath.Join(sessDir, "chat_history.jsonl")
	_ = os.WriteFile(chatPath, []byte(strings.Join(chatLines, "\n")), 0o644)

	tr, err := p.ExtractTranscript(tmpHome, cwd, nativeID)
	if err != nil || len(tr.Messages) != 2 || tr.Messages[0].Content != "Investigate high memory usage" || tr.Messages[1].Thinking != "Analyzing heap dump..." {
		t.Fatalf("1077 transcript mismatch: %+v err=%v", tr, err)
	}
	meta := p.ReadSessionMetadata(cwd, nativeID)
	if meta == nil || meta.Title != "Fix memory leak" || meta.GitBranch != "feat/fix-leak" || meta.ContextPct != 45 {
		t.Fatalf("1077 meta mismatch: %+v", meta)
	}

	updPath := filepath.Join(sessDir, "updates.jsonl")
	_ = os.WriteFile(updPath, []byte(`{"type":"noop"}`), 0o644)
	path, size, _, sErr := p.StatTranscript(tmpHome, cwd, nativeID)
	if sErr != nil || path != chatPath || size <= int64(len(`{"type":"noop"}`)) {
		t.Fatalf("StatTranscript mismatch: path=%q size=%d err=%v", path, size, sErr)
	}

	acpID := "grok-sess-acp"
	acpDir := filepath.Join(tmpHome, ".grok", "sessions", EncodeCwd(cwd), acpID)
	_ = os.MkdirAll(acpDir, 0o700)
	acpLines := []string{
		`{"timestamp":1794835355,"method":"session/update","params":{"sessionId":"grok-sess-acp","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"Fix "}},"_meta":{"agentTimestampMs":1794835355100}}}`,
		`{"timestamp":1794835355,"method":"session/update","params":{"sessionId":"grok-sess-acp","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"streaming tests"}},"_meta":{"agentTimestampMs":1794835355200}}}`,
		`{"timestamp":1794835356,"method":"session/update","params":{"sessionId":"grok-sess-acp","update":{"sessionUpdate":"agent_thought_chunk","thought":"Analyzing... "},"_meta":{"agentTimestampMs":1794835356000}}}`,
		`{"timestamp":1794835357,"method":"session/update","params":{"sessionId":"grok-sess-acp","update":{"sessionUpdate":"tool_call","title":"bash","rawInput":{"command":"go test ./..."}},"_meta":{"agentTimestampMs":1794835357000}}}`,
		`{"timestamp":1794835358,"method":"session/update","params":{"sessionId":"grok-sess-acp","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"All tests passed."}},"_meta":{"agentTimestampMs":1794835358000}}}`,
		`{"timestamp":1794835359,"method":"session/update","params":{"sessionId":"grok-sess-acp","update":{"sessionUpdate":"turn_completed"},"_meta":{"agentTimestampMs":1794835359000}}}`,
	}
	_ = os.WriteFile(filepath.Join(acpDir, "updates.jsonl"), []byte(strings.Join(acpLines, "\n")), 0o600)

	acpTr, err := p.ExtractTranscript(tmpHome, cwd, acpID)
	if err != nil || len(acpTr.Messages) != 2 || acpTr.Messages[0].Content != "Fix streaming tests" || acpTr.Messages[1].Content != "All tests passed." {
		t.Fatalf("ACP transcript mismatch: %+v err=%v", acpTr, err)
	}
	if !acpTr.Messages[0].Timestamp.Equal(time.UnixMilli(1794835355100)) {
		t.Errorf("ACP user timestamp mismatch: %v", acpTr.Messages[0].Timestamp)
	}
	if clean, keep := p.FilterUserEntry(nil, "<environment_context>x</environment_context>\nhi"); !keep || clean != "hi" {
		t.Errorf("FilterUserEntry mismatch: keep=%v clean=%q", keep, clean)
	}
}
