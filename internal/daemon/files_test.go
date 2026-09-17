package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectMIMEType(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
	}{
		{".png", "image/png"},
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".gif", "image/gif"},
		{".webp", "image/webp"},
		{".svg", "image/svg+xml"},
		{".html", "text/html; charset=utf-8"},
		{".htm", "text/html; charset=utf-8"},
		{".pdf", "application/pdf"},
		{".json", "application/json"},
		{".txt", "text/plain; charset=utf-8"},
		{".md", "text/markdown; charset=utf-8"},
		{".csv", "text/csv; charset=utf-8"},
	}

	for _, tt := range tests {
		got := detectMIMEType(tt.ext)
		if got != tt.expected {
			t.Errorf("detectMIMEType(%q) = %q, expected %q", tt.ext, got, tt.expected)
		}
	}
}

func TestResolveFilePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-files-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	server := &Server{}
	sess := &Session{
		ID:  "sess-1",
		Cwd: tmpDir,
	}

	// 1. Relative path within session cwd
	p, err := server.resolveFilePath(sess, "hello.txt")
	if err != nil {
		t.Fatalf("Unexpected error resolving relative path: %v", err)
	}
	if p != testFile {
		t.Errorf("Expected %q, got %q", testFile, p)
	}

	// 2. Absolute path within session cwd
	p2, err := server.resolveFilePath(sess, testFile)
	if err != nil {
		t.Fatalf("Unexpected error resolving absolute path: %v", err)
	}
	if p2 != testFile {
		t.Errorf("Expected %q, got %q", testFile, p2)
	}

	// 3. Sensitive path traversal blocked
	_, err = server.resolveFilePath(sess, "../../etc/shadow")
	if err == nil {
		t.Errorf("Expected error for sensitive path, got nil")
	}

	_, err = server.resolveFilePath(sess, "~/.ssh/id_rsa")
	if err == nil {
		t.Errorf("Expected error for .ssh path, got nil")
	}
}

func TestHandleFileContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-files-serve-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	htmlFile := filepath.Join(tmpDir, "index.html")
	htmlContent := "<h1>Hello Ackbar</h1>"
	if err := os.WriteFile(htmlFile, []byte(htmlContent), 0644); err != nil {
		t.Fatalf("Failed to write html file: %v", err)
	}

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	sess := &Session{
		ID:    "test-session-123",
		Name:  "Test Session",
		Cwd:   tmpDir,
		Agent: "claude-code",
	}
	_ = db.SaveSession(sess)

	// Test 1: Serve local HTML file
	req := httptest.NewRequest("GET", "/v1/files/content?path=index.html&session_id=test-session-123", nil)
	rec := httptest.NewRecorder()
	server.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Expected text/html Content-Type, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != htmlContent {
		t.Errorf("Expected %q, got %q", htmlContent, rec.Body.String())
	}

	// Test 2: Download parameter sets Content-Disposition
	reqDl := httptest.NewRequest("GET", "/v1/files/content?path=index.html&session_id=test-session-123&download=1", nil)
	recDl := httptest.NewRecorder()
	server.Mux().ServeHTTP(recDl, reqDl)

	if recDl.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", recDl.Code)
	}
	if !strings.Contains(recDl.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("Expected attachment Content-Disposition, got %q", recDl.Header().Get("Content-Disposition"))
	}

	// Test 3: Missing path returns 400
	reqNoPath := httptest.NewRequest("GET", "/v1/files/content", nil)
	recNoPath := httptest.NewRecorder()
	server.Mux().ServeHTTP(recNoPath, reqNoPath)
	if recNoPath.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request, got %d", recNoPath.Code)
	}

	// Test 4: Missing file returns 404
	reqMissing := httptest.NewRequest("GET", "/v1/files/content?path=nonexistent.png&session_id=test-session-123", nil)
	recMissing := httptest.NewRecorder()
	server.Mux().ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found, got %d", recMissing.Code)
	}
}

func TestHandleFileOpen_VSCode(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-files-open-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	body := strings.NewReader(`{"path":"/some/repo/main.go","app":"vscode"}`)
	req := httptest.NewRequest("POST", "/v1/files/open", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}
	if resp["status"] != "opened" {
		t.Errorf("Expected status 'opened', got %v", resp["status"])
	}
	if resp["app"] != "vscode" {
		t.Errorf("Expected app 'vscode', got %v", resp["app"])
	}
}

func TestHandleFileContent_RemoteProxy(t *testing.T) {
	// Remote daemon mock
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/files/content" && r.URL.Query().Get("path") == "remote/chart.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("fake-png-data"))
			return
		}
		http.NotFound(w, r)
	}))
	defer remoteServer.Close()

	tmpDir, err := os.MkdirTemp("", "ackbar-remote-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	// Register remote host in DB
	_ = db.SaveHost(&HostRecord{
		Name: "devbox",
		URL:  remoteServer.URL,
	})

	server := NewServer(db)
	sess := &Session{
		ID:   "remote-sess-1",
		Host: "devbox",
	}
	_ = db.SaveSession(sess)

	req := httptest.NewRequest("GET", "/v1/files/content?path=remote/chart.png&session_id=remote-sess-1", nil)
	rec := httptest.NewRecorder()
	server.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from proxied file, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("Expected image/png Content-Type, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "fake-png-data" {
		t.Errorf("Expected 'fake-png-data', got %q", rec.Body.String())
	}
}

func TestHandleFileOpen_RemoteDownload(t *testing.T) {
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/files/content" && r.URL.Query().Get("path") == "results/report.pdf" {
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4 test data"))
			return
		}
		http.NotFound(w, r)
	}))
	defer remoteServer.Close()

	tmpDir, err := os.MkdirTemp("", "ackbar-remote-open-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	_ = db.SaveHost(&HostRecord{
		Name: "gpu-node",
		URL:  remoteServer.URL,
	})

	server := NewServer(db)

	body := strings.NewReader(`{"path":"results/report.pdf","host":"gpu-node","app":"browser"}`)
	req := httptest.NewRequest("POST", "/v1/files/open", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}
	if resp["status"] != "opened" {
		t.Errorf("Expected status 'opened', got %v", resp["status"])
	}
	if resp["remote"] != true {
		t.Errorf("Expected remote=true, got %v", resp["remote"])
	}
	if resp["cached_path"] == nil || resp["cached_path"] == "" {
		t.Errorf("Expected cached_path to be non-empty")
	}

	// Verify cached file was written to disk
	cachedPath := resp["cached_path"].(string)
	data, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("Failed to read cached file: %v", err)
	}
	if string(data) != "%PDF-1.4 test data" {
		t.Errorf("Cached content mismatch: expected '%%PDF-1.4 test data', got %q", string(data))
	}
}
