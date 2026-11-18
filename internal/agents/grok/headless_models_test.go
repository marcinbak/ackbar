package grok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ackbar/internal/agent"
)

func TestHeadless_BuildTurnSpec(t *testing.T) {
	p := New()
	if p.Mode() != agent.HeadlessModeACP {
		t.Fatalf("expected HeadlessModeACP, got %q", p.Mode())
	}
	sess := &agent.Session{
		ID:             "grok:local:sess-123",
		Agent:          "grok",
		NativeID:       "sess-123",
		Model:          "grok-4.7",
		Effort:         "high",
		PermissionMode: "plan",
		ExtraDirs:      []string{"/tmp/extra1", "/tmp/extra2", "-unsafe-flag", "relative/dir"},
	}

	spec1, err := p.BuildTurnSpec(agent.HeadlessTurnInput{Session: sess})
	if err != nil {
		t.Fatalf("BuildTurnSpec Turn 1 failed: %v", err)
	}
	args1Str := strings.Join(spec1.Args, " ")
	for _, want := range []string{
		"agent stdio --no-auto-update",
		"--add-dir /tmp/extra1",
		"--add-dir /tmp/extra2",
		"--permission-mode plan",
		"-m grok-4.7",
		"--effort high",
	} {
		if !strings.Contains(args1Str, want) {
			t.Errorf("Turn 1 missing %q in %s", want, args1Str)
		}
	}
	if strings.Contains(args1Str, "-unsafe-flag") || strings.Contains(args1Str, "relative/dir") || strings.Contains(args1Str, "--always-approve") {
		t.Errorf("unexpected flags in Turn 1: %s", args1Str)
	}

	spec2, err := p.BuildTurnSpec(agent.HeadlessTurnInput{Session: sess, IsExisting: true})
	if err != nil {
		t.Fatalf("BuildTurnSpec Turn 2 failed: %v", err)
	}
	if strings.Contains(strings.Join(spec2.Args, " "), "--add-dir") {
		t.Errorf("Turn 2 must not include --add-dir: %v", spec2.Args)
	}

	for _, pm := range []string{"", "default"} {
		defSpec, _ := p.BuildTurnSpec(agent.HeadlessTurnInput{
			Session: &agent.Session{NativeID: "sess-def", PermissionMode: pm},
		})
		s := strings.Join(defSpec.Args, " ")
		if strings.Contains(s, "--always-approve") || strings.Contains(s, "--permission-mode") {
			t.Errorf("default permission mode %q should omit approval flags: %s", pm, s)
		}
	}
	for _, pm := range []string{"auto", "acceptEdits", "dontAsk"} {
		pmSpec, _ := p.BuildTurnSpec(agent.HeadlessTurnInput{
			Session: &agent.Session{NativeID: "sess-pm", PermissionMode: pm},
		})
		if !strings.Contains(strings.Join(pmSpec.Args, " "), "--permission-mode "+pm) {
			t.Errorf("expected --permission-mode %s in %v", pm, pmSpec.Args)
		}
	}
	yoloSpec, _ := p.BuildTurnSpec(agent.HeadlessTurnInput{
		Session: &agent.Session{NativeID: "sess-yolo", YoloMode: true, PermissionMode: "plan"},
	})
	if !strings.Contains(strings.Join(yoloSpec.Args, " "), "--always-approve") {
		t.Errorf("YoloMode=true should emit --always-approve: %v", yoloSpec.Args)
	}
	bypassSpec, _ := p.BuildTurnSpec(agent.HeadlessTurnInput{
		Session: &agent.Session{NativeID: "sess-bypass", PermissionMode: "bypassPermissions"},
	})
	if !strings.Contains(strings.Join(bypassSpec.Args, " "), "--always-approve") {
		t.Errorf("bypassPermissions should emit --always-approve: %v", bypassSpec.Args)
	}
}

