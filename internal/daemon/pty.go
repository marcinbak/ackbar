package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/net/websocket"
)

type PTYResizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type PTYControlMsg struct {
	Type      string `json:"type"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Direction string `json:"direction"`
	Lines     int    `json:"lines"`
}

type ptyCommandRunner func(name string, args ...string) ([]byte, error)

func defaultPTYCommandRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// performTmuxScroll executes tmux copy-mode scroll actions for mobile/web touch and page scrolling.
func performTmuxScroll(tmuxName string, isLocal bool, sshTarget string, direction string, lines int, runCmd ptyCommandRunner) error {
	if tmuxName == "" || !validTaskIDRegex.MatchString(tmuxName) {
		return nil
	}
	if runCmd == nil {
		runCmd = defaultPTYCommandRunner
	}
	if lines <= 0 {
		lines = 1
	} else if lines > 100 {
		lines = 100
	}
	dir := strings.ToLower(strings.TrimSpace(direction))

	if isLocal {
		switch dir {
		case "up":
			_, _ = runCmd("tmux", "copy-mode", "-e", "-t", tmuxName)
			_, err := runCmd("tmux", "send-keys", "-t", tmuxName, "-X", "-N", strconv.Itoa(lines), "scroll-up")
			return err
		case "down":
			_, err := runCmd("tmux", "send-keys", "-t", tmuxName, "-X", "-N", strconv.Itoa(lines), "scroll-down")
			if out, derr := runCmd("tmux", "display-message", "-p", "-t", tmuxName, "#{pane_in_mode} #{scroll_position}"); derr == nil {
				if strings.TrimSpace(string(out)) == "1 0" {
					_, _ = runCmd("tmux", "copy-mode", "-q", "-t", tmuxName)
				}
			}
			return err
		case "page_up":
			_, err := runCmd("tmux", "copy-mode", "-eu", "-t", tmuxName)
			return err
		case "page_down":
			_, err := runCmd("tmux", "send-keys", "-t", tmuxName, "-X", "page-down")
			if out, derr := runCmd("tmux", "display-message", "-p", "-t", tmuxName, "#{pane_in_mode} #{scroll_position}"); derr == nil {
				if strings.TrimSpace(string(out)) == "1 0" {
					_, _ = runCmd("tmux", "copy-mode", "-q", "-t", tmuxName)
				}
			}
			return err
		case "bottom":
			_, err := runCmd("tmux", "copy-mode", "-q", "-t", tmuxName)
			return err
		}
		return nil
	}

	if sshTarget == "" {
		return nil
	}

	var remoteCmd string
	switch dir {
	case "up":
		remoteCmd = fmt.Sprintf("tmux copy-mode -e -t %q 2>/dev/null; tmux send-keys -t %q -X -N %d scroll-up 2>/dev/null", tmuxName, tmuxName, lines)
	case "down":
		remoteCmd = fmt.Sprintf("tmux send-keys -t %q -X -N %d scroll-down 2>/dev/null; if [ \"$(tmux display-message -p -t %q '#{pane_in_mode} #{scroll_position}' 2>/dev/null)\" = '1 0' ]; then tmux copy-mode -q -t %q 2>/dev/null; fi", tmuxName, lines, tmuxName, tmuxName)
	case "page_up":
		remoteCmd = fmt.Sprintf("tmux copy-mode -eu -t %q 2>/dev/null", tmuxName)
	case "page_down":
		remoteCmd = fmt.Sprintf("tmux send-keys -t %q -X page-down 2>/dev/null; if [ \"$(tmux display-message -p -t %q '#{pane_in_mode} #{scroll_position}' 2>/dev/null)\" = '1 0' ]; then tmux copy-mode -q -t %q 2>/dev/null; fi", tmuxName, tmuxName, tmuxName)
	case "bottom":
		remoteCmd = fmt.Sprintf("tmux copy-mode -q -t %q 2>/dev/null", tmuxName)
	default:
		return nil
	}
	_, err := runCmd("ssh", sshTarget, remoteCmd)
	return err
}

// Minimum terminal dimensions accepted from a client resize request.
const (
	ptyMinCols = 10
	ptyMinRows = 4
)

// ptyResizeTracker deduplicates client resize requests for one attach PTY.
//
// Every TIOCSWINSZ on the attach PTY makes the tmux client report its size,
// and any resulting window size change delivers SIGWINCH to the agent. Some
// agents (e.g. Antigravity) answer each SIGWINCH by clearing the screen and
// reprinting their entire transcript, so redundant resizes must never reach
// the PTY. Not safe for concurrent use; owned by the WS->PTY goroutine.
type ptyResizeTracker struct {
	cols, rows int
	setsize    func(cols, rows int) error
}

// newPTYResizeTracker returns a tracker seeded with the size the PTY was
// started with, so a first resize message repeating it is a no-op.
func newPTYResizeTracker(cols, rows int, setsize func(cols, rows int) error) *ptyResizeTracker {
	return &ptyResizeTracker{cols: cols, rows: rows, setsize: setsize}
}

// Apply resizes the PTY only when the requested size is valid and differs
// from the last applied size. It reports whether setsize was called.
func (t *ptyResizeTracker) Apply(cols, rows int) (bool, error) {
	if cols < ptyMinCols || rows < ptyMinRows || cols > 0xFFFF || rows > 0xFFFF {
		return false, nil
	}
	if cols == t.cols && rows == t.rows {
		return false, nil
	}
	if err := t.setsize(cols, rows); err != nil {
		return true, err
	}
	t.cols, t.rows = cols, rows
	return true, nil
}

// ptyCodec handles both text and binary WebSocket frames seamlessly across Web, Mobile, and CLI clients
var ptyCodec = websocket.Codec{
	Marshal: func(v interface{}) ([]byte, byte, error) {
		switch data := v.(type) {
		case []byte:
			return data, websocket.BinaryFrame, nil
		case string:
			return []byte(data), websocket.TextFrame, nil
		default:
			return nil, 0, fmt.Errorf("unsupported payload type: %T", v)
		}
	},
	Unmarshal: func(msg []byte, payloadType byte, v interface{}) error {
		switch target := v.(type) {
		case *[]byte:
			*target = make([]byte, len(msg))
			copy(*target, msg)
			return nil
		case *string:
			*target = string(msg)
			return nil
		default:
			return fmt.Errorf("unsupported target type: %T", v)
		}
	},
}

func (s *Server) handlePTY(w http.ResponseWriter, r *http.Request) {
	server := websocket.Server{
		Handler: s.servePTYWS,
		Handshake: func(config *websocket.Config, req *http.Request) error {
			// Allow all loopback and tunneled origins since ackbard binds strictly to 127.0.0.1
			return nil
		},
	}
	server.ServeHTTP(w, r)
}

func buildRemotePTYAttachArgs(sshTarget, tmuxName, cwd, resumeCmd string) []string {
	var newSessCmd string
	if resumeCmd != "" {
		innerBash := fmt.Sprintf("cd %s 2>/dev/null || true; export PATH=\"/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$PATH\"; %s; exec bash -l",
			shellSingleQuote(cwd), resumeCmd)
		newSessCmd = fmt.Sprintf("tmux new-session -d -s %s -c %s bash -l -c %s",
			shellSingleQuote(tmuxName), shellSingleQuote(cwd), shellSingleQuote(innerBash))
	} else {
		newSessCmd = fmt.Sprintf("tmux new-session -d -s %s -c %s",
			shellSingleQuote(tmuxName), shellSingleQuote(cwd))
	}

	remoteScript := fmt.Sprintf("tmux has-session -t %s 2>/dev/null || %s; tmux set-option -t %s mouse on 2>/dev/null || true; tmux set-option -t %s window-size latest 2>/dev/null || true; tmux set-window-option -t %s window-size latest 2>/dev/null || true; exec tmux attach-session -t %s",
		shellSingleQuote(tmuxName),
		newSessCmd,
		shellSingleQuote(tmuxName),
		shellSingleQuote(tmuxName),
		shellSingleQuote(tmuxName),
		shellSingleQuote(tmuxName),
	)

	return []string{"-t", "--", sshTarget, remoteScript}
}

func (s *Server) servePTYWS(ws *websocket.Conn) {
	defer ws.Close()

	req := ws.Request()
	sessionID := req.URL.Query().Get("id")
	hostParam := req.URL.Query().Get("host")
	colsStr := req.URL.Query().Get("cols")
	rowsStr := req.URL.Query().Get("rows")

	cols := 120
	rows := 32
	if c, err := strconv.Atoi(colsStr); err == nil && c > 0 {
		cols = c
	}
	if r, err := strconv.Atoi(rowsStr); err == nil && r > 0 {
		rows = r
	}

	if sessionID == "" {
		_, _ = ws.Write([]byte("\r\n\x1b[31mError: Missing session 'id' parameter\x1b[0m\r\n"))
		return
	}

	if s.db.IsSessionDeleted(sessionID) {
		_, _ = ws.Write([]byte("\r\n\x1b[90m[Session was deleted]\x1b[0m\r\n"))
		return
	}

	// Resilient lookup of session
	sess := s.resolveSession(sessionID)
	if sess == nil {
		if parts := strings.Split(sessionID, ":"); len(parts) >= 3 {
			sess, _ = s.db.GetSession(parts[2])
		}
	}

	tmuxName := ""
	sessHost := "local"
	if hostParam != "" {
		sessHost = hostParam
	}

	if sess != nil {
		if sess.IsUnread {
			sess.IsUnread = false
			_ = s.db.MarkSessionRead(sess.ID)
			s.broadcast(sess)
		}
		if sess.TmuxName != "" && sess.TmuxName != "(deleted)" {
			tmuxName = sess.TmuxName
		}
		if sess.Host != "" {
			sessHost = sess.Host
		}
	}

	if tmuxName == "" || tmuxName == "(deleted)" {
		parts := strings.Split(sessionID, ":")
		nativeID := sessionID
		agent := "claude-code"
		if len(parts) >= 3 {
			agent = parts[0]
			nativeID = parts[2]
		}
		tmuxName = fmt.Sprintf("ackbar-%s-%s", agent, nativeID)
	}

	var cmd *exec.Cmd
	cwd := ""
	resumeCmd := ""
	if sess != nil {
		cwd = sess.Cwd
		if sess.NativeID != "" && sess.Agent != "" {
			resumeCmd = s.getResumeCmd(sess.Agent, sess.NativeID)
		}
	}
	if cwd == "" {
		cwd = os.Getenv("HOME")
	}

	isLocal := s.isRequestForLocalDaemon(req, sessHost)
	sshTarget := ""
	if isLocal {
		// Ensure local tmux session exists before attaching
		if err := exec.Command("tmux", "has-session", "-t", tmuxName).Run(); err != nil {
			if resumeCmd != "" {
				replaySetting := ""
				if s.db != nil {
					replaySetting, _ = s.db.GetSetting("replay_transcript_on_resume")
				}
				replayRequested := req.URL.Query().Get("replay") == "true" || replaySetting == "true"

				transcriptText := ""
				if replayRequested && sess != nil && sess.NativeID != "" {
					if t, terr := s.ExtractTranscript(sess.Agent, sess.NativeID, cwd); terr == nil && t != nil && len(t.Messages) > 0 {
						transcriptText = FormatTranscriptANSI(t)
					}
				}

				micExport := ""
				if sess != nil && strings.EqualFold(sess.Agent, "antigravity") {
					if addr := s.VoiceBridgeAddr(); addr != "" {
						micExport = fmt.Sprintf("export ANTIGRAVITY_MIC=%q; ", addr)
					}
				}

				if transcriptText != "" {
					tmpFile, ferr := os.CreateTemp("", "ackbar-transcript-*.txt")
					if ferr == nil {
						_, _ = tmpFile.WriteString(transcriptText)
						_ = tmpFile.Close()
						shellCmd := fmt.Sprintf("cat %q 2>/dev/null; rm -f %q; cd %q 2>/dev/null || true; %sexport PATH=\"/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$PATH\"; %s; exec bash -l", tmpFile.Name(), tmpFile.Name(), cwd, micExport, resumeCmd)
						_ = exec.Command("tmux", "new-session", "-d", "-s", tmuxName, "-c", cwd, "bash", "-l", "-c", shellCmd).Run()
					} else {
						shellCmd := fmt.Sprintf("cd %q 2>/dev/null || true; %sexport PATH=\"/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$PATH\"; %s; exec bash -l", cwd, micExport, resumeCmd)
						_ = exec.Command("tmux", "new-session", "-d", "-s", tmuxName, "-c", cwd, "bash", "-l", "-c", shellCmd).Run()
					}
				} else {
					shellCmd := fmt.Sprintf("cd %q 2>/dev/null || true; %sexport PATH=\"/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$PATH\"; %s; exec bash -l", cwd, micExport, resumeCmd)
					_ = exec.Command("tmux", "new-session", "-d", "-s", tmuxName, "-c", cwd, "bash", "-l", "-c", shellCmd).Run()
				}
			} else {
				_ = exec.Command("tmux", "new-session", "-d", "-s", tmuxName, "-c", cwd).Run()
			}
		}
		// Window sizing is left entirely to tmux: the attach PTY below is started
		// at the client's size and `window-size latest` makes the window follow
		// the most recently active client. Re-asserting `latest` on every attach
		// also heals sessions left in `manual` mode by older builds, which used
		// `resize-window` (that implicitly switches window-size to manual).
		_ = exec.Command("tmux", "set-option", "-t", tmuxName, "mouse", "on").Run()
		_ = exec.Command("tmux", "set-option", "-t", tmuxName, "window-size", "latest").Run()
		_ = exec.Command("tmux", "set-window-option", "-t", tmuxName, "window-size", "latest").Run()
		cmd = exec.Command("tmux", "attach-session", "-t", tmuxName)
	} else {
		sshTarget = s.resolveSSHTarget(sessHost)
		args := buildRemotePTYAttachArgs(sshTarget, tmuxName, cwd, resumeCmd)
		cmd = exec.Command("ssh", args...)
	}

	var cleanEnv []string
	hasPath := false
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TERM=") && !strings.HasPrefix(e, "COLORTERM=") && !strings.HasPrefix(e, "TERMINFO=") && !strings.HasPrefix(e, "TERMINFO_DIRS=") {
			if strings.HasPrefix(e, "PATH=") {
				hasPath = true
				cleanEnv = append(cleanEnv, "PATH=/opt/homebrew/bin:/usr/local/bin:"+os.Getenv("HOME")+"/.local/bin:"+os.Getenv("HOME")+"/.npm-global/bin:"+e[5:])
			} else {
				cleanEnv = append(cleanEnv, e)
			}
		}
	}
	if !hasPath {
		cleanEnv = append(cleanEnv, "PATH=/opt/homebrew/bin:/usr/local/bin:"+os.Getenv("HOME")+"/.local/bin:"+os.Getenv("HOME")+"/.npm-global/bin:/usr/bin:/bin:/usr/sbin:/sbin")
	}
	cleanEnv = append(cleanEnv,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
	)
	cmd.Env = cleanEnv

	winSize := &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	}

	log.Printf("[PTY] Spawning PTY for session %q (tmuxName: %q, host: %q, cmd: %v)", sessionID, tmuxName, sessHost, cmd.Args)

	ptyFile, err := pty.StartWithSize(cmd, winSize)
	if err != nil {
		log.Printf("[PTY] Failed to start PTY: %v", err)
		_, _ = ws.Write([]byte(fmt.Sprintf("\r\n\x1b[31mFailed to spawn PTY for tmux '%s': %v\x1b[0m\r\n", tmuxName, err)))
		return
	}
	defer func() {
		_ = ptyFile.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			_ = cmd.Process.Kill()
		}
		waitErr := cmd.Wait()
		log.Printf("[PTY] PTY process terminated (waitErr: %v)", waitErr)
	}()

	// Channel to signal connection close
	done := make(chan struct{})
	var wsMu sync.Mutex

	resizer := newPTYResizeTracker(cols, rows, func(c, r int) error {
		return pty.Setsize(ptyFile, &pty.Winsize{Rows: uint16(r), Cols: uint16(c)})
	})

	safeWrite := func(data []byte) error {
		wsMu.Lock()
		defer wsMu.Unlock()
		return ptyCodec.Send(ws, data)
	}

	// Goroutine 1: Stream PTY Output -> WebSocket
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptyFile.Read(buf)
			if err != nil {
				log.Printf("[PTY] Goroutine 1 (PTY->WS) read error: %v", err)
				return
			}
			if n > 0 {
				if werr := safeWrite(buf[:n]); werr != nil {
					log.Printf("[PTY] Goroutine 1 (PTY->WS) write error: %v", werr)
					return
				}
			}
		}
	}()

	// Goroutine 2: Stream WebSocket Input -> PTY using flexible ptyCodec
	go func() {
		for {
			var msg []byte
			err := ptyCodec.Receive(ws, &msg)
			if err != nil {
				if err != io.EOF {
					log.Printf("[PTY] Goroutine 2 (WS->PTY) receive error: %v", err)
				}
				_ = ptyFile.Close()
				return
			}
			if len(msg) > 0 {
				// Check if it's a JSON resize, scroll, or ping control payload
				if msg[0] == '{' {
					var ctrl PTYControlMsg
					if jerr := json.Unmarshal(msg, &ctrl); jerr == nil {
						if ctrl.Type == "resize" {
							// Only the attach PTY is resized: the tmux client attached
							// to it (local `tmux attach` or remote `ssh -t`) forwards
							// the new size and `window-size latest` makes the window
							// follow. Unchanged or invalid sizes are dropped so they
							// never SIGWINCH the agent. The message is always consumed
							// so it is never typed into the terminal.
							if _, rerr := resizer.Apply(ctrl.Cols, ctrl.Rows); rerr != nil {
								log.Printf("[PTY] Resize to %dx%d failed: %v", ctrl.Cols, ctrl.Rows, rerr)
							}
							continue
						}
						if ctrl.Type == "scroll" {
							if serr := performTmuxScroll(tmuxName, isLocal, sshTarget, ctrl.Direction, ctrl.Lines, defaultPTYCommandRunner); serr != nil {
								log.Printf("[PTY] Scroll (%s, %d) failed: %v", ctrl.Direction, ctrl.Lines, serr)
							}
							continue
						}
						if ctrl.Type == "ping" {
							// Consume client keepalive ping silently to keep socket active without injecting into terminal
							continue
						}
					}
				}
				// Forward raw keystrokes directly to PTY
				if _, werr := ptyFile.Write(msg); werr != nil {
					log.Printf("[PTY] Goroutine 2 (WS->PTY) write error: %v", werr)
					return
				}
			}
		}
	}()

	// Goroutine 3: Heartbeat keepalive loop to prevent tunnel/proxy timeouts
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = safeWrite([]byte{})
			}
		}
	}()

	<-done
}
