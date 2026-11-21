package claude

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type sessionRegistryMeta struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	NameSource string `json:"nameSource"`
	Entrypoint string `json:"entrypoint"`
	Kind       string `json:"kind"`
	Version    string `json:"version"`
}

func readRegularFileSlice(filePath string, offset, maxBytes int64) ([]byte, int64, int64, error) {
	f, err := openRegularFileNonBlocking(filePath)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	size := st.Size()
	modNano := st.ModTime().UnixNano()
	if size == 0 {
		return nil, 0, modNano, nil
	}
	if offset < 0 {
		offset = size - maxBytes
		if offset < 0 {
			offset = 0
		}
	}
	if offset >= size {
		return nil, size, modNano, nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, size, modNano, err
	}
	toRead := size - offset
	if toRead > maxBytes {
		toRead = maxBytes
	}
	buf := make([]byte, toRead)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, size, modNano, err
	}
	return buf[:n], size, modNano, nil
}

func findRegistryMeta(home string, targetPID int, targetID string) *sessionRegistryMeta {
	if home == "" || (targetPID <= 0 && targetID == "") {
		return nil
	}
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	files, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, f.Name()))
		if err != nil {
			continue
		}
		var meta sessionRegistryMeta
		if json.Unmarshal(data, &meta) != nil {
			continue
		}
		if (targetPID > 0 && meta.PID == targetPID) || (targetID != "" && meta.SessionID == targetID) {
			return &meta
		}
	}
	return nil
}

func parsePIDAndTargetID(nativeID string) (int, string, bool) {
	if strings.HasPrefix(nativeID, "proc-") {
		pid, err := strconv.Atoi(strings.TrimPrefix(nativeID, "proc-"))
		if err != nil || pid <= 0 {
			return 0, "", false
		}
		return pid, "", true
	}
	if !agentutil.IsUUID(nativeID) {
		return 0, "", false
	}
	return 0, nativeID, true
}

func (p *Plugin) ReadSessionMetadata(homeDir, cwd, nativeID string) *agent.SessionMeta {
	metaInfo := &agent.SessionMeta{
		Title:      p.ResolveSessionTitle(homeDir, cwd, nativeID),
		ContextPct: readContextUsage(homeDir, cwd, nativeID),
	}
	targetPID, targetID, ok := parsePIDAndTargetID(nativeID)
	if !ok {
		return metaInfo
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return metaInfo
	}

	if reg := findRegistryMeta(home, targetPID, targetID); reg != nil {
		if reg.Name != "" && reg.NameSource == "custom" {
			metaInfo.CustomTitle = reg.Name
		}
		metaInfo.Entrypoint = reg.Entrypoint
		metaInfo.Kind = reg.Kind
		metaInfo.Version = reg.Version
		if targetID == "" && agentutil.IsUUID(reg.SessionID) {
			targetID = reg.SessionID
		}
	}

	targetFile := FindTranscriptPath(home, cwd, targetID)
	if targetFile == "" {
		return metaInfo
	}
	data, _, _, err := readRegularFileSlice(targetFile, -1, 64*1024)
	if err != nil || len(data) == 0 {
		return metaInfo
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		ts := parseTimestampOrZero(obj["timestamp"])
		if !ts.IsZero() && (metaInfo.LastMessageAt.IsZero() || ts.After(metaInfo.LastMessageAt)) {
			metaInfo.LastMessageAt = ts
		}
		if gb, _ := obj["gitBranch"].(string); gb != "" && metaInfo.GitBranch == "" {
			metaInfo.GitBranch = gb
		}
		if ct, _ := obj["customTitle"].(string); ct != "" {
			metaInfo.CustomTitle = ct
		}
		if at, _ := obj["aiTitle"].(string); at != "" {
			metaInfo.AITitle = at
		}
		if tt, _ := obj["title"].(string); tt != "" && metaInfo.AITitle == "" {
			metaInfo.AITitle = tt
		}
		if sm, _ := obj["summary"].(string); sm != "" {
			metaInfo.AIDescription = sm
		}
		if promptText := firstPromptField(obj); promptText != "" {
			if clean, keep := p.FilterUserEntry(obj, promptText); keep {
				if metaInfo.FirstPrompt == "" {
					metaInfo.FirstPrompt = clean
				}
				metaInfo.LastPrompt = clean
				if !ts.IsZero() && (metaInfo.LastUserMessageAt.IsZero() || ts.After(metaInfo.LastUserMessageAt)) {
					metaInfo.LastUserMessageAt = ts
				}
			}
		}
	}
	return metaInfo
}

func firstPromptField(obj map[string]interface{}) string {
	if s, _ := obj["lastPrompt"].(string); s != "" {
		return s
	}
	if s, _ := obj["prompt"].(string); s != "" {
		return s
	}
	msgType, _ := obj["type"].(string)
	msg, _ := obj["message"].(map[string]interface{})
	role := ""
	if msg != nil {
		role, _ = msg["role"].(string)
	}
	if msgType == "user" || role == "user" {
		return extractUserMessageText(obj)
	}
	return ""
}

func parseTimestampOrZero(raw interface{}) time.Time {
	s, _ := raw.(string)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
