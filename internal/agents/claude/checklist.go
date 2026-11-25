package claude

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func baseToolName(toolName string) string {
	trimmed := strings.TrimSpace(toolName)
	if idx := strings.LastIndex(trimmed, "__"); idx >= 0 {
		trimmed = trimmed[idx+2:]
	}
	if idx := strings.LastIndexAny(trimmed, ":./"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	return strings.ToLower(strings.TrimSpace(trimmed))
}

func parseInputMap(toolInput any) map[string]any {
	switch v := toolInput.(type) {
	case map[string]any:
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		var m map[string]any
		if json.Unmarshal([]byte(trimmed), &m) == nil {
			return m
		}
	case []byte:
		var m map[string]any
		if json.Unmarshal(v, &m) == nil {
			return m
		}
	}
	return nil
}

func firstTrimmedString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func idString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return strings.TrimSpace(t.String())
	default:
		if v == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func normalizeChecklistStatus(raw any) agent.ChecklistStatus {
	s, _ := raw.(string)
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "done", "completed":
		return agent.ChecklistDone
	case "in_progress", "inprogress", "running":
		return agent.ChecklistInProgress
	case "skipped", "cancelled", "canceled":
		return agent.ChecklistSkipped
	case "failed", "error":
		return agent.ChecklistFailed
	default:
		return agent.ChecklistPending
	}
}

func maxNumericID(items []agent.ChecklistItem) int {
	maxID := 0
	for _, it := range items {
		if n, err := strconv.Atoi(strings.TrimSpace(it.ID)); err == nil && n > maxID {
			maxID = n
		}
	}
	return maxID
}

func bumpHighWater(high int, id string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(id)); err == nil && n > high {
		return n
	}
	return high
}

func sanitizeAndCapSlice(items []agent.ChecklistItem) []agent.ChecklistItem {
	if len(items) == 0 {
		return nil
	}
	limit := min(len(items), agent.MaxChecklistItems)
	out := make([]agent.ChecklistItem, 0, limit)
	for _, it := range items[:limit] {
		s := agent.SanitizeChecklistItem(it)
		if s.Title != "" {
			out = append(out, s)
		}
	}
	return out
}

type checklistReducer struct {
	items         []agent.ChecklistItem
	cleared       bool
	highWaterMark int
}

func (*Plugin) NewChecklistTracker() agent.ChecklistTracker {
	return &checklistReducer{}
}

func (r *checklistReducer) Seed(items []agent.ChecklistItem, cleared bool, highWater int) {
	if highWater > r.highWaterMark {
		r.highWaterMark = highWater
	}
	if maxID := maxNumericID(items); maxID > r.highWaterMark {
		r.highWaterMark = maxID
	}
	if cleared {
		r.items = nil
		r.cleared = true
		return
	}
	r.items = sanitizeAndCapSlice(items)
	r.cleared = false
}

func (r *checklistReducer) seedFromBase(base []agent.TranscriptMessage) {
	for _, m := range base {
		if m.ChecklistHighWater > r.highWaterMark {
			r.highWaterMark = m.ChecklistHighWater
		}
		if maxID := maxNumericID(m.Checklist); maxID > r.highWaterMark {
			r.highWaterMark = maxID
		}
		if m.ChecklistCleared {
			r.items = nil
			r.cleared = true
		} else if len(m.Checklist) > 0 {
			r.items = sanitizeAndCapSlice(m.Checklist)
			r.cleared = false
		}
	}
}

func (r *checklistReducer) applyOnly(toolName string, toolInput any) bool {
	matched := applyClaudeChecklistInPlace(&r.items, &r.highWaterMark, toolName, toolInput)
	if !matched {
		return false
	}
	r.cleared = len(r.items) == 0
	return true
}

func (r *checklistReducer) Apply(toolName string, toolInput any) ([]agent.ChecklistItem, bool, bool) {
	if !r.applyOnly(toolName, toolInput) {
		return nil, false, false
	}
	if len(r.items) == 0 {
		return nil, true, true
	}
	return agent.CloneChecklist(r.items), false, true
}

func (r *checklistReducer) Snapshot() ([]agent.ChecklistItem, bool, int) {
	if r.cleared {
		return nil, true, r.highWaterMark
	}
	return agent.CloneChecklist(r.items), false, r.highWaterMark
}

func (*Plugin) ApplyToolChecklist(prev []agent.ChecklistItem, toolName string, toolInput any) ([]agent.ChecklistItem, bool) {
	r := checklistReducer{
		items:         sanitizeAndCapSlice(prev),
		highWaterMark: maxNumericID(prev),
	}
	if !r.applyOnly(toolName, toolInput) {
		return nil, false
	}
	if len(r.items) == 0 {
		return []agent.ChecklistItem{}, true
	}
	return r.items, true
}

