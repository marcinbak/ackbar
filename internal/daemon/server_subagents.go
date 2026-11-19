package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func (s *Server) handleSessionSubagents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("session_id")
	}
	if sessionID == "" {
		http.Error(w, "Missing id or session_id parameter", http.StatusBadRequest)
		return
	}

	sess := s.resolveSession(sessionID)
	if sess == nil {
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 2 {
			if hostRec := s.resolveHost(parts[1]); hostRec != nil && hostRec.URL != "" {
				targetURL := fmt.Sprintf("%s/v1/sessions/subagents?%s", strings.TrimSuffix(hostRec.URL, "/"), r.URL.RawQuery)
				if proxyGETWithAuth(w, r, targetURL) {
					return
				}
			}
		}
	} else if hostRec := s.resolveHost(sess.Host); hostRec != nil && hostRec.URL != "" {
		targetURL := fmt.Sprintf("%s/v1/sessions/subagents?%s", strings.TrimSuffix(hostRec.URL, "/"), r.URL.RawQuery)
		if proxyGETWithAuth(w, r, targetURL) {
			return
		}
	}

	agent := "claude-code"
	nativeID := sessionID
	cwd := ""

	if sess != nil {
		agent = sess.Agent
		nativeID = sess.NativeID
		cwd = sess.Cwd
	} else {
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 3 {
			agent = parts[0]
			nativeID = parts[2]
		}
	}

	running := s.getRunningSubagents(sessionID, nativeID, agent, cwd)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"session_id":    sessionID,
		"native_id":     nativeID,
		"running_count": len(running),
		"subagents":     running,
	})
}

func (s *Server) getRunningSubagents(sessionID, nativeID, agent, cwd string) []SubagentInfo {
	// 1. Query Provider interface abstraction (source of truth on disk)
	var diskSubs []SubagentInfo
	var hasDiscoverer bool

	cacheKey := agent + ":" + nativeID + ":" + cwd
	s.subagentDiskMu.RLock()
	if s.subagentDiskCache != nil {
		if entry, ok := s.subagentDiskCache[cacheKey]; ok && time.Since(entry.cachedAt) < 3*time.Second {
			diskSubs = make([]SubagentInfo, len(entry.subagents))
			copy(diskSubs, entry.subagents)
			hasDiscoverer = true
			s.subagentDiskMu.RUnlock()
			goto reconcile
		}
	}
	s.subagentDiskMu.RUnlock()

	if p, ok := s.resolveProvider(agent); ok {
		if discoverer, ok := p.(SubagentDiscoverer); ok {
			supportsDisk := true
			if gated, hasGate := p.(interface{ SupportsSubagentDiscovery() bool }); hasGate {
				supportsDisk = gated.SupportsSubagentDiscovery()
			}
			if supportsDisk {
				hasDiscoverer = true
				home, _ := os.UserHomeDir()
				if activeSubs, err := discoverer.ListSubagents(home, cwd, nativeID); err == nil {
					for _, sub := range activeSubs {
						if sub != nil && sub.State == "running" {
							diskSubs = append(diskSubs, SubagentInfo{
								ID:        sub.ID,
								Name:      sub.Name,
								Role:      sub.Role,
								Prompt:    sub.Prompt,
								State:     sub.State,
								StartedAt: sub.StartedAt,
							})
						}
					}
				}
			}
		}
	}

	if hasDiscoverer {
		s.subagentDiskMu.Lock()
		if s.subagentDiskCache == nil {
			s.subagentDiskCache = make(map[string]*subagentDiskCacheEntry)
		}
		s.subagentDiskCache[cacheKey] = &subagentDiskCacheEntry{
			subagents: diskSubs,
			cachedAt:  time.Now(),
		}
		s.subagentDiskMu.Unlock()
	}

reconcile:

	// 2. Query and reconcile in-memory subagents
	s.subagentsMu.Lock()
	var inMem []*SubagentInfo
	targetKey := sessionID
	if list, ok := s.activeSubagents[sessionID]; ok && len(list) > 0 {
		inMem = list
	} else if nativeID != "" {
		if list, ok := s.activeSubagents[nativeID]; ok && len(list) > 0 {
			inMem = list
			targetKey = nativeID
		}
	}

	var activeInMem []*SubagentInfo
	var freshRunningInMem []SubagentInfo
	for _, sub := range inMem {
		if sub == nil {
			continue
		}
		// If disk discoverer checked disk and found no running subagents,
		// prune stale in-memory subagents older than 2 minutes.
		// If no discoverer exists, prune stale in-memory subagents older than 15 minutes.
		if hasDiscoverer && len(diskSubs) == 0 && time.Since(sub.StartedAt) > 2*time.Minute {
			continue
		} else if !hasDiscoverer && time.Since(sub.StartedAt) > 15*time.Minute {
			continue
		}
		if sub.State == "running" {
			freshRunningInMem = append(freshRunningInMem, *sub)
		}
		activeInMem = append(activeInMem, sub)
	}
	if len(activeInMem) != len(inMem) {
		s.activeSubagents[targetKey] = activeInMem
		if nativeID != "" && nativeID != targetKey {
			s.activeSubagents[nativeID] = activeInMem
		}
	}
	s.subagentsMu.Unlock()

	// If disk discovery found running subagents, return them (disk is source of truth)
	if len(diskSubs) > 0 {
		return diskSubs
	}

	// If disk discovery found none, return very recent in-memory subagents (<2m)
	if len(freshRunningInMem) > 0 {
		return freshRunningInMem
	}

	// 3. Fallback to plugin or transcript extraction
	var subs []SubagentInfo
	var err error
	if tr, ok := s.PluginRegistry().AsTranscript(agent); ok {
		home := s.effectiveHomeDir()
		subs, err = tr.ListSubagents(home, cwd, nativeID)
	} else {
		subs, err = ExtractSubagents(agent, nativeID, cwd)
	}
	if err == nil {
		var running []SubagentInfo
		for _, sub := range subs {
			if sub.State == "running" {
				running = append(running, sub)
			}
		}
		return running
	}
	return []SubagentInfo{}
}

