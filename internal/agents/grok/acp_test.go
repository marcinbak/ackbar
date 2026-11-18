package grok

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"ackbar/internal/agent"
)

func TestACPDriver_AuthModelsCommandsAndQueue(t *testing.T) {
	p := New()
	methods := []agent.ACPAuthMethod{
		{ID: "cached_token", Name: "Cached"},
		{ID: "xai.api_key", Name: "API Key"},
	}
	id, meta, ok := p.SelectACPAuth(methods, []string{"XAI_API_KEY=secret"})
	if !ok || id != "xai.api_key" || meta["headless"] != true {
		t.Fatalf("expected xai.api_key with API key, got (%q, %+v, %v)", id, meta, ok)
	}
	id2, _, ok2 := p.SelectACPAuth(methods, nil)
	if !ok2 || id2 != "cached_token" {
		t.Fatalf("expected cached_token fallback, got (%q, %v)", id2, ok2)
	}

	tmpHome := t.TempDir()
	res := p.HandleACPNotification(tmpHome, nil, "_x.ai/models/update", map[string]any{
		"currentModelId": "grok-4.7",
		"models": []any{
			map[string]any{"id": "grok-4.7", "name": "Grok 4.7"},
			map[string]any{"id": "grok-secret", "hidden": true},
			map[string]any{"id": "-bad-flag", "name": "Bad"},
		},
	})
	if res.DetectedModel != "grok-4.7" {
		t.Fatalf("expected DetectedModel=grok-4.7, got %q", res.DetectedModel)
	}
	models := p.CollectModels(tmpHome)
	if len(models) != 1 || models[0].ID != "grok-4.7" {
		t.Fatalf("expected cached grok-4.7 only, got %+v", models)
	}

	resMap := p.HandleACPNotification(tmpHome, []string{"GROK_HOME=" + tmpHome + "/prof"}, "_x.ai/models/update", map[string]any{
		"models": map[string]any{
			"grok-4.6": map[string]any{"name": "Grok 4.6"},
		},
	})
	_ = resMap
	if profModels := loadACPModelsCache(tmpHome + "/prof"); len(profModels) != 1 || profModels[0].ID != "grok-4.6" {
		t.Fatalf("expected GROK_HOME profile models cache, got %+v", profModels)
	}

	cmdRes := p.HandleACPNotification(tmpHome, nil, "session/update", map[string]any{
		"update": map[string]any{
			"sessionUpdate": "available_commands_update",
			"availableCommands": []any{
				map[string]any{"name": "goal", "description": "Live goal desc"},
				map[string]any{"name": "custom-acp", "description": "Custom cmd"},
			},
		},
	})
	if len(cmdRes.AvailableCommands) != 2 {
		t.Fatalf("expected 2 commands in notification result, got %+v", cmdRes.AvailableCommands)
	}
	cmds := p.CollectCommands(tmpHome)
	seen := map[string]bool{}
	for _, c := range cmds {
		seen[c.Command] = true
	}
	for _, want := range []string{"/compact", "/context", "/always-approve", "/goal", "/workflow", "/custom-acp"} {
		if !seen[want] {
			t.Errorf("missing command %q in %+v", want, cmds)
		}
	}

	qRes := p.HandleACPNotification(tmpHome, nil, "_x.ai/queue/changed", map[string]any{"count": float64(2)})
	if len(qRes.SystemEvents) != 1 || qRes.SystemEvents[0]["subtype"] != "background_tasks_changed" {
		t.Fatalf("unexpected queue system event: %+v", qRes.SystemEvents)
	}
	hugeRes := p.HandleACPNotification(tmpHome, nil, "_x.ai/queue/changed", map[string]any{"count": 1e20})
	if len(hugeRes.SystemEvents) != 1 {
		t.Fatalf("expected capped system event for huge count, got %+v", hugeRes.SystemEvents)
	}
	if tasks, _ := hugeRes.SystemEvents[0]["tasks"].([]any); len(tasks) != maxSynthesizedQueueTasks {
		t.Fatalf("expected huge count capped at %d tasks, got %d", maxSynthesizedQueueTasks, len(tasks))
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), -5} {
		badRes := p.HandleACPNotification(tmpHome, nil, "_x.ai/queue/changed", map[string]any{"count": bad})
		if len(badRes.SystemEvents) != 0 {
			t.Fatalf("expected invalid count %v ignored, got %+v", bad, badRes.SystemEvents)
		}
	}
	listM, execM, cancelM, sup := p.ACPRewindMethods()
	if !sup || listM != "_x.ai/rewind/points" || execM != "_x.ai/rewind/execute" || cancelM != "_x.ai/cancelRewind" {
		t.Fatalf("unexpected rewind methods: %q %q %q %v", listM, execM, cancelM, sup)
	}
}

