package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type CommandSuggestion struct {
	Name          string `json:"name"`    // e.g. "config", "clear", "dev-workflow"
	Trigger       string `json:"trigger"` // e.g. "/config", "/dev-workflow"
	Description   string `json:"description"`
	Category      string `json:"category"`       // "Session Control", "Workspace Command", "Skill"
	ExecutionMode string `json:"execution_mode"` // "client_action" | "prompt"
}

type FileSuggestion struct {
	Path  string `json:"path"` // relative to sess.Cwd
	Name  string `json:"name"` // base name
	IsDir bool   `json:"is_dir"`
}

type ModelSuggestion struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type cachedFileList struct {
	files     []FileSuggestion
	expiresAt time.Time
}

type cachedModelList struct {
	models    []ModelSuggestion
	expiresAt time.Time
}

var (
	suggestionCacheMu sync.Mutex
	suggestionCache   = make(map[string]cachedFileList)

	modelCacheMu          sync.Mutex
	antigravityModelCache cachedModelList
	modelIDRegex          = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

func getAntigravityDefaultModels() []ModelSuggestion {
	return []ModelSuggestion{
		{ID: "gemini-4-argon-high", Label: "Gemini 4 Argon (High)"},
		{ID: "gemini-4-argon-medium", Label: "Gemini 4 Argon (Medium)"},
		{ID: "gemini-4-argon-low", Label: "Gemini 4 Argon (Low)"},
		{ID: "gemini-3.8-flash-high", Label: "Gemini 3.8 Flash (High)"},
		{ID: "gemini-3.8-flash-medium", Label: "Gemini 3.8 Flash (Medium)"},
		{ID: "gemini-3.8-flash-low", Label: "Gemini 3.8 Flash (Low)"},
		{ID: "claude-opus-5-5-high", Label: "Claude Opus 5.5 (High)"},
		{ID: "claude-opus-5-5-medium", Label: "Claude Opus 5.5 (Medium)"},
		{ID: "claude-opus-5-5-low", Label: "Claude Opus 5.5 (Low)"},
		{ID: "claude-sonnet-5-5-high", Label: "Claude Sonnet 5.5 (High)"},
		{ID: "claude-sonnet-5-5-medium", Label: "Claude Sonnet 5.5 (Medium)"},
		{ID: "claude-sonnet-5-5-low", Label: "Claude Sonnet 5.5 (Low)"},
		{ID: "gpt-oss-120b-medium", Label: "GPT-OSS 120B (Medium)"},
	}
}

func getClaudeDefaultModels() []ModelSuggestion {
	return []ModelSuggestion{
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

func parseAntigravityModelsOutput(out string) []ModelSuggestion {
	var models []ModelSuggestion
	seen := make(map[string]bool)

	lines := strings.Split(out, "\n")
	for _, rawLine := range lines {
		line := rawLine
		if idx := strings.LastIndex(line, "\r"); idx != -1 {
			line = line[idx+1:]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		id := fields[0]
		if !modelIDRegex.MatchString(id) {
			continue
		}
		lowerID := strings.ToLower(id)
		if lowerID == "usage" || lowerID == "available" || lowerID == "flags" {
			continue
		}

		label := strings.TrimSpace(line[len(id):])
		if label == "" {
			label = id
		}

		if !seen[id] {
			seen[id] = true
			models = append(models, ModelSuggestion{
				ID:    id,
				Label: label,
			})
		}
	}
	return models
}

func (s *Server) collectModelsForAgent(agent string) []ModelSuggestion {
	if strings.EqualFold(agent, "antigravity") {
		modelCacheMu.Lock()
		if len(antigravityModelCache.models) > 0 && time.Now().Before(antigravityModelCache.expiresAt) {
			cached := antigravityModelCache.models
			modelCacheMu.Unlock()
			return cached
		}
		modelCacheMu.Unlock()

		bin, err := resolveAntigravityBinary()
		if err == nil && bin != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "models")
			var out bytes.Buffer
			cmd.Stdout = &out
			if cmdErr := cmd.Run(); cmdErr == nil && out.Len() > 0 {
				parsed := parseAntigravityModelsOutput(out.String())
				if len(parsed) > 0 {
					modelCacheMu.Lock()
					antigravityModelCache = cachedModelList{
						models:    parsed,
						expiresAt: time.Now().Add(5 * time.Minute),
					}
					modelCacheMu.Unlock()
					return parsed
				}
			}
		}

		return getAntigravityDefaultModels()
	}

	return getClaudeDefaultModels()
}

func getBuiltinCommands() []CommandSuggestion {
	return []CommandSuggestion{
		{
			Name:          "config",
			Trigger:       "/config",
			Description:   "Configure model, reasoning effort, plan mode, extra directories, and view context usage",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "plan",
			Trigger:       "/plan",
			Description:   "Toggle Plan Mode (read-only planning vs auto execution)",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "clear",
			Trigger:       "/clear",
			Description:   "Start a fresh conversation in this directory (rotates session)",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "reset",
			Trigger:       "/reset",
			Description:   "Reset conversation state and start fresh (alias for /clear)",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "compact",
			Trigger:       "/compact",
			Description:   "Summarize and compact conversation context",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "handover",
			Trigger:       "/handover",
			Description:   "Hand over session context to another agent or new session",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "terminal",
			Trigger:       "/terminal",
			Description:   "Take the wheel in an interactive tmux terminal",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "voice_live",
			Trigger:       "/voice live",
			Description:   "Toggle two-way Live Voice Mode via Ackbar Audio Bridge",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "model",
			Trigger:       "/model",
			Description:   "Set or switch LLM model",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "effort",
			Trigger:       "/effort",
			Description:   "Set reasoning effort (low, medium, high, xhigh, max)",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "permissions",
			Trigger:       "/permissions",
			Description:   "Set permission mode (bypassPermissions, auto, acceptEdits, dontAsk, plan)",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "auto",
			Trigger:       "/auto",
			Description:   "Switch permission mode to auto (execute without permission prompts)",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "add_dir",
			Trigger:       "/add-dir",
			Description:   "Add or remove allowed directories for tools (--add-dir)",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "mcp",
			Trigger:       "/mcp",
			Description:   "Inspect Model Context Protocol servers and authentication status",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "plugins",
			Trigger:       "/plugins",
			Description:   "List installed agent plugins",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "agents",
			Trigger:       "/agents",
			Description:   "List available and active agent definitions",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "doctor",
			Trigger:       "/doctor",
			Description:   "Run agent diagnostic checks and environment report",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "context",
			Trigger:       "/context",
			Description:   "Inspect token usage and context window limits",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "cost",
			Trigger:       "/cost",
			Description:   "View estimated API-equivalent turn and session cost",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "diff",
			Trigger:       "/diff",
			Description:   "View git status and diff for current working directory",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
		{
			Name:          "rewind",
			Trigger:       "/rewind",
			Description:   "Rewind conversation to a previous turn",
			Category:      "Session Control",
			ExecutionMode: "client_action",
		},
	}
}

// extractMarkdownDescription reads frontmatter description: or the first non-heading sentence
func extractMarkdownDescription(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inFrontmatter := false
	firstTextLine := ""

	lineIdx := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineIdx++

		if lineIdx == 1 && line == "---" {
			inFrontmatter = true
			continue
		}

		if inFrontmatter {
			if line == "---" {
				inFrontmatter = false
				continue
			}
			lower := strings.ToLower(line)
			if strings.HasPrefix(lower, "description:") {
				desc := strings.TrimSpace(line[len("description:"):])
				desc = strings.Trim(desc, `"'`)
				if desc != "" {
					return desc
				}
			}
			continue
		}

		if line == "" {
			continue
		}

		// First non-frontmatter line
		if firstTextLine == "" {
			if strings.HasPrefix(line, "#") {
				// Strip leading hashes
				heading := strings.TrimLeft(line, "# ")
				if heading != "" {
					firstTextLine = heading
				}
			} else {
				return line
			}
		} else if !strings.HasPrefix(line, "#") {
			return line
		}

		if lineIdx > 40 {
			break
		}
	}

	return firstTextLine
}

func scanMarkdownCommands(dir, category string) []CommandSuggestion {
	var results []CommandSuggestion
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".md") {
			stem := strings.TrimSuffix(name, filepath.Ext(name))
			fullPath := filepath.Join(dir, name)
			desc := extractMarkdownDescription(fullPath)
			if desc == "" {
				desc = "Run workspace workflow " + stem
			}
			results = append(results, CommandSuggestion{
				Name:          stem,
				Trigger:       "/" + stem,
				Description:   desc,
				Category:      category,
				ExecutionMode: "prompt",
			})
		}
	}
	return results
}

