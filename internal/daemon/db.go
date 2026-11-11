package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	db                     *sql.DB
	path                   string
	mu                     sync.Mutex
	interruptedHeadlessIDs []string
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	agent TEXT,
	host TEXT,
	native_id TEXT,
	cwd TEXT,
	roots TEXT,
	project_key TEXT,
	state INTEGER,
	blocked_kind TEXT,
	blocked_reason TEXT,
	blocked_since TIMESTAMP,
	blocked_question TEXT,
	blocked_options TEXT,
	activity TEXT,
	started_at TIMESTAMP,
	last_event_at TIMESTAMP,
	managed INTEGER,
	tmux_name TEXT,
	pid INTEGER,
	archived INTEGER,
	node_path TEXT,
	entrypoint TEXT,
	kind TEXT,
	version TEXT,
	is_done INTEGER DEFAULT 0,
	is_later INTEGER DEFAULT 0,
	is_supervisor INTEGER DEFAULT 0,
	current_task_id TEXT DEFAULT '',
	engine_type TEXT DEFAULT 'tmux',
	model TEXT DEFAULT '',
	effort TEXT DEFAULT '',
	permission_mode TEXT DEFAULT 'bypassPermissions',
	blocked_questions TEXT DEFAULT '',
	blocked_reset_at TIMESTAMP,
	metadata_updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS scheduled_prompts (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	prompt TEXT NOT NULL,
	deliver_at TIMESTAMP,
	created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scheduled_prompts_session ON scheduled_prompts(session_id);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tree_nodes (
	path TEXT PRIMARY KEY,
	project_dir TEXT,
	git_url TEXT,
	created_at TIMESTAMP,
	updated_at TIMESTAMP,
	deleted_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS node_host_paths (
	node_path TEXT NOT NULL,
	host TEXT NOT NULL,
	project_dir TEXT NOT NULL,
	git_remote TEXT,
	capabilities TEXT,
	updated_at TIMESTAMP NOT NULL,
	PRIMARY KEY (node_path, host)
);

CREATE TABLE IF NOT EXISTS hosts (
	name TEXT PRIMARY KEY,
	url TEXT,
	ssh_target TEXT,
	remote_cwd TEXT,
	policy_tags TEXT DEFAULT '[]',
	routing_weight INTEGER DEFAULT -1,
	detected_capabilities TEXT DEFAULT '[]',
	updated_at TIMESTAMP,
	created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS host_load_metrics (
	host TEXT NOT NULL,
	bucket_start TIMESTAMP NOT NULL,
	bucket_sec INTEGER NOT NULL DEFAULT 300,
	avg_working_sessions REAL NOT NULL DEFAULT 0,
	max_working_sessions INTEGER NOT NULL DEFAULT 0,
	avg_cpu_pct REAL NOT NULL DEFAULT 0,
	max_cpu_pct REAL NOT NULL DEFAULT 0,
	min_ram_free_gb REAL NOT NULL DEFAULT 0,
	sample_count INTEGER NOT NULL DEFAULT 1,
	tasks_spawned_count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (host, bucket_start, bucket_sec)
);

CREATE TABLE IF NOT EXISTS deleted_sessions (
	id TEXT PRIMARY KEY,
	deleted_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	group_name TEXT NOT NULL,
	project_name TEXT NOT NULL,
	subproject_name TEXT,
	status TEXT NOT NULL DEFAULT 'NEW',
	substatus TEXT NOT NULL DEFAULT 'ready',
	notes TEXT,
	blocker_question TEXT,
	branch TEXT,
	worktree_path TEXT,
	pr_url TEXT,
	pr_number INTEGER,
	pr_state TEXT,
	ci_status TEXT,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL,
	completed_at TIMESTAMP,
	deleted_at TIMESTAMP,
	task_kind TEXT NOT NULL DEFAULT 'code'
);

CREATE TABLE IF NOT EXISTS task_external_refs (
	task_id TEXT NOT NULL,
	tracker TEXT NOT NULL,
	ref_key TEXT NOT NULL,
	url TEXT,
	PRIMARY KEY (task_id, tracker, ref_key),
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_workers (
	task_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	agent TEXT NOT NULL,
	host TEXT NOT NULL,
	is_active INTEGER DEFAULT 1,
	assigned_at TIMESTAMP NOT NULL,
	repo_node_path TEXT,
	workflow_node TEXT,
	PRIMARY KEY (task_id, session_id),
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_workstreams (
	task_id TEXT NOT NULL,
	repo_node_path TEXT NOT NULL,
	target_host TEXT,
	depends_on TEXT,
	workflow_node TEXT,
	status TEXT NOT NULL DEFAULT 'NEW',
	branch TEXT,
	worktree_path TEXT,
	pr_url TEXT,
	updated_at TIMESTAMP NOT NULL,
	PRIMARY KEY (task_id, repo_node_path),
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_deliverables (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	title TEXT NOT NULL,
	host TEXT NOT NULL,
	file_path TEXT,
	url TEXT,
	provider TEXT,
	content_md TEXT,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL,
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS archived_tasks (
	id TEXT PRIMARY KEY,
	original_task_json TEXT NOT NULL,
	archived_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS speech_summaries (
	session_id TEXT NOT NULL,
	message_index INTEGER NOT NULL,
	spoken_text TEXT NOT NULL,
	suggested_chips TEXT NOT NULL DEFAULT '[]',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (session_id, message_index)
);

CREATE INDEX IF NOT EXISTS idx_tasks_group_project ON tasks(group_name, project_name);
CREATE INDEX IF NOT EXISTS idx_tasks_status_updated ON tasks(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_tasks_updated_at ON tasks(updated_at);
CREATE INDEX IF NOT EXISTS idx_task_workers_active ON task_workers(is_active);
CREATE INDEX IF NOT EXISTS idx_task_workstreams_task ON task_workstreams(task_id);
CREATE INDEX IF NOT EXISTS idx_task_deliverables_task_id ON task_deliverables(task_id);
CREATE INDEX IF NOT EXISTS idx_node_host_paths_node_path ON node_host_paths(node_path);
`

func InitDB(dbPath string) (*DB, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Ping database to verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create tables
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to execute schema: %w", err)
	}

	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN blocked_question TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN blocked_options TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN node_path TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN name TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN entrypoint TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN kind TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN version TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN context_pct INTEGER;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN git_branch TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN custom_title TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN ai_title TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN ai_description TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN first_prompt TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN last_prompt TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN is_unread INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN is_done INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN is_later INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN is_supervisor INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN current_task_id TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN last_state_change_at TIMESTAMP;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN account_id TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN engine_type TEXT DEFAULT 'tmux';")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN old_id TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN model TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN effort TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN permission_mode TEXT DEFAULT 'bypassPermissions';")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN blocked_questions TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN blocked_reset_at TIMESTAMP;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN metadata_updated_at TIMESTAMP;")
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS scheduled_prompts (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		prompt TEXT NOT NULL,
		deliver_at TIMESTAMP,
		created_at TIMESTAMP NOT NULL
	);`)
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_scheduled_prompts_session ON scheduled_prompts(session_id);")
	_, _ = db.Exec("ALTER TABLE tree_nodes ADD COLUMN updated_at TIMESTAMP;")
	_, _ = db.Exec("ALTER TABLE tree_nodes ADD COLUMN deleted_at TIMESTAMP;")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN deleted_at TIMESTAMP;")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN task_kind TEXT NOT NULL DEFAULT 'code';")
	_, _ = db.Exec("ALTER TABLE task_workers ADD COLUMN repo_node_path TEXT;")
	_, _ = db.Exec("ALTER TABLE task_workers ADD COLUMN workflow_node TEXT;")
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS task_workstreams (
		task_id TEXT NOT NULL,
		repo_node_path TEXT NOT NULL,
		target_host TEXT,
		depends_on TEXT,
		workflow_node TEXT,
		status TEXT NOT NULL DEFAULT 'NEW',
		branch TEXT,
		worktree_path TEXT,
		pr_url TEXT,
		updated_at TIMESTAMP NOT NULL,
		PRIMARY KEY (task_id, repo_node_path),
		FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
	);`)
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_task_workstreams_task ON task_workstreams(task_id);")
	_, _ = db.Exec("ALTER TABLE task_deliverables ADD COLUMN updated_at TIMESTAMP;")
	_, _ = db.Exec("ALTER TABLE task_deliverables ADD COLUMN provider TEXT;")
	_, _ = db.Exec("ALTER TABLE task_deliverables ADD COLUMN content_md TEXT;")
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS node_host_paths (
		node_path TEXT NOT NULL,
		host TEXT NOT NULL,
		project_dir TEXT NOT NULL,
		git_remote TEXT,
		capabilities TEXT,
		updated_at TIMESTAMP NOT NULL,
		PRIMARY KEY (node_path, host)
	);`)
	_, _ = db.Exec("UPDATE tree_nodes SET updated_at = COALESCE(created_at, CURRENT_TIMESTAMP) WHERE updated_at IS NULL;")
	_, _ = db.Exec("UPDATE task_deliverables SET updated_at = created_at WHERE updated_at IS NULL;")
	_, _ = db.Exec("UPDATE sessions SET metadata_updated_at = COALESCE(last_event_at, started_at, CURRENT_TIMESTAMP) WHERE metadata_updated_at IS NULL;")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_node_host_paths_node_path ON node_host_paths(node_path);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_tree_nodes_updated_at ON tree_nodes(updated_at);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_updated_at ON tasks(updated_at);")
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS accounts (
		id TEXT PRIMARY KEY,
		agent TEXT NOT NULL,
		name TEXT NOT NULL,
		display_name TEXT,
		config_dir TEXT,
		env_json TEXT,
		is_default INTEGER DEFAULT 0,
		created_at TIMESTAMP,
		updated_at TIMESTAMP
	);`)
	_, _ = db.Exec("CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);")
	_, _ = db.Exec("DELETE FROM settings WHERE (key = 'host_name' OR key = 'display_name') AND LOWER(value) = 'local';")
	_, _ = db.Exec("DELETE FROM sessions WHERE (COALESCE(managed, 0) = 0 AND COALESCE(activity, '') != 'Cleared (context reset)' AND COALESCE(is_done, 0) = 0 AND COALESCE(is_later, 0) = 0) OR id LIKE '%:observed:%' OR native_id LIKE 'proc-%';")
	_, _ = db.Exec("DELETE FROM tree_nodes WHERE path LIKE 'Project Y%' OR path LIKE 'ProjectY%' OR path LIKE '%Project Y%';")
	_, _ = db.Exec("UPDATE sessions SET tmux_name = 'ackbar-' || agent || '-' || native_id WHERE (tmux_name = '(deleted)' OR tmux_name = '') AND COALESCE(engine_type, 'tmux') != 'headless' AND COALESCE(is_done, 0) = 0;")
	_, _ = db.Exec("UPDATE sessions SET tmux_name = '' WHERE engine_type = 'headless' AND tmux_name = 'ackbar-' || agent || '-' || native_id;")
	var interruptedIDs []string
	if rows, err := db.Query("SELECT id FROM sessions WHERE engine_type = 'headless' AND (state = ? OR state = 'working');", int(StateWorking)); err == nil {
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil && id != "" {
				interruptedIDs = append(interruptedIDs, id)
			}
		}
		_ = rows.Close()
	}
	_, _ = db.Exec("UPDATE sessions SET state = ?, pid = 0, activity = '' WHERE engine_type = 'headless' AND (state = ? OR state = 'working');", int(StateIdle), int(StateWorking))
	_, _ = db.Exec("UPDATE tasks SET group_name = 'Personal' WHERE project_name = 'Ackbar' AND group_name = 'Modemobile';")

	// High-performance indices for fast session lookups, filtering, and liveness checks
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_state ON sessions(state);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_native_id ON sessions(native_id);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_host ON sessions(host);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_project_key ON sessions(project_key);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_account_id ON sessions(account_id);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_accounts_agent ON accounts(agent);")
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS speech_summaries (
		session_id TEXT NOT NULL,
		message_index INTEGER NOT NULL,
		spoken_text TEXT NOT NULL,
		suggested_chips TEXT NOT NULL DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (session_id, message_index)
	);`)
	_, _ = db.Exec("ALTER TABLE hosts ADD COLUMN policy_tags TEXT DEFAULT '[]';")
	_, _ = db.Exec("ALTER TABLE hosts ADD COLUMN routing_weight INTEGER DEFAULT -1;")
	_, _ = db.Exec("ALTER TABLE hosts ADD COLUMN detected_capabilities TEXT DEFAULT '[]';")
	_, _ = db.Exec("ALTER TABLE hosts ADD COLUMN updated_at TIMESTAMP;")
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS host_load_metrics (
		host TEXT NOT NULL,
		bucket_start TIMESTAMP NOT NULL,
		bucket_sec INTEGER NOT NULL DEFAULT 300,
		avg_working_sessions REAL NOT NULL DEFAULT 0,
		max_working_sessions INTEGER NOT NULL DEFAULT 0,
		avg_cpu_pct REAL NOT NULL DEFAULT 0,
		max_cpu_pct REAL NOT NULL DEFAULT 0,
		min_ram_free_gb REAL NOT NULL DEFAULT 0,
		sample_count INTEGER NOT NULL DEFAULT 1,
		tasks_spawned_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (host, bucket_start, bucket_sec)
	);`)
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_host_load_metrics_bucket ON host_load_metrics(bucket_start);")
	d := &DB{db: db, path: dbPath, interruptedHeadlessIDs: interruptedIDs}
	if _, err := d.PruneAllLegacyTasksOnce(); err != nil {
		log.Printf("[InitDB] Warning: PruneAllLegacyTasksOnce failed: %v", err)
	}
	_, _ = d.DeduplicateTasks()
	_, _ = db.Exec(`UPDATE tasks SET status = 'DONE', substatus = 'completed' WHERE deleted_at IS NULL AND UPPER(status) IN ('DONE', 'COMPLETED') AND LOWER(COALESCE(substatus, '')) NOT IN ('completed', 'rejected', 'abandoned');`)
	return d, nil
}

func (d *DB) Path() string {
	if d == nil {
		return ""
	}
	return d.path
}

func (d *DB) RemoveInterruptedHeadlessSessionID(id string) {
	if d == nil || id == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	filtered := d.interruptedHeadlessIDs[:0]
	for _, existing := range d.interruptedHeadlessIDs {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	d.interruptedHeadlessIDs = filtered
}

func (d *DB) PopInterruptedHeadlessSessionIDs() []string {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := d.interruptedHeadlessIDs
	d.interruptedHeadlessIDs = nil
	return ids
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) SaveSession(s *Session) error {
	rootsJSON, err := json.Marshal(s.Roots)
	if err != nil {
		return fmt.Errorf("failed to marshal roots: %w", err)
	}

	// Preserve user-assigned group (NodePath), CustomTitle, IsSupervisor, CurrentTaskID, and GitBranch if existing and empty in s
	if existing, _ := d.GetSession(s.ID); existing != nil {
		if s.MetadataUpdatedAt.IsZero() {
			if s.NodePath == "" && existing.NodePath != "" {
				s.NodePath = existing.NodePath
			}
			if s.CustomTitle == "" && existing.CustomTitle != "" {
				s.CustomTitle = existing.CustomTitle
			}
			if !s.IsSupervisor && existing.IsSupervisor {
				s.IsSupervisor = existing.IsSupervisor
			}
			if s.CurrentTaskID == "" && existing.CurrentTaskID != "" {
				s.CurrentTaskID = existing.CurrentTaskID
			}
			s.MetadataUpdatedAt = existing.MetadataUpdatedAt
		} else {
			if !s.IsSupervisor && existing.IsSupervisor {
				s.IsSupervisor = existing.IsSupervisor
			}
			if s.CurrentTaskID == "" && existing.CurrentTaskID != "" {
				s.CurrentTaskID = existing.CurrentTaskID
			}
		}
		if s.GitBranch == "" && existing.GitBranch != "" {
			s.GitBranch = existing.GitBranch
		}
		if s.OldID == "" && existing.OldID != "" {
			s.OldID = existing.OldID
		}
	}

	if s.GitBranch == "" && s.Cwd != "" {
		if s.State != StateEnded || strings.Contains(s.Cwd, "worktree") {
			s.GitBranch = ResolveGitBranch(s.Cwd)
		}
	}

	var blockedKind, blockedReason, blockedQuestion, blockedOptions, blockedQuestions sql.NullString
	var blockedSince, blockedResetAt sql.NullTime

	if s.Blocked != nil {
		blockedKind = sql.NullString{String: string(s.Blocked.Kind), Valid: true}
		blockedReason = sql.NullString{String: s.Blocked.Reason, Valid: true}
		blockedSince = sql.NullTime{Time: s.Blocked.Since, Valid: true}
		if s.Blocked.ResetAt != nil {
			blockedResetAt = sql.NullTime{Time: *s.Blocked.ResetAt, Valid: true}
		}
		if s.Blocked.Question != "" {
			blockedQuestion = sql.NullString{String: s.Blocked.Question, Valid: true}
		}
		if len(s.Blocked.Options) > 0 {
			if optBytes, err := json.Marshal(s.Blocked.Options); err == nil {
				blockedOptions = sql.NullString{String: string(optBytes), Valid: true}
			}
		}
		if len(s.Blocked.Questions) > 0 {
			if qBytes, err := json.Marshal(s.Blocked.Questions); err == nil {
				blockedQuestions = sql.NullString{String: string(qBytes), Valid: true}
			}
		}
	}

	isUnreadInt := 0
	if s.IsUnread {
		isUnreadInt = 1
	}

	isDoneInt := 0
	if s.IsDone {
		isDoneInt = 1
	}

	isLaterInt := 0
	if s.IsLater {
		isLaterInt = 1
	}

	isSupervisorInt := 0
	if s.IsSupervisor {
		isSupervisorInt = 1
	}

	var lastStateChangeAt sql.NullTime
	if !s.LastStateChangeAt.IsZero() {
		lastStateChangeAt = sql.NullTime{Time: s.LastStateChangeAt, Valid: true}
	}

	query := `
	INSERT INTO sessions (
		id, agent, host, native_id, cwd, roots, project_key, state, 
		blocked_kind, blocked_reason, blocked_since, blocked_question, blocked_options, blocked_questions, blocked_reset_at, activity, 
		started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
		custom_title, ai_title, ai_description, first_prompt, last_prompt, is_unread, last_state_change_at, is_done, is_later, account_id, engine_type, old_id, metadata_updated_at, is_supervisor, current_task_id, model, effort, permission_mode
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		cwd=excluded.cwd,
		roots=excluded.roots,
		project_key=excluded.project_key,
		state=excluded.state,
		blocked_kind=excluded.blocked_kind,
		blocked_reason=excluded.blocked_reason,
		blocked_since=excluded.blocked_since,
		blocked_question=excluded.blocked_question,
		blocked_options=excluded.blocked_options,
		blocked_questions=excluded.blocked_questions,
		blocked_reset_at=excluded.blocked_reset_at,
		activity=excluded.activity,
		last_event_at=excluded.last_event_at,
		managed=excluded.managed,
		tmux_name=excluded.tmux_name,
		pid=excluded.pid,
		archived=excluded.archived,
		node_path=excluded.node_path,
		name=excluded.name,
		entrypoint=excluded.entrypoint,
		kind=excluded.kind,
		version=excluded.version,
		context_pct=excluded.context_pct,
		git_branch=excluded.git_branch,
		custom_title=excluded.custom_title,
		ai_title=excluded.ai_title,
		ai_description=excluded.ai_description,
		first_prompt=excluded.first_prompt,
		last_prompt=excluded.last_prompt,
		is_unread=excluded.is_unread,
		last_state_change_at=excluded.last_state_change_at,
		is_done=excluded.is_done,
		is_later=excluded.is_later,
		account_id=COALESCE(NULLIF(excluded.account_id, ''), sessions.account_id),
		engine_type=COALESCE(NULLIF(excluded.engine_type, ''), sessions.engine_type),
		old_id=COALESCE(NULLIF(excluded.old_id, ''), sessions.old_id),
		metadata_updated_at=excluded.metadata_updated_at,
		is_supervisor=excluded.is_supervisor,
		current_task_id=excluded.current_task_id,
		model=excluded.model,
		effort=excluded.effort,
		permission_mode=excluded.permission_mode;
	`

	managedInt := 0
	if s.Managed {
		managedInt = 1
	}

	archivedInt := 0
	if s.Archived {
		archivedInt = 1
	}

	nodePath := sql.NullString{String: s.NodePath, Valid: s.NodePath != ""}
	sessName := sql.NullString{String: s.Name, Valid: s.Name != ""}
	entrypointVal := sql.NullString{String: s.Entrypoint, Valid: s.Entrypoint != ""}
	kindVal := sql.NullString{String: s.Kind, Valid: s.Kind != ""}
	versionVal := sql.NullString{String: s.Version, Valid: s.Version != ""}
	gitBranchVal := sql.NullString{String: s.GitBranch, Valid: s.GitBranch != ""}
	customTitleVal := sql.NullString{String: s.CustomTitle, Valid: s.CustomTitle != ""}
	aiTitleVal := sql.NullString{String: s.AITitle, Valid: s.AITitle != ""}
	aiDescVal := sql.NullString{String: s.AIDescription, Valid: s.AIDescription != ""}
	firstPromptVal := sql.NullString{String: s.FirstPrompt, Valid: s.FirstPrompt != ""}
	lastPromptVal := sql.NullString{String: s.LastPrompt, Valid: s.LastPrompt != ""}
	accountIDVal := sql.NullString{String: s.AccountID, Valid: s.AccountID != ""}
	oldIDVal := sql.NullString{String: s.OldID, Valid: s.OldID != ""}
	currentTaskIDVal := sql.NullString{String: s.CurrentTaskID, Valid: s.CurrentTaskID != ""}
	modelVal := sql.NullString{String: s.Model, Valid: s.Model != ""}
	effortVal := sql.NullString{String: s.Effort, Valid: s.Effort != ""}
	permMode := s.PermissionMode
	if permMode == "" {
		permMode = "bypassPermissions"
	}
	permModeVal := sql.NullString{String: permMode, Valid: true}
	engineType := s.EngineType
	if engineType == "" {
		engineType = EngineTmux
	}

	if s.MetadataUpdatedAt.IsZero() {
		s.MetadataUpdatedAt = time.Now().UTC()
	}

	_, err = d.db.Exec(query,
		s.ID, s.Agent, s.Host, s.NativeID, s.Cwd, string(rootsJSON), s.ProjectKey, int(s.State),
		blockedKind, blockedReason, blockedSince, blockedQuestion, blockedOptions, blockedQuestions, blockedResetAt, s.Activity,
		s.StartedAt, s.LastEventAt, managedInt, s.TmuxName, s.PID, archivedInt, nodePath, sessName, entrypointVal, kindVal, versionVal, s.ContextPct, gitBranchVal,
		customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal, isUnreadInt, lastStateChangeAt, isDoneInt, isLaterInt, accountIDVal, engineType, oldIDVal, s.MetadataUpdatedAt, isSupervisorInt, currentTaskIDVal,
		modelVal, effortVal, permModeVal,
	)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	return nil
}

func (d *DB) GetSession(id string) (*Session, error) {
	query := `
	SELECT id, agent, host, native_id, cwd, roots, project_key, state,
	       blocked_kind, blocked_reason, blocked_since, blocked_question, blocked_options, blocked_questions, blocked_reset_at, activity,
	       started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
	       custom_title, ai_title, ai_description, first_prompt, last_prompt, is_unread, last_state_change_at, is_done, is_later, account_id, engine_type, old_id, metadata_updated_at, is_supervisor, current_task_id,
	       model, effort, permission_mode
	FROM sessions WHERE id = ? OR native_id = ? OR native_id = ?;
	`

	var s Session
	var rootsStr, blockedKind, blockedReason, blockedQuestion, blockedOptions, blockedQuestions, nodePath, sessName, entrypointVal, kindVal, versionVal, gitBranchVal sql.NullString
	var customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal, accountIDVal, engineTypeVal, oldIDVal, currentTaskIDVal, modelVal, effortVal, permModeVal sql.NullString
	var ctxPct, isSupervisorVal sql.NullInt64
	var blockedSince, blockedResetAt, lastStateChangeAt, metadataUpdatedAt sql.NullTime
	var managedInt, archivedInt, isUnreadInt, isDoneInt, isLaterInt int

	nativeCandidate := id
	if idx := strings.LastIndex(id, ":"); idx != -1 {
		nativeCandidate = id[idx+1:]
	}

	row := d.db.QueryRow(query, id, id, nativeCandidate)
	err := row.Scan(
		&s.ID, &s.Agent, &s.Host, &s.NativeID, &s.Cwd, &rootsStr, &s.ProjectKey, (*int)(&s.State),
		&blockedKind, &blockedReason, &blockedSince, &blockedQuestion, &blockedOptions, &blockedQuestions, &blockedResetAt, &s.Activity,
		&s.StartedAt, &s.LastEventAt, &managedInt, &s.TmuxName, &s.PID, &archivedInt, &nodePath, &sessName, &entrypointVal, &kindVal, &versionVal, &ctxPct, &gitBranchVal,
		&customTitleVal, &aiTitleVal, &aiDescVal, &firstPromptVal, &lastPromptVal, &isUnreadInt, &lastStateChangeAt, &isDoneInt, &isLaterInt, &accountIDVal, &engineTypeVal, &oldIDVal, &metadataUpdatedAt, &isSupervisorVal, &currentTaskIDVal,
		&modelVal, &effortVal, &permModeVal,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to scan session: %w", err)
	}

	if metadataUpdatedAt.Valid {
		s.MetadataUpdatedAt = metadataUpdatedAt.Time
	}
	if isSupervisorVal.Valid {
		s.IsSupervisor = isSupervisorVal.Int64 == 1
	}
	if currentTaskIDVal.Valid {
		s.CurrentTaskID = currentTaskIDVal.String
	}

	if accountIDVal.Valid {
		s.AccountID = accountIDVal.String
	}
	if oldIDVal.Valid {
		s.OldID = oldIDVal.String
	}
	if engineTypeVal.Valid && engineTypeVal.String != "" {
		s.EngineType = engineTypeVal.String
	} else {
		s.EngineType = EngineTmux
	}

	s.Managed = managedInt == 1
	s.Archived = archivedInt == 1
	s.IsUnread = isUnreadInt == 1
	s.IsDone = isDoneInt == 1
	s.IsLater = isLaterInt == 1
	if lastStateChangeAt.Valid {
		s.LastStateChangeAt = lastStateChangeAt.Time
	}
	if nodePath.Valid {
		s.NodePath = nodePath.String
	}
	if sessName.Valid {
		s.Name = sessName.String
	}
	if entrypointVal.Valid {
		s.Entrypoint = entrypointVal.String
	}
	if kindVal.Valid {
		s.Kind = kindVal.String
	}
	if versionVal.Valid {
		s.Version = versionVal.String
	}
	if ctxPct.Valid {
		s.ContextPct = int(ctxPct.Int64)
	}
	if gitBranchVal.Valid {
		s.GitBranch = gitBranchVal.String
	} else if s.Cwd != "" {
		s.GitBranch = ResolveGitBranch(s.Cwd)
	}
	if customTitleVal.Valid {
		s.CustomTitle = customTitleVal.String
	}
	if aiTitleVal.Valid {
		s.AITitle = aiTitleVal.String
	}
	if aiDescVal.Valid {
		s.AIDescription = aiDescVal.String
	}
	if firstPromptVal.Valid {
		s.FirstPrompt = firstPromptVal.String
	}
	if lastPromptVal.Valid {
		s.LastPrompt = lastPromptVal.String
	}

	if rootsStr.Valid && rootsStr.String != "" {
		if err := json.Unmarshal([]byte(rootsStr.String), &s.Roots); err != nil {
			s.Roots = []string{}
		}
	} else {
		s.Roots = []string{}
	}

	if blockedKind.Valid && blockedKind.String != "" {
		s.Blocked = &Blocked{
			Kind:     BlockKind(blockedKind.String),
			Reason:   blockedReason.String,
			Since:    blockedSince.Time,
			Question: blockedQuestion.String,
		}
		if blockedResetAt.Valid {
			s.Blocked.ResetAt = &blockedResetAt.Time
		}
		if blockedOptions.Valid && blockedOptions.String != "" {
			_ = json.Unmarshal([]byte(blockedOptions.String), &s.Blocked.Options)
		}
		if blockedQuestions.Valid && blockedQuestions.String != "" {
			_ = json.Unmarshal([]byte(blockedQuestions.String), &s.Blocked.Questions)
		}
	}

	if modelVal.Valid {
		s.Model = modelVal.String
	}
	if effortVal.Valid {
		s.Effort = effortVal.String
	}
	if permModeVal.Valid && permModeVal.String != "" {
		s.PermissionMode = permModeVal.String
	} else {
		s.PermissionMode = "bypassPermissions"
	}

	return &s, nil
}

func (d *DB) ListSessions() ([]*Session, error) {
	query := `
	SELECT id, agent, host, native_id, cwd, roots, project_key, state,
	       blocked_kind, blocked_reason, blocked_since, blocked_question, blocked_options, blocked_questions, blocked_reset_at, activity,
	       started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
	       custom_title, ai_title, ai_description, first_prompt, last_prompt, is_unread, last_state_change_at, is_done, is_later, account_id, engine_type, old_id, metadata_updated_at, is_supervisor, current_task_id,
	       model, effort, permission_mode
	FROM sessions
	ORDER BY last_event_at DESC;
	`

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		var s Session
		var rootsStr, blockedKind, blockedReason, blockedQuestion, blockedOptions, blockedQuestions, nodePath, sessName, entrypointVal, kindVal, versionVal, gitBranchVal sql.NullString
		var customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal, accountIDVal, engineTypeVal, oldIDVal, currentTaskIDVal, modelVal, effortVal, permModeVal sql.NullString
		var ctxPct, isSupervisorVal sql.NullInt64
		var blockedSince, blockedResetAt, lastStateChangeAt, metadataUpdatedAt sql.NullTime
		var managedInt, archivedInt, isUnreadInt, isDoneInt, isLaterInt int

		err := rows.Scan(
			&s.ID, &s.Agent, &s.Host, &s.NativeID, &s.Cwd, &rootsStr, &s.ProjectKey, (*int)(&s.State),
			&blockedKind, &blockedReason, &blockedSince, &blockedQuestion, &blockedOptions, &blockedQuestions, &blockedResetAt, &s.Activity,
			&s.StartedAt, &s.LastEventAt, &managedInt, &s.TmuxName, &s.PID, &archivedInt, &nodePath, &sessName, &entrypointVal, &kindVal, &versionVal, &ctxPct, &gitBranchVal,
			&customTitleVal, &aiTitleVal, &aiDescVal, &firstPromptVal, &lastPromptVal, &isUnreadInt, &lastStateChangeAt, &isDoneInt, &isLaterInt, &accountIDVal, &engineTypeVal, &oldIDVal, &metadataUpdatedAt, &isSupervisorVal, &currentTaskIDVal,
			&modelVal, &effortVal, &permModeVal,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session in list: %w", err)
		}

		if metadataUpdatedAt.Valid {
			s.MetadataUpdatedAt = metadataUpdatedAt.Time
		}
		if isSupervisorVal.Valid {
			s.IsSupervisor = isSupervisorVal.Int64 == 1
		}
		if currentTaskIDVal.Valid {
			s.CurrentTaskID = currentTaskIDVal.String
		}

		if accountIDVal.Valid {
			s.AccountID = accountIDVal.String
		}
		if oldIDVal.Valid {
			s.OldID = oldIDVal.String
		}
		if engineTypeVal.Valid && engineTypeVal.String != "" {
			s.EngineType = engineTypeVal.String
		} else {
			s.EngineType = EngineTmux
		}

		s.Managed = managedInt == 1
		s.Archived = archivedInt == 1
		s.IsUnread = isUnreadInt == 1
		s.IsDone = isDoneInt == 1
		s.IsLater = isLaterInt == 1
		if lastStateChangeAt.Valid {
			s.LastStateChangeAt = lastStateChangeAt.Time
		}
		if nodePath.Valid {
			s.NodePath = nodePath.String
		}
		if sessName.Valid {
			s.Name = sessName.String
		}
		if entrypointVal.Valid {
			s.Entrypoint = entrypointVal.String
		}
		if kindVal.Valid {
			s.Kind = kindVal.String
		}
		if versionVal.Valid {
			s.Version = versionVal.String
		}
		if ctxPct.Valid {
			s.ContextPct = int(ctxPct.Int64)
		}
		if gitBranchVal.Valid {
			s.GitBranch = gitBranchVal.String
		} else if s.Cwd != "" {
			s.GitBranch = ResolveGitBranch(s.Cwd)
		}
		if customTitleVal.Valid {
			s.CustomTitle = customTitleVal.String
		}
		if aiTitleVal.Valid {
			s.AITitle = aiTitleVal.String
		}
		if aiDescVal.Valid {
			s.AIDescription = aiDescVal.String
		}
		if firstPromptVal.Valid {
			s.FirstPrompt = firstPromptVal.String
		}
		if lastPromptVal.Valid {
			s.LastPrompt = lastPromptVal.String
		}

		if rootsStr.Valid && rootsStr.String != "" {
			_ = json.Unmarshal([]byte(rootsStr.String), &s.Roots)
		} else {
			s.Roots = []string{}
		}

		if blockedKind.Valid && blockedKind.String != "" {
			s.Blocked = &Blocked{
				Kind:     BlockKind(blockedKind.String),
				Reason:   blockedReason.String,
				Since:    blockedSince.Time,
				Question: blockedQuestion.String,
			}
			if blockedResetAt.Valid {
				s.Blocked.ResetAt = &blockedResetAt.Time
			}
			if blockedOptions.Valid && blockedOptions.String != "" {
				_ = json.Unmarshal([]byte(blockedOptions.String), &s.Blocked.Options)
			}
			if blockedQuestions.Valid && blockedQuestions.String != "" {
				_ = json.Unmarshal([]byte(blockedQuestions.String), &s.Blocked.Questions)
			}
		}

		if modelVal.Valid {
			s.Model = modelVal.String
		}
		if effortVal.Valid {
			s.Effort = effortVal.String
		}
		if permModeVal.Valid && permModeVal.String != "" {
			s.PermissionMode = permModeVal.String
		} else {
			s.PermissionMode = "bypassPermissions"
		}

		sessions = append(sessions, &s)
	}

	return sessions, nil
}

func (d *DB) MarkSessionRead(id string) error {
	query := `UPDATE sessions SET is_unread = 0 WHERE id = ? OR native_id = ?;`
	_, err := d.db.Exec(query, id, id)
	return err
}

func (d *DB) MarkSessionDone(id string, isDone bool) error {
	now := time.Now().UTC()
	var query string
	if isDone {
		query = `UPDATE sessions SET is_done = 1, is_later = 0, metadata_updated_at = ? WHERE id = ? OR native_id = ?;`
	} else {
		query = `UPDATE sessions SET is_done = 0, metadata_updated_at = ? WHERE id = ? OR native_id = ?;`
	}
	_, err := d.db.Exec(query, now, id, id)
	return err
}

func (d *DB) MarkSessionLater(id string, isLater bool) error {
	now := time.Now().UTC()
	var query string
	if isLater {
		query = `UPDATE sessions SET is_later = 1, is_done = 0, metadata_updated_at = ? WHERE id = ? OR native_id = ?;`
	} else {
		query = `UPDATE sessions SET is_later = 0, metadata_updated_at = ? WHERE id = ? OR native_id = ?;`
	}
	_, err := d.db.Exec(query, now, id, id)
	return err
}

func (d *DB) TouchSessionMetadata(id string, ts time.Time) error {
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := d.db.Exec("UPDATE sessions SET metadata_updated_at = ? WHERE id = ? OR native_id = ?;", ts, id, id)
	return err
}

func (d *DB) GetSetting(key string) (string, error) {
	var val string
	err := d.db.QueryRow("SELECT value FROM settings WHERE key = ?;", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (d *DB) GetAllSettings() (map[string]string, error) {
	rows, err := d.db.Query("SELECT key, value FROM settings;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			res[k] = v
		}
	}
	return res, nil
}

func (d *DB) SetSettings(settings map[string]string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value;")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, v := range settings {
		if _, err := stmt.Exec(k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListActiveSessions queries only active, non-ended sessions (state != StateEnded)
func (d *DB) ListActiveSessions() ([]*Session, error) {
	query := `
	SELECT id, agent, host, native_id, cwd, roots, project_key, state,
	       blocked_kind, blocked_reason, blocked_since, blocked_question, blocked_options, blocked_questions, blocked_reset_at, activity,
	       started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
	       custom_title, ai_title, ai_description, first_prompt, last_prompt, is_unread, last_state_change_at, is_done, is_later, account_id, engine_type, old_id, metadata_updated_at, is_supervisor, current_task_id,
	       model, effort, permission_mode
	FROM sessions
	WHERE state != ?
	ORDER BY last_event_at DESC;
	`

	rows, err := d.db.Query(query, int(StateEnded))
	if err != nil {
		return nil, fmt.Errorf("failed to query active sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		var s Session
		var rootsStr, blockedKind, blockedReason, blockedQuestion, blockedOptions, blockedQuestions, nodePath, sessName, entrypointVal, kindVal, versionVal, gitBranchVal sql.NullString
		var customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal, accountIDVal, engineTypeVal, oldIDVal, currentTaskIDVal, modelVal, effortVal, permModeVal sql.NullString
		var ctxPct, isSupervisorVal sql.NullInt64
		var blockedSince, blockedResetAt, lastStateChangeAt, metadataUpdatedAt sql.NullTime
		var managedInt, archivedInt, isUnreadInt, isDoneInt, isLaterInt int

		err := rows.Scan(
			&s.ID, &s.Agent, &s.Host, &s.NativeID, &s.Cwd, &rootsStr, &s.ProjectKey, (*int)(&s.State),
			&blockedKind, &blockedReason, &blockedSince, &blockedQuestion, &blockedOptions, &blockedQuestions, &blockedResetAt, &s.Activity,
			&s.StartedAt, &s.LastEventAt, &managedInt, &s.TmuxName, &s.PID, &archivedInt, &nodePath, &sessName, &entrypointVal, &kindVal, &versionVal, &ctxPct, &gitBranchVal,
			&customTitleVal, &aiTitleVal, &aiDescVal, &firstPromptVal, &lastPromptVal, &isUnreadInt, &lastStateChangeAt, &isDoneInt, &isLaterInt, &accountIDVal, &engineTypeVal, &oldIDVal, &metadataUpdatedAt, &isSupervisorVal, &currentTaskIDVal,
			&modelVal, &effortVal, &permModeVal,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan active session in list: %w", err)
		}

		if metadataUpdatedAt.Valid {
			s.MetadataUpdatedAt = metadataUpdatedAt.Time
		}
		if isSupervisorVal.Valid {
			s.IsSupervisor = isSupervisorVal.Int64 == 1
		}
		if currentTaskIDVal.Valid {
			s.CurrentTaskID = currentTaskIDVal.String
		}

		if accountIDVal.Valid {
			s.AccountID = accountIDVal.String
		}
		if oldIDVal.Valid {
			s.OldID = oldIDVal.String
		}
		if engineTypeVal.Valid && engineTypeVal.String != "" {
			s.EngineType = engineTypeVal.String
		} else {
			s.EngineType = EngineTmux
		}

		s.Managed = managedInt == 1
		s.Archived = archivedInt == 1
		s.IsUnread = isUnreadInt == 1
		s.IsDone = isDoneInt == 1
		s.IsLater = isLaterInt == 1
		if lastStateChangeAt.Valid {
			s.LastStateChangeAt = lastStateChangeAt.Time
		}
		if nodePath.Valid {
			s.NodePath = nodePath.String
		}
		if sessName.Valid {
			s.Name = sessName.String
		}
		if entrypointVal.Valid {
			s.Entrypoint = entrypointVal.String
		}
		if kindVal.Valid {
			s.Kind = kindVal.String
		}
		if versionVal.Valid {
			s.Version = versionVal.String
		}
		if ctxPct.Valid {
			s.ContextPct = int(ctxPct.Int64)
		}
		if gitBranchVal.Valid {
			s.GitBranch = gitBranchVal.String
		} else if s.Cwd != "" {
			s.GitBranch = ResolveGitBranch(s.Cwd)
		}
		if customTitleVal.Valid {
			s.CustomTitle = customTitleVal.String
		}
		if aiTitleVal.Valid {
			s.AITitle = aiTitleVal.String
		}
		if aiDescVal.Valid {
			s.AIDescription = aiDescVal.String
		}
		if firstPromptVal.Valid {
			s.FirstPrompt = firstPromptVal.String
		}
		if lastPromptVal.Valid {
			s.LastPrompt = lastPromptVal.String
		}

		if rootsStr.Valid && rootsStr.String != "" {
			_ = json.Unmarshal([]byte(rootsStr.String), &s.Roots)
		} else {
			s.Roots = []string{}
		}

		if blockedKind.Valid && blockedKind.String != "" {
			s.Blocked = &Blocked{
				Kind:     BlockKind(blockedKind.String),
				Reason:   blockedReason.String,
				Since:    blockedSince.Time,
				Question: blockedQuestion.String,
			}
			if blockedResetAt.Valid {
				s.Blocked.ResetAt = &blockedResetAt.Time
			}
			if blockedOptions.Valid && blockedOptions.String != "" {
				_ = json.Unmarshal([]byte(blockedOptions.String), &s.Blocked.Options)
			}
			if blockedQuestions.Valid && blockedQuestions.String != "" {
				_ = json.Unmarshal([]byte(blockedQuestions.String), &s.Blocked.Questions)
			}
		}

		if modelVal.Valid {
			s.Model = modelVal.String
		}
		if effortVal.Valid {
			s.Effort = effortVal.String
		}
		if permModeVal.Valid && permModeVal.String != "" {
			s.PermissionMode = permModeVal.String
		} else {
			s.PermissionMode = "bypassPermissions"
		}

		sessions = append(sessions, &s)
	}

	return sessions, nil
}

func (d *DB) DeleteSession(id string) error {
	_, err := d.db.Exec("DELETE FROM sessions WHERE id = ? OR native_id = ?;", id, id)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

func (d *DB) SaveNode(node *TreeNode) error {
	if node.CreatedAt.IsZero() {
		node.CreatedAt = time.Now().UTC()
	}
	if node.UpdatedAt.IsZero() {
		node.UpdatedAt = time.Now().UTC()
	}
	query := `
	INSERT INTO tree_nodes (path, project_dir, git_url, created_at, updated_at, deleted_at)
	VALUES (?, ?, ?, ?, ?, NULL)
	ON CONFLICT(path) DO UPDATE SET
		project_dir=excluded.project_dir,
		git_url=excluded.git_url,
		updated_at=excluded.updated_at,
		deleted_at=NULL;
	`
	_, err := d.db.Exec(query, node.Path, node.ProjectDir, node.GitURL, node.CreatedAt, node.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to save node: %w", err)
	}
	return nil
}

func (d *DB) GetNode(path string) (*TreeNode, error) {
	var n TreeNode
	var projectDir, gitURL sql.NullString
	var updatedAt, deletedAt sql.NullTime
	err := d.db.QueryRow("SELECT path, project_dir, git_url, created_at, updated_at, deleted_at FROM tree_nodes WHERE path = ? AND deleted_at IS NULL;", path).Scan(&n.Path, &projectDir, &gitURL, &n.CreatedAt, &updatedAt, &deletedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get node: %w", err)
	}
	if projectDir.Valid {
		n.ProjectDir = projectDir.String
	}
	if gitURL.Valid {
		n.GitURL = gitURL.String
	}
	if updatedAt.Valid {
		n.UpdatedAt = updatedAt.Time
	}
	if deletedAt.Valid {
		n.DeletedAt = &deletedAt.Time
	}
	nhps, _ := d.ListNodeHostPaths(path)
	n.HostPaths = nhps
	return &n, nil
}

func (d *DB) ListNodes() ([]*TreeNode, error) {
	query := `SELECT path, project_dir, git_url, created_at, updated_at, deleted_at FROM tree_nodes WHERE deleted_at IS NULL ORDER BY path ASC;`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tree_nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*TreeNode
	for rows.Next() {
		var n TreeNode
		var projectDir, gitURL sql.NullString
		var updatedAt, deletedAt sql.NullTime
		if err := rows.Scan(&n.Path, &projectDir, &gitURL, &n.CreatedAt, &updatedAt, &deletedAt); err != nil {
			return nil, err
		}
		if projectDir.Valid {
			n.ProjectDir = projectDir.String
		}
		if gitURL.Valid {
			n.GitURL = gitURL.String
		}
		if updatedAt.Valid {
			n.UpdatedAt = updatedAt.Time
		}
		if deletedAt.Valid {
			n.DeletedAt = &deletedAt.Time
		}
		nodes = append(nodes, &n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	allHostPaths, _ := d.ListNodeHostPaths("")
	if len(allHostPaths) > 0 {
		hpMap := make(map[string][]NodeHostPath)
		for _, hp := range allHostPaths {
			hpMap[hp.NodePath] = append(hpMap[hp.NodePath], hp)
		}
		for _, node := range nodes {
			if hps, ok := hpMap[node.Path]; ok {
				node.HostPaths = hps
			}
		}
	}
	return nodes, nil
}

func (d *DB) DeleteNode(path string) error {
	now := time.Now().UTC()
	_, err := d.db.Exec("UPDATE tree_nodes SET deleted_at = ?, updated_at = ? WHERE (path = ? OR SUBSTR(path, 1, LENGTH(?) + 1) = ? || '/') AND deleted_at IS NULL;", now, now, path, path, path)
	if err != nil {
		return fmt.Errorf("failed to delete node: %w", err)
	}
	_, _ = d.db.Exec("UPDATE sessions SET node_path = '', metadata_updated_at = ? WHERE (node_path = ? OR SUBSTR(node_path, 1, LENGTH(?) + 1) = ? || '/');", now, path, path, path)
	_, _ = d.db.Exec("DELETE FROM node_host_paths WHERE node_path = ? OR SUBSTR(node_path, 1, LENGTH(?) + 1) = ? || '/';", path, path, path)
	return nil
}

func (d *DB) MoveNode(oldPath, newPath string) error {
	now := time.Now().UTC()

	// Find affected non-deleted tree_nodes
	rows, err := d.db.Query("SELECT path, project_dir, git_url, created_at FROM tree_nodes WHERE (path = ? OR SUBSTR(path, 1, LENGTH(?) + 1) = ? || '/') AND deleted_at IS NULL;", oldPath, oldPath, oldPath)
	if err != nil {
		return fmt.Errorf("failed to query nodes to move: %w", err)
	}
	type moveNodeItem struct {
		oldPath    string
		newPath    string
		projectDir string
		gitURL     string
		createdAt  time.Time
	}
	var items []moveNodeItem
	for rows.Next() {
		var item moveNodeItem
		var pDir, gURL sql.NullString
		if err := rows.Scan(&item.oldPath, &pDir, &gURL, &item.createdAt); err == nil {
			if pDir.Valid {
				item.projectDir = pDir.String
			}
			if gURL.Valid {
				item.gitURL = gURL.String
			}
			rel := strings.TrimPrefix(item.oldPath, oldPath)
			item.newPath = newPath + rel
			items = append(items, item)
		}
	}
	rows.Close()

	// Soft-delete old paths
	if _, err := d.db.Exec("UPDATE tree_nodes SET deleted_at = ?, updated_at = ? WHERE (path = ? OR SUBSTR(path, 1, LENGTH(?) + 1) = ? || '/') AND deleted_at IS NULL;", now, now, oldPath, oldPath, oldPath); err != nil {
		return fmt.Errorf("failed to soft-delete moved tree_nodes: %w", err)
	}

	// Insert or reactivate new paths
	for _, it := range items {
		qUpsert := `
		INSERT INTO tree_nodes (path, project_dir, git_url, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, NULL)
		ON CONFLICT(path) DO UPDATE SET
			project_dir=excluded.project_dir,
			git_url=excluded.git_url,
			updated_at=excluded.updated_at,
			deleted_at=NULL;
		`
		if _, err := d.db.Exec(qUpsert, it.newPath, it.projectDir, it.gitURL, it.createdAt, now); err != nil {
			return fmt.Errorf("failed to upsert new tree_node path: %w", err)
		}
	}

	// Update node_host_paths
	qHostPaths := `UPDATE node_host_paths SET node_path = ? || SUBSTR(node_path, LENGTH(?) + 1), updated_at = ? WHERE node_path = ? OR SUBSTR(node_path, 1, LENGTH(?) + 1) = ? || '/';`
	if _, err := d.db.Exec(qHostPaths, newPath, oldPath, now, oldPath, oldPath, oldPath); err != nil {
		return fmt.Errorf("failed to update node_host_paths: %w", err)
	}

	// Update sessions
	qSess := `UPDATE sessions SET node_path = ? || SUBSTR(node_path, LENGTH(?) + 1), metadata_updated_at = ? WHERE node_path = ? OR SUBSTR(node_path, 1, LENGTH(?) + 1) = ? || '/';`
	if _, err := d.db.Exec(qSess, newPath, oldPath, now, oldPath, oldPath, oldPath); err != nil {
		return fmt.Errorf("failed to update sessions node_path: %w", err)
	}
	return nil
}

func (d *DB) MoveSessionNode(sessionID, nodePath string) error {
	now := time.Now().UTC()
	_, err := d.db.Exec("UPDATE sessions SET node_path = ?, metadata_updated_at = ? WHERE id = ? OR native_id = ?;", nodePath, now, sessionID, sessionID)
	if err != nil {
		return fmt.Errorf("failed to move session node: %w", err)
	}
	return nil
}

func (d *DB) SaveNodeHostPath(nhp *NodeHostPath) error {
	if nhp.UpdatedAt.IsZero() {
		nhp.UpdatedAt = time.Now().UTC()
	}
	var capsJSON string
	if len(nhp.Capabilities) > 0 {
		if b, err := json.Marshal(nhp.Capabilities); err == nil {
			capsJSON = string(b)
		}
	}
	query := `
	INSERT INTO node_host_paths (node_path, host, project_dir, git_remote, capabilities, updated_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(node_path, host) DO UPDATE SET
		project_dir=excluded.project_dir,
		git_remote=excluded.git_remote,
		capabilities=excluded.capabilities,
		updated_at=excluded.updated_at;
	`
	_, err := d.db.Exec(query, nhp.NodePath, nhp.Host, nhp.ProjectDir, nhp.GitRemote, capsJSON, nhp.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to save node_host_path: %w", err)
	}
	return nil
}

func (d *DB) UpsertNodeHostPathLWW(hp *NodeHostPath) error {
	if hp == nil {
		return nil
	}
	var existingUpdAt sql.NullTime
	err := d.db.QueryRow("SELECT updated_at FROM node_host_paths WHERE node_path = ? AND host = ?;", hp.NodePath, hp.Host).Scan(&existingUpdAt)
	if err == nil && existingUpdAt.Valid && !hp.UpdatedAt.After(existingUpdAt.Time) {
		return nil
	}
	return d.SaveNodeHostPath(hp)
}

func (d *DB) GetNodeHostPath(nodePath, host string) (*NodeHostPath, error) {
	var nhp NodeHostPath
	var gitRemote, capsStr sql.NullString
	query := `SELECT node_path, host, project_dir, git_remote, capabilities, updated_at FROM node_host_paths WHERE node_path = ? AND host = ?;`
	err := d.db.QueryRow(query, nodePath, host).Scan(&nhp.NodePath, &nhp.Host, &nhp.ProjectDir, &gitRemote, &capsStr, &nhp.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get node_host_path: %w", err)
	}
	if gitRemote.Valid {
		nhp.GitRemote = gitRemote.String
	}
	if capsStr.Valid && capsStr.String != "" {
		_ = json.Unmarshal([]byte(capsStr.String), &nhp.Capabilities)
	}
	return &nhp, nil
}

func (d *DB) ListNodeHostPaths(nodePath string) ([]NodeHostPath, error) {
	var query string
	var args []interface{}
	if nodePath == "" {
		query = `SELECT node_path, host, project_dir, git_remote, capabilities, updated_at FROM node_host_paths ORDER BY host ASC;`
	} else {
		query = `SELECT node_path, host, project_dir, git_remote, capabilities, updated_at FROM node_host_paths WHERE node_path = ? ORDER BY host ASC;`
		args = append(args, nodePath)
	}
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list node_host_paths: %w", err)
	}
	defer rows.Close()

	var result []NodeHostPath
	for rows.Next() {
		var nhp NodeHostPath
		var gitRemote, capsStr sql.NullString
		if err := rows.Scan(&nhp.NodePath, &nhp.Host, &nhp.ProjectDir, &gitRemote, &capsStr, &nhp.UpdatedAt); err != nil {
			return nil, err
		}
		if gitRemote.Valid {
			nhp.GitRemote = gitRemote.String
		}
		if capsStr.Valid && capsStr.String != "" {
			_ = json.Unmarshal([]byte(capsStr.String), &nhp.Capabilities)
		}
		result = append(result, nhp)
	}
	return result, rows.Err()
}

func DefaultHostPolicy(hostName string) (policyTags []string, weight int) {
	lower := strings.ToLower(strings.TrimSpace(hostName))
	if idx := strings.LastIndex(lower, "@"); idx != -1 {
		lower = lower[idx+1:]
	}
	switch {
	case strings.Contains(lower, "legion"):
		return []string{"24/7"}, 75
	case strings.Contains(lower, "macmini") || strings.Contains(lower, "mini"):
		return []string{"24/7"}, 25
	case strings.Contains(lower, "macbook") || strings.Contains(lower, "laptop"):
		return []string{"ephemeral"}, 0
	default:
		return []string{"24/7"}, 50
	}
}

func (d *DB) SaveHost(h *HostRecord) error {
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Now().UTC()
	}
	if h.UpdatedAt.IsZero() {
		h.UpdatedAt = time.Now().UTC()
	}

	// Preserve existing policy_tags, routing_weight, and detected_capabilities if not provided in update
	existing, _ := d.GetHost(h.Name)
	if len(h.PolicyTags) == 0 {
		if existing != nil && len(existing.PolicyTags) > 0 {
			h.PolicyTags = existing.PolicyTags
		} else {
			defTags, _ := DefaultHostPolicy(h.Name)
			h.PolicyTags = defTags
		}
	}
	if !h.HasExplicitWeight && h.RoutingWeight == 0 {
		if existing != nil && existing.HasExplicitWeight {
			h.RoutingWeight = existing.RoutingWeight
			h.HasExplicitWeight = true
		} else {
			_, defWeight := DefaultHostPolicy(h.Name)
			// If policy_tags explicitly omits 24/7 and has ephemeral, default weight is 0
			isEphemeral := false
			has247 := false
			for _, t := range h.PolicyTags {
				if strings.EqualFold(t, "ephemeral") {
					isEphemeral = true
				}
				if strings.EqualFold(t, "24/7") || strings.EqualFold(t, "always_on") || strings.EqualFold(t, "always-on") {
					has247 = true
				}
			}
			if isEphemeral && !has247 {
				h.RoutingWeight = 0
			} else {
				h.RoutingWeight = defWeight
			}
		}
	}
	if len(h.DetectedCapabilities) == 0 && existing != nil && len(existing.DetectedCapabilities) > 0 {
		h.DetectedCapabilities = existing.DetectedCapabilities
	}
	if h.URL == "" && existing != nil && existing.URL != "" {
		h.URL = existing.URL
	}
	if h.SSHTarget == "" && existing != nil && existing.SSHTarget != "" {
		h.SSHTarget = existing.SSHTarget
	}
	if h.RemoteCwd == "" && existing != nil && existing.RemoteCwd != "" {
		h.RemoteCwd = existing.RemoteCwd
	}

	policyJSON, _ := json.Marshal(h.PolicyTags)
	capsJSON, _ := json.Marshal(h.DetectedCapabilities)

	query := `
	INSERT INTO hosts (name, url, ssh_target, remote_cwd, policy_tags, routing_weight, detected_capabilities, updated_at, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(name) DO UPDATE SET
		url=excluded.url,
		ssh_target=excluded.ssh_target,
		remote_cwd=excluded.remote_cwd,
		policy_tags=excluded.policy_tags,
		routing_weight=excluded.routing_weight,
		detected_capabilities=excluded.detected_capabilities,
		updated_at=excluded.updated_at;
	`
	_, err := d.db.Exec(query, h.Name, h.URL, h.SSHTarget, h.RemoteCwd, string(policyJSON), h.RoutingWeight, string(capsJSON), h.UpdatedAt, h.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to save host: %w", err)
	}
	return nil
}

func hydrateHostRecordDefaults(h *HostRecord, policyTagsStr, capsStr sql.NullString, routingWeightVal sql.NullInt64, updatedAt sql.NullTime) {
	if policyTagsStr.Valid && policyTagsStr.String != "" && policyTagsStr.String != "[]" && policyTagsStr.String != "null" {
		_ = json.Unmarshal([]byte(policyTagsStr.String), &h.PolicyTags)
	}
	if len(h.PolicyTags) == 0 {
		defTags, _ := DefaultHostPolicy(h.Name)
		h.PolicyTags = defTags
	}
	if routingWeightVal.Valid && routingWeightVal.Int64 >= 0 {
		h.RoutingWeight = int(routingWeightVal.Int64)
		h.HasExplicitWeight = true
	} else {
		_, defWeight := DefaultHostPolicy(h.Name)
		h.RoutingWeight = defWeight
	}
	if capsStr.Valid && capsStr.String != "" && capsStr.String != "[]" && capsStr.String != "null" {
		_ = json.Unmarshal([]byte(capsStr.String), &h.DetectedCapabilities)
	}
	if updatedAt.Valid {
		h.UpdatedAt = updatedAt.Time
	}
}

func (d *DB) ListHosts() ([]*HostRecord, error) {
	query := `SELECT name, url, ssh_target, remote_cwd, policy_tags, routing_weight, detected_capabilities, updated_at, created_at FROM hosts ORDER BY name ASC;`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query hosts: %w", err)
	}
	defer rows.Close()

	var hosts []*HostRecord
	for rows.Next() {
		var h HostRecord
		var url, sshTarget, remoteCwd, policyStr, capsStr sql.NullString
		var weightVal sql.NullInt64
		var updatedAt sql.NullTime
		if err := rows.Scan(&h.Name, &url, &sshTarget, &remoteCwd, &policyStr, &weightVal, &capsStr, &updatedAt, &h.CreatedAt); err != nil {
			return nil, err
		}
		if url.Valid {
			h.URL = url.String
		}
		if sshTarget.Valid {
			h.SSHTarget = sshTarget.String
		}
		if remoteCwd.Valid {
			h.RemoteCwd = remoteCwd.String
		}
		hydrateHostRecordDefaults(&h, policyStr, capsStr, weightVal, updatedAt)
		hosts = append(hosts, &h)
	}
	return hosts, nil
}

func (d *DB) GetHost(name string) (*HostRecord, error) {
	query := `SELECT name, url, ssh_target, remote_cwd, policy_tags, routing_weight, detected_capabilities, updated_at, created_at FROM hosts WHERE name = ? LIMIT 1;`
	row := d.db.QueryRow(query, name)
	var h HostRecord
	var url, sshTarget, remoteCwd, policyStr, capsStr sql.NullString
	var weightVal sql.NullInt64
	var updatedAt sql.NullTime
	if err := row.Scan(&h.Name, &url, &sshTarget, &remoteCwd, &policyStr, &weightVal, &capsStr, &updatedAt, &h.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if url.Valid {
		h.URL = url.String
	}
	if sshTarget.Valid {
		h.SSHTarget = sshTarget.String
	}
	if remoteCwd.Valid {
		h.RemoteCwd = remoteCwd.String
	}
	hydrateHostRecordDefaults(&h, policyStr, capsStr, weightVal, updatedAt)
	return &h, nil
}

func (d *DB) RecordHostLoadSample(host string, snap HostLoadSnapshot, tasksSpawnedDelta int, now time.Time) error {
	if host == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	bucketStart := now.Truncate(5 * time.Minute)
	bucketSec := 300

	var avgWork, avgCPU, maxCPU, minRAM float64
	var maxWork, sampleCount, spawnedCount int
	err := d.db.QueryRow(`
		SELECT avg_working_sessions, max_working_sessions, avg_cpu_pct, max_cpu_pct, min_ram_free_gb, sample_count, tasks_spawned_count
		FROM host_load_metrics
		WHERE host = ? AND bucket_start = ? AND bucket_sec = ?;
	`, host, bucketStart, bucketSec).Scan(&avgWork, &maxWork, &avgCPU, &maxCPU, &minRAM, &sampleCount, &spawnedCount)

	if err == sql.ErrNoRows {
		_, insErr := d.db.Exec(`
			INSERT INTO host_load_metrics (host, bucket_start, bucket_sec, avg_working_sessions, max_working_sessions, avg_cpu_pct, max_cpu_pct, min_ram_free_gb, sample_count, tasks_spawned_count)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?);
		`, host, bucketStart, bucketSec, float64(snap.WorkingSessions), snap.WorkingSessions, snap.CPULoadPct, snap.CPULoadPct, snap.RAMFreeGB, tasksSpawnedDelta)
		return insErr
	}
	if err != nil {
		return err
	}

	newSamples := sampleCount
	newAvgWork := avgWork
	newMaxWork := maxWork
	newAvgCPU := avgCPU
	newMaxCPU := maxCPU
	newMinRAM := minRAM
	if tasksSpawnedDelta == 0 {
		newSamples = sampleCount + 1
		newAvgWork = (avgWork*float64(sampleCount) + float64(snap.WorkingSessions)) / float64(newSamples)
		if snap.WorkingSessions > maxWork {
			newMaxWork = snap.WorkingSessions
		}
		newAvgCPU = (avgCPU*float64(sampleCount) + snap.CPULoadPct) / float64(newSamples)
		if snap.CPULoadPct > maxCPU {
			newMaxCPU = snap.CPULoadPct
		}
		if minRAM <= 0 || (snap.RAMFreeGB > 0 && snap.RAMFreeGB < minRAM) {
			newMinRAM = snap.RAMFreeGB
		}
	}
	newSpawned := spawnedCount + tasksSpawnedDelta

	_, updErr := d.db.Exec(`
		UPDATE host_load_metrics
		SET avg_working_sessions = ?, max_working_sessions = ?, avg_cpu_pct = ?, max_cpu_pct = ?, min_ram_free_gb = ?, sample_count = ?, tasks_spawned_count = ?
		WHERE host = ? AND bucket_start = ? AND bucket_sec = ?;
	`, newAvgWork, newMaxWork, newAvgCPU, newMaxCPU, newMinRAM, newSamples, newSpawned, host, bucketStart, bucketSec)
	return updErr
}

func (d *DB) GetHostLoadMetrics(hostFilter string, since time.Time) ([]HostLoadMetric, error) {
	var rows *sql.Rows
	var err error
	if hostFilter != "" {
		rows, err = d.db.Query(`
			SELECT host, bucket_start, bucket_sec, avg_working_sessions, max_working_sessions, avg_cpu_pct, max_cpu_pct, min_ram_free_gb, tasks_spawned_count
			FROM host_load_metrics
			WHERE host = ? AND bucket_start >= ?
			ORDER BY bucket_start ASC;
		`, hostFilter, since.UTC())
	} else {
		rows, err = d.db.Query(`
			SELECT host, bucket_start, bucket_sec, avg_working_sessions, max_working_sessions, avg_cpu_pct, max_cpu_pct, min_ram_free_gb, tasks_spawned_count
			FROM host_load_metrics
			WHERE bucket_start >= ?
			ORDER BY bucket_start ASC, host ASC;
		`, since.UTC())
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []HostLoadMetric
	for rows.Next() {
		var m HostLoadMetric
		if err := rows.Scan(&m.Host, &m.BucketStart, &m.BucketSec, &m.AvgWorkingSessions, &m.MaxWorkingSessions, &m.AvgCPUPct, &m.MaxCPUPct, &m.MinRAMFreeGB, &m.TasksSpawnedCount); err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// RollupAndPruneHostLoadMetrics compacts 5-min buckets older than 7 days into 1-hour buckets and prunes data older than 90 days.
func (d *DB) RollupAndPruneHostLoadMetrics(now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour).Truncate(time.Hour)
	ninetyDaysAgo := now.Add(-90 * 24 * time.Hour)

	// Prune >90d
	_, _ = d.db.Exec("DELETE FROM host_load_metrics WHERE bucket_start < ?;", ninetyDaysAgo)

	// Find 5-min buckets older than 7 days
	rows, err := d.db.Query(`
		SELECT host, bucket_start, avg_working_sessions, max_working_sessions, avg_cpu_pct, max_cpu_pct, min_ram_free_gb, sample_count, tasks_spawned_count
		FROM host_load_metrics
		WHERE bucket_sec = 300 AND bucket_start < ?
		ORDER BY host ASC, bucket_start ASC;
	`, sevenDaysAgo)
	if err != nil {
		return err
	}
	type rawRow struct {
		host      string
		hourStart time.Time
		avgWork   float64
		maxWork   int
		avgCPU    float64
		maxCPU    float64
		minRAM    float64
		samples   int
		spawned   int
	}
	type aggKey struct {
		host string
		hour time.Time
	}
	aggMap := make(map[aggKey]*rawRow)
	for rows.Next() {
		var r rawRow
		var bStart time.Time
		if err := rows.Scan(&r.host, &bStart, &r.avgWork, &r.maxWork, &r.avgCPU, &r.maxCPU, &r.minRAM, &r.samples, &r.spawned); err != nil {
			continue
		}
		if r.samples <= 0 {
			r.samples = 1
		}
		k := aggKey{host: r.host, hour: bStart.UTC().Truncate(time.Hour)}
		if existing, ok := aggMap[k]; !ok {
			r.hourStart = k.hour
			aggMap[k] = &r
		} else {
			totSamples := existing.samples + r.samples
			existing.avgWork = (existing.avgWork*float64(existing.samples) + r.avgWork*float64(r.samples)) / float64(totSamples)
			if r.maxWork > existing.maxWork {
				existing.maxWork = r.maxWork
			}
			existing.avgCPU = (existing.avgCPU*float64(existing.samples) + r.avgCPU*float64(r.samples)) / float64(totSamples)
			if r.maxCPU > existing.maxCPU {
				existing.maxCPU = r.maxCPU
			}
			if existing.minRAM <= 0 || (r.minRAM > 0 && r.minRAM < existing.minRAM) {
				existing.minRAM = r.minRAM
			}
			existing.samples = totSamples
			existing.spawned += r.spawned
		}
	}
	rows.Close()

	if len(aggMap) == 0 {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, a := range aggMap {
		_, _ = tx.Exec(`
			INSERT INTO host_load_metrics (host, bucket_start, bucket_sec, avg_working_sessions, max_working_sessions, avg_cpu_pct, max_cpu_pct, min_ram_free_gb, sample_count, tasks_spawned_count)
			VALUES (?, ?, 3600, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(host, bucket_start, bucket_sec) DO UPDATE SET
				avg_working_sessions = excluded.avg_working_sessions,
				max_working_sessions = MAX(host_load_metrics.max_working_sessions, excluded.max_working_sessions),
				avg_cpu_pct = excluded.avg_cpu_pct,
				max_cpu_pct = MAX(host_load_metrics.max_cpu_pct, excluded.max_cpu_pct),
				min_ram_free_gb = CASE WHEN host_load_metrics.min_ram_free_gb > 0 AND host_load_metrics.min_ram_free_gb < excluded.min_ram_free_gb THEN host_load_metrics.min_ram_free_gb ELSE excluded.min_ram_free_gb END,
				sample_count = excluded.sample_count,
				tasks_spawned_count = host_load_metrics.tasks_spawned_count + excluded.tasks_spawned_count;
		`, a.host, a.hourStart, a.avgWork, a.maxWork, a.avgCPU, a.maxCPU, a.minRAM, a.samples, a.spawned)
	}
	_, _ = tx.Exec("DELETE FROM host_load_metrics WHERE bucket_sec = 300 AND bucket_start < ?;", sevenDaysAgo)
	return tx.Commit()
}

func (d *DB) DeleteHost(name string) error {
	_, err := d.db.Exec("DELETE FROM hosts WHERE name = ?;", name)
	if err != nil {
		return fmt.Errorf("failed to delete host: %w", err)
	}
	return nil
}

// PurgeSessions clears all session records while strictly preserving tree_nodes and hosts
func (d *DB) PurgeSessions() error {
	_, err := d.db.Exec("DELETE FROM sessions;")
	if err != nil {
		return fmt.Errorf("failed to purge sessions: %w", err)
	}
	return nil
}

func (d *DB) MarkSessionDeleted(id string) error {
	if id == "" {
		return nil
	}
	_, err := d.db.Exec("INSERT OR REPLACE INTO deleted_sessions (id, deleted_at) VALUES (?, ?);", id, time.Now())
	if err != nil {
		return fmt.Errorf("failed to record deleted session: %w", err)
	}
	return d.DeleteSession(id)
}

func (d *DB) IsSessionDeleted(id string) bool {
	if id == "" {
		return false
	}
	var count int
	_ = d.db.QueryRow("SELECT COUNT(*) FROM deleted_sessions WHERE id = ?;", id).Scan(&count)
	return count > 0
}

// SaveAccount creates or updates an agent account/profile
func (d *DB) SaveAccount(a *AgentAccount) error {
	if a.ID == "" {
		a.ID = fmt.Sprintf("%s:%s", a.Agent, a.Name)
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	a.UpdatedAt = time.Now()

	var envJSON []byte
	if len(a.Env) > 0 {
		var err error
		envJSON, err = json.Marshal(a.Env)
		if err != nil {
			return fmt.Errorf("failed to marshal env: %w", err)
		}
	}

	isDefaultInt := 0
	if a.IsDefault {
		isDefaultInt = 1
		_, _ = d.db.Exec("UPDATE accounts SET is_default = 0 WHERE agent = ? AND id != ?;", a.Agent, a.ID)
	}

	query := `
	INSERT INTO accounts (id, agent, name, display_name, config_dir, env_json, is_default, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,
		display_name=excluded.display_name,
		config_dir=excluded.config_dir,
		env_json=excluded.env_json,
		is_default=excluded.is_default,
		updated_at=excluded.updated_at;
	`
	displayNameVal := sql.NullString{String: a.DisplayName, Valid: a.DisplayName != ""}
	configDirVal := sql.NullString{String: a.ConfigDir, Valid: a.ConfigDir != ""}
	envJSONVal := sql.NullString{String: string(envJSON), Valid: len(envJSON) > 0}

	_, err := d.db.Exec(query, a.ID, a.Agent, a.Name, displayNameVal, configDirVal, envJSONVal, isDefaultInt, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to save account: %w", err)
	}
	return nil
}

// GetAccount retrieves an account by its unique ID
func (d *DB) GetAccount(id string) (*AgentAccount, error) {
	query := `SELECT id, agent, name, display_name, config_dir, env_json, is_default, created_at, updated_at FROM accounts WHERE id = ?;`
	row := d.db.QueryRow(query, id)

	var a AgentAccount
	var displayNameVal, configDirVal, envJSONVal sql.NullString
	var isDefaultInt int

	err := row.Scan(&a.ID, &a.Agent, &a.Name, &displayNameVal, &configDirVal, &envJSONVal, &isDefaultInt, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to query account: %w", err)
	}

	if displayNameVal.Valid {
		a.DisplayName = displayNameVal.String
	}
	if configDirVal.Valid {
		a.ConfigDir = configDirVal.String
	}
	if envJSONVal.Valid && envJSONVal.String != "" {
		_ = json.Unmarshal([]byte(envJSONVal.String), &a.Env)
	}
	a.IsDefault = isDefaultInt == 1
	return &a, nil
}

// ListAccounts retrieves accounts optionally filtered by agent
func (d *DB) ListAccounts(agentFilter string) ([]*AgentAccount, error) {
	var query string
	var args []interface{}
	if agentFilter != "" {
		query = `SELECT id, agent, name, display_name, config_dir, env_json, is_default, created_at, updated_at FROM accounts WHERE agent = ? ORDER BY is_default DESC, name ASC;`
		args = append(args, agentFilter)
	} else {
		query = `SELECT id, agent, name, display_name, config_dir, env_json, is_default, created_at, updated_at FROM accounts ORDER BY agent ASC, is_default DESC, name ASC;`
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*AgentAccount
	for rows.Next() {
		var a AgentAccount
		var displayNameVal, configDirVal, envJSONVal sql.NullString
		var isDefaultInt int

		if err := rows.Scan(&a.ID, &a.Agent, &a.Name, &displayNameVal, &configDirVal, &envJSONVal, &isDefaultInt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if displayNameVal.Valid {
			a.DisplayName = displayNameVal.String
		}
		if configDirVal.Valid {
			a.ConfigDir = configDirVal.String
		}
		if envJSONVal.Valid && envJSONVal.String != "" {
			_ = json.Unmarshal([]byte(envJSONVal.String), &a.Env)
		}
		a.IsDefault = isDefaultInt == 1
		accounts = append(accounts, &a)
	}
	return accounts, nil
}

// DeleteAccount removes an account by its unique ID
func (d *DB) DeleteAccount(id string) error {
	_, err := d.db.Exec("DELETE FROM accounts WHERE id = ?;", id)
	if err != nil {
		return fmt.Errorf("failed to delete account: %w", err)
	}
	return nil
}

// SetDefaultAccount marks an account as default for its agent
func (d *DB) SetDefaultAccount(agent, id string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE accounts SET is_default = 0 WHERE agent = ?;", agent); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE accounts SET is_default = 1 WHERE id = ? AND agent = ?;", id, agent); err != nil {
		return err
	}
	return tx.Commit()
}

// MigrateLocalSessions updates sessions and deleted_sessions that currently have host = "local"
// (or empty host) to use the new canonical hostName.
func (d *DB) MigrateLocalSessions(newHost string) error {
	if newHost == "" || newHost == "local" {
		return nil
	}
	// Migrate sessions
	querySessions := `
		UPDATE sessions 
		SET host = ?, id = REPLACE(id, ':local:', ':' || ? || ':')
		WHERE host = 'local' OR host = '' OR host IS NULL;
	`
	if _, err := d.db.Exec(querySessions, newHost, newHost); err != nil {
		return fmt.Errorf("failed to migrate sessions host: %w", err)
	}

	// Also update deleted_sessions if any
	queryDeleted := `
		UPDATE deleted_sessions 
		SET id = REPLACE(id, ':local:', ':' || ? || ':')
		WHERE id LIKE '%:local:%';
	`
	_, _ = d.db.Exec(queryDeleted, newHost)

	return nil
}

type Task struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	GroupName       string            `json:"group_name"`
	ProjectName     string            `json:"project_name"`
	SubprojectName  string            `json:"subproject_name,omitempty"`
	Status          string            `json:"status"`
	Substatus       string            `json:"substatus"`
	Notes           string            `json:"notes,omitempty"`
	BlockerQuestion string            `json:"blocker_question,omitempty"`
	Branch          string            `json:"branch,omitempty"`
	WorktreePath    string            `json:"worktree_path,omitempty"`
	PRURL           string            `json:"pr_url,omitempty"`
	PRNumber        int               `json:"pr_number,omitempty"`
	PRState         string            `json:"pr_state,omitempty"`
	CIStatus        string            `json:"ci_status,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	CompletedAt     *time.Time        `json:"completed_at,omitempty"`
	DeletedAt       *time.Time        `json:"deleted_at,omitempty"`
	TaskKind        string            `json:"task_kind"`
	ExternalRefs    []TaskExternalRef `json:"external_refs"`
	Workers         []TaskWorker      `json:"workers"`
	Workstreams     []TaskWorkstream  `json:"workstreams"`
	Deliverables    []TaskDeliverable `json:"deliverables"`
}

type TaskExternalRef struct {
	TaskID  string `json:"task_id"`
	Tracker string `json:"tracker"`
	RefKey  string `json:"ref_key"`
	URL     string `json:"url,omitempty"`
}

type TaskWorker struct {
	TaskID       string    `json:"task_id"`
	SessionID    string    `json:"session_id"`
	Agent        string    `json:"agent"`
	Host         string    `json:"host"`
	IsActive     bool      `json:"is_active"`
	AssignedAt   time.Time `json:"assigned_at"`
	RepoNodePath string    `json:"repo_node_path,omitempty"`
	WorkflowNode string    `json:"workflow_node,omitempty"`
}

type TaskWorkstream struct {
	TaskID       string    `json:"task_id"`
	RepoNodePath string    `json:"repo_node_path"`
	TargetHost   string    `json:"target_host"`
	DependsOn    string    `json:"depends_on,omitempty"`
	WorkflowNode string    `json:"workflow_node,omitempty"`
	Status       string    `json:"status"` // NEW, QUEUED, IN_PROGRESS, REVIEW, DONE
	Branch       string    `json:"branch,omitempty"`
	WorktreePath string    `json:"worktree_path,omitempty"`
	PRURL        string    `json:"pr_url,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TaskDeliverable struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"task_id"`
	Kind         string    `json:"kind"`
	Title        string    `json:"title"`
	Host         string    `json:"host"`
	FilePath     string    `json:"file_path,omitempty"`
	URL          string    `json:"url,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	ContentMD    string    `json:"content_md,omitempty"`
	HasContentMD bool      `json:"has_content_md,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

func (d *DB) GetTasks() ([]Task, error) {
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind FROM tasks WHERE deleted_at IS NULL ORDER BY updated_at DESC;`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	tasks := []Task{}
	var taskIDs []string
	for rows.Next() {
		var t Task
		var subprojectName, notes, blockerQuestion, branch, worktreePath, prURL, prState, ciStatus, taskKindVal sql.NullString
		var prNumber sql.NullInt64
		var completedAt, deletedAt sql.NullTime

		if err := rows.Scan(
			&t.ID, &t.Title, &t.GroupName, &t.ProjectName, &subprojectName,
			&t.Status, &t.Substatus, &notes, &blockerQuestion, &branch, &worktreePath,
			&prURL, &prNumber, &prState, &ciStatus, &t.CreatedAt, &t.UpdatedAt, &completedAt, &deletedAt, &taskKindVal,
		); err != nil {
			return nil, err
		}

		t.SubprojectName = subprojectName.String
		t.Notes = notes.String
		t.BlockerQuestion = blockerQuestion.String
		t.Branch = branch.String
		t.WorktreePath = worktreePath.String
		t.PRURL = prURL.String
		t.PRNumber = int(prNumber.Int64)
		t.PRState = prState.String
		t.CIStatus = ciStatus.String
		t.TaskKind = taskKindVal.String
		if t.TaskKind == "" {
			t.TaskKind = "code"
		}
		if completedAt.Valid {
			t.CompletedAt = &completedAt.Time
		}
		if deletedAt.Valid {
			t.DeletedAt = &deletedAt.Time
		}

		tasks = append(tasks, t)
		taskIDs = append(taskIDs, t.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(tasks) > 0 {
		refs, err := d.getExternalRefsForTasks(taskIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to get external refs: %w", err)
		}
		workers, err := d.getWorkersForTasks(taskIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to get workers: %w", err)
		}
		dels, err := d.getDeliverablesForTasks(taskIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to get deliverables: %w", err)
		}
		workstreams, err := d.getWorkstreamsForTasks(taskIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to get workstreams: %w", err)
		}
		for i, t := range tasks {
			if r, ok := refs[t.ID]; ok {
				tasks[i].ExternalRefs = r
			} else {
				tasks[i].ExternalRefs = []TaskExternalRef{}
			}
			if w, ok := workers[t.ID]; ok {
				tasks[i].Workers = w
			} else {
				tasks[i].Workers = []TaskWorker{}
			}
			if de, ok := dels[t.ID]; ok {
				tasks[i].Deliverables = de
			} else {
				tasks[i].Deliverables = []TaskDeliverable{}
			}
			if ws, ok := workstreams[t.ID]; ok {
				tasks[i].Workstreams = ws
			} else {
				tasks[i].Workstreams = []TaskWorkstream{}
			}
		}
	}

	return tasks, nil
}

type scannableRow interface {
	Scan(dest ...any) error
}

func scanTaskRow(row scannableRow) (*Task, error) {
	var t Task
	var subprojectName, notes, blockerQuestion, branch, worktreePath, prURL, prState, ciStatus, taskKindVal sql.NullString
	var prNumber sql.NullInt64
	var completedAt, deletedAt sql.NullTime

	if err := row.Scan(
		&t.ID, &t.Title, &t.GroupName, &t.ProjectName, &subprojectName,
		&t.Status, &t.Substatus, &notes, &blockerQuestion, &branch, &worktreePath,
		&prURL, &prNumber, &prState, &ciStatus, &t.CreatedAt, &t.UpdatedAt, &completedAt, &deletedAt, &taskKindVal,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan task row: %w", err)
	}

	t.SubprojectName = subprojectName.String
	t.Notes = notes.String
	t.BlockerQuestion = blockerQuestion.String
	t.Branch = branch.String
	t.WorktreePath = worktreePath.String
	t.PRURL = prURL.String
	t.PRNumber = int(prNumber.Int64)
	t.PRState = prState.String
	t.CIStatus = ciStatus.String
	t.TaskKind = taskKindVal.String
	if t.TaskKind == "" {
		t.TaskKind = "code"
	}
	if completedAt.Valid {
		t.CompletedAt = &completedAt.Time
	}
	if deletedAt.Valid {
		t.DeletedAt = &deletedAt.Time
	}
	return &t, nil
}

func (d *DB) populateTaskRelations(t *Task) error {
	taskIDs := []string{t.ID}

	refs, err := d.getExternalRefsForTasks(taskIDs)
	if err != nil {
		return fmt.Errorf("failed to get external refs: %w", err)
	}
	workers, err := d.getWorkersForTasks(taskIDs)
	if err != nil {
		return fmt.Errorf("failed to get workers: %w", err)
	}
	dels, err := d.getDeliverablesForTasks(taskIDs)
	if err != nil {
		return fmt.Errorf("failed to get deliverables: %w", err)
	}
	workstreams, err := d.getWorkstreamsForTasks(taskIDs)
	if err != nil {
		return fmt.Errorf("failed to get workstreams: %w", err)
	}

	if r, ok := refs[t.ID]; ok {
		t.ExternalRefs = r
	} else {
		t.ExternalRefs = []TaskExternalRef{}
	}
	if w, ok := workers[t.ID]; ok {
		t.Workers = w
	} else {
		t.Workers = []TaskWorker{}
	}
	if de, ok := dels[t.ID]; ok {
		t.Deliverables = de
	} else {
		t.Deliverables = []TaskDeliverable{}
	}
	if ws, ok := workstreams[t.ID]; ok {
		t.Workstreams = ws
	} else {
		t.Workstreams = []TaskWorkstream{}
	}
	return nil
}

func (d *DB) GetTaskByID(id string) (*Task, error) {
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind FROM tasks WHERE id = ? AND deleted_at IS NULL;`
	row := d.db.QueryRow(query, id)
	t, err := scanTaskRow(row)
	if err != nil || t == nil {
		return t, err
	}
	if err := d.populateTaskRelations(t); err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) GetTaskByBranch(branch string) (*Task, error) {
	if branch == "" {
		return nil, nil
	}
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind FROM tasks WHERE branch = ? AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1;`
	row := d.db.QueryRow(query, branch)
	t, err := scanTaskRow(row)
	if err != nil || t == nil {
		return t, err
	}
	if err := d.populateTaskRelations(t); err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) GetTaskByWorktree(worktreePath string) (*Task, error) {
	if worktreePath == "" {
		return nil, nil
	}
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind FROM tasks WHERE worktree_path = ? AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1;`
	row := d.db.QueryRow(query, worktreePath)
	t, err := scanTaskRow(row)
	if err != nil || t == nil {
		return t, err
	}
	if err := d.populateTaskRelations(t); err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) GetTaskByExternalRef(refKey string) (*Task, error) {
	cleanKey := strings.TrimSpace(refKey)
	if cleanKey == "" {
		return nil, nil
	}
	query := `SELECT t.id, t.title, t.group_name, t.project_name, t.subproject_name, t.status, t.substatus, t.notes, t.blocker_question, t.branch, t.worktree_path, t.pr_url, t.pr_number, t.pr_state, t.ci_status, t.created_at, t.updated_at, t.completed_at, t.deleted_at, t.task_kind
			  FROM tasks t
			  JOIN task_external_refs r ON t.id = r.task_id
			  WHERE r.ref_key = ? AND t.deleted_at IS NULL
			  ORDER BY t.updated_at DESC LIMIT 1;`
	row := d.db.QueryRow(query, cleanKey)
	t, err := scanTaskRow(row)
	if err != nil || t == nil {
		return t, err
	}
	if err := d.populateTaskRelations(t); err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) GetTaskByTitle(title string) (*Task, error) {
	cleanTitle := strings.TrimSpace(title)
	if cleanTitle == "" {
		return nil, nil
	}
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind FROM tasks WHERE title = ? COLLATE NOCASE AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1;`
	row := d.db.QueryRow(query, cleanTitle)
	t, err := scanTaskRow(row)
	if err != nil || t == nil {
		return t, err
	}
	if err := d.populateTaskRelations(t); err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) GetActiveTaskForSession(sessionID string) (*Task, error) {
	if sessionID == "" {
		return nil, nil
	}
	query := `SELECT t.id, t.title, t.group_name, t.project_name, t.subproject_name, t.status, t.substatus, t.notes, t.blocker_question, t.branch, t.worktree_path, t.pr_url, t.pr_number, t.pr_state, t.ci_status, t.created_at, t.updated_at, t.completed_at, t.deleted_at, t.task_kind
			  FROM tasks t
			  JOIN task_workers w ON t.id = w.task_id
			  WHERE w.session_id = ? AND w.is_active = 1 AND t.deleted_at IS NULL
			  ORDER BY t.updated_at DESC LIMIT 1;`
	row := d.db.QueryRow(query, sessionID)
	t, err := scanTaskRow(row)
	if err != nil || t == nil {
		return t, err
	}
	if err := d.populateTaskRelations(t); err != nil {
		return nil, err
	}
	return t, nil
}

func chunkSlice(slice []string, chunkSize int) [][]string {
	if len(slice) == 0 {
		return nil
	}
	var chunks [][]string
	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}
	return chunks
}

func (d *DB) getExternalRefsForTasks(taskIDs []string) (map[string][]TaskExternalRef, error) {
	res := make(map[string][]TaskExternalRef)
	if len(taskIDs) == 0 {
		return res, nil
	}
	for _, chunk := range chunkSlice(taskIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(`SELECT task_id, tracker, ref_key, url FROM task_external_refs WHERE task_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := d.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r TaskExternalRef
			var url sql.NullString
			if err := rows.Scan(&r.TaskID, &r.Tracker, &r.RefKey, &url); err != nil {
				rows.Close()
				return nil, err
			}
			r.URL = url.String
			res[r.TaskID] = append(res[r.TaskID], r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return res, nil
}

func (d *DB) getWorkersForTasks(taskIDs []string) (map[string][]TaskWorker, error) {
	res := make(map[string][]TaskWorker)
	if len(taskIDs) == 0 {
		return res, nil
	}
	for _, chunk := range chunkSlice(taskIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(`SELECT task_id, session_id, agent, host, is_active, assigned_at, repo_node_path, workflow_node FROM task_workers WHERE task_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := d.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var w TaskWorker
			var isActive int
			var repoNodePath, workflowNode sql.NullString
			if err := rows.Scan(&w.TaskID, &w.SessionID, &w.Agent, &w.Host, &isActive, &w.AssignedAt, &repoNodePath, &workflowNode); err != nil {
				rows.Close()
				return nil, err
			}
			w.IsActive = isActive == 1
			w.RepoNodePath = repoNodePath.String
			w.WorkflowNode = workflowNode.String
			res[w.TaskID] = append(res[w.TaskID], w)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return res, nil
}

func (d *DB) getWorkstreamsForTasks(taskIDs []string) (map[string][]TaskWorkstream, error) {
	res := make(map[string][]TaskWorkstream)
	if len(taskIDs) == 0 {
		return res, nil
	}
	for _, chunk := range chunkSlice(taskIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(`SELECT task_id, repo_node_path, target_host, depends_on, workflow_node, status, branch, worktree_path, pr_url, updated_at FROM task_workstreams WHERE task_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := d.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ws TaskWorkstream
			var targetHost, dependsOn, workflowNode, branch, worktreePath, prURL sql.NullString
			if err := rows.Scan(&ws.TaskID, &ws.RepoNodePath, &targetHost, &dependsOn, &workflowNode, &ws.Status, &branch, &worktreePath, &prURL, &ws.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			ws.TargetHost = targetHost.String
			ws.DependsOn = dependsOn.String
			ws.WorkflowNode = workflowNode.String
			ws.Branch = branch.String
			ws.WorktreePath = worktreePath.String
			ws.PRURL = prURL.String
			res[ws.TaskID] = append(res[ws.TaskID], ws)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return res, nil
}

func insertTaskWorkstreamTx(tx *sql.Tx, ws *TaskWorkstream) error {
	if ws.Status == "" {
		ws.Status = "NEW"
	}
	if ws.UpdatedAt.IsZero() {
		ws.UpdatedAt = time.Now().UTC()
	}
	query := `INSERT INTO task_workstreams (task_id, repo_node_path, target_host, depends_on, workflow_node, status, branch, worktree_path, pr_url, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(task_id, repo_node_path) DO UPDATE SET
			  	target_host = COALESCE(NULLIF(excluded.target_host, ''), task_workstreams.target_host),
			  	depends_on = COALESCE(NULLIF(excluded.depends_on, ''), task_workstreams.depends_on),
			  	workflow_node = COALESCE(NULLIF(excluded.workflow_node, ''), task_workstreams.workflow_node),
			  	status = excluded.status,
			  	branch = COALESCE(NULLIF(excluded.branch, ''), task_workstreams.branch),
			  	worktree_path = COALESCE(NULLIF(excluded.worktree_path, ''), task_workstreams.worktree_path),
			  	pr_url = COALESCE(NULLIF(excluded.pr_url, ''), task_workstreams.pr_url),
			  	updated_at = excluded.updated_at;`
	_, err := tx.Exec(query, ws.TaskID, ws.RepoNodePath, ws.TargetHost, ws.DependsOn, ws.WorkflowNode, ws.Status, ws.Branch, ws.WorktreePath, ws.PRURL, ws.UpdatedAt)
	return err
}

func (d *DB) InsertTaskWorkstream(ws *TaskWorkstream) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTaskWorkstreamTx(tx, ws); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) getDeliverablesForTasks(taskIDs []string) (map[string][]TaskDeliverable, error) {
	res := make(map[string][]TaskDeliverable)
	if len(taskIDs) == 0 {
		return res, nil
	}
	for _, chunk := range chunkSlice(taskIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(`SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, created_at, updated_at FROM task_deliverables WHERE task_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := d.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var del TaskDeliverable
			var fp, u, prov, cMD sql.NullString
			var updAt sql.NullTime
			if err := rows.Scan(&del.ID, &del.TaskID, &del.Kind, &del.Title, &del.Host, &fp, &u, &prov, &cMD, &del.CreatedAt, &updAt); err != nil {
				rows.Close()
				return nil, err
			}
			del.FilePath = fp.String
			del.URL = u.String
			del.Provider = prov.String
			del.ContentMD = cMD.String
			if updAt.Valid {
				del.UpdatedAt = updAt.Time
			}
			res[del.TaskID] = append(res[del.TaskID], del)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return res, nil
}

func (d *DB) CreateTask(t *Task) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now().UTC()
	}
	if t.TaskKind == "" {
		t.TaskKind = "code"
	}

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO tasks (
		id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var completedAt, deletedAt sql.NullTime
	if t.CompletedAt != nil {
		completedAt.Time = *t.CompletedAt
		completedAt.Valid = true
	}
	if t.DeletedAt != nil {
		deletedAt.Time = *t.DeletedAt
		deletedAt.Valid = true
	}

	_, err = tx.Exec(query,
		t.ID, t.Title, t.GroupName, t.ProjectName, t.SubprojectName,
		t.Status, t.Substatus, t.Notes, t.BlockerQuestion, t.Branch, t.WorktreePath,
		t.PRURL, t.PRNumber, t.PRState, t.CIStatus, t.CreatedAt, t.UpdatedAt, completedAt, deletedAt, t.TaskKind,
	)
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	for i := range t.Workers {
		w := &t.Workers[i]
		w.TaskID = t.ID
		if err := insertTaskWorkerTx(tx, w); err != nil {
			return err
		}
	}
	for i := range t.Workstreams {
		ws := &t.Workstreams[i]
		ws.TaskID = t.ID
		if err := insertTaskWorkstreamTx(tx, ws); err != nil {
			return err
		}
	}
	for i := range t.ExternalRefs {
		r := &t.ExternalRefs[i]
		r.TaskID = t.ID
		if err := insertTaskExternalRefTx(tx, r); err != nil {
			return err
		}
	}
	for i := range t.Deliverables {
		del := &t.Deliverables[i]
		del.TaskID = t.ID
		if del.ID == "" {
			del.ID = fmt.Sprintf("del_%d_%d", time.Now().UnixNano(), i)
		}
		if err := insertTaskDeliverableTx(tx, del); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (d *DB) UpdateTask(t *Task) error {
	t.UpdatedAt = time.Now().UTC()
	if t.TaskKind == "" {
		t.TaskKind = "code"
	}

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `UPDATE tasks SET
		title = ?, group_name = ?, project_name = ?, subproject_name = ?, status = ?, substatus = ?,
		notes = ?, blocker_question = ?, branch = ?, worktree_path = ?, pr_url = ?, pr_number = ?,
		pr_state = ?, ci_status = ?, updated_at = ?, completed_at = ?, task_kind = ?, deleted_at = NULL
		WHERE id = ?`

	var completedAt sql.NullTime
	if t.CompletedAt != nil {
		completedAt.Time = *t.CompletedAt
		completedAt.Valid = true
	}

	res, err := tx.Exec(query,
		t.Title, t.GroupName, t.ProjectName, t.SubprojectName, t.Status, t.Substatus,
		t.Notes, t.BlockerQuestion, t.Branch, t.WorktreePath, t.PRURL, t.PRNumber,
		t.PRState, t.CIStatus, t.UpdatedAt, completedAt, t.TaskKind, t.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("task with id %s not found", t.ID)
	}

	// Only update Workers if explicitly provided (non-nil)
	if t.Workers != nil {
		_, err = tx.Exec(`DELETE FROM task_workers WHERE task_id = ?`, t.ID)
		if err != nil {
			return err
		}
		for i := range t.Workers {
			w := &t.Workers[i]
			w.TaskID = t.ID
			if err := insertTaskWorkerTx(tx, w); err != nil {
				return err
			}
		}
	}

	// Only update Workstreams if explicitly provided (non-nil)
	if t.Workstreams != nil {
		_, err = tx.Exec(`DELETE FROM task_workstreams WHERE task_id = ?`, t.ID)
		if err != nil {
			return err
		}
		for i := range t.Workstreams {
			ws := &t.Workstreams[i]
			ws.TaskID = t.ID
			if err := insertTaskWorkstreamTx(tx, ws); err != nil {
				return err
			}
		}
	}

	// Only update ExternalRefs if explicitly provided (non-nil)
	if t.ExternalRefs != nil {
		_, err = tx.Exec(`DELETE FROM task_external_refs WHERE task_id = ?`, t.ID)
		if err != nil {
			return err
		}
		for i := range t.ExternalRefs {
			r := &t.ExternalRefs[i]
			r.TaskID = t.ID
			if err := insertTaskExternalRefTx(tx, r); err != nil {
				return err
			}
		}
	}

	// Only update Deliverables if explicitly provided (non-nil)
	if t.Deliverables != nil {
		// Preserve existing content_md if incoming deliverable has empty ContentMD
		existingMDByID := make(map[string]string)
		existingMDByTitleKind := make(map[string]string)
		rows, err := tx.Query(`SELECT id, title, kind, content_md FROM task_deliverables WHERE task_id = ?`, t.ID)
		if err == nil {
			for rows.Next() {
				var eID, eTitle, eKind string
				var eMD sql.NullString
				if err := rows.Scan(&eID, &eTitle, &eKind, &eMD); err == nil && eMD.Valid {
					existingMDByID[eID] = eMD.String
					existingMDByTitleKind[eTitle+"::"+eKind] = eMD.String
				}
			}
			rows.Close()
		}

		_, err = tx.Exec(`DELETE FROM task_deliverables WHERE task_id = ?`, t.ID)
		if err != nil {
			return err
		}
		for i := range t.Deliverables {
			del := &t.Deliverables[i]
			del.TaskID = t.ID
			if del.ID == "" {
				del.ID = fmt.Sprintf("del_%d_%d", time.Now().UnixNano(), i)
			}
			if strings.TrimSpace(del.ContentMD) == "" {
				if md, ok := existingMDByID[del.ID]; ok && md != "" {
					del.ContentMD = md
				} else if md, ok := existingMDByTitleKind[del.Title+"::"+del.Kind]; ok && md != "" {
					del.ContentMD = md
				}
			}
			if err := insertTaskDeliverableTx(tx, del); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (d *DB) upsertTaskSync(t *Task) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now().UTC()
	}
	if t.TaskKind == "" {
		t.TaskKind = "code"
	}

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO tasks (
		id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
	ON CONFLICT(id) DO UPDATE SET
		title=excluded.title,
		group_name=excluded.group_name,
		project_name=excluded.project_name,
		subproject_name=excluded.subproject_name,
		status=excluded.status,
		substatus=excluded.substatus,
		notes=excluded.notes,
		blocker_question=excluded.blocker_question,
		branch=excluded.branch,
		worktree_path=excluded.worktree_path,
		pr_url=excluded.pr_url,
		pr_number=excluded.pr_number,
		pr_state=excluded.pr_state,
		ci_status=excluded.ci_status,
		updated_at=excluded.updated_at,
		completed_at=excluded.completed_at,
		task_kind=excluded.task_kind,
		deleted_at=NULL;`

	var completedAt sql.NullTime
	if t.CompletedAt != nil {
		completedAt.Time = *t.CompletedAt
		completedAt.Valid = true
	}

	_, err = tx.Exec(query,
		t.ID, t.Title, t.GroupName, t.ProjectName, t.SubprojectName,
		t.Status, t.Substatus, t.Notes, t.BlockerQuestion, t.Branch, t.WorktreePath,
		t.PRURL, t.PRNumber, t.PRState, t.CIStatus, t.CreatedAt, t.UpdatedAt, completedAt, t.TaskKind,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert task sync: %w", err)
	}

	if t.Workers != nil {
		for i := range t.Workers {
			w := &t.Workers[i]
			w.TaskID = t.ID
			if err := insertTaskWorkerTx(tx, w); err != nil {
				return err
			}
		}
	}

	if t.Workstreams != nil {
		for i := range t.Workstreams {
			ws := &t.Workstreams[i]
			ws.TaskID = t.ID
			if err := insertTaskWorkstreamTx(tx, ws); err != nil {
				return err
			}
		}
	}

	if t.ExternalRefs != nil {
		for i := range t.ExternalRefs {
			r := &t.ExternalRefs[i]
			r.TaskID = t.ID
			if err := insertTaskExternalRefTx(tx, r); err != nil {
				return err
			}
		}
	}

	if t.Deliverables != nil {
		for i := range t.Deliverables {
			del := &t.Deliverables[i]
			del.TaskID = t.ID
			if del.ID == "" {
				del.ID = fmt.Sprintf("del_%d_%d", time.Now().UnixNano(), i)
			}
			if err := insertTaskDeliverableTx(tx, del); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// DeleteTask deletes a task and all associated workers, external refs, and deliverables
func (d *DB) DeleteTask(taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}
	now := time.Now().UTC()
	res, err := d.db.Exec(`UPDATE tasks SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL;`, now, now, taskID)
	if err != nil {
		return fmt.Errorf("failed to soft delete task: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("task with id %s not found or already deleted", taskID)
	}
	_, _ = d.db.Exec(`DELETE FROM task_workers WHERE task_id = ?;`, taskID)
	_, _ = d.db.Exec(`DELETE FROM task_workstreams WHERE task_id = ?;`, taskID)
	_, _ = d.db.Exec(`DELETE FROM task_external_refs WHERE task_id = ?;`, taskID)
	_, _ = d.db.Exec(`DELETE FROM task_deliverables WHERE task_id = ?;`, taskID)
	_, _ = d.db.Exec(`UPDATE sessions SET current_task_id = '', metadata_updated_at = ? WHERE current_task_id = ?;`, now, taskID)
	return nil
}

func insertTaskWorkerTx(tx *sql.Tx, w *TaskWorker) error {
	isActive := 0
	if w.IsActive {
		isActive = 1
	}
	if w.AssignedAt.IsZero() {
		w.AssignedAt = time.Now()
	}
	if w.IsActive && w.TaskID != "" && w.SessionID != "" {
		_, _ = tx.Exec(`UPDATE task_workers SET is_active = 0 WHERE task_id = ? AND session_id != ? AND is_active = 1 AND (LOWER(COALESCE(repo_node_path, '')) = LOWER(?) OR COALESCE(repo_node_path, '') = '' OR ? = '');`,
			w.TaskID, w.SessionID, w.RepoNodePath, w.RepoNodePath)
	}
	query := `INSERT INTO task_workers (task_id, session_id, agent, host, is_active, assigned_at, repo_node_path, workflow_node)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(task_id, session_id) DO UPDATE SET
			  is_active = excluded.is_active,
			  agent = excluded.agent,
			  host = excluded.host,
			  repo_node_path = COALESCE(NULLIF(excluded.repo_node_path, ''), task_workers.repo_node_path),
			  workflow_node = COALESCE(NULLIF(excluded.workflow_node, ''), task_workers.workflow_node);`
	if _, err := tx.Exec(query, w.TaskID, w.SessionID, w.Agent, w.Host, isActive, w.AssignedAt, w.RepoNodePath, w.WorkflowNode); err != nil {
		return err
	}
	if w.SessionID != "" && w.TaskID != "" {
		_, _ = tx.Exec(`UPDATE sessions SET current_task_id = ?, metadata_updated_at = ? WHERE id = ? OR native_id = ?;`, w.TaskID, time.Now().UTC(), w.SessionID, w.SessionID)
	}
	return nil
}

func insertTaskExternalRefTx(tx *sql.Tx, r *TaskExternalRef) error {
	query := `INSERT INTO task_external_refs (task_id, tracker, ref_key, url)
			  VALUES (?, ?, ?, ?)
			  ON CONFLICT(task_id, tracker, ref_key) DO UPDATE SET url = excluded.url`
	_, err := tx.Exec(query, r.TaskID, r.Tracker, r.RefKey, r.URL)
	return err
}

func insertTaskDeliverableTx(tx *sql.Tx, td *TaskDeliverable) error {
	if td.ID == "" {
		td.ID = fmt.Sprintf("del_%d", time.Now().UnixNano())
	}
	if td.CreatedAt.IsZero() {
		td.CreatedAt = time.Now().UTC()
	}
	if td.UpdatedAt.IsZero() {
		td.UpdatedAt = time.Now().UTC()
	}

	// Deduplicate deliverable for same task and file path or url or (kind, title)
	if td.FilePath != "" {
		var existingID string
		err := tx.QueryRow(`SELECT id FROM task_deliverables WHERE task_id = ? AND file_path = ?`, td.TaskID, td.FilePath).Scan(&existingID)
		if err == nil && existingID != "" {
			td.ID = existingID
			_, err = tx.Exec(`UPDATE task_deliverables SET kind = ?, title = ?, host = ?, provider = ?, content_md = ?, updated_at = ? WHERE id = ?`, td.Kind, td.Title, td.Host, td.Provider, td.ContentMD, td.UpdatedAt, existingID)
			return err
		}
	} else if td.URL != "" {
		var existingID string
		err := tx.QueryRow(`SELECT id FROM task_deliverables WHERE task_id = ? AND url = ?`, td.TaskID, td.URL).Scan(&existingID)
		if err == nil && existingID != "" {
			td.ID = existingID
			_, err = tx.Exec(`UPDATE task_deliverables SET kind = ?, title = ?, host = ?, provider = ?, content_md = ?, updated_at = ? WHERE id = ?`, td.Kind, td.Title, td.Host, td.Provider, td.ContentMD, td.UpdatedAt, existingID)
			return err
		}
	} else {
		var existingID string
		err := tx.QueryRow(`SELECT id FROM task_deliverables WHERE task_id = ? AND kind = ? AND title = ?`, td.TaskID, td.Kind, td.Title).Scan(&existingID)
		if err == nil && existingID != "" {
			td.ID = existingID
			_, err = tx.Exec(`UPDATE task_deliverables SET host = ?, provider = ?, content_md = ?, updated_at = ? WHERE id = ?`, td.Host, td.Provider, td.ContentMD, td.UpdatedAt, existingID)
			return err
		}
	}

	query := `INSERT INTO task_deliverables (id, task_id, kind, title, host, file_path, url, provider, content_md, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  	kind = excluded.kind,
			  	title = excluded.title,
			  	host = excluded.host,
			  	file_path = excluded.file_path,
			  	url = excluded.url,
			  	provider = excluded.provider,
			  	content_md = excluded.content_md,
			  	updated_at = excluded.updated_at;`
	_, err := tx.Exec(query, td.ID, td.TaskID, td.Kind, td.Title, td.Host, td.FilePath, td.URL, td.Provider, td.ContentMD, td.CreatedAt, td.UpdatedAt)
	return err
}

func (d *DB) InsertTaskWorker(w *TaskWorker) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTaskWorkerTx(tx, w); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) ReassignWorkerSessionID(oldSessionID, newSessionID string) error {
	_, err := d.db.Exec(`UPDATE task_workers SET session_id = ? WHERE session_id = ?`, newSessionID, oldSessionID)
	return err
}

func (d *DB) InsertTaskExternalRef(r *TaskExternalRef) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTaskExternalRefTx(tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) InsertTaskDeliverable(td *TaskDeliverable) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTaskDeliverableTx(tx, td); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) GetTaskDeliverableByID(id string) (*TaskDeliverable, error) {
	if id == "" {
		return nil, nil
	}
	query := `SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, created_at, updated_at
			  FROM task_deliverables WHERE id = ?;`
	row := d.db.QueryRow(query, id)
	var del TaskDeliverable
	var filePath, urlVal, provider, contentMD sql.NullString
	var updAt sql.NullTime
	if err := row.Scan(&del.ID, &del.TaskID, &del.Kind, &del.Title, &del.Host, &filePath, &urlVal, &provider, &contentMD, &del.CreatedAt, &updAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if filePath.Valid {
		del.FilePath = filePath.String
	}
	if urlVal.Valid {
		del.URL = urlVal.String
	}
	if provider.Valid {
		del.Provider = provider.String
	}
	if contentMD.Valid {
		del.ContentMD = contentMD.String
		del.HasContentMD = strings.TrimSpace(del.ContentMD) != ""
	}
	if updAt.Valid {
		del.UpdatedAt = updAt.Time
	}
	return &del, nil
}

// PruneAllLegacyTasksOnce executes a one-time soft-delete migration for legacy ambient tasks.
// Returns count of soft-deleted tasks.
func (d *DB) PruneAllLegacyTasksOnce() (int, error) {
	var val string
	err := d.db.QueryRow("SELECT value FROM settings WHERE key = 'legacy_tasks_pruned_v1'").Scan(&val)
	if err == nil && val == "true" {
		return 0, nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin prune transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	res, err := tx.Exec("UPDATE tasks SET deleted_at = ?, updated_at = ? WHERE deleted_at IS NULL;", now, now)
	if err != nil {
		return 0, fmt.Errorf("failed to soft-delete tasks: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()

	if _, err := tx.Exec("DELETE FROM task_workers;"); err != nil {
		return 0, fmt.Errorf("failed to clear task_workers: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM task_workstreams;"); err != nil {
		return 0, fmt.Errorf("failed to clear task_workstreams: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM task_deliverables;"); err != nil {
		return 0, fmt.Errorf("failed to clear task_deliverables: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM task_external_refs;"); err != nil {
		return 0, fmt.Errorf("failed to clear task_external_refs: %w", err)
	}
	if _, err := tx.Exec("UPDATE sessions SET current_task_id = '', metadata_updated_at = ? WHERE current_task_id != '' AND current_task_id IS NOT NULL;", now); err != nil {
		return 0, fmt.Errorf("failed to clear sessions current_task_id: %w", err)
	}

	if _, err := tx.Exec("INSERT INTO settings (key, value) VALUES ('legacy_tasks_pruned_v1', 'true') ON CONFLICT(key) DO UPDATE SET value = 'true';"); err != nil {
		return 0, fmt.Errorf("failed to set legacy_tasks_pruned_v1: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit prune transaction: %w", err)
	}

	return int(rowsAffected), nil
}

// DeduplicateTasks removes redundant duplicate tasks sharing the same external reference
// or identical title and project when branch is empty, consolidating workers and deliverables.
// Invariant: Tasks with different non-empty git branches are NEVER merged or deleted.
func (d *DB) DeduplicateTasks() (int64, error) {
	var totalDeleted int64

	// 1. Group tasks by tracker and ref_key
	query := `SELECT r.tracker, r.ref_key, count(DISTINCT r.task_id) as cnt
			  FROM task_external_refs r
			  JOIN tasks t ON r.task_id = t.id
			  WHERE r.ref_key != ''
			  GROUP BY r.tracker, r.ref_key
			  HAVING cnt > 1;`
	rows, err := d.db.Query(query)
	if err == nil {
		type dupRef struct {
			tracker string
			refKey  string
		}
		var dupRefs []dupRef
		for rows.Next() {
			var dr dupRef
			var cnt int
			if err := rows.Scan(&dr.tracker, &dr.refKey, &cnt); err == nil {
				dupRefs = append(dupRefs, dr)
			}
		}
		rows.Close()

		for _, dr := range dupRefs {
			taskQuery := `SELECT t.id, t.title, t.group_name, t.project_name, t.subproject_name, t.status, t.substatus, t.notes, t.blocker_question, t.branch, t.worktree_path, t.pr_url, t.pr_number, t.pr_state, t.ci_status, t.created_at, t.updated_at, t.completed_at, t.deleted_at, t.task_kind
						  FROM tasks t
						  JOIN task_external_refs r ON t.id = r.task_id
						  WHERE r.tracker = ? AND r.ref_key = ?;`
			tRows, err := d.db.Query(taskQuery, dr.tracker, dr.refKey)
			if err != nil {
				continue
			}
			var tasks []*Task
			seenIDs := make(map[string]bool)
			for tRows.Next() {
				t, err := scanTaskRow(tRows)
				if err == nil && t != nil && !seenIDs[t.ID] {
					seenIDs[t.ID] = true
					tasks = append(tasks, t)
				}
			}
			tRows.Close()

			if len(tasks) <= 1 {
				continue
			}

			// Cluster tasks into groups that can be safely deduplicated.
			// Invariant: Tasks with different non-empty git branches MUST NEVER be in the same cluster.
			clusters := clusterTasksForDeduplication(tasks, dr.refKey)

			for _, cluster := range clusters {
				if len(cluster) <= 1 {
					continue
				}

				sortTasksForPrimary(d, cluster)
				primary := cluster[0]

				for _, dup := range cluster[1:] {
					if primary.Branch == "" && dup.Branch != "" {
						primary.Branch = dup.Branch
					}
					if primary.WorktreePath == "" && dup.WorktreePath != "" {
						primary.WorktreePath = dup.WorktreePath
					}
					if primary.Notes == "" && dup.Notes != "" {
						primary.Notes = dup.Notes
					}
					if primary.PRURL == "" && dup.PRURL != "" {
						primary.PRURL = dup.PRURL
						primary.PRNumber = dup.PRNumber
						primary.PRState = dup.PRState
					}

					_, _ = d.db.Exec(`UPDATE OR IGNORE task_workers SET task_id = ? WHERE task_id = ?`, primary.ID, dup.ID)
					_, _ = d.db.Exec(`UPDATE OR IGNORE task_workstreams SET task_id = ? WHERE task_id = ?`, primary.ID, dup.ID)
					_, _ = d.db.Exec(`UPDATE OR IGNORE task_deliverables SET task_id = ? WHERE task_id = ?`, primary.ID, dup.ID)
					_, _ = d.db.Exec(`DELETE FROM task_workers WHERE task_id = ?`, dup.ID)
					_, _ = d.db.Exec(`DELETE FROM task_workstreams WHERE task_id = ?`, dup.ID)
					_, _ = d.db.Exec(`DELETE FROM task_deliverables WHERE task_id = ?`, dup.ID)
					_, _ = d.db.Exec(`DELETE FROM task_external_refs WHERE task_id = ?`, dup.ID)
					res, err := d.db.Exec(`DELETE FROM tasks WHERE id = ?`, dup.ID)
					if err == nil {
						n, _ := res.RowsAffected()
						totalDeleted += n
					}
				}
				_ = d.UpdateTask(primary)
			}
		}
	}

	// 2. Deduplicate empty-branch tasks with identical (title, group_name, project_name)
	titleDupQuery := `SELECT title, group_name, project_name, count(*) as cnt
					  FROM tasks
					  WHERE (branch = '' OR branch IS NULL)
					  GROUP BY lower(title), lower(group_name), lower(project_name)
					  HAVING cnt > 1;`
	titleRows, err := d.db.Query(titleDupQuery)
	if err == nil {
		type titleDup struct {
			title       string
			groupName   string
			projectName string
		}
		var dupTitles []titleDup
		for titleRows.Next() {
			var td titleDup
			var cnt int
			if err := titleRows.Scan(&td.title, &td.groupName, &td.projectName, &cnt); err == nil {
				dupTitles = append(dupTitles, td)
			}
		}
		titleRows.Close()

		for _, td := range dupTitles {
			tRows, err := d.db.Query(`SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind
									  FROM tasks
									  WHERE lower(title) = lower(?) AND lower(group_name) = lower(?) AND lower(project_name) = lower(?) AND (branch = '' OR branch IS NULL)
									  ORDER BY 
										(SELECT count(*) FROM task_workers WHERE task_id = tasks.id) DESC,
										(SELECT count(*) FROM task_deliverables WHERE task_id = tasks.id) DESC,
										created_at ASC;`, td.title, td.groupName, td.projectName)
			if err != nil {
				continue
			}
			var tasks []*Task
			for tRows.Next() {
				t, err := scanTaskRow(tRows)
				if err == nil && t != nil {
					tasks = append(tasks, t)
				}
			}
			tRows.Close()

			if len(tasks) <= 1 {
				continue
			}

			primary := tasks[0]
			for _, dup := range tasks[1:] {
				if primary.Notes == "" && dup.Notes != "" {
					primary.Notes = dup.Notes
				}
				_, _ = d.db.Exec(`UPDATE OR IGNORE task_workers SET task_id = ? WHERE task_id = ?`, primary.ID, dup.ID)
				_, _ = d.db.Exec(`UPDATE OR IGNORE task_workstreams SET task_id = ? WHERE task_id = ?`, primary.ID, dup.ID)
				_, _ = d.db.Exec(`UPDATE OR IGNORE task_deliverables SET task_id = ? WHERE task_id = ?`, primary.ID, dup.ID)
				_, _ = d.db.Exec(`DELETE FROM task_workers WHERE task_id = ?`, dup.ID)
				_, _ = d.db.Exec(`DELETE FROM task_workstreams WHERE task_id = ?`, dup.ID)
				_, _ = d.db.Exec(`DELETE FROM task_deliverables WHERE task_id = ?`, dup.ID)
				_, _ = d.db.Exec(`DELETE FROM task_external_refs WHERE task_id = ?`, dup.ID)
				res, err := d.db.Exec(`DELETE FROM tasks WHERE id = ?`, dup.ID)
				if err == nil {
					n, _ := res.RowsAffected()
					totalDeleted += n
				}
			}
			_ = d.UpdateTask(primary)
		}
	}

	return totalDeleted, nil
}

// clusterTasksForDeduplication groups tasks sharing a refKey into safe deduplication clusters.
// Invariant: Two tasks with different non-empty git branches are NEVER placed in the same cluster.
func clusterTasksForDeduplication(tasks []*Task, refKey string) [][]*Task {
	cleanRef := strings.ToUpper(strings.TrimSpace(refKey))
	branchMap := make(map[string][]*Task)
	var emptyBranchTasks []*Task

	for _, t := range tasks {
		b := strings.TrimSpace(t.Branch)
		if b != "" {
			branchMap[b] = append(branchMap[b], t)
		} else {
			emptyBranchTasks = append(emptyBranchTasks, t)
		}
	}

	// Identify which branch clusters match the refKey (in branch name or task title)
	var matchingBranches []string
	for b, bTasks := range branchMap {
		bUpper := strings.ToUpper(b)
		matches := false
		if cleanRef != "" && strings.Contains(bUpper, cleanRef) {
			matches = true
		} else if issueKey := ExtractIssueKey(b); issueKey != "" && strings.EqualFold(issueKey, cleanRef) {
			matches = true
		} else {
			// Check if any task in this branch cluster has the refKey in its title
			for _, bt := range bTasks {
				if cleanRef != "" && strings.Contains(strings.ToUpper(bt.Title), cleanRef) {
					matches = true
					break
				}
			}
		}
		if matches {
			matchingBranches = append(matchingBranches, b)
		}
	}

	// Distribute empty-branch tasks
	if len(emptyBranchTasks) > 0 {
		var targetBranch string
		if len(matchingBranches) == 1 {
			// Exactly one branch cluster clearly corresponds to this refKey
			targetBranch = matchingBranches[0]
		} else if len(branchMap) == 1 && len(matchingBranches) == 0 {
			// Only one branch cluster exists overall for this refKey
			for b := range branchMap {
				targetBranch = b
			}
		}

		if targetBranch != "" {
			branchMap[targetBranch] = append(branchMap[targetBranch], emptyBranchTasks...)
		} else {
			// Cannot assign empty-branch tasks to any single branch without ambiguity.
			// Group empty-branch tasks by (group_name, project_name) so they can still
			// deduplicate among themselves without destroying any branch task.
			groupProjMap := make(map[string][]*Task)
			for _, et := range emptyBranchTasks {
				key := strings.ToLower(et.GroupName) + "|" + strings.ToLower(et.ProjectName)
				groupProjMap[key] = append(groupProjMap[key], et)
			}
			var result [][]*Task
			for _, cluster := range branchMap {
				result = append(result, cluster)
			}
			for _, cluster := range groupProjMap {
				result = append(result, cluster)
			}
			return result
		}
	}

	var result [][]*Task
	for _, cluster := range branchMap {
		result = append(result, cluster)
	}
	return result
}

func sortTasksForPrimary(d *DB, cluster []*Task) {
	statusWeight := func(status string) int {
		switch strings.ToUpper(status) {
		case "REVIEW":
			return 4
		case "DONE":
			return 3
		case "IN_PROGRESS":
			return 2
		case "NEW":
			return 1
		default:
			return 0
		}
	}

	sort.SliceStable(cluster, func(i, j int) bool {
		ti, tj := cluster[i], cluster[j]

		// 1. Task with non-empty branch first
		iHasBranch := strings.TrimSpace(ti.Branch) != ""
		jHasBranch := strings.TrimSpace(tj.Branch) != ""
		if iHasBranch != jHasBranch {
			return iHasBranch
		}

		// 2. Task with PR URL first
		iHasPR := strings.TrimSpace(ti.PRURL) != ""
		jHasPR := strings.TrimSpace(tj.PRURL) != ""
		if iHasPR != jHasPR {
			return iHasPR
		}

		// 3. Status priority: REVIEW > DONE > IN_PROGRESS > NEW
		iScore := statusWeight(ti.Status)
		jScore := statusWeight(tj.Status)
		if iScore != jScore {
			return iScore > jScore
		}

		// 4. More workers count
		var iWorkers, jWorkers int
		_ = d.db.QueryRow(`SELECT count(*) FROM task_workers WHERE task_id = ?`, ti.ID).Scan(&iWorkers)
		_ = d.db.QueryRow(`SELECT count(*) FROM task_workers WHERE task_id = ?`, tj.ID).Scan(&jWorkers)
		if iWorkers != jWorkers {
			return iWorkers > jWorkers
		}

		// 5. More deliverables count
		var iDelivs, jDelivs int
		_ = d.db.QueryRow(`SELECT count(*) FROM task_deliverables WHERE task_id = ?`, ti.ID).Scan(&iDelivs)
		_ = d.db.QueryRow(`SELECT count(*) FROM task_deliverables WHERE task_id = ?`, tj.ID).Scan(&jDelivs)
		if iDelivs != jDelivs {
			return iDelivs > jDelivs
		}

		// 6. Earliest created_at
		return ti.CreatedAt.Before(tj.CreatedAt)
	})
}

type SyncStatePayload struct {
	SourceHost    string            `json:"source_host"`
	Timestamp     time.Time         `json:"timestamp"`
	Nodes         []TreeNode        `json:"nodes,omitempty"`
	NodeHostPaths []NodeHostPath    `json:"node_host_paths,omitempty"`
	Tasks         []Task            `json:"tasks,omitempty"`
	Workstreams   []TaskWorkstream  `json:"workstreams,omitempty"`
	Deliverables  []TaskDeliverable `json:"deliverables,omitempty"`
}

func (d *DB) GetSyncStateSince(since time.Time, sourceHost string) (*SyncStatePayload, error) {
	payload := &SyncStatePayload{
		SourceHost: sourceHost,
		Timestamp:  time.Now().UTC(),
	}

	// 1. Nodes (including soft-deleted)
	nodeRows, err := d.db.Query("SELECT path, project_dir, git_url, created_at, updated_at, deleted_at FROM tree_nodes;")
	if err != nil {
		return nil, fmt.Errorf("failed to query tree_nodes for sync: %w", err)
	}
	defer nodeRows.Close()

	for nodeRows.Next() {
		var n TreeNode
		var pDir, gURL sql.NullString
		var updAt, delAt sql.NullTime
		if err := nodeRows.Scan(&n.Path, &pDir, &gURL, &n.CreatedAt, &updAt, &delAt); err != nil {
			return nil, err
		}
		if pDir.Valid {
			n.ProjectDir = pDir.String
		}
		if gURL.Valid {
			n.GitURL = gURL.String
		}
		if updAt.Valid {
			n.UpdatedAt = updAt.Time
		}
		if delAt.Valid {
			n.DeletedAt = &delAt.Time
		}
		if since.IsZero() || n.UpdatedAt.After(since) {
			nhps, _ := d.ListNodeHostPaths(n.Path)
			n.HostPaths = nhps
			payload.Nodes = append(payload.Nodes, n)
		}
	}
	if err := nodeRows.Err(); err != nil {
		return nil, err
	}

	// 2. NodeHostPaths
	allHPs, err := d.ListNodeHostPaths("")
	if err == nil {
		for _, hp := range allHPs {
			if since.IsZero() || hp.UpdatedAt.After(since) {
				payload.NodeHostPaths = append(payload.NodeHostPaths, hp)
			}
		}
	}

	// 3. Tasks (including soft-deleted)
	tRows, err := d.db.Query("SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind FROM tasks;")
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks for sync: %w", err)
	}
	defer tRows.Close()

	for tRows.Next() {
		t, err := scanTaskRow(tRows)
		if err != nil || t == nil {
			continue
		}
		if since.IsZero() || t.UpdatedAt.After(since) {
			if t.DeletedAt == nil {
				_ = d.populateTaskRelations(t)
			}
			payload.Tasks = append(payload.Tasks, *t)
		}
	}

	// 3b. TaskWorkstreams
	wsRows, err := d.db.Query("SELECT task_id, repo_node_path, target_host, depends_on, workflow_node, status, branch, worktree_path, pr_url, updated_at FROM task_workstreams;")
	if err == nil {
		for wsRows.Next() {
			var ws TaskWorkstream
			var targetHost, dependsOn, workflowNode, branch, worktreePath, prURL sql.NullString
			if err := wsRows.Scan(&ws.TaskID, &ws.RepoNodePath, &targetHost, &dependsOn, &workflowNode, &ws.Status, &branch, &worktreePath, &prURL, &ws.UpdatedAt); err == nil {
				ws.TargetHost = targetHost.String
				ws.DependsOn = dependsOn.String
				ws.WorkflowNode = workflowNode.String
				ws.Branch = branch.String
				ws.WorktreePath = worktreePath.String
				ws.PRURL = prURL.String
				if since.IsZero() || ws.UpdatedAt.After(since) {
					payload.Workstreams = append(payload.Workstreams, ws)
				}
			}
		}
		wsRows.Close()
	}

	// 4. TaskDeliverables
	delRows, err := d.db.Query("SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, created_at, updated_at FROM task_deliverables;")
	if err == nil {
		for delRows.Next() {
			var del TaskDeliverable
			var fp, u, prov, cMD sql.NullString
			var updAt sql.NullTime
			if err := delRows.Scan(&del.ID, &del.TaskID, &del.Kind, &del.Title, &del.Host, &fp, &u, &prov, &cMD, &del.CreatedAt, &updAt); err == nil {
				if fp.Valid {
					del.FilePath = fp.String
				}
				if u.Valid {
					del.URL = u.String
				}
				if prov.Valid {
					del.Provider = prov.String
				}
				if cMD.Valid {
					del.ContentMD = cMD.String
				}
				if updAt.Valid {
					del.UpdatedAt = updAt.Time
				}
				if since.IsZero() || del.UpdatedAt.After(since) {
					payload.Deliverables = append(payload.Deliverables, del)
				}
			}
		}
		delRows.Close()
	}

	return payload, nil
}

func (d *DB) ApplySyncState(payload *SyncStatePayload) ([]*Session, error) {
	if payload == nil {
		return nil, nil
	}

	var updatedSessions []*Session

	// 1. Reconcile Nodes via LWW
	for _, incoming := range payload.Nodes {
		var existingUpdAt, existingDelAt sql.NullTime
		err := d.db.QueryRow("SELECT updated_at, deleted_at FROM tree_nodes WHERE path = ?;", incoming.Path).Scan(&existingUpdAt, &existingDelAt)
		exists := err == nil
		if exists && existingUpdAt.Valid && !incoming.UpdatedAt.After(existingUpdAt.Time) {
			// Existing is newer or equal, ignore incoming
			continue
		}

		if incoming.DeletedAt != nil {
			// Incoming is soft-deleted: soft-delete this node and any descendants
			qDelNodes := `UPDATE tree_nodes SET deleted_at = ?, updated_at = ? WHERE (path = ? OR SUBSTR(path, 1, LENGTH(?) + 1) = ? || '/') AND (deleted_at IS NULL OR updated_at < ?);`
			_, _ = d.db.Exec(qDelNodes, incoming.DeletedAt, incoming.UpdatedAt, incoming.Path, incoming.Path, incoming.Path, incoming.UpdatedAt)
			// Clear node_path on sessions for this path and any descendants only if not updated more recently
			qClearSess := `UPDATE sessions SET node_path = '', metadata_updated_at = ? WHERE (node_path = ? OR SUBSTR(node_path, 1, LENGTH(?) + 1) = ? || '/') AND (metadata_updated_at IS NULL OR metadata_updated_at < ?);`
			_, _ = d.db.Exec(qClearSess, incoming.UpdatedAt, incoming.Path, incoming.Path, incoming.Path, incoming.UpdatedAt)
			_, _ = d.db.Exec("DELETE FROM node_host_paths WHERE (node_path = ? OR SUBSTR(node_path, 1, LENGTH(?) + 1) = ? || '/') AND updated_at <= ?;", incoming.Path, incoming.Path, incoming.Path, incoming.UpdatedAt)
		} else {
			// Incoming is active
			q := `
			INSERT INTO tree_nodes (path, project_dir, git_url, created_at, updated_at, deleted_at)
			VALUES (?, ?, ?, ?, ?, NULL)
			ON CONFLICT(path) DO UPDATE SET
				project_dir=excluded.project_dir,
				git_url=excluded.git_url,
				updated_at=excluded.updated_at,
				deleted_at=NULL;
			`
			_, _ = d.db.Exec(q, incoming.Path, incoming.ProjectDir, incoming.GitURL, incoming.CreatedAt, incoming.UpdatedAt)

			for _, hp := range incoming.HostPaths {
				_ = d.UpsertNodeHostPathLWW(&hp)
			}
		}
	}

	// 2. Reconcile NodeHostPaths via LWW
	for _, incoming := range payload.NodeHostPaths {
		_ = d.UpsertNodeHostPathLWW(&incoming)
	}

	// 3. Reconcile Tasks via LWW
	for _, incoming := range payload.Tasks {
		var existingUpdAt, existingDelAt sql.NullTime
		err := d.db.QueryRow("SELECT updated_at, deleted_at FROM tasks WHERE id = ?;", incoming.ID).Scan(&existingUpdAt, &existingDelAt)
		exists := err == nil
		if exists && existingUpdAt.Valid && !incoming.UpdatedAt.After(existingUpdAt.Time) {
			continue
		}

		if incoming.DeletedAt != nil {
			if exists {
				_, _ = d.db.Exec("UPDATE tasks SET deleted_at = ?, updated_at = ? WHERE id = ?;", incoming.DeletedAt, incoming.UpdatedAt, incoming.ID)
			} else {
				q := `INSERT INTO tasks (
					id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at, deleted_at, task_kind
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`
				var compAt sql.NullTime
				if incoming.CompletedAt != nil {
					compAt = sql.NullTime{Time: *incoming.CompletedAt, Valid: true}
				}
				taskKind := incoming.TaskKind
				if taskKind == "" {
					taskKind = "code"
				}
				_, _ = d.db.Exec(q,
					incoming.ID, incoming.Title, incoming.GroupName, incoming.ProjectName, incoming.SubprojectName,
					incoming.Status, incoming.Substatus, incoming.Notes, incoming.BlockerQuestion, incoming.Branch, incoming.WorktreePath,
					incoming.PRURL, incoming.PRNumber, incoming.PRState, incoming.CIStatus, incoming.CreatedAt, incoming.UpdatedAt, compAt, incoming.DeletedAt, taskKind)
			}
			_, _ = d.db.Exec(`DELETE FROM task_workers WHERE task_id = ?;`, incoming.ID)
			_, _ = d.db.Exec(`DELETE FROM task_workstreams WHERE task_id = ?;`, incoming.ID)
			_, _ = d.db.Exec(`DELETE FROM task_external_refs WHERE task_id = ?;`, incoming.ID)
			_, _ = d.db.Exec(`DELETE FROM task_deliverables WHERE task_id = ?;`, incoming.ID)
		} else {
			_ = d.upsertTaskSync(&incoming)
		}
	}

	// 4. Reconcile Workstreams via LWW
	for _, incoming := range payload.Workstreams {
		var existingUpdAt sql.NullTime
		err := d.db.QueryRow("SELECT updated_at FROM task_workstreams WHERE task_id = ? AND repo_node_path = ?;", incoming.TaskID, incoming.RepoNodePath).Scan(&existingUpdAt)
		if err == nil && existingUpdAt.Valid && !incoming.UpdatedAt.After(existingUpdAt.Time) {
			continue
		}
		var taskCount int
		_ = d.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE id = ?;", incoming.TaskID).Scan(&taskCount)
		if taskCount > 0 {
			_ = d.InsertTaskWorkstream(&incoming)
		}
	}

	// 5. Reconcile Deliverables via LWW
	for _, incoming := range payload.Deliverables {
		var existingUpdAt sql.NullTime
		err := d.db.QueryRow("SELECT updated_at FROM task_deliverables WHERE id = ?;", incoming.ID).Scan(&existingUpdAt)
		if err == nil && existingUpdAt.Valid && !incoming.UpdatedAt.After(existingUpdAt.Time) {
			continue
		}
		// Check parent task exists
		var taskCount int
		_ = d.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE id = ?;", incoming.TaskID).Scan(&taskCount)
		if taskCount > 0 {
			_ = d.InsertTaskDeliverable(&incoming)
		}
	}

	return updatedSessions, nil
}

type SpeechSummary struct {
	SessionID      string    `json:"session_id"`
	MessageIndex   int       `json:"message_index"`
	SpokenText     string    `json:"spoken_text"`
	SuggestedChips []string  `json:"suggested_chips"`
	Tier           string    `json:"tier,omitempty"`
	Cached         bool      `json:"cached,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func (d *DB) GetSpeechSummary(sessionID string, messageIndex int) (*SpeechSummary, error) {
	row := d.db.QueryRow("SELECT session_id, message_index, spoken_text, suggested_chips, created_at FROM speech_summaries WHERE session_id = ? AND message_index = ?", sessionID, messageIndex)

	var s SpeechSummary
	var chipsJSON string
	var createdAt time.Time
	if err := row.Scan(&s.SessionID, &s.MessageIndex, &s.SpokenText, &chipsJSON, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan speech summary: %w", err)
	}

	s.CreatedAt = createdAt
	s.SuggestedChips = []string{}
	if chipsJSON != "" {
		_ = json.Unmarshal([]byte(chipsJSON), &s.SuggestedChips)
	}
	return &s, nil
}

func (d *DB) SaveSpeechSummary(summary *SpeechSummary) error {
	if summary == nil || summary.SessionID == "" {
		return fmt.Errorf("invalid speech summary")
	}

	chipsJSON, err := json.Marshal(summary.SuggestedChips)
	if err != nil {
		chipsJSON = []byte("[]")
	}

	createdAt := summary.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	query := `
		INSERT INTO speech_summaries (session_id, message_index, spoken_text, suggested_chips, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id, message_index) DO UPDATE SET
			spoken_text = excluded.spoken_text,
			suggested_chips = excluded.suggested_chips,
			created_at = excluded.created_at;
	`
	_, err = d.db.Exec(query, summary.SessionID, summary.MessageIndex, summary.SpokenText, string(chipsJSON), createdAt)
	if err != nil {
		return fmt.Errorf("failed to save speech summary: %w", err)
	}
	return nil
}

// SaveScheduledPrompt inserts or updates a scheduled prompt item in SQLite.
func (d *DB) SaveScheduledPrompt(item *PromptQueueItem) error {
	if item == nil {
		return nil
	}
	query := `
	INSERT INTO scheduled_prompts (id, session_id, prompt, deliver_at, created_at)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id, prompt=excluded.prompt, deliver_at=excluded.deliver_at;
	`
	var deliverAt sql.NullTime
	if item.DeliverAt != nil {
		deliverAt = sql.NullTime{Time: *item.DeliverAt, Valid: true}
	}
	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := d.db.Exec(query, item.ID, item.SessionID, item.Text, deliverAt, createdAt)
	return err
}

// DeleteScheduledPrompt removes a scheduled prompt by item ID.
func (d *DB) DeleteScheduledPrompt(id string) error {
	_, err := d.db.Exec("DELETE FROM scheduled_prompts WHERE id = ?", id)
	return err
}

// DeleteScheduledPromptsBySession removes all scheduled prompts for a session.
func (d *DB) DeleteScheduledPromptsBySession(sessionID string) error {
	_, err := d.db.Exec("DELETE FROM scheduled_prompts WHERE session_id = ?", sessionID)
	return err
}

// ListScheduledPrompts returns all scheduled prompts ordered by creation time.
func (d *DB) ListScheduledPrompts() ([]*PromptQueueItem, error) {
	rows, err := d.db.Query("SELECT id, session_id, prompt, deliver_at, created_at FROM scheduled_prompts ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*PromptQueueItem
	for rows.Next() {
		var item PromptQueueItem
		var deliverAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Text, &deliverAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		if deliverAt.Valid {
			t := deliverAt.Time
			item.DeliverAt = &t
		}
		items = append(items, &item)
	}
	return items, nil
}
