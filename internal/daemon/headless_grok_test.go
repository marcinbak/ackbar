package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildGrokHeadlessArgs_Turn1AndTurn2(t *testing.T) {
	sess := &Session{
		ID:             "grok:local:sess-123",
		Agent:          "grok",
		NativeID:       "sess-123",
		Model:          "grok-4.7",
		Effort:         "high",
		PermissionMode: "plan",
		ExtraDirs:      []string{"/tmp/extra1", "/tmp/extra2", "-unsafe-flag", "relative/dir"},
	}

	// Turn 1: !isExisting -> expects --single=..., -s, -m, --effort, --permission-mode, and valid --add-dir
	args1 := buildGrokHeadlessArgs(sess, "hello turn 1", false)
	args1Str := strings.Join(args1, " ")
	if !strings.Contains(args1Str, "--single=hello turn 1") {
		t.Errorf("expected --single=hello turn 1 in Turn 1 args: %s", args1Str)
	}
	if !strings.Contains(args1Str, "-s sess-123") {
		t.Errorf("expected -s sess-123 in Turn 1 args: %s", args1Str)
	}
	if !strings.Contains(args1Str, "--add-dir /tmp/extra1") || !strings.Contains(args1Str, "--add-dir /tmp/extra2") {
		t.Errorf("expected valid --add-dir in Turn 1 args: %s", args1Str)
	}
	if strings.Contains(args1Str, "-unsafe-flag") || strings.Contains(args1Str, "relative/dir") {
		t.Errorf("expected invalid ExtraDirs to be skipped: %s", args1Str)
	}
	if !strings.Contains(args1Str, "-m grok-4.7") {
		t.Errorf("expected -m grok-4.7 in Turn 1 args: %s", args1Str)
	}
	if !strings.Contains(args1Str, "--effort high") {
		t.Errorf("expected --effort high in Turn 1 args: %s", args1Str)
	}
	if !strings.Contains(args1Str, "--permission-mode plan") {
		t.Errorf("expected --permission-mode plan in Turn 1 args: %s", args1Str)
	}
	if !strings.Contains(args1Str, "--output-format streaming-json") {
		t.Errorf("expected --output-format streaming-json in Turn 1 args: %s", args1Str)
	}

	// Turn 2+: isExisting -> expects -r, and CRITICAL: NEVER --add-dir
	args2 := buildGrokHeadlessArgs(sess, "hello turn 2", true)
	args2Str := strings.Join(args2, " ")
	if !strings.Contains(args2Str, "--single=hello turn 2") {
		t.Errorf("expected --single=hello turn 2 in Turn 2 args: %s", args2Str)
	}
	if !strings.Contains(args2Str, "-r sess-123") {
		t.Errorf("expected -r sess-123 in Turn 2 args: %s", args2Str)
	}
	if strings.Contains(args2Str, "--add-dir") {
		t.Errorf("CRITICAL INVARIANT VIOLATION: --add-dir must NOT appear in Turn 2 args: %s", args2Str)
	}
}

func TestBuildGrokHeadlessArgs_HyphenPrefixedPrompt(t *testing.T) {
	sess := &Session{
		ID:             "grok:local:sess-hyphen",
		Agent:          "grok",
		NativeID:       "sess-hyphen",
		PermissionMode: "plan",
	}

	args := buildGrokHeadlessArgs(sess, "--always-approve", false)
	argsStr := strings.Join(args, " ")
	if !strings.Contains(argsStr, "--single=--always-approve") {
		t.Errorf("expected --single=--always-approve, got: %s", argsStr)
	}
	if !strings.Contains(argsStr, "--permission-mode plan") {
		t.Errorf("expected --permission-mode plan, got: %s", argsStr)
	}
}

