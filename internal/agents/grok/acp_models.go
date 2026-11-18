package grok

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

const (
	acpModelsCacheFile   = "acp_models_cache.json"
	acpCommandsCacheFile = "acp_commands_cache.json"
)

func resolveGrokDir(homeDir string, env []string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], "GROK_HOME=") {
			val := strings.TrimSpace(strings.TrimPrefix(env[i], "GROK_HOME="))
			if val != "" {
				baseHome := homeDir
				if baseHome == "" {
					baseHome = agentutil.RealUserHome()
				}
				return agentutil.ExpandTildeWithHome(val, baseHome)
			}
		}
	}
	return HomeDir(homeDir)
}

func readRegularJSONFile(path string, target any) bool {
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() {
		return false
	}
	// O_NONBLOCK prevents blocking if an attacker or test places a FIFO at the cache path.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024))
	if err != nil || len(data) == 0 {
		return false
	}
	return json.Unmarshal(data, target) == nil
}

func saveACPModelsCache(grokDir string, models []agent.ModelSuggestion) error {
	if grokDir == "" {
		return nil
	}
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(models)
	if err != nil {
		return err
	}
	return agentutil.WriteFileAtomic(filepath.Join(grokDir, acpModelsCacheFile), data, 0o600)
}

func loadACPModelsCache(grokDir string) []agent.ModelSuggestion {
	var models []agent.ModelSuggestion
	if grokDir == "" || !readRegularJSONFile(filepath.Join(grokDir, acpModelsCacheFile), &models) {
		return nil
	}
	return models
}

func collectACPModels(homeDir string) []agent.ModelSuggestion {
	if models := loadACPModelsCache(HomeDir(homeDir)); len(models) > 0 {
		return models
	}
	userHome := homeDir
	if userHome == "" {
		userHome = agentutil.RealUserHome()
	}
	profilesDir := filepath.Join(userHome, ".grok-profiles")
	if entries, err := os.ReadDir(profilesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				if models := loadACPModelsCache(filepath.Join(profilesDir, e.Name())); len(models) > 0 {
					return models
				}
			}
		}
	}
	return nil
}

func saveACPCommandsCache(grokDir string, cmds []agent.ACPCommandInfo) error {
	if grokDir == "" {
		return nil
	}
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cmds)
	if err != nil {
		return err
	}
	return agentutil.WriteFileAtomic(filepath.Join(grokDir, acpCommandsCacheFile), data, 0o600)
}

func loadACPCommandsCache(grokDir string) []agent.ACPCommandInfo {
	var cmds []agent.ACPCommandInfo
	if grokDir == "" || !readRegularJSONFile(filepath.Join(grokDir, acpCommandsCacheFile), &cmds) {
		return nil
	}
	return cmds
}

func parseACPModelsPayload(raw any) []agent.ModelSuggestion {
	if m, ok := raw.(map[string]any); ok {
		if avail, has := m["availableModels"]; has {
			raw = avail
		}
	}
	var models []agent.ModelSuggestion
	seen := make(map[string]bool)
	addModel := func(id, label string, hidden bool) {
		id = strings.TrimSpace(id)
		if hidden || id == "" || !agentutil.IsSafeCLIArg(id) || seen[id] {
			return
		}
		seen[id] = true
		label = strings.TrimSpace(label)
		if label == "" {
			label = id
		}
		models = append(models, agent.ModelSuggestion{ID: id, Label: label})
	}
	extractEntry := func(fallbackID string, entry any) {
		switch v := entry.(type) {
		case string:
			addModel(v, v, false)
		case map[string]any:
			id, _ := v["id"].(string)
			if id == "" {
				id, _ = v["modelId"].(string)
			}
			label, _ := v["name"].(string)
			if label == "" {
				label, _ = v["label"].(string)
			}
			hidden, _ := v["hidden"].(bool)
			if info, ok := v["info"].(map[string]any); ok {
				if infoID, _ := info["id"].(string); infoID != "" {
					id = infoID
				}
				if infoName, _ := info["name"].(string); infoName != "" {
					label = infoName
				}
				if h, ok := info["hidden"].(bool); ok {
					hidden = h
				}
			}
			if id == "" {
				id = fallbackID
			}
			addModel(id, label, hidden)
		}
	}

	switch typed := raw.(type) {
	case []any:
		for _, item := range typed {
			extractEntry("", item)
		}
	case map[string]any:
		for k, v := range typed {
			extractEntry(k, v)
		}
	}
	sortModels(models)
	return models
}

func (*Plugin) CollectCommands(homeDir string) []agent.CommandSuggestion {
	native := []agent.CommandSuggestion{
		{Command: "/compact", Description: "Summarize and compact conversation context"},
		{Command: "/context", Description: "Inspect token usage and context window limits"},
		{Command: "/always-approve", Description: "Approve all tool executions for the current session"},
		{Command: "/goal", Description: "Set or inspect the active session goal"},
		{Command: "/workflow", Description: "Manage or run workflow commands"},
	}

	cached := loadACPCommandsCache(HomeDir(homeDir))
	if len(cached) == 0 {
		userHome := homeDir
		if userHome == "" {
			userHome = agentutil.RealUserHome()
		}
		if entries, err := os.ReadDir(filepath.Join(userHome, ".grok-profiles")); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					if c := loadACPCommandsCache(filepath.Join(userHome, ".grok-profiles", e.Name())); len(c) > 0 {
						cached = c
						break
					}
				}
			}
		}
	}

	seen := make(map[string]bool)
	var out []agent.CommandSuggestion
	add := func(cmd, desc, hint string) {
		norm := strings.TrimPrefix(strings.TrimSpace(cmd), "/")
		if norm == "" {
			return
		}
		key := strings.ToLower(norm)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, agent.CommandSuggestion{
			Command:      "/" + norm,
			Description:  strings.TrimSpace(desc),
			ArgumentHint: strings.TrimSpace(hint),
		})
	}

	for _, c := range cached {
		add(c.Name, c.Description, c.ArgumentHint)
	}
	for _, n := range native {
		add(n.Command, n.Description, n.ArgumentHint)
	}
	return out
}
