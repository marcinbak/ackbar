package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"ackbar/internal/daemon"
	"ackbar/internal/version"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styling definitions using lipgloss
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E90FF")).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4682B4")).
			Padding(1, 2).
			MarginBottom(1)

	sessionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF"))

	hostStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Italic(true)

	cwdStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#DAA520"))

	workingStatusStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#00FFFF"))

	blockedStatusStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FF3333"))

	idleStatusStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00FF00"))

	endedStatusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	activityStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CCCCCC")).
			PaddingLeft(2)

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3A3A3A")).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("#1E90FF")).
			PaddingLeft(1)

	unselectedStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	groupStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFA500"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#777777")).
			MarginTop(1)
)

type sessionUpdateMsg *daemon.Session
type fetchSessionsResult struct {
	sessions []*daemon.Session
	statuses map[string]HostStatus
}
type loadDocsMsg []string
type discoveryMsg map[string][]daemon.AgentDiscoveryResult
type nodesMsg []*daemon.TreeNode
type errorMsg string

type TreeRow struct {
	IsGroup   bool
	GroupPath string
	GroupName string
	Session   *daemon.Session
	Indent    int
}

type Model struct {
	hosts        []HostConfig
	groups       map[string][]string // logical path -> list of projectKeys
	sessions     []*daemon.Session
	selectedIdx  int
	loading      bool
	errMsg       string
	eventChan    chan *daemon.Session
	ctx          context.Context
	cancelCtx    context.CancelFunc
	termWidth    int
	termHeight             int
	archivedView           bool
	collapsed              map[string]bool // groupPath -> collapsed state
	confirmRestartSession  *daemon.Session
	viewingDocuments       *daemon.Session
	docPaths               []string
	docSelectedIdx         int
	showDiscovery          bool
	discoveryResults       map[string][]daemon.AgentDiscoveryResult
	creatingProject        bool
	inputStep              int
	newPathInput           string
	newNameInput           string
	newGitInput            string
	registeringHost        bool
	hostInput              string
	movingNode             bool
	moveOldPath            string
	moveNewPathInput       string
	movingSessionID        string
	movingSessionHost      string
	showingResumeCmd       *daemon.Session
	projectsDir            string
	firstRun               bool
	firstRunInput          string
	configPath             string
	hostStatuses           map[string]HostStatus
	treeNodes              []*daemon.TreeNode
	discoveryHostIdx       int
	visibleRows            []TreeRow
}

func NewModel(hosts []HostConfig, projectsDir string, groups map[string][]string, firstRun bool, configPath string) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{
		hosts:         hosts,
		projectsDir:   projectsDir,
		groups:        groups,
		firstRun:      firstRun,
		firstRunInput: projectsDir,
		configPath:    configPath,
		loading:       true,
		eventChan:     make(chan *daemon.Session, 100),
		ctx:           ctx,
		cancelCtx:     cancel,
		collapsed:     make(map[string]bool),
	}
}

func (m *Model) Init() tea.Cmd {
	SubscribeEvents(m.ctx, m.hosts, m.eventChan)

	fetchCmd := func() tea.Msg {
		sessions, statuses, err := FetchSessions(m.hosts)
		if err != nil {
			return errorMsg(err.Error())
		}
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	}

	waitForEvents := func() tea.Msg {
		return sessionUpdateMsg(<-m.eventChan)
	}

	return tea.Batch(m.clearErrorCmd(), fetchCmd, m.fetchDiscoveryCmd(), m.fetchNodesCmd(), waitForEvents)
}

