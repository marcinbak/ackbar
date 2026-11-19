package antigravity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (p *Plugin) ResolveSessionForPID(ctx context.Context, homeDir string, pid int, cmdFields []string) string {
	home := effectiveHome(homeDir)
	sID := p.findSessionForPIDWithDepth(ctx, home, pid, 0)
	if sID != "" && home != "" && p.IsSubagentSession(home, sID, nil) {
		return ""
	}
	if sID == "" {
		for i, arg := range cmdFields {
			if (arg == "--conversation" || arg == "-c" || arg == "--conversation-id") && i+1 < len(cmdFields) {
				cID := strings.TrimSpace(cmdFields[i+1])
				if agentutil.IsUUID(cID) {
					if home != "" && p.IsSubagentSession(home, cID, nil) {
						return ""
					}
					return cID
				}
			}
		}
	}
	return sID
}

func (p *Plugin) findSessionForPIDWithDepth(ctx context.Context, home string, pid, depth int) string {
	if pid <= 0 || depth > 2 {
		return ""
	}
	var fallbackCandidate string
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	if entries, err := os.ReadDir(fdDir); err == nil {
		for _, e := range entries {
			link, err := os.Readlink(filepath.Join(fdDir, e.Name()))
			if err != nil || link == "" {
				continue
			}
			cleanLink := filepath.ToSlash(link)
			convID := extractConvIDFromPath(cleanLink)
			if convID == "" || (home != "" && p.IsSubagentSession(home, convID, nil)) {
				continue
			}
			if strings.Contains(cleanLink, "/presence/") && strings.HasSuffix(cleanLink, ".lock") {
				return convID
			}
			if fallbackCandidate == "" {
				fallbackCandidate = convID
			}
		}
	}
	if fallbackCandidate != "" {
		return fallbackCandidate
	}

	subCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	out, err := exec.CommandContext(subCtx, "lsof", "-a", "-n", "-P", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err == nil || len(out) > 0 {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "n") {
				continue
			}
			cleanPath := filepath.ToSlash(line[1:])
			convID := extractConvIDFromPath(cleanPath)
			if convID == "" || (home != "" && p.IsSubagentSession(home, convID, nil)) {
				continue
			}
			if strings.Contains(cleanPath, "/presence/") && strings.HasSuffix(cleanPath, ".lock") {
				return convID
			}
			if fallbackCandidate == "" {
				fallbackCandidate = convID
			}
		}
	}
	if fallbackCandidate != "" {
		return fallbackCandidate
	}

	if pgrepOut, pErr := exec.CommandContext(subCtx, "pgrep", "-P", strconv.Itoa(pid)).Output(); pErr == nil {
		for _, cpStr := range strings.Fields(string(pgrepOut)) {
			if cp, err := strconv.Atoi(cpStr); err == nil && cp > 0 {
				if sID := p.findSessionForPIDWithDepth(ctx, home, cp, depth+1); sID != "" {
					if home == "" || !p.IsSubagentSession(home, sID, nil) {
						return sID
					}
				}
			}
		}
	}
	return ""
}

func (p *Plugin) findSpawningCandidate(home, cwd string, sessions []*agent.Session) *agent.Session {
	for _, sess := range sessions {
		if sess == nil || (sess.EngineType == agent.EngineHeadless && sess.TmuxName == "") {
			continue
		}
		if sess.Agent == "antigravity" && sess.Cwd == cwd && sess.Managed && sess.State == agent.StateUnknown && sess.OldID == "" {
			if !p.HasConversationArtifacts(home, sess.NativeID) {
				return sess
			}
		}
	}
	for _, sess := range sessions {
		if sess == nil || (sess.EngineType == agent.EngineHeadless && sess.TmuxName == "") {
			continue
		}
		if sess.Agent == "antigravity" && sameOrRelatedDir(sess.Cwd, cwd, home) && sess.Managed && sess.State == agent.StateUnknown && sess.OldID == "" {
			if !p.HasConversationArtifacts(home, sess.NativeID) {
				return sess
			}
		}
	}
	var best *agent.Session
	for _, sess := range sessions {
		if sess == nil || sess.Agent != "antigravity" || !sess.Managed || sess.State == agent.StateEnded || sess.OldID != "" {
			continue
		}
		if !sameOrRelatedDir(sess.Cwd, cwd, home) {
			continue
		}
		if sess.TmuxName != fmt.Sprintf("ackbar-antigravity-%s", sess.NativeID) {
			continue
		}
		if sess.FirstPrompt != "" || (sess.CustomTitle == "" && !agentutil.IsRawSessionName(sess.Name)) {
			continue
		}
		if p.HasConversationArtifacts(home, sess.NativeID) {
			continue
		}
		if best == nil || sess.StartedAt.After(best.StartedAt) {
			best = sess
		}
	}
	return best
}

func sameOrRelatedDir(dirA, dirB, home string) bool {
	if dirA == "" || dirB == "" {
		return false
	}
	a := filepath.Clean(agentutil.ExpandTildeWithHome(dirA, home))
	b := filepath.Clean(agentutil.ExpandTildeWithHome(dirB, home))
	if a == "" || b == "" || a == "/" || b == "/" || a == "." || b == "." {
		return false
	}
	cleanHome := ""
	if home != "" {
		cleanHome = filepath.Clean(home)
		if a == cleanHome || b == cleanHome {
			return false
		}
	}
	if a == b {
		return true
	}
	rootA, rootB := normalizeToGitRoot(a, home), normalizeToGitRoot(b, home)
	if rootA != "" && rootA == rootB && rootA != filepath.Clean(os.TempDir()) {
		return true
	}
	return isSubDir(a, b) || isSubDir(b, a)
}

func isSubDir(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
