package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ackbar/internal/agent"
)

type cachedModelEntry struct {
	size        int64
	modTimeNano int64
	modTime     time.Time
	model       string
}

type modelFileCache struct {
	mu    sync.RWMutex
	cache map[string]cachedModelEntry
}

var globalModelFileCache = &modelFileCache{
	cache: make(map[string]cachedModelEntry),
}

func openRegularFile(path string) (*os.File, error) {
	// Open with O_NONBLOCK so opening a FIFO or special file never blocks on a writer
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, os.ErrInvalid
	}
	return f, nil
}

func readCachedModelFile(filePath string, parser func(f *os.File) (string, error)) (string, time.Time, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return "", time.Time{}, err
	}
	if !stat.Mode().IsRegular() {
		return "", time.Time{}, os.ErrInvalid
	}

	globalModelFileCache.mu.RLock()
	entry, exists := globalModelFileCache.cache[filePath]
	globalModelFileCache.mu.RUnlock()

	if exists && entry.size == stat.Size() && entry.modTimeNano == stat.ModTime().UnixNano() {
		return entry.model, entry.modTime, nil
	}

	f, err := openRegularFile(filePath)
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()

	model, pErr := parser(f)
	if pErr != nil {
		return "", stat.ModTime(), pErr
	}

	globalModelFileCache.mu.Lock()
	globalModelFileCache.cache[filePath] = cachedModelEntry{
		size:        stat.Size(),
		modTimeNano: stat.ModTime().UnixNano(),
		modTime:     stat.ModTime(),
		model:       model,
	}
	globalModelFileCache.mu.Unlock()

	return model, stat.ModTime(), nil
}

func normalizeModelIdentifierWithRegistry(reg *agent.Registry, agentName, raw string) string {
	if reg != nil && strings.TrimSpace(agentName) != "" {
		if mc, ok := reg.AsModelCatalog(agentName); ok {
			return mc.NormalizeModelID(raw)
		}
	}
	return normalizeModelIdentifier(agentName, raw)
}

func normalizeModelIdentifier(agent, raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "default") || strings.EqualFold(s, "synthetic") || strings.HasPrefix(s, "<") {
		return ""
	}

	agentLower := strings.ToLower(strings.TrimSpace(agent))
	if agentLower == "claude" || agentLower == "claude-code" {
		if idx := strings.Index(s, "["); idx != -1 {
			s = strings.TrimSpace(s[:idx])
		}
		return s
	}

	if agentLower == "antigravity" {
		for _, m := range getAntigravityDefaultModels() {
			if strings.EqualFold(s, m.ID) || strings.EqualFold(s, m.Label) {
				return m.ID
			}
		}
		return s
	}

	return s
}

func (s *Server) populateResolvedModel(sess *Session) {
	if sess == nil || sess.Deleted || !s.isLocalHost(sess.Host) {
		return
	}
	sess.ResolvedModel = s.resolveSessionModel(sess)
}

func (s *Server) resolveSessionModel(sess *Session) string {
	if sess == nil {
		return ""
	}
	reg := s.PluginRegistry()

	if sess.Model != "" && !strings.EqualFold(sess.Model, "default") {
		return normalizeModelIdentifierWithRegistry(reg, sess.Agent, sess.Model)
	}

	if s.headless != nil {
		if rt := s.headless.GetRuntimeModel(sess.ID); rt != "" {
			if norm := normalizeModelIdentifierWithRegistry(reg, sess.Agent, rt); norm != "" {
				return norm
			}
		}
	}

	tailModel, tailModTime := s.resolveTranscriptTailModel(sess)
	if tailModel = normalizeModelIdentifierWithRegistry(reg, sess.Agent, tailModel); tailModel != "" {
		if sess.EngineType == EngineHeadless && !sess.MetadataUpdatedAt.IsZero() && sess.MetadataUpdatedAt.After(tailModTime) {
			if p3 := s.resolveSettingsModel(sess); p3 != "" {
				return p3
			}
		}
		return tailModel
	}

	return s.resolveSettingsModel(sess)
}

