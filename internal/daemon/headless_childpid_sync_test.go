package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMonitorWorkerTurn_SyncsColdStartChildPIDBeforeBlockedState(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	bcastCh := make(chan State, 16)
	runner := NewHeadlessRunner(db, func(s *Session) {
		if s != nil {
			select {
			case bcastCh <- s.State:
			default:
			}
		}
	})
	sess := newTestHeadlessSession("claude-code:local:cold-pid-sync", "cold-pid-sync", tmpDir, StateWorking)
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	sockPath, sockTempDir, err := allocateWorkerSocketPath(tmpDir, "cold-pid")
	if err != nil {
		t.Fatalf("allocateWorkerSocketPath: %v", err)
	}
	if sockTempDir != "" {
		t.Cleanup(func() { _ = os.RemoveAll(sockTempDir) })
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	workerMetaPath := filepath.Join(tmpDir, "w1.worker.json")
	metaPath := filepath.Join(tmpDir, "turn1.meta.json")
	spoolPath := filepath.Join(tmpDir, "turn1.ndjson")
	if err := os.WriteFile(spoolPath, nil, 0600); err != nil {
		t.Fatalf("write spool: %v", err)
	}

	workerMeta := &SessionWorkerMeta{
		WorkerID: "w1", SessionID: sess.ID, NativeID: sess.NativeID, Agent: sess.Agent,
		SocketPath: sockPath, WorkerMetaPath: workerMetaPath, State: "running_turn", ChildPID: 0,
	}
	if err := WriteSessionWorkerMeta(workerMetaPath, workerMeta); err != nil {
		t.Fatalf("WriteSessionWorkerMeta: %v", err)
	}
	meta := &TurnSpoolMeta{
		TurnID: "turn1", SessionID: sess.ID, OrigSessionID: sess.ID, NativeID: sess.NativeID, Agent: sess.Agent,
		SocketPath: sockPath, SpoolPath: spoolPath, MetaPath: metaPath,
		WorkerID: "w1", WorkerMetaPath: workerMetaPath, Status: "running", ChildPID: 0,
	}
	if err := WriteTurnSpoolMeta(metaPath, meta); err != nil {
		t.Fatalf("WriteTurnSpoolMeta: %v", err)
	}

	runner.mu.Lock()
	runner.warmWorkers[sess.ID] = &warmWorkerHandle{
		WorkerID: "w1", SessionID: sess.ID, NativeID: sess.NativeID,
		SocketPath: sockPath, WorkerMetaPath: workerMetaPath, ChildPID: 0,
	}
	runner.activeMonitors++
	runner.mu.Unlock()

	sub, unsub := runner.SubscribeWithReplay(sess.ID, "turn1", 0, sess.Agent)
	defer unsub()

	sendBlocked := make(chan struct{})
	finishTurn := make(chan struct{})
	srvDone := make(chan error, 1)
	go func() {
		conn, aErr := ln.Accept()
		if aErr != nil {
			srvDone <- aErr
			return
		}
		line, rErr := bufio.NewReader(conn).ReadBytes('\n')
		if rErr != nil {
			_ = conn.Close()
			srvDone <- rErr
			return
		}
		var req workerIPCReq
		if uErr := json.Unmarshal(line, &req); uErr != nil || req.Op != "subscribe" {
			_ = conn.Close()
			srvDone <- fmt.Errorf("unexpected req %q (err=%v)", string(line), uErr)
			return
		}
		writeEvt := func(evt ChatStreamEvent) error {
			b, mErr := json.Marshal(evt)
			if mErr != nil {
				return mErr
			}
			_, wErr := conn.Write(append(b, '\n'))
			return wErr
		}
		if wErr := writeEvt(ChatStreamEvent{Seq: 1, SessionID: sess.ID, TurnID: "turn1", Type: "turn_start"}); wErr != nil {
			_ = conn.Close()
			srvDone <- wErr
			return
		}
		<-sendBlocked
		if wErr := writeEvt(ChatStreamEvent{
			Seq: 2, SessionID: sess.ID, TurnID: "turn1", Type: "interactive_block", ToolName: "ExitPlanMode",
			Blocked: &Blocked{Kind: BlockApproval, RequestID: "req_plan_sync", Reason: "Plan ready"},
		}); wErr != nil {
			_ = conn.Close()
			srvDone <- wErr
			return
		}
		<-finishTurn
		tmDone := *meta
		tmDone.ChildPID = 42424
		tmDone.Status = "completed"
		_ = WriteTurnSpoolMeta(metaPath, &tmDone)
		_ = writeEvt(ChatStreamEvent{Seq: 3, SessionID: sess.ID, TurnID: "turn1", Type: "turn_complete"})
		_ = conn.Close()
		_ = ln.Close()
		srvDone <- nil
	}()

	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		runner.monitorWorkerTurn(sess, sess.ID, meta, nil, 0)
	}()

	select {
	case evt := <-sub:
		if evt.Seq != 1 || evt.Type != "turn_start" {
			t.Fatalf("unexpected initial event: %+v", evt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for monitorWorkerTurn to enter sc.Scan()")
	}

	runner.mu.RLock()
	earlyPID := runner.warmWorkers[sess.ID].ChildPID
	runner.mu.RUnlock()
	if earlyPID != 0 {
		t.Fatalf("expected ChildPID == 0 before cold-start persistence, got %d", earlyPID)
	}

	wmUpdated := *workerMeta
	wmUpdated.ChildPID = 42424
	if err := WriteSessionWorkerMeta(workerMetaPath, &wmUpdated); err != nil {
		t.Fatalf("WriteSessionWorkerMeta update: %v", err)
	}
	tmUpdated := *meta
	tmUpdated.ChildPID = 42424
	if err := WriteTurnSpoolMeta(metaPath, &tmUpdated); err != nil {
		t.Fatalf("WriteTurnSpoolMeta update: %v", err)
	}
	close(sendBlocked)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case st := <-bcastCh:
			if st == StateBlocked {
				cur, gErr := db.GetSession(sess.ID)
				if gErr != nil || cur == nil || cur.State != StateBlocked {
					t.Fatalf("expected DB session StateBlocked, got %+v err=%v", cur, gErr)
				}
				runner.mu.RLock()
				wh := runner.warmWorkers[sess.ID]
				gotPID := 0
				if wh != nil {
					gotPID = wh.ChildPID
				}
				runner.mu.RUnlock()
				if gotPID != 42424 {
					t.Fatalf("expected warmWorkers[%q].ChildPID == 42424 upon StateBlocked, got %d", sess.ID, gotPID)
				}
				goto doneBlocked
			}
		case <-deadline:
			t.Fatal("timed out waiting for StateBlocked broadcast")
		}
	}

doneBlocked:
	close(finishTurn)
	if err := <-srvDone; err != nil {
		t.Fatalf("mock worker socket failed: %v", err)
	}
	select {
	case <-monitorDone:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for monitorWorkerTurn to exit")
	}
}
