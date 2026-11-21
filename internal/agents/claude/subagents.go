package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) IsSubagentSession(_, nativeID string, _ map[string]bool) bool {
	return strings.HasPrefix(nativeID, "agent-")
}

type diskSubagentRecord struct {
	id        string
	name      string
	role      string
	prompt    string
	model     string
	state     string
	startedAt time.Time
}

func subagentDirs(home, cwd, nativeID string) []string {
	var dirs []string
	add := func(d string) {
		for _, ex := range dirs {
			if ex == d {
				return
			}
		}
		dirs = append(dirs, d)
	}
	if cwd != "" {
		add(filepath.Join(home, ".claude", "projects", encodeProjectDir(cwd), nativeID, "subagents"))
		add(filepath.Join(home, ".claude", "projects", strings.ReplaceAll(cwd, "/", "-"), nativeID, "subagents"))
	}
	if matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", nativeID, "subagents")); err == nil {
		for _, m := range matches {
			add(m)
		}
	}
	return dirs
}

func discoverDiskSubagents(home, cwd, nativeID string, staleDefaultCompleted bool) ([]diskSubagentRecord, map[string]bool) {
	seen := make(map[string]bool)
	var records []diskSubagentRecord
	for _, subDir := range subagentDirs(home, cwd, nativeID) {
		entries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
				continue
			}
			baseName := strings.TrimSuffix(entry.Name(), ".meta.json")
			if seen[baseName] {
				continue
			}
			seen[baseName] = true
			data, err := os.ReadFile(filepath.Join(subDir, entry.Name()))
			if err != nil {
				continue
			}
			var meta struct {
				Name            string `json:"name"`
				AgentType       string `json:"agentType"`
				CustomAgentType string `json:"customAgentType"`
				Description     string `json:"description"`
				Model           string `json:"model"`
				JoinedAt        int64  `json:"joinedAt"`
			}
			if json.Unmarshal(data, &meta) != nil {
				continue
			}
			name := firstNonEmpty(meta.Name, meta.AgentType, baseName)
			if meta.Name != "" {
				seen[meta.Name] = true
			}
			seen[name] = true
			role := firstNonEmpty(meta.CustomAgentType, meta.AgentType, name)

			startTime := time.Now()
			modTime := startTime
			if info, err := entry.Info(); err == nil {
				modTime = info.ModTime()
				startTime = modTime
			}
			if meta.JoinedAt > 0 {
				startTime = time.UnixMilli(meta.JoinedAt)
			}

			jsonlPath := filepath.Join(subDir, baseName+".jsonl")
			jInfo, statErr := os.Stat(jsonlPath)
			if statErr == nil {
				modTime = jInfo.ModTime()
			}

			state := "running"
			if time.Since(modTime) >= 30*time.Minute {
				state = "completed"
			}
			if !staleDefaultCompleted || state == "running" {
				if sr, ok := readTailStopReason(jsonlPath, statErr == nil && jInfo.Size() > 65536); ok {
					if sr == "end_turn" || sr == "stop_sequence" || sr == "stop" || time.Since(modTime) >= 30*time.Minute {
						state = "completed"
					} else {
						state = "running"
					}
				}
			}

			records = append(records, diskSubagentRecord{
				id: baseName, name: name, role: role, prompt: meta.Description, model: meta.Model, state: state, startedAt: startTime,
			})
		}
	}
	return records, seen
}

func readTailStopReason(jsonlPath string, isTruncatedHead bool) (string, bool) {
	data, _, _, err := readRegularFileSlice(jsonlPath, -1, 65536)
	if err != nil || len(data) == 0 {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	minIdx := 0
	if isTruncatedHead && len(lines) > 1 {
		minIdx = 1
	}
	for i := len(lines) - 1; i >= minIdx; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var step struct {
			StopReason string `json:"stop_reason"`
			Message    struct {
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &step) == nil {
			sr := firstNonEmpty(step.StopReason, step.Message.StopReason)
			if sr != "" {
				return sr, true
			}
		}
	}
	return "", false
}

func (*Plugin) ListActiveSubagents(homeDir, cwd, nativeID string) ([]*agent.ActiveSubagent, error) {
	if !agentutil.IsUUID(nativeID) {
		return nil, nil
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return nil, nil
	}
	diskRecs, seen := discoverDiskSubagents(home, cwd, nativeID, true)
	results := make([]*agent.ActiveSubagent, 0, len(diskRecs))
	for _, r := range diskRecs {
		results = append(results, &agent.ActiveSubagent{
			ID: r.id, Name: r.name, Role: r.role, Prompt: r.prompt, Model: r.model, State: r.state, StartedAt: r.startedAt,
		})
	}

	if matches, err := filepath.Glob(filepath.Join(home, ".claude", "teams", "*", "config.json")); err == nil {
		for _, cfgPath := range matches {
			data, err := os.ReadFile(cfgPath)
			if err != nil {
				continue
			}
			var teamCfg struct {
				Name          string `json:"name"`
				LeadSessionID string `json:"leadSessionId"`
				Members       []struct {
					AgentID   string `json:"agentId"`
					Name      string `json:"name"`
					AgentType string `json:"agentType"`
					Prompt    string `json:"prompt"`
					Model     string `json:"model"`
					JoinedAt  int64  `json:"joinedAt"`
				} `json:"members"`
			}
			if json.Unmarshal(data, &teamCfg) != nil {
				continue
			}
			if teamCfg.LeadSessionID != nativeID && (len(nativeID) < 8 || teamCfg.Name != "session-"+nativeID[:8]) {
				continue
			}
			for _, mem := range teamCfg.Members {
				if mem.Name == "team-lead" || seen[mem.Name] || seen[mem.AgentID] {
					continue
				}
				subID := firstNonEmpty(mem.AgentID, mem.Name)
				seen[subID], seen[mem.Name] = true, true
				st := time.Now()
				if mem.JoinedAt > 0 {
					st = time.UnixMilli(mem.JoinedAt)
				}
				results = append(results, &agent.ActiveSubagent{
					ID: subID, Name: mem.Name, Role: mem.AgentType, Prompt: mem.Prompt, Model: mem.Model, State: "completed", StartedAt: st,
				})
			}
		}
	}
	return results, nil
}
