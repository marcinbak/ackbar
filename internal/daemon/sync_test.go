package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type testPeer struct {
	srv    *Server
	ts     *httptest.Server
	db     *DB
	dbPath string
	name   string
	token  string
}

func setupPeer(t *testing.T, name, displayName, token string) *testPeer {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), name+".db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed for %s: %v", name, err)
	}

	srv := NewServer(db)
	srv.SetHostIdentity(name, displayName)
	srv.SetToken(token)
	ts := httptest.NewServer(srv.Mux())

	return &testPeer{
		srv:    srv,
		ts:     ts,
		db:     db,
		dbPath: dbPath,
		name:   name,
		token:  token,
	}
}

func connectPeers(t *testing.T, peers ...*testPeer) {
	t.Helper()
	for i, p1 := range peers {
		for j, p2 := range peers {
			if i == j {
				continue
			}
			err := p1.db.SaveHost(&HostRecord{
				Name:      p2.name,
				URL:       p2.ts.URL,
				CreatedAt: time.Now(),
			})
			if err != nil {
				t.Fatalf("failed to connect peer %s to %s: %v", p1.name, p2.name, err)
			}
		}
	}
}

func syncAllPeers(ctx context.Context, peers ...*testPeer) {
	for _, p := range peers {
		p.srv.PullFromPeers(ctx)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("condition timed out after %v", timeout)
}

func makeRequest(t *testing.T, method, url, token string, body []byte) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Ackbar-Token", token)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func TestThreeWayLWWSync_NodesAndHostPaths(t *testing.T) {
	token := "test-sync-token-nodes"
	macbook := setupPeer(t, "macbook", "MacBook Pro", token)
	defer macbook.ts.Close()
	defer macbook.db.Close()

	macmini := setupPeer(t, "macmini", "Mac Mini", token)
	defer macmini.ts.Close()
	defer macmini.db.Close()

	legion := setupPeer(t, "legion", "Lenovo Legion", token)
	defer legion.ts.Close()
	defer legion.db.Close()

	connectPeers(t, macbook, macmini, legion)
	ctx := context.Background()

	// 1. Create tree node infra/k8s on macbook
	nodePayload := []byte(`{"path":"infra/k8s","git_url":"https://github.com/org/k8s","project_dir":"/Users/dev4u/infra/k8s"}`)
	resp := makeRequest(t, http.MethodPost, macbook.ts.URL+"/v1/nodes", token, nodePayload)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/nodes failed on macbook: %d %s", resp.StatusCode, string(body))
	}
	_ = resp.Body.Close()

	// Wait for push sync or pull to propagate to macmini and legion
	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		n1, _ := macmini.db.GetNode("infra/k8s")
		n2, _ := legion.db.GetNode("infra/k8s")
		return n1 != nil && n2 != nil
	})

	nodeMini, err := macmini.db.GetNode("infra/k8s")
	if err != nil || nodeMini == nil {
		t.Fatalf("macmini missing node infra/k8s: %v", err)
	}
	if len(nodeMini.HostPaths) == 0 || nodeMini.HostPaths[0].Host != "macbook" {
		t.Fatalf("expected host path for macbook on macmini, got %+v", nodeMini.HostPaths)
	}

	// 2. Add host path for legion to infra/k8s
	pathPayload := []byte(`{"node_path":"infra/k8s","host":"legion","project_dir":"/home/dev4u/k8s","capabilities":["docker","gpu"]}`)
	respPath := makeRequest(t, http.MethodPost, legion.ts.URL+"/v1/nodes/paths", token, pathPayload)
	if respPath.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respPath.Body)
		t.Fatalf("POST /v1/nodes/paths failed on legion: %d %s", respPath.StatusCode, string(body))
	}
	_ = respPath.Body.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		nMac, _ := macbook.db.GetNode("infra/k8s")
		nMini, _ := macmini.db.GetNode("infra/k8s")
		return nMac != nil && len(nMac.HostPaths) >= 2 && nMini != nil && len(nMini.HostPaths) >= 2
	})

	nMac, _ := macbook.db.GetNode("infra/k8s")
	hasLegionPath := false
	for _, hp := range nMac.HostPaths {
		if hp.Host == "legion" && hp.ProjectDir == "/home/dev4u/k8s" {
			hasLegionPath = true
			break
		}
	}
	if !hasLegionPath {
		t.Fatalf("macbook did not receive legion host path: %+v", nMac.HostPaths)
	}

	// 3. LWW Conflict Resolution on updated_at
	// Advance time monotonically so legion has newer timestamp than olderNode and step 1
	time.Sleep(50 * time.Millisecond)
	olderNode := *nMac
	olderNode.GitURL = "https://github.com/org/k8s-older"
	olderNode.UpdatedAt = time.Now().UTC()
	_ = macmini.db.SaveNode(&olderNode) // artificially put older node on macmini

	time.Sleep(50 * time.Millisecond)
	newerNode := *nMac
	newerNode.GitURL = "https://github.com/org/k8s-newer"
	newerNode.UpdatedAt = time.Now().UTC()
	if err := legion.db.SaveNode(&newerNode); err != nil {
		t.Fatalf("failed to save newer node on legion: %v", err)
	}
	legion.srv.pushSyncPayload(&SyncStatePayload{Nodes: []TreeNode{newerNode}})

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		n1, _ := macbook.db.GetNode("infra/k8s")
		n2, _ := macmini.db.GetNode("infra/k8s")
		n3, _ := legion.db.GetNode("infra/k8s")
		return n1 != nil && n1.GitURL == "https://github.com/org/k8s-newer" &&
			n2 != nil && n2.GitURL == "https://github.com/org/k8s-newer" &&
			n3 != nil && n3.GitURL == "https://github.com/org/k8s-newer"
	})

	time.Sleep(50 * time.Millisecond)
	// Create child node infra/k8s/worker-pool on macbook
	childPayload := []byte(`{"path":"infra/k8s/worker-pool","project_dir":"/Users/dev4u/infra/k8s/worker-pool"}`)
	respChild := makeRequest(t, http.MethodPost, macbook.ts.URL+"/v1/nodes", token, childPayload)
	if respChild.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/nodes for child failed: %d", respChild.StatusCode)
	}
	_ = respChild.Body.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		cMini, _ := macmini.db.GetNode("infra/k8s/worker-pool")
		cLegion, _ := legion.db.GetNode("infra/k8s/worker-pool")
		return cMini != nil && cLegion != nil
	})

	time.Sleep(50 * time.Millisecond)
	// 4. Soft Delete Parent Node infra/k8s on macmini and verify Hierarchical Tombstone Propagation
	respDel := makeRequest(t, http.MethodDelete, macmini.ts.URL+"/v1/nodes?path=infra/k8s", token, nil)
	if respDel.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respDel.Body)
		t.Fatalf("DELETE /v1/nodes failed on macmini: %d %s", respDel.StatusCode, string(body))
	}
	_ = respDel.Body.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		nodesMac, _ := macbook.db.ListNodes()
		nodesMini, _ := macmini.db.ListNodes()
		nodesLegion, _ := legion.db.ListNodes()
		for _, list := range [][]*TreeNode{nodesMac, nodesMini, nodesLegion} {
			for _, n := range list {
				if n.Path == "infra/k8s" || n.Path == "infra/k8s/worker-pool" {
					return false
				}
			}
		}
		return true
	})

	// Verify both parent and child tombstones are recorded in database across all peers
	for _, p := range []*testPeer{macbook, macmini, legion} {
		for _, path := range []string{"infra/k8s", "infra/k8s/worker-pool"} {
			var dTime string
			err := p.db.db.QueryRow("SELECT deleted_at FROM tree_nodes WHERE path = ?;", path).Scan(&dTime)
			if err != nil || dTime == "" {
				t.Fatalf("expected deleted_at tombstone on %s for %s, got err=%v, dTime=%s", p.name, path, err, dTime)
			}
		}
	}
}

