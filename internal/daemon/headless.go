package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrTurnInProgress   = errors.New("a turn is already in progress for this session")
	ErrAgentUnsupported = errors.New("headless engine is currently supported for claude-code")
)

// ChatStreamEvent represents a real-time event pushed over SSE to chat clients
type ChatStreamEvent struct {
	SessionID  string    `json:"session_id"`
	TurnID     string    `json:"turn_id,omitempty"`
	Type       string    `json:"type"` // "turn_start", "text_delta", "thought_delta", "tool_start", "tool_result", "turn_complete", "turn_cancelled", "error"
	Text       string    `json:"text,omitempty"`
	Thinking   string    `json:"thinking,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolInput  any       `json:"tool_input,omitempty"`
	ToolOutput string    `json:"tool_output,omitempty"`
	IsError    bool      `json:"is_error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// StripBilledCredentials removes API keys and billed auth tokens from the environment,
// ensuring the child process uses the user's flat-rate subscription OAuth (e.g. ~/.claude.json).
func StripBilledCredentials(env []string) []string {
	billedVars := map[string]bool{
		"ANTHROPIC_API_KEY":    true,
		"ANTHROPIC_AUTH_TOKEN": true,
	}
	var clean []string
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) > 0 && billedVars[parts[0]] {
			continue
		}
		clean = append(clean, e)
	}
	return clean
}

// HeadlessRunner coordinates turn-by-turn headless execution of agent sessions
type HeadlessRunner struct {
	mu        sync.RWMutex
	processes map[string]*exec.Cmd
	subs      map[string][]chan ChatStreamEvent
	db        *DB
	broadcast func(s *Session)
}

func NewHeadlessRunner(db *DB, broadcast func(s *Session)) *HeadlessRunner {
	return &HeadlessRunner{
		processes: make(map[string]*exec.Cmd),
		subs:      make(map[string][]chan ChatStreamEvent),
		db:        db,
		broadcast: broadcast,
	}
}

// Subscribe opens a stream of ChatStreamEvents for a given session.
// The returned cleanup function must be called when the subscriber disconnects.
func (h *HeadlessRunner) Subscribe(sessionID string) (chan ChatStreamEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan ChatStreamEvent, 100)
	h.subs[sessionID] = append(h.subs[sessionID], ch)

	cleanup := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		subList := h.subs[sessionID]
		for i, c := range subList {
			if c == ch {
				h.subs[sessionID] = append(subList[:i], subList[i+1:]...)
				close(ch)
				break
			}
		}
	}

	return ch, cleanup
}

// Emit broadcasts an event to all active listeners for sessionID
func (h *HeadlessRunner) Emit(sessionID string, evt ChatStreamEvent) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	if evt.SessionID == "" {
		evt.SessionID = sessionID
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.subs[sessionID] {
		select {
		case ch <- evt:
		default:
			// Non-blocking drop if listener buffer is saturated
		}
	}
}

// IsRunning reports whether sessionID currently has an active turn running
func (h *HeadlessRunner) IsRunning(sessionID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.processes[sessionID]
	return ok
}

// CancelTurn sends SIGINT (and SIGKILL if needed) to interrupt the active turn process
func (h *HeadlessRunner) CancelTurn(sessionID string) error {
	h.mu.Lock()
	cmd, ok := h.processes[sessionID]
	h.mu.Unlock()

	if !ok || cmd == nil || cmd.Process == nil {
		return nil
	}

	_ = cmd.Process.Signal(syscall.SIGINT)

	go func() {
		time.Sleep(2 * time.Second)
		h.mu.RLock()
		activeCmd := h.processes[sessionID]
		h.mu.RUnlock()
		if activeCmd == cmd && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	h.Emit(sessionID, ChatStreamEvent{
		SessionID: sessionID,
		Type:      "turn_cancelled",
		Text:      "Turn was cancelled by user.",
		Timestamp: time.Now(),
	})

	return nil
}

// resolveClaudeBinary discovers the installed claude CLI executable
func resolveClaudeBinary() (string, error) {
	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}

	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".npm-global", "bin", "claude"),
		filepath.Join(home, "bin", "claude"),
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
		"/usr/bin/claude",
	}

	for _, c := range candidates {
		if stat, err := os.Stat(c); err == nil && !stat.IsDir() {
			return c, nil
		}
	}

	return "claude", nil
}

