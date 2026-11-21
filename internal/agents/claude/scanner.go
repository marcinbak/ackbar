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

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

var ignoredSubcommands = map[string]bool{
	"mcp": true, "doctor": true, "update": true, "upgrade": true,
	"auth": true, "login": true, "logout": true, "install": true,
	"plugin": true, "plugins": true, "project": true, "auto-mode": true,
	"gateway": true, "import": true, "logs": true, "respawn": true,
	"rm": true, "setup-token": true, "stop": true, "kill": true,
	"ultrareview": true, "agents": true, "attach": true,
}

func (*Plugin) IsValidNativeID(nativeID string) bool { return agentutil.IsUUID(nativeID) }

func (*Plugin) IsIgnoredCommand(fullCmd string) bool {
	return agentutil.CheckIgnoredCLIArgs(fullCmd, func(field, base string) bool {
		return base == "claude" || strings.HasPrefix(base, "claude") || strings.HasSuffix(field, "/claude.js") || strings.Contains(field, "claude-code")
	}, ignoredSubcommands, nil)
}

func (*Plugin) MatchProcessCommand(binLower, cmdLower string) bool {
	if binLower == "claude" || ((binLower == "node" || binLower == "bun") &&
		(strings.Contains(cmdLower, "@anthropic-ai/claude-code") || strings.Contains(cmdLower, "claude-code") ||
			strings.Contains(cmdLower, "/claude.js") || strings.Contains(cmdLower, "bin/claude"))) {
		if !agentutil.IsAckbarProcess(binLower, cmdLower) && !strings.Contains(cmdLower, "jest-worker") &&
			!strings.Contains(cmdLower, "react-native") && !strings.Contains(cmdLower, "yarn") {
			return true
		}
	}
	return false
}

func (*Plugin) RequiresResolvedSessionID() bool { return true }

func (*Plugin) ResolveSessionForPID(_ context.Context, homeDir string, pid int, cmdFields []string) string {
	home := effectiveHome(homeDir)
	var sID string
	if home != "" && pid > 0 {
		sessionsDir := filepath.Join(home, ".claude", "sessions")
		sID = readPIDSessionID(filepath.Join(sessionsDir, fmt.Sprintf("%d.json", pid)))
		if sID == "" {
			if out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output(); err == nil {
				for _, cpStr := range strings.Fields(string(out)) {
					if cp, err := strconv.Atoi(cpStr); err == nil && cp > 0 {
						if cid := readPIDSessionID(filepath.Join(sessionsDir, fmt.Sprintf("%d.json", cp))); cid != "" {
							sID = cid
							break
						}
					}
				}
			}
		}
	}
	if sID == "" {
		for i, arg := range cmdFields {
			if (arg == "--session-id" || arg == "-s" || arg == "--resume" || arg == "-r") && i+1 < len(cmdFields) {
				if cand := strings.TrimSpace(cmdFields[i+1]); agentutil.IsUUID(cand) {
					sID = cand
					break
				}
			}
		}
	}
	if agentutil.IsUUID(sID) {
		return sID
	}
	return ""
}

func readPIDSessionID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var meta struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(data, &meta) == nil {
		return meta.SessionID
	}
	return ""
}

func (*Plugin) ResolveSessionOwner(homeDir, sessionID string) (int, string) {
	home := effectiveHome(homeDir)
	if home == "" || !agentutil.IsUUID(sessionID) {
		return 0, ""
	}
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0, ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var meta struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			Tmux      string `json:"tmux"`
		}
		if json.Unmarshal(data, &meta) == nil && meta.SessionID == sessionID {
			tName := meta.Tmux
			if idx := strings.Index(tName, ":"); idx != -1 {
				tName = tName[:idx]
			}
			p := meta.PID
			if p == 0 {
				p, _ = strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
			}
			return p, tName
		}
	}
	return 0, ""
}

func (*Plugin) ScanDisk(_ context.Context, _ agent.DiskScanInput) agent.DiskScanResult {
	return agent.DiskScanResult{}
}
