package version

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyT0_DualAppVersionSync(t *testing.T) {
	dir := t.TempDir()

	srcScript, err := os.ReadFile(filepath.Join("..", "..", "scripts", "bump-version.sh"))
	if err != nil {
		t.Fatalf("failed to read bump script: %v", err)
	}

	scriptsDir := filepath.Join(dir, "scripts")
	_ = os.MkdirAll(scriptsDir, 0755)
	dstScript := filepath.Join(scriptsDir, "bump-version.sh")
	_ = os.WriteFile(dstScript, srcScript, 0755)

	verDir := filepath.Join(dir, "internal", "version")
	_ = os.MkdirAll(verDir, 0755)
	_ = os.WriteFile(filepath.Join(verDir, "VERSION"), []byte("20261111.01\n"), 0644)

	// Path 1: mobile/lib/core/constants/app_version.dart
	path1Dir := filepath.Join(dir, "mobile", "lib", "core", "constants")
	_ = os.MkdirAll(path1Dir, 0755)
	content1 := "class AppVersion {\n  static const String version = '20261111.01';\n}\n"
	_ = os.WriteFile(filepath.Join(path1Dir, "app_version.dart"), []byte(content1), 0644)

	// Path 2: mobile/lib/core/app_version.dart
	path2Dir := filepath.Join(dir, "mobile", "lib", "core")
	_ = os.MkdirAll(path2Dir, 0755)
	content2 := "class CoreAppVersion {\n  static const String version = '20261111.01';\n}\n"
	_ = os.WriteFile(filepath.Join(path2Dir, "app_version.dart"), []byte(content2), 0644)

	runCmd := func(c string, args ...string) (string, error) {
		cmd := exec.Command(c, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "ACKBAR_BUMP_NO_FETCH=1", "ACKBAR_BUMP_DATE=20261112")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	_, _ = runCmd("git", "init", "-b", "main")
	_, _ = runCmd("git", "config", "user.name", "Test")
	_, _ = runCmd("git", "config", "user.email", "test@test.com")
	_, _ = runCmd("git", "config", "commit.gpgsign", "false")
	_, _ = runCmd("git", "add", ".")
	_, _ = runCmd("git", "commit", "-m", "initial commit")

	// Create feature branch
	_, _ = runCmd("git", "checkout", "-b", "feat/test")

	// Bump version
	out, err := runCmd(dstScript, "--base", "main")
	if err != nil {
		t.Fatalf("bump-version.sh failed: %v\nOutput: %s", err, out)
	}

	// Verify BOTH files were updated to 20261112.01
	data1, _ := os.ReadFile(filepath.Join(path1Dir, "app_version.dart"))
	if !strings.Contains(string(data1), "version = '20261112.01'") {
		t.Errorf("expected path 1 to be synced, got: %s", string(data1))
	}
	data2, _ := os.ReadFile(filepath.Join(path2Dir, "app_version.dart"))
	if !strings.Contains(string(data2), "version = '20261112.01'") {
		t.Errorf("expected path 2 to be synced, got: %s", string(data2))
	}

	// Verify check passes
	checkOut, err := runCmd(dstScript, "--check", "--base", "main")
	if err != nil {
		t.Fatalf("check failed: %v\nOutput: %s", err, checkOut)
	}

	// Modify path 2 to simulate desync
	_ = os.WriteFile(filepath.Join(path2Dir, "app_version.dart"), []byte("class CoreAppVersion {\n  static const String version = '20261111.01';\n}\n"), 0644)
	desyncOut, err := runCmd(dstScript, "--check", "--base", "main")
	if err == nil {
		t.Fatalf("expected check to fail on desync, but succeeded")
	}
	if !strings.Contains(desyncOut, "mobile/lib/core/app_version.dart") {
		t.Errorf("expected error to name desynced file, got: %s", desyncOut)
	}
}

func TestVerifyT0_ScriptCLI(t *testing.T) {
	verifyScript, err := os.ReadFile(filepath.Join("..", "..", "scripts", "verify-t0.sh"))
	if err != nil {
		t.Fatalf("failed to read verify-t0.sh: %v", err)
	}

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "verify-t0.sh")
	if err := os.WriteFile(scriptPath, verifyScript, 0755); err != nil {
		t.Fatalf("failed to write test verify-t0.sh: %v", err)
	}

	// 1. --help exits 0
	cmdHelp := exec.Command(scriptPath, "--help")
	cmdHelp.Dir = dir
	var outHelp bytes.Buffer
	cmdHelp.Stdout = &outHelp
	if err := cmdHelp.Run(); err != nil {
		t.Errorf("--help failed: %v", err)
	}
	if !strings.Contains(outHelp.String(), "Usage:") {
		t.Errorf("expected Usage in help output, got: %s", outHelp.String())
	}

	// 2. Unknown option exits 1
	cmdUnknown := exec.Command(scriptPath, "--bogus-option")
	cmdUnknown.Dir = dir
	if err := cmdUnknown.Run(); err == nil {
		t.Errorf("expected failure on unknown option, got exit 0")
	}

	// 3. AGENTS.md line check failure
	// Create dummy repo with over-budget AGENTS.md and minimal Go module for go vet
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.25\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(strings.Repeat("line\n", 165)), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "scripts"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "scripts", "verify-t0.sh"), verifyScript, 0755)

	cmdBudget := exec.Command(filepath.Join(dir, "scripts", "verify-t0.sh"), "--skip-version-check")
	cmdBudget.Dir = dir
	outBudget, err := cmdBudget.CombinedOutput()
	if err == nil {
		t.Errorf("expected script to fail on AGENTS.md budget violation")
	}
	if !strings.Contains(string(outBudget), "exceeds 160 lines budget") {
		t.Errorf("expected budget error message, got: %s", string(outBudget))
	}
}
