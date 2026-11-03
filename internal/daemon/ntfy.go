package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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
	Click    string       `json:"click,omitempty"`
	Actions  []NtfyAction `json:"actions,omitempty"`
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

	serverURL := strings.TrimRight(s.NtfyServer(), "/")
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

func (s *Server) buildTaskReviewPayload(oldStatus string, task *Task) *NtfyPayload {
	if task == nil || s.NtfyTopic() == "" {
		return nil
	}
	if !strings.EqualFold(task.Status, "REVIEW") || strings.EqualFold(oldStatus, "REVIEW") {
		return nil
	}

	var click string
	activeSessionID := resolveTaskSessionID(task)
	if activeSessionID != "" {
		click = fmt.Sprintf("ackbar://session?id=%s", url.QueryEscape(activeSessionID))
	} else {
		click = fmt.Sprintf("ackbar://task?id=%s", url.QueryEscape(task.ID))
	}

	title := fmt.Sprintf("PR Review Needed: %s", task.Title)
	if strings.EqualFold(task.Substatus, "plan_review") {
		title = fmt.Sprintf("Plan Review Needed: %s", task.Title)
	}

	var parts []string
	if task.ProjectName != "" {
		parts = append(parts, fmt.Sprintf("Project: %s", task.ProjectName))
	}
	if task.Branch != "" {
		parts = append(parts, fmt.Sprintf("Branch: %s", task.Branch))
	}
	if task.PRNumber > 0 {
		parts = append(parts, fmt.Sprintf("PR: #%d", task.PRNumber))
	} else if task.PRURL != "" && task.PRURL != "-" {
		parts = append(parts, fmt.Sprintf("PR: %s", task.PRURL))
	}
	message := strings.Join(parts, "\n")
	if message == "" {
		message = fmt.Sprintf("Review required for task %s", task.Title)
	}

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

	var click string
	activeSessionID := resolveTaskSessionID(task)
	if activeSessionID != "" {
		click = fmt.Sprintf("ackbar://attention?session_id=%s", url.QueryEscape(activeSessionID))
	} else {
		click = fmt.Sprintf("ackbar://task?id=%s", url.QueryEscape(task.ID))
	}

	title := fmt.Sprintf("Task Blocked: %s", task.Title)

	q := task.BlockerQuestion
	if q == "" {
		q = "Task is blocked waiting for input"
	}
	message := q
	if task.ProjectName != "" {
		message = fmt.Sprintf("[%s] %s", task.ProjectName, q)
	}

	return &NtfyPayload{
		Topic:    s.NtfyTopic(),
		Title:    title,
		Message:  message,
		Priority: 4,
		Tags:     []string{"warning", "hand", "ackbar"},
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

	sessionDisplayName := sess.Name
	if sessionDisplayName == "" {
		sessionDisplayName = sess.ProjectKey
	}
	if sessionDisplayName == "" {
		sessionDisplayName = sess.ID
	}

	title := fmt.Sprintf("Question: %s", sessionDisplayName)
	if sess.Blocked != nil && sess.Blocked.Kind == BlockPermission {
		title = fmt.Sprintf("Permission Needed: %s", sessionDisplayName)
	}

	message := ""
	if sess.Blocked != nil {
		if sess.Blocked.Question != "" {
			message = sess.Blocked.Question
		} else if sess.Blocked.Reason != "" {
			message = sess.Blocked.Reason
		}
	}
	if message == "" {
		message = sess.Activity
	}
	if message == "" {
		message = "Session is waiting for your input"
	}

	return &NtfyPayload{
		Topic:    s.NtfyTopic(),
		Title:    title,
		Message:  message,
		Priority: 4,
		Tags:     []string{"warning", "hand", "ackbar"},
		Click:    fmt.Sprintf("ackbar://attention?session_id=%s", url.QueryEscape(sess.ID)),
	}
}

func (s *Server) maybeNotifySessionBlocked(oldState State, sess *Session) {
	payload := s.buildSessionBlockedPayload(oldState, sess)
	if payload != nil {
		s.dispatchNtfy(*payload)
	}
}
