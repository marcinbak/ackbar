package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type inspectCappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *inspectCappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len() >= c.limit {
		c.truncated = true
		return len(p), nil
	}
	remaining := c.limit - c.buf.Len()
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *inspectCappedBuffer) String() string {
	return c.buf.String()
}

func (s *Server) runAllowlistedInspectCmd(ctx context.Context, sess *Session, name string, args ...string) (string, error) {
	subCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(subCtx, name, args...)
	if sess != nil && sess.Cwd != "" {
		cmd.Dir = sess.Cwd
	}
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second

	if s.headless != nil {
		cmd.Env = s.headless.buildCommandEnv(sess)
	} else {
		cmd.Env = append(StripBilledCredentials(os.Environ()), "GIT_TERMINAL_PROMPT=0", "TERM=dumb")
	}

	capBuf := &inspectCappedBuffer{limit: 64 << 10}
	cmd.Stdout = capBuf
	cmd.Stderr = capBuf

	err := cmd.Run()
	return capBuf.String(), err
}

func scanAgentMarkdownFiles(dirs ...string) []map[string]string {
	var agents []map[string]string
	seen := make(map[string]bool)
	for _, dir := range dirs {
		if dir == "" || !dirExists(dir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			if seen[name] {
				continue
			}
			seen[name] = true
			agents = append(agents, map[string]string{
				"name":        name,
				"source":      filepath.Join(dir, e.Name()),
				"description": fmt.Sprintf("Local role definition from %s", filepath.Base(dir)),
			})
		}
	}
	return agents
}

func (s *Server) handleSessionInspect(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "", nil, func(_ *Session) string {
		return r.URL.RequestURI()
	})
	if proxied || sess == nil {
		return
	}

	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	home := s.effectiveHomeDir()

	switch kind {
	case "plugins":
		var out string
		if sess.Agent == "claude-code" {
			bin, err := resolveClaudeBinary()
			if err == nil {
				out, _ = s.runAllowlistedInspectCmd(r.Context(), sess, bin, "plugin", "list", "--json")
			}
		} else if ci, ok := s.PluginRegistry().AsCLIInspector(sess.Agent); ok {
			if args, ok := ci.InspectSubcommandArgs("plugins"); ok {
				if hd, ok := s.PluginRegistry().AsHeadlessDriver(sess.Agent); ok {
					if bin, err := hd.ResolveBinary(); err == nil && bin != "" {
						out, _ = s.runAllowlistedInspectCmd(r.Context(), sess, bin, args...)
					}
				}
			}
		} else if sess.Agent == "antigravity" {
			bin, err := resolveAntigravityBinary()
			if err == nil {
				out, _ = s.runAllowlistedInspectCmd(r.Context(), sess, bin, "plugin", "list", "--json")
			}
		}
		if home != "" && out != "" {
			out = strings.ReplaceAll(out, home, "~")
		}
		var parsed any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
			parsed = []any{}
		}
		workspacePlugins := probeWorkspacePluginsWithRegistry(home, sess, s.db, s.PluginRegistry())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":              "plugins",
			"agent":             sess.Agent,
			"installed_plugins": parsed,
			"workspace_plugins": workspacePlugins,
		})

	case "agents":
		var cliAgents any = []any{}
		if sess.Agent == "claude-code" {
			if bin, err := resolveClaudeBinary(); err == nil {
				if out, err := s.runAllowlistedInspectCmd(r.Context(), sess, bin, "agents", "--json"); err == nil {
					_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &cliAgents)
				}
			}
		} else if ci, ok := s.PluginRegistry().AsCLIInspector(sess.Agent); ok {
			if args, ok := ci.InspectSubcommandArgs("agents"); ok {
				if hd, ok := s.PluginRegistry().AsHeadlessDriver(sess.Agent); ok {
					if bin, err := hd.ResolveBinary(); err == nil && bin != "" {
						if out, err := s.runAllowlistedInspectCmd(r.Context(), sess, bin, args...); err == nil {
							trimmed := strings.TrimSpace(out)
							if uErr := json.Unmarshal([]byte(trimmed), &cliAgents); uErr != nil && strings.EqualFold(sess.Agent, "antigravity") {
								cliAgents = trimmed
							}
						}
					}
				}
			}
		} else if sess.Agent == "antigravity" {
			if bin, err := resolveAntigravityBinary(); err == nil {
				if out, err := s.runAllowlistedInspectCmd(r.Context(), sess, bin, "agents"); err == nil {
					cliAgents = strings.TrimSpace(out)
				}
			}
		}
		localAgents := scanAgentMarkdownFiles(
			filepath.Join(sess.Cwd, ".claude", "agents"),
			filepath.Join(home, ".claude", "agents"),
			filepath.Join(sess.Cwd, ".agents", "agents"),
			filepath.Join(home, ".gemini", "config", "agents"),
			filepath.Join(home, ".gemini", "antigravity-cli", "agents"),
			filepath.Join(home, ".grok", "agents"),
			filepath.Join(sess.Cwd, ".grok", "agents"),
		)
		builtins := []map[string]string{
			{"name": "Explore", "description": "Fast exploratory code indexing and architectural discovery"},
			{"name": "Plan", "description": "Structured architecture planning and specification breakdown"},
			{"name": "general-purpose", "description": "Standard software engineering and command execution"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":         "agents",
			"agent":        sess.Agent,
			"builtins":     builtins,
			"local_roles":  localAgents,
			"active_state": cliAgents,
		})

	case "doctor":
		var report string
		var rawVersion string
		isWorkerRunning := s.headless != nil && s.headless.IsRunning(sess.ID)
		handledDoctor := false
		if sess.Agent == "claude-code" {
			if bin, err := resolveClaudeBinary(); err == nil {
				report, _ = s.runAllowlistedInspectCmd(r.Context(), sess, bin, "doctor")
				rawVersion, _ = s.runAllowlistedInspectCmd(r.Context(), sess, bin, "--version")
			}
			handledDoctor = true
		} else if ci, ok := s.PluginRegistry().AsCLIInspector(sess.Agent); ok {
			if args, ok := ci.InspectSubcommandArgs("doctor"); ok {
				if hd, ok := s.PluginRegistry().AsHeadlessDriver(sess.Agent); ok {
					if bin, err := hd.ResolveBinary(); err == nil && bin != "" {
						report, _ = s.runAllowlistedInspectCmd(r.Context(), sess, bin, args...)
						rawVersion, _ = s.runAllowlistedInspectCmd(r.Context(), sess, bin, "--version")
					}
				}
				handledDoctor = true
			}
		}
		if !handledDoctor && strings.EqualFold(sess.Agent, "antigravity") {
			var bin string
			if hd, ok := s.PluginRegistry().AsHeadlessDriver(sess.Agent); ok {
				bin, _ = hd.ResolveBinary()
			} else if sess.Agent == "antigravity" {
				bin, _ = resolveAntigravityBinary()
			}
			if bin != "" {
				rawVersion, _ = s.runAllowlistedInspectCmd(r.Context(), sess, bin, "--version")
			}
			servers, _ := discoverConfiguredMCPServers(sess, s.db, home)
			plugins := probeWorkspacePluginsWithRegistry(home, sess, s.db, s.PluginRegistry())
			report = fmt.Sprintf("Antigravity Diagnostic Check\nVersion: %s\nConfigured MCP Servers: %d\nWorkspace Plugins: %d\nWorking Directory: %s",
				strings.TrimSpace(rawVersion), len(servers), len(plugins), sess.Cwd)
		}
		if home != "" && report != "" {
			report = strings.ReplaceAll(report, home, "~")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":               "doctor",
			"agent":              sess.Agent,
			"version":            strings.TrimSpace(rawVersion),
			"report":             strings.TrimSpace(report),
			"cwd":                sess.Cwd,
			"extra_dirs":         sess.ExtraDirs,
			"engine_type":        sess.EngineType,
			"warm_worker_active": isWorkerRunning,
		})

	case "context", "cost":
		data := s.computeTranscriptContextAndCost(home, sess)
		data["kind"] = kind
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)

	default:
		http.Error(w, fmt.Sprintf("Invalid inspect kind '%s': must be plugins, agents, doctor, context, or cost", kind), http.StatusBadRequest)
	}
}

