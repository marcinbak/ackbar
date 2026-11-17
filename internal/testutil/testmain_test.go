package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Setenv(k, v)
		}
	}

	t.Setenv("ACKBAR_HOST", "poison")
	t.Setenv("ACKBAR_TOKEN", "poison")
	t.Setenv("ACKBAR_SESSION_ID", "poison")
	t.Setenv("GIT_CONFIG_GLOBAL", "/bad")

	tmpHome, cleanup, err := IsolateTestEnv()
	if err != nil {
		t.Fatalf("IsolateTestEnv failed: %v", err)
	}

	for _, k := range []string{"ACKBAR_HOST", "ACKBAR_TOKEN", "ACKBAR_SESSION_ID", "GIT_CONFIG_GLOBAL"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("expected %s unset, got %q", k, v)
		}
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
