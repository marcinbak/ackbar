package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

	// 2. Run peer sync and verify sessions are NOT replicated across hosts' local DBs
	syncAllPeers(ctx, macbook, legion)
	if replicaOnLegion, _ := legion.db.GetSession(sessMacbook.ID); replicaOnLegion != nil {
		t.Fatalf("expected legion DB NOT to store macbook session replica, got %+v", replicaOnLegion)
	}
	if replicaOnMacbook, _ := macbook.db.GetSession(sessLegion.ID); replicaOnMacbook != nil {
		t.Fatalf("expected macbook DB NOT to store legion session replica, got %+v", replicaOnMacbook)
	}

	// 3. Query non-federated GET /v1/sessions on legion -> only legion session is returned
	respLocal := makeRequest(t, http.MethodGet, legion.ts.URL+"/v1/sessions", token, nil)
	if respLocal.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respLocal.Body)
		t.Fatalf("GET /v1/sessions failed on legion: %d %s", respLocal.StatusCode, string(body))
	}
	var localSessions []*Session
	_ = json.NewDecoder(respLocal.Body).Decode(&localSessions)
	_ = respLocal.Body.Close()
	if len(localSessions) != 1 || localSessions[0].ID != sessLegion.ID {
		t.Fatalf("expected GET /v1/sessions on legion to return only %q, got %+v", sessLegion.ID, localSessions)
	}

	// 4. Query GET /v1/sessions?federated=true on legion while macbook is online -> both sessions returned
	respFedOnline := makeRequest(t, http.MethodGet, legion.ts.URL+"/v1/sessions?federated=true", token, nil)
	if respFedOnline.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respFedOnline.Body)
		t.Fatalf("GET /v1/sessions?federated=true failed on legion: %d %s", respFedOnline.StatusCode, string(body))
	}
	var fedOnlineSessions []*Session
	_ = json.NewDecoder(respFedOnline.Body).Decode(&fedOnlineSessions)
	_ = respFedOnline.Body.Close()
	if len(fedOnlineSessions) != 2 {
		t.Fatalf("expected 2 federated sessions when both hosts are online, got %d", len(fedOnlineSessions))
	}

	// 4b. Control action sent to legion for macbook's session proxies with auth token to online macbook
	controlResp := makeRequest(t, http.MethodPost, legion.ts.URL+"/v1/sessions/control?id="+sessMacbook.ID+"&action=mark_done", token, nil)
	if controlResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(controlResp.Body)
		t.Fatalf("proxied control action failed on legion: %d %s", controlResp.StatusCode, string(body))
	}
	_ = controlResp.Body.Close()
	sessOnMacbook, err := macbook.db.GetSession(sessMacbook.ID)
	if err != nil || sessOnMacbook == nil || !sessOnMacbook.IsDone {
		t.Fatalf("expected proxied mark_done to update session on macbook, got %+v (err=%v)", sessOnMacbook, err)
	}

	// 5. Simulate macbook going offline -> federated endpoint returns only online legion session
	deadURL := "http://127.0.0.1:1"
	_ = legion.db.SaveHost(&HostRecord{
		Name:      "macbook",
		URL:       deadURL,
		CreatedAt: time.Now(),
	})

	respFedOffline := makeRequest(t, http.MethodGet, legion.ts.URL+"/v1/sessions?federated=true", token, nil)
	if respFedOffline.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respFedOffline.Body)
		t.Fatalf("GET /v1/sessions?federated=true failed on legion: %d %s", respFedOffline.StatusCode, string(body))
	}
	var fedOfflineSessions []*Session
	_ = json.NewDecoder(respFedOffline.Body).Decode(&fedOfflineSessions)
	_ = respFedOffline.Body.Close()
	if len(fedOfflineSessions) != 1 || fedOfflineSessions[0].ID != sessLegion.ID {
		t.Fatalf("expected only legion session when macbook is offline, got %+v", fedOfflineSessions)
	}
}

