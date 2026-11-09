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

	"ackbar/internal/tmux"
)

var (
	ErrTurnInProgress   = errors.New("a turn is already in progress for this session")
	ErrAgentUnsupported = errors.New("headless engine is currently supported for claude-code and antigravity")
)

// ChatStreamEvent represents a real-time event pushed over SSE to chat clients
type ChatStreamEvent struct {
	SessionID   string             `json:"session_id"`
	TurnID      string             `json:"turn_id,omitempty"`
	Type        string             `json:"type"` // "turn_start", "text_delta", "thought_delta", "tool_start", "tool_result", "turn_complete", "turn_cancelled", "error", "queue_update", "interactive_block"
	Text        string             `json:"text,omitempty"`
	Thinking    string             `json:"thinking,omitempty"`
	ToolName    string             `json:"tool_name,omitempty"`
	ToolInput   any                `json:"tool_input,omitempty"`
	ToolOutput  string             `json:"tool_output,omitempty"`
	Blocked     *Blocked           `json:"blocked,omitempty"`
	IsError     bool               `json:"is_error,omitempty"`
	QueueItems  []*PromptQueueItem `json:"queue_items,omitempty"`
	QueuePaused bool               `json:"queue_paused,omitempty"`
	Subagents   []*SubagentInfo    `json:"subagents,omitempty"`
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
		"GEMINI_API_KEY":       true,
		"GOOGLE_API_KEY":       true,
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
	mu                     sync.RWMutex
	processes              map[string]*exec.Cmd
	activePrompts          map[string]TranscriptMessage
	subs                   map[string][]chan ChatStreamEvent
	queues                 map[string][]*PromptQueueItem // sessionID -> queued prompts
	paused                 map[string]bool               // sessionID -> is queue paused
	interactiveBlocks      map[string]*Blocked           // sessionID -> pending interactive Blocked struct
	respondedWhileStopping map[string]bool               // sessionID -> response arrived while stopping
	db                     *DB
	broadcast              func(s *Session)
	onTurnComplete         func(sessionID, nativeID string)
}

func NewHeadlessRunner(db *DB, broadcast func(s *Session)) *HeadlessRunner {
	return &HeadlessRunner{
		processes:              make(map[string]*exec.Cmd),
		activePrompts:          make(map[string]TranscriptMessage),
		subs:                   make(map[string][]chan ChatStreamEvent),
		queues:                 make(map[string][]*PromptQueueItem),
		paused:                 make(map[string]bool),
		interactiveBlocks:      make(map[string]*Blocked),
		respondedWhileStopping: make(map[string]bool),
		db:                     db,
		broadcast:              broadcast,
	}
}

func (h *HeadlessRunner) setInteractiveBlock(sessionID string, b *Blocked) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.interactiveBlocks[sessionID] = b
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		h.interactiveBlocks[parts[2]] = b
	}
}

func (h *HeadlessRunner) takeInteractiveBlock(sessionID string) *Blocked {
	h.mu.Lock()
	defer h.mu.Unlock()

	var b *Blocked
	if block, ok := h.interactiveBlocks[sessionID]; ok {
		b = block
		delete(h.interactiveBlocks, sessionID)
	}

	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		nativeID := parts[2]
		if block, ok := h.interactiveBlocks[nativeID]; ok {
			if b == nil {
				b = block
			}
			delete(h.interactiveBlocks, nativeID)
		}
		for k, block := range h.interactiveBlocks {
			if strings.HasSuffix(k, ":"+nativeID) {
				if b == nil {
					b = block
				}
				delete(h.interactiveBlocks, k)
			}
		}
	}
	return b
}

func (h *HeadlessRunner) stopProcessGraceful(sessionID string) {
	h.mu.RLock()
	cmd, ok := h.processes[sessionID]
	if !ok {
		parts := strings.Split(sessionID, ":")
		if len(parts) == 3 {
			nativeID := parts[2]
			for k, p := range h.processes {
				if k == nativeID || strings.HasSuffix(k, ":"+nativeID) {
					cmd = p
					break
				}
			}
		}
	}
	h.mu.RUnlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		go func(p *os.Process) {
			time.Sleep(500 * time.Millisecond)
			if p != nil && isProcessAlive(p.Pid) {
				_ = p.Kill()
			}
		}(cmd.Process)
	}
}

