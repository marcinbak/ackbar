package claude

import (
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
)

func TestApplyToolChecklist_TodoWrite(t *testing.T) {
	p := New()

	if next, ok := p.ApplyToolChecklist(nil, "Bash", map[string]any{"command": "ls"}); ok || next != nil {
		t.Fatalf("expected non-checklist tool to return (nil, false), got (%+v, %v)", next, ok)
	}

	got, ok := p.ApplyToolChecklist(nil, "TodoWrite", map[string]any{
		"todos": []any{
			map[string]any{"id": "1", "content": "Read files", "status": "completed"},
			map[string]any{"id": "2", "content": "Write code", "activeForm": "Writing code", "status": "in_progress"},
			map[string]any{"content": "Run tests", "status": "pending"},
			map[string]any{"content": "Optional", "status": "cancelled"},
			map[string]any{"content": "Lint", "status": "failed"},
		},
	})
	if !ok || len(got) != 5 {
		t.Fatalf("TodoWrite = (%+v, %v), want 5 items", got, ok)
	}
	if got[0].Status != agent.ChecklistDone || got[0].Title != "Read files" {
		t.Fatalf("item 0 = %+v", got[0])
	}
	if got[1].Status != agent.ChecklistInProgress || got[1].ActiveForm != "Writing code" {
		t.Fatalf("item 1 = %+v", got[1])
	}
	if got[2].Status != agent.ChecklistPending || got[3].Status != agent.ChecklistSkipped || got[4].Status != agent.ChecklistFailed {
		t.Fatalf("statuses mismatch: %+v", got)
	}

	replaced, ok := p.ApplyToolChecklist(got, "mcp__srv__todo_write", map[string]any{
		"todos": []any{
			map[string]any{"content": "Final item", "status": "completed"},
		},
	})
	if !ok || len(replaced) != 1 || replaced[0].Title != "Final item" || replaced[0].Status != agent.ChecklistDone {
		t.Fatalf("TodoWrite replacement = (%+v, %v)", replaced, ok)
	}
}

