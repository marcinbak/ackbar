package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrPathNotConfined     = errors.New("deliverable file path is not confined to allowed roots")
	ErrDeliverableTooLarge = errors.New("deliverable file exceeds 2MB limit")
	ErrBinaryDeliverable   = errors.New("deliverable file is binary")
)

const maxDeliverableFileBytes = 2 * 1024 * 1024

var (
	reRetroHeader    = regexp.MustCompile(`(?i)#\s*Retro\s*—\s*([^(\n]+?)\s*\(([^,\n]+?),\s*([^)\n]+?)\)\s*—\s*(\S+)`)
	reImplAttempts   = regexp.MustCompile(`(?i)impl_attempts\s+(\d+)`)
	rePlanRevisions  = regexp.MustCompile(`(?i)plan_revisions\s+(\d+)`)
	reReviewRounds   = regexp.MustCompile(`(?i)review_rounds\s+(\d+)`)
	reReviewBudget   = regexp.MustCompile(`(?i)review_budget_left\s+(\d+)`)
	reReReviews      = regexp.MustCompile(`(?i)re-reviews\s+(\d+)`)
	reTier           = regexp.MustCompile(`(?i)tier\s+(T[0-3]|Tier\s*[0-3])`)
	reCacheHit       = regexp.MustCompile(`(?i)cache hit (?:rate:\s*)?([0-9.]+)%`)
	rePeakContext    = regexp.MustCompile(`(?i)peak context:\s*([0-9.]+)\s*([kKmM])?`)
	reWeightedTotal  = regexp.MustCompile(`(?i)weighted total\s*([0-9.]+)\s*([kKmM])?\s*\(([0-9.]+)\s*([kKmM])?\s*tokens\)`)
	reTableTotal     = regexp.MustCompile(`(?i)\*\*total\*\*\s*\|\s*\*\*([0-9.]+)\s*([kKmM])?\*\*\s*\|\s*\*\*([0-9.]+)\s*([kKmM])?\*\*`)
	reSubagents      = regexp.MustCompile(`(?i)subagents at\s*([0-9.]+)%`)
	reNumberedBullet = regexp.MustCompile(`(?m)^\s*(\d+)\.\s+(.+)$`)
)

func isSystemDirectory(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(p))
	if strings.HasPrefix(clean, "/var/folders/") || strings.HasPrefix(clean, "/private/var/folders/") {
		return false
	}
	if clean == "/var" || clean == "/private/var" {
		return true
	}
	systemPrefixes := []string{
		"/etc", "/private/etc",
		"/proc", "/sys", "/dev",
		"/root", "/var/root", "/private/var/root",
		"/var/log", "/private/var/log",
		"/var/db", "/private/var/db",
		"/var/run", "/private/var/run",
	}
	for _, sys := range systemPrefixes {
		if clean == sys || strings.HasPrefix(clean, sys+"/") {
			return true
		}
	}
	return false
}

func isSensitivePath(p, home string) bool {
	clean := filepath.Clean(p)
	if home != "" {
		cleanHome := filepath.Clean(home)
		confDir := filepath.Join(cleanHome, ".config")
		if clean == confDir || strings.HasPrefix(clean, confDir+string(filepath.Separator)) {
			return true
		}
		codexDir := filepath.Join(cleanHome, ".codex")
		if clean == codexDir || strings.HasPrefix(clean, codexDir+string(filepath.Separator)) {
			return true
		}
		claudeDir := filepath.Join(cleanHome, ".claude")
		if clean == claudeDir || strings.HasPrefix(clean, claudeDir+string(filepath.Separator)) {
			runsDir := filepath.Join(claudeDir, "dev-workflow-runs")
			if clean != runsDir && !strings.HasPrefix(clean, runsDir+string(filepath.Separator)) {
				return true
			}
		}
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for _, part := range parts {
		lower := strings.ToLower(part)
		if lower == ".claude" {
			slashClean := filepath.ToSlash(clean)
			if !strings.Contains(slashClean, "/.claude/dev-workflow-runs/") && !strings.HasSuffix(slashClean, "/.claude/dev-workflow-runs") {
				return true
			}
		}
		if lower == ".ssh" || lower == ".gnupg" || lower == ".aws" || lower == ".kube" || lower == ".docker" ||
			lower == ".netrc" || lower == ".npmrc" || lower == ".pypirc" ||
			lower == "ackbard.db" || lower == "ackbard.db-wal" || lower == "ackbard.db-shm" ||
			lower == ".env" || strings.HasPrefix(lower, ".env.") ||
			lower == "credentials.json" || lower == ".credentials.json" ||
			lower == "auth.json" || lower == "token.json" || lower == "hosts.yml" ||
			lower == ".git-credentials" || lower == "mcp_config.json" ||
			lower == "id_rsa" || lower == "id_ed25519" ||
			strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
			return true
		}
	}
	return false
}

