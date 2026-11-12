package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxTranscriptCacheEntries = 128

type transcriptCacheEntry struct {
	agent           string
	nativeID        string
	cwd             string
	filePath        string
	fileSize        int64
	modTimeUnixNano int64
	header128       [128]byte
	hasHeader       bool
	messages        []TranscriptMessage
	cachedAt        time.Time
}

type subagentDiskCacheEntry struct {
	subagents []SubagentInfo
	cachedAt  time.Time
}

// ParseClaudeCodeJSONL parses lines from an io.Reader into TranscriptMessages, coalescing onto base.
func ParseClaudeCodeJSONL(r io.Reader, filterUser func(raw map[string]interface{}, text string) (string, bool), base []TranscriptMessage) ([]TranscriptMessage, error) {
	messages := make([]TranscriptMessage, len(base))
	for i, m := range base {
		messages[i] = m
		if len(m.ToolCalls) > 0 {
			tc := make([]string, len(m.ToolCalls))
			copy(tc, m.ToolCalls)
			messages[i].ToolCalls = tc
		}
	}

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.Contains(line, `"user"`) && !strings.Contains(line, `"assistant"`) {
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

			if filterUser != nil {
				if clean, keep := filterUser(raw, fullText); keep {
					messages = append(messages, TranscriptMessage{
						Role:      "user",
						Content:   clean,
						Timestamp: ts,
					})
				}
			} else if strings.TrimSpace(fullText) != "" {
				messages = append(messages, TranscriptMessage{
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
						messages = append(messages, TranscriptMessage{
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
							messages = append(messages, TranscriptMessage{
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

func resolveTranscriptFileStat(home, agent, nativeID, cwd string) (string, int64, int64, error) {
	if nativeID == "" {
		return "", 0, 0, fmt.Errorf("nativeID cannot be empty")
	}

	var filePath string
	switch {
	case agent == "claude-code" || strings.HasPrefix(agent, "claude"):
		filePath = findClaudeTranscriptPath(home, nativeID, cwd)

	case agent == "antigravity":
		cleanID := filepath.Base(filepath.Clean(nativeID))
		candidateDirs := []string{
			filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "logs"),
			filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "logs"),
			filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "logs"),
		}
		if p1, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "logs")); len(p1) > 0 {
			candidateDirs = append(candidateDirs, p1...)
		}
		if p2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "logs")); len(p2) > 0 {
			candidateDirs = append(candidateDirs, p2...)
		}
		if p3, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity-cli", "brain", cleanID, ".system_generated", "logs")); len(p3) > 0 {
			candidateDirs = append(candidateDirs, p3...)
		}
		for _, dir := range candidateDirs {
			cand := filepath.Join(dir, "transcript.jsonl")
			if stat, err := os.Stat(cand); err == nil && !stat.IsDir() {
				filePath = cand
				break
			}
			candFull := filepath.Join(dir, "transcript_full.jsonl")
			if stat, err := os.Stat(candFull); err == nil && !stat.IsDir() {
				filePath = candFull
				break
			}
		}

	case agent == "codex":
		if matches, _ := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "*"+nativeID+"*.jsonl")); len(matches) > 0 {
			filePath = matches[0]
		} else if matches, _ := filepath.Glob(filepath.Join(home, ".codex", "archived_sessions", "*"+nativeID+"*.jsonl")); len(matches) > 0 {
			filePath = matches[0]
		}
	}

	if filePath == "" {
		return "", 0, 0, os.ErrNotExist
	}

	size, modTimeNano, err := statTranscriptFile(agent, filePath)
	if err != nil {
		return "", 0, 0, err
	}
	return filePath, size, modTimeNano, nil
}

func statTranscriptFile(agent, filePath string) (int64, int64, error) {
	st, err := os.Stat(filePath)
	if err != nil {
		return 0, 0, err
	}
	size := st.Size()
	modTimeNano := st.ModTime().UnixNano()

	if (agent == "antigravity" || strings.HasPrefix(agent, "antigravity")) && filepath.Base(filePath) == "transcript.jsonl" {
		siblingFull := filepath.Join(filepath.Dir(filePath), "transcript_full.jsonl")
		if fullStat, ferr := os.Stat(siblingFull); ferr == nil && !fullStat.IsDir() {
			size += fullStat.Size()
			if fullMod := fullStat.ModTime().UnixNano(); fullMod > modTimeNano {
				modTimeNano = fullMod
			}
		}
	}
	return size, modTimeNano, nil
}

