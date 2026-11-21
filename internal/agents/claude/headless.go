package claude

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

func (*Plugin) Mode() agent.HeadlessExecMode  { return agent.HeadlessModePersistent }
func (*Plugin) SupportsStdioControl() bool    { return true }
func (*Plugin) DefaultPermissionMode() string { return "bypassPermissions" }

func (p *Plugin) BuildTurnSpec(in agent.HeadlessTurnInput) (agent.HeadlessTurnSpec, error) {
	if in.Session == nil {
		return agent.HeadlessTurnSpec{}, fmt.Errorf("nil session")
	}
	if !agentutil.IsUUID(in.Session.NativeID) {
		return agent.HeadlessTurnSpec{}, fmt.Errorf("unsafe claude-code session ID: %q", in.Session.NativeID)
	}
	bin, err := p.ResolveBinary()
	if err != nil {
		return agent.HeadlessTurnSpec{}, fmt.Errorf("failed to locate claude binary: %w", err)
	}

	isExisting := in.IsExisting || in.Session.FirstPrompt != "" || in.Session.LastPrompt != ""
	if !isExisting && in.HomeDir != "" {
		isExisting = p.HasConversationArtifacts(in.HomeDir, in.Session.NativeID)
	}

	var args []string
	if in.Prompt == "" {
		args = []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--permission-prompt-tool", "stdio"}
		switch strings.ToLower(strings.TrimSpace(in.Session.PermissionMode)) {
		case "plan":
			args = append(args, "--permission-mode", "plan", "--allow-dangerously-skip-permissions")
		case "auto":
			args = append(args, "--permission-mode", "auto", "--allow-dangerously-skip-permissions")
		case "acceptedits":
			args = append(args, "--permission-mode", "acceptEdits", "--allow-dangerously-skip-permissions")
		case "dontask":
			args = append(args, "--permission-mode", "dontAsk", "--allow-dangerously-skip-permissions")
		default:
			args = append(args, "--dangerously-skip-permissions")
		}
		args = appendCommonTurnArgs(args, in, isExisting)
	} else {
		args = []string{"-p", in.Prompt, "--output-format", "stream-json", "--verbose"}
		switch strings.ToLower(strings.TrimSpace(in.Session.PermissionMode)) {
		case "plan":
			args = append(args, "--permission-mode", "plan")
		case "auto":
			args = append(args, "--permission-mode", "auto")
		case "acceptedits":
			args = append(args, "--permission-mode", "acceptEdits")
		case "dontask":
			args = append(args, "--permission-mode", "dontAsk")
		default:
			args = append(args, "--dangerously-skip-permissions")
		}
		args = appendCommonTurnArgs(args, in, isExisting)
	}

	return agent.HeadlessTurnSpec{
		Binary: bin,
		Args:   args,
		Cwd:    in.Session.Cwd,
		Mode:   agent.HeadlessModePersistent,
	}, nil
}

func appendCommonTurnArgs(args []string, in agent.HeadlessTurnInput, isExisting bool) []string {
	for _, d := range in.Session.ExtraDirs {
		if cleaned := filepath.Clean(strings.TrimSpace(d)); cleaned != "" && cleaned != "." {
			args = append(args, "--add-dir", cleaned)
		}
	}
	if isExisting {
		args = append(args, "--resume", in.Session.NativeID)
	} else {
		args = append(args, "--session-id", in.Session.NativeID)
	}
	if in.Session.Model != "" && !strings.EqualFold(in.Session.Model, "default") {
		args = append(args, "--model", in.Session.Model)
	} else if isExisting {
		// Pass --model default on resume so Claude does not stay pinned to the prior turn's transcript model.
		args = append(args, "--model", "default")
	}
	if in.Session.Effort != "" && !strings.EqualFold(in.Session.Effort, "default") {
		args = append(args, "--effort", in.Session.Effort)
	}
	if in.Session.IsSupervisor && in.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", in.SystemPrompt)
	}
	return args
}

func (*Plugin) FormatStdinTurn(in agent.PersistentTurnInput) (string, error) {
	payload := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{Type: "user"}
	payload.Message.Role = "user"
	payload.Message.Content = in.Prompt
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (*Plugin) FormatSetModel(model string) (string, bool) {
	b, err := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": fmt.Sprintf("req_model_%d", time.Now().UnixNano()),
		"request": map[string]any{
			"subtype": "set_model",
			"model":   model,
		},
	})
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (*Plugin) InspectSubcommandArgs(kind string) ([]string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "plugins":
		return []string{"plugin", "list", "--json"}, true
	case "agents":
		return []string{"agents", "--json"}, true
	case "doctor":
		return []string{"doctor"}, true
	case "mcp_login":
		return []string{"mcp", "login", "--no-browser"}, true
	default:
		return nil, false
	}
}