func (s *Server) markSessionSubagentLifecycleActive(sessionID, nativeID string) {
	s.subagentsMu.Lock()
	defer s.subagentsMu.Unlock()
	for _, sub := range s.activeSubagents[sessionID] {
		if sub != nil && sub.Name != "" && !strings.EqualFold(sub.Name, sub.ToolName) {
			sub.Synthetic = false
		}
	}
	if nativeID != "" && nativeID != sessionID {
		for _, sub := range s.activeSubagents[nativeID] {
			if sub != nil && sub.Name != "" && !strings.EqualFold(sub.Name, sub.ToolName) {
				sub.Synthetic = false
			}
		}
	}
}

func (s *Server) addSubagent(sessionID, nativeID string, info *SubagentInfo) {
	if info == nil {
		return
	}
	s.subagentsMu.Lock()
	merged := false
	if !info.Synthetic {
		for _, sub := range s.activeSubagents[sessionID] {
			if sub != nil && sub.Synthetic && time.Since(sub.StartedAt) < 5*time.Second {
				if sub.Name == info.Name || sub.Role == info.Name || strings.EqualFold(sub.Name, sub.ToolName) || sub.Role == "" {
					sub.Name = info.Name
					sub.Role = info.Role
					sub.Synthetic = false
					merged = true
					break
				}
			}
		}
	}
	if !merged {
		s.activeSubagents[sessionID] = append(s.activeSubagents[sessionID], info)
		if nativeID != "" && nativeID != sessionID {
			s.activeSubagents[nativeID] = append(s.activeSubagents[nativeID], info)
		}
	}
	list := s.activeSubagents[sessionID]
	s.subagentsMu.Unlock()

	var running []*SubagentInfo
	for _, sub := range list {
		if sub != nil && sub.State == "running" {
			running = append(running, sub)
		}
	}

	if sess := s.resolveSession(sessionID); sess != nil {
		count := len(running)
		if s.headless != nil {
			if bg := s.headless.GetBgTaskCount(sess.ID); bg > count {
				count = bg
			}
		}
		sess.RunningSubagents = count
		_ = s.db.SaveSession(sess)
		s.broadcast(sess)
	}
	if s.headless != nil {
		s.headless.EmitSubagentsUpdate(sessionID, running)
	}
}

func (s *Server) removeSubagent(sessionID, nativeID, subagentID string) {
	s.removeSubagentWithMode(sessionID, nativeID, subagentID, false)
}

func (s *Server) removeHookSubagent(sessionID, nativeID, subagentID string) {
	s.removeSubagentWithMode(sessionID, nativeID, subagentID, true)
}

func (s *Server) removeSubagentWithMode(sessionID, nativeID, subagentID string, popFallback bool) {
	s.subagentsMu.Lock()
	updateList := func(key string) []*SubagentInfo {
		list := s.activeSubagents[key]
		var updated []*SubagentInfo
		found := false
		for _, sub := range list {
			if sub == nil {
				continue
			}
			if !found && subagentID != "" && (sub.ID == subagentID || sub.Name == subagentID || sub.Role == subagentID) {
				found = true
				continue
			}
			updated = append(updated, sub)
		}
		if !found && len(updated) > 0 {
			if subagentID == "" {
				updated = updated[1:]
			} else {
				var afterFallback []*SubagentInfo
				for _, sub := range updated {
					if !found && sub.Synthetic && strings.EqualFold(sub.ToolName, subagentID) {
						found = true
						continue
					}
					afterFallback = append(afterFallback, sub)
				}
				updated = afterFallback
				if !found && popFallback && len(updated) > 0 {
					updated = updated[1:]
				}
			}
		}
		s.activeSubagents[key] = updated
		return updated
	}

	var remaining []*SubagentInfo
	if sessionID != "" {
		remaining = updateList(sessionID)
	}
	if nativeID != "" && nativeID != sessionID {
		updateList(nativeID)
	}
	s.subagentsMu.Unlock()

	var running []*SubagentInfo
	for _, sub := range remaining {
		if sub != nil && sub.State == "running" {
			running = append(running, sub)
		}
	}

	if sess := s.resolveSession(sessionID); sess != nil {
		count := len(running)
		if s.headless != nil {
			if bg := s.headless.GetBgTaskCount(sess.ID); bg > count {
				count = bg
			}
		}
		sess.RunningSubagents = count
		_ = s.db.SaveSession(sess)
		s.broadcast(sess)
	}
	if s.headless != nil {
		s.headless.EmitSubagentsUpdate(sessionID, running)
	}
}

