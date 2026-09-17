package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	SessionID   string             `json:"session_id"`
	TurnID      string             `json:"turn_id,omitempty"`
	Type        string             `json:"type"` // "turn_start", "text_delta", "thought_delta", "tool_start", "tool_result", "turn_complete", "turn_cancelled", "error", "queue_update"
	Text        string             `json:"text,omitempty"`
	Thinking    string             `json:"thinking,omitempty"`
	ToolName    string             `json:"tool_name,omitempty"`
	ToolInput   any                `json:"tool_input,omitempty"`
	ToolOutput  string             `json:"tool_output,omitempty"`
	IsError     bool               `json:"is_error,omitempty"`
	QueueItems  []*PromptQueueItem `json:"queue_items,omitempty"`
	QueuePaused bool               `json:"queue_paused,omitempty"`
	Timestamp   time.Time          `json:"timestamp"`
}

// PromptQueueItem represents a queued prompt held in daemon memory
type PromptQueueItem struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
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
	queues    map[string][]*PromptQueueItem // sessionID -> queued prompts
	paused    map[string]bool               // sessionID -> is queue paused
	db        *DB
	broadcast func(s *Session)
}

func NewHeadlessRunner(db *DB, broadcast func(s *Session)) *HeadlessRunner {
	return &HeadlessRunner{
		processes: make(map[string]*exec.Cmd),
		subs:      make(map[string][]chan ChatStreamEvent),
		queues:    make(map[string][]*PromptQueueItem),
		paused:    make(map[string]bool),
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

	broadcastTo := func(targetID string) {
		for _, ch := range h.subs[targetID] {
			select {
			case ch <- evt:
			default:
				// Non-blocking drop if listener buffer is saturated
			}
		}
	}

	broadcastTo(sessionID)

	// Also broadcast to any listeners subscribed by native UUID or alternate host prefix
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		nativeID := parts[2]
		if nativeID != sessionID {
			broadcastTo(nativeID)
		}
		localID := fmt.Sprintf("%s:local:%s", parts[0], parts[2])
		if localID != sessionID {
			broadcastTo(localID)
		}
	}
}

// IsRunning reports whether sessionID currently has an active turn running
func (h *HeadlessRunner) IsRunning(sessionID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, ok := h.processes[sessionID]; ok {
		return true
	}
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		nativeID := parts[2]
		for procKey := range h.processes {
			if procKey == nativeID || strings.HasSuffix(procKey, ":"+nativeID) {
				return true
			}
		}
	}
	return false
}

func (h *HeadlessRunner) resolveQueueKey(sessionID string) string {
	if _, ok := h.queues[sessionID]; ok {
		return sessionID
	}
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		nativeID := parts[2]
		for k := range h.queues {
			if k == nativeID || strings.HasSuffix(k, ":"+nativeID) {
				return k
			}
		}
	}
	return sessionID
}

// EnqueuePrompt adds a prompt to the session's in-memory FIFO queue
func (h *HeadlessRunner) EnqueuePrompt(sessionID string, text string) *PromptQueueItem {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := h.resolveQueueKey(sessionID)
	item := &PromptQueueItem{
		ID:        fmt.Sprintf("q_%d_%s", time.Now().UnixNano(), generateUUID()[:6]),
		SessionID: key,
		Text:      text,
		CreatedAt: time.Now(),
	}
	h.queues[key] = append(h.queues[key], item)
	return item
}

// GetPromptQueue returns a snapshot of queued prompts and pause state for a session
func (h *HeadlessRunner) GetPromptQueue(sessionID string) ([]*PromptQueueItem, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	key := h.resolveQueueKey(sessionID)
	items := h.queues[key]
	paused := h.paused[key]

	res := make([]*PromptQueueItem, len(items))
	copy(res, items)
	return res, paused
}

