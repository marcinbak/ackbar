package antigravity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func collectTranscriptSubagents(home, cleanID string) []agent.SubagentInfo {
	var file *os.File
	var openedDir string
	for _, dir := range transcriptCandidateDirs(home, cleanID) {
		f, err := os.Open(filepath.Join(dir, "transcript.jsonl"))
		if err == nil {
			file = f
			openedDir = dir
			break
		}
	}
	if file == nil {
		return nil
	}
	defer file.Close()

	var fileModTime time.Time
	if fStat, err := file.Stat(); err == nil {
		fileModTime = fStat.ModTime()
	}

	getFullLine := newFullStepLookup(openedDir)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var active []*agent.SubagentInfo
	convMap := make(map[string]*agent.SubagentInfo)
	var latestPending []*agent.SubagentInfo

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry transcriptEntry
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if hasTruncatedField(entry.TruncatedFields, "tool_calls") || hasTruncatedField(entry.TruncatedFields, "content") {
			if fullLine, ok := getFullLine(entry.StepIndex); ok {
				var fullEntry transcriptEntry
				if json.Unmarshal([]byte(fullLine), &fullEntry) == nil {
					entry = fullEntry
					line = fullLine
				}
			}
		}

		ts := parseStepTimestamp(entry.CreatedAt)
		if ts.IsZero() {
			ts = time.Now()
		}

		if entry.Type == "USER_INPUT" {
			active = nil
			convMap = make(map[string]*agent.SubagentInfo)
			latestPending = nil
			continue
		}

		if entry.Type == "PLANNER_RESPONSE" {
			hasInvoke := false
			for _, tc := range entry.ToolCalls {
				if tc.Name != "invoke_subagent" || tc.Args == nil {
					continue
				}
				hasInvoke = true
				batch := parseInvokeSubagentBatch(tc.Args["Subagents"], entry.StepIndex, ts)
				active = append(active, batch...)
				latestPending = batch
			}
			if !hasInvoke && len(entry.ToolCalls) == 0 && (entry.Status == "DONE" || entry.Status == "CANCELED" || entry.Status == "CANCELLED" || entry.Status == "ERROR") {
				for _, sub := range active {
					if sub != nil {
						sub.State = "completed"
					}
				}
				latestPending = nil
			}
		}

		if len(latestPending) > 0 && strings.Contains(entry.Content, "Created the following subagents:") {
			assignCreatedSubagentIDs(entry.Content, latestPending, convMap)
			latestPending = nil
		}

		if len(active) > 0 {
			for cID, sub := range convMap {
				if strings.Contains(entry.Content, fmt.Sprintf("sender=%s", cID)) ||
					strings.Contains(entry.Content, fmt.Sprintf(`sender="%s"`, cID)) ||
					(strings.Contains(line, fmt.Sprintf(`"conversationId":"%s"`, cID)) && strings.Contains(line, `"state":"idle"`)) {
					sub.State = "completed"
				}
			}
		}
	}

	if !fileModTime.IsZero() && time.Since(fileModTime) > 2*time.Minute {
		for _, s := range active {
			if s != nil {
				s.State = "completed"
			}
		}
	}

	var running []agent.SubagentInfo
	for _, s := range active {
		if s != nil && s.State == "running" && agentutil.IsSafeSessionID(s.ID) {
			running = append(running, *s)
		}
	}
	return running
}

func parseInvokeSubagentBatch(raw interface{}, stepIdx int, ts time.Time) []*agent.SubagentInfo {
	var subList []map[string]interface{}
	switch v := raw.(type) {
	case string:
		var parsed []map[string]interface{}
		if json.Unmarshal([]byte(v), &parsed) == nil {
			subList = parsed
		}
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				subList = append(subList, m)
			}
		}
	}
	var batch []*agent.SubagentInfo
	for i, subItem := range subList {
		role, _ := subItem["Role"].(string)
		typeName, _ := subItem["TypeName"].(string)
		prompt, _ := subItem["Prompt"].(string)
		name := role
		if name == "" {
			name = typeName
		}
		if name == "" {
			name = fmt.Sprintf("Subagent #%d", i+1)
		}
		batch = append(batch, &agent.SubagentInfo{
			ID:        fmt.Sprintf("subagent-%d-%d", stepIdx, i),
			Name:      name,
			Role:      role,
			Prompt:    prompt,
			State:     "running",
			StartedAt: ts,
		})
	}
	return batch
}

func assignCreatedSubagentIDs(content string, pending []*agent.SubagentInfo, convMap map[string]*agent.SubagentInfo) {
	batchIdx := 0
	for _, l := range strings.Split(content, "\n") {
		if !strings.Contains(l, `"conversationId":`) {
			continue
		}
		parts := strings.Split(l, `"conversationId":`)
		if len(parts) < 2 {
			continue
		}
		idVal := strings.Trim(parts[1], " \t\r\n,\"} ")
		idVal = strings.Trim(idVal, `"`)
		if idVal == "" || batchIdx >= len(pending) {
			continue
		}
		if agentutil.IsSafeSessionID(idVal) {
			pending[batchIdx].ID = idVal
			convMap[idVal] = pending[batchIdx]
		} else {
			pending[batchIdx].ID = ""
		}
		batchIdx++
	}
}