func (s *Server) clearInMemorySubagents(sessionID, nativeID string) {
	s.subagentsMu.Lock()
	delete(s.activeSubagents, sessionID)
	if nativeID != "" {
		delete(s.activeSubagents, nativeID)
	}
	s.subagentsMu.Unlock()
	if s.headless != nil {
		s.headless.EmitSubagentsUpdate(sessionID, []*SubagentInfo{})
	}
}

func (s *Server) clearSubagentDiskCache(sessionID, nativeID string) {
	s.subagentDiskMu.Lock()
	if s.subagentDiskCache != nil {
		for k := range s.subagentDiskCache {
			if strings.Contains(k, sessionID) || (nativeID != "" && strings.Contains(k, nativeID)) {
				delete(s.subagentDiskCache, k)
			}
		}
	}
	s.subagentDiskMu.Unlock()
}

func (s *Server) clearSubagents(sessionID, nativeID string) {
	s.clearSubagentDiskCache(sessionID, nativeID)
	s.clearInMemorySubagents(sessionID, nativeID)

	if sess := s.resolveSession(sessionID); sess != nil {
		sess.RunningSubagents = 0
		s.broadcast(sess)
	}
}

func (s *Server) pruneSyntheticSubagents(sessionID, nativeID string) {
	s.subagentsMu.Lock()
	filterKey := func(key string) []*SubagentInfo {
		if key == "" {
			return nil
		}
		var kept []*SubagentInfo
		for _, sub := range s.activeSubagents[key] {
			if sub != nil && !sub.Synthetic {
				kept = append(kept, sub)
			}
		}
		if len(kept) == 0 {
			delete(s.activeSubagents, key)
		} else {
			s.activeSubagents[key] = kept
		}
		return kept
	}
	kept := filterKey(sessionID)
	if nativeID != "" && nativeID != sessionID {
		filterKey(nativeID)
	}
	s.subagentsMu.Unlock()
	if s.headless != nil {
		if kept == nil {
			kept = []*SubagentInfo{}
		}
		s.headless.EmitSubagentsUpdate(sessionID, kept)
	}
}

func (s *Server) countInMemoryRunningSubagents(sessionID, nativeID string) int {
	s.subagentsMu.Lock()
	defer s.subagentsMu.Unlock()
	list := s.activeSubagents[sessionID]
	if len(list) == 0 && nativeID != "" {
		list = s.activeSubagents[nativeID]
	}
	count := 0
	for _, sub := range list {
		if sub != nil && sub.State == "running" {
			count++
		}
	}
	return count
}

func (s *Server) hasActiveSubagentStartSubs(sessionID, nativeID string) bool {
	s.subagentsMu.Lock()
	defer s.subagentsMu.Unlock()
	check := func(key string) bool {
		for _, sub := range s.activeSubagents[key] {
			if sub != nil && sub.State == "running" && !sub.Synthetic {
				return true
			}
		}
		return false
	}
	return check(sessionID) || (nativeID != "" && check(nativeID))
}

func (s *Server) onBgTaskCountChanged(sessionID, nativeID string, bgCount int) {
	sess := s.resolveSession(sessionID)
	if sess == nil && nativeID != "" {
		sess = s.resolveSession(nativeID)
	}
	if sess == nil {
		return
	}
	target := s.countInMemoryRunningSubagents(sess.ID, sess.NativeID)
	if bgCount > target {
		target = bgCount
	}
	if sess.RunningSubagents != target {
		sess.RunningSubagents = target
		s.broadcast(sess)
	}
}

func (s *Server) onHeadlessTurnComplete(sessionID, nativeID string) {
	s.pruneSyntheticSubagents(sessionID, nativeID)
	hasWarm := s.headless != nil && s.headless.HasWarmWorker(sessionID)
	if !hasWarm {
		s.clearSubagents(sessionID, nativeID)
		return
	}
	bgCount := s.headless.GetBgTaskCount(sessionID)
	hookCount := s.countInMemoryRunningSubagents(sessionID, nativeID)
	target := hookCount
	if bgCount > target {
		target = bgCount
	}
	if target == 0 {
		s.clearSubagents(sessionID, nativeID)
		return
	}
	if sess := s.resolveSession(sessionID); sess != nil && sess.RunningSubagents != target {
		sess.RunningSubagents = target
		s.broadcast(sess)
	}
}

func (s *Server) onHeadlessWorkerExited(sessionID, nativeID string) {
	s.clearSubagents(sessionID, nativeID)
}
