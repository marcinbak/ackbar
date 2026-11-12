package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"ackbar/internal/tmux"
)

var (
	oauthPortRegex = regexp.MustCompile(`http://(?:127\.0\.0\.1|localhost):(\d{4,5})\b`)
	oauthURLRegex  = regexp.MustCompile(`https://[^\s'"<>()\x00-\x1f\x7f]+`)
	oauthCodeRegex = regexp.MustCompile(`^[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]+$`)
)

type authRingBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newAuthRingBuffer(capacity int) *authRingBuffer {
	return &authRingBuffer{cap: capacity, buf: make([]byte, 0, capacity)}
}

func (r *authRingBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	if n >= r.cap {
		r.buf = append(r.buf[:0], p[n-r.cap:]...)
		return n, nil
	}
	overflow := (len(r.buf) + n) - r.cap
	if overflow > 0 {
		r.buf = append(r.buf[overflow:], p...)
	} else {
		r.buf = append(r.buf, p...)
	}
	return n, nil
}

func (r *authRingBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

type AuthFlow struct {
	ID           string
	SessionID    string
	Target       string
	RecordedPort int
	AcceptsStdin bool
	stdin        ioWriteCloser
	stdinUsed    bool
	cmd          *exec.Cmd
	cancel       func()
	buf          *authRingBuffer
	doneCh       chan struct{}
	exitCode     int
	exitErr      error
	startedAt    time.Time
	mu           sync.Mutex
}

type ioWriteCloser interface {
	Write(p []byte) (n int, err error)
	Close() error
}

func validateAuthURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, fmt.Errorf("invalid url length")
	}
	if strings.ContainsAny(raw, " \t\r\n\x00") {
		return nil, fmt.Errorf("url contains whitespace or null")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] == 0x7f {
			return nil, fmt.Errorf("url contains control character")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("url must be https with non-empty host and no userinfo")
	}
	return u, nil
}

func resolveAllowlistedAuthCommand(sess *Session, target string, db *DB, home string) ([]string, string, bool, error) {
	if home == "" || !filepath.IsAbs(home) {
		return nil, "", false, fmt.Errorf("invalid home directory")
	}
	if strings.HasPrefix(target, "plugin:") {
		name := strings.TrimPrefix(target, "plugin:")
		if name != "gslides" && name != "gdocs" && name != "gdrive" && name != "gsheets" {
			return nil, "", false, fmt.Errorf("unsupported plugin: %s", name)
		}
		scriptPath := filepath.Join(home, ".gemini", "config", "plugins", name, "skills", name, "scripts", name)
		evalGemini, err := filepath.EvalSymlinks(filepath.Join(home, ".gemini"))
		if err != nil {
			evalGemini = filepath.Join(home, ".gemini")
		}
		evalScript, err := filepath.EvalSymlinks(scriptPath)
		if err != nil {
			return nil, "", false, fmt.Errorf("plugin script not found: %s", scriptPath)
		}
		stat, err := os.Stat(evalScript)
		if err != nil || stat.IsDir() {
			return nil, "", false, fmt.Errorf("plugin script not regular file: %s", scriptPath)
		}
		prefix := evalGemini + string(filepath.Separator)
		if !strings.HasPrefix(evalScript, prefix) && evalScript != evalGemini {
			return nil, "", false, fmt.Errorf("plugin script escaped ~/.gemini: %s", evalScript)
		}
		return []string{evalScript, "auth", "login"}, "", true, nil
	}

	if target == "agent:agy" {
		bin, err := resolveAntigravityBinary()
		if err != nil {
			return nil, "", false, err
		}
		return []string{bin, "acp", "--login"}, "", true, nil
	}

	if strings.HasPrefix(target, "mcp:") {
		name := strings.TrimPrefix(target, "mcp:")
		if strings.HasPrefix(name, "-") || !validServerNameRegex.MatchString(name) {
			return nil, "", false, fmt.Errorf("invalid mcp server name: %s", name)
		}
		if sess == nil || sess.Agent != "claude-code" {
			return nil, "", false, fmt.Errorf("mcp auth broker requires claude-code session")
		}
		servers, err := discoverConfiguredMCPServers(sess, db, home)
		if err != nil {
			return nil, "", false, err
		}
		var found *MCPServerHealth
		for _, s := range servers {
			if s.Name == name {
				found = &s
				break
			}
		}
		if found == nil || (found.Transport != "http" && found.Transport != "sse") {
			return nil, "", false, fmt.Errorf("mcp server %s is not configured for http/sse", name)
		}
		bin, err := resolveClaudeBinary()
		if err != nil {
			return nil, "", false, err
		}
		return []string{bin, "mcp", "login", "--no-browser", name}, sess.Cwd, true, nil
	}

	return nil, "", false, fmt.Errorf("unrecognized target: %s", target)
}

