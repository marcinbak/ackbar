package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ackbar/internal/agentutil"
)

type cachedTitleEntry struct {
	title       string
	source      string
	filePath    string
	size        int64
	modTimeNano int64
}

var (
	titleCacheMu sync.RWMutex
	titleCache   = make(map[string]cachedTitleEntry)
)

func (p *Plugin) ResolveSessionTitle(homeDir, cwd, nativeID string) string {
	targetPID, targetID, ok := parsePIDAndTargetID(nativeID)
	if !ok {
		return ""
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return ""
	}

	cacheKey := home + "\x00" + cwd + "\x00" + nativeID
	titleCacheMu.RLock()
	cached, hasCached := titleCache[cacheKey]
	titleCacheMu.RUnlock()
	if hasCached && cached.title != "" {
		if cached.filePath != "" {
			if st, err := os.Stat(cached.filePath); err == nil && st.Size() == cached.size && st.ModTime().UnixNano() == cached.modTimeNano {
				return cached.title
			}
		} else if cached.source == "custom" {
			return cached.title
		}
	}

	var title, derivedTitle string
	source := "fallback"
	if reg := findRegistryMeta(home, targetPID, targetID); reg != nil {
		if reg.Name != "" && reg.NameSource == "custom" {
			title, source = reg.Name, "custom"
		} else if reg.Name != "" {
			derivedTitle = reg.Name
		}
		if targetID == "" && agentutil.IsUUID(reg.SessionID) {
			targetID = reg.SessionID
		}
	}

	var targetFile string
	var fileSize, modNano int64
	if title == "" && targetID != "" {
		targetFile = FindTranscriptPath(home, cwd, targetID)
		if targetFile != "" {
			var aiTitle, firstPrompt string
			title, source, aiTitle, firstPrompt, fileSize, modNano = scanTranscriptTitle(p, targetFile)
			if title == "" && aiTitle != "" {
				title, source = aiTitle, "ai"
			}
			if title == "" && firstPrompt != "" {
				title, source = agentutil.TruncateTitle(firstPrompt, 50), "prompt"
			}
		}
		if title == "" && derivedTitle != "" {
			title, source = derivedTitle, "fallback"
		}
	}

	if title == "" && targetID != "" {
		title, source = lookupHistoryTitle(p, home, targetID, source)
	}
	if title == "" && targetID != "" && len(targetID) >= 8 {
		title = fmt.Sprintf("Claude Code (%s)", targetID[:8])
	}
	if title != "" {
		titleCacheMu.Lock()
		titleCache[cacheKey] = cachedTitleEntry{title: title, source: source, filePath: targetFile, size: fileSize, modTimeNano: modNano}
		titleCacheMu.Unlock()
	}
	return title
}

func scanTranscriptTitle(p *Plugin, filePath string) (title, source, aiTitle, firstPrompt string, size, modNano int64) {
	tail, sz, mod, err := readRegularFileSlice(filePath, -1, 65536)
	if err != nil {
		return "", "fallback", "", "", 0, 0
	}
	size, modNano = sz, mod
	lines := strings.Split(string(tail), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var e struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			AgentName   string `json:"agentName"`
			AITitle     string `json:"aiTitle"`
			Title       string `json:"title"`
			Name        string `json:"name"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.CustomTitle != "" {
			return e.CustomTitle, "custom", aiTitle, firstPrompt, size, modNano
		}
		if e.AgentName != "" {
			return e.AgentName, "custom", aiTitle, firstPrompt, size, modNano
		}
		if e.Title != "" && title == "" {
			title, source = e.Title, "ai"
		}
		if e.AITitle != "" && aiTitle == "" {
			aiTitle = e.AITitle
		}
		if e.Type == "rename" && e.Name != "" {
			return e.Name, "custom", aiTitle, firstPrompt, size, modNano
		}
	}
	if title == "" && size > 0 {
		head, _, _, herr := readRegularFileSlice(filePath, 0, 65536)
		if herr == nil {
			for _, line := range strings.Split(string(head), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var obj map[string]interface{}
				if json.Unmarshal([]byte(line), &obj) != nil {
					continue
				}
				if ct, _ := obj["customTitle"].(string); ct != "" && title == "" {
					title, source = ct, "custom"
				}
				if at, _ := obj["aiTitle"].(string); at != "" && aiTitle == "" {
					aiTitle = at
				}
				if tt, _ := obj["title"].(string); tt != "" && aiTitle == "" {
					aiTitle = tt
				}
				if firstPrompt == "" {
					if pt := firstPromptField(obj); pt != "" {
						if clean, keep := p.FilterUserEntry(obj, pt); keep {
							firstPrompt = clean
						}
					}
				}
			}
		}
	}
	return title, source, aiTitle, firstPrompt, size, modNano
}

func lookupHistoryTitle(p *Plugin, home, targetID, curSource string) (string, string) {
	data, _, _, err := readRegularFileSlice(filepath.Join(home, ".claude", "history.jsonl"), -1, 8<<20)
	if err != nil || len(data) == 0 {
		return "", curSource
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var e struct {
			SessionID    string `json:"sessionId"`
			SessionIDOld string `json:"session_id"`
			DisplayName  string `json:"displayName"`
			CustomTitle  string `json:"customTitle"`
			Title        string `json:"title"`
			Display      string `json:"display"`
			Prompt       string `json:"prompt"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		sID := firstNonEmpty(e.SessionID, e.SessionIDOld)
		if sID != targetID {
			continue
		}
		if e.DisplayName != "" {
			return e.DisplayName, "custom"
		}
		if e.CustomTitle != "" {
			return e.CustomTitle, "custom"
		}
		if e.Title != "" {
			return e.Title, "ai"
		}
		pTxt := firstNonEmpty(e.Display, e.Prompt)
		if pTxt != "" {
			if clean, keep := p.FilterUserEntry(nil, pTxt); keep {
				return agentutil.TruncateTitle(clean, 50), "prompt"
			}
		}
	}
	return "", curSource
}
