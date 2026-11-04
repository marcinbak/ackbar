package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const AckbarNotificationIconURL = "https://raw.githubusercontent.com/marcinbak/ackbar/main/web/icons/icon-192.png"

type NtfyAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
	Clear  bool   `json:"clear,omitempty"`
}

type NtfyPayload struct {
	Topic    string       `json:"topic"`
	Title    string       `json:"title"`
	Message  string       `json:"message"`
	Priority int          `json:"priority,omitempty"`
	Tags     []string     `json:"tags,omitempty"`
	Icon     string       `json:"icon,omitempty"`
	Click    string       `json:"click,omitempty"`
	Actions  []NtfyAction `json:"actions,omitempty"`
	Server   string       `json:"-"`
}

func (s *Server) sendNtfy(ctx context.Context, payload NtfyPayload) error {
	topic := payload.Topic
	if topic == "" {
		topic = s.NtfyTopic()
	}
	if topic == "" {
		return nil
	}
	payload.Topic = topic

	if payload.Icon == "" {
		payload.Icon = AckbarNotificationIconURL
	}

	serverURL := strings.TrimSpace(payload.Server)
	if serverURL == "" {
		serverURL = strings.TrimSpace(s.NtfyServer())
	}
	if serverURL != "" && !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "https://" + serverURL
	}
	serverURL = strings.TrimRight(serverURL, "/")
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal ntfy payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create ntfy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := s.ntfyClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send ntfy request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("ntfy responded with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (s *Server) dispatchNtfy(payload NtfyPayload) {
	if s.ntfyHook != nil {
		s.ntfyHook(payload)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.sendNtfy(ctx, payload); err != nil {
			log.Printf("[Ntfy] Failed to send notification: %v", err)
		}
	}()
}

func resolveTaskSessionID(task *Task) string {
	if task == nil {
		return ""
	}
	for _, w := range task.Workers {
		if w.IsActive && w.SessionID != "" {
			return w.SessionID
		}
	}
	for _, w := range task.Workers {
		if w.SessionID != "" {
			return w.SessionID
		}
	}
	return ""
}

func (s *Server) resolveTaskSession(task *Task) *Session {
	if task == nil {
		return nil
	}
	sessionID := resolveTaskSessionID(task)
	if sessionID != "" {
		if s.db != nil {
			if sess, err := s.db.GetSession(sessionID); err == nil && sess != nil {
				return sess
			}
		}
		return &Session{ID: sessionID}
	}
	if s.db != nil {
		sessions, err := s.db.ListSessions()
		if err == nil {
			var candidate *Session
			for _, sess := range sessions {
				if sess.Deleted {
					continue
				}
				if sess.CurrentTaskID == task.ID {
					if sess.State != StateEnded {
						return sess
					}
					if candidate == nil {
						candidate = sess
					}
				}
			}
			if candidate != nil {
				return candidate
			}
			if task.Branch != "" && !strings.EqualFold(task.Branch, "main") && !strings.EqualFold(task.Branch, "master") && !strings.EqualFold(task.Branch, "develop") && !strings.EqualFold(task.Branch, "trunk") && !strings.EqualFold(task.Branch, "dev") {
				for _, sess := range sessions {
					if sess.Deleted {
						continue
					}
					if sess.GitBranch == task.Branch {
						if sess.State != StateEnded {
							return sess
						}
						if candidate == nil {
							candidate = sess
						}
					}
				}
				if candidate != nil {
					return candidate
				}
			}
		}
	}
	return nil
}

func formatSessionDisplayName(sess *Session, fallbackTitle string) string {
	if sess != nil {
		if sess.CustomTitle != "" {
			return sess.CustomTitle
		}
		if sess.Name != "" {
			return sess.Name
		}
	}
	if fallbackTitle != "" {
		return fallbackTitle
	}
	if sess != nil {
		if sess.ProjectKey != "" {
			return sess.ProjectKey
		}
		if sess.ID != "" {
			return sess.ID
		}
	}
	return "Ackbar Session"
}

func formatGroupPathSubtitle(sess *Session, task *Task) string {
	if sess != nil && sess.NodePath != "" && !strings.EqualFold(sess.NodePath, "unassigned") {
		parts := strings.Split(sess.NodePath, "/")
		var clean []string
		for _, p := range parts {
			t := strings.TrimSpace(p)
			if t != "" {
				clean = append(clean, t)
			}
		}
		if len(clean) > 0 {
			return strings.Join(clean, " / ")
		}
	}
	if task != nil {
		var parts []string
		for _, seg := range []string{task.GroupName, task.ProjectName, task.SubprojectName} {
			trimmed := strings.TrimSpace(seg)
			if trimmed == "" {
				continue
			}
			if len(parts) > 0 && strings.EqualFold(parts[len(parts)-1], trimmed) {
				continue
			}
			parts = append(parts, trimmed)
		}
		if len(parts) > 0 {
			return strings.Join(parts, " / ")
		}
	}
	if sess != nil && sess.ProjectKey != "" {
		return sess.ProjectKey
	}
	return ""
}

