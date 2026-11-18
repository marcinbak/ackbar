package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ackbar/internal/agent"
)

// SkillsRepoStatus represents the configuration, git tracking, and sync state of the external skills repository
type SkillsRepoStatus struct {
	Configured     bool   `json:"configured"`
	Cloned         bool   `json:"cloned"`
	RepoPath       string `json:"repo_path"`
	RepoURL        string `json:"repo_url"`
	SuggestedPath  string `json:"suggested_path,omitempty"`
	Branch         string `json:"branch,omitempty"`
	LocalSHA       string `json:"local_sha,omitempty"`
	RemoteSHA      string `json:"remote_sha,omitempty"`
	Dirty          bool   `json:"dirty"`
	BehindCount    int    `json:"behind_count"`
	InstallerFound bool   `json:"installer_found"`
	LastSyncedAt   string `json:"last_synced_at,omitempty"`
	LastCheckedAt  string `json:"last_checked_at,omitempty"`
	DriftDetected  bool   `json:"drift_detected"`
	Error          string `json:"error,omitempty"`
}

// SyncSkillsRepoOptions parameters for cloning and syncing the skills repository
type SyncSkillsRepoOptions struct {
	RepoPath       string `json:"repo_path"`
	RepoURL        string `json:"repo_url"`
	CloneIfMissing bool   `json:"clone_if_missing"`
}

// validateRepoPath ensures the given path is safe, absolute, and strictly underneath the user's home directory
func validateRepoPath(home, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("repository path cannot be empty")
	}
	if strings.HasPrefix(p, "-") {
		return "", fmt.Errorf("repository path cannot start with '-'")
	}
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	} else if p == "~" {
		return "", fmt.Errorf("repository path cannot be user home directory itself")
	} else if !filepath.IsAbs(p) {
		p = filepath.Join(home, p)
	}

	cleanHome := filepath.Clean(home)
	cleanPath := filepath.Clean(p)

	rel, err := filepath.Rel(cleanHome, cleanPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("repository path %q must be strictly inside home directory %q", cleanPath, cleanHome)
	}

	topComponent := strings.Split(filepath.ToSlash(rel), "/")[0]
	if topComponent == ".ssh" || topComponent == ".gnupg" || topComponent == ".claude" || topComponent == ".gemini" || topComponent == ".codex" || topComponent == ".grok" ||
		(strings.HasPrefix(topComponent, ".") && strings.HasSuffix(topComponent, "-profiles")) {
		return "", fmt.Errorf("repository path cannot be inside reserved or sensitive directory: %s", topComponent)
	}

	return cleanPath, nil
}

// validateGitURL validates git clone URL syntax to prevent option injection and dangerous arguments
func validateGitURL(u string) error {
	u = strings.TrimSpace(u)
	if u == "" {
		return fmt.Errorf("git URL cannot be empty")
	}
	if strings.HasPrefix(u, "-") {
		return fmt.Errorf("git URL cannot start with '-'")
	}
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") ||
		strings.HasPrefix(u, "ssh://") || strings.HasPrefix(u, "git@") {
		return nil
	}
	if filepath.IsAbs(u) {
		if fi, err := os.Stat(u); err == nil && fi.IsDir() {
			return nil
		}
		return fmt.Errorf("local git repository path does not exist or is not a directory: %s", u)
	}
	return fmt.Errorf("invalid git URL scheme: %s", u)
}

