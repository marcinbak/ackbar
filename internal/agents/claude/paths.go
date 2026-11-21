package claude

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func effectiveHome(homeDir string) string {
	if strings.TrimSpace(homeDir) != "" {
		return homeDir
	}
	return agentutil.RealUserHome()
}

func (*Plugin) ResolveBinary() (string, error) {
	if bin, err := agentutil.ResolveBinary("claude"); err == nil && bin != "" {
		return bin, nil
	}
	return "claude", nil
}

func openRegularFileNonBlocking(path string) (*os.File, error) {
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

func encodeProjectDir(p string) string {
	clean := filepath.Clean(p)
	var b strings.Builder
	for i := 0; i < len(clean); i++ {
		c := clean[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func FindTranscriptPath(homeDir, cwd, nativeID string) string {
	if !agentutil.IsUUID(nativeID) {
		return ""
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return ""
	}
	fileName := nativeID + ".jsonl"
	encodedCwd := ""
	slug := ""
	if cwd != "" {
		encodedCwd = encodeProjectDir(cwd)
		slug = strings.ReplaceAll(cwd, "/", "-")
	}

	checkBaseProjects := func(baseProjects string) string {
		if encodedCwd != "" {
			if p := filepath.Join(baseProjects, encodedCwd, fileName); isRegularFile(p) {
				return p
			}
			if slug != encodedCwd {
				if p := filepath.Join(baseProjects, slug, fileName); isRegularFile(p) {
					return p
				}
			}
		}
		if matches, err := filepath.Glob(filepath.Join(baseProjects, "*", fileName)); err == nil {
			for _, m := range matches {
				if isRegularFile(m) {
					return m
				}
			}
		}
		return ""
	}

	if p := checkBaseProjects(filepath.Join(home, ".claude", "projects")); p != "" {
		return p
	}
	if encodedCwd != "" {
		if matches, err := filepath.Glob(filepath.Join(home, ".claude-profiles", "*", "projects", encodedCwd, fileName)); err == nil {
			for _, m := range matches {
				if isRegularFile(m) {
					return m
				}
			}
		}
	}
	if matches, err := filepath.Glob(filepath.Join(home, ".claude-profiles", "*", "projects", "*", fileName)); err == nil {
		for _, m := range matches {
			if isRegularFile(m) {
				return m
			}
		}
	}
	if p := checkBaseProjects(filepath.Join(home, "projects")); p != "" {
		return p
	}
	return ""
}

func isNonSymlinkEntry(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

func (*Plugin) HasConversationArtifacts(dir, nativeID string) bool {
	if !agentutil.IsUUID(nativeID) {
		return false
	}
	base := effectiveHome(dir)
	if base == "" {
		return false
	}
	if isNonSymlinkEntry(filepath.Join(base, "session-env", nativeID)) ||
		isNonSymlinkEntry(filepath.Join(base, ".claude", "session-env", nativeID)) {
		return true
	}
	checkGlob := func(pat string, fn func(string) bool) bool {
		matches, err := filepath.Glob(pat)
		if err != nil {
			return false
		}
		for _, m := range matches {
			if fn(m) {
				return true
			}
		}
		return false
	}
	return checkGlob(filepath.Join(base, "projects", "*", nativeID+".jsonl"), isRegularFile) ||
		checkGlob(filepath.Join(base, ".claude", "projects", "*", nativeID+".jsonl"), isRegularFile) ||
		checkGlob(filepath.Join(base, ".claude-profiles", "*", "projects", "*", nativeID+".jsonl"), isRegularFile) ||
		checkGlob(filepath.Join(base, ".claude-profiles", "*", "session-env", nativeID), isNonSymlinkEntry)
}

func (*Plugin) StatTranscript(homeDir, cwd, nativeID string) (string, int64, int64, error) {
	filePath := FindTranscriptPath(homeDir, cwd, nativeID)
	if filePath == "" {
		return "", 0, 0, os.ErrNotExist
	}
	st, err := os.Stat(filePath)
	if err != nil {
		return "", 0, 0, err
	}
	if !st.Mode().IsRegular() {
		return "", 0, 0, os.ErrInvalid
	}
	return filePath, st.Size(), st.ModTime().UnixNano(), nil
}

func classifyDoc(name string) (category, label string, priority int) {
	nl := strings.ToLower(name)
	switch {
	case nl == "task.md" || nl == "implementation_plan.md" || nl == "walkthrough.md":
		return "plan", "Active Plan", 10
	case strings.Contains(nl, "plan") || strings.Contains(nl, "todo") || strings.Contains(nl, "backlog"):
		return "plan", "Plan / Task", 8
	case nl == "agents.md" || nl == "claude.md" || nl == "architecture.md":
		return "guidelines", "Guidelines", 7
	case nl == "readme.md" || strings.HasPrefix(nl, "readme"):
		return "project", "Readme", 5
	case strings.Contains(nl, "prd") || strings.Contains(nl, "rfc") || strings.Contains(nl, "spec"):
		return "project", "Specification", 6
	case strings.Contains(nl, "guide") || strings.Contains(nl, "handover"):
		return "guidelines", "Guide", 6
	default:
		return "plan", "Claude Plan", 2
	}
}

func (*Plugin) ListWorkspaceDocuments(_, cwd, _ string) []agent.WorkspaceDoc {
	if cwd == "" {
		return nil
	}
	claudeDir := filepath.Join(cwd, ".claude")
	if !agentutil.DirExists(claudeDir) {
		return nil
	}
	var docs []agent.WorkspaceDoc
	_ = filepath.WalkDir(claudeDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			rel, _ := filepath.Rel(cwd, p)
			cat, label, prio := classifyDoc(d.Name())
			docs = append(docs, agent.WorkspaceDoc{
				FullPath:      p,
				Title:         d.Name(),
				RelPath:       rel,
				Category:      cat,
				CategoryLabel: label,
				Priority:      prio,
			})
		}
		return nil
	})
	return docs
}
