package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"ackbar/internal/router"
	"ackbar/internal/tmux"
)

var validTaskIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawSessions, err := s.db.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Only return sessions authoritative to this host; prune any stray foreign replicas
	sessions := make([]*Session, 0, len(rawSessions))
	var active []*Session
	for _, sess := range rawSessions {
		if sess == nil {
			continue
		}
		if s.isForeignSession(sess.Host) {
			_ = s.db.DeleteSession(sess.ID)
			continue
		}
		sessions = append(sessions, sess)
		if sess.State != StateEnded && s.isLocalHost(sess.Host) {
			active = append(active, sess)
		}
	}
	if len(active) > 0 {
		s.verifyHeadlessLiveness(active)
		s.triggerAsyncLiveness()
	}

	if r.URL.Query().Get("federated") == "true" {
		sessions = s.getFederatedSessions(r.Context(), sessions)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessions)
}

func (s *Server) getFederatedSessions(ctx context.Context, localSessions []*Session) []*Session {
	hosts, err := s.db.ListHosts()
	if err != nil || len(hosts) == 0 {
		return localSessions
	}

	token := s.configuredToken
	if token == "" {
		token = os.Getenv("ACKBAR_TOKEN")
	}

	peerSessions := make(map[string][]*Session)
	var mu sync.Mutex

	var wg sync.WaitGroup
	for _, h := range hosts {
		if h.URL == "" || s.isLocalHost(h.Name) {
			continue
		}
		wg.Add(1)
		go func(hr *HostRecord) {
			defer wg.Done()
			reqURL := strings.TrimSuffix(hr.URL, "/") + "/v1/sessions"
			reqCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
			if err != nil {
				s.updateHostHealth(hr.Name, false, "", "", 0)
				return
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-Ackbar-Token", token)
			}
			client := &http.Client{Timeout: 1500 * time.Millisecond}
			start := time.Now()
			resp, err := client.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				s.updateHostHealth(hr.Name, false, "", "", 0)
				return
			}
			defer resp.Body.Close()

			var remoteSessions []*Session
			if err := json.NewDecoder(resp.Body).Decode(&remoteSessions); err != nil {
				s.updateHostHealth(hr.Name, false, "", "", 0)
				return
			}

			latency := time.Since(start).Milliseconds()
			mu.Lock()
			peerSessions[hr.Name] = remoteSessions
			mu.Unlock()
			s.updateHostHealth(hr.Name, true, hr.Version, hr.DisplayName, latency)
		}(h)
	}
	wg.Wait()

	merged := make(map[string]*Session, len(localSessions))
	for _, sess := range localSessions {
		sess.HostOffline = false
		merged[sess.ID] = sess
	}

	peerMatchesHost := func(peerHost, sessHost string) bool {
		if sessHost == "" || sessHost == "local" {
			return true
		}
		return peerHost == sessHost || strings.EqualFold(peerHost, sessHost) || strings.HasSuffix(peerHost, "@"+sessHost) || strings.HasSuffix(sessHost, "@"+peerHost)
	}

	for peerHost, rSessions := range peerSessions {
		for _, peerSess := range rSessions {
			if peerSess == nil || !peerMatchesHost(peerHost, peerSess.Host) {
				continue
			}
			if peerSess.Host == "" || peerSess.Host == "local" {
				peerSess.Host = peerHost
				if strings.Contains(peerSess.ID, ":local:") {
					if peerSess.OldID == "" {
						peerSess.OldID = peerSess.ID
					}
					peerSess.ID = strings.Replace(peerSess.ID, ":local:", ":"+peerHost+":", 1)
				}
			} else if s.isLocalHost(peerSess.Host) {
				continue
			}
			peerSess.HostOffline = false
			if existing, exists := merged[peerSess.ID]; !exists || (existing != nil && !s.isLocalHost(existing.Host)) {
				merged[peerSess.ID] = peerSess
			}
		}
	}

	result := make([]*Session, 0, len(merged))
	for _, sess := range merged {
		result = append(result, sess)
	}

	// Sort by LastEventAt descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastEventAt.After(result[j].LastEventAt)
	})

	return result
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
	if sess, err := s.db.GetSessionByOldID(sessionID); err == nil && sess != nil {
		return sess
	}

	if found, _ := s.db.GetSessionByAlias(sessionID); found != nil {
		return found
	}

	// Fallback 1: Try with local host alias if sessionID had a remote alias or vice-versa
	parts := strings.Split(sessionID, ":")
	if len(parts) == 3 {
		localID := fmt.Sprintf("%s:%s:%s", parts[0], s.HostName(), parts[2])
		if found, _ := s.db.GetSession(localID); found != nil {
			return found
		}
		if found, _ := s.db.GetSessionByOldID(localID); found != nil {
			return found
		}
		if found, _ := s.db.GetSessionByAlias(localID); found != nil {
			return found
		}
		localID = fmt.Sprintf("%s:local:%s", parts[0], parts[2])
		if found, _ := s.db.GetSession(localID); found != nil {
			return found
		}
		if found, _ := s.db.GetSessionByOldID(localID); found != nil {
			return found
		}
		if found, _ := s.db.GetSessionByAlias(localID); found != nil {
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
				if found, _ := s.db.GetSessionByOldID(candidate); found != nil {
					return found
				}
				if found, _ := s.db.GetSessionByAlias(candidate); found != nil {
					return found
				}
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
	if strings.HasPrefix(sessionID, "antigravity-") && len(sessionID) > len("antigravity-")+37 {
		trimmed := strings.TrimPrefix(sessionID, "antigravity-")
		if len(trimmed) > 37 && trimmed[len(trimmed)-37] == '-' {
			hostPart := trimmed[:len(trimmed)-37]
			return s.resolveHost(hostPart)
		}
	}
	return nil
}

var peerProxyHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
	},
}

