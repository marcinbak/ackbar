package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestHostIdentity_PersistenceAndPreservation(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "ackbard.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	srv := NewServer(db)
	sessID := "claude-code:macmini:sess-123"
	sess := &Session{
		ID:          sessID,
		NativeID:    "sess-123",
		Name:        "Test Session",
		Agent:       "claude-code",
		Host:        "macmini",
		Cwd:         "/work/proj",
		Managed:     true,
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	srv.SetHostIdentity("macmini", "MacMini")

	hSetting, err := db.GetSetting("host_name")
	if err != nil || hSetting != "macmini" {
		t.Fatalf("expected host_name 'macmini', got %q (err: %v)", hSetting, err)
	}
	dSetting, err := db.GetSetting("display_name")
	if err != nil || dSetting != "MacMini" {
		t.Fatalf("expected display_name 'MacMini', got %q (err: %v)", dSetting, err)
	}

	_ = db.Close()

	t.Setenv("ACKBAR_HOST", "")
	t.Setenv("ACKBAR_DISPLAY_NAME", "")

	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Second InitDB failed: %v", err)
	}
	defer db2.Close()

	srv2 := NewServer(db2)
	if srv2.HostName() != "macmini" {
		t.Fatalf("expected srv2.HostName() == 'macmini', got %q", srv2.HostName())
	}
	if srv2.DisplayName() != "MacMini" {
		t.Fatalf("expected srv2.DisplayName() == 'MacMini', got %q", srv2.DisplayName())
	}

	preserved, err := db2.GetSession(sessID)
	if err != nil || preserved == nil {
		t.Fatalf("expected session %s to be preserved after restart, got nil (err: %v)", sessID, err)
	}
	if preserved.Host != "macmini" {
		t.Fatalf("expected preserved session host 'macmini', got %q", preserved.Host)
	}
}

