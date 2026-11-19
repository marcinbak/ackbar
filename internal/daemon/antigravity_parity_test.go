package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agents/antigravity"
)

func TestAntigravityParity_RateLimitPromptAndStream(t *testing.T) {
	p := antigravity.New()
	leg := AntigravityAgentAdapter{}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	for _, txt := range []string{"Quota exceeded, resets in 10 minutes", "ResourceExhausted error", "429 Too Many Requests", "normal output"} {
		lim1, rt1, rsn1 := p.DetectRateLimit(txt, now)
		lim2, rt2, rsn2 := leg.DetectRateLimit(txt, now)
		if lim1 != lim2 || !rt1.Equal(rt2) || rsn1 != rsn2 {
			t.Fatalf("DetectRateLimit(%q) mismatch: plugin=(%v,%v,%q) legacy=(%v,%v,%q)", txt, lim1, rt1, rsn1, lim2, rt2, rsn2)
		}
	}

	raw := "<CONTEXT_SUMMARY>old</CONTEXT_SUMMARY>\n<USER_REQUEST>Real task\nWith <tag>inside</tag></USER_REQUEST>"
	c1, k1 := p.FilterUserEntry(map[string]any{"source": "USER_EXPLICIT"}, raw)
	c2, k2 := leg.FilterUserEntry(map[string]any{"source": "USER_EXPLICIT"}, raw)
	if c1 != c2 || k1 != k2 || c1 != "Real task\nWith <tag>inside</tag>" {
		t.Fatalf("FilterUserEntry mismatch: (%q,%v) vs (%q,%v)", c1, k1, c2, k2)
	}
	if _, keep := p.FilterUserEntry(map[string]any{"source": "SYSTEM"}, "ignored"); keep {
		t.Fatal("expected SYSTEM source rejected")
	}

	streamLines := strings.Join([]string{
		`{"event":"init","conversation_id":"conv-1","model":"gemini-4-carbon-high"}`,
		`{"event":"step_update","step_update":{"step_index":1,"step_type":"agent_response","state":"ACTIVE","text_delta":"Part A"}}`,
		`{"event":"step_update","step_update":{"step_index":1,"step_type":"agent_response","state":"DONE","text_delta":"Part A"}}`,
		`{"event":"step_update","step_update":{"step_index":2,"step_type":"tool","state":"ACTIVE","tool_name":"run_command","tool_info":{"args":{"command":"ls"}}}}`,
		`{"event":"step_update","step_update":{"step_index":2,"step_type":"tool","state":"DONE","tool_name":"run_command","tool_info":{"output":"ok"}}}`,
		`{"event":"step_update","step_update":{"step_index":3,"step_type":"agent_response","state":"DONE","text_delta":"Part B"}}`,
		`{"event":"result","result":{"status":"SUCCESS","response":"Final"}}`,
	}, "\n")

	var pluginEvts, legacyEvts []string
	var pFinal, lFinal string
	var pErr, lErr bool
	runStream := func(sp agent.StreamParser) {
		var b strings.Builder
		var res string
		var hadErr bool
		parseHeadlessStream("s-1", strings.NewReader(streamLines), streamParseHooks{
			emitTurnComplete: true,
			streamParser:     sp,
			emit: func(ev ChatStreamEvent) {
				if sp != nil {
					pluginEvts = append(pluginEvts, ev.Type+":"+ev.Text+":"+ev.ToolName)
				} else {
					legacyEvts = append(legacyEvts, ev.Type+":"+ev.Text+":"+ev.ToolName)
				}
			},
			onTurnResult: func(lastText, resultText string, streamHadError bool) {
				if sp != nil {
					pFinal, pErr = lastText, streamHadError
				} else {
					lFinal, lErr = lastText, streamHadError
				}
			},
		})
		_, _ = b.String(), res
		_ = hadErr
	}
	runStream(p.NewStreamParser("s-1"))
	runStream(nil)
	if !reflect.DeepEqual(pluginEvts, legacyEvts) || pErr != lErr || strings.TrimSpace(pFinal) == "" || strings.TrimSpace(lFinal) == "" {
		t.Fatalf("stream mismatch:\nplugin=%v (err=%v)\nlegacy=%v (err=%v)", pluginEvts, pErr, legacyEvts, lErr)
	}
}

