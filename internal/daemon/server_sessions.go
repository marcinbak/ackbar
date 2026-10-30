package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/router"
	"ackbar/internal/tmux"
)

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessions, err := s.db.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Only verify liveness on active sessions to avoid endpoint latency & exec overhead
	var active []*Session
	for _, sess := range sessions {
		if sess.State != StateEnded {
			active = append(active, sess)
		}
	}
	if len(active) > 0 {
		s.verifySessionLiveness(r.Context(), active)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessions)
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Capture user-assigned node_path mappings for existing sessions
	nodePathMap := make(map[string]string)
	if existing, err := s.db.ListSessions(); err == nil {
		for _, sess := range existing {
			if sess.NodePath != "" {
				nodePathMap[sess.ID] = sess.NodePath
				if sess.NativeID != "" {
					nodePathMap[sess.NativeID] = sess.NodePath
				}
			}
		}
	}

	// Clear sessions table (tree_nodes and hosts are strictly preserved)
	if err := s.db.PurgeSessions(); err != nil {
		http.Error(w, fmt.Sprintf("Purge failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Clear in-memory title cache
	titleCacheMutex.Lock()
	titleCache = make(map[string]TitleCacheEntry)
	titleCacheMutex.Unlock()

	// Re-scan live sessions from active tmux panes & OS processes
	s.scanObservedSessions(r.Context())

	// Re-apply preserved node_path mappings to newly scanned sessions
	if scanned, err := s.db.ListSessions(); err == nil {
		for _, sess := range scanned {
			if path, ok := nodePathMap[sess.ID]; ok && path != "" {
				sess.NodePath = path
				_ = s.db.SaveSession(sess)
				s.broadcast(sess)
			} else if path, ok := nodePathMap[sess.NativeID]; ok && path != "" {
				sess.NodePath = path
				_ = s.db.SaveSession(sess)
				s.broadcast(sess)
			}
		}
	}

	// Forward maintenance purge to remote hosts asynchronously
	if hosts, err := s.db.ListHosts(); err == nil {
		for _, h := range hosts {
			if h.URL != "" && h.Name != "local" {
				go func(targetURL string) {
					req, _ := http.NewRequest(http.MethodPost, strings.TrimSuffix(targetURL, "/")+"/v1/maintenance/purge", nil)
					client := &http.Client{Timeout: 5 * time.Second}
					_, _ = client.Do(req)
				}(h.URL)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "purged_and_rehydrated"})
}

func (s *Server) resolveSession(sessionID string) *Session {
	if sessionID == "" || s.db == nil {
		return nil
	}
	sess, err := s.db.GetSession(sessionID)
	if err == nil && sess != nil {
		return sess
	}

	// Fallback 1: Try with local host alias if sessionID had a remote alias or vice-versa
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		localID := fmt.Sprintf("%s:%s:%s", parts[0], s.HostName(), parts[2])
		if found, _ := s.db.GetSession(localID); found != nil {
			return found
		}
		localID = fmt.Sprintf("%s:local:%s", parts[0], parts[2])
		if found, _ := s.db.GetSession(localID); found != nil {
			return found
		}
	} else if strings.HasPrefix(sessionID, "antigravity-") {
		rest := strings.TrimPrefix(sessionID, "antigravity-")
		if idx := strings.Index(rest, "-"); idx > 0 && idx < len(rest)-1 {
			uuidPart := rest[idx+1:]
			for _, candidate := range []string{
				fmt.Sprintf("antigravity:%s:%s", s.HostName(), uuidPart),
				fmt.Sprintf("antigravity:local:%s", uuidPart),
				fmt.Sprintf("antigravity-%s-%s", s.HostName(), uuidPart),
				fmt.Sprintf("antigravity-local-%s", uuidPart),
			} {
				if found, _ := s.db.GetSession(candidate); found != nil {
					return found
				}
			}
		}
	}

	// Fallback 2: Check by OldID or NativeID match
	if all, err := s.db.ListSessions(); err == nil {
		for _, sRecord := range all {
			if sRecord.OldID != "" && (sRecord.OldID == sessionID || strings.EqualFold(sRecord.OldID, sessionID)) {
				return sRecord
			}
			if sRecord.NativeID != "" && (sRecord.NativeID == sessionID || strings.HasSuffix(sessionID, ":"+sRecord.NativeID) || strings.HasSuffix(sessionID, "-"+sRecord.NativeID)) {
				return sRecord
			}
			if strings.EqualFold(sRecord.ID, sessionID) {
				return sRecord
			}
		}
	}

	return nil
}

func (s *Server) remoteHostForSession(sessionID string, sess *Session) *HostRecord {
	if sess != nil {
		return s.resolveHost(sess.Host)
	}
	parts := strings.Split(sessionID, ":")
	if len(parts) >= 2 {
		return s.resolveHost(parts[1])
	}
	return nil
}

func proxyToRemoteHost(w http.ResponseWriter, method, targetURL, contentType string, body io.Reader) bool {
	fwdReq, err := http.NewRequest(method, targetURL, body)
	if err != nil {
		return false
	}
	if contentType != "" {
		fwdReq.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(fwdReq)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true
}

func (s *Server) resolveSessionOrProxy(w http.ResponseWriter, method, sessionID, contentType string, body io.Reader, buildPath func(sess *Session) string) (*Session, bool) {
	sess := s.resolveSession(sessionID)
	if hostRec := s.remoteHostForSession(sessionID, sess); hostRec != nil && hostRec.URL != "" {
		targetURL := strings.TrimSuffix(hostRec.URL, "/") + buildPath(sess)
		if proxyToRemoteHost(w, method, targetURL, contentType, body) {
			return nil, true
		}
	}
	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return nil, true
	}
	return sess, false
}

func (s *Server) handleSessionControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("id")
	action := r.URL.Query().Get("action")

	if sessionID == "" || action == "" {
		parts := stringsSplit(r.URL.Path, "/")
		if len(parts) >= 5 {
			sessionID = parts[3]
			action = parts[4]
		}
	}

	if sessionID == "" || action == "" {
		http.Error(w, "Invalid control parameters", http.StatusBadRequest)
		return
	}

	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
	}
	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, r.Header.Get("Content-Type"), bytes.NewReader(bodyBytes), func(_ *Session) string {
		return fmt.Sprintf("/v1/sessions/control?id=%s&action=%s", url.QueryEscape(sessionID), url.QueryEscape(action))
	})
	if proxied {
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	switch action {
	case "archive":
		sess.Archived = true
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"archived"}`))

	case "unarchive":
		sess.Archived = false
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"unarchived"}`))

	case "done", "mark_done":
		sess.IsDone = true
		sess.IsLater = false
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"done"}`))

	case "later", "mark_later":
		sess.IsLater = true
		sess.IsDone = false
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"later"}`))

	case "unlater":
		sess.IsLater = false
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"active"}`))

	case "undone", "active", "mark_active":
		sess.IsDone = false
		sess.IsLater = false
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"active"}`))

	case "documents":
		files, err := os.ReadDir(sess.Cwd)
		if err != nil {
			http.Error(w, "Failed to read workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var docs []string
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			nameLower := strings.ToLower(name)
			if strings.HasSuffix(nameLower, ".md") || strings.HasSuffix(nameLower, ".txt") || strings.HasSuffix(nameLower, ".jsonl") || nameLower == "gemini.md" || nameLower == "agents.md" {
				docs = append(docs, name)
			}
		}

		sort.Slice(docs, func(i, j int) bool {
			pi := getDocPriority(docs[i])
			pj := getDocPriority(docs[j])
			if pi != pj {
				return pi > pj
			}
			return docs[i] < docs[j]
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docs)

	case "kill":
		if sess.Managed && sess.TmuxName != "" {
			_ = tmux.Kill(r.Context(), sess.TmuxName)
		}
		if sess.PID > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(sess.PID)).Run()
		}

		sess.State = StateEnded
		sess.Activity = "Terminated by user"
		sess.LastEventAt = time.Now()

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"killed"}`))

	case "delete":
		if sess.Managed && sess.TmuxName != "" {
			_ = tmux.Kill(r.Context(), sess.TmuxName)
		}
		if sess.PID > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(sess.PID)).Run()
		}

		s.deleteSessionFilesOnDisk(sess)

		_ = s.db.MarkSessionDeleted(sess.ID)
		if sess.NativeID != "" {
			_ = s.db.MarkSessionDeleted(sess.NativeID)
		}
		_ = s.db.DeleteSession(sess.ID)
		if sess.NativeID != "" {
			_ = s.db.DeleteSession(sess.NativeID)
		}

		sess.Deleted = true
		sess.State = StateEnded
		sess.Activity = "Deleted"
		sess.LastEventAt = time.Now()
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "id": sess.ID})

	case "restart":
		if sess.Cwd == "" && sess.NodePath != "" {
			if node, err := s.db.GetNode(sess.NodePath); err == nil && node != nil && node.ProjectDir != "" {
				sess.Cwd = node.ProjectDir
			}
		}
		if sess.Cwd == "" {
			http.Error(w, "Session working directory is empty", http.StatusBadRequest)
			return
		}

		// Kill existing process/tmux if running
		if sess.Managed && sess.TmuxName != "" {
			_ = tmux.Kill(r.Context(), sess.TmuxName)
		}
		if !sess.Managed && sess.PID > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(sess.PID)).Run()
		}

		// Generate new tmux name if it didn't have one
		tmuxName := sess.TmuxName
		if tmuxName == "" {
			tmuxName = fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
		}
		_ = tmux.Kill(r.Context(), tmuxName)

		// Spawn new tmux session
		resumeCmd := s.getResumeCmd(sess.Agent, sess.NativeID)
		if resumeCmd == "" {
			http.Error(w, "Cannot restart session: missing or invalid session ID", http.StatusBadRequest)
			return
		}
		err := tmux.Spawn(r.Context(), tmuxName, sess.Cwd, resumeCmd)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to spawn tmux: %v", err), http.StatusInternalServerError)
			return
		}

		// Update state to working/restarting
		sess.Managed = true
		sess.TmuxName = tmuxName
		sess.State = StateWorking
		sess.Activity = "Restarting session..."
		sess.LastEventAt = time.Now()

		// Get and update PID
		if pid, err := tmux.GetPID(r.Context(), tmuxName); err == nil {
			sess.PID = pid
		}

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"restarted"}`))

	case "resume":
		if sess.Cwd == "" && sess.NodePath != "" {
			if node, err := s.db.GetNode(sess.NodePath); err == nil && node != nil && node.ProjectDir != "" {
				sess.Cwd = node.ProjectDir
			}
		}
		if sess.Cwd == "" {
			http.Error(w, "Session working directory is empty", http.StatusBadRequest)
			return
		}

		resumeCmd := s.getResumeCmd(sess.Agent, sess.NativeID)
		if resumeCmd == "" {
			http.Error(w, "Cannot resume session: missing or invalid session ID", http.StatusBadRequest)
			return
		}

		tmuxName := sess.TmuxName
		if tmuxName == "" {
			tmuxName = fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
		}
		_ = tmux.Kill(r.Context(), tmuxName)

		err := tmux.Spawn(r.Context(), tmuxName, sess.Cwd, resumeCmd)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to spawn tmux session: %v", err), http.StatusInternalServerError)
			return
		}

		sess.Managed = true
		sess.TmuxName = tmuxName
		sess.State = StateWorking
		sess.Activity = "Resumed session"
		sess.LastEventAt = time.Now()

		if pid, err := tmux.GetPID(r.Context(), tmuxName); err == nil {
			sess.PID = pid
		}

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "resumed",
			"id":        sess.ID,
			"tmux_name": tmuxName,
			"session":   sess,
		})

	case "move":
		nodePath := r.URL.Query().Get("node_path")
		if nodePath == "" {
			var bodyData struct {
				NodePath string `json:"node_path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&bodyData)
			nodePath = bodyData.NodePath
		}
		if err := s.db.MoveSessionNode(sess.ID, nodePath); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sess.NodePath = nodePath
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"moved"}`))

	case "rename":
		name := r.URL.Query().Get("name")
		if name == "" {
			var bodyData struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&bodyData)
			name = bodyData.Name
		}
		sess.Name = name
		sess.CustomTitle = name
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		cacheKey := fmt.Sprintf("%s:%s", sess.Cwd, sess.NativeID)
		titleCacheMutex.Lock()
		titleCache[cacheKey] = TitleCacheEntry{
			Title:     name,
			Source:    "custom",
			UpdatedAt: time.Now(),
		}
		titleCacheMutex.Unlock()

		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"renamed"}`))

	case "open_editor":
		if sess.Cwd == "" {
			http.Error(w, "Session Cwd is empty", http.StatusBadRequest)
			return
		}
		uri, err := LaunchVSCode(sess.Cwd, sess.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened", "path": sess.Cwd, "host": sess.Host, "uri": uri})

	case "respond":
		value := r.URL.Query().Get("value")
		respAction := r.URL.Query().Get("resp_action")
		if respAction == "" {
			respAction = r.URL.Query().Get("action_type")
		}
		if respAction == "" {
			var bodyData struct {
				Action string `json:"action"`
				Value  string `json:"value"`
			}
			_ = json.NewDecoder(r.Body).Decode(&bodyData)
			if bodyData.Action != "" {
				respAction = bodyData.Action
			}
			if bodyData.Value != "" {
				value = bodyData.Value
			}
		}
		if respAction == "" {
			respAction = "answer"
		}
		actionLower := strings.ToLower(respAction)
		_ = s.applyInteractiveResponse(r.Context(), sess, actionLower, value, sess.TmuxName != "", false, false)
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "responded",
			"id":      sess.ID,
			"action":  actionLower,
			"value":   value,
			"state":   sess.State.String(),
			"session": sess,
		})

	case "read", "mark_read", "view":
		sess.IsUnread = false
		_ = s.db.MarkSessionRead(sess.ID)
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "read",
			"id":      sess.ID,
			"session": sess,
		})

	case "handover":
		s.handleSessionHandover(w, r)
		return

	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
}

func (s *Server) applyInteractiveResponse(ctx context.Context, sess *Session, actionLower, value string, sendToTmux, strictAction, clearIsDone bool) error {
	if sendToTmux && sess.TmuxName != "" {
		adapter := s.agentAdapter(sess.Agent)
		switch actionLower {
		case "answer", "input":
			if err := tmux.SendInput(ctx, sess.TmuxName, value, true); err != nil {
				log.Printf("Warning: failed to send input to tmux session %s: %v", sess.TmuxName, err)
			}
		case "allow", "yes", "proceed", "1":
			if strictAction && actionLower != "allow" {
				return fmt.Errorf("Unsupported action: %s", actionLower)
			}
			keys := adapter.GetApprovalKeys(true)
			if err := tmux.SendKeys(ctx, sess.TmuxName, keys...); err != nil {
				log.Printf("Warning: failed to send allow to tmux session %s: %v", sess.TmuxName, err)
			}
		case "deny", "no", "4":
			if strictAction && actionLower != "deny" {
				return fmt.Errorf("Unsupported action: %s", actionLower)
			}
			keys := adapter.GetApprovalKeys(false)
			if err := tmux.SendKeys(ctx, sess.TmuxName, keys...); err != nil {
				log.Printf("Warning: failed to send deny to tmux session %s: %v", sess.TmuxName, err)
			}
		default:
			if strictAction {
				return fmt.Errorf("Unsupported action: %s", actionLower)
			}
		}
	}

	sess.State = StateWorking
	sess.Blocked = nil
	sess.LastEventAt = time.Now()
	if clearIsDone {
		sess.IsDone = false
	}
	switch actionLower {
	case "allow":
		sess.Activity = "Permission allowed"
	case "deny":
		sess.Activity = "Permission denied"
	case "answer", "input":
		if value != "" {
			sess.Activity = fmt.Sprintf("Answered: %s", value)
		} else {
			sess.Activity = "User answered"
		}
	}
	return nil
}

func (s *Server) handleRespond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionId"`
		Action    string `json:"action"` // "answer" | "allow" | "deny" | "input"
		Value     string `json:"value"`
	}

	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	targetID := req.ID
	if targetID == "" {
		targetID = req.SessionID
	}
	if targetID == "" {
		targetID = r.URL.Query().Get("id")
	}
	if targetID == "" {
		targetID = r.URL.Query().Get("sessionId")
	}

	action := req.Action
	if action == "" {
		action = r.URL.Query().Get("action")
	}

	value := req.Value
	if value == "" {
		value = r.URL.Query().Get("value")
	}

	if targetID == "" || action == "" {
		http.Error(w, "Missing required parameters (id, action)", http.StatusBadRequest)
		return
	}

	sess := s.resolveSession(targetID)
	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	actionLower := strings.ToLower(action)
	if err := s.applyInteractiveResponse(r.Context(), sess, actionLower, value, sess.Managed && sess.TmuxName != "", true, true); err != nil {
		http.Error(w, fmt.Sprintf("Unsupported action: %s", action), http.StatusBadRequest)
		return
	}

	if err := s.db.SaveSession(sess); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save session: %v", err), http.StatusInternalServerError)
		return
	}

	// Broadcast update via SSE
	s.broadcast(sess)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "responded",
		"id":      sess.ID,
		"action":  actionLower,
		"value":   value,
		"state":   sess.State.String(),
		"session": sess,
	})
}