func TestInferExistingLocalHostName(t *testing.T) {
	if got := InferExistingLocalHostName(nil); got != "" {
		t.Fatalf("expected empty string for nil db, got %q", got)
	}

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "ackbard.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	remotePeer := &HostRecord{
		Name: "legion",
		URL:  "http://192.168.1.100:7777",
	}
	if err := db.SaveHost(remotePeer); err != nil {
		t.Fatalf("SaveHost failed: %v", err)
	}

	remoteSess := &Session{
		ID:          "claude-code:legion:remote-001",
		NativeID:    "remote-001",
		Agent:       "claude-code",
		Host:        "legion",
		Managed:     true,
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(remoteSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	if got := InferExistingLocalHostName(db); got != "" {
		t.Fatalf("expected empty inference for only remote peer sessions, got %q", got)
	}

	sess1 := &Session{
		ID:          "claude-code:macmini:sess-1",
		NativeID:    "sess-1",
		Agent:       "claude-code",
		Host:        "macmini",
		Managed:     true,
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	sess2 := &Session{
		ID:          "claude-code:macmini:sess-2",
		NativeID:    "sess-2",
		Agent:       "claude-code",
		Host:        "macmini",
		Managed:     true,
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(sess1)
	_ = db.SaveSession(sess2)

	hSetting, _ := db.GetSetting("host_name")
	if hSetting != "" {
		t.Fatalf("expected empty host_name setting, got %q", hSetting)
	}

	if got := InferExistingLocalHostName(db); got != "macmini" {
		t.Fatalf("expected InferExistingLocalHostName to return 'macmini', got %q", got)
	}

	conflictSess := &Session{
		ID:          "claude-code:otherbox:sess-3",
		NativeID:    "sess-3",
		Agent:       "claude-code",
		Host:        "otherbox",
		Managed:     true,
		State:       StateIdle,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	_ = db.SaveSession(conflictSess)
	if got := InferExistingLocalHostName(db); got != "" {
		t.Fatalf("expected empty inference on host conflict, got %q", got)
	}
}

type testRecoveryProvider struct {
	MockProvider
	agent string
	meta  *SessionMeta
}

func (p *testRecoveryProvider) Agent() string {
	if p.agent != "" {
		return p.agent
	}
	return "claude-code"
}

func (p *testRecoveryProvider) ReadSessionMetadata(cwd, nativeID string) *SessionMeta {
	return p.meta
}

func TestReconstructManagedTmuxSession(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "ackbard.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	_, err = db.db.Exec(`INSERT INTO tasks (id, title, group_name, project_name, status, created_at, updated_at) VALUES ('TASK-100', 'Task 100', 'test-group', 'test-proj', 'IN_PROGRESS', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`)
	if err != nil {
		t.Fatalf("Failed to insert task: %v", err)
	}
	sessID := "claude-code:macmini:native-12345678"
	_, err = db.db.Exec(`INSERT INTO task_workers (task_id, session_id, agent, host, is_active, assigned_at, repo_node_path, workflow_node)
		VALUES ('TASK-100', ?, 'claude-code', 'macmini', 1, CURRENT_TIMESTAMP, 'nodes/ios', 'N3');`, sessID)
	if err != nil {
		t.Fatalf("Failed to insert task_worker: %v", err)
	}

	srv := NewServer(db)
	prov := &testRecoveryProvider{
		agent: "claude-code",
		meta: &SessionMeta{
			Title:             "Fix Login Flow",
			Entrypoint:        "cli",
			Kind:              "main",
			Version:           "2.0.1",
			ContextPct:        62,
			LastUserMessageAt: time.Now().Add(-10 * time.Minute),
		},
	}
	srv.RegisterProvider(prov)

	reconstructed := srv.reconstructManagedTmuxSession("macmini", "claude-code", "native-12345678", "ackbar-claude-code-native-12345678", "/repo/path", 4321, "ACKBAR_TASK_ID=\"TASK-CMD-OVERRIDE\" claude")
	if reconstructed == nil {
		t.Fatalf("expected reconstructed session, got nil")
	}
	if reconstructed.ID != sessID {
		t.Fatalf("expected ID %q, got %q", sessID, reconstructed.ID)
	}
	if !reconstructed.Managed {
		t.Fatalf("expected Managed == true")
	}
	if reconstructed.Name != "Fix Login Flow" {
		t.Fatalf("expected Name 'Fix Login Flow', got %q", reconstructed.Name)
	}
	if reconstructed.CurrentTaskID != "TASK-100" {
		t.Fatalf("expected CurrentTaskID 'TASK-100' from task_workers, got %q", reconstructed.CurrentTaskID)
	}
	if reconstructed.NodePath != "nodes/ios" {
		t.Fatalf("expected NodePath 'nodes/ios' from task_workers, got %q", reconstructed.NodePath)
	}
	if reconstructed.ContextPct != 62 {
		t.Fatalf("expected ContextPct 62, got %d", reconstructed.ContextPct)
	}

	scanState := &ScanState{
		hostName:        "macmini",
		knownIDs:        make(map[string]*Session),
		knownByPID:      make(map[int]*Session),
		knownByNativeID: make(map[string]*Session),
		knownByTmux:     make(map[string]*Session),
	}
	srv.adoptExistingTmuxSession(context.Background(), scanState, reconstructed, "claude-code", "ackbar-claude-code-native-12345678", "/repo/path", 4321, 4320)

	saved, err := db.GetSession(sessID)
	if err != nil || saved == nil {
		t.Fatalf("expected adopted session to be saved in DB, got nil (err: %v)", err)
	}
	if !saved.Managed {
		t.Fatalf("expected saved session Managed == true")
	}
	if saved.Name != "Fix Login Flow" {
		t.Fatalf("expected saved session Name 'Fix Login Flow', got %q", saved.Name)
	}
	if saved.CurrentTaskID != "TASK-100" {
		t.Fatalf("expected saved CurrentTaskID 'TASK-100', got %q", saved.CurrentTaskID)
	}

	orphanSess := srv.reconstructManagedTmuxSession("macmini", "claude-code", "native-87654321", "ackbar-claude-code-native-87654321", "/repo/path", 5432, "ACKBAR_TASK_ID=\"TASK-FALLBACK\" claude")
	if orphanSess.CurrentTaskID != "TASK-FALLBACK" {
		t.Fatalf("expected CurrentTaskID 'TASK-FALLBACK' from pane command line, got %q", orphanSess.CurrentTaskID)
	}
}

func TestShellQuoteArgs(t *testing.T) {
	input := []string{"--display-name", "MacBook Air", "--token", "abc'def;rm -rf /", "--flag=value with spaces"}
	quoted := shellQuoteArgs(input)
	expected := `'--display-name' 'MacBook Air' '--token' 'abc'\''def;rm -rf /' '--flag=value with spaces'`
	if quoted != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, quoted)
	}

	if empty := shellQuoteArgs(nil); empty != "" {
		t.Fatalf("expected empty string for nil args, got %q", empty)
	}
}
