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

type transcriptEntry struct {
	StepIndex       int      `json:"step_index"`
	Source          string   `json:"source"`
	Type            string   `json:"type"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	Content         string   `json:"content"`
	Thinking        string   `json:"thinking"`
	TruncatedFields []string `json:"truncated_fields"`
	Media           []struct {
		MimeType string `json:"mime_type"`
		URI      string `json:"uri"`
	} `json:"media"`
	ToolCalls []struct {
		Name string                 `json:"name"`
		Args map[string]interface{} `json:"args"`
	} `json:"tool_calls"`
}

func (p *Plugin) ExtractTranscript(homeDir, _, nativeID string) (*agent.Transcript, error) {
	home := effectiveHome(homeDir)
	if !agentutil.IsSafeSessionID(nativeID) {
		return nil, fmt.Errorf("invalid conversation ID %q", nativeID)
	}
	var file *os.File
	var openedDir string
	var openErr error
	for _, dir := range transcriptCandidateDirs(home, nativeID) {
		for _, name := range []string{"transcript.jsonl", "transcript_full.jsonl"} {
			file, openErr = os.Open(filepath.Join(dir, name))
			if openErr == nil {
				openedDir = dir
				break
			}
		}
		if file != nil {
			break
		}
	}
	if file == nil {
		return nil, fmt.Errorf("antigravity log not found for conversation %s: %w", nativeID, openErr)
	}
	defer file.Close()

	getFullLine := newFullStepLookup(openedDir)
	messages := make([]agent.TranscriptMessage, 0)
	lastPlannerHadImageGenTool := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		isGenericMedia := strings.Contains(line, `"GENERIC"`) && strings.Contains(line, `"media"`)
		if !strings.Contains(line, `"USER_INPUT"`) && !strings.Contains(line, `"PLANNER_RESPONSE"`) && !strings.Contains(line, `"CHECKPOINT"`) && !isGenericMedia {
			continue
		}
		var entry transcriptEntry
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if hasRelevantTruncatedField(entry.TruncatedFields) {
			if fullLine, ok := getFullLine(entry.StepIndex); ok {
				var fullEntry transcriptEntry
				if json.Unmarshal([]byte(fullLine), &fullEntry) == nil {
					entry = fullEntry
				}
			}
		}
		ts := parseStepTimestamp(entry.CreatedAt)
		if ts.IsZero() {
			ts = time.Now()
		}

		switch entry.Type {
		case "USER_INPUT":
			lastPlannerHadImageGenTool = false
			if entry.Source != "" && !strings.EqualFold(entry.Source, "USER_EXPLICIT") && !strings.EqualFold(entry.Source, "USER") {
				continue
			}
			cleanContent, _ := p.FilterUserEntry(nil, entry.Content)
			for _, m := range entry.Media {
				if m.URI != "" {
					cleanURI := strings.TrimPrefix(m.URI, "file://")
					att := fmt.Sprintf("[Attached file: %q]", cleanURI)
					if cleanContent != "" {
						cleanContent += "\n\n" + att
					} else {
						cleanContent = att
					}
				}
			}
			if cleanContent != "" {
				messages = append(messages, agent.TranscriptMessage{Role: "user", Content: cleanContent, Timestamp: ts})
			}

		case "PLANNER_RESPONSE":
			lastPlannerHadImageGenTool = hasImageGenTool(&entry)
			appendPlannerResponse(&messages, &entry, ts)

		case "CHECKPOINT":
			if strings.Contains(entry.Content, "CHECKPOINT") {
				messages = append(messages, agent.TranscriptMessage{Role: "system", Content: strings.TrimSpace(entry.Content), Timestamp: ts})
			}

		case "GENERIC":
			if !lastPlannerHadImageGenTool || len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
				continue
			}
			if strings.Contains(entry.Content, "Showing binary file of type ") || strings.Contains(entry.Content, "The following is the entire, complete content of the requested file.") {
				continue
			}
			appendGenericMedia(&messages[len(messages)-1], &entry)
		}
	}
	return &agent.Transcript{SessionID: nativeID, Messages: messages}, nil
}

func newFullStepLookup(openedDir string) func(int) (string, bool) {
	var fullSteps map[int]string
	loaded := false
	return func(stepIdx int) (string, bool) {
		if !loaded {
			loaded = true
			fullSteps = make(map[int]string)
			if fullFile, err := os.Open(filepath.Join(openedDir, "transcript_full.jsonl")); err == nil {
				defer fullFile.Close()
				fs := bufio.NewScanner(fullFile)
				fs.Buffer(make([]byte, 1024*1024), 20*1024*1024)
				for fs.Scan() {
					fLine := strings.TrimSpace(fs.Text())
					if fLine == "" || !strings.Contains(fLine, `"step_index"`) {
						continue
					}
					var idxOnly struct {
						StepIndex int `json:"step_index"`
					}
					if json.Unmarshal([]byte(fLine), &idxOnly) == nil {
						fullSteps[idxOnly.StepIndex] = fLine
					}
				}
			}
		}
		l, ok := fullSteps[stepIdx]
		return l, ok
	}
}
