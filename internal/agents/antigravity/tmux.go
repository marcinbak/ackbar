package antigravity

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
	"ackbar/internal/tmux"
)

func (*Plugin) SpawnCommand(_ string) string { return "agy" }

func (*Plugin) ResumeCommand(nativeID string) string {
	if nativeID != "" && agentutil.IsUUID(nativeID) {
		return "agy --conversation " + nativeID
	}
	return "agy"
}

func (p *Plugin) BuildLaunchSpec(in agent.LaunchInput) (agent.LaunchSpec, error) {
	baseCmd := p.SpawnCommand(in.TempUUID)
	if in.Resume {
		baseCmd = p.ResumeCommand(in.NativeID)
	}
	env := make(map[string]string, len(in.Env)+1)
	for k, v := range in.Env {
		env[k] = v
	}
	cmdLine := baseCmd
	promptInCmd := false
	if strings.TrimSpace(in.Prompt) != "" {
		env["ACKBAR_INIT_PROMPT"] = in.Prompt
		cmdLine += ` -i "$ACKBAR_INIT_PROMPT"`
		promptInCmd = true
	}
	return agent.LaunchSpec{
		Command:         "agy",
		CommandLine:     cmdLine,
		Env:             env,
		PromptInCommand: promptInCmd,
	}, nil
}

func (*Plugin) ResetCommand() string { return "/reset" }

func (*Plugin) ApprovalKeys(allow bool) []string {
	if allow {
		return []string{"1", "Enter"}
	}
	return []string{"4", "Enter"}
}

func (p *Plugin) InspectStatus(ctx context.Context, sess *agent.Session) bool {
	if sess == nil || sess.Agent != "antigravity" || sess.State == agent.StateEnded {
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

	logCandidates := transcriptCandidateDirs(home, sess.NativeID)
	if updateLastEventFromLogs(sess, logCandidates) {
		changed = true
	}

	runningCount := 0
	var activeRole string
	var runningSubs []agent.SubagentInfo
	if sess.NativeID != "" {
		if subs, err := p.ListSubagents(home, sess.Cwd, sess.NativeID); err == nil {
			for _, sub := range subs {
				if sub.State == "running" {
					runningCount++
					runningSubs = append(runningSubs, sub)
					if activeRole == "" {
						activeRole = sub.Role
					}
				}
			}
			if sess.RunningSubagents != runningCount {
				sess.RunningSubagents = runningCount
				changed = true
			}
		}
	}

	pane := p.inspectTmuxPane(ctx, sess, runningCount > 0 || sess.RunningSubagents > 0, &changed)
	if pane.ended || pane.blockedReturn {
		return changed
	}

	turnCompletedAfterBlock := false
	blockAnswered := false
	var lastStep transcriptStep
	hasLastStep := false
	var activeLogPath string
	blockSince := effectiveBlockedSince(sess)

	for _, dir := range logCandidates {
		logPath := filepath.Join(dir, "transcript.jsonl")
		data, err := agentutil.ReadTail(logPath, 256*1024)
		if err != nil || len(data) == 0 {
			continue
		}
		activeLogPath = logPath
		tLines := strings.Split(string(data), "\n")
		lastValidIdx := lastValidTranscriptIdx(tLines, logPath, &lastStep, &hasLastStep)
		if lastValidIdx != -1 {
			if blocked, activity, ok, answered := inspectTranscriptBlocked(logPath, tLines, lastValidIdx, blockSince); ok {
				if applyBlocked(sess, blocked, activity) {
					changed = true
				}
				return changed
			} else if answered {
				blockAnswered = true
				turnCompletedAfterBlock = true
			}
		}
		break
	}

	if runningCount > 0 && home != "" {
		subBrainDirs := brainDirs(home)
		for _, sub := range runningSubs {
			if !agentutil.IsSafeSessionID(sub.ID) {
				continue
			}
			cleanSubID := filepath.Base(filepath.Clean(sub.ID))
			for _, bDir := range subBrainDirs {
				subLogPath := filepath.Join(bDir, cleanSubID, ".system_generated", "logs", "transcript.jsonl")
				if blocked, activity, ok, answered := inspectTranscriptFileBlocked(subLogPath, blockSince); ok {
					if applyBlocked(sess, blocked, activity) {
						changed = true
					}
					return changed
				} else if answered {
					blockAnswered = true
					turnCompletedAfterBlock = true
				}
			}
		}
		if sess.State == agent.StateBlocked && isWithinBlockedGraceWindow(sess) && !turnCompletedAfterBlock {
			return changed
		}
		if sess.State != agent.StateWorking || sess.Blocked != nil {
			sess.State = agent.StateWorking
			sess.Blocked = nil
			sess.Activity = "Subagent running: " + activeRole
			changed = true
		}
		return changed
	}

	if hasLastStep {
		if ret, stepChanged := applyTranscriptStepState(sess, &lastStep, activeLogPath, pane.hasIdlePrompt, pane.hasSpinner, blockSince, &turnCompletedAfterBlock, blockAnswered); ret {
			return changed || stepChanged
		} else if stepChanged {
			changed = true
		}
	}

	if sess.TmuxName != "" && !pane.hasIdlePrompt && (pane.hasSpinner || pane.hasCancel) {
		if isWithinBlockedGraceWindow(sess) && !strings.Contains(pane.bottomText, "Thinking...") {
			return changed
		}
		if sess.State != agent.StateWorking {
			sess.State = agent.StateWorking
			sess.Blocked = nil
			sess.Activity = "Working..."
			changed = true
		}
		return changed
	}

	if sess.TmuxName != "" && len(pane.nonEmpty) > 0 && (pane.hasIdlePrompt || hasPromptInTail(pane.nonEmpty, 6)) {
		if isWithinBlockedGraceWindow(sess) && !turnCompletedAfterBlock {
			return changed
		}
		if sess.State != agent.StateIdle {
			sess.State = agent.StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		}
		return changed
	}

	if sess.State == agent.StateBlocked {
		if sess.Blocked != nil && sess.Blocked.Kind == agent.BlockRateLimit && sess.Blocked.ResetAt != nil && sess.Blocked.ResetAt.After(time.Now()) {
			return changed
		}
		if sess.TmuxName != "" || agentutil.IsProcessAlive(sess.PID) {
			if isWithinBlockedGraceWindow(sess) && !pane.hasSpinner && !turnCompletedAfterBlock {
				return changed
			}
			sess.State = agent.StateIdle
			sess.Blocked = nil
			sess.Activity = "Awaiting user prompt"
			changed = true
		} else {
			sess.State = agent.StateEnded
			sess.Blocked = nil
			sess.Activity = "Session ended"
			changed = true
		}
	}
	return changed
}
