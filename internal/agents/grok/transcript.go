package grok

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (p *Plugin) ExtractTranscript(homeDir, cwd, nativeID string) (*agent.Transcript, error) {
	msgs, err := extractTranscriptMessages(p, homeDir, cwd, nativeID)
	if err != nil {
		return nil, err
	}
	return &agent.Transcript{
		NativeID: nativeID,
		Agent:    "grok",
		Cwd:      cwd,
		Messages: msgs,
	}, nil
}

func extractTranscriptMessages(p *Plugin, home, cwd, nativeID string) ([]agent.TranscriptMessage, error) {
	if !agentutil.IsSafeGrokSessionID(nativeID) {
		return nil, fmt.Errorf("invalid grok session ID: %q", nativeID)
	}
	if sessDir := FindSessionDirForHome(home, cwd, nativeID); sessDir != "" {
		chatFile := filepath.Join(sessDir, "chat_history.jsonl")
		if agentutil.FileExists(chatFile) {
			return extractChatHistoryMessages(p, chatFile)
		}
		updatesFile := filepath.Join(sessDir, "updates.jsonl")
		if agentutil.FileExists(updatesFile) {
			return extractUpdatesMessages(p, updatesFile)
		}
	}
	if legacyFile := findLegacyLog(home, nativeID); legacyFile != "" {
		return extractLegacyMessages(p, legacyFile)
	}
	return nil, fmt.Errorf("grok transcript not found for session %q", nativeID)
}

