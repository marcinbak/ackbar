package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"ackbar/internal/tmux"
)

type MockProvider struct{}

func (m *MockProvider) Agent() string {
	return "mock-agent"
}

func (m *MockProvider) IsInstalled() bool {
	return true
}

func (m *MockProvider) CheckHookConfig() (bool, string, error) {
	return true, "mock-setup-cmd", nil
}

func (m *MockProvider) ParseHook(eventName string, payload []byte) (*Event, error) {
	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}

	sessionID, _ := data["session_id"].(string)
	cwd, _ := data["cwd"].(string)
	activity, _ := data["activity"].(string)

	return &Event{
		Agent:       "mock-agent",
		NativeID:    sessionID,
		Cwd:         cwd,
		EventName:   eventName,
		State:       StateWorking,
		Activity:    activity,
		LastEventAt: time.Now(),
	}, nil
}

func TestServer_Integration(t *testing.T) {
	// Create temporary db file
	dbFile := "./test_integration.db"
	defer os.Remove(dbFile)

	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.RegisterProvider(&MockProvider{})

	// 1. Test POST Hook Ingest
	payload := `{"session_id": "session-123", "cwd": "/path/to/project", "activity": "compiling"}`
	req := httptest.NewRequest("POST", "/v1/hooks/mock-agent?event=SessionStart", bytes.NewBufferString(payload))
	req.Header.Set("X-Ackbar-Host", "test-host")

	w := httptest.NewRecorder()
	server.Mux().ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var status map[string]string
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("Failed to parse response body: %v", err)
	}
	if status["status"] != "enqueued" {
		t.Errorf("Expected status enqueued, got %s", status["status"])
	}

	// Wait briefly for asynchronous background ingestion
	time.Sleep(300 * time.Millisecond)

	// 2. Test GET Sessions
	reqGet := httptest.NewRequest("GET", "/v1/sessions", nil)
	wGet := httptest.NewRecorder()
	server.Mux().ServeHTTP(wGet, reqGet)

	respGet := wGet.Result()
	if respGet.StatusCode != http.StatusOK {
		t.Errorf("Expected GET status 200, got %d", respGet.StatusCode)
	}

	var sessions []*Session
	if err := json.NewDecoder(respGet.Body).Decode(&sessions); err != nil {
		t.Fatalf("Failed to decode sessions: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session in database, got %d", len(sessions))
	}

	s := sessions[0]
	if s.ID != "mock-agent:test-host:session-123" {
		t.Errorf("Expected ID 'mock-agent:test-host:session-123', got '%s'", s.ID)
	}
	if s.Cwd != "/path/to/project" {
		t.Errorf("Expected Cwd '/path/to/project', got '%s'", s.Cwd)
	}
	if s.Activity != "compiling" {
		t.Errorf("Expected Activity 'compiling', got '%s'", s.Activity)
	}

	// 3. Test POST Archive Action
	reqArchive := httptest.NewRequest("POST", "/v1/sessions/mock-agent:test-host:session-123/archive", nil)
	wArchive := httptest.NewRecorder()
	server.Mux().ServeHTTP(wArchive, reqArchive)

	respArchive := wArchive.Result()
	if respArchive.StatusCode != http.StatusOK {
		t.Errorf("Expected Archive status 200, got %d", respArchive.StatusCode)
	}

	// Verify in DB that it is archived
	sArchived, err := db.GetSession("mock-agent:test-host:session-123")
	if err != nil {
		t.Fatalf("Failed to fetch session: %v", err)
	}
	if !sArchived.Archived {
		t.Errorf("Expected session to be archived")
	}

	// 4. Test POST Unarchive Action
	reqUnarchive := httptest.NewRequest("POST", "/v1/sessions/mock-agent:test-host:session-123/unarchive", nil)
	wUnarchive := httptest.NewRecorder()
	server.Mux().ServeHTTP(wUnarchive, reqUnarchive)

	respUnarchive := wUnarchive.Result()
	if respUnarchive.StatusCode != http.StatusOK {
		t.Errorf("Expected Unarchive status 200, got %d", respUnarchive.StatusCode)
	}

	// Verify in DB that it is unarchived
	sUnarchived, err := db.GetSession("mock-agent:test-host:session-123")
	if err != nil {
		t.Fatalf("Failed to fetch session: %v", err)
	}
	if sUnarchived.Archived {
		t.Errorf("Expected session to be unarchived")
	}

	// 5. Test POST Spawn Action (requires tmux)
	if _, err := exec.LookPath("tmux"); err == nil {
		spawnPayload := `{"agent": "mock-agent", "cwd": "` + os.TempDir() + `"}`
		reqSpawn := httptest.NewRequest("POST", "/v1/sessions/spawn", bytes.NewBufferString(spawnPayload))
		wSpawn := httptest.NewRecorder()
		server.Mux().ServeHTTP(wSpawn, reqSpawn)

		respSpawn := wSpawn.Result()
		if respSpawn.StatusCode != http.StatusOK {
			if strings.Contains(wSpawn.Body.String(), "Operation not permitted") {
				t.Skip("tmux socket restricted in sandbox execution")
			} else {
				t.Errorf("Expected Spawn status 200, got %d (body: %s)", respSpawn.StatusCode, wSpawn.Body.String())
			}
		}

		var spawnResp map[string]string
		_ = json.NewDecoder(respSpawn.Body).Decode(&spawnResp)
		tempUUID := spawnResp["session_id"]
		if tempUUID == "" {
			t.Errorf("Expected session_id in spawn response")
		}

		// Verify session was added to DB
		dbID := "mock-agent:local:" + tempUUID
		sSpawning, err := db.GetSession(dbID)
		if err != nil || sSpawning == nil {
			t.Fatalf("Spawning session not found in database: %v", err)
		}

		if !sSpawning.Managed || sSpawning.TmuxName == "" {
			t.Errorf("Expected spawning session to be managed and have a tmux name")
		}

		// Cleanup the tmux session spawned
		_ = tmux.Kill(context.Background(), sSpawning.TmuxName)
	}
}