func (m *Model) clearErrorCmd() tea.Cmd {
	return tea.Tick(15*time.Second, func(t time.Time) tea.Msg {
		return errorMsg("")
	})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	visibleRows := m.buildVisibleRows()

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height

	case nodesMsg:
		m.treeNodes = msg
		return m, nil

	case fetchSessionsResult:
		m.loading = false
		m.sessions = msg.sessions
		m.hostStatuses = msg.statuses
		m.selectedIdx = 0
		return m, nil

	case sessionUpdateMsg:
		found := false
		for i, s := range m.sessions {
			if s.ID == msg.ID {
				m.sessions[i] = msg
				found = true
				break
			}
		}
		if !found {
			m.sessions = append(m.sessions, msg)
		}

		waitForEvents := func() tea.Msg {
			return sessionUpdateMsg(<-m.eventChan)
		}
		return m, waitForEvents

	case loadDocsMsg:
		m.loading = false
		m.docPaths = msg
		m.docSelectedIdx = 0

	case discoveryMsg:
		m.loading = false
		m.discoveryResults = msg

	case errorMsg:
		m.loading = false
		if string(msg) != "" {
			m.errMsg = string(msg)
			return m, m.clearErrorCmd()
		}
		m.errMsg = ""

	case tea.KeyMsg:
		if m.errMsg != "" {
			m.errMsg = ""
			return m, nil
		}
		if m.showDiscovery {
			switch msg.String() {
			case "esc", "q", "H":
				m.showDiscovery = false
				return m, nil
			case "up", "k":
				if m.discoveryHostIdx > 0 {
					m.discoveryHostIdx--
				}
				return m, nil
			case "down", "j":
				if m.discoveryHostIdx < len(m.hosts)-1 {
					m.discoveryHostIdx++
				}
				return m, nil
			case "r":
				if len(m.hosts) > 0 && m.discoveryHostIdx < len(m.hosts) {
					target := m.hosts[m.discoveryHostIdx].Name
					m.loading = true
					return m, m.restartDaemonCmd(target)
				}
			case "u":
				if len(m.hosts) > 0 && m.discoveryHostIdx < len(m.hosts) {
					target := m.hosts[m.discoveryHostIdx].Name
					m.loading = true
					return m, m.updateDaemonCmd(target)
				}
			case "h", "1":
				if len(m.hosts) > 0 && m.discoveryHostIdx < len(m.hosts) {
					target := m.hosts[m.discoveryHostIdx].Name
					m.loading = true
					return m, m.reapplyHooksCmd(target)
				}
			}
		}
		// Intercept keystrokes if confirmation dialog is visible
		if m.confirmRestartSession != nil {
			switch msg.String() {
			case "y", "Y":
				sess := m.confirmRestartSession
				m.confirmRestartSession = nil
				return m, m.restartCmd(sess)
			case "n", "N", "esc":
				m.confirmRestartSession = nil
				return m, nil
			default:
				return m, nil
			}
		}

		// Intercept keystrokes if document selector is visible
		if m.viewingDocuments != nil && !m.loading {
			switch msg.String() {
			case "up", "k":
				if m.docSelectedIdx > 0 {
					m.docSelectedIdx--
				}
				return m, nil
			case "down", "j":
				if m.docSelectedIdx < len(m.docPaths)-1 {
					m.docSelectedIdx++
				}
				return m, nil
			case "esc", "backspace", "q":
				m.viewingDocuments = nil
				m.docPaths = nil
				m.docSelectedIdx = 0
				return m, nil
			case "enter":
				if len(m.docPaths) > 0 {
					sess := m.viewingDocuments
					doc := m.docPaths[m.docSelectedIdx]
					m.viewingDocuments = nil
					m.docPaths = nil
					m.docSelectedIdx = 0
					return m, m.openDocCmd(sess, doc)
				}
				m.viewingDocuments = nil
				return m, nil
			default:
				return m, nil
			}
		}

		// Intercept keystrokes if discovery modal is visible
		if m.showDiscovery {
			switch msg.String() {
			case "esc", "q", "H":
				m.showDiscovery = false
				return m, nil
			default:
				return m, nil
			}
		}

		// Intercept keystrokes if project creation wizard is visible
		if m.creatingProject {
			switch msg.String() {
			case "esc":
				m.creatingProject = false
				return m, nil
			case "enter":
				if m.inputStep == 0 {
					if m.newPathInput != "" {
						m.inputStep = 1
					}
				} else if m.inputStep == 1 {
					if m.newNameInput == "" {
						m.creatingProject = false
						return m, m.createProjectCmd(m.newPathInput, "", "")
					}
					m.inputStep = 2
				} else if m.inputStep == 2 {
					m.creatingProject = false
					return m, m.createProjectCmd(m.newPathInput, m.newNameInput, m.newGitInput)
				}
				return m, nil
			case "backspace":
				if m.inputStep == 0 && len(m.newPathInput) > 0 {
					m.newPathInput = m.newPathInput[:len(m.newPathInput)-1]
				} else if m.inputStep == 1 && len(m.newNameInput) > 0 {
					m.newNameInput = m.newNameInput[:len(m.newNameInput)-1]
				} else if m.inputStep == 2 && len(m.newGitInput) > 0 {
					m.newGitInput = m.newGitInput[:len(m.newGitInput)-1]
				}
				return m, nil
			default:
				if text, ok := isPrintableInput(msg); ok {
					if m.inputStep == 0 {
						m.newPathInput += text
					} else if m.inputStep == 1 {
						m.newNameInput += text
					} else if m.inputStep == 2 {
						m.newGitInput += text
					}
				}
				return m, nil
			}
		}

		// Intercept keystrokes if remote host wizard is visible
		if m.registeringHost {
			switch msg.String() {
			case "esc":
				m.registeringHost = false
				return m, nil
			case "enter":
				if m.hostInput != "" {
					target := m.hostInput
					m.registeringHost = false
					m.loading = true
					return m, m.registerHostCmd(target)
				}
				return m, nil
			case "backspace":
				if len(m.hostInput) > 0 {
					m.hostInput = m.hostInput[:len(m.hostInput)-1]
				}
				return m, nil
			default:
				if text, ok := isPrintableInput(msg); ok {
					m.hostInput += text
				}
				return m, nil
			}
		}

		// Intercept keystrokes if move node/session wizard is visible
		if m.movingNode {
			switch msg.String() {
			case "esc":
				m.movingNode = false
				return m, nil
			case "enter":
				if m.moveNewPathInput != "" {
					m.movingNode = false
					if m.movingSessionID != "" {
						return m, m.moveSessionCmd(m.movingSessionHost, m.movingSessionID, m.moveNewPathInput)
					} else {
						return m, m.moveNodeCmd(m.moveOldPath, m.moveNewPathInput)
					}
				}
				return m, nil
			case "backspace":
				if len(m.moveNewPathInput) > 0 {
					m.moveNewPathInput = m.moveNewPathInput[:len(m.moveNewPathInput)-1]
				}
				return m, nil
			default:
				if text, ok := isPrintableInput(msg); ok {
					m.moveNewPathInput += text
				}
				return m, nil
			}
		}

		// Intercept keystrokes if first-run setup modal is visible
		if m.firstRun {
			switch msg.String() {
			case "esc":
				m.firstRun = false
				return m, nil
			case "enter":
				if strings.TrimSpace(m.firstRunInput) != "" {
					m.projectsDir = strings.TrimSpace(m.firstRunInput)
					_ = SaveProjectsDirConfig(m.configPath, m.projectsDir)
				}
				m.firstRun = false
				return m, nil
			case "backspace":
				if len(m.firstRunInput) > 0 {
					m.firstRunInput = m.firstRunInput[:len(m.firstRunInput)-1]
				}
				return m, nil
			default:
				if text, ok := isPrintableInput(msg); ok {
					m.firstRunInput += text
				}
				return m, nil
			}
		}

		// Intercept keystrokes if resume command modal is visible
		if m.showingResumeCmd != nil {
			switch msg.String() {
			case "esc", "q", "c":
				m.showingResumeCmd = nil
				return m, nil
			default:
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.cancelCtx()
			return m, tea.Quit

		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}

		case "down", "j":
			if m.selectedIdx < len(visibleRows)-1 {
				m.selectedIdx++
			}

		case "v":
			m.archivedView = !m.archivedView
			m.selectedIdx = 0

		case "space", "enter", "a":
			if len(visibleRows) == 0 {
				break
			}
			row := visibleRows[m.selectedIdx]
			if row.IsGroup {
				m.collapsed[row.GroupPath] = !m.collapsed[row.GroupPath]
			} else if row.Session != nil {
				if row.Session.Managed || row.Session.TmuxName != "" {
					m.cancelCtx()
					return m, m.attachCmd(row.Session)
				}
				// Observed session without tmux pane: prompt warning before restarting into managed session
				m.confirmRestartSession = row.Session
			}
		case "x":
			if len(visibleRows) == 0 {
				break
			}
			row := visibleRows[m.selectedIdx]
			if !row.IsGroup && row.Session != nil {
				return m, m.toggleArchiveCmd(row.Session)
			}
		case "r":
			if len(visibleRows) == 0 {
				break
			}
			row := visibleRows[m.selectedIdx]
			if !row.IsGroup && row.Session != nil {
				if row.Session.Managed {
					return m, m.restartCmd(row.Session)
				}
				// observed session restart warning
				m.confirmRestartSession = row.Session
			}
		case "K":
			if len(visibleRows) == 0 {
				break
			}
			row := visibleRows[m.selectedIdx]
			if !row.IsGroup && row.Session != nil {
				return m, m.killCmd(row.Session)
			}
		case "d":
			if len(visibleRows) == 0 {
				break
			}
			row := visibleRows[m.selectedIdx]
			if row.IsGroup {
				targetPath := row.GroupPath
				hasChildren := false

				// Check if any registered tree node starts with targetPath + "/"
				for _, node := range m.treeNodes {
					if node.Path != targetPath && strings.HasPrefix(node.Path, targetPath+"/") {
						hasChildren = true
						break
					}
				}

				// Check if any session is inside targetPath or child paths
				if !hasChildren {
					for _, s := range m.sessions {
						if s.NodePath == targetPath || strings.HasPrefix(s.NodePath, targetPath+"/") {
							hasChildren = true
							break
						}
						if s.Cwd != "" {
							for _, node := range m.treeNodes {
								if (node.Path == targetPath || strings.HasPrefix(node.Path, targetPath+"/")) && node.ProjectDir != "" && sameOrSubDir(s.Cwd, node.ProjectDir) {
									hasChildren = true
									break
								}
							}
						}
						if hasChildren {
							break
						}
					}
				}

				if hasChildren {
					m.errMsg = fmt.Sprintf("Cannot delete group '%s': group contains child nodes or active sessions.", row.GroupName)
					return m, nil
				}
				return m, m.deleteNodeCmd(targetPath)
			} else if row.Session != nil {
				return m, m.deleteCmd(row.Session)
			}
		case "o":
			if len(visibleRows) == 0 {
				break
			}
			row := visibleRows[m.selectedIdx]
			if !row.IsGroup && row.Session != nil {
				return m, m.openCodeCmd(row.Session)
			}
		case "t":
			if len(visibleRows) > 0 {
				row := visibleRows[m.selectedIdx]
				if !row.IsGroup && row.Session != nil {
					return m, m.openTerminalTabCmd(row.Session)
				}
			}
		case "s":
			if len(visibleRows) == 0 {
				break
			}
			row := visibleRows[m.selectedIdx]
			var cwd string
			if row.IsGroup {
				for _, node := range m.treeNodes {
					if node.Path == row.GroupPath && node.ProjectDir != "" {
						cwd = node.ProjectDir
						break
					}
				}
				if cwd == "" {
					for _, sess := range m.sessions {
						if sess.ProjectKey == row.GroupPath || sess.Cwd == row.GroupPath {
							cwd = sess.Cwd
							break
						}
					}
				}
				if cwd == "" {
					cwd = row.GroupPath
				}
			} else if row.Session != nil {
				cwd = row.Session.Cwd
			}

			if cwd != "" {
				return m, m.spawnCmd(cwd)
			}
		case "V":
			if len(visibleRows) == 0 {
				break
			}
			row := visibleRows[m.selectedIdx]
			if !row.IsGroup && row.Session != nil {
				m.viewingDocuments = row.Session
				m.loading = true
				return m, m.fetchDocsCmd(row.Session)
			}
		case "H":
			m.showDiscovery = true
			m.loading = true
			return m, m.fetchDiscoveryCmd()
		case "N":
			m.creatingProject = true
			m.inputStep = 0
			m.newPathInput = ""
			m.newNameInput = ""
			m.newGitInput = ""
			if len(visibleRows) > 0 {
				row := visibleRows[m.selectedIdx]
				if row.IsGroup {
					m.newPathInput = row.GroupPath
				} else if row.Session != nil {
					m.newPathInput = row.Session.ProjectKey
				}
			}
		case "R":
			m.registeringHost = true
			m.hostInput = ""
		case "c":
			if len(visibleRows) > 0 {
				row := visibleRows[m.selectedIdx]
				if !row.IsGroup && row.Session != nil {
					m.showingResumeCmd = row.Session
				}
			}
		case "M":
			if len(visibleRows) > 0 {
				row := visibleRows[m.selectedIdx]
				m.movingNode = true
				m.moveNewPathInput = ""
				if row.IsGroup {
					m.moveOldPath = row.GroupPath
					m.movingSessionID = ""
					m.movingSessionHost = ""
				} else if row.Session != nil {
					m.moveOldPath = row.Session.NodePath
					m.movingSessionID = row.Session.ID
					m.movingSessionHost = row.Session.Host
				}
			}
		case "P":
			m.loading = true
			return m, m.purgeSessionsCmd()
		}
	}
	return m, nil
}

