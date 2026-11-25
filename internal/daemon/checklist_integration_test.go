package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
)

func TestTranscriptEndpointAndCache_PreservesChecklist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	srv, db := setupTestServer(t)
	srv.homeDir = home

	t.Run("antigravity json and ndjson plus cache hit", func(t *testing.T) {
		convID := "aaaaaaaa-1111-2222-3333-444444444444"
		logDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatal(err)
		}
		fixture, err := os.ReadFile(filepath.Join("..", "agents", "antigravity", "testdata", "checklist_transcript.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		logPath := filepath.Join(logDir, "transcript.jsonl")
		if err := os.WriteFile(logPath, fixture, 0o644); err != nil {
			t.Fatal(err)
		}

		sessID := "antigravity:" + srv.HostName() + ":" + convID
		if err := db.SaveSession(&Session{
			ID:       sessID,
			NativeID: convID,
			Agent:    "antigravity",
			Host:     srv.HostName(),
			Managed:  true,
			State:    StateIdle,
		}); err != nil {
			t.Fatal(err)
		}

		reqJSON := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sessID+"&format=json&include_formats=false", nil)
		recJSON := httptest.NewRecorder()
		srv.handleSessionTranscript(recJSON, reqJSON)
		if recJSON.Code != http.StatusOK {
			t.Fatalf("json status = %d body=%s", recJSON.Code, recJSON.Body.String())
		}
		var payload struct {
			Messages []TranscriptMessage `json:"messages"`
		}
		if err := json.Unmarshal(recJSON.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal json: %v", err)
		}
		if len(payload.Messages) != 4 {
			t.Fatalf("got %d messages, want 4", len(payload.Messages))
		}
		if len(payload.Messages[1].Checklist) != 2 || payload.Messages[1].Checklist[1].ActiveForm != "Building UI" {
			t.Fatalf("turn1 checklist = %+v", payload.Messages[1].Checklist)
		}
		if payload.Messages[3].Checklist != nil {
			t.Fatalf("turn2 checklist should be nil, got %+v", payload.Messages[3].Checklist)
		}

		// Corrupt disk preserving size/mtime to confirm warm cache serves Checklist intact.
		st, err := os.Stat(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(logPath, bytes.Repeat([]byte("X"), int(st.Size())), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(logPath, st.ModTime(), st.ModTime())

		reqNDJSON := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sessID+"&format=ndjson", nil)
		recNDJSON := httptest.NewRecorder()
		srv.handleSessionTranscript(recNDJSON, reqNDJSON)
		if recNDJSON.Code != http.StatusOK {
			t.Fatalf("ndjson status = %d", recNDJSON.Code)
		}
		var msg1 agent.ChecklistItem
		foundChecklist := false
		sc := bufio.NewScanner(recNDJSON.Body)
		for sc.Scan() {
			var frame struct {
				Type    string            `json:"type"`
				Index   int               `json:"index"`
				Message TranscriptMessage `json:"message"`
			}
			if json.Unmarshal(sc.Bytes(), &frame) == nil && frame.Type == "msg" && frame.Index == 1 {
				if len(frame.Message.Checklist) == 2 {
					msg1 = frame.Message.Checklist[0]
					foundChecklist = true
				}
			}
		}
		if !foundChecklist || msg1.Status != agent.ChecklistDone {
			t.Fatalf("ndjson cached message 1 missing checklist: found=%v item=%+v", foundChecklist, msg1)
		}
	})

	t.Run("claude incremental append preserves and updates checklist", func(t *testing.T) {
		cwd := filepath.Join(home, "repo")
		nativeID := "bbbbbbbb-1111-4222-8333-444444444444"
		projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(cwd))
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			t.Fatal(err)
		}
		baseBytes, err := os.ReadFile(filepath.Join("..", "agents", "claude", "testdata", "checklist_base.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		deltaBytes, err := os.ReadFile(filepath.Join("..", "agents", "claude", "testdata", "checklist_delta.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		logPath := filepath.Join(projDir, nativeID+".jsonl")
		if err := os.WriteFile(logPath, baseBytes, 0o644); err != nil {
			t.Fatal(err)
		}

		sessID := "claude-code:" + srv.HostName() + ":" + nativeID
		if err := db.SaveSession(&Session{
			ID:       sessID,
			NativeID: nativeID,
			Agent:    "claude-code",
			Host:     srv.HostName(),
			Cwd:      cwd,
			Managed:  true,
			State:    StateIdle,
		}); err != nil {
			t.Fatal(err)
		}

		rec1 := httptest.NewRecorder()
		srv.handleSessionTranscript(rec1, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sessID+"&format=json&include_formats=false", nil))
		if rec1.Code != http.StatusOK {
			t.Fatalf("claude json 1 = %d %s", rec1.Code, rec1.Body.String())
		}
		var p1 struct {
			Messages []TranscriptMessage `json:"messages"`
		}
		if err := json.Unmarshal(rec1.Body.Bytes(), &p1); err != nil {
			t.Fatal(err)
		}
		if len(p1.Messages) != 2 || len(p1.Messages[1].Checklist) != 2 || p1.Messages[1].Checklist[0].Status != agent.ChecklistPending {
			t.Fatalf("claude initial checklist = %+v", p1.Messages)
		}

		// Append delta bytes cleanly and confirm incremental cache append updates Checklist.
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(deltaBytes); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		_ = f.Close()

		rec2 := httptest.NewRecorder()
		srv.handleSessionTranscript(rec2, httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sessID+"&format=json&include_formats=false", nil))
		if rec2.Code != http.StatusOK {
			t.Fatalf("claude json 2 = %d %s", rec2.Code, rec2.Body.String())
		}
		var p2 struct {
			Messages []TranscriptMessage `json:"messages"`
		}
		if err := json.Unmarshal(rec2.Body.Bytes(), &p2); err != nil {
			t.Fatal(err)
		}
		if len(p2.Messages) != 2 || len(p2.Messages[1].Checklist) != 2 {
			t.Fatalf("claude appended messages = %+v", p2.Messages)
		}
		if p2.Messages[1].Checklist[0].Status != agent.ChecklistDone || p2.Messages[1].Checklist[1].Status != agent.ChecklistInProgress || p2.Messages[1].Checklist[1].ActiveForm != "Wiring handlers" {
			t.Fatalf("claude appended checklist = %+v", p2.Messages[1].Checklist)
		}
		// Ensure cached base copy from first call was not mutated in-place.
		if p1.Messages[1].Checklist[0].Status != agent.ChecklistPending {
			t.Fatalf("earlier returned slice mutated: %+v", p1.Messages[1].Checklist)
		}
	})
}

