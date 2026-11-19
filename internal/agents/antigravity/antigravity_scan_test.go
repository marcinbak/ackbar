package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
)

func TestLaunchSpecCommandFilterAndPIDResolution(t *testing.T) {
	p := New()
	spec, err := p.BuildLaunchSpec(agent.LaunchInput{TempUUID: "tmp-1", Prompt: "hello", Env: map[string]string{"A": "B"}})
	if err != nil || !spec.PromptInCommand || !strings.Contains(spec.CommandLine, `-i "$ACKBAR_INIT_PROMPT"`) || spec.Env["ACKBAR_INIT_PROMPT"] != "hello" {
		t.Fatalf("unexpected launch spec: %+v err=%v", spec, err)
	}
	resSpec, err := p.BuildLaunchSpec(agent.LaunchInput{Resume: true, NativeID: "11111111-2222-3333-4444-555555555555"})
	if err != nil || !strings.Contains(resSpec.CommandLine, "--conversation 11111111-2222-3333-4444-555555555555") {
		t.Fatalf("unexpected resume launch spec: %+v err=%v", resSpec, err)
	}
	if !p.IsIgnoredCommand("agy models --json") || p.IsIgnoredCommand("agy --conversation 11111111-2222-3333-4444-555555555555") {
		t.Fatal("unexpected IsIgnoredCommand classification")
	}
	if !p.MatchProcessCommand("agy", "/usr/local/bin/agy") || p.MatchProcessCommand("ackbar", "ackbar-hook") {
		t.Fatal("unexpected MatchProcessCommand result")
	}
	cid := p.ResolveSessionForPID(context.Background(), t.TempDir(), 0, []string{"agy", "--conversation", "11111111-2222-3333-4444-555555555555"})
	if cid != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("ResolveSessionForPID fallback = %q", cid)
	}
}

func TestSubagentsScanDiskAndBlockInspection(t *testing.T) {
	p := New()
	home := t.TempDir()
	projDir := filepath.Join(home, "workproj")
	_ = os.MkdirAll(projDir, 0o755)

	parentID := "11111111-2222-3333-4444-555555555555"
	subID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	parentBrain := filepath.Join(home, ".gemini", "antigravity", "brain", parentID)
	_ = os.MkdirAll(filepath.Join(parentBrain, ".system_generated", "logs"), 0o755)
	_ = os.MkdirAll(filepath.Join(parentBrain, ".system_generated", "subagents"), 0o755)
	_ = os.WriteFile(filepath.Join(parentBrain, ".system_generated", "logs", "transcript.jsonl"),
		[]byte(`{"step_index":0,"type":"USER_INPUT","source":"USER_EXPLICIT","content":"Main task","created_at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`+"\n"+
			projDir+` -> corpus`+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(parentBrain, ".system_generated", "subagents", subID+".json"),
		[]byte(`{"conversationId":"`+subID+`","subagentDescriptor":{"typeName":"reviewer","role":"Clean Reviewer"},"state":"RUNNING"}`), 0o644)

	subLogs := filepath.Join(home, ".gemini", "antigravity", "brain", subID, ".system_generated", "logs")
	_ = os.MkdirAll(subLogs, 0o755)
	_ = os.WriteFile(filepath.Join(subLogs, "transcript.jsonl"),
		[]byte(`{"step_index":1,"type":"PLANNER_RESPONSE","tool_calls":[{"name":"ask_permission","args":{"command":"npm test"}}]}`+"\n"), 0o644)

	if !p.IsSubagentSession(home, subID, nil) {
		t.Fatal("expected subagent detected via parent registry")
	}
	pid, role, _ := p.ResolveSubagentParent(home, subID)
	if pid != parentID || role != "Clean Reviewer" {
		t.Fatalf("ResolveSubagentParent = (%q, %q)", pid, role)
	}
	blk, _, ok := p.InspectSubagentBlock(home, subID, time.Time{})
	if !ok || blk == nil || blk.Kind != agent.BlockPermission || blk.Reason != "npm test" {
		t.Fatalf("InspectSubagentBlock = %+v ok=%v", blk, ok)
	}

	tempID := "99999999-9999-9999-9999-999999999999"
	res := p.ScanDisk(context.Background(), agent.DiskScanInput{
		HomeDir:  home,
		HostName: "local",
		ExistingSessions: []*agent.Session{
			{ID: "antigravity:local:" + tempID, Agent: "antigravity", Host: "local", NativeID: tempID, Cwd: projDir, Managed: true, State: agent.StateUnknown, StartedAt: time.Now().Add(-time.Minute)},
			{ID: "antigravity:local:" + subID, Agent: "antigravity", Host: "local", NativeID: subID, State: agent.StateIdle},
		},
	})
	if len(res.AdoptUpdates) != 1 || res.AdoptUpdates[0].NewNativeID != parentID {
		t.Fatalf("expected AdoptUpdates for %s, got %+v", parentID, res.AdoptUpdates)
	}
	if len(res.PruneSessionIDs) != 1 || res.PruneSessionIDs[0] != "antigravity:local:"+subID {
		t.Fatalf("expected PruneSessionIDs for subagent, got %+v", res.PruneSessionIDs)
	}

	lines := []string{
		"┌──────────────────────────────────────────┐",
		"│ Run this command?                        │",
		"│ Command: go test ./...                   │",
		"│ ❯ 1. Yes, run command                    │",
		"│   2. No, and tell Antigravity CLI what   │",
		"└──────────────────────────────────────────┘",
	}
	q, r, opts, matched := detectPermissionPrompt(lines, false)
	if !matched || q != "Run this command?" || r != "Command: go test ./..." || len(opts) < 2 {
		t.Fatalf("detectPermissionPrompt = (%q, %q, %v, %v)", q, r, opts, matched)
	}
}
