package antigravity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ackbar/internal/agent"
)

func TestApplyToolChecklist(t *testing.T) {
	p := New()

	if next, ok := p.ApplyToolChecklist(nil, "run_command", map[string]any{"CommandLine": "ls"}); ok || next != nil {
		t.Fatalf("expected non-checklist tool to return (nil, false), got (%+v, %v)", next, ok)
	}

	nativeInput := map[string]any{
		"steps": []any{
			map[string]any{"id": "s1", "title": "Read code", "status": "done"},
			map[string]any{"title": "Edit files", "active_form": "Editing files", "status": "running"},
			map[string]any{"content": "Verify", "status": "pending"},
			map[string]any{"text": "Optional check", "status": "cancelled"},
			map[string]any{"name": "Deploy", "status": "error"},
			map[string]any{"title": "   ", "status": "done"},
		},
	}
	got, ok := p.ApplyToolChecklist(nil, "default_api:update_steps", nativeInput)
	if !ok || len(got) != 5 {
		t.Fatalf("ApplyToolChecklist(native) = (%+v, %v), want 5 items", got, ok)
	}
	if got[0].ID != "s1" || got[0].Title != "Read code" || got[0].Status != agent.ChecklistDone {
		t.Fatalf("item 0 = %+v", got[0])
	}
	if got[1].Title != "Edit files" || got[1].ActiveForm != "Editing files" || got[1].Status != agent.ChecklistInProgress {
		t.Fatalf("item 1 = %+v", got[1])
	}
	if got[2].Title != "Verify" || got[2].Status != agent.ChecklistPending {
		t.Fatalf("item 2 = %+v", got[2])
	}
	if got[3].Title != "Optional check" || got[3].Status != agent.ChecklistSkipped {
		t.Fatalf("item 3 = %+v", got[3])
	}
	if got[4].Title != "Deploy" || got[4].Status != agent.ChecklistFailed {
		t.Fatalf("item 4 = %+v", got[4])
	}

	jsonStepsInput := map[string]any{
		"steps": `[{"title":"Hydrated step","status":"completed"},{"title":"Next","status":"inprogress"}]`,
	}
	gotJSON, ok := p.ApplyToolChecklist(got, "tools.UPDATE_STEPS", jsonStepsInput)
	if !ok || len(gotJSON) != 2 || gotJSON[0].Status != agent.ChecklistDone || gotJSON[1].Status != agent.ChecklistInProgress {
		t.Fatalf("ApplyToolChecklist(json-steps) = (%+v, %v)", gotJSON, ok)
	}

	cleared, ok := p.ApplyToolChecklist(gotJSON, "antigravity/update_steps", `{"steps":[]}`)
	if !ok || cleared == nil || len(cleared) != 0 {
		t.Fatalf("ApplyToolChecklist(empty steps) = (%#v, %v), want empty non-nil slice", cleared, ok)
	}
}

func TestExtractTranscript_AntigravityChecklistAndHydration(t *testing.T) {
	p := New()
	home := t.TempDir()
	convID := "44444444-2222-3333-4444-555555555555"
	logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	compact := strings.Join([]string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Ship checklists"}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"update_steps","args":{"steps":[{"title":"Step A","status":"in_progress"},{"title":"Step B","status":"pending"}]}}]}`,
		`{"step_index":2,"type":"PLANNER_RESPONSE","status":"DONE","content":"Working on B","truncated_fields":["tool_calls"]}`,
		`{"step_index":3,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Summarize"}`,
		`{"step_index":4,"type":"PLANNER_RESPONSE","status":"DONE","content":"All done."}`,
	}, "\n") + "\n"
	full := `{"step_index":2,"type":"PLANNER_RESPONSE","status":"DONE","content":"Working on B","tool_calls":[{"name":"default_api:update_steps","args":{"steps":"[{\"title\":\"Step A\",\"status\":\"done\"},{\"title\":\"Step B\",\"status\":\"in_progress\"}]"}}]}` + "\n"

	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(compact), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}

	tr, err := p.ExtractTranscript(home, "", convID)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}
	if len(tr.Messages) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(tr.Messages), tr.Messages)
	}
	turn1Asst := tr.Messages[1]
	if turn1Asst.Role != "assistant" || turn1Asst.Content != "Working on B" {
		t.Fatalf("unexpected turn1 assistant: %+v", turn1Asst)
	}
	if len(turn1Asst.Checklist) != 2 || turn1Asst.Checklist[0].Status != agent.ChecklistDone || turn1Asst.Checklist[1].Status != agent.ChecklistInProgress {
		t.Fatalf("turn1 checklist = %+v", turn1Asst.Checklist)
	}
	turn2Asst := tr.Messages[3]
	if turn2Asst.Role != "assistant" || turn2Asst.Content != "All done." || turn2Asst.Checklist != nil {
		t.Fatalf("turn2 assistant should have nil Checklist, got %+v", turn2Asst)
	}
}

