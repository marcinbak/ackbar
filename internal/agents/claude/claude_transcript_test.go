package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscriptCleaningCoalescingUsageRewindAndSubagents(t *testing.T) {
	p := New()
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	_ = os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, ".claude", "plan.md"), []byte("# Plan"), 0o644)

	nativeID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	newID := "ffffffff-1111-4222-8333-444444444444"
	projDir := filepath.Join(home, ".claude", "projects", encodeProjectDir(cwd))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tPath := filepath.Join(projDir, nativeID+".jsonl")
	now := time.Now().UTC().Format(time.RFC3339)

	initial := strings.Join([]string{
		`{"type":"attachment","sessionId":"` + nativeID + `","timestamp":"` + now + `"}`,
		`{"type":"user","sessionId":"` + nativeID + `","timestamp":"` + now + `","message":{"role":"user","content":"<system-reminder>x</system-reminder>First prompt"}}`,
		`{"type":"assistant","sessionId":"` + nativeID + `","timestamp":"` + now + `","message":{"model":"claude-sonnet-4-6[1m]","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":20,"cache_read_input_tokens":30},"output_tokens_details":{"thinking_tokens":15},"content":[{"type":"text","text":"Working"},{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}]}}`,
		`{"type":"user","sessionId":"` + nativeID + `","timestamp":"` + now + `","message":{"role":"user","content":"Second prompt"}}`,
		`{"type":"last-prompt","lastPrompt":"Second prompt"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(tPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if title := p.ResolveSessionTitle(home, cwd, nativeID); title != "First prompt" {
		t.Fatalf("ResolveSessionTitle = %q, want First prompt", title)
	}
	if !p.HasConversationArtifacts(home, nativeID) {
		t.Fatal("expected HasConversationArtifacts true")
	}

	tr, err := p.ExtractTranscript(home, cwd, nativeID)
	if err != nil || len(tr.Messages) != 3 {
		t.Fatalf("ExtractTranscript = %+v err=%v", tr, err)
	}
	appended, err := p.AppendTranscript(strings.NewReader(`{"type":"assistant","timestamp":"`+now+`","message":{"content":"Followup"}}`+"\n"), tr.Messages)
	if err != nil || len(appended) != 4 || appended[3].Content != "Followup" {
		t.Fatalf("AppendTranscript = %+v err=%v", appended, err)
	}

	usage, ok := p.ComputeTranscriptUsage(home, cwd, nativeID, 200000)
	if !ok || usage.TurnCount != 2 || usage.InputTokens != 100 || usage.OutputTokens != 50 || usage.CacheWriteTokens != 20 || usage.CacheReadTokens != 30 || usage.ThinkingTokens != 15 {
		t.Fatalf("ComputeTranscriptUsage = %+v ok=%v", usage, ok)
	}
	if ModelContextLimit("claude-opus-4-6") != 1000000 || ModelContextLimit("claude-3-5-sonnet-20241022") != 200000 {
		t.Fatal("unexpected ModelContextLimit")
	}

	turns, err := p.ListRewindTurns(home, cwd, nativeID)
	if err != nil || len(turns) != 2 {
		t.Fatalf("ListRewindTurns = %+v err=%v", turns, err)
	}
	cloned, err := p.CloneTranscriptBeforeTurn(home, cwd, nativeID, 1, newID)
	if err != nil || !cloned {
		t.Fatalf("CloneTranscriptBeforeTurn(1) = %v err=%v", cloned, err)
	}
	cloned2, err := p.CloneTranscriptBeforeTurn(home, cwd, nativeID, 2, newID)
	if err != nil || !cloned2 {
		t.Fatalf("CloneTranscriptBeforeTurn(2) = %v err=%v", cloned2, err)
	}
	clonedBytes, _ := os.ReadFile(filepath.Join(projDir, newID+".jsonl"))
	if strings.Contains(string(clonedBytes), "Second prompt") || !strings.Contains(string(clonedBytes), newID) {
		t.Fatalf("unexpected cloned transcript:\n%s", clonedBytes)
	}

	subDir := filepath.Join(projDir, nativeID, "subagents")
	_ = os.MkdirAll(subDir, 0o755)
	_ = os.WriteFile(filepath.Join(subDir, "agent-1.meta.json"), []byte(`{"name":"worker","agentType":"coder"}`), 0o644)
	_ = os.WriteFile(filepath.Join(subDir, "agent-1.jsonl"), []byte(`{"type":"assistant","message":{"stop_reason":"end_turn"}}`), 0o644)
	activeSubs, err := p.ListActiveSubagents(home, cwd, nativeID)
	if err != nil || len(activeSubs) != 1 || activeSubs[0].State != "completed" {
		t.Fatalf("ListActiveSubagents = %+v err=%v", activeSubs, err)
	}
	if docs := p.ListWorkspaceDocuments(home, cwd, nativeID); len(docs) != 1 || docs[0].Category != "plan" {
		t.Fatalf("ListWorkspaceDocuments = %+v", docs)
	}

	// Verify CleanSessionFiles removes all session artifacts across .claude so HasConversationArtifacts is false.
	_ = os.MkdirAll(filepath.Join(home, ".claude", "session-env", nativeID), 0o755)
	_ = os.MkdirAll(filepath.Join(home, ".claude", "tasks", nativeID), 0o755)
	_ = os.MkdirAll(filepath.Join(home, ".claude", "file-history", nativeID), 0o755)
	_ = os.MkdirAll(filepath.Join(home, ".claude", "shell-snapshots", nativeID), 0o755)
	_ = os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude", "sessions", "42.json"), []byte(`{"pid":42,"sessionId":"`+nativeID+`"}`), 0o644)
	_ = os.WriteFile(filepath.Join(projDir, "agent-"+nativeID+".jsonl"), []byte("{}"), 0o644)
	if err := p.CleanSessionFiles(home, cwd, nativeID); err != nil {
		t.Fatalf("CleanSessionFiles: %v", err)
	}
	if p.HasConversationArtifacts(home, nativeID) {
		t.Fatal("expected HasConversationArtifacts false after CleanSessionFiles")
	}
	for _, leftover := range []string{
		tPath,
		filepath.Join(projDir, nativeID),
		filepath.Join(projDir, "agent-"+nativeID+".jsonl"),
		filepath.Join(home, ".claude", "session-env", nativeID),
		filepath.Join(home, ".claude", "tasks", nativeID),
		filepath.Join(home, ".claude", "file-history", nativeID),
		filepath.Join(home, ".claude", "shell-snapshots", nativeID),
		filepath.Join(home, ".claude", "sessions", "42.json"),
	} {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Fatalf("CleanSessionFiles left artifact %s (err=%v)", leftover, err)
		}
	}
}

func TestFilterSyntheticEntries(t *testing.T) {
	p := New()
	if clean, keep := p.FilterUserEntry(map[string]interface{}{"isMeta": true}, "hidden"); keep || clean != "" {
		t.Fatalf("isMeta entry should be dropped, got (%q, %v)", clean, keep)
	}
	if clean, keep := p.FilterUserEntry(map[string]interface{}{"origin": map[string]interface{}{"kind": "human"}}, "<task-notification><status>done</status><summary>ok</summary></task-notification>"); !keep || clean == "" {
		t.Fatalf("explicit human entry should survive, got (%q, %v)", clean, keep)
	}
}
