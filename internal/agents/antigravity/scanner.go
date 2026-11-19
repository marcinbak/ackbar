package antigravity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

var (
	ignoredSubcommands = map[string]bool{
		"agent": true, "agents": true, "changelog": true, "help": true,
		"install": true, "mcp": true, "mic-serve": true, "models": true,
		"plugin": true, "plugins": true, "update": true,
	}
	ignoredFlags = map[string]bool{"--prompt": true}
)

func (*Plugin) IsValidNativeID(nativeID string) bool { return agentutil.IsSafeSessionID(nativeID) }

func (*Plugin) IsIgnoredCommand(fullCmd string) bool {
	return agentutil.CheckIgnoredCLIArgs(fullCmd, func(_, base string) bool {
		return base == "antigravity" || base == "agy" || strings.HasPrefix(base, "agy")
	}, ignoredSubcommands, ignoredFlags)
}

func (*Plugin) MatchProcessCommand(binLower, cmdLower string) bool {
	if binLower == "antigravity" || binLower == "agy" || strings.Contains(cmdLower, "bin/agy") {
		return !agentutil.IsAckbarProcess(binLower, cmdLower)
	}
	return false
}

func (*Plugin) RequiresResolvedSessionID() bool               { return true }
func (*Plugin) ResolveSessionOwner(_, _ string) (int, string) { return 0, "" }

func (p *Plugin) ScanDisk(_ context.Context, in agent.DiskScanInput) agent.DiskScanResult {
	var res agent.DiskScanResult
	home := effectiveHome(in.HomeDir)
	if home == "" {
		return res
	}

	bDirs := brainDirs(home)
	subagentIDs := make(map[string]bool)
	for _, bDir := range bDirs {
		if matches, err := filepath.Glob(filepath.Join(bDir, "*", ".system_generated", "subagents", "*.json")); err == nil {
			for _, m := range matches {
				id := strings.TrimSuffix(filepath.Base(m), ".json")
				if agentutil.IsUUID(id) {
					subagentIDs[strings.ToLower(id)] = true
				}
			}
		}
	}

	knownIDs := make(map[string]*agent.Session, len(in.ExistingSessions))
	knownByNative := make(map[string]*agent.Session, len(in.ExistingSessions))
	hasSpawningCandidate := false
	for _, s := range in.ExistingSessions {
		if s == nil {
			continue
		}
		if s.ID != "" {
			knownIDs[s.ID] = s
		}
		if s.NativeID != "" {
			knownByNative[s.NativeID] = s
		}
		if s.Agent == "antigravity" && s.Managed && s.State != agent.StateEnded && s.OldID == "" {
			hasSpawningCandidate = true
		}
	}

	isDeleted := func(ids ...string) bool {
		if in.IsSessionDeleted == nil {
			return false
		}
		for _, id := range ids {
			if id != "" && in.IsSessionDeleted(id) {
				return true
			}
		}
		return false
	}

	pruned := make(map[string]bool)
	adoptedOld := make(map[string]bool)

	for _, brainDir := range bDirs {
		entries, err := os.ReadDir(brainDir)
		if err != nil {
			continue
		}
		for _, bDir := range entries {
			convID := bDir.Name()
			convPath := filepath.Join(brainDir, convID)
			logPath := filepath.Join(convPath, ".system_generated", "logs", "transcript.jsonl")
			if !bDir.IsDir() || !agentutil.FileExists(logPath) || !agentutil.IsUUID(convID) {
				continue
			}
			sessID := fmt.Sprintf("antigravity:%s:%s", in.HostName, convID)
			if isDeleted(sessID, convID) {
				continue
			}

			if p.IsSubagentSession(home, convID, subagentIDs) {
				if ex := knownIDs[sessID]; ex != nil && ex.CustomTitle == "" && agentutil.IsUUID(ex.NativeID) && !pruned[sessID] {
					res.PruneSessionIDs = append(res.PruneSessionIDs, sessID)
					pruned[sessID] = true
				}
				continue
			}

			existing := knownIDs[sessID]
			if existing == nil {
				existing = knownByNative[convID]
			}
			if existing == nil {
				if !hasSpawningCandidate {
					continue
				}
				modTime := time.Now()
				convStart := modTime
				if st, serr := os.Stat(convPath); serr == nil {
					modTime = st.ModTime()
					convStart = modTime
				}
				if lst, lerr := os.Stat(logPath); lerr == nil && lst.ModTime().After(modTime) {
					modTime = lst.ModTime()
				}
				if time.Since(modTime) >= 15*time.Minute {
					continue
				}
				convStart = extractStartTime(logPath, convStart)
				if time.Since(convStart) >= 15*time.Minute {
					continue
				}
				cwd := extractWorkspace(logPath, home)
				if cwd == "" {
					continue
				}
				sp := p.findSpawningCandidate(home, cwd, in.ExistingSessions)
				if sp != nil && sp.NativeID != convID && !adoptedOld[sp.ID] && convStart.After(sp.StartedAt.Add(-5*time.Second)) {
					title := p.ResolveSessionTitle(home, "", convID)
					if title == "" {
						title = fmt.Sprintf("Antigravity (%s)", convID[:8])
					}
					if sp.CustomTitle != "" {
						title = sp.CustomTitle
					}
					adoptedCwd := sp.Cwd
					if adoptedCwd == "" {
						adoptedCwd = cwd
					}
					res.AdoptUpdates = append(res.AdoptUpdates, agent.DiskSessionAdoption{
						OldSessionID: sp.ID, NewNativeID: convID, Title: title, Cwd: adoptedCwd, UpdatedAt: modTime,
					})
					adoptedOld[sp.ID] = true
				}
				continue
			}

			var updCwd, updTitle string
			if existing.Cwd == "" {
				updCwd = extractWorkspace(logPath, home)
			}
			effCwd := existing.Cwd
			if effCwd == "" {
				effCwd = updCwd
			}
			if existing.CustomTitle == "" {
				if agentutil.IsRawSessionName(existing.Name) {
					if title := p.ResolveSessionTitle(home, effCwd, convID); title != "" && !agentutil.IsRawSessionName(title) {
						updTitle = title
					}
				} else if anno := ReadAnnotationTitle(home, convID); anno != "" && anno != existing.Name {
					updTitle = anno
				}
			}
			needsNodePath := existing.NodePath == "" && !existing.IsSupervisor && effCwd != ""
			if updCwd != "" || updTitle != "" || needsNodePath {
				res.MetadataUpdates = append(res.MetadataUpdates, agent.DiskSessionMetadataUpdate{
					SessionID: existing.ID, Cwd: updCwd, Title: updTitle,
				})
			}
		}
	}

	for _, sObj := range in.ExistingSessions {
		if sObj == nil || sObj.Deleted || sObj.Agent != "antigravity" || sObj.CustomTitle != "" || !agentutil.IsUUID(sObj.NativeID) {
			continue
		}
		if in.HostName != "" && sObj.Host != "" && !strings.EqualFold(sObj.Host, in.HostName) && !strings.EqualFold(sObj.Host, "local") && !strings.EqualFold(in.HostName, "local") {
			continue
		}
		if p.IsSubagentSession(home, sObj.NativeID, subagentIDs) && !pruned[sObj.ID] {
			res.PruneSessionIDs = append(res.PruneSessionIDs, sObj.ID)
			pruned[sObj.ID] = true
		}
	}
	return res
}
