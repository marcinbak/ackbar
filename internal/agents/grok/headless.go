package grok

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) Mode() agent.HeadlessExecMode {
	return agent.HeadlessModeACP
}

func (*Plugin) ResolveBinary() (string, error) {
	home := agentutil.RealUserHome()
	var extra []string
	if home != "" {
		extra = append(extra, filepath.Join(home, ".grok", "bin"))
	}
	if p, err := agentutil.ResolveBinary("grok", extra...); err == nil && p != "" {
		return p, nil
	}
	if p, err := agentutil.ResolveBinary("grok-cli", extra...); err == nil && p != "" {
		return p, nil
	}
	return "", exec.ErrNotFound
}

func (p *Plugin) BuildTurnSpec(in agent.HeadlessTurnInput) (agent.HeadlessTurnSpec, error) {
	if in.Session == nil {
		return agent.HeadlessTurnSpec{}, fmt.Errorf("nil session")
	}
	if !agentutil.IsSafeGrokSessionID(in.Session.NativeID) {
		return agent.HeadlessTurnSpec{}, fmt.Errorf("unsafe grok session ID: %q", in.Session.NativeID)
	}

	args := []string{"agent", "stdio", "--no-auto-update"}
	if !in.IsExisting {
		for _, dir := range in.Session.ExtraDirs {
			dir = strings.TrimSpace(dir)
			if dir != "" && !strings.ContainsAny(dir, "\x00\r\n") {
				cleaned := filepath.Clean(dir)
				if !strings.HasPrefix(cleaned, "-") && filepath.IsAbs(cleaned) {
					args = append(args, "--add-dir", cleaned)
				}
			}
		}
	}

	pm := strings.ToLower(strings.TrimSpace(in.Session.PermissionMode))
	if in.Session.YoloMode || pm == "bypasspermissions" {
		args = append(args, "--always-approve")
	} else {
		switch pm {
		case "plan":
			args = append(args, "--permission-mode", "plan")
		case "acceptedits":
			args = append(args, "--permission-mode", "acceptEdits")
		case "dontask":
			args = append(args, "--permission-mode", "dontAsk")
		case "auto":
			args = append(args, "--permission-mode", "auto")
		}
	}

	if m := strings.TrimSpace(in.Session.Model); m != "" && !strings.EqualFold(m, "default") && agentutil.IsSafeCLIArg(m) {
		args = append(args, "-m", m)
	}
	if e := strings.TrimSpace(in.Session.Effort); e != "" && !strings.EqualFold(e, "default") && agentutil.IsSafeCLIArg(e) {
		args = append(args, "--effort", e)
	}

	bin, err := p.ResolveBinary()
	if err != nil || bin == "" {
		bin = "grok"
	}
	return agent.HeadlessTurnSpec{
		Binary: bin,
		Args:   args,
		Cwd:    in.Session.Cwd,
		Mode:   agent.HeadlessModeACP,
	}, nil
}

func (p *Plugin) ComputeUsage(sessionID, cwd, model string) (agent.TranscriptUsage, bool) {
	_ = model
	nativeID := sessionID
	if parts := strings.Split(sessionID, ":"); len(parts) >= 3 {
		nativeID = parts[2]
	}
	return p.ComputeTranscriptUsage(agentutil.RealUserHome(), cwd, nativeID, 0)
}

func (p *Plugin) ComputeTranscriptUsage(homeDir, cwd, nativeID string, defaultLimit int) (agent.TranscriptUsage, bool) {
	u := agent.TranscriptUsage{ContextLimitTokens: defaultLimit}
	sessDir := FindSessionDirForHome(homeDir, cwd, nativeID)
	if sessDir != "" {
		if sigData, err := os.ReadFile(filepath.Join(sessDir, "signals.json")); err == nil {
			var sig struct {
				ContextTokensUsed, ContextWindowTokens int
			}
			if json.Unmarshal(sigData, &sig) == nil {
				if sig.ContextWindowTokens > 0 {
					u.ContextLimitTokens = sig.ContextWindowTokens
				}
				if sig.ContextTokensUsed > 0 {
					u.LastTurnContextTokens = sig.ContextTokensUsed
				}
			}
		}

		if usageData, err := os.ReadFile(filepath.Join(sessDir, "usage.json")); err == nil {
			var usg struct {
				CostUsdTicks     float64 `json:"costUsdTicks"`
				InputTokens      int     `json:"inputTokens"`
				OutputTokens     int     `json:"outputTokens"`
				CachedReadTokens int     `json:"cachedReadTokens"`
				Session          *struct {
					InputTokens      int     `json:"inputTokens"`
					OutputTokens     int     `json:"outputTokens"`
					CachedReadTokens int     `json:"cachedReadTokens"`
					CostUsdTicks     float64 `json:"costUsdTicks"`
				} `json:"session"`
			}
			if json.Unmarshal(usageData, &usg) == nil {
				if usg.Session != nil && (usg.Session.InputTokens > 0 || usg.Session.OutputTokens > 0 || usg.Session.CachedReadTokens > 0 || usg.Session.CostUsdTicks > 0) {
					u.InputTokens = usg.Session.InputTokens
					u.OutputTokens = usg.Session.OutputTokens
					u.CacheReadTokens = usg.Session.CachedReadTokens
					if usg.Session.CostUsdTicks > 0 {
						u.CostUSD = usg.Session.CostUsdTicks / 1e10
					}
				} else {
					u.InputTokens = usg.InputTokens
					u.OutputTokens = usg.OutputTokens
					u.CacheReadTokens = usg.CachedReadTokens
					if usg.CostUsdTicks > 0 {
						u.CostUSD = usg.CostUsdTicks / 1e10
					}
				}
			}
		}
	}

	if tr, err := p.ExtractTranscript(homeDir, cwd, nativeID); err == nil && tr != nil {
		for _, m := range tr.Messages {
			if m.Role == "user" {
				u.TurnCount++
			}
		}
	}
	return u, true
}

func (*Plugin) InspectSubcommandArgs(kind string) ([]string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "plugins":
		return []string{"plugin", "list", "--json"}, true
	case "agents":
		return []string{"inspect"}, true
	case "doctor":
		return []string{"doctor"}, true
	default:
		return nil, false
	}
}
