package agent

import "time"

type PersistentTurnInput struct {
	Agent        string
	SessionID    string
	NativeID     string
	Prompt       string
	SystemPrompt string
	IsSupervisor bool
	IsFirstTurn  bool
}

type RewindTurn struct {
	ID        string    `json:"id,omitempty"`
	Turn      int       `json:"turn"`
	Timestamp time.Time `json:"timestamp"`
	Preview   string    `json:"preview"`
	RawPrompt string    `json:"-"`
}
