package client

import (
	"strings"
	"testing"
	"time"

	"ackbar/internal/daemon"
	tea "github.com/charmbracelet/bubbletea"
)

func TestBuildVisibleRows_SortsSessionsByInteractionRecency(t *testing.T) {
	now := time.Now()
	older := now.Add(-10 * time.Minute)
	oldest := now.Add(-1 * time.Hour)

	m := &Model{
		sessions: []*daemon.Session{
			{
				ID:          "sess-1",
				Name:        "Oldest Session",
				Host:        "local",
				NodePath:    "Projects/Backend",
				StartedAt:   oldest,
				LastEventAt: oldest,
			},
			{
				ID:          "sess-2",
				Name:        "Newest Interaction Session",
				Host:        "legion",
				NodePath:    "Projects/Backend",
				StartedAt:   oldest,
				LastEventAt: now,
			},
			{
				ID:          "sess-3",
				Name:        "Medium Session",
				Host:        "local",
				NodePath:    "Projects/Backend",
				StartedAt:   older,
				LastEventAt: older,
			},
		},
		collapsed: make(map[string]bool),
	}

	rows := m.buildVisibleRows()

	var sessionOrder []string
	for _, r := range rows {
		if !r.IsGroup && r.Session != nil {
			sessionOrder = append(sessionOrder, r.Session.ID)
		}
	}

	if len(sessionOrder) != 3 {
		t.Fatalf("expected 3 sessions in tree, got %d", len(sessionOrder))
	}

	if sessionOrder[0] != "sess-2" || sessionOrder[1] != "sess-3" || sessionOrder[2] != "sess-1" {
		t.Errorf("expected session order [sess-2, sess-3, sess-1], got %v", sessionOrder)
	}
}

func TestNavigation_ClearsUnreadStateOnMove(t *testing.T) {
	sess1 := &daemon.Session{
		ID:       "sess-1",
		Name:     "Session 1",
		Host:     "local",
		NodePath: "Projects/Backend",
		IsUnread: true,
	}
	sess2 := &daemon.Session{
		ID:       "sess-2",
		Name:     "Session 2",
		Host:     "local",
		NodePath: "Projects/Backend",
		IsUnread: false,
	}

	m := &Model{
		sessions:    []*daemon.Session{sess1, sess2},
		collapsed:   make(map[string]bool),
		selectedIdx: 1, // cursor on sess1 (row 0 is group, row 1 is sess1, row 2 is sess2)
	}

	rows := m.buildVisibleRows()
	sess1Idx := -1
	for idx, r := range rows {
		if !r.IsGroup && r.Session != nil && r.Session.ID == "sess-1" {
			sess1Idx = idx
			break
		}
	}
	if sess1Idx == -1 {
		t.Fatalf("sess-1 not found in visible rows")
	}

	m.selectedIdx = sess1Idx

	// Move cursor down
	msg := tea.KeyMsg{Type: tea.KeyDown}
	newModel, cmd := m.Update(msg)
	m = newModel.(*Model)

	if m.selectedIdx != sess1Idx+1 {
		t.Errorf("expected selectedIdx to be %d, got %d", sess1Idx+1, m.selectedIdx)
	}

	if sess1.IsUnread {
		t.Errorf("expected sess1.IsUnread to be false after moving cursor away, got true")
	}

	if cmd == nil {
		t.Errorf("expected tea.Cmd returned to sync read status to daemon, got nil")
	}
}

func TestBuildVisibleRows_PreservesMultipleSessionsInSameCwdWithDifferentArchiveState(t *testing.T) {
	sessActive := &daemon.Session{
		ID:       "claude-code:local:uuid-active-1",
		NativeID: "uuid-active-1",
		Name:     "Active Turn",
		Host:     "local",
		Cwd:      "/workspace/mobile/app",
		NodePath: "Mobile",
		Archived: false,
		State:    daemon.StateIdle,
	}
	sessArchived := &daemon.Session{
		ID:       "claude-code:local:uuid-archived-1",
		NativeID: "uuid-archived-1",
		Name:     "Archived Turn",
		Host:     "local",
		Cwd:      "/workspace/mobile/app", // Same CWD as active session!
		NodePath: "Mobile",
		Archived: true,
		State:    daemon.StateEnded,
	}

	// 1. In standard active view (archivedView = false): only sessActive should be visible
	m := &Model{
		sessions:     []*daemon.Session{sessActive, sessArchived},
		collapsed:    make(map[string]bool),
		archivedView: false,
	}

	rowsActive := m.buildVisibleRows()
	var visibleActiveSessions []string
	for _, r := range rowsActive {
		if !r.IsGroup && r.Session != nil {
			visibleActiveSessions = append(visibleActiveSessions, r.Session.ID)
		}
	}

	if len(visibleActiveSessions) != 1 || visibleActiveSessions[0] != "claude-code:local:uuid-active-1" {
		t.Errorf("expected only active session visible, got %v", visibleActiveSessions)
	}

	// 2. In archived view (archivedView = true): only sessArchived should be visible
	m.archivedView = true
	rowsArchived := m.buildVisibleRows()
	var visibleArchivedSessions []string
	for _, r := range rowsArchived {
		if !r.IsGroup && r.Session != nil {
			visibleArchivedSessions = append(visibleArchivedSessions, r.Session.ID)
		}
	}

	if len(visibleArchivedSessions) != 1 || visibleArchivedSessions[0] != "claude-code:local:uuid-archived-1" {
		t.Errorf("expected only archived session visible in archive view, got %v", visibleArchivedSessions)
	}
}

