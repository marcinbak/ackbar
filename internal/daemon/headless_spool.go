package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TurnSpoolMeta persists worker sidecar metadata at 0600 without environment variables or secrets.
type TurnSpoolMeta struct {
	TurnID            string    `json:"turn_id"`
	SessionID         string    `json:"session_id"`
	OrigSessionID     string    `json:"orig_session_id,omitempty"`
	NativeID          string    `json:"native_id"`
	Agent             string    `json:"agent"`
	Cwd               string    `json:"cwd,omitempty"`
	Prompt            string    `json:"prompt"`
	Bin               string    `json:"bin"`
	Args              []string  `json:"args,omitempty"`
	SocketPath        string    `json:"socket_path"`
	SocketTempDir     string    `json:"socket_temp_dir,omitempty"`
	SpoolPath         string    `json:"spool_path"`
	MetaPath          string    `json:"meta_path,omitempty"`
	WorkerPID         int       `json:"worker_pid,omitempty"`
	ChildPID          int       `json:"child_pid,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at,omitempty"`
	Status            string    `json:"status"`
	ExitCode          int       `json:"exit_code"`
	StderrTail        string    `json:"stderr_tail,omitempty"`
	FinalizedByDaemon bool      `json:"finalized_by_daemon,omitempty"`
}

type InFlightTurnSnapshot struct {
	TurnID         string
	SessionID      string
	NativeID       string
	UserPrompt     string
	UserTimestamp  time.Time
	AssistantText  string
	ThinkingText   string
	ToolCalls      []string
	ResultText     string
	LastTurnText   string
	Blocked        *Blocked
	StreamHadError bool
	WasCancelled   bool
	LastSeq        int64
	IsActive       bool
	FinishedAt     time.Time
	UpdatedAt      time.Time
}

func DefaultHeadlessRunDir(dbPath string) string {
	trimmed := strings.TrimSpace(dbPath)
	if trimmed == "" || trimmed == ":memory:" || strings.HasPrefix(trimmed, "file:") {
		return ""
	}
	clean := filepath.Clean(trimmed)
	dir := filepath.Dir(clean)
	// Relative working-directory test DBs use an ephemeral temp run dir to avoid polluting the repo.
	if dir == "." {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if clean == filepath.Join(home, ".config", "ackbar", "ackbard.db") || clean == filepath.Join(home, ".ackbard.db") {
			return filepath.Join(home, ".config", "ackbar", "run")
		}
	}
	return filepath.Join(dir, "run")
}

func (h *HeadlessRunner) resolveRunDir(create bool) (string, error) {
	var runDir string
	if h.db != nil {
		runDir = DefaultHeadlessRunDir(h.db.Path())
	}
	if runDir == "" {
		h.mu.Lock()
		if h.fallbackRunDir == "" && create {
			td, err := os.MkdirTemp("", "ackbar-headless-run-*")
			if err != nil {
				h.mu.Unlock()
				return "", err
			}
			_ = os.Chmod(td, 0700)
			h.fallbackRunDir = td
		}
		runDir = h.fallbackRunDir
		h.mu.Unlock()
	}
	if runDir == "" {
		return "", nil
	}
	if create {
		if err := os.MkdirAll(runDir, 0700); err != nil {
			return "", err
		}
		return runDir, os.Chmod(runDir, 0700)
	}
	if _, err := os.Stat(runDir); err != nil {
		return "", nil
	}
	return runDir, nil
}

// allocateWorkerSocketPath falls back to an unpredictable 0700 temp directory when sun_path would exceed 96 bytes.
func allocateWorkerSocketPath(runDir, turnID string) (string, string, error) {
	cand := filepath.Join(runDir, turnID+".sock")
	if len(cand) <= 96 {
		return cand, "", nil
	}
	td, err := os.MkdirTemp("", "akw-*")
	if err != nil {
		return "", "", err
	}
	if err := os.Chmod(td, 0700); err != nil {
		_ = os.RemoveAll(td)
		return "", "", err
	}
	return filepath.Join(td, "w.sock"), td, nil
}

func cleanupTurnSocketArtifacts(meta *TurnSpoolMeta) {
	if meta == nil {
		return
	}
	if meta.SocketPath != "" {
		_ = os.Remove(meta.SocketPath)
	}
	if meta.SocketTempDir != "" {
		_ = os.RemoveAll(meta.SocketTempDir)
	}
}

