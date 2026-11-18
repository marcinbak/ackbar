package opencode

import (
	"context"
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
	return "opencode"
}

func (*Plugin) ResumeCommand(nativeID string) string {
	if nativeID != "" && agentutil.IsSafeSessionID(nativeID) {
		return "opencode resume " + nativeID
	}
	return "opencode"
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
		Command:         "opencode",
		Args:            args,
		Env:             maps.Clone(in.Env),
		CommandLine:     cmd,
		PromptInCommand: false,
	}, nil
}

func (*Plugin) InspectStatus(ctx context.Context, sess *agent.Session) bool {
	if sess == nil || (sess.Agent != "opencode" && sess.Agent != "open-code") || sess.State == agent.StateEnded {
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

		tailText := agentutil.ExtractTailLines(string(out), 25)

		if strings.Contains(tailText, "requires approval") ||
			strings.Contains(tailText, "Approval required") ||
			strings.Contains(tailText, "Permission requested") ||
			strings.Contains(tailText, "Authorize execution") ||
			strings.Contains(tailText, "Do you want to run") ||
			strings.Contains(tailText, "Do you want to proceed") ||
			strings.Contains(tailText, "Allow once") ||
			strings.Contains(tailText, "[y/N]") ||
			strings.Contains(tailText, "[Y/n]") ||
			strings.Contains(tailText, "Are you sure?") {
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
			strings.Contains(tailText, "Waiting for input") ||
			strings.Contains(tailText, "request_user_input") ||
			strings.Contains(tailText, "Enter to select") ||
			strings.Contains(tailText, "Type something") {
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

		if sess.State == agent.StateBlocked {
			sess.State = agent.StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
	}

	return changed
}
