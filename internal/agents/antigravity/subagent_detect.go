package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agentutil"
)

func extractSenderUUIDFromSystemMessage(text, selfID string) string {
	lower := strings.ToLower(text)
	search := lower
	for {
		idx := strings.Index(search, "sender=")
		if idx == -1 {
			break
		}
		rest := strings.TrimSpace(text[len(text)-len(search)+idx+len("sender="):])
		rest = strings.TrimLeft(rest, `"'`)
		if end := strings.IndexAny(rest, " \t\r\n\"'\\>]"); end != -1 {
			rest = rest[:end]
		}
		cand := strings.Trim(rest, `"'`)
		if agentutil.IsUUID(cand) && (selfID == "" || !strings.EqualFold(cand, selfID)) {
			return cand
		}
		search = search[idx+len("sender="):]
	}
	return ""
}

func hasSystemStepSubagentMarker(lower string) bool {
	return strings.Contains(lower, "<subagent_invocation>") ||
		strings.Contains(lower, "you are a subagent") ||
		strings.Contains(lower, "subagent task") ||
		strings.Contains(lower, "invoked by parent") ||
		strings.Contains(lower, "role contract (") ||
		strings.Contains(lower, "advisory restriction:")
}

func hasLegacySubagentPromptPrefix(lower string) bool {
	trimmed := strings.TrimSpace(lower)
	return strings.Contains(trimmed, "<subagent_invocation>") ||
		strings.HasPrefix(trimmed, "you are a subagent") ||
		strings.HasPrefix(trimmed, "you are reviewer") ||
		strings.HasPrefix(trimmed, "you are code reviewer") ||
		strings.HasPrefix(trimmed, "you are a code reviewer") ||
		strings.HasPrefix(trimmed, "you are the dedicated security reviewer") ||
		strings.HasPrefix(trimmed, "you are the security reviewer") ||
		strings.HasPrefix(trimmed, "you are the security auditor") ||
		strings.HasPrefix(trimmed, "you are the security-reviewer") ||
		strings.HasPrefix(trimmed, "you are the clean-reviewer") ||
		strings.HasPrefix(trimmed, "you are the context-reviewer") ||
		strings.HasPrefix(trimmed, "advisory: you are read-only") ||
		((strings.HasPrefix(trimmed, "you are the ") || strings.HasPrefix(trimmed, "you are a ")) && strings.Contains(trimmed, "-reviewer for project"))
}

func (*Plugin) IsSubagentSession(homeDir, convID string, precomputed map[string]bool) bool {
	if convID == "" || !agentutil.IsUUID(convID) {
		return false
	}
	home := effectiveHome(homeDir)
	convID = filepath.Base(filepath.Clean(convID))
	// Annotations are created exclusively for primary interactive user sessions.
	if hasUserAnnotation(home, convID) {
		return false
	}
	bDirs := brainDirs(home)
	if precomputed != nil {
		if precomputed[strings.ToLower(convID)] {
			return true
		}
	} else {
		for _, bDir := range bDirs {
			if matches, err := filepath.Glob(filepath.Join(bDir, "*", ".system_generated", "subagents", convID+".json")); err == nil && len(matches) > 0 {
				return true
			}
		}
	}

	for _, mp := range metadataCachePaths(home) {
		if data, err := os.ReadFile(mp); err == nil {
			var meta struct {
				Conversations map[string]struct {
					IsInternal bool `json:"is_internal"`
				} `json:"conversations"`
			}
			if json.Unmarshal(data, &meta) == nil {
				if c, exists := meta.Conversations[convID]; exists && c.IsInternal {
					return true
				}
			}
		}
	}

	for _, bDir := range bDirs {
		logPath := filepath.Join(bDir, convID, ".system_generated", "logs", "transcript.jsonl")
		if data, err := agentutil.ReadHead(logPath, 64*1024); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var step struct {
					StepIndex int    `json:"step_index"`
					Source    string `json:"source"`
					Type      string `json:"type"`
					Content   string `json:"content"`
				}
				if json.Unmarshal([]byte(line), &step) == nil {
					if step.StepIndex == 0 {
						cl := strings.ToLower(step.Content)
						if (strings.EqualFold(step.Type, "SYSTEM_MESSAGE") || strings.EqualFold(step.Source, "SYSTEM")) &&
							((strings.Contains(cl, "<system_message>") && extractSenderUUIDFromSystemMessage(step.Content, convID) != "") ||
								hasSystemStepSubagentMarker(cl) || hasLegacySubagentPromptPrefix(cl)) {
							return true
						}
						if hasLegacySubagentPromptPrefix(cl) {
							return true
						}
						if strings.EqualFold(step.Type, "USER_INPUT") || strings.EqualFold(step.Source, "USER_EXPLICIT") || strings.EqualFold(step.Source, "USER") {
							return false
						}
					}
				} else {
					ll := strings.ToLower(line)
					if (strings.Contains(ll, "<system_message>") && extractSenderUUIDFromSystemMessage(line, convID) != "") ||
						hasSystemStepSubagentMarker(ll) || hasLegacySubagentPromptPrefix(ll) {
						return true
					}
				}
				break
			}
		}

		msgDir := filepath.Join(bDir, convID, ".system_generated", "messages")
		if entries, err := os.ReadDir(msgDir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "read.json" {
					continue
				}
				if mData, mErr := agentutil.ReadHead(filepath.Join(msgDir, e.Name()), 8*1024); mErr == nil {
					mStr := string(mData)
					if strings.Contains(mStr, "invoke_subagent") ||
						strings.Contains(mStr, "Message from Parent Agent") ||
						strings.Contains(mStr, "Message from Root Agent") {
						return true
					}
				}
			}
		}
	}
	return false
}
