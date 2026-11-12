package version

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestRepo(t *testing.T) (string, func(args ...string) (string, string, error)) {
	t.Helper()
	dir := t.TempDir()

	srcScript, err := os.ReadFile(filepath.Join("..", "..", "scripts", "bump-version.sh"))
	if err != nil {
		t.Fatalf("failed to read bump script: %v", err)
	}

	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("failed to create scripts dir: %v", err)
	}
	dstScript := filepath.Join(scriptsDir, "bump-version.sh")
	if err := os.WriteFile(dstScript, srcScript, 0755); err != nil {
		t.Fatalf("failed to write test bump script: %v", err)
	}

	verDir := filepath.Join(dir, "internal", "version")
	if err := os.MkdirAll(verDir, 0755); err != nil {
		t.Fatalf("failed to create version dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(verDir, "VERSION"), []byte("20261111.07\n"), 0644); err != nil {
		t.Fatalf("failed to write initial VERSION: %v", err)
	}

	mobileDir := filepath.Join(dir, "mobile", "lib", "core", "constants")
	if err := os.MkdirAll(mobileDir, 0755); err != nil {
		t.Fatalf("failed to create mobile constants dir: %v", err)
	}
	appVerContent := "class AppVersion {\n  static const String version = '20261111.07';\n  static const String displayVersion = 'v$version';\n}\n"
	if err := os.WriteFile(filepath.Join(mobileDir, "app_version.dart"), []byte(appVerContent), 0644); err != nil {
		t.Fatalf("failed to write initial app_version.dart: %v", err)
	}

	runGit := func(gitArgs ...string) {
		cmd := exec.Command("git", gitArgs...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\nOutput: %s", strings.Join(gitArgs, " "), err, out)
		}
	}

	runGit("init", "-b", "main")
	runGit("config", "user.name", "Test Runner")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "commit.gpgsign", "false")
	runGit("add", ".")
	runGit("commit", "-m", "initial")
	runGit("update-ref", "refs/remotes/origin/main", "HEAD")
	runGit("checkout", "-b", "feat/test-sync")

	runBump := func(args ...string) (string, string, error) {
		cmd := exec.Command("./scripts/bump-version.sh", args...)
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		cmd.Env = append(os.Environ(),
			"ACKBAR_BUMP_NO_FETCH=1",
			"ACKBAR_BUMP_DATE=20261112",
			"GIT_CONFIG_GLOBAL=/dev/null",
		)
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	return dir, runBump
}

func TestBumpScript_SyncAndParity(t *testing.T) {
	dir, runBump := setupTestRepo(t)
	verPath := filepath.Join(dir, "internal", "version", "VERSION")
	appVerPath := filepath.Join(dir, "mobile", "lib", "core", "constants", "app_version.dart")

	// Case 1: Bump sync + --check pass
	stdout, stderr, err := runBump()
	if err != nil {
		t.Fatalf("bump failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	verBytes, err := os.ReadFile(verPath)
	if err != nil {
		t.Fatalf("failed to read VERSION: %v", err)
	}
	if strings.TrimSpace(string(verBytes)) != "20261112.01" {
		t.Fatalf("expected VERSION to be 20261112.01, got %q", string(verBytes))
	}

	appVerBytes, err := os.ReadFile(appVerPath)
	if err != nil {
		t.Fatalf("failed to read app_version.dart: %v", err)
	}
	appVerStr := string(appVerBytes)
	if !strings.Contains(appVerStr, "static const String version = '20261112.01';") {
		t.Fatalf("app_version.dart missing updated version: %s", appVerStr)
	}
	if !strings.Contains(appVerStr, "static const String displayVersion = 'v$version';") {
		t.Fatalf("app_version.dart corrupted displayVersion: %s", appVerStr)
	}

	stdout, stderr, err = runBump("--check")
	if err != nil {
		t.Fatalf("--check failed after bump: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	// Case 2: --check failure on app_version.dart mismatch
	legacyContent := "class AppVersion {\n  static const String version = '20261111.07';\n  static const String displayVersion = 'v$version';\n}\n"
	if err := os.WriteFile(appVerPath, []byte(legacyContent), 0644); err != nil {
		t.Fatalf("failed to overwrite app_version.dart: %v", err)
	}

	_, stderr, err = runBump("--check")
	if err == nil {
		t.Fatal("expected --check to fail on mismatched version, but passed")
	}
	expectedErr := "ERROR: mobile/lib/core/constants/app_version.dart (20261111.07) does not match internal/version/VERSION (20261112.01). Run './scripts/bump-version.sh' to sync."
	if !strings.Contains(stderr, expectedErr) {
		t.Fatalf("stderr does not contain expected error.\nExpected: %s\nActual: %s", expectedErr, stderr)
	}

	// Case 3: Drift repair when VERSION is already bumped
	stdout, stderr, err = runBump()
	if err != nil {
		t.Fatalf("drift repair bump failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	verBytes, err = os.ReadFile(verPath)
	if err != nil {
		t.Fatalf("failed to read VERSION: %v", err)
	}
	if strings.TrimSpace(string(verBytes)) != "20261112.01" {
		t.Fatalf("expected VERSION to remain 20261112.01, got %q", string(verBytes))
	}

	appVerBytes, err = os.ReadFile(appVerPath)
	if err != nil {
		t.Fatalf("failed to read app_version.dart: %v", err)
	}
	if !strings.Contains(string(appVerBytes), "static const String version = '20261112.01';") {
		t.Fatalf("app_version.dart was not repaired: %s", string(appVerBytes))
	}

	_, stderr, err = runBump("--check")
	if err != nil {
		t.Fatalf("--check failed after repair: %v\nstderr: %s", err, stderr)
	}
}
