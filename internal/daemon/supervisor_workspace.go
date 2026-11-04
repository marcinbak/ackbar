package daemon

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DefaultSupervisorDir returns the designated workspace directory for Ackbar Supervisor sessions.
func DefaultSupervisorDir(home string) string {
	if home == "" {
		return filepath.Join("~", ".ackbar", "supervisor")
	}
	return filepath.Join(home, ".ackbar", "supervisor")
}

// BuildSupervisorSystemPrompt returns the high-signal instructions for Supervisor Dispatcher agents.
func BuildSupervisorSystemPrompt(sessionID string) string {
	sessStr := sessionID
	if sessStr == "" {
		sessStr = "current"
	}
	return fmt.Sprintf(`You are an ⚓ Ackbar Supervisor Dispatcher (session_id: %s).
Your primary role is to coordinate, delegate, and supervise agentic tasks across project repositories.

CRITICAL DISPATCHER RULES:
1. HARD RULE 1 — NEVER WRITE CODE HERE: Never write product code, edit application repositories, or create git worktrees directly inside this Supervisor session workspace (~/.ackbar/supervisor). All implementation and refactoring MUST occur in worker sessions.
2. HARD RULE 2 — TURN 1 DISPATCH & BINDING: On Turn 1 (or whenever the user mentions a project, Jira ticket such as NGL-XXX, or new feature), immediately:
   a. Call mcp__ackbar__bind_supervisor_session to place this supervisor session under the target project node (e.g. Modemobile/NGL) with an appropriate title.
   b. Call mcp__ackbar__create_task with auto_start: true, passing title, repos, required_capabilities (e.g. ["ios"], ["android"], ["mcp:gdrive"]), target_host ("auto" for 75/25 weighted load balancing across 24/7 hosts), and prompt (containing the full ticket requirements/user instructions). This spawns a dedicated worker session in the actual repository directory.
3. HARD RULE 3 — SUPERVISE VIA CONTROL TOOLS: Use list_hosts, list_sessions, get_session_output, send_session_input, list_tasks, and control_session to inspect host capabilities/load, monitor, steer, and verify worker sessions. Inform the human user of worker progress and deliverables.`, sessStr)
}

// ensureSupervisorWorkspace creates the supervisor workspace directory and populates CLAUDE.md and AGENTS.md
func (s *Server) ensureSupervisorWorkspace(dir string) {
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0755)

	var sb strings.Builder
	sb.WriteString("# ⚓ Ackbar Supervisor Dispatcher Workspace\n\n")
	sb.WriteString("Welcome! This workspace is dedicated exclusively to **Ackbar Supervisor Dispatcher** sessions.\n\n")
	sb.WriteString("## Core Rules of Engagement\n\n")
	sb.WriteString("1. **Hard Rule 1 — Zero Product Code:** Never write product code, edit repository files, or create git worktrees directly in this supervisor workspace. All execution work belongs in dedicated worker sessions.\n")
	sb.WriteString("2. **Hard Rule 2 — Turn 1 Dispatch & Binding:** On Turn 1 (or whenever the user mentions a project, Jira ticket such as `NGL-XXX`, or initiative), immediately:\n")
	sb.WriteString("   - Call `bind_supervisor_session` to place this supervisor session under the target project node (e.g. `Modemobile/NGL`) with a descriptive title.\n")
	sb.WriteString("   - Call `create_task` with `auto_start: true`, specifying `repos`, `agent`, `required_capabilities` (e.g. `[\"ios\"]`, `[\"android\"]`, `[\"mcp:gdrive\"]`), `target_host` (`\"auto\"` for 75/25 weighted load balancing across `24/7` hosts), and `prompt` (with the complete ticket requirements) to launch an execution worker in the actual repository.\n")
	sb.WriteString("3. **Hard Rule 3 — Supervise via MCP Tools:** Use `list_hosts`, `list_sessions`, `get_session_output`, `send_session_input`, `list_tasks`, and `control_session` to inspect fleet capabilities/load, monitor, steer, and verify worker progress. Report milestones and blockers to the user.\n\n")

	sb.WriteString("## Registered Ackbar Project Nodes\n\n")
	if s != nil && s.db != nil {
		nodes, err := s.db.ListNodes()
		if err == nil && len(nodes) > 0 {
			sb.WriteString("| Node Path | Group | Project | Host | Project Directory | Git Remote |\n")
			sb.WriteString("| --- | --- | --- | --- | --- | --- |\n")
			for _, n := range nodes {
				parts := strings.Split(n.Path, "/")
				group := parts[0]
				proj := parts[0]
				if len(parts) >= 2 {
					proj = strings.Join(parts[1:], "/")
				}

				hasHostPaths := false
				for _, hp := range n.HostPaths {
					hasHostPaths = true
					hName := hp.Host
					if hName == "" {
						hName = "local"
					}
					gitRem := hp.GitRemote
					if gitRem == "" {
						gitRem = n.GitURL
					}
					sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | `%s` | %s |\n",
						n.Path, group, proj, hName, hp.ProjectDir, redactGitURL(gitRem)))
				}

				if !hasHostPaths {
					hName := "local"
					pDir := n.ProjectDir
					if pDir == "" {
						pDir = "(not set)"
					}
					sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | `%s` | %s |\n",
						n.Path, group, proj, hName, pDir, redactGitURL(n.GitURL)))
				}
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString("*No registered project nodes found in local daemon database.*\n\n")
		}
	} else {
		sb.WriteString("*Daemon database not initialized.*\n\n")
	}

	content := []byte(sb.String())
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), content, 0600)
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), content, 0600)
}

func redactGitURL(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		return u.Redacted()
	}
	return raw
}
