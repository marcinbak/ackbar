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
	"os/exec"
	"path/filepath"
	"strings"
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

func TestBumpVersionScript(t *testing.T) {
	// Look for scripts/bump-version.sh relative to this test file
	scriptPath := filepath.Join("..", "..", "scripts", "bump-version.sh")
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("Failed to read bump-version.sh: %v", err)
	}

	tempDir := t.TempDir()

	runCmd := func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		cmd.Dir = tempDir
		cmd.Env = append(os.Environ(),
			"ACKBAR_BUMP_NO_FETCH=1",
			"ACKBAR_BUMP_DATE=20261105",
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, runErr := cmd.CombinedOutput()
		return string(out), runErr
	}

	// Initialize git repo
	if out, err := runCmd("git", "init"); err != nil {
		t.Fatalf("git init failed: %v, out: %s", err, out)
	}
	if out, err := runCmd("git", "checkout", "-b", "main"); err != nil {
		t.Fatalf("git checkout -b main failed: %v, out: %s", err, out)
	}
	if out, err := runCmd("git", "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config failed: %v, out: %s", err, out)
	}
	if out, err := runCmd("git", "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config failed: %v, out: %s", err, out)
	}

	// Set up initial internal/version/VERSION (20261105.01)
	if err := os.MkdirAll(filepath.Join(tempDir, "internal", "version"), 0755); err != nil {
		t.Fatalf("Failed to create internal/version dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "internal", "version", "VERSION"), []byte("20261105.01\n"), 0644); err != nil {
		t.Fatalf("Failed to write VERSION file: %v", err)
	}

	// Copy scripts/bump-version.sh
	if err := os.MkdirAll(filepath.Join(tempDir, "scripts"), 0755); err != nil {
		t.Fatalf("Failed to create scripts dir: %v", err)
	}
	destScript := filepath.Join(tempDir, "scripts", "bump-version.sh")
	if err := os.WriteFile(destScript, scriptBytes, 0755); err != nil {
		t.Fatalf("Failed to write bump-version.sh to temp dir: %v", err)
	}

	// Commit on main
	if out, err := runCmd("git", "add", "-A"); err != nil {
		t.Fatalf("git add failed: %v, out: %s", err, out)
	}
	if out, err := runCmd("git", "commit", "-m", "initial commit on main"); err != nil {
		t.Fatalf("git commit failed: %v, out: %s", err, out)
	}

	// Create origin/main ref
	if out, err := runCmd("git", "update-ref", "refs/remotes/origin/main", "HEAD"); err != nil {
		t.Fatalf("git update-ref failed: %v, out: %s", err, out)
	}
	if out, err := runCmd("git", "branch", "origin/main"); err != nil {
		t.Fatalf("git branch origin/main failed: %v, out: %s", err, out)
	}

	// Create and switch to feat/test branch
	if out, err := runCmd("git", "checkout", "-b", "feat/test"); err != nil {
		t.Fatalf("git checkout -b feat/test failed: %v, out: %s", err, out)
	}

	// 1. --check fails on feature branch when VERSION equals origin/main (20261105.01)
	out, err := runCmd("./scripts/bump-version.sh", "--check")
	if err == nil {
		t.Fatalf("Expected --check to fail when VERSION is not bumped, but succeeded. Output:\n%s", out)
	}
	if !strings.Contains(out, "must be bumped above origin/main (20261105.01)") {
		t.Errorf("Expected failure message about bump, got:\n%s", out)
	}

	// 2. Running ./scripts/bump-version.sh bumps internal/version/VERSION to 20261105.02
	out, err = runCmd("./scripts/bump-version.sh")
	if err != nil {
		t.Fatalf("./scripts/bump-version.sh failed: %v, out: %s", err, out)
	}
	verBytes, err := os.ReadFile(filepath.Join(tempDir, "internal", "version", "VERSION"))
	if err != nil {
		t.Fatalf("Failed to read VERSION: %v", err)
	}
	if got := strings.TrimSpace(string(verBytes)); got != "20261105.02" {
		t.Fatalf("Expected version 20261105.02, got %q", got)
	}

	// 3. Running ./scripts/bump-version.sh a second time is idempotent (stays 20261105.02)
	out, err = runCmd("./scripts/bump-version.sh")
	if err != nil {
		t.Fatalf("Second ./scripts/bump-version.sh run failed: %v, out: %s", err, out)
	}
	verBytes, err = os.ReadFile(filepath.Join(tempDir, "internal", "version", "VERSION"))
	if err != nil {
		t.Fatalf("Failed to read VERSION: %v", err)
	}
	if got := strings.TrimSpace(string(verBytes)); got != "20261105.02" {
		t.Fatalf("Expected version to stay 20261105.02, got %q", got)
	}
	if !strings.Contains(out, "Version already bumped to 20261105.02") {
		t.Errorf("Expected idempotent message, got:\n%s", out)
	}

	// 4. --check passes once bumped to 20261105.02
	out, err = runCmd("./scripts/bump-version.sh", "--check")
	if err != nil {
		t.Fatalf("Expected --check to pass for 20261105.02, but failed: %v, out: %s", err, out)
	}
	if !strings.Contains(out, "Version check passed: 20261105.02 (> origin/main 20261105.01)") {
		t.Errorf("Expected passed message, got:\n%s", out)
	}

	// 5. Creating a tag v20261105.02 on origin/main causes --check to fail and ./scripts/bump-version.sh to advance to 20261105.03
	// Commit the 20261105.02 change on feat/test first so HEAD is not at origin/main commit
	if out, err := runCmd("git", "commit", "-am", "bump version to 20261105.02"); err != nil {
		t.Fatalf("git commit on feat/test failed: %v, out: %s", err, out)
	}
	// Tag origin/main commit with v20261105.02
	mainCommit, err := runCmd("git", "rev-parse", "refs/remotes/origin/main")
	if err != nil {
		t.Fatalf("git rev-parse refs/remotes/origin/main failed: %v", err)
	}
	if out, err := runCmd("git", "tag", "v20261105.02", strings.TrimSpace(mainCommit)); err != nil {
		t.Fatalf("git tag failed: %v, out: %s", err, out)
	}

	out, err = runCmd("./scripts/bump-version.sh", "--check")
	if err == nil {
		t.Fatalf("Expected --check to fail when tag v20261105.02 exists on another commit, but succeeded. Output:\n%s", out)
	}
	if !strings.Contains(out, "ERROR: Tag v20261105.02 already exists. Run './scripts/bump-version.sh --force' to advance to the next free version.") {
		t.Errorf("Expected tag already exists message, got:\n%s", out)
	}

	out, err = runCmd("./scripts/bump-version.sh")
	if err != nil {
		t.Fatalf("bump-version after conflicting tag failed: %v, out: %s", err, out)
	}
	verBytes, err = os.ReadFile(filepath.Join(tempDir, "internal", "version", "VERSION"))
	if err != nil {
		t.Fatalf("Failed to read VERSION: %v", err)
	}
	if got := strings.TrimSpace(string(verBytes)); got != "20261105.03" {
		t.Fatalf("Expected version to advance to 20261105.03, got %q", got)
	}

	// 6. --check skips and exits 0 when branch is homebrew-formula-v20261105.01
	if out, err := runCmd("git", "checkout", "-b", "homebrew-formula-v20261105.01"); err != nil {
		t.Fatalf("git checkout -b homebrew-formula-... failed: %v, out: %s", err, out)
	}
	// Revert internal/version/VERSION to unbumped 20261105.01
	if err := os.WriteFile(filepath.Join(tempDir, "internal", "version", "VERSION"), []byte("20261105.01\n"), 0644); err != nil {
		t.Fatalf("Failed to revert VERSION: %v", err)
	}
	out, err = runCmd("./scripts/bump-version.sh", "--check")
	if err != nil {
		t.Fatalf("Expected --check to skip on homebrew-formula branch, but failed: %v, out: %s", err, out)
	}
	if !strings.Contains(out, "Version bump check skipped for branch 'homebrew-formula-v20261105.01'") {
		t.Errorf("Expected skip message, got:\n%s", out)
	}
}