func TestExtractTranscript_AntigravityChecklistOnlyPlannerTurn(t *testing.T) {
	p := New()
	home := t.TempDir()
	convID := "55555555-2222-3333-4444-555555555555"
	logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	lines := strings.Join([]string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"Start"}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"update_steps","args":{"steps":[{"title":"Only step","status":"pending"}]}}]}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	tr, err := p.ExtractTranscript(home, "", convID)
	if err != nil {
		t.Fatalf("ExtractTranscript failed: %v", err)
	}
	if len(tr.Messages) != 2 || len(tr.Messages[1].Checklist) != 1 || tr.Messages[1].Checklist[0].Title != "Only step" {
		t.Fatalf("expected checklist-only assistant turn, got %+v", tr.Messages)
	}
}

func TestStreamParser_AntigravityChecklistEvents(t *testing.T) {
	sp := New().NewStreamParser("agy-sess-1")

	evts1, handled1, err := sp.ParseStreamLine([]byte(`{"event":"step_update","step_update":{"step_index":1,"step_type":"tool","state":"ACTIVE","tool_name":"default_api:update_steps","tool_info":{"parameters":{"steps":[{"title":"Plan","status":"in_progress"},{"title":"Code","status":"pending"}]}}}}`))
	if err != nil || !handled1 || len(evts1) != 1 {
		t.Fatalf("step 1 = (%+v, %v, %v)", evts1, handled1, err)
	}
	if evts1[0].Type != "tool_start" || len(evts1[0].Checklist) != 2 || evts1[0].Checklist[0].Status != agent.ChecklistInProgress {
		t.Fatalf("unexpected tool_start 1: %+v", evts1[0])
	}

	evtsNonChecklist, _, _ := sp.ParseStreamLine([]byte(`{"event":"step_update","step_update":{"step_index":2,"step_type":"tool","state":"ACTIVE","tool_name":"run_command","tool_info":{"parameters":{"CommandLine":"go test"}}}}`))
	if len(evtsNonChecklist) != 1 || evtsNonChecklist[0].Checklist != nil {
		t.Fatalf("non-checklist tool should have nil Checklist, got %+v", evtsNonChecklist)
	}

	evts2, handled2, err := sp.ParseStreamLine([]byte(`{"event":"step_update","step_update":{"step_index":3,"step_type":"tool","state":"ACTIVE","tool_name":"update_steps","tool_info":{"arguments":{"steps":[{"title":"Plan","status":"done"},{"title":"Code","status":"done"}]}}}}`))
	if err != nil || !handled2 || len(evts2) != 1 {
		t.Fatalf("step 2 = (%+v, %v, %v)", evts2, handled2, err)
	}
	if len(evts2[0].Checklist) != 2 || evts2[0].Checklist[0].Status != agent.ChecklistDone || evts2[0].Checklist[1].Status != agent.ChecklistDone {
		t.Fatalf("unexpected tool_start 2: %+v", evts2[0])
	}

	evts3, _, _ := sp.ParseStreamLine([]byte(`{"event":"step_update","step_update":{"step_index":4,"step_type":"tool","state":"ACTIVE","tool_name":"update_steps","tool_info":{"parameters":{"steps":[]}}}}`))
	if len(evts3) != 1 || evts3[0].Checklist != nil || !evts3[0].ChecklistCleared {
		t.Fatalf("expected ChecklistCleared=true and nil Checklist on empty steps, got %+v", evts3)
	}
}

func TestAntigravityChecklistBounds(t *testing.T) {
	p := New()
	rawSteps := make([]any, 0, agent.MaxChecklistItems+25)
	for i := 0; i < agent.MaxChecklistItems+25; i++ {
		rawSteps = append(rawSteps, map[string]any{
			"id":     strings.Repeat("k", 90),
			"title":  strings.Repeat("x", agent.MaxChecklistTitleRunes+40),
			"status": "pending",
		})
	}
	got, ok := p.ApplyToolChecklist(nil, "update_steps", map[string]any{"steps": rawSteps})
	if !ok || len(got) != agent.MaxChecklistItems {
		t.Fatalf("expected capped %d items, got (%d, %v)", agent.MaxChecklistItems, len(got), ok)
	}
	if len(got[0].ID) != agent.MaxChecklistIDBytes || len([]rune(got[0].Title)) != agent.MaxChecklistTitleRunes {
		t.Fatalf("expected clamped ID/title, got id=%d title=%d", len(got[0].ID), len([]rune(got[0].Title)))
	}
}
