package provider

import (
	"ackbar/internal/daemon"
)

var _ daemon.FullProvider = (*GrokProvider)(nil)

func (g *GrokProvider) ListSubagents(home, cwd, nativeID string) ([]*daemon.ActiveSubagent, error) {
	subs, err := daemon.LoadGrokSubagents(home, cwd, nativeID)
	if err != nil {
		return nil, err
	}
	var res []*daemon.ActiveSubagent
	for _, s := range subs {
		res = append(res, &daemon.ActiveSubagent{
			ID:        s.ID,
			Name:      s.Name,
			Role:      s.Role,
			Prompt:    s.Prompt,
			State:     s.State,
			StartedAt: s.StartedAt,
		})
	}
	return res, nil
}
