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
