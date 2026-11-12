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

type workerChildGen struct {
	pgid        int
	stdinPipe   io.WriteCloser
	stdinMu     sync.Mutex
	stdinClosed bool
	done        chan struct{}
	stderrTail  *boundedTailBuffer
	replaced    bool
}

func (g *workerChildGen) writeLine(sLine string) {
	if g == nil || sLine == "" || g.stdinPipe == nil {
		return
	}
	g.stdinMu.Lock()
	if !g.stdinClosed {
		_, _ = g.stdinPipe.Write([]byte(strings.TrimSpace(sLine) + "\n"))
	}
	g.stdinMu.Unlock()
}

func (g *workerChildGen) shutdown(grace time.Duration) {
	if g == nil {
		return
	}
	g.stdinMu.Lock()
	if !g.stdinClosed && g.stdinPipe != nil {
		g.stdinClosed = true
		_ = g.stdinPipe.Close()
	}
	g.stdinMu.Unlock()
	if g.pgid > 1 {
		_ = syscall.Kill(-g.pgid, syscall.SIGTERM)
	}
	select {
	case <-g.done:
	case <-time.After(grace):
		if g.pgid > 1 {
			_ = syscall.Kill(-g.pgid, syscall.SIGKILL)
		}
		select {
		case <-g.done:
		case <-time.After(250 * time.Millisecond):
		}
	}
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
	var restartMu sync.Mutex
	state := "starting"
	var activeTurn *activeTurnState
	var curChild *workerChildGen
	var pendingInitConvID string
	completedTurns := make(map[string]string)
	var subWG sync.WaitGroup
	var idleTimer *time.Timer

	getChildPGIDLocked := func() int {
		if curChild != nil {
			return curChild.pgid
		}
		return 0
	}

	writeStdinLine := func(sLine string) {
		stateMu.Lock()
		g := curChild
		stateMu.Unlock()
		g.writeLine(sLine)
	}

	shutdownChildGroup := func(grace time.Duration) {
		stateMu.Lock()
		g := curChild
		stateMu.Unlock()
		g.shutdown(grace)
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

	finalizeTurnMeta := func(turn *activeTurnState, tail *boundedTailBuffer, status string, exitCode int, resText string) {
		_ = resText
		turn.meta.Status, turn.meta.ExitCode = status, exitCode
		if tail != nil {
			turn.meta.StderrTail = strings.TrimSpace(tail.String())
		}
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

	spawnChildGen := func(bin string, args []string, env []string) (*workerChildGen, error) {
		cmd := exec.Command(bin, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if len(env) > 0 {
			cmd.Env = env
		} else {
			cmd.Env = os.Environ()
		}
		cmd.Dir = workerMeta.Cwd
		cmd.WaitDelay = 4 * time.Second
		stderrTailBuf := &boundedTailBuffer{max: 16384}
		cmd.Stderr = stderrTailBuf

		var stdinPipe io.WriteCloser
		var devNull *os.File
		if workerMetaPath != "" {
			stdinPipe, _ = cmd.StdinPipe()
		} else if dn, dErr := os.Open(os.DevNull); dErr == nil {
			devNull = dn
			cmd.Stdin = dn
		} else {
			cmd.Stdin = strings.NewReader("")
		}

		stdoutPipe, outPipeErr := cmd.StdoutPipe()
		if outPipeErr != nil {
			if devNull != nil {
				_ = devNull.Close()
			}
			return nil, outPipeErr
		}
		if err := cmd.Start(); err != nil {
			if devNull != nil {
				_ = devNull.Close()
			}
			return nil, err
		}
		if devNull != nil {
			_ = devNull.Close()
		}

		gen := &workerChildGen{
			pgid:       cmd.Process.Pid,
			stdinPipe:  stdinPipe,
			done:       make(chan struct{}),
			stderrTail: stderrTailBuf,
		}

		onGenTurnResult := func(lastTurnText, resultText string, streamHadError bool) {
			stateMu.Lock()
			if curChild != gen || gen.replaced {
				stateMu.Unlock()
				return
			}
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
					if failBlock := detectTurnFailureBlock(workerMeta.Agent, strings.TrimSpace(lastTurnText+"\n"+stderrTailBuf.String())); failBlock != nil {
						_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "interactive_block", Blocked: failBlock})
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
			if curChild != gen || gen.replaced {
				stateMu.Unlock()
				return
			}
			finalizeTurnMeta(turn, stderrTailBuf, status, 0, resultText)
			if hadBlock {
				state, workerMeta.State = "shutting_down", "shutting_down"
				saveWorkerMeta()
				stateMu.Unlock()
				go func() { gen.shutdown(500 * time.Millisecond); exitCleanly(0) }()
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

		stdoutDone := make(chan struct{})
		go func() {
			defer close(stdoutDone)
			parseHeadlessStream(workerMeta.SessionID, stdoutPipe, streamParseHooks{
				emitTurnComplete: false,
				emit: func(evt ChatStreamEvent) {
					stateMu.Lock()
					if curChild == gen && !gen.replaced && activeTurn != nil && activeTurn.spool != nil {
						_, _ = activeTurn.spool.Append(evt)
					}
					stateMu.Unlock()
				},
				onAntigravityInit: func(convID string) {
					stateMu.Lock()
					if curChild == gen && !gen.replaced {
						workerMeta.NativeID = convID
						saveWorkerMeta()
						if activeTurn != nil {
							activeTurn.meta.NativeID = convID
							_ = WriteTurnSpoolMeta(activeTurn.metaPath, activeTurn.meta)
							_, _ = activeTurn.spool.Append(ChatStreamEvent{SessionID: workerMeta.SessionID, TurnID: activeTurn.turnID, Type: "session_init", NativeID: convID})
						} else {
							pendingInitConvID = convID
						}
					}
					stateMu.Unlock()
				},
				onInteractiveBlock: func(toolName string, toolInput any, blocked *Blocked) {
					stateMu.Lock()
					if curChild != gen || gen.replaced {
						stateMu.Unlock()
						return
					}
					turn := activeTurn
					if turn != nil {
						turn.hadBlock = true
						_, _ = turn.spool.Append(ChatStreamEvent{SessionID: workerMeta.SessionID, TurnID: turn.turnID, Type: "interactive_block", ToolName: toolName, ToolInput: toolInput, Blocked: blocked})
					}
					state, workerMeta.State = "shutting_down", "shutting_down"
					saveWorkerMeta()
					stateMu.Unlock()

					go func() {
						gen.shutdown(500 * time.Millisecond)
						stateMu.Lock()
						if turn != nil {
							finalizeTurnMeta(turn, stderrTailBuf, "completed", 0, "")
							activeTurn = nil
						}
						stateMu.Unlock()
						exitCleanly(0)
					}()
				},
				onTurnResult: onGenTurnResult,
			})
		}()

		go func() {
			<-stdoutDone
			waitErr := cmd.Wait()
			close(gen.done)

			stateMu.Lock()
			if gen.replaced || curChild != gen {
				stateMu.Unlock()
				return
			}
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
					if failBlock := detectTurnFailureBlock(workerMeta.Agent, tail); failBlock != nil {
						_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.meta.TurnID, Type: "interactive_block", Blocked: failBlock})
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
			finalizeTurnMeta(turn, stderrTailBuf, status, code, "")
			state, workerMeta.State = "exited", "exited"
			saveWorkerMeta()
			stateMu.Unlock()
			exitCleanly(0)
		}()

		return gen, nil
	}

	firstGen, err := spawnChildGen(workerMeta.Bin, workerMeta.Args, nil)
	if err != nil {
		_ = ln.Close()
		cleanupArtifacts()
		return 2
	}
	curChild = firstGen
	workerMeta.WorkerPID, workerMeta.ChildPID = os.Getpid(), firstGen.pgid

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

	restartChild := func(req workerIPCReq) (int, error) {
		restartMu.Lock()
		defer restartMu.Unlock()

		stateMu.Lock()
		if state == "shutting_down" || state == "exited" {
			cur := state
			stateMu.Unlock()
			return 0, fmt.Errorf("worker_%s", cur)
		}
		if idleTimer != nil {
			idleTimer.Stop()
		}
		oldGen := curChild
		oldTurn := activeTurn
		if oldGen != nil {
			oldGen.replaced = true
		}
		if oldTurn != nil {
			oldTurn.cancelled = true
		}
		activeTurn = nil
		state, workerMeta.State = "restarting_child", "restarting_child"
		if req.SessionID != "" {
			workerMeta.SessionID = req.SessionID
		}
		if req.NativeID != "" {
			workerMeta.NativeID = req.NativeID
		}
		nextBin := workerMeta.Bin
		if req.Bin != "" {
			nextBin = req.Bin
		}
		nextArgs := workerMeta.Args
		if len(req.Args) > 0 {
			nextArgs = req.Args
		}
		stateMu.Unlock()

		oldGen.shutdown(500 * time.Millisecond)
		if oldTurn != nil {
			_, _ = oldTurn.spool.Append(ChatStreamEvent{
				SessionID: oldTurn.meta.SessionID,
				TurnID:    oldTurn.turnID,
				Type:      "turn_cancelled",
				Text:      "Turn was cancelled by session restart.",
			})
			stateMu.Lock()
			var tail *boundedTailBuffer
			if oldGen != nil {
				tail = oldGen.stderrTail
			}
			finalizeTurnMeta(oldTurn, tail, "cancelled", 0, "")
			stateMu.Unlock()
		}

		newGen, sErr := spawnChildGen(nextBin, nextArgs, req.Env)
		if sErr != nil {
			stateMu.Lock()
			state, workerMeta.State = "exited", "exited"
			saveWorkerMeta()
			stateMu.Unlock()
			go exitCleanly(1)
			return 0, sErr
		}

		stateMu.Lock()
		curChild = newGen
		pendingInitConvID = ""
		workerMeta.Bin, workerMeta.Args = nextBin, nextArgs
		if req.ConfigFingerprint != "" {
			workerMeta.ConfigFingerprint = req.ConfigFingerprint
		}
		if req.MCPFingerprint != "" {
			workerMeta.MCPFingerprint = req.MCPFingerprint
		}
		workerMeta.ChildPID = newGen.pgid
		workerMeta.ActiveTurnID, workerMeta.ActiveMetaPath = "", ""
		workerMeta.UpdatedAt = time.Now().UTC()
		state, workerMeta.State = "idle_warm", "idle_warm"
		saveWorkerMeta()
		if idleTTL > 0 {
			if idleTimer == nil {
				idleTimer = time.AfterFunc(idleTTL, onIdleTTLExpired)
			} else {
				idleTimer.Reset(idleTTL)
			}
		}
		stateMu.Unlock()
		return newGen.pgid, nil
	}

	startTurnLocked := func(tMetaPath string) error {
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

		tMeta.WorkerPID, tMeta.ChildPID, tMeta.Status = os.Getpid(), getChildPGIDLocked(), "running"
		_ = WriteTurnSpoolMeta(tMetaPath, tMeta)

		activeTurn = &activeTurnState{turnID: tMeta.TurnID, metaPath: tMetaPath, meta: tMeta, spool: spool}
		return nil
	}

	var initialStdinLine string
	if specPath != "" {
		if tMeta, rErr := ReadTurnSpoolMeta(specPath); rErr == nil && tMeta != nil {
			if workerMetaPath != "" {
				isNewConv := !slices.Contains(workerMeta.Args, "--conversation")
				initialStdinLine, _ = formatPersistentStdinTurn(workerMeta.Agent, workerMeta.SessionID, workerMeta.IsSupervisor, isNewConv, tMeta.Prompt)
			}
			stateMu.Lock()
			_ = startTurnLocked(specPath)
			stateMu.Unlock()
		}
	} else {
		stateMu.Lock()
		state, workerMeta.State = "idle_warm", "idle_warm"
		saveWorkerMeta()
		if idleTTL > 0 {
			if idleTimer == nil {
				idleTimer = time.AfterFunc(idleTTL, onIdleTTLExpired)
			} else {
				idleTimer.Reset(idleTTL)
			}
		}
		stateMu.Unlock()
	}

	if initialStdinLine != "" {
		writeStdinLine(initialStdinLine)
	}

	for {
		conn, aErr := ln.Accept()
		if aErr != nil {
			return 0
		}
		go handleWorkerIPC(conn, &stateMu, &state, workerMeta, getChildPGIDLocked, &activeTurn, completedTurns, &subWG, shutdownChildGroup, exitCleanly, startTurnLocked, writeStdinLine, restartChild)
	}
}