func TestComputeGrokTranscriptUsage_NestedSession(t *testing.T) {
	tmpHome := t.TempDir()
	sessID := "sess-nested-usage"
	cwd := filepath.Join(tmpHome, "proj")
	_ = os.MkdirAll(cwd, 0755)

	sessDir := filepath.Join(tmpHome, ".grok", "sessions", EncodeGrokCwd(cwd), sessID)
	_ = os.MkdirAll(sessDir, 0755)

	usageJSON := `{"sessionId":"` + sessID + `","session":{"inputTokens":22590,"outputTokens":716,"cachedReadTokens":1664,"costUsdTicks":469800000}}`
	_ = os.WriteFile(filepath.Join(sessDir, "usage.json"), []byte(usageJSON), 0644)

	signalsJSON := `{"contextTokensUsed":23306,"contextWindowTokens":131072}`
	_ = os.WriteFile(filepath.Join(sessDir, "signals.json"), []byte(signalsJSON), 0644)

	sess := &Session{NativeID: sessID, Cwd: cwd}
	sumIn, sumOut, cacheRead, lastTurn, limitTokens, _, costUSD := computeGrokTranscriptUsage(tmpHome, sess, 128000)
	if sumIn != 22590 || sumOut != 716 || cacheRead != 1664 {
		t.Errorf("unexpected tokens: in=%d, out=%d, cache=%d", sumIn, sumOut, cacheRead)
	}
	if costUSD != 0.04698 {
		t.Errorf("expected costUSD 0.04698, got %f", costUSD)
	}
	if lastTurn != 23306 || limitTokens != 131072 {
		t.Errorf("unexpected context tokens: last=%d limit=%d", lastTurn, limitTokens)
	}
}

func TestCollectGrokModels_NestedInfoAndHidden(t *testing.T) {
	srv, _ := setupTestServer(t)
	tmpHome := t.TempDir()
	srv.homeDir = tmpHome

	grokHome := filepath.Join(tmpHome, ".grok")
	_ = os.MkdirAll(grokHome, 0755)

	cacheJSON := `{
		"models": {
			"grok-4.7": {
				"info": {
					"id": "grok-4.7",
					"name": "Grok 4.7",
					"description": "Frontier flagship",
					"hidden": false
				}
			},
			"grok-internal": {
				"info": {
					"id": "grok-internal",
					"name": "Internal Test Model",
					"hidden": true
				}
			}
		}
	}`
	_ = os.WriteFile(filepath.Join(grokHome, "models_cache.json"), []byte(cacheJSON), 0644)

	models := srv.collectGrokModels()
	for _, m := range models {
		if m.ID == "grok-internal" {
			t.Errorf("expected grok-internal to be excluded (hidden)")
		}
	}
	found47 := false
	for _, m := range models {
		if m.ID == "grok-4.7" {
			found47 = true
			if m.Label != "Grok 4.7" {
				t.Errorf("expected label 'Grok 4.7', got %q", m.Label)
			}
		}
	}
	if !found47 {
		t.Errorf("expected grok-4.7 in collected models")
	}
}

func TestBuildGrokHeadlessArgs_AlwaysApproveFallback(t *testing.T) {
	for _, pm := range []string{"", "default", "bypassPermissions", "auto"} {
		sess := &Session{
			NativeID:       "sess-approve",
			PermissionMode: pm,
		}
		args := buildGrokHeadlessArgs(sess, "run prompt", false)
		argsStr := strings.Join(args, " ")
		if !strings.Contains(argsStr, "--always-approve") {
			t.Errorf("expected --always-approve for permissionMode %q, got: %s", pm, argsStr)
		}
		if strings.Contains(argsStr, "--permission-mode") {
			t.Errorf("did not expect --permission-mode for permissionMode %q, got: %s", pm, argsStr)
		}
	}
}

