package daemon

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	hashLineRegex  = regexp.MustCompile(`(?i)#L(\d+)(?:-L?(\d+))?$`)
	colonLineRegex = regexp.MustCompile(`:(\d+)(?:[-:](\d+))?$`)

	sensitiveSubstrings = []string{
		"/.ssh/", "/.ssh", "/.gnupg/", "/.gnupg", "/.aws/", "/.aws",
		"/etc/shadow", "/etc/sudoers", "/etc/master.passwd", "/.config/gcloud/", "/.config/gcloud",
	}
)

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func stripFileScheme(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "file://localhost/") {
		s = "/" + strings.TrimPrefix(s, "file://localhost/")
	} else if strings.HasPrefix(s, "file://") {
		s = strings.TrimPrefix(s, "file://")
	}
	if len(s) >= 3 && s[0] == '/' && isDriveLetter(s[1]) && s[2] == ':' {
		s = s[1:]
	}
	return s
}

func splitFilePathAndLine(rawPath string) (cleanPath string, line int, col int) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", 0, 0
	}
	if loc := hashLineRegex.FindStringSubmatchIndex(rawPath); loc != nil {
		startL, _ := strconv.Atoi(rawPath[loc[2]:loc[3]])
		return rawPath[:loc[0]], startL, 0
	}
	if loc := colonLineRegex.FindStringSubmatchIndex(rawPath); loc != nil {
		startL, _ := strconv.Atoi(rawPath[loc[2]:loc[3]])
		colNum := 0
		if loc[4] >= 0 && loc[5] >= 0 && loc[4]-1 >= 0 && rawPath[loc[4]-1] == ':' {
			colNum, _ = strconv.Atoi(rawPath[loc[4]:loc[5]])
		}
		return rawPath[:loc[0]], startL, colNum
	}
	return rawPath, 0, 0
}

func isPathWithinOrEqual(target, parent string) bool {
	if parent == "" || target == "" {
		return false
	}
	cleanTarget := filepath.Clean(target)
	cleanParent := filepath.Clean(parent)
	if cleanTarget == cleanParent {
		return true
	}
	prefix := cleanParent
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(cleanTarget, prefix)
}

func checkSensitivePath(p string) error {
	clean := filepath.Clean(p)
	for _, sub := range sensitiveSubstrings {
		if strings.Contains(clean, sub) || strings.HasSuffix(clean, strings.TrimSuffix(sub, "/")) {
			return fmt.Errorf("access denied: forbidden sensitive file path")
		}
	}
	return nil
}

func checkPermittedRoots(p string, sess *Session, extraAllowedRoot, homeDir string) bool {
	if (sess != nil && sess.Cwd != "" && isPathWithinOrEqual(p, sess.Cwd)) ||
		(extraAllowedRoot != "" && isPathWithinOrEqual(p, extraAllowedRoot)) ||
		(homeDir != "" && isPathWithinOrEqual(p, homeDir)) {
		return true
	}
	tmpRoots := []string{filepath.Clean(os.TempDir()), "/tmp", "/private/tmp", "/var/tmp"}
	if evalTmp, err := filepath.EvalSymlinks(os.TempDir()); err == nil && evalTmp != "" {
		tmpRoots = append(tmpRoots, filepath.Clean(evalTmp))
	}
	for _, tmp := range tmpRoots {
		if isPathWithinOrEqual(p, tmp) {
			return true
		}
	}
	return false
}

func validatePermittedFilePath(cleanPath string, sess *Session, extraAllowedRoot, homeDir string) error {
	clean := filepath.Clean(cleanPath)
	if err := checkSensitivePath(clean); err != nil {
		return err
	}
	if !checkPermittedRoots(clean, sess, extraAllowedRoot, homeDir) {
		return fmt.Errorf("access denied: path outside permitted directories")
	}

	if target, err := filepath.EvalSymlinks(clean); err == nil {
		cleanTarget := filepath.Clean(target)
		if err := checkSensitivePath(cleanTarget); err != nil {
			return err
		}
		if !checkPermittedRoots(cleanTarget, sess, extraAllowedRoot, homeDir) {
			return fmt.Errorf("access denied: symlink target outside permitted directories")
		}
	} else if fi, lerr := os.Lstat(clean); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		if rawTarget, rerr := os.Readlink(clean); rerr == nil {
			target := rawTarget
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(clean), target)
			}
			cleanTarget := filepath.Clean(target)
			if err := checkSensitivePath(cleanTarget); err != nil {
				return err
			}
			if !checkPermittedRoots(cleanTarget, sess, extraAllowedRoot, homeDir) {
				return fmt.Errorf("access denied: symlink target outside permitted directories")
			}
		}
	}
	return nil
}

