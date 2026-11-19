package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agentutil"
)

func (*Plugin) ResolveSubagentParent(homeDir, convID string) (parentID, role, typeName string) {
	home := effectiveHome(homeDir)
	if home == "" || convID == "" || !agentutil.IsUUID(convID) {
		return "", "", ""
	}
	convID = filepath.Base(filepath.Clean(convID))
	if hasUserAnnotation(home, convID) {
		return "", "", ""
	}

	bDirs := brainDirs(home)
	for _, bDir := range bDirs {
		matches, err := filepath.Glob(filepath.Join(bDir, "*", ".system_generated", "subagents", convID+".json"))
		if err != nil {
			continue
		}
		for _, match := range matches {
			parentCand := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(match))))
			if !agentutil.IsUUID(parentCand) {
				continue
			}
			if data, rErr := os.ReadFile(match); rErr == nil {
				var payload struct {
					Role               string `json:"role"`
					TypeName           string `json:"typeName"`
					SubagentDescriptor struct {
						Role     string `json:"role"`
						TypeName string `json:"typeName"`
					} `json:"subagentDescriptor"`
				}
				if json.Unmarshal(data, &payload) == nil {
					role = payload.SubagentDescriptor.Role
					if role == "" {
						role = payload.Role
					}
					typeName = payload.SubagentDescriptor.TypeName
					if typeName == "" {
						typeName = payload.TypeName
					}
				}
			}
			return parentCand, role, typeName
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
						if (strings.EqualFold(step.Type, "SYSTEM_MESSAGE") || strings.EqualFold(step.Source, "SYSTEM")) &&
							strings.Contains(strings.ToLower(step.Content), "<system_message>") {
							if senderID := extractSenderUUIDFromSystemMessage(step.Content, convID); senderID != "" {
								return senderID, "", ""
							}
						}
						if strings.EqualFold(step.Type, "USER_INPUT") || strings.EqualFold(step.Source, "USER_EXPLICIT") || strings.EqualFold(step.Source, "USER") {
							return "", "", ""
						}
					}
				} else if strings.Contains(strings.ToLower(line), "<system_message>") {
					if senderID := extractSenderUUIDFromSystemMessage(line, convID); senderID != "" {
						return senderID, "", ""
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
					if otherUUID := extractConvIDFromMessageBytes(string(mData), convID); otherUUID != "" {
						return otherUUID, "", ""
					}
				}
			}
		}
	}
	return "", "", ""
}

func extractConvIDFromMessageBytes(search, selfID string) string {
	for {
		idx := strings.Index(search, `"conversationId":`)
		if idx == -1 {
			return ""
		}
		rest := strings.TrimSpace(search[idx+len(`"conversationId":`):])
		if strings.HasPrefix(rest, `"`) {
			rest = rest[1:]
			if qEnd := strings.Index(rest, `"`); qEnd != -1 {
				cand := rest[:qEnd]
				if agentutil.IsUUID(cand) && !strings.EqualFold(cand, selfID) {
					return cand
				}
			}
		}
		search = search[idx+len(`"conversationId":`):]
	}
}
