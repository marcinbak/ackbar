package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"ackbar/internal/daemon"
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

func TestDaemonRestartValidationAndArgs(t *testing.T) {
	if !isValidDisplayName("MacBook Air") {
		t.Error("expected 'MacBook Air' to be valid display name")
	}
	if isValidDisplayName("") || isValidDisplayName("Mac\nAir") || isValidDisplayName("Mac\x00Air") || isValidDisplayName("Mac\x7f") {
		t.Error("expected control characters and empty string to be invalid display names")
	}

	if !validHostNameRegex.MatchString("macmini") || !validHostNameRegex.MatchString("mac-mini.local") {
		t.Error("expected valid host names to match regex")
	}
	if validHostNameRegex.MatchString("") || validHostNameRegex.MatchString("-mac") || validHostNameRegex.MatchString("mac mini") {
		t.Error("expected invalid host names to fail regex match")
	}

	tempHome := t.TempDir()
	// No DB exists: args should be constructed from valid values
	args := buildDaemonRestartArgs(tempHome, "macmini", "MacBook Air")
	expected := []string{"--host-name", "macmini", "--display-name", "MacBook Air"}
	if len(args) != len(expected) || args[0] != expected[0] || args[1] != expected[1] || args[2] != expected[2] || args[3] != expected[3] {
		t.Fatalf("expected args %v, got %v", expected, args)
	}

	// Invalid host and invalid display name are omitted
	badArgs := buildDaemonRestartArgs(tempHome, "bad host!", "bad\nname")
	if len(badArgs) != 0 {
		t.Fatalf("expected empty args for invalid host and display name, got %v", badArgs)
	}

	// Persisted DB setting prevents overriding with command line flags
	cfgDir := filepath.Join(tempHome, ".config", "ackbar")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	db, err := daemon.InitDB(filepath.Join(cfgDir, "ackbard.db"))
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	_ = db.SetSetting("host_name", "persisted-host")
	_ = db.Close()

	if !hasPersistedHostIdentity(tempHome) {
		t.Fatal("expected hasPersistedHostIdentity to return true")
	}
	persistedArgs := buildDaemonRestartArgs(tempHome, "spoofed-host", "Spoofed")
	if len(persistedArgs) != 0 {
		t.Fatalf("expected nil/empty args when host identity is already persisted, got %v", persistedArgs)
	}
}
