package daemon

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ackbar/internal/tmux"
)

type paginationMockProvider struct {
	MockProvider
	agentName string
	messages  []TranscriptMessage
}

func (m *paginationMockProvider) Agent() string {
	return m.agentName
}

func (m *paginationMockProvider) ExtractTranscript(home, cwd, nativeID string) ([]TranscriptMessage, error) {
	return m.messages, nil
}

func TestTranscriptPagination_LegacyCompatibility(t *testing.T) {
	srv, db := setupTestServer(t)

	mockProv := &paginationMockProvider{
		agentName: "mock-agent",
		messages: []TranscriptMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
			{Role: "assistant", Content: "Done"},
		},
	}
	srv.RegisterProvider(mockProv)

	sess := &Session{
		ID:        "mock-agent:local:leg-1",
		NativeID:  "leg-1",
		Agent:     "mock-agent",
		Host:      "local",
		State:     StateIdle,
		StartedAt: time.Now().UTC(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id=mock-agent:local:leg-1", nil)
	rec := httptest.NewRecorder()
	srv.handleSessionTranscript(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got: %d (%s)", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if _, ok := resp["pagination"]; ok {
		t.Errorf("unpaginated request should not contain 'pagination' field")
	}
	if _, ok := resp["truncated"]; ok {
		t.Errorf("unpaginated request should not contain 'truncated' field")
	}
	msgs, ok := resp["messages"].([]any)
	if !ok || len(msgs) != 3 {
		t.Errorf("expected 3 messages, got: %v", resp["messages"])
	}
}

func TestTranscriptPagination_RuneSafetyAndParams(t *testing.T) {
	srv, db := setupTestServer(t)

	multiByteText := "こんにちは世界🌍🚀"
	repeated := strings.Repeat(multiByteText, 100) // 900 runes

	mockProv := &paginationMockProvider{
		agentName: "mock-agent",
		messages: []TranscriptMessage{
			{Role: "assistant", Content: repeated},
		},
	}
	srv.RegisterProvider(mockProv)

	sess := &Session{
		ID:        "mock-agent:local:utf8-1",
		NativeID:  "utf8-1",
		Agent:     "mock-agent",
		Host:      "local",
		State:     StateIdle,
		StartedAt: time.Now().UTC(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id=mock-agent:local:utf8-1&max_chars=500&offset=0", nil)
	rec := httptest.NewRecorder()
	srv.handleSessionTranscript(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got: %d", rec.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if tr, _ := resp["truncated"].(bool); !tr {
		t.Errorf("expected truncated: true")
	}
	pag, ok := resp["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("expected pagination object")
	}
	retChars := int(pag["returned_chars"].(float64))
	if retChars != 500 {
		t.Errorf("expected returned_chars: 500, got: %d", retChars)
	}
	nextCharOffset := int(pag["next_char_offset"].(float64))
	if nextCharOffset != 500 {
		t.Errorf("expected next_char_offset: 500, got: %d", nextCharOffset)
	}

	msgs := resp["messages"].([]any)
	returnedContent := msgs[0].(map[string]any)["content"].(string)
	if len([]rune(returnedContent)) != 500 {
		t.Errorf("expected 500 runes in returned message, got %d", len([]rune(returnedContent)))
	}
}

func TestTranscriptPagination_OldIDResolution(t *testing.T) {
	srv, db := setupTestServer(t)

	mockProv := &paginationMockProvider{
		agentName: "mock-agent",
		messages: []TranscriptMessage{
			{Role: "assistant", Content: "Hello from migrated session"},
		},
	}
	srv.RegisterProvider(mockProv)

	sess := &Session{
		ID:        "mock-agent:host1:new-native",
		OldID:     "mock-agent:local:old-native",
		NativeID:  "new-native",
		Agent:     "mock-agent",
		Host:      "local",
		State:     StateIdle,
		StartedAt: time.Now().UTC(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id=mock-agent:local:old-native", nil)
	rec := httptest.NewRecorder()
	srv.handleSessionTranscript(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got: %d", rec.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if resp["session_id"] != "mock-agent:host1:new-native" {
		t.Errorf("expected effective session_id, got: %v", resp["session_id"])
	}
	if resp["old_id"] != "mock-agent:local:old-native" {
		t.Errorf("expected old_id in response, got: %v", resp["old_id"])
	}
}

func TestTranscriptPagination_TmuxFallback(t *testing.T) {
	if !tmux.IsTmuxInstalled() {
		t.Skip("tmux not installed")
	}

	srv, db := setupTestServer(t)

	tmuxName := "test-tmux-fallback-pr2a"
	ctx := context.Background()
	_ = tmux.Kill(ctx, tmuxName)
	if err := tmux.Spawn(ctx, tmuxName, "", "echo 'Hello from tmux scrollback'; sleep 30"); err != nil {
		t.Fatalf("failed to spawn tmux session: %v", err)
	}
	t.Cleanup(func() {
		_ = tmux.Kill(context.Background(), tmuxName)
	})

	time.Sleep(300 * time.Millisecond)

	sess := &Session{
		ID:        "mock-agent:local:tmux-fallback-sess",
		NativeID:  "tmux-fallback-sess",
		Agent:     "mock-agent",
		Host:      "local",
		TmuxName:  tmuxName,
		State:     StateWorking,
		StartedAt: time.Now().UTC(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/transcript?id="+sess.ID, nil)
	rec := httptest.NewRecorder()
	srv.handleSessionTranscript(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got: %d (%s)", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if resp["source"] != "tmux_capture" {
		t.Errorf("expected source: tmux_capture, got: %v", resp["source"])
	}
	msgs, ok := resp["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("expected synthesized message from tmux capture, got: %v", resp["messages"])
	}
	content := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(content, "Hello from tmux scrollback") {
		t.Errorf("expected tmux output in synthesized message, got: %s", content)
	}
}

func TestTranscriptPagination_IntegerOverflowProtection(t *testing.T) {
	msgs := []TranscriptMessage{
		{Role: "assistant", Content: "Msg 0"},
		{Role: "assistant", Content: "Msg 1"},
	}
	opts := TranscriptPaginationOptions{
		HasPagination: true,
		HasOffset:     true,
		Offset:        1,
		Limit:         math.MaxInt,
	}
	res, pag := PaginateTranscriptMessages(msgs, opts)
	if len(res) != 1 || res[0].Content != "Msg 1" {
		t.Fatalf("expected 1 message (Msg 1), got: %v", res)
	}
	if pag.Offset != 1 || pag.ReturnedCount != 1 {
		t.Fatalf("unexpected pagination: %+v", pag)
	}
}
