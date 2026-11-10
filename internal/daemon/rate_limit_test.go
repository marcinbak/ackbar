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

func TestParseRateLimitResetTime(t *testing.T) {
	now := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

	// 1. Pipe-delimited epoch
	epochText := "Claude AI usage limit reached|1762736400"
	gotEpoch := ParseRateLimitResetTime(epochText, now)
	expectedEpoch := time.Unix(1762736400, 0).Add(1 * time.Minute)
	if !gotEpoch.Equal(expectedEpoch) {
		t.Errorf("pipe epoch expected %v, got %v", expectedEpoch, gotEpoch)
	}

	// 2. Relative durations
	relCases := []struct {
		text     string
		expected time.Time
	}{
		{"try again in 30 minutes", now.Add(30 * time.Minute).Add(1 * time.Minute)},
		{"limit reached, retry after 60s", now.Add(60 * time.Second).Add(1 * time.Minute)},
		{"resets in 1h 20m", now.Add(80 * time.Minute).Add(1 * time.Minute)},
		{"try again in 2 hours", now.Add(2 * time.Hour).Add(1 * time.Minute)},
	}
	for _, tc := range relCases {
		got := ParseRateLimitResetTime(tc.text, now)
		if !got.Equal(tc.expected) {
			t.Errorf("text %q expected %v, got %v", tc.text, tc.expected, got)
		}
	}

	// 3. Month + Clock time
	monthText := "resets Oct 10 at 2:00pm"
	gotMonth := ParseRateLimitResetTime(monthText, now)
	// Oct 10 is before Nov 10, so it rolls to 2027
	expectedMonth := time.Date(2027, time.October, 10, 14, 0, 0, 0, time.UTC).Add(1 * time.Minute)
	if !gotMonth.Equal(expectedMonth) {
		t.Errorf("month clock expected %v, got %v", expectedMonth, gotMonth)
	}

	// Lowercase month
	monthLowerText := "resets oct 10 at 2:00pm"
	gotMonthLower := ParseRateLimitResetTime(monthLowerText, now)
	if !gotMonthLower.Equal(expectedMonth) {
		t.Errorf("lowercase month clock expected %v, got %v", expectedMonth, gotMonthLower)
	}

	// 4. Clock time (12h): future today (14:30 is 2:30pm, after 10:00)
	clock12Text := "resets at 2:30 PM"
	got12 := ParseRateLimitResetTime(clock12Text, now)
	expected12 := time.Date(2026, 11, 10, 14, 30, 0, 0, time.UTC).Add(1 * time.Minute)
	if !got12.Equal(expected12) {
		t.Errorf("clock 12h future expected %v, got %v", expected12, got12)
	}

	// Rollover test: 2:00am is before 10:00am now, so it must roll over to tomorrow Nov 11
	clockRollover := "resets at 2:00 AM"
	gotRollover := ParseRateLimitResetTime(clockRollover, now)
	expectedRollover := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC).Add(1 * time.Minute)
	if !gotRollover.Equal(expectedRollover) {
		t.Errorf("clock 12h rollover expected %v, got %v", expectedRollover, gotRollover)
	}

	// 5. Clock time (24h)
	clock24Text := "resets 14:00"
	got24 := ParseRateLimitResetTime(clock24Text, now)
	expected24 := time.Date(2026, 11, 10, 14, 0, 0, 0, time.UTC).Add(1 * time.Minute)
	if !got24.Equal(expected24) {
		t.Errorf("clock 24h expected %v, got %v", expected24, got24)
	}

	// 6. Fallback (1 hour)
	fallbackText := "Claude usage limit reached with no time specified"
	gotFallback := ParseRateLimitResetTime(fallbackText, now)
	expectedFallback := now.Add(1 * time.Hour)
	if !gotFallback.Equal(expectedFallback) {
		t.Errorf("fallback expected %v, got %v", expectedFallback, gotFallback)
	}
}

