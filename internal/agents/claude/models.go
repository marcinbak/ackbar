package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type cachedModelEntry struct {
	size        int64
	modTimeNano int64
	modTime     time.Time
	model       string
}

var (
	modelCacheMu sync.RWMutex
	modelCache   = make(map[string]cachedModelEntry)
)

func (*Plugin) DefaultModels() []agent.ModelSuggestion {
	return []agent.ModelSuggestion{
		{ID: "claude-fable-5-1", Label: "Claude Fable 5.1"},
		{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"},
		{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5"},
		{ID: "claude-opus-4-6", Label: "Claude Opus 4.6"},
		{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6"},
		{ID: "claude-haiku-4-5", Label: "Claude Haiku 4.5"},
		{ID: "claude-3-7-sonnet-20250219", Label: "Claude 3.7 Sonnet"},
		{ID: "claude-3-5-sonnet-20241022", Label: "Claude 3.5 Sonnet"},
	}
}

func (p *Plugin) CollectModels(_ string) []agent.ModelSuggestion {
	return p.DefaultModels()
}

func (*Plugin) ValidateModelID(model string) bool {
	return model != "" && len(model) <= 128 && !strings.HasPrefix(model, "-") && !strings.ContainsAny(model, "\x00\r\n \t")
}

func (*Plugin) NormalizeModelID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "default") || strings.EqualFold(s, "synthetic") || strings.HasPrefix(s, "<") {
		return ""
	}
	if idx := strings.Index(s, "["); idx != -1 {
		s = strings.TrimSpace(s[:idx])
	}
	return s
}

func (p *Plugin) ResolveArtifactModel(homeDir, accConfigDir, cwd, nativeID string) (string, time.Time) {
	if !agentutil.IsUUID(nativeID) {
		return "", time.Time{}
	}
	home := effectiveHome(homeDir)
	encodedCwd := encodeProjectDir(cwd)
	var candidates []string
	if accConfigDir != "" {
		accDir := agentutil.ExpandTildeWithHome(accConfigDir, home)
		candidates = append(candidates, filepath.Join(accDir, "projects", encodedCwd, nativeID+".jsonl"))
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".claude", "projects", encodedCwd, nativeID+".jsonl"))
	}
	if matched := FindTranscriptPath(home, cwd, nativeID); matched != "" {
		candidates = append(candidates, matched)
	}
	for _, cand := range candidates {
		if !isRegularFile(cand) {
			continue
		}
		model, modTime, err := readCachedModel(cand, func(f *os.File) (string, error) {
			return p.parseTranscriptTailModel(f)
		})
		if err == nil && model != "" {
			return model, modTime
		}
	}
	return "", time.Time{}
}

func (p *Plugin) ResolveConfiguredModel(homeDir string, acct *agent.AgentAccount) string {
	return p.ResolveWorkspaceModel(homeDir, "", acct)
}

func (p *Plugin) ResolveWorkspaceModel(homeDir, cwd string, acct *agent.AgentAccount) string {
	home := effectiveHome(homeDir)
	if cwd != "" {
		if m := p.readSettingsModel(filepath.Join(cwd, ".claude", "settings.local.json")); m != "" {
			return m
		}
		if m := p.readSettingsModel(filepath.Join(cwd, ".claude", "settings.json")); m != "" {
			return m
		}
	}
	if acct != nil && acct.Env != nil && acct.Env["ANTHROPIC_MODEL"] != "" {
		if norm := p.NormalizeModelID(acct.Env["ANTHROPIC_MODEL"]); norm != "" {
			return norm
		}
	}
	if acct != nil && acct.ConfigDir != "" {
		accDir := agentutil.ExpandTildeWithHome(acct.ConfigDir, home)
		if m := p.readSettingsModel(filepath.Join(accDir, "settings.json")); m != "" {
			return m
		}
	}
	if cfgEnv := os.Getenv("CLAUDE_CONFIG_DIR"); cfgEnv != "" {
		if m := p.readSettingsModel(filepath.Join(cfgEnv, "settings.json")); m != "" {
			return m
		}
	}
	if home != "" {
		if m := p.readSettingsModel(filepath.Join(home, ".claude", "settings.json")); m != "" {
			return m
		}
	}
	return ""
}

func (p *Plugin) readSettingsModel(filePath string) string {
	model, _, err := readCachedModel(filePath, func(f *os.File) (string, error) {
		var raw map[string]interface{}
		if err := json.NewDecoder(io.LimitReader(f, 256*1024)).Decode(&raw); err != nil {
			return "", err
		}
		if m, ok := raw["model"].(string); ok {
			if norm := p.NormalizeModelID(m); norm != "" {
				return norm, nil
			}
		}
		if envMap, ok := raw["env"].(map[string]interface{}); ok {
			if m, ok := envMap["ANTHROPIC_MODEL"].(string); ok {
				if norm := p.NormalizeModelID(m); norm != "" {
					return norm, nil
				}
			}
		}
		return "", nil
	})
	if err == nil {
		return model
	}
	return ""
}

func (p *Plugin) parseTranscriptTailModel(f *os.File) (string, error) {
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := st.Size()
	offset := size - 64*1024
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return "", err
	}
	lines := bytes.Split(buf, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		if raw["type"] == "assistant" {
			if msg, ok := raw["message"].(map[string]interface{}); ok {
				if m, ok := msg["model"].(string); ok {
					if norm := p.NormalizeModelID(m); norm != "" {
						return norm, nil
					}
				}
			}
		}
	}
	return "", nil
}

func readCachedModel(filePath string, parser func(f *os.File) (string, error)) (string, time.Time, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return "", time.Time{}, err
	}
	if !stat.Mode().IsRegular() {
		return "", time.Time{}, os.ErrInvalid
	}
	modelCacheMu.RLock()
	entry, exists := modelCache[filePath]
	modelCacheMu.RUnlock()
	if exists && entry.size == stat.Size() && entry.modTimeNano == stat.ModTime().UnixNano() {
		return entry.model, entry.modTime, nil
	}

	f, err := openRegularFileNonBlocking(filePath)
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()

	model, pErr := parser(f)
	if pErr != nil {
		return "", stat.ModTime(), pErr
	}
	modelCacheMu.Lock()
	modelCache[filePath] = cachedModelEntry{
		size: stat.Size(), modTimeNano: stat.ModTime().UnixNano(), modTime: stat.ModTime(), model: model,
	}
	modelCacheMu.Unlock()
	return model, stat.ModTime(), nil
}
