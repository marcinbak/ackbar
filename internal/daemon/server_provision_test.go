package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestServer_AgentsStatusAndProvisionHandlers(t *testing.T) {
	tempHome := t.TempDir()
	server := &Server{homeDir: tempHome}

	// 1. GET /v1/agents/status
	reqStatus := httptest.NewRequest("GET", "/v1/agents/status", nil)
	recStatus := httptest.NewRecorder()
	server.handleAgentsStatus(recStatus, reqStatus)

	if recStatus.Code != http.StatusOK {
		t.Errorf("Expected 200 OK from GET /v1/agents/status, got %d", recStatus.Code)
	}

	var statusResp struct {
		Status  string        `json:"status"`
		Version string        `json:"version"`
		Agents  []AgentStatus `json:"agents"`
	}
	if err := json.Unmarshal(recStatus.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("Failed to parse status response: %v", err)
	}
	if statusResp.Status != "success" {
		t.Errorf("Expected status 'success', got %s", statusResp.Status)
	}
	if len(statusResp.Agents) == 0 {
		t.Errorf("Expected supported agents in response, got 0")
	}

	// 2. Method not allowed on GET
	reqBad := httptest.NewRequest("POST", "/v1/agents/status", nil)
	recBad := httptest.NewRecorder()
	server.handleAgentsStatus(recBad, reqBad)
	if recBad.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405 Method Not Allowed, got %d", recBad.Code)
	}

	// 3. POST /v1/agents/provision
	bodyProv, _ := json.Marshal(map[string]any{
		"agents": []string{"claude-code"},
	})
	reqProv := httptest.NewRequest("POST", "/v1/agents/provision", bytes.NewReader(bodyProv))
	recProv := httptest.NewRecorder()
	server.handleAgentsProvision(recProv, reqProv)

	if recProv.Code != http.StatusOK {
		t.Errorf("Expected 200 OK from POST /v1/agents/provision, got %d: %s", recProv.Code, recProv.Body.String())
	}

	var provResp struct {
		Status  string        `json:"status"`
		Message string        `json:"message"`
		Agents  []AgentStatus `json:"agents"`
	}
	if err := json.Unmarshal(recProv.Body.Bytes(), &provResp); err != nil {
		t.Fatalf("Failed to parse provision response: %v", err)
	}
	if provResp.Status != "success" {
		t.Errorf("Expected status 'success', got %s", provResp.Status)
	}

	claudeCfgPath := tempHome + "/.claude.json"
	if _, err := os.Stat(claudeCfgPath); err != nil {
		t.Errorf("Expected provisioned config at %s, but file not found: %v", claudeCfgPath, err)
	}
}