func (s *Server) proxyToRemoteHost(w http.ResponseWriter, method, targetURL, contentType string, body io.Reader) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fwdReq, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return false
	}
	if contentType != "" {
		fwdReq.Header.Set("Content-Type", contentType)
	}
	token := s.configuredToken
	if token == "" {
		token = os.Getenv("ACKBAR_TOKEN")
	}
	if token != "" {
		fwdReq.Header.Set("Authorization", "Bearer "+token)
		fwdReq.Header.Set("X-Ackbar-Token", token)
	}
	resp, err := peerProxyHTTPClient.Do(fwdReq)
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
	if hostRec := s.remoteHostForSession(sessionID, nil); hostRec != nil && hostRec.URL != "" {
		targetURL := strings.TrimSuffix(hostRec.URL, "/") + buildPath(&Session{ID: sessionID})
		if s.proxyToRemoteHost(w, method, targetURL, contentType, body) {
			return nil, true
		}
	}
	sess := s.resolveSession(sessionID)
	if hostRec := s.remoteHostForSession(sessionID, sess); hostRec != nil && hostRec.URL != "" {
		targetURL := strings.TrimSuffix(hostRec.URL, "/") + buildPath(sess)
		if s.proxyToRemoteHost(w, method, targetURL, contentType, body) {
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

	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
	}

	if sessionID == "" || action == "" {
		parts := stringsSplit(r.URL.Path, "/")
		if len(parts) >= 5 {
			sessionID = parts[3]
			action = parts[4]
		}
	}

	if (sessionID == "" || action == "") && len(bodyBytes) > 0 {
		var reqBody struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if err := json.Unmarshal(bodyBytes, &reqBody); err == nil {
			if sessionID == "" {
				sessionID = reqBody.ID
			}
			if action == "" {
				action = reqBody.Action
			}
		}
	}

	if sessionID == "" || action == "" {
		http.Error(w, "Invalid control parameters", http.StatusBadRequest)
		return
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
		sess.MetadataUpdatedAt = time.Now().UTC()
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"archived"}`))

	case "unarchive":
		sess.Archived = false
		sess.MetadataUpdatedAt = time.Now().UTC()
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"unarchived"}`))

	case "done", "mark_done":
		if err := s.markSessionDoneAndCleanup(r.Context(), sess, ""); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"done"}`))

	case "later", "mark_later":
		sess.IsLater = true
		sess.IsDone = false
		sess.MetadataUpdatedAt = time.Now().UTC()
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"later"}`))

	case "unlater":
		sess.IsLater = false
		sess.MetadataUpdatedAt = time.Now().UTC()
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
		sess.MetadataUpdatedAt = time.Now().UTC()
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
		if s.headless != nil {
			s.headless.ShutdownSessionWorker(sess.ID, sess.NativeID, 3*time.Second)
		}
		if sess.Managed && sess.TmuxName != "" {
			_ = tmux.Kill(r.Context(), sess.TmuxName)
		}
		if sess.PID > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(sess.PID)).Run()
		}

		sess.State = StateEnded
		sess.Activity = "Terminated by user"
		sess.LastEventAt = time.Now()
		s.SyncSessionTaskWorker(sess)

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.broadcast(sess)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"killed"}`))

	case "delete":
		if s.headless != nil {
			s.headless.CleanupSessionRuntime(sess.ID, sess.NativeID)
		}
		if sess.Managed && sess.TmuxName != "" {
			_ = tmux.Kill(r.Context(), sess.TmuxName)
		}
		if sess.PID > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(sess.PID)).Run()
		}

		if s.authBroker != nil {
			s.authBroker.CancelSession(sess.ID)
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
		s.SyncSessionTaskWorker(sess)
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
		if s.authBroker != nil {
			s.authBroker.CancelSession(sess.ID)
		}
		s.invalidateMCPStatusCache(sess.ID)
		s.blockedBySubagent.Delete(sess.ID)

		if sess.EngineType == EngineHeadless && sess.TmuxName == "" {
			restartedInPlace := false
			if s.headless != nil {
				restartedInPlace = s.headless.RestartSessionChild(sess, 3*time.Second)
				if !restartedInPlace {
					if s.headless.IsRunning(sess.ID) {
						s.headless.CancelTurn(sess.ID)
					}
					s.headless.ClearInteractiveBlockAndWait(sess.ID, 2*time.Second)
				}
			}
			s.clearSubagents(sess.ID, sess.NativeID)

			now := time.Now()
			sess.IsDone = false
			sess.Blocked = nil
			sess.IsUnread = false
			sess.TmuxName = ""
			sess.PID = 0
			sess.RunningSubagents = 0
			sess.State = StateIdle
			if restartedInPlace {
				sess.Activity = "Session restarted (MCP reloaded)"
			} else {
				sess.Activity = "Session restarted"
			}
			sess.LastStateChangeAt = now
			sess.LastEventAt = now

			if err := s.db.SaveSession(sess); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			s.SyncSessionTaskWorker(sess)
			s.broadcast(sess)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":             "restarted",
				"id":                 sess.ID,
				"engine_type":        EngineHeadless,
				"restarted_in_place": restartedInPlace,
				"session":            sess,
			})
			return
		}

		if s.headless != nil {
			s.headless.ShutdownSessionWorker(sess.ID, sess.NativeID, 3*time.Second)
		}
		if !sess.Managed && sess.PID > 1 {
			terminateExternalPID(sess.PID, 300*time.Millisecond)
		}

		home := s.effectiveHomeDir()
		launchCmd, isExisting := s.getRestartOrResumeCmd(sess, home)
		if launchCmd == "" {
			http.Error(w, "Cannot restart session: missing or invalid session ID", http.StatusBadRequest)
			return
		}

		tmuxName := sess.TmuxName
		if tmuxName == "" {
			tmuxName = fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
		}
		envVars := s.buildTmuxSessionEnv(sess, home)
		s.recentTmuxRestarts.Store(sess.ID, time.Now())
		if err := tmux.RespawnWithEnv(r.Context(), tmuxName, sess.Cwd, launchCmd, envVars); err != nil {
			s.recentTmuxRestarts.Delete(sess.ID)
			http.Error(w, fmt.Sprintf("Failed to restart tmux session: %v", err), http.StatusInternalServerError)
			return
		}

		wasUnadoptedSpawning := !isExisting && sess.Managed && sess.State == StateUnknown && sess.OldID == ""
		sess.IsDone = false
		sess.Blocked = nil
		sess.IsUnread = false
		sess.Managed = true
		sess.EngineType = EngineTmux
		sess.TmuxName = tmuxName
		if wasUnadoptedSpawning {
			sess.State = StateUnknown
		} else {
			sess.State = StateWorking
		}
		sess.Activity = "Restarting session..."
		sess.LastEventAt = time.Now()

		if pid, err := tmux.GetPID(r.Context(), tmuxName); err == nil {
			sess.PID = pid
		}

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.SyncSessionTaskWorker(sess)
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "restarted",
			"id":          sess.ID,
			"tmux_name":   tmuxName,
			"engine_type": EngineTmux,
			"session":     sess,
		})

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
		if s.authBroker != nil {
			s.authBroker.CancelSession(sess.ID)
		}
		s.invalidateMCPStatusCache(sess.ID)
		s.blockedBySubagent.Delete(sess.ID)

		if sess.EngineType == EngineHeadless && sess.TmuxName == "" {
			if s.headless != nil {
				s.headless.RestartSessionChild(sess, 3*time.Second)
			}
			now := time.Now()
			sess.IsDone = false
			sess.Blocked = nil
			sess.IsUnread = false
			sess.TmuxName = ""
			sess.PID = 0
			sess.State = StateIdle
			sess.Activity = "Resumed session"
			sess.LastStateChangeAt = now
			sess.LastEventAt = now

			if err := s.db.SaveSession(sess); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			s.SyncSessionTaskWorker(sess)
			s.broadcast(sess)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":      "resumed",
				"id":          sess.ID,
				"engine_type": EngineHeadless,
				"session":     sess,
			})
			return
		}

		if s.headless != nil {
			s.headless.ShutdownSessionWorker(sess.ID, sess.NativeID, 3*time.Second)
		}

		home := s.effectiveHomeDir()
		launchCmd, _ := s.getRestartOrResumeCmd(sess, home)
		if launchCmd == "" {
			http.Error(w, "Cannot resume session: missing or invalid session ID", http.StatusBadRequest)
			return
		}

		tmuxName := sess.TmuxName
		if tmuxName == "" {
			tmuxName = fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
		}
		envVars := s.buildTmuxSessionEnv(sess, home)
		s.recentTmuxRestarts.Store(sess.ID, time.Now())
		if err := tmux.RespawnWithEnv(r.Context(), tmuxName, sess.Cwd, launchCmd, envVars); err != nil {
			s.recentTmuxRestarts.Delete(sess.ID)
			http.Error(w, fmt.Sprintf("Failed to spawn tmux session: %v", err), http.StatusInternalServerError)
			return
		}

		sess.IsDone = false
		sess.Blocked = nil
		sess.Managed = true
		sess.EngineType = EngineTmux
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
		s.SyncSessionTaskWorker(sess)
		s.broadcast(sess)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "resumed",
			"id":          sess.ID,
			"tmux_name":   tmuxName,
			"engine_type": EngineTmux,
			"session":     sess,
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
		sess.MetadataUpdatedAt = time.Now().UTC()
		_ = s.db.SaveSession(sess)
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
		sess.MetadataUpdatedAt = time.Now().UTC()
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
		s.handleRespond(w, r)
		return

	case "update_config", "config":
		s.handleSessionConfig(w, r, sess.ID)
		return

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
	if sess.Blocked != nil && sess.Blocked.Kind == BlockRateLimit {
		valLower := strings.ToLower(value)
		var deliverAt time.Time
		if strings.Contains(valLower, "1h") || strings.Contains(valLower, "1 hour") || strings.Contains(actionLower, "1h") {
			deliverAt = time.Now().Add(1 * time.Hour)
		} else if sess.Blocked.ResetAt != nil && !sess.Blocked.ResetAt.IsZero() {
			deliverAt = *sess.Blocked.ResetAt
		} else {
			deliverAt = time.Now().Add(1 * time.Hour)
		}

		sess.State = StateBlocked
		sess.Blocked.ResetAt = &deliverAt
		sess.Blocked.Options = nil
		sess.Activity = fmt.Sprintf("Scheduled resume at %s", deliverAt.Local().Format("15:04"))
		sess.LastEventAt = time.Now()
		if clearIsDone {
			sess.IsDone = false
		}
		if s.headless != nil {
			s.headless.EnqueueDelayedPrompt(sess.ID, "Continue", &deliverAt)
			s.headless.SetQueuePaused(sess.ID, false)
			s.headless.EmitQueueUpdate(sess.ID)
		}
		return nil
	}

	if sendToTmux && sess.TmuxName != "" {
		adapter := s.agentAdapter(sess.Agent)
		switch actionLower {
		case "answer", "input":
			if err := tmux.SendInput(ctx, sess.TmuxName, value, true); err != nil {
				log.Printf("Warning: failed to send input to tmux session %s: %v", sess.TmuxName, err)
			}
		case "allow", "yes", "proceed", "1", "approve":
			if strictAction && actionLower != "allow" && actionLower != "approve" {
				return fmt.Errorf("Unsupported action: %s", actionLower)
			}
			keys := adapter.GetApprovalKeys(true)
			if err := tmux.SendKeys(ctx, sess.TmuxName, keys...); err != nil {
				log.Printf("Warning: failed to send allow to tmux session %s: %v", sess.TmuxName, err)
			}
		case "deny", "no", "4", "reject":
			if value != "" {
				if err := tmux.SendInput(ctx, sess.TmuxName, value, true); err != nil {
					log.Printf("Warning: failed to send rejection feedback to tmux session %s: %v", sess.TmuxName, err)
				}
			} else {
				if strictAction && actionLower != "deny" && actionLower != "reject" {
					return fmt.Errorf("Unsupported action: %s", actionLower)
				}
				keys := adapter.GetApprovalKeys(false)
				if err := tmux.SendKeys(ctx, sess.TmuxName, keys...); err != nil {
					log.Printf("Warning: failed to send deny to tmux session %s: %v", sess.TmuxName, err)
				}
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
	case "allow", "approve":
		sess.Activity = "Permission allowed"
	case "deny", "reject":
		if value != "" {
			sess.Activity = fmt.Sprintf("Rejected: %s", value)
		} else {
			sess.Activity = "Permission denied"
		}
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
		ID          string `json:"id"`
		SessionID   string `json:"sessionId"`
		SessionIDSC string `json:"session_id"`
		Action      string `json:"action"` // "answer" | "allow" | "deny" | "input"
		Value       string `json:"value"`
		Answer      string `json:"answer"`
	}

	var bodyBytes []byte
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Request body too large or read failure", http.StatusBadRequest)
			return
		}
		_ = json.Unmarshal(bodyBytes, &req)
	}

	targetID := req.ID
	if targetID == "" {
		targetID = req.SessionID
	}
	if targetID == "" {
		targetID = req.SessionIDSC
	}
	if targetID == "" {
		targetID = r.URL.Query().Get("id")
	}
	if targetID == "" {
		targetID = r.URL.Query().Get("sessionId")
	}
	if targetID == "" {
		targetID = r.URL.Query().Get("session_id")
	}
	if targetID == "" {
		parts := stringsSplit(r.URL.Path, "/")
		if len(parts) >= 5 && parts[4] == "respond" {
			targetID = parts[3]
		}
	}

	action := req.Action
	if action == "" {
		action = r.URL.Query().Get("action")
	}

	value := req.Value
	if value == "" {
		value = req.Answer
	}
	if value == "" {
		value = r.URL.Query().Get("value")
	}
	if value == "" {
		value = r.URL.Query().Get("answer")
	}

	if action == "" && value != "" {
		action = "answer"
	}

	if targetID == "" || action == "" {
		http.Error(w, "Missing required parameters (id, action)", http.StatusBadRequest)
		return
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, targetID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return "/v1/sessions/respond"
	})
	if proxied {
		return
	}
	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if s.authBroker != nil {
		s.authBroker.CancelSession(sess.ID)
	}

	actionLower := strings.ToLower(action)

	if sess.Blocked != nil && sess.Blocked.Kind == BlockAuth {
		if actionLower == "dismiss" {
			if s.headless != nil {
				s.headless.ClearInteractiveBlockAndWait(sess.ID, 3*time.Second)
				s.headless.SetQueuePaused(sess.ID, false)
				s.headless.EmitQueueUpdate(sess.ID)
			}
			sess.Blocked = nil
			sess.State = StateIdle
			sess.Activity = "Authentication dismissed"
			sess.LastEventAt = time.Now()
			sess.IsUnread = false
			if err := s.db.SaveSession(sess); err != nil {
				http.Error(w, fmt.Sprintf("Failed to save session: %v", err), http.StatusInternalServerError)
				return
			}
			s.broadcast(sess)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "dismissed",
				"id":      sess.ID,
				"action":  actionLower,
				"state":   sess.State.String(),
				"session": sess,
			})
			return
		}
		if actionLower == "retry" || actionLower == "approve" || actionLower == "answer" {
			prevBlocked := sess.Blocked
			sess.Blocked = nil
			sess.State = StateWorking
			sess.Activity = "Retrying after authentication"
			sess.LastEventAt = time.Now()
			sess.IsUnread = false

			if s.headless != nil {
				s.headless.ClearInteractiveBlockAndWait(sess.ID, 3*time.Second)
				s.headless.SetQueuePaused(sess.ID, false)
				s.headless.EmitQueueUpdate(sess.ID)
			}
			if err := s.db.SaveSession(sess); err != nil {
				http.Error(w, fmt.Sprintf("Failed to save session: %v", err), http.StatusInternalServerError)
				return
			}
			s.broadcast(sess)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "responded",
				"id":      sess.ID,
				"action":  actionLower,
				"state":   sess.State.String(),
				"session": sess,
			})
			if s.headless != nil {
				sessCopy := *sess
				go func() {
					home, _ := os.UserHomeDir()
					prompt := resumePromptForAuth(&sessCopy, prevBlocked, home, s.db)
					if value != "" && value != "retry" && value != "approve" {
						prompt = value
					}
					_ = s.headless.RunTurn(context.Background(), &sessCopy, prompt)
				}()
			}
			return
		}
	}

	if sess.Blocked != nil && sess.Blocked.Kind == BlockRateLimit {
		valLower := strings.ToLower(value)
		var deliverAt time.Time
		if strings.Contains(valLower, "1h") || strings.Contains(valLower, "1 hour") || strings.Contains(actionLower, "1h") {
			deliverAt = time.Now().Add(1 * time.Hour)
		} else if sess.Blocked.ResetAt != nil && !sess.Blocked.ResetAt.IsZero() {
			deliverAt = *sess.Blocked.ResetAt
		} else {
			deliverAt = time.Now().Add(1 * time.Hour)
		}

		sess.State = StateBlocked
		sess.Blocked.ResetAt = &deliverAt
		sess.Blocked.Options = nil
		sess.Activity = fmt.Sprintf("Scheduled resume at %s", deliverAt.Local().Format("15:04"))
		sess.LastEventAt = time.Now()
		sess.IsDone = false
		if s.headless != nil {
			s.headless.EnqueueDelayedPrompt(sess.ID, "Continue", &deliverAt)
			s.headless.SetQueuePaused(sess.ID, false)
			s.headless.EmitQueueUpdate(sess.ID)
		}
		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, fmt.Sprintf("Failed to save session: %v", err), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "scheduled",
			"id":         sess.ID,
			"action":     actionLower,
			"deliver_at": deliverAt.Format(time.RFC3339),
			"state":      sess.State.String(),
			"session":    sess,
		})
		return
	}

	isHeadless := sess.EngineType == EngineHeadless || (sess.Managed && sess.TmuxName == "")
	if isHeadless {
		answerText := value
		if answerText == "" && (actionLower == "allow" || actionLower == "yes" || actionLower == "1" || actionLower == "approve") {
			answerText = "Approve Plan"
		}
		if (answerText == "Approve Plan" || ((sess.Blocked == nil || sess.Blocked.Kind == BlockApproval) && strings.HasPrefix(strings.ToLower(answerText), "approve"))) && sess.PermissionMode == "plan" {
			sess.PermissionMode = "bypassPermissions"
		}

		if s.headless != nil {
			s.headless.ClearInteractiveBlockAndWait(sess.ID, 3*time.Second)
		}

		sess.Blocked = nil
		sess.State = StateWorking
		if answerText != "" {
			sess.Activity = "Answered: " + answerText
		} else {
			sess.Activity = "User answered"
		}
		sess.LastEventAt = time.Now()
		sess.IsUnread = false

		if s.headless != nil {
			s.headless.SetQueuePaused(sess.ID, false)
		}

		if err := s.db.SaveSession(sess); err != nil {
			http.Error(w, fmt.Sprintf("Failed to save session: %v", err), http.StatusInternalServerError)
			return
		}
		s.broadcast(sess)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "responded",
			"id":      sess.ID,
			"action":  actionLower,
			"value":   answerText,
			"state":   sess.State.String(),
			"session": sess,
		})

		if s.headless != nil {
			sessCopy := *sess
			go func() {
				_ = s.headless.RunTurn(context.Background(), &sessCopy, answerText)
			}()
		}
		return
	}

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

func (s *Server) handleSessionConfig(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if sessionID == "" {
		sessionID = r.URL.Query().Get("id")
	}
	if sessionID == "" {
		parts := stringsSplit(r.URL.Path, "/")
		if len(parts) >= 5 {
			sessionID = parts[3]
		}
	}

	bodyReader := http.MaxBytesReader(w, r.Body, 32<<10)
	var bodyBytes []byte
	var readErr error
	if r.Body != nil {
		bodyBytes, readErr = io.ReadAll(bodyReader)
		if readErr != nil {
			http.Error(w, "Request body exceeds 32 KB limit or is invalid", http.StatusBadRequest)
			return
		}
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "application/json", bytes.NewReader(bodyBytes), func(target *Session) string {
		targetID := sessionID
		if target != nil && target.ID != "" {
			targetID = target.ID
		}
		return "/v1/sessions/" + url.PathEscape(targetID) + "/config"
	})
	if proxied {
		return
	}
	if sess == nil {
		return
	}

	var req struct {
		Model          *string   `json:"model"`
		Effort         *string   `json:"effort"`
		PermissionMode *string   `json:"permission_mode"`
		ExtraDirs      *[]string `json:"extra_dirs"`
		AddDirs        []string  `json:"add_dirs"`
		RemoveDirs     []string  `json:"remove_dirs"`
	}

	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
			return
		}
	}

	if req.Model != nil {
		m := strings.TrimSpace(*req.Model)
		if strings.EqualFold(m, "default") {
			sess.Model = ""
		} else {
			if strings.HasPrefix(m, "-") || strings.ContainsAny(m, "\x00\r\n \t") {
				http.Error(w, "Invalid model name", http.StatusBadRequest)
				return
			}
			sess.Model = m
		}
	}

	if req.Effort != nil {
		eff := strings.ToLower(strings.TrimSpace(*req.Effort))
		if eff == "" || eff == "default" {
			sess.Effort = ""
		} else if eff == "low" || eff == "medium" || eff == "high" || eff == "xhigh" || eff == "max" {
			sess.Effort = eff
		} else {
			http.Error(w, "Invalid effort: must be 'low', 'medium', 'high', 'xhigh', or 'max'", http.StatusBadRequest)
			return
		}
	}

	if req.PermissionMode != nil {
		rawPm := strings.TrimSpace(*req.PermissionMode)
		pmLower := strings.ToLower(rawPm)
		if pmLower == "" || pmLower == "default" || pmLower == "bypasspermissions" {
			sess.PermissionMode = "bypassPermissions"
		} else if pmLower == "plan" {
			sess.PermissionMode = "plan"
		} else if pmLower == "auto" {
			sess.PermissionMode = "auto"
		} else if pmLower == "acceptedits" {
			sess.PermissionMode = "acceptEdits"
		} else if pmLower == "dontask" {
			if strings.EqualFold(sess.Agent, "antigravity") {
				http.Error(w, "permission_mode 'dontAsk' is not supported for antigravity", http.StatusBadRequest)
				return
			}
			sess.PermissionMode = "dontAsk"
		} else {
			http.Error(w, "Invalid permission_mode: must be 'bypassPermissions', 'auto', 'acceptEdits', 'dontAsk', or 'plan'", http.StatusBadRequest)
			return
		}
	}

	if req.ExtraDirs != nil || len(req.AddDirs) > 0 || len(req.RemoveDirs) > 0 {
		newDirs, err := normalizeAndValidateExtraDirs(sess.Cwd, sess.ExtraDirs, req.ExtraDirs, req.AddDirs, req.RemoveDirs, s.effectiveHomeDir())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sess.ExtraDirs = newDirs
	}

	sess.MetadataUpdatedAt = time.Now().UTC()
	if err := s.db.SaveSession(sess); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save session config: %v", err), http.StatusInternalServerError)
		return
	}

	if s.headless != nil {
		s.headless.NotifyConfigChanged(sess)
	}

	s.broadcast(sess)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(sess)
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

	if s.authBroker != nil {
		s.authBroker.CancelSession(sess.ID)
	}

	if sess.EngineType == EngineHeadless && s.headless != nil {
		s.headless.ShutdownSessionWorker(sess.ID, sess.NativeID, 3*time.Second)
		_ = s.headless.CancelTurn(sess.ID)
		s.headless.ClearInteractiveBlockAndWait(sess.ID, 1500*time.Millisecond)
		s.clearSubagents(sess.ID, sess.NativeID)
	}

	tmuxName := fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
	home := s.effectiveHomeDir()
	resumeCmd, _ := s.getRestartOrResumeCmd(sess, home)
	if resumeCmd == "" {
		resumeCmd = fmt.Sprintf("claude --resume %s", sess.NativeID)
	}
	envVars := s.buildTmuxSessionEnv(sess, home)

	// If tmux session already exists, check whether an active agent child process is running in the pane
	if tmux.HasSession(r.Context(), tmuxName) {
		panePID, perr := tmux.GetPID(r.Context(), tmuxName)
		hasActiveAgent := false
		if perr == nil && panePID > 0 {
			activeChildren := getActiveChildProcesses(r.Context(), panePID)
			hasActiveAgent = len(activeChildren) > 0
		}
		if hasActiveAgent {
			sess.IsDone = false
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
	}

	if spawnErr := tmux.RespawnWithEnv(r.Context(), tmuxName, sess.Cwd, resumeCmd, envVars); spawnErr != nil {
		http.Error(w, fmt.Sprintf("Failed to spawn tmux session: %v", spawnErr), http.StatusInternalServerError)
		return
	}

	sess.IsDone = false
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

func (s *Server) handleSwitchHeadless(w http.ResponseWriter, r *http.Request) {
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
		return "/v1/sessions/switch-headless"
	})
	if proxied {
		return
	}
	if sess == nil {
		return
	}

	if sess.Agent != "claude-code" && sess.Agent != "antigravity" {
		http.Error(w, ErrAgentUnsupported.Error(), http.StatusBadRequest)
		return
	}

	tmuxName := sess.TmuxName
	if tmuxName != "" && tmuxName != "(deleted)" {
		_ = tmux.Kill(r.Context(), tmuxName)
	}
	if sess.NativeID != "" {
		defaultTmuxName := fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
		if defaultTmuxName != tmuxName {
			_ = tmux.Kill(r.Context(), defaultTmuxName)
		}
	}

	if s.headless != nil {
		s.headless.ShutdownSessionWorker(sess.ID, sess.NativeID, 3*time.Second)
	}

	sess.IsDone = false
	sess.TmuxName = ""
	sess.EngineType = EngineHeadless
	sess.Managed = true
	sess.PID = 0
	sess.State = StateIdle
	sess.Blocked = nil
	sess.Activity = "Switched to headless mode"
	sess.LastEventAt = time.Now()

	_ = s.db.SaveSession(sess)
	s.broadcast(sess)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":      "ok",
		"engine_type": EngineHeadless,
		"session_id":  sess.ID,
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
		Agent                string   `json:"agent"`
		Cwd                  string   `json:"cwd"`
		Host                 string   `json:"host"`
		TargetHost           string   `json:"target_host"`
		RequiredCapabilities []string `json:"required_capabilities"`
		NodePath             string   `json:"node_path"`
		Name                 string   `json:"name"`
		AccountID            string   `json:"account_id"`
		EngineType           string   `json:"engine_type"`
		Prompt               string   `json:"prompt"`
		IsSupervisor         bool     `json:"is_supervisor"`
		TaskID               string   `json:"task_id"`
		CurrentTaskID        string   `json:"current_task_id"`
		TaskKind             string   `json:"task_kind"`
		RepoNodePath         string   `json:"repo_node_path"`
		ExtraDirs            []string `json:"extra_dirs"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Host == "" && req.TargetHost != "" {
		req.Host = req.TargetHost
	}

	if req.TaskID == "" && req.CurrentTaskID != "" {
		req.TaskID = req.CurrentTaskID
	}

	if req.TaskID != "" {
		if !validTaskIDRegex.MatchString(req.TaskID) {
			http.Error(w, "Invalid task_id format", http.StatusBadRequest)
			return
		}
	}

	if req.NodePath == "" && req.RepoNodePath != "" {
		req.NodePath = req.RepoNodePath
	} else if req.RepoNodePath == "" && req.NodePath != "" && !req.IsSupervisor {
		req.RepoNodePath = req.NodePath
	}

	if req.IsSupervisor {
		if req.Agent == "" {
			req.Agent = "claude-code"
		}
		if req.Cwd == "" || req.Cwd == "~" {
			req.Cwd = "~/.ackbar/supervisor"
		}
		if req.Name == "" {
			req.Name = "⚓ New Supervisor"
		}
	}

	if req.Agent == "" {
		http.Error(w, "Missing agent", http.StatusBadRequest)
		return
	}

	if req.Cwd == "" && req.NodePath == "" && req.RepoNodePath == "" {
		http.Error(w, "Missing cwd or node_path", http.StatusBadRequest)
		return
	}

	if req.EngineType == "" {
		req.EngineType = EngineTmux
	}

	hasRoutingHint := len(req.RequiredCapabilities) > 0 ||
		len(inferCapabilitiesFromRepoPath(req.RepoNodePath)) > 0 ||
		len(inferCapabilitiesFromRepoPath(req.NodePath)) > 0
	if !hasRoutingHint && !req.IsSupervisor && s.db != nil {
		for _, p := range []string{req.RepoNodePath, req.NodePath} {
			if strings.TrimSpace(p) != "" {
				if nhps, err := s.db.ListNodeHostPaths(strings.TrimSpace(p)); err == nil && len(nhps) > 0 {
					hasRoutingHint = true
					break
				}
			}
		}
	}

	targetForRouting := strings.TrimSpace(req.Host)
	if targetForRouting == "" && hasRoutingHint {
		targetForRouting = "auto"
	} else if targetForRouting == "" {
		targetForRouting = "local"
	}
	routingDecision := s.SelectTargetHost(r.Context(), HostRoutingRequest{
		TargetHost:           targetForRouting,
		RequiredCapabilities: req.RequiredCapabilities,
		RepoNodePath:         req.RepoNodePath,
		NodePath:             req.NodePath,
		TaskKind:             req.TaskKind,
		Agent:                req.Agent,
		IsSupervisor:         req.IsSupervisor,
	})
	if strings.EqualFold(strings.TrimSpace(req.Host), "auto") || (req.Host == "" && hasRoutingHint) {
		req.Host = routingDecision.SelectedHost
	}

	// 1. Forward to remote host if specified
	if !s.isRequestForLocalDaemon(r, req.Host) {
		hostRec := s.resolveHost(req.Host)
		if hostRec != nil && hostRec.URL != "" && !s.isLocalHost(hostRec.Name) && hostRec.URL != "http://127.0.0.1:7777" {
			targetURL := strings.TrimSuffix(hostRec.URL, "/") + "/v1/sessions/spawn"
			payload, _ := json.Marshal(map[string]interface{}{
				"agent":          req.Agent,
				"cwd":            req.Cwd,
				"node_path":      req.NodePath,
				"name":           req.Name,
				"account_id":     req.AccountID,
				"engine_type":    req.EngineType,
				"prompt":         req.Prompt,
				"is_supervisor":  req.IsSupervisor,
				"task_id":        req.TaskID,
				"task_kind":      req.TaskKind,
				"repo_node_path": req.RepoNodePath,
				"extra_dirs":     req.ExtraDirs,
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
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
			if resp.StatusCode >= 200 && resp.StatusCode < 300 && s.db != nil {
				snap := HostLoadSnapshot{}
				if routingDecision.Load != nil {
					snap = *routingDecision.Load
				}
				nowUTC := time.Now().UTC()
				_ = s.db.RecordHostLoadSample(hostRec.Name, snap, 1, nowUTC)
				if req.TaskID != "" {
					var remoteSpawnResp struct {
						ID   string `json:"id"`
						Host string `json:"host"`
					}
					_ = json.Unmarshal(respBody, &remoteSpawnResp)
					rHost := remoteSpawnResp.Host
					if rHost == "" {
						rHost = hostRec.Name
					}
					s.activateTaskForSpawnedWorker(req.TaskID, remoteSpawnResp.ID, req.Agent, rHost, req.RepoNodePath, nowUTC)
				}
			}
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(respBody)
			return
		} else if hostRec == nil || (!s.isLocalHost(hostRec.Name) && hostRec.URL != "http://127.0.0.1:7777") {
			http.Error(w, fmt.Sprintf("Remote host '%s' not found or unreachable", req.Host), http.StatusNotFound)
			return
		}
	}

	// 2. Resolve Cwd for local daemon execution
	resolveCwdFromNode := func(candidate string) string {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return ""
		}
		if nhp, _ := s.db.GetNodeHostPath(candidate, s.HostName()); nhp != nil && nhp.ProjectDir != "" {
			return nhp.ProjectDir
		}
		if node, _ := s.db.GetNode(candidate); node != nil && node.ProjectDir != "" {
			return node.ProjectDir
		}
		return ""
	}

	if req.Cwd == "" || !filepath.IsAbs(req.Cwd) || req.Cwd == req.NodePath || req.Cwd == req.RepoNodePath {
		for _, cand := range []string{req.NodePath, req.RepoNodePath, req.Cwd} {
			if dir := resolveCwdFromNode(cand); dir != "" {
				req.Cwd = dir
				break
			}
		}
	}

	if req.Cwd == "" {
		req.Cwd = "~"
	}

	// Expand ~ to user home directory and ensure directory exists
	home := s.homeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if strings.HasPrefix(req.Cwd, "~/") && home != "" {
		req.Cwd = filepath.Join(home, req.Cwd[2:])
	} else if req.Cwd == "~" && home != "" {
		req.Cwd = home
	}
	if !filepath.IsAbs(req.Cwd) {
		if home != "" {
			req.Cwd = home
		} else {
			cwd, _ := os.Getwd()
			req.Cwd = cwd
		}
	}
	_ = os.MkdirAll(req.Cwd, 0755)
	if req.IsSupervisor && filepath.Clean(req.Cwd) == filepath.Clean(DefaultSupervisorDir(home)) {
		s.ensureSupervisorWorkspace(req.Cwd)
	}

	if req.NodePath == "" && !req.IsSupervisor && req.Cwd != "" {
		req.NodePath = s.resolveSessionNodePath(req.Cwd)
	}

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
	spawnSessID := fmt.Sprintf("%s:%s:%s", req.Agent, effectiveHost, tempUUID)
	if envVars == nil {
		envVars = make(map[string]string)
	}
	envVars["ACKBAR_SESSION_ID"] = spawnSessID
	if req.IsSupervisor {
		envVars["ACKBAR_IS_SUPERVISOR"] = "1"
	}

	taskKind := strings.ToLower(strings.TrimSpace(req.TaskKind))
	if req.TaskID != "" {
		envVars["ACKBAR_TASK_ID"] = req.TaskID
		if taskKind == "" {
			if t, _ := s.db.GetTaskByID(req.TaskID); t != nil && t.TaskKind != "" {
				taskKind = strings.ToLower(strings.TrimSpace(t.TaskKind))
			}
		}
	}
	if taskKind != "design" && taskKind != "code" {
		taskKind = "code"
	}
	envVars["ACKBAR_TASK_KIND"] = taskKind

	if req.EngineType == EngineHeadless {
		var initialExtraDirs []string
		if len(req.ExtraDirs) > 0 {
			cleanDirs, err := normalizeAndValidateExtraDirs(req.Cwd, nil, &req.ExtraDirs, nil, nil, s.effectiveHomeDir())
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			initialExtraDirs = cleanDirs
		}
		now := time.Now().UTC()
		sess := &Session{
			ID:                fmt.Sprintf("%s:%s:%s", req.Agent, effectiveHost, tempUUID),
			Agent:             req.Agent,
			Host:              effectiveHost,
			NativeID:          tempUUID,
			Cwd:               req.Cwd,
			ProjectKey:        GetProjectKey(req.Cwd),
			NodePath:          req.NodePath,
			Name:              req.Name,
			IsSupervisor:      req.IsSupervisor,
			CurrentTaskID:     req.TaskID,
			Managed:           true,
			TmuxName:          "",
			EngineType:        EngineHeadless,
			ExtraDirs:         initialExtraDirs,
			State:             StateIdle,
			Activity:          "Headless chat session ready",
			StartedAt:         now,
			LastEventAt:       now,
			MetadataUpdatedAt: now,
			AccountID:         accountID,
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
		if req.TaskID != "" {
			s.activateTaskForSpawnedWorker(req.TaskID, sess.ID, req.Agent, effectiveHost, req.RepoNodePath, now)
		}
		if s.db != nil {
			snap := s.CollectLocalHostLoad()
			_ = s.db.RecordHostLoadSample(effectiveHost, snap, 1, now)
		}
		s.broadcast(sess)
		if initialPrompt != "" && s.headless != nil {
			_ = s.headless.RunTurn(context.Background(), sess, initialPrompt)
		}
		respPayload := map[string]interface{}{
			"status":         "spawned",
			"session_id":     tempUUID,
			"id":             spawnID,
			"host":           effectiveHost,
			"engine_type":    EngineHeadless,
			"session":        sess,
			"routing_reason": routingDecision.Reason,
		}
		if routingDecision.LoadWarning != "" {
			respPayload["load_warning"] = routingDecision.LoadWarning
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respPayload)
		return
	}

	tmuxName := fmt.Sprintf("ackbar-%s-%s", req.Agent, tempUUID)
	launchCmd := s.getSpawnCmd(req.Agent, tempUUID)
	useCLIInitPrompt := false
	if strings.TrimSpace(req.Prompt) != "" && req.EngineType == EngineTmux {
		if initCmd := appendCLIInitPrompt(req.Agent, launchCmd); initCmd != "" {
			envVars["ACKBAR_INIT_PROMPT"] = req.Prompt
			launchCmd = initCmd
			useCLIInitPrompt = true
		}
	}

	err := tmux.SpawnWithEnv(r.Context(), tmuxName, req.Cwd, launchCmd, envVars)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to spawn session: %v", err), http.StatusInternalServerError)
		return
	}

	if strings.TrimSpace(req.Prompt) != "" && req.EngineType == EngineTmux && !useCLIInitPrompt {
		go func(sessName, promptText string) {
			time.Sleep(1500 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tmux.SendInput(ctx, sessName, promptText, true)
		}(tmuxName, req.Prompt)
	}

	// Insert temporary spawning session
	now := time.Now().UTC()
	sess := &Session{
		ID:                fmt.Sprintf("%s:%s:%s", req.Agent, effectiveHost, tempUUID),
		Agent:             req.Agent,
		Host:              effectiveHost,
		NativeID:          tempUUID,
		Cwd:               req.Cwd,
		ProjectKey:        GetProjectKey(req.Cwd),
		NodePath:          req.NodePath,
		Name:              req.Name,
		IsSupervisor:      req.IsSupervisor,
		CurrentTaskID:     req.TaskID,
		Managed:           true,
		TmuxName:          tmuxName,
		EngineType:        EngineTmux,
		State:             StateUnknown,
		Activity:          "Spawning session...",
		StartedAt:         now,
		LastEventAt:       now,
		MetadataUpdatedAt: now,
		AccountID:         accountID,
	}
	if req.Name != "" {
		sess.CustomTitle = req.Name
	} else if strings.TrimSpace(req.Prompt) != "" {
		sess.Name = TruncateTitle(strings.TrimSpace(req.Prompt))
	}

	if pid, err := tmux.GetPID(r.Context(), tmuxName); err == nil {
		sess.PID = pid
	}

	if err := s.db.SaveSession(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.TaskID != "" {
		s.activateTaskForSpawnedWorker(req.TaskID, sess.ID, req.Agent, effectiveHost, req.RepoNodePath, now)
	}

	if s.db != nil {
		snap := s.CollectLocalHostLoad()
		_ = s.db.RecordHostLoadSample(effectiveHost, snap, 1, now)
	}

	s.broadcast(sess)

	respPayload := map[string]interface{}{
		"status":         "spawning",
		"session_id":     tempUUID,
		"id":             sess.ID,
		"host":           effectiveHost,
		"engine_type":    EngineTmux,
		"session":        sess,
		"routing_reason": routingDecision.Reason,
	}
	if routingDecision.LoadWarning != "" {
		respPayload["load_warning"] = routingDecision.LoadWarning
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(respPayload)
}

func (s *Server) handleBindSupervisor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req struct {
		SessionID       string `json:"session_id"`
		ID              string `json:"id"`
		NodePath        string `json:"node_path"`
		ProjectNodePath string `json:"project_node_path"`
		Title           string `json:"title"`
		CustomTitle     string `json:"custom_title"`
	}
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
			return
		}
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(req.ID)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(r.URL.Query().Get("session_id"))
	}

	nodePath := strings.TrimSpace(req.NodePath)
	if nodePath == "" {
		nodePath = strings.TrimSpace(req.ProjectNodePath)
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(req.CustomTitle)
	}

	if sessionID == "" || nodePath == "" {
		http.Error(w, "session_id and node_path are required", http.StatusBadRequest)
		return
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return "/v1/sessions/bind-supervisor"
	})
	if proxied {
		return
	}

	now := time.Now().UTC()
	if existingNode, _ := s.db.GetNode(nodePath); existingNode == nil {
		newNode := &TreeNode{
			Path:      nodePath,
			CreatedAt: now,
			UpdatedAt: now,
		}
		_ = s.db.SaveNode(newNode)
		s.pushSyncPayload(&SyncStatePayload{Nodes: []TreeNode{*newNode}})
	}

	sess.IsSupervisor = true
	sess.NodePath = nodePath
	if title != "" {
		sess.Name = title
		sess.CustomTitle = title
		cacheKey := fmt.Sprintf("%s:%s", sess.Cwd, sess.NativeID)
		titleCacheMutex.Lock()
		titleCache[cacheKey] = TitleCacheEntry{
			Title:     title,
			Source:    "custom",
			UpdatedAt: now,
		}
		titleCacheMutex.Unlock()
	}
	sess.MetadataUpdatedAt = now

	if err := s.db.SaveSession(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.headless != nil {
		s.headless.NotifyConfigChanged(sess)
	}

	s.broadcast(sess)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "bound",
		"id":      sess.ID,
		"session": sess,
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

func appendCLIInitPrompt(agent, baseCmd string) string {
	if baseCmd == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude-code", "claude":
		return baseCmd + ` "$ACKBAR_INIT_PROMPT"`
	case "antigravity", "agy":
		return baseCmd + ` -i "$ACKBAR_INIT_PROMPT"`
	default:
		return ""
	}
}