func TestParseDelayDuration(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"15m", 15 * time.Minute, false},
		{"30m", 30 * time.Minute, false},
		{"1h", 1 * time.Hour, false},
		{"2h", 2 * time.Hour, false},
		{"45s", 45 * time.Second, false},
		{"1h30m", 90 * time.Minute, false},
		{"2 hours", 2 * time.Hour, false},
		{"45 minutes", 45 * time.Minute, false},
		{"", 0, true},
		{"invalid", 0, true},
	}

	for _, tc := range cases {
		d, err := ParseDelayDuration(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("input %q expected error, got nil", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("input %q unexpected error: %v", tc.input, err)
			}
			if d != tc.expected {
				t.Errorf("input %q expected %v, got %v", tc.input, tc.expected, d)
			}
		}
	}
}

func TestParseSlashDelayCommand(t *testing.T) {
	cases := []struct {
		input       string
		expectedDur time.Duration
		expectedTxt string
		expectedOk  bool
	}{
		{"/delay 1h Continue", 1 * time.Hour, "Continue", true},
		{"/delay 30m", 30 * time.Minute, "", true},
		{"/delay 2 hours run tests", 2 * time.Hour, "run tests", true},
		{"/delay 1h 30m check status", 90 * time.Minute, "check status", true},
		{"/delay 45s", 45 * time.Second, "", true},
		{"regular prompt without delay", 0, "", false},
		{"/delay", 0, "", false},
		{"/delay invalid", 0, "", false},
	}
	for _, tc := range cases {
		dur, txt, ok := ParseSlashDelayCommand(tc.input)
		if ok != tc.expectedOk {
			t.Errorf("input %q expected ok %v, got %v", tc.input, tc.expectedOk, ok)
		}
		if ok {
			if dur != tc.expectedDur {
				t.Errorf("input %q expected dur %v, got %v", tc.input, tc.expectedDur, dur)
			}
			if txt != tc.expectedTxt {
				t.Errorf("input %q expected txt %q, got %q", tc.input, tc.expectedTxt, txt)
			}
		}
	}
}

func TestDetectRateLimit_Adapters(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	// Claude
	claude := ClaudeAgentAdapter{}
	matchClaude, resetClaude, reasonClaude := claude.DetectRateLimit("Claude 5-hour limit reached · resets at 2:00pm", now)
	if !matchClaude {
		t.Errorf("Claude 5-hour limit should be detected")
	}
	if resetClaude.IsZero() || !strings.Contains(reasonClaude, "5-hour limit reached") {
		t.Errorf("Claude reason/resetAt invalid: reason=%s, resetAt=%v", reasonClaude, resetClaude)
	}

	matchPipe, _, reasonPipe := claude.DetectRateLimit("Claude AI usage limit reached|1762736400", now)
	if !matchPipe || !strings.Contains(reasonPipe, "Usage limit reached") {
		t.Errorf("Claude pipe format should match")
	}

	nonClaude, _, _ := claude.DetectRateLimit("Task completed successfully", now)
	if nonClaude {
		t.Errorf("Normal text should not match Claude rate limit")
	}

	// Antigravity
	anti := AntigravityAgentAdapter{}
	matchAnti, _, reasonAnti := anti.DetectRateLimit("ResourceExhausted: 429 Too Many Requests, quota exceeded", now)
	if !matchAnti || (!strings.Contains(reasonAnti, "Quota exceeded") && !strings.Contains(reasonAnti, "Resource exhausted")) {
		t.Errorf("Antigravity quota exceeded should match, got %v, reason=%s", matchAnti, reasonAnti)
	}

	nonAnti, _, _ := anti.DetectRateLimit("Building project files...", now)
	if nonAnti {
		t.Errorf("Normal text should not match Antigravity rate limit")
	}

	// Other / Codex / Default
	def := defaultAgentAdapter{}
	matchDef, _, reasonDef := def.DetectRateLimit("You've hit your usage limit. Try again in 30 minutes.", now)
	if !matchDef || !strings.Contains(reasonDef, "Usage limit reached") {
		t.Errorf("Default adapter should match usage limit, got %v, reason=%s", matchDef, reasonDef)
	}

	nonDef, _, _ := def.DetectRateLimit("Code compiled without errors", now)
	if nonDef {
		t.Errorf("Normal text should not match default rate limit")
	}

	// Verify that conversational prose with bare phrases does not false positive
	proseCases := []string{
		"We should investigate the rate limit settings in our router.",
		"What is our API usage limit for this customer?",
		"The weekly limit calculation should be based on calendar days.",
		"Check the session limit in postgresql.conf",
	}
	for _, text := range proseCases {
		if matched, _, _ := claude.DetectRateLimit(text, now); matched {
			t.Errorf("Claude adapter false-positive on prose: %q", text)
		}
		if matched, _, _ := anti.DetectRateLimit(text, now); matched {
			t.Errorf("Antigravity adapter false-positive on prose: %q", text)
		}
		if matched, _, _ := def.DetectRateLimit(text, now); matched {
			t.Errorf("Default adapter false-positive on prose: %q", text)
		}
	}
}

