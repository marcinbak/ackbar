package daemon

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func grokStr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func LoadGrokSubagents(home, cwd, nativeID string) ([]SubagentInfo, error) {
	if !IsSafeGrokSessionID(nativeID) {
		return nil, nil
	}
	sessDir := FindGrokSessionDirForHome(home, cwd, nativeID)
	if sessDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(sessDir, "subagents"))
	if err != nil {
		return nil, nil
	}

	var results []SubagentInfo
	seen := make(map[string]bool)

	parseItem := func(childID, role, name, prompt, model string, startedAt time.Time, termStatus string) {
		if !IsSafeGrokSessionID(childID) || seen[childID] {
			return
		}
		seen[childID] = true
		status := "running"
		st := strings.ToLower(strings.TrimSpace(termStatus))
		if isTerminalStatus(st) {
			status = "completed"
		} else if childDir := FindGrokSessionDirForHome(home, "", childID); childDir != "" {
			if sumData, sErr := os.ReadFile(filepath.Join(childDir, "summary.json")); sErr == nil {
				var sum map[string]interface{}
				if json.Unmarshal(sumData, &sum) == nil && isTerminalStatus(grokStr(sum, "status", "state")) {
					status = "completed"
				}
			}
			if status == "running" {
				upPath := filepath.Join(childDir, "updates.jsonl")
				if fi, statErr := os.Stat(upPath); statErr == nil {
					if time.Since(fi.ModTime()) > 15*time.Minute {
						status = "completed"
					} else if tail := readTailBytes(upPath, 64<<10); len(tail) > 0 {
						lines := strings.Split(strings.TrimSpace(string(tail)), "\n")
						for i := len(lines) - 1; i >= 0; i-- {
							var ev map[string]interface{}
							if json.Unmarshal([]byte(strings.TrimSpace(lines[i])), &ev) == nil {
								t, sr := grokStr(ev, "type"), grokStr(ev, "stop_reason")
								if t == "turn_completed" || t == "completed" || sr == "end_turn" || sr == "cancelled" {
									status = "completed"
									break
								}
							}
						}
					}
				}
			}
		}

		dispName := name
		if dispName == "" {
			dispName = role
		}
		if dispName == "" {
			dispName = childID
		}
		results = append(results, SubagentInfo{ID: childID, Name: dispName, Role: role, Prompt: prompt, State: status, StartedAt: startedAt})
	}

	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join(sessDir, "subagents", name)
		if entry.IsDir() {
			metaFile := filepath.Join(fullPath, "meta.json")
			if !fileExists(metaFile) {
				metaFile = filepath.Join(fullPath, "summary.json")
			}
			if data, err := os.ReadFile(metaFile); err == nil {
				var m map[string]interface{}
				if json.Unmarshal(data, &m) == nil {
					id := grokStr(m, "session_id", "sessionId")
					if id == "" {
						id = name
					}
					ts := parseGrokTime(m["created_at"])
					if ts.IsZero() {
						if fi, _ := entry.Info(); fi != nil {
							ts = fi.ModTime()
						}
					}
					parseItem(id, grokStr(m, "subagent_type", "role"), grokStr(m, "name", "title"), grokStr(m, "prompt"), grokStr(m, "model"), ts, grokStr(m, "status"))
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
						id := grokStr(item, "id", "session_id")
						parseItem(id, grokStr(item, "role"), grokStr(item, "name"), grokStr(item, "prompt"), "", parseGrokTime(item["created_at"]), grokStr(item, "status"))
					}
				}
				continue
			}

			var m map[string]interface{}
			if json.Unmarshal(data, &m) == nil {
				id := grokStr(m, "session_id", "sessionId", "child_session_id")
				if id == "" {
					id = baseID
				}
				ts := parseGrokTime(m["created_at"])
				if ts.IsZero() {
					if fi, _ := entry.Info(); fi != nil {
						ts = fi.ModTime()
					}
				}
				parseItem(id, grokStr(m, "subagent_type", "role"), grokStr(m, "name", "title"), grokStr(m, "prompt"), grokStr(m, "model"), ts, grokStr(m, "status"))
			}
		}
	}
	return results, nil
}

func isTerminalStatus(st string) bool {
	s := strings.ToLower(strings.TrimSpace(st))
	return s == "completed" || s == "done" || s == "failed" || s == "cancelled" || s == "error" || s == "stopped" || s == "ended"
}

func readTailBytes(path string, maxBytes int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return nil
	}
	offset := int64(0)
	if st.Size() > maxBytes {
		offset = st.Size() - maxBytes
	}
	_, _ = f.Seek(offset, io.SeekStart)
	data, _ := io.ReadAll(f)
	return data
}

func findGrokSubagentParentPattern(home, nativeID string) string {
	patterns := []string{
		filepath.Join(GrokHomeDir(home), "sessions", "*", "*", "subagents", nativeID+".*"),
		filepath.Join(GrokHomeDir(home), "sessions", "*", "*", "subagents", nativeID),
		filepath.Join(home, ".grok-profiles", "*", "sessions", "*", "*", "subagents", nativeID+".*"),
		filepath.Join(home, ".grok-profiles", "*", "sessions", "*", "*", "subagents", nativeID),
	}
	for _, p := range patterns {
		if matches, err := filepath.Glob(p); err == nil {
			for _, m := range matches {
				parentNativeID := filepath.Base(filepath.Dir(filepath.Dir(m)))
				if IsSafeGrokSessionID(parentNativeID) {
					return parentNativeID
				}
			}
		}
	}
	return ""
}

func ResolveGrokSubagentParent(home, nativeID string) (parentID, role, typeName string) {
	if !IsSafeGrokSessionID(nativeID) {
		return "", "", ""
	}
	if childDir := FindGrokSessionDirForHome(home, "", nativeID); childDir != "" {
		if data, err := os.ReadFile(filepath.Join(childDir, "summary.json")); err == nil {
			var m map[string]interface{}
			if json.Unmarshal(data, &m) == nil {
				if pid := grokStr(m, "parent_session_id", "parentSessionId"); pid != "" {
					return pid, grokStr(m, "role"), grokStr(m, "subagent_type")
				}
			}
		}
	}
	if pID := findGrokSubagentParentPattern(home, nativeID); pID != "" {
		return pID, "subagent", "subagent"
	}
	return "", "", ""
}

func (a GrokAgentAdapter) IsSubagentSession(home, nativeID string, precomputed map[string]bool) bool {
	if !IsSafeGrokSessionID(nativeID) {
		return false
	}
	if precomputed != nil && precomputed[nativeID] {
		return true
	}

	if childDir := FindGrokSessionDirForHome(home, "", nativeID); childDir != "" {
		if data, err := os.ReadFile(filepath.Join(childDir, "summary.json")); err == nil {
			var m map[string]interface{}
			if json.Unmarshal(data, &m) == nil {
				if pid := grokStr(m, "parent_session_id", "parentSessionId", "subagent_type", "subagentType"); pid != "" {
					return true
				}
				if isSub, ok := m["is_subagent"].(bool); ok && isSub {
					return true
				}
			}
		}
	}

	return findGrokSubagentParentPattern(home, nativeID) != ""
}