func extractUUIDCandidate(sessionID string) string {
	s := strings.TrimSpace(sessionID)
	if IsUUID(s) {
		return s
	}
	parts := strings.Split(s, ":")
	if len(parts) > 1 && IsUUID(parts[len(parts)-1]) {
		return parts[len(parts)-1]
	}
	if len(s) >= 36 && IsUUID(s[len(s)-36:]) {
		return s[len(s)-36:]
	}
	return ""
}

func (s *Server) resolveSessionForFile(sessionID string) *Session {
	if sessionID == "" {
		return nil
	}
	if sess := s.resolveSession(sessionID); sess != nil {
		return sess
	}
	uuidCandidate := extractUUIDCandidate(sessionID)
	if uuidCandidate == "" {
		return nil
	}
	homeDir, _ := os.UserHomeDir()
	if homeDir == "" {
		return nil
	}
	parentID := ""
	if h, ok := s.PluginRegistry().AsHooks("antigravity"); ok {
		parentID, _, _ = h.ResolveSubagentParent(homeDir, uuidCandidate)
	} else {
		parentID, _, _ = ResolveAntigravitySubagentParent(homeDir, uuidCandidate)
	}
	if parentID != "" {
		if parentSess := s.resolveSession(parentID); parentSess != nil {
			sessCopy := *parentSess
			sessCopy.NativeID = uuidCandidate
			return &sessCopy
		}
	}
	for _, bDir := range antigravityBrainDirs(homeDir) {
		if fi, err := os.Stat(filepath.Join(bDir, uuidCandidate)); err == nil && fi.IsDir() {
			return &Session{ID: sessionID, Agent: "antigravity", NativeID: uuidCandidate}
		}
	}
	return nil
}

func (s *Server) applyEffectiveCwd(sess *Session, cwdParam, targetHost string) (*Session, string, error) {
	origCwd := ""
	if sess != nil {
		origCwd = sess.Cwd
	}
	cwdParam = strings.TrimSpace(cwdParam)
	if cwdParam == "" {
		return sess, origCwd, nil
	}
	resolvedCwd, err := s.resolveFilePath(sess, cwdParam)
	if err != nil {
		return nil, "", err
	}
	var effective *Session
	if sess != nil {
		cp := *sess
		cp.Cwd = resolvedCwd
		effective = &cp
	} else {
		effective = &Session{
			Host: targetHost,
			Cwd:  resolvedCwd,
		}
	}
	return effective, origCwd, nil
}

func tryWorktreeRootFallback(p string) string {
	for _, marker := range []string{"/.worktrees/", "/.claude/worktrees/"} {
		if idx := strings.Index(p, marker); idx != -1 {
			repoRoot := p[:idx]
			rest := p[idx+len(marker):]
			if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
				return filepath.Join(repoRoot, rest[slashIdx+1:])
			}
		}
	}
	return ""
}

func tryCrossOSOtherUserFallback(p, homeDir string) string {
	if homeDir == "" {
		return ""
	}
	cleanHome := filepath.Clean(homeDir)
	currentUser := filepath.Base(cleanHome)
	if runtime.GOOS != "darwin" && strings.HasPrefix(p, "/Users/") {
		rest := strings.TrimPrefix(p, "/Users/")
		if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
			otherUser := rest[:slashIdx]
			if otherUser != currentUser {
				return filepath.Join(cleanHome, rest[slashIdx+1:])
			}
		}
	} else if runtime.GOOS == "darwin" && strings.HasPrefix(p, "/home/") {
		rest := strings.TrimPrefix(p, "/home/")
		if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
			otherUser := rest[:slashIdx]
			if otherUser != currentUser {
				return filepath.Join(cleanHome, rest[slashIdx+1:])
			}
		}
	}
	return ""
}

