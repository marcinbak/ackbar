package antigravity

import (
	"fmt"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func CleanPrompt(raw string) string {
	s := raw
	// Returning directly when closed preserves quoted inner XML tags inside user messages.
	if startIdx := strings.Index(s, "<USER_REQUEST>"); startIdx != -1 {
		start := startIdx + len("<USER_REQUEST>")
		if end := strings.LastIndex(s, "</USER_REQUEST>"); end > start {
			return strings.TrimSpace(s[start:end])
		}
		s = s[start:]
	}
	for _, tag := range []string{"<CONTEXT_SUMMARY>", "<ADDITIONAL_METADATA>", "<USER_SETTINGS_CHANGE>", "<SYSTEM_MESSAGE>", "<EPHEMERAL_MESSAGE>"} {
		endTag := "</" + strings.Trim(tag, "<>") + ">"
		for {
			start := strings.Index(s, tag)
			if start == -1 {
				break
			}
			if relEnd := strings.Index(s[start+len(tag):], endTag); relEnd != -1 {
				end := start + len(tag) + relEnd + len(endTag)
				s = s[:start] + s[end:]
			} else {
				s = s[:start]
				break
			}
		}
	}
	return strings.TrimSpace(s)
}

func (*Plugin) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	if raw != nil {
		if src, _ := raw["source"].(string); src != "" && !strings.EqualFold(src, "USER_EXPLICIT") && !strings.EqualFold(src, "USER") {
			return "", false
		}
	}
	clean := CleanPrompt(content)
	if clean == "" {
		return "", false
	}
	return clean, true
}

func hasRelevantTruncatedField(fields []string) bool {
	for _, f := range fields {
		switch f {
		case "content", "thinking", "tool_calls", "media":
			return true
		}
	}
	return false
}

func hasImageGenTool(entry *transcriptEntry) bool {
	for _, tc := range entry.ToolCalls {
		l := strings.ToLower(tc.Name)
		if strings.Contains(l, "generate_image") || strings.Contains(l, "image_generator") ||
			strings.Contains(l, "image-generator") || strings.Contains(l, "edit_image") || strings.Contains(l, "generate_screen") {
			return true
		}
	}
	return false
}

func appendPlannerResponse(messages *[]agent.TranscriptMessage, entry *transcriptEntry, ts time.Time) {
	var toolSummaries []string
	tracker := (*Plugin)(nil).NewChecklistTracker()
	matchedChecklist := false
	for _, tc := range entry.ToolCalls {
		if _, _, matched := tracker.Apply(tc.Name, tc.Args); matched {
			matchedChecklist = true
		}
		if mcpName, mcpDetail, isMCP := agentutil.FormatMCPToolDisplay(tc.Name, tc.Args); isMCP {
			if mcpDetail != "" {
				toolSummaries = append(toolSummaries, fmt.Sprintf("%s: %s", mcpName, mcpDetail))
			} else {
				toolSummaries = append(toolSummaries, mcpName)
			}
			continue
		}
		action := ""
		if tc.Args != nil {
			if act, ok := tc.Args["toolAction"].(string); ok && act != "" {
				action = strings.Trim(act, `"`)
			} else if sum, ok := tc.Args["toolSummary"].(string); ok && sum != "" {
				action = strings.Trim(sum, `"`)
			}
		}
		if action != "" {
			toolSummaries = append(toolSummaries, fmt.Sprintf("%s (%s)", tc.Name, action))
		} else if tc.Name != "" {
			toolSummaries = append(toolSummaries, tc.Name)
		}
	}

	content := entry.Content
	for _, m := range entry.Media {
		if m.URI != "" {
			att := fmt.Sprintf("![Attachment](%s)", m.URI)
			if content != "" {
				content += "\n\n" + att
			} else {
				content = att
			}
		}
	}
	if content == "" && entry.Thinking == "" && len(toolSummaries) == 0 && !matchedChecklist {
		return
	}
	var turnChecklist []agent.ChecklistItem
	var turnCleared bool
	if matchedChecklist {
		turnChecklist, turnCleared, _ = tracker.Snapshot()
	}
	if len(*messages) > 0 && (*messages)[len(*messages)-1].Role == "assistant" {
		last := &(*messages)[len(*messages)-1]
		if content != "" {
			if last.Content == "" {
				last.Content = content
			} else if !strings.Contains(last.Content, content) {
				last.Content += "\n\n" + content
			}
		}
		if entry.Thinking != "" {
			if last.Thinking == "" {
				last.Thinking = entry.Thinking
			} else if !strings.Contains(last.Thinking, entry.Thinking) {
				last.Thinking += "\n\n" + entry.Thinking
			}
		}
		if len(toolSummaries) > 0 {
			last.ToolCalls = append(last.ToolCalls, toolSummaries...)
		}
		if matchedChecklist {
			if turnCleared {
				last.Checklist = nil
				last.ChecklistCleared = true
			} else {
				last.Checklist = agent.CloneChecklist(turnChecklist)
				last.ChecklistCleared = false
			}
		}
		last.Timestamp = ts
		return
	}
	var checklist []agent.ChecklistItem
	if matchedChecklist && !turnCleared {
		checklist = agent.CloneChecklist(turnChecklist)
	}
	*messages = append(*messages, agent.TranscriptMessage{
		Role: "assistant", Content: content, Thinking: entry.Thinking, ToolCalls: toolSummaries, Checklist: checklist, ChecklistCleared: matchedChecklist && turnCleared, Timestamp: ts,
	})
}

func appendGenericMedia(last *agent.TranscriptMessage, entry *transcriptEntry) {
	for _, m := range entry.Media {
		if m.URI == "" || strings.Contains(m.URI, "/.system_generated/logs/media/") || strings.Contains(m.URI, "/.tempmediaStorage/") {
			continue
		}
		cleanURI := strings.TrimPrefix(m.URI, "file://")
		if strings.Contains(last.Content, m.URI) || (cleanURI != "" && strings.Contains(last.Content, cleanURI)) {
			continue
		}
		att := fmt.Sprintf("![Attachment](%s)", m.URI)
		if last.Content != "" {
			last.Content += "\n\n" + att
		} else {
			last.Content = att
		}
	}
}
