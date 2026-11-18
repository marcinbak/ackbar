package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ackbar/internal/agent"
)

type SessionWorkerMeta struct {
	WorkerID                 string                 `json:"worker_id"`
	SessionID                string                 `json:"session_id"`
	OrigSessionID            string                 `json:"orig_session_id,omitempty"`
	NativeID                 string                 `json:"native_id"`
	Agent                    string                 `json:"agent"`
	Cwd                      string                 `json:"cwd,omitempty"`
	IsSupervisor             bool                   `json:"is_supervisor,omitempty"`
	ConfigFingerprint        string                 `json:"config_fingerprint"`
	ConfigFingerprintNoModel string                 `json:"config_fingerprint_no_model,omitempty"`
	ImmutableFingerprint     string                 `json:"immutable_fingerprint,omitempty"`
	MCPFingerprint           string                 `json:"mcp_fingerprint,omitempty"`
	IsExisting               bool                   `json:"is_existing,omitempty"`
	SystemPrompt             string                 `json:"system_prompt,omitempty"`
	PermissionMode           string                 `json:"permission_mode,omitempty"`
	Model                    string                 `json:"model,omitempty"`
	Effort                   string                 `json:"effort,omitempty"`
	Bin                      string                 `json:"bin"`
	Args                     []string               `json:"args,omitempty"`
	SocketPath               string                 `json:"socket_path"`
	SocketTempDir            string                 `json:"socket_temp_dir,omitempty"`
	WorkerMetaPath           string                 `json:"worker_meta_path,omitempty"`
	WorkerPID                int                    `json:"worker_pid,omitempty"`
	ChildPID                 int                    `json:"child_pid,omitempty"`
	State                    string                 `json:"state"`
	ActiveTurnID             string                 `json:"active_turn_id,omitempty"`
	ActiveMetaPath           string                 `json:"active_meta_path,omitempty"`
	BgTaskCount              int                    `json:"bg_task_count,omitempty"`
	PendingControl           *PendingControlRequest `json:"pending_control,omitempty"`
	IdleTTLNs                time.Duration          `json:"idle_ttl_ns,omitempty"`
	StartedAt                time.Time              `json:"started_at"`
	LastTurnEndedAt          time.Time              `json:"last_turn_ended_at,omitempty"`
	UpdatedAt                time.Time              `json:"updated_at"`
}

type workerIPCReq struct {
	Op                       string   `json:"op"`
	TurnID                   string   `json:"turn_id,omitempty"`
	AfterSeq                 int64    `json:"after_seq,omitempty"`
	MetaPath                 string   `json:"meta_path,omitempty"`
	StdinLine                string   `json:"stdin_line,omitempty"`
	Prompt                   string   `json:"prompt,omitempty"`
	Model                    string   `json:"model,omitempty"`
	Effort                   string   `json:"effort,omitempty"`
	PermissionMode           string   `json:"permission_mode,omitempty"`
	SessionID                string   `json:"session_id,omitempty"`
	NativeID                 string   `json:"native_id,omitempty"`
	Bin                      string   `json:"bin,omitempty"`
	Args                     []string `json:"args,omitempty"`
	Env                      []string `json:"env,omitempty"`
	ConfigFingerprint        string   `json:"config_fingerprint,omitempty"`
	ConfigFingerprintNoModel string   `json:"config_fingerprint_no_model,omitempty"`
	ImmutableFingerprint     string   `json:"immutable_fingerprint,omitempty"`
	MCPFingerprint           string   `json:"mcp_fingerprint,omitempty"`
	IsExisting               bool     `json:"is_existing,omitempty"`
	RequestID                string   `json:"request_id,omitempty"`
	Action                   string   `json:"action,omitempty"`
	Approved                 bool     `json:"approved,omitempty"`
	Answer                   string   `json:"answer,omitempty"`
	TargetID                 string   `json:"target_id,omitempty"`
}

type workerIPCResp struct {
	OK                       bool                   `json:"ok"`
	ErrCode                  string                 `json:"err_code,omitempty"`
	Approved                 bool                   `json:"approved,omitempty"`
	PlanApproved             bool                   `json:"plan_approved,omitempty"`
	KeptWarm                 bool                   `json:"kept_warm,omitempty"`
	WorkerPID                int                    `json:"worker_pid,omitempty"`
	ChildPID                 int                    `json:"child_pid,omitempty"`
	TurnID                   string                 `json:"turn_id,omitempty"`
	State                    string                 `json:"state,omitempty"`
	ConfigFingerprint        string                 `json:"config_fingerprint,omitempty"`
	ConfigFingerprintNoModel string                 `json:"config_fingerprint_no_model,omitempty"`
	MCPFingerprint           string                 `json:"mcp_fingerprint,omitempty"`
	PermissionMode           string                 `json:"permission_mode,omitempty"`
	BgTaskCount              int                    `json:"bg_task_count,omitempty"`
	Blocked                  *Blocked               `json:"blocked,omitempty"`
	RewindPoints             []agent.ACPRewindPoint `json:"rewind_points,omitempty"`
	RewindResult             json.RawMessage        `json:"rewind_result,omitempty"`
}

