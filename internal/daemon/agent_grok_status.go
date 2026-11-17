package daemon

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/tmux"
)

func InspectGrokStatus(ctx context.Context, sess *Session) bool {
	if sess == nil || sess.Agent != "grok" || sess.State == StateEnded {
		return false
	}
	if sess.EngineType == EngineHeadless && sess.TmuxName == "" && (sess.PID <= 0 || !isProcessAlive(sess.PID)) {
		return false
	}

	changed := false
	home, _ := os.UserHomeDir()

	if meta := ReadGrokSessionMeta(home, sess.Cwd, sess.NativeID); meta != nil {
		if meta.ContextPct > 0 && sess.ContextPct != meta.ContextPct {
			sess.ContextPct, changed = meta.ContextPct, true
		}
		if meta.GitBranch != "" && sess.GitBranch != meta.GitBranch {
			sess.GitBranch, changed = meta.GitBranch, true
		}
		if meta.CustomTitle != "" && sess.CustomTitle != meta.CustomTitle {
			sess.CustomTitle, changed = meta.CustomTitle, true
		}
		if meta.AITitle != "" && sess.AITitle != meta.AITitle {
			sess.AITitle, changed = meta.AITitle, true
		}
		if sess.Name == "" || isRawSessionName(sess.Name) {
			if sess.CustomTitle != "" {
				sess.Name, changed = sess.CustomTitle, true
			} else if sess.AITitle != "" {
				sess.Name, changed = sess.AITitle, true
			} else if meta.Title != "" {
				sess.Name, changed = meta.Title, true
			}
		}
	}

	if subs, err := ExtractSubagents("grok", sess.NativeID, sess.Cwd); err == nil {
		running := 0
		for _, s := range subs {
			if s.State == "running" {
				running++
			}
		}
		if sess.RunningSubagents != running {
			sess.RunningSubagents, changed = running, true
		}
	}

	if sess.PID <= 0 && sess.TmuxName != "" {
		if pid, err := tmux.GetPID(ctx, sess.TmuxName); err == nil && pid > 0 {
			sess.PID, changed = pid, true
		}
	}

	if sess.TmuxName == "" {
		return changed
	}

	out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
	if err != nil {
		sess.State, sess.Activity, sess.PID, sess.Blocked = StateEnded, "Session ended (process exited)", 0, nil
		return true
	}

	if sess.PID > 0 && !isProcessAlive(sess.PID) {
		if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs != nil || len(strings.TrimSpace(string(outPs))) == 0 {
			sess.State, sess.Activity, sess.PID, sess.Blocked = StateEnded, "Session ended (process exited)", 0, nil
			return true
		}
	}

	tailText := extractTailText(strings.Split(string(out), "\n"), 25)

	if strings.Contains(tailText, "grok login") || strings.Contains(tailText, "Authentication failed") || strings.Contains(tailText, "not logged in") {
		if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockAuth {
			sess.State, sess.Blocked, sess.Activity = StateBlocked, &Blocked{
				Kind: BlockAuth, AuthKind: "agent", AuthTarget: "agent:grok", AuthServer: "grok",
				Reason: "Grok authentication required", Question: "Sign in with 'grok login'?",
				Options: []string{"Sign in", "Dismiss"}, Since: time.Now(),
			}, "Authentication required"
			return true
		}
		return changed
	}

	if limited, resetAt, reason := (GrokAgentAdapter{}).DetectRateLimit(tailText, time.Now()); limited {
		if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockRateLimit {
			sess.State, sess.Blocked, sess.Activity = StateBlocked, &Blocked{
				Kind: BlockRateLimit, Reason: reason, Question: reason, ResetAt: &resetAt,
				Options: []string{resetAt.Local().Format("15:04"), "Resume in 1h"}, Since: time.Now(),
			}, "Rate limited: "+reason
			return true
		}
		return changed
	}

	if strings.Contains(tailText, "requires approval") || strings.Contains(tailText, "Approval required") ||
		strings.Contains(tailText, "Authorize execution") || strings.Contains(tailText, "[y/N]") ||
		strings.Contains(tailText, "[Y/n]") || strings.Contains(tailText, "Do you want to proceed") {
		if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockPermission {
			sess.State, sess.Blocked, sess.Activity = StateBlocked, &Blocked{
				Kind: BlockPermission, Reason: "Tool permission requested", Question: "Permission required",
				Options: []string{"Allow", "Deny"}, Since: time.Now(),
			}, "Waiting for tool authorization"
			return true
		}
		return changed
	}

	if strings.Contains(tailText, "Approve plan") || strings.Contains(tailText, "Plan approval required") || strings.Contains(tailText, "approve implementation plan") {
		if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockApproval {
			sess.State, sess.Blocked, sess.Activity = StateBlocked, &Blocked{
				Kind: BlockApproval, Reason: "Plan review requested", Question: "Approve implementation plan?",
				Options: []string{"Approve", "Request Changes"}, Since: time.Now(),
			}, "Waiting for plan approval"
			return true
		}
		return changed
	}

	if strings.Contains(tailText, "Waiting for user response") || strings.Contains(tailText, "Waiting for input") || strings.Contains(tailText, "request_user_input") {
		if sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockQuestion {
			sess.State, sess.Blocked, sess.Activity = StateBlocked, &Blocked{
				Kind: BlockQuestion, Reason: "Waiting for user response", Question: "Waiting for user response",
				Options: nil, Since: time.Now(),
			}, "Waiting for user response"
			return true
		}
		return changed
	}

	if strings.Contains(tailText, "Subagent running:") || strings.Contains(tailText, "Thinking") || strings.Contains(tailText, "Esc to interrupt") {
		if sess.State == StateIdle {
			sess.State, sess.Activity, changed = StateWorking, "Thinking...", true
		}
		return changed
	}

	if sess.State == StateBlocked {
		sess.State, sess.Blocked, sess.Activity, changed = StateIdle, nil, "Awaiting user prompt", true
	}

	return changed
}