func (m *Model) purgeSessionsCmd() tea.Cmd {
	return func() tea.Msg {
		for _, h := range m.hosts {
			_ = PurgeDaemonSessions(h.URL)
		}
		sessions, statuses, err := FetchSessions(m.hosts)
		if err != nil {
			return errorMsg(err.Error())
		}
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	}
}

func (m *Model) fetchNodesCmd() tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == "local" || hostURL == "" {
				hostURL = h.URL
			}
		}
		nodes, err := FetchNodes(hostURL)
		if err != nil {
			return errorMsg(err.Error())
		}
		return nodesMsg(nodes)
	}
}

func (m *Model) fetchDiscoveryCmd() tea.Cmd {
	return func() tea.Msg {
		results := FetchAllAgentDiscovery(m.hosts)
		return discoveryMsg(results)
	}
}

func (m *Model) createProjectCmd(path, name, gitURL string) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == "local" || hostURL == "" {
				hostURL = h.URL
			}
		}
		if err := CreateProject(hostURL, path, name, gitURL, m.projectsDir); err != nil {
			return errorMsg(err.Error())
		}
		nodes, err := FetchNodes(hostURL)
		if err != nil {
			return errorMsg(err.Error())
		}
		return nodesMsg(nodes)
	}
}

func (m *Model) registerHostCmd(target string) tea.Cmd {
	return func() tea.Msg {
		// Stage 1: Test passwordless SSH connectivity
		cmdSSH := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, "echo ok")
		if output, err := cmdSSH.CombinedOutput(); err != nil {
			return errorMsg(fmt.Sprintf("Stage 1 (SSH Connect) Failed: Cannot connect to '%s' via passwordless SSH (%s). Please run 'ssh-copy-id %s' to authorize your public key.", target, strings.TrimSpace(string(output)), target))
		}

		// Stage 2: Ensure remote ackbard daemon is installed and running
		checkCmd := exec.Command("ssh", "-o", "BatchMode=yes", target, "curl -s http://127.0.0.1:7777/v1/version")
		if out, err := checkCmd.Output(); err != nil || !strings.Contains(string(out), "version") {
			// Query remote OS and ARCH via SSH
			osOut, _ := exec.Command("ssh", "-o", "BatchMode=yes", target, "uname -s").Output()
			archOut, _ := exec.Command("ssh", "-o", "BatchMode=yes", target, "uname -m").Output()

			remoteOS := strings.ToLower(strings.TrimSpace(string(osOut)))
			if remoteOS == "" {
				remoteOS = "linux"
			}
			remoteArchRaw := strings.TrimSpace(string(archOut))
			remoteArch := "amd64"
			if strings.Contains(remoteArchRaw, "aarch64") || strings.Contains(remoteArchRaw, "arm64") {
				remoteArch = "arm64"
			}

			// Cross-compile ackbard for target OS and ARCH
			// Cross-compile ackbard and ackbar-hook for target OS and ARCH
			tmpBard := filepath.Join(os.TempDir(), fmt.Sprintf("ackbard-%s-%s", remoteOS, remoteArch))
			tmpHook := filepath.Join(os.TempDir(), fmt.Sprintf("ackbar-hook-%s-%s", remoteOS, remoteArch))

			cmdBard := exec.Command("go", "build", "-o", tmpBard, "./cmd/ackbard")
			cmdBard.Env = append(os.Environ(), "GOOS="+remoteOS, "GOARCH="+remoteArch, "CGO_ENABLED=0")
			_ = cmdBard.Run()

			cmdHook := exec.Command("go", "build", "-o", tmpHook, "./cmd/ackbar-hook")
			cmdHook.Env = append(os.Environ(), "GOOS="+remoteOS, "GOARCH="+remoteArch, "CGO_ENABLED=0")
			_ = cmdHook.Run()

			// Copy compiled binaries to remote ~/.local/bin/
			_ = exec.Command("ssh", "-o", "BatchMode=yes", target, "mkdir -p ~/.local/bin ~/.claude").Run()
			if _, err := os.Stat(tmpBard); err == nil {
				_ = exec.Command("scp", "-o", "BatchMode=yes", tmpBard, fmt.Sprintf("%s:~/.local/bin/ackbard", target)).Run()
				_ = os.Remove(tmpBard)
			}
			if _, err := os.Stat(tmpHook); err == nil {
				_ = exec.Command("scp", "-o", "BatchMode=yes", tmpHook, fmt.Sprintf("%s:~/.local/bin/ackbar-hook", target)).Run()
				_ = os.Remove(tmpHook)
			}

			// Run python hook injector script over SSH to configure Claude Code & Antigravity hooks
			remoteSetupScript := `export PATH=$PATH:$HOME/.local/bin:$HOME/go/bin
chmod +x ~/.local/bin/ackbard ~/.local/bin/ackbar-hook 2>/dev/null
python3 -c '
import json, os
home = os.path.expanduser("~")

# 1. Claude Code Hooks
claude_hook = os.path.join(home, ".local", "bin", "ackbar-hook") + " claude-code "
claude_events = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Notification", "Stop"]
claude_obj = {ev: [{"matcher": "", "hooks": [{"type": "command", "command": claude_hook + ev}]}] for ev in claude_events}
for p in [os.path.join(home, ".claude", "settings.json"), os.path.join(home, ".claude.json")]:
    os.makedirs(os.path.dirname(p), exist_ok=True)
    cfg = {}
    if os.path.exists(p):
        try:
            with open(p) as f: cfg = json.load(f)
        except: pass
    cfg["hooks"] = claude_obj
    with open(p, "w") as f: json.dump(cfg, f, indent=2)

# 2. Antigravity Hooks
agy_hook = os.path.join(home, ".local", "bin", "ackbar-hook") + " antigravity "
agy_events = ["PreInvocation", "PostToolUse", "PermissionRequest", "PostInvocation"]
agy_obj = {ev: [{"command": agy_hook + ev}] for ev in agy_events}
for p in [os.path.join(home, ".gemini", "config", "hooks.json"), os.path.join(home, ".antigravity", "config", "hooks.json")]:
    os.makedirs(os.path.dirname(p), exist_ok=True)
    cfg = {}
    if os.path.exists(p):
        try:
            with open(p) as f: cfg = json.load(f)
        except: pass
    cfg["hooks"] = agy_obj
    with open(p, "w") as f: json.dump(cfg, f, indent=2)
'
pkill ackbard 2>/dev/null || true
nohup ~/.local/bin/ackbard > ~/.ackbard.log 2>&1 &`
			_ = exec.Command("ssh", "-o", "BatchMode=yes", target, remoteSetupScript).Run()

			// Wait up to 3 seconds for remote daemon
			daemonReady := false
			for i := 0; i < 15; i++ {
				time.Sleep(200 * time.Millisecond)
				if out2, err2 := exec.Command("ssh", "-o", "BatchMode=yes", target, "curl -s http://127.0.0.1:7777/v1/version").Output(); err2 == nil && strings.Contains(string(out2), "version") {
					daemonReady = true
					break
				}
			}

			if !daemonReady {
				return errorMsg(fmt.Sprintf("Stage 2 (Remote Daemon) Failed: Connected to '%s' over SSH, but remote 'ackbard' daemon is missing or failed to start on port 7777. Please verify permissions on %s.", target, target))
			}
		}

		// Stage 3: Setup local port forward tunnel
		port := 7778 + len(m.hosts) - 1
		localURL := fmt.Sprintf("http://127.0.0.1:%d", port)

		tunnelCmd := exec.Command("ssh", "-f", "-N", "-o", "ExitOnForwardFailure=yes", "-L", fmt.Sprintf("%d:127.0.0.1:7777", port), target)
		if err := tunnelCmd.Run(); err != nil {
			return errorMsg(fmt.Sprintf("Stage 3 (Local Tunnel) Failed: Could not set up SSH port forward on local port %d to '%s': %v.", port, target, err))
		}

		// Stage 4: Register host in local daemon & persist config
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == "local" || hostURL == "" {
				hostURL = h.URL
			}
		}

		rec := &daemon.HostRecord{
			Name:      target,
			SSHTarget: target,
			URL:       localURL,
		}
		body, _ := json.Marshal(rec)
		reqURL := fmt.Sprintf("%s/v1/hosts", strings.TrimSuffix(hostURL, "/"))
		_, _ = http.Post(reqURL, "application/json", bytes.NewBuffer(body))

		m.hosts = append(m.hosts, HostConfig{Name: target, URL: localURL})
		_ = SaveHostConfig(m.configPath, m.hosts)

		sessions, statuses, err := FetchSessions(m.hosts)
		if err != nil {
			return errorMsg(fmt.Sprintf("Tunneled to '%s', but failed to query remote sessions: %v", target, err))
		}
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	}
}

