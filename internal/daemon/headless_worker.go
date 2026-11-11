package daemon

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func RunHeadlessWorker(args []string) int {
	var workerMetaPath, specPath, idleTTLStr string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--worker-spec" && i+1 < len(args) {
			workerMetaPath, i = args[i+1], i+1
		} else if strings.HasPrefix(a, "--worker-spec=") {
			workerMetaPath = strings.TrimPrefix(a, "--worker-spec=")
		} else if a == "--spec" && i+1 < len(args) {
			specPath, i = args[i+1], i+1
		} else if strings.HasPrefix(a, "--spec=") {
			specPath = strings.TrimPrefix(a, "--spec=")
		} else if a == "--idle-ttl" && i+1 < len(args) {
			idleTTLStr, i = args[i+1], i+1
		} else if strings.HasPrefix(a, "--idle-ttl=") {
			idleTTLStr = strings.TrimPrefix(a, "--idle-ttl=")
		}
	}
	if workerMetaPath == "" && specPath == "" {
		return 2
	}
	return runWorkerLoop(workerMetaPath, specPath, idleTTLStr)
}

type activeTurnState struct {
	turnID    string
	metaPath  string
	meta      *TurnSpoolMeta
	spool     *TurnSpoolWriter
	cancelled bool
	hadBlock  bool
}