func TestBlockRateLimit_DBPersistence(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_ratelimit_db.sqlite")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	resetAt := time.Date(2026, 11, 10, 15, 30, 0, 0, time.UTC)
	sess := &Session{
		ID:        "sess_rl_1",
		TmuxName:  "tmux_rl_1",
		Agent:     "claude",
		Cwd:       "/tmp",
		State:     StateBlocked,
		Activity:  "Rate limit reached",
		StartedAt: time.Now(),
		Blocked: &Blocked{
			Kind:     BlockRateLimit,
			Reason:   "5-hour limit reached · resets 15:30",
			Question: "5-hour limit reached · resets 15:30",
			ResetAt:  &resetAt,
			Options:  []string{"Resume at 15:30", "Resume in 1h"},
			Since:    time.Now(),
		},
	}

	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	loaded, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if loaded.State != StateBlocked {
		t.Errorf("expected StateBlocked, got %s", loaded.State)
	}
	if loaded.Blocked == nil {
		t.Fatalf("expected Blocked struct, got nil")
	}
	if loaded.Blocked.Kind != BlockRateLimit {
		t.Errorf("expected BlockRateLimit kind, got %s", loaded.Blocked.Kind)
	}
	if loaded.Blocked.ResetAt == nil {
		t.Fatalf("expected Blocked.ResetAt, got nil")
	}
	if !loaded.Blocked.ResetAt.Equal(resetAt) {
		t.Errorf("expected ResetAt %v, got %v", resetAt, *loaded.Blocked.ResetAt)
	}
}

func TestScheduledPrompts_DB(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_sched_db.sqlite")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	deliverAt := time.Date(2026, 11, 10, 16, 0, 0, 0, time.UTC)
	item := &PromptQueueItem{
		ID:        "sched_item_1",
		SessionID: "sess_test",
		Text:      "Run integration tests",
		DeliverAt: &deliverAt,
		CreatedAt: time.Now(),
	}

	if err := db.SaveScheduledPrompt(item); err != nil {
		t.Fatalf("SaveScheduledPrompt failed: %v", err)
	}

	list, err := db.ListScheduledPrompts()
	if err != nil {
		t.Fatalf("ListScheduledPrompts failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 scheduled prompt, got %d", len(list))
	}
	if list[0].ID != item.ID || list[0].Text != item.Text || list[0].DeliverAt == nil || !list[0].DeliverAt.Equal(deliverAt) {
		t.Errorf("loaded scheduled item mismatch: %+v", list[0])
	}

	// Delete
	if err := db.DeleteScheduledPrompt(item.ID); err != nil {
		t.Fatalf("DeleteScheduledPrompt failed: %v", err)
	}
	listAfter, err := db.ListScheduledPrompts()
	if err != nil {
		t.Fatalf("ListScheduledPrompts failed: %v", err)
	}
	if len(listAfter) != 0 {
		t.Errorf("expected 0 scheduled prompts after delete, got %d", len(listAfter))
	}
}

