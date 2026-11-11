package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteHeadlessHandover(t *testing.T) {
	srv, db, tmpDir := setupTestCommandsServer(t)
	defer db.Close()

	sess := &Session{
		ID:            "claude-code:local:handover-test-1",
		Agent:         "claude-code",
		Host:          "local",
		NativeID:      "handover-test-1",
		Cwd:           tmpDir,
		EngineType:    EngineHeadless,
		Managed:       true,
		State:         StateIdle,
		FirstPrompt:   "Build an analytics widget",
		CurrentTaskID: "task_handover_42",
		ExtraDirs:     []string{tmpDir},
	}
	_ = db.SaveSession(sess)

	// 1. In-place handover
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/handover", bytes.NewReader([]byte(fmt.Sprintf(`{"id": "%s", "strategy": "in_place", "custom_instruction": "Test thoroughly"}`, sess.ID))))
	w := httptest.NewRecorder()
	srv.handleSessionHandover(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for headless handover, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if res["status"] != "ok" || res["new_session_id"] == nil {
		t.Fatalf("expected status ok and new_session_id, got %+v", res)
	}
	briefing, _ := res["briefing"].(string)
	if !bytes.Contains([]byte(briefing), []byte("Build an analytics widget")) {
		t.Fatalf("expected briefing to contain goal, got %s", briefing)
	}
	if !bytes.Contains([]byte(briefing), []byte("Test thoroughly")) {
		t.Fatalf("expected briefing to contain custom instructions, got %s", briefing)
	}

	// Verify old session marked done
	oldSess, _ := db.GetSession(sess.ID)
	if oldSess == nil || !oldSess.IsDone || oldSess.State != StateEnded {
		t.Fatalf("expected old session marked done/ended, got %+v", oldSess)
	}

	// Verify new session preserved CurrentTaskID and ExtraDirs
	newSessID, _ := res["new_session_id"].(string)
	newSess, _ := db.GetSession(newSessID)
	if newSess == nil {
		t.Fatalf("expected new session to exist in db")
	}
	if newSess.CurrentTaskID != "task_handover_42" {
		t.Fatalf("expected CurrentTaskID preserved, got %q", newSess.CurrentTaskID)
	}
	if len(newSess.ExtraDirs) != 1 || newSess.ExtraDirs[0] != tmpDir {
		t.Fatalf("expected ExtraDirs preserved, got %v", newSess.ExtraDirs)
	}
}

func TestHandleSessionRewind(t *testing.T) {
	srv, db, tmpDir := setupTestCommandsServer(t)
	defer db.Close()

	// Mock claude project transcript
	encodedCwd := encodeClaudeProjectDir(tmpDir)
	projDir := filepath.Join(tmpDir, ".claude", "projects", encodedCwd)
	_ = os.MkdirAll(projDir, 0755)

	nativeID := "rewind-native-uuid-1"
	sess := &Session{
		ID:         "claude-code:local:" + nativeID,
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   nativeID,
		Cwd:        tmpDir,
		EngineType: EngineHeadless,
		Managed:    true,
		State:      StateIdle,
	}
	_ = db.SaveSession(sess)

	transcriptPath := filepath.Join(projDir, nativeID+".jsonl")
	lines := []string{
		`{"type": "user", "message": {"content": "Turn 1: initialize git"}, "timestamp": "2026-11-10T10:00:00Z", "sessionId": "rewind-native-uuid-1"}`,
		`{"type": "assistant", "message": {"content": "Git initialized."}, "timestamp": "2026-11-10T10:00:05Z", "sessionId": "rewind-native-uuid-1"}`,
		`{"type": "attachment", "path": "test.txt", "sessionId": "rewind-native-uuid-1"}`,
		`{"type": "user", "message": {"content": "Turn 2: add documentation"}, "timestamp": "2026-11-10T10:01:00Z", "sessionId": "rewind-native-uuid-1"}`,
		`{"type": "assistant", "message": {"content": "Docs added."}, "timestamp": "2026-11-10T10:01:05Z", "sessionId": "rewind-native-uuid-1"}`,
	}
	_ = os.WriteFile(transcriptPath, []byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n", lines[0], lines[1], lines[2], lines[3], lines[4])), 0600)

	// 1. GET rewind turns -> returns Turn 1 and Turn 2
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/rewind", nil)
	wGet := httptest.NewRecorder()
	srv.handleSessionRewind(wGet, reqGet, sess.ID)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for GET rewind, got %d: %s", wGet.Code, wGet.Body.String())
	}
	var resGet struct {
		TotalTurns int               `json:"total_turns"`
		Turns      []rewindHumanTurn `json:"turns"`
	}
	if err := json.Unmarshal(wGet.Body.Bytes(), &resGet); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resGet.TotalTurns != 2 || len(resGet.Turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", resGet.TotalTurns)
	}

	// 2. POST rewind before Turn 2: clones transcript up to before attachment and Turn 2
	reqPost := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sess.ID+"/rewind", bytes.NewReader([]byte(`{"before_turn": 2}`)))
	wPost := httptest.NewRecorder()
	srv.handleSessionRewind(wPost, reqPost, sess.ID)
	if wPost.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for POST rewind, got %d: %s", wPost.Code, wPost.Body.String())
	}
	var resPost map[string]interface{}
	if err := json.Unmarshal(wPost.Body.Bytes(), &resPost); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resPost["mode"] != "cloned_transcript" {
		t.Fatalf("expected cloned_transcript mode, got %v", resPost["mode"])
	}

	newSessRaw, _ := resPost["session"].(map[string]interface{})
	clonedNativeID, _ := newSessRaw["native_id"].(string)
	if clonedNativeID == "" || clonedNativeID == nativeID {
		t.Fatalf("expected new native ID for cloned transcript, got %q", clonedNativeID)
	}

	clonedFile := filepath.Join(projDir, clonedNativeID+".jsonl")
	clonedBytes, err := os.ReadFile(clonedFile)
	if err != nil {
		t.Fatalf("failed to read cloned transcript file: %v", err)
	}
	// Verify 0600 permissions
	fi, _ := os.Stat(clonedFile)
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("expected 0600 permissions on cloned transcript, got %v", fi.Mode().Perm())
	}
	// Verify cloned transcript does NOT contain Turn 2 or pre-turn attachment
	if bytes.Contains(clonedBytes, []byte("Turn 2")) {
		t.Fatalf("cloned transcript must not contain Turn 2")
	}
	if bytes.Contains(clonedBytes, []byte("test.txt")) {
		t.Fatalf("cloned transcript must not contain pre-turn attachment")
	}
	// Verify top-level sessionId rewrite on cloned line
	if !bytes.Contains(clonedBytes, []byte(clonedNativeID)) {
		t.Fatalf("cloned transcript must have sessionId rewritten to %s", clonedNativeID)
	}
}