// isDedicatedSkillsRepo verifies that candidateRoot contains markers or structures of a dedicated skills repository
func isDedicatedSkillsRepo(root string) bool {
	if _, err := os.Stat(filepath.Join(root, ".claude-plugin", "marketplace.json")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(root, ".ackbar-skills-source")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(root, ".personal-config-managed")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(root, "install.sh")); err == nil {
		if fi, err := os.Stat(filepath.Join(root, "skills")); err == nil && fi.IsDir() {
			return true
		}
		if fi, err := os.Stat(filepath.Join(root, "plugins")); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

func collectSkillDirsWithRegistry(home string, db *DB, reg *agent.Registry, includeProfiles bool) []string {
	var out []string
	seen := make(map[string]bool)
	expandPath := func(raw string) string {
		s := strings.TrimSpace(raw)
		if s == "" {
			return ""
		}
		if home != "" {
			if s == "~" {
				s = home
			} else if strings.HasPrefix(s, "~/") {
				s = filepath.Join(home, s[2:])
			}
		}
		clean := filepath.Clean(s)
		if clean == "" || clean == "." {
			return ""
		}
		return clean
	}
	addDir := func(raw string) {
		clean := expandPath(raw)
		if clean != "" && !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}

	if home != "" {
		if reg == nil {
			addDir(filepath.Join(home, ".gemini", "config", "skills"))
			addDir(filepath.Join(home, ".gemini", "antigravity-cli", "skills"))
			addDir(filepath.Join(home, ".claude", "skills"))
			addDir(filepath.Join(home, ".codex", "skills"))
			addDir(filepath.Join(home, ".grok", "skills"))
		} else {
			if _, ok := reg.AsProvisioner("antigravity"); !ok {
				addDir(filepath.Join(home, ".gemini", "config", "skills"))
				addDir(filepath.Join(home, ".gemini", "antigravity-cli", "skills"))
			}
			if _, ok := reg.AsProvisioner("claude-code"); !ok {
				addDir(filepath.Join(home, ".claude", "skills"))
			}
		}
	}

	if reg != nil {
		for p := range reg.All() {
			if prov, ok := reg.AsProvisioner(p.Key()); ok {
				for _, d := range prov.SkillDirs(home) {
					addDir(d)
				}
			}
		}
	}

	if includeProfiles && db != nil {
		if reg == nil || func() bool { _, ok := reg.AsAccounts("claude-code"); return !ok }() {
			if accounts, err := db.ListAccounts("claude-code"); err == nil {
				for _, acc := range accounts {
					if acc != nil && strings.TrimSpace(acc.ConfigDir) != "" {
						if cfg := expandPath(acc.ConfigDir); cfg != "" {
							addDir(filepath.Join(cfg, "skills"))
						}
					}
				}
			}
		}
		if reg == nil || func() bool { _, ok := reg.AsAccounts("antigravity"); return !ok }() {
			if accounts, err := db.ListAccounts("antigravity"); err == nil {
				for _, acc := range accounts {
					if acc != nil && strings.TrimSpace(acc.ConfigDir) != "" {
						if cfg := expandPath(acc.ConfigDir); cfg != "" {
							addDir(filepath.Join(cfg, "config", "skills"))
							addDir(filepath.Join(cfg, "antigravity-cli", "skills"))
						}
					}
				}
			}
		}
		if reg != nil {
			for p := range reg.All() {
				if ac, ok := reg.AsAccounts(p.Key()); ok {
					if accounts, err := db.ListAccounts(p.Key()); err == nil {
						for _, acc := range accounts {
							for _, d := range ac.ProfileSkillDirs(home, acc) {
								addDir(d)
							}
						}
					}
				}
			}
		}
	}

	return out
}

func DiscoverSkillsRepo(home string, db *DB) (repoPath, repoURL string) {
	return DiscoverSkillsRepoWithRegistry(home, db, nil)
}

func DiscoverSkillsRepoWithRegistry(home string, db *DB, reg *agent.Registry) (repoPath, repoURL string) {
	if db != nil {
		if p, err := db.GetSetting("skills_repo_path"); err == nil && p != "" {
			repoPath = p
		}
		if u, err := db.GetSetting("skills_repo_url"); err == nil && u != "" {
			repoURL = u
		}
	}

	if repoPath != "" {
		if validPath, err := validateRepoPath(home, repoPath); err == nil {
			if repoURL == "" {
				cmd := exec.Command("git", "-C", validPath, "remote", "get-url", "origin")
				if out, err := cmd.Output(); err == nil {
					repoURL = strings.TrimSpace(string(out))
					if db != nil {
						_ = db.SetSettings(map[string]string{"skills_repo_url": repoURL})
					}
				}
			}
			return validPath, repoURL
		}
	}

	scanDirs := collectSkillDirsWithRegistry(home, db, reg, true)

	for _, sDir := range scanDirs {
		entries, err := os.ReadDir(sDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			entryPath := filepath.Join(sDir, entry.Name())

			// 1. Check for marker files
			markerFiles := []string{
				filepath.Join(entryPath, ".ackbar-skills-source"),
				filepath.Join(entryPath, ".personal-config-managed"),
			}
			var candidateRoot string
			for _, mf := range markerFiles {
				if data, err := os.ReadFile(mf); err == nil {
					// First attempt JSON parsing (e.g. .personal-config-managed contains {"source_repo": "/path/to/repo", ...})
					var meta struct {
						SourceRepo string `json:"source_repo"`
					}
					if err := json.Unmarshal(data, &meta); err == nil && meta.SourceRepo != "" {
						candidateRoot = meta.SourceRepo
						break
					}

					// Fallback to line-by-line parsing
					for _, line := range strings.Split(string(data), "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "#") || line == "" || strings.HasPrefix(line, "{") {
							continue
						}
						if strings.HasPrefix(line, "source_repo=") {
							candidateRoot = strings.TrimPrefix(line, "source_repo=")
							break
						} else if candidateRoot == "" {
							candidateRoot = line
							break
						}
					}
					if candidateRoot != "" {
						break
					}
				}
			}

			// 2. Check if symlink pointing inside a git repository
			isSymlink := false
			if candidateRoot == "" {
				fi, err := os.Lstat(entryPath)
				if err == nil && (fi.Mode()&os.ModeSymlink != 0) {
					isSymlink = true
					linkTarget, err := os.Readlink(entryPath)
					if err == nil {
						if !filepath.IsAbs(linkTarget) {
							linkTarget = filepath.Join(sDir, linkTarget)
						}
						linkTarget = filepath.Clean(linkTarget)

						// Walk up parent dirs looking for .git
						curr := linkTarget
						for {
							if _, err := os.Stat(filepath.Join(curr, ".git")); err == nil {
								candidateRoot = curr
								break
							}
							parent := filepath.Dir(curr)
							if parent == curr || parent == "." || parent == "/" {
								break
							}
							curr = parent
						}
					}
				}
			}

			if candidateRoot != "" {
				// For symlinks, verify it looks like a dedicated skills/plugin repository
				if isSymlink && !isDedicatedSkillsRepo(candidateRoot) {
					continue
				}

				if strings.HasPrefix(candidateRoot, "~/") {
					candidateRoot = filepath.Join(home, candidateRoot[2:])
				}
				candidateRoot = filepath.Clean(candidateRoot)

				if validPath, err := validateRepoPath(home, candidateRoot); err == nil {
					repoPath = validPath
					if repoURL == "" {
						cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
						if out, err := cmd.Output(); err == nil {
							repoURL = strings.TrimSpace(string(out))
						}
					}
					if db != nil {
						_ = db.SetSettings(map[string]string{
							"skills_repo_path": repoPath,
							"skills_repo_url":  repoURL,
						})
					}
					return repoPath, repoURL
				}
			}
		}
	}

	return repoPath, repoURL
}

// computeSuggestedPath derives a suggested clone path from a repository URL
func computeSuggestedPath(home, repoURL string) string {
	if repoURL == "" {
		return ""
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(repoURL), ".git")
	trimmed = strings.TrimRight(trimmed, "/")
	repoName := filepath.Base(trimmed)
	if idx := strings.LastIndex(repoName, ":"); idx >= 0 {
		repoName = repoName[idx+1:]
	}
	if repoName == "" || repoName == "." || repoName == "/" {
		repoName = "skills"
	}

	personalDir := filepath.Join(home, "Work", "Personal")
	if fi, err := os.Stat(personalDir); err == nil && fi.IsDir() {
		return filepath.Join(personalDir, repoName)
	}
	return filepath.Join(home, "Projects", repoName)
}

func GetSkillsRepoStatus(home string, db *DB, fetchRemote bool) SkillsRepoStatus {
	return GetSkillsRepoStatusWithRegistry(home, db, nil, fetchRemote)
}

func GetSkillsRepoStatusWithRegistry(home string, db *DB, reg *agent.Registry, fetchRemote bool) SkillsRepoStatus {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}

	repoPath, repoURL := DiscoverSkillsRepoWithRegistry(home, db, reg)

	var lastSync, lastCheck string
	if db != nil {
		lastSync, _ = db.GetSetting("skills_repo_last_sync")
		lastCheck, _ = db.GetSetting("skills_repo_last_check")
	}

	status := SkillsRepoStatus{
		RepoPath:      repoPath,
		RepoURL:       repoURL,
		LastSyncedAt:  lastSync,
		LastCheckedAt: lastCheck,
	}

	if repoPath == "" && repoURL != "" {
		status.SuggestedPath = computeSuggestedPath(home, repoURL)
	}

	if repoPath != "" {
		gitDir := filepath.Join(repoPath, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			status.Cloned = true
			status.Configured = true

			// Check installer existence
			installerPath := filepath.Join(repoPath, "install.sh")
			if _, err := os.Stat(installerPath); err == nil {
				status.InstallerFound = true
			}

			// If remote fetch requested, fetch origin with 10s timeout (if origin remote exists)
			if fetchRemote {
				cmdRemoteCheck := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
				if err := cmdRemoteCheck.Run(); err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					cmdFetch := exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--quiet", "origin")
					_ = cmdFetch.Run()
					nowStr := time.Now().UTC().Format(time.RFC3339)
					status.LastCheckedAt = nowStr
					if db != nil {
						_ = db.SetSettings(map[string]string{"skills_repo_last_check": nowStr})
					}
				}
			}

			// Branch name
			cmdBranch := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
			if out, err := cmdBranch.Output(); err == nil {
				status.Branch = strings.TrimSpace(string(out))
			}

			// Local commit SHA
			cmdSHA := exec.Command("git", "-C", repoPath, "rev-parse", "--short", "HEAD")
			if out, err := cmdSHA.Output(); err == nil {
				status.LocalSHA = strings.TrimSpace(string(out))
			}

			// Dirty working tree check
			cmdDirty := exec.Command("git", "-C", repoPath, "status", "--porcelain")
			if out, err := cmdDirty.Output(); err == nil {
				status.Dirty = len(bytes.TrimSpace(out)) > 0
			}

			// Upstream SHA & behind count
			upstreamRef := "origin/" + status.Branch
			if status.Branch == "" || status.Branch == "HEAD" {
				upstreamRef = "@{u}"
			}

			cmdRemoteSHA := exec.Command("git", "-C", repoPath, "rev-parse", "--short", upstreamRef)
			if out, err := cmdRemoteSHA.Output(); err == nil {
				status.RemoteSHA = strings.TrimSpace(string(out))
			} else {
				cmdFallback := exec.Command("git", "-C", repoPath, "rev-parse", "--short", "@{u}")
				if outU, errU := cmdFallback.Output(); errU == nil {
					status.RemoteSHA = strings.TrimSpace(string(outU))
				}
			}

			cmdBehind := exec.Command("git", "-C", repoPath, "rev-list", "--count", "HEAD.."+upstreamRef)
			if out, err := cmdBehind.Output(); err == nil {
				var count int
				if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count); err == nil {
					status.BehindCount = count
				}
			} else {
				cmdBehindFallback := exec.Command("git", "-C", repoPath, "rev-list", "--count", "HEAD..@{u}")
				if outU, errU := cmdBehindFallback.Output(); errU == nil {
					var count int
					if _, err := fmt.Sscanf(strings.TrimSpace(string(outU)), "%d", &count); err == nil {
						status.BehindCount = count
					}
				}
			}

			status.DriftDetected = status.BehindCount > 0 || status.Dirty
		}
	}

	if status.RepoURL != "" && !status.Cloned {
		status.Configured = true
		status.DriftDetected = true
		if status.SuggestedPath == "" {
			status.SuggestedPath = computeSuggestedPath(home, status.RepoURL)
		}
	}

	return status
}

