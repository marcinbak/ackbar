package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

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

type workerIPCReq struct {
	Op       string `json:"op"`
	AfterSeq int64  `json:"after_seq,omitempty"`
}

type workerIPCResp struct {
	OK        bool   `json:"ok"`
	WorkerPID int    `json:"worker_pid,omitempty"`
	ChildPID  int    `json:"child_pid,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
}

func RunHeadlessWorker(args []string) int {
	var metaPath string
	for i := 0; i < len(args); i++ {
		if args[i] == "--spec" && i+1 < len(args) {
			metaPath = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--spec=") {
			metaPath = strings.TrimPrefix(args[i], "--spec=")
		}
	}
	meta, err := ReadTurnSpoolMeta(metaPath)
	if metaPath == "" || err != nil || meta == nil {
		return 2
	}
	spool, err := OpenTurnSpoolWriter(meta.SpoolPath, meta.TurnID)
	if err != nil {
		return 2
	}
	if spool.seq == 0 {
		_, _ = spool.Append(ChatStreamEvent{
			SessionID: meta.SessionID,
			TurnID:    meta.TurnID,
			Type:      "turn_start",
			Text:      meta.Prompt,
			Timestamp: meta.StartedAt,
		})
	}
	_ = os.Remove(meta.SocketPath)
	ln, err := net.Listen("unix", meta.SocketPath)
	if err != nil {
		_ = spool.Close()
		return 2
	}
	_ = os.Chmod(meta.SocketPath, 0600)

	var stateMu sync.Mutex
	var childCmd *exec.Cmd
	childPGID := 0
	cancelled, hasInteractiveBlock := false, false
	childDone := make(chan struct{})
	var subWG sync.WaitGroup

	signalChildStop := func(grace time.Duration) {
		stateMu.Lock()
		pgid := childPGID
		var proc *os.Process
		if childCmd != nil {
			proc = childCmd.Process
		}
		stateMu.Unlock()
		if pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGINT)
		}
		if proc != nil {
			_ = proc.Signal(syscall.SIGINT)
		}
		go func() {
			select {
			case <-childDone:
			case <-time.After(grace):
				if pgid > 0 {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				}
				if proc != nil {
					_ = proc.Kill()
				}
			}
		}()
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				var req workerIPCReq
				if (err != nil && len(line) == 0) || json.Unmarshal(line, &req) != nil {
					return
				}
				switch req.Op {
				case "ping":
					stateMu.Lock()
					cpid := childPGID
					stateMu.Unlock()
					b, _ := json.Marshal(workerIPCResp{OK: true, WorkerPID: os.Getpid(), ChildPID: cpid, TurnID: meta.TurnID})
					_, _ = c.Write(append(b, '\n'))
				case "cancel":
					stateMu.Lock()
					cancelled = true
					stateMu.Unlock()
					b, _ := json.Marshal(workerIPCResp{OK: true})
					_, _ = c.Write(append(b, '\n'))
					signalChildStop(2 * time.Second)
				case "subscribe":
					subWG.Add(1)
					defer subWG.Done()
					replay, ch, unsub, err := spool.SubscribeFrom(req.AfterSeq)
					if err != nil {
						return
					}
					defer unsub()
					writeEvt := func(evt ChatStreamEvent) bool {
						b, mErr := json.Marshal(evt)
						if mErr != nil {
							return true
						}
						_, wErr := c.Write(append(b, '\n'))
						return wErr == nil
					}
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
				}
			}(conn)
		}
	}()

	finishWorker := func() int {
		_ = spool.Close()
		waitCh := make(chan struct{})
		go func() { subWG.Wait(); close(waitCh) }()
		select {
		case <-waitCh:
		case <-time.After(time.Second):
		}
		_ = ln.Close()
		cleanupTurnSocketArtifacts(meta)
		return 0
	}

	cmd := exec.Command(meta.Bin, meta.Args...)
	// Put child CLI in its own process group so cancel kills sub-processes cleanly on both Linux and Darwin.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()
	cmd.WaitDelay = 4 * time.Second
	cmd.Dir = meta.Cwd
	if devNull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	} else {
		cmd.Stdin = strings.NewReader("")
	}
	stderrTailBuf := &boundedTailBuffer{max: 16384}
	cmd.Stderr = stderrTailBuf
	stdoutPipe, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		close(childDone)
		_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "error", Text: pipeErr.Error(), IsError: true})
		_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "turn_complete"})
		meta.Status, meta.ExitCode, meta.StderrTail, meta.FinishedAt = "failed", 1, pipeErr.Error(), time.Now().UTC()
		_ = WriteTurnSpoolMeta(metaPath, meta)
		return finishWorker()
	}

	stateMu.Lock()
	startErr := cmd.Start()
	if startErr == nil {
		childCmd, childPGID = cmd, cmd.Process.Pid
		meta.WorkerPID, meta.ChildPID, meta.Status = os.Getpid(), childPGID, "running"
		_ = WriteTurnSpoolMeta(metaPath, meta)
	}
	alreadyCancelled := cancelled
	stateMu.Unlock()
	if startErr != nil {
		close(childDone)
		_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "error", Text: startErr.Error(), IsError: true})
		_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "turn_complete"})
		meta.Status, meta.ExitCode, meta.StderrTail, meta.FinishedAt = "failed", 127, startErr.Error(), time.Now().UTC()
		_ = WriteTurnSpoolMeta(metaPath, meta)
		return finishWorker()
	}
	if alreadyCancelled {
		signalChildStop(2 * time.Second)
	}

	lastTurnText, resultText, streamHadError := parseHeadlessStream(meta.SessionID, stdoutPipe, streamParseHooks{
		emitTurnComplete: false,
		emit:             func(evt ChatStreamEvent) { _, _ = spool.Append(evt) },
		onAntigravityInit: func(convID string) {
			stateMu.Lock()
			meta.NativeID = convID
			_ = WriteTurnSpoolMeta(metaPath, meta)
			stateMu.Unlock()
			_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "session_init", NativeID: convID})
		},
		onInteractiveBlock: func(toolName string, toolInput any, blocked *Blocked) {
			stateMu.Lock()
			hasInteractiveBlock = true
			stateMu.Unlock()
			_, _ = spool.Append(ChatStreamEvent{
				SessionID: meta.SessionID,
				TurnID:    meta.TurnID,
				Type:      "interactive_block",
				ToolName:  toolName,
				ToolInput: toolInput,
				Blocked:   blocked,
			})
			signalChildStop(500 * time.Millisecond)
		},
	})

	waitErr := cmd.Wait()
	close(childDone)
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	stderrTail := strings.TrimSpace(stderrTailBuf.String())

	stateMu.Lock()
	wasCancelled, hadBlock := cancelled, hasInteractiveBlock
	stateMu.Unlock()

	status := "completed"
	if wasCancelled {
		_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "turn_cancelled", Text: "Turn was cancelled by user."})
		status = "cancelled"
	} else if !hadBlock {
		if waitErr != nil || streamHadError {
			combined := strings.TrimSpace(lastTurnText + "\n" + stderrTail)
			if limited, resetAt, reason := getDefaultAgentAdapter(meta.Agent).DetectRateLimit(combined, time.Now()); limited {
				rateBlock := &Blocked{
					Kind:     BlockRateLimit,
					Reason:   reason,
					Question: reason,
					ResetAt:  &resetAt,
					Options:  []string{fmt.Sprintf("Resume at %s", resetAt.Local().Format("15:04")), "Resume in 1h"},
					Since:    time.Now(),
				}
				_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "interactive_block", Blocked: rateBlock})
				hadBlock = true
			}
		}
		if !hadBlock {
			if waitErr != nil {
				status = "failed"
				if stderrTail != "" {
					_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "error", Text: stderrTail, IsError: true})
				}
			}
			_, _ = spool.Append(ChatStreamEvent{SessionID: meta.SessionID, TurnID: meta.TurnID, Type: "turn_complete", Text: resultText})
		}
	}

	stateMu.Lock()
	meta.Status, meta.ExitCode, meta.StderrTail, meta.FinishedAt = status, exitCode, stderrTail, time.Now().UTC()
	_ = WriteTurnSpoolMeta(metaPath, meta)
	stateMu.Unlock()
	return finishWorker()
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

func pingWorkerSocket(meta *TurnSpoolMeta) bool {
	if meta == nil || meta.SocketPath == "" {
		return false
	}
	resp, err := callWorkerSocket(meta.SocketPath, workerIPCReq{Op: "ping"}, 250*time.Millisecond)
	return err == nil && resp != nil && resp.OK
}

func sendWorkerCancel(meta *TurnSpoolMeta) error {
	if meta == nil {
		return errors.New("nil meta")
	}
	for deadline := time.Now().Add(350 * time.Millisecond); ; time.Sleep(15 * time.Millisecond) {
		sock := meta.SocketPath
		if meta.MetaPath != "" {
			if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil && dm.SocketPath != "" {
				sock = dm.SocketPath
			}
		}
		if resp, err := callWorkerSocket(sock, workerIPCReq{Op: "cancel"}, 250*time.Millisecond); err == nil && resp != nil && resp.OK {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("worker socket unreachable")
		}
	}
}

func killWorkerAndChildGroup(meta *TurnSpoolMeta) {
	if meta == nil {
		return
	}
	if meta.MetaPath != "" {
		if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil {
			meta = dm
		}
	}
	if meta.ChildPID > 0 {
		_ = syscall.Kill(-meta.ChildPID, syscall.SIGKILL)
		_ = syscall.Kill(meta.ChildPID, syscall.SIGKILL)
	}
	if meta.WorkerPID > 0 {
		_ = syscall.Kill(meta.WorkerPID, syscall.SIGKILL)
	}
}
