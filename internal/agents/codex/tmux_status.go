package codex

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/tmux"
)

func (*Plugin) SpawnCommand(tempUUID string) string {
	_ = tempUUID
	return "codex exec"
}

func (*Plugin) ResumeCommand(nativeID string) string {
	if nativeID != "" && agentutil.IsUUID(nativeID) {
		return "codex exec resume " + nativeID
	}
	return "codex exec"
}

func (p *Plugin) BuildLaunchSpec(in agent.LaunchInput) (agent.LaunchSpec, error) {
	cmd := p.SpawnCommand(in.TempUUID)
	if in.Resume {
		cmd = p.ResumeCommand(in.NativeID)
	}
	var args []string
	if fields := strings.Fields(cmd); len(fields) > 1 {
		args = fields[1:]
	}
	return agent.LaunchSpec{
		Command:         "codex",
		Args:            args,
		Env:             maps.Clone(in.Env),
		CommandLine:     cmd,
		PromptInCommand: false,
	}, nil
}

func (p *Plugin) InspectStatus(ctx context.Context, sess *agent.Session) bool {
	if sess == nil || sess.Agent != "codex" || sess.State == agent.StateEnded {
		return false
	}
	changed := false

	if sess.PID <= 0 && sess.TmuxName != "" {
		if pid, err := tmux.GetPID(ctx, sess.TmuxName); err == nil && pid > 0 {
			sess.PID = pid
			changed = true
		}
	}

	if sess.TmuxName != "" {
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
		if err != nil {
			sess.State = agent.StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			return true
		}

		if sess.PID > 0 && !agentutil.IsProcessAlive(sess.PID) {
			if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs == nil && len(strings.TrimSpace(string(outPs))) > 0 {
			} else {
				sess.State = agent.StateEnded
				sess.Activity = "Session ended (process exited)"
				sess.PID = 0
				sess.Blocked = nil
				return true
			}
		}

		paneText := string(out)
		tailText := agentutil.ExtractTailLines(paneText, 25)

		if strings.Contains(tailText, "requires approval") ||
			strings.Contains(tailText, "Approval required") ||
			strings.Contains(tailText, "Authorize execution") ||
			strings.Contains(tailText, "[y/N]") ||
			strings.Contains(tailText, "[Y/n]") ||
			strings.Contains(tailText, "Do you want to proceed") {
			if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockPermission {
				sess.State = agent.StateBlocked
				sess.Blocked = &agent.Blocked{
					Kind:     agent.BlockPermission,
					Reason:   "Tool permission requested",
					Question: "Permission required",
					Options:  []string{"Allow", "Deny"},
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for tool authorization"
				changed = true
			}
			return changed
		}

		if strings.Contains(tailText, "Waiting for user response") ||
			strings.Contains(tailText, "request_user_input") {
			if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockQuestion {
				sess.State = agent.StateBlocked
				sess.Blocked = &agent.Blocked{
					Kind:     agent.BlockQuestion,
					Reason:   "Waiting for user response",
					Question: "Waiting for user response",
					Options:  nil,
					Since:    time.Now(),
				}
				sess.Activity = "Waiting for user response"
				changed = true
			}
			return changed
		}

		if sess.State == agent.StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == agent.BlockRateLimit {
			if sess.Blocked.ResetAt == nil || sess.Blocked.ResetAt.After(time.Now()) {
				return changed
			}
		} else {
			bottom6Text := agentutil.ExtractTailLines(paneText, 6)
			if limited, resetAt, reason := p.DetectRateLimit(bottom6Text, time.Now()); limited {
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
					changed = true
				}
				return changed
			}
		}

		if sess.State == agent.StateBlocked {
			if sess.Blocked != nil && sess.Blocked.Kind == agent.BlockRateLimit && sess.Blocked.ResetAt != nil && sess.Blocked.ResetAt.After(time.Now()) {
				return changed
			}
			sess.State = agent.StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
	}

	return changed
}
