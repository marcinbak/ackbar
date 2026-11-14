package daemon

import (
	"net/url"
	"strconv"
	"strings"
)

type TranscriptPagination struct {
	TotalMessages  int  `json:"total_messages"`
	ReturnedCount  int  `json:"returned_count"`
	RangeStart     int  `json:"range_start"`
	RangeEnd       int  `json:"range_end"`
	Offset         int  `json:"offset"`
	NextOffset     int  `json:"next_offset"`
	CharOffset     int  `json:"char_offset"`
	NextCharOffset int  `json:"next_char_offset"`
	MaxChars       int  `json:"max_chars"`
	ReturnedChars  int  `json:"returned_chars"`
	Truncated      bool `json:"truncated"`
}

type TranscriptPaginationOptions struct {
	HasPagination bool
	Role          string
	HasOffset     bool
	Offset        int
	Limit         int
	CharOffset    int
	HasMaxChars   bool
	MaxChars      int
}

func ParseTranscriptPaginationOptions(query url.Values) TranscriptPaginationOptions {
	opts := TranscriptPaginationOptions{}

	roleStr := strings.TrimSpace(query.Get("role"))
	offsetStr := strings.TrimSpace(query.Get("offset"))
	limitStr := strings.TrimSpace(query.Get("limit"))
	recentCountStr := strings.TrimSpace(query.Get("recent_count"))
	charOffsetStr := strings.TrimSpace(query.Get("char_offset"))
	maxCharsStr := strings.TrimSpace(query.Get("max_chars"))

	if roleStr != "" || offsetStr != "" || limitStr != "" || recentCountStr != "" || charOffsetStr != "" || maxCharsStr != "" {
		opts.HasPagination = true
	}

	opts.Role = roleStr

	if offsetStr != "" {
		if v, err := strconv.Atoi(offsetStr); err == nil && v >= 0 {
			opts.HasOffset = true
			opts.Offset = v
		}
	}

	limitVal := 0
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limitVal = v
		}
	}
	if limitVal <= 0 && recentCountStr != "" {
		if v, err := strconv.Atoi(recentCountStr); err == nil && v > 0 {
			limitVal = v
		}
	}
	opts.Limit = limitVal

	if charOffsetStr != "" {
		if v, err := strconv.Atoi(charOffsetStr); err == nil && v >= 0 {
			opts.CharOffset = v
		}
	}

	if maxCharsStr != "" {
		if v, err := strconv.Atoi(maxCharsStr); err == nil {
			opts.HasMaxChars = true
			if v < 500 {
				v = 500
			} else if v > 50000 {
				v = 50000
			}
			opts.MaxChars = v
		}
	} else if opts.HasPagination {
		opts.MaxChars = 10000
	}

	return opts
}

func PaginateTranscriptMessages(allMsgs []TranscriptMessage, opts TranscriptPaginationOptions) ([]TranscriptMessage, *TranscriptPagination) {
	msgs := allMsgs
	if strings.EqualFold(opts.Role, "assistant") {
		var filtered []TranscriptMessage
		for _, m := range allMsgs {
			if strings.EqualFold(m.Role, "assistant") {
				filtered = append(filtered, m)
			}
		}
		if len(filtered) > 0 {
			msgs = filtered
		}
	}

	totalMessages := len(msgs)
	if opts.MaxChars <= 0 {
		opts.MaxChars = 10000
	} else if opts.MaxChars < 500 {
		opts.MaxChars = 500
	} else if opts.MaxChars > 50000 {
		opts.MaxChars = 50000
	}

	startIdx := 0
	endIdx := totalMessages

	if opts.Limit > 0 {
		if opts.HasOffset {
			startIdx = opts.Offset
			if startIdx > totalMessages {
				startIdx = totalMessages
			}
			if opts.Limit > totalMessages-startIdx {
				endIdx = totalMessages
			} else {
				endIdx = startIdx + opts.Limit
			}
		} else {
			startIdx = totalMessages - opts.Limit
			if startIdx < 0 {
				startIdx = 0
			}
			endIdx = totalMessages
		}
	} else if opts.HasOffset {
		startIdx = opts.Offset
		if startIdx > totalMessages {
			startIdx = totalMessages
		}
		endIdx = totalMessages
	}

	selected := msgs[startIdx:endIdx]

	var result []TranscriptMessage
	returnedChars := 0
	budgetRemaining := opts.MaxChars
	truncated := false
	nextOffset := 0
	nextCharOffset := 0

	for i, m := range selected {
		msgIdx := startIdx + i
		runes := []rune(m.Content)

		charOffsetForMsg := 0
		if i == 0 && opts.CharOffset > 0 {
			charOffsetForMsg = opts.CharOffset
		}
		if charOffsetForMsg > len(runes) {
			charOffsetForMsg = len(runes)
		}
		availRunes := runes[charOffsetForMsg:]

		if len(availRunes) <= budgetRemaining {
			m.Content = string(availRunes)
			result = append(result, m)
			returnedChars += len(availRunes)
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
			m.Content = string(availRunes[:budgetRemaining])
			result = append(result, m)
			returnedChars += budgetRemaining
			truncated = true
			nextOffset = msgIdx
			nextCharOffset = charOffsetForMsg + budgetRemaining
			budgetRemaining = 0
			break
		}
	}

	if !truncated && opts.HasOffset && endIdx < totalMessages {
		truncated = true
		nextOffset = endIdx
		nextCharOffset = 0
	}

	rangeStart := 0
	rangeEnd := 0
	if len(result) > 0 {
		rangeStart = startIdx + 1
		rangeEnd = startIdx + len(result)
	}

	pagination := &TranscriptPagination{
		TotalMessages:  totalMessages,
		ReturnedCount:  len(result),
		RangeStart:     rangeStart,
		RangeEnd:       rangeEnd,
		Offset:         startIdx,
		NextOffset:     nextOffset,
		CharOffset:     opts.CharOffset,
		NextCharOffset: nextCharOffset,
		MaxChars:       opts.MaxChars,
		ReturnedChars:  returnedChars,
		Truncated:      truncated,
	}

	return result, pagination
}
