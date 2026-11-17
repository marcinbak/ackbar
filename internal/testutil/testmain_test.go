package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackageHomeIsIsolated(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir failed: %v", err)
	}
	if !strings.Contains(filepath.Base(home), "ackbar-test-home-") {
		t.Fatalf("expected UserHomeDir base to contain ackbar-test-home-, got %q", home)
	}

	cfgDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir failed: %v", err)
	}
	if !strings.Contains(cfgDir, "ackbar-test-home-") {
		t.Fatalf("expected UserConfigDir to be under isolated home, got %q", cfgDir)
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir failed: %v", err)
	}
	if !strings.Contains(cacheDir, "ackbar-test-home-") {
		t.Fatalf("expected UserCacheDir to be under isolated home, got %q", cacheDir)
	}
}

func TestIsolateTestEnv_RestoresAndCleansReadOnlyFiles(t *testing.T) {
	origHome := os.Getenv("HOME")

	t.Setenv("ACKBAR_HOST", "poison")
	t.Setenv("ACKBAR_TOKEN", "poison")
	t.Setenv("ACKBAR_SESSION_ID", "poison")
	t.Setenv("GIT_CONFIG_GLOBAL", "/bad")
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/claude")
	t.Setenv("GEMINI_CLI_HOME", "/custom/gemini")
	t.Setenv("GROK_HOME", "/custom/grok")

	tmpHome, cleanup, err := IsolateTestEnv()
	if err != nil {
		t.Fatalf("IsolateTestEnv failed: %v", err)
	}

	for _, k := range []string{"ACKBAR_HOST", "ACKBAR_TOKEN", "ACKBAR_SESSION_ID", "GIT_CONFIG_GLOBAL", "CLAUDE_CONFIG_DIR", "GEMINI_CLI_HOME", "GROK_HOME"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("expected %s unset, got %q", k, v)
		}
	}

	if os.Getenv("HOME") != tmpHome {
		t.Errorf("expected HOME %q, got %q", tmpHome, os.Getenv("HOME"))
	}

	if os.Getenv("GOCACHE") == "" {
		t.Error("expected GOCACHE to be populated")
	}
	if os.Getenv("GOPATH") == "" {
		t.Error("expected GOPATH to be populated")
	}

	subDir := filepath.Join(tmpHome, "ro-dir")
	if err := os.Mkdir(subDir, 0o700); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	filePath := filepath.Join(subDir, "ro-file")
	if err := os.WriteFile(filePath, []byte("data"), 0o400); err != nil {
		t.Fatalf("failed to write ro-file: %v", err)
	}
	_ = os.Chmod(filePath, 0o400)
	if err := os.Chmod(subDir, 0o500); err != nil {
		t.Fatalf("failed to chmod subDir to 0500: %v", err)
	}

	cleanup()

	if _, err := os.Stat(tmpHome); !os.IsNotExist(err) {
		t.Fatalf("expected tmpHome to be removed, got err: %v", err)
	}

	if got := os.Getenv("HOME"); got != origHome {
		t.Errorf("expected HOME restored to %q, got %q", origHome, got)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "/custom/claude" {
		t.Errorf("expected CLAUDE_CONFIG_DIR restored to /custom/claude, got %q", got)
	}
	if got := os.Getenv("GEMINI_CLI_HOME"); got != "/custom/gemini" {
		t.Errorf("expected GEMINI_CLI_HOME restored to /custom/gemini, got %q", got)
	}
	if got := os.Getenv("GROK_HOME"); got != "/custom/grok" {
		t.Errorf("expected GROK_HOME restored to /custom/grok, got %q", got)
	}
}

