package antigravity

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agentutil"
)

func extractStartTime(logPath string, fallback time.Time) time.Time {
	data, err := agentutil.ReadHead(logPath, 8*1024)
	if err != nil {
		return fallback
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var step struct {
			CreatedAt string `json:"created_at"`
		}
		if json.Unmarshal([]byte(line), &step) == nil && step.CreatedAt != "" {
			if t := parseStepTimestamp(step.CreatedAt); !t.IsZero() {
				return t
			}
		}
		break
	}
	return fallback
}

func extractWorkspace(logPath, home string) string {
	data, err := agentutil.ReadHead(logPath, 64*1024)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, " -> ") {
			continue
		}
		idx := strings.Index(line, " -> ")
		startIdx := strings.LastIndexAny(line[:idx], " \t\n\r\"'[]") + 1
		cand := strings.TrimPrefix(strings.TrimSpace(line[startIdx:idx]), "file://")
		if agentutil.DirExists(cand) && cand != home && !strings.Contains(cand, "/.gemini") {
			return normalizeToGitRoot(cand, home)
		}
	}
	maxLines := len(lines)
	if maxLines > 50 {
		maxLines = 50
	}
	for i := 0; i < maxLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var step struct {
			ToolCalls []struct {
				Args map[string]interface{} `json:"args"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal([]byte(line), &step) != nil {
			continue
		}
		for _, tc := range step.ToolCalls {
			for _, k := range []string{"Cwd", "DirectoryPath", "SearchPath"} {
				if s, ok := tc.Args[k].(string); ok && s != "" {
					s = strings.Trim(s, `"`)
					if agentutil.DirExists(s) && s != home && !strings.Contains(s, "/.gemini") {
						return normalizeToGitRoot(s, home)
					}
				}
			}
			for _, k := range []string{"AbsolutePath", "Path", "TargetFile"} {
				if s, ok := tc.Args[k].(string); ok && s != "" {
					dir := filepath.Dir(strings.Trim(s, `"`))
					if agentutil.DirExists(dir) && dir != home && !strings.Contains(dir, "/.gemini") {
						return normalizeToGitRoot(dir, home)
					}
				}
			}
		}
	}
	return ""
}