func scanSkillsInDir(parentDir string) []CommandSuggestion {
	var results []CommandSuggestion
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return nil
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillName := e.Name()
		skillFile := filepath.Join(parentDir, skillName, "SKILL.md")
		if stat, err := os.Stat(skillFile); err == nil && !stat.IsDir() {
			desc := extractMarkdownDescription(skillFile)
			if desc == "" {
				desc = "Skill: " + skillName
			}
			results = append(results, CommandSuggestion{
				Name:          skillName,
				Trigger:       "/" + skillName,
				Description:   desc,
				Category:      "Skill",
				ExecutionMode: "prompt",
			})
		}
	}
	return results
}

func (s *Server) collectCommands(cwd string, agentOpt ...string) []CommandSuggestion {
	var cmds []CommandSuggestion
	seen := make(map[string]bool)
	agent := ""
	if len(agentOpt) > 0 {
		agent = strings.ToLower(strings.TrimSpace(agentOpt[0]))
	}

	// 1. Builtins
	for _, b := range getBuiltinCommands() {
		if agent == "antigravity" && b.Name == "effort" {
			continue
		}
		cmds = append(cmds, b)
		seen[b.Name] = true
	}

	// 2. Workspace Commands (.claude/commands/*.md & .agents/workflows/*.md)
	if cwd != "" {
		for _, c := range scanMarkdownCommands(filepath.Join(cwd, ".claude", "commands"), "Workspace Command") {
			if !seen[c.Name] {
				cmds = append(cmds, c)
				seen[c.Name] = true
			}
		}
		for _, c := range scanMarkdownCommands(filepath.Join(cwd, ".agents", "workflows"), "Workspace Command") {
			if !seen[c.Name] {
				cmds = append(cmds, c)
				seen[c.Name] = true
			}
		}

		// Workspace Skills
		for _, sk := range scanSkillsInDir(filepath.Join(cwd, ".agents", "skills")) {
			if !seen[sk.Name] {
				cmds = append(cmds, sk)
				seen[sk.Name] = true
			}
		}
		for _, sk := range scanSkillsInDir(filepath.Join(cwd, ".claude", "skills")) {
			if !seen[sk.Name] {
				cmds = append(cmds, sk)
				seen[sk.Name] = true
			}
		}
	}

	// 3. Global Skills
	home, _ := os.UserHomeDir()
	if home != "" {
		globalSkillDirs := []string{
			filepath.Join(home, ".gemini", "config", "skills"),
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(home, ".agents", "skills"),
			filepath.Join(home, ".config", "skills"),
		}
		if s.db != nil {
			repoPath, _ := DiscoverSkillsRepo(home, s.db)
			if repoPath != "" {
				globalSkillDirs = append(globalSkillDirs, repoPath, filepath.Join(repoPath, "skills"))
			}
		}
		for _, dir := range globalSkillDirs {
			for _, sk := range scanSkillsInDir(dir) {
				if !seen[sk.Name] {
					cmds = append(cmds, sk)
					seen[sk.Name] = true
				}
			}
		}
	}

	return cmds
}

