package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDB_SpeechSummaryPersistence(t *testing.T) {
	tempDir := t.TempDir()
	db, err := InitDB(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	summary := &SpeechSummary{
		SessionID:      "claude-code:macmini:101",
		MessageIndex:   2,
		SpokenText:     "The plan is ready with 3 sections.",
		SuggestedChips: []string{"Hear Schema", "Hear Rollout", "Approve Plan"},
		CreatedAt:      time.Now().UTC(),
	}

	if err := db.SaveSpeechSummary(summary); err != nil {
		t.Fatalf("SaveSpeechSummary failed: %v", err)
	}

	retrieved, err := db.GetSpeechSummary("claude-code:macmini:101", 2)
	if err != nil {
		t.Fatalf("GetSpeechSummary failed: %v", err)
	}
	if retrieved == nil {
		t.Fatalf("expected non-nil summary")
	}
	if retrieved.SpokenText != summary.SpokenText {
		t.Errorf("expected %q, got %q", summary.SpokenText, retrieved.SpokenText)
	}
	if len(retrieved.SuggestedChips) != 3 {
		t.Errorf("expected 3 chips, got %d", len(retrieved.SuggestedChips))
	}
	if retrieved.SuggestedChips[0] != "Hear Schema" {
		t.Errorf("expected 'Hear Schema', got %q", retrieved.SuggestedChips[0])
	}

	// Test non-existent entry returns nil without error
	missing, err := db.GetSpeechSummary("non-existent", 99)
	if err != nil || missing != nil {
		t.Errorf("expected (nil, nil) for missing summary, got (%v, %v)", missing, err)
	}
}

func TestBuildStructuralSpeechSummary(t *testing.T) {
	samplePlan := `I have prepared the implementation plan for the Voice Companion layer.

Here is the high-level architecture:
` + "```go\nfunc Run() error { return nil }\n```" + `

┌────────────────────────┐
│   Supervisor Control   │
└────────────────────────┘

## Schema Changes
We need a speech_summaries table in SQLite.

## Daemon Endpoint
We expose POST /v1/sessions/summarize-speech with caching.

## Web Companion UI
Add audio read controls, auto-read toggle, and quick chips.

Should we enable auto-read by default on mobile devices?
`

	spoken, chips := BuildStructuralSpeechSummary(samplePlan)

	if !strings.Contains(spoken, "implementation plan") {
		t.Errorf("expected opening prose in spoken text, got:\n%s", spoken)
	}
	if !strings.Contains(spoken, "covers 3 sections") {
		t.Errorf("expected 'covers 3 sections' in spoken text, got:\n%s", spoken)
	}
	if !strings.Contains(spoken, "Schema Changes") || !strings.Contains(spoken, "Daemon Endpoint") {
		t.Errorf("expected section headings in spoken text, got:\n%s", spoken)
	}
	if !strings.Contains(spoken, "open question") {
		t.Errorf("expected open question mention in spoken text, got:\n%s", spoken)
	}
	if strings.Contains(spoken, "```") || strings.Contains(spoken, "┌") {
		t.Errorf("expected code blocks and box drawing to be stripped, got:\n%s", spoken)
	}

	// Verify suggested chips
	hasSchemaChip := false
	hasQuestionChip := false
	hasApproveChip := false
	for _, chip := range chips {
		if strings.Contains(chip, "Schema Changes") {
			hasSchemaChip = true
		}
		if chip == "Hear Open Question" {
			hasQuestionChip = true
		}
		if chip == "Approve Plan" {
			hasApproveChip = true
		}
	}
	if !hasSchemaChip {
		t.Errorf("expected drill-down chip for Schema Changes, got: %+v", chips)
	}
	if !hasQuestionChip {
		t.Errorf("expected 'Hear Open Question' chip, got: %+v", chips)
	}
	if !hasApproveChip {
		t.Errorf("expected 'Approve Plan' chip, got: %+v", chips)
	}
}

func TestServerSpeech_EndpointAndCaching(t *testing.T) {
	t.Setenv("DISABLE_EXTERNAL_CLI_TESTS", "1")
	tempDir := t.TempDir()
	db, err := InitDB(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := NewServer(db)
	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	content := `## Step 1: Initial Setup
First we initialize the environment.

## Step 2: Implementation
Then we write the code.
`

	reqPayload := SummarizeSpeechRequest{
		SessionID:    "claude-code:macmini:200",
		MessageIndex: 1,
		Content:      content,
	}
	reqBytes, _ := json.Marshal(reqPayload)

	// 1. Initial request -> tier: "structural", cached: false
	resp, err := http.Post(ts.URL+"/v1/sessions/summarize-speech", "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var firstRes SpeechSummary
	if err := json.NewDecoder(resp.Body).Decode(&firstRes); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if firstRes.Cached {
		t.Errorf("expected first call to have cached=false")
	}
	if firstRes.Tier != "structural" {
		t.Errorf("expected structural tier, got %q", firstRes.Tier)
	}
	if len(firstRes.SuggestedChips) == 0 {
		t.Errorf("expected suggested chips in response")
	}

	// 2. Second request -> cached: true
	resp2, err := http.Post(ts.URL+"/v1/sessions/summarize-speech", "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	defer resp2.Body.Close()

	var secondRes SpeechSummary
	_ = json.NewDecoder(resp2.Body).Decode(&secondRes)
	if !secondRes.Cached {
		t.Errorf("expected second call to have cached=true")
	}
	if secondRes.SpokenText != firstRes.SpokenText {
		t.Errorf("expected identical spoken text from cache")
	}

	// 3. Force refresh -> cached: false
	reqPayload.ForceRefresh = true
	reqBytesRefresh, _ := json.Marshal(reqPayload)
	resp3, err := http.Post(ts.URL+"/v1/sessions/summarize-speech", "application/json", bytes.NewReader(reqBytesRefresh))
	if err != nil {
		t.Fatalf("request 3 failed: %v", err)
	}
	defer resp3.Body.Close()

	var thirdRes SpeechSummary
	_ = json.NewDecoder(resp3.Body).Decode(&thirdRes)
	if thirdRes.Cached {
		t.Errorf("expected force_refresh call to have cached=false")
	}

	// 4. Test Injectable speechLLMRunner
	srv.SetSpeechLLMRunner(func(ctx context.Context, content string) (string, []string, error) {
		return "Custom LLM speech synthesis.", []string{"Custom Chip"}, nil
	})

	reqPayload.MessageIndex = 2
	reqPayload.ForceRefresh = false
	reqBytesLLM, _ := json.Marshal(reqPayload)
	resp4, err := http.Post(ts.URL+"/v1/sessions/summarize-speech", "application/json", bytes.NewReader(reqBytesLLM))
	if err != nil {
		t.Fatalf("request 4 failed: %v", err)
	}
	defer resp4.Body.Close()

	var fourthRes SpeechSummary
	_ = json.NewDecoder(resp4.Body).Decode(&fourthRes)
	if fourthRes.Tier != "llm" {
		t.Errorf("expected tier=llm from custom runner, got %q", fourthRes.Tier)
	}
	if fourthRes.SpokenText != "Custom LLM speech synthesis." {
		t.Errorf("unexpected spoken text: %q", fourthRes.SpokenText)
	}
}
