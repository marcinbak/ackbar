package antigravity

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

func (*Plugin) ApplyToolChecklist(prev []agent.ChecklistItem, toolName string, toolInput any) ([]agent.ChecklistItem, bool) {
	_ = prev
	if baseToolName(toolName) != "update_steps" {
		return nil, false
	}
	inputMap := parseInputMap(toolInput)
	if inputMap == nil {
		return nil, false
	}
	rawSteps, hasSteps := inputMap["steps"]
	if !hasSteps {
		return nil, false
	}
	parsedSteps := agentutil.ParseInterfaceSlice(rawSteps)
	if parsedSteps == nil {
		return nil, false
	}

	items := make([]agent.ChecklistItem, 0, min(len(parsedSteps), agent.MaxChecklistItems))
	for _, rawStep := range parsedSteps {
		if len(items) >= agent.MaxChecklistItems {
			break
		}
		stepMap, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		rawTitle := firstTrimmedString(stepMap, "title", "content", "text", "name")
		if rawTitle == "" {
			continue
		}
		item := agent.SanitizeChecklistItem(agent.ChecklistItem{
			ID:         idString(stepMap["id"]),
			Title:      rawTitle,
			ActiveForm: firstTrimmedString(stepMap, "active_form", "activeForm"),
			Status:     normalizeChecklistStatus(stepMap["status"]),
		})
		if item.Title == "" {
			continue
		}
		items = append(items, item)
	}
	if len(items) > 0 {
		return items, true
	}
	if len(parsedSteps) == 0 {
		return []agent.ChecklistItem{}, true
	}
	return nil, false
}

type antigravityChecklistTracker struct {
	items   []agent.ChecklistItem
	cleared bool
}

func (*Plugin) NewChecklistTracker() agent.ChecklistTracker {
	return &antigravityChecklistTracker{}
}

func (t *antigravityChecklistTracker) Seed(items []agent.ChecklistItem, cleared bool, highWater int) {
	_ = highWater
	if cleared {
		t.items = nil
		t.cleared = true
		return
	}
	if len(items) == 0 {
		t.items = nil
		t.cleared = false
		return
	}
	limit := min(len(items), agent.MaxChecklistItems)
	sanitized := make([]agent.ChecklistItem, 0, limit)
	for _, it := range items[:limit] {
		s := agent.SanitizeChecklistItem(it)
		if s.Title != "" {
			sanitized = append(sanitized, s)
		}
	}
	t.items = sanitized
	t.cleared = false
}

func (t *antigravityChecklistTracker) Apply(toolName string, toolInput any) ([]agent.ChecklistItem, bool, bool) {
	next, matched := (*Plugin)(nil).ApplyToolChecklist(t.items, toolName, toolInput)
	if !matched {
		return nil, false, false
	}
	if len(next) == 0 {
		t.items = nil
		t.cleared = true
		return nil, true, true
	}
	t.items = next
	t.cleared = false
	return agent.CloneChecklist(next), false, true
}

func (t *antigravityChecklistTracker) Snapshot() ([]agent.ChecklistItem, bool, int) {
	if t.cleared {
		return nil, true, 0
	}
	return agent.CloneChecklist(t.items), false, 0
}