func TestHandleGrokStreamEvent(t *testing.T) {
	var events []ChatStreamEvent
	hooks := streamParseHooks{
		emitTurnComplete: true,
		onModelDetected:  func(m string) {},
		onTurnResult:     func(lastTurnText, resultText string, streamHadError bool) {},
	}
	emit := func(ev ChatStreamEvent) {
		events = append(events, ev)
	}

	sessionID := "grok:local:sess-1"
	seenGrok := new(bool)
	needsSeparation := new(bool)
	hasEmittedAssistantText := new(bool)
	emittedAuthBlock := new(bool)
	toolUses := make(map[string]string)
	toolInputs := make(map[string]map[string]any)
	var lastText strings.Builder
	var resultText string
	var hadError bool

	feed := func(jsonStr string) {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v", err)
		}
		handleGrokStreamEvent(raw, sessionID, seenGrok, &lastText, &resultText, &hadError, needsSeparation, hasEmittedAssistantText, toolUses, toolInputs, emittedAuthBlock, emit, hooks)
	}

	// 1. thought
	feed(`{"type":"thought","data":"thinking about solution"}`)
	if len(events) != 1 || events[0].Type != "thought_delta" || events[0].Thinking != "thinking about solution" {
		t.Fatalf("unexpected thought event: %+v", events)
	}

	// 2. text
	feed(`{"type":"text","data":"hello user"}`)
	if len(events) != 2 || events[1].Type != "text_delta" || events[1].Text != "hello user" {
		t.Fatalf("unexpected text event: %+v", events)
	}

	// 3. tool_call
	feed(`{"type":"tool_call","toolCallId":"call-1","toolName":"read_file","input":{"path":"foo.go"}}`)
	if len(events) != 3 || events[2].Type != "tool_start" || events[2].ToolName != "read_file" {
		t.Fatalf("unexpected tool_start event: %+v", events)
	}

	// 4. tool_call_update
	feed(`{"type":"tool_call_update","toolCallId":"call-1","status":"completed","rawOutput":"package main"}`)
	if len(events) != 4 || events[3].Type != "tool_result" || events[3].ToolOutput != "package main" {
		t.Fatalf("unexpected tool_result event: %+v", events)
	}

	// 5. end
	feed(`{"type":"end","stopReason":"stop"}`)
	if len(events) != 5 || events[4].Type != "turn_complete" {
		t.Fatalf("unexpected turn_complete event: %+v", events)
	}
}

func TestHeadlessRunner_GrokOneShotWorkerE2E(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)

	// Create mock grok CLI executable
	mockGrokPath := filepath.Join(binDir, "grok")
	script := `#!/bin/sh
# Echo arguments to a log for assertion
echo "$@" >> "` + filepath.Join(tmpDir, "grok.log") + `"
# Emit streaming JSON NDJSON events
echo '{"type":"thought","data":"analyzing"}'
echo '{"type":"text","data":"pong from mock grok"}'
echo '{"type":"end","stopReason":"stop"}'
`
	if err := os.WriteFile(mockGrokPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock grok: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)

	srv, db := setupTestServer(t)
	cwd := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(cwd, 0755)

	sess := &Session{
		ID:         "grok:local:grok-e2e-1",
		Agent:      "grok",
		EngineType: EngineHeadless,
		NativeID:   "grok-e2e-1",
		Cwd:        cwd,
		State:      StateIdle,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	hr := srv.headless
	if hr == nil {
		t.Fatalf("HeadlessRunner is nil on test server")
	}

	// Turn 1 execution
	err := hr.RunTurn(context.Background(), sess, "ping turn 1")
	if err != nil {
		t.Fatalf("RunTurn Turn 1 failed: %v", err)
	}

	var logStr string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(tmpDir, "grok.log")); err == nil && len(b) > 0 {
			if !hr.IsRunning(sess.ID) {
				logStr = string(b)
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if logStr == "" {
		t.Fatalf("timed out waiting for Turn 1 completion and grok.log")
	}
	if !strings.Contains(logStr, "-s grok-e2e-1") {
		t.Errorf("expected -s grok-e2e-1 in Turn 1 run, got: %s", logStr)
	}

	// Turn 2 execution
	sess.LastPrompt = "ping turn 1"
	err = hr.RunTurn(context.Background(), sess, "ping turn 2")
	if err != nil {
		t.Fatalf("RunTurn Turn 2 failed: %v", err)
	}

	var logStr2 string
	deadline2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline2) {
		if b, err := os.ReadFile(filepath.Join(tmpDir, "grok.log")); err == nil {
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			if len(lines) >= 2 && !hr.IsRunning(sess.ID) {
				logStr2 = string(b)
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if logStr2 == "" {
		t.Fatalf("timed out waiting for Turn 2 completion and grok.log")
	}
	lines := strings.Split(strings.TrimSpace(logStr2), "\n")
	if !strings.Contains(lines[len(lines)-1], "-r grok-e2e-1") {
		t.Errorf("expected -r grok-e2e-1 in Turn 2 run, got: %s", lines[len(lines)-1])
	}
}
