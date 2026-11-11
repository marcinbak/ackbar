package daemon

import (
	"encoding/json"
	"strings"
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
	BlockApproval   BlockKind = "approval"
	BlockRateLimit  BlockKind = "rate_limit"
	BlockAuth       BlockKind = "auth"
)

type BlockedQuestion struct {
	Question    string   `json:"question"`
	Header      string   `json:"header,omitempty"`
	Options     []string `json:"options,omitempty"`
	MultiSelect bool     `json:"multi_select,omitempty"`
}

type Blocked struct {
	Kind       BlockKind         `json:"kind"`
	Reason     string            `json:"reason"`
	Since      time.Time         `json:"since"`
	Question   string            `json:"question,omitempty"`
	Options    []string          `json:"options,omitempty"`
	Questions  []BlockedQuestion `json:"questions,omitempty"`
	ResetAt    *time.Time        `json:"reset_at,omitempty"`
	AuthTarget string            `json:"auth_target,omitempty"`
	AuthKind   string            `json:"auth_kind,omitempty"`
	AuthServer string            `json:"auth_server,omitempty"`
	AuthURL    string            `json:"auth_url,omitempty"`
	AuthHint   string            `json:"auth_hint,omitempty"`
	FlowID     string            `json:"flow_id,omitempty"`
}

type blockedMetaJSON struct {
	AuthTarget string `json:"auth_target,omitempty"`
	AuthKind   string `json:"auth_kind,omitempty"`
	AuthServer string `json:"auth_server,omitempty"`
	AuthURL    string `json:"auth_url,omitempty"`
	AuthHint   string `json:"auth_hint,omitempty"`
	FlowID     string `json:"flow_id,omitempty"`
}

func MarshalBlockedMeta(b *Blocked) string {
	if b == nil {
		return ""
	}
	if b.AuthTarget == "" && b.AuthKind == "" && b.AuthServer == "" && b.AuthURL == "" && b.AuthHint == "" && b.FlowID == "" {
		return ""
	}
	meta := blockedMetaJSON{
		AuthTarget: b.AuthTarget,
		AuthKind:   b.AuthKind,
		AuthServer: b.AuthServer,
		AuthURL:    b.AuthURL,
		AuthHint:   b.AuthHint,
		FlowID:     b.FlowID,
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(raw)
}

func ApplyBlockedMeta(b *Blocked, raw string) {
	if b == nil || strings.TrimSpace(raw) == "" {
		return
	}
	var meta blockedMetaJSON
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return
	}
	b.AuthTarget = meta.AuthTarget
	b.AuthKind = meta.AuthKind
	b.AuthServer = meta.AuthServer
	b.AuthURL = meta.AuthURL
	b.AuthHint = meta.AuthHint
	b.FlowID = meta.FlowID
}

