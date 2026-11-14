package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func extractAssistantMessage(output string) string {
	marker := "#### Message 1 (assistant):\n"
	idx := strings.Index(output, marker)
	if idx == -1 {
		return ""
	}
	content := output[idx+len(marker):]
	if truncIdx := strings.Index(content, "\n\n[Output truncated."); truncIdx != -1 {
		return content[:truncIdx]
	}
	return strings.TrimRight(content, "\n")
}

func TestSessionOutput_PaginationAndTruncation(t *testing.T) {
	longMsg := strings.Repeat("A", 12000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/sessions/transcript") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"session_id": "sess-1",
				"title":      "Test Task",
				"agent":      "claude-code",
				"messages": []any{
					map[string]any{"role": "assistant", "content": longMsg},
				},
			})
			return
		}
		if r.URL.Path == "/v1/sessions" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "sess-1", "state": "1"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewDaemonClient(server.URL, "test-token")
	ctx := context.Background()

	// 1. Default 10 KB call truncates 12 KB message
	res, err := executeGetSessionOutput(ctx, client, map[string]any{"session_id": "sess-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outText := res.Content[0].Text
	if !strings.Contains(outText, "**Truncated:** true") {
		t.Errorf("expected truncated: true in header, got: %s", outText)
	}
	if !strings.Contains(outText, "[Output truncated.") {
		t.Errorf("expected continuation notice, got: %s", outText)
	}

	// 2. max_chars=50000 returns full 12 KB message without truncation
	res50k, err := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id": "sess-1",
		"max_chars":  50000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out50kText := res50k.Content[0].Text
	if !strings.Contains(out50kText, "**Truncated:** false") {
		t.Errorf("expected truncated: false with 50k budget, got: %s", out50kText)
	}
	if strings.Contains(out50kText, "[Output truncated.") {
		t.Errorf("did not expect continuation notice with 50k budget")
	}

	// 3. Clamp >50000 -> 50000 and <500 -> 500
	resOver, _ := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id": "sess-1",
		"max_chars":  99999,
	})
	if !strings.Contains(resOver.Content[0].Text, "max_chars=50000") {
		t.Errorf("expected max_chars clamped to 50000")
	}
	resUnder, _ := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id": "sess-1",
		"max_chars":  100,
	})
	if !strings.Contains(resUnder.Content[0].Text, "max_chars=500") {
		t.Errorf("expected max_chars clamped to 500")
	}
}