func TestBuildVisibleRows_AssignsSessionByNodePathLeafFallback(t *testing.T) {
	sess := &daemon.Session{
		ID:       "antigravity:local:test-uuid-1",
		NativeID: "test-uuid-1",
		Name:     "Ackbar Session",
		Host:     "local",
		Cwd:      "/Users/dev4u/Work/Ackbar",
		NodePath: "", // Missing NodePath!
		State:    daemon.StateEnded,
	}

	m := &Model{
		sessions: []*daemon.Session{sess},
		treeNodes: []*daemon.TreeNode{
			{Path: "Personal/Ackbar", ProjectDir: ""}, // No project_dir!
		},
		collapsed: make(map[string]bool),
	}

	rows := m.buildVisibleRows()

	foundUnderPersonalAckbar := false
	inUnassigned := false

	currentGroup := ""
	for _, r := range rows {
		if r.IsGroup {
			currentGroup = r.GroupPath
			if currentGroup == "Unassigned" {
				inUnassigned = true
			}
		} else if r.Session != nil && r.Session.ID == "antigravity:local:test-uuid-1" {
			if currentGroup == "Personal/Ackbar" {
				foundUnderPersonalAckbar = true
			}
		}
	}

	if inUnassigned {
		t.Errorf("expected session not to be in Unassigned")
	}
	if !foundUnderPersonalAckbar {
		t.Errorf("expected session to be assigned to Personal/Ackbar by leaf fallback, but was not")
	}
}

func TestRenderHostAgentDiscovery_CompactBadges(t *testing.T) {
	// 1. Empty list
	emptyOut := renderHostAgentDiscovery(nil)
	if !strings.Contains(emptyOut, "Querying host agents...") {
		t.Errorf("expected querying message for empty list, got %q", emptyOut)
	}

	// 2. Mixed list
	discList := []daemon.AgentDiscoveryResult{
		{Agent: "claude-code", DisplayName: "Claude Code", Installed: true, HookConfigured: true},
		{Agent: "antigravity", DisplayName: "Antigravity", Installed: true, HookConfigured: true},
		{Agent: "grok", DisplayName: "Grok", Installed: true, HookConfigured: false, SetupCmd: "ackbar-hook --agent=grok"},
		{Agent: "codex", DisplayName: "Codex", Installed: false},
		{Agent: "opencode", DisplayName: "OpenCode", Installed: false},
	}

	out := renderHostAgentDiscovery(discList)

	// Check installed badges
	if !strings.Contains(out, "Installed: ") {
		t.Errorf("expected 'Installed: ' line, got %q", out)
	}
	if !strings.Contains(out, "Claude Code 🟢") {
		t.Errorf("expected Claude Code 🟢 badge, got %q", out)
	}
	if !strings.Contains(out, "Grok 🟡") {
		t.Errorf("expected Grok 🟡 badge, got %q", out)
	}

	// Check missing hook warning
	if !strings.Contains(out, "⚠️ Hook Missing: grok (ackbar-hook --agent=grok)") {
		t.Errorf("expected hook missing warning, got %q", out)
	}

	// Check not installed line
	if !strings.Contains(out, "Not Installed: codex, opencode") {
		t.Errorf("expected not installed list, got %q", out)
	}

	// 3. No installed agents
	noneList := []daemon.AgentDiscoveryResult{
		{Agent: "codex", Installed: false},
		{Agent: "grok", Installed: false},
	}
	noneOut := renderHostAgentDiscovery(noneList)
	if !strings.Contains(noneOut, "Installed: (none detected)") {
		t.Errorf("expected (none detected) for no installed agents, got %q", noneOut)
	}
	if !strings.Contains(noneOut, "Not Installed: codex, grok") {
		t.Errorf("expected not installed line, got %q", noneOut)
	}

	// 4. All installed and active
	allList := []daemon.AgentDiscoveryResult{
		{Agent: "claude-code", DisplayName: "Claude Code", Installed: true, HookConfigured: true},
		{Agent: "antigravity", DisplayName: "Antigravity", Installed: true, HookConfigured: true},
	}
	allOut := renderHostAgentDiscovery(allList)
	if strings.Contains(allOut, "Hook Missing") {
		t.Errorf("did not expect Hook Missing when all hooks are configured, got %q", allOut)
	}
	if strings.Contains(allOut, "Not Installed") {
		t.Errorf("did not expect Not Installed when all are installed, got %q", allOut)
	}
}
