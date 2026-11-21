package claude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ackbar/internal/agent"
)

func TestSecurity_RejectsPathTraversalAndUnsafeNativeIDs(t *testing.T) {
	p := New()
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.jsonl")
	_ = os.WriteFile(outside, []byte(`{"type":"user","message":{"content":"secret"}}`), 0o600)

	projDir := filepath.Join(home, ".claude", "projects", "-tmp")
	_ = os.MkdirAll(projDir, 0o755)
	symlinkID := "22222222-3333-4444-8555-666666666666"
	_ = os.Symlink(outside, filepath.Join(projDir, symlinkID+".jsonl"))

	for _, bad := range []string{"", "../evil", "foo/bar", "test-non-uuid", "11111111-2222-4333-8444-55555555555*", symlinkID} {
		if got := FindTranscriptPath(home, "/tmp", bad); got != "" {
			t.Fatalf("FindTranscriptPath(%q) leaked %q", bad, got)
		}
		if p.HasConversationArtifacts(home, bad) {
			t.Fatalf("HasConversationArtifacts(%q) should be false", bad)
		}
		if m, _ := p.ResolveArtifactModel(home, "", "/tmp", bad); m != "" {
			t.Fatalf("ResolveArtifactModel(%q) should return empty, got %q", bad, m)
		}
		if _, err := p.CloneTranscriptBeforeTurn(home, "/tmp", bad, 1, testUUID); err == nil {
			t.Fatalf("CloneTranscriptBeforeTurn(%q) should fail", bad)
		}
		if meta := p.ReadSessionMetadata(home, "/tmp", bad); meta.FirstPrompt != "" {
			t.Fatalf("ReadSessionMetadata(%q) leaked FirstPrompt %q", bad, meta.FirstPrompt)
		}
		if subs, err := p.ListActiveSubagents(home, "/tmp", bad); err != nil || len(subs) != 0 {
			t.Fatalf("ListActiveSubagents(%q) leaked %+v err=%v", bad, subs, err)
		}
		if bad != symlinkID {
			if spec, err := p.BuildTurnSpec(agent.HeadlessTurnInput{HomeDir: home, Session: &agent.Session{NativeID: bad, Cwd: "/tmp"}}); err == nil {
				t.Fatalf("BuildTurnSpec(%q) should fail, got %+v", bad, spec)
			}
			if ev, err := p.ParseHook("Stop", []byte(`{"session_id":"`+bad+`"}`)); err == nil && ev != nil {
				t.Fatalf("ParseHook(%q) should ignore non-UUID session_id", bad)
			}
		}
		if p.ValidateModelID("-" + bad) {
			t.Fatalf("ValidateModelID allowed leading dash")
		}
	}
	spec, err := p.BuildTurnSpec(agent.HeadlessTurnInput{HomeDir: home, Session: &agent.Session{NativeID: symlinkID, Cwd: "/tmp"}})
	if err != nil || slices.Contains(spec.Args, "--resume") {
		t.Fatalf("symlink transcript must not trigger --resume: %+v err=%v", spec, err)
	}
	if _, err := p.CloneTranscriptBeforeTurn(home, "/tmp", testUUID, 1, "../evil"); err == nil {
		t.Fatal("CloneTranscriptBeforeTurn allowed unsafe target newNativeID")
	}
}
