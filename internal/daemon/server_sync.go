package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

func (s *Server) handleSyncState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var since time.Time
	if sStr := r.URL.Query().Get("since"); sStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, sStr); err == nil {
			since = t
		} else if t, err := time.Parse(time.RFC3339, sStr); err == nil {
			since = t
		}
	}

	state, err := s.db.GetSyncStateSince(since, s.HostName())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (s *Server) handleSyncPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024)
	var payload SyncStatePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	updatedSessions, err := s.db.ApplySyncState(&payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, sess := range updatedSessions {
		s.broadcast(sess)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":           "synced",
		"updated_sessions": len(updatedSessions),
	})
}

func (s *Server) handleNodeHostPaths(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		nodePath := r.URL.Query().Get("node_path")
		paths, err := s.db.ListNodeHostPaths(nodePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(paths)

	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)
		var nhp NodeHostPath
		if err := json.NewDecoder(r.Body).Decode(&nhp); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		if nhp.NodePath == "" {
			http.Error(w, "Missing node_path", http.StatusBadRequest)
			return
		}
		if nhp.Host == "" {
			nhp.Host = s.HostName()
		}
		nhp.UpdatedAt = time.Now().UTC()

		if err := s.db.SaveNodeHostPath(&nhp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.pushSyncPayload(&SyncStatePayload{
			NodeHostPaths: []NodeHostPath{nhp},
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "saved",
			"host_path": nhp,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) pushSyncPayload(payload *SyncStatePayload) {
	if payload == nil || s.db == nil {
		return
	}
	payload.SourceHost = s.HostName()
	if payload.Timestamp.IsZero() {
		payload.Timestamp = time.Now().UTC()
	}

	hosts, err := s.db.ListHosts()
	if err != nil || len(hosts) == 0 {
		return
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}

	token := s.configuredToken
	if token == "" {
		token = os.Getenv("ACKBAR_TOKEN")
	}

	for _, h := range hosts {
		if h.URL == "" || s.isLocalHost(h.Name) {
			continue
		}
		go func(peerURL, peerName string) {
			reqURL := strings.TrimSuffix(peerURL, "/") + "/v1/sync/push"
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payloadBytes))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-Ackbar-Token", token)
			}
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
		}(h.URL, h.Name)
	}
}

func (s *Server) pushSyncStateToPeer(host *HostRecord) {
	if host == nil || host.URL == "" || s.isLocalHost(host.Name) || s.db == nil {
		return
	}
	settingKey := fmt.Sprintf("last_push_%s", host.Name)
	var since time.Time
	if lastPushStr, _ := s.db.GetSetting(settingKey); lastPushStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, lastPushStr); err == nil {
			since = t
		} else if t, err := time.Parse(time.RFC3339, lastPushStr); err == nil {
			since = t
		}
	}
	state, err := s.db.GetSyncStateSince(since, s.HostName())
	if err != nil || state == nil {
		return
	}
	payloadBytes, _ := json.Marshal(state)
	reqURL := strings.TrimSuffix(host.URL, "/") + "/v1/sync/push"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	token := s.configuredToken
	if token == "" {
		token = os.Getenv("ACKBAR_TOKEN")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Ackbar-Token", token)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	_ = s.db.SetSettings(map[string]string{settingKey: state.Timestamp.UTC().Format(time.RFC3339Nano)})
}

func (s *Server) PullFromPeer(ctx context.Context, host *HostRecord) error {
	if host == nil || host.URL == "" || s.isLocalHost(host.Name) || s.db == nil {
		return nil
	}
	settingKey := fmt.Sprintf("last_sync_%s", host.Name)
	var since time.Time
	if lastSyncStr, _ := s.db.GetSetting(settingKey); lastSyncStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, lastSyncStr); err == nil {
			since = t
		} else if t, err := time.Parse(time.RFC3339, lastSyncStr); err == nil {
			since = t
		}
	}

	reqURL := strings.TrimSuffix(host.URL, "/") + "/v1/sync/state"
	if !since.IsZero() {
		reqURL += "?since=" + url.QueryEscape(since.UTC().Format(time.RFC3339Nano))
	}

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	token := s.configuredToken
	if token == "" {
		token = os.Getenv("ACKBAR_TOKEN")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Ackbar-Token", token)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer sync responded with status %d", resp.StatusCode)
	}

	var state SyncStatePayload
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return err
	}

	updatedSessions, err := s.db.ApplySyncState(&state)
	if err != nil {
		return err
	}
	for _, sess := range updatedSessions {
		s.broadcast(sess)
	}

	if !state.Timestamp.IsZero() {
		_ = s.db.SetSettings(map[string]string{settingKey: state.Timestamp.UTC().Format(time.RFC3339Nano)})
	}
	return nil
}

func (s *Server) PullFromPeers(ctx context.Context) {
	if s.db == nil {
		return
	}
	hosts, err := s.db.ListHosts()
	if err != nil || len(hosts) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, h := range hosts {
		if h.URL == "" || s.isLocalHost(h.Name) {
			continue
		}
		wg.Add(1)
		go func(hr *HostRecord) {
			defer wg.Done()
			_ = s.PullFromPeer(ctx, hr)
		}(h)
	}
	wg.Wait()
}

func (s *Server) PullFromPeersWithTimeout(ctx context.Context, timeout time.Duration) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s.PullFromPeers(ctxWithTimeout)
}

func sessionToMetaSync(sess *Session) SessionMetaSync {
	if sess == nil {
		return SessionMetaSync{}
	}
	upd := sess.MetadataUpdatedAt
	if upd.IsZero() {
		upd = sess.LastEventAt
	}
	if upd.IsZero() {
		upd = time.Now().UTC()
	}
	return SessionMetaSync{
		ID:          sess.ID,
		Agent:       sess.Agent,
		Host:        sess.Host,
		NativeID:    sess.NativeID,
		Cwd:         sess.Cwd,
		NodePath:    sess.NodePath,
		Name:        sess.Name,
		CustomTitle: sess.CustomTitle,
		IsDone:      sess.IsDone,
		IsLater:     sess.IsLater,
		Archived:    sess.Archived,
		State:       sess.State,
		Activity:    sess.Activity,
		GitBranch:   sess.GitBranch,
		StartedAt:   sess.StartedAt,
		LastEventAt: sess.LastEventAt,
		UpdatedAt:   upd,
	}
}

func (s *Server) syncSessionMeta(sess *Session) {
	if sess == nil {
		return
	}
	s.pushSyncPayload(&SyncStatePayload{
		SessionMetas: []SessionMetaSync{sessionToMetaSync(sess)},
	})
}
