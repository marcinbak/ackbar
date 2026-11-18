package grok

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agentutil"
)

func HomeDir(home string) string {
	realHome := agentutil.RealUserHome()
	if envHome := strings.TrimSpace(os.Getenv("GROK_HOME")); envHome != "" && (home == "" || filepath.Clean(home) == filepath.Clean(realHome)) {
		return envHome
	}
	if home == "" {
		home = realHome
	}
	return filepath.Join(home, ".grok")
}

func EncodeCwd(cwd string) string {
	return url.PathEscape(filepath.Clean(cwd))
}

func FindSessionDir(grokHome, cwd, nativeID string) string {
	if grokHome == "" || !agentutil.IsSafeGrokSessionID(nativeID) {
		return ""
	}
	if cwd != "" {
		encoded := EncodeCwd(cwd)
		if len(encoded) <= 255 {
			dir := filepath.Join(grokHome, "sessions", encoded, nativeID)
			if agentutil.DirExists(dir) {
				return dir
			}
		}
	}
	sessRoot := filepath.Join(grokHome, "sessions")
	if entries, err := os.ReadDir(sessRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				dir := filepath.Join(sessRoot, e.Name(), nativeID)
				if agentutil.DirExists(dir) {
					return dir
				}
			}
		}
	}
	flatDir := filepath.Join(sessRoot, nativeID)
	if agentutil.DirExists(flatDir) {
		return flatDir
	}
	return ""
}

func FindSessionDirForHome(home, cwd, nativeID string) string {
	if !agentutil.IsSafeGrokSessionID(nativeID) {
		return ""
	}
	if dir := FindSessionDir(HomeDir(home), cwd, nativeID); dir != "" {
		return dir
	}
	userHome := home
	if userHome == "" {
		userHome = agentutil.RealUserHome()
	}
	profilesDir := filepath.Join(userHome, ".grok-profiles")
	if entries, err := os.ReadDir(profilesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				if dir := FindSessionDir(filepath.Join(profilesDir, e.Name()), cwd, nativeID); dir != "" {
					return dir
				}
			}
		}
	}
	return ""
}

func findLegacyLog(home, nativeID string) string {
	if !agentutil.IsSafeGrokSessionID(nativeID) {
		return ""
	}
	if home == "" {
		home = agentutil.RealUserHome()
	}
	for _, baseDir := range []string{HomeDir(home), filepath.Join(home, ".grok")} {
		cand1 := filepath.Join(baseDir, "sessions", nativeID+".jsonl")
		if agentutil.FileExists(cand1) {
			return cand1
		}
		cand2 := filepath.Join(baseDir, "sessions", nativeID, "transcript.jsonl")
		if agentutil.FileExists(cand2) {
			return cand2
		}
		sessRoot := filepath.Join(baseDir, "sessions")
		if entries, err := os.ReadDir(sessRoot); err == nil {
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() && strings.Contains(name, nativeID) && strings.HasSuffix(name, ".jsonl") {
					return filepath.Join(sessRoot, name)
				}
			}
		}
	}
	return ""
}
