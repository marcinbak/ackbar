package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeliverables_DBMigrationAndUpdateTaskPreservation(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "migration_preservation.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	task := &Task{
		ID:          "task_mig_1",
		Title:       "Test Migration Task",
		GroupName:   "Eng",
		ProjectName: "Ackbar",
		Status:      "IN_PROGRESS",
		Deliverables: []TaskDeliverable{
			{
				ID:        "del_plan_1",
				TaskID:    "task_mig_1",
				Kind:      "plan",
				Title:     "Architecture Plan",
				Host:      "macbook",
				ContentMD: "# Full Architecture Markdown Content",
			},
			{
				ID:           "del_retro_1",
				TaskID:       "task_mig_1",
				Kind:         "retro",
				Title:        "Retrospective",
				Host:         "macbook",
				ContentMD:    "# Full Retro Content",
				MetadataJSON: `{"tier":"T0","raw_tokens":12345}`,
			},
		},
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	taskUpdate := &Task{
		ID:          task.ID,
		Title:       "Updated Title",
		GroupName:   task.GroupName,
		ProjectName: task.ProjectName,
		Status:      "REVIEW",
		Substatus:   "in_review",
		Deliverables: []TaskDeliverable{
			{
				ID:           "del_retro_1",
				TaskID:       task.ID,
				Kind:         "retro",
				Title:        "Retrospective",
				Host:         "macbook",
				ContentMD:    "",
				MetadataJSON: "",
			},
		},
	}
	if err := db.UpdateTask(taskUpdate); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	delsMap, err := db.getDeliverablesForTasks([]string{task.ID})
	if err != nil {
		t.Fatalf("getDeliverablesForTasks failed: %v", err)
	}
	dels := delsMap[task.ID]
	if len(dels) != 2 {
		t.Fatalf("Expected both deliverables preserved, got %d", len(dels))
	}

	var planDel, retroDel *TaskDeliverable
	for i := range dels {
		if dels[i].ID == "del_plan_1" {
			planDel = &dels[i]
		}
		if dels[i].ID == "del_retro_1" {
			retroDel = &dels[i]
		}
	}
	if planDel == nil || planDel.ContentMD != "# Full Architecture Markdown Content" {
		t.Errorf("Plan deliverable omitted from update was corrupted: %+v", planDel)
	}
	if retroDel == nil || retroDel.ContentMD != "# Full Retro Content" {
		t.Errorf("Retro deliverable content_md wiped: %+v", retroDel)
	}
	if retroDel == nil || !strings.Contains(retroDel.MetadataJSON, "12345") {
		t.Errorf("Retro deliverable metadata_json wiped: %+v", retroDel)
	}

	if err := db.DeleteTask(task.ID); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}

	afterDeleteMap, err := db.getDeliverablesForTasks([]string{task.ID})
	if err != nil {
		t.Fatalf("getDeliverablesForTasks after delete failed: %v", err)
	}
	if len(afterDeleteMap[task.ID]) != 0 {
		t.Errorf("Expected 0 active deliverables after task deletion, got %d", len(afterDeleteMap[task.ID]))
	}

	var delAt sql.NullTime
	var cMD string
	err = db.db.QueryRow("SELECT deleted_at, content_md FROM task_deliverables WHERE id = 'del_retro_1'").Scan(&delAt, &cMD)
	if err != nil || !delAt.Valid || cMD != "" {
		t.Errorf("Expected soft-deleted tombstone with empty content_md, got delAt=%v, content=%q, err=%v", delAt, cMD, err)
	}
}

