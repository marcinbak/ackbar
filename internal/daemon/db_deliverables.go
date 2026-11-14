package daemon

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type RetroMetrics struct {
	RunSlug                       string     `json:"run_slug,omitempty"`
	Repo                          string     `json:"repo,omitempty"`
	NodePath                      string     `json:"node_path,omitempty"`
	Branch                        string     `json:"branch,omitempty"`
	Agent                         string     `json:"agent,omitempty"`
	Outcome                       string     `json:"outcome,omitempty"`
	Tier                          string     `json:"tier,omitempty"`
	Lite                          bool       `json:"lite,omitempty"`
	PRURL                         string     `json:"pr_url,omitempty"`
	StartedAt                     *time.Time `json:"started_at,omitempty"`
	CompletedAt                   *time.Time `json:"completed_at,omitempty"`
	ImplAttempts                  int        `json:"impl_attempts,omitempty"`
	PlanRevisions                 int        `json:"plan_revisions,omitempty"`
	ReviewRounds                  int        `json:"review_rounds,omitempty"`
	ReviewBudgetLeft              int        `json:"review_budget_left,omitempty"`
	ReReviews                     int        `json:"re_reviews,omitempty"`
	InterventionsCount            int        `json:"interventions_count,omitempty"`
	PreventableInterventionsCount int        `json:"preventable_interventions_count,omitempty"`
	RawTokens                     int        `json:"raw_tokens,omitempty"`
	WeightedTokens                int        `json:"weighted_tokens,omitempty"`
	CacheHitPct                   float64    `json:"cache_hit_pct,omitempty"`
	PeakContext                   int        `json:"peak_context,omitempty"`
	SubagentShare                 float64    `json:"subagent_share,omitempty"`
	Headlines                     []string   `json:"headlines,omitempty"`
}

func TaskNodePath(t *Task) string {
	if t == nil {
		return ""
	}
	var parts []string
	if g := strings.TrimSpace(t.GroupName); g != "" {
		parts = append(parts, g)
	}
	if p := strings.TrimSpace(t.ProjectName); p != "" {
		parts = append(parts, p)
	}
	if s := strings.TrimSpace(t.SubprojectName); s != "" {
		parts = append(parts, s)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "/")
	}
	if len(t.Workstreams) > 0 {
		return strings.TrimSpace(t.Workstreams[0].RepoNodePath)
	}
	return ""
}

func normalizeDeliverableKind(kind, typ, filePath string) string {
	k := strings.TrimSpace(kind)
	if k == "" {
		k = strings.TrimSpace(typ)
	}
	k = strings.ToLower(k)
	if k == "" && filePath != "" {
		lowerPath := strings.ToLower(filePath)
		if strings.HasSuffix(lowerPath, ".retro.md") {
			return "retro"
		}
		if strings.HasSuffix(lowerPath, ".brief.md") {
			return "plan"
		}
	}
	if k == "" {
		return "other"
	}
	return k
}

func isRetroKind(k string) bool {
	lk := strings.ToLower(strings.TrimSpace(k))
	return lk == "retro" || lk == "retrospective"
}

func canonicalDeliverableKind(kind string) string {
	if isRetroKind(kind) {
		return "retro"
	}
	return strings.ToLower(strings.TrimSpace(kind))
}

func cleanCrossHostSuffix(p string) string {
	clean := filepath.ToSlash(filepath.Clean(p))
	for _, marker := range []string{"/.worktrees/", "/dev-workflow-runs/", "/Personal/", "/Work/", "/repo/"} {
		if idx := strings.Index(clean, marker); idx >= 0 {
			return strings.ToLower(clean[idx+1:])
		}
	}
	return strings.ToLower(clean)
}

func computeDeliverableDedupKey(td *TaskDeliverable) string {
	ck := canonicalDeliverableKind(td.Kind)
	if ck == "" {
		ck = normalizeDeliverableKind(td.Kind, "", td.FilePath)
		ck = canonicalDeliverableKind(ck)
	}
	cleanPath := filepath.Clean(td.FilePath)
	base := strings.ToLower(filepath.Base(cleanPath))
	if td.FilePath != "" && (strings.Contains(cleanPath, "dev-workflow-runs") ||
		strings.HasSuffix(base, ".retro.md") ||
		strings.HasSuffix(base, ".brief.md") ||
		strings.HasSuffix(base, ".impl-notes.md")) {
		return "wf:" + ck + ":" + base
	}
	if td.FilePath != "" {
		return "fp:" + ck + ":" + cleanCrossHostSuffix(td.FilePath)
	}
	if td.URL != "" {
		return "url:" + ck + ":" + strings.TrimSpace(td.URL)
	}
	return "kt:" + ck + ":" + strings.ToLower(strings.TrimSpace(td.Title))
}