func TestHeadlessStreamAndSpoolReplay_PreservesChecklist(t *testing.T) {
	srv, _ := setupTestServer(t)
	reg := srv.PluginRegistry()

	for _, tc := range []struct {
		agentName string
		fixture   string
		wantTitle string
	}{
		{
			agentName: "antigravity",
			fixture:   filepath.Join("..", "agents", "antigravity", "testdata", "checklist_stream.ndjson"),
			wantTitle: "Daemon",
		},
		{
			agentName: "claude-code",
			fixture:   filepath.Join("..", "agents", "claude", "testdata", "checklist_stream.ndjson"),
			wantTitle: "Step 2",
		},
	} {
		t.Run(tc.agentName, func(t *testing.T) {
			rawStream, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var parser agent.StreamParser
			if f, ok := reg.AsStreamParserFactory(tc.agentName); ok {
				parser = f.NewStreamParser("sess-stream-" + tc.agentName)
			}
			ce, ok := reg.AsChecklistExtractor(tc.agentName)
			if !ok {
				t.Fatalf("expected ChecklistExtractor for %s", tc.agentName)
			}

			var emitted []ChatStreamEvent
			parseHeadlessStream("sess-stream-"+tc.agentName, bytes.NewReader(rawStream), streamParseHooks{
				streamParser:       parser,
				checklistExtractor: ce,
				emit: func(evt ChatStreamEvent) {
					emitted = append(emitted, evt)
				},
			})

			if len(emitted) != 1 || emitted[0].Type != "tool_start" || len(emitted[0].Checklist) != 2 {
				t.Fatalf("emitted events = %+v", emitted)
			}
			if emitted[0].Checklist[1].Title != tc.wantTitle || emitted[0].Checklist[1].Status != agent.ChecklistInProgress {
				t.Fatalf("emitted checklist = %+v", emitted[0].Checklist)
			}

			// Persist emitted events into a turn spool and verify synthesis + transcript merge.
			runDir := t.TempDir()
			turnID := "turn_cl_" + tc.agentName
			spoolPath := filepath.Join(runDir, turnID+".ndjson")
			metaPath := filepath.Join(runDir, turnID+".meta.json")
			now := time.Now().UTC()
			sw, err := OpenTurnSpoolWriter(spoolPath, turnID)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = sw.Append(ChatStreamEvent{SessionID: "s1", TurnID: turnID, Type: "turn_start", Text: "Build it", Timestamp: now})
			for _, e := range emitted {
				e.SessionID = "s1"
				e.TurnID = turnID
				_, _ = sw.Append(e)
			}
			_ = sw.Close()

			meta := &TurnSpoolMeta{
				TurnID:    turnID,
				SessionID: "s1",
				Agent:     tc.agentName,
				Prompt:    "Build it",
				SpoolPath: spoolPath,
				MetaPath:  metaPath,
				StartedAt: now,
				Status:    "running",
			}
			_ = WriteTurnSpoolMeta(metaPath, meta)
			srv.headless.mu.Lock()
			srv.headless.sessionSpools["s1"] = meta
			srv.headless.mu.Unlock()

			snap, err := SynthesizeInFlightTurnWithRegistry(reg, meta, tc.agentName)
			if err != nil || !snap.HasChecklist || len(snap.Checklist) != 2 {
				t.Fatalf("SynthesizeInFlightTurnWithRegistry = (%+v, %v)", snap, err)
			}

			merged, inFlightPayload := srv.mergeInFlightSpoolIntoTranscript(nil, &Session{ID: "s1", Agent: tc.agentName, EngineType: EngineHeadless}, "s1", "", tc.agentName, runDir)
			if merged == nil || len(merged.Messages) != 2 {
				t.Fatalf("merged transcript = %+v", merged)
			}
			if len(merged.Messages[1].Checklist) != 2 || merged.Messages[1].Checklist[1].Title != tc.wantTitle {
				t.Fatalf("merged assistant checklist = %+v", merged.Messages[1].Checklist)
			}
			if inFlightPayload == nil {
				t.Fatal("expected non-nil inFlightPayload")
			}
			cl, ok := inFlightPayload["checklist"].([]agent.ChecklistItem)
			if !ok || len(cl) != 2 || cl[1].Title != tc.wantTitle {
				t.Fatalf("inFlightPayload checklist = %#v", inFlightPayload["checklist"])
			}

			// Verify merging onto an existing disk assistant turn updates Checklist too.
			disk := &Transcript{
				SessionID: "s1",
				Agent:     tc.agentName,
				Messages: []TranscriptMessage{
					{Role: "user", Content: "Build it", Timestamp: now},
					{Role: "assistant", Content: "Partial", Timestamp: now.Add(time.Millisecond)},
				},
			}
			mergedExisting, _ := srv.mergeInFlightSpoolIntoTranscript(disk, &Session{ID: "s1", Agent: tc.agentName, EngineType: EngineHeadless}, "s1", "", tc.agentName, runDir)
			if len(mergedExisting.Messages) != 2 || len(mergedExisting.Messages[1].Checklist) != 2 {
				t.Fatalf("mergedExisting checklist = %+v", mergedExisting.Messages)
			}
		})
	}
}