func (m *Model) moveNodeCmd(oldPath, newPath string) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == "local" || hostURL == "" {
				hostURL = h.URL
			}
		}
		if err := MoveNode(hostURL, oldPath, newPath); err != nil {
			return errorMsg(err.Error())
		}
		nodes, err := FetchNodes(hostURL)
		if err != nil {
			return errorMsg(err.Error())
		}
		return nodesMsg(nodes)
	}
}

func (m *Model) deleteNodeCmd(path string) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == "local" || hostURL == "" {
				hostURL = h.URL
			}
		}
		if err := DeleteNode(hostURL, path); err != nil {
			return errorMsg(err.Error())
		}
		delete(m.groups, path)
		for g := range m.groups {
			if strings.HasPrefix(g, path+"/") {
				delete(m.groups, g)
			}
		}
		_ = SaveGroupConfig(m.configPath, m.groups)
		nodes, err := FetchNodes(hostURL)
		if err != nil {
			return errorMsg(err.Error())
		}
		return nodesMsg(nodes)
	}
}

func (m *Model) restartDaemonCmd(target string) tea.Cmd {
	return func() tea.Msg {
		if target == "local" {
			_, _ = http.Post("http://127.0.0.1:7777/v1/shutdown", "application/json", nil)
			time.Sleep(300 * time.Millisecond)
			home, _ := os.UserHomeDir()
			ackbardBin := "ackbard"
			if home != "" {
				localBin := filepath.Join(home, ".local", "bin", "ackbard")
				if _, err := os.Stat(localBin); err == nil {
					ackbardBin = localBin
				}
			}
			cmd := exec.Command(ackbardBin)
			_ = cmd.Start()
			time.Sleep(300 * time.Millisecond)
		} else {
			var hostURL string
			for _, h := range m.hosts {
				if h.Name == target {
					hostURL = h.URL
					break
				}
			}
			if hostURL != "" {
				_, _ = http.Post(fmt.Sprintf("%s/v1/shutdown", strings.TrimSuffix(hostURL, "/")), "application/json", nil)
			}
			_ = exec.Command("ssh", "-o", "BatchMode=yes", target, "export PATH=$PATH:$HOME/.local/bin:$HOME/go/bin; pkill ackbard 2>/dev/null || true; nohup ~/.local/bin/ackbard > ~/.ackbard.log 2>&1 &").Run()
			time.Sleep(500 * time.Millisecond)
		}
		sessions, statuses, _ := FetchSessions(m.hosts)
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	}
}

func (m *Model) updateDaemonCmd(target string) tea.Cmd {
	return func() tea.Msg {
		if target == "local" {
			if exe, err := os.Executable(); err == nil {
				_ = exec.Command(exe, "setup-hooks", "--no-tui").Run()
			}
		} else {
			osOut, _ := exec.Command("ssh", "-o", "BatchMode=yes", target, "uname -s").Output()
			archOut, _ := exec.Command("ssh", "-o", "BatchMode=yes", target, "uname -m").Output()
			remoteOS := strings.ToLower(strings.TrimSpace(string(osOut)))
			if remoteOS == "" {
				remoteOS = "linux"
			}
			remoteArchRaw := strings.TrimSpace(string(archOut))
			remoteArch := "amd64"
			if strings.Contains(remoteArchRaw, "aarch64") || strings.Contains(remoteArchRaw, "arm64") {
				remoteArch = "arm64"
			}

			tmpBard := filepath.Join(os.TempDir(), fmt.Sprintf("ackbard-%s-%s", remoteOS, remoteArch))
			tmpHook := filepath.Join(os.TempDir(), fmt.Sprintf("ackbar-hook-%s-%s", remoteOS, remoteArch))

			cmdBard := exec.Command("go", "build", "-o", tmpBard, "./cmd/ackbard")
			cmdBard.Env = append(os.Environ(), "GOOS="+remoteOS, "GOARCH="+remoteArch, "CGO_ENABLED=0")
			_ = cmdBard.Run()

			cmdHook := exec.Command("go", "build", "-o", tmpHook, "./cmd/ackbar-hook")
			cmdHook.Env = append(os.Environ(), "GOOS="+remoteOS, "GOARCH="+remoteArch, "CGO_ENABLED=0")
			_ = cmdHook.Run()

			_ = exec.Command("ssh", "-o", "BatchMode=yes", target, "mkdir -p ~/.local/bin ~/.claude").Run()
			if _, err := os.Stat(tmpBard); err == nil {
				_ = exec.Command("scp", "-o", "BatchMode=yes", tmpBard, fmt.Sprintf("%s:~/.local/bin/ackbard", target)).Run()
				_ = os.Remove(tmpBard)
			}
			if _, err := os.Stat(tmpHook); err == nil {
				_ = exec.Command("scp", "-o", "BatchMode=yes", tmpHook, fmt.Sprintf("%s:~/.local/bin/ackbar-hook", target)).Run()
				_ = os.Remove(tmpHook)
			}

			_ = exec.Command("ssh", "-o", "BatchMode=yes", target, "export PATH=$PATH:$HOME/.local/bin:$HOME/go/bin; chmod +x ~/.local/bin/ackbard ~/.local/bin/ackbar-hook 2>/dev/null; pkill ackbard 2>/dev/null || true; nohup ~/.local/bin/ackbard > ~/.ackbard.log 2>&1 &").Run()
			time.Sleep(500 * time.Millisecond)
		}
		sessions, statuses, _ := FetchSessions(m.hosts)
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	}
}

func (m *Model) reapplyHooksCmd(target string) tea.Cmd {
	return func() tea.Msg {
		if target == "local" {
			if exe, err := os.Executable(); err == nil {
				_ = exec.Command(exe, "setup-hooks").Run()
			}
		} else {
			remoteSetupScript := `export PATH=$PATH:$HOME/.local/bin:$HOME/go/bin
python3 -c '
import json, os
home = os.path.expanduser("~")

# 1. Claude Code Hooks
claude_hook = os.path.join(home, ".local", "bin", "ackbar-hook") + " claude-code "
claude_events = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Notification", "Stop"]
claude_obj = {ev: [{"matcher": "", "hooks": [{"type": "command", "command": claude_hook + ev}]}] for ev in claude_events}
for p in [os.path.join(home, ".claude", "settings.json"), os.path.join(home, ".claude.json")]:
    os.makedirs(os.path.dirname(p), exist_ok=True)
    cfg = {}
    if os.path.exists(p):
        try:
            with open(p) as f: cfg = json.load(f)
        except: pass
    cfg["hooks"] = claude_obj
    with open(p, "w") as f: json.dump(cfg, f, indent=2)

# 2. Antigravity Hooks
agy_hook = os.path.join(home, ".local", "bin", "ackbar-hook") + " antigravity "
agy_events = ["PreInvocation", "PostToolUse", "PermissionRequest", "PostInvocation"]
agy_obj = {ev: [{"command": agy_hook + ev}] for ev in agy_events}
for p in [os.path.join(home, ".gemini", "config", "hooks.json"), os.path.join(home, ".antigravity", "config", "hooks.json")]:
    os.makedirs(os.path.dirname(p), exist_ok=True)
    cfg = {}
    if os.path.exists(p):
        try:
            with open(p) as f: cfg = json.load(f)
        except: pass
    cfg["hooks"] = agy_obj
    with open(p, "w") as f: json.dump(cfg, f, indent=2)
'`
			_ = exec.Command("ssh", "-o", "BatchMode=yes", target, remoteSetupScript).Run()
		}
		results := FetchAllAgentDiscovery(m.hosts)
		return discoveryMsg(results)
	}
}

func (m *Model) moveSessionCmd(sessionHost, sessionID, newPath string) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == sessionHost {
				hostURL = h.URL
				break
			}
		}
		if hostURL == "" {
			parts := strings.Split(sessionID, ":")
			if len(parts) >= 2 {
				targetHost := parts[1]
				for _, h := range m.hosts {
					if h.Name == targetHost {
						hostURL = h.URL
						break
					}
				}
			}
		}
		if hostURL == "" {
			for _, h := range m.hosts {
				if h.Name == "local" || hostURL == "" {
					hostURL = h.URL
				}
			}
		}
		if err := MoveSession(hostURL, sessionID, newPath); err != nil {
			return errorMsg(err.Error())
		}
		sessions, statuses, err := FetchSessions(m.hosts)
		if err != nil {
			return errorMsg(err.Error())
		}
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	}
}

func (m *Model) spawnCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == "local" || hostURL == "" {
				hostURL = h.URL
			}
		}

		payload := map[string]string{
			"agent": "claude-code",
			"cwd":   cwd,
		}
		body, _ := json.Marshal(payload)
		reqURL := fmt.Sprintf("%s/v1/sessions/spawn", strings.TrimSuffix(hostURL, "/"))
		resp, err := http.Post(reqURL, "application/json", bytes.NewBuffer(body))
		if err != nil {
			return errorMsg("Failed to spawn session: " + err.Error())
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			return errorMsg(fmt.Sprintf("Failed to spawn session (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody))))
		}

		sessions, statuses, err := FetchSessions(m.hosts)
		if err != nil {
			return errorMsg(err.Error())
		}
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	}
}

