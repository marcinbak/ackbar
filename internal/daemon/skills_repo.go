package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
	if topComponent == ".ssh" || topComponent == ".gnupg" || topComponent == ".claude" || topComponent == ".gemini" || topComponent == ".codex" {
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
		strings.HasPrefix(u, "ssh://") || strings.HasPrefix(u, "git@") ||
		filepath.IsAbs(u) {
		return nil
	}
	return fmt.Errorf("invalid git URL scheme: %s", u)
}

// DiscoverSkillsRepo inspects persistent settings or scans agent skill directories for an external repository
func DiscoverSkillsRepo(home string, db *DB) (repoPath, repoURL string) {
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

	// Scan candidate agent skill directories
	scanDirs := []string{
		filepath.Join(home, ".gemini", "config", "skills"),
		filepath.Join(home, ".gemini", "antigravity-cli", "skills"),
		filepath.Join(home, ".claude", "skills"),
	}

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
					for _, line := range strings.Split(string(data), "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "#") || line == "" {
							continue
						}
						if strings.HasPrefix(line, "source_repo=") {
							candidateRoot = strings.TrimPrefix(line, "source_repo=")
							break
						} else if candidateRoot == "" {
							candidateRoot = line
						}
					}
					if candidateRoot != "" {
						break
					}
				}
			}

			// 2. Check if symlink pointing inside a git repository
			if candidateRoot == "" {
				fi, err := os.Lstat(entryPath)
				if err == nil && (fi.Mode()&os.ModeSymlink != 0) {
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
				if strings.HasPrefix(candidateRoot, "~/") {
					candidateRoot = filepath.Join(home, candidateRoot[2:])
				}
				candidateRoot = filepath.Clean(candidateRoot)

				// Verify candidate root is inside home and not in .claude, .gemini, or .codex
				rel, err := filepath.Rel(filepath.Clean(home), candidateRoot)
				if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					slashRel := filepath.ToSlash(rel)
					if !strings.HasPrefix(slashRel, ".claude") &&
						!strings.HasPrefix(slashRel, ".gemini") &&
						!strings.HasPrefix(slashRel, ".codex") {
						repoPath = candidateRoot
						cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
						if out, err := cmd.Output(); err == nil {
							repoURL = strings.TrimSpace(string(out))
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

// GetSkillsRepoStatus returns the current git and sync status of the skills repository
func GetSkillsRepoStatus(home string, db *DB, fetchRemote bool) SkillsRepoStatus {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}

	repoPath, repoURL := DiscoverSkillsRepo(home, db)

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

			// If remote fetch requested, fetch origin with 10s timeout
			if fetchRemote {
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

// SyncSkillsRepo clones, pulls, and executes the skill installer for the configured skills repository
func SyncSkillsRepo(home string, db *DB, opts SyncSkillsRepoOptions) (*SkillsRepoStatus, error) {
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

	status := GetSkillsRepoStatus(home, db, false)
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

		cloneCmd := exec.Command("git", "clone", "--", repoURL, targetPath)
		if out, err := cloneCmd.CombinedOutput(); err != nil {
			return &status, fmt.Errorf("git clone failed: %v (output: %s)", err, strings.TrimSpace(string(out)))
		}

		repoPath = targetPath
		if db != nil {
			_ = db.SetSettings(map[string]string{"skills_repo_path": repoPath})
		}
	}

	// Cloned repository maintenance: fetch & fast-forward pull if clean
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmdFetch := exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--quiet", "origin")
	_ = cmdFetch.Run()

	cmdDirty := exec.Command("git", "-C", repoPath, "status", "--porcelain")
	outDirty, err := cmdDirty.Output()
	isDirty := err == nil && len(bytes.TrimSpace(outDirty)) > 0

	if !isDirty {
		cmdPull := exec.CommandContext(ctx, "git", "-C", repoPath, "pull", "--ff-only")
		_ = cmdPull.Run()
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

		cmdInstall := exec.Command("/bin/bash", "./install.sh")
		cmdInstall.Dir = repoPath
		cmdInstall.Env = os.Environ()
		cmdInstall.Env = append(cmdInstall.Env, fmt.Sprintf("HOME=%s", home))
		if len(claudeDirs) > 0 {
			cmdInstall.Env = append(cmdInstall.Env, fmt.Sprintf("ACKBAR_CLAUDE_DIRS=%s", strings.Join(claudeDirs, ":")))
		}

		outInstall, err := cmdInstall.CombinedOutput()
		if err != nil {
			return &status, fmt.Errorf("installer failed: %v (output: %s)", err, strings.TrimSpace(string(outInstall)))
		}
	} else {
		// Fallback: copy skills directly
		if err := fallbackInstallSkills(home, repoPath); err != nil {
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

	finalStatus := GetSkillsRepoStatus(home, db, false)
	return &finalStatus, nil
}

// fallbackInstallSkills copies skills when no install.sh script exists in the repository
func fallbackInstallSkills(home, repoPath string) error {
	destDirs := []string{
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".gemini", "config", "skills"),
		filepath.Join(home, ".gemini", "antigravity-cli", "skills"),
		filepath.Join(home, ".codex", "skills"),
	}

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

// copyDirectory recursively copies a directory from src to dst
func copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		perm := info.Mode().Perm()
		if perm == 0 {
			perm = 0644
		}
		return os.WriteFile(target, data, perm)
	})
}

// MaybeAutoSyncSkillsRepo executes a background sync if >=24h has elapsed since last check
func MaybeAutoSyncSkillsRepo(ctx context.Context, home string, db *DB) {
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

	status := GetSkillsRepoStatus(home, db, false)
	if status.Cloned && status.RepoPath != "" {
		_, _ = SyncSkillsRepo(home, db, SyncSkillsRepoOptions{})
	}
}
