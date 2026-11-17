package agentutil

import (
	"testing"

	"ackbar/internal/agent"
)

func TestToolDisplayAndQuestionExtraction(t *testing.T) {
	if got := FormatToolDisplay("bash", map[string]any{"command": "go test ./..."}); got != "bash: go test ./..." {
		t.Errorf("FormatToolDisplay = %q", got)
	}

	name, detail, isMCP := FormatMCPToolDisplay("mcp__github__list_issues", map[string]any{"repo": "ackbar"})
	if !isMCP || name != "mcp/github/list_issues" || detail == "" {
		t.Errorf("FormatMCPToolDisplay = (%q, %q, %v)", name, detail, isMCP)
	}

	args := map[string]any{
		"questions": []any{
			map[string]any{
				"question":    "Pick mode?",
				"header":      "Mode",
				"options":     []any{map[string]any{"label": "Fast"}, "Safe"},
				"multiSelect": true,
			},
		},
	}
	qs := ExtractQuestions(args)
	if len(qs) != 1 || qs[0].Question != "Pick mode?" || !qs[0].MultiSelect || len(qs[0].Options) != 2 {
		t.Fatalf("ExtractQuestions = %+v", qs)
	}
	q, opts := ExtractQuestionAndOptions(args)
	if q != "Pick mode?" || len(opts) != 2 {
		t.Fatalf("ExtractQuestionAndOptions = (%q, %v)", q, opts)
	}

	blk := ParseInteractiveToolBlock("ExitPlanMode", map[string]any{"plan": "# Plan"})
	if blk == nil || blk.Kind != agent.BlockApproval || blk.Reason != "# Plan" {
		t.Fatalf("ParseInteractiveToolBlock ExitPlanMode = %+v", blk)
	}
	blkQ := ParseInteractiveToolBlock("AskUserQuestion", args)
	if blkQ == nil || blkQ.Kind != agent.BlockQuestion || blkQ.Question != "Pick mode?" {
		t.Fatalf("ParseInteractiveToolBlock AskUserQuestion = %+v", blkQ)
	}

	authMCP := DetectToolResultAuthBlock("claude-code", "mcp__github__create_issue", nil, "401 Unauthorized", true)
	if authMCP == nil || authMCP.Kind != agent.BlockAuth || authMCP.AuthTarget != "mcp:github" {
		t.Fatalf("DetectToolResultAuthBlock mcp = %+v", authMCP)
	}
	if authRead := DetectToolResultAuthBlock("claude-code", "read_file", nil, "401 Unauthorized", true); authRead != nil {
		t.Fatalf("expected read_file to be ignored, got %+v", authRead)
	}
	authPlugin := DetectToolResultAuthBlock("antigravity", "bash", nil, "Command exited with code 1.\ngdocs auth login required", false)
	if authPlugin == nil || authPlugin.AuthTarget != "plugin:gdocs" {
		t.Fatalf("DetectToolResultAuthBlock plugin = %+v", authPlugin)
	}
}
