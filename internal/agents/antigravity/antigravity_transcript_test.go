package antigravity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTitlesMetadataAndWorkspaceDocs(t *testing.T) {
	p := New()
	home := t.TempDir()
	convID := "11111111-2222-3333-4444-555555555555"

	logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Refactor plugin layer"}`+"\n"), 0o644)
	if got := p.ResolveSessionTitle(home, "", convID); got != "Refactor plugin layer" {
		t.Fatalf("tier 4 prompt title = %q", got)
	}

	annoDir := filepath.Join(home, ".gemini", "antigravity", "annotations")
	_ = os.MkdirAll(annoDir, 0o755)
	_ = os.WriteFile(filepath.Join(annoDir, convID+".pbtxt"), []byte(`title: "Annotated Title"`), 0o644)
	meta := p.ReadSessionMetadata(home, "", convID)
	if meta == nil || meta.Title != "Annotated Title" || meta.AITitle != "Annotated Title" {
		t.Fatalf("expected annotation metadata, got %+v", meta)
	}

	_ = os.WriteFile(filepath.Join(home, ".gemini", "antigravity", "brain", convID, "implementation_plan.md"), []byte("# Plan"), 0o644)
	docs := p.ListWorkspaceDocuments(home, "", convID)
	if len(docs) != 1 || docs[0].Category != "plan" {
		t.Fatalf("unexpected workspace docs: %+v", docs)
	}
}

func TestTranscriptExtractStatUsageRewindAndClean(t *testing.T) {
	p := New()
	home := t.TempDir()
	convID := "22222222-2222-3333-4444-555555555555"
	logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0o755)

	compact := strings.Join([]string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"<USER_REQUEST>Build feature</USER_REQUEST>","input_tokens":500,"cache_read_tokens":100}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","content":"short","truncated_fields":["content"],"input_tokens":1000,"cache_read_tokens":200,"output_tokens":50}`,
	}, "\n") + "\n"
	full := `{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","content":"Hydrated full response","tool_calls":[{"name":"generate_image"}]}` + "\n"
	_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(compact), 0o644)
	_ = os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(full), 0o644)

	tr, err := p.ExtractTranscript(home, "", convID)
	if err != nil || len(tr.Messages) != 2 || tr.Messages[0].Content != "Build feature" || tr.Messages[1].Content != "Hydrated full response" {
		t.Fatalf("unexpected hydrated transcript: %+v err=%v", tr, err)
	}
	_, size, _, err := p.StatTranscript(home, "", convID)
	if err != nil || size != int64(len(compact)+len(full)) {
		t.Fatalf("StatTranscript size = %d (want %d) err=%v", size, len(compact)+len(full), err)
	}
	usage, ok := p.ComputeTranscriptUsage(home, "", convID, 200000)
	if !ok || usage.ContextLimitTokens != 1000000 || usage.TurnCount != 1 || usage.OutputTokens != 50 || usage.LastTurnContextTokens != 1000 {
		t.Fatalf("unexpected usage: %+v ok=%v", usage, ok)
	}
	turns, err := p.ListRewindTurns(home, "", convID)
	if err != nil || len(turns) != 1 || turns[0].RawPrompt != "Build feature" {
		t.Fatalf("unexpected rewind turns: %+v err=%v", turns, err)
	}
	if cloned, err := p.CloneTranscriptBeforeTurn(home, "", convID, 1, "new-id"); err != nil || cloned {
		t.Fatalf("expected CloneTranscriptBeforeTurn=(false,nil), got %v %v", cloned, err)
	}
	if err := p.CleanSessionFiles(home, "", convID); err != nil {
		t.Fatalf("CleanSessionFiles failed: %v", err)
	}
	if p.HasConversationArtifacts(home, convID) {
		t.Fatal("expected conversation artifacts removed after CleanSessionFiles")
	}
}

func TestModelsCatalogAndResolution(t *testing.T) {
	p := New()
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	_ = os.MkdirAll(binDir, 0o755)
	_ = os.WriteFile(filepath.Join(binDir, "agy"), []byte("#!/bin/sh\necho 'gemini-4-carbon-high Gemini 4 Carbon (High)'\n"), 0o755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	models := p.CollectModels(home)
	if len(models) != 1 || models[0].ID != "gemini-4-carbon-high" {
		t.Fatalf("expected collected models from agy CLI, got %+v", models)
	}
	if norm := p.NormalizeModelID("Gemini 4 Carbon (High)"); norm != "gemini-4-carbon-high" {
		t.Fatalf("NormalizeModelID = %q", norm)
	}
	if !p.ValidateModelID("gemini-4-carbon-high") || p.ValidateModelID("--bad flag") {
		t.Fatal("unexpected ValidateModelID result")
	}
	settingsDir := filepath.Join(home, ".gemini", "antigravity-cli")
	_ = os.MkdirAll(settingsDir, 0o755)
	_ = os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"model":{"name":"Gemini 4 Argon (High)"}}`), 0o644)
	if got := p.ResolveConfiguredModel(home, nil); got != "gemini-4-argon-high" {
		t.Fatalf("ResolveConfiguredModel = %q", got)
	}
}

func TestTranscriptSubagentFallbackAndHydration(t *testing.T) {
	p := New()
	home := t.TempDir()
	convID := "33333333-2222-3333-4444-555555555555"
	sub1 := "bbbbbbbb-1111-2222-3333-444444444444"
	sub2 := "cccccccc-1111-2222-3333-444444444444"

	logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	compact := strings.Join([]string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Audit auth"}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"RUNNING","truncated_fields":["tool_calls"]}`,
		`{"step_index":2,"type":"SYSTEM","content":"Created the following subagents:\n\"conversationId\": \"` + sub1 + `\"\n\"conversationId\": \"` + sub2 + `\""}`,
		`{"step_index":3,"type":"SYSTEM","content":"Message from sender=` + sub1 + ` completed"}`,
	}, "\n") + "\n"
	full := `{"step_index":1,"type":"PLANNER_RESPONSE","status":"RUNNING","tool_calls":[{"name":"invoke_subagent","args":{"Subagents":[{"Role":"Security","TypeName":"sec","Prompt":"Audit tokens"},{"Role":"Tests","TypeName":"qa","Prompt":"Write tests"}]}}]}` + "\n"
	_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(compact), 0o644)
	_ = os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(full), 0o644)

	subs, err := p.ListSubagents(home, "", convID)
	if err != nil || len(subs) != 1 || subs[0].ID != sub2 || subs[0].Name != "Tests" || subs[0].State != "running" {
		t.Fatalf("expected only active hydrated sub2, got %+v err=%v", subs, err)
	}
	active, err := p.ListActiveSubagents(home, "", convID)
	if err != nil || len(active) != 1 || active[0].ID != sub2 {
		t.Fatalf("unexpected ListActiveSubagents: %+v err=%v", active, err)
	}
}