func (s *Server) activateTaskForSpawnedWorker(taskID, sessionID, agent, host, repoNodePath string, now time.Time) {
	if taskID == "" || s.db == nil {
		return
	}
	if sessionID != "" {
		_ = s.db.InsertTaskWorker(&TaskWorker{
			TaskID:       taskID,
			SessionID:    sessionID,
			Agent:        agent,
			Host:         host,
			IsActive:     true,
			RepoNodePath: repoNodePath,
			WorkflowNode: "N0",
			AssignedAt:   now,
		})
	}
	if task, err := s.db.GetTaskByID(taskID); err == nil && task != nil {
		taskChanged := false
		if task.Status == "" || strings.EqualFold(task.Status, "NEW") {
			task.Status = "IN_PROGRESS"
			if task.Substatus == "" || task.Substatus == "queued" || task.Substatus == "ready" {
				task.Substatus = "active"
			}
			taskChanged = true
		}
		for i := range task.Workstreams {
			ws := &task.Workstreams[i]
			if (repoNodePath == "" || len(task.Workstreams) == 1 || strings.EqualFold(ws.RepoNodePath, repoNodePath)) &&
				(ws.Status == "" || strings.EqualFold(ws.Status, "NEW")) {
				ws.Status = "IN_PROGRESS"
				ws.UpdatedAt = now
				taskChanged = true
			}
		}
		if taskChanged {
			task.UpdatedAt = now
			_ = s.db.UpdateTask(task)
		}
		s.pushSyncPayload(&SyncStatePayload{
			Tasks:       []Task{*task},
			Workstreams: task.Workstreams,
		})
	}
}

