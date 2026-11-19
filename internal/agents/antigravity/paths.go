package antigravity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ackbar/internal/agentutil"
)

func (*Plugin) ResolveBinary() (string, error) {
	for _, name := range []string{"agy", "antigravity"} {
		if bin, err := agentutil.ResolveBinary(name); err == nil && bin != "" {
			return bin, nil
		}
	}
	return "", exec.ErrNotFound
}

func effectiveHome(homeDir string) string {
	if strings.TrimSpace(homeDir) != "" {
		return homeDir
	}
	return agentutil.RealUserHome()
}

func baseDirs(home string) []string {
	if home == "" {
		return nil
	}
	dirs := []string{
		filepath.Join(home, ".gemini", "antigravity"),
		filepath.Join(home, ".gemini", "antigravity-cli"),
		filepath.Join(home, ".antigravity"),
	}
	if p1, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*")); err == nil {
		dirs = append(dirs, p1...)
	}
	if p2, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity")); err == nil {
		dirs = append(dirs, p2...)
	}
	return dirs
}

func brainDirs(home string) []string {
	if home == "" {
		return nil
	}
	dirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain"),
		filepath.Join(home, ".antigravity", "brain"),
	}
	if m1, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "brain")); err == nil {
		dirs = append(dirs, m1...)
	}
	if m2, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain")); err == nil {
		dirs = append(dirs, m2...)
	}
	return dirs
}

func transcriptCandidateDirs(home, convID string) []string {
	if home == "" || !agentutil.IsSafeSessionID(convID) {
		return nil
	}
	cleanID := filepath.Base(filepath.Clean(convID))
	dirs := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", cleanID, ".system_generated", "logs"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", cleanID, ".system_generated", "logs"),
		filepath.Join(home, ".antigravity", "brain", cleanID, ".system_generated", "logs"),
	}
	for _, pat := range []string{
		filepath.Join(home, ".gemini-profiles", "*", "brain", cleanID, ".system_generated", "logs"),
		filepath.Join(home, ".gemini-profiles", "*", "antigravity", "brain", cleanID, ".system_generated", "logs"),
		filepath.Join(home, ".gemini-profiles", "*", "antigravity-cli", "brain", cleanID, ".system_generated", "logs"),
	} {
		if m, err := filepath.Glob(pat); err == nil {
			dirs = append(dirs, m...)
		}
	}
	return dirs
}

func annotationDirs(home string) []string {
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".gemini", "antigravity", "annotations"),
		filepath.Join(home, ".gemini", "antigravity-cli", "annotations"),
		filepath.Join(home, ".antigravity", "annotations"),
	}
}

func metadataCachePaths(home string) []string {
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".gemini", "antigravity", "cache", "conversation_metadata.json"),
		filepath.Join(home, ".antigravity", "cache", "conversation_metadata.json"),
	}
}

func protoPaths(home string) []string {
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".gemini", "antigravity", "agyhub_summaries_proto.pb"),
		filepath.Join(home, ".gemini", "antigravity-cli", "agyhub_summaries_proto.pb"),
		filepath.Join(home, ".antigravity", "agyhub_summaries_proto.pb"),
	}
}

func hookConfigCandidatePaths(home string) []string {
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".gemini", "config", "hooks.json"),
		filepath.Join(home, ".gemini", "settings.json"),
		filepath.Join(home, ".gemini", "antigravity", "settings.json"),
		filepath.Join(home, ".antigravity", "settings.json"),
		filepath.Join(home, ".antigravity", "config", "hooks.json"),
		filepath.Join(home, ".gemini", "antigravity", "config", "hooks.json"),
	}
}

func hasUserAnnotation(home, convID string) bool {
	if home == "" || convID == "" {
		return false
	}
	var paths []string
	for _, d := range annotationDirs(home) {
		paths = append(paths, filepath.Join(d, convID+".pbtxt"))
	}
	if m1, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "annotations", convID+".pbtxt")); err == nil {
		paths = append(paths, m1...)
	}
	if m2, err := filepath.Glob(filepath.Join(home, ".gemini-profiles", "*", "antigravity", "annotations", convID+".pbtxt")); err == nil {
		paths = append(paths, m2...)
	}
	for _, p := range paths {
		if agentutil.FileExists(p) {
			return true
		}
	}
	return false
}

func (p *Plugin) HasConversationArtifacts(homeDir, nativeID string) bool {
	home := effectiveHome(homeDir)
	if home == "" || !agentutil.IsSafeSessionID(nativeID) {
		return false
	}
	brainAndDB := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", nativeID),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID),
		filepath.Join(home, ".antigravity", "brain", nativeID),
		filepath.Join(home, "antigravity-cli", "brain", nativeID),
		filepath.Join(home, "antigravity", "brain", nativeID),
		filepath.Join(home, "brain", nativeID),
		filepath.Join(home, ".gemini", "antigravity-cli", "conversations", nativeID+".db"),
		filepath.Join(home, ".gemini", "antigravity", "conversations", nativeID+".db"),
		filepath.Join(home, ".antigravity", "conversations", nativeID+".db"),
		filepath.Join(home, "antigravity-cli", "conversations", nativeID+".db"),
		filepath.Join(home, "conversations", nativeID+".db"),
	}
	for _, c := range brainAndDB {
		if stat, err := os.Stat(c); err == nil && (stat.IsDir() || stat.Size() > 0) {
			return true
		}
	}
	if !agentutil.IsUUID(nativeID) {
		return false
	}
	extra := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "presence", nativeID+".lock"),
		filepath.Join(home, ".gemini", "antigravity", "presence", nativeID+".lock"),
		filepath.Join(home, ".antigravity", "presence", nativeID+".lock"),
		filepath.Join(home, ".gemini", "antigravity", "annotations", nativeID+".pbtxt"),
		filepath.Join(home, ".gemini", "antigravity-cli", "annotations", nativeID+".pbtxt"),
		filepath.Join(home, ".antigravity", "annotations", nativeID+".pbtxt"),
	}
	for _, c := range extra {
		if _, err := os.Stat(c); err == nil {
			return true
		}
	}
	return false
}

func extractConvIDFromPath(path string) string {
	clean := filepath.ToSlash(path)
	for _, m := range []string{"/presence/", "/brain/", "/conversations/", "/annotations/"} {
		if idx := strings.Index(clean, m); idx != -1 {
			sub := clean[idx+len(m):]
			if slashIdx := strings.Index(sub, "/"); slashIdx != -1 {
				sub = sub[:slashIdx]
			}
			sub = strings.TrimSuffix(sub, ".lock")
			sub = strings.TrimSuffix(sub, ".db")
			sub = strings.TrimSuffix(sub, ".pbtxt")
			if agentutil.IsUUID(sub) {
				return sub
			}
		}
	}
	return ""
}

func normalizeToGitRoot(dir, home string) string {
	if dir == "" {
		return ""
	}
	cleanHome := ""
	if home != "" {
		cleanHome = filepath.Clean(home)
	}
	curr := filepath.Clean(dir)
	for curr != "" && curr != "/" && curr != "." && curr != cleanHome {
		if _, err := os.Stat(filepath.Join(curr, ".git")); err == nil {
			return curr
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return dir
}
