package provider

import (
	"os"
	"path/filepath"
	"testing"

	"ackbar/internal/agent"
	"ackbar/internal/agents/antigravity"
)

func TestAntigravityProvider_PluginParity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	leg := NewAntigravityProvider()
	plg := antigravity.New()

	if leg.Agent() != plg.Key() || leg.DisplayName() != plg.DisplayName() || leg.BrandColor() != plg.BrandColor() || leg.IconSVG() != plg.IconSVG() {
		t.Fatal("identity/branding mismatch between legacy AntigravityProvider and plugin")
	}

	okL, _, _ := leg.CheckHookConfig()
	okP, _, _ := plg.CheckHookConfig(home)
	if okL != okP || okL {
		t.Fatalf("expected both unconfigured before hooks written: legacy=%v plugin=%v", okL, okP)
	}
	if err := plg.InstallHooks(home); err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}
	okL, _, _ = leg.CheckHookConfig()
	okP, _, _ = plg.CheckHookConfig(home)
	if !okL || !okP {
		t.Fatalf("expected both configured after InstallHooks: legacy=%v plugin=%v", okL, okP)
	}

	payload := []byte(`{"conversationId":"11111111-2222-3333-4444-555555555555","workspacePaths":["/tmp/p"],"toolCall":{"name":"ask_question","args":{"question":"Choice?","options":["One","Two"]}}}`)
	evL, errL := leg.ParseHook("PreToolUse", payload)
	evP, errP := plg.ParseHook("PreToolUse", payload)
	if errL != nil || errP != nil || evL.State != evP.State || evL.Blocked.Kind != agent.BlockKind(evP.Blocked.Kind) || evL.Blocked.Question != evP.Blocked.Question {
		t.Fatalf("ParseHook mismatch: leg=%+v (%v) vs plg=%+v (%v)", evL, errL, evP, errP)
	}

	convID := "22222222-2222-3333-4444-555555555555"
	annoDir := filepath.Join(home, ".gemini", "antigravity", "annotations")
	_ = os.MkdirAll(annoDir, 0o755)
	_ = os.WriteFile(filepath.Join(annoDir, convID+".pbtxt"), []byte(`title: "Parity Meta"`), 0o644)
	if leg.ResolveSessionTitle("", convID) != plg.ResolveSessionTitle(home, "", convID) {
		t.Fatal("ResolveSessionTitle mismatch")
	}
	if err := plg.CleanSessionFiles(home, "", convID); err != nil {
		t.Fatalf("CleanSessionFiles failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(annoDir, convID+".pbtxt")); !os.IsNotExist(err) {
		t.Fatal("expected annotation file deleted by CleanSessionFiles")
	}
}
