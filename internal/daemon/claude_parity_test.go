package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agents/claude"
)

func TestClaudeParity_RateLimitPromptArgsAndControls(t *testing.T) {
	p := claude.New()
	leg := ClaudeAgentAdapter{}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	for _, txt := range []string{"5-hour limit reached, resets at 15:30", "Weekly limit reached", "Usage limit reached", "normal response"} {
		l1, t1, r1 := p.DetectRateLimit(txt, now)
		l2, t2, r2 := leg.DetectRateLimit(txt, now)
		if l1 != l2 || !t1.Equal(t2) || r1 != r2 {
			t.Fatalf("DetectRateLimit(%q) mismatch: (%v,%v,%q) vs (%v,%v,%q)", txt, l1, t1, r1, l2, t2, r2)
		}
	}

	raw := "<system-reminder>hide</system-reminder>\n<command-name>review</command-name><command-args>pr 42</command-args>"
	c1, k1 := p.FilterUserEntry(nil, raw)
	c2, k2 := leg.FilterUserEntry(nil, raw)
	if c1 != c2 || k1 != k2 || c1 != "/review pr 42" {
		t.Fatalf("FilterUserEntry mismatch: (%q,%v) vs (%q,%v)", c1, k1, c2, k2)
	}
	if _, keep := p.FilterUserEntry(map[string]any{"isMeta": true}, "ignored"); keep {
		t.Fatal("isMeta entry must be filtered")
	}

	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	_ = os.MkdirAll(binDir, 0o755)
	_ = os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("HOME", home)

	sess := &Session{
		ID: "claude-code:local:11111111-2222-4333-8444-555555555555", Agent: "claude-code",
		NativeID: "11111111-2222-4333-8444-555555555555", Cwd: "/repo",
		PermissionMode: "plan", Effort: "high", ExtraDirs: []string{"/extra"}, IsSupervisor: true,
	}
	specP, errP := p.BuildTurnSpec(agent.HeadlessTurnInput{Session: sess, HomeDir: home, IsExisting: true, SystemPrompt: BuildSupervisorSystemPrompt(sess.ID)})
	_, argsL, errL := buildPersistentArgs(sess, true)
	if errP != nil || errL != nil || !reflect.DeepEqual(specP.Args, argsL) {
		t.Fatalf("persistent BuildTurnSpec mismatch (%v/%v):\nplugin=%v\nlegacy=%v", errP, errL, specP.Args, argsL)
	}

	hr := &HeadlessRunner{}
	oneP, errOP := p.BuildTurnSpec(agent.HeadlessTurnInput{Session: sess, Prompt: "hello", HomeDir: home, SystemPrompt: BuildSupervisorSystemPrompt(sess.ID)})
	_, oneArgsL, errOL := hr.buildArgs(sess, "hello", home)
	if errOP != nil || errOL != nil || !reflect.DeepEqual(oneP.Args, oneArgsL) {
		t.Fatalf("oneshot BuildTurnSpec mismatch (%v/%v):\nplugin=%v\nlegacy=%v", errOP, errOL, oneP.Args, oneArgsL)
	}

	inP, _ := p.FormatStdinTurn(agent.PersistentTurnInput{Prompt: "ping"})
	inL, _ := formatPersistentStdinTurn("claude-code", sess.ID, true, true, "ping")
	if inP != inL {
		t.Fatalf("FormatStdinTurn mismatch:\nplugin=%s\nlegacy=%s", inP, inL)
	}
	smP, okP := p.FormatSetModel("claude-opus-4-6")
	smL, okL := formatLegacyClaudeSetModel("claude-opus-4-6")
	if !okP || !okL || !strings.Contains(smP, `"subtype":"set_model"`) || !strings.Contains(smL, `"subtype":"set_model"`) {
		t.Fatalf("FormatSetModel mismatch: %s vs %s", smP, smL)
	}
	if p.NormalizeModelID("claude-sonnet-4-6[1m]") != normalizeModelIdentifier("claude-code", "claude-sonnet-4-6[1m]") {
		t.Fatal("NormalizeModelID mismatch")
	}
}

