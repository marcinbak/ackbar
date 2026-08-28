package client

import (
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