func deterministicDeliverableID(taskID, dedupKey string) string {
	h := sha256.Sum256([]byte(taskID + "\x00" + dedupKey))
	return "del_" + hex.EncodeToString(h[:])[:20]
}

func (d *DB) UpsertTaskDeliverableLWW(td *TaskDeliverable, isPeerSync bool) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed, err := upsertTaskDeliverableTx(tx, td, isPeerSync)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

func upsertTaskDeliverableTx(tx *sql.Tx, td *TaskDeliverable, isPeerSync bool) (bool, error) {
	if td == nil || td.TaskID == "" {
		return false, fmt.Errorf("task_id is required")
	}

	td.Kind = normalizeDeliverableKind(td.Kind, "", td.FilePath)
	if td.DedupKey == "" {
		td.DedupKey = computeDeliverableDedupKey(td)
	}
	if td.ID == "" {
		td.ID = deterministicDeliverableID(td.TaskID, td.DedupKey)
	}
	if td.MetadataJSON == "" {
		td.MetadataJSON = "{}"
	}

	var existing TaskDeliverable
	var eFP, eURL, eProv, eCMD, eDedup, eMeta sql.NullString
	var eCreated, eUpd, eDel sql.NullTime

	rowFound := false
	if td.ID != "" {
		err := tx.QueryRow(`SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE id = ?`, td.ID).
			Scan(&existing.ID, &existing.TaskID, &existing.Kind, &existing.Title, &existing.Host, &eFP, &eURL, &eProv, &eCMD, &eDedup, &eMeta, &eCreated, &eUpd, &eDel)
		if err == nil {
			rowFound = true
		}
	}

	if !rowFound && td.DedupKey != "" {
		err := tx.QueryRow(`SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE task_id = ? AND dedup_key = ?`, td.TaskID, td.DedupKey).
			Scan(&existing.ID, &existing.TaskID, &existing.Kind, &existing.Title, &existing.Host, &eFP, &eURL, &eProv, &eCMD, &eDedup, &eMeta, &eCreated, &eUpd, &eDel)
		if err == nil {
			rowFound = true
		}
	}

	if !rowFound {
		if td.FilePath != "" {
			err := tx.QueryRow(`SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE task_id = ? AND file_path = ?`, td.TaskID, td.FilePath).
				Scan(&existing.ID, &existing.TaskID, &existing.Kind, &existing.Title, &existing.Host, &eFP, &eURL, &eProv, &eCMD, &eDedup, &eMeta, &eCreated, &eUpd, &eDel)
			if err == nil {
				rowFound = true
			}
		} else if td.URL != "" {
			err := tx.QueryRow(`SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE task_id = ? AND url = ?`, td.TaskID, td.URL).
				Scan(&existing.ID, &existing.TaskID, &existing.Kind, &existing.Title, &existing.Host, &eFP, &eURL, &eProv, &eCMD, &eDedup, &eMeta, &eCreated, &eUpd, &eDel)
			if err == nil {
				rowFound = true
			}
		} else {
			var err error
			if isRetroKind(td.Kind) {
				err = tx.QueryRow(`SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE task_id = ? AND LOWER(kind) IN ('retro', 'retrospective') AND title = ?`, td.TaskID, td.Title).
					Scan(&existing.ID, &existing.TaskID, &existing.Kind, &existing.Title, &existing.Host, &eFP, &eURL, &eProv, &eCMD, &eDedup, &eMeta, &eCreated, &eUpd, &eDel)
			} else {
				err = tx.QueryRow(`SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE task_id = ? AND kind = ? AND title = ?`, td.TaskID, td.Kind, td.Title).
					Scan(&existing.ID, &existing.TaskID, &existing.Kind, &existing.Title, &existing.Host, &eFP, &eURL, &eProv, &eCMD, &eDedup, &eMeta, &eCreated, &eUpd, &eDel)
			}
			if err == nil {
				rowFound = true
			}
		}
	}

	if rowFound {
		existing.FilePath = eFP.String
		existing.URL = eURL.String
		existing.Provider = eProv.String
		existing.ContentMD = eCMD.String
		existing.DedupKey = eDedup.String
		existing.MetadataJSON = eMeta.String
		if eCreated.Valid {
			existing.CreatedAt = eCreated.Time
		}
		if eUpd.Valid {
			existing.UpdatedAt = eUpd.Time
		}
		if eDel.Valid {
			existing.DeletedAt = &eDel.Time
		}
	}

	if !rowFound {
		if td.CreatedAt.IsZero() {
			if !td.UpdatedAt.IsZero() {
				td.CreatedAt = td.UpdatedAt
			} else {
				td.CreatedAt = time.Now().UTC()
			}
		}
		if td.UpdatedAt.IsZero() {
			td.UpdatedAt = td.CreatedAt
		}
		td.HasContentMD = strings.TrimSpace(td.ContentMD) != ""
		td.ContentBytes = len(td.ContentMD)
		td.ContentChars = len([]rune(td.ContentMD))

		_, err := tx.Exec(`INSERT INTO task_deliverables (id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			td.ID, td.TaskID, td.Kind, td.Title, td.Host, td.FilePath, td.URL, td.Provider, td.ContentMD, td.DedupKey, td.MetadataJSON, td.CreatedAt, td.UpdatedAt, td.DeletedAt)
		return true, err
	}

	if !isPeerSync {
		sameKind := (isRetroKind(td.Kind) && isRetroKind(existing.Kind)) || strings.EqualFold(td.Kind, existing.Kind)
		sameTitle := td.Title == "" || td.Title == existing.Title
		sameContent := (td.ContentMD != "" && td.ContentMD == existing.ContentMD) || (td.ContentMD == "" && existing.ContentMD != "") || (td.ContentMD == "" && existing.ContentMD == "")
		sameURL := td.URL == existing.URL || (td.URL == "" && existing.URL != "")
		sameFP := td.FilePath == existing.FilePath || (td.FilePath == "" && existing.FilePath != "")
		sameProv := td.Provider == "" || td.Provider == existing.Provider
		sameHost := td.Host == "" || td.Host == existing.Host
		sameMeta := td.MetadataJSON == existing.MetadataJSON || (td.MetadataJSON == "{}" && existing.MetadataJSON != "") || (td.MetadataJSON == "" && existing.MetadataJSON != "")

		if existing.DeletedAt == nil && sameKind && sameTitle && sameContent && sameURL && sameFP && sameProv && sameHost && sameMeta {
			td.ID = existing.ID
			td.Kind = existing.Kind
			td.Title = existing.Title
			td.Host = existing.Host
			td.FilePath = existing.FilePath
			td.URL = existing.URL
			td.Provider = existing.Provider
			td.ContentMD = existing.ContentMD
			td.DedupKey = existing.DedupKey
			td.MetadataJSON = existing.MetadataJSON
			td.CreatedAt = existing.CreatedAt
			td.UpdatedAt = existing.UpdatedAt
			td.HasContentMD = strings.TrimSpace(td.ContentMD) != ""
			td.ContentBytes = len(td.ContentMD)
			td.ContentChars = len([]rune(td.ContentMD))
			return false, nil
		}

		if strings.TrimSpace(td.ContentMD) == "" && existing.ContentMD != "" {
			td.ContentMD = existing.ContentMD
		}
		if (td.MetadataJSON == "" || td.MetadataJSON == "{}") && existing.MetadataJSON != "" && existing.MetadataJSON != "{}" {
			td.MetadataJSON = existing.MetadataJSON
		}
		if td.Title == "" {
			td.Title = existing.Title
		}
		if td.Host == "" {
			td.Host = existing.Host
		}
		if td.Provider == "" {
			td.Provider = existing.Provider
		}
		if td.FilePath == "" {
			td.FilePath = existing.FilePath
		}
		if td.URL == "" {
			td.URL = existing.URL
		}
		td.ID = existing.ID
		td.CreatedAt = existing.CreatedAt
		td.UpdatedAt = time.Now().UTC()
		td.DeletedAt = nil
		td.HasContentMD = strings.TrimSpace(td.ContentMD) != ""
		td.ContentBytes = len(td.ContentMD)
		td.ContentChars = len([]rune(td.ContentMD))

		_, err := tx.Exec(`UPDATE task_deliverables SET kind = ?, title = ?, host = ?, file_path = ?, url = ?, provider = ?, content_md = ?, dedup_key = ?, metadata_json = ?, updated_at = ?, deleted_at = NULL WHERE id = ?`,
			td.Kind, td.Title, td.Host, td.FilePath, td.URL, td.Provider, td.ContentMD, td.DedupKey, td.MetadataJSON, td.UpdatedAt, existing.ID)
		return true, err
	}

	incomingWins := td.UpdatedAt.After(existing.UpdatedAt) || (td.UpdatedAt.Equal(existing.UpdatedAt) && td.ID > existing.ID)
	if !incomingWins {
		return false, nil
	}

	if td.DeletedAt != nil {
		_, err := tx.Exec(`UPDATE task_deliverables SET deleted_at = ?, content_md = '', updated_at = ? WHERE id = ?`,
			td.DeletedAt, td.UpdatedAt, existing.ID)
		return true, err
	}

	if strings.TrimSpace(td.ContentMD) == "" && existing.ContentMD != "" {
		td.ContentMD = existing.ContentMD
	}
	if (td.MetadataJSON == "" || td.MetadataJSON == "{}") && existing.MetadataJSON != "" && existing.MetadataJSON != "{}" {
		td.MetadataJSON = existing.MetadataJSON
	}
	td.DeletedAt = nil
	td.HasContentMD = strings.TrimSpace(td.ContentMD) != ""
	td.ContentBytes = len(td.ContentMD)
	td.ContentChars = len([]rune(td.ContentMD))

	if existing.ID != td.ID {
		_, err := tx.Exec(`UPDATE task_deliverables SET id = ?, kind = ?, title = ?, host = ?, file_path = ?, url = ?, provider = ?, content_md = ?, dedup_key = ?, metadata_json = ?, created_at = ?, updated_at = ?, deleted_at = NULL WHERE id = ?`,
			td.ID, td.Kind, td.Title, td.Host, td.FilePath, td.URL, td.Provider, td.ContentMD, td.DedupKey, td.MetadataJSON, td.CreatedAt, td.UpdatedAt, existing.ID)
		return true, err
	}

	_, err := tx.Exec(`UPDATE task_deliverables SET kind = ?, title = ?, host = ?, file_path = ?, url = ?, provider = ?, content_md = ?, dedup_key = ?, metadata_json = ?, created_at = ?, updated_at = ?, deleted_at = NULL WHERE id = ?`,
		td.Kind, td.Title, td.Host, td.FilePath, td.URL, td.Provider, td.ContentMD, td.DedupKey, td.MetadataJSON, td.CreatedAt, td.UpdatedAt, existing.ID)
	return true, err
}

func (d *DB) DeleteTaskDeliverable(id string) error {
	now := time.Now().UTC()
	_, err := d.db.Exec(`UPDATE task_deliverables SET deleted_at = ?, updated_at = ?, content_md = '' WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	return err
}

func (d *DB) GetTaskDeliverableLookup(id, taskID, kind string) (*TaskDeliverable, error) {
	id = strings.TrimSpace(id)
	taskID = strings.TrimSpace(taskID)
	kind = strings.TrimSpace(kind)

	var query string
	var args []any
	if id != "" {
		query = `SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE id = ? AND deleted_at IS NULL`
		args = []any{id}
	} else if taskID != "" {
		if isRetroKind(kind) {
			query = `SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE task_id = ? AND deleted_at IS NULL AND LOWER(kind) IN ('retro', 'retrospective') ORDER BY updated_at DESC, id DESC LIMIT 1`
			args = []any{taskID}
		} else if kind != "" {
			query = `SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE task_id = ? AND deleted_at IS NULL AND LOWER(kind) = LOWER(?) ORDER BY updated_at DESC, id DESC LIMIT 1`
			args = []any{taskID, kind}
		} else {
			query = `SELECT id, task_id, kind, title, host, file_path, url, provider, content_md, dedup_key, metadata_json, created_at, updated_at, deleted_at FROM task_deliverables WHERE task_id = ? AND deleted_at IS NULL ORDER BY updated_at DESC, id DESC LIMIT 1`
			args = []any{taskID}
		}
	} else {
		return nil, nil
	}

	row := d.db.QueryRow(query, args...)
	var del TaskDeliverable
	var fp, u, prov, cMD, dedup, meta sql.NullString
	var updAt, delAt sql.NullTime
	if err := row.Scan(&del.ID, &del.TaskID, &del.Kind, &del.Title, &del.Host, &fp, &u, &prov, &cMD, &dedup, &meta, &del.CreatedAt, &updAt, &delAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	del.FilePath = fp.String
	del.URL = u.String
	del.Provider = prov.String
	del.ContentMD = cMD.String
	del.DedupKey = dedup.String
	del.MetadataJSON = meta.String
	if updAt.Valid {
		del.UpdatedAt = updAt.Time
	}
	if delAt.Valid {
		del.DeletedAt = &delAt.Time
	}
	del.HasContentMD = strings.TrimSpace(del.ContentMD) != ""
	del.ContentBytes = len(del.ContentMD)
	del.ContentChars = len([]rune(del.ContentMD))
	if del.MetadataJSON != "" && del.MetadataJSON != "{}" {
		var m RetroMetrics
		if err := json.Unmarshal([]byte(del.MetadataJSON), &m); err == nil {
			del.Metrics = &m
		}
	}
	return &del, nil
}

type RetroFilter struct {
	Since    time.Time
	Until    time.Time
	TaskID   string
	NodePath string
	Repo     string
	Host     string
	Agent    string
	Limit    int
}

type RetroSummary struct {
	ID           string        `json:"id"`
	TaskID       string        `json:"task_id"`
	TaskTitle    string        `json:"task_title,omitempty"`
	NodePath     string        `json:"node_path,omitempty"`
	Repo         string        `json:"repo,omitempty"`
	Agent        string        `json:"agent,omitempty"`
	Branch       string        `json:"branch,omitempty"`
	Outcome      string        `json:"outcome,omitempty"`
	Kind         string        `json:"kind"`
	Title        string        `json:"title"`
	Host         string        `json:"host"`
	FilePath     string        `json:"file_path,omitempty"`
	URL          string        `json:"url,omitempty"`
	Provider     string        `json:"provider,omitempty"`
	HasContentMD bool          `json:"has_content_md"`
	ContentBytes int           `json:"content_bytes"`
	ContentChars int           `json:"content_chars"`
	DedupKey     string        `json:"dedup_key,omitempty"`
	MetadataJSON string        `json:"metadata_json,omitempty"`
	Metrics      *RetroMetrics `json:"metrics,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at,omitempty"`
}

type retroCandidate struct {
	summary RetroSummary
	effTime time.Time
}

func (d *DB) ListRetros(filter RetroFilter) ([]RetroSummary, error) {
	query := `
		SELECT d.id, d.task_id, d.kind, d.title, d.host, d.file_path, d.url, d.provider, d.content_md, d.dedup_key, d.metadata_json, d.created_at, d.updated_at,
		       t.group_name, t.project_name, t.subproject_name
		FROM task_deliverables d
		LEFT JOIN tasks t ON t.id = d.task_id
		WHERE d.deleted_at IS NULL AND (t.deleted_at IS NULL OR t.id IS NULL) AND LOWER(d.kind) IN ('retro', 'retrospective')
		ORDER BY d.updated_at DESC, d.id DESC
	`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query retros: %w", err)
	}
	defer rows.Close()

	taskCache := make(map[string]*Task)
	var candidates []retroCandidate

	for rows.Next() {
		var del TaskDeliverable
		var fp, u, prov, cMD, dedup, meta sql.NullString
		var updAt sql.NullTime
		var gn, pn, subn sql.NullString
		if err := rows.Scan(&del.ID, &del.TaskID, &del.Kind, &del.Title, &del.Host, &fp, &u, &prov, &cMD, &dedup, &meta, &del.CreatedAt, &updAt, &gn, &pn, &subn); err != nil {
			return nil, err
		}
		del.FilePath = fp.String
		del.URL = u.String
		del.Provider = prov.String
		del.ContentMD = cMD.String
		del.DedupKey = dedup.String
		del.MetadataJSON = meta.String
		if updAt.Valid {
			del.UpdatedAt = updAt.Time
		}
		del.HasContentMD = strings.TrimSpace(del.ContentMD) != ""
		del.ContentBytes = len(del.ContentMD)
		del.ContentChars = len([]rune(del.ContentMD))

		var metrics *RetroMetrics
		if del.MetadataJSON != "" && del.MetadataJSON != "{}" {
			var m RetroMetrics
			if err := json.Unmarshal([]byte(del.MetadataJSON), &m); err == nil {
				metrics = &m
			}
		}

		task, cached := taskCache[del.TaskID]
		if !cached && del.TaskID != "" {
			task, _ = d.GetTaskByID(del.TaskID)
			taskCache[del.TaskID] = task
		}
		if task == nil {
			task = &Task{
				ID:             del.TaskID,
				GroupName:      gn.String,
				ProjectName:    pn.String,
				SubprojectName: subn.String,
			}
		}

		if metrics == nil && del.ContentMD != "" {
			metrics = extractRetroMetricsFromMarkdown(task, del.ContentMD)
		}

		effTime := del.UpdatedAt
		if metrics != nil && metrics.CompletedAt != nil && !metrics.CompletedAt.IsZero() {
			effTime = *metrics.CompletedAt
		} else if effTime.IsZero() {
			effTime = del.CreatedAt
		}

		if !filter.Since.IsZero() && effTime.Before(filter.Since) {
			continue
		}
		if !filter.Until.IsZero() && effTime.After(filter.Until) {
			continue
		}
		if filter.TaskID != "" && !strings.EqualFold(del.TaskID, filter.TaskID) {
			continue
		}
		if filter.Host != "" {
			match := strings.EqualFold(del.Host, filter.Host)
			if !match && task != nil {
				for _, w := range task.Workers {
					if strings.EqualFold(w.Host, filter.Host) {
						match = true
						break
					}
				}
			}
			if !match {
				continue
			}
		}
		if filter.Agent != "" {
			match := false
			if metrics != nil && strings.EqualFold(metrics.Agent, filter.Agent) {
				match = true
			}
			if !match && strings.EqualFold(del.Provider, filter.Agent) {
				match = true
			}
			if !match && task != nil {
				for _, w := range task.Workers {
					if strings.EqualFold(w.Agent, filter.Agent) {
						match = true
						break
					}
				}
			}
			if !match {
				continue
			}
		}
		if filter.NodePath != "" {
			normFilter := strings.Trim(strings.ToLower(filter.NodePath), "/")
			match := false
			taskNP := strings.Trim(strings.ToLower(TaskNodePath(task)), "/")
			if taskNP == normFilter || strings.HasPrefix(taskNP, normFilter+"/") {
				match = true
			}
			if !match && metrics != nil {
				mNP := strings.Trim(strings.ToLower(metrics.NodePath), "/")
				if mNP == normFilter || strings.HasPrefix(mNP, normFilter+"/") {
					match = true
				}
			}
			if !match && task != nil {
				for _, ws := range task.Workstreams {
					wsNP := strings.Trim(strings.ToLower(ws.RepoNodePath), "/")
					if wsNP == normFilter || strings.HasPrefix(wsNP, normFilter+"/") {
						match = true
						break
					}
				}
			}
			if !match {
				continue
			}
		}
		if filter.Repo != "" {
			rClean := strings.ToLower(strings.TrimSpace(filter.Repo))
			match := false
			if metrics != nil && strings.EqualFold(strings.ToLower(metrics.Repo), rClean) {
				match = true
			}
			if !match && task != nil {
				if strings.EqualFold(strings.ToLower(filepath.Base(TaskNodePath(task))), rClean) ||
					strings.EqualFold(strings.ToLower(task.ProjectName), rClean) ||
					strings.EqualFold(strings.ToLower(task.SubprojectName), rClean) {
					match = true
				}
				if !match {
					for _, ws := range task.Workstreams {
						if strings.EqualFold(strings.ToLower(filepath.Base(ws.RepoNodePath)), rClean) {
							match = true
							break
						}
					}
				}
			}
			if !match {
				continue
			}
		}

		taskTitle := ""
		if task != nil && task.Title != "" {
			taskTitle = task.Title
		} else {
			taskTitle = del.Title
		}

		nodePath := ""
		if metrics != nil && metrics.NodePath != "" {
			nodePath = metrics.NodePath
		} else if task != nil {
			nodePath = TaskNodePath(task)
		}

		repo := ""
		if metrics != nil && metrics.Repo != "" {
			repo = metrics.Repo
		} else if task != nil {
			repo = task.ProjectName
		}

		agent := ""
		if metrics != nil && metrics.Agent != "" {
			agent = metrics.Agent
		} else if task != nil {
			for _, w := range task.Workers {
				if w.Agent != "" {
					agent = w.Agent
					break
				}
			}
		}
		if agent == "" && del.Provider != "" && del.Provider != "local" {
			agent = del.Provider
		}

		branch := ""
		if metrics != nil && metrics.Branch != "" {
			branch = metrics.Branch
		} else if task != nil {
			branch = task.Branch
		}

		outcome := ""
		if metrics != nil && metrics.Outcome != "" {
			outcome = metrics.Outcome
		} else if task != nil {
			if strings.EqualFold(task.Status, "DONE") {
				outcome = "completed"
			} else {
				outcome = strings.ToLower(task.Status)
			}
		}

		s := RetroSummary{
			ID:           del.ID,
			TaskID:       del.TaskID,
			TaskTitle:    taskTitle,
			NodePath:     nodePath,
			Repo:         repo,
			Agent:        agent,
			Branch:       branch,
			Outcome:      outcome,
			Kind:         del.Kind,
			Title:        del.Title,
			Host:         del.Host,
			FilePath:     del.FilePath,
			URL:          del.URL,
			Provider:     del.Provider,
			HasContentMD: del.HasContentMD,
			ContentBytes: del.ContentBytes,
			ContentChars: del.ContentChars,
			DedupKey:     del.DedupKey,
			MetadataJSON: del.MetadataJSON,
			Metrics:      metrics,
			CreatedAt:    del.CreatedAt,
			UpdatedAt:    del.UpdatedAt,
		}
		candidates = append(candidates, retroCandidate{summary: s, effTime: effTime})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].effTime.Equal(candidates[j].effTime) {
			return candidates[i].summary.ID > candidates[j].summary.ID
		}
		return candidates[i].effTime.After(candidates[j].effTime)
	})

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	res := make([]RetroSummary, len(candidates))
	for i, c := range candidates {
		res[i] = c.summary
	}
	return res, nil
}