func (s *Server) getCachedFiles(cwd string) []FileSuggestion {
	suggestionCacheMu.Lock()
	cached, ok := suggestionCache[cwd]
	if ok && time.Now().Before(cached.expiresAt) {
		suggestionCacheMu.Unlock()
		return cached.files
	}
	suggestionCacheMu.Unlock()

	files := s.populateFiles(cwd)

	suggestionCacheMu.Lock()
	suggestionCache[cwd] = cachedFileList{
		files:     files,
		expiresAt: time.Now().Add(10 * time.Second),
	}
	suggestionCacheMu.Unlock()

	return files
}

func (s *Server) populateFiles(cwd string) []FileSuggestion {
	if cwd == "" {
		return nil
	}

	// 1. Try git ls-files
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "ls-files", "--cached", "--others", "--exclude-standard")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err == nil && stdout.Len() > 0 {
		var list []FileSuggestion
		scanner := bufio.NewScanner(&stdout)
		count := 0
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			list = append(list, FileSuggestion{
				Path:  line,
				Name:  filepath.Base(line),
				IsDir: false,
			})
			count++
			if count >= 8000 {
				break
			}
		}
		if len(list) > 0 {
			return list
		}
	}

	// 2. Fallback to bounded filepath.WalkDir
	var list []FileSuggestion
	cleanRoot := filepath.Clean(cwd)
	count := 0

	ignoredDirs := map[string]bool{
		".git":         true,
		".worktrees":   true,
		"node_modules": true,
		"vendor":       true,
		"build":        true,
		"dist":         true,
		"Library":      true,
		".dart_tool":   true,
		".gradle":      true,
		"Pods":         true,
	}

	_ = filepath.WalkDir(cleanRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == cleanRoot {
			return nil
		}

		rel, err := filepath.Rel(cleanRoot, p)
		if err != nil {
			return nil
		}

		// Depth limit check (maxDepth = 4)
		depth := strings.Count(rel, string(filepath.Separator))
		if depth > 4 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		base := d.Name()
		if d.IsDir() {
			if ignoredDirs[base] {
				return fs.SkipDir
			}
			if strings.HasPrefix(base, ".") && base != ".claude" && base != ".agents" {
				return fs.SkipDir
			}
			return nil
		}

		list = append(list, FileSuggestion{
			Path:  filepath.ToSlash(rel),
			Name:  base,
			IsDir: false,
		})
		count++
		if count >= 5000 {
			return fs.SkipAll
		}
		return nil
	})

	return list
}