type workerIPCExtensions struct {
	bgTaskCount     func() int
	pendingBlocked  func() *Blocked
	onAnswerControl func(workerIPCReq) workerIPCResp
	onWatchWorker   func(net.Conn)
	onACPStartTurn  func(workerIPCReq) workerIPCResp
	onACPSetConfig  func(workerIPCReq) workerIPCResp
	onACPCancel     func(workerIPCReq) workerIPCResp
	onACPRewind     func(workerIPCReq) workerIPCResp
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
func (b *boundedTailBuffer) Reset() { b.mu.Lock(); defer b.mu.Unlock(); b.buf = nil }

func allocateWorkerSocketPath(runDir, id string) (string, string, error) {
	if cand := filepath.Join(runDir, id+".sock"); len(cand) <= 96 {
		return cand, "", nil
	}
	td, err := os.MkdirTemp("", "akw-*")
	if err != nil {
		return "", "", err
	}
	if chErr := os.Chmod(td, 0700); chErr != nil {
		_ = os.RemoveAll(td)
		return "", "", chErr
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
	if wErr != nil {
		_ = os.Remove(tmpName)
		return wErr
	}
	if rErr := os.Rename(tmpName, path); rErr != nil {
		_ = os.Remove(tmpName)
		return rErr
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

func handleWorkerIPC(c net.Conn, stateMu *sync.Mutex, state *string, workerMeta *SessionWorkerMeta, getChildPGID func() int, activeTurn **activeTurnState, completedTurns map[string]string, subWG *sync.WaitGroup, shutdownChildGroup func(time.Duration), exitCleanly func(int), startTurnLocked func(string) error, writeStdinLine func(string), restartChild func(workerIPCReq) (int, error), ext workerIPCExtensions) {
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
		curChildPID := getChildPGID()
		cfgFP, mcpFP := workerMeta.ConfigFingerprint, workerMeta.MCPFingerprint
		bgCount := workerMeta.BgTaskCount
		if ext.bgTaskCount != nil {
			bgCount = ext.bgTaskCount()
		}
		var blocked *Blocked
		if ext.pendingBlocked != nil {
			blocked = ext.pendingBlocked()
		}
		stateMu.Unlock()
		reply(workerIPCResp{OK: ok, WorkerPID: os.Getpid(), ChildPID: curChildPID, TurnID: activeTID, State: curState, ConfigFingerprint: cfgFP, MCPFingerprint: mcpFP, BgTaskCount: bgCount, Blocked: blocked})

	case "send_turn":
		if ext.onACPStartTurn != nil {
			reply(ext.onACPStartTurn(req))
			return
		}
		stateMu.Lock()
		curChildPID := getChildPGID()
		if *state == "shutting_down" || *state == "exited" || *state == "restarting_child" {
			reply(workerIPCResp{OK: false, ErrCode: *state, WorkerPID: os.Getpid(), ChildPID: curChildPID})
			stateMu.Unlock()
			return
		}
		if *state == "running_turn" || *state == "blocked_control" || *activeTurn != nil {
			activeTID := ""
			if *activeTurn != nil {
				activeTID = (*activeTurn).turnID
			}
			reply(workerIPCResp{OK: false, ErrCode: "busy", State: *state, TurnID: activeTID, WorkerPID: os.Getpid(), ChildPID: curChildPID})
			stateMu.Unlock()
			return
		}
		if req.SessionID != "" {
			workerMeta.SessionID = req.SessionID
		}
		if req.NativeID != "" {
			workerMeta.NativeID = req.NativeID
		}
		startErr := startTurnLocked(req.MetaPath)
		curTurnID := ""
		if *activeTurn != nil {
			curTurnID = (*activeTurn).turnID
		}
		stateMu.Unlock()
		if startErr != nil {
			reply(workerIPCResp{OK: false, ErrCode: startErr.Error()})
			return
		}
		writeStdinLine(req.StdinLine)
		reply(workerIPCResp{OK: true, WorkerPID: os.Getpid(), ChildPID: curChildPID, TurnID: curTurnID, State: "running_turn"})

	case "restart_child":
		if restartChild == nil {
			reply(workerIPCResp{OK: false, ErrCode: "unsupported", WorkerPID: os.Getpid()})
			return
		}
		newPID, rErr := restartChild(req)
		if rErr != nil {
			reply(workerIPCResp{OK: false, ErrCode: rErr.Error(), WorkerPID: os.Getpid()})
			return
		}
		stateMu.Lock()
		cfgFP, mcpFP := workerMeta.ConfigFingerprint, workerMeta.MCPFingerprint
		stateMu.Unlock()
		reply(workerIPCResp{
			OK:                true,
			WorkerPID:         os.Getpid(),
			ChildPID:          newPID,
			State:             "idle_warm",
			ConfigFingerprint: cfgFP,
			MCPFingerprint:    mcpFP,
		})

	case "set_model":
		stateMu.Lock()
		curChildPID := getChildPGID()
		if !strings.EqualFold(workerMeta.Agent, "claude-code") || *state == "shutting_down" || *state == "exited" || *state == "restarting_child" || curChildPID <= 1 {
			stateMu.Unlock()
			reply(workerIPCResp{OK: false, ErrCode: "unsupported_or_inactive", WorkerPID: os.Getpid()})
			return
		}
		targetModel := strings.TrimSpace(req.Model)
		if targetModel == "" {
			targetModel = "default"
		}
		ctrlLine, _ := json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": fmt.Sprintf("req_model_%d", time.Now().UnixNano()),
			"request": map[string]any{
				"subtype": "set_model",
				"model":   targetModel,
			},
		})
		if len(req.Args) > 0 {
			workerMeta.Args = req.Args
		}
		if req.ConfigFingerprint != "" {
			workerMeta.ConfigFingerprint = req.ConfigFingerprint
		}
		if req.ConfigFingerprintNoModel != "" {
			workerMeta.ConfigFingerprintNoModel = req.ConfigFingerprintNoModel
		}
		workerMeta.UpdatedAt = time.Now().UTC()
		if workerMeta.WorkerMetaPath != "" {
			_ = WriteSessionWorkerMeta(workerMeta.WorkerMetaPath, workerMeta)
		}
		curState := *state
		cfgFP, mcpFP := workerMeta.ConfigFingerprint, workerMeta.MCPFingerprint
		stateMu.Unlock()

		writeStdinLine(string(ctrlLine))
		reply(workerIPCResp{
			OK:                true,
			WorkerPID:         os.Getpid(),
			ChildPID:          curChildPID,
			State:             curState,
			ConfigFingerprint: cfgFP,
			MCPFingerprint:    mcpFP,
		})

	case "set_config":
		if ext.onACPSetConfig == nil {
			reply(workerIPCResp{OK: false, ErrCode: "unsupported", WorkerPID: os.Getpid()})
			return
		}
		reply(ext.onACPSetConfig(req))

	case "acp_rewind_list", "acp_rewind_exec", "acp_rewind_cancel":
		if ext.onACPRewind == nil {
			reply(workerIPCResp{OK: false, ErrCode: "unsupported", WorkerPID: os.Getpid()})
			return
		}
		reply(ext.onACPRewind(req))

	case "answer_control":
		if ext.onAnswerControl == nil {
			reply(workerIPCResp{OK: false, ErrCode: "unsupported", WorkerPID: os.Getpid()})
			return
		}
		reply(ext.onAnswerControl(req))

	case "watch_worker":
		if ext.onWatchWorker != nil {
			ext.onWatchWorker(c)
		}

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
		if req.Op == "cancel" && ext.onACPCancel != nil {
			reply(ext.onACPCancel(req))
			return
		}
		stateMu.Lock()
		curChildPID := getChildPGID()
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
				turn := *activeTurn
				turn.cancelled = true
				_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: cancelledTID, Type: "turn_cancelled", Text: "Turn was cancelled by user."})
				turn.meta.Status, turn.meta.PendingControl, turn.meta.FinishedAt = "cancelled", nil, time.Now().UTC()
				_ = WriteTurnSpoolMeta(turn.metaPath, turn.meta)
				_ = turn.spool.Close()
				completedTurns[cancelledTID] = turn.meta.SpoolPath
				*activeTurn = nil
				workerMeta.ActiveTurnID, workerMeta.ActiveMetaPath, workerMeta.PendingControl = "", "", nil
				if workerMeta.WorkerMetaPath != "" {
					_ = WriteSessionWorkerMeta(workerMeta.WorkerMetaPath, workerMeta)
				}
			}
		}
		stateMu.Unlock()
		if isCancel {
			reply(workerIPCResp{OK: true, WorkerPID: os.Getpid(), ChildPID: curChildPID, TurnID: cancelledTID})
			go func() { shutdownChildGroup(500 * time.Millisecond); exitCleanly(0) }()
			return
		}
		shutdownChildGroup(500 * time.Millisecond)
		reply(workerIPCResp{OK: true, WorkerPID: os.Getpid(), ChildPID: curChildPID})
		exitCleanly(0)
	}
}