func (s *Server) buildTmuxSessionEnv(sess *Session, home string) map[string]string {
	if sess == nil {
		return nil
	}
	if home == "" {
		home = s.effectiveHomeDir()
	}
	_, envVars := s.resolveAccountEnv(sess.Agent, sess.AccountID, home, false)
	if envVars == nil {
		envVars = make(map[string]string)
	}
	if sess.ID != "" {
		envVars["ACKBAR_SESSION_ID"] = sess.ID
	}
	if sess.IsSupervisor {
		envVars["ACKBAR_IS_SUPERVISOR"] = "1"
	}
	taskKind := "code"
	if sess.CurrentTaskID != "" {
		envVars["ACKBAR_TASK_ID"] = sess.CurrentTaskID
		if s.db != nil {
			if t, _ := s.db.GetTaskByID(sess.CurrentTaskID); t != nil && t.TaskKind != "" {
				cand := strings.ToLower(strings.TrimSpace(t.TaskKind))
				if cand == "design" || cand == "code" {
					taskKind = cand
				}
			}
		}
	}
	envVars["ACKBAR_TASK_KIND"] = taskKind
	return envVars
}

func (s *Server) getRestartOrResumeCmd(sess *Session, home string) (string, bool) {
	if sess == nil || sess.Agent == "" {
		return "", false
	}
	if home == "" {
		home = s.effectiveHomeDir()
	}
	hasDisk := hasConversationOnDisk(sess, home, s.db)
	var isExisting bool
	if sess.Agent == "claude-code" {
		isExisting = hasDisk
	} else {
		isExisting = hasDisk || sess.FirstPrompt != "" || sess.LastPrompt != "" || sess.OldID != "" || !sess.Managed
	}
	if isExisting {
		if cmd := s.getResumeCmd(sess.Agent, sess.NativeID); cmd != "" {
			return cmd, true
		}
	}
	if cmd := s.getSpawnCmd(sess.Agent, sess.NativeID); cmd != "" {
		return cmd, false
	}
	return s.getResumeCmd(sess.Agent, sess.NativeID), isExisting
}

func terminateExternalPID(pid int, grace time.Duration) {
	if pid <= 1 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	if isProcessAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
