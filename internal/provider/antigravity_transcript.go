package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ackbar/internal/daemon"
)

var antigravityMetaMu sync.Mutex

func (a *AntigravityProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	title := a.ResolveSessionTitle(cwd, nativeID)
	if title != "" {
		return &daemon.SessionMeta{
			Title: title,
		}
	}
	return nil
}

func (a *AntigravityProvider) ResolveSessionTitle(cwd, nativeID string) string {
	return daemon.ReadAntigravitySessionTitle(cwd, nativeID)
}

func (a *AntigravityProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	candidatePaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(home, ".antigravity", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
	}

	if profilePaths, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl")); len(profilePaths) > 0 {
		candidatePaths = append(candidatePaths, profilePaths...)
	}
	if profilePaths2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl")); len(profilePaths2) > 0 {
		candidatePaths = append(candidatePaths, profilePaths2...)
	}

	var file *os.File
	var err error
	for _, path := range candidatePaths {
		file, err = os.Open(path)
		if err == nil {
			break
		}
	}

	if file == nil {
		return nil, fmt.Errorf("antigravity log not found for conversation %s: %w", nativeID, err)
	}
	defer file.Close()

	var messages []daemon.TranscriptMessage
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry struct {
			StepIndex int    `json:"step_index"`
			Source    string `json:"source"`
			Type      string `json:"type"`
			Status    string `json:"status"`
			CreatedAt string `json:"created_at"`
			Content   string `json:"content"`
			Thinking  string `json:"thinking"`
			ToolCalls []struct {
				Name string                 `json:"name"`
				Args map[string]interface{} `json:"args"`
			} `json:"tool_calls"`
		}

		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339, entry.CreatedAt)
		if ts.IsZero() {
			ts = time.Now()
		}

		if entry.Type == "USER_INPUT" {
			cleanContent := daemon.CleanAntigravityPrompt(entry.Content)
			if cleanContent != "" {
				messages = append(messages, daemon.TranscriptMessage{
					Role:      "user",
					Content:   cleanContent,
					Timestamp: ts,
				})
			}
		} else if entry.Type == "PLANNER_RESPONSE" {
			var tools []string
			for _, tc := range entry.ToolCalls {
				action := ""
				if tc.Args != nil {
					if act, ok := tc.Args["toolAction"].(string); ok && act != "" {
						action = strings.Trim(act, "\"")
					} else if sum, ok := tc.Args["toolSummary"].(string); ok && sum != "" {
						action = strings.Trim(sum, "\"")
					}
				}
				if action != "" {
					tools = append(tools, fmt.Sprintf("%s (%s)", tc.Name, action))
				} else if tc.Name != "" {
					tools = append(tools, tc.Name)
				}
			}
			if entry.Content != "" || entry.Thinking != "" || len(tools) > 0 {
				if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
					last := &messages[len(messages)-1]
					if entry.Content != "" {
						if last.Content != "" {
							if !strings.Contains(last.Content, entry.Content) {
								last.Content += "\n\n" + entry.Content
							}
						} else {
							last.Content = entry.Content
						}
					}
					if entry.Thinking != "" {
						if last.Thinking == "" {
							last.Thinking = entry.Thinking
						} else if !strings.Contains(last.Thinking, entry.Thinking) {
							last.Thinking += "\n\n" + entry.Thinking
						}
					}
					if len(tools) > 0 {
						last.ToolCalls = append(last.ToolCalls, tools...)
					}
					last.Timestamp = ts
				} else {
					messages = append(messages, daemon.TranscriptMessage{
						Role:      "assistant",
						Content:   entry.Content,
						Thinking:  entry.Thinking,
						ToolCalls: tools,
						Timestamp: ts,
					})
				}
			}
		} else if entry.Type == "CHECKPOINT" {
			if strings.Contains(entry.Content, "CHECKPOINT") {
				messages = append(messages, daemon.TranscriptMessage{
					Role:      "system",
					Content:   strings.TrimSpace(entry.Content),
					Timestamp: ts,
				})
			}
		}
	}

	return messages, nil
}

func (a *AntigravityProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if home == "" || nativeID == "" || nativeID == "." || nativeID == ".." ||
		filepath.Base(nativeID) != nativeID ||
		strings.ContainsAny(nativeID, "/\\") ||
		!isSafeSessionID(nativeID) {
		return nil
	}

	baseDirs := []string{
		filepath.Join(home, ".gemini", "antigravity"),
		filepath.Join(home, ".gemini", "antigravity-cli"),
		filepath.Join(home, ".antigravity"),
	}

	if profilePaths, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*")); len(profilePaths) > 0 {
		baseDirs = append(baseDirs, profilePaths...)
	}
	if profilePaths2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity")); len(profilePaths2) > 0 {
		baseDirs = append(baseDirs, profilePaths2...)
	}

	seen := make(map[string]bool)
	for _, baseDir := range baseDirs {
		if seen[baseDir] {
			continue
		}
		seen[baseDir] = true

		// Remove brain directory
		_ = os.RemoveAll(filepath.Join(baseDir, "brain", nativeID))

		// Remove matching files in conversations, annotations, and presence subdirectories
		for _, sub := range []string{"conversations", "annotations", "presence"} {
			if matches, err := filepath.Glob(filepath.Join(baseDir, sub, nativeID+".*")); err == nil {
				for _, match := range matches {
					_ = os.Remove(match)
				}
			}
			_ = os.Remove(filepath.Join(baseDir, sub, nativeID))
		}

		// Prune nativeID from cache/conversation_metadata.json if that file exists
		metaPath := filepath.Join(baseDir, "cache", "conversation_metadata.json")
		func() {
			antigravityMetaMu.Lock()
			defer antigravityMetaMu.Unlock()

			data, err := os.ReadFile(metaPath)
			if err != nil {
				return
			}
			var root map[string]interface{}
			if err := json.Unmarshal(data, &root); err != nil || root == nil {
				return
			}
			convs, ok := root["conversations"].(map[string]interface{})
			if !ok || convs == nil {
				return
			}
			if _, exists := convs[nativeID]; !exists {
				return
			}
			delete(convs, nativeID)
			out, err := json.MarshalIndent(root, "", "  ")
			if err != nil {
				return
			}

			tmpFile, err := os.CreateTemp(filepath.Dir(metaPath), "conversation_metadata.*.tmp")
			if err != nil {
				return
			}
			tmpName := tmpFile.Name()
			defer os.Remove(tmpName)

			if _, err := tmpFile.Write(out); err != nil {
				_ = tmpFile.Close()
				return
			}
			if err := tmpFile.Close(); err != nil {
				return
			}
			_ = os.Rename(tmpName, metaPath)
		}()
	}

	return nil
}