func (h *HeadlessRunner) triggerInteractiveBlock(sessionID string, sess *Session, toolName string, toolInput interface{}, blocked *Blocked, emitStreamEvt func(ChatStreamEvent)) {
	h.setInteractiveBlock(sessionID, blocked)
	if sess != nil && sess.ID != "" && sess.ID != sessionID {
		h.setInteractiveBlock(sess.ID, blocked)
	}

	// Immediately persist blocked state to DB and broadcast before process exits
	if sess != nil && h.db != nil {
		sess.State = StateBlocked
		sess.Blocked = blocked
		if blocked.Kind == BlockApproval {
			sess.Activity = "Waiting for plan approval"
		} else {
			sess.Activity = "Waiting for user response"
		}
		sess.LastStateChangeAt = time.Now()
		sess.LastEventAt = time.Now()
		sess.IsUnread = true
		_ = h.db.SaveSession(sess)
		if h.broadcast != nil {
			h.broadcast(sess)
		}
	}

	h.SetQueuePaused(sessionID, true)
	emitStreamEvt(ChatStreamEvent{
		SessionID: sessionID,
		Type:      "interactive_block",
		ToolName:  toolName,
		ToolInput: toolInput,
		Blocked:   blocked,
		Timestamp: time.Now(),
	})
	h.stopProcessGraceful(sessionID)
}