func (m *Model) fetchDocsCmd(sess *daemon.Session) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == sess.Host {
				hostURL = h.URL
				break
			}
		}
		docs, err := FetchDocuments(hostURL, sess.ID)
		if err != nil {
			return errorMsg(err.Error())
		}
		return loadDocsMsg(docs)
	}
}

func (m *Model) openDocCmd(sess *daemon.Session, docName string) tea.Cmd {
	return func() tea.Msg {
		codeBin := findCodeBinary()
		var cmd *exec.Cmd
		fullPath := sess.Cwd + "/" + docName
		if sess.Host == "local" || sess.Host == "" {
			cmd = exec.Command(codeBin, fullPath)
		} else {
			hostLabel := sess.Host
			if idx := strings.LastIndex(hostLabel, "@"); idx != -1 {
				hostLabel = hostLabel[idx+1:]
			}
			cmd = exec.Command(codeBin, "--remote", "ssh-remote+"+hostLabel, fullPath)
		}
		if err := cmd.Start(); err != nil {
			return errorMsg("VS Code launcher error: " + err.Error())
		}
		return nil
	}
}

func (m *Model) openCodeCmd(sess *daemon.Session) tea.Cmd {
	return func() tea.Msg {
		codeBin := findCodeBinary()
		var cmd *exec.Cmd
		if sess.Host == "local" || sess.Host == "" {
			cmd = exec.Command(codeBin, sess.Cwd)
		} else {
			hostLabel := sess.Host
			if idx := strings.LastIndex(hostLabel, "@"); idx != -1 {
				hostLabel = hostLabel[idx+1:]
			}
			cmd = exec.Command(codeBin, "--remote", "ssh-remote+"+hostLabel, sess.Cwd)
		}
		if err := cmd.Start(); err != nil {
			return errorMsg("VS Code launcher error: " + err.Error())
		}
		return nil
	}
}

func (m *Model) restartCmd(sess *daemon.Session) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == sess.Host {
				hostURL = h.URL
				break
			}
		}
		if err := RunAction(hostURL, "restart", sess.ID); err != nil {
			return errorMsg(err.Error())
		}
		return nil
	}
}

func (m *Model) killCmd(sess *daemon.Session) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == sess.Host {
				hostURL = h.URL
				break
			}
		}
		if err := RunAction(hostURL, "kill", sess.ID); err != nil {
			return errorMsg(err.Error())
		}
		return nil
	}
}

func (m *Model) deleteCmd(sess *daemon.Session) tea.Cmd {
	return func() tea.Msg {
		var hostURL string
		for _, h := range m.hosts {
			if h.Name == sess.Host {
				hostURL = h.URL
				break
			}
		}
		if err := RunAction(hostURL, "delete", sess.ID); err != nil {
			return errorMsg(err.Error())
		}
		sessions, statuses, err := FetchSessions(m.hosts)
		if err != nil {
			return errorMsg(err.Error())
		}
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	}
}

func (m *Model) toggleArchiveCmd(sess *daemon.Session) tea.Cmd {
	return func() tea.Msg {
		action := "archive"
		if sess.Archived {
			action = "unarchive"
		}

		var hostURL string
		for _, h := range m.hosts {
			if h.Name == sess.Host {
				hostURL = h.URL
				break
			}
		}

		if err := RunAction(hostURL, action, sess.ID); err != nil {
			return errorMsg(err.Error())
		}
		return nil
	}
}

func (m *Model) attachCmd(sess *daemon.Session) tea.Cmd {
	return tea.ExecProcess(m.getAttachCmd(sess), func(err error) tea.Msg {
		m.ctx, m.cancelCtx = context.WithCancel(context.Background())
		SubscribeEvents(m.ctx, m.hosts, m.eventChan)

		if err != nil {
			return errorMsg(fmt.Sprintf("Attach failed: tmux session '%s' on %s is not active or has exited (%v)", sess.TmuxName, sess.Host, err))
		}

		sessions, statuses, fetchErr := FetchSessions(m.hosts)
		if fetchErr != nil {
			return errorMsg(fetchErr.Error())
		}
		return fetchSessionsResult{sessions: sessions, statuses: statuses}
	})
}

func (m *Model) getAttachCmd(sess *daemon.Session) *exec.Cmd {
	tmuxName := sess.TmuxName
	if tmuxName == "" {
		tmuxName = fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
	}

	if sess.Host == "local" {
		return exec.Command("tmux", "attach-session", "-t", tmuxName)
	}

	return exec.Command("ssh", "-t", sess.Host, fmt.Sprintf("tmux attach-session -t %q", tmuxName))
}

func (m *Model) openTerminalTabCmd(sess *daemon.Session) tea.Cmd {
	return func() tea.Msg {
		tmuxName := sess.TmuxName
		if tmuxName == "" {
			tmuxName = fmt.Sprintf("ackbar-%s-%s", sess.Agent, sess.NativeID)
		}

		var attachCmdStr string
		if sess.Host == "local" {
			attachCmdStr = fmt.Sprintf("tmux attach-session -t %q", tmuxName)
		} else {
			attachCmdStr = fmt.Sprintf("ssh -t %s 'tmux attach-session -t %q'", sess.Host, tmuxName)
		}

		// 1. If running inside tmux, open in a new tmux window
		if os.Getenv("TMUX") != "" {
			tabTitle := sess.Name
			if tabTitle == "" {
				tabTitle = tmuxName
			}
			cmd := exec.Command("tmux", "new-window", "-n", tabTitle, attachCmdStr)
			if err := cmd.Run(); err == nil {
				return nil
			}
		}

		termProg := os.Getenv("TERM_PROGRAM")

		// 2. macOS AppleScript for iTerm2 or Terminal.app
		if runtime.GOOS == "darwin" {
			if strings.Contains(termProg, "iTerm") {
				script := fmt.Sprintf(`tell application "iTerm2"
					tell current window
						create tab with default profile
						tell current session of current tab
							write text "%s"
						end tell
					end tell
				end tell`, strings.ReplaceAll(attachCmdStr, `"`, `\"`))
				if err := exec.Command("osascript", "-e", script).Run(); err == nil {
					return nil
				}
			}

			if strings.Contains(termProg, "ghostty") {
				if err := exec.Command("ghostty", "-e", "sh", "-c", attachCmdStr).Start(); err == nil {
					return nil
				}
			}

			if strings.Contains(termProg, "WezTerm") {
				if err := exec.Command("wezterm", "cli", "spawn", "--", "sh", "-c", attachCmdStr).Run(); err == nil {
					return nil
				}
			}

			// Default macOS Terminal.app fallback
			script := fmt.Sprintf(`tell application "Terminal"
				do script "%s"
				activate
			end tell`, strings.ReplaceAll(attachCmdStr, `"`, `\"`))
			if err := exec.Command("osascript", "-e", script).Run(); err == nil {
				return nil
			}
		}

		// 3. Linux / generic terminal fallbacks
		if strings.Contains(termProg, "WezTerm") {
			if err := exec.Command("wezterm", "cli", "spawn", "--", "sh", "-c", attachCmdStr).Run(); err == nil {
				return nil
			}
		}

		for _, term := range []string{"x-terminal-emulator", "gnome-terminal", "alacritty", "kitty"} {
			if _, err := exec.LookPath(term); err == nil {
				var cmd *exec.Cmd
				switch term {
				case "gnome-terminal":
					cmd = exec.Command(term, "--", "sh", "-c", attachCmdStr)
				case "kitty":
					cmd = exec.Command(term, "@", "launch", "--type=tab", "sh", "-c", attachCmdStr)
				default:
					cmd = exec.Command(term, "-e", "sh", "-c", attachCmdStr)
				}
				if err := cmd.Start(); err == nil {
					return nil
				}
			}
		}

		return errorMsg("Failed to open new terminal tab/window automatically")
	}
}

