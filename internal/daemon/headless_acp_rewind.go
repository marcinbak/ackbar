package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"ackbar/internal/agent"
)

func parseACPRewindPoints(raw json.RawMessage) []agent.ACPRewindPoint {
	if len(raw) == 0 {
		return nil
	}
	var items []any
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil && obj != nil {
		for _, k := range []string{"points", "checkpoints", "turns"} {
			if arr, ok := obj[k].([]any); ok {
				items = arr
				break
			}
		}
	} else {
		_ = json.Unmarshal(raw, &items)
	}
	var out []agent.ACPRewindPoint
	for i, rawItem := range items {
		m, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(m, "id", "pointId", "checkpointId", "targetId")
		if id == "" {
			continue
		}
		turn := i + 1
		if tNum, ok := m["turn"].(float64); ok && tNum > 0 {
			turn = int(tNum)
		} else if tNum, ok := m["turnNumber"].(float64); ok && tNum > 0 {
			turn = int(tNum)
		}
		preview := stringField(m, "preview", "prompt", "title", "summary")
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		ts := parseACPTime(m["timestamp"])
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		out = append(out, agent.ACPRewindPoint{ID: id, Turn: turn, Preview: preview, Timestamp: ts})
	}
	return out
}

func parseACPTime(val any) time.Time {
	switch v := val.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	case float64:
		if v > 1e11 {
			return time.UnixMilli(int64(v))
		}
		if v > 0 {
			return time.Unix(int64(v), 0)
		}
	}
	return time.Time{}
}

func (s *Server) tryACPRewindGet(w http.ResponseWriter, sess *Session) bool {
	if s.headless == nil || !isACPHeadlessAgent(s.PluginRegistry(), sess.Agent) || !s.headless.HasWarmWorker(sess.ID) {
		return false
	}
	points, ok := s.headless.ListACPRewindPoints(sess)
	if !ok || len(points) == 0 {
		return false
	}
	turns := make([]rewindHumanTurn, 0, len(points))
	for _, pt := range points {
		turns = append(turns, rewindHumanTurn{
			ID:        pt.ID,
			Turn:      pt.Turn,
			Timestamp: pt.Timestamp,
			Preview:   pt.Preview,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session_id":  sess.ID,
		"agent":       sess.Agent,
		"acp_native":  true,
		"total_turns": len(turns),
		"turns":       turns,
	})
	return true
}

func (s *Server) tryACPRewindPost(w http.ResponseWriter, r *http.Request, sess *Session, bodyBytes []byte) bool {
	var req struct {
		BeforeTurn int    `json:"before_turn"`
		TargetID   string `json:"target_id"`
		Cancel     bool   `json:"cancel"`
	}
	if json.Unmarshal(bodyBytes, &req) != nil {
		return false
	}
	if req.Cancel {
		if s.headless != nil {
			_ = s.headless.CancelACPRewind(sess)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     "ok",
			"mode":       "cancel_rewind",
			"session_id": sess.ID,
		})
		return true
	}
	if s.headless == nil || !isACPHeadlessAgent(s.PluginRegistry(), sess.Agent) || !s.headless.HasWarmWorker(sess.ID) {
		return false
	}
	targetID := strings.TrimSpace(req.TargetID)
	if targetID == "" && req.BeforeTurn >= 1 {
		if points, ok := s.headless.ListACPRewindPoints(sess); ok && len(points) > 0 {
			for _, pt := range points {
				if pt.Turn == req.BeforeTurn {
					targetID = pt.ID
					break
				}
			}
			if targetID == "" && req.BeforeTurn-1 < len(points) {
				targetID = points[req.BeforeTurn-1].ID
			}
		}
	}
	if targetID == "" {
		return false
	}
	execRes, handled, err := s.headless.ExecuteACPRewind(r.Context(), sess, targetID)
	if !handled {
		return false
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "ok",
		"mode":        "acp_native",
		"before_turn": req.BeforeTurn,
		"target_id":   targetID,
		"session":     sess,
		"result":      execRes,
	})
	return true
}
