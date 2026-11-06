package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSessionConfig_DBPersistence(t *testing.T) {
	_, db := setupTestServer(t)

	sess := &Session{
		ID:             "sess-config-1",
		Agent:          "claude-code",
		Cwd:            t.TempDir(),
		State:          StateBlocked,
		EngineType:     EngineHeadless,
		Model:          "claude-3-7-sonnet-20250219",
		Effort:         "high",
		PermissionMode: "plan",
		Blocked: &Blocked{
			Kind:     BlockQuestion,
			Question: "Which database to use?",
			Options:  []string{"SQLite", "PostgreSQL"},
			Questions: []BlockedQuestion{
				{
					Question:    "Which database to use?",
					Header:      "Database Selection",
					Options:     []string{"SQLite", "PostgreSQL"},
					MultiSelect: false,
				},
				{
					Question:    "Enable WAL mode?",
					Header:      "Performance",
					Options:     []string{"Yes", "No"},
					MultiSelect: false,
				},
			},
		},
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	got, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if got.Model != sess.Model {
		t.Errorf("expected Model %q, got %q", sess.Model, got.Model)
	}
	if got.Effort != sess.Effort {
		t.Errorf("expected Effort %q, got %q", sess.Effort, got.Effort)
	}
	if got.PermissionMode != sess.PermissionMode {
		t.Errorf("expected PermissionMode %q, got %q", sess.PermissionMode, got.PermissionMode)
	}
	if got.Blocked == nil {
		t.Fatalf("expected Blocked != nil")
	}
	if len(got.Blocked.Questions) != 2 {
		t.Fatalf("expected 2 Blocked.Questions, got %d", len(got.Blocked.Questions))
	}
	if got.Blocked.Questions[0].Question != "Which database to use?" || got.Blocked.Questions[0].Header != "Database Selection" {
		t.Errorf("unexpected first question: %+v", got.Blocked.Questions[0])
	}
	if got.Blocked.Questions[1].Question != "Enable WAL mode?" {
		t.Errorf("unexpected second question: %+v", got.Blocked.Questions[1])
	}

	// Also verify ListSessions
	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	var found *Session
	for _, s := range sessions {
		if s.ID == sess.ID {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatalf("Session %s not found in ListSessions", sess.ID)
	}
	if found.Model != sess.Model || found.Effort != sess.Effort || found.PermissionMode != sess.PermissionMode {
		t.Errorf("ListSessions returned mismatched config fields: %+v", found)
	}
	if len(found.Blocked.Questions) != 2 {
		t.Errorf("ListSessions returned mismatched Blocked.Questions: %+v", found.Blocked)
	}
}

func TestHeadlessRunner_BuildArgs(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)

	// Case 1: Default / bypassPermissions Claude Code
	sess1 := &Session{
		ID:             "s1",
		Agent:          "claude-code",
		PermissionMode: "bypassPermissions",
	}
	_, args1, err := runner.buildArgs(sess1, "hello", "/home/test")
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if !slices.Contains(args1, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions in args1: %v", args1)
	}
	if slices.Contains(args1, "--permission-mode") {
		t.Errorf("did not expect --permission-mode in args1: %v", args1)
	}

	// Case 2: Plan Mode Claude Code with Model and Effort
	sess2 := &Session{
		ID:             "s2",
		Agent:          "claude-code",
		PermissionMode: "plan",
		Model:          "claude-3-7-sonnet-20250219",
		Effort:         "medium",
	}
	_, args2, err := runner.buildArgs(sess2, "hello", "/home/test")
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if slices.Contains(args2, "--dangerously-skip-permissions") {
		t.Errorf("did not expect --dangerously-skip-permissions in plan mode: %v", args2)
	}
	permIdx := slices.Index(args2, "--permission-mode")
	if permIdx == -1 || permIdx >= len(args2)-1 || args2[permIdx+1] != "plan" {
		t.Errorf("expected --permission-mode plan in args2: %v", args2)
	}
	modelIdx := slices.Index(args2, "--model")
	if modelIdx == -1 || modelIdx >= len(args2)-1 || args2[modelIdx+1] != "claude-3-7-sonnet-20250219" {
		t.Errorf("expected --model claude-3-7-sonnet-20250219 in args2: %v", args2)
	}
	effortIdx := slices.Index(args2, "--effort")
	if effortIdx == -1 || effortIdx >= len(args2)-1 || args2[effortIdx+1] != "medium" {
		t.Errorf("expected --effort medium in args2: %v", args2)
	}

	// Case 3: Antigravity agent in plan mode receives --mode plan, does not receive --dangerously-skip-permissions or --effort
	sess3 := &Session{
		ID:             "s3",
		Agent:          "antigravity",
		PermissionMode: "plan",
		Effort:         "high",
	}
	_, args3, err := runner.buildArgs(sess3, "hello", "/home/test")
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if slices.Contains(args3, "--effort") {
		t.Errorf("antigravity should not receive --effort flag: %v", args3)
	}
	modeIdx := slices.Index(args3, "--mode")
	if modeIdx == -1 || modeIdx >= len(args3)-1 || args3[modeIdx+1] != "plan" {
		t.Errorf("expected --mode plan in args3: %v", args3)
	}
	if slices.Contains(args3, "--dangerously-skip-permissions") {
		t.Errorf("antigravity in plan mode should not receive --dangerously-skip-permissions: %v", args3)
	}

	// Case 4: Default Antigravity agent receives --dangerously-skip-permissions
	sess4 := &Session{
		ID:             "s4",
		Agent:          "antigravity",
		PermissionMode: "bypassPermissions",
	}
	_, args4, err := runner.buildArgs(sess4, "hello", "/home/test")
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if !slices.Contains(args4, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions in default antigravity args: %v", args4)
	}
	if slices.Contains(args4, "--mode") {
		t.Errorf("did not expect --mode in default antigravity args: %v", args4)
	}
}

func TestHeadlessRunner_ParseInteractiveToolBlock(t *testing.T) {
	// Test 1: AskUserQuestion with questions array and multiSelect
	input1 := map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"question":    "Select options:",
				"header":      "Config Options",
				"options":     []interface{}{"Opt A", "Opt B", "Opt C"},
				"multiSelect": true,
			},
		},
	}
	b1 := parseInteractiveToolBlock("AskUserQuestion", input1)
	if b1 == nil {
		t.Fatalf("expected b1 != nil")
	}
	if b1.Kind != BlockQuestion {
		t.Errorf("expected BlockQuestion, got %s", b1.Kind)
	}
	if b1.Question != "Select options:" {
		t.Errorf("expected Question 'Select options:', got %q", b1.Question)
	}
	if len(b1.Questions) != 1 || !b1.Questions[0].MultiSelect {
		t.Errorf("expected MultiSelect = true, got: %+v", b1.Questions)
	}

	// Test 2: ExitPlanMode with plan markdown
	input2 := map[string]interface{}{
		"plan": "# Implementation Plan\n\n1. Step one\n2. Step two",
	}
	b2 := parseInteractiveToolBlock("ExitPlanMode", input2)
	if b2 == nil {
		t.Fatalf("expected b2 != nil")
	}
	if b2.Kind != BlockApproval {
		t.Errorf("expected BlockApproval, got %s", b2.Kind)
	}
	if !strings.Contains(b2.Reason, "Implementation Plan") {
		t.Errorf("expected plan markdown in Reason, got %q", b2.Reason)
	}
	if len(b2.Options) != 2 || b2.Options[0] != "Approve Plan" {
		t.Errorf("expected Approve Plan options, got %v", b2.Options)
	}
}

