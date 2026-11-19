package antigravity

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"ackbar/internal/agent"
)

func (*Plugin) Mode() agent.HeadlessExecMode         { return agent.HeadlessModePersistent }
func (*Plugin) SupportsStdioControl() bool           { return false }
func (*Plugin) DefaultPermissionMode() string        { return "bypassPermissions" }
func (*Plugin) FormatSetModel(string) (string, bool) { return "", false }

func (p *Plugin) BuildTurnSpec(in agent.HeadlessTurnInput) (agent.HeadlessTurnSpec, error) {
	if in.Session == nil {
		return agent.HeadlessTurnSpec{}, fmt.Errorf("nil session")
	}
	bin, err := p.ResolveBinary()
	if err != nil || bin == "" {
		bin = "agy"
	}

	var args []string
	if in.Prompt != "" {
		effectivePrompt := in.Prompt
		if in.Session.IsSupervisor && !in.IsExisting && in.SystemPrompt != "" {
			effectivePrompt = "[System Instruction: " + in.SystemPrompt + "]\n\n" + in.Prompt
		}
		args = []string{"-p", effectivePrompt, "--output-format", "stream-json"}
	} else {
		// Antigravity's -p requires an immediate value and consumes --input-format if bare.
		args = []string{"--input-format", "stream-json", "--output-format", "stream-json"}
	}

	switch strings.ToLower(strings.TrimSpace(in.Session.PermissionMode)) {
	case "plan":
		args = append(args, "--mode", "plan")
	case "auto":
		args = append(args, "--mode", "auto")
	case "acceptedits":
		args = append(args, "--mode", "accept-edits")
	default:
		args = append(args, "--dangerously-skip-permissions")
	}

	for _, d := range in.Session.ExtraDirs {
		if cleaned := filepath.Clean(strings.TrimSpace(d)); cleaned != "" && cleaned != "." {
			args = append(args, "--add-dir", cleaned)
		}
	}
	if in.IsExisting && in.Session.NativeID != "" {
		args = append(args, "--conversation", in.Session.NativeID)
	}
	if in.Session.Model != "" && !strings.EqualFold(in.Session.Model, "default") {
		args = append(args, "--model", in.Session.Model)
	}

	return agent.HeadlessTurnSpec{
		Binary: bin,
		Args:   args,
		Cwd:    in.Session.Cwd,
		Mode:   agent.HeadlessModePersistent,
	}, nil
}

func (*Plugin) FormatStdinTurn(in agent.PersistentTurnInput) (string, error) {
	effectivePrompt := in.Prompt
	if in.IsSupervisor && in.IsFirstTurn && in.SystemPrompt != "" {
		effectivePrompt = "[System Instruction: " + in.SystemPrompt + "]\n\n" + in.Prompt
	}
	payload := struct {
		Event   string `json:"event"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{
		Event: "user",
	}
	payload.Message.Role = "user"
	payload.Message.Content = effectivePrompt
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (*Plugin) ComputeUsage(_, _, _ string) (agent.TranscriptUsage, bool) {
	return agent.TranscriptUsage{}, false
}

func (*Plugin) InspectSubcommandArgs(kind string) ([]string, bool) {
	switch kind {
	case "plugins":
		return []string{"plugin", "list", "--json"}, true
	case "agents":
		return []string{"agents"}, true
	default:
		return nil, false
	}
}
