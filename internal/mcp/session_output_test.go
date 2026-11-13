package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestSessionOutput_CursorPagination25KB(t *testing.T) {
	// 25 KB single message
	content25k := strings.Repeat("X", 25000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/sessions/transcript") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"session_id": "sess-25k",
				"title":      "Large Payload",
				"agent":      "claude-code",
				"messages": []any{
					map[string]any{"role": "assistant", "content": content25k},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewDaemonClient(server.URL, "test-token")
	ctx := context.Background()

	// Chunk 1: offset=0, char_offset=0
	r1, err := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id":  "sess-25k",
		"offset":      0,
		"char_offset": 0,
		"max_chars":   10000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t1 := r1.Content[0].Text
	if !strings.Contains(t1, "**Truncated:** true") || !strings.Contains(t1, "offset=0, char_offset=10000") {
		t.Errorf("expected next chunk cursor offset=0, char_offset=10000, got: %s", t1)
	}

	// Chunk 2: offset=0, char_offset=10000
	r2, err := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id":  "sess-25k",
		"offset":      0,
		"char_offset": 10000,
		"max_chars":   10000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t2 := r2.Content[0].Text
	if !strings.Contains(t2, "**Truncated:** true") || !strings.Contains(t2, "offset=0, char_offset=20000") {
		t.Errorf("expected next chunk cursor offset=0, char_offset=20000, got: %s", t2)
	}

	// Chunk 3: offset=0, char_offset=20000 (remaining 5000 runes fit completely)
	r3, err := executeGetSessionOutput(ctx, client, map[string]any{
		"session_id":  "sess-25k",
		"offset":      0,
		"char_offset": 20000,
		"max_chars":   10000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t3 := r3.Content[0].Text
	if !strings.Contains(t3, "**Truncated:** false") {
		t.Errorf("expected final chunk truncated: false, got: %s", t3)
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
