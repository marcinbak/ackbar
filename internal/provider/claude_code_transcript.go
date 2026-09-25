package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/daemon"
)

func (c *ClaudeProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	return daemon.ReadClaudeSessionMeta(cwd, nativeID)
}

func (c *ClaudeProvider) ResolveSessionTitle(cwd, nativeID string) string {
	meta := c.ReadSessionMetadata(cwd, nativeID)
	if meta != nil {
		if meta.CustomTitle != "" {
			return meta.CustomTitle
		}
		if meta.AITitle != "" {
			return meta.AITitle
		}
		if meta.Title != "" {
			return meta.Title
		}
		if meta.FirstPrompt != "" {
			return daemon.TruncateTitle(meta.FirstPrompt)
		}
	}
	return ""
}

func (c *ClaudeProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	var targetFile string
	projectsDir := filepath.Join(home, ".claude", "projects")

	if cwd != "" {
		encodedCwd := strings.ReplaceAll(cwd, "/", "-")
		cand := filepath.Join(projectsDir, encodedCwd, nativeID+".jsonl")
		if fileExists(cand) {
			targetFile = cand
		}
	}

	if targetFile == "" && dirExists(projectsDir) {
		entries, err := os.ReadDir(projectsDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					cand := filepath.Join(projectsDir, e.Name(), nativeID+".jsonl")
					if fileExists(cand) {
						targetFile = cand
						break
					}
				}
			}
		}
	}

	if targetFile == "" {
		profileDirs, _ := filepath.Glob(filepath.Join(home, ".claude-profiles", "*", "projects"))
		for _, pDir := range profileDirs {
			if cwd != "" {
				encodedCwd := strings.ReplaceAll(cwd, "/", "-")
				cand := filepath.Join(pDir, encodedCwd, nativeID+".jsonl")
				if fileExists(cand) {
					targetFile = cand
					break
				}
			}
			if dirExists(pDir) {
				entries, err := os.ReadDir(pDir)
				if err == nil {
					for _, e := range entries {
						if e.IsDir() {
							cand := filepath.Join(pDir, e.Name(), nativeID+".jsonl")
							if fileExists(cand) {
								targetFile = cand
								break
							}
						}
					}
				}
			}
			if targetFile != "" {
				break
			}
		}
	}

	if targetFile == "" {
		return nil, fmt.Errorf("claude log not found for session %s", nativeID)
	}

	file, err := os.Open(targetFile)
	if err != nil {
		return nil, err
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

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		msgType, _ := raw["type"].(string)
		tsStr, _ := raw["timestamp"].(string)
		ts, _ := time.Parse(time.RFC3339, tsStr)
		if ts.IsZero() {
			ts = time.Now()
		}

		if msgType == "user" {
			var fullText string
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentStr, ok := msgObj["content"].(string); ok {
					fullText = contentStr
				} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
					var textParts []string
					for _, cItem := range contentArr {
						if cMap, ok := cItem.(map[string]interface{}); ok {
							if cType, ok := cMap["type"].(string); ok && cType == "text" {
								if txt, ok := cMap["text"].(string); ok && txt != "" {
									textParts = append(textParts, txt)
								}
							}
						} else if cStr, ok := cItem.(string); ok && cStr != "" {
							textParts = append(textParts, cStr)
						}
					}
					fullText = strings.Join(textParts, "\n")
				}
			} else if contentStr, ok := raw["content"].(string); ok {
				fullText = contentStr
			} else if textStr, ok := raw["text"].(string); ok {
				fullText = textStr
			}

			fullText = strings.TrimSpace(fullText)
			if fullText != "" && !strings.Contains(fullText, "<EXTREMELY_IMPORTANT>") {
				messages = append(messages, daemon.TranscriptMessage{
					Role:      "user",
					Content:   fullText,
					Timestamp: ts,
				})
			}
		} else if msgType == "assistant" {
			if msgObj, ok := raw["message"].(map[string]interface{}); ok {
				if contentStr, ok := msgObj["content"].(string); ok && contentStr != "" {
					if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
						last := &messages[len(messages)-1]
						if last.Content != "" {
							if !strings.Contains(last.Content, contentStr) {
								last.Content += "\n\n" + contentStr
							}
						} else {
							last.Content = contentStr
						}
						last.Timestamp = ts
					} else {
						messages = append(messages, daemon.TranscriptMessage{
							Role:      "assistant",
							Content:   contentStr,
							Timestamp: ts,
						})
					}
				} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
					var textParts []string
					var tools []string
					for _, cItem := range contentArr {
						if cMap, ok := cItem.(map[string]interface{}); ok {
							if cType, ok := cMap["type"].(string); ok {
								if cType == "text" {
									if txt, ok := cMap["text"].(string); ok && txt != "" {
										textParts = append(textParts, txt)
									}
								} else if cType == "tool_use" {
									tName, _ := cMap["name"].(string)
									if tName != "" {
										detail := ""
										if inputMap, ok := cMap["input"].(map[string]interface{}); ok {
											if cmd, ok := inputMap["command"].(string); ok && cmd != "" {
												detail = strings.TrimSpace(cmd)
											} else if desc, ok := inputMap["description"].(string); ok && desc != "" {
												detail = strings.TrimSpace(desc)
											} else if fp, ok := inputMap["file_path"].(string); ok && fp != "" {
												detail = strings.TrimSpace(fp)
											} else if p, ok := inputMap["path"].(string); ok && p != "" {
												detail = strings.TrimSpace(p)
											} else if pat, ok := inputMap["pattern"].(string); ok && pat != "" {
												detail = strings.TrimSpace(pat)
											} else if q, ok := inputMap["query"].(string); ok && q != "" {
												detail = strings.TrimSpace(q)
											}
										}
										if detail != "" {
											tools = append(tools, fmt.Sprintf("%s: %s", tName, detail))
										} else {
											tools = append(tools, tName)
										}
									}
								}
							}
						}
					}
					fullText := strings.Join(textParts, "\n\n")
					if fullText != "" || len(tools) > 0 {
						if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
							last := &messages[len(messages)-1]
							if fullText != "" {
								if last.Content != "" {
									if !strings.Contains(last.Content, fullText) {
										last.Content += "\n\n" + fullText
									}
								} else {
									last.Content = fullText
								}
							}
							if len(tools) > 0 {
								last.ToolCalls = append(last.ToolCalls, tools...)
							}
							last.Timestamp = ts
						} else {
							messages = append(messages, daemon.TranscriptMessage{
								Role:      "assistant",
								Content:   fullText,
								ToolCalls: tools,
								Timestamp: ts,
							})
						}
					}
				}
			}
		}
	}

	return messages, nil
}

func (c *ClaudeProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if nativeID == "" {
		return nil
	}
	claudeDir := filepath.Join(home, ".claude")
	claudeProjectsDir := filepath.Join(claudeDir, "projects")
	if projDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
		for _, pDir := range projDirs {
			if pDir.IsDir() {
				projPath := filepath.Join(claudeProjectsDir, pDir.Name())
				_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("%s.jsonl", nativeID)))
				_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("agent-%s.jsonl", nativeID)))
				_ = os.RemoveAll(filepath.Join(projPath, nativeID))
			}
		}
	}

	sessionsDir := filepath.Join(claudeDir, "sessions")
	if sFiles, err := os.ReadDir(sessionsDir); err == nil {
		for _, sf := range sFiles {
			if strings.HasSuffix(sf.Name(), ".json") {
				metaPath := filepath.Join(sessionsDir, sf.Name())
				if data, err := os.ReadFile(metaPath); err == nil {
					if strings.Contains(string(data), nativeID) {
						_ = os.Remove(metaPath)
					}
				}
			}
		}
	}

	_ = os.RemoveAll(filepath.Join(claudeDir, "tasks", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "session-env", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "file-history", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "shell-snapshots", nativeID))

	return nil
}
