package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

func (a *AntigravityProvider) ListSubagents(home, cwd, nativeID string) ([]*daemon.ActiveSubagent, error) {
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

	candidateDirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "subagents"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "subagents"),
		filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "subagents"),
	}

	if profiles, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "subagents")); len(profiles) > 0 {
		candidateDirs = append(candidateDirs, profiles...)
	}
	if profiles2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "subagents")); len(profiles2) > 0 {
		candidateDirs = append(candidateDirs, profiles2...)
	}

	var results []*daemon.ActiveSubagent
	seen := make(map[string]bool)

	for _, dir := range candidateDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			filePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			var payload struct {
				ConversationID     string `json:"conversationId"`
				SubagentDescriptor struct {
					TypeName string `json:"typeName"`
					Role     string `json:"role"`
				} `json:"subagentDescriptor"`
				State          string `json:"state"`
				SpawnStepIndex int    `json:"spawnStepIndex"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				continue
			}

			subID := payload.ConversationID
			if subID == "" {
				subID = strings.TrimSuffix(entry.Name(), ".json")
			}
			if seen[subID] {
				continue
			}
			seen[subID] = true

			stateNorm := "completed"
			stUpper := strings.ToUpper(payload.State)
			if strings.Contains(stUpper, "RUNNING") {
				stateNorm = "running"
			} else if strings.Contains(stUpper, "KILLED") || strings.Contains(stUpper, "CANCEL") {
				stateNorm = "killed"
			}

			role := payload.SubagentDescriptor.Role
			if role == "" {
				role = payload.SubagentDescriptor.TypeName
			}
			if role == "" {
				role = "Subagent"
			}

			name := payload.SubagentDescriptor.TypeName
			if name == "" {
				name = role
			}

			info, _ := entry.Info()
			startTime := time.Now()
			if info != nil {
				startTime = info.ModTime()
			}

			results = append(results, &daemon.ActiveSubagent{
				ID:        subID,
				Name:      name,
				Role:      role,
				State:     stateNorm,
				StartedAt: startTime,
			})
		}
	}

	return results, nil
}