func (s *Server) resolveFilePath(sess *Session, reqPath string) (string, error) {
	return s.resolveFilePathWithOrigCwd(sess, reqPath, "")
}

func (s *Server) resolveFilePathWithOrigCwd(sess *Session, reqPath, origCwd string) (string, error) {
	rawReq := strings.TrimSpace(reqPath)
	if rawReq == "" {
		return "", fmt.Errorf("empty file path")
	}

	reqPath = stripFileScheme(rawReq)
	cleanReqPath, _, _ := splitFilePathAndLine(reqPath)
	if cleanReqPath != "" {
		reqPath = cleanReqPath
	}

	homeDir, _ := os.UserHomeDir()
	if reqPath == "~" || strings.HasPrefix(reqPath, "~/") {
		if homeDir == "" {
			return "", fmt.Errorf("user home directory not found")
		}
		reqPath = filepath.Join(homeDir, strings.TrimPrefix(reqPath, "~"))
	}

	var targetPath string
	if filepath.IsAbs(reqPath) {
		targetPath = filepath.Clean(reqPath)
	} else {
		cwd := ""
		if sess != nil && sess.Cwd != "" {
			cwd = sess.Cwd
		}
		if cwd == "" && origCwd != "" {
			cwd = origCwd
		}
		if cwd == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return "", fmt.Errorf("cannot resolve relative path without session working directory")
			}
		}
		targetPath = filepath.Clean(filepath.Join(cwd, reqPath))
	}

	cleanPath := filepath.Clean(targetPath)

	// Cross-OS same-username fallback (/Users/<user> <-> /home/<user>)
	if homeDir != "" && filepath.IsAbs(cleanPath) {
		cleanHome := filepath.Clean(homeDir)
		currentUser := filepath.Base(cleanHome)
		if currentUser != "" && currentUser != "/" && currentUser != "." {
			hasHomePrefix := cleanPath == cleanHome || strings.HasPrefix(cleanPath, cleanHome+string(filepath.Separator))
			if !hasHomePrefix {
				var rewritten string
				macPrefix := "/Users/" + currentUser
				linuxPrefix := "/home/" + currentUser
				if cleanPath == macPrefix || strings.HasPrefix(cleanPath, macPrefix+"/") {
					rewritten = filepath.Join(cleanHome, strings.TrimPrefix(strings.TrimPrefix(cleanPath, macPrefix), "/"))
				} else if cleanPath == linuxPrefix || strings.HasPrefix(cleanPath, linuxPrefix+"/") {
					rewritten = filepath.Join(cleanHome, strings.TrimPrefix(strings.TrimPrefix(cleanPath, linuxPrefix), "/"))
				}
				if rewritten != "" {
					if _, err := os.Stat(cleanPath); err != nil {
						cleanPath = filepath.Clean(rewritten)
					}
				}
			}
		}
	}

	primaryErr := validatePermittedFilePath(cleanPath, sess, origCwd, homeDir)
	if primaryErr == nil {
		if fi, err := os.Stat(cleanPath); err == nil && !fi.IsDir() {
			return cleanPath, nil
		}
	}

	// Fallback probes
	var candidatePaths []string

	// a) Percent-decoded variant
	if strings.Contains(rawReq, "%") {
		if unescaped, err := url.PathUnescape(rawReq); err == nil && unescaped != rawReq {
			cleanUnescaped := stripFileScheme(unescaped)
			cleanUnescaped, _, _ = splitFilePathAndLine(cleanUnescaped)
			if cleanUnescaped == "~" || strings.HasPrefix(cleanUnescaped, "~/") {
				if homeDir != "" {
					cleanUnescaped = filepath.Join(homeDir, strings.TrimPrefix(cleanUnescaped, "~"))
				}
			}
			if !filepath.IsAbs(cleanUnescaped) {
				cwd := ""
				if sess != nil && sess.Cwd != "" {
					cwd = sess.Cwd
				}
				if cwd == "" && origCwd != "" {
					cwd = origCwd
				}
				if cwd != "" {
					cleanUnescaped = filepath.Join(cwd, cleanUnescaped)
				}
			}
			candidatePaths = append(candidatePaths, filepath.Clean(cleanUnescaped))
		}
	}

	// b) Cross-OS different-username rewrite
	if alt := tryCrossOSOtherUserFallback(cleanPath, homeDir); alt != "" {
		candidatePaths = append(candidatePaths, alt)
	}

	// c) Deleted worktree -> repo root
	if wtCand := tryWorktreeRootFallback(cleanPath); wtCand != "" {
		candidatePaths = append(candidatePaths, wtCand)
	}
	for _, cand := range candidatePaths {
		if wtCand := tryWorktreeRootFallback(cand); wtCand != "" {
			candidatePaths = append(candidatePaths, wtCand)
		}
	}

	// d) Relative / CWD-scoped fallbacks
	var rel string
	if !filepath.IsAbs(reqPath) {
		rel = reqPath
	} else if sess != nil && sess.Cwd != "" && isPathWithinOrEqual(cleanPath, sess.Cwd) {
		rel = strings.TrimPrefix(cleanPath, filepath.Clean(sess.Cwd))
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
	} else if origCwd != "" && isPathWithinOrEqual(cleanPath, origCwd) {
		rel = strings.TrimPrefix(cleanPath, filepath.Clean(origCwd))
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
	}

	if rel != "" {
		if unescapedRel, err := url.PathUnescape(rel); err == nil && unescapedRel != rel {
			rel = unescapedRel
		}
		rel = filepath.Clean(rel)
	}

	isSafeRel := rel != "" && rel != "." && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!strings.ContainsAny(rel, "*?[")

	if isSafeRel && origCwd != "" && (sess == nil || origCwd != sess.Cwd) {
		candidatePaths = append(candidatePaths, filepath.Join(origCwd, rel))
	}

	// Probe candidate list
	for _, cand := range candidatePaths {
		cand = filepath.Clean(cand)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			if validatePermittedFilePath(cand, sess, origCwd, homeDir) == nil {
				return cand, nil
			}
		}
	}

	// Worktree discovery in repo roots
	if isSafeRel {
		var roots []string
		if sess != nil && sess.Cwd != "" {
			roots = append(roots, sess.Cwd)
		}
		if origCwd != "" && (sess == nil || origCwd != sess.Cwd) {
			roots = append(roots, origCwd)
		}
		var bestMatch string
		var bestModTime time.Time
		for _, root := range roots {
			subdirs := []string{
				filepath.Join(root, ".worktrees"),
				filepath.Join(root, ".claude", "worktrees"),
			}
			for _, wtBase := range subdirs {
				entries, err := os.ReadDir(wtBase)
				if err != nil {
					continue
				}
				for _, entry := range entries {
					if !entry.IsDir() {
						continue
					}
					cand := filepath.Join(wtBase, entry.Name(), rel)
					if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
						if validatePermittedFilePath(cand, sess, origCwd, homeDir) == nil {
							if bestMatch == "" || fi.ModTime().After(bestModTime) {
								bestMatch = cand
								bestModTime = fi.ModTime()
							}
						}
					}
				}
			}
		}
		if bestMatch != "" {
			return bestMatch, nil
		}

		// Antigravity brain directories
		var nativeID string
		if sess != nil {
			nativeID = sess.NativeID
			if nativeID == "" {
				nativeID = extractUUIDCandidate(sess.ID)
			}
		}
		if nativeID != "" && homeDir != "" {
			for _, bDir := range antigravityBrainDirs(homeDir) {
				cand := filepath.Join(bDir, nativeID, rel)
				if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
					if validatePermittedFilePath(cand, sess, origCwd, homeDir) == nil {
						return cand, nil
					}
				}
				subagentsDir := filepath.Join(bDir, nativeID, ".system_generated", "subagents")
				if entries, err := os.ReadDir(subagentsDir); err == nil {
					for _, entry := range entries {
						if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
							continue
						}
						subUUID := strings.TrimSuffix(entry.Name(), ".json")
						if subUUID != "" {
							subCand := filepath.Join(bDir, subUUID, rel)
							if fi, err := os.Stat(subCand); err == nil && !fi.IsDir() {
								if validatePermittedFilePath(subCand, sess, origCwd, homeDir) == nil {
									return subCand, nil
								}
							}
						}
					}
				}
			}
		}
	}

	if primaryErr == nil {
		return cleanPath, nil
	}
	return "", primaryErr
}
