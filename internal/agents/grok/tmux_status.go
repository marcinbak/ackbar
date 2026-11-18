package grok

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/tmux"
)

func (*Plugin) SpawnCommand(tempUUID string) string {
	if tempUUID != "" && agentutil.IsUUID(tempUUID) {
		return "grok -s " + tempUUID
	}
	return "grok"
}

func (*Plugin) ResumeCommand(nativeID string) string {
	if nativeID != "" && agentutil.IsSafeGrokSessionID(nativeID) {
		return "grok -r " + nativeID
	}
	return ""
}

func (p *Plugin) BuildLaunchSpec(in agent.LaunchInput) (agent.LaunchSpec, error) {
	var baseCmd string
	if in.Resume {
		baseCmd = p.ResumeCommand(in.NativeID)
	}
	if baseCmd == "" {
		id := in.TempUUID
		if id == "" {
			id = in.NativeID
		}
		baseCmd = p.SpawnCommand(id)
	}

	cmd := baseCmd
	var args []string
	if fields := strings.Fields(baseCmd); len(fields) > 1 {
		args = append(args, fields[1:]...)
	}

	if m := strings.TrimSpace(in.Model); m != "" && !strings.EqualFold(m, "default") && agentutil.IsSafeCLIArg(m) {
		cmd += " -m " + m
		args = append(args, "-m", m)
	}
	if e := strings.TrimSpace(in.Effort); e != "" && !strings.EqualFold(e, "default") && agentutil.IsSafeCLIArg(e) {
		cmd += " --effort " + e
		args = append(args, "--effort", e)
	}
	if pm := strings.TrimSpace(in.PermissionMode); pm != "" && !strings.EqualFold(pm, "default") && agentutil.IsSafeCLIArg(pm) {
		cmd += " --permission-mode " + pm
		args = append(args, "--permission-mode", pm)
	}
	if !in.Resume {
		for _, dir := range in.ExtraDirs {
			dir = strings.TrimSpace(dir)
			if dir == "" || strings.ContainsAny(dir, "\x00\r\n") {
				continue
			}
			escaped := strings.ReplaceAll(dir, "'", `'\''`)
			cmd += " --add-dir '" + escaped + "'"
			args = append(args, "--add-dir", dir)
		}
	}

	var env map[string]string
	if len(in.Env) > 0 {
		env = maps.Clone(in.Env)
	}
	if strings.TrimSpace(in.Prompt) != "" {
		if env == nil {
			env = make(map[string]string)
		}
		env["ACKBAR_INIT_PROMPT"] = in.Prompt
		cmd += ` -- "$ACKBAR_INIT_PROMPT"`
		args = append(args, "--", in.Prompt)
	}

	return agent.LaunchSpec{
		Command:     "grok",
		Args:        args,
		Env:         env,
		CommandLine: cmd,
	}, nil
}

