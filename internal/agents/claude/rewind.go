package claude

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

type rewindTurnEntry struct {
	turn      agent.RewindTurn
	lineIndex int
}

func (p *Plugin) ListRewindTurns(homeDir, cwd, nativeID string) ([]agent.RewindTurn, error) {
	entries, _, _ := p.extractRewindTurnsWithLines(homeDir, cwd, nativeID)
	out := make([]agent.RewindTurn, len(entries))
	for i, e := range entries {
		out[i] = e.turn
	}
	return out, nil
}

func (p *Plugin) CloneTranscriptBeforeTurn(homeDir, cwd, nativeID string, beforeTurn int, newNativeID string) (bool, error) {
	if !agentutil.IsUUID(nativeID) || !agentutil.IsUUID(newNativeID) || beforeTurn < 1 {
		return false, fmt.Errorf("invalid rewind target")
	}
	entries, rawLines, targetFile := p.extractRewindTurnsWithLines(homeDir, cwd, nativeID)
	if beforeTurn > len(entries) || targetFile == "" {
		return false, fmt.Errorf("turn %d not found in transcript", beforeTurn)
	}

	cutIdx := entries[beforeTurn-1].lineIndex
	for cutIdx > 0 {
		prevLine := strings.TrimSpace(rawLines[cutIdx-1])
		if prevLine == "" {
			cutIdx--
			continue
		}
		var prevMap map[string]interface{}
		if json.Unmarshal([]byte(prevLine), &prevMap) == nil {
			if prevType, _ := prevMap["type"].(string); prevType == "attachment" {
				cutIdx--
				continue
			}
		}
		break
	}

	newFile := filepath.Join(filepath.Dir(targetFile), newNativeID+".jsonl")
	destFile, err := os.OpenFile(newFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}
	writer := bufio.NewWriter(destFile)
	for i := 0; i < cutIdx; i++ {
		line := strings.TrimSpace(rawLines[i])
		if line == "" {
			continue
		}
		var item map[string]interface{}
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		if itemType, _ := item["type"].(string); itemType == "last-prompt" {
			continue
		}
		item["sessionId"] = newNativeID
		if rewritten, err := json.Marshal(item); err == nil {
			_, _ = writer.Write(rewritten)
			_, _ = writer.WriteString("\n")
		}
	}
	if err := writer.Flush(); err != nil {
		_ = destFile.Close()
		return false, err
	}
	return true, destFile.Close()
}

func (p *Plugin) extractRewindTurnsWithLines(homeDir, cwd, nativeID string) ([]rewindTurnEntry, []string, string) {
	targetFile := FindTranscriptPath(homeDir, cwd, nativeID)
	if targetFile == "" {
		return nil, nil, ""
	}
	f, err := openRegularFileNonBlocking(targetFile)
	if err != nil {
		return nil, nil, ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var lines []string
	var entries []rewindTurnEntry
	lineIdx := 0
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			var raw map[string]interface{}
			if json.Unmarshal([]byte(trimmed), &raw) == nil {
				if msgType, _ := raw["type"].(string); msgType == "user" {
					fullText := extractUserMessageText(raw)
					if cleanPrompt, keep := p.FilterUserEntry(raw, fullText); keep && strings.TrimSpace(cleanPrompt) != "" {
						ts := parseTimestampOrNow(raw["timestamp"])
						preview := strings.TrimSpace(cleanPrompt)
						if len(preview) > 120 {
							preview = preview[:120] + "..."
						}
						entries = append(entries, rewindTurnEntry{
							turn: agent.RewindTurn{
								Turn:      len(entries) + 1,
								Timestamp: ts,
								Preview:   preview,
								RawPrompt: cleanPrompt,
							},
							lineIndex: lineIdx,
						})
					}
				}
			}
		}
		lineIdx++
	}
	return entries, lines, targetFile
}
