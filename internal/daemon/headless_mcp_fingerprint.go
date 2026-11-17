package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type mcpFingerprintCacheKey struct {
	path      string
	cwdScope  string
	size      int64
	mtimeNano int64
}

type normalizedProjectMCP struct {
	MCPServers             map[string]any `json:"mcpServers,omitempty"`
	DisabledMCPServers     []string       `json:"disabledMcpServers,omitempty"`
	EnabledMCPJSONServers  []string       `json:"enabledMcpjsonServers,omitempty"`
	DisabledMCPJSONServers []string       `json:"disabledMcpjsonServers,omitempty"`
}

type normalizedMCPConfig struct {
	MCPServers         map[string]any        `json:"mcpServers,omitempty"`
	DisabledMCPServers []string              `json:"disabledMcpServers,omitempty"`
	DisabledMCPSnake   []string              `json:"disabled_mcp_servers,omitempty"`
	Project            *normalizedProjectMCP `json:"project,omitempty"`
}

var (
	mcpFingerprintCacheMu sync.RWMutex
	mcpFingerprintCache   = make(map[mcpFingerprintCacheKey][32]byte)
	emptyMCPDigest        = sha256.Sum256([]byte("{}"))
)

func asStringAnyMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sortedStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return out
}

func computeMCPFileDigest(path, rawCwd string) ([32]byte, bool) {
	data, err := readBoundedJSONFile(path, false)
	if err != nil {
		return emptyMCPDigest, false
	}
	if strings.HasSuffix(path, ".toml") {
		return sha256.Sum256(data), true
	}
	isClaudeJSON := filepath.Base(path) == ".claude.json"
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		if isClaudeJSON {
			// Treat transient/mid-write invalid JSON in .claude.json as empty so warm workers do not flap.
			return emptyMCPDigest, false
		}
		return sha256.Sum256(data), true
	}
	norm := normalizedMCPConfig{
		MCPServers:         asStringAnyMap(root["mcpServers"]),
		DisabledMCPServers: sortedStringSlice(root["disabledMcpServers"]),
		DisabledMCPSnake:   sortedStringSlice(root["disabled_mcp_servers"]),
	}
	if isClaudeJSON && rawCwd != "" {
		if projects := asStringAnyMap(root["projects"]); projects != nil {
			proj := asStringAnyMap(projects[rawCwd])
			if proj == nil {
				if cleanCwd := filepath.Clean(rawCwd); cleanCwd != rawCwd {
					proj = asStringAnyMap(projects[cleanCwd])
				}
			}
			if proj != nil {
				pNorm := &normalizedProjectMCP{
					MCPServers:             asStringAnyMap(proj["mcpServers"]),
					DisabledMCPServers:     sortedStringSlice(proj["disabledMcpServers"]),
					EnabledMCPJSONServers:  sortedStringSlice(proj["enabledMcpjsonServers"]),
					DisabledMCPJSONServers: sortedStringSlice(proj["disabledMcpjsonServers"]),
				}
				if len(pNorm.MCPServers) > 0 || len(pNorm.DisabledMCPServers) > 0 ||
					len(pNorm.EnabledMCPJSONServers) > 0 || len(pNorm.DisabledMCPJSONServers) > 0 {
					norm.Project = pNorm
				}
			}
		}
	}
	encoded, err := json.Marshal(norm)
	if err != nil {
		return emptyMCPDigest, false
	}
	return sha256.Sum256(encoded), true
}

func MCPFingerprint(sess *Session, db *DB, home string) string {
	if sess == nil {
		return ""
	}
	var candidates []string
	if sess.Cwd != "" {
		candidates = append(candidates, filepath.Join(sess.Cwd, ".mcp.json"))
	}
	if accDir := resolveSessionAccountConfigDir(sess, home, db); accDir != "" {
		candidates = append(candidates,
			filepath.Join(accDir, ".claude.json"),
			filepath.Join(accDir, "mcp_config.json"),
			filepath.Join(accDir, "config.toml"),
		)
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".claude.json"),
			filepath.Join(home, ".claude", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			filepath.Join(home, ".gemini", "config", "mcp_config.json"),
			filepath.Join(home, ".codex", "config.json"),
			filepath.Join(home, ".grok", "config.toml"),
		)
	}
	cleanCwd := ""
	if sess.Cwd != "" {
		cleanCwd = filepath.Clean(sess.Cwd)
	}
	seen := make(map[string]bool, len(candidates))
	h := sha256.New()
	for _, rawPath := range candidates {
		p := filepath.Clean(rawPath)
		if seen[p] {
			continue
		}
		seen[p] = true
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		cwdScope := ""
		if filepath.Base(p) == ".claude.json" {
			cwdScope = cleanCwd
		}
		key := mcpFingerprintCacheKey{
			path:      p,
			cwdScope:  cwdScope,
			size:      st.Size(),
			mtimeNano: st.ModTime().UnixNano(),
		}
		mcpFingerprintCacheMu.RLock()
		digest, cached := mcpFingerprintCache[key]
		mcpFingerprintCacheMu.RUnlock()
		if !cached {
			var cacheable bool
			digest, cacheable = computeMCPFileDigest(p, sess.Cwd)
			if cacheable {
				mcpFingerprintCacheMu.Lock()
				if len(mcpFingerprintCache) >= 2048 {
					clear(mcpFingerprintCache)
				}
				mcpFingerprintCache[key] = digest
				mcpFingerprintCacheMu.Unlock()
			}
		}
		_, _ = fmt.Fprintf(h, "%s:%x\n", p, digest)
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}
