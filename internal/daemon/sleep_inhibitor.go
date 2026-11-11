package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"syscall"
)

// SleepInhibitor holds an OS idle-sleep lock only while one or more headless turns are active.
// Child inhibitors monitor parentPID via cross-OS POSIX mechanisms so no lock leaks if ackbard crashes.
type SleepInhibitor struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	goos       string
	parentPID  int
	cmdFactory func(goos string, parentPID int) *exec.Cmd
	warnedOnce bool
}

func NewSleepInhibitor() *SleepInhibitor {
	return &SleepInhibitor{
		goos:       runtime.GOOS,
		parentPID:  os.Getpid(),
		cmdFactory: defaultSleepInhibitorCmd,
	}
}

func defaultSleepInhibitorCmd(goos string, parentPID int) *exec.Cmd {
	if parentPID <= 1 {
		return nil
	}
	var cmd *exec.Cmd
	switch goos {
	case "darwin":
		cmd = exec.Command("caffeinate", "-i", "-w", strconv.Itoa(parentPID))
	case "linux":
		// Poll parent liveness via POSIX kill -0 rather than Linux-only Pdeathsig so builds remain cross-OS.
		watchScript := fmt.Sprintf("while kill -0 %d 2>/dev/null; do sleep 2; done", parentPID)
		cmd = exec.Command(
			"systemd-inhibit",
			"--what=idle:sleep",
			"--who=ackbard",
			"--why=Active headless agent turn",
			"--mode=block",
			"sh", "-c", watchScript,
		)
	default:
		return nil
	}
	// Run inhibitor in its own process group so releasing the lock kills child sh/sleep sub-processes too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func (s *SleepInhibitor) SyncActiveTurns(count int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if count <= 0 {
		if s.cmd != nil && s.cmd.Process != nil {
			if pid := s.cmd.Process.Pid; pid > 1 {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
			_ = s.cmd.Process.Kill()
		}
		s.cmd = nil
		return
	}

	if s.cmd != nil {
		return
	}

	factory := s.cmdFactory
	if factory == nil {
		factory = defaultSleepInhibitorCmd
	}
	goos := s.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	pid := s.parentPID
	if pid <= 1 {
		pid = os.Getpid()
	}

	cmd := factory(goos, pid)
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	} else {
		cmd.SysProcAttr.Setpgid = true
	}
	if err := cmd.Start(); err != nil {
		if !s.warnedOnce {
			s.warnedOnce = true
			log.Printf("[SleepInhibitor] idle-sleep lock unavailable (%s): %v", goos, err)
		}
		return
	}

	s.cmd = cmd
	go func(started *exec.Cmd) {
		_ = started.Wait()
		s.mu.Lock()
		if s.cmd == started {
			s.cmd = nil
		}
		s.mu.Unlock()
	}(cmd)
}

func (s *SleepInhibitor) Release() {
	s.SyncActiveTurns(0)
}

func (s *SleepInhibitor) IsActive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil && s.cmd.Process != nil && s.cmd.Process.Pid > 1
}
