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
}
