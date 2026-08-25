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