func isWithinAllowedRoots(target string, allowedRoots []string) bool {
	cleanTarget := filepath.Clean(target)
	for _, root := range allowedRoots {
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		if cleanTarget == cleanRoot {
			return true
		}
		rel, err := filepath.Rel(cleanRoot, cleanTarget)
		if err == nil && !strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, "/") && !strings.HasPrefix(rel, "\\") {
			return true
		}
	}
	return false
}

func (s *Server) getAllowedDeliverableRoots(task *Task) []string {
	var roots []string
	home := s.effectiveHomeDir()
	if home != "" {
		roots = append(roots,
			filepath.Join(home, ".claude", "dev-workflow-runs"),
			filepath.Join(home, ".gemini", "antigravity-cli", "brain"),
			filepath.Join(home, ".gemini", "antigravity", "brain"),
		)
	}
	if task != nil {
		if task.WorktreePath != "" {
			roots = append(roots, task.WorktreePath)
		}
		for _, ws := range task.Workstreams {
			if ws.WorktreePath != "" {
				roots = append(roots, ws.WorktreePath)
			}
		}
		for _, w := range task.Workers {
			if w.SessionID != "" {
				if sess, _ := s.db.GetSession(w.SessionID); sess != nil {
					if sess.Cwd != "" {
						roots = append(roots, sess.Cwd)
					}
					roots = append(roots, sess.ExtraDirs...)
				}
			}
		}
	}
	if s != nil && s.db != nil {
		if nodes, err := s.db.ListNodes(); err == nil {
			for _, n := range nodes {
				if n.ProjectDir != "" {
					roots = append(roots, n.ProjectDir)
				}
			}
		}
		if hostPaths, err := s.db.ListNodeHostPaths(""); err == nil {
			for _, hp := range hostPaths {
				if hp.ProjectDir != "" && s.isLocalHost(hp.Host) {
					roots = append(roots, hp.ProjectDir)
				}
			}
		}
	}

	cleanHome := ""
	realHome := ""
	if home != "" {
		cleanHome = filepath.Clean(home)
		if rh, err := filepath.EvalSymlinks(cleanHome); err == nil {
			realHome = rh
		}
	}

	isDeniedPath := func(p string) bool {
		clean := filepath.Clean(p)
		if clean == "" || clean == "." {
			return true
		}
		for _, sys := range []string{"/", "/Users", "/home", "/tmp", "/private/tmp", "/var", "/private/var", "/etc", "/proc", "/sys", "/dev", "/root"} {
			if clean == sys {
				return true
			}
		}
		if cleanHome != "" && clean == cleanHome {
			return true
		}
		if realHome != "" && clean == realHome {
			return true
		}
		parts := strings.Split(strings.Trim(filepath.ToSlash(clean), "/"), "/")
		if len(parts) < 2 {
			return true
		}
		if cleanHome != "" {
			supDir := filepath.Join(cleanHome, ".ackbar", "supervisor")
			if clean == supDir || strings.HasPrefix(clean, supDir+string(filepath.Separator)) {
				return true
			}
		}
		if realHome != "" {
			supDir := filepath.Join(realHome, ".ackbar", "supervisor")
			if clean == supDir || strings.HasPrefix(clean, supDir+string(filepath.Separator)) {
				return true
			}
		}
		slashClean := filepath.ToSlash(clean)
		if cleanHome != "" && (strings.HasPrefix(slashClean, "/Users/") || strings.HasPrefix(slashClean, "/home/")) {
			if clean != cleanHome && !strings.HasPrefix(clean, cleanHome+string(filepath.Separator)) {
				if realHome == "" || (clean != realHome && !strings.HasPrefix(clean, realHome+string(filepath.Separator))) {
					return true
				}
			}
		}
		return false
	}

	var canonical []string
	seen := make(map[string]bool)
	for _, r := range roots {
		clean := filepath.Clean(r)
		if isDeniedPath(clean) {
			continue
		}
		realRoot, err := filepath.EvalSymlinks(clean)
		if err != nil {
			realRoot = clean
		}
		if isDeniedPath(realRoot) {
			continue
		}
		if !seen[realRoot] {
			seen[realRoot] = true
			canonical = append(canonical, realRoot)
		}
	}
	return canonical
}