func TestHeadless_UsageAndModels(t *testing.T) {
	p := New()
	tmpHome := t.TempDir()
	sessID := "sess-nested-usage"
	cwd := filepath.Join(tmpHome, "proj")
	_ = os.MkdirAll(cwd, 0o755)
	sessDir := filepath.Join(tmpHome, ".grok", "sessions", EncodeCwd(cwd), sessID)
	_ = os.MkdirAll(sessDir, 0o755)

	_ = os.WriteFile(filepath.Join(sessDir, "usage.json"), []byte(`{"session":{"inputTokens":22590,"outputTokens":716,"cachedReadTokens":1664,"costUsdTicks":469800000}}`), 0o644)
	_ = os.WriteFile(filepath.Join(sessDir, "signals.json"), []byte(`{"contextTokensUsed":23306,"contextWindowTokens":131072}`), 0o644)

	u, ok := p.ComputeTranscriptUsage(tmpHome, cwd, sessID, 128000)
	if !ok || u.InputTokens != 22590 || u.OutputTokens != 716 || u.CacheReadTokens != 1664 || u.CostUSD != 0.04698 || u.LastTurnContextTokens != 23306 || u.ContextLimitTokens != 131072 {
		t.Fatalf("ComputeTranscriptUsage mismatch: %+v ok=%v", u, ok)
	}

	grokHome := filepath.Join(tmpHome, ".grok")
	cacheJSON := `{"models":{"grok-4.7":{"info":{"id":"grok-4.7","name":"Grok 4.7","hidden":false}},"grok-internal":{"info":{"id":"grok-internal","hidden":true}}}}`
	_ = os.WriteFile(filepath.Join(grokHome, "models_cache.json"), []byte(cacheJSON), 0o644)
	models := p.CollectModels(tmpHome)
	found47 := false
	for _, m := range models {
		if m.ID == "grok-internal" {
			t.Errorf("hidden model grok-internal should be excluded")
		}
		if m.ID == "grok-4.7" && m.Label == "Grok 4.7" {
			found47 = true
		}
	}
	if !found47 {
		t.Errorf("expected grok-4.7 in %+v", models)
	}
	if !p.ValidateModelID("grok-4.7") || p.ValidateModelID("-bad") || p.ValidateModelID("grok;rm") {
		t.Errorf("ValidateModelID mismatch")
	}
	if args, ok := p.InspectSubcommandArgs("agents"); !ok || len(args) != 1 || args[0] != "inspect" {
		t.Errorf("InspectSubcommandArgs(agents) = (%v, %v)", args, ok)
	}
}

func TestHeadless_StreamParser(t *testing.T) {
	p := New()
	sp := p.NewStreamParser("grok:local:sess-1")
	var all []agent.ChatStreamEvent
	feed := func(line string) {
		evts, handled, err := sp.ParseStreamLine([]byte(line))
		if err != nil || !handled {
			t.Fatalf("ParseStreamLine(%s) handled=%v err=%v", line, handled, err)
		}
		all = append(all, evts...)
	}

	feed(`{"type":"thought","data":"thinking about solution"}`)
	feed(`{"type":"text","data":"hello user"}`)
	feed(`{"type":"tool_call","toolCallId":"call-1","toolName":"read_file","input":{"path":"foo.go"}}`)
	feed(`{"type":"tool_call_update","toolCallId":"call-1","status":"completed","rawOutput":"package main"}`)
	feed(`{"type":"text","data":"done"}`)
	feed(`{"type":"usage","model":"grok-4.7"}`)
	feed(`{"type":"end","stopReason":"stop"}`)

	if len(all) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(all), all)
	}
	if all[0].Type != "thought_delta" || all[1].Type != "text_delta" || all[2].Type != "tool_start" ||
		all[3].Type != "tool_result" || all[4].Text != "\n\ndone" || all[5].Type != "model_detected" || all[6].Type != "turn_complete" {
		t.Fatalf("stream event sequence mismatch: %+v", all)
	}
}
