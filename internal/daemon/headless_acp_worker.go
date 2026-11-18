package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type workerACPDeps struct {
	saveMeta           func()
	finalizeTurnMeta   func(turn *activeTurnState, tail *boundedTailBuffer, status string, exitCode int, resText string)
	exitCleanly        func(code int)
	resetIdleTimer     func(wait time.Duration)
	stopIdleTimer      func()
	onSessionInit      func(gen *workerChildGen, nativeID, model string)
	onModelDetected    func(gen *workerChildGen, model string)
	onInteractiveBlock func(gen *workerChildGen, toolName string, toolInput any, blocked *Blocked)
	onRateLimitBlock   func(gen *workerChildGen, turn *activeTurnState, blocked *Blocked)
	onTurnFailure      func(gen *workerChildGen, turn *activeTurnState, text string)
	completedTurns     map[string]string
	clearRateLimit     func()
}

type acpChildSession struct {
	rt         *workerRuntime
	gen        *workerChildGen
	driver     agent.ACPDriver
	reg        *agent.Registry
	homeDir    string
	env        []string
	cmd        *exec.Cmd
	conn       *agentutil.ACPConn
	stderrTail *boundedTailBuffer
	deps       workerACPDeps

	streamMu    sync.Mutex
	streamState agentutil.ACPStreamState

	mu              sync.Mutex
	sessionID       string
	isExisting      bool
	sysPrompt       string
	sentFirstTurn   bool
	curModel        string
	curEffort       string
	curPermMode     string
	bootDone        chan struct{}
	bootErr         error
	bootBlock       *Blocked
	inPromptTurn    bool
	promptDone      chan struct{}
	cancelTimer     *time.Timer
	cancelSettledCh chan struct{}
	inFlightRewind  bool
}

func spawnACPChildGen(rt *workerRuntime, reg *agent.Registry, driver agent.ACPDriver, bin string, args, env []string, isExisting bool, sysPrompt string, deps workerACPDeps) (*workerChildGen, error) {
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = env
	if len(cmd.Env) == 0 {
		cmd.Env = os.Environ()
	}
	cmd.Dir = rt.workerMeta.Cwd
	cmd.WaitDelay = 4 * time.Second
	stderrTailBuf := &boundedTailBuffer{max: 16384}
	cmd.Stderr = stderrTailBuf

	stdinPipe, inErr := cmd.StdinPipe()
	if inErr != nil {
		return nil, inErr
	}
	stdoutPipe, outErr := cmd.StdoutPipe()
	if outErr != nil {
		_ = stdinPipe.Close()
		return nil, outErr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	homeDir, _ := os.UserHomeDir()
	gen := &workerChildGen{pgid: cmd.Process.Pid, stdinPipe: stdinPipe, done: make(chan struct{}), stderrTail: stderrTailBuf}
	s := &acpChildSession{
		rt: rt, gen: gen, driver: driver, reg: reg, homeDir: homeDir, env: cmd.Env, cmd: cmd,
		stderrTail: stderrTailBuf, deps: deps, sessionID: rt.workerMeta.NativeID,
		isExisting: isExisting, sysPrompt: sysPrompt,
		curModel: normalizeACPConfigValue(rt.workerMeta.Model), curEffort: normalizeACPConfigValue(rt.workerMeta.Effort),
		curPermMode: rt.workerMeta.PermissionMode, bootDone: make(chan struct{}),
	}
	gen.acp = s

	conn := agentutil.NewACPConn(stdoutPipe, stdinPipe)
	s.conn = conn
	conn.OnNotification = s.handleNotification
	conn.OnServerRequest = s.handleServerRequest
	conn.Start()

	go s.bootstrap()
	go s.waitForExit()
	return gen, nil
}

func (s *acpChildSession) isValidSessionID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || !agentutil.IsSafeCLIArg(id) {
		return false
	}
	if s.reg != nil {
		if sc, ok := s.reg.AsScanner(s.rt.workerMeta.Agent); ok {
			return sc.IsValidNativeID(id)
		}
	}
	return true
}