func extractChatHistoryMessages(p *Plugin, chatFile string) ([]agent.TranscriptMessage, error) {
	f, err := os.Open(chatFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []agent.TranscriptMessage
	var pendingAssistant *agent.TranscriptMessage
	flushAssistant := func() {
		if pendingAssistant != nil {
			if pendingAssistant.Content != "" || len(pendingAssistant.ToolCalls) > 0 || pendingAssistant.Thinking != "" {
				messages = append(messages, *pendingAssistant)
			}
			pendingAssistant = nil
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		tVal, _ := raw["type"].(string)
		roleVal, _ := raw["role"].(string)
		ts := parseGrokTime(raw["timestamp"])
		if ts.IsZero() {
			ts = parseGrokTime(raw["created_at"])
		}

		if tVal == "system" || roleVal == "system" || tVal == "function_call_output" || tVal == "tool_result" {
			continue
		}

		if tVal == "user" || roleVal == "user" {
			text := parseGrokContentText(raw["content"])
			if text == "" {
				text, _ = raw["text"].(string)
			}
			if cleaned, keep := p.FilterUserEntry(raw, text); keep {
				flushAssistant()
				messages = append(messages, agent.TranscriptMessage{Role: "user", Content: cleaned, Timestamp: ts})
			}
			continue
		}

		if tVal == "reasoning" {
			thinking := parseGrokContentText(raw["summary"])
			if thinking == "" {
				thinking = parseGrokContentText(raw["content"])
			}
			if thinking != "" {
				if pendingAssistant == nil {
					pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
				}
				if pendingAssistant.Thinking != "" {
					pendingAssistant.Thinking += "\n" + thinking
				} else {
					pendingAssistant.Thinking = thinking
				}
			}
			continue
		}

		if tVal == "function_call" || tVal == "tool_call" || tVal == "custom_tool_call" {
			toolName := firstMapStr(raw, "name", "tool")
			display := agentutil.FormatToolDisplay(toolName, parseGrokToolInput(raw))
			if pendingAssistant == nil {
				pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, display)
			continue
		}

		if tVal == "assistant" || roleVal == "assistant" {
			content := parseGrokContentText(raw["content"])
			if content == "" {
				content, _ = raw["text"].(string)
			}
			if pendingAssistant == nil {
				pendingAssistant = &agent.TranscriptMessage{Role: "assistant", Timestamp: ts}
			}
			if content != "" {
				if pendingAssistant.Content != "" {
					pendingAssistant.Content += "\n\n" + content
				} else {
					pendingAssistant.Content = content
				}
			}
			if !ts.IsZero() && pendingAssistant.Timestamp.IsZero() {
				pendingAssistant.Timestamp = ts
			}
			if tCalls, ok := raw["tool_calls"].([]interface{}); ok {
				for _, tc := range tCalls {
					if tcMap, ok := tc.(map[string]interface{}); ok {
						tName, _ := tcMap["name"].(string)
						pendingAssistant.ToolCalls = append(pendingAssistant.ToolCalls, agentutil.FormatToolDisplay(tName, parseGrokToolInput(tcMap)))
					}
				}
			}
		}
	}
	flushAssistant()
	return messages, nil
}

func (*Plugin) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	if raw != nil {
		if sr, ok := raw["synthetic_reason"].(string); ok && strings.TrimSpace(sr) != "" {
			return "", false
		}
		if t, _ := raw["type"].(string); t == "user" {
			if _, hasIdx := raw["prompt_index"]; !hasIdx &&
				!strings.Contains(content, "<user_query>") &&
				(strings.Contains(content, "<user_info>") || strings.Contains(content, "<system-reminder>")) {
				return "", false
			}
		}
	}
	if start := strings.Index(content, "<user_query>"); start != -1 {
		end := strings.LastIndex(content, "</user_query>")
		if end > start+len("<user_query>") {
			clean := strings.TrimSpace(content[start+len("<user_query>") : end])
			return clean, clean != ""
		}
	}
	clean := agentutil.StripXMLBlock(content, "user_info")
	if raw != nil {
		clean = agentutil.StripXMLBlock(clean, "system-reminder")
	}
	clean = agentutil.CleanCLIEnvironmentTags(clean)
	trimmed := strings.TrimSpace(clean)
	return trimmed, trimmed != ""
}

func (*Plugin) HasConversationArtifacts(homeDir, nativeID string) bool {
	if sessDir := FindSessionDirForHome(homeDir, "", nativeID); sessDir != "" {
		for _, f := range []string{"chat_history.jsonl", "updates.jsonl", "summary.json"} {
			if stat, err := os.Stat(filepath.Join(sessDir, f)); err == nil && stat.Size() > 0 {
				return true
			}
		}
	}
	userHome := homeDir
	if userHome == "" {
		userHome = agentutil.RealUserHome()
	}
	for _, baseDir := range []string{HomeDir(homeDir), filepath.Join(userHome, ".grok")} {
		if stat, err := os.Stat(filepath.Join(baseDir, "sessions", nativeID+".jsonl")); err == nil && stat.Size() > 0 {
			return true
		}
		if stat, err := os.Stat(filepath.Join(baseDir, "sessions", nativeID, "transcript.jsonl")); err == nil && stat.Size() > 0 {
			return true
		}
	}
	return false
}

func (*Plugin) CleanSessionFiles(homeDir, cwd, nativeID string) error {
	if nativeID == "" || !agentutil.IsSafeGrokSessionID(nativeID) {
		return nil
	}
	if sessDir := FindSessionDirForHome(homeDir, cwd, nativeID); sessDir != "" {
		_ = os.RemoveAll(sessDir)
	}
	grokHome := HomeDir(homeDir)
	_ = os.RemoveAll(filepath.Join(grokHome, "sessions", nativeID))
	_ = os.Remove(filepath.Join(grokHome, "sessions", nativeID+".jsonl"))
	return nil
}

func (*Plugin) StatTranscript(homeDir, cwd, nativeID string) (string, int64, int64, error) {
	if nativeID == "" {
		return "", 0, 0, fmt.Errorf("nativeID cannot be empty")
	}
	var filePath string
	if sessDir := FindSessionDirForHome(homeDir, cwd, nativeID); sessDir != "" {
		candHistory := filepath.Join(sessDir, "chat_history.jsonl")
		candUpdates := filepath.Join(sessDir, "updates.jsonl")
		if agentutil.FileExists(candHistory) {
			filePath = candHistory
		} else if agentutil.FileExists(candUpdates) {
			filePath = candUpdates
		}
	}
	if filePath == "" {
		candFlat := filepath.Join(HomeDir(homeDir), "sessions", nativeID+".jsonl")
		if agentutil.FileExists(candFlat) {
			filePath = candFlat
		} else {
			candDir := filepath.Join(HomeDir(homeDir), "sessions", nativeID, "transcript.jsonl")
			if agentutil.FileExists(candDir) {
				filePath = candDir
			}
		}
	}
	if filePath == "" {
		return "", 0, 0, os.ErrNotExist
	}
	st, err := os.Stat(filePath)
	if err != nil {
		return "", 0, 0, err
	}
	size, modTimeNano := st.Size(), st.ModTime().UnixNano()
	if filepath.Base(filePath) == "chat_history.jsonl" {
		siblingUpdates := filepath.Join(filepath.Dir(filePath), "updates.jsonl")
		if updStat, err := os.Stat(siblingUpdates); err == nil && !updStat.IsDir() {
			size += updStat.Size()
			if updMod := updStat.ModTime().UnixNano(); updMod > modTimeNano {
				modTimeNano = updMod
			}
		}
	}
	return filePath, size, modTimeNano, nil
}