func TestACPControl_ExitPlanAskQuestionAndStream(t *testing.T) {
	p := New()
	exitPrompt, ok := p.ParseACPServerRequest(json.RawMessage(`"plan-1"`), "plan-1", "_x.ai/exit_plan_mode", map[string]any{
		"planContent": "Step 1: edit files",
	})
	if !ok || exitPrompt.Blocked.Kind != agent.BlockApproval || exitPrompt.PlanContent != "Step 1: edit files" {
		t.Fatalf("exit_plan_mode parse mismatch: %+v ok=%v", exitPrompt, ok)
	}
	exitApprove, err := p.BuildACPServerResponse(exitPrompt, true, "")
	if err != nil || exitApprove.(map[string]any)["outcome"] != "approved" {
		t.Fatalf("exit_plan_mode approve mismatch: %+v err=%v", exitApprove, err)
	}
	exitAbandon, _ := p.BuildACPServerResponse(exitPrompt, false, "abandon")
	if exitAbandon.(map[string]any)["outcome"] != "abandoned" {
		t.Fatalf("exit_plan_mode abandon mismatch: %+v", exitAbandon)
	}

	qPrompt, ok := p.ParseACPServerRequest(json.RawMessage(`5`), "5", "_x.ai/ask_user_question", map[string]any{
		"questions": []any{
			map[string]any{
				"id":          "q1",
				"question":    "Pick DB",
				"multiSelect": true,
				"options":     []any{map[string]any{"label": "SQLite"}, map[string]any{"label": "Postgres"}},
			},
			map[string]any{
				"id":       "q2",
				"question": "Notes?",
			},
		},
	})
	if !ok || qPrompt.Blocked.Kind != agent.BlockQuestion || len(qPrompt.Questions) != 2 {
		t.Fatalf("ask_user_question parse mismatch: %+v ok=%v", qPrompt, ok)
	}
	qAns, err := p.BuildACPServerResponse(qPrompt, true, `{"q1":"SQLite, Postgres","q2":"use WAL"}`)
	if err != nil {
		t.Fatalf("BuildACPServerResponse failed: %v", err)
	}
	b, _ := json.Marshal(qAns)
	if !strings.Contains(string(b), `"outcome":"accepted"`) || !strings.Contains(string(b), `"SQLite","Postgres"`) || !strings.Contains(string(b), `"textResponse":"use WAL"`) {
		t.Fatalf("unexpected ask_user_question response: %s", b)
	}

	if _, ok := p.ParseACPServerRequest(json.RawMessage(`9`), "9", "_x.ai/unknown_method", nil); ok {
		t.Fatalf("expected unknown method to return false")
	}

	sp := p.NewStreamParser("grok:local:s1")
	evts, handled, err := sp.ParseStreamLine([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"text":"hello acp"}}}}`))
	if err != nil || !handled || len(evts) != 1 || evts[0].Text != "hello acp" {
		t.Fatalf("JSON-RPC stream line mismatch: evts=%+v handled=%v err=%v", evts, handled, err)
	}
}
