package daemon

import (
	"strings"

	"ackbar/internal/agentutil"
)

func compactDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > 100 {
		return string(runes[:100]) + "..."
	}
	return s
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
	return agentutil.FormatMCPToolDisplay(toolName, toolInput)
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
