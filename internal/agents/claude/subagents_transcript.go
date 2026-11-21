package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (p *Plugin) ListSubagents(homeDir, cwd, nativeID string) ([]agent.SubagentInfo, error) {
	if !agentutil.IsUUID(nativeID) {
		return nil, nil
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return nil, nil
	}
	diskRecs, _ := discoverDiskSubagents(home, cwd, nativeID, false)
	if len(diskRecs) > 0 {
		out := make([]agent.SubagentInfo, 0, len(diskRecs))
		for _, r := range diskRecs {
			out = append(out, agent.SubagentInfo{
				ID: r.id, Name: r.name, Role: r.role, Prompt: r.prompt, State: r.state, StartedAt: r.startedAt,
			})
		}
		return out, nil
	}

	targetFile := FindTranscriptPath(home, cwd, nativeID)
	if targetFile == "" {
		return nil, fmt.Errorf("claude log not found for session %s", nativeID)
	}
	f, err := openRegularFileNonBlocking(targetFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var modTime time.Time
	if st, serr := f.Stat(); serr == nil {
		modTime = st.ModTime()
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var active []*agent.SubagentInfo
	toolUseMap := make(map[string]*agent.SubagentInfo)
	lastStopReason := ""

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || (!strings.Contains(line, `"user"`) && !strings.Contains(line, `"assistant"`)) {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		ts := parseTimestampOrNow(raw["timestamp"])
		switch raw["type"] {
		case "user":
			lastStopReason = ""
			isToolResult, textParts := inspectSubagentUserLine(raw, toolUseMap)
			if !isToolResult {
				if _, keep := p.FilterUserEntry(raw, strings.Join(textParts, "\n")); keep {
					active = nil
					toolUseMap = make(map[string]*agent.SubagentInfo)
				}
			}
		case "assistant":
			if sr := collectAssistantSubagents(raw, ts, &active, toolUseMap); sr != "" {
				lastStopReason = sr
			}
		}
	}

	if lastStopReason == "end_turn" || lastStopReason == "stop_sequence" || (!modTime.IsZero() && time.Since(modTime) > 2*time.Minute) {
		for _, s := range active {
			if s != nil {
				s.State = "completed"
			}
		}
	}
	var running []agent.SubagentInfo
	for _, s := range active {
		if s != nil && s.State == "running" {
			running = append(running, *s)
		}
	}
	return running, nil
}

func inspectSubagentUserLine(raw map[string]interface{}, toolUseMap map[string]*agent.SubagentInfo) (bool, []string) {
	isToolResult := false
	var parts []string
	msgObj, ok := raw["message"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	if s, ok := msgObj["content"].(string); ok {
		return false, []string{s}
	}
	arr, ok := msgObj["content"].([]interface{})
	if !ok {
		return false, nil
	}
	for _, item := range arr {
		block, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		switch block["type"] {
		case "tool_result":
			isToolResult = true
			toolID, _ := block["tool_use_id"].(string)
			if sub, found := toolUseMap[toolID]; found {
				resStr := fmt.Sprintf("%v", block["content"])
				if !strings.Contains(resStr, "teammate_spawned") && !strings.Contains(resStr, "Spawned successfully") {
					sub.State = "completed"
				}
			}
		case "text":
			if txt, ok := block["text"].(string); ok && txt != "" {
				parts = append(parts, txt)
			}
		}
	}
	return isToolResult, parts
}

func collectAssistantSubagents(raw map[string]interface{}, ts time.Time, active *[]*agent.SubagentInfo, toolUseMap map[string]*agent.SubagentInfo) string {
	msgObj, ok := raw["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	sr, _ := msgObj["stop_reason"].(string)
	arr, ok := msgObj["content"].([]interface{})
	if !ok {
		return sr
	}
	for _, item := range arr {
		block, ok := item.(map[string]interface{})
		if !ok || block["type"] != "tool_use" {
			continue
		}
		toolName, _ := block["name"].(string)
		if toolName != "Agent" && toolName != "Task" {
			continue
		}
		toolID, _ := block["id"].(string)
		var prompt, desc, subType string
		if inp, ok := block["input"].(map[string]interface{}); ok {
			prompt, _ = inp["prompt"].(string)
			desc, _ = inp["description"].(string)
			subType, _ = inp["subagent_type"].(string)
		}
		name := firstNonEmpty(subType, desc, toolName)
		sub := &agent.SubagentInfo{ID: toolID, Name: name, Role: subType, Prompt: prompt, State: "running", StartedAt: ts}
		*active = append(*active, sub)
		if toolID != "" {
			toolUseMap[toolID] = sub
		}
	}
	return sr
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