// DequeuePrompt pops the next prompt from the session queue, or nil if empty
func (h *HeadlessRunner) DequeuePrompt(sessionID string) *PromptQueueItem {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := h.resolveQueueKey(sessionID)
	items := h.queues[key]
	if len(items) == 0 {
		return nil
	}
	next := items[0]
	h.queues[key] = items[1:]
	return next
}

// DeletePromptQueueItem removes a specific prompt from the queue by ID
func (h *HeadlessRunner) DeletePromptQueueItem(sessionID string, itemID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := h.resolveQueueKey(sessionID)
	items := h.queues[key]
	found := false
	var updated []*PromptQueueItem
	for _, it := range items {
		if it.ID == itemID {
			found = true
			continue
		}
		updated = append(updated, it)
	}
	if found {
		h.queues[key] = updated
	}
	return found
}

// ClearPromptQueue removes all queued prompts and resets pause state
func (h *HeadlessRunner) ClearPromptQueue(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := h.resolveQueueKey(sessionID)
	delete(h.queues, key)
	delete(h.paused, key)
}

// SetQueuePaused updates the pause state of a session's prompt queue
func (h *HeadlessRunner) SetQueuePaused(sessionID string, paused bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := h.resolveQueueKey(sessionID)
	h.paused[key] = paused
}

// IsQueuePaused checks whether prompt queue processing is paused for a session
func (h *HeadlessRunner) IsQueuePaused(sessionID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	key := h.resolveQueueKey(sessionID)
	return h.paused[key]
}

// EmitQueueUpdate broadcasts current queue items and pause status over SSE
func (h *HeadlessRunner) EmitQueueUpdate(sessionID string) {
	items, paused := h.GetPromptQueue(sessionID)
	h.Emit(sessionID, ChatStreamEvent{
		SessionID:   sessionID,
		Type:        "queue_update",
		QueueItems:  items,
		QueuePaused: paused,
		Timestamp:   time.Now(),
	})
}

