package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ackbar/internal/daemon"
)

var antigravityMetaMu sync.Mutex

func (a *AntigravityProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	title := a.ResolveSessionTitle(cwd, nativeID)
	if title != "" {
		return &daemon.SessionMeta{
			Title: title,
		}
	}
	return nil
}

func (a *AntigravityProvider) ResolveSessionTitle(cwd, nativeID string) string {
	return daemon.ReadAntigravitySessionTitle(cwd, nativeID)
}

func (a *AntigravityProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	return daemon.ExtractAntigravityTranscriptMessages(home, nativeID)
}

func (a *AntigravityProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if home == "" || nativeID == "" || nativeID == "." || nativeID == ".." ||
		filepath.Base(nativeID) != nativeID ||
		strings.ContainsAny(nativeID, "/\\") ||
		!isSafeSessionID(nativeID) {
		return nil
	}

	baseDirs := []string{
		filepath.Join(home, ".gemini", "antigravity"),
		filepath.Join(home, ".gemini", "antigravity-cli"),
		filepath.Join(home, ".antigravity"),
	}

	if profilePaths, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*")); len(profilePaths) > 0 {
		baseDirs = append(baseDirs, profilePaths...)
	}
	if profilePaths2, _ := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity")); len(profilePaths2) > 0 {
		baseDirs = append(baseDirs, profilePaths2...)
	}

	seen := make(map[string]bool)
	for _, baseDir := range baseDirs {
		if seen[baseDir] {
			continue
		}
		seen[baseDir] = true

		// Remove brain directory
		_ = os.RemoveAll(filepath.Join(baseDir, "brain", nativeID))

		// Remove matching files in conversations, annotations, and presence subdirectories
		for _, sub := range []string{"conversations", "annotations", "presence"} {
			if matches, err := filepath.Glob(filepath.Join(baseDir, sub, nativeID+".*")); err == nil {
				for _, match := range matches {
					_ = os.Remove(match)
				}
			}
			_ = os.Remove(filepath.Join(baseDir, sub, nativeID))
		}

		// Prune nativeID from cache/conversation_metadata.json if that file exists
		metaPath := filepath.Join(baseDir, "cache", "conversation_metadata.json")
		func() {
			antigravityMetaMu.Lock()
			defer antigravityMetaMu.Unlock()

			data, err := os.ReadFile(metaPath)
			if err != nil {
				return
			}
			var root map[string]interface{}
			if err := json.Unmarshal(data, &root); err != nil || root == nil {
				return
			}
			convs, ok := root["conversations"].(map[string]interface{})
			if !ok || convs == nil {
				return
			}
			if _, exists := convs[nativeID]; !exists {
				return
			}
			delete(convs, nativeID)
			out, err := json.MarshalIndent(root, "", "  ")
			if err != nil {
				return
			}

			tmpFile, err := os.CreateTemp(filepath.Dir(metaPath), "conversation_metadata.*.tmp")
			if err != nil {
				return
			}
			tmpName := tmpFile.Name()
			defer os.Remove(tmpName)

			if _, err := tmpFile.Write(out); err != nil {
				_ = tmpFile.Close()
				return
			}
			if err := tmpFile.Close(); err != nil {
				return
			}
			_ = os.Rename(tmpName, metaPath)
		}()
	}

	return nil
}