func (s *Server) resolveTranscriptTailModel(sess *Session) (string, time.Time) {
	if sess.NativeID == "" {
		return "", time.Time{}
	}

	home := s.effectiveHomeDir()
	acc := s.resolveAccountRecord(sess.Agent, sess.AccountID)
	var accCfgDir string
	if acc != nil && acc.ConfigDir != "" {
		accCfgDir = acc.ConfigDir
		if strings.HasPrefix(accCfgDir, "~/") && home != "" {
			accCfgDir = filepath.Join(home, accCfgDir[2:])
		}
	}

	if reg := s.PluginRegistry(); reg != nil {
		if mr, ok := reg.AsModelResolver(sess.Agent); ok {
			return mr.ResolveArtifactModel(home, accCfgDir, sess.Cwd, sess.NativeID)
		}
	}

	agentLower := strings.ToLower(strings.TrimSpace(sess.Agent))
	if agentLower == "claude" || agentLower == "claude-code" {
		encodedCwd := encodeClaudeProjectDir(sess.Cwd)
		var candidates []string
		if accCfgDir != "" {
			candidates = append(candidates, filepath.Join(accCfgDir, "projects", encodedCwd, sess.NativeID+".jsonl"))
		}
		candidates = append(candidates, filepath.Join(home, ".claude", "projects", encodedCwd, sess.NativeID+".jsonl"))

		for _, cand := range candidates {
			model, modTime, err := readCachedModelFile(cand, func(f *os.File) (string, error) {
				return parseClaudeTranscriptTail(f)
			})
			if err == nil && model != "" {
				return model, modTime
			}
		}
		return "", time.Time{}
	}

	if agentLower == "antigravity" {
		var candidates []string
		if accCfgDir != "" {
			candidates = append(candidates,
				filepath.Join(accCfgDir, ".gemini", "antigravity-cli", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
				filepath.Join(accCfgDir, "antigravity-cli", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
				filepath.Join(accCfgDir, "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
			)
		}
		candidates = append(candidates,
			filepath.Join(home, ".gemini", "antigravity-cli", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".gemini", "antigravity", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(home, ".antigravity", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl"),
		)
		if profiles, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain", sess.NativeID, ".system_generated", "logs", "transcript.jsonl")); len(profiles) > 0 {
			candidates = append(candidates, profiles...)
		}

		for _, cand := range candidates {
			model, modTime, err := readCachedModelFile(cand, func(f *os.File) (string, error) {
				return parseAntigravityTranscriptTail(f)
			})
			if err == nil && model != "" {
				return model, modTime
			}
		}
		return "", time.Time{}
	}

	return "", time.Time{}
}

func readTailBuffer(f *os.File, maxTail int64) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	offset := int64(0)
	if size > maxTail {
		offset = size - maxTail
	}
	buf := make([]byte, size-offset)
	_, err = f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

func parseClaudeTranscriptTail(f *os.File) (string, error) {
	buf, err := readTailBuffer(f, 64*1024)
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
		if jErr := json.Unmarshal(line, &raw); jErr != nil {
			continue
		}
		if raw["type"] == "assistant" {
			if msg, ok := raw["message"].(map[string]interface{}); ok {
				if m, ok := msg["model"].(string); ok {
					if norm := normalizeModelIdentifier("claude-code", m); norm != "" {
						return norm, nil
					}
				}
			}
		}
	}
	return "", nil
}

func parseAntigravityTranscriptTail(f *os.File) (string, error) {
	buf, err := readTailBuffer(f, 64*1024)
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
		if jErr := json.Unmarshal(line, &raw); jErr != nil {
			continue
		}
		if m, ok := raw["model"].(string); ok {
			if norm := normalizeModelIdentifier("antigravity", m); norm != "" {
				return norm, nil
			}
		}
		if msg, ok := raw["message"].(map[string]interface{}); ok {
			if m, ok := msg["model"].(string); ok {
				if norm := normalizeModelIdentifier("antigravity", m); norm != "" {
					return norm, nil
				}
			}
		}
	}
	return "", nil
}

func (s *Server) resolveAccountRecord(agent, accountID string) *AgentAccount {
	if s.db == nil {
		return nil
	}
	if accountID != "" && accountID != "default" {
		acc, err := s.db.GetAccount(accountID)
		if (err != nil || acc == nil) && !strings.Contains(accountID, ":") {
			acc, _ = s.db.GetAccount(agent + ":" + accountID)
		}
		return acc
	}
	if accountID == "" {
		if list, err := s.db.ListAccounts(agent); err == nil {
			for _, a := range list {
				if a.IsDefault && a.Name != "default" {
					return a
				}
			}
		}
	}
	return nil
}

func (s *Server) resolveSettingsModel(sess *Session) string {
	home := s.effectiveHomeDir()
	acc := s.resolveAccountRecord(sess.Agent, sess.AccountID)
	var accCfgDir string
	if acc != nil && acc.ConfigDir != "" {
		accCfgDir = acc.ConfigDir
		if strings.HasPrefix(accCfgDir, "~/") && home != "" {
			accCfgDir = filepath.Join(home, accCfgDir[2:])
		}
	}

	reg := s.PluginRegistry()
	if reg != nil {
		if wmr, ok := reg.AsWorkspaceModelResolver(sess.Agent); ok {
			return normalizeModelIdentifierWithRegistry(reg, sess.Agent, wmr.ResolveWorkspaceModel(home, sess.Cwd, acc))
		}
		if mr, ok := reg.AsModelResolver(sess.Agent); ok {
			return normalizeModelIdentifierWithRegistry(reg, sess.Agent, mr.ResolveConfiguredModel(home, acc))
		}
	}

	agentLower := strings.ToLower(strings.TrimSpace(sess.Agent))
	if agentLower == "claude" || agentLower == "claude-code" {
		if sess.Cwd != "" {
			if m := readClaudeSettingsFile(filepath.Join(sess.Cwd, ".claude", "settings.local.json")); m != "" {
				return m
			}
			if m := readClaudeSettingsFile(filepath.Join(sess.Cwd, ".claude", "settings.json")); m != "" {
				return m
			}
		}
		if acc != nil && acc.Env != nil && acc.Env["ANTHROPIC_MODEL"] != "" {
			if norm := normalizeModelIdentifierWithRegistry(reg, sess.Agent, acc.Env["ANTHROPIC_MODEL"]); norm != "" {
				return norm
			}
		}
		if accCfgDir != "" {
			if m := readClaudeSettingsFile(filepath.Join(accCfgDir, "settings.json")); m != "" {
				return m
			}
		}
		if cfgEnv := os.Getenv("CLAUDE_CONFIG_DIR"); cfgEnv != "" {
			if m := readClaudeSettingsFile(filepath.Join(cfgEnv, "settings.json")); m != "" {
				return m
			}
		}
		if m := readClaudeSettingsFile(filepath.Join(home, ".claude", "settings.json")); m != "" {
			return m
		}
		return ""
	}

	if agentLower == "antigravity" {
		if acc != nil && acc.Env != nil && acc.Env["GEMINI_MODEL"] != "" {
			if norm := normalizeModelIdentifierWithRegistry(reg, sess.Agent, acc.Env["GEMINI_MODEL"]); norm != "" {
				return norm
			}
		}
		if accCfgDir != "" {
			subDirs := []string{
				filepath.Join(accCfgDir, ".gemini", "antigravity-cli", "settings.json"),
				filepath.Join(accCfgDir, "antigravity-cli", "settings.json"),
				filepath.Join(accCfgDir, ".gemini", "antigravity", "settings.json"),
				filepath.Join(accCfgDir, "antigravity", "settings.json"),
				filepath.Join(accCfgDir, "settings.json"),
			}
			for _, p := range subDirs {
				if m := readAntigravitySettingsFile(p); m != "" {
					return m
				}
			}
		}
		if cliHome := os.Getenv("GEMINI_CLI_HOME"); cliHome != "" {
			subDirs := []string{
				filepath.Join(cliHome, ".gemini", "antigravity-cli", "settings.json"),
				filepath.Join(cliHome, "antigravity-cli", "settings.json"),
				filepath.Join(cliHome, "settings.json"),
			}
			for _, p := range subDirs {
				if m := readAntigravitySettingsFile(p); m != "" {
					return m
				}
			}
		}
		fallbacks := []string{
			filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"),
			filepath.Join(home, ".gemini", "antigravity", "settings.json"),
			filepath.Join(home, ".antigravity", "settings.json"),
		}
		for _, p := range fallbacks {
			if m := readAntigravitySettingsFile(p); m != "" {
				return m
			}
		}
		return ""
	}

	return ""
}

func readClaudeSettingsFile(filePath string) string {
	model, _, err := readCachedModelFile(filePath, func(f *os.File) (string, error) {
		lr := io.LimitReader(f, 256*1024)
		var raw map[string]interface{}
		if err := json.NewDecoder(lr).Decode(&raw); err != nil {
			return "", err
		}
		if m, ok := raw["model"].(string); ok {
			if norm := normalizeModelIdentifier("claude-code", m); norm != "" {
				return norm, nil
			}
		}
		if envMap, ok := raw["env"].(map[string]interface{}); ok {
			if m, ok := envMap["ANTHROPIC_MODEL"].(string); ok {
				if norm := normalizeModelIdentifier("claude-code", m); norm != "" {
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

func readAntigravitySettingsFile(filePath string) string {
	model, _, err := readCachedModelFile(filePath, func(f *os.File) (string, error) {
		lr := io.LimitReader(f, 256*1024)
		var raw map[string]interface{}
		if err := json.NewDecoder(lr).Decode(&raw); err != nil {
			return "", err
		}
		if m, ok := raw["model"].(string); ok {
			if norm := normalizeModelIdentifier("antigravity", m); norm != "" {
				return norm, nil
			}
		}
		if mObj, ok := raw["model"].(map[string]interface{}); ok {
			if name, ok := mObj["name"].(string); ok {
				if norm := normalizeModelIdentifier("antigravity", name); norm != "" {
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