func TestAntigravityParity_ArgsTranscriptTitleAndSubagents(t *testing.T) {
	p := antigravity.New()
	home := t.TempDir()
	t.Setenv("HOME", home)
	convID := "11111111-2222-3333-4444-555555555555"
	subID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
	subDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "subagents")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.MkdirAll(subDir, 0o755)

	compact := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"<USER_REQUEST>Build API</USER_REQUEST>","input_tokens":300,"cache_read_tokens":50}` + "\n" +
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","content":"trunc","truncated_fields":["content"],"input_tokens":800,"output_tokens":40}` + "\n"
	full := `{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","content":"Full output"}` + "\n"
	_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(compact), 0o644)
	_ = os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(full), 0o644)
	_ = os.WriteFile(filepath.Join(subDir, subID+".json"), []byte(`{"conversationId":"`+subID+`","subagentDescriptor":{"typeName":"auditor","role":"Security Auditor"},"state":"RUNNING"}`), 0o644)

	subLogDir := filepath.Join(home, ".gemini", "antigravity", "brain", subID, ".system_generated", "logs")
	_ = os.MkdirAll(subLogDir, 0o755)
	_ = os.WriteFile(filepath.Join(subLogDir, "transcript.jsonl"), []byte(`{"step_index":1,"type":"PLANNER_RESPONSE","tool_calls":[{"name":"ask_permission","args":{"command":"git status"}}]}`+"\n"), 0o644)

	trP, err1 := p.ExtractTranscript(home, "", convID)
	msgsL, err2 := ExtractAntigravityTranscriptMessages(home, convID)
	if err1 != nil || err2 != nil || len(trP.Messages) != len(msgsL) || trP.Messages[1].Content != msgsL[1].Content {
		t.Fatalf("ExtractTranscript mismatch: %+v vs %+v (%v/%v)", trP, msgsL, err1, err2)
	}
	pPath, pSize, pMod, pStatErr := p.StatTranscript(home, "", convID)
	lPath, lSize, lMod, lStatErr := resolveTranscriptFileStat(home, "antigravity", convID, "")
	if pStatErr != nil || lStatErr != nil || pPath != lPath || pSize != lSize || pMod != lMod {
		t.Fatalf("StatTranscript mismatch: (%q,%d,%d,%v) vs (%q,%d,%d,%v)", pPath, pSize, pMod, pStatErr, lPath, lSize, lMod, lStatErr)
	}
	if p.ResolveSessionTitle(home, "", convID) != ReadAntigravitySessionTitle("", convID) {
		t.Fatalf("Title mismatch: %q vs %q", p.ResolveSessionTitle(home, "", convID), ReadAntigravitySessionTitle("", convID))
	}
	pPar, pRole, pType := p.ResolveSubagentParent(home, subID)
	lPar, lRole, lType := ResolveAntigravitySubagentParent(home, subID)
	if pPar != lPar || pRole != lRole || pType != lType {
		t.Fatalf("ResolveSubagentParent mismatch: (%q,%q,%q) vs (%q,%q,%q)", pPar, pRole, pType, lPar, lRole, lType)
	}
	bP, actP, okP := p.InspectSubagentBlock(home, subID, time.Time{})
	bL, actL, okL, _ := inspectAntigravityTranscriptFileBlocked(filepath.Join(subLogDir, "transcript.jsonl"))
	if okP != okL || actP != actL || bP.Reason != bL.Reason || bP.Kind != bL.Kind {
		t.Fatalf("InspectSubagentBlock mismatch: (%+v,%q,%v) vs (%+v,%q,%v)", bP, actP, okP, bL, actL, okL)
	}

	sess := &Session{ID: "antigravity:local:s1", Agent: "antigravity", NativeID: convID, PermissionMode: "acceptEdits", Model: "Gemini 4 Carbon (High)", ExtraDirs: []string{"/tmp/ext"}}
	specP, _ := p.BuildTurnSpec(agent.HeadlessTurnInput{Session: sess, IsExisting: true})
	_, argsL, _ := buildPersistentArgs(sess, true)
	if !reflect.DeepEqual(specP.Args, argsL) {
		t.Fatalf("persistent args mismatch:\nplugin=%v\nlegacy=%v", specP.Args, argsL)
	}
	s1P, _ := p.FormatStdinTurn(agent.PersistentTurnInput{Agent: "antigravity", SessionID: sess.ID, IsSupervisor: true, IsFirstTurn: true, SystemPrompt: BuildSupervisorSystemPrompt(sess.ID), Prompt: "go"})
	s1L, _ := formatPersistentStdinTurn("antigravity", sess.ID, true, true, "go")
	if s1P != s1L {
		t.Fatalf("FormatStdinTurn mismatch:\nplugin=%s\nlegacy=%s", s1P, s1L)
	}
	if p.NormalizeModelID("Gemini 4 Carbon (High)") != normalizeModelIdentifier("antigravity", "Gemini 4 Carbon (High)") {
		t.Fatal("NormalizeModelID mismatch")
	}
	_ = context.Background()
	_ = bytes.NewBuffer(nil)
}