func TestDeliverables_AutoIngestAndMetricsExtraction(t *testing.T) {
	tempHome := t.TempDir()
	dbFile := filepath.Join(tempHome, "autoingest.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := &Server{db: db, homeDir: tempHome}

	task := &Task{
		ID:          "task_auto_1",
		Title:       "Auto Ingest Task",
		GroupName:   "Eng",
		ProjectName: "Ackbar",
		Status:      "IN_PROGRESS",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	runsDir := filepath.Join(tempHome, ".claude", "dev-workflow-runs")
	if err := os.MkdirAll(runsDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	jsonContent := `{
		"ticket": "task_auto_1",
		"repo": "ackbar",
		"branch": "feat/auto-test",
		"worktree": "/path/to/worktree",
		"node": "done",
		"started": "2026-11-14T00:00:00Z",
		"updated": "2026-11-14T01:30:00Z",
		"lite": false,
		"impl_attempts": 2,
		"plan_revisions": 1,
		"review_budget_left": 2,
		"review_rounds": 1,
		"tier": "T0",
		"notes": ["Note step 1", "Note step 2"],
		"interventions": [{"rule_could_decide": true, "reason": "auth decision"}],
		"human_interventions": [{"rule_could_decide": false, "reason": "creative review"}],
		"implementer_agent": "claude-subagent-1"
	}`
	jsonPath := filepath.Join(runsDir, "test-slug-1.json")
	if err := os.WriteFile(jsonPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("WriteFile json failed: %v", err)
	}

	retroContent := `# Retro — task_auto_1 (ackbar, feat/auto-test) — 2026-11-14

1. **Gate 1** — clean verification pass without manual diff edits.
2. **Budgets spent** — impl_attempts 2, plan_revisions 1, re-reviews 1, review_rounds 1.
3. **Review noise** — 0 findings dropped at triage across 3 reviewers.
4. **Verification** — tier T0 verified cleanly on local harness.
5. **Manifest improvements** — none.
6. **Token usage** — cache hit rate: 98.2%.
- **Peak context: 145k tokens.** Cache writes are 25% of weighted cost.
- **weighted total 2.4M (14.2M tokens)**
- Subagents at 45.0%
`
	retroPath := filepath.Join(runsDir, "test-slug-1.retro.md")
	if err := os.WriteFile(retroPath, []byte(retroContent), 0644); err != nil {
		t.Fatalf("WriteFile retro failed: %v", err)
	}

	reqPayload := TaskDeliverablePayload{
		TaskID:   task.ID,
		Type:     "retro",
		FilePath: retroPath,
	}
	b, _ := json.Marshal(reqPayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/deliverable", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	srv.handleTaskDeliverable(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("handleTaskDeliverable returned %d: %s", rec.Code, rec.Body.String())
	}

	var createdDel TaskDeliverable
	if err := json.Unmarshal(rec.Body.Bytes(), &createdDel); err != nil {
		t.Fatalf("Unmarshal response failed: %v", err)
	}
	if createdDel.ContentMD != retroContent {
		t.Errorf("Expected content_md auto-read, got %q", createdDel.ContentMD)
	}
	if createdDel.Metrics == nil {
		t.Fatalf("Expected metrics extracted, got nil")
	}
	m := createdDel.Metrics
	if m.RunSlug != "test-slug-1" {
		t.Errorf("Expected RunSlug test-slug-1, got %q", m.RunSlug)
	}
	if m.ImplAttempts != 2 || m.PlanRevisions != 1 {
		t.Errorf("Budget metrics mismatch: %+v", m)
	}
	if m.InterventionsCount != 2 || m.PreventableInterventionsCount != 1 {
		t.Errorf("Interventions mismatch: count=%d, preventable=%d", m.InterventionsCount, m.PreventableInterventionsCount)
	}
	if m.CacheHitPct != 98.2 {
		t.Errorf("Expected cache hit 98.2, got %f", m.CacheHitPct)
	}
	if len(m.Headlines) < 5 {
		t.Errorf("Expected at least 5 headlines, got %d", len(m.Headlines))
	}

	reReq := httptest.NewRequest(http.MethodPost, "/v1/tasks/deliverable", bytes.NewReader(b))
	reRec := httptest.NewRecorder()
	srv.handleTaskDeliverable(reRec, reReq)
	if reRec.Code != http.StatusCreated {
		t.Fatalf("Re-attach returned %d: %s", reRec.Code, reRec.Body.String())
	}
	var reDel TaskDeliverable
	_ = json.Unmarshal(reRec.Body.Bytes(), &reDel)
	if reDel.ID != createdDel.ID {
		t.Errorf("Expected idempotent deliverable ID %s, got %s", createdDel.ID, reDel.ID)
	}
	if !reDel.UpdatedAt.Equal(createdDel.UpdatedAt) {
		t.Errorf("Expected unchanged UpdatedAt on idempotent re-attach, was %v now %v", createdDel.UpdatedAt, reDel.UpdatedAt)
	}
}

func TestDeliverables_PathConfinementSecurity(t *testing.T) {
	tempHome := t.TempDir()
	tempWorktree := filepath.Join(tempHome, "worktree")
	_ = os.MkdirAll(tempWorktree, 0755)

	dbFile := filepath.Join(tempHome, "security.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := &Server{db: db, homeDir: tempHome}

	task := &Task{
		ID:           "task_sec_1",
		Title:        "Security Task",
		GroupName:    "Eng",
		ProjectName:  "Ackbar",
		Status:       "IN_PROGRESS",
		WorktreePath: tempWorktree,
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	testCases := []struct {
		name       string
		setupPath  func() string
		targetPath string
		expectCode int
	}{
		{
			name:       "Rejects /etc/passwd",
			targetPath: "/etc/passwd",
			expectCode: http.StatusForbidden,
		},
		{
			name:       "Rejects /etc/hosts",
			targetPath: "/etc/hosts",
			expectCode: http.StatusForbidden,
		},
		{
			name:       "Rejects /etc/nonexistent",
			targetPath: "/etc/nonexistent",
			expectCode: http.StatusForbidden,
		},
		{
			name: "Rejects ~/.ssh/id_rsa",
			setupPath: func() string {
				sshDir := filepath.Join(tempHome, ".ssh")
				_ = os.MkdirAll(sshDir, 0700)
				f := filepath.Join(sshDir, "id_rsa")
				_ = os.WriteFile(f, []byte("fake-key"), 0600)
				return f
			},
			expectCode: http.StatusForbidden,
		},
		{
			name: "Rejects worktree .env",
			setupPath: func() string {
				f := filepath.Join(tempWorktree, ".env")
				_ = os.WriteFile(f, []byte("SECRET=1"), 0644)
				return f
			},
			expectCode: http.StatusForbidden,
		},
		{
			name: "Rejects symlink escape to /etc/hosts",
			setupPath: func() string {
				link := filepath.Join(tempWorktree, "escape.retro.md")
				_ = os.Symlink("/etc/hosts", link)
				return link
			},
			expectCode: http.StatusForbidden,
		},
		{
			name: "Rejects oversized file > 2MB",
			setupPath: func() string {
				f := filepath.Join(tempWorktree, "large.retro.md")
				largeData := make([]byte, 2*1024*1024+10)
				_ = os.WriteFile(f, largeData, 0644)
				return f
			},
			expectCode: http.StatusRequestEntityTooLarge,
		},
		{
			name: "Allows ~/.claude/dev-workflow-runs file",
			setupPath: func() string {
				dir := filepath.Join(tempHome, ".claude", "dev-workflow-runs")
				_ = os.MkdirAll(dir, 0755)
				f := filepath.Join(dir, "allowed.retro.md")
				_ = os.WriteFile(f, []byte("# Valid retro markdown"), 0644)
				return f
			},
			expectCode: http.StatusCreated,
		},
		{
			name: "Allows worktree file",
			setupPath: func() string {
				f := filepath.Join(tempWorktree, "allowed_in_worktree.retro.md")
				_ = os.WriteFile(f, []byte("# Valid worktree retro"), 0644)
				return f
			},
			expectCode: http.StatusCreated,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.targetPath
			if tc.setupPath != nil {
				path = tc.setupPath()
			}
			p := TaskDeliverablePayload{
				TaskID:   task.ID,
				Type:     "retro",
				FilePath: path,
			}
			body, _ := json.Marshal(p)
			req := httptest.NewRequest(http.MethodPost, "/v1/tasks/deliverable", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			srv.handleTaskDeliverable(rec, req)

			if rec.Code != tc.expectCode {
				t.Errorf("Path %s: expected status %d, got %d: %s", path, tc.expectCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDeliverables_TaskDoneAndWorkflowWatcherBackfill(t *testing.T) {
	tempHome := t.TempDir()
	dbFile := filepath.Join(tempHome, "watcher.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := &Server{db: db, homeDir: tempHome}

	runsDir := filepath.Join(tempHome, ".claude", "dev-workflow-runs")
	_ = os.MkdirAll(runsDir, 0755)

	jsonPath := filepath.Join(runsDir, "done-task-run.json")
	jsonContent := `{
		"ticket": "task_done_test_1",
		"repo": "ackbar",
		"branch": "feat/done-test",
		"node": "done",
		"started": "2026-11-14T00:00:00Z",
		"impl_attempts": 1,
		"tier": "T0"
	}`
	_ = os.WriteFile(jsonPath, []byte(jsonContent), 0644)

	retroPath := filepath.Join(runsDir, "done-task-run.retro.md")
	retroContent := `# Retro — task_done_test_1 (ackbar, feat/done-test) — 2026-11-14

1. **Human touchpoints** — none.
2. **Budgets spent** — impl_attempts 1, plan_revisions 0, re-reviews 0.
3. **Token usage** — unavailable on this runtime.
`
	_ = os.WriteFile(retroPath, []byte(retroContent), 0644)

	task := &Task{
		ID:          "task_done_test_1",
		Title:       "Task Ready For Done",
		GroupName:   "Eng",
		ProjectName: "Ackbar",
		Status:      "DONE",
		Branch:      "feat/done-test",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if err := srv.IngestDevWorkflowRuns(); err != nil {
		t.Fatalf("IngestDevWorkflowRuns failed: %v", err)
	}

	fetchedTask, err := db.GetTaskByID(task.ID)
	if err != nil || fetchedTask == nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if fetchedTask.Status != "DONE" {
		t.Errorf("Expected status to remain DONE, got %s", fetchedTask.Status)
	}
	if len(fetchedTask.Deliverables) != 1 {
		t.Fatalf("Expected 1 retro deliverable backfilled onto DONE task, got %d", len(fetchedTask.Deliverables))
	}
	del := fetchedTask.Deliverables[0]
	if del.ContentMD != retroContent {
		t.Errorf("Expected exact retro content, got %q", del.ContentMD)
	}
	if del.Metrics == nil || del.Metrics.RunSlug != "done-task-run" {
		t.Errorf("Expected Metrics with RunSlug done-task-run, got %+v", del.Metrics)
	}

	for _, ref := range fetchedTask.ExternalRefs {
		if strings.EqualFold(ref.Tracker, "jira") && strings.HasPrefix(ref.RefKey, "TASK_") {
			t.Errorf("Found forbidden bogus Jira external ref for internal task: %+v", ref)
		}
	}

	updTimeBefore := del.UpdatedAt
	if err := srv.IngestDevWorkflowRuns(); err != nil {
		t.Fatalf("Second IngestDevWorkflowRuns failed: %v", err)
	}
	fetchedAgain, _ := db.GetTaskByID(task.ID)
	if len(fetchedAgain.Deliverables) != 1 {
		t.Errorf("Expected exactly 1 deliverable after second run, got %d", len(fetchedAgain.Deliverables))
	}
	if !fetchedAgain.Deliverables[0].UpdatedAt.Equal(updTimeBefore) {
		t.Errorf("Expected zero churn on repeated watcher run: before %v, after %v", updTimeBefore, fetchedAgain.Deliverables[0].UpdatedAt)
	}
}

func TestDeliverables_ThreeWayLWWMeshSync(t *testing.T) {
	token := "test-token-mesh-retro"
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

	taskPayload := []byte(`{"title":"Retro Mesh Feature","project_name":"Ackbar","status":"in_progress"}`)
	resp := makeRequest(t, http.MethodPost, macbook.ts.URL+"/v1/tasks", token, taskPayload)
	var createdTask Task
	_ = json.NewDecoder(resp.Body).Decode(&createdTask)
	_ = resp.Body.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		tMini, _ := macmini.db.GetTaskByID(createdTask.ID)
		tLegion, _ := legion.db.GetTaskByID(createdTask.ID)
		return tMini != nil && tLegion != nil
	})

	retroContent := `# Retro — ` + createdTask.ID + ` (ackbar, feat/mesh) — 2026-11-14

1. **Headlines** — Mesh sync verified across 3 nodes.
`
	delPayload := []byte(fmt.Sprintf(`{
		"task_id": "%s",
		"type": "retro",
		"file_path": "/home/dev4u/.claude/dev-workflow-runs/mesh-run.retro.md",
		"content_md": %q
	}`, createdTask.ID, retroContent))

	respDel := makeRequest(t, http.MethodPost, legion.ts.URL+"/v1/tasks/deliverable", token, delPayload)
	var legionDel TaskDeliverable
	_ = json.NewDecoder(respDel.Body).Decode(&legionDel)
	_ = respDel.Body.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		tMini, _ := macmini.db.GetTaskByID(createdTask.ID)
		tMac, _ := macbook.db.GetTaskByID(createdTask.ID)
		return tMini != nil && len(tMini.Deliverables) == 1 &&
			tMac != nil && len(tMac.Deliverables) == 1 &&
			tMini.Deliverables[0].ID == legionDel.ID &&
			tMac.Deliverables[0].ID == legionDel.ID &&
			tMini.Deliverables[0].ContentMD == retroContent
	})

	metaTouch := []byte(fmt.Sprintf(`{
		"task_id": "%s",
		"type": "retro",
		"file_path": "/Users/dev4u/.claude/dev-workflow-runs/mesh-run.retro.md",
		"content_md": ""
	}`, createdTask.ID))
	respTouch := makeRequest(t, http.MethodPost, macbook.ts.URL+"/v1/tasks/deliverable", token, metaTouch)
	_ = respTouch.Body.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		tMac, _ := macbook.db.GetTaskByID(createdTask.ID)
		return tMac != nil && len(tMac.Deliverables) == 1 && tMac.Deliverables[0].ContentMD == retroContent
	})

	delTombstone := TaskDeliverable{
		ID:        legionDel.ID,
		TaskID:    createdTask.ID,
		UpdatedAt: time.Now().UTC().Add(1 * time.Second),
	}
	nowDel := delTombstone.UpdatedAt
	delTombstone.DeletedAt = &nowDel
	legion.srv.pushSyncPayload(&SyncStatePayload{Deliverables: []TaskDeliverable{delTombstone}})

	waitForCondition(t, 2*time.Second, func() bool {
		syncAllPeers(ctx, macbook, macmini, legion)
		tMini, _ := macmini.db.GetTaskByID(createdTask.ID)
		tMac, _ := macbook.db.GetTaskByID(createdTask.ID)
		tLegion, _ := legion.db.GetTaskByID(createdTask.ID)
		return len(tMini.Deliverables) == 0 && len(tMac.Deliverables) == 0 && len(tLegion.Deliverables) == 0
	})
}

func TestDeliverables_ListRetrosFiltering(t *testing.T) {
	tempHome := t.TempDir()
	dbFile := filepath.Join(tempHome, "filter_retros.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := &Server{db: db, homeDir: tempHome}

	task1 := &Task{ID: "t_flt_1", Title: "Task 1", GroupName: "Eng", ProjectName: "RepoAlpha", Status: "DONE"}
	task2 := &Task{ID: "t_flt_2", Title: "Task 2", GroupName: "Design", ProjectName: "RepoBeta", Status: "DONE"}
	_ = db.CreateTask(task1)
	_ = db.CreateTask(task2)

	d1Time := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	d2Time := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	del1 := TaskDeliverable{
		ID:           "del_flt_1",
		TaskID:       task1.ID,
		Kind:         "retro",
		Title:        "Alpha Retro",
		Host:         "legion",
		ContentMD:    "# Secret markdown 1",
		MetadataJSON: `{"repo":"RepoAlpha","agent":"claude-3","tier":"T0","completed_at":"2026-11-10T10:00:00Z"}`,
		CreatedAt:    d1Time,
		UpdatedAt:    d1Time,
	}
	del2 := TaskDeliverable{
		ID:           "del_flt_2",
		TaskID:       task2.ID,
		Kind:         "retro",
		Title:        "Beta Retro",
		Host:         "macbook",
		ContentMD:    "# Secret markdown 2",
		MetadataJSON: `{"repo":"RepoBeta","agent":"codex-1","tier":"T1","completed_at":"2026-11-12T10:00:00Z"}`,
		CreatedAt:    d2Time,
		UpdatedAt:    d2Time,
	}
	_, _ = db.UpsertTaskDeliverableLWW(&del1, false)
	_, _ = db.UpsertTaskDeliverableLWW(&del2, false)

	reqBad := httptest.NewRequest(http.MethodGet, "/v1/retros?since=not-a-date", nil)
	recBad := httptest.NewRecorder()
	srv.handleListRetros(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for bad date, got %d", recBad.Code)
	}

	reqAll := httptest.NewRequest(http.MethodGet, "/v1/retros", nil)
	recAll := httptest.NewRecorder()
	srv.handleListRetros(recAll, reqAll)
	if recAll.Code != http.StatusOK {
		t.Fatalf("GET /v1/retros failed: %d", recAll.Code)
	}
	var resAll struct {
		Retros []RetroSummary `json:"retros"`
		Count  int            `json:"count"`
	}
	_ = json.Unmarshal(recAll.Body.Bytes(), &resAll)
	if resAll.Count != 2 {
		t.Fatalf("Expected 2 retros, got %d", resAll.Count)
	}
	if strings.Contains(recAll.Body.String(), "Secret markdown") {
		t.Errorf("GET /v1/retros response leaked content_md!")
	}

	reqSince := httptest.NewRequest(http.MethodGet, "/v1/retros?since=2026-11-11", nil)
	recSince := httptest.NewRecorder()
	srv.handleListRetros(recSince, reqSince)
	var resSince struct {
		Retros []RetroSummary `json:"retros"`
		Count  int            `json:"count"`
	}
	_ = json.Unmarshal(recSince.Body.Bytes(), &resSince)
	if resSince.Count != 1 || resSince.Retros[0].ID != "del_flt_2" {
		t.Errorf("Expected only del_flt_2 after 2026-11-11, got %+v", resSince)
	}

	reqRepo := httptest.NewRequest(http.MethodGet, "/v1/retros?repo=repoalpha", nil)
	recRepo := httptest.NewRecorder()
	srv.handleListRetros(recRepo, reqRepo)
	var resRepo struct {
		Retros []RetroSummary `json:"retros"`
		Count  int            `json:"count"`
	}
	_ = json.Unmarshal(recRepo.Body.Bytes(), &resRepo)
	if resRepo.Count != 1 || resRepo.Retros[0].ID != "del_flt_1" {
		t.Errorf("Expected only del_flt_1 for repo RepoAlpha, got %+v", resRepo)
	}
}

func TestDeliverables_GetDeliverableAndPagination(t *testing.T) {
	tempHome := t.TempDir()
	dbFile := filepath.Join(tempHome, "page_del.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	srv := &Server{db: db, homeDir: tempHome}

	task := &Task{ID: "t_page_1", Title: "Pagination Task", GroupName: "Eng", ProjectName: "Ackbar", Status: "DONE"}
	_ = db.CreateTask(task)

	fullContent := "🚀 UTF-8 Test: café, naïve, ñ, 日本語! " + strings.Repeat("Lorem ipsum dolor sit amet. ", 50)
	del := TaskDeliverable{
		ID:        "del_page_utf8",
		TaskID:    task.ID,
		Kind:      "retro",
		Title:     "UTF-8 Retro",
		Host:      "legion",
		ContentMD: fullContent,
	}
	_, _ = db.UpsertTaskDeliverableLWW(&del, false)

	reqLegacy := httptest.NewRequest(http.MethodGet, "/v1/tasks/deliverable?id="+del.ID, nil)
	recLegacy := httptest.NewRecorder()
	srv.handleTaskDeliverable(recLegacy, reqLegacy)
	if recLegacy.Code != http.StatusOK {
		t.Fatalf("Legacy deliverable lookup failed: %d", recLegacy.Code)
	}
	var legacyDel TaskDeliverable
	_ = json.Unmarshal(recLegacy.Body.Bytes(), &legacyDel)
	if legacyDel.ContentMD != fullContent {
		t.Errorf("Legacy lookup failed to return full content_md")
	}

	pageSize := 30
	offset := 0
	var reconstructed strings.Builder

	for {
		url := fmt.Sprintf("/v1/deliverables/%s?max_chars=%d&char_offset=%d", del.ID, pageSize, offset)
		reqPage := httptest.NewRequest(http.MethodGet, url, nil)
		recPage := httptest.NewRecorder()
		srv.handleDeliverables(recPage, reqPage)
		if recPage.Code != http.StatusOK {
			t.Fatalf("handleDeliverables returned %d: %s", recPage.Code, recPage.Body.String())
		}
		var pageResp DeliverablePageResponse
		if err := json.Unmarshal(recPage.Body.Bytes(), &pageResp); err != nil {
			t.Fatalf("Unmarshal page response failed: %v", err)
		}
		reconstructed.WriteString(pageResp.ContentMD)
		if !pageResp.Truncated {
			break
		}
		offset = pageResp.NextCharOffset
	}

	if reconstructed.String() != fullContent {
		t.Errorf("Reconstructed UTF-8 string did not match original! Length orig=%d, recon=%d", len(fullContent), len(reconstructed.String()))
	}

	reqByTask := httptest.NewRequest(http.MethodGet, "/v1/deliverables?task_id="+task.ID+"&type=retro", nil)
	recByTask := httptest.NewRecorder()
	srv.handleDeliverables(recByTask, reqByTask)
	if recByTask.Code != http.StatusOK {
		t.Fatalf("Lookup by task_id and type failed: %d: %s", recByTask.Code, recByTask.Body.String())
	}
	var byTaskResp DeliverablePageResponse
	_ = json.Unmarshal(recByTask.Body.Bytes(), &byTaskResp)
	if byTaskResp.ID != del.ID {
		t.Errorf("Expected ID %s from task_id lookup, got %s", del.ID, byTaskResp.ID)
	}
}
