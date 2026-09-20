package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFallbackResolve(t *testing.T) {
	req := ResolveRequest{
		Prompt: "PROJ-123 Fix connection leak in auth database",
		Hosts: []CandidateHost{
			{Name: "local", URL: "http://127.0.0.1:7777"},
			{Name: "legion", URL: "http://legion:7777"},
		},
		Nodes: []CandidateNode{
			{Path: "Ackbar/Backend", ProjectDir: "/work/backend"},
			{Path: "Ackbar/Mobile", ProjectDir: "/work/mobile"},
		},
		Sessions: []CandidateSession{
			{ID: "sess-1", Name: "PROJ-123: Fix connection leak", Agent: "claude-code", Host: "local"},
		},
	}

	result := FallbackResolve(req)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// 1. Should match existing session by ticket ID
	if result.MatchedSessionID != "sess-1" {
		t.Errorf("Expected MatchedSessionID 'sess-1', got %q", result.MatchedSessionID)
	}

	// 2. Should derive backend group
	if result.NodePath != "Ackbar/Backend" {
		t.Errorf("Expected NodePath 'Ackbar/Backend', got %q", result.NodePath)
	}
	if result.Cwd != "/work/backend" {
		t.Errorf("Expected Cwd '/work/backend', got %q", result.Cwd)
	}

	// 3. Should default to claude-code for backend tasks
	if result.Agent != "claude-code" {
		t.Errorf("Expected Agent 'claude-code', got %q", result.Agent)
	}

	// 4. Should derive local host
	if result.Host != "local" {
		t.Errorf("Expected Host 'local', got %q", result.Host)
	}
}

func TestFallbackResolve_MobileAndRemote(t *testing.T) {
	req := ResolveRequest{
		Prompt: "Update mobile drawer navigation on legion with flutter animations",
		Hosts: []CandidateHost{
			{Name: "local", URL: "http://127.0.0.1:7777"},
			{Name: "legion", URL: "http://legion:7777"},
		},
		Nodes: []CandidateNode{
			{Path: "Ackbar/Backend", ProjectDir: "/work/backend"},
			{Path: "Ackbar/Mobile", ProjectDir: "/work/mobile"},
		},
	}

	result := FallbackResolve(req)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// Should derive legion host because it's mentioned
	if result.Host != "legion" {
		t.Errorf("Expected Host 'legion', got %q", result.Host)
	}

	// Should derive antigravity because flutter/mobile is mentioned
	if result.Agent != "antigravity" {
		t.Errorf("Expected Agent 'antigravity', got %q", result.Agent)
	}

	// Should derive Ackbar/Mobile group
	if result.NodePath != "Ackbar/Mobile" {
		t.Errorf("Expected NodePath 'Ackbar/Mobile', got %q", result.NodePath)
	}
}

func TestJevClient_Classify(t *testing.T) {
	// Mock Jev AI API server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var payload jevRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := jevResponse{
			Answers: map[string]jevChoiceAnswer{
				"host": {
					Choice:     "legion",
					Confidence: 0.98,
				},
				"group": {
					Choice:     "Ackbar/Backend",
					Confidence: 0.94,
				},
				"agent": {
					Choice:     "claude-code",
					Confidence: 0.96,
				},
				"existing_session": {
					Choice:     "none",
					Confidence: 0.99,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &JevClient{
		APIKey:     "test-key",
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	req := ResolveRequest{
		Prompt: "Train embeddings and verify postgres sync on legion",
		Hosts: []CandidateHost{
			{Name: "local"},
			{Name: "legion"},
		},
		Nodes: []CandidateNode{
			{Path: "Ackbar/Backend", ProjectDir: "/work/backend"},
		},
		APIKey: "test-key",
	}

	res, err := client.classify(context.Background(), req)
	if err != nil {
		t.Fatalf("classify failed: %v", err)
	}

	if res.Host != "legion" {
		t.Errorf("Expected Host 'legion', got %q", res.Host)
	}
	if res.NodePath != "Ackbar/Backend" {
		t.Errorf("Expected NodePath 'Ackbar/Backend', got %q", res.NodePath)
	}
	if res.Agent != "claude-code" {
		t.Errorf("Expected Agent 'claude-code', got %q", res.Agent)
	}
	if res.MatchedSessionID != "" {
		t.Errorf("Expected empty MatchedSessionID for 'none', got %q", res.MatchedSessionID)
	}
	if res.Confidence < 0.95 {
		t.Errorf("Expected confidence >= 0.95, got %f", res.Confidence)
	}
}
