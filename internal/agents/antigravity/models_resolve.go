package antigravity

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
	modelFileCacheMu sync.Mutex
	modelFileCache   = make(map[string]cachedModelEntry)
)

func readCachedModel(filePath string, parser func(string) (string, error)) (string, time.Time, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return "", time.Time{}, err
	}
	modNano := stat.ModTime().UnixNano()
	modelFileCacheMu.Lock()
	if e, ok := modelFileCache[filePath]; ok && e.size == stat.Size() && e.modTimeNano == modNano {
		modelFileCacheMu.Unlock()
		return e.model, e.modTime, nil
	}
	modelFileCacheMu.Unlock()

	model, pErr := parser(filePath)
	if pErr != nil {
		return "", stat.ModTime(), pErr
	}
	modelFileCacheMu.Lock()
	modelFileCache[filePath] = cachedModelEntry{size: stat.Size(), modTimeNano: modNano, modTime: stat.ModTime(), model: model}
	modelFileCacheMu.Unlock()
	return model, stat.ModTime(), nil
}

func (p *Plugin) ResolveArtifactModel(homeDir, accCfgDir, _, nativeID string) (string, time.Time) {
	if !agentutil.IsSafeSessionID(nativeID) {
		return "", time.Time{}
	}
	home := effectiveHome(homeDir)
	var candidates []string
	if accCfgDir != "" {
		candidates = append(candidates,
			filepath.Join(accCfgDir, ".gemini", "antigravity-cli", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(accCfgDir, "antigravity-cli", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(accCfgDir, "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
		)
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".gemini", "antigravity", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".antigravity", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl"),
		)
		if profiles, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", nativeID, ".system_generated", "logs", "transcript.jsonl")); err == nil && len(profiles) > 0 {
			candidates = append(candidates, profiles...)
		}
	}
	for _, cand := range candidates {
		model, modTime, err := readCachedModel(cand, p.parseTranscriptTailModel)
		if err == nil && model != "" {
			return model, modTime
		}
	}
	return "", time.Time{}
}

func (p *Plugin) parseTranscriptTailModel(filePath string) (string, error) {
	buf, err := agentutil.ReadTail(filePath, 64*1024)
	if err != nil {
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
		if m, ok := raw["model"].(string); ok {
			if norm := p.NormalizeModelID(m); norm != "" {
				return norm, nil
			}
		}
		if msg, ok := raw["message"].(map[string]interface{}); ok {
			if m, ok := msg["model"].(string); ok {
				if norm := p.NormalizeModelID(m); norm != "" {
					return norm, nil
				}
			}
		}
	}
	return "", nil
}

func (p *Plugin) ResolveConfiguredModel(homeDir string, acct *agent.AgentAccount) string {
	home := effectiveHome(homeDir)
	if acct != nil && acct.Env != nil && acct.Env["GEMINI_MODEL"] != "" {
		if norm := p.NormalizeModelID(acct.Env["GEMINI_MODEL"]); norm != "" {
			return norm
		}
	}
	if acct != nil && acct.ConfigDir != "" {
		accCfgDir := agentutil.ExpandTildeWithHome(acct.ConfigDir, home)
		for _, cand := range []string{
			filepath.Join(accCfgDir, ".gemini", "antigravity-cli", "settings.json"),
			filepath.Join(accCfgDir, "antigravity-cli", "settings.json"),
			filepath.Join(accCfgDir, ".gemini", "antigravity", "settings.json"),
			filepath.Join(accCfgDir, "antigravity", "settings.json"),
			filepath.Join(accCfgDir, "settings.json"),
		} {
			if m := p.readSettingsModel(cand); m != "" {
				return m
			}
		}
	}
	if cliHome := os.Getenv("GEMINI_CLI_HOME"); cliHome != "" {
		for _, cand := range []string{
			filepath.Join(cliHome, ".gemini", "antigravity-cli", "settings.json"),
			filepath.Join(cliHome, "antigravity-cli", "settings.json"),
			filepath.Join(cliHome, "settings.json"),
		} {
			if m := p.readSettingsModel(cand); m != "" {
				return m
			}
		}
	}
	if home != "" {
		for _, cand := range []string{
			filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"),
			filepath.Join(home, ".gemini", "antigravity", "settings.json"),
			filepath.Join(home, ".antigravity", "settings.json"),
		} {
			if m := p.readSettingsModel(cand); m != "" {
				return m
			}
		}
	}
	return ""
}

func (p *Plugin) readSettingsModel(filePath string) string {
	model, _, err := readCachedModel(filePath, func(fp string) (string, error) {
		f, err := os.Open(fp)
		if err != nil {
			return "", err
		}
		defer f.Close()
		var raw map[string]interface{}
		if err := json.NewDecoder(io.LimitReader(f, 256*1024)).Decode(&raw); err != nil {
			return "", err
		}
		if m, ok := raw["model"].(string); ok {
			if norm := p.NormalizeModelID(m); norm != "" {
				return norm, nil
			}
		}
		if mObj, ok := raw["model"].(map[string]interface{}); ok {
			if name, ok := mObj["name"].(string); ok {
				if norm := p.NormalizeModelID(name); norm != "" {
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
