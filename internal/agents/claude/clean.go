package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agentutil"
)

func (*Plugin) CleanSessionFiles(homeDir, _, nativeID string) error {
	if !agentutil.IsUUID(nativeID) {
		return nil
	}
	home := effectiveHome(homeDir)
	if home == "" {
		return nil
	}

	var bases []string
	seen := make(map[string]bool)
	addBase := func(dir string) {
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if !seen[clean] {
			seen[clean] = true
			bases = append(bases, clean)
		}
	}

	addBase(filepath.Join(home, ".claude"))
	if matches, err := filepath.Glob(filepath.Join(home, ".claude-profiles", "*")); err == nil {
		for _, m := range matches {
			if info, serr := os.Stat(m); serr == nil && info.IsDir() {
				addBase(m)
			}
		}
	}
	addBase(home)

	for _, base := range bases {
		cleanBaseSessionArtifacts(base, nativeID)
	}
	return nil
}

func cleanBaseSessionArtifacts(baseDir, nativeID string) {
	projectsDir := filepath.Join(baseDir, "projects")
	if projDirs, err := os.ReadDir(projectsDir); err == nil {
		for _, pDir := range projDirs {
			if !pDir.IsDir() {
				continue
			}
			projPath := filepath.Join(projectsDir, pDir.Name())
			_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("%s.jsonl", nativeID)))
			_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("agent-%s.jsonl", nativeID)))
			_ = os.RemoveAll(filepath.Join(projPath, nativeID))
		}
	}

	sessionsDir := filepath.Join(baseDir, "sessions")
	if sFiles, err := os.ReadDir(sessionsDir); err == nil {
		for _, sf := range sFiles {
			if sf.IsDir() || !strings.HasSuffix(sf.Name(), ".json") {
				continue
			}
			metaPath := filepath.Join(sessionsDir, sf.Name())
			if data, err := os.ReadFile(metaPath); err == nil && strings.Contains(string(data), nativeID) {
				_ = os.Remove(metaPath)
			}
		}
	}

	for _, sub := range []string{"tasks", "session-env", "file-history", "shell-snapshots"} {
		_ = os.RemoveAll(filepath.Join(baseDir, sub, nativeID))
	}
}