func runWorkerLoop(workerMetaPath, specPath, idleTTLStr string) int {
	var workerMeta *SessionWorkerMeta
	var initialTMeta *TurnSpoolMeta
	var err error

	if workerMetaPath != "" {
		if workerMeta, err = ReadSessionWorkerMeta(workerMetaPath); err != nil || workerMeta == nil {
			return 2
		}
	} else {
		if initialTMeta, err = ReadTurnSpoolMeta(specPath); err != nil || initialTMeta == nil {
			return 2
		}
		workerMeta = &SessionWorkerMeta{
			WorkerID: initialTMeta.TurnID, SessionID: initialTMeta.SessionID, OrigSessionID: initialTMeta.OrigSessionID,
			NativeID: initialTMeta.NativeID, Agent: initialTMeta.Agent, Cwd: initialTMeta.Cwd, Bin: initialTMeta.Bin,
			Args: initialTMeta.Args, SocketPath: initialTMeta.SocketPath, SocketTempDir: initialTMeta.SocketTempDir,
			ActiveTurnID: initialTMeta.TurnID, ActiveMetaPath: specPath, StartedAt: initialTMeta.StartedAt,
		}
	}

	idleTTL := time.Duration(0)
	if workerMetaPath != "" {
		idleTTL = ResolveHeadlessIdleTTL(0)
		if idleTTLStr != "" {
			if d, pErr := time.ParseDuration(idleTTLStr); pErr == nil && d > 0 {
				idleTTL = d
			}
		} else if workerMeta.IdleTTLNs > 0 {
			idleTTL = workerMeta.IdleTTLNs
		}
	}

	saveWorkerMeta := func() {
		if workerMetaPath != "" {
			_ = WriteSessionWorkerMeta(workerMetaPath, workerMeta)
		}
	}
	cleanupArtifacts := func() {
		if workerMetaPath != "" {
			cleanupWorkerArtifacts(workerMeta)
		} else if initialTMeta != nil {
			cleanupTurnSocketArtifacts(initialTMeta)
		}
	}

	_ = os.Remove(workerMeta.SocketPath)
	ln, err := net.Listen("unix", workerMeta.SocketPath)
	if err != nil {
		return 2
	}
	_ = os.Chmod(workerMeta.SocketPath, 0600)

	var stateMu sync.Mutex
	state := "starting"
	var activeTurn *activeTurnState
	var pendingInitConvID string
	completedTurns := make(map[string]string)
	childDone := make(chan struct{})
	var subWG sync.WaitGroup
	var idleTimer *time.Timer

	cmd := exec.Command(workerMeta.Bin, workerMeta.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()
	cmd.Dir = workerMeta.Cwd
	cmd.WaitDelay = 4 * time.Second
	stderrTailBuf := &boundedTailBuffer{max: 16384}
	cmd.Stderr = stderrTailBuf

	var stdinPipe io.WriteCloser
	if workerMetaPath != "" {
		stdinPipe, _ = cmd.StdinPipe()
	} else if devNull, dErr := os.Open(os.DevNull); dErr == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	} else {
		cmd.Stdin = strings.NewReader("")
	}

	stdoutPipe, outPipeErr := cmd.StdoutPipe()
	if outPipeErr != nil {
		_ = ln.Close()
		cleanupArtifacts()
		return 2
	}
	if err := cmd.Start(); err != nil {
		_ = ln.Close()
		cleanupArtifacts()
		return 2
	}
	childPGID := cmd.Process.Pid
	workerMeta.WorkerPID, workerMeta.ChildPID, workerMeta.State = os.Getpid(), childPGID, "idle_warm"
	saveWorkerMeta()
	state = "idle_warm"

	shutdownChildGroup := func(grace time.Duration) {
		if stdinPipe != nil {
			_ = stdinPipe.Close()
		}
		if childPGID > 1 {
			_ = syscall.Kill(-childPGID, syscall.SIGTERM)
		}
		select {
		case <-childDone:
		case <-time.After(grace):
			if childPGID > 1 {
				_ = syscall.Kill(-childPGID, syscall.SIGKILL)
			}
		}
	}

	exitCleanly := func(code int) {
		waitCh := make(chan struct{})
		go func() { subWG.Wait(); close(waitCh) }()
		select {
		case <-waitCh:
		case <-time.After(time.Second):
		}
		_ = ln.Close()
		cleanupArtifacts()
		os.Exit(code)
	}

	if suitePIDStr := os.Getenv("ACKBAR_TEST_SUITE_PID"); suitePIDStr != "" {
		if suitePID, sErr := strconv.Atoi(suitePIDStr); sErr == nil && suitePID > 0 {
			go func() {
				ticker := time.NewTicker(500 * time.Millisecond)
				defer ticker.Stop()
				for range ticker.C {
					if !isProcessAlive(suitePID) {
						shutdownChildGroup(300 * time.Millisecond)
						exitCleanly(0)
					}
				}
			}()
		}
	}

	detectRate := func(text string) *Blocked {
		if limited, resetAt, reason := getDefaultAgentAdapter(workerMeta.Agent).DetectRateLimit(text, time.Now()); limited {
			return &Blocked{
				Kind: BlockRateLimit, Reason: reason, Question: reason, ResetAt: &resetAt,
				Options: []string{fmt.Sprintf("Resume at %s", resetAt.Local().Format("15:04")), "Resume in 1h"}, Since: time.Now(),
			}
		}
		return nil
	}

	finalizeTurnMeta := func(turn *activeTurnState, status string, exitCode int, resText string) {
		turn.meta.Status, turn.meta.ExitCode = status, exitCode
		turn.meta.StderrTail = strings.TrimSpace(stderrTailBuf.String())
		turn.meta.FinishedAt = time.Now().UTC()
		_ = WriteTurnSpoolMeta(turn.metaPath, turn.meta)
		_ = turn.spool.Close()
		completedTurns[turn.meta.TurnID] = turn.meta.SpoolPath
	}

	var onIdleTTLExpired func()
	onIdleTTLExpired = func() {
		stateMu.Lock()
		if state != "idle_warm" {
			stateMu.Unlock()
			return
		}
		state, workerMeta.State = "shutting_down", "shutting_down"
		saveWorkerMeta()
		stateMu.Unlock()
		shutdownChildGroup(300 * time.Millisecond)
		exitCleanly(0)
	}

	onTurnResult := func(lastTurnText, resultText string, streamHadError bool) {
		stateMu.Lock()
		turn := activeTurn
		if turn == nil {
			stateMu.Unlock()
			return
		}
		wasCancelled, hadBlock := turn.cancelled, turn.hadBlock
		stateMu.Unlock()

		status := "completed"
		if wasCancelled {
			_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "turn_cancelled", Text: "Turn was cancelled by user."})
			status = "cancelled"
		} else if !hadBlock {
			if streamHadError {
				if rateBlock := detectRate(strings.TrimSpace(lastTurnText + "\n" + stderrTailBuf.String())); rateBlock != nil {
					_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "interactive_block", Blocked: rateBlock})
					hadBlock, turn.hadBlock = true, true
				}
			}
			if !hadBlock {
				if streamHadError {
					status = "failed"
				}
				_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "turn_complete", Text: resultText})
			}
		}

		stateMu.Lock()
		finalizeTurnMeta(turn, status, 0, resultText)
		if hadBlock {
			state, workerMeta.State = "shutting_down", "shutting_down"
			saveWorkerMeta()
			stateMu.Unlock()
			go func() { shutdownChildGroup(500 * time.Millisecond); exitCleanly(0) }()
			return
		}
		if workerMetaPath == "" || idleTTL <= 0 {
			stateMu.Unlock()
			exitCleanly(0)
			return
		}

		stderrTailBuf.Reset()
		state, workerMeta.State = "idle_warm", "idle_warm"
		workerMeta.ActiveTurnID, workerMeta.ActiveMetaPath = "", ""
		workerMeta.LastTurnEndedAt, workerMeta.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		saveWorkerMeta()
		activeTurn = nil

		if idleTimer == nil {
			idleTimer = time.AfterFunc(idleTTL, onIdleTTLExpired)
		} else {
			idleTimer.Reset(idleTTL)
		}
		stateMu.Unlock()
	}

	startTurnLocked := func(tMetaPath, stdinLine string) error {
		tMeta, err := ReadTurnSpoolMeta(tMetaPath)
		if err != nil || tMeta == nil {
			return fmt.Errorf("failed to read turn meta: %w", err)
		}
		spool, err := OpenTurnSpoolWriter(tMeta.SpoolPath, tMeta.TurnID)
		if err != nil {
			return fmt.Errorf("failed to open spool writer: %w", err)
		}
		if spool.seq == 0 {
			_, _ = spool.Append(ChatStreamEvent{SessionID: tMeta.SessionID, TurnID: tMeta.TurnID, Type: "turn_start", Text: tMeta.Prompt, Timestamp: tMeta.StartedAt})
		}
		if pendingInitConvID != "" {
			workerMeta.NativeID, tMeta.NativeID = pendingInitConvID, pendingInitConvID
			_, _ = spool.Append(ChatStreamEvent{SessionID: tMeta.SessionID, TurnID: tMeta.TurnID, Type: "session_init", NativeID: pendingInitConvID})
			pendingInitConvID = ""
		}
		if idleTimer != nil {
			idleTimer.Stop()
		}
		state, workerMeta.State = "running_turn", "running_turn"
		workerMeta.ActiveTurnID, workerMeta.ActiveMetaPath, workerMeta.UpdatedAt = tMeta.TurnID, tMetaPath, time.Now().UTC()
		saveWorkerMeta()

		tMeta.WorkerPID, tMeta.ChildPID, tMeta.Status = os.Getpid(), childPGID, "running"
		_ = WriteTurnSpoolMeta(tMetaPath, tMeta)

		activeTurn = &activeTurnState{turnID: tMeta.TurnID, metaPath: tMetaPath, meta: tMeta, spool: spool}
		if stdinLine != "" && stdinPipe != nil {
			_, _ = stdinPipe.Write([]byte(strings.TrimSpace(stdinLine) + "\n"))
		}
		return nil
	}

	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		parseHeadlessStream(workerMeta.SessionID, stdoutPipe, streamParseHooks{
			emitTurnComplete: false,
			emit: func(evt ChatStreamEvent) {
				stateMu.Lock()
				if activeTurn != nil && activeTurn.spool != nil {
					_, _ = activeTurn.spool.Append(evt)
				}
				stateMu.Unlock()
			},
			onAntigravityInit: func(convID string) {
				stateMu.Lock()
				workerMeta.NativeID = convID
				saveWorkerMeta()
				if activeTurn != nil {
					activeTurn.meta.NativeID = convID
					_ = WriteTurnSpoolMeta(activeTurn.metaPath, activeTurn.meta)
					_, _ = activeTurn.spool.Append(ChatStreamEvent{SessionID: workerMeta.SessionID, TurnID: activeTurn.turnID, Type: "session_init", NativeID: convID})
				} else {
					pendingInitConvID = convID
				}
				stateMu.Unlock()
			},
			onInteractiveBlock: func(toolName string, toolInput any, blocked *Blocked) {
				stateMu.Lock()
				turn := activeTurn
				if turn != nil {
					turn.hadBlock = true
					_, _ = turn.spool.Append(ChatStreamEvent{SessionID: workerMeta.SessionID, TurnID: turn.turnID, Type: "interactive_block", ToolName: toolName, ToolInput: toolInput, Blocked: blocked})
				}
				state, workerMeta.State = "shutting_down", "shutting_down"
				saveWorkerMeta()
				stateMu.Unlock()

				go func() {
					shutdownChildGroup(500 * time.Millisecond)
					stateMu.Lock()
					if turn != nil {
						finalizeTurnMeta(turn, "completed", 0, "")
						activeTurn = nil
					}
					stateMu.Unlock()
					exitCleanly(0)
				}()
			},
			onTurnResult: onTurnResult,
		})
	}()

	go func() {
		<-stdoutDone
		waitErr := cmd.Wait()
		close(childDone)

		stateMu.Lock()
		curState, turn := state, activeTurn
		if curState == "shutting_down" || curState == "exited" {
			stateMu.Unlock()
			return
		}
		if curState == "idle_warm" || turn == nil {
			state, workerMeta.State = "exited", "exited"
			saveWorkerMeta()
			stateMu.Unlock()
			exitCleanly(0)
			return
		}
		status, code := "completed", 0
		if turn.cancelled {
			status = "cancelled"
			_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "turn_cancelled", Text: "Turn was cancelled by user."})
		} else if !turn.hadBlock {
			if waitErr != nil {
				tail := strings.TrimSpace(stderrTailBuf.String())
				if rateBlock := detectRate(tail); rateBlock != nil {
					_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "interactive_block", Blocked: rateBlock})
					turn.hadBlock = true
				} else {
					status = "failed"
					if tail != "" {
						_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "error", Text: tail, IsError: true})
					}
				}
			}
			if !turn.hadBlock {
				_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "turn_complete"})
			}
		}
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				code = 1
			}
		}
		finalizeTurnMeta(turn, status, code, "")
		state, workerMeta.State = "exited", "exited"
		saveWorkerMeta()
		stateMu.Unlock()
		exitCleanly(0)
	}()

	if specPath != "" {
		if tMeta, rErr := ReadTurnSpoolMeta(specPath); rErr == nil && tMeta != nil {
			var sLine string
			if workerMetaPath != "" {
				isNewConv := !slices.Contains(workerMeta.Args, "--conversation")
				sLine, _ = formatPersistentStdinTurn(workerMeta.Agent, workerMeta.SessionID, workerMeta.IsSupervisor, isNewConv, tMeta.Prompt)
			}
			stateMu.Lock()
			_ = startTurnLocked(specPath, sLine)
			stateMu.Unlock()
		}
	}

	for {
		conn, aErr := ln.Accept()
		if aErr != nil {
			return 0
		}
		go handleWorkerIPC(conn, &stateMu, &state, workerMeta, childPGID, &activeTurn, completedTurns, &subWG, shutdownChildGroup, exitCleanly, startTurnLocked)
	}
}