func removeTurnSpoolFiles(metaPath string, meta *TurnSpoolMeta) {
	cleanupTurnSocketArtifacts(meta)
	if meta != nil {
		if meta.SpoolPath != "" {
			_ = os.Remove(meta.SpoolPath)
		}
		if meta.MetaPath != "" {
			_ = os.Remove(meta.MetaPath)
		}
	}
	if metaPath != "" {
		_ = os.Remove(metaPath)
	}
}

func WriteTurnSpoolMeta(metaPath string, meta *TurnSpoolMeta) error {
	if metaPath == "" || meta == nil {
		return fmt.Errorf("invalid meta path or nil meta")
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(metaPath), ".meta-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, metaPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Chmod(metaPath, 0600)
}

func ReadTurnSpoolMeta(metaPath string) (*TurnSpoolMeta, error) {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var meta TurnSpoolMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.MetaPath == "" {
		meta.MetaPath = metaPath
	}
	return &meta, nil
}

type TurnSpoolWriter struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	turnID string
	seq    int64
	closed bool
	subs   map[chan ChatStreamEvent]struct{}
}

func OpenTurnSpoolWriter(spoolPath, turnID string) (*TurnSpoolWriter, error) {
	_, maxSeq, _ := ReadTurnSpoolAfter(spoolPath, 0)
	f, err := os.OpenFile(spoolPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Chmod(0600)
	return &TurnSpoolWriter{file: f, path: spoolPath, turnID: turnID, seq: maxSeq, subs: make(map[chan ChatStreamEvent]struct{})}, nil
}

func (w *TurnSpoolWriter) Append(evt ChatStreamEvent) (ChatStreamEvent, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return evt, os.ErrClosed
	}
	w.seq++
	evt.Seq = w.seq
	if evt.TurnID == "" {
		evt.TurnID = w.turnID
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	line, err := json.Marshal(evt)
	if err != nil {
		return evt, err
	}
	if _, err := w.file.Write(append(line, '\n')); err != nil {
		return evt, err
	}
	for ch := range w.subs {
		select {
		case ch <- evt:
		default:
		}
	}
	return evt, nil
}

// SubscribeFrom atomically reads sequenced events after afterSeq and registers a live channel under the writer lock.
func (w *TurnSpoolWriter) SubscribeFrom(afterSeq int64) ([]ChatStreamEvent, chan ChatStreamEvent, func(), error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	events, _, err := ReadTurnSpoolAfter(w.path, afterSeq)
	if err != nil {
		return nil, nil, func() {}, err
	}
	ch := make(chan ChatStreamEvent, 2048)
	if w.closed {
		close(ch)
		return events, ch, func() {}, nil
	}
	w.subs[ch] = struct{}{}
	return events, ch, func() {
		w.mu.Lock()
		if _, ok := w.subs[ch]; ok {
			delete(w.subs, ch)
			close(ch)
		}
		w.mu.Unlock()
	}, nil
}

func (w *TurnSpoolWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	for ch := range w.subs {
		delete(w.subs, ch)
		close(ch)
	}
	return w.file.Close()
}

func ReadTurnSpoolAfter(spoolPath string, afterSeq int64) ([]ChatStreamEvent, int64, error) {
	f, err := os.Open(spoolPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var out []ChatStreamEvent
	var maxSeq int64
	for {
		line, readErr := reader.ReadBytes('\n')
		// Ignore a trailing incomplete line if read concurrently mid-write before '\n'.
		if len(line) > 0 && line[len(line)-1] == '\n' {
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				var evt ChatStreamEvent
				if json.Unmarshal(trimmed, &evt) == nil {
					if evt.Seq > maxSeq {
						maxSeq = evt.Seq
					}
					if evt.Seq > afterSeq {
						out = append(out, evt)
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return out, maxSeq, readErr
		}
	}
	return out, maxSeq, nil
}

func SynthesizeInFlightTurn(meta *TurnSpoolMeta, streamAgent string) (*InFlightTurnSnapshot, error) {
	if meta == nil {
		return nil, nil
	}
	agent := streamAgent
	if agent == "" {
		agent = meta.Agent
	}
	snap := &InFlightTurnSnapshot{
		TurnID: meta.TurnID, SessionID: meta.SessionID, NativeID: meta.NativeID,
		UserPrompt: cleanActivePromptForAgent(agent, meta.Prompt), UserTimestamp: meta.StartedAt,
		IsActive: meta.Status == "starting" || meta.Status == "running", FinishedAt: meta.FinishedAt, UpdatedAt: meta.StartedAt,
	}
	events, maxSeq, err := ReadTurnSpoolAfter(meta.SpoolPath, 0)
	if err != nil {
		return snap, err
	}
	snap.LastSeq = maxSeq
	for _, evt := range events {
		if !evt.Timestamp.IsZero() {
			snap.UpdatedAt = evt.Timestamp
		}
		switch evt.Type {
		case "session_init":
			if evt.NativeID != "" {
				snap.NativeID = evt.NativeID
			}
		case "turn_start":
			if evt.Text != "" {
				snap.UserPrompt = cleanActivePromptForAgent(agent, evt.Text)
			}
			if !evt.Timestamp.IsZero() {
				snap.UserTimestamp = evt.Timestamp
			}
		case "text_delta":
			snap.AssistantText += evt.Text
			snap.LastTurnText += evt.Text
		case "thought_delta":
			snap.ThinkingText += evt.Thinking
		case "tool_start":
			name := strings.TrimSpace(evt.ToolName)
			if name == "" {
				name = "tool"
			}
			detail := extractFilePathFromToolInput(evt.ToolInput)
			if detail == "" {
				detail = extractCommandFromToolInput(evt.ToolInput)
			}
			if detail = strings.Join(strings.Fields(detail), " "); detail != "" {
				if len(detail) > 100 {
					detail = detail[:100] + "..."
				}
				name += ": " + detail
			}
			snap.ToolCalls = append(snap.ToolCalls, name)
		case "interactive_block":
			snap.Blocked, snap.IsActive = evt.Blocked, false
		case "error":
			snap.StreamHadError = true
			if evt.Text != "" {
				snap.LastTurnText += "\n" + evt.Text
			}
		case "turn_cancelled":
			snap.WasCancelled, snap.IsActive = true, false
		case "turn_complete":
			snap.ResultText, snap.IsActive = evt.Text, false
			if evt.Text != "" {
				snap.LastTurnText += "\n" + evt.Text
				if snap.AssistantText == "" {
					snap.AssistantText = evt.Text
				}
			}
		}
	}
	return snap, nil
}

func metaMatchesSession(meta *TurnSpoolMeta, sessionID, nativeID string) bool {
	if meta == nil {
		return false
	}
	if sessionID != "" && (meta.SessionID == sessionID || meta.OrigSessionID == sessionID) {
		return true
	}
	if nativeID != "" && meta.NativeID == nativeID {
		return true
	}
	if parts := strings.Split(sessionID, ":"); len(parts) == 3 && parts[2] != "" {
		return meta.NativeID == parts[2] || strings.HasSuffix(meta.SessionID, ":"+parts[2])
	}
	return false
}

func (h *HeadlessRunner) findSpoolForSessionLocked(sessionID, nativeID string) *TurnSpoolMeta {
	if sessionID != "" && h.sessionSpools[sessionID] != nil {
		return h.sessionSpools[sessionID]
	}
	for k, m := range h.sessionSpools {
		if metaMatchesSession(m, sessionID, nativeID) || (nativeID != "" && strings.HasSuffix(k, ":"+nativeID)) {
			return m
		}
	}
	return nil
}

func (h *HeadlessRunner) GetInFlightTurnSnapshot(sess *Session, sessionID, nativeID, streamAgent string) *InFlightTurnSnapshot {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	meta := h.findSpoolForSessionLocked(sessionID, nativeID)
	if meta == nil && sess != nil && sess.ID != "" {
		meta = h.findSpoolForSessionLocked(sess.ID, sess.NativeID)
	}
	h.mu.RUnlock()

	if meta != nil {
		if meta.MetaPath != "" {
			if dm, err := ReadTurnSpoolMeta(meta.MetaPath); err == nil && dm != nil {
				meta = dm
			}
		}
		if snap, err := SynthesizeInFlightTurn(meta, streamAgent); err == nil && snap != nil {
			if h.IsRunning(sessionID) || (sess != nil && sess.ID != "" && h.IsRunning(sess.ID)) {
				snap.IsActive = true
			} else {
				snap.IsActive = (meta.Status == "starting" || meta.Status == "running") && h.isSpoolWorkerLive(meta)
			}
			return snap
		}
	}

	for _, candidate := range []string{sessionID, func() string {
		if sess != nil {
			return sess.ID
		}
		return ""
	}(), nativeID} {
		if candidate != "" {
			if msg, ok := h.GetActiveTurnPrompt(candidate); ok && strings.TrimSpace(msg.Content) != "" {
				return &InFlightTurnSnapshot{
					TurnID:        msg.TurnID,
					SessionID:     sessionID,
					NativeID:      nativeID,
					UserPrompt:    cleanActivePromptForAgent(streamAgent, msg.Content),
					UserTimestamp: msg.Timestamp,
					IsActive:      true,
					UpdatedAt:     msg.Timestamp,
				}
			}
		}
	}
	return nil
}
