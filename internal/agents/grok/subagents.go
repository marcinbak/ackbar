package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) ListSubagents(homeDir, cwd, nativeID string) ([]agent.SubagentInfo, error) {
	if !agentutil.IsSafeGrokSessionID(nativeID) {
		return nil, nil
	}
	sessDir := FindSessionDirForHome(homeDir, cwd, nativeID)
	if sessDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(sessDir, "subagents"))
	if err != nil {
		return nil, nil
	}

	var results []agent.SubagentInfo
	seen := make(map[string]bool)

	parseItem := func(childID, role, name, prompt string, startedAt time.Time, termStatus string) {
		if !agentutil.IsSafeGrokSessionID(childID) || seen[childID] {
			return
		}
		seen[childID] = true
		status := "running"
		if isTerminalStatus(termStatus) {
			status = "completed"
		} else if childDir := FindSessionDirForHome(homeDir, "", childID); childDir != "" {
			if sumData, sErr := os.ReadFile(filepath.Join(childDir, "summary.json")); sErr == nil {
				var sum map[string]interface{}
				if json.Unmarshal(sumData, &sum) == nil && isTerminalStatus(firstMapStr(sum, "status", "state")) {
					status = "completed"
				}
			}
			if status == "running" && childUpdatesCompleted(filepath.Join(childDir, "updates.jsonl")) {
				status = "completed"
			}
		}

		dispName := name
		if dispName == "" {
			dispName = role
		}
		if dispName == "" {
			dispName = childID
		}
		results = append(results, agent.SubagentInfo{
			ID: childID, Name: dispName, Role: role, Prompt: prompt, State: status, StartedAt: startedAt,
		})
	}

	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join(sessDir, "subagents", name)
		if entry.IsDir() {
			metaFile := filepath.Join(fullPath, "meta.json")
			if !agentutil.FileExists(metaFile) {
				metaFile = filepath.Join(fullPath, "summary.json")
			}
			if data, err := os.ReadFile(metaFile); err == nil {
				var m map[string]interface{}
				if json.Unmarshal(data, &m) == nil {
					id := firstMapStr(m, "session_id", "sessionId")
					if id == "" {
						id = name
					}
					ts := parseGrokTime(m["created_at"])
					if ts.IsZero() {
						if fi, _ := entry.Info(); fi != nil {
							ts = fi.ModTime()
						}
					}
					parseItem(id, firstMapStr(m, "subagent_type", "role"), firstMapStr(m, "name", "title"), firstMapStr(m, "prompt"), ts, firstMapStr(m, "status"))
				}
			}
			continue
		}

		if strings.HasSuffix(name, ".meta.json") || strings.HasSuffix(name, ".json") {
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			baseID := strings.TrimSuffix(strings.TrimSuffix(name, ".meta.json"), ".json")
			if baseID == "meta" {
				var arr []map[string]interface{}
				if json.Unmarshal(data, &arr) == nil {
					for _, item := range arr {
						parseItem(firstMapStr(item, "id", "session_id"), firstMapStr(item, "role"), firstMapStr(item, "name"), firstMapStr(item, "prompt"), parseGrokTime(item["created_at"]), firstMapStr(item, "status"))
					}
				}
				continue
			}

			var m map[string]interface{}
			if json.Unmarshal(data, &m) == nil {
				id := firstMapStr(m, "session_id", "sessionId", "child_session_id")
				if id == "" {
					id = baseID
				}
				ts := parseGrokTime(m["created_at"])
				if ts.IsZero() {
					if fi, _ := entry.Info(); fi != nil {
						ts = fi.ModTime()
					}
				}
				parseItem(id, firstMapStr(m, "subagent_type", "role"), firstMapStr(m, "name", "title"), firstMapStr(m, "prompt"), ts, firstMapStr(m, "status"))
			}
		}
	}
	return results, nil
}

