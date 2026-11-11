package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEventsKeepaliveAndFlush(t *testing.T) {
	srv, _ := setupTestServer(t)
	srv.SetEventsKeepaliveInterval(25 * time.Millisecond)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	gotKeepalive := make(chan bool, 1)

	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, ": keepalive") {
				select {
				case gotKeepalive <- true:
				default:
				}
				return
			}
		}
	}()

	select {
	case <-gotKeepalive:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for : keepalive frame")
	}

	sess := &Session{
		ID:        "sess-stream-test",
		Name:      "Stream Test",
		State:     StateIdle,
		Host:      srv.HostName(),
		UpdatedAt: time.Now(),
	}
	srv.broadcast(sess)

	gotBroadcast := make(chan bool, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, "sess-stream-test") {
				select {
				case gotBroadcast <- true:
				default:
				}
				return
			}
		}
	}()

	select {
	case <-gotBroadcast:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast session event")
	}
}

func TestPromptDedup_Unit(t *testing.T) {
	now := time.Now()
	dedup := newPromptDedup()
	dedup.nowFn = func() time.Time { return now }

	res, dup, err := dedup.Reserve(context.Background(), "sess1\x00turn1")
	if err != nil || dup != nil || res == nil {
		t.Fatalf("expected fresh reservation, got res=%v dup=%v err=%v", res, dup, err)
	}
	res.Commit("running")

	res2, dup2, err := dedup.Reserve(context.Background(), "sess1\x00turn1")
	if err != nil || dup2 == nil || res2 != nil {
		t.Fatalf("expected duplicate hit, got res=%v dup=%v err=%v", res2, dup2, err)
	}
	if dup2.Status != "running" {
		t.Errorf("expected status 'running', got %q", dup2.Status)
	}

	res3, dup3, err := dedup.Reserve(context.Background(), "sess1\x00turn2")
	if err != nil || dup3 != nil || res3 == nil {
		t.Fatalf("expected fresh reservation, got err=%v", err)
	}
	res3.Abort()

	res4, dup4, err := dedup.Reserve(context.Background(), "sess1\x00turn2")
	if err != nil || dup4 != nil || res4 == nil {
		t.Fatalf("expected aborted key to be reservable again, got err=%v", err)
	}
	res4.Commit("scheduled")

	// Advance time past 10m TTL
	now = now.Add(11 * time.Minute)
	res5, dup5, err := dedup.Reserve(context.Background(), "sess1\x00turn1")
	if err != nil || dup5 != nil || res5 == nil {
		t.Fatalf("expected expired entry to allow fresh reservation, got dup=%v", dup5)
	}
	res5.Commit("queued")
}

func TestPromptDedup_Eviction(t *testing.T) {
	dedup := newPromptDedup()
	dedup.maxEntries = 5

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("k%d", i)
		res, _, err := dedup.Reserve(context.Background(), key)
		if err != nil {
			t.Fatalf("Reserve failed: %v", err)
		}
		res.Commit("running")
	}

	if len(dedup.entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(dedup.entries))
	}

	// 6th entry should evict oldest (k0)
	res, _, err := dedup.Reserve(context.Background(), "k5")
	if err != nil {
		t.Fatalf("Reserve k5 failed: %v", err)
	}
	res.Commit("running")

	if len(dedup.entries) != 5 {
		t.Fatalf("expected 5 entries after eviction, got %d", len(dedup.entries))
	}
	if _, exists := dedup.entries["k0"]; exists {
		t.Errorf("expected k0 to be evicted")
	}
	if _, exists := dedup.entries["k5"]; !exists {
		t.Errorf("expected k5 to exist")
	}
}

func TestPromptDedup_ConcurrentWait(t *testing.T) {
	dedup := newPromptDedup()
	key := "sess-race\x00turn-race"
	const goroutines = 10

	var wg sync.WaitGroup
	startCh := make(chan struct{})
	reservations := make(chan *DedupReservation, goroutines)
	duplicates := make(chan *DedupResult, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			res, dup, err := dedup.Reserve(context.Background(), key)
			if err != nil {
				return
			}
			if res != nil {
				reservations <- res
				time.Sleep(20 * time.Millisecond)
				res.Commit("running")
			} else if dup != nil {
				duplicates <- dup
			}
		}()
	}

	close(startCh)
	wg.Wait()
	close(reservations)
	close(duplicates)

	var resCount, dupCount int
	for range reservations {
		resCount++
	}
	for d := range duplicates {
		dupCount++
		if d.Status != "running" {
			t.Errorf("expected duplicate status running, got %q", d.Status)
		}
	}

	if resCount != 1 {
		t.Fatalf("expected exactly 1 primary reservation, got %d", resCount)
	}
	if dupCount != goroutines-1 {
		t.Fatalf("expected %d duplicate hits, got %d", goroutines-1, dupCount)
	}
}