func (s *Server) getCachedTranscript(agent, nativeID, cwd string) ([]TranscriptMessage, bool) {
	if s == nil {
		return nil, false
	}

	key := agent + ":" + nativeID + ":" + cwd
	s.transcriptCacheMu.RLock()
	if s.transcriptCache == nil {
		s.transcriptCacheMu.RUnlock()
		return nil, false
	}
	entry, exists := s.transcriptCache[key]
	if !exists || entry == nil || entry.filePath == "" {
		s.transcriptCacheMu.RUnlock()
		return nil, false
	}
	filePath := entry.filePath
	entrySize := entry.fileSize
	entryModTime := entry.modTimeUnixNano
	entryHead := entry.header128
	entryHasHead := entry.hasHeader
	cachedMsgs := entry.messages
	s.transcriptCacheMu.RUnlock()

	curSize, curModTime, err := statTranscriptFile(agent, filePath)
	if err != nil {
		s.transcriptCacheMu.Lock()
		delete(s.transcriptCache, key)
		s.transcriptCacheMu.Unlock()
		return nil, false
	}

	// Exact file match
	if curSize == entrySize && curModTime == entryModTime {
		res := make([]TranscriptMessage, len(cachedMsgs))
		for i, m := range cachedMsgs {
			res[i] = m
			if len(m.ToolCalls) > 0 {
				tc := make([]string, len(m.ToolCalls))
				copy(tc, m.ToolCalls)
				res[i].ToolCalls = tc
			}
		}
		return res, true
	}

	// Incremental append for Claude Code
	if (agent == "claude-code" || strings.HasPrefix(agent, "claude")) && curSize > entrySize && entrySize > 0 {
		f, ferr := os.Open(filePath)
		if ferr == nil {
			defer f.Close()
			var head [128]byte
			n, rerr := io.ReadFull(f, head[:])
			if rerr == nil && n == 128 && entryHasHead && head == entryHead {
				if _, serr := f.Seek(entrySize, io.SeekStart); serr == nil {
					updatedMsgs, perr := ParseClaudeCodeJSONL(f, (ClaudeAgentAdapter{}).FilterUserEntry, cachedMsgs)
					if perr == nil && len(updatedMsgs) >= len(cachedMsgs) {
						newEntry := &transcriptCacheEntry{
							agent:           agent,
							nativeID:        nativeID,
							cwd:             cwd,
							filePath:        filePath,
							fileSize:        curSize,
							modTimeUnixNano: curModTime,
							header128:       entryHead,
							hasHeader:       entryHasHead,
							messages:        updatedMsgs,
							cachedAt:        time.Now(),
						}
						s.transcriptCacheMu.Lock()
						s.transcriptCache[key] = newEntry
						s.transcriptCacheMu.Unlock()

						res := make([]TranscriptMessage, len(updatedMsgs))
						for i, m := range updatedMsgs {
							res[i] = m
							if len(m.ToolCalls) > 0 {
								tc := make([]string, len(m.ToolCalls))
								copy(tc, m.ToolCalls)
								res[i].ToolCalls = tc
							}
						}
						return res, true
					}
				}
			}
		}
	}

	return nil, false
}

func (s *Server) setCachedTranscript(agent, nativeID, cwd, filePath string, fileSize, modTimeNano int64, msgs []TranscriptMessage) {
	if s == nil || filePath == "" {
		return
	}

	var head [128]byte
	hasHeader := false
	if f, err := os.Open(filePath); err == nil {
		n, _ := io.ReadFull(f, head[:])
		if n == 128 {
			hasHeader = true
		}
		f.Close()
	}

	key := agent + ":" + nativeID + ":" + cwd
	s.transcriptCacheMu.Lock()
	defer s.transcriptCacheMu.Unlock()

	if s.transcriptCache == nil {
		s.transcriptCache = make(map[string]*transcriptCacheEntry)
	}

	if len(s.transcriptCache) >= maxTranscriptCacheEntries {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range s.transcriptCache {
			if oldestTime.IsZero() || v.cachedAt.Before(oldestTime) {
				oldestTime = v.cachedAt
				oldestKey = k
			}
		}
		if oldestKey != "" {
			delete(s.transcriptCache, oldestKey)
		}
	}

	storedMsgs := make([]TranscriptMessage, len(msgs))
	for i, m := range msgs {
		storedMsgs[i] = m
		if len(m.ToolCalls) > 0 {
			tc := make([]string, len(m.ToolCalls))
			copy(tc, m.ToolCalls)
			storedMsgs[i].ToolCalls = tc
		}
	}

	s.transcriptCache[key] = &transcriptCacheEntry{
		agent:           agent,
		nativeID:        nativeID,
		cwd:             cwd,
		filePath:        filePath,
		fileSize:        fileSize,
		modTimeUnixNano: modTimeNano,
		header128:       head,
		hasHeader:       hasHeader,
		messages:        storedMsgs,
		cachedAt:        time.Now(),
	}
}