func TestThreeWayLWWSync_TasksAndDeliverables(t *testing.T) {
	token := "test-sync-token-tasks"
	macbook := setupPeer(t, "macbook", "MacBook Pro", token)
	defer macbook.ts.Close()
	defer macbook.db.Close()

	macmini := setupPeer(t, "macmini", "Mac Mini", token)
	defer macmini.ts.Close()
	defer macmini.db.Close()

	legion := setupPeer(t, "legion", "Lenovo Legion", token)
	defer legion.ts.Close()
	defer legion.db.Close()

	connectPeers(t, macbook, macmini, legion)
	ctx := context.Background()

	// 1. Create task on macbook
	taskPayload := []byte(`{"title":"Implement Auth Sync","project_name":"Ackbar","status":"todo"}`)
	resp := makeRequest(t, http.MethodPost, macbook.ts.URL+"/v1/tasks", token, taskPayload)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/tasks failed on macbook: %d %s", resp.StatusCode, string(body))
	}
	var createdTask Task
	_ = json.NewDecoder(resp.Body).Decode(&createdTask)
	_ = resp.Body.Close()
	if createdTask.ID == "" {
		t.Fatalf("expected non-empty task ID from creation")
	}

	// Propagate to macmini and legion
	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		tMini, _ := macmini.db.GetTaskByID(createdTask.ID)
		tLegion, _ := legion.db.GetTaskByID(createdTask.ID)
		return tMini != nil && tLegion != nil
	})

	// 2. Update status and attach deliverable on legion
	updatePayload := []byte(fmt.Sprintf(`{"id":"%s","status":"in_progress"}`, createdTask.ID))
	respUpd := makeRequest(t, http.MethodPost, legion.ts.URL+"/v1/tasks", token, updatePayload)
	if respUpd.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respUpd.Body)
		t.Fatalf("POST /v1/tasks update failed on legion: %d %s", respUpd.StatusCode, string(body))
	}
	_ = respUpd.Body.Close()

	delPayload := []byte(fmt.Sprintf(`{"task_id":"%s","kind":"plan","title":"Auth Architecture Plan","file_path":"docs/auth.md","provider":"linear","content_md":"# Auth Plan\nDetailed architecture"}`, createdTask.ID))
	respDel := makeRequest(t, http.MethodPost, legion.ts.URL+"/v1/tasks/deliverable", token, delPayload)
	if respDel.StatusCode != http.StatusOK && respDel.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(respDel.Body)
		t.Fatalf("POST /v1/tasks/deliverable failed on legion: %d %s", respDel.StatusCode, string(body))
	}
	_ = respDel.Body.Close()

	// Verify macmini and macbook converge
	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		tMini, _ := macmini.db.GetTaskByID(createdTask.ID)
		tMac, _ := macbook.db.GetTaskByID(createdTask.ID)
		return tMini != nil && tMini.Status == "in_progress" && len(tMini.Deliverables) == 1 &&
			tMini.Deliverables[0].Provider == "linear" && tMini.Deliverables[0].ContentMD == "# Auth Plan\nDetailed architecture" &&
			tMac != nil && tMac.Status == "in_progress" && len(tMac.Deliverables) == 1 &&
			tMac.Deliverables[0].Provider == "linear" && tMac.Deliverables[0].ContentMD == "# Auth Plan\nDetailed architecture"
	})

	// 3. Delete task on macmini and verify tombstone propagation
	respDelete := makeRequest(t, http.MethodDelete, macmini.ts.URL+"/v1/tasks?id="+createdTask.ID, token, nil)
	if respDelete.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respDelete.Body)
		t.Fatalf("DELETE /v1/tasks failed on macmini: %d %s", respDelete.StatusCode, string(body))
	}
	_ = respDelete.Body.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		tMini, _ := macmini.db.GetTaskByID(createdTask.ID)
		tMac, _ := macbook.db.GetTaskByID(createdTask.ID)
		tLegion, _ := legion.db.GetTaskByID(createdTask.ID)
		return tMini == nil && tMac == nil && tLegion == nil
	})

	// Verify soft-deleted tombstone in tasks table on all peers
	for _, p := range []*testPeer{macbook, macmini, legion} {
		var delAt string
		err := p.db.db.QueryRow("SELECT deleted_at FROM tasks WHERE id = ?;", createdTask.ID).Scan(&delAt)
		if err != nil || delAt == "" {
			t.Fatalf("expected deleted_at tombstone on %s for task, got err=%v, delAt=%s", p.name, err, delAt)
		}
	}
}

