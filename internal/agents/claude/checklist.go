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

type checklistReducer struct {
	items         []agent.ChecklistItem
	highWaterMark int
}

func (r *checklistReducer) seedFromBase(base []agent.TranscriptMessage) {
	for _, m := range base {
		if len(m.Checklist) > 0 {
			r.items = agent.CloneChecklist(m.Checklist)
			if maxID := maxNumericID(m.Checklist); maxID > r.highWaterMark {
				r.highWaterMark = maxID
			}
		}
	}
}

func (r *checklistReducer) apply(toolName string, toolInput any) bool {
	next, nextHigh, matched := applyClaudeChecklist(r.items, r.highWaterMark, toolName, toolInput)
	if !matched {
		return false
	}
	r.items = next
	r.highWaterMark = nextHigh
	return true
}

func (*Plugin) ApplyToolChecklist(prev []agent.ChecklistItem, toolName string, toolInput any) ([]agent.ChecklistItem, bool) {
	next, _, matched := applyClaudeChecklist(prev, maxNumericID(prev), toolName, toolInput)
	return next, matched
}

func applyClaudeChecklist(prev []agent.ChecklistItem, highWater int, toolName string, toolInput any) ([]agent.ChecklistItem, int, bool) {
	high := highWater
	if m := maxNumericID(prev); m > high {
		high = m
	}

	base := baseToolName(toolName)
	switch base {
	case "todowrite", "todo_write":
		inputMap := parseInputMap(toolInput)
		if inputMap == nil {
			return nil, high, false
		}
		rawList, hasList := inputMap["todos"]
		if !hasList {
			rawList, hasList = inputMap["tasks"]
		}
		if !hasList {
			return nil, high, false
		}
		parsed := agentutil.ParseInterfaceSlice(rawList)
		if parsed == nil {
			return nil, high, false
		}
		items := make([]agent.ChecklistItem, 0, len(parsed))
		for _, rawItem := range parsed {
			m, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			title := firstTrimmedString(m, "content", "title", "subject", "text", "name")
			if title == "" {
				continue
			}
			id := idString(m["id"])
			high = bumpHighWater(high, id)
			items = append(items, agent.ChecklistItem{
				ID:         id,
				Title:      title,
				ActiveForm: firstTrimmedString(m, "activeForm", "active_form"),
				Status:     normalizeChecklistStatus(m["status"]),
			})
		}
		if len(items) > 0 {
			return items, high, true
		}
		if len(parsed) == 0 {
			return []agent.ChecklistItem{}, high, true
		}
		return nil, high, false

	case "taskcreate", "task_create":
		inputMap := parseInputMap(toolInput)
		if inputMap == nil {
			return nil, high, false
		}
		title := firstTrimmedString(inputMap, "subject", "title", "content", "description")
		if title == "" {
			return nil, high, false
		}
		id := idString(inputMap["id"])
		if id == "" {
			id = idString(inputMap["taskId"])
		}
		if id == "" {
			id = idString(inputMap["task_id"])
		}
		if id == "" {
			high++
			id = strconv.Itoa(high)
		} else {
			high = bumpHighWater(high, id)
		}
		next := agent.CloneChecklist(prev)
		next = append(next, agent.ChecklistItem{
			ID:         id,
			Title:      title,
			ActiveForm: firstTrimmedString(inputMap, "activeForm", "active_form"),
			Status:     normalizeChecklistStatus(inputMap["status"]),
		})
		return next, high, true

	case "taskupdate", "task_update":
		inputMap := parseInputMap(toolInput)
		if inputMap == nil {
			return nil, high, false
		}
		taskID := idString(inputMap["taskId"])
		if taskID == "" {
			taskID = idString(inputMap["task_id"])
		}
		if taskID == "" {
			taskID = idString(inputMap["id"])
		}
		statusStr, hasStatus := inputMap["status"].(string)
		hasStatus = hasStatus && strings.TrimSpace(statusStr) != ""
		if hasStatus && strings.EqualFold(strings.TrimSpace(statusStr), "deleted") {
			if taskID == "" {
				return nil, high, false
			}
			filtered := make([]agent.ChecklistItem, 0, len(prev))
			for _, it := range prev {
				if it.ID != taskID {
					filtered = append(filtered, it)
				}
			}
			return filtered, high, true
		}

		title := firstTrimmedString(inputMap, "subject", "title")
		activeForm := firstTrimmedString(inputMap, "activeForm", "active_form")
		next := agent.CloneChecklist(prev)
		if taskID != "" {
			for i := range next {
				if next[i].ID == taskID {
					if hasStatus {
						next[i].Status = normalizeChecklistStatus(statusStr)
					}
					if title != "" {
						next[i].Title = title
					}
					if activeForm != "" {
						next[i].ActiveForm = activeForm
					}
					return next, high, true
				}
			}
		}
		if title != "" {
			id := taskID
			if id == "" {
				high++
				id = strconv.Itoa(high)
			} else {
				high = bumpHighWater(high, id)
			}
			st := agent.ChecklistPending
			if hasStatus {
				st = normalizeChecklistStatus(statusStr)
			}
			next = append(next, agent.ChecklistItem{
				ID:         id,
				Title:      title,
				ActiveForm: activeForm,
				Status:     st,
			})
			return next, high, true
		}
		return nil, high, false
	}

	return nil, high, false
}
