package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var reConvTitle = regexp.MustCompile(`\s*\(Conv\s*(\d+)\)$`)

type shellCappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *shellCappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len() >= c.limit {
		c.truncated = true
		return len(p), nil
	}
	remaining := c.limit - c.buf.Len()
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *shellCappedBuffer) String() string {
	return c.buf.String()
}

func normalizeAndValidateExtraDirs(cwd string, existing []string, reqExtra *[]string, addDirs []string, removeDirs []string, homeDir string) ([]string, error) {
	var base []string
	if reqExtra != nil {
		base = append([]string(nil), *reqExtra...)
	} else {
		base = append([]string(nil), existing...)
	}

	cleanPath := func(p string) (string, error) {
		p = strings.TrimSpace(p)
		if p == "" {
			return "", errors.New("empty directory path")
		}
		if strings.ContainsAny(p, "\x00\r\n") {
			return "", errors.New("directory path contains control characters")
		}
		if strings.HasPrefix(p, "-") {
			return "", errors.New("directory path starts with flag hyphen")
		}
		if strings.HasPrefix(p, "~/") && homeDir != "" {
			p = filepath.Join(homeDir, p[2:])
		} else if p == "~" && homeDir != "" {
			p = homeDir
		}
		if !filepath.IsAbs(p) {
			if cwd == "" {
				return "", errors.New("relative path cannot be resolved without cwd")
			}
			p = filepath.Join(cwd, p)
		}
		cleaned := filepath.Clean(p)
		if strings.HasPrefix(cleaned, "-") {
			return "", errors.New("resolved directory path starts with flag hyphen")
		}
		return cleaned, nil
	}

	removeSet := make(map[string]bool)
	for _, r := range removeDirs {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		removeSet[r] = true
		if cleaned, err := cleanPath(r); err == nil {
			removeSet[cleaned] = true
			if canonical, err := filepath.EvalSymlinks(cleaned); err == nil {
				removeSet[canonical] = true
			}
		}
	}

	var candidateList []string
	for _, b := range base {
		b = strings.TrimSpace(b)
		if b == "" || removeSet[b] {
			continue
		}
		cleaned, err := cleanPath(b)
		if err != nil || removeSet[cleaned] {
			continue
		}
		candidateList = append(candidateList, cleaned)
	}

	for _, a := range addDirs {
		a = strings.TrimSpace(a)
		if a == "" || removeSet[a] {
			continue
		}
		cleaned, err := cleanPath(a)
		if err != nil {
			return nil, err
		}
		if !removeSet[cleaned] {
			candidateList = append(candidateList, cleaned)
		}
	}

	seen := make(map[string]bool)
	var result []string
	for _, p := range candidateList {
		canonical := p
		if sym, err := filepath.EvalSymlinks(p); err == nil {
			canonical = sym
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			continue
		}
		if canonical == "/" || filepath.Clean(canonical) == "/" {
			return nil, errors.New("filesystem root cannot be added as extra directory")
		}
		if len(canonical) > 1024 {
			return nil, errors.New("directory path exceeds 1024 bytes")
		}
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		result = append(result, canonical)
		if len(result) > 16 {
			return nil, errors.New("extra directories exceed maximum cap of 16")
		}
	}

	if result == nil {
		result = []string{}
	}
	return result, nil
}

