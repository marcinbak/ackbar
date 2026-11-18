package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ackbar/internal/agent"
)

func TestPlugin_TitleTranscriptMetadataAndClean(t *testing.T) {
	tmpHome := t.TempDir()
	p := New()

	if title := p.ResolveSessionTitle(tmpHome, "/tmp", "session-123"); title != "" {
		t.Errorf("expected empty title without index, got %q", title)
	}
	codexDir := filepath.Join(tmpHome, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	indexLines := `{"id":"session-123","thread_name":"Build responsive fleet UI"}` + "\n" +
		`{"id":"session-123","thread_name":"Build responsive fleet UI - Updated"}` + "\n"
	_ = os.WriteFile(filepath.Join(codexDir, "session_index.jsonl"), []byte(indexLines), 0o644)
	if title := p.ResolveSessionTitle(tmpHome, "/tmp", "session-123"); title != "Build responsive fleet UI - Updated" {
		t.Errorf("ResolveSessionTitle = %q", title)
	}

	sessionID := "019fa2cd-7d2b-7510-8940-e80ae59e2757"
	sessDir := filepath.Join(codexDir, "sessions", "2026", "09", "28")
	_ = os.MkdirAll(sessDir, 0o755)
	logLines := []string{
		`{"timestamp":"2026-09-28T09:00:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","cli_version":"0.150.0"}}`,
		`{"timestamp":"2026-09-28T09:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"System prompt"}]}}`,
		`{"timestamp":"2026-09-28T09:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n/work\n</environment_context>"}]}}`,
		`{"timestamp":"2026-09-28T09:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Refactor the database queries"}]}}`,
		`{"timestamp":"2026-09-28T09:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"git status"}}`,
		`{"timestamp":"2026-09-28T09:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I analyzed git status and refactored the queries."}]}}`,
		`{"timestamp":"2026-09-28T09:00:06Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":50000},"model_context_window":200000},"rate_limits":{"primary":{"used_percent":25.0}}}}`,
	}
	logPath := filepath.Join(sessDir, fmt.Sprintf("rollout-2026-09-28T09-00-00-%s.jsonl", sessionID))
	_ = os.WriteFile(logPath, []byte(strings.Join(logLines, "\n")), 0o644)

	tr, err := p.ExtractTranscript(tmpHome, "/work", sessionID)
	if err != nil || len(tr.Messages) != 2 || tr.Messages[0].Content != "Refactor the database queries" || len(tr.Messages[1].ToolCalls) != 1 {
		t.Fatalf("ExtractTranscript mismatch: %+v err=%v", tr, err)
	}
	meta := p.ReadSessionMetadata(tmpHome, "/work", sessionID)
	if meta == nil || meta.FirstPrompt != "Refactor the database queries" || meta.Version != "0.150.0" || meta.ContextPct != 25 || meta.LastMessageAt.IsZero() || meta.LastUserMessageAt.IsZero() {
		t.Fatalf("ReadSessionMetadata mismatch: %+v", meta)
	}
	if !p.HasConversationArtifacts(tmpHome, sessionID) {
		t.Error("expected HasConversationArtifacts true")
	}
	if statPath, sz, _, err := p.StatTranscript(tmpHome, "/work", sessionID); err != nil || statPath != logPath || sz == 0 {
		t.Fatalf("StatTranscript mismatch: path=%q sz=%d err=%v", statPath, sz, err)
	}

	emptyID := "11111111-1111-1111-1111-111111111111"
	_ = os.WriteFile(filepath.Join(sessDir, fmt.Sprintf("rollout-%s.jsonl", emptyID)), []byte(`{"type":"session_meta","payload":{}}`+"\n"), 0o644)
	if _, err := p.ExtractTranscript(tmpHome, "/work", emptyID); err == nil {
		t.Error("expected ExtractTranscript error when log has zero messages")
	}
	if _, err := p.ExtractTranscript(tmpHome, "/work", ""); err == nil {
		t.Error("expected ExtractTranscript error for empty nativeID")
	}
	if findCodexSessionLog(tmpHome, "../bad") != "" {
		t.Error("expected empty path for unsafe nativeID")
	}

	_ = p.CleanSessionFiles(tmpHome, "/work", "../bad")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("unsafe CleanSessionFiles removed file: %v", err)
	}
	t.Setenv("CODEX_HOME", codexDir)
	if err := p.CleanSessionFiles("", "/work", sessionID); err != nil {
		t.Fatalf("empty homeDir CleanSessionFiles: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("empty homeDir CleanSessionFiles removed file: %v", err)
	}
	if err := p.CleanSessionFiles(tmpHome, "/work", sessionID); err != nil {
		t.Fatalf("CleanSessionFiles: %v", err)
	}
	if p.HasConversationArtifacts(tmpHome, sessionID) {
		t.Error("expected artifacts removed after CleanSessionFiles")
	}

	if p.InspectStatus(context.Background(), &agent.Session{Agent: "codex", State: agent.StateEnded}) {
		t.Error("expected InspectStatus false for ended session")
	}
}