func TestClaudeParity_TranscriptTitleUsageRewindAndScanner(t *testing.T) {
	p := claude.New()
	leg := ClaudeAgentAdapter{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := filepath.Join(home, "proj")
	_ = os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, ".claude", "notes.md"), []byte("# Notes"), 0o644)

	nativeID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(cwd))
	_ = os.MkdirAll(projDir, 0o755)
	jsonl := strings.Join([]string{
		`{"type":"user","sessionId":"` + nativeID + `","timestamp":"2026-11-20T10:00:00Z","message":{"role":"user","content":"Turn 1"}}`,
		`{"type":"assistant","sessionId":"` + nativeID + `","timestamp":"2026-11-20T10:00:02Z","message":{"model":"claude-sonnet-4-6[1m]","usage":{"input_tokens":100,"output_tokens":40,"cache_creation_input_tokens":10,"cache_read_input_tokens":20},"output_tokens_details":{"thinking_tokens":8},"content":[{"type":"text","text":"Working"},{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}]}}`,
		`{"type":"user","sessionId":"` + nativeID + `","timestamp":"2026-11-20T10:01:00Z","message":{"role":"user","content":"Turn 2"}}`,
	}, "\n") + "\n"
	_ = os.WriteFile(filepath.Join(projDir, nativeID+".jsonl"), []byte(jsonl), 0o644)

	trP, err1 := p.ExtractTranscript(home, cwd, nativeID)
	trL, err2 := extractTranscriptWithHome(home, "claude-code", nativeID, cwd)
	if err1 != nil || err2 != nil || len(trP.Messages) != len(trL.Messages) || trP.Messages[1].Content != trL.Messages[1].Content {
		t.Fatalf("ExtractTranscript mismatch: %+v vs %+v (%v/%v)", trP, trL, err1, err2)
	}

	pPath, pSize, pMod, e1 := p.StatTranscript(home, cwd, nativeID)
	lPath, lSize, lMod, e2 := resolveTranscriptFileStat(home, "claude-code", nativeID, cwd)
	if e1 != nil || e2 != nil || pPath != lPath || pSize != lSize || pMod != lMod {
		t.Fatalf("StatTranscript mismatch: (%q,%d,%d,%v) vs (%q,%d,%d,%v)", pPath, pSize, pMod, e1, lPath, lSize, lMod, e2)
	}
	if p.ResolveSessionTitle(home, cwd, nativeID) != ReadClaudeSessionTitle(cwd, nativeID) {
		t.Fatalf("Title mismatch: %q vs %q", p.ResolveSessionTitle(home, cwd, nativeID), ReadClaudeSessionTitle(cwd, nativeID))
	}

	uP, okU := p.ComputeTranscriptUsage(home, cwd, nativeID, 1000000)
	srv := NewServer(nil)
	legacyUsage := srv.computeTranscriptContextAndCost(home, &Session{Agent: "claude-code", NativeID: nativeID, Cwd: cwd})
	if !okU || uP.TurnCount != legacyUsage["turn_count"].(int) || uP.InputTokens != legacyUsage["input_tokens"].(int) ||
		uP.CacheWriteTokens != legacyUsage["cache_write_tokens"].(int) || uP.ThinkingTokens != legacyUsage["thinking_tokens"].(int) {
		t.Fatalf("usage parity mismatch: plugin=%+v legacy=%+v", uP, legacyUsage)
	}

	rewP, _ := p.ListRewindTurns(home, cwd, nativeID)
	rewL, _, _ := srv.extractClaudeRewindTurns(home, &Session{Agent: "claude-code", NativeID: nativeID, Cwd: cwd})
	if len(rewP) != len(rewL) || rewP[0].RawPrompt != rewL[0].RawPrompt {
		t.Fatalf("ListRewindTurns mismatch: %+v vs %+v", rewP, rewL)
	}

	if p.IsIgnoredCommand("claude doctor") != leg.IsIgnoredCommand("claude doctor") ||
		p.MatchProcessCommand("claude", "claude --session-id "+nativeID) != leg.MatchProcessCommand("claude", "claude --session-id "+nativeID) {
		t.Fatal("scanner parity mismatch")
	}
}
