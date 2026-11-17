package agentutil

import (
	"encoding/json"
	"fmt"
	"strings"
)

func compactDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > 100 {
		return string(runes[:100]) + "..."
	}
	return s
}

func firstNonEmptyString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func FormatToolDisplay(name string, input interface{}) string {
	if name == "" {
		name = "tool"
	}
	if input == nil {
		return name
	}
	if m, ok := input.(map[string]interface{}); ok {
		for _, k := range []string{"command", "cmd", "query", "path", "file_path", "pattern", "prompt"} {
			if v, exists := m[k]; exists {
				if s, ok := v.(string); ok && s != "" {
					return fmt.Sprintf("%s: %s", name, TruncateTitle(strings.TrimSpace(s), 60))
				}
			}
		}
		b, _ := json.Marshal(m)
		s := string(b)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return fmt.Sprintf("%s(%s)", name, s)
	}
	if s, ok := input.(string); ok && s != "" {
		s = strings.TrimSpace(s)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return fmt.Sprintf("%s: %s", name, s)
	}
	return name
}

func FormatMCPToolDisplay(toolName string, toolInput any) (displayName string, detail string, isMCP bool) {
	var inputMap map[string]any
	switch v := toolInput.(type) {
	case map[string]any:
		inputMap = v
	case string:
		if s := strings.TrimSpace(v); strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			_ = json.Unmarshal([]byte(s), &inputMap)
		}
	}

	trimmedName := strings.TrimSpace(toolName)
	if strings.EqualFold(trimmedName, "call_mcp_tool") {
		isMCP = true
		server := firstNonEmptyString(inputMap, "ServerName", "server_name", "server")
		tool := firstNonEmptyString(inputMap, "ToolName", "tool_name", "tool")
		if server != "" && tool != "" {
			displayName = "mcp/" + server + "/" + tool
		} else if tool != "" {
			displayName = "mcp/" + tool
		} else if server != "" {
			displayName = "mcp/" + server
		} else {
			displayName = "call_mcp_tool"
		}

		var rawArgs any
		for _, k := range []string{"Arguments", "arguments", "args"} {
			if a, ok := inputMap[k]; ok && a != nil {
				rawArgs = a
				break
			}
		}
		if a, ok := rawArgs.(map[string]any); ok && len(a) > 0 {
			if b, err := json.Marshal(a); err == nil {
				detail = compactDetail(string(b))
			}
		} else if s, ok := rawArgs.(string); ok {
			if trimmed := strings.TrimSpace(s); trimmed != "" && trimmed != "{}" {
				detail = compactDetail(trimmed)
			}
		}
		return displayName, detail, isMCP
	}

	var parts []string
	if strings.HasPrefix(trimmedName, "mcp__") {
		parts = strings.SplitN(trimmedName, "__", 3)
	} else if strings.HasPrefix(trimmedName, "mcp_") {
		parts = strings.SplitN(trimmedName, "_", 3)
	}
	if len(parts) == 3 && parts[1] != "" && parts[2] != "" {
		isMCP = true
		displayName = "mcp/" + parts[1] + "/" + parts[2]
		if len(inputMap) > 0 {
			if b, err := json.Marshal(inputMap); err == nil {
				detail = compactDetail(string(b))
			}
		}
		return displayName, detail, isMCP
	}
	return "", "", false
}