func (s *Server) confineAndReadDeliverableFile(task *Task, rawPath string) (string, error) {
	if rawPath == "" || strings.ContainsRune(rawPath, 0) {
		return "", ErrPathNotConfined
	}

	clean := strings.TrimPrefix(rawPath, "file://")
	home := s.effectiveHomeDir()
	if strings.HasPrefix(clean, "~/") && home != "" {
		clean = filepath.Join(home, clean[2:])
	} else if clean == "~" && home != "" {
		clean = home
	}

	targetPath := clean
	if home != "" {
		slashHome := filepath.ToSlash(filepath.Clean(home))
		isDarwinHost := strings.HasPrefix(slashHome, "/Users/") || (!strings.HasPrefix(slashHome, "/home/") && runtime.GOOS == "darwin")
		var foreignPrefix string
		if isDarwinHost {
			foreignPrefix = "/home/"
		} else {
			foreignPrefix = "/Users/"
		}
		if strings.HasPrefix(clean, foreignPrefix) {
			parts := strings.Split(clean, "/")
			if len(parts) >= 3 {
				targetPath = filepath.Join(home, filepath.Join(parts[3:]...))
			}
		}
	}

	if !filepath.IsAbs(targetPath) {
		var relBase string
		if task != nil && task.WorktreePath != "" {
			relBase = task.WorktreePath
		} else if task != nil {
			for _, w := range task.Workers {
				if w.IsActive && w.SessionID != "" {
					if sess, _ := s.db.GetSession(w.SessionID); sess != nil && sess.Cwd != "" {
						relBase = sess.Cwd
						break
					}
				}
			}
		}
		if relBase == "" {
			return "", ErrPathNotConfined
		}
		targetPath = filepath.Join(relBase, targetPath)
	}

	if isSensitivePath(targetPath, home) || isSystemDirectory(targetPath) {
		return "", ErrPathNotConfined
	}

	allowedRoots := s.getAllowedDeliverableRoots(task)

	if _, err := os.Lstat(targetPath); err != nil {
		if os.IsNotExist(err) {
			curr := filepath.Dir(targetPath)
			for {
				if curr == "" || curr == "." || curr == "/" {
					break
				}
				if _, statErr := os.Lstat(curr); statErr == nil {
					break
				}
				parent := filepath.Dir(curr)
				if parent == curr {
					break
				}
				curr = parent
			}

			inAllowed := isWithinAllowedRoots(targetPath, allowedRoots)
			if !inAllowed && curr != "" && curr != "." && curr != "/" {
				if isSensitivePath(curr, home) || isSystemDirectory(curr) {
					return "", ErrPathNotConfined
				}
				if realAncestor, realErr := filepath.EvalSymlinks(curr); realErr == nil {
					if isSensitivePath(realAncestor, home) || isSystemDirectory(realAncestor) {
						return "", ErrPathNotConfined
					}
					if rel, relErr := filepath.Rel(curr, targetPath); relErr == nil {
						resolvedPath := filepath.Join(realAncestor, rel)
						if isWithinAllowedRoots(resolvedPath, allowedRoots) {
							inAllowed = true
						}
					}
				}
			}

			if !inAllowed {
				return "", ErrPathNotConfined
			}
			return "", os.ErrNotExist
		}
		return "", err
	}

	realPath, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return "", ErrPathNotConfined
	}

	if isSensitivePath(realPath, home) || isSystemDirectory(realPath) {
		return "", ErrPathNotConfined
	}

	var matchedRoot string
	for _, root := range allowedRoots {
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		if realPath == cleanRoot {
			matchedRoot = cleanRoot
			break
		}
		rel, err := filepath.Rel(cleanRoot, realPath)
		if err == nil && !strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, "/") && !strings.HasPrefix(rel, "\\") {
			matchedRoot = cleanRoot
			break
		}
	}
	if matchedRoot == "" {
		return "", ErrPathNotConfined
	}

	root, err := os.OpenRoot(matchedRoot)
	if err != nil {
		return "", ErrPathNotConfined
	}
	defer root.Close()

	rel, err := filepath.Rel(matchedRoot, realPath)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return "", ErrPathNotConfined
	}

	f, err := root.Open(rel)
	if err != nil {
		if os.IsNotExist(err) {
			return "", os.ErrNotExist
		}
		return "", ErrPathNotConfined
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", ErrPathNotConfined
	}
	if fi.Size() > maxDeliverableFileBytes {
		return "", ErrDeliverableTooLarge
	}

	buf, err := io.ReadAll(io.LimitReader(f, maxDeliverableFileBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(buf)) > maxDeliverableFileBytes {
		return "", ErrDeliverableTooLarge
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		return "", ErrBinaryDeliverable
	}

	return strings.ToValidUTF8(string(buf), ""), nil
}

