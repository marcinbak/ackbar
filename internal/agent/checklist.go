package agent

import (
	"strings"
	"unicode/utf8"
)

const (
	MaxChecklistItems      = 100
	MaxChecklistTitleRunes = 512
	MaxChecklistIDBytes    = 64
)

type ChecklistStatus string

const (
	ChecklistPending    ChecklistStatus = "pending"
	ChecklistInProgress ChecklistStatus = "in_progress"
	ChecklistDone       ChecklistStatus = "done"
	ChecklistSkipped    ChecklistStatus = "skipped"
	ChecklistFailed     ChecklistStatus = "failed"
)

type ChecklistItem struct {
	ID         string          `json:"id,omitempty"`
	Title      string          `json:"title"`
	ActiveForm string          `json:"active_form,omitempty"`
	Status     ChecklistStatus `json:"status"`
}

func CloneChecklist(src []ChecklistItem) []ChecklistItem {
	if src == nil {
		return nil
	}
	dst := make([]ChecklistItem, len(src))
	copy(dst, src)
	return dst
}

func TruncateRunes(s string, maxRunes int) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if maxRunes <= 0 || s == "" {
		return ""
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return strings.TrimSpace(s[:i])
		}
		count++
	}
	return s
}

func TruncateBytesUTF8(s string, maxBytes int) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if maxBytes <= 0 || s == "" {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	end := 0
	for i, r := range s {
		rLen := utf8.RuneLen(r)
		if i+rLen > maxBytes {
			break
		}
		end = i + rLen
	}
	return strings.TrimSpace(s[:end])
}

func SanitizeChecklistItem(it ChecklistItem) ChecklistItem {
	it.ID = TruncateBytesUTF8(it.ID, MaxChecklistIDBytes)
	it.Title = TruncateRunes(it.Title, MaxChecklistTitleRunes)
	it.ActiveForm = TruncateRunes(it.ActiveForm, MaxChecklistTitleRunes)
	return it
}