func TestServer_SessionConfigEndpoint(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:             "sess-cfg-api",
		Agent:          "claude-code",
		Cwd:            t.TempDir(),
		State:          StateIdle,
		EngineType:     EngineHeadless,
		PermissionMode: "bypassPermissions",
		StartedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 1. PATCH /v1/sessions/{id}/config
	payload := map[string]string{
		"model":           "claude-3-7-sonnet-20250219",
		"effort":          "high",
		"permission_mode": "plan",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+sess.ID+"/config", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	sessResp := resp
	if s, ok := resp["session"].(map[string]interface{}); ok {
		sessResp = s
	}
	if sessResp["model"] != "claude-3-7-sonnet-20250219" || sessResp["effort"] != "high" || sessResp["permission_mode"] != "plan" {
		t.Errorf("unexpected session in response: %+v", sessResp)
	}

	// Verify persistence in DB
	updated, _ := db.GetSession(sess.ID)
	if updated.Model != "claude-3-7-sonnet-20250219" || updated.Effort != "high" || updated.PermissionMode != "plan" {
		t.Errorf("DB fields not updated: %+v", updated)
	}

	// 2. POST /v1/sessions/control with update_config action
	controlPayload := map[string]interface{}{
		"id":              sess.ID,
		"action":          "update_config",
		"permission_mode": "bypassPermissions",
		"effort":          "low",
	}
	body2, _ := json.Marshal(controlPayload)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/sessions/control", bytes.NewBuffer(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w2.Code, w2.Body.String())
	}

	updated2, _ := db.GetSession(sess.ID)
	if updated2.PermissionMode != "bypassPermissions" || updated2.Effort != "low" {
		t.Errorf("control update_config failed to persist: %+v", updated2)
	}
}