func parseTokenCount(val string, unit string) int {
	v, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(unit) {
	case "k":
		v *= 1000
	case "m":
		v *= 1000000
	}
	return int(math.Round(v))
}

func extractRetroMetricsFromMarkdown(task *Task, contentMD string) *RetroMetrics {
	if contentMD == "" {
		return nil
	}
	m := &RetroMetrics{}

	if hdr := reRetroHeader.FindStringSubmatch(contentMD); len(hdr) > 4 {
		m.Repo = strings.TrimSpace(hdr[2])
		m.Branch = strings.TrimSpace(hdr[3])
		dateStr := strings.TrimSpace(hdr[4])
		if t, err := time.Parse("2006-01-02", dateStr); err == nil {
			m.CompletedAt = &t
		} else if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
			m.CompletedAt = &t
		}
	}

	if sub := reImplAttempts.FindStringSubmatch(contentMD); len(sub) > 1 {
		m.ImplAttempts, _ = strconv.Atoi(sub[1])
	}
	if sub := rePlanRevisions.FindStringSubmatch(contentMD); len(sub) > 1 {
		m.PlanRevisions, _ = strconv.Atoi(sub[1])
	}
	if sub := reReviewRounds.FindStringSubmatch(contentMD); len(sub) > 1 {
		m.ReviewRounds, _ = strconv.Atoi(sub[1])
	}
	if sub := reReviewBudget.FindStringSubmatch(contentMD); len(sub) > 1 {
		m.ReviewBudgetLeft, _ = strconv.Atoi(sub[1])
	}
	if sub := reReReviews.FindStringSubmatch(contentMD); len(sub) > 1 {
		m.ReReviews, _ = strconv.Atoi(sub[1])
	}
	if sub := reTier.FindStringSubmatch(contentMD); len(sub) > 1 {
		m.Tier = strings.TrimSpace(sub[1])
	}
	if sub := reCacheHit.FindStringSubmatch(contentMD); len(sub) > 1 {
		m.CacheHitPct, _ = strconv.ParseFloat(sub[1], 64)
	}
	if sub := rePeakContext.FindStringSubmatch(contentMD); len(sub) > 1 {
		unit := ""
		if len(sub) > 2 {
			unit = sub[2]
		}
		m.PeakContext = parseTokenCount(sub[1], unit)
	}
	if sub := reWeightedTotal.FindStringSubmatch(contentMD); len(sub) > 4 {
		m.WeightedTokens = parseTokenCount(sub[1], sub[2])
		m.RawTokens = parseTokenCount(sub[3], sub[4])
	} else if sub := reTableTotal.FindStringSubmatch(contentMD); len(sub) > 4 {
		m.RawTokens = parseTokenCount(sub[1], sub[2])
		m.WeightedTokens = parseTokenCount(sub[3], sub[4])
	}
	if sub := reSubagents.FindStringSubmatch(contentMD); len(sub) > 1 {
		m.SubagentShare, _ = strconv.ParseFloat(sub[1], 64)
	}

	var headlines []string
	for _, match := range reNumberedBullet.FindAllStringSubmatch(contentMD, -1) {
		if len(headlines) >= 6 {
			break
		}
		line := strings.TrimSpace(match[2])
		runes := []rune(line)
		if len(runes) > 200 {
			line = string(runes[:197]) + "..."
		}
		headlines = append(headlines, line)
	}
	m.Headlines = headlines

	if task != nil {
		if m.NodePath == "" {
			m.NodePath = TaskNodePath(task)
		}
		if m.Repo == "" {
			m.Repo = task.ProjectName
		}
		if m.Branch == "" {
			m.Branch = task.Branch
		}
		if m.PRURL == "" {
			m.PRURL = task.PRURL
		}
		if m.Outcome == "" {
			if strings.EqualFold(task.Status, "DONE") {
				m.Outcome = "completed"
			} else {
				m.Outcome = strings.ToLower(task.Status)
			}
		}
		if m.CompletedAt == nil && task.CompletedAt != nil {
			m.CompletedAt = task.CompletedAt
		}
		for _, w := range task.Workers {
			if w.Agent != "" {
				m.Agent = w.Agent
				break
			}
		}
	}

	return m
}

