package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ackbar/internal/daemon"
	"ackbar/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.RunIsolatedTestMain(m)
}

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
	dbFile := filepath.Join(cfgDir, "ackbard.db")
	db, err := daemon.InitDB(dbFile)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	_ = db.SetSetting("host_name", "persisted-host")
	workingSess := &daemon.Session{
		ID:         "antigravity:persisted-host:headless-123",
		NativeID:   "headless-123",
		Agent:      "antigravity",
		Host:       "persisted-host",
		EngineType: "headless",
		State:      daemon.StateWorking,
	}
	if err := db.SaveSession(workingSess); err != nil {
		t.Fatalf("failed to save working session: %v", err)
	}
	_ = db.Close()

	if !hasPersistedHostIdentity(tempHome) {
		t.Fatal("expected hasPersistedHostIdentity to return true")
	}
	persistedArgs := buildDaemonRestartArgs(tempHome, "spoofed-host", "Spoofed")
	if len(persistedArgs) != 0 {
		t.Fatalf("expected nil/empty args when host identity is already persisted, got %v", persistedArgs)
	}

	// ReadPersistedHostName must not run InitDB mutations on active sessions
	readHost := daemon.ReadPersistedHostName(dbFile)
	if readHost != "persisted-host" {
		t.Fatalf("expected ReadPersistedHostName 'persisted-host', got %q", readHost)
	}

	verifyRaw, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed to open raw sqlite: %v", err)
	}
	defer verifyRaw.Close()
	var rawState int
	if err := verifyRaw.QueryRow("SELECT state FROM sessions WHERE id = ?", workingSess.ID).Scan(&rawState); err != nil {
		t.Fatalf("failed to query raw session state: %v", err)
	}
	if daemon.State(rawState) != daemon.StateWorking {
		t.Fatalf("expected state %v to remain unaltered, got %v", daemon.StateWorking, rawState)
	}

}

func TestEnsureDaemonRunningWith_DrainDeferredAndAuthHeaders(t *testing.T) {
	if daemonProbeTimeout != 2*time.Second {
		t.Fatalf("expected daemonProbeTimeout == 2s, got %v", daemonProbeTimeout)
	}
	if daemonReadyWaitTimeout != 5*time.Second {
		t.Fatalf("expected daemonReadyWaitTimeout == 5s, got %v", daemonReadyWaitTimeout)
	}

	var versionAuthOK atomic.Bool
	var shutdownAuthOK atomic.Bool
	var shutdownDrainOK atomic.Bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/version":
			if r.Header.Get("Authorization") == "Bearer test-secret-tok" {
				versionAuthOK.Store(true)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"version": "19990101.01",
				"host":    "macmini",
			})
		case "/v1/shutdown":
			if r.Header.Get("Authorization") == "Bearer test-secret-tok" {
				shutdownAuthOK.Store(true)
			}
			if r.URL.Query().Get("drain") == "true" {
				shutdownDrainOK.Store(true)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":           "draining",
				"restart_deferred": true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	managedCalled := false
	spawnCalled := false
	ensureDaemonRunningWith(ts.URL, "test-secret-tok", "20990101.01", func() bool {
		managedCalled = true
		return false
	}, func(_, _ string) {
		spawnCalled = true
	})

	if !versionAuthOK.Load() {
		t.Errorf("expected Authorization: Bearer header on /v1/version probe")
	}
	if !shutdownAuthOK.Load() {
		t.Errorf("expected Authorization: Bearer header on /v1/shutdown?drain=true")
	}
	if !shutdownDrainOK.Load() {
		t.Errorf("expected /v1/shutdown?drain=true query parameter")
	}
	if managedCalled || spawnCalled {
		t.Fatalf("expected restartManaged and spawnFn NOT called when restart_deferred=true (managed=%v spawn=%v)", managedCalled, spawnCalled)
	}

	// Also verify non-deferred mismatch proceeds to managed restart (and fallback spawn)
	var versionCount atomic.Int32
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/version":
			if versionCount.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(map[string]string{"version": "19990101.01", "host": "macmini"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "20990101.01", "host": "macmini"})
		case "/v1/shutdown":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "shutting_down", "restart_deferred": false})
		}
	}))
	defer ts2.Close()

	managedCalled2 := false
	spawnCalled2 := false
	ensureDaemonRunningWith(ts2.URL, "test-secret-tok", "20990101.01", func() bool {
		managedCalled2 = true
		return false
	}, func(host, _ string) {
		spawnCalled2 = host == "macmini"
	})
	if !managedCalled2 || !spawnCalled2 {
		t.Fatalf("expected managedCalled2=true and spawnCalled2=true on non-deferred restart, got managed=%v spawn=%v", managedCalled2, spawnCalled2)
	}
}
