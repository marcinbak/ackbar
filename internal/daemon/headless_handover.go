package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
)

func (s *Server) buildDeterministicHandoverBriefing(sess *Session, customInstruction string) string {
	home := s.effectiveHomeDir()
	var recentPrompts []string

	if sess.Agent == "antigravity" {
		if msgs, err := ExtractAntigravityTranscriptMessages(home, sess.NativeID); err == nil {
			for _, m := range msgs {
				if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
					recentPrompts = append(recentPrompts, strings.TrimSpace(m.Content))
				}
			}
		}
	} else {
		turns, _, _ := s.extractClaudeRewindTurns(home, sess)
		for _, t := range turns {
			if strings.TrimSpace(t.RawPrompt) != "" {
				recentPrompts = append(recentPrompts, strings.TrimSpace(t.RawPrompt))
			}
		}
	}

	goal := sess.FirstPrompt
	if goal == "" && len(recentPrompts) > 0 {
		goal = recentPrompts[0]
	}
	if goal == "" {
		goal = sess.Activity
	}
	if goal == "" {
		goal = "General codebase maintenance"
	}

	gitStatus := "Clean / No git repo"
	if sess.Cwd != "" && dirExists(sess.Cwd) {
		cmd := exec.Command("git", "-C", sess.Cwd, "status", "-sb", "--no-color")
		cmd.Env = append(StripBilledCredentials(cmd.Environ()), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "TERM=dumb")
		if out, err := cmd.Output(); err == nil && len(out) > 0 {
			gitStatus = strings.TrimSpace(string(out))
		}
	}

	var sb strings.Builder
	sb.WriteString("# Handover Briefing\n\n")
	sb.WriteString(fmt.Sprintf("1. **Core Objective:** %s\n\n", strings.TrimSpace(goal)))

	sb.WriteString("2. **Completed Milestones & Recent Activity:**\n")
	if len(recentPrompts) > 1 {
		start := 0
		if len(recentPrompts) > 4 {
			start = len(recentPrompts) - 4
		}
		for i := start; i < len(recentPrompts); i++ {
			p := recentPrompts[i]
			if len(p) > 160 {
				p = p[:160] + "..."
			}
			sb.WriteString(fmt.Sprintf("   - Turn %d: %s\n", i+1, p))
		}
	} else if sess.Activity != "" {
		sb.WriteString(fmt.Sprintf("   - %s\n", sess.Activity))
	} else {
		sb.WriteString("   - Initial session discovery completed\n")
	}
	sb.WriteString("\n")

	sb.WriteString("3. **Current State:**\n")
	sb.WriteString(fmt.Sprintf("   - Active Branch & Changes:\n```\n%s\n```\n\n", gitStatus))

	sb.WriteString("4. **Immediate Next Steps:**\n")
	if customInstruction != "" {
		sb.WriteString(fmt.Sprintf("   - %s\n", strings.TrimSpace(customInstruction)))
	} else if sess.LastPrompt != "" && sess.LastPrompt != goal {
		sb.WriteString(fmt.Sprintf("   - Resume pending turn: %s\n", sess.LastPrompt))
	} else {
		sb.WriteString("   - Review current git status and resume planned milestones.\n")
	}

	return sb.String()
}

func (s *Server) executeHeadlessHandover(w http.ResponseWriter, r *http.Request, sess *Session, strategy, customInstruction string) {
	briefing := s.buildDeterministicHandoverBriefing(sess, customInstruction)
	reseedPrompt := fmt.Sprintf("%s\n\nPlease ingest this briefing and state readiness to continue.", briefing)

	isNewSession := strategy == "new_session"
	newSess, err := s.rotateHeadlessConversation(r.Context(), sess, "Handover completed (in-place rotation)", "", !isNewSession)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to rotate conversation for handover: %v", err), http.StatusInternalServerError)
		return
	}

	if isNewSession && s.headless != nil {
		s.headless.CleanupSessionRuntime(sess.ID, sess.NativeID)
	}

	if s.headless != nil {
		newSessCopy := *newSess
		go func() {
			_ = s.headless.RunTurn(context.Background(), &newSessCopy, reseedPrompt)
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"strategy":       strategy,
		"old_session_id": sess.ID,
		"new_session_id": newSess.ID,
		"session":        newSess,
		"briefing":       briefing,
	})
}

func (s *Server) executeHeadlessCompaction(w http.ResponseWriter, r *http.Request, sess *Session, instructions string) {
	briefing := s.buildDeterministicHandoverBriefing(sess, instructions)
	compactionPrompt := fmt.Sprintf("Prior conversation compacted into summary briefing:\n\n%s\n\nPlease acknowledge and summarize current readiness.", briefing)

	newSess, err := s.rotateHeadlessConversation(r.Context(), sess, "Compacted conversation", "", true)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to compact conversation: %v", err), http.StatusInternalServerError)
		return
	}

	if s.headless != nil {
		newSessCopy := *newSess
		go func() {
			_ = s.headless.RunTurn(context.Background(), &newSessCopy, compactionPrompt)
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"mode":           "rotated_session",
		"old_session_id": sess.ID,
		"session":        newSess,
	})
}