func (s *Server) extractRetroMetrics(task *Task, del *TaskDeliverable) *RetroMetrics {
	if del == nil {
		return nil
	}

	var run *DevWorkflowRun
	var jsonPath string

	if del.FilePath != "" && strings.HasSuffix(strings.ToLower(del.FilePath), ".retro.md") {
		candidateJSON := strings.TrimSuffix(del.FilePath, ".retro.md") + ".json"
		if data, err := s.confineAndReadDeliverableFile(task, candidateJSON); err == nil && data != "" {
			var r DevWorkflowRun
			if err := json.Unmarshal([]byte(data), &r); err == nil {
				run = &r
				jsonPath = candidateJSON
			}
		}
	}

	if run == nil && task != nil {
		home := s.effectiveHomeDir()
		if home != "" {
			runsDir := filepath.Join(home, ".claude", "dev-workflow-runs")
			if entries, err := os.ReadDir(runsDir); err == nil {
				for _, entry := range entries {
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
						continue
					}
					jPath := filepath.Join(runsDir, entry.Name())
					var modTime time.Time
					if info, infoErr := entry.Info(); infoErr == nil && info != nil {
						modTime = info.ModTime()
					}
					data, err := readWorkflowFileBounded(jPath, 512*1024)
					if err != nil {
						continue
					}
					var r DevWorkflowRun
					if err := json.Unmarshal(data, &r); err != nil {
						continue
					}
					if s.matchWorkflowRunToTask(&r, modTime, []*Task{task}) != nil {
						run = &r
						jsonPath = jPath
						break
					}
				}
			}
		}
	}

	metrics := extractRetroMetricsFromMarkdown(task, del.ContentMD)
	if metrics == nil {
		metrics = &RetroMetrics{}
	}

	if jsonPath != "" {
		slug := strings.TrimSuffix(filepath.Base(jsonPath), ".json")
		metrics.RunSlug = slug
	} else if del.FilePath != "" {
		metrics.RunSlug = strings.TrimSuffix(filepath.Base(del.FilePath), ".retro.md")
	}

	if run != nil {
		if run.Repo != "" {
			metrics.Repo = run.Repo
		}
		if run.Branch != "" {
			metrics.Branch = run.Branch
		}
		if run.ImplementerAgent != nil {
			agStr := fmt.Sprint(run.ImplementerAgent)
			isUUID := len(agStr) == 36 && strings.Count(agStr, "-") == 4
			if agStr != "" && !isUUID {
				metrics.Agent = agStr
			}
		}
		if run.Tier != "" {
			metrics.Tier = run.Tier
		}
		metrics.Lite = run.Lite
		if run.PR != nil && run.PR.URL != "" {
			metrics.PRURL = run.PR.URL
		}
		if run.Started != "" {
			if t, err := time.Parse(time.RFC3339, run.Started); err == nil {
				metrics.StartedAt = &t
			}
		}
		if run.Updated != "" {
			if t, err := time.Parse(time.RFC3339, run.Updated); err == nil {
				metrics.CompletedAt = &t
			}
		}
		if run.ImplAttempts > 0 {
			metrics.ImplAttempts = run.ImplAttempts
		}
		if run.PlanRevisions > 0 {
			metrics.PlanRevisions = run.PlanRevisions
		}
		if run.ReviewRounds > 0 {
			metrics.ReviewRounds = run.ReviewRounds
		}
		if run.ReviewBudgetLeft > 0 {
			metrics.ReviewBudgetLeft = run.ReviewBudgetLeft
		}
		if run.ReReviews > 0 {
			metrics.ReReviews = run.ReReviews
		}

		allInterventions := run.AllInterventions()
		metrics.InterventionsCount = len(allInterventions)
		preventable := 0
		for _, item := range allInterventions {
			if item == nil {
				continue
			}
			if b, ok := item["rule_could_decide"].(bool); ok && b {
				preventable++
			} else if fmt.Sprint(item["rule_could_decide"]) == "true" {
				preventable++
			}
		}
		metrics.PreventableInterventionsCount = preventable

		if run.RawTokens > 0 {
			metrics.RawTokens = run.RawTokens
		}
		if run.WeightedTokens > 0 {
			metrics.WeightedTokens = run.WeightedTokens
		}
		if run.CacheHitPct > 0 {
			metrics.CacheHitPct = run.CacheHitPct
		}
		if run.PeakContext > 0 {
			metrics.PeakContext = run.PeakContext
		}
		if run.SubagentShare > 0 {
			metrics.SubagentShare = run.SubagentShare
		}
		if len(run.Headlines) > 0 {
			metrics.Headlines = run.Headlines
		}
		if strings.EqualFold(run.Node, "done") || strings.EqualFold(run.Node, "n15") {
			metrics.Outcome = "completed"
		} else if run.Node != "" {
			metrics.Outcome = run.Node
		}
	}

	if task != nil {
		if metrics.NodePath == "" {
			metrics.NodePath = TaskNodePath(task)
		}
		if metrics.Repo == "" {
			metrics.Repo = task.ProjectName
		}
		if metrics.Branch == "" {
			metrics.Branch = task.Branch
		}
		if metrics.PRURL == "" {
			metrics.PRURL = task.PRURL
		}
		if metrics.Agent == "" {
			for _, w := range task.Workers {
				if w.Agent != "" {
					metrics.Agent = w.Agent
					break
				}
			}
			if metrics.Agent == "" && del.Provider != "" && del.Provider != "local" {
				metrics.Agent = del.Provider
			}
		}
		if metrics.Outcome == "" {
			if strings.EqualFold(task.Status, "DONE") {
				metrics.Outcome = "completed"
			} else {
				metrics.Outcome = strings.ToLower(task.Status)
			}
		}
		if metrics.CompletedAt == nil && task.CompletedAt != nil {
			metrics.CompletedAt = task.CompletedAt
		}
	}

	del.Metrics = metrics
	if b, err := json.Marshal(metrics); err == nil {
		del.MetadataJSON = string(b)
	}
	return metrics
}