func TestIsolateTestEnv_StubsAgentCLIs(t *testing.T) {
	origPath := os.Getenv("PATH")

	tmpHome, cleanup, err := IsolateTestEnv()
	if err != nil {
		t.Fatalf("IsolateTestEnv failed: %v", err)
	}
	cleaned := false
	defer func() {
		if !cleaned {
			cleanup()
		}
	}()

	ext := ""
	stubContent := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		ext = ".bat"
		stubContent = "@echo off\r\nexit /b 0\r\n"
	}
	samePath := func(a, b string) bool {
		if runtime.GOOS == "windows" {
			return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
		}
		return filepath.Clean(a) == filepath.Clean(b)
	}

	agentBins := []string{"claude", "agy", "antigravity", "codex", "grok", "grok-cli", "opencode"}
	for _, name := range agentBins {
		got, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("exec.LookPath(%q) failed: %v", name, err)
		}
		want := filepath.Join(tmpHome, ".test-bin", name+ext)
		if !samePath(got, want) {
			t.Errorf("exec.LookPath(%q) = %q, want %q", name, got, want)
		}
	}

	customDir := t.TempDir()
	customClaude := filepath.Join(customDir, "claude"+ext)
	if err := os.WriteFile(customClaude, []byte(stubContent), 0o755); err != nil {
		t.Fatalf("write custom claude: %v", err)
	}
	if err := os.Setenv("PATH", customDir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		t.Fatalf("Setenv PATH: %v", err)
	}

	gotCustom, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("exec.LookPath(custom claude) failed: %v", err)
	}
	if !samePath(gotCustom, customClaude) {
		t.Errorf("expected custom PATH dir to take precedence: got %q, want %q", gotCustom, customClaude)
	}

	cleanup()
	cleaned = true

	if got := os.Getenv("PATH"); got != origPath {
		t.Errorf("expected PATH restored to %q, got %q", origPath, got)
	}
}

func TestRepoPackagesEnforceTestMainIsolation(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("failed to find repo root containing go.mod")
		}
		dir = parent
	}
	repoRoot := dir

	err = filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != repoRoot && (name == ".git" || name == ".worktrees" || name == ".claude" || name == ".gemini" || name == "vendor" || name == "mobile" || name == "node_modules" || name == "bin") {
			return filepath.SkipDir
		}

		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}

		var testFiles []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
				testFiles = append(testFiles, filepath.Join(path, e.Name()))
			}
		}
		if len(testFiles) == 0 {
			return nil
		}

		relDir, _ := filepath.Rel(repoRoot, path)
		var testMainDecl *ast.FuncDecl
		var testMainFile string
		fset := token.NewFileSet()

		for _, tf := range testFiles {
			node, err := parser.ParseFile(fset, tf, nil, 0)
			if err != nil {
				t.Fatalf("failed to parse %s: %v", tf, err)
			}
			for _, decl := range node.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Name.Name == "TestMain" {
					testMainDecl = fn
					testMainFile = tf
					break
				}
			}
			if testMainDecl != nil {
				break
			}
		}

		if testMainDecl == nil {
			t.Errorf("Package %s contains test files but no TestMain declaration", relDir)
			return nil
		}

		var runIsolatedPos token.Pos
		ast.Inspect(testMainDecl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "RunIsolatedTestMain" {
					runIsolatedPos = call.Pos()
				}
			case *ast.SelectorExpr:
				if fun.Sel.Name == "RunIsolatedTestMain" {
					runIsolatedPos = call.Pos()
				}
			}
			return true
		})

		if !runIsolatedPos.IsValid() {
			t.Errorf("Package %s TestMain in %s does not call RunIsolatedTestMain", relDir, filepath.Base(testMainFile))
			return nil
		}

		if relDir == filepath.Join("internal", "daemon") {
			var workerPos token.Pos
			ast.Inspect(testMainDecl.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if ok && strings.Contains(lit.Value, "--headless-worker") {
					workerPos = lit.Pos()
					return false
				}
				return true
			})
			if !workerPos.IsValid() {
				t.Errorf("Package %s TestMain missing --headless-worker check", relDir)
			} else if workerPos >= runIsolatedPos {
				t.Errorf("Package %s TestMain --headless-worker pos %v not before RunIsolatedTestMain pos %v", relDir, workerPos, runIsolatedPos)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir failed: %v", err)
	}
}