func TestServer_SuggestionsEndpoint(t *testing.T) {
	srv, db := setupTestServer(t)

	tmpDir := t.TempDir()
	// Create sample files in tmpDir
	os.WriteFile(filepath.Join(tmpDir, "server.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# Hello"), 0644)

	sess := &Session{
		ID:         "sess-sug",
		Agent:      "claude-code",
		Cwd:        tmpDir,
		State:      StateIdle,
		EngineType: EngineHeadless,
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 1. Suggestions: kind=commands
	req1 := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/suggestions?kind=commands&q=co", nil)
	w1 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w1.Code, w1.Body.String())
	}

	var commands []CommandSuggestion
	if err := json.Unmarshal(w1.Body.Bytes(), &commands); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(commands) == 0 {
		t.Fatalf("expected command suggestions for query 'co'")
	}
	hasConfig := false
	for _, cmd := range commands {
		if cmd.Name == "config" {
			hasConfig = true
			if cmd.ExecutionMode != "client_action" {
				t.Errorf("expected client_action for /config, got %s", cmd.ExecutionMode)
			}
		}
	}
	if !hasConfig {
		t.Errorf("expected /config in suggestions, got: %+v", commands)
	}

	// 2. Suggestions: kind=files
	req2 := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/suggestions?kind=files&q=server", nil)
	w2 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w2.Code, w2.Body.String())
	}

	var files []FileSuggestion
	if err := json.Unmarshal(w2.Body.Bytes(), &files); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("expected file suggestions for query 'server'")
	}
	if files[0].Name != "server.go" {
		t.Errorf("expected server.go, got: %+v", files[0])
	}
}