func (s *Server) handleSessionSuggestions(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "", nil, func(target *Session) string {
		return "/v1/sessions/" + url.PathEscape(target.ID) + "/suggestions?" + r.URL.RawQuery
	})
	if proxied {
		return
	}
	if sess == nil {
		return
	}

	kind := strings.ToLower(r.URL.Query().Get("kind"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	qLower := strings.ToLower(q)

	w.Header().Set("Content-Type", "application/json")

	switch kind {
	case "files":
		allFiles := s.getCachedFiles(sess.Cwd)
		if qLower == "" {
			limit := 50
			if len(allFiles) < limit {
				limit = len(allFiles)
			}
			_ = json.NewEncoder(w).Encode(allFiles[:limit])
			return
		}

		type scoredFile struct {
			file  FileSuggestion
			score int
		}
		var scored []scoredFile
		for _, f := range allFiles {
			nameLower := strings.ToLower(f.Name)
			pathLower := strings.ToLower(f.Path)

			score := 0
			if strings.HasPrefix(nameLower, qLower) {
				score = 3
			} else if strings.Contains(nameLower, qLower) {
				score = 2
			} else if strings.Contains(pathLower, qLower) {
				score = 1
			}

			if score > 0 {
				scored = append(scored, scoredFile{file: f, score: score})
			}
		}

		sort.Slice(scored, func(i, j int) bool {
			if scored[i].score != scored[j].score {
				return scored[i].score > scored[j].score
			}
			if len(scored[i].file.Path) != len(scored[j].file.Path) {
				return len(scored[i].file.Path) < len(scored[j].file.Path)
			}
			return scored[i].file.Path < scored[j].file.Path
		})

		limit := 50
		if len(scored) < limit {
			limit = len(scored)
		}
		result := make([]FileSuggestion, limit)
		for i := 0; i < limit; i++ {
			result[i] = scored[i].file
		}
		_ = json.NewEncoder(w).Encode(result)

	case "models":
		models := s.collectModelsForAgent(sess.Agent)
		if qLower != "" {
			var filtered []ModelSuggestion
			for _, m := range models {
				if strings.Contains(strings.ToLower(m.ID), qLower) || strings.Contains(strings.ToLower(m.Label), qLower) {
					filtered = append(filtered, m)
				}
			}
			models = filtered
		}
		if models == nil {
			models = []ModelSuggestion{}
		}
		_ = json.NewEncoder(w).Encode(models)

	case "commands":
		fallthrough
	default:
		allCmds := s.collectCommands(sess.Cwd, sess.Agent)
		// Clean query of leading slash if present
		cleanQ := strings.TrimPrefix(qLower, "/")

		if cleanQ == "" {
			_ = json.NewEncoder(w).Encode(allCmds)
			return
		}

		type scoredCmd struct {
			cmd   CommandSuggestion
			score int
		}
		var scored []scoredCmd
		for _, c := range allCmds {
			nameLower := strings.ToLower(c.Name)
			descLower := strings.ToLower(c.Description)
			catLower := strings.ToLower(c.Category)

			score := 0
			if strings.HasPrefix(nameLower, cleanQ) {
				score = 3
			} else if strings.Contains(nameLower, cleanQ) {
				score = 2
			} else if strings.Contains(descLower, cleanQ) || strings.Contains(catLower, cleanQ) {
				score = 1
			}

			if score > 0 {
				scored = append(scored, scoredCmd{cmd: c, score: score})
			}
		}

		sort.Slice(scored, func(i, j int) bool {
			if scored[i].score != scored[j].score {
				return scored[i].score > scored[j].score
			}
			return scored[i].cmd.Name < scored[j].cmd.Name
		})

		result := make([]CommandSuggestion, len(scored))
		for i := 0; i < len(scored); i++ {
			result[i] = scored[i].cmd
		}
		_ = json.NewEncoder(w).Encode(result)
	}
}