func (s *Server) buildTaskReviewPayload(oldStatus string, task *Task) *NtfyPayload {
	if task == nil || s.NtfyTopic() == "" {
		return nil
	}
	if !strings.EqualFold(task.Status, "REVIEW") || strings.EqualFold(oldStatus, "REVIEW") {
		return nil
	}

	sess := s.resolveTaskSession(task)

	var click string
	if sess != nil && sess.ID != "" {
		click = fmt.Sprintf("ackbar://session?id=%s", url.QueryEscape(sess.ID))
	} else {
		click = fmt.Sprintf("ackbar://task?id=%s", url.QueryEscape(task.ID))
	}

	title := formatSessionDisplayName(sess, task.Title)

	var lines []string
	if groupPath := formatGroupPathSubtitle(sess, task); groupPath != "" {
		lines = append(lines, fmt.Sprintf("📂 %s", groupPath))
	}

	statusLine := "🟢 PR Review Needed"
	if strings.EqualFold(task.Substatus, "plan_review") {
		statusLine = "🟢 Plan Review Needed"
	} else if task.PRNumber > 0 {
		statusLine = fmt.Sprintf("🟢 PR Review Needed (#%d)", task.PRNumber)
	}
	lines = append(lines, statusLine)

	if sess != nil && (sess.Name != "" || sess.CustomTitle != "") && task.Title != "" && !strings.EqualFold(title, task.Title) {
		lines = append(lines, fmt.Sprintf("Task: %s", task.Title))
	}
	message := strings.Join(lines, "\n")

	var actions []NtfyAction
	if task.PRURL != "" && task.PRURL != "-" {
		label := "Open PR"
		if task.PRNumber > 0 {
			label = fmt.Sprintf("Open PR #%d", task.PRNumber)
		}
		actions = append(actions, NtfyAction{
			Action: "view",
			Label:  label,
			URL:    task.PRURL,
		})
	}

	return &NtfyPayload{
		Topic:    s.NtfyTopic(),
		Title:    title,
		Message:  message,
		Priority: 4,
		Tags:     []string{"eyes", "pull_request", "ackbar"},
		Icon:     AckbarNotificationIconURL,
		Click:    click,
		Actions:  actions,
	}
}

func (s *Server) maybeNotifyTaskReview(oldStatus string, task *Task) {
	payload := s.buildTaskReviewPayload(oldStatus, task)
	if payload != nil {
		s.dispatchNtfy(*payload)
	}
}

func (s *Server) buildTaskBlockedPayload(oldSubstatus, oldQuestion string, task *Task) *NtfyPayload {
	if task == nil || s.NtfyTopic() == "" {
		return nil
	}

	isBlockedNow := strings.EqualFold(task.Substatus, "blocked")
	wasBlocked := strings.EqualFold(oldSubstatus, "blocked")
	questionChanged := task.BlockerQuestion != "" && task.BlockerQuestion != oldQuestion
	if !isBlockedNow || (wasBlocked && !questionChanged) {
		return nil
	}

	sess := s.resolveTaskSession(task)

	var click string
	if sess != nil && sess.ID != "" {
		click = fmt.Sprintf("ackbar://session?id=%s", url.QueryEscape(sess.ID))
	} else {
		click = fmt.Sprintf("ackbar://task?id=%s", url.QueryEscape(task.ID))
	}

	title := formatSessionDisplayName(sess, task.Title)

	q := task.BlockerQuestion
	if q == "" {
		q = "Task is blocked waiting for input"
	}

	var lines []string
	if groupPath := formatGroupPathSubtitle(sess, task); groupPath != "" {
		lines = append(lines, fmt.Sprintf("📂 %s", groupPath))
	}
	lines = append(lines, fmt.Sprintf("❓ Blocked: %s", q))
	message := strings.Join(lines, "\n")

	return &NtfyPayload{
		Topic:    s.NtfyTopic(),
		Title:    title,
		Message:  message,
		Priority: 4,
		Tags:     []string{"warning", "hand", "ackbar"},
		Icon:     AckbarNotificationIconURL,
		Click:    click,
	}
}

func (s *Server) maybeNotifyTaskBlocked(oldSubstatus, oldQuestion string, task *Task) {
	payload := s.buildTaskBlockedPayload(oldSubstatus, oldQuestion, task)
	if payload != nil {
		s.dispatchNtfy(*payload)
	}
}

