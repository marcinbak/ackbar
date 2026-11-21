package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ackbar/internal/agent"
	"ackbar/internal/agents"
)

func TestClaudeDualMode_HooksAliasesRegistryAndWorkerEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv, db := setupTestServer(t)

	hk, ok := srv.PluginRegistry().AsHooks("claude")
	if !ok || string(hk.HookAckPayload("PreToolUse")) != "{}" {
		t.Fatalf("HookAckPayload mismatch via claude alias: ok=%v", ok)
	}
	uuid := "aaaaaaaa-1111-4222-8333-bbbbbbbbbbbb"
	sessID := "claude-code:" + srv.HostName() + ":" + uuid
	_ = db.SaveSession(&Session{
		ID: sessID, Agent: "claude-code", Host: srv.HostName(), NativeID: uuid,
		Cwd: home, Managed: true, State: StateWorking, StartedAt: time.Now(),
	})
	getRunning := func() int {
		rec := httptest.NewRecorder()
		srv.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/subagents?id="+sessID, nil))
		var out struct {
			RunningCount int `json:"running_count"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.RunningCount
	}
	postHook := func(path, evt, body string) {
		rec := httptest.NewRecorder()
		srv.handleHook(rec, httptest.NewRequest(http.MethodPost, path+"?event="+evt, strings.NewReader(body)))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "enqueued") {
			t.Fatalf("POST %s = %d %q", path, rec.Code, rec.Body.String())
		}
	}

	postHook("/v1/hooks/claude-code", "SubagentStart", `{"session_id":"`+uuid+`","hook_event_name":"SubagentStart","agent_id":"sub-1","agent_type":"coder","cwd":"`+home+`"}`)
	for d := time.Now().Add(2 * time.Second); time.Now().Before(d) && getRunning() != 1; {
		time.Sleep(10 * time.Millisecond)
	}
	if rc := getRunning(); rc != 1 {
		t.Fatalf("expected running_count == 1 after SubagentStart, got %d", rc)
	}

	postHook("/v1/hooks/claude", "Stop", `{"session_id":"`+uuid+`","hook_event_name":"Stop","cwd":"`+home+`"}`)
	for d := time.Now().Add(2 * time.Second); time.Now().Before(d); {
		if s, err := db.GetSession(sessID); err == nil && s != nil && s.State == StateIdle {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rc := getRunning(); rc != 1 {
		t.Fatalf("expected Stop to preserve running_count == 1, got %d", rc)
	}

	postHook("/v1/hooks/claude-code", "SubagentStop", `{"session_id":"`+uuid+`","hook_event_name":"SubagentStop","agent_id":"sub-1","agent_type":"coder","cwd":"`+home+`"}`)
	for d := time.Now().Add(2 * time.Second); time.Now().Before(d) && getRunning() != 0; {
		time.Sleep(10 * time.Millisecond)
	}
	if rc := getRunning(); rc != 0 {
		t.Fatalf("expected running_count == 0 after SubagentStop, got %d", rc)
	}

	for _, path := range []string{"/v1/hooks/claude-code", "/v1/hooks/claude"} {
		postHook(path, "PreToolUse", `{"session_id":"`+uuid+`","hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{"plan":"Plan A"},"cwd":"`+home+`"}`)
	}
	var sess *Session
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if s, err := db.GetSession(sessID); err == nil && s != nil && s.State == StateBlocked {
			sess = s
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sess == nil || sess.State != StateBlocked || sess.Blocked == nil || sess.Blocked.Kind != BlockApproval {
		t.Fatalf("expected blocked ExitPlanMode session from plugin hook, got %+v", sess)
	}

	// InstallHooks in plugin mode cleans legacy .claude.json hooks & installs HTTP hooks.
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"hooks":{"Stop":[]}}`), 0o600)
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"permissions":{"allow":["Write(~/.memories/log/**)"]}}`), 0o644)
	prov, ok := srv.PluginRegistry().AsProvisioner("claude-code")
	if !ok {
		t.Fatal("missing claude-code Provisioner")
	}
	if err := prov.InstallHooks(home); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	rawSettings, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(rawSettings), "Edit(~/.memories/log/**)") || !strings.Contains(string(rawSettings), "SubagentStart") {
		t.Fatalf("InstallHooks failed to update settings.json: %s", rawSettings)
	}

	// ACKBAR_LEGACY_AGENTS disables claude plugin across registry and propagates in headless worker env.
	for _, legacyVal := range []string{"claude-code", "claude"} {
		t.Setenv(agent.LegacyAgentsEnvVar, legacyVal)
		if !agent.IsLegacyAgent("claude-code", "claude") {
			t.Fatalf("expected IsLegacyAgent true for %s", legacyVal)
		}
		reg, err := agents.NewDefaultRegistry()
		if err != nil {
			t.Fatalf("NewDefaultRegistry: %v", err)
		}
		if _, has := reg.Get("claude-code"); has {
			t.Fatalf("expected claude-code disabled when %s=%s", agent.LegacyAgentsEnvVar, legacyVal)
		}
		env := buildWorkerEnvWithRegistry(reg, &Session{Agent: "claude-code", Cwd: home}, db, home)
		foundEnv := false
		for _, kv := range env {
			if kv == agent.LegacyAgentsEnvVar+"="+legacyVal {
				foundEnv = true
			}
		}
		if !foundEnv {
			t.Fatalf("expected %s=%s propagated to worker env", agent.LegacyAgentsEnvVar, legacyVal)
		}
	}
}