type DeliverablePageResponse struct {
	ID             string        `json:"id"`
	TaskID         string        `json:"task_id"`
	Kind           string        `json:"kind"`
	Title          string        `json:"title"`
	Host           string        `json:"host"`
	FilePath       string        `json:"file_path,omitempty"`
	URL            string        `json:"url,omitempty"`
	Provider       string        `json:"provider,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at,omitempty"`
	HasContentMD   bool          `json:"has_content_md"`
	ContentBytes   int           `json:"content_bytes"`
	Metrics        *RetroMetrics `json:"metrics,omitempty"`
	ContentMD      string        `json:"content_md"`
	TotalChars     int           `json:"total_chars"`
	CharOffset     int           `json:"char_offset"`
	ReturnedChars  int           `json:"returned_chars"`
	MaxChars       int           `json:"max_chars"`
	Truncated      bool          `json:"truncated"`
	NextCharOffset int           `json:"next_char_offset"`
}

func paginateDeliverableContent(d *TaskDeliverable, charOffset, maxChars int) DeliverablePageResponse {
	if maxChars <= 0 {
		maxChars = 20000
	} else if maxChars > 50000 {
		maxChars = 50000
	}
	if charOffset < 0 {
		charOffset = 0
	}

	runes := []rune(d.ContentMD)
	totalChars := len(runes)
	if charOffset > totalChars {
		charOffset = totalChars
	}
	end := charOffset + maxChars
	if end > totalChars {
		end = totalChars
	}
	page := string(runes[charOffset:end])
	returnedChars := end - charOffset
	truncated := end < totalChars
	nextCharOffset := 0
	if truncated {
		nextCharOffset = end
	}

	return DeliverablePageResponse{
		ID:             d.ID,
		TaskID:         d.TaskID,
		Kind:           d.Kind,
		Title:          d.Title,
		Host:           d.Host,
		FilePath:       d.FilePath,
		URL:            d.URL,
		Provider:       d.Provider,
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
		HasContentMD:   d.HasContentMD || strings.TrimSpace(d.ContentMD) != "",
		ContentBytes:   len(d.ContentMD),
		Metrics:        d.Metrics,
		ContentMD:      page,
		TotalChars:     totalChars,
		CharOffset:     charOffset,
		ReturnedChars:  returnedChars,
		MaxChars:       maxChars,
		Truncated:      truncated,
		NextCharOffset: nextCharOffset,
	}
}