func (s *Server) handleTakeWheel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "Missing session_id", http.StatusBadRequest)
		return
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, req.SessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return "/v1/sessions/take-wheel"
	})
	if proxied {
		return
	}

	if sess.EngineType == EngineHeadless && s.headless != nil && s.headless.IsRunning(sess.ID) {
		http.Error(w, "Cannot take wheel while a headless turn is active. Please wait or cancel the turn.", http.StatusConflict)
		return
	}

	tmuxName := fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
	var resumeCmd string
	if p, ok := s.providers[sess.Agent]; ok {
		resumeCmd = p.GetResumeCommand(sess.NativeID)
	}
	if resumeCmd == "" {
		resumeCmd = fmt.Sprintf("claude --resume %s", sess.NativeID)
	}

	home, _ := os.UserHomeDir()
	_, envVars := s.resolveAccountEnv(sess.Agent, sess.AccountID, home, false)

	// If tmux session already exists, check whether an active agent child process is running in the pane
	if tmux.HasSession(r.Context(), tmuxName) {
		panePID, perr := tmux.GetPID(r.Context(), tmuxName)
		hasActiveAgent := false
		if perr == nil && panePID > 0 {
			activeChildren := getActiveChildProcesses(r.Context(), panePID)
			hasActiveAgent = len(activeChildren) > 0
		}
		if hasActiveAgent {
			sess.TmuxName = tmuxName
			sess.EngineType = EngineTmux
			sess.Managed = true
			sess.State = StateWorking
			sess.Activity = "Interactive terminal attached"
			sess.LastEventAt = time.Now()
			sess.PID = panePID
			_ = s.db.SaveSession(sess)
			s.broadcast(sess)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":    "ok",
				"tmux_name": tmuxName,
			})
			return
		}
		// Stale / zombie tmux session with no active agent child processes (e.g. dead bash shell).
		// Kill dead session so a fresh one can be spawned with resumeCmd.
		_ = tmux.Kill(r.Context(), tmuxName)
	}

	var spawnErr error
	if len(envVars) > 0 {
		spawnErr = tmux.SpawnWithEnv(r.Context(), tmuxName, sess.Cwd, resumeCmd, envVars)
	} else {
		spawnErr = tmux.Spawn(r.Context(), tmuxName, sess.Cwd, resumeCmd)
	}
	if spawnErr != nil {
		http.Error(w, fmt.Sprintf("Failed to spawn tmux session: %v", spawnErr), http.StatusInternalServerError)
		return
	}

	sess.TmuxName = tmuxName
	sess.EngineType = EngineTmux
	sess.Managed = true
	sess.State = StateWorking
	sess.Activity = "Interactive terminal attached"
	sess.LastEventAt = time.Now()
	if pid, perr := tmux.GetPID(r.Context(), tmuxName); perr == nil {
		sess.PID = pid
	}

	_ = s.db.SaveSession(sess)
	s.broadcast(sess)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":    "ok",
		"tmux_name": tmuxName,
	})
}

