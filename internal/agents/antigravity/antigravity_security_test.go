package antigravity

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
)

func TestNativeIDValidation_S1(t *testing.T) {
	p := New()
	badIDs := []string{"../../../Pictures", "--malicious-flag", "../escape", ""}
	for _, bad := range badIDs {
		if p.IsValidNativeID(bad) {
			t.Fatalf("expected IsValidNativeID(%q) == false", bad)
		}
		if bad != "" {
			if _, err := p.ParseHook("SessionStart", []byte(`{"conversationId":"`+bad+`"}`)); err == nil {
				t.Fatalf("expected ParseHook to reject conversationId %q", bad)
			}
			sp := p.NewStreamParser("s-1")
			for _, line := range []string{
				`{"event":"init","conversation_id":"` + bad + `","model":"gemini-4-carbon-high"}`,
				`{"type":"init","conversation_id":"` + bad + `","model":"gemini-4-carbon-high"}`,
			} {
				evts, _, _ := sp.ParseStreamLine([]byte(line))
				for _, ev := range evts {
					if ev.NativeID == bad {
						t.Fatalf("stream parser leaked invalid NativeID %q from %s", bad, line)
					}
				}
			}
		}
		spec, err := p.BuildTurnSpec(agent.HeadlessTurnInput{
			Session:    &agent.Session{ID: "s-1", NativeID: bad, Cwd: "/tmp"},
			IsExisting: true,
		})
		if err != nil {
			t.Fatalf("BuildTurnSpec error on %q: %v", bad, err)
		}
		if slices.Contains(spec.Args, "--conversation") || (bad != "" && slices.Contains(spec.Args, bad)) {
			t.Fatalf("BuildTurnSpec passed unsafe NativeID %q in args: %v", bad, spec.Args)
		}
	}
	if !p.IsValidNativeID("11111111-2222-3333-4444-555555555555") {
		t.Fatal("expected valid UUID accepted")
	}
}

func TestPathTraversalGuards_S2(t *testing.T) {
	p := New()
	home := t.TempDir()
	evil := "../../../Pictures"

	// Populate target at home/Pictures that traversal from brain/<id> would hit if unguarded.
	picDir := filepath.Join(home, "Pictures")
	_ = os.MkdirAll(filepath.Join(picDir, ".system_generated", "logs"), 0o755)
	_ = os.WriteFile(filepath.Join(picDir, "task.md"), []byte("# Private"), 0o644)
	_ = os.WriteFile(filepath.Join(picDir, ".system_generated", "logs", "transcript.jsonl"), []byte(`{"step_index":0,"type":"USER_INPUT","content":"secret","model":"gemini-4-carbon-high"}`+"\n"), 0o644)

	if docs := p.ListWorkspaceDocuments(home, "", evil); len(docs) != 0 {
		t.Fatalf("ListWorkspaceDocuments traversal returned %+v", docs)
	}
	if m, _ := p.ResolveArtifactModel(home, "", "", evil); m != "" {
		t.Fatalf("ResolveArtifactModel traversal returned %q", m)
	}
	if title := ReadAnnotationTitle(home, evil); title != "" {
		t.Fatalf("ReadAnnotationTitle traversal returned %q", title)
	}
	if title := p.ResolveSessionTitle(home, "", evil); title != "" {
		t.Fatalf("ResolveSessionTitle traversal returned %q", title)
	}
	if meta := p.ReadSessionMetadata(home, "", evil); meta != nil {
		t.Fatalf("ReadSessionMetadata traversal returned %+v", meta)
	}
	if hasUserAnnotation(home, evil) {
		t.Fatal("hasUserAnnotation should reject traversal ID")
	}
	if _, _, _, err := p.StatTranscript(home, "", evil); err == nil {
		t.Fatal("StatTranscript should reject traversal ID")
	}
	if u, ok := p.ComputeTranscriptUsage(home, "", evil, 200000); ok || u.TurnCount != 0 {
		t.Fatalf("ComputeTranscriptUsage should reject traversal ID, got %+v ok=%v", u, ok)
	}
	if _, err := p.ExtractTranscript(home, "", evil); err == nil {
		t.Fatal("ExtractTranscript should reject traversal ID")
	}
	if p.HasConversationArtifacts(home, evil) {
		t.Fatal("HasConversationArtifacts should reject traversal ID")
	}
	_ = p.CleanSessionFiles(home, "", evil)
	if _, err := os.Stat(filepath.Join(picDir, "task.md")); err != nil {
		t.Fatalf("CleanSessionFiles touched decoy parent directory: %v", err)
	}
}

func TestSubagentIDValidation_S3(t *testing.T) {
	p := New()
	home := t.TempDir()
	convID := "44444444-2222-3333-4444-555555555555"
	brainBase := filepath.Join(home, ".gemini", "antigravity", "brain")

	subDir := filepath.Join(brainBase, convID, ".system_generated", "subagents")
	_ = os.MkdirAll(subDir, 0o755)
	_ = os.WriteFile(filepath.Join(subDir, "bad.json"), []byte(`{"conversationId":"../../../Pictures","subagentDescriptor":{"typeName":"r","role":"R"},"state":"RUNNING"}`), 0o644)
	if subs, err := p.ListSubagents(home, "", convID); err != nil || len(subs) != 0 {
		t.Fatalf("expected unsafe disk subID skipped, got %+v err=%v", subs, err)
	}

	_ = os.Remove(filepath.Join(subDir, "bad.json"))
	logDir := filepath.Join(brainBase, convID, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	tx := strings.Join([]string{
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"RUNNING","tool_calls":[{"name":"invoke_subagent","args":{"Subagents":[{"Role":"Sec","TypeName":"sec","Prompt":"check"}]}}]}`,
		`{"step_index":2,"type":"TASK_BOUNDARY","content":"Created the following subagents:\n\"conversationId\": \"../../../Pictures\""}`,
	}, "\n") + "\n"
	_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(tx), 0o644)
	if subs, err := p.ListSubagents(home, "", convID); err != nil || len(subs) != 0 {
		t.Fatalf("expected unsafe transcript subID rejected, got %+v err=%v", subs, err)
	}

	st, _, _ := p.inspectSubagentTranscript(brainBase, "../../../Pictures", time.Now())
	if st != "completed" {
		t.Fatalf("expected unsafe subID transcript inspection completed, got %q", st)
	}
}
