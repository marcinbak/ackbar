package client

import (
	"testing"
	"time"

	"ackbar/internal/daemon"
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
