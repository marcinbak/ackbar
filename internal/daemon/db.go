package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
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
	engine_type TEXT DEFAULT 'tmux'
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tree_nodes (
	path TEXT PRIMARY KEY,
	project_dir TEXT,
	git_url TEXT,
	created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS hosts (
	name TEXT PRIMARY KEY,
	url TEXT,
	ssh_target TEXT,
	remote_cwd TEXT,
	created_at TIMESTAMP
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
	completed_at TIMESTAMP
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
	PRIMARY KEY (task_id, session_id),
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
	created_at TIMESTAMP NOT NULL,
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS archived_tasks (
	id TEXT PRIMARY KEY,
	original_task_json TEXT NOT NULL,
	archived_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_group_project ON tasks(group_name, project_name);
CREATE INDEX IF NOT EXISTS idx_tasks_status_updated ON tasks(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_task_workers_active ON task_workers(is_active);
CREATE INDEX IF NOT EXISTS idx_task_deliverables_task_id ON task_deliverables(task_id);
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
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN last_state_change_at TIMESTAMP;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN account_id TEXT;")
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN engine_type TEXT DEFAULT 'tmux';")
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
	_, _ = db.Exec("DELETE FROM tree_nodes WHERE path LIKE 'Project Y%' OR path LIKE 'ProjectY%' OR path LIKE '%Project Y%';")
	_, _ = db.Exec("UPDATE sessions SET tmux_name = 'ackbar-' || agent || '-' || native_id WHERE tmux_name = '(deleted)' OR tmux_name = '';")
	_, _ = db.Exec("UPDATE tasks SET group_name = 'Personal' WHERE project_name = 'Ackbar' AND group_name = 'Modemobile';")

	// High-performance indices for fast session lookups, filtering, and liveness checks
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_state ON sessions(state);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_native_id ON sessions(native_id);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_host ON sessions(host);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_project_key ON sessions(project_key);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_account_id ON sessions(account_id);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_accounts_agent ON accounts(agent);")
	d := &DB{db: db}
	_, _ = d.DeduplicateTasks()
	return d, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) SaveSession(s *Session) error {
	rootsJSON, err := json.Marshal(s.Roots)
	if err != nil {
		return fmt.Errorf("failed to marshal roots: %w", err)
	}

	// Preserve user-assigned group (NodePath), CustomTitle, and GitBranch if existing and empty in s
	if existing, _ := d.GetSession(s.ID); existing != nil {
		if s.NodePath == "" && existing.NodePath != "" {
			s.NodePath = existing.NodePath
		}
		if s.CustomTitle == "" && existing.CustomTitle != "" {
			s.CustomTitle = existing.CustomTitle
		}
		if s.GitBranch == "" && existing.GitBranch != "" {
			s.GitBranch = existing.GitBranch
		}
	}

	if s.GitBranch == "" && s.Cwd != "" {
		if s.State != StateEnded || strings.Contains(s.Cwd, "worktree") {
			s.GitBranch = ResolveGitBranch(s.Cwd)
		}
	}

	var blockedKind, blockedReason, blockedQuestion, blockedOptions sql.NullString
	var blockedSince sql.NullTime

	if s.Blocked != nil {
		blockedKind = sql.NullString{String: string(s.Blocked.Kind), Valid: true}
		blockedReason = sql.NullString{String: s.Blocked.Reason, Valid: true}
		blockedSince = sql.NullTime{Time: s.Blocked.Since, Valid: true}
		if s.Blocked.Question != "" {
			blockedQuestion = sql.NullString{String: s.Blocked.Question, Valid: true}
		}
		if len(s.Blocked.Options) > 0 {
			if optBytes, err := json.Marshal(s.Blocked.Options); err == nil {
				blockedOptions = sql.NullString{String: string(optBytes), Valid: true}
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

	var lastStateChangeAt sql.NullTime
	if !s.LastStateChangeAt.IsZero() {
		lastStateChangeAt = sql.NullTime{Time: s.LastStateChangeAt, Valid: true}
	}

	query := `
	INSERT INTO sessions (
		id, agent, host, native_id, cwd, roots, project_key, state, 
		blocked_kind, blocked_reason, blocked_since, blocked_question, blocked_options, activity, 
		started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
		custom_title, ai_title, ai_description, first_prompt, last_prompt, is_unread, last_state_change_at, is_done, is_later, account_id, engine_type
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		engine_type=COALESCE(NULLIF(excluded.engine_type, ''), sessions.engine_type);
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
	engineType := s.EngineType
	if engineType == "" {
		engineType = EngineTmux
	}

	_, err = d.db.Exec(query,
		s.ID, s.Agent, s.Host, s.NativeID, s.Cwd, string(rootsJSON), s.ProjectKey, int(s.State),
		blockedKind, blockedReason, blockedSince, blockedQuestion, blockedOptions, s.Activity,
		s.StartedAt, s.LastEventAt, managedInt, s.TmuxName, s.PID, archivedInt, nodePath, sessName, entrypointVal, kindVal, versionVal, s.ContextPct, gitBranchVal,
		customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal, isUnreadInt, lastStateChangeAt, isDoneInt, isLaterInt, accountIDVal, engineType,
	)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	return nil
}

func (d *DB) GetSession(id string) (*Session, error) {
	query := `
	SELECT id, agent, host, native_id, cwd, roots, project_key, state,
	       blocked_kind, blocked_reason, blocked_since, blocked_question, blocked_options, activity,
	       started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
	       custom_title, ai_title, ai_description, first_prompt, last_prompt, is_unread, last_state_change_at, is_done, is_later, account_id, engine_type
	FROM sessions WHERE id = ? OR native_id = ? OR native_id = ?;
	`

	var s Session
	var rootsStr, blockedKind, blockedReason, blockedQuestion, blockedOptions, nodePath, sessName, entrypointVal, kindVal, versionVal, gitBranchVal sql.NullString
	var customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal, accountIDVal, engineTypeVal sql.NullString
	var ctxPct sql.NullInt64
	var blockedSince, lastStateChangeAt sql.NullTime
	var managedInt, archivedInt, isUnreadInt, isDoneInt, isLaterInt int

	nativeCandidate := id
	if idx := strings.LastIndex(id, ":"); idx != -1 {
		nativeCandidate = id[idx+1:]
	}

	row := d.db.QueryRow(query, id, id, nativeCandidate)
	err := row.Scan(
		&s.ID, &s.Agent, &s.Host, &s.NativeID, &s.Cwd, &rootsStr, &s.ProjectKey, (*int)(&s.State),
		&blockedKind, &blockedReason, &blockedSince, &blockedQuestion, &blockedOptions, &s.Activity,
		&s.StartedAt, &s.LastEventAt, &managedInt, &s.TmuxName, &s.PID, &archivedInt, &nodePath, &sessName, &entrypointVal, &kindVal, &versionVal, &ctxPct, &gitBranchVal,
		&customTitleVal, &aiTitleVal, &aiDescVal, &firstPromptVal, &lastPromptVal, &isUnreadInt, &lastStateChangeAt, &isDoneInt, &isLaterInt, &accountIDVal, &engineTypeVal,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to scan session: %w", err)
	}

	if accountIDVal.Valid {
		s.AccountID = accountIDVal.String
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
		if blockedOptions.Valid && blockedOptions.String != "" {
			_ = json.Unmarshal([]byte(blockedOptions.String), &s.Blocked.Options)
		}
	}

	return &s, nil
}

func (d *DB) ListSessions() ([]*Session, error) {
	query := `
	SELECT id, agent, host, native_id, cwd, roots, project_key, state,
	       blocked_kind, blocked_reason, blocked_since, blocked_question, blocked_options, activity,
	       started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
	       custom_title, ai_title, ai_description, first_prompt, last_prompt, is_unread, last_state_change_at, is_done, is_later, account_id, engine_type
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
		var rootsStr, blockedKind, blockedReason, blockedQuestion, blockedOptions, nodePath, sessName, entrypointVal, kindVal, versionVal, gitBranchVal sql.NullString
		var customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal, accountIDVal, engineTypeVal sql.NullString
		var ctxPct sql.NullInt64
		var blockedSince, lastStateChangeAt sql.NullTime
		var managedInt, archivedInt, isUnreadInt, isDoneInt, isLaterInt int

		err := rows.Scan(
			&s.ID, &s.Agent, &s.Host, &s.NativeID, &s.Cwd, &rootsStr, &s.ProjectKey, (*int)(&s.State),
			&blockedKind, &blockedReason, &blockedSince, &blockedQuestion, &blockedOptions, &s.Activity,
			&s.StartedAt, &s.LastEventAt, &managedInt, &s.TmuxName, &s.PID, &archivedInt, &nodePath, &sessName, &entrypointVal, &kindVal, &versionVal, &ctxPct, &gitBranchVal,
			&customTitleVal, &aiTitleVal, &aiDescVal, &firstPromptVal, &lastPromptVal, &isUnreadInt, &lastStateChangeAt, &isDoneInt, &isLaterInt, &accountIDVal, &engineTypeVal,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session in list: %w", err)
		}

		if accountIDVal.Valid {
			s.AccountID = accountIDVal.String
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
			if blockedOptions.Valid && blockedOptions.String != "" {
				_ = json.Unmarshal([]byte(blockedOptions.String), &s.Blocked.Options)
			}
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
	var query string
	if isDone {
		query = `UPDATE sessions SET is_done = 1, is_later = 0 WHERE id = ? OR native_id = ?;`
	} else {
		query = `UPDATE sessions SET is_done = 0 WHERE id = ? OR native_id = ?;`
	}
	_, err := d.db.Exec(query, id, id)
	return err
}

func (d *DB) MarkSessionLater(id string, isLater bool) error {
	var query string
	if isLater {
		query = `UPDATE sessions SET is_later = 1, is_done = 0 WHERE id = ? OR native_id = ?;`
	} else {
		query = `UPDATE sessions SET is_later = 0 WHERE id = ? OR native_id = ?;`
	}
	_, err := d.db.Exec(query, id, id)
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
	       blocked_kind, blocked_reason, blocked_since, blocked_question, blocked_options, activity,
	       started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
	       custom_title, ai_title, ai_description, first_prompt, last_prompt, is_unread, last_state_change_at, is_done, is_later, account_id, engine_type
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
		var rootsStr, blockedKind, blockedReason, blockedQuestion, blockedOptions, nodePath, sessName, entrypointVal, kindVal, versionVal, gitBranchVal sql.NullString
		var customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal, accountIDVal, engineTypeVal sql.NullString
		var ctxPct sql.NullInt64
		var blockedSince, lastStateChangeAt sql.NullTime
		var managedInt, archivedInt, isUnreadInt, isDoneInt, isLaterInt int

		err := rows.Scan(
			&s.ID, &s.Agent, &s.Host, &s.NativeID, &s.Cwd, &rootsStr, &s.ProjectKey, (*int)(&s.State),
			&blockedKind, &blockedReason, &blockedSince, &blockedQuestion, &blockedOptions, &s.Activity,
			&s.StartedAt, &s.LastEventAt, &managedInt, &s.TmuxName, &s.PID, &archivedInt, &nodePath, &sessName, &entrypointVal, &kindVal, &versionVal, &ctxPct, &gitBranchVal,
			&customTitleVal, &aiTitleVal, &aiDescVal, &firstPromptVal, &lastPromptVal, &isUnreadInt, &lastStateChangeAt, &isDoneInt, &isLaterInt, &accountIDVal, &engineTypeVal,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan active session in list: %w", err)
		}

		if accountIDVal.Valid {
			s.AccountID = accountIDVal.String
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
			if blockedOptions.Valid && blockedOptions.String != "" {
				_ = json.Unmarshal([]byte(blockedOptions.String), &s.Blocked.Options)
			}
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
	query := `
	INSERT INTO tree_nodes (path, project_dir, git_url, created_at)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		project_dir=excluded.project_dir,
		git_url=excluded.git_url;
	`
	_, err := d.db.Exec(query, node.Path, node.ProjectDir, node.GitURL, node.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to save node: %w", err)
	}
	return nil
}

func (d *DB) GetNode(path string) (*TreeNode, error) {
	var n TreeNode
	var projectDir, gitURL sql.NullString
	err := d.db.QueryRow("SELECT path, project_dir, git_url, created_at FROM tree_nodes WHERE path = ?;", path).Scan(&n.Path, &projectDir, &gitURL, &n.CreatedAt)
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
	return &n, nil
}

func (d *DB) ListNodes() ([]*TreeNode, error) {
	query := `SELECT path, project_dir, git_url, created_at FROM tree_nodes ORDER BY path ASC;`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tree_nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*TreeNode
	for rows.Next() {
		var n TreeNode
		var projectDir, gitURL sql.NullString
		if err := rows.Scan(&n.Path, &projectDir, &gitURL, &n.CreatedAt); err != nil {
			return nil, err
		}
		if projectDir.Valid {
			n.ProjectDir = projectDir.String
		}
		if gitURL.Valid {
			n.GitURL = gitURL.String
		}
		nodes = append(nodes, &n)
	}
	return nodes, nil
}

func (d *DB) DeleteNode(path string) error {
	_, err := d.db.Exec("DELETE FROM tree_nodes WHERE path = ? OR path LIKE ?;", path, path+"/%")
	if err != nil {
		return fmt.Errorf("failed to delete node: %w", err)
	}
	_, _ = d.db.Exec("UPDATE sessions SET node_path = '' WHERE node_path = ? OR node_path LIKE ?;", path, path+"/%")
	return nil
}

func (d *DB) MoveNode(oldPath, newPath string) error {
	prefix := oldPath + "/"
	qNodes := `UPDATE tree_nodes SET path = ? || SUBSTR(path, LENGTH(?) + 1) WHERE path = ? OR path LIKE ?;`
	if _, err := d.db.Exec(qNodes, newPath, oldPath, oldPath, prefix+"%"); err != nil {
		return fmt.Errorf("failed to update tree_nodes path: %w", err)
	}

	qSess := `UPDATE sessions SET node_path = ? || SUBSTR(node_path, LENGTH(?) + 1) WHERE node_path = ? OR node_path LIKE ?;`
	if _, err := d.db.Exec(qSess, newPath, oldPath, oldPath, prefix+"%"); err != nil {
		return fmt.Errorf("failed to update sessions node_path: %w", err)
	}
	return nil
}

func (d *DB) MoveSessionNode(sessionID, nodePath string) error {
	_, err := d.db.Exec("UPDATE sessions SET node_path = ? WHERE id = ? OR native_id = ?;", nodePath, sessionID, sessionID)
	if err != nil {
		return fmt.Errorf("failed to move session node: %w", err)
	}
	return nil
}

func (d *DB) SaveHost(h *HostRecord) error {
	query := `
	INSERT INTO hosts (name, url, ssh_target, remote_cwd, created_at)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(name) DO UPDATE SET
		url=excluded.url,
		ssh_target=excluded.ssh_target,
		remote_cwd=excluded.remote_cwd;
	`
	_, err := d.db.Exec(query, h.Name, h.URL, h.SSHTarget, h.RemoteCwd, h.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to save host: %w", err)
	}
	return nil
}

func (d *DB) ListHosts() ([]*HostRecord, error) {
	query := `SELECT name, url, ssh_target, remote_cwd, created_at FROM hosts ORDER BY name ASC;`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query hosts: %w", err)
	}
	defer rows.Close()

	var hosts []*HostRecord
	for rows.Next() {
		var h HostRecord
		var url, sshTarget, remoteCwd sql.NullString
		if err := rows.Scan(&h.Name, &url, &sshTarget, &remoteCwd, &h.CreatedAt); err != nil {
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
		hosts = append(hosts, &h)
	}
	return hosts, nil
}

func (d *DB) GetHost(name string) (*HostRecord, error) {
	query := `SELECT name, url, ssh_target, remote_cwd, created_at FROM hosts WHERE name = ? LIMIT 1;`
	row := d.db.QueryRow(query, name)
	var h HostRecord
	var url, sshTarget, remoteCwd sql.NullString
	if err := row.Scan(&h.Name, &url, &sshTarget, &remoteCwd, &h.CreatedAt); err != nil {
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
	return &h, nil
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
	ExternalRefs    []TaskExternalRef `json:"external_refs"`
	Workers         []TaskWorker      `json:"workers"`
	Deliverables    []TaskDeliverable `json:"deliverables"`
}

type TaskExternalRef struct {
	TaskID  string `json:"task_id"`
	Tracker string `json:"tracker"`
	RefKey  string `json:"ref_key"`
	URL     string `json:"url,omitempty"`
}

type TaskWorker struct {
	TaskID     string    `json:"task_id"`
	SessionID  string    `json:"session_id"`
	Agent      string    `json:"agent"`
	Host       string    `json:"host"`
	IsActive   bool      `json:"is_active"`
	AssignedAt time.Time `json:"assigned_at"`
}

type TaskDeliverable struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Host      string    `json:"host"`
	FilePath  string    `json:"file_path,omitempty"`
	URL       string    `json:"url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (d *DB) GetTasks() ([]Task, error) {
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at FROM tasks ORDER BY updated_at DESC;`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	tasks := []Task{}
	var taskIDs []string
	for rows.Next() {
		var t Task
		var subprojectName, notes, blockerQuestion, branch, worktreePath, prURL, prState, ciStatus sql.NullString
		var prNumber sql.NullInt64
		var completedAt sql.NullTime

		if err := rows.Scan(
			&t.ID, &t.Title, &t.GroupName, &t.ProjectName, &subprojectName,
			&t.Status, &t.Substatus, &notes, &blockerQuestion, &branch, &worktreePath,
			&prURL, &prNumber, &prState, &ciStatus, &t.CreatedAt, &t.UpdatedAt, &completedAt,
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
		if completedAt.Valid {
			t.CompletedAt = &completedAt.Time
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
		}
	}

	return tasks, nil
}

type scannableRow interface {
	Scan(dest ...any) error
}

func scanTaskRow(row scannableRow) (*Task, error) {
	var t Task
	var subprojectName, notes, blockerQuestion, branch, worktreePath, prURL, prState, ciStatus sql.NullString
	var prNumber sql.NullInt64
	var completedAt sql.NullTime

	if err := row.Scan(
		&t.ID, &t.Title, &t.GroupName, &t.ProjectName, &subprojectName,
		&t.Status, &t.Substatus, &notes, &blockerQuestion, &branch, &worktreePath,
		&prURL, &prNumber, &prState, &ciStatus, &t.CreatedAt, &t.UpdatedAt, &completedAt,
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
	if completedAt.Valid {
		t.CompletedAt = &completedAt.Time
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
	return nil
}

func (d *DB) GetTaskByID(id string) (*Task, error) {
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at FROM tasks WHERE id = ?;`
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
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at FROM tasks WHERE branch = ? ORDER BY updated_at DESC LIMIT 1;`
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
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at FROM tasks WHERE worktree_path = ? ORDER BY updated_at DESC LIMIT 1;`
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
	query := `SELECT t.id, t.title, t.group_name, t.project_name, t.subproject_name, t.status, t.substatus, t.notes, t.blocker_question, t.branch, t.worktree_path, t.pr_url, t.pr_number, t.pr_state, t.ci_status, t.created_at, t.updated_at, t.completed_at
			  FROM tasks t
			  JOIN task_external_refs r ON t.id = r.task_id
			  WHERE r.ref_key = ?
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
	query := `SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at FROM tasks WHERE title = ? COLLATE NOCASE ORDER BY updated_at DESC LIMIT 1;`
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
	query := `SELECT t.id, t.title, t.group_name, t.project_name, t.subproject_name, t.status, t.substatus, t.notes, t.blocker_question, t.branch, t.worktree_path, t.pr_url, t.pr_number, t.pr_state, t.ci_status, t.created_at, t.updated_at, t.completed_at
			  FROM tasks t
			  JOIN task_workers w ON t.id = w.task_id
			  WHERE w.session_id = ? AND w.is_active = 1
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
		query := fmt.Sprintf(`SELECT task_id, session_id, agent, host, is_active, assigned_at FROM task_workers WHERE task_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := d.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var w TaskWorker
			var isActive int
			if err := rows.Scan(&w.TaskID, &w.SessionID, &w.Agent, &w.Host, &isActive, &w.AssignedAt); err != nil {
				rows.Close()
				return nil, err
			}
			w.IsActive = isActive == 1
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
		query := fmt.Sprintf(`SELECT id, task_id, kind, title, host, file_path, url, created_at FROM task_deliverables WHERE task_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := d.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var del TaskDeliverable
			var fp, u sql.NullString
			if err := rows.Scan(&del.ID, &del.TaskID, &del.Kind, &del.Title, &del.Host, &fp, &u, &del.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			del.FilePath = fp.String
			del.URL = u.String
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
		t.CreatedAt = time.Now()
	}
	t.UpdatedAt = time.Now()

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO tasks (
		id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var completedAt sql.NullTime
	if t.CompletedAt != nil {
		completedAt.Time = *t.CompletedAt
		completedAt.Valid = true
	}

	_, err = tx.Exec(query,
		t.ID, t.Title, t.GroupName, t.ProjectName, t.SubprojectName,
		t.Status, t.Substatus, t.Notes, t.BlockerQuestion, t.Branch, t.WorktreePath,
		t.PRURL, t.PRNumber, t.PRState, t.CIStatus, t.CreatedAt, t.UpdatedAt, completedAt,
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
	t.UpdatedAt = time.Now()

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `UPDATE tasks SET
		title = ?, group_name = ?, project_name = ?, subproject_name = ?, status = ?, substatus = ?,
		notes = ?, blocker_question = ?, branch = ?, worktree_path = ?, pr_url = ?, pr_number = ?,
		pr_state = ?, ci_status = ?, updated_at = ?, completed_at = ?
		WHERE id = ?`

	var completedAt sql.NullTime
	if t.CompletedAt != nil {
		completedAt.Time = *t.CompletedAt
		completedAt.Valid = true
	}

	res, err := tx.Exec(query,
		t.Title, t.GroupName, t.ProjectName, t.SubprojectName, t.Status, t.Substatus,
		t.Notes, t.BlockerQuestion, t.Branch, t.WorktreePath, t.PRURL, t.PRNumber,
		t.PRState, t.CIStatus, t.UpdatedAt, completedAt, t.ID,
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
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM task_workers WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("failed to delete task workers: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM task_external_refs WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("failed to delete task external refs: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM task_deliverables WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("failed to delete task deliverables: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("task with id %s not found", taskID)
	}

	return tx.Commit()
}

func insertTaskWorkerTx(tx *sql.Tx, w *TaskWorker) error {
	isActive := 0
	if w.IsActive {
		isActive = 1
	}
	if w.AssignedAt.IsZero() {
		w.AssignedAt = time.Now()
	}
	query := `INSERT INTO task_workers (task_id, session_id, agent, host, is_active, assigned_at)
			  VALUES (?, ?, ?, ?, ?, ?)
			  ON CONFLICT(task_id, session_id) DO UPDATE SET
			  is_active = excluded.is_active,
			  agent = excluded.agent,
			  host = excluded.host`
	_, err := tx.Exec(query, w.TaskID, w.SessionID, w.Agent, w.Host, isActive, w.AssignedAt)
	return err
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
		td.CreatedAt = time.Now()
	}

	// Deduplicate deliverable for same task and file path
	if td.FilePath != "" {
		var existingID string
		err := tx.QueryRow(`SELECT id FROM task_deliverables WHERE task_id = ? AND file_path = ?`, td.TaskID, td.FilePath).Scan(&existingID)
		if err == nil && existingID != "" {
			td.ID = existingID
			_, err = tx.Exec(`UPDATE task_deliverables SET kind = ?, title = ?, host = ? WHERE id = ?`, td.Kind, td.Title, td.Host, existingID)
			return err
		}
	} else if td.URL != "" {
		var existingID string
		err := tx.QueryRow(`SELECT id FROM task_deliverables WHERE task_id = ? AND url = ?`, td.TaskID, td.URL).Scan(&existingID)
		if err == nil && existingID != "" {
			td.ID = existingID
			_, err = tx.Exec(`UPDATE task_deliverables SET kind = ?, title = ?, host = ? WHERE id = ?`, td.Kind, td.Title, td.Host, existingID)
			return err
		}
	}

	query := `INSERT INTO task_deliverables (id, task_id, kind, title, host, file_path, url, created_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := tx.Exec(query, td.ID, td.TaskID, td.Kind, td.Title, td.Host, td.FilePath, td.URL, td.CreatedAt)
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

// DeduplicateTasks removes redundant duplicate tasks sharing the same external reference
// or identical title and project when branch is empty, consolidating workers and deliverables.
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
			taskQuery := `SELECT t.id, t.title, t.group_name, t.project_name, t.subproject_name, t.status, t.substatus, t.notes, t.blocker_question, t.branch, t.worktree_path, t.pr_url, t.pr_number, t.pr_state, t.ci_status, t.created_at, t.updated_at, t.completed_at
						  FROM tasks t
						  JOIN task_external_refs r ON t.id = r.task_id
						  WHERE r.tracker = ? AND r.ref_key = ?
						  ORDER BY 
							(CASE WHEN t.branch != '' AND t.branch IS NOT NULL THEN 1 ELSE 0 END) DESC,
							(SELECT count(*) FROM task_workers WHERE task_id = t.id) DESC,
							(SELECT count(*) FROM task_deliverables WHERE task_id = t.id) DESC,
							t.created_at ASC;`
			tRows, err := d.db.Query(taskQuery, dr.tracker, dr.refKey)
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
				_, _ = d.db.Exec(`UPDATE OR IGNORE task_deliverables SET task_id = ? WHERE task_id = ?`, primary.ID, dup.ID)
				_, _ = d.db.Exec(`DELETE FROM task_workers WHERE task_id = ?`, dup.ID)
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
			tRows, err := d.db.Query(`SELECT id, title, group_name, project_name, subproject_name, status, substatus, notes, blocker_question, branch, worktree_path, pr_url, pr_number, pr_state, ci_status, created_at, updated_at, completed_at
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
				_, _ = d.db.Exec(`UPDATE OR IGNORE task_deliverables SET task_id = ? WHERE task_id = ?`, primary.ID, dup.ID)
				_, _ = d.db.Exec(`DELETE FROM task_workers WHERE task_id = ?`, dup.ID)
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