func TestParseJSONL_TaskCreateUpdateDeleteMonotonicAndAppendTranscript(t *testing.T) {
	p := New()
	now := time.Now().UTC().Format(time.RFC3339)

	jsonl := strings.Join([]string{
		`{"type":"user","timestamp":"` + now + `","message":{"role":"user","content":"Build feature"}}`,
		`{"type":"assistant","timestamp":"` + now + `","message":{"content":[{"type":"tool_use","name":"TaskCreate","input":{"subject":"Create schema","activeForm":"Creating schema"}}]}}`,
		`{"type":"assistant","timestamp":"` + now + `","message":{"content":[{"type":"tool_use","name":"TaskCreate","input":{"subject":"Temporary task"}},{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"1","status":"in_progress"}},{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"2","status":"deleted"}},{"type":"tool_use","name":"TaskCreate","input":{"subject":"Write tests"}}]}}`,
		`{"type":"user","timestamp":"` + now + `","message":{"role":"user","content":"Proceed"}}`,
		`{"type":"assistant","timestamp":"` + now + `","message":{"content":[{"type":"text","text":"Plain reply without checklist tool"}]}}`,
	}, "\n") + "\n"

	msgs, err := ParseJSONL(strings.NewReader(jsonl), p.FilterUserEntry, nil)
	if err != nil {
		t.Fatalf("ParseJSONL failed: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(msgs), msgs)
	}
	turn1Asst := msgs[1]
	if len(turn1Asst.Checklist) != 2 {
		t.Fatalf("turn1 checklist = %+v, want 2 items", turn1Asst.Checklist)
	}
	if turn1Asst.Checklist[0].ID != "1" || turn1Asst.Checklist[0].Status != agent.ChecklistInProgress || turn1Asst.Checklist[0].ActiveForm != "Creating schema" {
		t.Fatalf("task 1 = %+v", turn1Asst.Checklist[0])
	}
	// Task "2" was deleted before creating "Write tests", so monotonic ID must be "3" (not colliding on "2").
	if turn1Asst.Checklist[1].ID != "3" || turn1Asst.Checklist[1].Title != "Write tests" || turn1Asst.Checklist[1].Status != agent.ChecklistPending {
		t.Fatalf("task 3 = %+v", turn1Asst.Checklist[1])
	}
	if msgs[3].Checklist != nil {
		t.Fatalf("turn2 without checklist tool should have nil Checklist, got %+v", msgs[3].Checklist)
	}

	delta := strings.Join([]string{
		`{"type":"user","timestamp":"` + now + `","message":{"role":"user","content":"Update tasks"}}`,
		`{"type":"assistant","timestamp":"` + now + `","message":{"content":[{"type":"tool_use","name":"TaskUpdate","input":{"taskId":1,"status":"completed"}},{"type":"tool_use","name":"TaskCreate","input":{"subject":"Ship PR"}}]}}`,
	}, "\n") + "\n"
	appended, err := p.AppendTranscript(strings.NewReader(delta), msgs)
	if err != nil {
		t.Fatalf("AppendTranscript failed: %v", err)
	}
	if len(appended) != 6 {
		t.Fatalf("got %d messages after append, want 6", len(appended))
	}
	// Base messages must remain untouched (deep copy).
	if msgs[1].Checklist[0].Status != agent.ChecklistInProgress {
		t.Fatalf("base transcript checklist mutated: %+v", msgs[1].Checklist)
	}
	turn3Asst := appended[5]
	if len(turn3Asst.Checklist) != 3 {
		t.Fatalf("turn3 checklist = %+v, want 3 items", turn3Asst.Checklist)
	}
	if turn3Asst.Checklist[0].ID != "1" || turn3Asst.Checklist[0].Status != agent.ChecklistDone {
		t.Fatalf("turn3 task 1 = %+v", turn3Asst.Checklist[0])
	}
	if turn3Asst.Checklist[2].ID != "4" || turn3Asst.Checklist[2].Title != "Ship PR" {
		t.Fatalf("turn3 task 4 = %+v", turn3Asst.Checklist[2])
	}
}

func TestApplyToolChecklist_TaskUpdateMidSessionFallbackAndStream(t *testing.T) {
	p := New()

	// Mid-session attach where taskId wasn't in prev but subject is provided.
	next, ok := p.ApplyToolChecklist(nil, "TaskUpdate", map[string]any{
		"taskId":     "5",
		"subject":    "Recovered task",
		"activeForm": "Recovering",
		"status":     "in_progress",
	})
	if !ok || len(next) != 1 || next[0].ID != "5" || next[0].Status != agent.ChecklistInProgress {
		t.Fatalf("TaskUpdate fallback = (%+v, %v)", next, ok)
	}

	evts1, err := p.ParseStreamEvent([]byte(`{"type":"content_block_start","content_block":{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"content":"Inspect","status":"in_progress"}]}}}`))
	if err != nil || len(evts1) != 1 || evts1[0].Type != "tool_start" || len(evts1[0].Checklist) != 1 {
		t.Fatalf("content_block_start TodoWrite = (%+v, %v)", evts1, err)
	}

	evts2, err := p.ParseStreamEvent([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"TaskCreate","input":{"subject":"First"}},{"type":"tool_use","name":"TaskCreate","input":{"subject":"Second"}}]}}`))
	if err != nil || len(evts2) != 2 {
		t.Fatalf("assistant TaskCreate = (%+v, %v)", evts2, err)
	}
	if len(evts2[0].Checklist) != 1 || evts2[0].Checklist[0].ID != "1" {
		t.Fatalf("event 0 checklist = %+v", evts2[0].Checklist)
	}
	if len(evts2[1].Checklist) != 2 || evts2[1].Checklist[1].ID != "2" {
		t.Fatalf("event 1 checklist = %+v", evts2[1].Checklist)
	}
}