func TestHandlePrompt_ClientTurnID(t *testing.T) {
	srv, db := setupTestServer(t)
	sess := &Session{
		ID:         "sess-dedup-test",
		Name:       "Dedup Test",
		EngineType: EngineHeadless,
		State:      StateIdle,
		Host:       srv.HostName(),
		UpdatedAt:  time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 1. Oversized client_turn_id (>128 chars) should return 400
	oversizedID := strings.Repeat("a", 129)
	payload := map[string]interface{}{
		"session_id":     sess.ID,
		"prompt":         "Hello oversized",
		"client_turn_id": oversizedID,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/prompt", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized client_turn_id, got %d", rec.Code)
	}

	// 2. Normal prompt with client_turn_id
	turnID := "ct_12345_abc"
	payload["client_turn_id"] = turnID
	payload["prompt"] = "Hello headless"
	body, _ = json.Marshal(payload)

	req1 := httptest.NewRequest(http.MethodPost, "/v1/sessions/prompt", bytes.NewReader(body))
	rec1 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec1.Code, rec1.Body.String())
	}
	var res1 map[string]interface{}
	_ = json.Unmarshal(rec1.Body.Bytes(), &res1)
	if res1["status"] != "running" {
		t.Errorf("expected status 'running', got %v", res1["status"])
	}
	if res1["deduplicated"] == true {
		t.Errorf("first request should not be deduplicated")
	}

	// 3. Duplicate prompt with identical client_turn_id
	req2 := httptest.NewRequest(http.MethodPost, "/v1/sessions/prompt", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	var res2 map[string]interface{}
	_ = json.Unmarshal(rec2.Body.Bytes(), &res2)
	if res2["status"] != "running" {
		t.Errorf("expected status 'running', got %v", res2["status"])
	}
	if res2["deduplicated"] != true {
		t.Errorf("expected duplicate request to have deduplicated: true")
	}

	// 4. Tmux missing process error aborts reservation so retry works
	tmuxSess := &Session{
		ID:         "sess-tmux-err",
		Name:       "Tmux Err",
		EngineType: EngineTmux,
		TmuxName:   "",
		State:      StateIdle,
		Host:       srv.HostName(),
		UpdatedAt:  time.Now(),
	}
	_ = db.SaveSession(tmuxSess)

	tmuxTurnID := "ct_tmux_1"
	tmuxPayload := map[string]interface{}{
		"session_id":     tmuxSess.ID,
		"prompt":         "Tmux prompt",
		"client_turn_id": tmuxTurnID,
	}
	tmuxBody, _ := json.Marshal(tmuxPayload)

	tmuxReq1 := httptest.NewRequest(http.MethodPost, "/v1/sessions/prompt", bytes.NewReader(tmuxBody))
	tmuxRec1 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(tmuxRec1, tmuxReq1)
	if tmuxRec1.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for tmux without TmuxName, got %d", tmuxRec1.Code)
	}

	// Retry should not be blocked or deduplicated
	tmuxReq2 := httptest.NewRequest(http.MethodPost, "/v1/sessions/prompt", bytes.NewReader(tmuxBody))
	tmuxRec2 := httptest.NewRecorder()
	srv.Mux().ServeHTTP(tmuxRec2, tmuxReq2)
	if tmuxRec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 again on retry, got %d", tmuxRec2.Code)
	}
}

func TestPromptDedup_MultiEntryEviction(t *testing.T) {
	dedup := newPromptDedup()
	dedup.maxEntries = 3
	ctx := context.Background()

	r1, _, err := dedup.Reserve(ctx, "k1")
	if err != nil {
		t.Fatal(err)
	}
	r1.Commit("s1")

	r2, _, err := dedup.Reserve(ctx, "k2")
	if err != nil {
		t.Fatal(err)
	}
	r2.Commit("s2")

	r3, _, err := dedup.Reserve(ctx, "k3")
	if err != nil {
		t.Fatal(err)
	}
	r3.Commit("s3")

	if count := len(dedup.entries); count != 3 {
		t.Fatalf("expected 3 entries, got %d", count)
	}

	dedup.maxEntries = 1
	r4, _, err := dedup.Reserve(ctx, "k4")
	if err != nil {
		t.Fatal(err)
	}
	r4.Commit("s4")

	if count := len(dedup.entries); count != 1 {
		t.Fatalf("expected 1 entry after multi-entry eviction, got %d", count)
	}
	if _, ok := dedup.entries["k4"]; !ok {
		t.Fatalf("expected k4 to remain in dedup cache")
	}
}
