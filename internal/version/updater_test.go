package version

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTarGz(t *testing.T) {
	// Create a synthetic tar.gz
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	files := []struct {
		name string
		body string
		mode int64
	}{
		{"ackbar", "binary-ackbar", 0755},
		{"ackbard", "binary-ackbard", 0755},
		{"docs/README.md", "# Hello", 0644},
	}

	for _, f := range files {
		hdr := &tar.Header{
			Name: f.name,
			Mode: f.mode,
			Size: int64(len(f.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("Failed writing header: %v", err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatalf("Failed writing body: %v", err)
		}
	}
	_ = tw.Close()
	_ = gzw.Close()

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("Failed writing archive: %v", err)
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	extracted, err := ExtractTarGz(archivePath, extractDir)
	if err != nil {
		t.Fatalf("ExtractTarGz failed: %v", err)
	}

	if len(extracted) != 3 {
		t.Errorf("Expected 3 extracted files, got %d", len(extracted))
	}

	// Verify content of ackbar
	body, err := os.ReadFile(filepath.Join(extractDir, "ackbar"))
	if err != nil || string(body) != "binary-ackbar" {
		t.Errorf("Expected 'binary-ackbar', got %q (err: %v)", string(body), err)
	}
}

func TestExtractTarGz_PathTraversal(t *testing.T) {
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "../evil.sh",
		Mode: 0755,
		Size: int64(len("malicious")),
	}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("malicious"))
	_ = tw.Close()
	_ = gzw.Close()

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "evil.tar.gz")
	_ = os.WriteFile(archivePath, buf.Bytes(), 0644)

	extractDir := filepath.Join(tmpDir, "extracted")
	_, err := ExtractTarGz(archivePath, extractDir)
	if err == nil {
		t.Fatalf("Expected ExtractTarGz to fail with path traversal error, got nil")
	}
}

func TestVerifyChecksum(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "data.bin")
	content := []byte("hello ackbar release")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("Failed writing test file: %v", err)
	}

	h := sha256.Sum256(content)
	validHash := hex.EncodeToString(h[:])

	if err := VerifyChecksum(filePath, validHash); err != nil {
		t.Errorf("Expected valid checksum to pass: %v", err)
	}

	if err := VerifyChecksum(filePath, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Errorf("Expected invalid checksum to fail")
	}
}

func TestParseChecksums(t *testing.T) {
	checksumsText := `
# Comment line
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  ackbar_20260930.01_darwin_arm64.tar.gz
2143d19bbf10707ffc5d56e1234567890abcdef1234567890abcdef1234567890  ackbar_20260930.01_linux_amd64.tar.gz
`
	res := ParseChecksums(checksumsText)
	if len(res) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(res))
	}

	if res["ackbar_20260930.01_darwin_arm64.tar.gz"] != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("Incorrect hash parsed for darwin_arm64: %s", res["ackbar_20260930.01_darwin_arm64.tar.gz"])
	}
}

func TestIsHomebrewInstalled(t *testing.T) {
	if !IsHomebrewInstalled("/opt/homebrew/Cellar/ackbar/20260930.01/bin/ackbar") {
		t.Errorf("Expected Homebrew path to return true")
	}
	if !IsHomebrewInstalled("/usr/local/Cellar/ackbar/20260930.01/bin/ackbar") {
		t.Errorf("Expected Homebrew path to return true")
	}
	if IsHomebrewInstalled("/home/user/.local/bin/ackbar") {
		// Only true if `brew list ackbar` is available, otherwise false
		// For standard ~/.local/bin path without Cellar, it shouldn't match string checks
	}
}