// RunTurn launches an asynchronous headless turn for the session.
func (h *HeadlessRunner) RunTurn(ctx context.Context, sess *Session, prompt string) error {
	if sess.Agent != "claude-code" {
		return ErrAgentUnsupported
	}

	h.mu.Lock()
	if _, running := h.processes[sess.ID]; running {
		h.mu.Unlock()
		return ErrTurnInProgress
	}

	bin, err := resolveClaudeBinary()
	if err != nil {
		h.mu.Unlock()
		return fmt.Errorf("failed to locate claude binary: %w", err)
	}

	if sess.NativeID == "" {
		sess.NativeID = generateUUID()
	}

	// Build arguments: if first turn vs follow-up turn
	var args []string
	home, _ := os.UserHomeDir()
	encodedCwd := encodeClaudeProjectDir(sess.Cwd)
	transcriptFile := filepath.Join(home, ".claude", "projects", encodedCwd, sess.NativeID+".jsonl")

	if fileExists(transcriptFile) {
		args = []string{"-p", prompt, "--output-format", "stream-json", "--verbose", "--resume", sess.NativeID}
	} else {
		args = []string{"-p", prompt, "--output-format", "stream-json", "--verbose", "--session-id", sess.NativeID}
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader("")
	if sess.Cwd != "" {
		cmd.Dir = sess.Cwd
	}

	// Clean environment enforcing Subscription OAuth protection
	cleanEnv := StripBilledCredentials(os.Environ())
	if sess.AccountID != "" {
		// If custom account directory is configured
		if acc, _ := h.db.GetAccount(sess.AccountID); acc != nil && acc.ConfigDir != "" {
			cfgDir := acc.ConfigDir
			if strings.HasPrefix(cfgDir, "~/") && home != "" {
				cfgDir = filepath.Join(home, cfgDir[2:])
			}
			cleanEnv = append(cleanEnv, "CLAUDE_CONFIG_DIR="+cfgDir)
		}
	}
	cmd.Env = cleanEnv

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		h.mu.Unlock()
		return fmt.Errorf("failed to pipe stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		h.mu.Unlock()
		return fmt.Errorf("failed to start headless process: %w", err)
	}

	h.processes[sess.ID] = cmd
	h.mu.Unlock()

	// Update session state to WORKING
	sess.State = StateWorking
	sess.Activity = "Thinking..."
	sess.LastEventAt = time.Now()
	if sess.FirstPrompt == "" {
		sess.FirstPrompt = prompt
	}
	sess.LastPrompt = prompt
	sess.PID = cmd.Process.Pid
	_ = h.db.SaveSession(sess)
	if h.broadcast != nil {
		h.broadcast(sess)
	}

	// Emit turn_start event
	h.Emit(sess.ID, ChatStreamEvent{
		SessionID: sess.ID,
		Type:      "turn_start",
		Text:      prompt,
		Timestamp: time.Now(),
	})

	// Process output stream asynchronously
	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.processes, sess.ID)
			h.mu.Unlock()
		}()

		scanner := bufio.NewScanner(stdoutPipe)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 10*1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				continue
			}

			evtType, _ := raw["type"].(string)
			switch evtType {
			case "content_block_delta":
				if delta, ok := raw["delta"].(map[string]interface{}); ok {
					deltaType, _ := delta["type"].(string)
					if deltaType == "text_delta" {
						text, _ := delta["text"].(string)
						h.Emit(sess.ID, ChatStreamEvent{
							SessionID: sess.ID,
							Type:      "text_delta",
							Text:      text,
						})
					} else if deltaType == "thinking_delta" {
						thinking, _ := delta["thinking"].(string)
						h.Emit(sess.ID, ChatStreamEvent{
							SessionID: sess.ID,
							Type:      "thought_delta",
							Thinking:  thinking,
						})
					}
				}

			case "content_block_start":
				if cb, ok := raw["content_block"].(map[string]interface{}); ok {
					cbType, _ := cb["type"].(string)
					if cbType == "tool_use" {
						toolName, _ := cb["name"].(string)
						toolInput := cb["input"]
						h.Emit(sess.ID, ChatStreamEvent{
							SessionID: sess.ID,
							Type:      "tool_start",
							ToolName:  toolName,
							ToolInput: toolInput,
						})
					}
				}

			case "tool_result":
				outputStr := ""
				if out, ok := raw["content"].(string); ok {
					outputStr = out
				} else if contentArr, ok := raw["content"].([]interface{}); ok {
					for _, part := range contentArr {
						if partMap, ok := part.(map[string]interface{}); ok {
							if txt, ok := partMap["text"].(string); ok {
								outputStr += txt
							}
						}
					}
				}
				h.Emit(sess.ID, ChatStreamEvent{
					SessionID:  sess.ID,
					Type:       "tool_result",
					ToolOutput: outputStr,
				})

			case "assistant":
				if msg, ok := raw["message"].(map[string]interface{}); ok {
					if contentStr, ok := msg["content"].(string); ok && contentStr != "" {
						h.Emit(sess.ID, ChatStreamEvent{
							SessionID: sess.ID,
							Type:      "text_delta",
							Text:      contentStr,
						})
					} else if contentArr, ok := msg["content"].([]interface{}); ok {
						for _, item := range contentArr {
							if itemMap, ok := item.(map[string]interface{}); ok {
								if itemMap["type"] == "text" {
									if txt, ok := itemMap["text"].(string); ok && txt != "" {
										h.Emit(sess.ID, ChatStreamEvent{
											SessionID: sess.ID,
											Type:      "text_delta",
											Text:      txt,
										})
									}
								}
							}
						}
					}
				}

			case "result":
				resText, _ := raw["result"].(string)
				isErr, _ := raw["is_error"].(bool)
				if isErr && resText != "" {
					h.Emit(sess.ID, ChatStreamEvent{
						SessionID: sess.ID,
						Type:      "error",
						Text:      resText,
						IsError:   true,
					})
				} else {
					h.Emit(sess.ID, ChatStreamEvent{
						SessionID: sess.ID,
						Type:      "turn_complete",
						Text:      resText,
					})
				}
			}
		}

		waitErr := cmd.Wait()

		// Reload session from DB to preserve title/flags updated during the turn
		currentSess, err := h.db.GetSession(sess.ID)
		if err == nil && currentSess != nil {
			sess = currentSess
		}

		sess.State = StateIdle
		sess.Activity = ""
		sess.LastEventAt = time.Now()
		sess.PID = 0
		if waitErr != nil {
			errDetail := strings.TrimSpace(stderrBuf.String())
			if errDetail != "" {
				sess.Activity = "Turn error: " + errDetail
				h.Emit(sess.ID, ChatStreamEvent{
					SessionID: sess.ID,
					Type:      "error",
					Text:      errDetail,
					IsError:   true,
				})
			} else {
				sess.Activity = "Turn completed with status: " + waitErr.Error()
			}
		}

		// Update context percentage if metadata readable
		if home != "" {
			meta := ReadClaudeSessionMeta(sess.Cwd, sess.NativeID)
			if meta != nil {
				sess.ContextPct = meta.ContextPct
				if meta.CustomTitle != "" {
					sess.CustomTitle = meta.CustomTitle
				}
			}
		}

		_ = h.db.SaveSession(sess)
		if h.broadcast != nil {
			h.broadcast(sess)
		}

		h.Emit(sess.ID, ChatStreamEvent{
			SessionID: sess.ID,
			Type:      "turn_complete",
			Timestamp: time.Now(),
		})
	}()

	return nil
}