func (s *Server) deleteSessionFilesOnDisk(sess *Session) {
	if sess == nil {
		return
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return
	}

	nativeID := sess.NativeID
	if nativeID == "" {
		parts := strings.Split(sess.ID, ":")
		if len(parts) >= 3 {
			nativeID = parts[2]
		}
	}
	if nativeID == "" {
		return
	}

	if p, ok := s.providers[sess.Agent]; ok {
		_ = p.CleanSessionFiles(home, sess.Cwd, nativeID)
		return
	}

	// Fallback to all providers
	for _, p := range s.providers {
		_ = p.CleanSessionFiles(home, sess.Cwd, nativeID)
	}
}

func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Agent      string `json:"agent"`
		Cwd        string `json:"cwd"`
		Host       string `json:"host"`
		NodePath   string `json:"node_path"`
		Name       string `json:"name"`
		AccountID  string `json:"account_id"`
		EngineType string `json:"engine_type"`
		Prompt     string `json:"prompt"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Agent == "" || req.Cwd == "" {
		http.Error(w, "Missing agent or cwd", http.StatusBadRequest)
		return
	}

	if req.EngineType == "" {
		req.EngineType = EngineTmux
	}

	if req.NodePath == "" {
		req.NodePath = s.resolveSessionNodePath(req.Cwd)
	}

	// 1. Forward to remote host if specified
	if !s.isRequestForLocalDaemon(r, req.Host) {
		hostRec := s.resolveHost(req.Host)
		if hostRec != nil && hostRec.URL != "" && !s.isLocalHost(hostRec.Name) && hostRec.URL != "http://127.0.0.1:7777" {
			targetURL := strings.TrimSuffix(hostRec.URL, "/") + "/v1/sessions/spawn"
			payload, _ := json.Marshal(map[string]string{
				"agent":       req.Agent,
				"cwd":         req.Cwd,
				"node_path":   req.NodePath,
				"name":        req.Name,
				"account_id":  req.AccountID,
				"engine_type": req.EngineType,
				"prompt":      req.Prompt,
			})
			fwdReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewBuffer(payload))
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to create remote request: %v", err), http.StatusInternalServerError)
				return
			}
			fwdReq.Header.Set("Content-Type", "application/json")
			if auth := r.Header.Get("Authorization"); auth != "" {
				fwdReq.Header.Set("Authorization", auth)
			}
			if tok := r.Header.Get("X-Ackbar-Token"); tok != "" {
				fwdReq.Header.Set("X-Ackbar-Token", tok)
			}
			resp, err := http.DefaultClient.Do(fwdReq)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to spawn on remote host %s: %v", req.Host, err), http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		} else if hostRec == nil || (!s.isLocalHost(hostRec.Name) && hostRec.URL != "http://127.0.0.1:7777") {
			http.Error(w, fmt.Sprintf("Remote host '%s' not found or unreachable", req.Host), http.StatusNotFound)
			return
		}
	}

	// Expand ~ to user home directory and ensure directory exists
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(req.Cwd, "~/") && home != "" {
		req.Cwd = filepath.Join(home, req.Cwd[2:])
	} else if req.Cwd == "~" && home != "" {
		req.Cwd = home
	}
	_ = os.MkdirAll(req.Cwd, 0755)

	if req.NodePath != "" {
		if existingNode, _ := s.db.GetNode(req.NodePath); existingNode == nil {
			_ = s.db.SaveNode(&TreeNode{
				Path:       req.NodePath,
				ProjectDir: req.Cwd,
				CreatedAt:  time.Now(),
			})
		} else if existingNode.ProjectDir == "" && req.Cwd != "" {
			existingNode.ProjectDir = req.Cwd
			_ = s.db.SaveNode(existingNode)
		}
	}

	// Resolve account and environment
	accountID, envVars := s.resolveAccountEnv(req.Agent, req.AccountID, home, true)

	tempUUID := generateUUID()
	effectiveHost := s.HostName()

	if req.EngineType == EngineHeadless {
		sess := &Session{
			ID:          fmt.Sprintf("%s:%s:%s", req.Agent, effectiveHost, tempUUID),
			Agent:       req.Agent,
			Host:        effectiveHost,
			NativeID:    tempUUID,
			Cwd:         req.Cwd,
			ProjectKey:  GetProjectKey(req.Cwd),
			NodePath:    req.NodePath,
			Name:        req.Name,
			Managed:     true,
			TmuxName:    "",
			EngineType:  EngineHeadless,
			State:       StateIdle,
			Activity:    "Headless chat session ready",
			StartedAt:   time.Now(),
			LastEventAt: time.Now(),
			AccountID:   accountID,
		}
		initialPrompt := strings.TrimSpace(req.Prompt)
		if req.Name != "" {
			sess.CustomTitle = req.Name
		} else if initialPrompt != "" {
			sess.Name = TruncateTitle(initialPrompt)
		}
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		spawnID := sess.ID
		s.broadcast(sess)
		if initialPrompt != "" && s.headless != nil {
			_ = s.headless.RunTurn(context.Background(), sess, initialPrompt)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "spawned",
			"session_id":  tempUUID,
			"id":          spawnID,
			"host":        effectiveHost,
			"engine_type": EngineHeadless,
			"session":     sess,
		})
		return
	}

	tmuxName := fmt.Sprintf("ackbar-%s-%s", req.Agent, tempUUID)
	launchCmd := s.getSpawnCmd(req.Agent, tempUUID)

	err := tmux.SpawnWithEnv(r.Context(), tmuxName, req.Cwd, launchCmd, envVars)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to spawn session: %v", err), http.StatusInternalServerError)
		return
	}

	if req.Prompt != "" && req.EngineType == EngineTmux {
		go func(sessName, promptText string) {
			time.Sleep(1500 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tmux.SendInput(ctx, sessName, promptText, true)
		}(tmuxName, req.Prompt)
	}

	// Insert temporary spawning session
	sess := &Session{
		ID:          fmt.Sprintf("%s:%s:%s", req.Agent, effectiveHost, tempUUID),
		Agent:       req.Agent,
		Host:        effectiveHost,
		NativeID:    tempUUID,
		Cwd:         req.Cwd,
		ProjectKey:  GetProjectKey(req.Cwd),
		NodePath:    req.NodePath,
		Name:        req.Name,
		Managed:     true,
		TmuxName:    tmuxName,
		EngineType:  EngineTmux,
		State:       StateUnknown,
		Activity:    "Spawning session...",
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
		AccountID:   accountID,
	}
	if req.Name != "" {
		sess.CustomTitle = req.Name
	}

	if pid, err := tmux.GetPID(r.Context(), tmuxName); err == nil {
		sess.PID = pid
	}

	if err := s.db.SaveSession(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.broadcast(sess)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "spawning",
		"session_id":  tempUUID,
		"id":          sess.ID,
		"host":        effectiveHost,
		"engine_type": EngineTmux,
		"session":     sess,
	})
}

func (s *Server) handleMetaResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(body.Prompt) == "" {
		http.Error(w, "Prompt is required", http.StatusBadRequest)
		return
	}

	// 1. Gather Candidate Hosts
	var candidateHosts []router.CandidateHost
	candidateHosts = append(candidateHosts, router.CandidateHost{
		Name: s.HostName(),
		URL:  "http://127.0.0.1:7777",
	})
	if hosts, err := s.db.ListHosts(); err == nil {
		for _, h := range hosts {
			if h.Name != "" && h.Name != s.HostName() && h.Name != "local" {
				candidateHosts = append(candidateHosts, router.CandidateHost{
					Name: h.Name,
					URL:  h.URL,
				})
			}
		}
	}

	// 2. Gather Candidate Sessions & Compute Project Preferred Agents
	nodeAgentFreq := make(map[string]map[string]int)
	var candidateSessions []router.CandidateSession
	if sessions, err := s.db.ListSessions(); err == nil {
		for _, sess := range sessions {
			if sess.NodePath != "" && sess.Agent != "" {
				if nodeAgentFreq[sess.NodePath] == nil {
					nodeAgentFreq[sess.NodePath] = make(map[string]int)
				}
				nodeAgentFreq[sess.NodePath][sess.Agent]++
			}
			if sess.Managed && sess.State != StateEnded {
				candidateSessions = append(candidateSessions, router.CandidateSession{
					ID:        sess.ID,
					Name:      sess.Name,
					Agent:     sess.Agent,
					Host:      sess.Host,
					NodePath:  sess.NodePath,
					Cwd:       sess.Cwd,
					GitBranch: sess.GitBranch,
					Activity:  sess.Activity,
				})
			}
		}
	}

	// 3. Gather Candidate Nodes with Preferred Agent from session history
	var candidateNodes []router.CandidateNode
	if nodes, err := s.db.ListNodes(); err == nil {
		for _, n := range nodes {
			preferred := ""
			maxCount := 0
			if freq, ok := nodeAgentFreq[n.Path]; ok {
				for ag, count := range freq {
					if count > maxCount {
						maxCount = count
						preferred = ag
					}
				}
			}
			candidateNodes = append(candidateNodes, router.CandidateNode{
				Path:           n.Path,
				ProjectDir:     n.ProjectDir,
				PreferredAgent: preferred,
			})
		}
	}

	// 4. Check for API key in DB settings or env
	apiKey := ""
	if val, err := s.db.GetSetting("typesafe_api_key"); err == nil && val != "" {
		apiKey = val
	}
	if apiKey == "" {
		apiKey = os.Getenv("TYPESAFE_API_KEY")
	}

	resolveReq := router.ResolveRequest{
		Prompt:   body.Prompt,
		Hosts:    candidateHosts,
		Nodes:    candidateNodes,
		Sessions: candidateSessions,
		APIKey:   apiKey,
	}

	result, err := router.Resolve(r.Context(), resolveReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("Resolution failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Server) getResumeCmd(agent, nativeID string) string {
	if strings.HasPrefix(nativeID, "proc-") {
		nativeID = ""
	}
	if p, ok := s.providers[agent]; ok {
		return p.GetResumeCommand(nativeID)
	}
	if agent == "mock-agent" {
		return "sleep 5"
	}
	if nativeID != "" && isValidUUID(nativeID) {
		return "claude --resume " + nativeID
	}
	return ""
}

func (s *Server) getSpawnCmd(agent, tempUUID string) string {
	if p, ok := s.providers[agent]; ok {
		return p.GetSpawnCommand(tempUUID)
	}
	if agent == "mock-agent" {
		return "sleep 5"
	}
	if tempUUID != "" && isValidUUID(tempUUID) {
		return "claude --session-id " + tempUUID
	}
	return "claude"
}
