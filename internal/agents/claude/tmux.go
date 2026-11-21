package claude

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/tmux"
)

func (*Plugin) SpawnCommand(tempUUID string) string {
	if tempUUID != "" && agentutil.IsUUID(tempUUID) {
		return "claude --session-id " + tempUUID
	}
	return "claude"
}

func (*Plugin) ResumeCommand(nativeID string) string {
	if nativeID != "" && agentutil.IsUUID(nativeID) {
		return "claude --resume " + nativeID
	}
	return ""
}

func (p *Plugin) BuildLaunchSpec(in agent.LaunchInput) (agent.LaunchSpec, error) {
	baseCmd := p.SpawnCommand(in.TempUUID)
	if in.Resume {
		if r := p.ResumeCommand(in.NativeID); r != "" {
			baseCmd = r
		} else {
			baseCmd = "claude"
		}
	}
	env := make(map[string]string, len(in.Env)+1)
	for k, v := range in.Env {
		env[k] = v
	}
	cmdLine := baseCmd
	promptInCmd := false
	if strings.TrimSpace(in.Prompt) != "" {
		env["ACKBAR_INIT_PROMPT"] = in.Prompt
		cmdLine += ` "$ACKBAR_INIT_PROMPT"`
		promptInCmd = true
	}
	return agent.LaunchSpec{
		Command:         "claude",
		CommandLine:     cmdLine,
		Env:             env,
		PromptInCommand: promptInCmd,
	}, nil
}

func (*Plugin) ResetCommand() string { return "/clear" }

func (*Plugin) ApprovalKeys(allow bool) []string {
	if allow {
		return []string{"y", "Enter"}
	}
	return []string{"n", "Enter"}
}

func (p *Plugin) InspectStatus(ctx context.Context, sess *agent.Session) bool {
	if sess == nil || sess.Agent != "claude-code" || sess.State == agent.StateEnded {
		return false
	}
	if sess.EngineType == agent.EngineHeadless && sess.TmuxName == "" && (sess.PID <= 0 || !agentutil.IsProcessAlive(sess.PID)) {
		return false
	}
	changed := false
	home := agentutil.RealUserHome()

	if sess.PID <= 0 && sess.TmuxName != "" {
		if pid, err := tmux.GetPID(ctx, sess.TmuxName); err == nil && pid > 0 {
			sess.PID = pid
			changed = true
		}
	}

	var pane paneScanResult
	if sess.TmuxName != "" {
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-pt", sess.TmuxName, "-p").Output()
		if err != nil || !isTmuxProcessAlive(ctx, sess.PID) {
			sess.State = agent.StateEnded
			sess.Activity = "Session ended (process exited)"
			sess.PID = 0
			sess.Blocked = nil
			return true
		}
		pane = scanPaneText(string(out))
		if handled, bChanged := applyPaneImmediateBlock(p, sess, pane); handled {
			return changed || bChanged
		}
		if sess.State == agent.StateBlocked && sess.Blocked != nil && sess.Blocked.Kind == agent.BlockRateLimit &&
			(sess.Blocked.ResetAt == nil || sess.Blocked.ResetAt.After(time.Now())) {
			return changed
		}
		if !pane.hasSpinner {
			if b, act, ok := p.rateLimitBlocked(pane.bottom6Text); ok {
				if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != agent.BlockRateLimit || sess.Blocked.Reason != b.Reason {
					sess.State, sess.Blocked, sess.Activity, changed = agent.StateBlocked, b, act, true
				}
				return changed
			}
		}
	}

	transcriptEndTurn, transcriptAfterBlock, blockedHandled, blockedChanged := p.inspectTranscriptTailBlock(home, sess, pane.hasPrompt, pane.hasSpinner)
	if blockedHandled {
		return changed || blockedChanged
	}

	if sess.NativeID != "" {
		if subs, err := p.ListSubagents(home, sess.Cwd, sess.NativeID); err == nil {
			runningCount := 0
			var activeSubName string
			for _, sub := range subs {
				if sub.State == "running" {
					runningCount++
					if activeSubName == "" {
						activeSubName = sub.Name
					}
				}
			}
			if sess.RunningSubagents != runningCount {
				sess.RunningSubagents = runningCount
				changed = true
			}
			if runningCount > 0 {
				if sess.State != agent.StateWorking {
					sess.State = agent.StateWorking
					sess.Blocked = nil
					sess.Activity = "Subagent running: " + activeSubName
					changed = true
				}
				return changed
			}
		}
	}

	if agentutil.IsUUID(sess.NativeID) && !pane.hasPrompt && hasActiveBackgroundTaskOutput(sess.NativeID) {
		if sess.State != agent.StateWorking {
			sess.State = agent.StateWorking
			sess.Blocked = nil
			sess.Activity = "Executing background task..."
			changed = true
		}
		return changed
	}

	if sess.TmuxName != "" {
		if pane.hasSpinner {
			if sess.State != agent.StateWorking {
				sess.State, sess.Blocked, sess.Activity, changed = agent.StateWorking, nil, "Working...", true
			}
			return changed
		}
		if pane.hasPrompt {
			if isWithinBlockedGraceWindow(sess) && !transcriptAfterBlock {
				return changed
			}
			if sess.State != agent.StateIdle {
				sess.State, sess.Blocked, sess.Activity, changed = agent.StateIdle, nil, "Awaiting user prompt", true
			}
			return changed
		}
		if strings.Contains(pane.tailText, "No conversation found with session ID") {
			if sess.State != agent.StateEnded {
				sess.State, sess.Activity, sess.Blocked, changed = agent.StateEnded, "Agent process exited: No conversation found", nil, true
			}
			return changed
		}
	}

	if transcriptEndTurn && !pane.hasSpinner {
		if isWithinBlockedGraceWindow(sess) && !transcriptAfterBlock {
			return changed
		}
		if sess.State != agent.StateIdle {
			sess.State, sess.Blocked, sess.Activity, changed = agent.StateIdle, nil, "Awaiting user prompt", true
		}
		return changed
	}

	if sess.TmuxName != "" && sess.State == agent.StateBlocked {
		if sess.Blocked != nil && sess.Blocked.Kind == agent.BlockRateLimit && sess.Blocked.ResetAt != nil && sess.Blocked.ResetAt.After(time.Now()) {
			return changed
		}
		if isWithinBlockedGraceWindow(sess) && !pane.hasSpinner && !transcriptAfterBlock {
			return changed
		}
		sess.State, sess.Blocked, sess.Activity, changed = agent.StateIdle, nil, "Awaiting user prompt", true
	}
	return changed
}
