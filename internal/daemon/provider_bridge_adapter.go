package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (b *PluginBridge) GetResetCommand() string {
	if td, ok := b.tmuxDriver(); ok {
		return td.ResetCommand()
	}
	return b.fallback().GetResetCommand()
}

func (b *PluginBridge) GetApprovalKeys(allow bool) []string {
	if td, ok := b.tmuxDriver(); ok {
		return td.ApprovalKeys(allow)
	}
	return b.fallback().GetApprovalKeys(allow)
}

func (b *PluginBridge) AccountEnvVars(home string, acc *AgentAccount) map[string]string {
	if ac, ok := b.accounts(); ok {
		return ac.AccountEnvVars(home, acc)
	}
	return b.fallback().AccountEnvVars(home, acc)
}

func (b *PluginBridge) IsAccountLoggedIn(home string, acc *AgentAccount) bool {
	if ac, ok := b.accounts(); ok {
		return ac.IsAccountLoggedIn(home, acc)
	}
	return b.fallback().IsAccountLoggedIn(home, acc)
}

func (b *PluginBridge) SetupProfileDir(home, name, configDir string) (string, error) {
	if ac, ok := b.accounts(); ok {
		return ac.SetupProfileDir(home, name, configDir)
	}
	return b.fallback().SetupProfileDir(home, name, configDir)
}

func (b *PluginBridge) GetLoginCommand() string {
	if ac, ok := b.accounts(); ok {
		return ac.LoginCommand()
	}
	return b.fallback().GetLoginCommand()
}

func (b *PluginBridge) IsValidNativeID(nativeID string) bool {
	if sc, ok := b.scanner(); ok {
		return sc.IsValidNativeID(nativeID)
	}
	return b.fallback().IsValidNativeID(nativeID)
}

func (b *PluginBridge) IsIgnoredCommand(fullCmd string) bool {
	if sc, ok := b.scanner(); ok {
		return sc.IsIgnoredCommand(fullCmd)
	}
	return false
}

func (b *PluginBridge) MatchProcessCommand(binLower, cmdLower string) bool {
	if sc, ok := b.scanner(); ok {
		return sc.MatchProcessCommand(binLower, cmdLower)
	}
	return false
}

func (b *PluginBridge) RequiresResolvedSessionID() bool {
	if sc, ok := b.scanner(); ok {
		return sc.RequiresResolvedSessionID()
	}
	return false
}

func (b *PluginBridge) ResolveSessionForPID(ctx context.Context, home string, pid int, cmdFields []string) string {
	if sc, ok := b.scanner(); ok {
		return sc.ResolveSessionForPID(ctx, home, pid, cmdFields)
	}
	return ""
}

func (b *PluginBridge) ResolveSessionOwner(home, sessionID string) (int, string) {
	if sc, ok := b.scanner(); ok {
		return sc.ResolveSessionOwner(home, sessionID)
	}
	return 0, ""
}

func (b *PluginBridge) HasConversationArtifacts(home, nativeID string) bool {
	if tr, ok := b.transcripts(); ok {
		return tr.HasConversationArtifacts(home, nativeID)
	}
	return false
}

func (b *PluginBridge) IsSubagentSession(home, nativeID string, precomputed map[string]bool) bool {
	if tr, ok := b.transcripts(); ok {
		return tr.IsSubagentSession(home, nativeID, precomputed)
	}
	return false
}

func (b *PluginBridge) ListWorkspaceDocuments(home, cwd, nativeID string, addDoc func(fullPath, title, relPath, category, categoryLabel string, priority int)) {
	if addDoc == nil {
		return
	}
	if tr, ok := b.transcripts(); ok {
		for _, doc := range tr.ListWorkspaceDocuments(home, cwd, nativeID) {
			addDoc(doc.FullPath, doc.Title, doc.RelPath, doc.Category, doc.CategoryLabel, doc.Priority)
		}
	}
}

