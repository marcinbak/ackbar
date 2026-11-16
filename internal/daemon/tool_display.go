package daemon

import (
	"encoding/json"
	"strings"
)

func compactDetail(s string) string {
	if s = strings.Join(strings.Fields(s), " "); len(s) > 100 {
		return s[:100] + "..."
	}
	return s
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func extractToolInfoInput(info map[string]interface{}) any {
	if info == nil {
		return nil
	}
	for _, k := range []string{"parameters", "arguments", "args"} {
		if v, ok := info[k]; ok && v != nil {
			return v
		}
	}
	return nil
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
		server := firstString(inputMap, "ServerName", "server_name", "server")
		tool := firstString(inputMap, "ToolName", "tool_name", "tool")
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

func FormatSpoolToolSummary(toolName string, toolInput any) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	if mcpName, mcpDetail, isMCP := FormatMCPToolDisplay(name, toolInput); isMCP {
		if mcpDetail != "" {
			return mcpName + ": " + mcpDetail
		}
		return mcpName
	}
	detail := extractFilePathFromToolInput(toolInput)
	if detail == "" {
		detail = extractCommandFromToolInput(toolInput)
	}
	if detail = compactDetail(detail); detail != "" {
		return name + ": " + detail
	}
	return name
}