func TestThreeWayLWWSync_SessionMetadataAndOfflineCatchup(t *testing.T) {
	token := "test-sync-token-sessions"
	macbook := setupPeer(t, "macbook", "MacBook Pro", token)
	defer macbook.ts.Close()
	defer macbook.db.Close()

	legion := setupPeer(t, "legion", "Lenovo Legion", token)
	defer legion.ts.Close()
	defer legion.db.Close()

	connectPeers(t, macbook, legion)
	ctx := context.Background()

	// 1. Create session on macbook
	now := time.Now().UTC()
	sessMacbook := &Session{
		ID:                "claude:macbook:session-42",
		Agent:             "claude-code",
		Host:              "macbook",
		NativeID:          "session-42",
		Cwd:               "/Users/dev4u/Personal/ackbar",
		Name:              "Initial Session Name",
		State:             StateWorking,
		Activity:          "Building peer sync",
		StartedAt:         now,
		LastEventAt:       now,
		MetadataUpdatedAt: now,
	}
	if err := macbook.db.SaveSession(sessMacbook); err != nil {
		t.Fatalf("failed to save session on macbook: %v", err)
	}

	// Also create a local session on legion
	sessLegion := &Session{
		ID:                "codex:legion:session-99",
		Agent:             "codex",
		Host:              "legion",
		NativeID:          "session-99",
		Cwd:               "/home/dev4u/Personal/ackbar",
		Name:              "Legion Local Session",
		State:             StateWorking,
		Activity:          "Idle",
		StartedAt:         now,
		LastEventAt:       now,
		MetadataUpdatedAt: now,
	}
	if err := legion.db.SaveSession(sessLegion); err != nil {
		t.Fatalf("failed to save session on legion: %v", err)
	}

	// Sync session metadata to legion so legion has the replica record
	syncAllPeers(ctx, macbook, legion)
	replicaOnLegion, err := legion.db.GetSession(sessMacbook.ID)
	if err != nil || replicaOnLegion == nil {
		t.Fatalf("expected replica session on legion: %v", err)
	}

	// 2. Simulate macbook going offline (point its URL on legion to a dead endpoint)
	deadURL := "http://127.0.0.1:1"
	_ = legion.db.SaveHost(&HostRecord{
		Name:      "macbook",
		URL:       deadURL,
		CreatedAt: time.Now(),
	})

	// 3. Query GET /v1/sessions?federated=true on legion
	respFed := makeRequest(t, http.MethodGet, legion.ts.URL+"/v1/sessions?federated=true", token, nil)
	if respFed.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respFed.Body)
		t.Fatalf("GET /v1/sessions?federated=true failed on legion: %d %s", respFed.StatusCode, string(body))
	}
	var fedSessions []*Session
	_ = json.NewDecoder(respFed.Body).Decode(&fedSessions)
	_ = respFed.Body.Close()

	var foundMacbookSess *Session
	var foundLegionSess *Session
	for _, s := range fedSessions {
		if s.ID == sessMacbook.ID {
			foundMacbookSess = s
		}
		if s.ID == sessLegion.ID {
			foundLegionSess = s
		}
	}
	if foundMacbookSess == nil {
		t.Fatalf("macbook session not returned by federated endpoint when offline")
	}
	if !foundMacbookSess.HostOffline {
		t.Fatalf("expected macbook session to have HostOffline = true, got %+v", foundMacbookSess)
	}
	if foundLegionSess == nil {
		t.Fatalf("legion session not returned by federated endpoint")
	}
	if foundLegionSess.HostOffline {
		t.Fatalf("expected legion local session to have HostOffline = false")
	}

	// 4. On legion, mutate session metadata while macbook is offline
	time.Sleep(50 * time.Millisecond) // Ensure MetadataUpdatedAt is strictly after
	controlResp := makeRequest(t, http.MethodPost, legion.ts.URL+"/v1/sessions/control?id="+sessMacbook.ID+"&action=mark_done", token, nil)
	if controlResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(controlResp.Body)
		t.Fatalf("control action failed on legion: %d %s", controlResp.StatusCode, string(body))
	}
	_ = controlResp.Body.Close()

	updatedOnLegion, err := legion.db.GetSession(sessMacbook.ID)
	if err != nil || !updatedOnLegion.IsDone {
		t.Fatalf("expected IsDone = true on legion replica: %v", err)
	}

	// Also rename session on legion
	renamePayload := []byte(`{"name":"Completed Feature Branch"}`)
	renameResp := makeRequest(t, http.MethodPost, legion.ts.URL+"/v1/sessions/control?id="+sessMacbook.ID+"&action=rename", token, renamePayload)
	if renameResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(renameResp.Body)
		t.Fatalf("rename failed on legion: %d %s", renameResp.StatusCode, string(body))
	}
	_ = renameResp.Body.Close()

	// 5. Bring macbook back online
	_ = legion.db.SaveHost(&HostRecord{
		Name:      "macbook",
		URL:       macbook.ts.URL,
		CreatedAt: time.Now(),
	})

	// Run catch-up pull on macbook
	macbook.srv.PullFromPeers(ctx)

	// 6. Verify macbook's copy of session-42 has caught up
	sessOnMacbook, err := macbook.db.GetSession(sessMacbook.ID)
	if err != nil || sessOnMacbook == nil {
		t.Fatalf("failed to get session on macbook: %v", err)
	}
	if !sessOnMacbook.IsDone {
		t.Fatalf("macbook did not catch up IsDone: %+v", sessOnMacbook)
	}
	if sessOnMacbook.CustomTitle != "Completed Feature Branch" && sessOnMacbook.Name != "Completed Feature Branch" {
		t.Fatalf("macbook did not catch up CustomTitle/Name: %+v", sessOnMacbook)
	}
}