// ClearInteractiveBlockAndWait clears interactive blocks for a session and waits for any stopping
// headless turn to finish so that a newly launched turn does not collide with the previous process.
func (h *HeadlessRunner) ClearInteractiveBlockAndWait(sessionID string, timeout time.Duration) {
	h.mu.Lock()
	delete(h.interactiveBlocks, sessionID)
	delete(h.activePrompts, sessionID)
	h.respondedWhileStopping[sessionID] = true
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		nativeID := parts[2]
		delete(h.interactiveBlocks, nativeID)
		delete(h.activePrompts, nativeID)
		h.respondedWhileStopping[nativeID] = true
	}

	cmd := h.processes[sessionID]
	if cmd == nil && len(parts) == 3 {
		cmd = h.processes[parts[2]]
	}
	h.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	deadline := time.Now().Add(timeout)
	killAfter := time.Now().Add(200 * time.Millisecond)
	sigkillSent := false

	for time.Now().Before(deadline) {
		if !h.IsRunning(sessionID) {
			break
		}
		if !sigkillSent && time.Now().After(killAfter) {
			_ = cmd.Process.Kill()
			sigkillSent = true
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func parseInteractiveToolBlock(toolName string, toolInput interface{}) *Blocked {
	if toolName == "" {
		return nil
	}
	tLower := strings.ToLower(toolName)

	// 1. ExitPlanMode
	if tLower == "exitplanmode" || tLower == "exit_plan_mode" {
		planMarkdown := ""
		if toolInput != nil {
			switch v := toolInput.(type) {
			case string:
				var m map[string]interface{}
				if err := json.Unmarshal([]byte(v), &m); err == nil {
					if pStr, ok := m["plan"].(string); ok {
						planMarkdown = pStr
					}
				} else {
					planMarkdown = v
				}
			case map[string]interface{}:
				if pStr, ok := v["plan"].(string); ok {
					planMarkdown = pStr
				}
			}
		}
		return &Blocked{
			Kind:     BlockApproval,
			Reason:   planMarkdown,
			Question: "Approve implementation plan?",
			Options:  []string{"Approve Plan", "Request Changes"},
			Since:    time.Now(),
		}
	}

	// 2. AskUserQuestion / ask_question / etc.
	if toolName == "AskUserQuestion" || toolName == "ask_question" || toolName == "AskFollowupQuestion" ||
		strings.Contains(tLower, "question") || strings.Contains(tLower, "askuser") {
		var inputMap map[string]interface{}
		if toolInput != nil {
			switch v := toolInput.(type) {
			case string:
				_ = json.Unmarshal([]byte(v), &inputMap)
			case map[string]interface{}:
				inputMap = v
			}
		}

		questions := ExtractAntigravityQuestions(inputMap)
		questionText, optionsList := ExtractAntigravityQuestionAndOptions(inputMap)
		if questionText == "" && len(questions) > 0 {
			questionText = questions[0].Question
		}
		if len(optionsList) == 0 && len(questions) > 0 {
			optionsList = questions[0].Options
		}

		reason := "Waiting for user response"
		if questionText != "" {
			reason = questionText
		}

		return &Blocked{
			Kind:      BlockQuestion,
			Reason:    reason,
			Question:  questionText,
			Options:   optionsList,
			Questions: questions,
			Since:     time.Now(),
		}
	}

	return nil
}

// SetOnTurnComplete registers a callback invoked when a headless turn completes
func (h *HeadlessRunner) SetOnTurnComplete(fn func(sessionID, nativeID string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onTurnComplete = fn
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
		closed := false
		for key, subList := range h.subs {
			for i, c := range subList {
				if c == ch {
					h.subs[key] = append(subList[:i], subList[i+1:]...)
					if !closed {
						close(ch)
						closed = true
					}
					break
				}
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

// IsRunning reports whether sessionID currently has an active turn running.
// It verifies that tracked OS processes are genuinely alive, cleaning up dead processes.
func (h *HeadlessRunner) IsRunning(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	checkProcess := func(key string, cmd *exec.Cmd) bool {
		if cmd != nil && cmd.Process != nil && cmd.Process.Pid > 0 {
			if !isProcessAlive(cmd.Process.Pid) {
				delete(h.processes, key)
				delete(h.activePrompts, key)
				return false
			}
		}
		return true
	}

	if cmd, ok := h.processes[sessionID]; ok {
		if checkProcess(sessionID, cmd) {
			return true
		}
	}
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		nativeID := parts[2]
		for procKey, cmd := range h.processes {
			if procKey == nativeID || strings.HasSuffix(procKey, ":"+nativeID) {
				if checkProcess(procKey, cmd) {
					return true
				}
			}
		}
	}
	return false
}

func (h *HeadlessRunner) GetActiveTurnPrompt(sessionID string) (TranscriptMessage, bool) {
	if !h.IsRunning(sessionID) {
		return TranscriptMessage{}, false
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if msg, ok := h.activePrompts[sessionID]; ok {
		return msg, true
	}

	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		nativeID := parts[2]
		if msg, ok := h.activePrompts[nativeID]; ok {
			return msg, true
		}
		for k, msg := range h.activePrompts {
			if strings.HasSuffix(k, ":"+nativeID) {
				return msg, true
			}
		}
	} else if len(parts) == 1 && sessionID != "" {
		for k, msg := range h.activePrompts {
			if k == sessionID || strings.HasSuffix(k, ":"+sessionID) {
				return msg, true
			}
		}
	}

	return TranscriptMessage{}, false
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

// EmitSubagentsUpdate broadcasts current running subagents over SSE
func (h *HeadlessRunner) EmitSubagentsUpdate(sessionID string, subagents []*SubagentInfo) {
	h.Emit(sessionID, ChatStreamEvent{
		SessionID: sessionID,
		Type:      "subagents_update",
		Subagents: subagents,
		Timestamp: time.Now(),
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
		if ok {
			h.mu.Lock()
			delete(h.processes, activeKey)
			delete(h.activePrompts, activeKey)
			h.mu.Unlock()
		}
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

// resolveAntigravityBinary discovers the installed agy / antigravity CLI executable
func resolveAntigravityBinary() (string, error) {
	if path, err := exec.LookPath("agy"); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath("antigravity"); err == nil {
		return path, nil
	}

	home, _ := os.UserHomeDir()
	var candidates []string
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "agy"),
			filepath.Join(home, ".local", "bin", "antigravity"),
			filepath.Join(home, "bin", "agy"),
			filepath.Join(home, "bin", "antigravity"),
		)
	}
	candidates = append(candidates,
		"/opt/homebrew/bin/agy",
		"/opt/homebrew/bin/antigravity",
		"/usr/local/bin/agy",
		"/usr/local/bin/antigravity",
		"/usr/bin/agy",
		"/usr/bin/antigravity",
	)

	for _, c := range candidates {
		if stat, err := os.Stat(c); err == nil && !stat.IsDir() {
			return c, nil
		}
	}

	return "agy", nil
}

// hasAntigravityConversation checks if a conversation already exists on disk for nativeID
func hasAntigravityConversation(home, nativeID string) bool {
	if home == "" || nativeID == "" || filepath.Base(nativeID) != nativeID {
		return false
	}
	candidates := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "conversations", nativeID+".db"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID),
		filepath.Join(home, ".gemini", "antigravity", "brain", nativeID),
		filepath.Join(home, ".antigravity", "brain", nativeID),
	}
	for _, c := range candidates {
		if stat, err := os.Stat(c); err == nil {
			if stat.IsDir() || stat.Size() > 0 {
				return true
			}
		}
	}
	return false
}

// buildArgs constructs the binary path and CLI arguments for a headless turn.
func (h *HeadlessRunner) buildArgs(sess *Session, prompt, home string) (string, []string, error) {
	if sess.Agent == "antigravity" {
		bin, err := resolveAntigravityBinary()
		if err != nil {
			return "", nil, fmt.Errorf("failed to locate agy binary: %w", err)
		}

		effectivePrompt := prompt
		if sess.IsSupervisor && sess.FirstPrompt == "" && sess.LastPrompt == "" && !hasAntigravityConversation(home, sess.NativeID) {
			effectivePrompt = "[System Instruction: " + BuildSupervisorSystemPrompt(sess.ID) + "]\n\n" + prompt
		}

		args := []string{"-p", effectivePrompt, "--output-format", "stream-json"}
		if strings.EqualFold(sess.PermissionMode, "plan") {
			args = append(args, "--mode", "plan")
		} else {
			args = append(args, "--dangerously-skip-permissions")
		}
		if hasAntigravityConversation(home, sess.NativeID) || sess.FirstPrompt != "" || sess.LastPrompt != "" {
			args = append(args, "--conversation", sess.NativeID)
		}
		if sess.Model != "" && !strings.EqualFold(sess.Model, "default") {
			args = append(args, "--model", sess.Model)
		}
		return bin, args, nil
	}

	bin, err := resolveClaudeBinary()
	if err != nil {
		return "", nil, fmt.Errorf("failed to locate claude binary: %w", err)
	}

	claudeDir := filepath.Join(home, ".claude")
	if sess.AccountID != "" && h.db != nil {
		if acc, _ := h.db.GetAccount(sess.AccountID); acc != nil && acc.ConfigDir != "" {
			cfgDir := acc.ConfigDir
			if strings.HasPrefix(cfgDir, "~/") && home != "" {
				cfgDir = filepath.Join(home, cfgDir[2:])
			}
			claudeDir = cfgDir
		}
	}

	encodedCwd := encodeClaudeProjectDir(sess.Cwd)
	transcriptFile := filepath.Join(claudeDir, "projects", encodedCwd, sess.NativeID+".jsonl")

	hasTranscript := fileExists(transcriptFile)
	if !hasTranscript {
		projectsDir := filepath.Join(claudeDir, "projects")
		if entries, err := os.ReadDir(projectsDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					cand := filepath.Join(projectsDir, e.Name(), sess.NativeID+".jsonl")
					if fileExists(cand) {
						hasTranscript = true
						break
					}
				}
			}
		}
	}

	if !hasTranscript && fileExists(filepath.Join(claudeDir, "session-env", sess.NativeID)) {
		hasTranscript = true
	}

	var permFlags []string
	if strings.EqualFold(sess.PermissionMode, "plan") {
		permFlags = []string{"--permission-mode", "plan"}
	} else {
		permFlags = []string{"--dangerously-skip-permissions"}
	}

	// If transcript or session-env exists, or if a previous turn ran in this session, resume conversation
	var args []string
	if hasTranscript || sess.FirstPrompt != "" || sess.LastPrompt != "" {
		args = []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
		args = append(args, permFlags...)
		args = append(args, "--resume", sess.NativeID)
	} else {
		args = []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
		args = append(args, permFlags...)
		args = append(args, "--session-id", sess.NativeID)
	}

	if sess.Model != "" && !strings.EqualFold(sess.Model, "default") {
		args = append(args, "--model", sess.Model)
	}
	if sess.Effort != "" && !strings.EqualFold(sess.Effort, "default") && sess.Agent == "claude-code" {
		args = append(args, "--effort", sess.Effort)
	}

	if sess.IsSupervisor {
		args = append(args, "--append-system-prompt", BuildSupervisorSystemPrompt(sess.ID))
	}
	return bin, args, nil
}

// RunTurn launches an asynchronous headless turn for the session.
func (h *HeadlessRunner) RunTurn(ctx context.Context, sess *Session, prompt string) error {
	if sess.Agent != "claude-code" && sess.Agent != "antigravity" {
		return ErrAgentUnsupported
	}

	h.mu.Lock()
	if _, running := h.processes[sess.ID]; running {
		h.mu.Unlock()
		return ErrTurnInProgress
	}

	if sess.NativeID == "" {
		sess.NativeID = generateUUID()
	}

	home, _ := os.UserHomeDir()
	bin, args, err := h.buildArgs(sess, prompt, home)
	if err != nil {
		h.mu.Unlock()
		return err
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	devNull, devNullErr := os.Open(os.DevNull)
	if devNullErr == nil {
		cmd.Stdin = devNull
	} else {
		cmd.Stdin = strings.NewReader("")
	}
	if sess.Cwd != "" {
		cmd.Dir = sess.Cwd
	}

	// Clean environment enforcing Subscription OAuth protection
	cleanEnv := StripBilledCredentials(os.Environ())
	if sess.ID != "" {
		cleanEnv = append(cleanEnv, "ACKBAR_SESSION_ID="+sess.ID)
	}
	if sess.IsSupervisor {
		cleanEnv = append(cleanEnv, "ACKBAR_IS_SUPERVISOR=1")
	}
	if sess.AccountID != "" && h.db != nil {
		if acc, _ := h.db.GetAccount(sess.AccountID); acc != nil {
			cfgDir := acc.ConfigDir
			if cfgDir != "" {
				if strings.HasPrefix(cfgDir, "~/") && home != "" {
					cfgDir = filepath.Join(home, cfgDir[2:])
				}
				if sess.Agent == "claude-code" {
					cleanEnv = append(cleanEnv, "CLAUDE_CONFIG_DIR="+cfgDir)
				} else if sess.Agent == "antigravity" {
					cleanEnv = append(cleanEnv, "GEMINI_CLI_HOME="+cfgDir)
				}
			}
			for k, v := range acc.Env {
				cleanEnv = append(cleanEnv, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}
	cmd.Env = cleanEnv

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	cmd.WaitDelay = 4 * time.Second
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		if devNull != nil {
			_ = devNull.Close()
		}
		h.mu.Unlock()
		return fmt.Errorf("failed to pipe stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if devNull != nil {
			_ = devNull.Close()
		}
		h.mu.Unlock()
		return fmt.Errorf("failed to start headless process: %w", err)
	}
	if devNull != nil {
		_ = devNull.Close()
	}

	h.processes[sess.ID] = cmd
	h.activePrompts[sess.ID] = TranscriptMessage{
		Role:      "user",
		Content:   prompt,
		Timestamp: time.Now(),
	}
	h.mu.Unlock()

	// Update session state to WORKING
	sess.State = StateWorking
	sess.Blocked = nil
	sess.Activity = "Thinking..."
	sess.LastEventAt = time.Now()
	sess.PID = cmd.Process.Pid
	if sess.FirstPrompt == "" {
		sess.FirstPrompt = prompt
	}
	sess.LastPrompt = prompt
	if h.db != nil {
		_ = h.db.SaveSession(sess)
	}
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
	origSessionID := sess.ID
	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.processes, origSessionID)
			delete(h.processes, sess.ID)
			delete(h.activePrompts, origSessionID)
			delete(h.activePrompts, sess.ID)
			h.mu.Unlock()
		}()

		h.processStreamWithPrompt(origSessionID, stdoutPipe, prompt, sess)

		waitErr := cmd.Wait()

		h.mu.Lock()
		delete(h.processes, origSessionID)
		delete(h.processes, sess.ID)
		delete(h.activePrompts, origSessionID)
		delete(h.activePrompts, sess.ID)
		responded := h.respondedWhileStopping[sess.ID] || h.respondedWhileStopping[origSessionID]
		delete(h.respondedWhileStopping, sess.ID)
		delete(h.respondedWhileStopping, origSessionID)
		h.mu.Unlock()

		if responded {
			// User already responded to this interactive block and launched a new turn.
			// Do not overwrite session state in DB or emit conflicting events.
			return
		}

		// Reload session from DB to preserve title/flags updated during the turn
		if h.db != nil {
			currentSess, err := h.db.GetSession(sess.ID)
			if err == nil && currentSess != nil {
				if sess.OldID != "" && currentSess.OldID == "" {
					currentSess.OldID = sess.OldID
				}
				sess = currentSess
			}
		}

		interactiveBlock := h.takeInteractiveBlock(sess.ID)
		if interactiveBlock == nil && origSessionID != sess.ID {
			interactiveBlock = h.takeInteractiveBlock(origSessionID)
		}

		if interactiveBlock != nil {
			sess.State = StateBlocked
			sess.Blocked = interactiveBlock
			if interactiveBlock.Kind == BlockApproval {
				sess.Activity = "Waiting for plan approval"
			} else {
				sess.Activity = "Waiting for user response"
			}
			sess.PID = 0
			sess.IsUnread = true
			sess.LastStateChangeAt = time.Now()
			sess.LastEventAt = time.Now()
			if sess.FirstPrompt == "" {
				sess.FirstPrompt = prompt
			}
			sess.LastPrompt = prompt
		} else {
			sess.State = StateIdle
			sess.Blocked = nil
			sess.Activity = ""
			sess.LastEventAt = time.Now()
			sess.PID = 0
			if waitErr == nil {
				if sess.FirstPrompt == "" {
					sess.FirstPrompt = prompt
				}
				sess.LastPrompt = prompt
			} else {
				errDetail := strings.TrimSpace(stderrBuf.String())
				if errDetail != "" {
					sess.Activity = "Turn error: " + errDetail
					errEvt := ChatStreamEvent{
						SessionID: sess.ID,
						Type:      "error",
						Text:      errDetail,
						IsError:   true,
					}
					h.Emit(sess.ID, errEvt)
					if origSessionID != sess.ID {
						h.Emit(origSessionID, errEvt)
					}
				} else {
					sess.Activity = "Turn completed with status: " + waitErr.Error()
				}
				h.SetQueuePaused(sess.ID, true)
				h.EmitQueueUpdate(sess.ID)
			}
		}

		// Update title / context metadata
		if sess.Agent == "antigravity" {
			if sess.CustomTitle != "" {
				sess.Name = sess.CustomTitle
			} else if title := ReadAntigravitySessionTitle(sess.Cwd, sess.NativeID); title != "" && !isRawSessionName(title) {
				sess.Name = title
			} else if sess.Name == "" || isRawSessionName(sess.Name) {
				sess.Name = TruncateTitle(prompt)
			}
		} else if home != "" {
			meta := ReadClaudeSessionMeta(sess.Cwd, sess.NativeID)
			if meta != nil {
				sess.ContextPct = meta.ContextPct
				if meta.CustomTitle != "" {
					sess.CustomTitle = meta.CustomTitle
				}
			}
			if sess.Name == "" || isRawSessionName(sess.Name) {
				if sess.CustomTitle != "" {
					sess.Name = sess.CustomTitle
				} else {
					sess.Name = TruncateTitle(prompt)
				}
			}
		}

		if h.db != nil {
			_ = h.db.SaveSession(sess)
		}
		if h.broadcast != nil {
			h.broadcast(sess)
		}

		completeEvt := ChatStreamEvent{
			SessionID: sess.ID,
			Type:      "turn_complete",
			Timestamp: time.Now(),
		}
		h.Emit(sess.ID, completeEvt)
		if origSessionID != sess.ID {
			h.Emit(origSessionID, completeEvt)
		}

		h.mu.RLock()
		onDone := h.onTurnComplete
		h.mu.RUnlock()
		if onDone != nil {
			onDone(sess.ID, sess.NativeID)
		}

		// Auto-dispatch next queued prompt if queue is not paused and not blocked
		if interactiveBlock == nil && !h.IsQueuePaused(sess.ID) {
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

func (h *HeadlessRunner) processStream(sessionID string, r io.Reader, optionalSess ...*Session) {
	h.processStreamWithPrompt(sessionID, r, "", optionalSess...)
}

func (h *HeadlessRunner) handleAntigravityInit(sess *Session, convID, prompt string) {
	if sess == nil || convID == "" {
		return
	}
	changed := false
	if prompt != "" {
		if sess.FirstPrompt == "" {
			sess.FirstPrompt = prompt
			changed = true
		}
		if sess.LastPrompt != prompt {
			sess.LastPrompt = prompt
			changed = true
		}
	}
	if sess.NativeID != convID {
		oldID := sess.ID
		sess.NativeID = convID
		changed = true
		if sess.TmuxName != "" {
			newTmuxName := fmt.Sprintf("ackbar-%s-%s", sess.Agent, convID)
			if sess.TmuxName != newTmuxName {
				if err := tmux.Rename(context.Background(), sess.TmuxName, newTmuxName); err == nil {
					sess.TmuxName = newTmuxName
				}
			}
		}
		if sess.Agent != "" && sess.Host != "" {
			newID := fmt.Sprintf("%s:%s:%s", sess.Agent, sess.Host, convID)
			if oldID != "" && oldID != newID {
				sess.OldID = oldID
				sess.ID = newID
				if h.db != nil {
					_ = h.db.DeleteSession(oldID)
				}
				h.mu.Lock()
				if cmd, exists := h.processes[oldID]; exists {
					delete(h.processes, oldID)
					h.processes[newID] = cmd
				}
				if promptMsg, exists := h.activePrompts[oldID]; exists {
					delete(h.activePrompts, oldID)
					h.activePrompts[newID] = promptMsg
				}
				if oldQueue, exists := h.queues[oldID]; exists {
					for _, item := range oldQueue {
						if item != nil {
							item.SessionID = newID
						}
					}
					h.queues[newID] = append(oldQueue, h.queues[newID]...)
					delete(h.queues, oldID)
				}
				if oldPaused, exists := h.paused[oldID]; exists {
					if oldPaused {
						h.paused[newID] = true
					}
					delete(h.paused, oldID)
				}
				if oldSubs, exists := h.subs[oldID]; exists {
					h.subs[newID] = append(h.subs[newID], oldSubs...)
					delete(h.subs, oldID)
				}
				h.mu.Unlock()
			}
		}
	}
	if changed {
		if h.db != nil {
			_ = h.db.SaveSession(sess)
		}
		if h.broadcast != nil {
			h.broadcast(sess)
		}
	}
}

func (h *HeadlessRunner) processStreamWithPrompt(sessionID string, r io.Reader, prompt string, optionalSess ...*Session) {
	var sess *Session
	if len(optionalSess) > 0 {
		sess = optionalSess[0]
	}

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	toolUses := make(map[string]string)
	emittedToolStart := make(map[string]bool)
	hasEmittedAssistantText := false
	hasEmittedDeltaInTurn := false
	needsSeparation := false
	emitStreamEvt := func(evt ChatStreamEvent) {
		h.Emit(sessionID, evt)
		if sess != nil && sess.ID != "" && sess.ID != sessionID {
			evtCopy := evt
			evtCopy.SessionID = sess.ID
			h.Emit(sess.ID, evtCopy)
		}
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		// Check for Antigravity stream format: top-level "event" field
		if agyEvt, ok := raw["event"].(string); ok && agyEvt != "" {
			switch agyEvt {
			case "init":
				if convID, ok := raw["conversation_id"].(string); ok && convID != "" {
					h.handleAntigravityInit(sess, convID, prompt)
				}

			case "step_update":
				if su, ok := raw["step_update"].(map[string]interface{}); ok {
					stepType, _ := su["step_type"].(string)
					state, _ := su["state"].(string)

					switch stepType {
					case "agent_response":
						if textDelta, ok := su["text_delta"].(string); ok && textDelta != "" {
							emitStreamEvt(ChatStreamEvent{
								SessionID: sessionID,
								Type:      "text_delta",
								Text:      textDelta,
							})
						}
						if thoughtDelta, ok := su["thought_delta"].(string); ok && thoughtDelta != "" {
							emitStreamEvt(ChatStreamEvent{
								SessionID: sessionID,
								Type:      "thought_delta",
								Thinking:  thoughtDelta,
							})
						} else if thinking, ok := su["thinking"].(string); ok && thinking != "" {
							emitStreamEvt(ChatStreamEvent{
								SessionID: sessionID,
								Type:      "thought_delta",
								Thinking:  thinking,
							})
						}

					case "tool":
						toolName, _ := su["tool_name"].(string)
						toolInfo, _ := su["tool_info"].(map[string]interface{})

						if state == "ACTIVE" {
							var toolInput any
							if toolInfo != nil {
								toolInput = toolInfo["parameters"]
							}
							emitStreamEvt(ChatStreamEvent{
								SessionID: sessionID,
								Type:      "tool_start",
								ToolName:  toolName,
								ToolInput: toolInput,
							})

							if blocked := parseInteractiveToolBlock(toolName, toolInput); blocked != nil {
								h.triggerInteractiveBlock(sessionID, sess, toolName, toolInput, blocked, emitStreamEvt)
							}
						} else if state == "DONE" || state == "ERROR" {
							toolOutput := ""
							if toolInfo != nil {
								if out, ok := toolInfo["output"].(string); ok {
									toolOutput = out
								}
							}
							emitStreamEvt(ChatStreamEvent{
								SessionID:  sessionID,
								Type:       "tool_result",
								ToolName:   toolName,
								ToolOutput: toolOutput,
								IsError:    state == "ERROR",
							})
						}

					case "subagent":
						role, _ := su["role"].(string)
						taskName, _ := su["task_name"].(string)
						subagentName := role
						if subagentName == "" {
							subagentName = taskName
						}
						if subagentName == "" {
							subagentName = "subagent"
						}

						if state == "ACTIVE" {
							emitStreamEvt(ChatStreamEvent{
								SessionID: sessionID,
								Type:      "tool_start",
								ToolName:  "Task: " + subagentName,
								ToolInput: su["description"],
							})
						} else if state == "DONE" || state == "ERROR" {
							emitStreamEvt(ChatStreamEvent{
								SessionID:  sessionID,
								Type:       "tool_result",
								ToolName:   "Task: " + subagentName,
								ToolOutput: fmt.Sprintf("Subagent completed (%s)", state),
								IsError:    state == "ERROR",
							})
						}

					case "error_message":
						if errMsg, ok := su["error"].(string); ok && errMsg != "" {
							emitStreamEvt(ChatStreamEvent{
								SessionID: sessionID,
								Type:      "error",
								Text:      errMsg,
								IsError:   true,
							})
						}
					}
				}

			case "result":
				if res, ok := raw["result"].(map[string]interface{}); ok {
					status, _ := res["status"].(string)
					respText, _ := res["response"].(string)
					if status == "ERROR" {
						errText, _ := res["error"].(string)
						if errText == "" {
							errText = respText
						}
						emitStreamEvt(ChatStreamEvent{
							SessionID: sessionID,
							Type:      "error",
							Text:      errText,
							IsError:   true,
						})
					} else {
						emitStreamEvt(ChatStreamEvent{
							SessionID: sessionID,
							Type:      "turn_complete",
							Text:      respText,
						})
					}
				}
			}
			continue
		}

		evtType, _ := raw["type"].(string)
		switch evtType {
		case "init":
			if convID, ok := raw["conversation_id"].(string); ok && convID != "" {
				h.handleAntigravityInit(sess, convID, prompt)
			} else if sess != nil && prompt != "" {
				if sess.FirstPrompt == "" {
					sess.FirstPrompt = prompt
				}
				sess.LastPrompt = prompt
				if h.db != nil {
					_ = h.db.SaveSession(sess)
				}
			}

		case "system":
			if subtype, _ := raw["subtype"].(string); subtype == "init" {
				if sess != nil && prompt != "" {
					if sess.FirstPrompt == "" {
						sess.FirstPrompt = prompt
					}
					sess.LastPrompt = prompt
					if h.db != nil {
						_ = h.db.SaveSession(sess)
					}
				}
			}

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
					hasEmittedDeltaInTurn = true
					emitStreamEvt(ChatStreamEvent{
						SessionID: sessionID,
						Type:      "text_delta",
						Text:      text,
					})
				} else if deltaType == "thinking_delta" {
					thinking, _ := delta["thinking"].(string)
					hasEmittedDeltaInTurn = true
					emitStreamEvt(ChatStreamEvent{
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
					toolID, _ := cb["id"].(string)
					if toolID != "" {
						emittedToolStart[toolID] = true
						if toolName != "" {
							toolUses[toolID] = toolName
						}
					}
					toolInput := cb["input"]
					emitStreamEvt(ChatStreamEvent{
						SessionID: sessionID,
						Type:      "tool_start",
						ToolName:  toolName,
						ToolInput: toolInput,
					})
					if toolInput != nil {
						if blocked := parseInteractiveToolBlock(toolName, toolInput); blocked != nil {
							h.triggerInteractiveBlock(sessionID, sess, toolName, toolInput, blocked, emitStreamEvt)
						}
					}
				} else if cbType == "text" && hasEmittedAssistantText {
					needsSeparation = true
				}
			}

		case "tool_result":
			needsSeparation = true
			hasEmittedDeltaInTurn = false
			outputStr := ""
			toolID, _ := raw["tool_use_id"].(string)
			toolName := ""
			if toolID != "" {
				toolName = toolUses[toolID]
			}
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
			isErr, _ := raw["is_error"].(bool)
			emitStreamEvt(ChatStreamEvent{
				SessionID:  sessionID,
				Type:       "tool_result",
				ToolName:   toolName,
				ToolOutput: outputStr,
				IsError:    isErr,
			})

		case "user":
			hasEmittedDeltaInTurn = false
			if msg, ok := raw["message"].(map[string]interface{}); ok {
				if contentArr, ok := msg["content"].([]interface{}); ok {
					for _, item := range contentArr {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if itemMap["type"] == "tool_result" {
								needsSeparation = true
								toolID, _ := itemMap["tool_use_id"].(string)
								toolName := ""
								if toolID != "" {
									toolName = toolUses[toolID]
								}
								outputStr := ""
								if out, ok := itemMap["content"].(string); ok {
									outputStr = out
								} else if partArr, ok := itemMap["content"].([]interface{}); ok {
									for _, part := range partArr {
										if partMap, ok := part.(map[string]interface{}); ok {
											if txt, ok := partMap["text"].(string); ok {
												outputStr += txt
											}
										}
									}
								}
								isErr, _ := itemMap["is_error"].(bool)
								emitStreamEvt(ChatStreamEvent{
									SessionID:  sessionID,
									Type:       "tool_result",
									ToolName:   toolName,
									ToolOutput: outputStr,
									IsError:    isErr,
								})
							}
						}
					}
				}
			}

		case "assistant":
			if msg, ok := raw["message"].(map[string]interface{}); ok {
				var textBlocks []string
				flushTextBlocks := func() {
					if len(textBlocks) > 0 && !hasEmittedDeltaInTurn {
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
						emitStreamEvt(ChatStreamEvent{
							SessionID: sessionID,
							Type:      "text_delta",
							Text:      joined,
						})
					}
					textBlocks = nil
				}
				if contentStr, ok := msg["content"].(string); ok && contentStr != "" {
					textBlocks = append(textBlocks, contentStr)
				} else if contentArr, ok := msg["content"].([]interface{}); ok {
					for _, item := range contentArr {
						if itemMap, ok := item.(map[string]interface{}); ok {
							switch itemMap["type"] {
							case "thinking":
								if !hasEmittedDeltaInTurn {
									if thinking, ok := itemMap["thinking"].(string); ok && thinking != "" {
										emitStreamEvt(ChatStreamEvent{
											SessionID: sessionID,
											Type:      "thought_delta",
											Thinking:  thinking,
										})
									}
								}
							case "text":
								if txt, ok := itemMap["text"].(string); ok && txt != "" {
									textBlocks = append(textBlocks, txt)
								}
							case "tool_use":
								flushTextBlocks()
								needsSeparation = true
								toolName, _ := itemMap["name"].(string)
								toolID, _ := itemMap["id"].(string)
								if toolID != "" && toolName != "" {
									toolUses[toolID] = toolName
								}
								toolInput := itemMap["input"]
								if toolID == "" || !emittedToolStart[toolID] {
									if toolID != "" {
										emittedToolStart[toolID] = true
									}
									emitStreamEvt(ChatStreamEvent{
										SessionID: sessionID,
										Type:      "tool_start",
										ToolName:  toolName,
										ToolInput: toolInput,
									})
								}
								if blocked := parseInteractiveToolBlock(toolName, toolInput); blocked != nil {
									h.triggerInteractiveBlock(sessionID, sess, toolName, toolInput, blocked, emitStreamEvt)
								}
							}
						}
					}
				}
				flushTextBlocks()
			}

		case "result":
			resText, _ := raw["result"].(string)
			isErr, _ := raw["is_error"].(bool)
			if isErr && resText != "" {
				emitStreamEvt(ChatStreamEvent{
					SessionID: sessionID,
					Type:      "error",
					Text:      resText,
					IsError:   true,
				})
			} else {
				emitStreamEvt(ChatStreamEvent{
					SessionID: sessionID,
					Type:      "turn_complete",
					Text:      resText,
				})
			}
		}
	}
}
