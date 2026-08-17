package daemon

import (
	"time"
)

type State int

const (
	StateUnknown State = iota
	StateWorking
	StateBlocked
	StateIdle
	StateEnded
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateWorking:
		return "working"
	case StateBlocked:
		return "blocked"
	case StateIdle:
		return "idle"
	case StateEnded:
		return "ended"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type BlockKind string

const (
	BlockPermission BlockKind = "permission"
	BlockQuestion   BlockKind = "question"
)

type Blocked struct {
	Kind   BlockKind `json:"kind"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
}

type Session struct {
	ID          string    `json:"id"`           // "{agent}:{host}:{nativeID}"
	Agent       string    `json:"agent"`         // "claude-code" | "codex" | "antigravity"
	Host        string    `json:"host"`          // machine alias, e.g. "devbox"
	NativeID    string    `json:"native_id"`     // agent's own session/conversation id
	Cwd         string    `json:"cwd"`           // primary working directory
	Roots       []string  `json:"roots"`         // array of workspace paths
	ProjectKey  string    `json:"project_key"`   // normalized git remote or fallback
	State       State     `json:"state"`         // enum representing agent state
	Blocked     *Blocked  `json:"blocked,omitempty"`
	Activity    string    `json:"activity"`      // current task description
	StartedAt   time.Time `json:"started_at"`
	LastEventAt time.Time `json:"last_event_at"`
	Managed     bool      `json:"managed"`       // daemon-spawned
	TmuxName    string    `json:"tmux_name"`     // name of tmux session if managed
	PID         int       `json:"pid"`
	Archived    bool      `json:"archived"`
	NodePath    string    `json:"node_path"`     // optional custom tree node path
	Name        string    `json:"name,omitempty"` // optional human readable session title/name
	Entrypoint  string    `json:"entrypoint,omitempty"` // "claude-vscode", "terminal", "claude-cli"
	Kind        string    `json:"kind,omitempty"`       // "interactive", "batch"
	Version     string    `json:"version,omitempty"`    // agent version
	ContextPct  int       `json:"context_pct,omitempty"` // percentage of used context window (0-100)
	GitBranch   string    `json:"git_branch,omitempty"`  // active git branch or worktree name
	Deleted     bool      `json:"deleted,omitempty"`
	CustomTitle string    `json:"custom_title,omitempty"`
	AITitle     string    `json:"ai_title,omitempty"`
	AIDescription string  `json:"ai_description,omitempty"`
	FirstPrompt string    `json:"first_prompt,omitempty"`
	LastPrompt  string    `json:"last_prompt,omitempty"`
}

type TreeNode struct {
	Path       string    `json:"path"`        // e.g. "Work/ProjectY/ProjectY-web/analytics-integration"
	ProjectDir string    `json:"project_dir"` // optional local/remote working directory
	GitURL     string    `json:"git_url"`     // optional git remote URL
	CreatedAt  time.Time `json:"created_at"`
}

type HostRecord struct {
	Name      string    `json:"name"`       // machine alias, e.g. "devbox"
	URL       string    `json:"url"`        // daemon HTTP endpoint, e.g. "http://127.0.0.1:7778"
	SSHTarget string    `json:"ssh_target"` // optional SSH alias, e.g. "dev@remote-box"
	RemoteCwd string    `json:"remote_cwd"` // default workspace root on remote
	CreatedAt time.Time `json:"created_at"`
}