func (p *Plugin) InspectStatus(ctx context.Context, sess *agent.Session) bool {
	if sess == nil || (sess.Agent != "grok" && sess.Agent != "grok-cli") || sess.State == agent.StateEnded {
		return false
	}
	if sess.EngineType == agent.EngineHeadless && sess.TmuxName == "" && (sess.PID <= 0 || !agentutil.IsProcessAlive(sess.PID)) {
		return false
	}

	changed := false
	home, _ := os.UserHomeDir()

	if meta := ReadSessionMetadataForHome(home, sess.Cwd, sess.NativeID); meta != nil {
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
		if sess.Name == "" || agentutil.IsRawSessionName(sess.Name) {
			if sess.CustomTitle != "" {
				sess.Name, changed = sess.CustomTitle, true
			} else if sess.AITitle != "" {
				sess.Name, changed = sess.AITitle, true
			} else if meta.Title != "" {
				sess.Name, changed = meta.Title, true
			}
		}
	}

	if subs, err := p.ListSubagents(home, sess.Cwd, sess.NativeID); err == nil {
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
		sess.State, sess.Activity, sess.PID, sess.Blocked = agent.StateEnded, "Session ended (process exited)", 0, nil
		return true
	}

	if sess.PID > 0 && !agentutil.IsProcessAlive(sess.PID) {
		if outPs, errPs := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(sess.PID)).Output(); errPs != nil || len(strings.TrimSpace(string(outPs))) == 0 {
			sess.State, sess.Activity, sess.PID, sess.Blocked = agent.StateEnded, "Session ended (process exited)", 0, nil
			return true
		}
	}

	tailText := agentutil.ExtractTailLines(string(out), 25)

	if strings.Contains(tailText, "grok login") || strings.Contains(tailText, "Authentication failed") || strings.Contains(tailText, "not logged in") {
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockAuth {
			sess.State, sess.Blocked, sess.Activity = agent.StateBlocked, &agent.Blocked{
				Kind: agent.BlockAuth, AuthKind: "agent", AuthTarget: "agent:grok", AuthServer: "grok",
				Reason: "Grok authentication required", Question: "Sign in with 'grok login'?",
				Options: []string{"Sign in", "Dismiss"}, Since: time.Now(),
			}, "Authentication required"
			return true
		}
		return changed
	}

	if limited, resetAt, reason := p.DetectRateLimit(tailText, time.Now()); limited {
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockRateLimit {
			sess.State, sess.Blocked, sess.Activity = agent.StateBlocked, &agent.Blocked{
				Kind: agent.BlockRateLimit, Reason: reason, Question: reason, ResetAt: &resetAt,
				Options: []string{resetAt.Local().Format("15:04"), "Resume in 1h"}, Since: time.Now(),
			}, "Rate limited: "+reason
			return true
		}
		return changed
	}

	if strings.Contains(tailText, "requires approval") || strings.Contains(tailText, "Approval required") ||
		strings.Contains(tailText, "Authorize execution") || strings.Contains(tailText, "[y/N]") ||
		strings.Contains(tailText, "[Y/n]") || strings.Contains(tailText, "Do you want to proceed") {
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockPermission {
			sess.State, sess.Blocked, sess.Activity = agent.StateBlocked, &agent.Blocked{
				Kind: agent.BlockPermission, Reason: "Tool permission requested", Question: "Permission required",
				Options: []string{"Allow", "Deny"}, Since: time.Now(),
			}, "Waiting for tool authorization"
			return true
		}
		return changed
	}

	if strings.Contains(tailText, "Approve plan") || strings.Contains(tailText, "Plan approval required") || strings.Contains(tailText, "approve implementation plan") {
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockApproval {
			sess.State, sess.Blocked, sess.Activity = agent.StateBlocked, &agent.Blocked{
				Kind: agent.BlockApproval, Reason: "Plan review requested", Question: "Approve implementation plan?",
				Options: []string{"Approve", "Request Changes"}, Since: time.Now(),
			}, "Waiting for plan approval"
			return true
		}
		return changed
	}

	if strings.Contains(tailText, "Waiting for user response") || strings.Contains(tailText, "Waiting for input") || strings.Contains(tailText, "request_user_input") {
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockQuestion {
			sess.State, sess.Blocked, sess.Activity = agent.StateBlocked, &agent.Blocked{
				Kind: agent.BlockQuestion, Reason: "Waiting for user response", Question: "Waiting for user response",
				Since: time.Now(),
			}, "Waiting for user response"
			return true
		}
		return changed
	}

	if strings.Contains(tailText, "Subagent running:") || strings.Contains(tailText, "Thinking") || strings.Contains(tailText, "Esc to interrupt") {
		if sess.State == agent.StateIdle {
			sess.State, sess.Activity, changed = agent.StateWorking, "Thinking...", true
		}
		return changed
	}

	if sess.State == agent.StateBlocked {
		sess.State, sess.Blocked, sess.Activity, changed = agent.StateIdle, nil, "Awaiting user prompt", true
	}
	return changed
}
