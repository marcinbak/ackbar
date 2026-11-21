package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"ackbar/internal/agent"
)

func TestClaudeDualMode_PluginAndLegacy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		legacyEnv  string
		wantPlugin bool
	}{
		{name: "plugin", legacyEnv: "", wantPlugin: true},
		{name: "legacy", legacyEnv: "claude-code", wantPlugin: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv(agent.LegacyAgentsEnvVar, tc.legacyEnv)

			srv, db := setupTestServer(t)
			srv.homeDir = home
			_, hasPlugin := srv.PluginRegistry().Get("claude-code")
			if hasPlugin != tc.wantPlugin {
				t.Fatalf("PluginRegistry.Get(claude-code) = %v, want %v", hasPlugin, tc.wantPlugin)
			}

			proj := filepath.Join(home, "proj")
			_ = os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
			_ = os.WriteFile(filepath.Join(proj, ".claude", "plan.md"), []byte("# Plan"), 0o644)

			nativeID := "11111111-2222-4333-8444-555555555555"
			projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(proj))
			_ = os.MkdirAll(projDir, 0o755)
			tPath := filepath.Join(projDir, nativeID+".jsonl")
			now := time.Now().UTC().Format(time.RFC3339)
			_ = os.WriteFile(tPath, []byte(strings.Join([]string{
				`{"type":"user","sessionId":"` + nativeID + `","timestamp":"` + now + `","message":{"role":"user","content":"Turn 1"}}`,
				`{"type":"assistant","sessionId":"` + nativeID + `","timestamp":"` + now + `","message":{"model":"claude-sonnet-4-6[1m]","usage":{"input_tokens":500,"output_tokens":50,"cache_creation_input_tokens":20,"cache_read_input_tokens":100},"output_tokens_details":{"thinking_tokens":12},"content":[{"type":"text","text":"Done with Turn 1 and ready for caching"}]}}`,
			}, "\n")+"\n"), 0o644)

			// Incremental transcript caching: cache >128B header on first read, coalesce appended Turn 2 on second read.
			tr1, err := srv.ExtractTranscript("claude-code", nativeID, proj)
			if err != nil || len(tr1.Messages) != 2 {
				t.Fatalf("ExtractTranscript 1 = %+v err=%v", tr1, err)
			}
			fApp, err := os.OpenFile(tPath, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = fApp.WriteString(strings.Join([]string{
				`{"type":"attachment","path":"note.txt","sessionId":"` + nativeID + `"}`,
				`{"type":"user","sessionId":"` + nativeID + `","timestamp":"` + now + `","message":{"role":"user","content":"Turn 2"}}`,
				`{"type":"assistant","sessionId":"` + nativeID + `","timestamp":"` + now + `","message":{"model":"claude-sonnet-4-6","content":[{"type":"text","text":"Done Turn 2"}]}}`,
				`{"type":"last-prompt","lastPrompt":"Turn 2"}`,
			}, "\n") + "\n")
			_ = fApp.Close()
			tr2, err := srv.ExtractTranscript("claude-code", nativeID, proj)
			if err != nil || len(tr2.Messages) != 4 || tr2.Messages[3].Content != "Done Turn 2" {
				t.Fatalf("ExtractTranscript 2 (incremental) = %+v err=%v", tr2, err)
			}

			// 1. Spawn headless session & check persistent/live set_model formatting.
			spawnBody, _ := json.Marshal(map[string]any{"agent": "claude-code", "cwd": proj, "engine_type": EngineHeadless, "permission_mode": "plan", "effort": "high"})
			wSpawn := httptest.NewRecorder()
			srv.handleSpawn(wSpawn, httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(spawnBody)))
			if wSpawn.Code != http.StatusOK {
				t.Fatalf("spawn failed: %d %s", wSpawn.Code, wSpawn.Body.String())
			}
			var spawnResp map[string]any
			_ = json.Unmarshal(wSpawn.Body.Bytes(), &spawnResp)
			sessID := spawnResp["session_id"].(string)
			sess, _ := db.GetSession(sessID)
			inspectID := "claude-code:" + srv.HostName() + ":" + nativeID
			sess.ID, sess.NativeID = inspectID, nativeID
			_ = db.SaveSession(sess)

			// 2. Inspect context/cost/plugins/agents/doctor, rewind, and suggestions.
			for _, kind := range []string{"context", "cost", "doctor", "plugins", "agents"} {
				rec := httptest.NewRecorder()
				srv.handleSessionInspect(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+inspectID+"/inspect?kind="+kind, nil), inspectID)
				if rec.Code != http.StatusOK {
					t.Fatalf("inspect %s = %d %s", kind, rec.Code, rec.Body.String())
				}
				if kind == "context" && (!strings.Contains(rec.Body.String(), `"thinking_tokens":12`) || !strings.Contains(rec.Body.String(), `"cache_write_tokens":20`)) {
					t.Fatalf("expected claude token details in context inspect, got %s", rec.Body.String())
				}
			}
			wRew := httptest.NewRecorder()
			srv.handleSessionRewind(wRew, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+inspectID+"/rewind", nil), inspectID)
			if wRew.Code != http.StatusOK || !strings.Contains(wRew.Body.String(), `"total_turns":2`) {
				t.Fatalf("rewind GET = %d %s", wRew.Code, wRew.Body.String())
			}
			wRewPost := httptest.NewRecorder()
			srv.handleSessionRewind(wRewPost, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+inspectID+"/rewind", strings.NewReader(`{"before_turn":2}`)), inspectID)
			if wRewPost.Code != http.StatusOK || !strings.Contains(wRewPost.Body.String(), `"cloned_transcript"`) {
				t.Fatalf("rewind POST = %d %s", wRewPost.Code, wRewPost.Body.String())
			}
			var rewResp struct {
				Session *Session `json:"session"`
			}
			_ = json.Unmarshal(wRewPost.Body.Bytes(), &rewResp)
			if rewResp.Session == nil || rewResp.Session.NativeID == "" || rewResp.Session.NativeID == nativeID {
				t.Fatalf("unexpected cloned session: %+v", rewResp.Session)
			}
			clonedPath := filepath.Join(projDir, rewResp.Session.NativeID+".jsonl")
			fi, err := os.Stat(clonedPath)
			if err != nil || fi.Mode().Perm() != 0o600 {
				t.Fatalf("cloned file stat=%v err=%v", fi, err)
			}
			clonedRaw, _ := os.ReadFile(clonedPath)
			clonedStr := string(clonedRaw)
			if !strings.Contains(clonedStr, rewResp.Session.NativeID) || strings.Contains(clonedStr, "Turn 2") ||
				strings.Contains(clonedStr, "note.txt") || strings.Contains(clonedStr, "last-prompt") {
				t.Fatalf("cloned transcript mismatch:\n%s", clonedStr)
			}

			// c2 & c3 checks: facet-first mcp_login and NativeIDEagerUUID restart/resume.
			_ = os.WriteFile(filepath.Join(proj, ".mcp.json"), []byte(`{"mcpServers":{"corp":{"type":"http","url":"https://mcp.example.com"}}}`), 0o600)
			mcps, _ := discoverConfiguredMCPServersWithRegistry(srv.PluginRegistry(), sess, db, home)
			if len(mcps) == 0 || !mcps[0].AuthSupported {
				t.Fatalf("expected AuthSupported=true via registry/fallback, got %+v", mcps)
			}
			freshUUID := "99999999-8888-4777-8666-555555555555"
			if _, existing := srv.getRestartOrResumeCmd(&Session{Agent: "claude-code", NativeID: freshUUID, Cwd: proj, FirstPrompt: "hi"}, home); existing {
				t.Fatal("expected NativeIDEagerUUID without disk file to have isExisting=false")
			}
			var hasEffort bool
			for _, c := range srv.collectCommands(proj, "claude-code") {
				if c.Name == "effort" {
					hasEffort = true
				}
			}
			if !hasEffort || len(srv.collectModelsForAgent("claude-code")) == 0 {
				t.Fatal("expected claude effort command and model catalog")
			}

			// 3. Provisioning & workspace documents.
			_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"hooks":{"Stop":[]}}`), 0o600)
			statuses, err := ProvisionAgentsWithRegistry(home, []string{"claude-code"}, srv.PluginRegistry())
			if err != nil {
				t.Fatalf("ProvisionAgentsWithRegistry: %v", err)
			}
			found := false
			for _, st := range statuses {
				if st.Key == "claude-code" && st.MCPInstalled && st.SkillInstalled && st.RulesInstalled {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected provisioned claude-code status, got %+v", statuses)
			}

			wDocs := httptest.NewRecorder()
			srv.handleDocuments(wDocs, httptest.NewRequest(http.MethodGet, "/v1/documents?session_id="+inspectID, nil))
			if wDocs.Code != http.StatusOK || !strings.Contains(wDocs.Body.String(), "plan.md") {
				t.Fatalf("workspace docs = %d %s", wDocs.Code, wDocs.Body.String())
			}

			// 4. Preserve custom/user prompt name during tmux refresh (eager UUID never adopts AITitle).
			_ = os.WriteFile(tPath, []byte(`{"type":"assistant","sessionId":"`+nativeID+`","aiTitle":"Auto Generated"}`+"\n"), 0o644)
			refreshSess := &Session{ID: inspectID, Agent: "claude-code", Host: srv.HostName(), NativeID: nativeID, Cwd: proj, Name: "Conv 1"}
			st := &ScanState{hostName: srv.HostName(), knownIDs: map[string]*Session{}, knownByNativeID: map[string]*Session{}, knownByTmux: map[string]*Session{}, knownByPID: map[int]*Session{}}
			srv.adoptExistingTmuxSession(t.Context(), st, refreshSess, "claude-code", "ackbar-claude-code-"+nativeID, proj, 0, 0)
			if refreshSess.Name != "Conv 1" {
				t.Fatalf("expected eager-UUID claude session to keep 'Conv 1', got %q", refreshSess.Name)
			}

			// 5. Workspace model resolution precedence & FIFO non-blocking.
			_ = os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(`{"model":"claude-opus-4-6[1m]"}`), 0o644)
			fifoPath := filepath.Join(proj, ".claude", "settings.local.json")
			if err := syscall.Mkfifo(fifoPath, 0o600); err == nil {
				got := srv.resolveSettingsModel(&Session{Agent: "claude-code", Cwd: proj})
				if got != "claude-opus-4-6" {
					t.Fatalf("resolveSettingsModel FIFO fallback = %q, want claude-opus-4-6", got)
				}
			}
		})
	}
}