func TestServer_RespondApprovePlan(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:             "sess-respond-plan",
		Agent:          "claude-code",
		Cwd:            t.TempDir(),
		State:          StateBlocked,
		EngineType:     EngineHeadless,
		PermissionMode: "plan",
		Blocked: &Blocked{
			Kind:     BlockApproval,
			Question: "Approve Implementation Plan?",
			Reason:   "Detailed Plan",
			Options:  []string{"Approve Plan", "Request Changes"},
		},
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// User responds "Approve Plan"
	payload := map[string]string{
		"id":     sess.ID,
		"action": "approve",
		"answer": "Approve Plan",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/respond", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	// Verify that DB has cleared Blocked and flipped PermissionMode to bypassPermissions
	updated, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if updated.Blocked != nil {
		t.Errorf("expected Blocked to be nil, got: %+v", updated.Blocked)
	}
	if updated.PermissionMode != "bypassPermissions" {
		t.Errorf("expected PermissionMode to flip to bypassPermissions, got %q", updated.PermissionMode)
	}
	if srv.headless != nil {
		srv.headless.ClearInteractiveBlockAndWait(sess.ID, 500*time.Millisecond)
	}
}

func TestServer_RespondTmuxApproveAndReject(t *testing.T) {
	srv, db := setupTestServer(t)

	sess := &Session{
		ID:             "sess-tmux-approval",
		Agent:          "claude-code",
		Cwd:            t.TempDir(),
		State:          StateBlocked,
		EngineType:     EngineTmux,
		Managed:        false, // Avoid real tmux calls in unit test
		PermissionMode: "plan",
		Blocked: &Blocked{
			Kind:     BlockApproval,
			Question: "Approve Plan?",
			Options:  []string{"Approve Plan", "Request Changes"},
		},
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 1. "approve" action
	body1, _ := json.Marshal(map[string]string{
		"id":     sess.ID,
		"action": "approve",
	})
	req1 := httptest.NewRequest(http.MethodPost, "/v1/sessions/respond", bytes.NewBuffer(body1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for approve on tmux session, got %d: %s", w1.Code, w1.Body.String())
	}

	// 2. "reject" action with feedback
	body2, _ := json.Marshal(map[string]string{
		"id":     sess.ID,
		"action": "reject",
		"value":  "Please re-plan using SQLite",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/v1/sessions/respond", bytes.NewBuffer(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for reject on tmux session, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestHeadlessRunner_ClearInteractiveBlockAndWait(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "sess-interactive-test"

	runner.setInteractiveBlock(sessionID, &Blocked{Kind: BlockQuestion, Question: "Continue?"})
	if runner.takeInteractiveBlock(sessionID) == nil {
		t.Fatalf("expected interactive block to exist")
	}

	runner.setInteractiveBlock(sessionID, &Blocked{Kind: BlockQuestion, Question: "Continue?"})
	runner.ClearInteractiveBlockAndWait(sessionID, 100*time.Millisecond)

	if runner.takeInteractiveBlock(sessionID) != nil {
		t.Errorf("expected interactive block to be cleared")
	}
	if !runner.respondedWhileStopping[sessionID] {
		t.Errorf("expected respondedWhileStopping to be true")
	}
}

func TestParseAntigravityModelsOutput(t *testing.T) {
	spinnerOutput := "\r⠋ Fetching available models...\rgemini-4-argon-medium     Gemini 4 Argon (Medium)\ngemini-3.8-flash-high     Gemini 3.8 Flash (High)\n"
	models := parseAntigravityModelsOutput(spinnerOutput)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(models), models)
	}
	if models[0].ID != "gemini-4-argon-medium" || models[0].Label != "Gemini 4 Argon (Medium)" {
		t.Errorf("unexpected model[0]: %+v", models[0])
	}
	if models[1].ID != "gemini-3.8-flash-high" || models[1].Label != "Gemini 3.8 Flash (High)" {
		t.Errorf("unexpected model[1]: %+v", models[1])
	}
}

func TestServer_SuggestionsModelsByAgent(t *testing.T) {
	srv, db := setupTestServer(t)

	// Session 1: Claude Code
	claudeSess := &Session{
		ID:         "sess-sug-claude",
		Agent:      "claude-code",
		Cwd:        t.TempDir(),
		State:      StateIdle,
		EngineType: EngineHeadless,
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.SaveSession(claudeSess); err != nil {
		t.Fatalf("SaveSession claude failed: %v", err)
	}

	// Session 2: Antigravity
	agySess := &Session{
		ID:         "sess-sug-antigravity",
		Agent:      "antigravity",
		Cwd:        t.TempDir(),
		State:      StateIdle,
		EngineType: EngineHeadless,
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.SaveSession(agySess); err != nil {
		t.Fatalf("SaveSession agy failed: %v", err)
	}

	// 1. GET Claude models
	reqClaude := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+claudeSess.ID+"/suggestions?kind=models", nil)
	wClaude := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wClaude, reqClaude)

	if wClaude.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for claude models, got %d: %s", wClaude.Code, wClaude.Body.String())
	}

	var claudeModels []ModelSuggestion
	if err := json.Unmarshal(wClaude.Body.Bytes(), &claudeModels); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(claudeModels) == 0 {
		t.Fatalf("expected non-empty claude models")
	}

	hasClaudeOpus := false
	hasClaudeFable := false
	for _, m := range claudeModels {
		if m.ID == "claude-opus-4-6" {
			hasClaudeOpus = true
		}
		if m.ID == "claude-fable-5-1" {
			hasClaudeFable = true
		}
		if m.ID == "gemini-2.5-pro" || m.ID == "gemini-4-argon-medium" {
			t.Errorf("claude models should not return gemini model %s", m.ID)
		}
	}
	if !hasClaudeOpus || !hasClaudeFable {
		t.Errorf("expected claude-opus-4-6 and claude-fable-5-1 in claude models: %+v", claudeModels)
	}

	// 2. GET Antigravity models
	reqAgy := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+agySess.ID+"/suggestions?kind=models", nil)
	wAgy := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wAgy, reqAgy)

	if wAgy.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for antigravity models, got %d: %s", wAgy.Code, wAgy.Body.String())
	}

	var agyModels []ModelSuggestion
	if err := json.Unmarshal(wAgy.Body.Bytes(), &agyModels); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(agyModels) == 0 {
		t.Fatalf("expected non-empty antigravity models")
	}

	hasGeminiArgon := false
	hasGeminiFlash := false
	for _, m := range agyModels {
		if m.ID == "gemini-4-argon-medium" {
			hasGeminiArgon = true
		}
		if m.ID == "gemini-3.8-flash-high" {
			hasGeminiFlash = true
		}
		if m.ID == "claude-opus-4-6" || m.ID == "claude-3-7-sonnet-20250219" {
			t.Errorf("antigravity models should not return claude code-only model %s", m.ID)
		}
	}
	if !hasGeminiArgon || !hasGeminiFlash {
		t.Errorf("expected gemini-4-argon-medium and gemini-3.8-flash-high in antigravity models: %+v", agyModels)
	}

	// 3. Query filter on Antigravity models: ?kind=models&q=argon
	reqAgyFilter := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+agySess.ID+"/suggestions?kind=models&q=argon", nil)
	wAgyFilter := httptest.NewRecorder()
	srv.Mux().ServeHTTP(wAgyFilter, reqAgyFilter)

	if wAgyFilter.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for filtered antigravity models, got %d: %s", wAgyFilter.Code, wAgyFilter.Body.String())
	}
	var filteredAgy []ModelSuggestion
	if err := json.Unmarshal(wAgyFilter.Body.Bytes(), &filteredAgy); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(filteredAgy) == 0 {
		t.Fatalf("expected non-empty filtered antigravity models for query 'argon'")
	}
	for _, m := range filteredAgy {
		if !strings.Contains(strings.ToLower(m.ID), "argon") && !strings.Contains(strings.ToLower(m.Label), "argon") {
			t.Errorf("filtered model should contain 'argon': %+v", m)
		}
	}
}
