package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.SetToken("secret-token-123")

	mux := server.Mux()

	// 1. Unauthenticated request to /v1/sessions should return 401
	req1 := httptest.NewRequest("GET", "/v1/sessions", nil)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized, got %d", rec1.Code)
	}

	// 2. Healthz should bypass authentication
	req2 := httptest.NewRequest("GET", "/healthz", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /healthz, got %d", rec2.Code)
	}

	// 3. Version should bypass authentication
	req3 := httptest.NewRequest("GET", "/v1/version", nil)
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /v1/version, got %d", rec3.Code)
	}

	// 4. Request with Authorization: Bearer secret-token-123 should succeed
	req4 := httptest.NewRequest("GET", "/v1/sessions", nil)
	req4.Header.Set("Authorization", "Bearer secret-token-123")
	rec4 := httptest.NewRecorder()
	mux.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with Bearer token, got %d", rec4.Code)
	}

	// 5. Request with X-Ackbar-Token: secret-token-123 should succeed
	req5 := httptest.NewRequest("GET", "/v1/sessions", nil)
	req5.Header.Set("X-Ackbar-Token", "secret-token-123")
	rec5 := httptest.NewRecorder()
	mux.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with X-Ackbar-Token header, got %d", rec5.Code)
	}

	// 6. Request with ?token=secret-token-123 query param should succeed
	req6 := httptest.NewRequest("GET", "/v1/sessions?token=secret-token-123", nil)
	rec6 := httptest.NewRecorder()
	mux.ServeHTTP(rec6, req6)
	if rec6.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with ?token= param, got %d", rec6.Code)
	}
}

func TestAuthMiddleware_DisabledWhenEmpty(t *testing.T) {
	os.Unsetenv("ACKBAR_TOKEN")
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	server.SetToken("") // Empty token = open local/mesh access

	mux := server.Mux()

	req := httptest.NewRequest("GET", "/v1/sessions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK when token is empty, got %d", rec.Code)
	}
}