// buildVisibleRows parses logical groups and sessions to build the flat visible TreeRow list
func (m *Model) buildVisibleRows() []TreeRow {
	var rows []TreeRow

	// 0. Deduplicate sessions across hook events and process scanning
	var filteredSessions []*daemon.Session
	seenSessions := make(map[string]*daemon.Session)

	mergeSessions := func(target, src *daemon.Session) {
		// If src is active and target is ended, promote target to active
		if target.State == daemon.StateEnded && src.State != daemon.StateEnded {
			target.State = src.State
			target.Activity = src.Activity
			target.StartedAt = src.StartedAt
			target.LastEventAt = src.LastEventAt
			target.PID = src.PID
			target.TmuxName = src.TmuxName
			target.Managed = src.Managed
			target.ContextPct = src.ContextPct
			if src.Entrypoint != "" {
				target.Entrypoint = src.Entrypoint
			}
		} else if src.State == daemon.StateEnded && target.State != daemon.StateEnded {
			if target.ContextPct == 0 && src.ContextPct > 0 {
				target.ContextPct = src.ContextPct
			}
		}

		if !target.Managed && src.Managed {
			target.Managed = true
		}
		if target.TmuxName == "" {
			target.TmuxName = src.TmuxName
		}
		if target.PID == 0 {
			target.PID = src.PID
		}
		if target.Name == "" || target.Name == target.Agent {
			if src.Name != "" && src.Name != src.Agent {
				target.Name = src.Name
			}
		}
		if target.NodePath == "" && src.NodePath != "" {
			target.NodePath = src.NodePath
		}
	}

	for _, s := range m.sessions {
		var matched *daemon.Session

		if s.TmuxName != "" {
			k1 := fmt.Sprintf("%s:tmux:%s", s.Host, s.TmuxName)
			if ex, ok := seenSessions[k1]; ok {
				matched = ex
			}
		}

		if matched == nil && s.NativeID != "" {
			k2 := fmt.Sprintf("%s:native:%s", s.Host, s.NativeID)
			if ex, ok := seenSessions[k2]; ok {
				matched = ex
			}
		}

		if matched == nil && s.Cwd != "" {
			k3 := fmt.Sprintf("%s:cwd:%s", s.Host, expandPath(s.Cwd))
			if ex, ok := seenSessions[k3]; ok {
				matched = ex
			}
		}

		if matched == nil && s.Name != "" && s.Name != s.Agent {
			k4 := fmt.Sprintf("%s:name:%s", s.Host, strings.ToLower(s.Name))
			if ex, ok := seenSessions[k4]; ok {
				matched = ex
			}
		}

		if matched != nil {
			mergeSessions(matched, s)
			continue
		}

		if s.TmuxName != "" {
			seenSessions[fmt.Sprintf("%s:tmux:%s", s.Host, s.TmuxName)] = s
		}
		if s.NativeID != "" {
			seenSessions[fmt.Sprintf("%s:native:%s", s.Host, s.NativeID)] = s
		}
		if s.Cwd != "" {
			seenSessions[fmt.Sprintf("%s:cwd:%s", s.Host, expandPath(s.Cwd))] = s
		}
		if s.Name != "" && s.Name != s.Agent {
			seenSessions[fmt.Sprintf("%s:name:%s", s.Host, strings.ToLower(s.Name))] = s
		}
		filteredSessions = append(filteredSessions, s)
	}

	// 1. Group sessions by their assigned logical path
	sessionsByPath := make(map[string][]*daemon.Session)
	var unassignedSessions []*daemon.Session

	// Sort nodes by longest path first so most specific child node matches before parent
	nodesCopy := make([]*daemon.TreeNode, len(m.treeNodes))
	copy(nodesCopy, m.treeNodes)
	sort.Slice(nodesCopy, func(i, j int) bool {
		return len(nodesCopy[i].Path) > len(nodesCopy[j].Path)
	})

	for _, s := range filteredSessions {
		// Skip based on archive view state
		if s.Archived != m.archivedView {
			continue
		}

		assignedPath := s.NodePath
		if assignedPath == "" && s.Cwd != "" {
			for _, node := range nodesCopy {
				if node.ProjectDir != "" && sameOrSubDir(s.Cwd, node.ProjectDir) {
					assignedPath = node.Path
					break
				}
			}
		}

		if assignedPath == "" {
			for path, projKeys := range m.groups {
				for _, pk := range projKeys {
					if s.ProjectKey == pk {
						assignedPath = path
						break
					}
				}
				if assignedPath != "" {
					break
				}
			}
		}

		if assignedPath != "" {
			sessionsByPath[assignedPath] = append(sessionsByPath[assignedPath], s)
		} else {
			unassignedSessions = append(unassignedSessions, s)
		}
	}

	// 2. Discover all unique group nodes for registered tree nodes, group configs, and active session paths
	allGroupPaths := make(map[string]bool)

	for _, node := range m.treeNodes {
		if node.Path != "" {
			parts := strings.Split(node.Path, "/")
			for i := 1; i <= len(parts); i++ {
				parentPath := strings.Join(parts[:i], "/")
				allGroupPaths[parentPath] = true
			}
		}
	}

	for path := range m.groups {
		if path != "" {
			parts := strings.Split(path, "/")
			for i := 1; i <= len(parts); i++ {
				parentPath := strings.Join(parts[:i], "/")
				allGroupPaths[parentPath] = true
			}
		}
	}

	for path, sessList := range sessionsByPath {
		if len(sessList) > 0 {
			parts := strings.Split(path, "/")
			for i := 1; i <= len(parts); i++ {
				parentPath := strings.Join(parts[:i], "/")
				allGroupPaths[parentPath] = true
			}
		}
	}

	if len(unassignedSessions) > 0 {
		allGroupPaths["Unassigned"] = true
	}

	// Sort group paths alphabetically
	var sortedGroups []string
	for path := range allGroupPaths {
		if path != "Unassigned" {
			sortedGroups = append(sortedGroups, path)
		}
	}
	sort.Strings(sortedGroups)
	sortedGroups = append(sortedGroups, "Unassigned") // place Unassigned at the end

	// 3. Helper to recursively add group nodes and their matching sessions
	var addGroup func(path string, indent int)
	addedGroups := make(map[string]bool)

	addGroup = func(path string, indent int) {
		if addedGroups[path] {
			return
		}
		addedGroups[path] = true

		parts := strings.Split(path, "/")
		name := parts[len(parts)-1]

		isCollapsed := m.collapsed[path]

		rows = append(rows, TreeRow{
			IsGroup:   true,
			GroupPath: path,
			GroupName: name,
			Indent:    indent,
		})

		if isCollapsed {
			return
		}

		// Add matching sessions directly in this group
		var directSessions []*daemon.Session
		if path == "Unassigned" {
			directSessions = unassignedSessions
		} else {
			directSessions = sessionsByPath[path]
		}
		for _, s := range directSessions {
			rows = append(rows, TreeRow{
				IsGroup: false,
				Session: s,
				Indent:  indent + 1,
			})
		}

		// Find and recursively add nested subgroups
		for _, g := range sortedGroups {
			if strings.HasPrefix(g, path+"/") {
				subparts := strings.Split(strings.TrimPrefix(g, path+"/"), "/")
				if len(subparts) == 1 { // only direct descendants
					addGroup(g, indent+1)
				}
			}
		}
	}

	// Add root groups (indent 0)
	for _, g := range sortedGroups {
		if !strings.Contains(g, "/") && g != "Unassigned" {
			addGroup(g, 0)
		}
	}

	if len(unassignedSessions) > 0 {
		addGroup("Unassigned", 0)
	}

	return rows
}

