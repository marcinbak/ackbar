package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type SessionWorkerMeta struct {
	WorkerID          string        `json:"worker_id"`
	SessionID         string        `json:"session_id"`
	OrigSessionID     string        `json:"orig_session_id,omitempty"`
	NativeID          string        `json:"native_id"`
	Agent             string        `json:"agent"`
	Cwd               string        `json:"cwd,omitempty"`
	IsSupervisor      bool          `json:"is_supervisor,omitempty"`
	ConfigFingerprint string        `json:"config_fingerprint"`
	Bin               string        `json:"bin"`
	Args              []string      `json:"args,omitempty"`
	SocketPath        string        `json:"socket_path"`
	SocketTempDir     string        `json:"socket_temp_dir,omitempty"`
	WorkerMetaPath    string        `json:"worker_meta_path,omitempty"`
	WorkerPID         int           `json:"worker_pid,omitempty"`
	ChildPID          int           `json:"child_pid,omitempty"`
	State             string        `json:"state"`
	ActiveTurnID      string        `json:"active_turn_id,omitempty"`
	ActiveMetaPath    string        `json:"active_meta_path,omitempty"`
	IdleTTLNs         time.Duration `json:"idle_ttl_ns,omitempty"`
	StartedAt         time.Time     `json:"started_at"`
	LastTurnEndedAt   time.Time     `json:"last_turn_ended_at,omitempty"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

type workerIPCReq struct {
	Op        string `json:"op"`
	TurnID    string `json:"turn_id,omitempty"`
	AfterSeq  int64  `json:"after_seq,omitempty"`
	MetaPath  string `json:"meta_path,omitempty"`
	StdinLine string `json:"stdin_line,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	NativeID  string `json:"native_id,omitempty"`
}

type workerIPCResp struct {
	OK                bool   `json:"ok"`
	ErrCode           string `json:"err_code,omitempty"`
	WorkerPID         int    `json:"worker_pid,omitempty"`
	ChildPID          int    `json:"child_pid,omitempty"`
	TurnID            string `json:"turn_id,omitempty"`
	State             string `json:"state,omitempty"`
	ConfigFingerprint string `json:"config_fingerprint,omitempty"`
}

type boundedTailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *boundedTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if b.max > 0 && len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *boundedTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
func (b *boundedTailBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = nil
}

func allocateWorkerSocketPath(runDir, id string) (string, string, error) {
	if cand := filepath.Join(runDir, id+".sock"); len(cand) <= 96 {
		return cand, "", nil
	}
	td, err := os.MkdirTemp("", "akw-*")
	if err != nil || os.Chmod(td, 0700) != nil {
		if td != "" {
			_ = os.RemoveAll(td)
		}
		return "", "", err
	}
	return filepath.Join(td, "w.sock"), td, nil
}

func cleanupWorkerArtifacts(meta *SessionWorkerMeta) {
	if meta != nil {
		_ = os.Remove(meta.SocketPath)
		_ = os.RemoveAll(meta.SocketTempDir)
		_ = os.Remove(meta.WorkerMetaPath)
	}
}

