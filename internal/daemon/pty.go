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

func (s *Server) servePTYWS(ws *websocket.Conn) {
	defer ws.Close()
	ws.PayloadType = websocket.BinaryFrame

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
	sess, _ := s.db.GetSession(sessionID)
	if sess == nil {
		// Fallback 1: check with agent:host:id format
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 3 {
			localID := parts[2]
			sess, _ = s.db.GetSession(localID)
		}
	}
	if sess == nil {
		// Fallback 2: Check by NativeID match
		if all, err := s.db.ListSessions(); err == nil {
			for _, sRecord := range all {
				if sRecord.NativeID != "" && strings.HasSuffix(sessionID, ":"+sRecord.NativeID) {
					sess = sRecord
					break
				}
			}
		}
	}

	tmuxName := ""
	sessHost := "local"
	if hostParam != "" {
		sessHost = hostParam
	}

	if sess != nil {
		if sess.TmuxName != "" {
			tmuxName = sess.TmuxName
		}
		if sess.Host != "" {
			sessHost = sess.Host
		}
	}

	if tmuxName == "" {
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
			resumeCmd = getResumeCmd(sess.Agent, sess.NativeID)
		}
	}
	if cwd == "" {
		cwd = os.Getenv("HOME")
	}

	if sessHost == "local" {
		// Ensure local tmux session exists before attaching
		if err := exec.Command("tmux", "has-session", "-t", tmuxName).Run(); err != nil {
			if resumeCmd != "" {
				shellCmd := fmt.Sprintf("export PATH=\"$HOME/.local/bin:$HOME/.npm-global/bin:$PATH\"; %s; exec bash -l", resumeCmd)
				_ = exec.Command("tmux", "new-session", "-d", "-s", tmuxName, "-c", cwd, "bash", "-l", "-c", shellCmd).Run()
			} else {
				_ = exec.Command("tmux", "new-session", "-d", "-s", tmuxName, "-c", cwd).Run()
			}
		}
		cmd = exec.Command("tmux", "attach-session", "-t", tmuxName)
	} else {
		// Ensure remote tmux session exists before attaching
		remoteShellCmd := ""
		if resumeCmd != "" {
			remoteShellCmd = fmt.Sprintf(" bash -l -c %q", fmt.Sprintf("export PATH=\"$HOME/.local/bin:$HOME/.npm-global/bin:$PATH\"; %s; exec bash -l", resumeCmd))
		}
		ensureRemoteCmd := fmt.Sprintf("tmux has-session -t %q 2>/dev/null || tmux new-session -d -s %q -c %q%s", tmuxName, tmuxName, cwd, remoteShellCmd)
		_ = exec.Command("ssh", sessHost, ensureRemoteCmd).Run()
		cmd = exec.Command("ssh", "-t", sessHost, fmt.Sprintf("tmux attach-session -t %q", tmuxName))
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

	safeWrite := func(data []byte) error {
		wsMu.Lock()
		defer wsMu.Unlock()
		_, err := ws.Write(data)
		return err
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

	// Goroutine 2: Stream WebSocket Input -> PTY using raw ws.Read
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ws.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("[PTY] Goroutine 2 (WS->PTY) receive error: %v", err)
				}
				_ = ptyFile.Close()
				return
			}
			if n > 0 {
				msg := buf[:n]
				// Check if it's a JSON resize or ping control payload
				if msg[0] == '{' {
					var ctrl struct {
						Type string `json:"type"`
						Cols int    `json:"cols"`
						Rows int    `json:"rows"`
					}
					if jerr := json.Unmarshal(msg, &ctrl); jerr == nil {
						if ctrl.Type == "resize" && ctrl.Cols >= 10 && ctrl.Rows >= 4 {
							_ = pty.Setsize(ptyFile, &pty.Winsize{
								Rows: uint16(ctrl.Rows),
								Cols: uint16(ctrl.Cols),
							})
							if sessHost == "local" {
								_ = exec.Command("tmux", "resize-window", "-t", tmuxName, "-x", strconv.Itoa(ctrl.Cols), "-y", strconv.Itoa(ctrl.Rows)).Run()
								_ = exec.Command("tmux", "refresh-client", "-S").Run()
							} else {
								_ = exec.Command("ssh", sessHost, fmt.Sprintf("tmux resize-window -t %q -x %d -y %d 2>/dev/null; tmux refresh-client -S 2>/dev/null", tmuxName, ctrl.Cols, ctrl.Rows)).Run()
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
