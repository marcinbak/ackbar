package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

var pbtxtTitleRegex = regexp.MustCompile(`title\s*:\s*"([^"]+)"`)

func ReadAnnotationTitle(homeDir, sessionID string) string {
	if sessionID == "" || strings.HasPrefix(sessionID, "proc-") {
		return ""
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return ""
	}
	for _, aDir := range annotationDirs(home) {
		if data, err := os.ReadFile(filepath.Join(aDir, sessionID+".pbtxt")); err == nil {
			if m := pbtxtTitleRegex.FindStringSubmatch(string(data)); len(m) > 1 {
				if t := strings.TrimSpace(m[1]); t != "" {
					return t
				}
			}
		}
	}
	return ""
}

func (p *Plugin) ReadSessionMetadata(homeDir, cwd, nativeID string) *agent.SessionMeta {
	home := effectiveHome(homeDir)
	anno := ReadAnnotationTitle(home, nativeID)
	title := p.ResolveSessionTitle(home, cwd, nativeID)
	if title == "" && anno == "" {
		return nil
	}
	if title == "" {
		title = anno
	}
	return &agent.SessionMeta{Title: title, AITitle: anno}
}

func (p *Plugin) ResolveSessionTitle(homeDir, _, nativeID string) string {
	if nativeID == "" || strings.HasPrefix(nativeID, "proc-") {
		return ""
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return ""
	}

	if annoTitle := ReadAnnotationTitle(home, nativeID); annoTitle != "" {
		return annoTitle
	}

	for _, mPath := range metadataCachePaths(home) {
		if data, err := os.ReadFile(mPath); err == nil {
			var meta struct {
				Conversations map[string]struct {
					Summary struct {
						Title   string `json:"Title"`
						Preview string `json:"Preview"`
					} `json:"summary"`
				} `json:"conversations"`
			}
			if json.Unmarshal(data, &meta) == nil {
				if c, exists := meta.Conversations[nativeID]; exists {
					if c.Summary.Title != "" {
						return c.Summary.Title
					}
					if c.Summary.Preview != "" && c.Summary.Preview != "Session Exit Command" {
						return agentutil.TruncateTitle(c.Summary.Preview, 50)
					}
				}
			}
		}
	}

	bDirs := brainDirs(home)
	for _, bDir := range bDirs {
		if data, err := os.ReadFile(filepath.Join(bDir, nativeID, "task.md.metadata.json")); err == nil {
			var meta struct {
				Summary string `json:"summary"`
			}
			if json.Unmarshal(data, &meta) == nil && meta.Summary != "" {
				return agentutil.TruncateTitle(meta.Summary, 50)
			}
		}
	}

	for _, bDir := range bDirs {
		logPath := filepath.Join(bDir, nativeID, ".system_generated", "logs", "transcript.jsonl")
		if data, err := agentutil.ReadHead(logPath, 64*1024); err == nil && len(data) > 0 {
			firstPromptTitle := ""
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var step struct {
					Source  string `json:"source"`
					Type    string `json:"type"`
					Content string `json:"content"`
				}
				if json.Unmarshal([]byte(line), &step) == nil {
					if step.Type == "CHECKPOINT" && strings.Contains(step.Content, "# USER Objective:") {
						idx := strings.Index(step.Content, "# USER Objective:")
						sub := strings.TrimSpace(step.Content[idx+len("# USER Objective:"):])
						if end := strings.Index(sub, "\n"); end != -1 {
							sub = strings.TrimSpace(sub[:end])
						}
						if sub != "" && !strings.HasPrefix(sub, "<") && len(sub) >= 4 && !agentutil.IsRawSessionName(sub) {
							return agentutil.TruncateTitle(sub, 50)
						}
					}
					if step.Type == "USER_INPUT" && step.Content != "" && firstPromptTitle == "" {
						clean, keep := p.FilterUserEntry(map[string]interface{}{"source": step.Source}, step.Content)
						if keep && !strings.HasPrefix(clean, "/") && !strings.HasPrefix(clean, "<") && !agentutil.IsRawSessionName(clean) {
							firstPromptTitle = agentutil.TruncateTitle(clean, 50)
						}
					}
				}
			}
			if firstPromptTitle != "" {
				return firstPromptTitle
			}
		}
	}

	for _, protoPath := range protoPaths(home) {
		if data, err := agentutil.ReadHead(protoPath, 256*1024); err == nil && len(data) > 0 {
			if title := extractProtoTitle(string(data), nativeID); title != "" {
				return title
			}
		}
	}
	return ""
}

func extractProtoTitle(str, targetID string) string {
	idx := strings.Index(str, targetID)
	if idx == -1 {
		return ""
	}
	sub := str[idx+len(targetID):]
	if len(sub) > 250 {
		sub = sub[:250]
	}
	var words []string
	var cur strings.Builder
	for _, r := range sub {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == ' ' || r == '-' || r == '_' || r == ':' || r == '/' || r == '(' || r == ')' {
			cur.WriteRune(r)
		} else {
			if cur.Len() >= 4 {
				words = append(words, strings.TrimSpace(cur.String()))
			}
			cur.Reset()
		}
	}
	if cur.Len() >= 4 {
		words = append(words, strings.TrimSpace(cur.String()))
	}
	for _, w := range words {
		if !strings.HasPrefix(w, "file:") && !strings.HasPrefix(w, "git@") &&
			!strings.Contains(w, "Users/") && !strings.Contains(w, "home/") &&
			len(w) >= 5 && !agentutil.IsRawSessionName(w) {
			return agentutil.TruncateTitle(w, 50)
		}
	}
	return ""
}
