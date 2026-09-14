package daemon

import (
	"os"
	"testing"
	"time"
)

func TestDB_TreeNodesAndHosts(t *testing.T) {
	dbFile := "./test_nodes.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// 1. Test Node Operations
	node1 := &TreeNode{
		Path:       "Work/ProjectY/ProjectY-web/analytics-integration",
		ProjectDir: "/path/to/project-web",
		GitURL:     "https://github.com/mode/project-web.git",
		CreatedAt:  time.Now(),
	}

	if err := db.SaveNode(node1); err != nil {
		t.Fatalf("SaveNode failed: %v", err)
	}

	nodes, err := db.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("Expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Path != node1.Path {
		t.Errorf("Expected path %s, got %s", node1.Path, nodes[0].Path)
	}

	// 2. Test Host Operations
	host1 := &HostRecord{
		Name:      "devbox-remote",
		URL:       "http://127.0.0.1:7778",
		SSHTarget: "dev@devbox.remote",
		RemoteCwd: "/home/dev/projects",
		CreatedAt: time.Now(),
	}

	if err := db.SaveHost(host1); err != nil {
		t.Fatalf("SaveHost failed: %v", err)
	}

	hosts, err := db.ListHosts()
	if err != nil {
		t.Fatalf("ListHosts failed: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("Expected 1 host, got %d", len(hosts))
	}
	if hosts[0].Name != host1.Name {
		t.Errorf("Expected host name %s, got %s", host1.Name, hosts[0].Name)
	}

	// 3. Delete operations
	if err := db.DeleteNode(node1.Path); err != nil {
		t.Fatalf("DeleteNode failed: %v", err)
	}
	nodes, _ = db.ListNodes()
	if len(nodes) != 0 {
		t.Errorf("Expected 0 nodes after delete, got %d", len(nodes))
	}

	// 4. Test Session Active vs Ended Filtering
	sActive := &Session{
		ID:          "claude:local:active-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "active-1",
		State:       StateWorking,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	sEnded := &Session{
		ID:          "claude:local:ended-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "ended-1",
		State:       StateEnded,
		StartedAt:   time.Now().Add(-1 * time.Hour),
		LastEventAt: time.Now().Add(-30 * time.Minute),
	}

	if err := db.SaveSession(sActive); err != nil {
		t.Fatalf("SaveSession sActive failed: %v", err)
	}
	if err := db.SaveSession(sEnded); err != nil {
		t.Fatalf("SaveSession sEnded failed: %v", err)
	}

	allSessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(allSessions) != 2 {
		t.Fatalf("Expected 2 total sessions, got %d", len(allSessions))
	}

	activeSessions, err := db.ListActiveSessions()
	if err != nil {
		t.Fatalf("ListActiveSessions failed: %v", err)
	}
	if len(activeSessions) != 1 {
		t.Fatalf("Expected 1 active session, got %d", len(activeSessions))
	}
	if activeSessions[0].ID != sActive.ID {
		t.Errorf("Expected active session %s, got %s", sActive.ID, activeSessions[0].ID)
	}
}

func TestDB_Accounts(t *testing.T) {
	dbFile := "./test_accounts.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// 1. Create accounts
	acc1 := &AgentAccount{
		Agent:       "claude-code",
		Name:        "work",
		DisplayName: "Anthropic Team (Work)",
		ConfigDir:   "~/.claude-profiles/work",
		Env: map[string]string{
			"ANTHROPIC_API_KEY": "sk-test-key",
		},
		IsDefault: false,
	}
	if err := db.SaveAccount(acc1); err != nil {
		t.Fatalf("SaveAccount acc1 failed: %v", err)
	}

	acc2 := &AgentAccount{
		Agent:       "claude-code",
		Name:        "personal",
		DisplayName: "Claude Pro (Personal)",
		ConfigDir:   "~/.claude-profiles/personal",
		IsDefault:   true,
	}
	if err := db.SaveAccount(acc2); err != nil {
		t.Fatalf("SaveAccount acc2 failed: %v", err)
	}

	acc3 := &AgentAccount{
		Agent:       "antigravity",
		Name:        "corp",
		DisplayName: "Google Vertex (Corp)",
		ConfigDir:   "~/.gemini-profiles/corp",
		IsDefault:   true,
	}
	if err := db.SaveAccount(acc3); err != nil {
		t.Fatalf("SaveAccount acc3 failed: %v", err)
	}

	// 2. GetAccount
	fetched, err := db.GetAccount(acc1.ID)
	if err != nil {
		t.Fatalf("GetAccount failed: %v", err)
	}
	if fetched == nil {
		t.Fatalf("Expected fetched account to not be nil")
	}
	if fetched.DisplayName != acc1.DisplayName {
		t.Errorf("Expected display name %s, got %s", acc1.DisplayName, fetched.DisplayName)
	}
	if fetched.Env["ANTHROPIC_API_KEY"] != "sk-test-key" {
		t.Errorf("Expected env var sk-test-key, got %s", fetched.Env["ANTHROPIC_API_KEY"])
	}

	// 3. ListAccounts
	claudeAccs, err := db.ListAccounts("claude-code")
	if err != nil {
		t.Fatalf("ListAccounts(claude-code) failed: %v", err)
	}
	if len(claudeAccs) != 2 {
		t.Fatalf("Expected 2 claude accounts, got %d", len(claudeAccs))
	}
	// acc2 is default, so it should be listed first
	if claudeAccs[0].Name != "personal" || !claudeAccs[0].IsDefault {
		t.Errorf("Expected default account 'personal' first, got %s", claudeAccs[0].Name)
	}

	allAccs, err := db.ListAccounts("")
	if err != nil {
		t.Fatalf("ListAccounts('') failed: %v", err)
	}
	if len(allAccs) != 3 {
		t.Fatalf("Expected 3 accounts total, got %d", len(allAccs))
	}

	// 4. SetDefaultAccount
	if err := db.SetDefaultAccount("claude-code", acc1.ID); err != nil {
		t.Fatalf("SetDefaultAccount failed: %v", err)
	}
	acc1Fetched, _ := db.GetAccount(acc1.ID)
	acc2Fetched, _ := db.GetAccount(acc2.ID)
	if !acc1Fetched.IsDefault {
		t.Errorf("Expected acc1 to be default")
	}
	if acc2Fetched.IsDefault {
		t.Errorf("Expected acc2 to no longer be default")
	}

	// 5. Test Session with AccountID
	sess := &Session{
		ID:          "claude:local:sess-acc-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "sess-acc-1",
		State:       StateWorking,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
		AccountID:   acc1.ID,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession with AccountID failed: %v", err)
	}

	savedSess, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if savedSess.AccountID != acc1.ID {
		t.Errorf("Expected session AccountID %s, got %s", acc1.ID, savedSess.AccountID)
	}

	// 6. DeleteAccount
	if err := db.DeleteAccount(acc3.ID); err != nil {
		t.Fatalf("DeleteAccount failed: %v", err)
	}
	acc3Fetched, err := db.GetAccount(acc3.ID)
	if err != nil {
		t.Fatalf("GetAccount after delete failed: %v", err)
	}
	if acc3Fetched != nil {
		t.Errorf("Expected acc3 to be deleted, found: %+v", acc3Fetched)
	}
}

