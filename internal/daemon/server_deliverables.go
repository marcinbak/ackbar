package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type TaskDeliverablePayload struct {
	TaskID    string `json:"task_id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Type      string `json:"type"`
	FilePath  string `json:"file_path"`
	URL       string `json:"url"`
	Provider  string `json:"provider"`
	ContentMD string `json:"content_md"`
	Host      string `json:"host"`
}

func parseFilterTime(val string, isUntil bool) (time.Time, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", val); err == nil {
		if isUntil {
			return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.UTC), nil
		}
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return time.Time{}, errors.New("must be RFC3339 or YYYY-MM-DD")
}

func isReadableTextDeliverable(kind, filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != "" {
		switch ext {
		case ".md", ".markdown", ".txt", ".json", ".yaml", ".yml", ".html", ".htm", ".csv", ".log", ".rst":
			return true
		default:
			return false
		}
	}
	return isRetroKind(kind) || kind == "plan" || kind == "prd" || kind == "learnings" || kind == "doc"
}

func (s *Server) handleTaskDeliverable(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		delID := strings.TrimSpace(r.URL.Query().Get("id"))
		if delID == "" {
			delID = strings.TrimSpace(r.URL.Query().Get("deliverable_id"))
		}
		taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		if kind == "" {
			kind = strings.TrimSpace(r.URL.Query().Get("type"))
		}

		if delID == "" && taskID == "" {
			http.Error(w, "deliverable id or task_id is required", http.StatusBadRequest)
			return
		}

		del, err := s.db.GetTaskDeliverableLookup(delID, taskID, kind)
		if err != nil {
			http.Error(w, "Failed to get deliverable: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if del == nil {
			http.Error(w, "Deliverable not found", http.StatusNotFound)
			return
		}

		if strings.TrimSpace(del.ContentMD) == "" && del.FilePath != "" && isReadableTextDeliverable(del.Kind, del.FilePath) {
			if del.Host == "" || s.isLocalHost(del.Host) {
				task, _ := s.db.GetTaskByID(del.TaskID)
				content, err := s.confineAndReadDeliverableFile(task, del.FilePath)
				if err == nil && content != "" {
					del.ContentMD = content
					if isRetroKind(del.Kind) {
						s.extractRetroMetrics(task, del)
					}
					if changed, err := s.db.UpsertTaskDeliverableLWW(del, false); err == nil && changed {
						s.pushSyncPayload(&SyncStatePayload{Deliverables: []TaskDeliverable{*del}})
					}
				}
			}
		}

		hasPagination := r.URL.Query().Has("max_chars") || r.URL.Query().Has("char_offset")
		w.Header().Set("Content-Type", "application/json")
		if hasPagination {
			maxChars, _ := strconv.Atoi(r.URL.Query().Get("max_chars"))
			charOffset, _ := strconv.Atoi(r.URL.Query().Get("char_offset"))
			pageResp := paginateDeliverableContent(del, charOffset, maxChars)
			json.NewEncoder(w).Encode(pageResp)
			return
		}

		json.NewEncoder(w).Encode(del)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4*1024*1024)
	var p TaskDeliverablePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	p.TaskID = strings.TrimSpace(p.TaskID)
	if p.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	kind := normalizeDeliverableKind(p.Kind, p.Type, p.FilePath)
	title := strings.TrimSpace(p.Title)
	if title == "" {
		if isRetroKind(kind) {
			title = "Retrospective"
		} else if strings.HasSuffix(strings.ToLower(p.FilePath), ".brief.md") {
			title = "Change Brief"
		} else if p.FilePath != "" {
			title = filepath.Base(p.FilePath)
		} else {
			title = "Deliverable"
		}
	}

	filePath := strings.TrimSpace(p.FilePath)
	urlStr := strings.TrimSpace(p.URL)
	contentMD := p.ContentMD

	if filePath == "" && urlStr == "" && strings.TrimSpace(contentMD) == "" {
		http.Error(w, "either file_path, url, or content_md is required", http.StatusBadRequest)
		return
	}

	if filePath != "" {
		filePath = strings.TrimPrefix(filePath, "file://")
		for _, seg := range strings.Split(filepath.ToSlash(filePath), "/") {
			if seg == ".." {
				http.Error(w, "path traversal detected in file_path", http.StatusBadRequest)
				return
			}
		}
		filePath = filepath.Clean(filePath)
		if isSensitivePath(filePath, s.effectiveHomeDir()) || isSystemDirectory(filePath) {
			http.Error(w, "deliverable file path is not confined to allowed roots", http.StatusForbidden)
			return
		}
	}

	if urlStr != "" {
		lowerURL := strings.ToLower(urlStr)
		if !strings.HasPrefix(lowerURL, "http://") && !strings.HasPrefix(lowerURL, "https://") {
			http.Error(w, "url must begin with http:// or https://", http.StatusBadRequest)
			return
		}
	}

	provider := strings.TrimSpace(p.Provider)
	if provider == "" {
		if strings.Contains(urlStr, "docs.google.com/document") {
			provider = "gdocs"
		} else if strings.Contains(urlStr, "docs.google.com/presentation") {
			provider = "gslides"
		} else if strings.Contains(urlStr, "docs.google.com/spreadsheets") {
			provider = "gsheets"
		} else if strings.Contains(urlStr, "drive.google.com") {
			provider = "gdrive"
		} else if strings.Contains(urlStr, "claude.ai") || strings.Contains(urlStr, "claude.site") {
			provider = "claude_artifact"
		} else if strings.Contains(urlStr, "github.com") {
			provider = "github"
		} else if filePath != "" {
			provider = "local"
		}
	}

	task, err := s.db.GetTaskByID(p.TaskID)
	if err != nil {
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	if contentMD == "" && filePath != "" && isReadableTextDeliverable(kind, filePath) {
		if p.Host == "" || s.isLocalHost(p.Host) {
			readContent, readErr := s.confineAndReadDeliverableFile(task, filePath)
			if errors.Is(readErr, ErrPathNotConfined) {
				http.Error(w, "deliverable file path is not confined to allowed roots", http.StatusForbidden)
				return
			}
			if errors.Is(readErr, ErrDeliverableTooLarge) {
				http.Error(w, "deliverable file exceeds 2MB limit", http.StatusRequestEntityTooLarge)
				return
			}
			if readErr == nil {
				contentMD = readContent
			} else if !os.IsNotExist(readErr) && !errors.Is(readErr, ErrBinaryDeliverable) {
				http.Error(w, "Failed to read deliverable file: "+readErr.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	hostName := p.Host
	if hostName == "" {
		hostName = s.HostName()
	}

	del := &TaskDeliverable{
		TaskID:    p.TaskID,
		Kind:      kind,
		Title:     title,
		Host:      hostName,
		FilePath:  filePath,
		URL:       urlStr,
		Provider:  provider,
		ContentMD: contentMD,
	}

	if isRetroKind(del.Kind) || strings.HasSuffix(strings.ToLower(del.FilePath), ".retro.md") {
		s.extractRetroMetrics(task, del)
	}

	if _, err := s.db.UpsertTaskDeliverableLWW(del, false); err != nil {
		http.Error(w, "Failed to insert deliverable: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if task != nil && len(task.Workers) > 0 {
		if sess, _ := s.db.GetSession(task.Workers[0].SessionID); sess != nil {
			s.broadcast(sess)
		}
	}
	s.pushSyncPayload(&SyncStatePayload{Deliverables: []TaskDeliverable{*del}})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(del)
}

func (s *Server) handleListRetros(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var filter RetroFilter
	sinceStr := strings.TrimSpace(r.URL.Query().Get("since"))
	if sinceStr != "" {
		t, err := parseFilterTime(sinceStr, false)
		if err != nil {
			http.Error(w, "invalid since timestamp: "+err.Error(), http.StatusBadRequest)
			return
		}
		filter.Since = t
	}
	untilStr := strings.TrimSpace(r.URL.Query().Get("until"))
	if untilStr != "" {
		t, err := parseFilterTime(untilStr, true)
		if err != nil {
			http.Error(w, "invalid until timestamp: "+err.Error(), http.StatusBadRequest)
			return
		}
		filter.Until = t
	}

	filter.NodePath = strings.TrimSpace(r.URL.Query().Get("node_path"))
	filter.Repo = strings.TrimSpace(r.URL.Query().Get("repo"))
	filter.Host = strings.TrimSpace(r.URL.Query().Get("host"))
	filter.Agent = strings.TrimSpace(r.URL.Query().Get("agent"))
	filter.TaskID = strings.TrimSpace(r.URL.Query().Get("task_id"))
	if limStr := strings.TrimSpace(r.URL.Query().Get("limit")); limStr != "" {
		if lim, err := strconv.Atoi(limStr); err == nil {
			filter.Limit = lim
		}
	}

	summaries, err := s.db.ListRetros(filter)
	if err != nil {
		http.Error(w, "Failed to list retros: "+err.Error(), http.StatusInternalServerError)
		return
	}

	effectiveLimit := filter.Limit
	if effectiveLimit <= 0 {
		effectiveLimit = 50
	} else if effectiveLimit > 200 {
		effectiveLimit = 200
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"retros": summaries,
		"count":  len(summaries),
		"limit":  effectiveLimit,
	})
}

func (s *Server) handleDeliverables(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.Trim(strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/v1/deliverables/")), "/")
	if id == "" || id == "v1/deliverables" {
		id = strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			id = strings.TrimSpace(r.URL.Query().Get("deliverable_id"))
		}
	}

	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = strings.TrimSpace(r.URL.Query().Get("type"))
	}

	if id == "" && taskID == "" {
		http.Error(w, "either id or task_id is required", http.StatusBadRequest)
		return
	}

	del, err := s.db.GetTaskDeliverableLookup(id, taskID, kind)
	if err != nil {
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if del == nil {
		http.Error(w, "Deliverable not found", http.StatusNotFound)
		return
	}

	if strings.TrimSpace(del.ContentMD) == "" && del.FilePath != "" && isReadableTextDeliverable(del.Kind, del.FilePath) {
		if del.Host == "" || s.isLocalHost(del.Host) {
			task, _ := s.db.GetTaskByID(del.TaskID)
			content, err := s.confineAndReadDeliverableFile(task, del.FilePath)
			if err == nil && content != "" {
				del.ContentMD = content
				if isRetroKind(del.Kind) {
					s.extractRetroMetrics(task, del)
				}
				if changed, err := s.db.UpsertTaskDeliverableLWW(del, false); err == nil && changed {
					s.pushSyncPayload(&SyncStatePayload{Deliverables: []TaskDeliverable{*del}})
				}
			}
		}
	}

	maxChars, _ := strconv.Atoi(r.URL.Query().Get("max_chars"))
	charOffset, _ := strconv.Atoi(r.URL.Query().Get("char_offset"))
	pageResp := paginateDeliverableContent(del, charOffset, maxChars)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pageResp)
}