func resumePromptForAuth(sess *Session, prevBlocked *Blocked, home string, db *DB) string {
	if prevBlocked != nil && prevBlocked.AuthKind == "agent" && sess != nil && sess.LastPrompt != "" {
		return sess.LastPrompt
	}
	if sess != nil && !hasConversationOnDisk(sess, home, db) && sess.LastPrompt != "" {
		return sess.LastPrompt
	}
	server := "operation"
	if prevBlocked != nil && prevBlocked.AuthServer != "" {
		server = prevBlocked.AuthServer
	}
	return fmt.Sprintf("Authentication for %s succeeded. Please retry the failed operation and continue.", server)
}

func (b *AuthBroker) CancelSession(sessionID string) {
	b.mu.Lock()
	flow, exists := b.activeFlows[sessionID]
	if exists {
		delete(b.activeFlows, sessionID)
	}
	b.mu.Unlock()
	if exists && flow != nil {
		if flow.cancel != nil {
			flow.cancel()
		}
		if flow.stdin != nil {
			_ = flow.stdin.Close()
		}
	}
}

func (b *AuthBroker) CloseAll() {
	b.mu.Lock()
	flows := make([]*AuthFlow, 0, len(b.activeFlows))
	for _, f := range b.activeFlows {
		flows = append(flows, f)
	}
	b.activeFlows = make(map[string]*AuthFlow)
	b.mu.Unlock()

	for _, f := range flows {
		if f.cancel != nil {
			f.cancel()
		}
		if f.stdin != nil {
			_ = f.stdin.Close()
		}
	}
}

func (b *AuthBroker) watchAuthFlow(flow *AuthFlow) {
	waitErr := flow.cmd.Wait()
	flow.mu.Lock()
	flow.exitErr = waitErr
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			flow.exitCode = exitErr.ExitCode()
		} else {
			flow.exitCode = 1
		}
	} else {
		flow.exitCode = 0
	}
	flow.mu.Unlock()
	close(flow.doneCh)

	b.mu.Lock()
	if cur := b.activeFlows[flow.SessionID]; cur == flow {
		delete(b.activeFlows, flow.SessionID)
	}
	if len(b.completedFlows) >= 64 {
		var oldestID string
		var oldestTime time.Time
		for id, f := range b.completedFlows {
			if oldestID == "" || f.startedAt.Before(oldestTime) {
				oldestID = id
				oldestTime = f.startedAt
			}
		}
		if oldestID != "" {
			delete(b.completedFlows, oldestID)
		}
	}
	b.completedFlows[flow.ID] = flow
	b.mu.Unlock()

	time.AfterFunc(2*time.Minute, func() {
		b.mu.Lock()
		delete(b.completedFlows, flow.ID)
		b.mu.Unlock()
	})

	if flow.exitCode != 0 {
		return
	}

	s := b.server
	if s == nil {
		return
	}

	s.invalidateMCPStatusCache(flow.SessionID)

	var sess *Session
	if s.db != nil {
		sess, _ = s.db.GetSession(flow.SessionID)
	}
	isPureHeadless := sess != nil && sess.EngineType == EngineHeadless && sess.TmuxName == ""
	if s.headless != nil && sess != nil {
		if isPureHeadless {
			if s.headless.IsRunning(flow.SessionID) {
				s.headless.ScheduleChildRestartAfterTurn(flow.SessionID)
			} else {
				s.headless.RestartSessionChild(sess, 3*time.Second)
			}
		}
		s.headless.Emit(flow.SessionID, ChatStreamEvent{
			SessionID: flow.SessionID,
			Type:      "auth_complete",
			Text:      flow.Target,
			Timestamp: time.Now(),
		}, "")
	}

	if sess != nil && sess.State == StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == BlockAuth {
		prevBlocked := sess.Blocked
		sess.Blocked = nil
		sess.State = StateIdle
		sess.Activity = "Authentication completed"
		sess.LastStateChangeAt = time.Now()
		sess.LastEventAt = time.Now()
		sess.IsUnread = false

		if s.headless != nil {
			s.headless.SetQueuePaused(flow.SessionID, false)
			s.headless.EmitQueueUpdate(flow.SessionID)
		}
		if !isPureHeadless && sess.TmuxName != "" && sess.Cwd != "" {
			home := s.effectiveHomeDir()
			if launchCmd, _ := s.getRestartOrResumeCmd(sess, home); launchCmd != "" {
				envVars := s.buildTmuxSessionEnv(sess, home)
				s.recentTmuxRestarts.Store(sess.ID, time.Now())
				if err := tmux.RespawnWithEnv(context.Background(), sess.TmuxName, sess.Cwd, launchCmd, envVars); err == nil {
					sess.State = StateWorking
					sess.Activity = "Restarted after authentication"
					if pid, perr := tmux.GetPID(context.Background(), sess.TmuxName); perr == nil {
						sess.PID = pid
					}
				}
			}
		}
		if s.db != nil {
			_ = s.db.SaveSession(sess)
		}
		s.broadcast(sess)

		if isPureHeadless && s.headless != nil {
			sessCopy := *sess
			go func() {
				home, _ := os.UserHomeDir()
				prompt := resumePromptForAuth(&sessCopy, prevBlocked, home, s.db)
				_ = s.headless.RunTurn(context.Background(), &sessCopy, prompt)
			}()
		}
	}
}