func (b *PluginBridge) ScanDiskSessions(ctx context.Context, s *Server, home string, state *ScanState) {
	sc, ok := b.scanner()
	if !ok {
		return
	}
	in := agent.DiskScanInput{HomeDir: home}
	if state != nil {
		in.HostName = state.hostName
		in.ExistingSessions = state.existingSessions
	}
	applyDiskScanResult(s, state, sc.ScanDisk(ctx, in))
}

func (b *PluginBridge) FilterUserEntry(raw map[string]interface{}, content string) (string, bool) {
	if tr, ok := b.transcripts(); ok {
		return tr.FilterUserEntry(raw, content)
	}
	return b.fallback().FilterUserEntry(raw, content)
}

func (b *PluginBridge) DetectRateLimit(text string, now time.Time) (bool, time.Time, string) {
	if rl, ok := b.rateLimiter(); ok {
		return rl.DetectRateLimit(text, now)
	}
	return agentutil.DetectDefaultRateLimitAt(text, now)
}

func applyDiskScanResult(s *Server, state *ScanState, res agent.DiskScanResult) {
	if state == nil {
		return
	}
	for _, sessID := range res.PruneSessionIDs {
		if s != nil && s.db != nil {
			_ = s.db.DeleteSession(sessID)
		}
		if existing, ok := state.knownIDs[sessID]; ok && existing != nil {
			existing.Deleted = true
			existing.State = StateEnded
			existing.Activity = "Deleted"
			if s != nil {
				s.broadcast(existing)
			}
			delete(state.knownIDs, sessID)
			if existing.NativeID != "" {
				delete(state.knownByNativeID, existing.NativeID)
			}
		}
	}
	for _, upd := range res.MetadataUpdates {
		sess := state.knownIDs[upd.SessionID]
		if sess == nil {
			continue
		}
		changed := false
		if upd.Cwd != "" && sess.Cwd != upd.Cwd {
			sess.Cwd = upd.Cwd
			sess.ProjectKey = GetProjectKey(upd.Cwd)
			changed = true
		}
		if upd.Title != "" && sess.CustomTitle == "" && sess.Name != upd.Title {
			sess.Name = upd.Title
			changed = true
		}
		if changed && s != nil {
			if s.db != nil {
				_ = s.db.SaveSession(sess)
			}
			s.broadcast(sess)
		}
	}
	for _, adopt := range res.AdoptUpdates {
		newNativeID := strings.TrimSpace(adopt.NewNativeID)
		if newNativeID == "" {
			continue
		}
		if state.knownIDs == nil {
			state.knownIDs = make(map[string]*Session)
		}
		if state.knownByNativeID == nil {
			state.knownByNativeID = make(map[string]*Session)
		}
		oldSess := state.knownIDs[adopt.OldSessionID]
		if oldSess == nil {
			continue
		}
		newSessID := fmt.Sprintf("%s:%s:%s", oldSess.Agent, state.hostName, newNativeID)
		if existingNew, exists := state.knownIDs[newSessID]; exists && existingNew != oldSess {
			continue
		}
		if s != nil && s.db != nil {
			_ = s.db.DeleteSession(adopt.OldSessionID)
			_ = s.db.ReassignWorkerSessionID(adopt.OldSessionID, newSessID)
		}
		delete(state.knownIDs, adopt.OldSessionID)
		if oldSess.NativeID != "" {
			delete(state.knownByNativeID, oldSess.NativeID)
		}

		oldSess.ID = newSessID
		oldSess.OldID = adopt.OldSessionID
		oldSess.NativeID = newNativeID
		if adopt.Cwd != "" && oldSess.Cwd == "" {
			oldSess.Cwd = adopt.Cwd
			oldSess.ProjectKey = GetProjectKey(adopt.Cwd)
		}
		if adopt.Title != "" && oldSess.CustomTitle == "" {
			oldSess.Name = adopt.Title
		}
		if !adopt.UpdatedAt.IsZero() {
			oldSess.LastEventAt = adopt.UpdatedAt
		}
		state.knownIDs[newSessID] = oldSess
		state.knownByNativeID[newNativeID] = oldSess
		if s != nil {
			if s.db != nil {
				_ = s.db.SaveSession(oldSess)
			}
			s.broadcast(oldSess)
		}
	}
}
