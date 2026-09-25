package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

func (c *ClaudeProvider) ListSubagents(home, cwd, nativeID string) ([]*daemon.ActiveSubagent, error) {
	if nativeID == "" {
		return nil, nil
	}
	cleanID := filepath.Base(filepath.Clean(nativeID))
	if cleanID == "" || cleanID == "." || cleanID == ".." || strings.ContainsAny(cleanID, "*?[") {
		return nil, nil
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return nil, nil
	}

	// 1. Locate subagents directory via exact cwd slug or glob
	var candidateDirs []string
	if cwd != "" {
		slug := strings.ReplaceAll(cwd, "/", "-")
		candidateDirs = append(candidateDirs, filepath.Join(home, ".claude", "projects", slug, cleanID, "subagents"))
	}

	// Glob across projects for this nativeID
	if matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", cleanID, "subagents")); err == nil {
		for _, m := range matches {
			already := false
			for _, cd := range candidateDirs {
				if cd == m {
					already = true
					break
				}
			}
			if !already {
				candidateDirs = append(candidateDirs, m)
			}
		}
	}

	var results []*daemon.ActiveSubagent
	seen := make(map[string]bool)

	for _, subDir := range candidateDirs {
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

			metaPath := filepath.Join(subDir, entry.Name())
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}

			var meta struct {
				Name            string `json:"name"`
				AgentType       string `json:"agentType"`
				CustomAgentType string `json:"customAgentType"`
				Description     string `json:"description"`
				Model           string `json:"model"`
				TaskKind        string `json:"taskKind"`
				Color           string `json:"color"`
				JoinedAt        int64  `json:"joinedAt"`
			}
			if err := json.Unmarshal(data, &meta); err != nil {
				continue
			}

			name := meta.Name
			if name == "" {
				name = meta.AgentType
			}
			if name == "" {
				name = baseName
			}

			role := meta.CustomAgentType
			if role == "" {
				role = meta.AgentType
			}
			if role == "" {
				role = name
			}

			startTime := time.Now()
			if meta.JoinedAt > 0 {
				startTime = time.UnixMilli(meta.JoinedAt)
			} else if info, err := entry.Info(); err == nil {
				startTime = info.ModTime()
			}

			// Check companion .jsonl transcript to evaluate running vs completed
			state := "running"
			jsonlPath := filepath.Join(subDir, baseName+".jsonl")
			if jsonlData, err := daemon.ReadTail(jsonlPath, 8192); err == nil && len(jsonlData) > 0 {
				lines := strings.Split(strings.TrimSpace(string(jsonlData)), "\n")
				for i := len(lines) - 1; i >= 0; i-- {
					line := strings.TrimSpace(lines[i])
					if line == "" {
						continue
					}
					var step struct {
						Type       string `json:"type"`
						StopReason string `json:"stop_reason"`
						Message    struct {
							StopReason string `json:"stop_reason"`
						} `json:"message"`
					}
					if jerr := json.Unmarshal([]byte(line), &step); jerr == nil {
						sr := step.StopReason
						if sr == "" {
							sr = step.Message.StopReason
						}
						if sr == "end_turn" {
							state = "completed"
						} else {
							state = "running"
						}
						break
					}
				}
			}

			results = append(results, &daemon.ActiveSubagent{
				ID:        baseName,
				Name:      name,
				Role:      role,
				Prompt:    meta.Description,
				Model:     meta.Model,
				State:     state,
				StartedAt: startTime,
			})
		}
	}

	// 2. Discover teammates from ~/.claude/teams/
	teamsDir := filepath.Join(home, ".claude", "teams")
	if teamMatches, err := filepath.Glob(filepath.Join(teamsDir, "*", "config.json")); err == nil {
		for _, cfgPath := range teamMatches {
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
			if err := json.Unmarshal(data, &teamCfg); err != nil {
				continue
			}
			// Match either exact leadSessionId or name (e.g. session-16bef887 matching nativeID prefix)
			isMatch := teamCfg.LeadSessionID == cleanID
			if !isMatch && len(cleanID) >= 8 && teamCfg.Name == "session-"+cleanID[:8] {
				isMatch = true
			}
			if isMatch {
				for _, mem := range teamCfg.Members {
					if mem.Name == "team-lead" {
						continue // skip the lead session itself
					}
					if seen[mem.Name] || seen[mem.AgentID] {
						continue
					}
					subID := mem.AgentID
					if subID == "" {
						subID = mem.Name
					}
					seen[subID] = true
					seen[mem.Name] = true

					startTime := time.Now()
					if mem.JoinedAt > 0 {
						startTime = time.UnixMilli(mem.JoinedAt)
					}

					results = append(results, &daemon.ActiveSubagent{
						ID:        subID,
						Name:      mem.Name,
						Role:      mem.AgentType,
						Prompt:    mem.Prompt,
						Model:     mem.Model,
						State:     "running",
						StartedAt: startTime,
					})
				}
			}
		}
	}

	return results, nil
}
