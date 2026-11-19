package antigravity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

var metaCacheMu sync.Mutex

func (*Plugin) StatTranscript(homeDir, _, nativeID string) (string, int64, int64, error) {
	home := effectiveHome(homeDir)
	for _, dir := range transcriptCandidateDirs(home, nativeID) {
		for _, name := range []string{"transcript.jsonl", "transcript_full.jsonl"} {
			cand := filepath.Join(dir, name)
			st, err := os.Stat(cand)
			if err != nil || st.IsDir() {
				continue
			}
			size, modNano := st.Size(), st.ModTime().UnixNano()
			if name == "transcript.jsonl" {
				if fst, ferr := os.Stat(filepath.Join(dir, "transcript_full.jsonl")); ferr == nil && !fst.IsDir() {
					size += fst.Size()
					if fm := fst.ModTime().UnixNano(); fm > modNano {
						modNano = fm
					}
				}
			}
			return cand, size, modNano, nil
		}
	}
	return "", 0, 0, os.ErrNotExist
}

func (*Plugin) ComputeTranscriptUsage(homeDir, _, nativeID string, defaultLimit int) (agent.TranscriptUsage, bool) {
	limitTokens := defaultLimit
	if limitTokens < 1000000 {
		limitTokens = 1000000
	}
	home := effectiveHome(homeDir)
	var logFile string
	if home != "" && agentutil.IsSafeSessionID(nativeID) {
		cleanID := filepath.Base(filepath.Clean(nativeID))
		candidates := []string{
			filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "logs", "transcript.jsonl"),
		}
		for _, c := range candidates {
			if agentutil.FileExists(c) {
				logFile = c
				break
			}
		}
	}

	var sumInput, output, cacheRead, turnCount, lastTurnContext int
	if logFile != "" {
		if f, err := os.Open(logFile); err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1024*1024), 10*1024*1024)
			for sc.Scan() {
				var raw map[string]interface{}
				if json.Unmarshal([]byte(sc.Text()), &raw) != nil {
					continue
				}
				msgType, _ := raw["type"].(string)
				if inT, ok := raw["input_tokens"].(float64); ok && inT > 0 {
					if msgType == "PLANNER_RESPONSE" {
						lastTurnContext = int(inT)
					}
					crT, _ := raw["cache_read_tokens"].(float64)
					if uncached := int(inT) - int(crT); uncached > 0 {
						sumInput += uncached
					}
					cacheRead += int(crT)
				}
				if outT, ok := raw["output_tokens"].(float64); ok && outT > 0 {
					output += int(outT)
				}
				if raw["source"] == "USER" || raw["type"] == "USER_INPUT" {
					turnCount++
				}
			}
		}
	}

	costUSD := (float64(sumInput)*3.0 + float64(output)*15.0 + float64(cacheRead)*0.30) / 1000000.0
	return agent.TranscriptUsage{
		InputTokens:           sumInput,
		OutputTokens:          output,
		CacheReadTokens:       cacheRead,
		LastTurnContextTokens: lastTurnContext,
		ContextLimitTokens:    limitTokens,
		TurnCount:             turnCount,
		CostUSD:               costUSD,
	}, true
}

func (p *Plugin) ListRewindTurns(homeDir, cwd, nativeID string) ([]agent.RewindTurn, error) {
	t, err := p.ExtractTranscript(homeDir, cwd, nativeID)
	if err != nil || t == nil || len(t.Messages) == 0 {
		return nil, nil
	}
	var turns []agent.RewindTurn
	for _, m := range t.Messages {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			preview := strings.TrimSpace(m.Content)
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			turns = append(turns, agent.RewindTurn{
				Turn: len(turns) + 1, Timestamp: m.Timestamp, Preview: preview, RawPrompt: m.Content,
			})
		}
	}
	return turns, nil
}

func (*Plugin) CloneTranscriptBeforeTurn(_, _, _ string, _ int, _ string) (bool, error) {
	return false, nil
}

func (*Plugin) CleanSessionFiles(homeDir, _, nativeID string) error {
	home := effectiveHome(homeDir)
	if home == "" || !agentutil.IsSafeSessionID(nativeID) {
		return nil
	}
	seen := make(map[string]bool)
	for _, baseDir := range baseDirs(home) {
		if seen[baseDir] {
			continue
		}
		seen[baseDir] = true
		_ = os.RemoveAll(filepath.Join(baseDir, "brain", nativeID))
		for _, sub := range []string{"conversations", "annotations", "presence"} {
			if matches, err := filepath.Glob(filepath.Join(baseDir, sub, nativeID+".*")); err == nil {
				for _, match := range matches {
					_ = os.Remove(match)
				}
			}
			_ = os.Remove(filepath.Join(baseDir, sub, nativeID))
		}
		pruneMetadataCacheEntry(filepath.Join(baseDir, "cache", "conversation_metadata.json"), nativeID)
	}
	return nil
}

func pruneMetadataCacheEntry(metaPath, nativeID string) {
	metaCacheMu.Lock()
	defer metaCacheMu.Unlock()
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return
	}
	var root map[string]interface{}
	if json.Unmarshal(data, &root) != nil || root == nil {
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
	_ = agentutil.WriteFileAtomic(metaPath, out, 0o644)
}

func (*Plugin) ListWorkspaceDocuments(homeDir, _, nativeID string) []agent.WorkspaceDoc {
	home := effectiveHome(homeDir)
	if nativeID == "" || home == "" {
		return nil
	}
	var docs []agent.WorkspaceDoc
	for _, bDir := range []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", nativeID),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID),
		filepath.Join(home, ".antigravity", "brain", nativeID),
	} {
		if !agentutil.DirExists(bDir) {
			continue
		}
		files, err := os.ReadDir(bDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
				cat, label, prio := classifyDoc(f.Name())
				if cat == "other" {
					cat, label = "plan", "Antigravity Plan"
				}
				docs = append(docs, agent.WorkspaceDoc{
					FullPath:      filepath.Join(bDir, f.Name()),
					Title:         fmt.Sprintf("⚡ %s", f.Name()),
					RelPath:       f.Name(),
					Category:      cat,
					CategoryLabel: label,
					Priority:      prio + 5,
				})
			}
		}
	}
	return docs
}

func classifyDoc(name string) (category, label string, priority int) {
	nl := strings.ToLower(name)
	switch {
	case nl == "task.md" || nl == "implementation_plan.md" || nl == "walkthrough.md":
		return "plan", "Active Plan", 10
	case strings.Contains(nl, "plan") || strings.Contains(nl, "todo") || strings.Contains(nl, "backlog"):
		return "plan", "Plan / Task", 8
	case nl == "agents.md" || nl == "claude.md" || nl == "architecture.md":
		return "guidelines", "Guidelines", 7
	case nl == "readme.md" || strings.HasPrefix(nl, "readme"):
		return "project", "Readme", 5
	case strings.Contains(nl, "prd") || strings.Contains(nl, "rfc") || strings.Contains(nl, "spec"):
		return "project", "Specification", 6
	case strings.Contains(nl, "guide") || strings.Contains(nl, "handover"):
		return "guidelines", "Guide", 6
	default:
		return "other", "Document", 2
	}
}