func SyncSkillsRepo(home string, db *DB, opts SyncSkillsRepoOptions) (*SkillsRepoStatus, error) {
	return SyncSkillsRepoWithRegistry(home, db, nil, opts)
}

func SyncSkillsRepoWithRegistry(home string, db *DB, reg *agent.Registry, opts SyncSkillsRepoOptions) (*SkillsRepoStatus, error) {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}

	// Persist explicit options to DB if provided
	if opts.RepoPath != "" {
		cleanPath, err := validateRepoPath(home, opts.RepoPath)
		if err != nil {
			return nil, fmt.Errorf("invalid repository path: %w", err)
		}
		opts.RepoPath = cleanPath
		if db != nil {
			_ = db.SetSettings(map[string]string{"skills_repo_path": opts.RepoPath})
		}
	}

	if opts.RepoURL != "" {
		if err := validateGitURL(opts.RepoURL); err != nil {
			return nil, fmt.Errorf("invalid repository URL: %w", err)
		}
		if db != nil {
			_ = db.SetSettings(map[string]string{"skills_repo_url": opts.RepoURL})
		}
	}

	status := GetSkillsRepoStatusWithRegistry(home, db, reg, false)
	repoPath := status.RepoPath
	repoURL := status.RepoURL

	if opts.RepoPath != "" {
		repoPath = opts.RepoPath
	}
	if opts.RepoURL != "" {
		repoURL = opts.RepoURL
	}

	// Check if repository needs to be cloned
	isCloned := false
	if repoPath != "" {
		if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
			isCloned = true
		}
	}

	if !isCloned {
		if !opts.CloneIfMissing {
			status.Error = "repository not cloned locally (confirmation required to clone)"
			return &status, fmt.Errorf("skills repo not cloned at %s (clone confirmation required)", repoPath)
		}

		targetPath := repoPath
		if targetPath == "" {
			targetPath = status.SuggestedPath
		}
		if targetPath == "" && repoURL != "" {
			targetPath = computeSuggestedPath(home, repoURL)
		}
		if targetPath == "" {
			return &status, fmt.Errorf("cannot clone repository: no target path specified or suggested")
		}

		cleanTarget, err := validateRepoPath(home, targetPath)
		if err != nil {
			return &status, fmt.Errorf("invalid target clone path: %w", err)
		}
		targetPath = cleanTarget

		if err := validateGitURL(repoURL); err != nil {
			return &status, fmt.Errorf("invalid repository URL: %w", err)
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return &status, fmt.Errorf("failed to create directory for repository clone: %w", err)
		}

		ctxClone, cancelClone := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancelClone()

		cloneCmd := exec.CommandContext(ctxClone, "git", "clone", "--", repoURL, targetPath)
		cloneCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cloneCmd.CombinedOutput(); err != nil {
			return &status, fmt.Errorf("git clone failed: %v (output: %s)", err, strings.TrimSpace(string(out)))
		}

		repoPath = targetPath
		if db != nil {
			_ = db.SetSettings(map[string]string{"skills_repo_path": repoPath})
		}
	}

	// Cloned repository maintenance: fetch & fast-forward pull if clean and origin remote exists
	cmdRemoteCheck := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	if err := cmdRemoteCheck.Run(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		cmdFetch := exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--quiet", "origin")
		cmdFetch.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		_ = cmdFetch.Run()

		cmdDirty := exec.Command("git", "-C", repoPath, "status", "--porcelain")
		outDirty, err := cmdDirty.Output()
		isDirty := err == nil && len(bytes.TrimSpace(outDirty)) > 0

		if !isDirty {
			cmdPull := exec.CommandContext(ctx, "git", "-C", repoPath, "pull", "--ff-only")
			cmdPull.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
			_ = cmdPull.Run()
		}
	}

	// Run installer script or fallback copy
	installerPath := filepath.Join(repoPath, "install.sh")
	if fi, err := os.Stat(installerPath); err == nil {
		// Verify file ownership matches current UID
		if sys := fi.Sys(); sys != nil {
			if stat, ok := sys.(*syscall.Stat_t); ok {
				if stat.Uid != uint32(os.Getuid()) {
					return &status, fmt.Errorf("refusing to execute %s: file owner UID %d does not match process UID %d", installerPath, stat.Uid, os.Getuid())
				}
			}
		}

		if _, err := validateRepoPath(home, repoPath); err != nil {
			return &status, fmt.Errorf("security validation failed for installer directory: %w", err)
		}

		var claudeDirs []string
		if db != nil {
			if accounts, err := db.ListAccounts("claude-code"); err == nil {
				for _, acc := range accounts {
					if acc.ConfigDir != "" {
						claudeDirs = append(claudeDirs, acc.ConfigDir)
					}
				}
			}
		}

		skillDirs := collectSkillDirsWithRegistry(home, db, reg, true)

		ctxInstall, cancelInstall := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancelInstall()

		cmdInstall := exec.CommandContext(ctxInstall, "/bin/bash", "./install.sh")
		cmdInstall.Dir = repoPath
		cmdInstall.Env = os.Environ()
		cmdInstall.Env = append(cmdInstall.Env, "GIT_TERMINAL_PROMPT=0")
		cmdInstall.Env = append(cmdInstall.Env, fmt.Sprintf("HOME=%s", home))
		if len(claudeDirs) > 0 {
			cmdInstall.Env = append(cmdInstall.Env, fmt.Sprintf("ACKBAR_CLAUDE_DIRS=%s", strings.Join(claudeDirs, ":")))
		}
		if len(skillDirs) > 0 {
			cmdInstall.Env = append(cmdInstall.Env, fmt.Sprintf("ACKBAR_SKILL_DIRS=%s", strings.Join(skillDirs, string(os.PathListSeparator))))
		}

		outInstall, err := cmdInstall.CombinedOutput()
		if err != nil {
			return &status, fmt.Errorf("installer failed: %v (output: %s)", err, strings.TrimSpace(string(outInstall)))
		}
	} else {
		if err := fallbackInstallSkillsWithRegistry(home, db, reg, repoPath); err != nil {
			return &status, fmt.Errorf("fallback skill installation failed: %w", err)
		}
	}

	// Update sync & check timestamps
	nowStr := time.Now().UTC().Format(time.RFC3339)
	if db != nil {
		_ = db.SetSettings(map[string]string{
			"skills_repo_last_sync":  nowStr,
			"skills_repo_last_check": nowStr,
		})
	}

	finalStatus := GetSkillsRepoStatusWithRegistry(home, db, reg, false)
	return &finalStatus, nil
}