func applyClaudeChecklistInPlace(items *[]agent.ChecklistItem, high *int, toolName string, toolInput any) bool {
	if m := maxNumericID(*items); m > *high {
		*high = m
	}

	base := baseToolName(toolName)
	switch base {
	case "todowrite", "todo_write":
		inputMap := parseInputMap(toolInput)
		if inputMap == nil {
			return false
		}
		rawList, hasList := inputMap["todos"]
		if !hasList {
			rawList, hasList = inputMap["tasks"]
		}
		if !hasList {
			return false
		}
		parsed := agentutil.ParseInterfaceSlice(rawList)
		if parsed == nil {
			return false
		}
		next := make([]agent.ChecklistItem, 0, min(len(parsed), agent.MaxChecklistItems))
		for _, rawItem := range parsed {
			if len(next) >= agent.MaxChecklistItems {
				break
			}
			m, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			rawTitle := firstTrimmedString(m, "content", "title", "subject", "text", "name")
			if rawTitle == "" {
				continue
			}
			item := agent.SanitizeChecklistItem(agent.ChecklistItem{
				ID:         idString(m["id"]),
				Title:      rawTitle,
				ActiveForm: firstTrimmedString(m, "activeForm", "active_form"),
				Status:     normalizeChecklistStatus(m["status"]),
			})
			if item.Title == "" {
				continue
			}
			*high = bumpHighWater(*high, item.ID)
			next = append(next, item)
		}
		if len(next) > 0 {
			*items = next
			return true
		}
		if len(parsed) == 0 {
			*items = nil
			return true
		}
		return false

	case "taskcreate", "task_create":
		inputMap := parseInputMap(toolInput)
		if inputMap == nil {
			return false
		}
		rawTitle := firstTrimmedString(inputMap, "subject", "title", "content", "description")
		if rawTitle == "" {
			return false
		}
		id := agent.TruncateBytesUTF8(firstNonEmptyID(inputMap, "id", "taskId", "task_id"), agent.MaxChecklistIDBytes)
		if id == "" {
			*high++
			id = strconv.Itoa(*high)
		} else {
			*high = bumpHighWater(*high, id)
		}
		item := agent.SanitizeChecklistItem(agent.ChecklistItem{
			ID:         id,
			Title:      rawTitle,
			ActiveForm: firstTrimmedString(inputMap, "activeForm", "active_form"),
			Status:     normalizeChecklistStatus(inputMap["status"]),
		})
		if item.Title == "" {
			return false
		}
		if len(*items) < agent.MaxChecklistItems {
			*items = append(*items, item)
		}
		return true

	case "taskupdate", "task_update":
		inputMap := parseInputMap(toolInput)
		if inputMap == nil {
			return false
		}
		taskID := agent.TruncateBytesUTF8(firstNonEmptyID(inputMap, "taskId", "task_id", "id"), agent.MaxChecklistIDBytes)
		statusStr, hasStatus := inputMap["status"].(string)
		hasStatus = hasStatus && strings.TrimSpace(statusStr) != ""
		if hasStatus && strings.EqualFold(strings.TrimSpace(statusStr), "deleted") {
			if taskID == "" {
				return false
			}
			kept := (*items)[:0]
			for _, it := range *items {
				if it.ID != taskID {
					kept = append(kept, it)
				}
			}
			*items = kept
			return true
		}

		title := agent.TruncateRunes(firstTrimmedString(inputMap, "subject", "title"), agent.MaxChecklistTitleRunes)
		activeForm := agent.TruncateRunes(firstTrimmedString(inputMap, "activeForm", "active_form"), agent.MaxChecklistTitleRunes)
		if taskID != "" {
			for i := range *items {
				if (*items)[i].ID == taskID {
					if hasStatus {
						(*items)[i].Status = normalizeChecklistStatus(statusStr)
					}
					if title != "" {
						(*items)[i].Title = title
					}
					if activeForm != "" {
						(*items)[i].ActiveForm = activeForm
					}
					return true
				}
			}
		}
		if title != "" {
			id := taskID
			if id == "" {
				*high++
				id = strconv.Itoa(*high)
			} else {
				*high = bumpHighWater(*high, id)
			}
			st := agent.ChecklistPending
			if hasStatus {
				st = normalizeChecklistStatus(statusStr)
			}
			if len(*items) < agent.MaxChecklistItems {
				*items = append(*items, agent.ChecklistItem{
					ID:         id,
					Title:      title,
					ActiveForm: activeForm,
					Status:     st,
				})
			}
			return true
		}
		return false
	}

	return false
}

func firstNonEmptyID(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if id := idString(m[k]); id != "" {
			return id
		}
	}
	return ""
}
