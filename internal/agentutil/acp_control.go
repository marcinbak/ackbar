package agentutil

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"ackbar/internal/agent"
)

func ParseACPPermissionRequest(rawID json.RawMessage, reqID string, params map[string]any) (*agent.ACPControlPrompt, bool) {
	if params == nil {
		return nil, false
	}
	toolCall, _ := params["toolCall"].(map[string]any)
	toolName := acpFirstStr(toolCall, "toolName", "title", "name", "kind")
	if toolName == "" {
		toolName = acpFirstStr(params, "toolName", "title", "name")
	}
	if toolName == "" {
		toolName = "Tool"
	}
	rawInput := firstMapAny(toolCall, "rawInput", "input", "arguments")
	if rawInput == nil {
		rawInput = firstMapAny(params, "rawInput", "input")
	}
	desc := acpFirstStr(params, "description", "reason", "title")
	if desc == "" {
		desc = acpFirstStr(toolCall, "title", "description")
	}
	if desc == "" {
		desc = toolName
	}

	var opts []agent.ACPPermissionOption
	var labels []string
	for _, rawOpt := range ParseInterfaceSlice(params["options"]) {
		m, ok := rawOpt.(map[string]any)
		if !ok {
			continue
		}
		optID := acpFirstStr(m, "optionId", "option_id", "id")
		kind := acpFirstStr(m, "kind")
		name := acpFirstStr(m, "name", "label")
		if optID == "" {
			optID = kind
		}
		if name == "" {
			name = optID
		}
		if optID == "" {
			continue
		}
		opts = append(opts, agent.ACPPermissionOption{OptionID: optID, Kind: kind, Name: name})
		labels = append(labels, name)
	}
	if len(labels) == 0 {
		labels = []string{"Allow Once", "Reject"}
	}

	blocked := &agent.Blocked{
		Kind:      agent.BlockPermission,
		ToolName:  toolName,
		Reason:    desc,
		Question:  "Allow " + toolName + "?",
		Options:   labels,
		RequestID: reqID,
		Since:     time.Now(),
	}
	return &agent.ACPControlPrompt{
		RequestID: reqID,
		RawID:     append([]byte(nil), bytes.TrimSpace(rawID)...),
		Method:    "session/request_permission",
		ToolName:  toolName,
		ToolInput: rawInput,
		Blocked:   blocked,
		Options:   opts,
	}, true
}

func BuildACPPermissionResponse(prompt *agent.ACPControlPrompt, approved bool, answer string) any {
	trimmed := strings.TrimSpace(answer)
	if !approved && strings.EqualFold(trimmed, "cancel") {
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	}
	var opts []agent.ACPPermissionOption
	if prompt != nil {
		opts = prompt.Options
	}
	if trimmed != "" {
		for _, o := range opts {
			if !optionMatchesApproval(o, approved) {
				continue
			}
			if strings.EqualFold(trimmed, o.OptionID) || strings.EqualFold(trimmed, o.Name) || (o.Kind != "" && strings.EqualFold(trimmed, o.Kind)) {
				return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": o.OptionID}}
			}
		}
	}
	if approved {
		selected := pickOptionByKind(opts, "allow_once", "allow")
		if selected == "" {
			for _, o := range opts {
				if optionMatchesApproval(o, true) {
					selected = o.OptionID
					break
				}
			}
		}
		if selected == "" {
			selected = "allow_once"
		}
		return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": selected}}
	}
	if selected := pickOptionByKind(opts, "reject_once", "reject"); selected != "" {
		return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": selected}}
	}
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}

func optionMatchesApproval(o agent.ACPPermissionOption, approved bool) bool {
	k, id := strings.ToLower(o.Kind), strings.ToLower(o.OptionID)
	isAllow := strings.HasPrefix(k, "allow") || strings.HasPrefix(k, "proceed") || strings.HasPrefix(id, "allow") || strings.HasPrefix(id, "proceed")
	isReject := strings.HasPrefix(k, "reject") || strings.HasPrefix(k, "deny") || strings.HasPrefix(id, "reject") || strings.HasPrefix(id, "deny")
	if approved {
		return !isReject
	}
	return !isAllow
}

func pickOptionByKind(opts []agent.ACPPermissionOption, exactKind, prefix string) string {
	for _, o := range opts {
		if strings.EqualFold(o.Kind, exactKind) {
			return o.OptionID
		}
	}
	for _, o := range opts {
		if strings.HasPrefix(strings.ToLower(o.Kind), prefix) {
			return o.OptionID
		}
	}
	return ""
}

func ParseACPCommands(raw any) []agent.ACPCommandInfo {
	var out []agent.ACPCommandInfo
	for _, item := range ParseInterfaceSlice(raw) {
		switch v := item.(type) {
		case string:
			if name := strings.TrimSpace(v); name != "" {
				out = append(out, agent.ACPCommandInfo{Name: name})
			}
		case map[string]any:
			name := acpFirstStr(v, "name", "command", "trigger")
			if name == "" {
				continue
			}
			hint := acpFirstStr(v, "argumentHint", "argument_hint", "hint")
			if hint == "" {
				if inp, ok := v["input"].(map[string]any); ok {
					hint = acpFirstStr(inp, "hint", "argumentHint", "placeholder")
				}
			}
			out = append(out, agent.ACPCommandInfo{
				Name:         name,
				Description:  acpFirstStr(v, "description"),
				ArgumentHint: hint,
			})
		}
	}
	return out
}

func parseACPAuthMethods(raw any) []agent.ACPAuthMethod {
	var out []agent.ACPAuthMethod
	for _, item := range ParseInterfaceSlice(raw) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := acpFirstStr(m, "id", "methodId")
		if id == "" {
			continue
		}
		out = append(out, agent.ACPAuthMethod{
			ID:          id,
			Name:        acpFirstStr(m, "name"),
			Description: acpFirstStr(m, "description"),
		})
	}
	return out
}

func extractACPModelFromMap(m map[string]any) string {
	if m == nil {
		return ""
	}
	if v := acpFirstStr(m, "currentModelId", "modelId", "currentModel", "current_model_id"); v != "" {
		return v
	}
	if models, ok := m["models"].(map[string]any); ok {
		if v := acpFirstStr(models, "currentModelId", "currentModel", "modelId"); v != "" {
			return v
		}
	}
	for _, item := range ParseInterfaceSlice(m["configOptions"]) {
		opt, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.ToLower(acpFirstStr(opt, "id", "category"))
		if id == "model" {
			if v := acpFirstStr(opt, "currentValue", "value"); v != "" {
				return v
			}
		}
	}
	return ""
}

func extractACPText(m map[string]any, keys ...string) string {
	for _, k := range keys {
		val := m[k]
		if val == nil {
			continue
		}
		if s := stringifyACPValue(val); s != "" {
			return s
		}
	}
	return ""
}

func stringifyACPValue(val any) string {
	switch v := val.(type) {
	case nil:
		return ""
	case string:
		return v
	case map[string]any:
		for _, k := range []string{"text", "content", "data"} {
			if inner, exists := v[k]; exists && inner != nil {
				if s := stringifyACPValue(inner); s != "" {
					return s
				}
			}
		}
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	case []any:
		var b strings.Builder
		for _, item := range v {
			b.WriteString(stringifyACPValue(item))
		}
		return b.String()
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	return ""
}