type Session struct {
	ID                string    `json:"id"`          // "{agent}:{host}:{nativeID}"
	Agent             string    `json:"agent"`       // "claude-code" | "codex" | "antigravity"
	Host              string    `json:"host"`        // machine alias, e.g. "devbox"
	NativeID          string    `json:"native_id"`   // agent's own session/conversation id
	Cwd               string    `json:"cwd"`         // primary working directory
	Roots             []string  `json:"roots"`       // array of workspace paths
	ProjectKey        string    `json:"project_key"` // normalized git remote or fallback
	State             State     `json:"state"`       // enum representing agent state
	Blocked           *Blocked  `json:"blocked,omitempty"`
	Activity          string    `json:"activity"` // current task description
	StartedAt         time.Time `json:"started_at"`
	LastEventAt       time.Time `json:"last_event_at"`
	Managed           bool      `json:"managed"`   // daemon-spawned
	TmuxName          string    `json:"tmux_name"` // name of tmux session if managed
	PID               int       `json:"pid"`
	Archived          bool      `json:"archived"`
	NodePath          string    `json:"node_path"`             // optional custom tree node path
	Name              string    `json:"name,omitempty"`        // optional human readable session title/name
	Entrypoint        string    `json:"entrypoint,omitempty"`  // "claude-vscode", "terminal", "claude-cli"
	Kind              string    `json:"kind,omitempty"`        // "interactive", "batch"
	Version           string    `json:"version,omitempty"`     // agent version
	ContextPct        int       `json:"context_pct,omitempty"` // percentage of used context window (0-100)
	GitBranch         string    `json:"git_branch,omitempty"`  // active git branch or worktree name
	Deleted           bool      `json:"deleted,omitempty"`
	CustomTitle       string    `json:"custom_title,omitempty"`
	AITitle           string    `json:"ai_title,omitempty"`
	AIDescription     string    `json:"ai_description,omitempty"`
	FirstPrompt       string    `json:"first_prompt,omitempty"`
	LastPrompt        string    `json:"last_prompt,omitempty"`
	IsUnread          bool      `json:"is_unread,omitempty"`
	IsDone            bool      `json:"is_done,omitempty"`
	IsLater           bool      `json:"is_later,omitempty"`
	IsSupervisor      bool      `json:"is_supervisor,omitempty"`
	CurrentTaskID     string    `json:"current_task_id,omitempty"`
	LastStateChangeAt time.Time `json:"last_state_change_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
	AccountID         string    `json:"account_id,omitempty"`  // profile/account identifier (e.g. "work", "personal", "default")
	EngineType        string    `json:"engine_type,omitempty"` // "tmux" (default) or "headless"
	Model             string    `json:"model,omitempty"`
	Effort            string    `json:"effort,omitempty"`
	PermissionMode    string    `json:"permission_mode,omitempty"`
	RunningSubagents  int       `json:"running_subagents,omitempty"`
	HostOffline       bool      `json:"host_offline,omitempty"`
	MetadataUpdatedAt time.Time `json:"metadata_updated_at,omitempty"`
	OldID             string    `json:"old_id,omitempty"`      // populated during identity migration (e.g. placeholder tempUUID -> real native UUID)
	MigratedTo        string    `json:"migrated_to,omitempty"` // populated if identity was migrated to another session ID
}

const (
	EngineTmux     = "tmux"
	EngineHeadless = "headless"
)

type AgentAccount struct {
	ID          string            `json:"id"`                     // Unique key, e.g. "claude-code:work"
	Agent       string            `json:"agent"`                  // "claude-code" | "antigravity" | "codex"
	Name        string            `json:"name"`                   // "work", "personal", "default"
	DisplayName string            `json:"display_name"`           // "Anthropic Team (Work)"
	ConfigDir   string            `json:"config_dir,omitempty"`   // custom config dir e.g. "~/.claude-profiles/work"
	Env         map[string]string `json:"env,omitempty"`          // custom env vars (ANTHROPIC_API_KEY, etc.)
	IsDefault   bool              `json:"is_default"`             // default account for this agent
	IsLoggedIn  bool              `json:"is_logged_in,omitempty"` // computed credential check
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

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

func parseInterfaceSlice(raw interface{}) []interface{} {
	switch v := raw.(type) {
	case []interface{}:
		return v
	case []string:
		out := make([]interface{}, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out
	case []map[string]interface{}:
		out := make([]interface{}, len(v))
		for i, m := range v {
			out[i] = m
		}
		return out
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			var parsed []interface{}
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				return parsed
			}
		}
	}
	return nil
}

func extractOptionStrings(raw interface{}) []string {
	var optionsList []string
	for _, opt := range parseInterfaceSlice(raw) {
		if s, ok := opt.(string); ok && s != "" {
			optionsList = append(optionsList, s)
		} else if optMap, ok := opt.(map[string]interface{}); ok {
			if label, ok := optMap["label"].(string); ok && label != "" {
				optionsList = append(optionsList, label)
			} else if text, ok := optMap["text"].(string); ok && text != "" {
				optionsList = append(optionsList, text)
			} else if title, ok := optMap["title"].(string); ok && title != "" {
				optionsList = append(optionsList, title)
			}
		}
	}
	return optionsList
}

// ExtractQuestionAndOptions extracts question and option strings from tool input args
func ExtractQuestionAndOptions(toolName string, args map[string]interface{}) (string, []string) {
	return ExtractAntigravityQuestionAndOptions(args)
}

// ExtractAntigravityQuestionAndOptions extracts question and option strings from Antigravity tool args
func ExtractAntigravityQuestionAndOptions(args map[string]interface{}) (string, []string) {
	if args == nil {
		return "", nil
	}

	var questionText string
	var optionsList []string

	// 1. Array of questions (standard ask_question schema, or JSON-stringified array)
	if questionsRaw := parseInterfaceSlice(args["questions"]); len(questionsRaw) > 0 {
		for _, qItem := range questionsRaw {
			if qMap, ok := qItem.(map[string]interface{}); ok {
				if questionText == "" {
					if qStr, ok := qMap["question"].(string); ok && qStr != "" {
						questionText = qStr
					} else if pStr, ok := qMap["prompt"].(string); ok && pStr != "" {
						questionText = pStr
					} else if tStr, ok := qMap["text"].(string); ok && tStr != "" {
						questionText = tStr
					}
				}
				if opts := extractOptionStrings(qMap["options"]); len(opts) > 0 {
					optionsList = append(optionsList, opts...)
				}
			}
		}
	}

	// 2. Direct question / prompt field
	if questionText == "" {
		if qStr, ok := args["question"].(string); ok {
			questionText = qStr
		} else if promptStr, ok := args["prompt"].(string); ok {
			questionText = promptStr
		}
	}

	// 3. Direct options field
	if len(optionsList) == 0 {
		optionsList = extractOptionStrings(args["options"])
	}

	return questionText, optionsList
}

// ExtractAntigravityQuestions extracts all BlockedQuestion items from tool input args
func ExtractAntigravityQuestions(args map[string]interface{}) []BlockedQuestion {
	if args == nil {
		return nil
	}

	var questions []BlockedQuestion
	if questionsRaw := parseInterfaceSlice(args["questions"]); len(questionsRaw) > 0 {
		for _, qItem := range questionsRaw {
			if qMap, ok := qItem.(map[string]interface{}); ok {
				var bq BlockedQuestion
				if qStr, ok := qMap["question"].(string); ok {
					bq.Question = qStr
				} else if pStr, ok := qMap["prompt"].(string); ok {
					bq.Question = pStr
				} else if tStr, ok := qMap["text"].(string); ok {
					bq.Question = tStr
				}
				if hStr, ok := qMap["header"].(string); ok {
					bq.Header = hStr
				}
				if ms, ok := qMap["multiSelect"].(bool); ok {
					bq.MultiSelect = ms
				} else if ms, ok := qMap["multi_select"].(bool); ok {
					bq.MultiSelect = ms
				} else if ms, ok := qMap["is_multi_select"].(bool); ok {
					bq.MultiSelect = ms
				}
				bq.Options = extractOptionStrings(qMap["options"])
				if bq.Question != "" || len(bq.Options) > 0 {
					questions = append(questions, bq)
				}
			}
		}
	}

	if len(questions) == 0 {
		var bq BlockedQuestion
		if qStr, ok := args["question"].(string); ok {
			bq.Question = qStr
		} else if promptStr, ok := args["prompt"].(string); ok {
			bq.Question = promptStr
		}
		if hStr, ok := args["header"].(string); ok {
			bq.Header = hStr
		}
		if ms, ok := args["multiSelect"].(bool); ok {
			bq.MultiSelect = ms
		} else if ms, ok := args["multi_select"].(bool); ok {
			bq.MultiSelect = ms
		} else if ms, ok := args["is_multi_select"].(bool); ok {
			bq.MultiSelect = ms
		}
		bq.Options = extractOptionStrings(args["options"])
		if bq.Question != "" || len(bq.Options) > 0 {
			questions = append(questions, bq)
		}
	}

	return questions
}
