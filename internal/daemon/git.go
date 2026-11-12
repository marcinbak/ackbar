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

func inspectGitDir(gitDirOrFile, baseDir string) (branch string, gitFound bool) {
	stat, err := os.Stat(gitDirOrFile)
	if err != nil {
		return "", false
	}
	if stat.IsDir() {
		headFile := filepath.Join(gitDirOrFile, "HEAD")
		content, rerr := os.ReadFile(headFile)
		if rerr != nil {
			return "", true
		}
		headLine := strings.TrimSpace(string(content))
		if strings.HasPrefix(headLine, "ref: refs/heads/") {
			return strings.TrimPrefix(headLine, "ref: refs/heads/"), true
		}
		return "", true
	}

	content, rerr := os.ReadFile(gitDirOrFile)
	if rerr != nil {
		return "", true
	}
	line := strings.TrimSpace(string(content))
	if strings.HasPrefix(line, "gitdir:") {
		gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(baseDir, gitDir)
		}
		if headContent, herr := os.ReadFile(filepath.Join(gitDir, "HEAD")); herr == nil {
			headLine := strings.TrimSpace(string(headContent))
			if strings.HasPrefix(headLine, "ref: refs/heads/") {
				return strings.TrimPrefix(headLine, "ref: refs/heads/"), true
			}
		}
		return filepath.Base(gitDir), true
	}
	return "", true
}

// ResolveGitBranch resolves the active Git branch or worktree for a working directory.
func ResolveGitBranch(cwd string) string {
	if cwd == "" {
		return ""
	}

	gitBranchCacheMu.Lock()
	if entry, ok := gitBranchCache[cwd]; ok {
		if time.Since(entry.checkedAt) < 60*time.Second {
			branch := entry.branch
			gitBranchCacheMu.Unlock()
			return branch
		}
	}
	gitBranchCacheMu.Unlock()

	stat, err := os.Stat(cwd)
	if err != nil || !stat.IsDir() {
		gitBranchCacheMu.Lock()
		gitBranchCache[cwd] = gitBranchCacheEntry{
			branch:    "",
			checkedAt: time.Now(),
		}
		gitBranchCacheMu.Unlock()
		return ""
	}

	branch := ""
	var foundGitDir string

	cur := filepath.Clean(cwd)
	for i := 0; i < 20; i++ {
		gitPath := filepath.Join(cur, ".git")
		b, found := inspectGitDir(gitPath, cur)
		if found {
			foundGitDir = cur
			if b != "" {
				branch = b
				break
			}
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur || parent == "" {
			break
		}
		cur = parent
	}

	if branch == "" && foundGitDir == "" {
		indicators := []string{"worktrees/", "worktree-dir/", ".worktrees/", "-worktrees/", "_worktrees/"}
		for _, ind := range indicators {
			if idx := strings.Index(cwd, ind); idx != -1 {
				wtName := strings.Trim(cwd[idx+len(ind):], "/")
				parts := strings.Split(wtName, "/")
				if len(parts) > 0 && parts[0] != "" {
					branch = parts[0]
					break
				}
			}
		}
	}

	if branch == "" && foundGitDir == "" {
		if entries, rerr := os.ReadDir(cwd); rerr == nil {
			for _, e := range entries {
				if e.IsDir() {
					subBase := filepath.Join(cwd, e.Name())
					subGit := filepath.Join(subBase, ".git")
					b, found := inspectGitDir(subGit, subBase)
					if found && b != "" {
						branch = b
						break
					}
				}
			}
		}
	}

	if branch == "" && foundGitDir != "" {
		cmd := exec.Command("git", "-C", foundGitDir, "describe", "--tags", "--exact-match")
		if out, oerr := cmd.Output(); oerr == nil {
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