func (s *Server) rotateHeadlessConversation(ctx context.Context, sess *Session, doneActivity, customNewTitle string, markDone bool) (*Session, error) {
	if sess == nil {
		return nil, errors.New("nil session")
	}

	if markDone {
		_ = s.markSessionDoneAndCleanup(ctx, sess, doneActivity)
	}

	baseTitle := strings.TrimSpace(customNewTitle)
	if baseTitle == "" {
		if sess.CustomTitle != "" {
			baseTitle = sess.CustomTitle
		} else if sess.Name != "" {
			baseTitle = sess.Name
		} else if sess.Cwd != "" {
			baseTitle = filepath.Base(sess.Cwd)
		}
	}
	if baseTitle == "" || baseTitle == "/" || baseTitle == "." {
		baseTitle = sess.Agent
	}

	var newTurnName string
	if m := reConvTitle.FindStringSubmatch(baseTitle); len(m) == 2 {
		num, _ := strconv.Atoi(m[1])
		cleanBase := strings.TrimSpace(reConvTitle.ReplaceAllString(baseTitle, ""))
		newTurnName = fmt.Sprintf("%s (Conv %d)", cleanBase, num+1)
	} else {
		newTurnName = fmt.Sprintf("%s (Conv 2)", baseTitle)
	}

	newUUID := generateUUID()
	newSessID := fmt.Sprintf("%s:%s:%s", sess.Agent, sess.Host, newUUID)
	now := time.Now().UTC()

	var extraDirsCopy []string
	if len(sess.ExtraDirs) > 0 {
		extraDirsCopy = append([]string(nil), sess.ExtraDirs...)
	}

	newSess := &Session{
		ID:                newSessID,
		Agent:             sess.Agent,
		Host:              sess.Host,
		NativeID:          newUUID,
		Cwd:               sess.Cwd,
		ProjectKey:        sess.ProjectKey,
		Roots:             sess.Roots,
		NodePath:          sess.NodePath,
		Name:              newTurnName,
		CustomTitle:       newTurnName,
		Entrypoint:        sess.Entrypoint,
		Kind:              sess.Kind,
		Version:           sess.Version,
		GitBranch:         sess.GitBranch,
		AccountID:         sess.AccountID,
		EngineType:        EngineHeadless,
		Model:             sess.Model,
		Effort:            sess.Effort,
		PermissionMode:    sess.PermissionMode,
		ExtraDirs:         extraDirsCopy,
		IsSupervisor:      sess.IsSupervisor,
		CurrentTaskID:     sess.CurrentTaskID,
		Managed:           true,
		State:             StateIdle,
		Activity:          "Headless chat session ready",
		StartedAt:         now,
		LastEventAt:       now,
		MetadataUpdatedAt: now,
	}

	if s.db != nil {
		if err := s.db.SaveSession(newSess); err != nil {
			return nil, err
		}
		if sess.CurrentTaskID != "" {
			_ = s.db.ReassignWorkerSessionID(sess.ID, newSess.ID)
		}
	}

	s.broadcast(newSess)
	return newSess, nil
}

func (s *Server) handleSessionShell(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyReader := http.MaxBytesReader(w, r.Body, 32<<10)
	bodyBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		http.Error(w, "Request body exceeds 32 KB limit or is invalid", http.StatusBadRequest)
		return
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return r.URL.RequestURI()
	})
	if proxied || sess == nil {
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		http.Error(w, "Missing command", http.StatusBadRequest)
		return
	}

	cwdInfo, err := os.Stat(sess.Cwd)
	if err != nil || !cwdInfo.IsDir() {
		http.Error(w, "Invalid session working directory", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = sess.Cwd
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second

	if s.headless != nil {
		cmd.Env = s.headless.buildCommandEnv(sess)
	} else {
		cmd.Env = append(StripBilledCredentials(os.Environ()), "GIT_TERMINAL_PROMPT=0", "TERM=dumb")
	}

	stdoutBuf := &shellCappedBuffer{limit: 128 << 10}
	stderrBuf := &shellCappedBuffer{limit: 128 << 10}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if timedOut && exitCode == 0 {
		exitCode = 124
	}

	resp := map[string]interface{}{
		"command":     command,
		"cwd":         sess.Cwd,
		"stdout":      stdoutBuf.String(),
		"stderr":      stderrBuf.String(),
		"exit_code":   exitCode,
		"duration_ms": durationMs,
		"timed_out":   timedOut,
		"truncated":   stdoutBuf.truncated || stderrBuf.truncated,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSessionClear(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyReader := http.MaxBytesReader(w, r.Body, 4<<10)
	bodyBytes, _ := io.ReadAll(bodyReader)

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return r.URL.RequestURI()
	})
	if proxied || sess == nil {
		return
	}

	newSess, err := s.rotateHeadlessConversation(r.Context(), sess, "Cleared (context reset)", "", true)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to rotate conversation: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"old_session_id": sess.ID,
		"session":        newSess,
	})
}

func (s *Server) handleSessionCompact(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyReader := http.MaxBytesReader(w, r.Body, 16<<10)
	bodyBytes, _ := io.ReadAll(bodyReader)

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return r.URL.RequestURI()
	})
	if proxied || sess == nil {
		return
	}

	var req struct {
		Instructions string `json:"instructions"`
	}
	if len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, &req)
	}

	if s.headless != nil && s.headless.IsRunning(sess.ID) {
		http.Error(w, "Cannot compact while a turn is running", http.StatusConflict)
		return
	}

	if sess.Agent == "antigravity" {
		s.executeHeadlessCompaction(w, r, sess, req.Instructions)
		return
	}

	instructions := strings.TrimSpace(req.Instructions)
	instructions = strings.ReplaceAll(instructions, "\r", " ")
	instructions = strings.ReplaceAll(instructions, "\n", " ")
	instructions = strings.TrimSpace(instructions)

	cmdPrompt := "/compact"
	if instructions != "" {
		cmdPrompt = "/compact " + instructions
	}

	if s.headless != nil {
		sessCopy := *sess
		go func() {
			_ = s.headless.RunTurn(context.Background(), &sessCopy, cmdPrompt)
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"mode":       "native_stream",
		"session_id": sess.ID,
	})
}
