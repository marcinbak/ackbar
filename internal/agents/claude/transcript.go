package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (p *Plugin) ExtractTranscript(homeDir, cwd, nativeID string) (*agent.Transcript, error) {
	targetFile := FindTranscriptPath(homeDir, cwd, nativeID)
	if targetFile == "" {
		return nil, fmt.Errorf("claude log not found for session %s", nativeID)
	}
	file, err := openRegularFileNonBlocking(targetFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	msgs, err := ParseJSONL(file, p.FilterUserEntry, nil)
	if err != nil {
		return nil, err
	}
	return &agent.Transcript{
		NativeID: nativeID,
		Agent:    "claude-code",
		Cwd:      cwd,
		Messages: msgs,
	}, nil
}

func (p *Plugin) AppendTranscript(r io.Reader, existing []agent.TranscriptMessage) ([]agent.TranscriptMessage, error) {
	return ParseJSONL(r, p.FilterUserEntry, existing)
}

func ParseJSONL(r io.Reader, filterUser func(raw map[string]interface{}, text string) (string, bool), base []agent.TranscriptMessage) ([]agent.TranscriptMessage, error) {
	messages := make([]agent.TranscriptMessage, len(base))
	for i, m := range base {
		messages[i] = m
		if len(m.ToolCalls) > 0 {
			tc := make([]string, len(m.ToolCalls))
			copy(tc, m.ToolCalls)
			messages[i].ToolCalls = tc
		}
		messages[i].Checklist = agent.CloneChecklist(m.Checklist)
	}
	var reducer checklistReducer
	reducer.seedFromBase(messages)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || (!strings.Contains(line, `"user"`) && !strings.Contains(line, `"assistant"`)) {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		msgType, _ := raw["type"].(string)
		ts := parseTimestampOrNow(raw["timestamp"])

		switch msgType {
		case "user":
			fullText := extractUserMessageText(raw)
			if filterUser != nil {
				if clean, keep := filterUser(raw, fullText); keep {
					messages = append(messages, agent.TranscriptMessage{Role: "user", Content: clean, Timestamp: ts})
				}
			} else if strings.TrimSpace(fullText) != "" {
				messages = append(messages, agent.TranscriptMessage{Role: "user", Content: fullText, Timestamp: ts})
			}

		case "assistant":
			msgObj, ok := raw["message"].(map[string]interface{})
			if !ok {
				continue
			}
			messages = appendAssistantContent(messages, msgObj["content"], ts, &reducer)
		}
	}
	return messages, scanner.Err()
}

func appendAssistantContent(messages []agent.TranscriptMessage, rawContent interface{}, ts time.Time, reducer *checklistReducer) []agent.TranscriptMessage {
	switch c := rawContent.(type) {
	case string:
		if c == "" {
			return messages
		}
		return mergeOrAppendAssistant(messages, c, nil, nil, false, ts)
	case []interface{}:
		var textParts, tools []string
		matchedChecklist := false
		for _, cItem := range c {
			cMap, ok := cItem.(map[string]interface{})
			if !ok {
				continue
			}
			switch cMap["type"] {
			case "text":
				if txt, ok := cMap["text"].(string); ok && txt != "" {
					textParts = append(textParts, txt)
				}
			case "tool_use":
				if formatted := formatTranscriptToolUse(cMap); formatted != "" {
					tools = append(tools, formatted)
				}
				if reducer != nil {
					tName, _ := cMap["name"].(string)
					if reducer.apply(tName, cMap["input"]) {
						matchedChecklist = true
					}
				}
			}
		}
		fullText := strings.Join(textParts, "\n\n")
		if fullText == "" && len(tools) == 0 && !matchedChecklist {
			return messages
		}
		var turnChecklist []agent.ChecklistItem
		if matchedChecklist && reducer != nil {
			turnChecklist = reducer.items
		}
		return mergeOrAppendAssistant(messages, fullText, tools, turnChecklist, matchedChecklist, ts)
	}
	return messages
}

func mergeOrAppendAssistant(messages []agent.TranscriptMessage, text string, tools []string, turnChecklist []agent.ChecklistItem, matchedChecklist bool, ts time.Time) []agent.TranscriptMessage {
	if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		last := &messages[len(messages)-1]
		if text != "" {
			if last.Content != "" {
				if !strings.Contains(last.Content, text) {
					last.Content += "\n\n" + text
				}
			} else {
				last.Content = text
			}
		}
		if len(tools) > 0 {
			last.ToolCalls = append(last.ToolCalls, tools...)
		}
		if matchedChecklist {
			last.Checklist = agent.CloneChecklist(turnChecklist)
		}
		last.Timestamp = ts
		return messages
	}
	var checklist []agent.ChecklistItem
	if matchedChecklist {
		checklist = agent.CloneChecklist(turnChecklist)
	}
	return append(messages, agent.TranscriptMessage{
		Role:      "assistant",
		Content:   text,
		ToolCalls: tools,
		Checklist: checklist,
		Timestamp: ts,
	})
}

func formatTranscriptToolUse(cMap map[string]interface{}) string {
	tName, _ := cMap["name"].(string)
	if tName == "" {
		return ""
	}
	if mcpName, mcpDetail, isMCP := agentutil.FormatMCPToolDisplay(tName, cMap["input"]); isMCP {
		if mcpDetail != "" {
			return mcpName + ": " + mcpDetail
		}
		return mcpName
	}
	var detail string
	if inputMap, ok := cMap["input"].(map[string]interface{}); ok {
		for _, k := range []string{"command", "description", "file_path", "path", "pattern", "query"} {
			if v, ok := inputMap[k].(string); ok && v != "" {
				detail = strings.TrimSpace(v)
				break
			}
		}
	}
	if detail != "" {
		return tName + ": " + detail
	}
	return tName
}

func parseTimestampOrNow(rawTS interface{}) time.Time {
	tsStr, _ := rawTS.(string)
	if tsStr != "" {
		if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
			return t
		}
	}
	return time.Now()
}