func TestCoalesceTranscriptMessagesForWire_PreservesChecklist(t *testing.T) {
	msgs := []TranscriptMessage{
		{
			Role:      "assistant",
			Content:   "First",
			Checklist: []agent.ChecklistItem{{Title: "Step 1", Status: agent.ChecklistInProgress}},
		},
		{
			Role:      "assistant",
			Content:   "Second",
			Checklist: []agent.ChecklistItem{{Title: "Step 1", Status: agent.ChecklistDone}},
		},
		{
			Role:    "user",
			Content: strings.TrimSpace("Next"),
		},
	}
	out := CoalesceTranscriptMessagesForWire(msgs)
	if len(out) != 2 {
		t.Fatalf("got %d messages, want 2", len(out))
	}
	if len(out[0].Checklist) != 1 || out[0].Checklist[0].Status != agent.ChecklistDone {
		t.Fatalf("coalesced checklist = %+v", out[0].Checklist)
	}
	out[0].Checklist[0].Status = agent.ChecklistFailed
	if msgs[1].Checklist[0].Status != agent.ChecklistDone {
		t.Fatalf("CoalesceTranscriptMessagesForWire mutated source checklist: %+v", msgs[1].Checklist)
	}
}

func TestMultiTurnSeedingAndExplicitClear_Integration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	srv, db := setupTestServer(t)
	srv.homeDir = home
	reg := srv.PluginRegistry()

	// 1. Multi-turn Claude seeding: Turn 1 on disk created 1 & 2, deleted 2 -> [1] with highWater=2.
	cwd := filepath.Join(home, "repo-mt")
	nativeID := "cccccccc-1111-4222-8333-444444444444"
	projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(cwd))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t1Bytes, err := os.ReadFile(filepath.Join("..", "agents", "claude", "testdata", "checklist_turn1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, nativeID+".jsonl"), t1Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	sessID := "claude-code:" + srv.HostName() + ":" + nativeID
	sess := &Session{
		ID:         sessID,
		NativeID:   nativeID,
		Agent:      "claude-code",
		Host:       srv.HostName(),
		Cwd:        cwd,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatal(err)
	}

	// Verify initTurnSpool seeds SeedChecklist and SeedChecklistHighWater from disk Turn 1.
	runDir := t.TempDir()
	turnID := "turn_mt_2"
	now := time.Now().UTC()
	meta, _, err := srv.headless.initTurnSpool(runDir, turnID, sessID, nativeID, "claude-code", cwd, "Finish Alpha and add Gamma", "claude", nil, "", "", "", "", now)
	if err != nil {
		t.Fatalf("initTurnSpool failed: %v", err)
	}
	if len(meta.SeedChecklist) != 1 || meta.SeedChecklist[0].ID != "1" || meta.SeedChecklistHighWater != 2 {
		t.Fatalf("unexpected TurnSpoolMeta seed: items=%+v high=%d cleared=%v", meta.SeedChecklist, meta.SeedChecklistHighWater, meta.SeedChecklistCleared)
	}

	// Live Turn 2 stream mutates Task 1 (from Turn 1 seed) and creates Task 3 (using highWater=2).
	t2Stream, err := os.ReadFile(filepath.Join("..", "agents", "claude", "testdata", "checklist_turn2_update_stream.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	ce, _ := reg.AsChecklistExtractor("claude-code")
	var emitted []ChatStreamEvent
	parseHeadlessStream(sessID, bytes.NewReader(t2Stream), streamParseHooks{
		checklistExtractor:        ce,
		initialChecklist:          meta.SeedChecklist,
		initialChecklistCleared:   meta.SeedChecklistCleared,
		initialChecklistHighWater: meta.SeedChecklistHighWater,
		emit: func(evt ChatStreamEvent) {
			emitted = append(emitted, evt)
		},
	})
	if len(emitted) != 2 {
		t.Fatalf("expected 2 tool_start events in turn 2, got %+v", emitted)
	}
	lastCl := emitted[1].Checklist
	if len(lastCl) != 2 || lastCl[0].ID != "1" || lastCl[0].Status != agent.ChecklistDone || lastCl[1].ID != "3" || lastCl[1].Title != "Gamma" {
		t.Fatalf("seeded turn 2 live checklist = %+v", lastCl)
	}

	// Spool synthesis from raw tool inputs (without pre-populated evt.Checklist) also resolves seed + highWater.
	sw, err := OpenTurnSpoolWriter(meta.SpoolPath, turnID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range emitted {
		// Strip pre-computed checklist to test synthesis tracker path with SeedChecklist too.
		ev.Checklist = nil
		ev.ChecklistCleared = false
		_, _ = sw.Append(ev)
	}
	_ = sw.Close()

	snap, err := SynthesizeInFlightTurnWithRegistry(reg, meta, "claude-code")
	if err != nil || !snap.HasChecklist || len(snap.Checklist) != 2 {
		t.Fatalf("SynthesizeInFlightTurnWithRegistry seeded = (%+v, %v)", snap, err)
	}
	if snap.Checklist[0].Status != agent.ChecklistDone || snap.Checklist[1].ID != "3" {
		t.Fatalf("synthesized seeded checklist = %+v", snap.Checklist)
	}

	// 2. Explicit clear wire flag across stream, spool synthesis, and completed spool suppression.
	for _, tc := range []struct {
		name    string
		agent   string
		fixture string
	}{
		{"antigravity clear", "antigravity", filepath.Join("..", "agents", "antigravity", "testdata", "checklist_clear_stream.ndjson")},
		{"claude clear", "claude-code", filepath.Join("..", "agents", "claude", "testdata", "checklist_clear_stream.ndjson")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var sp agent.StreamParser
			if f, ok := reg.AsStreamParserFactory(tc.agent); ok {
				sp = f.NewStreamParser("s-clr")
			}
			ext, _ := reg.AsChecklistExtractor(tc.agent)
			var clrEvts []ChatStreamEvent
			parseHeadlessStream("s-clr", bytes.NewReader(raw), streamParseHooks{
				streamParser:       sp,
				checklistExtractor: ext,
				initialChecklist:   []agent.ChecklistItem{{ID: "1", Title: "Old", Status: agent.ChecklistInProgress}},
				emit:               func(evt ChatStreamEvent) { clrEvts = append(clrEvts, evt) },
			})
			if len(clrEvts) != 1 || !clrEvts[0].ChecklistCleared || clrEvts[0].Checklist != nil {
				t.Fatalf("expected checklist_cleared=true and nil Checklist, got %+v", clrEvts)
			}
			wireJSON, err := json.Marshal(clrEvts[0])
			if err != nil || !strings.Contains(string(wireJSON), `"checklist_cleared":true`) {
				t.Fatalf("wire JSON missing checklist_cleared: %s err=%v", wireJSON, err)
			}

			clrDir := t.TempDir()
			clrTurn := "turn_clr_" + tc.agent
			clrSpool := filepath.Join(clrDir, clrTurn+".ndjson")
			clrMetaPath := filepath.Join(clrDir, clrTurn+".meta.json")
			cw, err := OpenTurnSpoolWriter(clrSpool, clrTurn)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = cw.Append(ChatStreamEvent{SessionID: "s-clr", TurnID: clrTurn, Type: "turn_start", Text: "Clear steps", Timestamp: now})
			clrEvts[0].SessionID = "s-clr"
			clrEvts[0].TurnID = clrTurn
			_, _ = cw.Append(clrEvts[0])
			_ = cw.Close()

			clrMeta := &TurnSpoolMeta{
				TurnID:        clrTurn,
				SessionID:     "s-clr",
				Agent:         tc.agent,
				Prompt:        "Clear steps",
				SpoolPath:     clrSpool,
				MetaPath:      clrMetaPath,
				StartedAt:     now,
				Status:        "running",
				SeedChecklist: []agent.ChecklistItem{{ID: "1", Title: "Old", Status: agent.ChecklistInProgress}},
			}
			_ = WriteTurnSpoolMeta(clrMetaPath, clrMeta)
			srv.headless.mu.Lock()
			srv.headless.sessionSpools["s-clr"] = clrMeta
			srv.headless.activeTurns["s-clr"] = true
			srv.headless.mu.Unlock()

			mergedLive, inFlight := srv.mergeInFlightSpoolIntoTranscript(&Transcript{
				SessionID: "s-clr",
				Agent:     tc.agent,
				Messages: []TranscriptMessage{
					{Role: "user", Content: "Clear steps", Timestamp: now},
					{Role: "assistant", Content: "Clearing", Checklist: []agent.ChecklistItem{{ID: "1", Title: "Old", Status: agent.ChecklistPending}}, Timestamp: now.Add(time.Millisecond)},
				},
			}, &Session{ID: "s-clr", Agent: tc.agent, EngineType: EngineHeadless}, "s-clr", "", tc.agent, clrDir)
			if len(mergedLive.Messages) != 2 || !mergedLive.Messages[1].ChecklistCleared || len(mergedLive.Messages[1].Checklist) != 0 {
				t.Fatalf("mergedLive should clear stale assistant checklist: %+v", mergedLive.Messages)
			}
			if clearedVal, _ := inFlight["checklist_cleared"].(bool); !clearedVal {
				t.Fatalf("expected in_flight_turn.checklist_cleared=true, got %+v", inFlight)
			}

			// Completed spool must be suppressed once disk transcript reflects ChecklistCleared=true.
			clrMeta.Status = "completed"
			clrMeta.FinishedAt = now.Add(20 * time.Millisecond)
			_ = WriteTurnSpoolMeta(clrMetaPath, clrMeta)
			srv.headless.mu.Lock()
			delete(srv.headless.activeTurns, "s-clr")
			srv.headless.mu.Unlock()
			mergedDone, inFlightDone := srv.mergeInFlightSpoolIntoTranscript(&Transcript{
				SessionID: "s-clr",
				Agent:     tc.agent,
				Messages: []TranscriptMessage{
					{Role: "user", Content: "Clear steps", Timestamp: now},
					{Role: "assistant", Content: "", ChecklistCleared: true, Timestamp: now.Add(time.Millisecond)},
				},
			}, &Session{ID: "s-clr", Agent: tc.agent, EngineType: EngineHeadless}, "s-clr", "", tc.agent, clrDir)
			if inFlightDone != nil || len(mergedDone.Messages) != 2 || !mergedDone.Messages[1].ChecklistCleared {
				t.Fatalf("expected completed cleared-checklist spool to be suppressed cleanly, got inFlight=%+v msgs=%+v", inFlightDone, mergedDone.Messages)
			}
		})
	}
}
