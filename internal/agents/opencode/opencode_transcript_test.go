package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlugin_TitleTranscriptMetadataAndClean(t *testing.T) {
	tmpHome := t.TempDir()
	p := New()

	if title := p.ResolveSessionTitle(tmpHome, "/tmp", "session-123"); title != "" {
		t.Errorf("expected empty title without file, got %q", title)
	}
	sessRoot := filepath.Join(tmpHome, ".opencode", "sessions")
	_ = os.MkdirAll(sessRoot, 0o755)
	logLines := `{"timestamp":"2026-09-28T09:00:00Z","type":"init","version":"0.5.0"}` + "\n" +
		`{"timestamp":"2026-09-28T09:00:01Z","role":"user","content":"Implement dark mode theme"}` + "\n"
	_ = os.WriteFile(filepath.Join(sessRoot, "session-123.jsonl"), []byte(logLines), 0o644)
	if title := p.ResolveSessionTitle(tmpHome, "/tmp", "session-123"); title != "Implement dark mode theme" {
		t.Errorf("ResolveSessionTitle prompt = %q", title)
	}
	_ = os.WriteFile(filepath.Join(sessRoot, "session-123.jsonl"), []byte(logLines+`{"title":"Dark Mode Overhaul"}`+"\n"), 0o644)
	if title := p.ResolveSessionTitle(tmpHome, "/tmp", "session-123"); title != "Dark Mode Overhaul" {
		t.Errorf("ResolveSessionTitle custom = %q", title)
	}

	sessionID := "opencode-sess-999"
	sessDir := filepath.Join(sessRoot, sessionID)
	_ = os.MkdirAll(sessDir, 0o755)
	transcriptLines := []string{
		`{"timestamp":"2026-09-28T09:00:00Z","type":"init","version":"0.5.0"}`,
		`{"timestamp":"2026-09-28T09:00:01Z","role":"user","content":"<environment_context>noise</environment_context>\nImplement auth middleware"}`,
		`{"timestamp":"2026-09-28T09:00:02Z","type":"tool_call","name":"read_file","input":{"path":"internal/auth.go"}}`,
		`{"timestamp":"2026-09-28T09:00:03Z","role":"assistant","content":"I implemented JWT verification in auth middleware."}`,
		`{"timestamp":"2026-09-28T09:00:04Z","type":"usage","rate_limits":{"primary":{"used_percent":55.0}}}`,
	}
	logPath := filepath.Join(sessDir, "transcript.jsonl")
	_ = os.WriteFile(logPath, []byte(strings.Join(transcriptLines, "\n")), 0o644)

	tr, err := p.ExtractTranscript(tmpHome, "/work", sessionID)
	if err != nil || len(tr.Messages) != 2 || tr.Messages[0].Content != "Implement auth middleware" || len(tr.Messages[1].ToolCalls) != 1 {
		t.Fatalf("ExtractTranscript mismatch: %+v err=%v", tr, err)
	}
	meta := p.ReadSessionMetadata(tmpHome, "/work", sessionID)
	if meta == nil || meta.FirstPrompt != "Implement auth middleware" || meta.Version != "0.5.0" || meta.ContextPct != 55 || meta.LastMessageAt.IsZero() || meta.LastUserMessageAt.IsZero() {
		t.Fatalf("ReadSessionMetadata mismatch: %+v", meta)
	}
	if !p.HasConversationArtifacts(tmpHome, sessionID) {
		t.Error("expected HasConversationArtifacts true")
	}
	if statPath, sz, _, err := p.StatTranscript(tmpHome, "/work", sessionID); err != nil || statPath != logPath || sz == 0 {
		t.Fatalf("StatTranscript = (%q, %d, %v)", statPath, sz, err)
	}

	// OpenCode ExtractTranscript returns empty Messages with nil error when valid log has 0 messages.
	emptyID := "opencode-empty-1"
	_ = os.WriteFile(filepath.Join(sessRoot, emptyID+".jsonl"), []byte(`{"type":"init","version":"0.5.0"}`+"\n"), 0o644)
	emptyTr, err := p.ExtractTranscript(tmpHome, "/work", emptyID)
	if err != nil || emptyTr == nil || len(emptyTr.Messages) != 0 {
		t.Fatalf("expected nil error and 0 messages on empty valid log, got %+v err=%v", emptyTr, err)
	}
	if _, err := p.ExtractTranscript(tmpHome, "/work", ""); err == nil {
		t.Error("expected error for empty nativeID")
	}
	if findOpenCodeSessionLog(tmpHome, "../bad") != "" {
		t.Error("expected empty path for unsafe nativeID")
	}

	_ = p.CleanSessionFiles(tmpHome, "/work", "../bad")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("unsafe CleanSessionFiles removed file: %v", err)
	}
	if err := p.CleanSessionFiles(tmpHome, "/work", sessionID); err != nil {
		t.Fatalf("CleanSessionFiles: %v", err)
	}
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Errorf("expected sessDir deleted, err=%v", err)
	}
}
