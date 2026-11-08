package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginAccount_TmuxName(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/accounts/claude-code:work/login" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":    "login_spawned",
			"tmux_name": "ackbar-login-claude-work",
		})
	}))
	defer ts.Close()

	session, err := loginAccount(ts.URL, "claude-code:work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session != "ackbar-login-claude-work" {
		t.Errorf("expected ackbar-login-claude-work, got %q", session)
	}
}

func TestLoginAccount_FallbackTmuxSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":       "login_spawned",
			"tmux_session": "fallback-session-123",
		})
	}))
	defer ts.Close()

	session, err := loginAccount(ts.URL, "claude-code:work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session != "fallback-session-123" {
		t.Errorf("expected fallback-session-123, got %q", session)
	}
}

func TestLoginAccount_EmptySessionReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "login_spawned",
		})
	}))
	defer ts.Close()

	_, err := loginAccount(ts.URL, "claude-code:work")
	if err == nil {
		t.Fatal("expected error for empty session name, got nil")
	}
	if err.Error() != "daemon returned empty tmux session name" {
		t.Errorf("unexpected error message: %v", err)
	}
}
