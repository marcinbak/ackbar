package grok

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
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

var modelCache struct {
	mu    sync.RWMutex
	items map[string]cachedModelEntry
}

func (*Plugin) DefaultModels() []agent.ModelSuggestion {
	return []agent.ModelSuggestion{
		{ID: "grok-4.7", Label: "Grok 4.7"},
		{ID: "grok-4.6", Label: "Grok 4.6"},
		{ID: "grok-build", Label: "Grok Build"},
	}
}

func sortModels(models []agent.ModelSuggestion) {
	priority := func(id string) int {
		lower := strings.ToLower(id)
		switch {
		case strings.HasPrefix(lower, "grok-4.7"):
			return 1
		case strings.HasPrefix(lower, "grok-4.6"):
			return 2
		case strings.HasPrefix(lower, "grok-build"):
			return 3
		default:
			return 10
		}
	}
	sort.Slice(models, func(i, j int) bool {
		pi, pj := priority(models[i].ID), priority(models[j].ID)
		if pi != pj {
			return pi < pj
		}
		return models[i].ID < models[j].ID
	})
}

func (p *Plugin) CollectModels(homeDir string) []agent.ModelSuggestion {
	if acpModels := collectACPModels(homeDir); len(acpModels) > 0 {
		return acpModels
	}
	cachePath := filepath.Join(HomeDir(homeDir), "models_cache.json")
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		var cache struct {
			Models map[string]struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Description string `json:"description"`
				Hidden      bool   `json:"hidden"`
				Info        *struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					Description string `json:"description"`
					Hidden      bool   `json:"hidden"`
				} `json:"info"`
			} `json:"models"`
		}
		if json.Unmarshal(data, &cache) == nil && len(cache.Models) > 0 {
			var models []agent.ModelSuggestion
			for k, v := range cache.Models {
				hidden, id, label := v.Hidden, v.ID, v.Name
				if v.Info != nil {
					hidden = v.Info.Hidden
					if v.Info.ID != "" {
						id = v.Info.ID
					}
					if v.Info.Name != "" {
						label = v.Info.Name
					}
				}
				if hidden {
					continue
				}
				if id == "" {
					id = k
				}
				if label == "" {
					label = id
				}
				models = append(models, agent.ModelSuggestion{ID: id, Label: label})
			}
			if len(models) > 0 {
				sortModels(models)
				return models
			}
		}
	}
	return p.DefaultModels()
}

func (*Plugin) ValidateModelID(model string) bool {
	return agentutil.IsSafeCLIArg(strings.TrimSpace(model))
}

func normalizeModelID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "default") || strings.EqualFold(s, "synthetic") || strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

func readCachedModelFile(filePath string, parser func(f *os.File) (string, error)) (string, time.Time, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return "", time.Time{}, err
	}
	if !stat.Mode().IsRegular() {
		return "", time.Time{}, os.ErrInvalid
	}

	modelCache.mu.RLock()
	entry, exists := modelCache.items[filePath]
	modelCache.mu.RUnlock()
	if exists && entry.size == stat.Size() && entry.modTimeNano == stat.ModTime().UnixNano() {
		return entry.model, entry.modTime, nil
	}

	// Open with O_NONBLOCK so FIFOs or special files never block on a writer.
	f, err := os.OpenFile(filePath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", time.Time{}, os.ErrInvalid
	}

	model, pErr := parser(f)
	if pErr != nil {
		return "", stat.ModTime(), pErr
	}

	modelCache.mu.Lock()
	if modelCache.items == nil {
		modelCache.items = make(map[string]cachedModelEntry)
	}
	modelCache.items[filePath] = cachedModelEntry{
		size:        stat.Size(),
		modTimeNano: stat.ModTime().UnixNano(),
		modTime:     stat.ModTime(),
		model:       model,
	}
	modelCache.mu.Unlock()
	return model, stat.ModTime(), nil
}

func (*Plugin) ResolveArtifactModel(homeDir, accConfigDir, cwd, nativeID string) (string, time.Time) {
	if nativeID == "" {
		return "", time.Time{}
	}
	var sessDir string
	if accConfigDir != "" {
		sessDir = FindSessionDir(accConfigDir, cwd, nativeID)
	}
	if sessDir == "" {
		sessDir = FindSessionDirForHome(homeDir, cwd, nativeID)
	}
	if sessDir == "" {
		return "", time.Time{}
	}

	sumPath := filepath.Join(sessDir, "summary.json")
	if model, modTime, err := readCachedModelFile(sumPath, func(f *os.File) (string, error) {
		var sum map[string]interface{}
		if err := json.NewDecoder(f).Decode(&sum); err != nil {
			return "", err
		}
		for _, k := range []string{"current_model_id", "model"} {
			if m, ok := sum[k].(string); ok && m != "" {
				return normalizeModelID(m), nil
			}
		}
		return "", nil
	}); err == nil && model != "" {
		return model, modTime
	}

	sigPath := filepath.Join(sessDir, "signals.json")
	if model, modTime, err := readCachedModelFile(sigPath, func(f *os.File) (string, error) {
		var sig map[string]interface{}
		if err := json.NewDecoder(f).Decode(&sig); err != nil {
			return "", err
		}
		if m, ok := sig["primaryModelId"].(string); ok && m != "" {
			return normalizeModelID(m), nil
		}
		return "", nil
	}); err == nil && model != "" {
		return model, modTime
	}
	return "", time.Time{}
}

func (*Plugin) ResolveConfiguredModel(homeDir string, acct *agent.AgentAccount) string {
	if acct != nil && acct.Env != nil && acct.Env["GROK_MODEL"] != "" {
		if norm := normalizeModelID(acct.Env["GROK_MODEL"]); norm != "" {
			return norm
		}
	}
	var candidates []string
	if acct != nil && acct.ConfigDir != "" {
		cfgDir := acct.ConfigDir
		if strings.HasPrefix(cfgDir, "~/") && homeDir != "" {
			cfgDir = filepath.Join(homeDir, cfgDir[2:])
		}
		candidates = append(candidates, filepath.Join(cfgDir, "config.toml"))
	}
	candidates = append(candidates, filepath.Join(HomeDir(homeDir), "config.toml"))
	for _, p := range candidates {
		if m := readSettingsFile(p); m != "" {
			return m
		}
	}
	return ""
}

func readSettingsFile(filePath string) string {
	model, _, err := readCachedModelFile(filePath, func(f *os.File) (string, error) {
		data, err := io.ReadAll(io.LimitReader(f, 256*1024))
		if err != nil {
			return "", err
		}
		currentSection := ""
		for _, rawLine := range strings.Split(string(data), "\n") {
			line := agentutil.StripTOMLComment(rawLine)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				currentSection = strings.Trim(line[1:len(line)-1], " \"'")
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			k := strings.TrimSpace(parts[0])
			v := strings.Trim(strings.TrimSpace(parts[1]), " \"'")
			if (currentSection == "models" && (k == "default" || k == "model")) || (currentSection == "" && (k == "model" || k == "default_model")) {
				if norm := normalizeModelID(v); norm != "" {
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