func TestHeadlessRunner_DelayedQueue(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_runner_db.sqlite")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	runner := NewHeadlessRunner(db, nil)
	sessionID := "test:sess:123"

	// 1. Enqueue prompt with future DeliverAt
	future := time.Now().Add(1 * time.Hour)
	delayedItem := runner.EnqueueDelayedPrompt(sessionID, "Future Prompt", &future)
	if delayedItem == nil || delayedItem.DeliverAt == nil {
		t.Fatalf("failed to enqueue delayed prompt")
	}

	// DequeuePrompt must return nil because DeliverAt is in the future
	head := runner.DequeuePrompt(sessionID)
	if head != nil {
		t.Fatalf("expected DequeuePrompt to return nil for future item, got %v", head)
	}

	// 2. Trigger send now
	triggered := runner.TriggerPromptQueueItemNow(sessionID, delayedItem.ID)
	if triggered == nil {
		t.Fatalf("TriggerPromptQueueItemNow returned nil")
	}
	if triggered.DeliverAt != nil {
		t.Errorf("expected DeliverAt to be cleared after trigger send now")
	}

	// Now DequeuePrompt should return it
	popped := runner.DequeuePrompt(sessionID)
	if popped == nil || popped.ID != delayedItem.ID {
		t.Fatalf("expected popped item %s, got %v", delayedItem.ID, popped)
	}

	// 3. Test LoadScheduledPrompts into fresh runner
	future2 := time.Now().Add(2 * time.Hour)
	item2 := &PromptQueueItem{
		ID:        "item_persist_2",
		SessionID: sessionID,
		Text:      "Persisted prompt",
		DeliverAt: &future2,
		CreatedAt: time.Now(),
	}
	if err := db.SaveScheduledPrompt(item2); err != nil {
		t.Fatalf("SaveScheduledPrompt failed: %v", err)
	}

	runner2 := NewHeadlessRunner(db, nil)
	runner2.LoadScheduledPrompts()

	q, _ := runner2.GetPromptQueue(sessionID)
	if len(q) != 1 {
		t.Fatalf("expected 1 item loaded into runner2 queue, got %d", len(q))
	}
	if q[0].ID != item2.ID || q[0].Text != item2.Text || q[0].DeliverAt == nil {
		t.Errorf("loaded item mismatch: %+v", q[0])
	}
}

