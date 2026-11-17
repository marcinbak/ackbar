package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func installScriptMockClaude(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	mockPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(mockPath, []byte("#!/usr/bin/env bash\n"+script+"\n"), 0755); err != nil {
		t.Fatalf("write mock claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func postSessionRespond(t *testing.T, srv *Server, payload map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/respond", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)
	return w
}

func TestBuildPersistentArgs_StdioControlAndAllowDangerouslySkipPermissions(t *testing.T) {
	for _, mode := range []string{"plan", "auto", "acceptEdits", "dontAsk"} {
		sess := &Session{ID: "c-" + mode, Agent: "claude-code", NativeID: "nat-" + mode, PermissionMode: mode}
		_, args, err := buildPersistentArgs(sess, false)
		if err != nil {
			t.Fatalf("buildPersistentArgs(%s): %v", mode, err)
		}
		pptIdx := slices.Index(args, "--permission-prompt-tool")
		if pptIdx == -1 || pptIdx+1 >= len(args) || args[pptIdx+1] != "stdio" {
			t.Fatalf("mode %s missing --permission-prompt-tool stdio: %v", mode, args)
		}
		pmIdx := slices.Index(args, "--permission-mode")
		if pmIdx == -1 || pmIdx+1 >= len(args) || args[pmIdx+1] != mode {
			t.Fatalf("mode %s missing --permission-mode %s: %v", mode, mode, args)
		}
		if !slices.Contains(args, "--allow-dangerously-skip-permissions") {
			t.Fatalf("mode %s missing --allow-dangerously-skip-permissions: %v", mode, args)
		}
		if slices.Contains(args, "--dangerously-skip-permissions") {
			t.Fatalf("mode %s must not pass --dangerously-skip-permissions: %v", mode, args)
		}
	}

	bypass := &Session{ID: "c-bypass", Agent: "claude-code", NativeID: "nat-bypass", PermissionMode: "bypassPermissions"}
	_, bypassArgs, err := buildPersistentArgs(bypass, true)
	if err != nil {
		t.Fatalf("buildPersistentArgs(bypass): %v", err)
	}
	if !slices.Contains(bypassArgs, "--permission-prompt-tool") || !slices.Contains(bypassArgs, "--dangerously-skip-permissions") {
		t.Fatalf("bypassPermissions args mismatch: %v", bypassArgs)
	}
	if slices.Contains(bypassArgs, "--allow-dangerously-skip-permissions") {
		t.Fatalf("bypassPermissions must not pass --allow-dangerously-skip-permissions: %v", bypassArgs)
	}

	planA := &Session{ID: "fp1", Agent: "claude-code", Cwd: "/tmp/p", Model: "claude-sonnet-4-6", PermissionMode: "plan"}
	planB := &Session{ID: "fp1", Agent: "claude-code", Cwd: "/tmp/p", Model: "claude-opus-4-6", PermissionMode: "plan"}
	bypassA := &Session{ID: "fp1", Agent: "claude-code", Cwd: "/tmp/p", Model: "claude-sonnet-4-6", PermissionMode: "bypassPermissions"}
	if ConfigFingerprintWithoutModel(planA) != ConfigFingerprintWithoutModel(planB) {
		t.Fatalf("ConfigFingerprintWithoutModel changed across model change")
	}
	if ConfigFingerprint(planA) == ConfigFingerprint(bypassA) {
		t.Fatalf("ConfigFingerprint must differ between plan and bypassPermissions")
	}
}

func TestStdioControl_AskUserQuestion_SingleWithSemicolonAndMultiQuestion(t *testing.T) {
	t.Run("single question preserves semicolons and sleep inhibitor releases while blocked", func(t *testing.T) {
		tmpDir := t.TempDir()
		stdinLog := filepath.Join(tmpDir, "stdin.log")
		pidFile := filepath.Join(tmpDir, "child.pid")
		installScriptMockClaude(t, fmt.Sprintf(`
echo $$ > %q
read -r turn1
echo "$turn1" >> %q
echo '{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_q1","name":"AskUserQuestion","input":{}}}'
echo '{"type":"control_request","request_id":"req_q_single","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"tu_q1","input":{"questions":[{"question":"Which store should we use?","header":"Store","options":[{"label":"Redis","description":"Fast"},{"label":"SQLite","description":"Local"}],"multiSelect":false}]}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_q1","content":"answered","is_error":false}]}}'
echo '{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Using your store choice."}}'
echo '{"type":"result","result":"Using your store choice.","is_error":false}'
`, pidFile, stdinLog, stdinLog))

		db, err := InitDB(filepath.Join(tmpDir, "test.db"))
		if err != nil {
			t.Fatalf("InitDB: %v", err)
		}
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)
		defer srv.headless.ReleaseSleepInhibitor()
		srv.headless.sleepInhibitor.cmdFactory = func(_ string, _ int) *exec.Cmd { return exec.Command("sleep", "30") }

		sess := newTestHeadlessSession("claude-code:local:q-single", "q-single", tmpDir, StateIdle)
		sess.PermissionMode = "plan"
		_ = db.SaveSession(sess)

		sub, unsub := srv.headless.Subscribe(sess.ID)
		defer unsub()

		if err := srv.headless.RunTurn(context.Background(), sess, "Ask me"); err != nil {
			t.Fatalf("RunTurn: %v", err)
		}

		waitForWorkerCond(t, 5*time.Second, "blocked_control AskUserQuestion", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateBlocked && cur.Blocked != nil && cur.Blocked.Kind == BlockQuestion && !srv.headless.IsSleepInhibitorActive()
		})
		if srv.headless.IsSleepInhibitorActive() {
			t.Fatalf("sleep inhibitor must be released while blocked_control")
		}

		proseAnswer := "Use Redis; keep SQLite for metadata"
		rec := postSessionRespond(t, srv, map[string]string{
			"id":     sess.ID,
			"action": "answer",
			"answer": proseAnswer,
		})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stdio_control"`) {
			t.Fatalf("unexpected /respond result: code=%d body=%s", rec.Code, rec.Body.String())
		}
		if !srv.headless.IsSleepInhibitorActive() {
			t.Fatalf("sleep inhibitor must re-acquire immediately on control_resolved")
		}

		waitForWorkerCond(t, 5*time.Second, "turn completion after AskUserQuestion", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})
		if srv.headless.IsSleepInhibitorActive() {
			t.Fatalf("sleep inhibitor must be released after turn completion")
		}

		var sawResolved bool
		drainDone := false
		for !drainDone {
			select {
			case e := <-sub:
				if e.Type == "control_resolved" {
					sawResolved = true
				}
			default:
				drainDone = true
			}
		}
		if !sawResolved {
			t.Fatalf("expected control_resolved stream event")
		}

		logBytes, _ := os.ReadFile(stdinLog)
		lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 stdin lines (turn1 + control_response), got %d: %s", len(lines), string(logBytes))
		}
		var ctrlResp struct {
			Type     string `json:"type"`
			Response struct {
				RequestID string `json:"request_id"`
				Response  struct {
					Behavior     string                 `json:"behavior"`
					UpdatedInput map[string]interface{} `json:"updatedInput"`
				} `json:"response"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(lines[1]), &ctrlResp); err != nil {
			t.Fatalf("unmarshal control_response: %v", err)
		}
		if ctrlResp.Response.RequestID != "req_q_single" || ctrlResp.Response.Response.Behavior != "allow" {
			t.Fatalf("unexpected control_response: %+v", ctrlResp)
		}
		answers, _ := ctrlResp.Response.Response.UpdatedInput["answers"].(map[string]interface{})
		if answers["Which store should we use?"] != proseAnswer {
			t.Fatalf("single-question prose answer with semicolon corrupted: got %q, want %q", answers["Which store should we use?"], proseAnswer)
		}
		qs, _ := ctrlResp.Response.Response.UpdatedInput["questions"].([]interface{})
		if len(qs) != 1 {
			t.Fatalf("expected updatedInput.questions preserved verbatim, got %+v", ctrlResp.Response.Response.UpdatedInput)
		}
	})

	t.Run("multi question splits on semicolon", func(t *testing.T) {
		tmpDir := t.TempDir()
		stdinLog := filepath.Join(tmpDir, "stdin-multi.log")
		installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo '{"type":"control_request","request_id":"req_q_multi","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"tu_qm","input":{"questions":[{"question":"Language?","options":[{"label":"Go"}]},{"question":"Database?","options":[{"label":"SQLite"}]}]}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"result","result":"done","is_error":false}'
`, stdinLog))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:q-multi", "q-multi", tmpDir, StateIdle)
		sess.PermissionMode = "plan"
		_ = db.SaveSession(sess)

		if err := srv.headless.RunTurn(context.Background(), sess, "Multi Q"); err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "blocked_control multi", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateBlocked
		})
		rec := postSessionRespond(t, srv, map[string]string{
			"id":     sess.ID,
			"action": "answer",
			"answer": "Go; SQLite",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("/respond failed: %d %s", rec.Code, rec.Body.String())
		}
		waitForWorkerCond(t, 5*time.Second, "turn complete multi", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})

		raw, _ := os.ReadFile(stdinLog)
		var ctrlResp struct {
			Response struct {
				Response struct {
					UpdatedInput struct {
						Answers map[string]string `json:"answers"`
					} `json:"updatedInput"`
				} `json:"response"`
			} `json:"response"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(raw), &ctrlResp); err != nil {
			t.Fatalf("unmarshal multi control_response: %v", err)
		}
		ans := ctrlResp.Response.Response.UpdatedInput.Answers
		if ans["Language?"] != "Go" || ans["Database?"] != "SQLite" {
			t.Fatalf("unexpected multi-question answers: %+v", ans)
		}
	})
}

func TestStdioControl_ExitPlanMode_ApproveReusesWarmPIDAndRejectFeedback(t *testing.T) {
	t.Run("approve updates permissions in-place and Turn 2 reuses exact ChildPID", func(t *testing.T) {
		tmpDir := t.TempDir()
		stdinLog := filepath.Join(tmpDir, "plan-approve.log")
		pidLog := filepath.Join(tmpDir, "pids.log")
		installScriptMockClaude(t, fmt.Sprintf(`
echo $$ >> %q
read -r turn1
echo "$turn1" >> %q
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Plan step 1: refactor handler"}]}}'
echo '{"type":"control_request","request_id":"req_plan_ok","request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode","tool_use_id":"tu_plan1","input":{}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Executed plan!"}}'
echo '{"type":"result","result":"Executed plan!","is_error":false}'
while IFS= read -r turnN; do
  echo $$ >> %q
  echo "$turnN" >> %q
  echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Turn 2 same child!"}}'
  echo '{"type":"result","result":"Turn 2 same child!","is_error":false}'
done
`, pidLog, stdinLog, stdinLog, pidLog, stdinLog))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:plan-ok", "plan-ok", tmpDir, StateIdle)
		sess.PermissionMode = "plan"
		_ = db.SaveSession(sess)

		if err := srv.headless.RunTurn(context.Background(), sess, "Create plan"); err != nil {
			t.Fatalf("RunTurn 1: %v", err)
		}

		waitForWorkerCond(t, 5*time.Second, "blocked on ExitPlanMode", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateBlocked && cur.Blocked != nil && cur.Blocked.Kind == BlockApproval
		})
		curBlocked, _ := db.GetSession(sess.ID)
		if !strings.Contains(curBlocked.Blocked.Reason, "Plan step 1") {
			t.Fatalf("expected fallback assistant plan text in Blocked.Reason, got %q", curBlocked.Blocked.Reason)
		}

		srv.headless.mu.RLock()
		h1 := srv.headless.warmWorkers[sess.ID]
		childPID1 := h1.ChildPID
		workerPID1 := h1.WorkerPID
		srv.headless.mu.RUnlock()

		rec := postSessionRespond(t, srv, map[string]string{
			"id":     sess.ID,
			"action": "approve",
			"answer": "Approve Plan",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("approve /respond failed: %d %s", rec.Code, rec.Body.String())
		}

		waitForWorkerCond(t, 5*time.Second, "Turn 1 completed", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})

		afterTurn1, _ := db.GetSession(sess.ID)
		if afterTurn1.PermissionMode != "bypassPermissions" {
			t.Fatalf("expected PermissionMode=bypassPermissions after approve, got %q", afterTurn1.PermissionMode)
		}

		if err := srv.headless.RunTurn(context.Background(), afterTurn1, "Follow-up task"); err != nil {
			t.Fatalf("RunTurn 2: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "Turn 2 completed", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})

		srv.headless.mu.RLock()
		h2 := srv.headless.warmWorkers[sess.ID]
		childPID2 := h2.ChildPID
		workerPID2 := h2.WorkerPID
		srv.headless.mu.RUnlock()
		if childPID1 <= 1 || childPID2 != childPID1 || workerPID2 != workerPID1 {
			t.Fatalf("expected Turn 2 to reuse exact ChildPID=%d WorkerPID=%d, got ChildPID=%d WorkerPID=%d", childPID1, workerPID1, childPID2, workerPID2)
		}

		pidLines := strings.Split(strings.TrimSpace(mustReadFile(t, pidLog)), "\n")
		if len(pidLines) != 2 || pidLines[0] != pidLines[1] {
			t.Fatalf("expected Turn 1 and Turn 2 handled by exact same child OS PID, got %v", pidLines)
		}

		stdinLines := strings.Split(strings.TrimSpace(mustReadFile(t, stdinLog)), "\n")
		if len(stdinLines) != 3 {
			t.Fatalf("expected 3 stdin lines, got %d: %v", len(stdinLines), stdinLines)
		}
		var ctrlResp map[string]any
		_ = json.Unmarshal([]byte(stdinLines[1]), &ctrlResp)
		respInner := ctrlResp["response"].(map[string]any)["response"].(map[string]any)
		if respInner["behavior"] != "allow" {
			t.Fatalf("expected behavior=allow, got %+v", respInner)
		}
		perms, _ := respInner["updatedPermissions"].([]any)
		if len(perms) != 1 {
			t.Fatalf("expected updatedPermissions len 1, got %+v", respInner["updatedPermissions"])
		}
		p0 := perms[0].(map[string]any)
		if p0["type"] != "setMode" || p0["mode"] != "bypassPermissions" || p0["destination"] != "session" {
			t.Fatalf("unexpected updatedPermissions[0]: %+v", p0)
		}
	})

	t.Run("reject sends deny with feedback on same PID", func(t *testing.T) {
		tmpDir := t.TempDir()
		stdinLog := filepath.Join(tmpDir, "plan-reject.log")
		installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo '{"type":"control_request","request_id":"req_plan_rej","request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode","tool_use_id":"tu_plan_rej","input":{"plan":"Initial plan"}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"result","result":"Revising plan.","is_error":false}'
`, stdinLog))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:plan-rej", "plan-rej", tmpDir, StateIdle)
		sess.PermissionMode = "plan"
		_ = db.SaveSession(sess)

		if err := srv.headless.RunTurn(context.Background(), sess, "Plan it"); err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "blocked on ExitPlanMode reject", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateBlocked
		})
		rec := postSessionRespond(t, srv, map[string]string{
			"id":     sess.ID,
			"action": "reject",
			"value":  "Include rollback tests",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("reject /respond failed: %d %s", rec.Code, rec.Body.String())
		}
		waitForWorkerCond(t, 5*time.Second, "turn complete after reject", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})
		after, _ := db.GetSession(sess.ID)
		if after.PermissionMode != "plan" {
			t.Fatalf("expected PermissionMode to remain plan on reject, got %q", after.PermissionMode)
		}
		raw := mustReadFile(t, stdinLog)
		if !strings.Contains(raw, `"behavior":"deny"`) || !strings.Contains(raw, `"Include rollback tests"`) {
			t.Fatalf("expected deny feedback in control_response, got: %s", raw)
		}
	})
}