func (s *Server) handleSessionDiff(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "", nil, func(_ *Session) string {
		return r.URL.RequestURI()
	})
	if proxied || sess == nil {
		return
	}

	if sess.Cwd == "" || !dirExists(sess.Cwd) {
		http.Error(w, "Invalid session working directory", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	runGit := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", sess.Cwd}, args...)...)
		cmd.Stdin = nil
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process != nil && cmd.Process.Pid > 0 {
				return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			return nil
		}
		cmd.WaitDelay = 2 * time.Second
		cmd.Env = append(StripBilledCredentials(os.Environ()), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "TERM=dumb")
		capBuf := &inspectCappedBuffer{limit: 64 << 10}
		cmd.Stdout = capBuf
		cmd.Stderr = nil
		err := cmd.Run()
		return capBuf.String(), err
	}

	isRepoOut, err := runGit("rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(isRepoOut) != "true" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"is_git_repo": false,
			"cwd":         sess.Cwd,
		})
		return
	}

	statusOut, _ := runGit("status", "--porcelain=v1", "-b")
	var branch string
	var statusLines []string
	for _, l := range strings.Split(statusOut, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "## ") {
			branch = strings.TrimPrefix(l, "## ")
		} else if strings.TrimSpace(l) != "" {
			statusLines = append(statusLines, l)
		}
	}

	statOut, err := runGit("diff", "HEAD", "--stat", "--no-color")
	if err != nil || strings.TrimSpace(statOut) == "" {
		statOut, _ = runGit("diff", "--stat", "--no-color")
	}

	diffOut, err := runGit("diff", "HEAD", "--no-ext-diff", "--no-color")
	if err != nil || strings.TrimSpace(diffOut) == "" {
		diffOut, _ = runGit("diff", "--no-ext-diff", "--no-color")
	}

	truncated := len(diffOut) >= 64<<10
	isClean := len(statusLines) == 0 && strings.TrimSpace(diffOut) == ""

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"is_git_repo":  true,
		"cwd":          sess.Cwd,
		"branch":       branch,
		"status_lines": statusLines,
		"stat":         strings.TrimSpace(statOut),
		"diff":         diffOut,
		"truncated":    truncated,
		"clean":        isClean,
	})
}