func (s *Server) buildSessionBlockedPayload(oldState State, sess *Session) *NtfyPayload {
	if sess == nil || s.NtfyTopic() == "" || sess.State != StateBlocked || oldState == StateBlocked {
		return nil
	}

	title := formatSessionDisplayName(sess, "")

	detail := ""
	if sess.Blocked != nil {
		if sess.Blocked.Question != "" {
			detail = sess.Blocked.Question
		} else if sess.Blocked.Reason != "" {
			detail = sess.Blocked.Reason
		}
	}
	if detail == "" {
		detail = sess.Activity
	}
	if detail == "" {
		detail = "Session is waiting for your input"
	}

	prefix := "❓ Question: "
	if sess.Blocked != nil && sess.Blocked.Kind == BlockPermission {
		prefix = "🔒 Permission Needed: "
	}

	var lines []string
	if groupPath := formatGroupPathSubtitle(sess, nil); groupPath != "" {
		lines = append(lines, fmt.Sprintf("📂 %s", groupPath))
	}
	lines = append(lines, fmt.Sprintf("%s%s", prefix, detail))
	message := strings.Join(lines, "\n")

	return &NtfyPayload{
		Topic:    s.NtfyTopic(),
		Title:    title,
		Message:  message,
		Priority: 4,
		Tags:     []string{"warning", "hand", "ackbar"},
		Icon:     AckbarNotificationIconURL,
		Click:    fmt.Sprintf("ackbar://session?id=%s", url.QueryEscape(sess.ID)),
	}
}

func (s *Server) maybeNotifySessionBlocked(oldState State, sess *Session) {
	payload := s.buildSessionBlockedPayload(oldState, sess)
	if payload != nil {
		s.dispatchNtfy(*payload)
	}
}

func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NtfyTopic  string `json:"ntfy_topic"`
		NtfyServer string `json:"ntfy_server"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	topic := strings.TrimSpace(req.NtfyTopic)
	if topic == "" {
		topic = s.NtfyTopic()
	}
	if topic == "" {
		http.Error(w, "ntfy_topic is not configured", http.StatusBadRequest)
		return
	}

	serverURL := strings.TrimSpace(req.NtfyServer)
	if serverURL == "" {
		serverURL = strings.TrimSpace(s.NtfyServer())
	}
	if serverURL != "" && !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "https://" + serverURL
	}
	serverURL = strings.TrimRight(serverURL, "/")
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}

	parsedURL, err := url.ParseRequestURI(serverURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		http.Error(w, "Invalid ntfy_server URL", http.StatusBadRequest)
		return
	}

	hostName := parsedURL.Hostname()
	if hostName == "169.254.169.254" || strings.HasPrefix(hostName, "169.254.") {
		http.Error(w, "Invalid ntfy_server host", http.StatusBadRequest)
		return
	}
	if ip := net.ParseIP(hostName); ip != nil && ip.IsLinkLocalUnicast() {
		http.Error(w, "Invalid ntfy_server host", http.StatusBadRequest)
		return
	}

	payload := NtfyPayload{
		Topic:    topic,
		Title:    fmt.Sprintf("Ackbar (%s)", s.DisplayName()),
		Message:  "📂 Personal / Ackbar\n🔔 Test notification from Ackbar Web UI",
		Priority: 4,
		Tags:     []string{"white_check_mark", "ackbar"},
		Icon:     AckbarNotificationIconURL,
		Click:    "ackbar://session",
		Server:   serverURL,
	}

	if err := s.sendNtfy(r.Context(), payload); err != nil {
		log.Printf("[Ntfy] Test notification failed: %v", err)
		http.Error(w, "Failed to send test notification to ntfy server", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

func (s *Server) propagateSettingsToFleet(subset map[string]string) {
	if s.db == nil || len(subset) == 0 {
		return
	}
	if strings.TrimSpace(subset["ntfy_topic"]) == "" {
		return
	}
	hosts, err := s.db.ListHosts()
	if err != nil || len(hosts) == 0 {
		return
	}

	payloadMap := make(map[string]string)
	for k, v := range subset {
		if k != "propagate_ntfy" {
			payloadMap[k] = v
		}
	}
	bodyBytes, err := json.Marshal(payloadMap)
	if err != nil {
		return
	}

	token := s.configuredToken
	if token == "" {
		token = os.Getenv("ACKBAR_TOKEN")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	for _, h := range hosts {
		if h.Name == "local" || h.Name == s.HostName() || h.URL == "" || s.isLocalHost(h.Name) {
			continue
		}
		targetURL := fmt.Sprintf("%s/v1/settings", strings.TrimSuffix(h.URL, "/"))
		req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Ackbar-Token", token)
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[Settings] Failed to propagate settings to host %s (%s): %v", h.Name, targetURL, err)
			continue
		}
		resp.Body.Close()
	}
}