func TestStdioControl_GenericPermissionCancelAndKeepAlive(t *testing.T) {
	tmpDir := t.TempDir()
	stdinLog := filepath.Join(tmpDir, "perm.log")
	releaseCancel := filepath.Join(tmpDir, "cancel.flag")
	installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo '{"type":"keep_alive"}'
echo '{"type":"control_request","request_id":"req_bash_1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tu_b1","decision_reason":"Needs shell approval","input":{"command":"ls -la"}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"control_request","request_id":"req_cancel_1","request":{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"tu_w1","input":{"file_path":"a.txt"}}}'
while [ ! -f %q ]; do sleep 0.02; done
echo '{"type":"control_cancel_request","request_id":"req_cancel_1"}'
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Resumed after cancel"}}'
echo '{"type":"result","result":"Resumed after cancel","is_error":false}'
`, stdinLog, releaseCancel))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ShutdownAllWorkers(time.Second)
	defer srv.headless.ReleaseSleepInhibitor()
	srv.headless.sleepInhibitor.cmdFactory = func(_ string, _ int) *exec.Cmd { return exec.Command("sleep", "30") }

	sess := newTestHeadlessSession("claude-code:local:perm-cancel", "perm-cancel", tmpDir, StateIdle)
	sess.PermissionMode = "auto"
	_ = db.SaveSession(sess)

	if err := srv.headless.RunTurn(context.Background(), sess, "Run command"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	waitForWorkerCond(t, 5*time.Second, "blocked on Bash permission", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateBlocked && cur.Blocked != nil && cur.Blocked.Kind == BlockPermission
	})
	curPerm, _ := db.GetSession(sess.ID)
	if !strings.Contains(curPerm.Blocked.Reason, "Bash") || !strings.Contains(curPerm.Blocked.Reason, "ls -la") || !strings.Contains(curPerm.Blocked.Reason, "Needs shell approval") {
		t.Fatalf("unexpected permission block summary: %+v", curPerm.Blocked)
	}

	rec := postSessionRespond(t, srv, map[string]string{
		"id":     sess.ID,
		"action": "allow",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("allow /respond failed: %d %s", rec.Code, rec.Body.String())
	}

	waitForWorkerCond(t, 5*time.Second, "blocked on req_cancel_1", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateBlocked && cur.Blocked != nil && cur.Blocked.RequestID == "req_cancel_1" && !srv.headless.IsSleepInhibitorActive()
	})

	_ = os.WriteFile(releaseCancel, []byte("go"), 0600)
	waitForWorkerCond(t, 5*time.Second, "completed after control_cancel_request", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
	})

	raw := mustReadFile(t, stdinLog)
	if !strings.Contains(raw, `"behavior":"allow"`) || !strings.Contains(raw, `"command":"ls -la"`) {
		t.Fatalf("expected allow response with preserved input, got %s", raw)
	}
}

func TestStdioControl_DaemonRestartReattachWhileBlockedControl(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dbPath := filepath.Join(t.TempDir(), "ackbard.db")
	stdinLog := filepath.Join(tmpHome, "reattach-stdin.log")
	pidLog := filepath.Join(tmpHome, "reattach-pid.log")

	installScriptMockClaude(t, fmt.Sprintf(`
echo $$ > %q
read -r turn1
echo '{"type":"control_request","request_id":"req_reattach_1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tu_ra1","input":{"command":"echo hi"}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Continued after daemon restart"}}'
echo '{"type":"result","result":"Continued after daemon restart","is_error":false}'
`, pidLog, stdinLog))

	db1, _ := InitDB(dbPath)
	s1 := NewServer(db1)
	sess := newTestHeadlessSession("claude-code:local:reattach-ctrl", "reattach-ctrl", tmpHome, StateIdle)
	sess.PermissionMode = "auto"
	_ = db1.SaveSession(sess)

	if err := s1.headless.RunTurn(context.Background(), sess, "Do work"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "blocked_control before restart", func() bool {
		cur, _ := db1.GetSession(sess.ID)
		return cur != nil && cur.State == StateBlocked && cur.Blocked != nil && cur.Blocked.RequestID == "req_reattach_1"
	})

	origChildPID := strings.TrimSpace(mustReadFile(t, pidLog))
	s1.headless.stopAllWarmWatchers()
	s1.headless.ReleaseSleepInhibitor()
	_ = db1.Close()

	db2, _ := InitDB(dbPath)
	defer db2.Close()
	s2 := NewServer(db2)
	defer s2.headless.ShutdownAllWorkers(time.Second)

	if !s2.headless.IsRunning(sess.ID) || !s2.headless.IsBlockedControl(sess.ID) {
		t.Fatalf("expected reattached session running=%v blocked_control=%v", s2.headless.IsRunning(sess.ID), s2.headless.IsBlockedControl(sess.ID))
	}
	reattached, _ := db2.GetSession(sess.ID)
	if reattached == nil || reattached.State != StateBlocked || reattached.Blocked == nil || reattached.Blocked.RequestID != "req_reattach_1" {
		t.Fatalf("expected StateBlocked with req_reattach_1 after reattach, got %+v", reattached)
	}

	rec := postSessionRespond(t, s2, map[string]string{
		"id":     sess.ID,
		"action": "allow",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("/respond after reattach failed: %d %s", rec.Code, rec.Body.String())
	}

	waitForWorkerCond(t, 5*time.Second, "turn finalized after reattach answer", func() bool {
		cur, _ := db2.GetSession(sess.ID)
		return cur != nil && cur.State == StateIdle && !s2.headless.IsRunning(sess.ID)
	})
	if gotPID := strings.TrimSpace(mustReadFile(t, pidLog)); gotPID != origChildPID {
		t.Fatalf("expected surviving child PID %s, got %s", origChildPID, gotPID)
	}
	raw := mustReadFile(t, stdinLog)
	if !strings.Contains(raw, `"request_id":"req_reattach_1"`) || !strings.Contains(raw, `"behavior":"allow"`) {
		t.Fatalf("expected allow control_response on surviving worker, got %s", raw)
	}
}

func mustReadFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestStdioControl_FailClosedAndStaleRespondConflict(t *testing.T) {
	t.Run("dismiss on BlockPermission sends deny", func(t *testing.T) {
		tmpDir := t.TempDir()
		stdinLog := filepath.Join(tmpDir, "dismiss.log")
		installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo "$turn1" >> %q
echo '{"type":"control_request","request_id":"req_perm_dismiss","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tu_bash_d","input":{"command":"rm -rf /tmp/x"}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Skipped."}}'
echo '{"type":"result","result":"Skipped.","is_error":false}'
read -r hold_line
`, stdinLog, stdinLog))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:perm-dismiss", "perm-dismiss", tmpDir, StateIdle)
		_ = db.SaveSession(sess)
		if err := srv.headless.RunTurn(context.Background(), sess, "Run cmd"); err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "blocked on Bash permission", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateBlocked && cur.Blocked != nil && cur.Blocked.Kind == BlockPermission
		})

		rec := postSessionRespond(t, srv, map[string]string{"id": sess.ID, "action": "dismiss"})
		if rec.Code != http.StatusOK {
			t.Fatalf("dismiss /respond status %d: %s", rec.Code, rec.Body.String())
		}
		waitForWorkerCond(t, 5*time.Second, "turn finalized", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})
		raw := mustReadFile(t, stdinLog)
		if !strings.Contains(raw, `"request_id":"req_perm_dismiss"`) || !strings.Contains(raw, `"behavior":"deny"`) {
			t.Fatalf("expected dismiss on BlockPermission to send deny, got %s", raw)
		}
	})

	t.Run("ExitPlanMode with nil DB Blocked and rejection text stays plan and sends deny", func(t *testing.T) {
		tmpDir := t.TempDir()
		stdinLog := filepath.Join(tmpDir, "plan-nil.log")
		installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo "$turn1" >> %q
echo '{"type":"control_request","request_id":"req_plan_nil","request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode","tool_use_id":"tu_p_nil","input":{"plan":"Step 1"}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Revised."}}'
echo '{"type":"result","result":"Revised.","is_error":false}'
read -r hold_line
`, stdinLog, stdinLog))

		db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
		defer db.Close()
		srv := NewServer(db)
		defer srv.headless.ShutdownAllWorkers(time.Second)

		sess := newTestHeadlessSession("claude-code:local:plan-nil", "plan-nil", tmpDir, StateIdle)
		sess.PermissionMode = "plan"
		_ = db.SaveSession(sess)
		if err := srv.headless.RunTurn(context.Background(), sess, "Plan"); err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		waitForWorkerCond(t, 5*time.Second, "blocked on ExitPlanMode", func() bool {
			cur, _ := db.GetSession(sess.ID)
			return cur != nil && cur.State == StateBlocked
		})

		cur, _ := db.GetSession(sess.ID)
		cur.Blocked = nil
		_ = db.SaveSession(cur)

		rec := postSessionRespond(t, srv, map[string]string{
			"id":     sess.ID,
			"action": "answer",
			"value":  "No, revise step 2",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("/respond status %d: %s", rec.Code, rec.Body.String())
		}
		waitForWorkerCond(t, 5*time.Second, "turn finalized", func() bool {
			c, _ := db.GetSession(sess.ID)
			return c != nil && c.State == StateIdle && !srv.headless.IsRunning(sess.ID)
		})
		after, _ := db.GetSession(sess.ID)
		if after.PermissionMode != "plan" {
			t.Fatalf("expected PermissionMode to remain plan on rejection feedback, got %q", after.PermissionMode)
		}
		raw := mustReadFile(t, stdinLog)
		if !strings.Contains(raw, `"behavior":"deny"`) || !strings.Contains(raw, "No, revise step 2") {
			t.Fatalf("expected deny control_response with feedback, got %s", raw)
		}

		staleRec := postSessionRespond(t, srv, map[string]string{"id": sess.ID, "action": "allow"})
		if staleRec.Code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict for stale /respond in idle_warm, got %d: %s", staleRec.Code, staleRec.Body.String())
		}
		if !srv.headless.HasWarmWorker(sess.ID) {
			t.Fatalf("expected stale /respond not to kill warm worker")
		}
		if lines := strings.Split(strings.TrimSpace(mustReadFile(t, stdinLog)), "\n"); len(lines) != 2 {
			t.Fatalf("expected zero extra stdin writes on stale 409 /respond, got %d", len(lines))
		}

		srv.headless.ShutdownAllWorkers(time.Second)
		staleIdleRec := postSessionRespond(t, srv, map[string]string{"id": sess.ID, "action": "allow"})
		if staleIdleRec.Code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict for /respond on idle session without worker, got %d: %s", staleIdleRec.Code, staleIdleRec.Body.String())
		}
	})
}

func TestStdioControl_HttpHooksPreserveBlockedControl(t *testing.T) {
	tmpDir := t.TempDir()
	stdinLog := filepath.Join(tmpDir, "hooks.log")
	installScriptMockClaude(t, fmt.Sprintf(`
read -r turn1
echo "$turn1" >> %q
echo '{"type":"control_request","request_id":"req_hook_keep","request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode","tool_use_id":"tu_hk","input":{"plan":"Keep gate"}}}'
read -r ctrl1
echo "$ctrl1" >> %q
echo '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done."}}'
echo '{"type":"result","result":"Done.","is_error":false}'
read -r hold_line
`, stdinLog, stdinLog))

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ShutdownAllWorkers(time.Second)

	nativeID := "33333333-3333-3333-3333-333333333333"
	sess := newTestHeadlessSession("claude-code:"+srv.HostName()+":"+nativeID, nativeID, tmpDir, StateIdle)
	sess.Host = srv.HostName()
	sess.PermissionMode = "plan"
	_ = db.SaveSession(sess)

	if err := srv.headless.RunTurn(context.Background(), sess, "Plan with hooks"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "blocked on ExitPlanMode", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateBlocked && cur.Blocked != nil && cur.Blocked.RequestID == "req_hook_keep"
	})

	for _, ev := range []*Event{
		{Agent: "claude-code", NativeID: nativeID, Cwd: tmpDir, EventName: "PostToolUse", ToolName: "Read", State: StateWorking, LastEventAt: time.Now()},
		{Agent: "claude-code", NativeID: nativeID, Cwd: tmpDir, EventName: "SubagentStart", ToolName: "Explore", State: StateWorking, LastEventAt: time.Now()},
		{Agent: "claude-code", NativeID: nativeID, Cwd: tmpDir, EventName: "PermissionRequest", State: StateBlocked, Blocked: &Blocked{Kind: BlockPermission, Reason: "Bash"}, LastEventAt: time.Now()},
		{Agent: "claude-code", NativeID: nativeID, Cwd: tmpDir, EventName: "Stop", State: StateIdle, LastEventAt: time.Now()},
	} {
		srv.processParsedHookEvent(ev, ev.EventName, sess.Host, "", false)
	}

	cur, _ := db.GetSession(sess.ID)
	if cur.State != StateBlocked || cur.Blocked == nil || cur.Blocked.Kind != BlockApproval || cur.Blocked.RequestID != "req_hook_keep" {
		t.Fatalf("expected HTTP hooks to preserve stdio blocked_control state, got state=%v blocked=%+v", cur.State, cur.Blocked)
	}

	rec := postSessionRespond(t, srv, map[string]string{"id": sess.ID, "action": "approve"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /respond to succeed after hooks, got %d: %s", rec.Code, rec.Body.String())
	}
	waitForWorkerCond(t, 5*time.Second, "turn finalized", func() bool {
		c, _ := db.GetSession(sess.ID)
		return c != nil && c.State == StateIdle && !srv.headless.IsRunning(sess.ID)
	})
}

func TestStdioControl_CancelTurnAndRestartChildClearBlockedControl(t *testing.T) {
	tmpDir := t.TempDir()
	installScriptMockClaude(t, `
read -r turn1
echo '{"type":"control_request","request_id":"req_cancel_1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tu_c1","input":{"command":"sleep 10"}}}'
read -r hold_line
`)

	db, _ := InitDB(filepath.Join(tmpDir, "test.db"))
	defer db.Close()
	srv := NewServer(db)
	defer srv.headless.ShutdownAllWorkers(time.Second)

	sess := newTestHeadlessSession("claude-code:local:cancel-ctrl", "cancel-ctrl", tmpDir, StateIdle)
	_ = db.SaveSession(sess)

	if err := srv.headless.RunTurn(context.Background(), sess, "Do bash"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "blocked_control active", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateBlocked && srv.headless.IsBlockedControl(sess.ID)
	})

	if err := srv.headless.CancelTurn(sess.ID); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	waitForWorkerCond(t, 5*time.Second, "cancelled back to StateIdle", func() bool {
		cur, _ := db.GetSession(sess.ID)
		return cur != nil && cur.State == StateIdle && cur.Blocked == nil && !srv.headless.IsRunning(sess.ID) && !srv.headless.IsBlockedControl(sess.ID)
	})

	srv.headless.setControlBlockState(sess.ID)
	srv.headless.setBgTaskCount(sess.ID, sess.NativeID, 3)
	if !srv.headless.IsBlockedControl(sess.ID) || srv.headless.GetBgTaskCount(sess.ID) != 3 {
		t.Fatalf("expected setup blockedControl and bgTaskCount=3")
	}
	srv.headless.RestartSessionChild(sess, time.Second)
	if srv.headless.IsBlockedControl(sess.ID) || srv.headless.GetBgTaskCount(sess.ID) != 0 {
		t.Fatalf("expected RestartSessionChild to clear blockedControl=%v and bgTaskCount=%d", srv.headless.IsBlockedControl(sess.ID), srv.headless.GetBgTaskCount(sess.ID))
	}
}