func (s *acpChildSession) waitStderrDrained(timeout time.Duration) {
	select {
	case <-s.gen.done:
		return
	case <-s.conn.Done():
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case <-s.gen.done:
		case <-t.C:
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func (s *acpChildSession) bootstrap() {
	defer close(s.bootDone)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := agentutil.BootstrapACPSession(ctx, s.conn, agentutil.ACPBootstrapInput{
		Cwd: s.rt.workerMeta.Cwd, SessionID: s.rt.workerMeta.NativeID, IsExisting: s.isExisting,
		Env: s.env, SelectAuth: s.driver.SelectACPAuth,
	})
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			s.waitStderrDrained(500 * time.Millisecond)
		}
		tail := strings.TrimSpace(s.stderrTail.String())
		enrichedErr := err
		if tail != "" && !strings.Contains(err.Error(), tail) {
			enrichedErr = fmt.Errorf("%w: %s", err, tail)
		}
		s.mu.Lock()
		s.bootErr = enrichedErr
		s.bootBlock = detectTurnFailureBlockWithRegistry(s.reg, s.rt.workerMeta.Agent, err.Error()+"\n"+tail)
		s.mu.Unlock()
		return
	}
	if res == nil {
		return
	}
	adoptedValid := res.SessionID != "" && s.isValidSessionID(res.SessionID)
	s.mu.Lock()
	if adoptedValid {
		s.sessionID = res.SessionID
	}
	s.mu.Unlock()

	if adoptedValid && res.AdoptedNewID && s.deps.onSessionInit != nil {
		s.deps.onSessionInit(s.gen, res.SessionID, res.CurrentModel)
	} else if res.CurrentModel != "" && s.deps.onModelDetected != nil {
		s.deps.onModelDetected(s.gen, res.CurrentModel)
	}
	if res.Models != nil {
		s.driver.HandleACPNotification(s.homeDir, s.env, "_x.ai/models/update", map[string]any{"models": res.Models, "currentModelId": res.CurrentModel})
	}
	if res.AvailableCommands != nil {
		s.driver.HandleACPNotification(s.homeDir, s.env, "session/update", map[string]any{"update": map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": res.AvailableCommands}})
	}
}

func (s *acpChildSession) waitForExit() {
	<-s.conn.Done()
	waitErr := s.cmd.Wait()
	close(s.gen.done)

	code := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	s.streamMu.Lock()
	lastText := s.streamState.LastText()
	s.streamMu.Unlock()
	tail := strings.TrimSpace(s.stderrTail.String())

	s.rt.mu.Lock()
	if s.gen.replaced || *s.rt.curChild != s.gen || *s.rt.state == "shutting_down" || *s.rt.state == "exited" {
		s.rt.mu.Unlock()
		return
	}
	curState, turn := *s.rt.state, *s.rt.activeTurn
	if curState == "idle_warm" || curState == "cancelling" || turn == nil {
		s.rt.bgCtrl.resetOnChildChangeLocked()
		*s.rt.pendingControl, s.rt.workerMeta.PendingControl = nil, nil
		*s.rt.state, s.rt.workerMeta.State = "exited", "exited"
		s.deps.saveMeta()
		s.rt.notifyWatchersLocked()
		s.rt.mu.Unlock()
		s.deps.exitCleanly(0)
		return
	}

	finalStatus := "completed"
	if turn.cancelled {
		_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.turnID, Type: "turn_cancelled", Text: "Turn was cancelled by user."})
		finalStatus = "cancelled"
	} else if failBlock := detectTurnFailureBlockWithRegistry(s.reg, s.rt.workerMeta.Agent, strings.TrimSpace(lastText+"\n"+tail)); failBlock != nil {
		_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.turnID, Type: "interactive_block", Blocked: failBlock})
		turn.hadBlock = true
	} else {
		errMsg := tail
		if errMsg == "" {
			errMsg = fmt.Sprintf("Process exited with status %d", code)
		}
		_, _ = turn.spool.Append(ChatStreamEvent{SessionID: turn.meta.SessionID, TurnID: turn.turnID, Type: "error", Text: errMsg, IsError: true})
		finalStatus = "failed"
	}
	s.deps.finalizeTurnMeta(turn, s.stderrTail, finalStatus, code, "")
	*s.rt.activeTurn = nil
	s.rt.bgCtrl.resetOnChildChangeLocked()
	*s.rt.pendingControl, s.rt.workerMeta.PendingControl = nil, nil
	*s.rt.state, s.rt.workerMeta.State = "exited", "exited"
	s.deps.saveMeta()
	s.rt.notifyWatchersLocked()
	s.rt.mu.Unlock()
	s.deps.exitCleanly(0)
}