func fallbackInstallSkills(home, repoPath string) error {
	return fallbackInstallSkillsWithRegistry(home, nil, nil, repoPath)
}

func fallbackInstallSkillsWithRegistry(home string, db *DB, reg *agent.Registry, repoPath string) error {
	destDirs := collectSkillDirsWithRegistry(home, db, reg, true)

	type skillCandidate struct {
		name string
		path string
	}
	var candidates []skillCandidate

	// 1. skills/*
	skillsSubdir := filepath.Join(repoPath, "skills")
	if entries, err := os.ReadDir(skillsSubdir); err == nil {
		for _, e := range entries {
			sDir := filepath.Join(skillsSubdir, e.Name())
			if _, err := os.Stat(filepath.Join(sDir, "SKILL.md")); err == nil {
				candidates = append(candidates, skillCandidate{name: e.Name(), path: sDir})
			}
		}
	}

	// 2. plugins/*/skills/*
	pluginsSubdir := filepath.Join(repoPath, "plugins")
	if pEntries, err := os.ReadDir(pluginsSubdir); err == nil {
		for _, pe := range pEntries {
			plugSkillsDir := filepath.Join(pluginsSubdir, pe.Name(), "skills")
			if sEntries, err := os.ReadDir(plugSkillsDir); err == nil {
				for _, se := range sEntries {
					sDir := filepath.Join(plugSkillsDir, se.Name())
					if _, err := os.Stat(filepath.Join(sDir, "SKILL.md")); err == nil {
						candidates = append(candidates, skillCandidate{name: se.Name(), path: sDir})
					}
				}
			}
		}
	}

	// Deploy to destination directories
	for _, cand := range candidates {
		for _, destBase := range destDirs {
			target := filepath.Join(destBase, cand.name)
			if err := copyDirectory(cand.path, target); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyDirectory recursively copies a directory from src to dst skipping symlinks
func copyDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if fi, err := os.Lstat(target); err == nil && (fi.Mode()&os.ModeSymlink != 0) {
			_ = os.Remove(target)
		}
		info, err := d.Info()
		perm := os.FileMode(0644)
		if err == nil {
			if p := info.Mode().Perm(); p != 0 {
				perm = p
			}
		}
		return os.WriteFile(target, data, perm)
	})
}

func MaybeAutoSyncSkillsRepo(ctx context.Context, home string, db *DB) {
	MaybeAutoSyncSkillsRepoWithRegistry(ctx, home, db, nil)
}

func MaybeAutoSyncSkillsRepoWithRegistry(ctx context.Context, home string, db *DB, reg *agent.Registry) {
	_ = ctx
	if db == nil {
		return
	}
	lastCheckStr, _ := db.GetSetting("skills_repo_last_check")
	shouldSync := false
	if lastCheckStr == "" {
		shouldSync = true
	} else if t, err := time.Parse(time.RFC3339, lastCheckStr); err == nil {
		if time.Since(t) >= 24*time.Hour {
			shouldSync = true
		}
	} else {
		shouldSync = true
	}

	if !shouldSync {
		return
	}

	status := GetSkillsRepoStatusWithRegistry(home, db, reg, false)
	if status.Cloned && status.RepoPath != "" {
		_, _ = SyncSkillsRepoWithRegistry(home, db, reg, SyncSkillsRepoOptions{})
	}
}
