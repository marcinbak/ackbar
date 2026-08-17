package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	gitBranchCache   = make(map[string]gitBranchCacheEntry)
	gitBranchCacheMu sync.Mutex
)

type gitBranchCacheEntry struct {
	branch    string
	checkedAt time.Time
}

// ResolveGitBranch resolves the active Git branch or worktree for a working directory.
func ResolveGitBranch(cwd string) string {
	if cwd == "" {
		return ""
	}

	gitBranchCacheMu.Lock()
	if entry, ok := gitBranchCache[cwd]; ok {
		if time.Since(entry.checkedAt) < 10*time.Second {
			branch := entry.branch
			gitBranchCacheMu.Unlock()
			return branch
		}
	}
	gitBranchCacheMu.Unlock()

	branch := ""

	// 1. Check if cwd is a git worktree directly (via git file or path indicator)
	gitFilePath := filepath.Join(cwd, ".git")
	if stat, err := os.Stat(gitFilePath); err == nil && !stat.IsDir() {
		// .git is a worktree pointer file (e.g. "gitdir: /path/to/.git/worktrees/<name>")
		if content, rerr := os.ReadFile(gitFilePath); rerr == nil {
			line := strings.TrimSpace(string(content))
			if strings.HasPrefix(line, "gitdir:") {
				gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(cwd, gitDir)
				}
				// Read HEAD from worktree gitdir
				if headContent, herr := os.ReadFile(filepath.Join(gitDir, "HEAD")); herr == nil {
					headLine := strings.TrimSpace(string(headContent))
					if strings.HasPrefix(headLine, "ref: refs/heads/") {
						branch = strings.TrimPrefix(headLine, "ref: refs/heads/")
					}
				}
				if branch == "" {
					branch = filepath.Base(gitDir)
				}
			}
		}
	}

	// 2. Extract worktree name from known worktree folder patterns
	if branch == "" {
		indicators := []string{"worktrees/", "worktree-dir/", ".worktrees/", "-worktrees/", "_worktrees/"}
		for _, ind := range indicators {
			if idx := strings.Index(cwd, ind); idx != -1 {
				wtName := strings.Trim(cwd[idx+len(ind):], "/")
				// Extract the immediate child directory name if nested
				parts := strings.Split(wtName, "/")
				if len(parts) > 0 && parts[0] != "" {
					branch = parts[0]
					break
				}
			}
		}
	}

	// 3. Try git rev-parse --abbrev-ref HEAD in cwd
	if branch == "" {
		cmd := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
		if out, err := cmd.Output(); err == nil {
			b := strings.TrimSpace(string(out))
			if b != "" && b != "HEAD" {
				branch = b
			}
		}
	}

	// 4. Try git branch --show-current in cwd
	if branch == "" {
		cmd := exec.Command("git", "-C", cwd, "branch", "--show-current")
		if out, err := cmd.Output(); err == nil {
			b := strings.TrimSpace(string(out))
			if b != "" {
				branch = b
			}
		}
	}

	// 5. If cwd is a parent directory containing child git repos or worktrees, find first active repo/worktree
	if branch == "" {
		if entries, err := os.ReadDir(cwd); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					sub := filepath.Join(cwd, e.Name())
					if _, statErr := os.Stat(filepath.Join(sub, ".git")); statErr == nil {
						cmd := exec.Command("git", "-C", sub, "rev-parse", "--abbrev-ref", "HEAD")
						if out, err := cmd.Output(); err == nil {
							b := strings.TrimSpace(string(out))
							if b != "" && b != "HEAD" {
								branch = b
								break
							}
						}
					}
				}
			}
		}
	}

	// 6. If still detached, check if on a git tag
	if branch == "" {
		cmd := exec.Command("git", "-C", cwd, "describe", "--tags", "--exact-match")
		if out, err := cmd.Output(); err == nil {
			tag := strings.TrimSpace(string(out))
			if tag != "" {
				branch = "tag:" + tag
			}
		}
	}

	gitBranchCacheMu.Lock()
	gitBranchCache[cwd] = gitBranchCacheEntry{
		branch:    branch,
		checkedAt: time.Now(),
	}
	gitBranchCacheMu.Unlock()

	return branch
}
