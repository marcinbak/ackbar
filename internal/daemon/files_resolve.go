package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	hashLineRegex  = regexp.MustCompile(`(?i)#L(\d+)(?:-L?(\d+))?$`)
	colonLineRegex = regexp.MustCompile(`:(\d+)(?:[-:](\d+))?$`)
)

// splitFilePathAndLine extracts clean file path, line number, and column number
// from paths containing line anchors:
// Supports /path/to/file.go#L75-L176, /path/to/file.go#L75, /path/to/file.go:75:10, /path/to/file.go:75-176, and /path/to/file.go:75.
func splitFilePathAndLine(rawPath string) (cleanPath string, line int, col int) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", 0, 0
	}

	// 1. Check for #L(\d+)(?:-L?(\d+))?
	if loc := hashLineRegex.FindStringSubmatchIndex(rawPath); loc != nil {
		clean := rawPath[:loc[0]]
		startStr := rawPath[loc[2]:loc[3]]
		startL, _ := strconv.Atoi(startStr)
		return clean, startL, 0
	}

	// 2. Check for :(\d+)(?:[-:](\d+))?
	if loc := colonLineRegex.FindStringSubmatchIndex(rawPath); loc != nil {
		clean := rawPath[:loc[0]]
		startStr := rawPath[loc[2]:loc[3]]
		startL, _ := strconv.Atoi(startStr)
		colNum := 0
		if loc[4] >= 0 && loc[5] >= 0 {
			// Only treat group 2 as col when the preceding separator is ':' (not '-')
			sepIdx := loc[4] - 1
			if sepIdx >= 0 && rawPath[sepIdx] == ':' {
				colStr := rawPath[loc[4]:loc[5]]
				colNum, _ = strconv.Atoi(colStr)
			}
		}
		return clean, startL, colNum
	}

	return rawPath, 0, 0
}

// resolveFilePath resolves a user or agent requested file path against a session's CWD
// and ensures security restrictions (no traversal into sensitive credentials or system files).
func (s *Server) resolveFilePath(sess *Session, reqPath string) (string, error) {
	reqPath = strings.TrimSpace(reqPath)
	if reqPath == "" {
		return "", fmt.Errorf("empty file path")
	}

	// Strip file:// prefix if present
	if strings.HasPrefix(reqPath, "file://") {
		reqPath = strings.TrimPrefix(reqPath, "file://")
	}

	// Strip line range anchors (#L... or :line) so /v1/files/content and /v1/files/open resolve real file on disk
	cleanReqPath, _, _ := splitFilePathAndLine(reqPath)
	if cleanReqPath != "" {
		reqPath = cleanReqPath
	}

	homeDir, _ := os.UserHomeDir()

	// Expand home directory ~ prefix
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
		// Relative path: resolve against session cwd if available
		cwd := ""
		if sess != nil && sess.Cwd != "" {
			cwd = sess.Cwd
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

	// Cross-OS Home Directory Path Fallback (/Users/<user> <-> /home/<user>)
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

	// Block explicitly sensitive paths and credentials
	sensitiveSubstrings := []string{
		"/.ssh/",
		"/.ssh",
		"/.gnupg/",
		"/.gnupg",
		"/.aws/",
		"/.aws",
		"/etc/shadow",
		"/etc/sudoers",
		"/etc/master.passwd",
		"/.config/gcloud/",
	}
	for _, sub := range sensitiveSubstrings {
		if strings.Contains(cleanPath, sub) || strings.HasSuffix(cleanPath, strings.TrimSuffix(sub, "/")) {
			return "", fmt.Errorf("access denied: forbidden sensitive file path")
		}
	}

	// Permitted directory roots check:
	// 1. Session CWD (if provided)
	// 2. User Home Directory
	// 3. System Temporary Directory (os.TempDir(), /tmp, /private/tmp, /var/tmp)
	allowed := false

	if sess != nil && sess.Cwd != "" {
		cleanCwd := filepath.Clean(sess.Cwd)
		if cleanPath == cleanCwd || strings.HasPrefix(cleanPath, cleanCwd+string(filepath.Separator)) {
			allowed = true
		}
	}

	if !allowed && homeDir != "" {
		cleanHome := filepath.Clean(homeDir)
		if cleanPath == cleanHome || strings.HasPrefix(cleanPath, cleanHome+string(filepath.Separator)) {
			allowed = true
		}
	}

	if !allowed {
		allowedTmpDirs := []string{
			filepath.Clean(os.TempDir()),
			"/tmp",
			"/private/tmp",
			"/var/tmp",
		}
		for _, tmp := range allowedTmpDirs {
			if cleanPath == tmp || strings.HasPrefix(cleanPath, tmp+string(filepath.Separator)) {
				allowed = true
				break
			}
		}
	}

	if !allowed {
		return "", fmt.Errorf("access denied: path outside permitted directories")
	}

	return cleanPath, nil
}
