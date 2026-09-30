package version

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		vA, vB   string
		expected int
	}{
		{"20260930.02", "20260930.01", 1},
		{"20260930.01", "20260930.02", -1},
		{"20260930.01", "20260930.01", 0},
		{"v20260930.02", "20260930.01", 1},
		{"20260930.01", "v20260904.04", 1},
		{"20260904.04", "20260930.01", -1},
		{"0.2.2", "0.2.1", 1},
		{"0.2.1", "0.2.2", -1},
		{"v1.0.0", "v1.0.0", 0},
		{"20260930.01", "", 1},
		{"", "20260930.01", -1},
		{"", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.vA+"_vs_"+tt.vB, func(t *testing.T) {
			got := Compare(tt.vA, tt.vB)
			if got != tt.expected {
				t.Errorf("Compare(%q, %q) = %d; want %d", tt.vA, tt.vB, got, tt.expected)
			}
		})
	}
}

func TestIsNewer(t *testing.T) {
	if !IsNewer("20260930.02", "20260930.01") {
		t.Errorf("Expected 20260930.02 to be newer than 20260930.01")
	}
	if IsNewer("20260930.01", "20260930.02") {
		t.Errorf("Expected 20260930.01 NOT to be newer than 20260930.02")
	}
	if IsNewer("20260930.01", "20260930.01") {
		t.Errorf("Expected identical versions NOT to be newer")
	}
}

func TestExtractTarGz(t *testing.T) {
	// Build an in-memory tar.gz containing ackbar and ackbard dummy executables
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	files := []struct {
		name    string
		content string
	}{
		{"ackbar", "#!/bin/sh\necho ackbar\n"},
		{"ackbard", "#!/bin/sh\necho ackbard\n"},
		{"README.md", "ignore me\n"},
	}

	for _, f := range files {
		hdr := &tar.Header{
			Name: f.name,
			Mode: 0755,
			Size: int64(len(f.content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("Failed to write header: %v", err)
		}
		if _, err := tw.Write([]byte(f.content)); err != nil {
			t.Fatalf("Failed to write content: %v", err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("Failed to close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("Failed to close gzip: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "test-extract-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	extracted, err := ExtractTarGz(&buf, tmpDir, []string{"ackbar", "ackbard"})
	if err != nil {
		t.Fatalf("ExtractTarGz failed: %v", err)
	}

	if len(extracted) != 2 {
		t.Errorf("Expected 2 extracted binaries, got %d (%v)", len(extracted), extracted)
	}

	for _, name := range []string{"ackbar", "ackbard"} {
		dest := filepath.Join(tmpDir, name)
		info, err := os.Stat(dest)
		if err != nil {
			t.Errorf("Expected file %s to exist: %v", dest, err)
			continue
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("Expected file %s to be executable, mode: %v", dest, info.Mode())
		}
	}

	// Ensure README.md was NOT extracted
	if _, err := os.Stat(filepath.Join(tmpDir, "README.md")); !os.IsNotExist(err) {
		t.Errorf("Expected README.md to be omitted, but found it")
	}
}

func TestResolvePlatformAsset(t *testing.T) {
	assets := []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{
		{"ackbar_20260930.02_darwin_arm64.tar.gz", "https://example.com/darwin_arm64"},
		{"ackbar_20260930.02_linux_amd64.tar.gz", "https://example.com/linux_amd64"},
	}

	name, url := resolvePlatformAsset(assets, "20260930.02", "darwin", "arm64")
	if name != "ackbar_20260930.02_darwin_arm64.tar.gz" || url != "https://example.com/darwin_arm64" {
		t.Errorf("Asset resolution failed for darwin/arm64: got %s, %s", name, url)
	}

	// Test fallback when asset not in list
	name, url = resolvePlatformAsset(assets, "20260930.02", "darwin", "amd64")
	if name != "ackbar_20260930.02_darwin_amd64.tar.gz" {
		t.Errorf("Expected fallback name ackbar_20260930.02_darwin_amd64.tar.gz, got %s", name)
	}
	if url == "" {
		t.Errorf("Expected non-empty fallback URL")
	}
}

func TestCheckLatestRelease_Mock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name":     "v20991231.99",
			"html_url":     "https://github.com/marcinbak/ackbar/releases/tag/v20991231.99",
			"body":         "Major release",
			"published_at": "2099-12-31T23:59:59Z",
			"assets": []map[string]string{
				{
					"name":                 "ackbar_20991231.99_linux_amd64.tar.gz",
					"browser_download_url": "https://example.com/download/ackbar_linux_amd64.tar.gz",
				},
			},
		})
	}))
	defer ts.Close()

	origAPI := ReleaseAPI
	ReleaseAPI = ts.URL
	defer func() { ReleaseAPI = origAPI }()

	info, err := CheckLatestRelease(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckLatestRelease failed: %v", err)
	}

	if info.Version != "20991231.99" {
		t.Errorf("Expected version 20991231.99, got %s", info.Version)
	}
	if !info.IsNewer {
		t.Errorf("Expected 20991231.99 to be marked as newer")
	}
	if info.Changelog != "Major release" {
		t.Errorf("Expected changelog 'Major release', got %s", info.Changelog)
	}
}