func TestSessionOutput_SequentialPaginationWithDaemonPagination(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < 25000; i++ {
		builder.WriteByte(byte('a' + (i % 26)))
	}
	content25k := builder.String()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/sessions/transcript") {
			q := r.URL.Query()
			charOffset := 0
			if v, err := strconv.Atoi(q.Get("char_offset")); err == nil {
				charOffset = v
			}
			maxChars := 10000
			if v, err := strconv.Atoi(q.Get("max_chars")); err == nil {
				maxChars = v
			}

			runes := []rune(content25k)
			avail := runes[charOffset:]
			budget := maxChars
			if budget > len(avail) {
				budget = len(avail)
			}
			chunk := string(avail[:budget])
			truncated := (charOffset + budget) < len(runes)
			nextCharOffset := 0
			if truncated {
				nextCharOffset = charOffset + budget
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"session_id": "sess-25k-daemon",
				"title":      "25k Runes Daemon Pagination",
				"agent":      "claude-code",
				"messages": []any{
					map[string]any{"role": "assistant", "content": chunk},
				},
				"pagination": map[string]any{
					"total_messages":   1,
					"returned_count":   1,
					"range_start":      1,
					"range_end":        1,
					"offset":           0,
					"next_offset":      0,
					"char_offset":      charOffset,
					"next_char_offset": nextCharOffset,
					"max_chars":        maxChars,
					"returned_chars":   len(chunk),
					"truncated":        truncated,
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewDaemonClient(server.URL, "test-token")
	ctx := context.Background()

	// Call 1: char_offset=0, max_chars=10000
	r1, err := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id":  "sess-25k-daemon",
		"char_offset": 0,
		"max_chars":   10000,
	})
	if err != nil {
		t.Fatalf("call 1 failed: %v", err)
	}
	c1 := extractAssistantMessage(r1.Content[0].Text)
	if len(c1) != 10000 {
		t.Fatalf("call 1 expected 10000 chars, got: %d", len(c1))
	}
	if !strings.Contains(r1.Content[0].Text, "**Truncated:** true") || !strings.Contains(r1.Content[0].Text, "char_offset=10000") {
		t.Errorf("call 1 expected continuation notice for char_offset=10000")
	}

	// Call 2: char_offset=10000, max_chars=10000
	r2, err := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id":  "sess-25k-daemon",
		"char_offset": 10000,
		"max_chars":   10000,
	})
	if err != nil {
		t.Fatalf("call 2 failed: %v", err)
	}
	c2 := extractAssistantMessage(r2.Content[0].Text)
	if len(c2) != 10000 {
		t.Fatalf("call 2 expected 10000 chars, got: %d", len(c2))
	}
	if !strings.Contains(r2.Content[0].Text, "**Truncated:** true") || !strings.Contains(r2.Content[0].Text, "char_offset=20000") {
		t.Errorf("call 2 expected continuation notice for char_offset=20000")
	}

	// Call 3: char_offset=20000, max_chars=10000
	r3, err := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id":  "sess-25k-daemon",
		"char_offset": 20000,
		"max_chars":   10000,
	})
	if err != nil {
		t.Fatalf("call 3 failed: %v", err)
	}
	c3 := extractAssistantMessage(r3.Content[0].Text)
	if len(c3) != 5000 {
		t.Fatalf("call 3 expected 5000 chars, got: %d", len(c3))
	}
	if !strings.Contains(r3.Content[0].Text, "**Truncated:** false") {
		t.Errorf("call 3 expected truncated: false")
	}

	// Assert concatenated 3 chunks equal the exact original 25,000-rune message
	concatenated := c1 + c2 + c3
	if len(concatenated) != len(content25k) {
		t.Fatalf("concatenated length mismatch: got %d, expected %d", len(concatenated), len(content25k))
	}
	if concatenated != content25k {
		t.Fatalf("concatenated 3 chunks do not match original 25k content!")
	}
}

func TestSessionOutput_OldIDAndBlockedRouting(t *testing.T) {
	var requestedTranscriptID string
	var respondPayload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/sessions":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":        "antigravity:host1:real-uuid-1234",
					"old_id":    "antigravity:local:real-uuid-1234",
					"native_id": "real-uuid-1234",
					"state":     "blocked",
					"blocked":   map[string]any{"kind": "approval"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/v1/sessions/transcript"):
			requestedTranscriptID = r.URL.Query().Get("id")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"session_id": "antigravity:host1:real-uuid-1234",
				"old_id":     "antigravity:local:real-uuid-1234",
				"title":      "Blocked Session",
				"agent":      "antigravity",
				"messages": []any{
					map[string]any{"role": "assistant", "content": "Need confirmation to run bash"},
				},
			})
		case r.URL.Path == "/v1/sessions/respond":
			_ = json.NewDecoder(r.Body).Decode(&respondPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewDaemonClient(server.URL, "test-token")
	ctx := context.Background()

	// 1. get_session_output with old_id resolves to canonical id
	_, err := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id": "antigravity:local:real-uuid-1234",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestedTranscriptID != "antigravity:host1:real-uuid-1234" {
		t.Errorf("expected canonical session ID in transcript request, got: %s", requestedTranscriptID)
	}

	// 2. executeSendSessionInput with old_id routes to /v1/sessions/respond for blocked session
	res, err := executeSendSessionInput(ctx, client, map[string]any{
		"session_id": "antigravity:local:real-uuid-1234",
		"input":      "yes, proceed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, "Answered blocked session:") {
		t.Errorf("expected response to blocked session, got: %s", res.Content[0].Text)
	}
	if respondPayload["session_id"] != "antigravity:host1:real-uuid-1234" {
		t.Errorf("expected canonical session_id in respond payload, got: %v", respondPayload["session_id"])
	}
}
