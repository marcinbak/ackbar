package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func executeGetSessionOutput(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	sessionID := getString(args, "session_id")
	if sessionID == "" {
		return formatError("session_id is required"), nil
	}

	recentCount := getInt(args, "recent_count", 3)
	if recentCount <= 0 {
		recentCount = 3
	}

	hasOffset := false
	offset := 0
	if rawOffset, ok := args["offset"]; ok && rawOffset != nil {
		hasOffset = true
		offset = getInt(args, "offset", 0)
		if offset < 0 {
			offset = 0
		}
	}

	charOffset := getInt(args, "char_offset", 0)
	if charOffset < 0 {
		charOffset = 0
	}

	maxChars := getInt(args, "max_chars", 10000)
	if maxChars < 500 {
		maxChars = 500
	} else if maxChars > 50000 {
		maxChars = 50000
	}

	includeAllRoles, _ := args["include_all_roles"].(bool)

	var sessions []map[string]any
	if err := client.doRequest(ctx, http.MethodGet, "/v1/sessions?federated=true", nil, &sessions); err == nil {
		if match := resolveSessionFromList(sessions, sessionID); match != nil {
			if id := getString(match, "id"); id != "" {
				sessionID = id
			}
		}
	}

	endpoint := fmt.Sprintf("/v1/sessions/transcript?id=%s&format=json&include_formats=false&max_chars=%d&recent_count=%d&char_offset=%d",
		url.QueryEscape(sessionID), maxChars, recentCount, charOffset)
	if hasOffset {
		endpoint += fmt.Sprintf("&offset=%d", offset)
	}
	if includeAllRoles {
		endpoint += "&role=all"
	} else {
		endpoint += "&role=assistant"
	}

	var data map[string]any
	if err := client.doRequest(ctx, http.MethodGet, endpoint, nil, &data); err != nil {
		return formatError(fmt.Sprintf("Failed to fetch session transcript: %v", err)), nil
	}

	title := getString(data, "title")
	agent := getString(data, "agent")
	messagesRaw, _ := data["messages"].([]any)

	var displayedMsgs []map[string]any
	truncated := false
	nextOffset := 0
	nextCharOffset := 0
	startIdx := 0
	rangeStart := 0
	rangeEnd := 0
	totalMessages := 0

	if pag, ok := data["pagination"].(map[string]any); ok && pag != nil {
		totalMessages = getInt(pag, "total_messages", 0)
		rangeStart = getInt(pag, "range_start", 0)
		rangeEnd = getInt(pag, "range_end", 0)
		if rangeStart > 0 {
			startIdx = rangeStart - 1
		} else if off := getInt(pag, "offset", 0); off > 0 {
			startIdx = off
		}
		truncated, _ = pag["truncated"].(bool)
		nextOffset = getInt(pag, "next_offset", 0)
		nextCharOffset = getInt(pag, "next_char_offset", 0)

		budgetRemaining := maxChars
		for _, mRaw := range messagesRaw {
			m, ok := mRaw.(map[string]any)
			if !ok {
				continue
			}
			content := getString(m, "content")
			runes := []rune(content)
			if len(runes) <= budgetRemaining {
				copyM := make(map[string]any, len(m))
				for k, v := range m {
					copyM[k] = v
				}
				displayedMsgs = append(displayedMsgs, copyM)
				budgetRemaining -= len(runes)
				if budgetRemaining == 0 {
					break
				}
			} else {
				copyM := make(map[string]any, len(m))
				for k, v := range m {
					copyM[k] = v
				}
				copyM["content"] = string(runes[:budgetRemaining])
				displayedMsgs = append(displayedMsgs, copyM)
				truncated = true
				if nextCharOffset == 0 {
					nextCharOffset = budgetRemaining
				}
				budgetRemaining = 0
				break
			}
		}
		if rangeStart == 0 && len(displayedMsgs) > 0 {
			rangeStart = startIdx + 1
			rangeEnd = startIdx + len(displayedMsgs)
		}
	} else {
		var candidateMsgs []map[string]any
		for _, mRaw := range messagesRaw {
			if m, ok := mRaw.(map[string]any); ok {
				role := strings.ToLower(getString(m, "role"))
				if includeAllRoles || role == "assistant" {
					candidateMsgs = append(candidateMsgs, m)
				}
			}
		}
		if !includeAllRoles && len(candidateMsgs) == 0 {
			for _, mRaw := range messagesRaw {
				if m, ok := mRaw.(map[string]any); ok {
					candidateMsgs = append(candidateMsgs, m)
				}
			}
		}

		totalMessages = len(candidateMsgs)
		startIdx = 0
		endIdx := totalMessages

		if recentCount > 0 {
			if hasOffset {
				startIdx = offset
				if startIdx > totalMessages {
					startIdx = totalMessages
				}
				if recentCount > totalMessages-startIdx {
					endIdx = totalMessages
				} else {
					endIdx = startIdx + recentCount
				}
			} else {
				startIdx = totalMessages - recentCount
				if startIdx < 0 {
					startIdx = 0
				}
				endIdx = totalMessages
			}
		} else if hasOffset {
			startIdx = offset
			if startIdx > totalMessages {
				startIdx = totalMessages
			}
			endIdx = totalMessages
		}
		candidateMsgs = candidateMsgs[startIdx:endIdx]

		budgetRemaining := maxChars
		for i, m := range candidateMsgs {
			msgIdx := startIdx + i
			content := getString(m, "content")
			runes := []rune(content)

			charOffsetForMsg := 0
			if i == 0 && charOffset > 0 {
				charOffsetForMsg = charOffset
			}
			if charOffsetForMsg > len(runes) {
				charOffsetForMsg = len(runes)
			}
			availRunes := runes[charOffsetForMsg:]

			if len(availRunes) <= budgetRemaining {
				copyM := make(map[string]any, len(m))
				for k, v := range m {
					copyM[k] = v
				}
				copyM["content"] = string(availRunes)
				displayedMsgs = append(displayedMsgs, copyM)
				budgetRemaining -= len(availRunes)

				if budgetRemaining == 0 {
					if msgIdx+1 < totalMessages {
						truncated = true
						nextOffset = msgIdx + 1
						nextCharOffset = 0
					}
					break
				}
			} else {
				copyM := make(map[string]any, len(m))
				for k, v := range m {
					copyM[k] = v
				}
				copyM["content"] = string(availRunes[:budgetRemaining])
				displayedMsgs = append(displayedMsgs, copyM)
				truncated = true
				nextOffset = msgIdx
				nextCharOffset = charOffsetForMsg + budgetRemaining
				budgetRemaining = 0
				break
			}
		}

		if !truncated && hasOffset && endIdx < totalMessages {
			truncated = true
			nextOffset = endIdx
			nextCharOffset = 0
		}

		if len(displayedMsgs) > 0 {
			rangeStart = startIdx + 1
			rangeEnd = startIdx + len(displayedMsgs)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Output for Session `%s`\n", sessionID))
	truncStr := "false"
	if truncated {
		truncStr = "true"
	}
	sb.WriteString(fmt.Sprintf("**Title:** %s | **Agent:** %s | **Truncated:** %s\n", title, agent, truncStr))
	if totalMessages > 0 && len(displayedMsgs) > 0 {
		sb.WriteString(fmt.Sprintf("showing %d-%d of %d (0-based offset=%d, total_messages=%d, max_chars=%d)\n\n",
			rangeStart, rangeEnd, totalMessages, startIdx, totalMessages, maxChars))
	} else {
		sb.WriteString("\n")
	}

	if len(displayedMsgs) == 0 {
		sb.WriteString("*(No messages found in transcript)*\n")
	} else {
		for i, m := range displayedMsgs {
			role := getString(m, "role")
			content := getString(m, "content")
			sb.WriteString(fmt.Sprintf("#### Message %d (%s):\n%s\n\n", rangeStart+i, role, content))
		}
	}

	if truncated {
		sb.WriteString(fmt.Sprintf("[Output truncated. Showing messages %d-%d of %d. To fetch the next chunk, call get_session_output with offset=%d, char_offset=%d, or increase max_chars=50000.]\n",
			rangeStart, rangeEnd, totalMessages, nextOffset, nextCharOffset))
	}

	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: strings.TrimSpace(sb.String())}},
	}, nil
}