func TestHandlePrompt_DelayAndScheduled(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_chat_delay.sqlite")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	sess := &Session{
		ID:         "sess_chat_delay",
		EngineType: EngineHeadless,
		Agent:      "claude",
		Cwd:        "/tmp",
		State:      StateIdle,
		StartedAt:  time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	server := NewServer(db)

	// 1. Slash /delay command syntax
	body := map[string]interface{}{
		"session_id": sess.ID,
		"prompt":     "/delay 1h Continue the implementation",
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/sessions/prompt", bytes.NewReader(data))
	w := httptest.NewRecorder()
	server.handlePrompt(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res["status"] != "scheduled" {
		t.Errorf("expected status 'scheduled', got %v", res["status"])
	}
	itemID, _ := res["id"].(string)
	if itemID == "" {
		t.Errorf("expected non-empty scheduled id")
	}

	// 2. Queue inspect
	reqQ := httptest.NewRequest("GET", "/v1/sessions/prompt/queue?session_id="+sess.ID, nil)
	wQ := httptest.NewRecorder()
	server.handlePromptQueue(wQ, reqQ)
	if wQ.Code != http.StatusOK {
		t.Fatalf("queue get failed: %d", wQ.Code)
	}
	var qRes map[string]interface{}
	_ = json.Unmarshal(wQ.Body.Bytes(), &qRes)
	qList, _ := qRes["items"].([]interface{})
	if len(qList) != 1 {
		t.Fatalf("expected 1 item in queue, got %d", len(qList))
	}

	// 3. POST action send_now
	sendNowBody := map[string]interface{}{
		"session_id": sess.ID,
		"item_id":    itemID,
		"action":     "send_now",
	}
	snb, _ := json.Marshal(sendNowBody)
	reqSendNow := httptest.NewRequest("POST", "/v1/sessions/prompt/queue", bytes.NewReader(snb))
	wSendNow := httptest.NewRecorder()
	server.handlePromptQueue(wSendNow, reqSendNow)
	if wSendNow.Code != http.StatusOK {
		t.Fatalf("send_now failed: %d: %s", wSendNow.Code, wSendNow.Body.String())
	}
}

func TestProcessDueScheduledPrompts(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_due_prompts.sqlite")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	resetAt := time.Now().Add(-5 * time.Minute)
	sess := &Session{
		ID:         "sess_due_1",
		EngineType: EngineHeadless,
		Agent:      "claude",
		Cwd:        "/tmp",
		State:      StateBlocked,
		Activity:   "Rate limited",
		StartedAt:  time.Now(),
		Blocked: &Blocked{
			Kind:    BlockRateLimit,
			Reason:  "Rate limit",
			ResetAt: &resetAt,
			Since:   time.Now().Add(-10 * time.Minute),
		},
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	server := NewServer(db)

	// Enqueue prompt with DeliverAt in the past
	past := time.Now().Add(-1 * time.Minute)
	server.headless.EnqueueDelayedPrompt(sess.ID, "Resume work after quota reset", &past)

	// Session is blocked on rate limit; processDueScheduledPrompts must unblock and clear the block
	ctx := context.Background()
	server.processDueScheduledPrompts(ctx)

	updatedSess, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if updatedSess.Blocked != nil {
		t.Errorf("expected Blocked to be cleared, got %+v", updatedSess.Blocked)
	}
}

func TestHeadlessRunner_DelayedQueue_OutOfOrder(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_runner_ooo.sqlite")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	runner := NewHeadlessRunner(db, nil)
	sessionID := "test:sess:ooo"

	// 1. Enqueue item 1 with future DeliverAt (+1 hour)
	future := time.Now().Add(1 * time.Hour)
	delayedItem1 := runner.EnqueueDelayedPrompt(sessionID, "Delayed 1h", &future)
	if delayedItem1 == nil {
		t.Fatalf("failed to enqueue delayedItem1")
	}

	// 2. Enqueue item 2 immediate (DeliverAt == nil)
	runner.EnqueuePrompt(sessionID, "Immediate prompt")

	// 3. Enqueue item 3 with future DeliverAt (+2 hours)
	future2 := time.Now().Add(2 * time.Hour)
	delayedItem2 := runner.EnqueueDelayedPrompt(sessionID, "Delayed 2h", &future2)
	if delayedItem2 == nil {
		t.Fatalf("failed to enqueue delayedItem2")
	}

	// DequeuePrompt must pop item 2 (Immediate prompt) without being blocked by delayedItem1!
	popped := runner.DequeuePrompt(sessionID)
	if popped == nil || popped.Text != "Immediate prompt" {
		t.Fatalf("expected immediate prompt to dequeue first, got %v", popped)
	}

	// Next DequeuePrompt returns nil because both remaining items are in the future
	if next := runner.DequeuePrompt(sessionID); next != nil {
		t.Fatalf("expected nil when all remaining items are in the future, got %v", next)
	}

	// 4. Trigger delayedItem2 (which is currently second in queue behind delayedItem1)
	triggered := runner.TriggerPromptQueueItemNow(sessionID, delayedItem2.ID)
	if triggered == nil {
		t.Fatalf("failed to trigger delayedItem2")
	}

	// TriggerPromptQueueItemNow must have moved delayedItem2 to front of queue and cleared DeliverAt
	popped2 := runner.DequeuePrompt(sessionID)
	if popped2 == nil || popped2.ID != delayedItem2.ID {
		t.Fatalf("expected triggered delayedItem2 to dequeue next, got %v", popped2)
	}
}

func TestProcessDueScheduledPrompts_DoesNotDispatchBlockedPermission(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_perm_block.sqlite")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	sess := &Session{
		ID:         "sess_perm_1",
		EngineType: EngineHeadless,
		Agent:      "claude",
		Cwd:        "/tmp",
		State:      StateBlocked,
		Activity:   "Waiting for permission",
		StartedAt:  time.Now(),
		Blocked: &Blocked{
			Kind:     BlockPermission,
			Reason:   "Tool permission requested",
			Question: "Permission required",
			Options:  []string{"Allow", "Deny"},
			Since:    time.Now().Add(-5 * time.Minute),
		},
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	server := NewServer(db)

	// Enqueue prompt with DeliverAt in the past
	past := time.Now().Add(-1 * time.Minute)
	server.headless.EnqueueDelayedPrompt(sess.ID, "Prompt waiting for dispatch", &past)

	ctx := context.Background()
	server.processDueScheduledPrompts(ctx)

	// Session must REMAIN in StateBlocked with BlockPermission
	updatedSess, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if updatedSess.State != StateBlocked || updatedSess.Blocked == nil || updatedSess.Blocked.Kind != BlockPermission {
		t.Errorf("session state must remain StateBlocked with BlockPermission, got state=%s, blocked=%+v", updatedSess.State, updatedSess.Blocked)
	}

	// Queue must still contain the prompt (not dispatched while permission is pending)
	items, _ := server.headless.GetPromptQueue(sess.ID)
	if len(items) != 1 {
		t.Errorf("prompt must remain in queue while session is blocked on permission, got %d items", len(items))
	}
}