func (s *Server) autoIngestTaskRetro(task *Task) {
	if s == nil || s.db == nil || task == nil {
		return
	}

	delsMap, err := s.db.getDeliverablesForTasks([]string{task.ID})
	if err == nil {
		for _, d := range delsMap[task.ID] {
			if isRetroKind(d.Kind) && d.HasContentMD && d.Metrics != nil {
				return
			}
		}
	}

	home := s.effectiveHomeDir()
	if home == "" {
		return
	}
	runsDir := filepath.Join(home, ".claude", "dev-workflow-runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}

	type entryWithTime struct {
		entry   os.DirEntry
		modTime time.Time
	}
	var jsonEntries []entryWithTime
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var modTime time.Time
		if info, infoErr := entry.Info(); infoErr == nil && info != nil {
			modTime = info.ModTime()
		}
		jsonEntries = append(jsonEntries, entryWithTime{entry: entry, modTime: modTime})
	}
	sort.Slice(jsonEntries, func(i, j int) bool {
		return jsonEntries[i].modTime.After(jsonEntries[j].modTime)
	})

	for _, item := range jsonEntries {
		entry := item.entry
		modTime := item.modTime
		jsonPath := filepath.Join(runsDir, entry.Name())
		data, err := readWorkflowFileBounded(jsonPath, 512*1024)
		if err != nil {
			continue
		}
		var run DevWorkflowRun
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		if s.matchWorkflowRunToTask(&run, modTime, []*Task{task}) == nil {
			continue
		}

		retroPath := strings.TrimSuffix(jsonPath, ".json") + ".retro.md"
		retroData, err := readWorkflowFileBounded(retroPath, 1*1024*1024)
		if err != nil {
			continue
		}
		del := TaskDeliverable{
			TaskID:    task.ID,
			Kind:      "retro",
			Title:     "Retrospective",
			FilePath:  retroPath,
			Provider:  "local",
			ContentMD: string(retroData),
		}
		s.extractRetroMetrics(task, &del)
		changed, err := s.db.UpsertTaskDeliverableLWW(&del, false)
		if err == nil && changed {
			s.pushSyncPayload(&SyncStatePayload{Deliverables: []TaskDeliverable{del}})
		}
		return
	}
}

func (s *Server) enrichAndUpsertDeliverable(task *Task, del *TaskDeliverable) error {
	if del == nil {
		return fmt.Errorf("nil deliverable")
	}
	if isRetroKind(del.Kind) || strings.HasSuffix(strings.ToLower(del.FilePath), ".retro.md") {
		s.extractRetroMetrics(task, del)
	}
	changed, err := s.db.UpsertTaskDeliverableLWW(del, false)
	if err != nil {
		return err
	}
	if changed {
		s.pushSyncPayload(&SyncStatePayload{Deliverables: []TaskDeliverable{*del}})
	}
	return nil
}