func (m *Model) View() string {
	if m.loading {
		return "\n  Loading tactical display...\n"
	}

	var sb strings.Builder

	// Header block
	headerText := fmt.Sprintf("⚓ ACKBAR SESSION TACTICAL DISPLAY [v%s]", version.Version)
	if m.archivedView {
		headerText += " [ARCHIVED VIEW]"
	}
	sb.WriteString(headerStyle.Render(headerText))
	sb.WriteString("\n")

	// Connected machines project directories & connection status summary
	var machineSummaries []string
	for _, h := range m.hosts {
		st, ok := m.hostStatuses[h.Name]
		pDir := h.ProjectsDir
		if pDir == "" {
			pDir = m.projectsDir
		}

		verText := ""
		if ok && st.Version != "" && st.Version != "unknown" {
			verText = " v" + st.Version
		}

		statusBadge := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Render("[ONLINE]")
		if ok && !st.Online {
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3333")).Render("[OFFLINE]")
		}
		machineSummaries = append(machineSummaries, fmt.Sprintf("%s%s %s (%s)", h.Name, verText, statusBadge, pDir))
	}
	if len(machineSummaries) > 0 {
		summaryLine := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(fmt.Sprintf("💻 Connected Machines: %s", strings.Join(machineSummaries, " | ")))
		sb.WriteString(summaryLine)
		sb.WriteString("\n\n")
	}

	visibleRows := m.buildVisibleRows()

	if len(visibleRows) == 0 {
		emptyBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4682B4")).
			Padding(1, 2).
			MarginTop(1).
			Render(
				"📡 NO SESSIONS OR PROJECTS DETECTED\n\n" +
					"Ackbar is active and supervising local & remote agent state.\n\n" +
					"• Press 'H' to view discovered AI agents and active hook settings.\n" +
					"• Press 'N' to create a new project directory or group.\n" +
					"• Press 'R' to register a remote SSH machine.",
			)
		sb.WriteString(emptyBox)
		sb.WriteString("\n\n")
	} else {
		for i, r := range visibleRows {
			isSelected := i == m.selectedIdx
			indentStr := strings.Repeat("  ", r.Indent)

			var rowContent string
			if r.IsGroup {
				collapseArrow := "▼"
				if m.collapsed[r.GroupPath] {
					collapseArrow = "▶"
				}
				rowContent = fmt.Sprintf("%s %s", collapseArrow, groupStyle.Render(r.GroupName))
			} else {
				s := r.Session
				displayName := s.Name
				if displayName == "" {
					if s.TmuxName != "" {
						displayName = s.TmuxName
					} else if s.NativeID != "" {
						displayName = fmt.Sprintf("%s (%s)", s.Agent, s.NativeID)
					} else {
						displayName = s.Agent
					}
				}
				if !strings.HasPrefix(displayName, `"`) && !strings.HasPrefix(displayName, "claude") && !strings.HasPrefix(displayName, "antigravity") && !strings.HasPrefix(displayName, "codex") {
					displayName = fmt.Sprintf("%q", displayName)
				}

				var statusEmoji string
				switch s.State {
				case daemon.StateWorking:
					statusEmoji = "⚡"
				case daemon.StateBlocked:
					statusEmoji = "❓"
				case daemon.StateIdle:
					statusEmoji = "✅"
				case daemon.StateEnded, daemon.StateFailed:
					statusEmoji = "⚪"
				default:
					statusEmoji = "⚪"
				}

				managedStr := "(observed)"
				managedTag := lipgloss.NewStyle().Foreground(lipgloss.Color("#DA70D6")).Render(managedStr)
				if s.Managed {
					managedStr = "(managed)"
					managedTag = lipgloss.NewStyle().Foreground(lipgloss.Color("#1E90FF")).Render(managedStr)
				}

				tmuxTag := ""
				if s.TmuxName != "" {
					tmuxTag = fmt.Sprintf(" [tmux: %s]", lipgloss.NewStyle().Foreground(lipgloss.Color("#1E90FF")).Render(s.TmuxName))
				}

				originTag := ""
				if s.Entrypoint != "" {
					epStr := s.Entrypoint
					if strings.HasPrefix(epStr, "claude-") {
						epStr = strings.TrimPrefix(epStr, "claude-")
					}
					originTag = fmt.Sprintf(" [%s]", lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Render(strings.ToUpper(epStr)))
				}

				ctxTag := ""
				if s.ContextPct > 0 {
					ctxColor := "#00FF00"
					if s.ContextPct > 70 {
						ctxColor = "#FFA500"
					}
					if s.ContextPct > 85 {
						ctxColor = "#FF3333"
					}
					ctxTag = fmt.Sprintf(" [ctx: %s]", lipgloss.NewStyle().Foreground(lipgloss.Color(ctxColor)).Render(fmt.Sprintf("%d%%", s.ContextPct)))
				}

				rowHeader := fmt.Sprintf("%s%s%s%s @%s %s %s", sessionTitleStyle.Render(displayName), tmuxTag, originTag, ctxTag, hostStyle.Render(s.Host), statusEmoji, managedTag)

				if isSelected {
					var statusBadge string
					switch s.State {
					case daemon.StateWorking:
						statusBadge = workingStatusStyle.Render("[WORKING]")
					case daemon.StateBlocked:
						statusBadge = blockedStatusStyle.Render("[BLOCKED]")
						if s.Blocked != nil {
							statusBadge += fmt.Sprintf(" (%s: %s)", s.Blocked.Kind, s.Blocked.Reason)
						}
					case daemon.StateIdle:
						statusBadge = idleStatusStyle.Render("[IDLE]")
					case daemon.StateEnded:
						statusBadge = endedStatusStyle.Render("[ENDED]")
					case daemon.StateFailed:
						statusBadge = endedStatusStyle.Render("[FAILED]")
					default:
						statusBadge = endedStatusStyle.Render("[UNKNOWN]")
					}

					rowID := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(fmt.Sprintf("ID: %s", s.NativeID))
					rowCwd := fmt.Sprintf("CWD: %s", cwdStyle.Render(s.Cwd))
					rowInfo := fmt.Sprintf("%s | %s", statusBadge, rowCwd)
					rowContent = fmt.Sprintf("%s  [%s]\n%s  %s\n%s  %s", rowHeader, rowID, indentStr, rowInfo, indentStr, activityStyle.Render(s.Activity))
				} else {
					rowContent = rowHeader
				}
			}

			var rowStr string
			if isSelected {
				rowStr = selectedStyle.Render(fmt.Sprintf("%s%s", indentStr, rowContent))
			} else {
				rowStr = unselectedStyle.Render(fmt.Sprintf("%s%s", indentStr, rowContent))
			}

			sb.WriteString(rowStr)
			sb.WriteString("\n")
			if !r.IsGroup && isSelected {
				sb.WriteString("\n") // Add spacing for selected session details
			}
		}
	}

	if m.confirmRestartSession != nil {
		warningBox := lipgloss.NewStyle().
			Bold(true).
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#FF3333")).
			Padding(1, 2).
			MarginTop(1).
			Render(fmt.Sprintf(
				"⚠️  WARNING: RESUMING OBSERVED SESSION\n\n"+
					"This session (%s) was started manually outside of Ackbar.\n"+
					"Resuming it will run it inside a managed tmux session, but\n"+
					"custom environment variables or CLI flags may not carry over.\n\n"+
					"[y] Resume in Managed tmux  |  [n] Cancel",
				m.confirmRestartSession.ID,
			))
		sb.WriteString("\n")
		sb.WriteString(warningBox)
		sb.WriteString("\n")
	}

	if m.viewingDocuments != nil && !m.loading {
		var docsBuilder strings.Builder
		docsBuilder.WriteString("📂 WORKSPACE DOCUMENTS\n\n")
		if len(m.docPaths) == 0 {
			docsBuilder.WriteString("  No documents found in workspace.\n")
		} else {
			for idx, d := range m.docPaths {
				isSelected := idx == m.docSelectedIdx
				if isSelected {
					docsBuilder.WriteString(fmt.Sprintf("▶ %s\n", lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00FFFF")).Render(d)))
				} else {
					docsBuilder.WriteString(fmt.Sprintf("  %s\n", d))
				}
			}
		}
		docsBuilder.WriteString("\n[Enter] Open in Editor | [esc/q] Go Back")

		docBox := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#1E90FF")).
			Padding(1, 2).
			MarginTop(1).
			Render(docsBuilder.String())
		sb.WriteString("\n")
		sb.WriteString(docBox)
		sb.WriteString("\n")
	}

	if m.showDiscovery && !m.loading {
		var discBuilder strings.Builder
		discBuilder.WriteString("💻 DAEMON & AGENT MANAGEMENT CONTROL PANEL\n\n")
		for idx, h := range m.hosts {
			isSelectedHost := idx == m.discoveryHostIdx
			st, ok := m.hostStatuses[h.Name]
			pDir := h.ProjectsDir
			if pDir == "" {
				pDir = m.projectsDir
			}

			verText := ""
			if ok && st.Version != "" && st.Version != "unknown" {
				verText = fmt.Sprintf(" (v%s)", st.Version)
			}

			statusText := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Render("✅ ONLINE" + verText)
			if ok && !st.Online {
				errStageText := ""
				if st.ErrorStage != "" {
					errStageText = fmt.Sprintf(" [%s]", st.ErrorStage)
				}
				errText := ""
				if st.Error != "" {
					errText = fmt.Sprintf(" (%s)", st.Error)
				}
				statusText = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3333")).Render("❌ OFFLINE" + errStageText + errText)
			}

			prefix := "  "
			hostNameFormatted := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1E90FF")).Render(h.Name)
			if isSelectedHost {
				prefix = "▶ "
				hostNameFormatted = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00FFFF")).Render(h.Name)
			}

			discBuilder.WriteString(fmt.Sprintf("%sHost: %s [%s] ➔ Status: %s | Projects Dir: %s\n",
				prefix,
				hostNameFormatted,
				h.URL,
				statusText,
				lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00FF00")).Render(pDir)))

			if discList, exists := m.discoveryResults[h.Name]; exists && len(discList) > 0 {
				for _, d := range discList {
					instText := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3333")).Render("❌ Not Installed")
					if d.Installed {
						instText = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Render("✅ Installed")
					}
					hookText := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3333")).Render("❌ Hook Missing")
					if d.HookConfigured {
						hookText = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Render("✅ Hook Active")
					}

					discBuilder.WriteString(fmt.Sprintf("    ↳ Agent: %s ➔ Status: %s | %s\n", lipgloss.NewStyle().Bold(true).Render(d.Agent), instText, hookText))
					if d.Installed && !d.HookConfigured {
						discBuilder.WriteString(fmt.Sprintf("      Setup Cmd: %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF")).Render(d.SetupCmd)))
					}
				}
			} else {
				discBuilder.WriteString("    ↳ Agent Discovery: Querying host agents...\n")
			}
			discBuilder.WriteString("\n")
		}

		selectedHostName := "local"
		if m.discoveryHostIdx < len(m.hosts) {
			selectedHostName = m.hosts[m.discoveryHostIdx].Name
		}

		controlsHeader := fmt.Sprintf("── HOST CONTROLS [%s] ──────────────────────────────────────────", selectedHostName)
		discBuilder.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF")).Render(controlsHeader) + "\n")
		discBuilder.WriteString(" [r] Restart ackbard Daemon    | [u] Update Binary & Restart Daemon\n")
		discBuilder.WriteString(" [k] Re-apply Agent Hooks      | [esc/q/H] Close Management Panel")

		discBox := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#00FFFF")).
			Padding(1, 2).
			MarginTop(1).
			Render(discBuilder.String())
		sb.WriteString("\n")
		sb.WriteString(discBox)
		sb.WriteString("\n")
	}

	if m.creatingProject {
		var formBuilder strings.Builder
		formBuilder.WriteString("✨ NEW PROJECT / SUBGROUP WIZARD\n\n")
		if m.inputStep == 0 {
			formBuilder.WriteString(fmt.Sprintf("1. Enter Tree Path (e.g. Work/ProjectY/ProjectY-web):\n▶ %s_\n", m.newPathInput))
		} else if m.inputStep == 1 {
			formBuilder.WriteString(fmt.Sprintf("Tree Path: %s\n2. Enter Folder Name or Absolute Path (Optional - press Enter to create an empty subgroup):\n▶ %s_\n", m.newPathInput, m.newNameInput))
		} else if m.inputStep == 2 {
			formBuilder.WriteString(fmt.Sprintf("Tree Path: %s\nFolder Name: %s\n3. Enter Git Remote URL (Optional, press Enter to init empty git repo):\n▶ %s_\n", m.newPathInput, m.newNameInput, m.newGitInput))
		}
		formBuilder.WriteString("\n[Enter] Next/Submit | [esc] Cancel")

		formBox := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#FFA500")).
			Padding(1, 2).
			MarginTop(1).
			Render(formBuilder.String())
		sb.WriteString("\n")
		sb.WriteString(formBox)
		sb.WriteString("\n")
	}

	if m.registeringHost {
		var hostBuilder strings.Builder
		hostBuilder.WriteString("🌐 REGISTER REMOTE MACHINE WIZARD\n\n")
		hostBuilder.WriteString(fmt.Sprintf("Enter Remote SSH Target (e.g. user@remote-box):\n▶ %s_\n", m.hostInput))
		hostBuilder.WriteString("\n[Enter] Register & Test SSH | [esc] Cancel")

		hostBox := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#DA70D6")).
			Padding(1, 2).
			MarginTop(1).
			Render(hostBuilder.String())
		sb.WriteString("\n")
		sb.WriteString(hostBox)
		sb.WriteString("\n")
	}

	if m.movingNode {
		var moveBuilder strings.Builder
		moveBuilder.WriteString("📦 MOVE NODE / SESSION WIZARD\n\n")
		if m.movingSessionID != "" {
			moveBuilder.WriteString(fmt.Sprintf("Moving Session: %s\nTarget Parent Path:\n▶ %s_\n", m.movingSessionID, m.moveNewPathInput))
		} else {
			moveBuilder.WriteString(fmt.Sprintf("Moving Path: %s\nNew Path:\n▶ %s_\n", m.moveOldPath, m.moveNewPathInput))
		}
		moveBuilder.WriteString("\n[Enter] Confirm Move | [esc] Cancel")

		moveBox := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#1E90FF")).
			Padding(1, 2).
			MarginTop(1).
			Render(moveBuilder.String())
		sb.WriteString("\n")
		sb.WriteString(moveBox)
		sb.WriteString("\n")
	}

	if m.showingResumeCmd != nil {
		var cmdBuilder strings.Builder
		cmdBuilder.WriteString("📋 MANUAL TERMINAL RESUME COMMAND\n\n")
		cmdBuilder.WriteString(fmt.Sprintf("Session ID: %s\n", lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00FFFF")).Render(m.showingResumeCmd.NativeID)))
		cmdBuilder.WriteString(fmt.Sprintf("Working Directory: %s\n\n", m.showingResumeCmd.Cwd))
		cmdBuilder.WriteString("To resume this session manually in your shell, run:\n")
		cmdBuilder.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00FF00")).Render(fmt.Sprintf("cd %s && claude resume %s", m.showingResumeCmd.Cwd, m.showingResumeCmd.NativeID)))
		cmdBuilder.WriteString("\n\n[Press any key or Esc to Dismiss]")

		cmdBox := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#00FF00")).
			Padding(1, 2).
			MarginTop(1).
			Render(cmdBuilder.String())
		sb.WriteString("\n")
		sb.WriteString(cmdBox)
		sb.WriteString("\n")
	}

	if m.errMsg != "" {
		var errBuilder strings.Builder
		errBuilder.WriteString("⚠️  SYSTEM ALERT\n\n")
		errBuilder.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF5555")).Render(m.errMsg))
		errBuilder.WriteString("\n\n[Press any key or Enter / Esc to Dismiss]")

		alertBox := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#FF3333")).
			Padding(1, 3).
			MarginTop(1).
			Render(errBuilder.String())
		sb.WriteString("\n")
		sb.WriteString(alertBox)
		sb.WriteString("\n")
	}

	if m.firstRun {
		var welcomeBuilder strings.Builder
		welcomeBuilder.WriteString("⚓ WELCOME TO ACKBAR — FIRST-TIME SETUP\n\n")
		welcomeBuilder.WriteString("Ackbar uses a default base directory when creating new projects.\n")
		welcomeBuilder.WriteString("Please specify your preferred projects directory:\n\n")
		welcomeBuilder.WriteString(fmt.Sprintf("▶ %s_\n", m.firstRunInput))
		welcomeBuilder.WriteString("\n[Enter] Save & Continue | [esc] Keep Default (~/Projects)")

		welcomeBox := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#1E90FF")).
			Padding(1, 2).
			MarginTop(1).
			Render(welcomeBuilder.String())
		sb.WriteString("\n")
		sb.WriteString(welcomeBox)
		sb.WriteString("\n")
	}

	sb.WriteString(helpStyle.Render("↑/↓: navigate | Enter/a: attach | t: new tab | s: spawn | c: resume cmd | N: new | M: move | H: hooks | R: remote | r: restart | k: kill | d: delete | o: code | V: docs | q: quit"))

	return sb.String()
}

func FormatResumeCmd(agent, nativeID string) string {
	switch agent {
	case "claude-code":
		return fmt.Sprintf("claude resume %s", nativeID)
	case "codex":
		return fmt.Sprintf("codex resume %s", nativeID)
	case "antigravity":
		return fmt.Sprintf("agy resume %s", nativeID)
	default:
		return fmt.Sprintf("claude resume %s", nativeID)
	}
}

func SaveProjectsDirConfig(path, projectsDir string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = make(map[string]interface{})
	}
	cfg["projects_dir"] = projectsDir
	newData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, newData, 0644)
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Clean(path)
}

func sameOrSubDir(cwd, projDir string) bool {
	if cwd == "" || projDir == "" {
		return false
	}
	c := expandPath(cwd)
	p := expandPath(projDir)

	if c == p {
		return true
	}
	if strings.HasPrefix(c, p+"/") {
		return true
	}
	return false
}

func isPrintableInput(msg tea.KeyMsg) (string, bool) {
	str := msg.String()
	if str == "enter" || str == "esc" || str == "backspace" || str == "tab" || str == "up" || str == "down" || str == "left" || str == "right" {
		return "", false
	}
	if strings.HasPrefix(str, "ctrl+") || strings.HasPrefix(str, "alt+") || strings.HasPrefix(str, "f") {
		return "", false
	}
	return str, true
}

func findCodeBinary() string {
	if p, err := exec.LookPath("code"); err == nil {
		return p
	}
	candidates := []string{
		"/usr/local/bin/code",
		"/opt/homebrew/bin/code",
		"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
		"/Applications/Visual Studio Code - Insiders.app/Contents/Resources/app/bin/code",
		"/Applications/Cursor.app/Contents/Resources/app/bin/cursor",
		"/Applications/Windsurf.app/Contents/Resources/app/bin/windsurf",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "code"
}
