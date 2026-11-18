package daemon

import (
	"path/filepath"
	"strings"
)

var tccMediaLibrarySuffixes = []string{
	".photoslibrary",
	".photolibrary",
	".aplibrary",
	".musiclibrary",
	".tvlibrary",
	".theater",
}

var tccProtectedHomeSubdirs = map[string]bool{
	"library":   true,
	"pictures":  true,
	"music":     true,
	"movies":    true,
	"desktop":   true,
	"documents": true,
	"downloads": true,
	"public":    true,
	".trash":    true,
}

// IsProtectedHomeWalkDir reports whether a directory walk should skip candidatePath to avoid macOS TCC prompts.
func IsProtectedHomeWalkDir(candidatePath, homeDir, baseName string) bool {
	lowerBase := strings.ToLower(baseName)
	for _, suffix := range tccMediaLibrarySuffixes {
		if strings.HasSuffix(lowerBase, suffix) {
			return true
		}
	}

	if homeDir == "" || candidatePath == "" {
		return false
	}

	cleanHome := filepath.Clean(homeDir)
	cleanCandidate := filepath.Clean(candidatePath)
	if cleanCandidate != cleanHome && filepath.Dir(cleanCandidate) == cleanHome {
		return tccProtectedHomeSubdirs[lowerBase]
	}

	return false
}
