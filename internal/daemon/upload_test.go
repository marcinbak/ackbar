package daemon

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createTestServer(t *testing.T) *Server {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to init test DB: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return NewServer(db)
}

func TestHandleUploadMultipartPNG(t *testing.T) {
	srv := createTestServer(t)

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("file", "screenshot.png")
	if err != nil {
		t.Fatalf("CreateFormFile error: %v", err)
	}
	// Fake PNG header
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	w.Close()

	req := httptest.NewRequest("POST", "/v1/uploads?host=local", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res UploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if res.Status != "ok" {
		t.Errorf("Expected status 'ok', got %q", res.Status)
	}
	if !strings.HasSuffix(res.Path, ".png") {
		t.Errorf("Expected .png extension, got path %q", res.Path)
	}
	if _, err := os.Stat(res.Path); os.IsNotExist(err) {
		t.Errorf("Uploaded file does not exist on disk: %s", res.Path)
	}
	defer os.Remove(res.Path)
}

func TestHandleUploadRawBinaryBMP(t *testing.T) {
	srv := createTestServer(t)

	// Fake BMP data
	bmpData := []byte("BM\x00\x00\x00\x00\x00\x00\x00\x00\x36\x00\x00\x00")
	req := httptest.NewRequest("POST", "/v1/uploads?filename=graphic.bmp&host=local", bytes.NewReader(bmpData))
	req.Header.Set("Content-Type", "image/bmp")
	rec := httptest.NewRecorder()

	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res UploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if !strings.HasSuffix(res.Filename, ".bmp") {
		t.Errorf("Expected .bmp extension, got %q", res.Filename)
	}
	if _, err := os.Stat(res.Path); os.IsNotExist(err) {
		t.Errorf("Uploaded file does not exist on disk: %s", res.Path)
	}
	defer os.Remove(res.Path)
}

func TestHandleUploadPDF(t *testing.T) {
	srv := createTestServer(t)

	pdfData := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF")
	req := httptest.NewRequest("POST", "/v1/uploads?filename=spec.pdf&host=local", bytes.NewReader(pdfData))
	req.Header.Set("Content-Type", "application/pdf")
	rec := httptest.NewRecorder()

	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res UploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if !strings.HasSuffix(res.Filename, ".pdf") {
		t.Errorf("Expected .pdf extension, got %q", res.Filename)
	}
	defer os.Remove(res.Path)
}

func TestHandleUploadTextAndDataFiles(t *testing.T) {
	srv := createTestServer(t)

	// Test .txt upload
	txtData := []byte("error log trace content")
	reqTxt := httptest.NewRequest("POST", "/v1/uploads?filename=trace.txt&host=local", bytes.NewReader(txtData))
	reqTxt.Header.Set("Content-Type", "text/plain")
	recTxt := httptest.NewRecorder()
	srv.Mux().ServeHTTP(recTxt, reqTxt)
	if recTxt.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for txt upload, got %d: %s", recTxt.Code, recTxt.Body.String())
	}
	var resTxt UploadResponse
	if err := json.Unmarshal(recTxt.Body.Bytes(), &resTxt); err != nil {
		t.Fatalf("Failed to parse txt upload JSON: %v", err)
	}
	if !strings.HasSuffix(resTxt.Filename, ".txt") {
		t.Errorf("Expected .txt extension, got %q", resTxt.Filename)
	}
	defer os.Remove(resTxt.Path)

	// Test .json upload
	jsonData := []byte(`{"key": "value"}`)
	reqJSON := httptest.NewRequest("POST", "/v1/uploads?filename=config.json&host=local", bytes.NewReader(jsonData))
	reqJSON.Header.Set("Content-Type", "application/json")
	recJSON := httptest.NewRecorder()
	srv.Mux().ServeHTTP(recJSON, reqJSON)
	if recJSON.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for json upload, got %d: %s", recJSON.Code, recJSON.Body.String())
	}
	var resJSON UploadResponse
	if err := json.Unmarshal(recJSON.Body.Bytes(), &resJSON); err != nil {
		t.Fatalf("Failed to parse json upload JSON: %v", err)
	}
	if !strings.HasSuffix(resJSON.Filename, ".json") {
		t.Errorf("Expected .json extension, got %q", resJSON.Filename)
	}
	defer os.Remove(resJSON.Path)
}

func TestHandleUploadDisallowedExtension(t *testing.T) {
	srv := createTestServer(t)

	shData := []byte("#!/bin/bash\necho hello\n")
	req := httptest.NewRequest("POST", "/v1/uploads?filename=script.sh", bytes.NewReader(shData))
	req.Header.Set("Content-Type", "application/x-sh")
	rec := httptest.NewRecorder()

	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for .sh upload, got %d", rec.Code)
	}
}

func TestPruneOldUploads(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-test-prune-*")
	if err != nil {
		t.Fatalf("MkdirTemp error: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldFile := filepath.Join(tmpDir, "old_file.png")
	newFile := filepath.Join(tmpDir, "new_file.png")

	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Set old file modified time to 10 days ago
	tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour)
	_ = os.Chtimes(oldFile, tenDaysAgo, tenDaysAgo)

	PruneOldUploads(tmpDir, 7*24*time.Hour)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("Expected old file to be pruned, but it still exists")
	}
	if _, err := os.Stat(newFile); os.IsNotExist(err) {
		t.Errorf("Expected new file to remain, but it was deleted")
	}
}
