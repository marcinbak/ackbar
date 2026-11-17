package daemon

import (
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type State = agent.State

const (
	StateUnknown = agent.StateUnknown
	StateWorking = agent.StateWorking
	StateBlocked = agent.StateBlocked
	StateIdle    = agent.StateIdle
	StateEnded   = agent.StateEnded
	StateFailed  = agent.StateFailed
)

type BlockKind = agent.BlockKind

const (
	BlockPermission = agent.BlockPermission
	BlockQuestion   = agent.BlockQuestion
	BlockApproval   = agent.BlockApproval
	BlockRateLimit  = agent.BlockRateLimit
	BlockAuth       = agent.BlockAuth
)

type BlockedQuestion = agent.BlockedQuestion
type Blocked = agent.Blocked

func MarshalBlockedMeta(b *Blocked) string {
	return agent.MarshalBlockedMeta(b)
}

func ApplyBlockedMeta(b *Blocked, raw string) {
	agent.ApplyBlockedMeta(b, raw)
}

type Session = agent.Session

const (
	EngineTmux     = agent.EngineTmux
	EngineHeadless = agent.EngineHeadless
)

type AgentAccount = agent.AgentAccount

type NodeHostPath struct {
	NodePath     string    `json:"node_path"`
	Host         string    `json:"host"`
	ProjectDir   string    `json:"project_dir"`
	GitRemote    string    `json:"git_remote,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TreeNode struct {
	Path       string         `json:"path"`        // e.g. "Work/ProjectY/ProjectY-web/analytics-integration"
	ProjectDir string         `json:"project_dir"` // optional local/remote working directory
	GitURL     string         `json:"git_url"`     // optional git remote URL
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at,omitempty"`
	DeletedAt  *time.Time     `json:"deleted_at,omitempty"`
	HostPaths  []NodeHostPath `json:"host_paths,omitempty"`
}

type HostLoadSnapshot struct {
	WorkingSessions   int     `json:"working_sessions"`
	QueuedWorkstreams int     `json:"queued_workstreams"`
	IdleSessions      int     `json:"idle_sessions"`
	CPULoadPct        float64 `json:"cpu_load_pct"`
	RAMFreeGB         float64 `json:"ram_free_gb"`
	RAMTotalGB        float64 `json:"ram_total_gb"`
	DiskFreeGB        float64 `json:"disk_free_gb"`
	Overloaded        bool    `json:"overloaded"`
}

type HostLoadMetric struct {
	Host               string    `json:"host"`
	BucketStart        time.Time `json:"bucket_start"`
	BucketSec          int       `json:"bucket_sec"`
	AvgWorkingSessions float64   `json:"avg_working_sessions"`
	MaxWorkingSessions int       `json:"max_working_sessions"`
	AvgCPUPct          float64   `json:"avg_cpu_pct"`
	MaxCPUPct          float64   `json:"max_cpu_pct"`
	MinRAMFreeGB       float64   `json:"min_ram_free_gb"`
	TasksSpawnedCount  int       `json:"tasks_spawned_count"`
}

type HostRecord struct {
	Name                 string            `json:"name"`       // machine alias, e.g. "devbox"
	URL                  string            `json:"url"`        // daemon HTTP endpoint, e.g. "http://127.0.0.1:7778"
	SSHTarget            string            `json:"ssh_target"` // optional SSH alias, e.g. "dev@remote-box"
	RemoteCwd            string            `json:"remote_cwd"` // default workspace root on remote
	PolicyTags           []string          `json:"policy_tags,omitempty"`
	RoutingWeight        int               `json:"routing_weight"`
	HasExplicitWeight    bool              `json:"-"`
	DetectedCapabilities []string          `json:"detected_capabilities,omitempty"`
	UpdatedAt            time.Time         `json:"updated_at,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	Online               bool              `json:"online,omitempty"`       // live health reported by daemon
	Version              string            `json:"version,omitempty"`      // remote daemon version
	DisplayName          string            `json:"display_name,omitempty"` // remote daemon display name
	LatencyMs            int64             `json:"latency_ms,omitempty"`   // remote ping latency in ms
	Load                 *HostLoadSnapshot `json:"load,omitempty"`
	IsLocal              bool              `json:"is_local,omitempty"`
}

func ExtractQuestionAndOptions(toolName string, args map[string]interface{}) (string, []string) {
	return agentutil.ExtractQuestionAndOptions(args)
}

func ExtractAntigravityQuestionAndOptions(args map[string]interface{}) (string, []string) {
	return agentutil.ExtractQuestionAndOptions(args)
}

func ExtractAntigravityQuestions(args map[string]interface{}) []BlockedQuestion {
	return agentutil.ExtractQuestions(args)
}
