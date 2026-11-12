package provider

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/daemon"
)

func (c *ClaudeProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	return daemon.ReadClaudeSessionMeta(cwd, nativeID)
}

func (c *ClaudeProvider) ResolveSessionTitle(cwd, nativeID string) string {
	meta := c.ReadSessionMetadata(cwd, nativeID)
	if meta != nil {
		if meta.CustomTitle != "" {
			return meta.CustomTitle
		}
		if meta.AITitle != "" {
			return meta.AITitle
		}
		if meta.Title != "" {
			return meta.Title
		}
		if meta.FirstPrompt != "" {
			return daemon.TruncateTitle(meta.FirstPrompt)
		}
	}
	return ""
}

// FindClaudeTranscriptPath resolves the on-disk JSONL log path for a Claude Code session.
func FindClaudeTranscriptPath(home, cwd, nativeID string) string {
	var targetFile string
	projectsDir := filepath.Join(home, ".claude", "projects")

	if cwd != "" {
		encodedCwd := strings.ReplaceAll(cwd, "/", "-")
		cand := filepath.Join(projectsDir, encodedCwd, nativeID+".jsonl")
		if fileExists(cand) {
			targetFile = cand
		}
	}

	if targetFile == "" && dirExists(projectsDir) {
		entries, err := os.ReadDir(projectsDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					cand := filepath.Join(projectsDir, e.Name(), nativeID+".jsonl")
					if fileExists(cand) {
						targetFile = cand
						break
					}
				}
			}
		}
	}

	if targetFile == "" {
		profileDirs, _ := filepath.Glob(filepath.Join(home, ".claude-profiles", "*", "projects"))
		for _, pDir := range profileDirs {
			if cwd != "" {
				encodedCwd := strings.ReplaceAll(cwd, "/", "-")
				cand := filepath.Join(pDir, encodedCwd, nativeID+".jsonl")
				if fileExists(cand) {
					targetFile = cand
					break
				}
			}
			if dirExists(pDir) {
				entries, err := os.ReadDir(pDir)
				if err == nil {
					for _, e := range entries {
						if e.IsDir() {
							cand := filepath.Join(pDir, e.Name(), nativeID+".jsonl")
							if fileExists(cand) {
								targetFile = cand
								break
							}
						}
					}
				}
			}
			if targetFile != "" {
				break
			}
		}
	}
	return targetFile
}

// ParseClaudeCodeJSONL parses lines from an io.Reader into TranscriptMessages, coalescing onto base.
func ParseClaudeCodeJSONL(r io.Reader, filterUser func(raw map[string]interface{}, text string) (string, bool), base []daemon.TranscriptMessage) ([]daemon.TranscriptMessage, error) {
	return daemon.ParseClaudeCodeJSONL(r, filterUser, base)
}

func (c *ClaudeProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	targetFile := FindClaudeTranscriptPath(home, cwd, nativeID)
	if targetFile == "" {
		return nil, fmt.Errorf("claude log not found for session %s", nativeID)
	}

	file, err := os.Open(targetFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return ParseClaudeCodeJSONL(file, c.FilterUserEntry, nil)
}

func (c *ClaudeProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if nativeID == "" {
		return nil
	}
	claudeDir := filepath.Join(home, ".claude")
	claudeProjectsDir := filepath.Join(claudeDir, "projects")
	if projDirs, err := os.ReadDir(claudeProjectsDir); err == nil {
		for _, pDir := range projDirs {
			if pDir.IsDir() {
				projPath := filepath.Join(claudeProjectsDir, pDir.Name())
				_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("%s.jsonl", nativeID)))
				_ = os.Remove(filepath.Join(projPath, fmt.Sprintf("agent-%s.jsonl", nativeID)))
				_ = os.RemoveAll(filepath.Join(projPath, nativeID))
			}
		}
	}

	sessionsDir := filepath.Join(claudeDir, "sessions")
	if sFiles, err := os.ReadDir(sessionsDir); err == nil {
		for _, sf := range sFiles {
			if strings.HasSuffix(sf.Name(), ".json") {
				metaPath := filepath.Join(sessionsDir, sf.Name())
				if data, err := os.ReadFile(metaPath); err == nil {
					if strings.Contains(string(data), nativeID) {
						_ = os.Remove(metaPath)
					}
				}
			}
		}
	}

	_ = os.RemoveAll(filepath.Join(claudeDir, "tasks", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "session-env", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "file-history", nativeID))
	_ = os.RemoveAll(filepath.Join(claudeDir, "shell-snapshots", nativeID))

	return nil
}
