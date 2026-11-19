package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

type paneCheckState struct {
	lines         []string
	nonEmpty      []string
	bottomText    string
	hasIdlePrompt bool
	hasSpinner    bool
	hasCancel     bool
	ended         bool
	blockedReturn bool
}

func updateLastEventFromLogs(sess *agent.Session, logCandidates []string) bool {
	for _, dir := range logCandidates {
		logPath := filepath.Join(dir, "transcript.jsonl")
		if data, err := agentutil.ReadTail(logPath, 256*1024); err == nil && len(data) > 0 {
			tLines := strings.Split(string(data), "\n")
			for i := len(tLines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(tLines[i])
				if line == "" {
					continue
				}
				var cand transcriptStep
				if json.Unmarshal([]byte(line), &cand) == nil && cand.Type == "USER_INPUT" {
					if ut := parseStepTimestamp(cand.CreatedAt); !ut.IsZero() && (sess.LastEventAt.IsZero() || ut.After(sess.LastEventAt)) {
						sess.LastEventAt = ut
						return true
					}
					return false
				}
			}
			return false
		}
	}
	return false
}

func (p *Plugin) inspectTmuxPane(ctx context.Context, sess *agent.Session, hasSubs bool, changed *bool) paneCheckState {
	var st paneCheckState
	if sess.TmuxName == "" {
		return st
	}
	out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
	if err != nil {
		sess.State = agent.StateEnded
		sess.Activity = "Session ended (process exited)"
		sess.PID = 0
		sess.Blocked = nil
		*changed = true
		st.ended = true
		return st
	}
	if sess.PID > 0 && !agentutil.IsProcessAlive(sess.PID) {
		outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output()
		if errPs != nil || len(strings.TrimSpace(string(outPs))) == 0 {
			sess.State = agent.StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			*changed = true
			st.ended = true
			return st
		}
	}
	st.lines = strings.Split(string(out), "\n")
	for _, l := range st.lines {
		if t := strings.TrimSpace(l); t != "" {
			st.nonEmpty = append(st.nonEmpty, t)
		}
	}
	st.hasIdlePrompt = hasPromptInTail(st.nonEmpty, 4)
	st.bottomText = strings.Join(agentutil.ExtractTailLinesSlice(st.lines, 5), "\n")
	st.hasSpinner = !st.hasIdlePrompt && hasSpinnerText(st.bottomText)
	st.hasCancel = !st.hasIdlePrompt && strings.Contains(st.bottomText, "esc to cancel")

	if q, r, opts, matched := detectPermissionPrompt(st.lines, hasSubs); matched {
		since := time.Now()
		if sess.State == agent.StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == agent.BlockPermission &&
			sess.Blocked.Question == q && sess.Blocked.Reason == r && !sess.Blocked.Since.IsZero() {
			since = sess.Blocked.Since
		}
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockPermission ||
			sess.Blocked.Question != q || sess.Blocked.Reason != r || sess.Activity != "Waiting for user permission" {
			sess.State = agent.StateBlocked
			sess.Blocked = &agent.Blocked{Kind: agent.BlockPermission, Reason: r, Question: q, Options: opts, Since: since}
			sess.Activity = "Waiting for user permission"
			*changed = true
		}
		st.blockedReturn = true
		return st
	}
	if sess.State == agent.StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == agent.BlockRateLimit {
		if sess.Blocked.ResetAt == nil || sess.Blocked.ResetAt.After(time.Now()) {
			st.blockedReturn = true
			return st
		}
	} else if !st.hasSpinner && !st.hasCancel {
		bottom6 := strings.Join(agentutil.ExtractTailLinesSlice(st.nonEmpty, 6), "\n")
		if limited, resetAt, reason := p.DetectRateLimit(bottom6, time.Now()); limited {
			if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockRateLimit || sess.Blocked.Reason != reason {
				sess.State = agent.StateBlocked
				sess.Blocked = &agent.Blocked{
					Kind:     agent.BlockRateLimit,
					Reason:   reason,
					Question: reason,
					ResetAt:  &resetAt,
					Options:  []string{fmt.Sprintf("Resume at %s", resetAt.Local().Format("15:04")), "Resume in 1h"},
					Since:    time.Now(),
				}
				sess.Activity = "Rate limit: " + reason
				*changed = true
			}
			st.blockedReturn = true
			return st
		}
	}
	return st
}