// CancelTurn sends SIGINT (and SIGKILL if needed) to interrupt the active turn process
func (h *HeadlessRunner) CancelTurn(sessionID string) error {
	h.mu.Lock()
	cmd, ok := h.processes[sessionID]
	activeKey := sessionID
	if !ok {
		parts := strings.Split(sessionID, ":")
		if len(parts) == 3 {
			nativeID := parts[2]
			for procKey, procCmd := range h.processes {
				if procKey == nativeID || strings.HasSuffix(procKey, ":"+nativeID) {
					cmd = procCmd
					activeKey = procKey
					ok = true
					break
				}
			}
		}
	}
	h.mu.Unlock()

	if !ok || cmd == nil || cmd.Process == nil {
		return nil
	}

	_ = cmd.Process.Signal(syscall.SIGINT)

	go func() {
		time.Sleep(2 * time.Second)
		h.mu.RLock()
		activeCmd := h.processes[activeKey]
		h.mu.RUnlock()
		if activeCmd == cmd && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	h.SetQueuePaused(sessionID, true)
	h.EmitQueueUpdate(sessionID)

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

		h.processStream(sess.ID, stdoutPipe)

		waitErr := cmd.Wait()

		h.mu.Lock()
		delete(h.processes, sess.ID)
		h.mu.Unlock()

		// Reload session from DB to preserve title/flags updated during the turn
		if h.db != nil {
			currentSess, err := h.db.GetSession(sess.ID)
			if err == nil && currentSess != nil {
				sess = currentSess
			}
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
			h.SetQueuePaused(sess.ID, true)
			h.EmitQueueUpdate(sess.ID)
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

		if h.db != nil {
			_ = h.db.SaveSession(sess)
		}
		if h.broadcast != nil {
			h.broadcast(sess)
		}

		h.Emit(sess.ID, ChatStreamEvent{
			SessionID: sess.ID,
			Type:      "turn_complete",
			Timestamp: time.Now(),
		})

		// Auto-dispatch next queued prompt if queue is not paused
		if !h.IsQueuePaused(sess.ID) {
			nextItem := h.DequeuePrompt(sess.ID)
			if nextItem != nil {
				h.EmitQueueUpdate(sess.ID)
				go func(nextPrompt string) {
					time.Sleep(50 * time.Millisecond)
					var targetSess *Session = sess
					if h.db != nil {
						if currentSess, err := h.db.GetSession(sess.ID); err == nil && currentSess != nil {
							targetSess = currentSess
						}
					}
					_ = h.RunTurn(context.Background(), targetSess, nextPrompt)
				}(nextItem.Text)
			}
		}
	}()

	return nil
}

func (h *HeadlessRunner) processStream(sessionID string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	hasEmittedAssistantText := false
	needsSeparation := false
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
					if needsSeparation && hasEmittedAssistantText {
						if !strings.HasPrefix(text, "\n") {
							text = "\n\n" + text
						} else if !strings.HasPrefix(text, "\n\n") {
							text = "\n" + text
						}
						needsSeparation = false
					}
					hasEmittedAssistantText = true
					h.Emit(sessionID, ChatStreamEvent{
						SessionID: sessionID,
						Type:      "text_delta",
						Text:      text,
					})
				} else if deltaType == "thinking_delta" {
					thinking, _ := delta["thinking"].(string)
					h.Emit(sessionID, ChatStreamEvent{
						SessionID: sessionID,
						Type:      "thought_delta",
						Thinking:  thinking,
					})
				}
			}

		case "content_block_start":
			if cb, ok := raw["content_block"].(map[string]interface{}); ok {
				cbType, _ := cb["type"].(string)
				if cbType == "tool_use" {
					needsSeparation = true
					toolName, _ := cb["name"].(string)
					toolInput := cb["input"]
					h.Emit(sessionID, ChatStreamEvent{
						SessionID: sessionID,
						Type:      "tool_start",
						ToolName:  toolName,
						ToolInput: toolInput,
					})
				} else if cbType == "text" && hasEmittedAssistantText {
					needsSeparation = true
				}
			}

		case "tool_result":
			needsSeparation = true
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
			h.Emit(sessionID, ChatStreamEvent{
				SessionID:  sessionID,
				Type:       "tool_result",
				ToolOutput: outputStr,
			})

		case "assistant":
			if msg, ok := raw["message"].(map[string]interface{}); ok {
				var textBlocks []string
				if contentStr, ok := msg["content"].(string); ok && contentStr != "" {
					textBlocks = append(textBlocks, contentStr)
				} else if contentArr, ok := msg["content"].([]interface{}); ok {
					for _, item := range contentArr {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if itemMap["type"] == "text" {
								if txt, ok := itemMap["text"].(string); ok && txt != "" {
									textBlocks = append(textBlocks, txt)
								}
							}
						}
					}
				}
				if len(textBlocks) > 0 {
					joined := strings.Join(textBlocks, "\n\n")
					if hasEmittedAssistantText || needsSeparation {
						if !strings.HasPrefix(joined, "\n") {
							joined = "\n\n" + joined
						} else if !strings.HasPrefix(joined, "\n\n") {
							joined = "\n" + joined
						}
					}
					hasEmittedAssistantText = true
					needsSeparation = false
					h.Emit(sessionID, ChatStreamEvent{
						SessionID: sessionID,
						Type:      "text_delta",
						Text:      joined,
					})
				}
			}

		case "result":
			resText, _ := raw["result"].(string)
			isErr, _ := raw["is_error"].(bool)
			if isErr && resText != "" {
				h.Emit(sessionID, ChatStreamEvent{
					SessionID: sessionID,
					Type:      "error",
					Text:      resText,
					IsError:   true,
				})
			} else {
				h.Emit(sessionID, ChatStreamEvent{
					SessionID: sessionID,
					Type:      "turn_complete",
					Text:      resText,
				})
			}
		}
	}
}
