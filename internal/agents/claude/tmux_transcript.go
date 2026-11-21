package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (p *Plugin) inspectTranscriptTailBlock(home string, sess *agent.Session, hasPrompt, hasSpinner bool) (endTurn, afterBlock, blockedHandled, blockedChanged bool) {
	if home == "" || !agentutil.IsUUID(sess.NativeID) {
		return false, false, false, false
	}
	transcriptPath := FindTranscriptPath(home, sess.Cwd, sess.NativeID)
	if transcriptPath == "" {
		return false, false, false, false
	}
	data, size, _, err := readRegularFileSlice(transcriptPath, -1, 65536)
	if err != nil || len(data) == 0 {
		return false, false, false, false
	}
	tLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	minIdx := 0
	if size > 65536 && len(tLines) > 1 {
		minIdx = 1
	}
	for i := len(tLines) - 1; i >= minIdx; i-- {
		line := strings.TrimSpace(tLines[i])
		if line == "" {
			continue
		}
		var entry struct {
			Type       string `json:"type"`
			Timestamp  string `json:"timestamp"`
			StopReason string `json:"stop_reason"`
			Message    struct {
				StopReason string          `json:"stop_reason"`
				Content    json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || (entry.Type != "user" && entry.Type != "assistant") {
			continue
		}
		entryTime, _ := time.Parse(time.RFC3339, entry.Timestamp)
		if entry.Type == "assistant" {
			sr := firstNonEmpty(entry.Message.StopReason, entry.StopReason)
			if sr == "tool_use" && !hasPrompt && !hasSpinner {
				if handled, bChanged := applyTranscriptToolBlock(sess, entry.Message.Content, entryTime); handled {
					return false, false, true, bChanged
				}
			} else if sr == "end_turn" || sr == "stop_sequence" {
				blockSince := effectiveBlockedSince(sess)
				after := !entryTime.IsZero() && (blockSince.IsZero() || !entryTime.Before(blockSince))
				return true, after, false, false
			}
		} else {
			blockSince := effectiveBlockedSince(sess)
			if !entryTime.IsZero() && !blockSince.IsZero() && entryTime.After(blockSince) {
				return false, true, false, false
			}
		}
		break
	}
	return false, false, false, false
}

func applyTranscriptToolBlock(sess *agent.Session, content json.RawMessage, entryTime time.Time) (handled bool, changed bool) {
	var blocks []struct {
		Type  string                 `json:"type"`
		Name  string                 `json:"name"`
		Input map[string]interface{} `json:"input"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return false, false
	}
	for _, b := range blocks {
		if b.Type != "tool_use" || (b.Name != "AskUserQuestion" && b.Name != "ask_question" && b.Name != "ExitPlanMode") {
			continue
		}
		var q string
		var opts []string
		if b.Name == "ExitPlanMode" {
			q, opts = "Approve plan?", []string{"Yes", "No"}
		} else {
			q, opts = agentutil.ExtractQuestionAndOptions(b.Input)
			if q == "" {
				q = "Waiting for user response"
			}
		}
		since := entryTime
		if since.IsZero() {
			since = time.Now()
		}
		if sess.State != agent.StateBlocked || sess.Blocked == nil || sess.Blocked.Question != q {
			sess.State = agent.StateBlocked
			sess.Blocked = &agent.Blocked{
				Kind: agent.BlockQuestion, Reason: q, Question: q, Options: opts, Since: since,
			}
			sess.Activity = "Question: " + agentutil.TruncateTitle(q, 50)
			changed = true
		}
		return true, changed
	}
	return false, false
}

func isTmuxProcessAlive(ctx context.Context, pid int) bool {
	if pid <= 0 || agentutil.IsProcessAlive(pid) {
		return true
	}
	out, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid)).Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

func hasActiveBackgroundTaskOutput(cleanUUID string) bool {
	matches, err := filepath.Glob(fmt.Sprintf("/tmp/claude-*/*/%s/tasks/*.output", cleanUUID))
	if err != nil {
		return false
	}
	for _, taskPath := range matches {
		info, err := os.Stat(taskPath)
		if err != nil || time.Since(info.ModTime()) >= 30*time.Second {
			continue
		}
		if data, _, _, rerr := readRegularFileSlice(taskPath, -1, 256); rerr == nil && !strings.Contains(string(data), "[exited with code") {
			return true
		}
	}
	return false
}

func effectiveBlockedSince(sess *agent.Session) time.Time {
	if sess == nil {
		return time.Time{}
	}
	if sess.Blocked != nil && !sess.Blocked.Since.IsZero() {
		return sess.Blocked.Since
	}
	if !sess.UpdatedAt.IsZero() {
		return sess.UpdatedAt
	}
	return sess.LastStateChangeAt
}

func isWithinBlockedGraceWindow(sess *agent.Session) bool {
	if sess == nil || sess.State != agent.StateBlocked || sess.Blocked == nil {
		return false
	}
	return (!sess.UpdatedAt.IsZero() && time.Since(sess.UpdatedAt) < 15*time.Second) ||
		(!sess.Blocked.Since.IsZero() && time.Since(sess.Blocked.Since) < 15*time.Second) ||
		(!sess.LastStateChangeAt.IsZero() && time.Since(sess.LastStateChangeAt) < 15*time.Second)
}