func childUpdatesCompleted(upPath string) bool {
	fi, err := os.Stat(upPath)
	if err != nil {
		return false
	}
	if time.Since(fi.ModTime()) > 15*time.Minute {
		return true
	}
	tail, err := agentutil.ReadTail(upPath, 64<<10)
	if err != nil || len(tail) == 0 {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(tail)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(lines[i])), &ev) != nil {
			continue
		}
		targetMap := ev
		if params, ok := ev["params"].(map[string]interface{}); ok && params != nil {
			if upd, ok := params["update"].(map[string]interface{}); ok && upd != nil {
				targetMap = upd
			}
		}
		t := firstMapStr(targetMap, "sessionUpdate", "type")
		if t == "" {
			t = firstMapStr(ev, "type")
		}
		sr := firstMapStr(targetMap, "stop_reason", "stopReason")
		if sr == "" {
			sr = firstMapStr(ev, "stop_reason")
		}
		if t == "turn_completed" || t == "completed" || sr == "end_turn" || sr == "cancelled" {
			return true
		}
	}
	return false
}

func isTerminalStatus(st string) bool {
	switch strings.ToLower(strings.TrimSpace(st)) {
	case "completed", "done", "failed", "cancelled", "error", "stopped", "ended":
		return true
	}
	return false
}

func findSubagentParentPattern(home, nativeID string) string {
	patterns := []string{
		filepath.Join(HomeDir(home), "sessions", "*", "*", "subagents", nativeID+".*"),
		filepath.Join(HomeDir(home), "sessions", "*", "*", "subagents", nativeID),
		filepath.Join(home, ".grok-profiles", "*", "sessions", "*", "*", "subagents", nativeID+".*"),
		filepath.Join(home, ".grok-profiles", "*", "sessions", "*", "*", "subagents", nativeID),
	}
	for _, p := range patterns {
		if matches, err := filepath.Glob(p); err == nil {
			for _, m := range matches {
				parentNativeID := filepath.Base(filepath.Dir(filepath.Dir(m)))
				if agentutil.IsSafeGrokSessionID(parentNativeID) && parentNativeID != nativeID {
					return parentNativeID
				}
			}
		}
	}
	return ""
}

func ResolveSubagentParent(home, nativeID string) (parentID, role, typeName string) {
	if !agentutil.IsSafeGrokSessionID(nativeID) {
		return "", "", ""
	}
	if childDir := FindSessionDirForHome(home, "", nativeID); childDir != "" {
		if data, err := os.ReadFile(filepath.Join(childDir, "summary.json")); err == nil {
			var m map[string]interface{}
			if json.Unmarshal(data, &m) == nil {
				if pid := firstMapStr(m, "parent_session_id", "parentSessionId"); pid != "" && agentutil.IsSafeGrokSessionID(pid) && pid != nativeID {
					return pid, firstMapStr(m, "role"), firstMapStr(m, "subagent_type")
				}
			}
		}
	}
	if pID := findSubagentParentPattern(home, nativeID); pID != "" {
		return pID, "subagent", "subagent"
	}
	return "", "", ""
}

func (*Plugin) IsSubagentSession(homeDir, nativeID string, precomputed map[string]bool) bool {
	if !agentutil.IsSafeGrokSessionID(nativeID) {
		return false
	}
	if precomputed != nil && precomputed[nativeID] {
		return true
	}
	if childDir := FindSessionDirForHome(homeDir, "", nativeID); childDir != "" {
		if data, err := os.ReadFile(filepath.Join(childDir, "summary.json")); err == nil {
			var m map[string]interface{}
			if json.Unmarshal(data, &m) == nil {
				if pid := firstMapStr(m, "parent_session_id", "parentSessionId", "subagent_type", "subagentType"); pid != "" {
					return true
				}
				if isSub, ok := m["is_subagent"].(bool); ok && isSub {
					return true
				}
			}
		}
	}
	return findSubagentParentPattern(homeDir, nativeID) != ""
}
