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

func TestChecklistTrackerAndBounds_ClaudeRegressions(t *testing.T) {
	p := New()
	now := time.Now().UTC().Format(time.RFC3339)

	// 1. Delete highest task ID -> TaskCreate -> TaskUpdate via ChecklistTracker.
	tr := p.NewChecklistTracker()
	if items, cleared, ok := tr.Apply("TaskCreate", map[string]any{"subject": "Task A"}); !ok || cleared || len(items) != 1 || items[0].ID != "1" {
		t.Fatalf("create A = (%+v, %v, %v)", items, cleared, ok)
	}
	if items, cleared, ok := tr.Apply("TaskCreate", map[string]any{"subject": "Task B"}); !ok || cleared || len(items) != 2 || items[1].ID != "2" {
		t.Fatalf("create B = (%+v, %v, %v)", items, cleared, ok)
	}
	if items, cleared, ok := tr.Apply("TaskUpdate", map[string]any{"taskId": "2", "status": "deleted"}); !ok || cleared || len(items) != 1 {
		t.Fatalf("delete B = (%+v, %v, %v)", items, cleared, ok)
	}
	if items, cleared, ok := tr.Apply("TaskCreate", map[string]any{"subject": "Task C"}); !ok || cleared || len(items) != 2 || items[1].ID != "3" {
		t.Fatalf("create C after deleting highest ID must allocate '3', got (%+v, %v, %v)", items, cleared, ok)
	}
	if items, cleared, ok := tr.Apply("TaskUpdate", map[string]any{"taskId": "3", "status": "completed"}); !ok || cleared || items[1].Status != agent.ChecklistDone {
		t.Fatalf("update Task C (id 3) = (%+v, %v, %v)", items, cleared, ok)
	}
	// Deleting remaining tasks clears tracker and sets cleared=true while retaining highWater=3.
	_, _, _ = tr.Apply("TaskUpdate", map[string]any{"taskId": "1", "status": "deleted"})
	items, cleared, ok := tr.Apply("TaskUpdate", map[string]any{"taskId": "3", "status": "deleted"})
	if !ok || !cleared || items != nil {
		t.Fatalf("deleting last task should emit (nil, cleared=true, ok=true), got (%+v, %v, %v)", items, cleared, ok)
	}
	_, snapCleared, snapHigh := tr.Snapshot()
	if !snapCleared || snapHigh != 3 {
		t.Fatalf("Snapshot after full delete = (cleared=%v, high=%d), want (true, 3)", snapCleared, snapHigh)
	}

	// 2. ParseJSONL + AppendTranscript: Turn 1 creates/deletes highest ID, Turn 2 clears via TodoWrite([]),
	//    Turn 3 AppendTranscript must NOT re-seed Turn 1's stale tasks and must preserve highWater=2.
	turn1 := strings.Join([]string{
		`{"type":"user","timestamp":"` + now + `","message":{"role":"user","content":"Turn 1"}}`,
		`{"type":"assistant","timestamp":"` + now + `","message":{"content":[{"type":"tool_use","name":"TaskCreate","input":{"subject":"Alpha"}},{"type":"tool_use","name":"TaskCreate","input":{"subject":"Beta"}},{"type":"tool_use","name":"TaskUpdate","input":{"taskId":"2","status":"deleted"}}]}}`,
	}, "\n") + "\n"
	msgs1, err := ParseJSONL(strings.NewReader(turn1), p.FilterUserEntry, nil)
	if err != nil || len(msgs1) != 2 || msgs1[1].ChecklistHighWater != 2 {
		t.Fatalf("turn1 = (%+v, %v)", msgs1, err)
	}

	turn2Delta := strings.Join([]string{
		`{"type":"user","timestamp":"` + now + `","message":{"role":"user","content":"Turn 2"}}`,
		`{"type":"assistant","timestamp":"` + now + `","message":{"content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[]}}]}}`,
	}, "\n") + "\n"
	msgs2, err := p.AppendTranscript(strings.NewReader(turn2Delta), msgs1)
	if err != nil || len(msgs2) != 4 || !msgs2[3].ChecklistCleared || len(msgs2[3].Checklist) != 0 {
		t.Fatalf("turn2 cleared = (%+v, %v)", msgs2, err)
	}

	turn3Delta := strings.Join([]string{
		`{"type":"user","timestamp":"` + now + `","message":{"role":"user","content":"Turn 3"}}`,
		`{"type":"assistant","timestamp":"` + now + `","message":{"content":[{"type":"tool_use","name":"TaskCreate","input":{"subject":"Gamma"}}]}}`,
	}, "\n") + "\n"
	msgs3, err := p.AppendTranscript(strings.NewReader(turn3Delta), msgs2)
	if err != nil || len(msgs3) != 6 {
		t.Fatalf("turn3 = (%+v, %v)", msgs3, err)
	}
	turn3Checklist := msgs3[5].Checklist
	if len(turn3Checklist) != 1 || turn3Checklist[0].Title != "Gamma" || turn3Checklist[0].ID != "3" {
		t.Fatalf("turn3 should only contain Gamma(id=3) and NOT re-seed Alpha, got %+v", turn3Checklist)
	}

	// 3. Enforce MaxChecklistItems and title/ID clamping.
	boundTracker := p.NewChecklistTracker()
	for i := 0; i < agent.MaxChecklistItems+15; i++ {
		_, _, _ = boundTracker.Apply("TaskCreate", map[string]any{
			"id":         strings.Repeat("z", 80),
			"subject":    strings.Repeat("t", agent.MaxChecklistTitleRunes+40),
			"activeForm": strings.Repeat("a", agent.MaxChecklistTitleRunes+20),
		})
	}
	capped, _, _ := boundTracker.Snapshot()
	if len(capped) != agent.MaxChecklistItems {
		t.Fatalf("expected capped %d items, got %d", agent.MaxChecklistItems, len(capped))
	}
	if len(capped[0].ID) != agent.MaxChecklistIDBytes || len([]rune(capped[0].Title)) != agent.MaxChecklistTitleRunes || len([]rune(capped[0].ActiveForm)) != agent.MaxChecklistTitleRunes {
		t.Fatalf("expected clamped fields, got id=%d title=%d active=%d", len(capped[0].ID), len([]rune(capped[0].Title)), len([]rune(capped[0].ActiveForm)))
	}
}