func TestZeroInputSupervisorSpawnAndBind_LWWSync(t *testing.T) {
	token := "test-sync-token-supervisor"
	macbook := setupPeer(t, "macbook", "MacBook Pro", token)
	defer macbook.ts.Close()
	defer macbook.db.Close()

	legion := setupPeer(t, "legion", "Lenovo Legion", token)
	defer legion.ts.Close()
	defer legion.db.Close()

	connectPeers(t, macbook, legion)
	ctx := context.Background()

	// 1. Zero-input Supervisor spawn on macbook (omitting cwd, agent, and name)
	spawnPayload := []byte(`{"is_supervisor":true,"engine_type":"headless"}`)
	respSpawn := makeRequest(t, http.MethodPost, macbook.ts.URL+"/v1/sessions/spawn", token, spawnPayload)
	if respSpawn.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respSpawn.Body)
		t.Fatalf("POST /v1/sessions/spawn failed: %d %s", respSpawn.StatusCode, string(body))
	}
	var spawnRes struct {
		Status  string   `json:"status"`
		ID      string   `json:"id"`
		Session *Session `json:"session"`
	}
	_ = json.NewDecoder(respSpawn.Body).Decode(&spawnRes)
	_ = respSpawn.Body.Close()

	if spawnRes.Session == nil || !spawnRes.Session.IsSupervisor {
		t.Fatalf("expected spawned session to have IsSupervisor=true, got %+v", spawnRes.Session)
	}
	if spawnRes.Session.Agent != "claude-code" {
		t.Fatalf("expected default agent claude-code, got %q", spawnRes.Session.Agent)
	}
	if spawnRes.Session.Name != "⚓ New Supervisor" {
		t.Fatalf("expected default name '⚓ New Supervisor', got %q", spawnRes.Session.Name)
	}
	if spawnRes.Session.NodePath != "" {
		t.Fatalf("expected unassigned NodePath on zero-input supervisor spawn, got %q", spawnRes.Session.NodePath)
	}
	home, _ := os.UserHomeDir()
	if home != "" && spawnRes.Session.Cwd != home {
		t.Fatalf("expected default Cwd to be user home %q, got %q", home, spawnRes.Session.Cwd)
	}

	// 2. Bind Supervisor session via POST /v1/sessions/bind-supervisor on macbook
	time.Sleep(20 * time.Millisecond)
	bindPayload := []byte(fmt.Sprintf(`{"session_id":%q,"node_path":"Modemobile/NGL","title":"⚓ NGL Feature Planning"}`, spawnRes.ID))
	respBind := makeRequest(t, http.MethodPost, macbook.ts.URL+"/v1/sessions/bind-supervisor", token, bindPayload)
	if respBind.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respBind.Body)
		t.Fatalf("POST /v1/sessions/bind-supervisor failed: %d %s", respBind.StatusCode, string(body))
	}
	var bindRes struct {
		Status  string   `json:"status"`
		Session *Session `json:"session"`
	}
	_ = json.NewDecoder(respBind.Body).Decode(&bindRes)
	_ = respBind.Body.Close()

	if bindRes.Status != "bound" || bindRes.Session == nil {
		t.Fatalf("unexpected bind response: %+v", bindRes)
	}
	if !bindRes.Session.IsSupervisor || bindRes.Session.NodePath != "Modemobile/NGL" || bindRes.Session.CustomTitle != "⚓ NGL Feature Planning" {
		t.Fatalf("unexpected bound session fields: %+v", bindRes.Session)
	}

	// Verify tree node was automatically created
	if node, _ := macbook.db.GetNode("Modemobile/NGL"); node == nil {
		t.Fatalf("expected tree_node 'Modemobile/NGL' to exist after bind-supervisor")
	}

	// 3. Create a task worker session on macbook and verify current_task_id LWW sync
	workerSess := &Session{
		ID:                "claude-code:macbook:worker-101",
		Agent:             "claude-code",
		Host:              "macbook",
		NativeID:          "worker-101",
		Cwd:               "/Users/dev4u/Work/ngl",
		NodePath:          "Modemobile/NGL",
		Name:              "Worker 101",
		State:             StateWorking,
		StartedAt:         time.Now().UTC(),
		LastEventAt:       time.Now().UTC(),
		MetadataUpdatedAt: time.Now().UTC(),
	}
	_ = macbook.db.SaveSession(workerSess)
	task := &Task{
		ID:          "task_ngl_101",
		Title:       "NGL-101: Feature Work",
		GroupName:   "Modemobile",
		ProjectName: "NGL",
		Status:      "IN_PROGRESS",
		Substatus:   "active",
		Workers: []TaskWorker{
			{
				TaskID:     "task_ngl_101",
				SessionID:  workerSess.ID,
				Agent:      "claude-code",
				Host:       "macbook",
				IsActive:   true,
				AssignedAt: time.Now().UTC(),
			},
		},
	}
	if err := macbook.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	reloadedWorker, _ := macbook.db.GetSession(workerSess.ID)
	if reloadedWorker == nil || reloadedWorker.CurrentTaskID != "task_ngl_101" {
		t.Fatalf("expected worker session CurrentTaskID='task_ngl_101', got %+v", reloadedWorker)
	}

	// 4. Sync peers and verify legion receives node & task via LWW sync, while sessions stay on macbook and are visible via federated query
	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, legion)
		nodeLegion, _ := legion.db.GetNode("Modemobile/NGL")
		taskLegion, _ := legion.db.GetTaskByID("task_ngl_101")
		return nodeLegion != nil && taskLegion != nil && len(taskLegion.Workers) == 1 && taskLegion.Workers[0].SessionID == workerSess.ID
	})

	if supLegion, _ := legion.db.GetSession(spawnRes.ID); supLegion != nil {
		t.Fatalf("expected legion DB not to store macbook supervisor session locally, got %+v", supLegion)
	}

	respFed := makeRequest(t, http.MethodGet, legion.ts.URL+"/v1/sessions?federated=true", token, nil)
	if respFed.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respFed.Body)
		t.Fatalf("GET /v1/sessions?federated=true failed on legion: %d %s", respFed.StatusCode, string(body))
	}
	var fedSessions []*Session
	_ = json.NewDecoder(respFed.Body).Decode(&fedSessions)
	_ = respFed.Body.Close()

	var supFed, wrkFed *Session
	for _, s := range fedSessions {
		if s.ID == spawnRes.ID {
			supFed = s
		}
		if s.ID == workerSess.ID {
			wrkFed = s
		}
	}
	if supFed == nil || !supFed.IsSupervisor || supFed.NodePath != "Modemobile/NGL" || supFed.CustomTitle != "⚓ NGL Feature Planning" {
		t.Fatalf("expected federated query on legion to return macbook supervisor session, got %+v", supFed)
	}
	if wrkFed == nil || wrkFed.CurrentTaskID != "task_ngl_101" {
		t.Fatalf("expected federated query on legion to return macbook worker session with CurrentTaskID='task_ngl_101', got %+v", wrkFed)
	}
}
