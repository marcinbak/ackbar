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
	version TEXT
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
`

func InitDB(dbPath string) (*DB, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
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
	_, _ = db.Exec("DELETE FROM tree_nodes WHERE path LIKE 'Project Y%' OR path LIKE 'ProjectY%' OR path LIKE '%Project Y%';")

	return &DB{db: db}, nil
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

	var blockedKind, blockedReason sql.NullString
	var blockedSince sql.NullTime

	if s.Blocked != nil {
		blockedKind = sql.NullString{String: string(s.Blocked.Kind), Valid: true}
		blockedReason = sql.NullString{String: s.Blocked.Reason, Valid: true}
		blockedSince = sql.NullTime{Time: s.Blocked.Since, Valid: true}
	}

	query := `
	INSERT INTO sessions (
		id, agent, host, native_id, cwd, roots, project_key, state, 
		blocked_kind, blocked_reason, blocked_since, activity, 
		started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
		custom_title, ai_title, ai_description, first_prompt, last_prompt
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		cwd=excluded.cwd,
		roots=excluded.roots,
		project_key=excluded.project_key,
		state=excluded.state,
		blocked_kind=excluded.blocked_kind,
		blocked_reason=excluded.blocked_reason,
		blocked_since=excluded.blocked_since,
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
		last_prompt=excluded.last_prompt;
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

	_, err = d.db.Exec(query,
		s.ID, s.Agent, s.Host, s.NativeID, s.Cwd, string(rootsJSON), s.ProjectKey, int(s.State),
		blockedKind, blockedReason, blockedSince, s.Activity,
		s.StartedAt, s.LastEventAt, managedInt, s.TmuxName, s.PID, archivedInt, nodePath, sessName, entrypointVal, kindVal, versionVal, s.ContextPct, gitBranchVal,
		customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal,
	)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	return nil
}

func (d *DB) GetSession(id string) (*Session, error) {
	query := `
	SELECT id, agent, host, native_id, cwd, roots, project_key, state,
	       blocked_kind, blocked_reason, blocked_since, activity,
	       started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
	       custom_title, ai_title, ai_description, first_prompt, last_prompt
	FROM sessions WHERE id = ? OR native_id = ?;
	`

	var s Session
	var rootsStr, blockedKind, blockedReason, nodePath, sessName, entrypointVal, kindVal, versionVal, gitBranchVal sql.NullString
	var customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal sql.NullString
	var ctxPct sql.NullInt64
	var blockedSince sql.NullTime
	var managedInt, archivedInt int

	row := d.db.QueryRow(query, id, id)
	err := row.Scan(
		&s.ID, &s.Agent, &s.Host, &s.NativeID, &s.Cwd, &rootsStr, &s.ProjectKey, (*int)(&s.State),
		&blockedKind, &blockedReason, &blockedSince, &s.Activity,
		&s.StartedAt, &s.LastEventAt, &managedInt, &s.TmuxName, &s.PID, &archivedInt, &nodePath, &sessName, &entrypointVal, &kindVal, &versionVal, &ctxPct, &gitBranchVal,
		&customTitleVal, &aiTitleVal, &aiDescVal, &firstPromptVal, &lastPromptVal,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to scan session: %w", err)
	}

	s.Managed = managedInt == 1
	s.Archived = archivedInt == 1
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
			Kind:   BlockKind(blockedKind.String),
			Reason: blockedReason.String,
			Since:  blockedSince.Time,
		}
	}

	return &s, nil
}

func (d *DB) ListSessions() ([]*Session, error) {
	query := `
	SELECT id, agent, host, native_id, cwd, roots, project_key, state,
	       blocked_kind, blocked_reason, blocked_since, activity,
	       started_at, last_event_at, managed, tmux_name, pid, archived, node_path, name, entrypoint, kind, version, context_pct, git_branch,
	       custom_title, ai_title, ai_description, first_prompt, last_prompt
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
		var rootsStr, blockedKind, blockedReason, nodePath, sessName, entrypointVal, kindVal, versionVal, gitBranchVal sql.NullString
		var customTitleVal, aiTitleVal, aiDescVal, firstPromptVal, lastPromptVal sql.NullString
		var ctxPct sql.NullInt64
		var blockedSince sql.NullTime
		var managedInt, archivedInt int

		err := rows.Scan(
			&s.ID, &s.Agent, &s.Host, &s.NativeID, &s.Cwd, &rootsStr, &s.ProjectKey, (*int)(&s.State),
			&blockedKind, &blockedReason, &blockedSince, &s.Activity,
			&s.StartedAt, &s.LastEventAt, &managedInt, &s.TmuxName, &s.PID, &archivedInt, &nodePath, &sessName, &entrypointVal, &kindVal, &versionVal, &ctxPct, &gitBranchVal,
			&customTitleVal, &aiTitleVal, &aiDescVal, &firstPromptVal, &lastPromptVal,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session in list: %w", err)
		}

		s.Managed = managedInt == 1
		s.Archived = archivedInt == 1
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
				Kind:   BlockKind(blockedKind.String),
				Reason: blockedReason.String,
				Since:  blockedSince.Time,
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