func WriteSessionWorkerMeta(path string, meta *SessionWorkerMeta) error {
	if path == "" || meta == nil {
		return errors.New("invalid worker meta path or nil meta")
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wmeta-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_ = tmp.Chmod(0600)
	_, wErr := tmp.Write(data)
	_ = tmp.Close()
	if wErr != nil || os.Rename(tmpName, path) != nil {
		_ = os.Remove(tmpName)
		if wErr != nil {
			return wErr
		}
		return errors.New("failed to rename temp meta")
	}
	return os.Chmod(path, 0600)
}

func ReadSessionWorkerMeta(path string) (*SessionWorkerMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta SessionWorkerMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.WorkerMetaPath == "" {
		meta.WorkerMetaPath = path
	}
	return &meta, nil
}

func callWorkerSocket(sockPath string, req workerIPCReq, timeout time.Duration) (*workerIPCResp, error) {
	if sockPath == "" {
		return nil, errors.New("empty socket path")
	}
	conn, err := net.DialTimeout("unix", sockPath, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	var resp workerIPCResp
	if (err != nil && len(line) == 0) || json.Unmarshal(line, &resp) != nil {
		return nil, errors.New("invalid worker response")
	}
	return &resp, nil
}

func streamSpoolToConn(c net.Conn, spool *TurnSpoolWriter, compPath string, afterSeq int64) {
	writeEvt := func(evt ChatStreamEvent) bool {
		b, err := json.Marshal(evt)
		if err != nil {
			return true
		}
		_, err = c.Write(append(b, '\n'))
		return err == nil
	}
	if spool != nil {
		if replay, ch, unsub, err := spool.SubscribeFrom(afterSeq); err == nil {
			defer unsub()
			for _, evt := range replay {
				if !writeEvt(evt) {
					return
				}
			}
			for evt := range ch {
				if !writeEvt(evt) {
					return
				}
			}
			return
		}
	}
	if compPath != "" {
		evts, _, _ := ReadTurnSpoolAfter(compPath, afterSeq)
		for _, evt := range evts {
			if !writeEvt(evt) {
				return
			}
		}
	}
}

func handleWorkerIPC(c net.Conn, stateMu *sync.Mutex, state *string, workerMeta *SessionWorkerMeta, childPGID int, activeTurn **activeTurnState, completedTurns map[string]string, subWG *sync.WaitGroup, shutdownChildGroup func(time.Duration), exitCleanly func(int), startTurnLocked func(string, string) error) {
	defer c.Close()
	line, err := bufio.NewReader(c).ReadBytes('\n')
	var req workerIPCReq
	if (err != nil && len(line) == 0) || json.Unmarshal(line, &req) != nil {
		return
	}
	reply := func(resp workerIPCResp) {
		b, _ := json.Marshal(resp)
		_, _ = c.Write(append(b, '\n'))
	}
	switch req.Op {
	case "status", "ping":
		stateMu.Lock()
		ok, curState, activeTID := req.TurnID == "" || (*activeTurn != nil && (*activeTurn).turnID == req.TurnID), *state, ""
		if *activeTurn != nil {
			activeTID = (*activeTurn).turnID
		}
		stateMu.Unlock()
		reply(workerIPCResp{OK: ok, WorkerPID: os.Getpid(), ChildPID: childPGID, TurnID: activeTID, State: curState, ConfigFingerprint: workerMeta.ConfigFingerprint})

	case "send_turn":
		stateMu.Lock()
		if *state == "shutting_down" || *state == "exited" {
			reply(workerIPCResp{OK: false, ErrCode: *state, WorkerPID: os.Getpid(), ChildPID: childPGID})
			stateMu.Unlock()
			return
		}
		if *state == "running_turn" || *activeTurn != nil {
			reply(workerIPCResp{OK: false, ErrCode: "busy", WorkerPID: os.Getpid(), ChildPID: childPGID})
			stateMu.Unlock()
			return
		}
		if req.SessionID != "" {
			workerMeta.SessionID = req.SessionID
		}
		if req.NativeID != "" {
			workerMeta.NativeID = req.NativeID
		}
		startErr := startTurnLocked(req.MetaPath, req.StdinLine)
		curTurnID := ""
		if *activeTurn != nil {
			curTurnID = (*activeTurn).turnID
		}
		stateMu.Unlock()
		if startErr != nil {
			reply(workerIPCResp{OK: false, ErrCode: startErr.Error()})
			return
		}
		reply(workerIPCResp{OK: true, WorkerPID: os.Getpid(), ChildPID: childPGID, TurnID: curTurnID, State: "running_turn"})

	case "subscribe":
		subWG.Add(1)
		defer subWG.Done()
		stateMu.Lock()
		var spool *TurnSpoolWriter
		compPath := filepath.Join(filepath.Dir(workerMeta.SocketPath), req.TurnID+".ndjson")
		if *activeTurn != nil && (req.TurnID == "" || (*activeTurn).turnID == req.TurnID) {
			spool = (*activeTurn).spool
		} else if p, ok := completedTurns[req.TurnID]; ok {
			compPath = p
		}
		stateMu.Unlock()
		streamSpoolToConn(c, spool, compPath, req.AfterSeq)

	case "cancel", "shutdown":
		stateMu.Lock()
		*state, workerMeta.State = "shutting_down", "shutting_down"
		if workerMeta.WorkerMetaPath != "" {
			_ = WriteSessionWorkerMeta(workerMeta.WorkerMetaPath, workerMeta)
		}
		isCancel, cancelledTID := req.Op == "cancel", ""
		if *activeTurn != nil {
			cancelledTID = (*activeTurn).turnID
			if isCancel {
				if req.TurnID != "" && cancelledTID != req.TurnID {
					stateMu.Unlock()
					reply(workerIPCResp{OK: false, ErrCode: "turn_mismatch"})
					return
				}
				(*activeTurn).cancelled = true
			}
		}
		stateMu.Unlock()
		if isCancel {
			reply(workerIPCResp{OK: true, WorkerPID: os.Getpid(), ChildPID: childPGID, TurnID: cancelledTID})
			go func() { shutdownChildGroup(500 * time.Millisecond); exitCleanly(0) }()
			return
		}
		shutdownChildGroup(500 * time.Millisecond)
		reply(workerIPCResp{OK: true, WorkerPID: os.Getpid(), ChildPID: childPGID})
		exitCleanly(0)
	}
}
