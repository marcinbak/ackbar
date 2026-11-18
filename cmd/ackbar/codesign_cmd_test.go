package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ackbar/internal/version"
)

func TestCodesignSetupRunner(t *testing.T) {
	t.Run("help_flag", func(t *testing.T) {
		var out bytes.Buffer
		r := &codesignSetupRunner{goos: "linux", stdout: &out}
		if err := r.run([]string{"--help"}); err != nil {
			t.Fatalf("--help failed: %v", err)
		}
		if !strings.Contains(out.String(), "ackbar setup-codesign") {
			t.Fatalf("expected usage text, got: %s", out.String())
		}
	})

	t.Run("rejects_non_darwin", func(t *testing.T) {
		var out bytes.Buffer
		r := &codesignSetupRunner{goos: "linux", homeDir: t.TempDir(), stdout: &out}
		err := r.run(nil)
		if err == nil || !strings.Contains(err.Error(), "macOS") {
			t.Fatalf("expected macOS rejection error, got: %v", err)
		}
	})

	t.Run("setup_signs_local_and_homebrew_and_restarts", func(t *testing.T) {
		home := t.TempDir()
		localBin := filepath.Join(home, ".local", "bin")
		brewBin := filepath.Join(t.TempDir(), "opt", "ackbar", "bin")
		_ = os.MkdirAll(localBin, 0755)
		_ = os.MkdirAll(brewBin, 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbard"), []byte("bin"), 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbar"), []byte("bin"), 0755)
		_ = os.WriteFile(filepath.Join(brewBin, "ackbard"), []byte("bin"), 0755)

		var gotIdentityArg string
		var signedDirs []string
		restarted := false
		var out bytes.Buffer

		r := &codesignSetupRunner{
			goos:               "darwin",
			homeDir:            home,
			homebrewOptBinDirs: []string{brewBin},
			stdout:             &out,
			bootstrapIdentity: func(ctx context.Context, h, custom string) (string, string, bool, error) {
				gotIdentityArg = custom
				return version.DefaultLocalCodesignIdentity, "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678", true, nil
			},
			codesignDir: func(ctx context.Context, dir string, binaries []string) {
				signedDirs = append(signedDirs, dir)
			},
			inspectReq: func(ctx context.Context, filePath string) (string, error) {
				return `designated => identifier "com.marcinbak.ackbard" and certificate root = H"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"`, nil
			},
			restartDaemon: func(w io.Writer) error {
				restarted = true
				return nil
			},
		}

		if err := r.run([]string{"--identity", "Developer ID Application: Test"}); err != nil {
			t.Fatalf("run failed: %v", err)
		}
		if gotIdentityArg != "Developer ID Application: Test" {
			t.Errorf("got custom identity %q", gotIdentityArg)
		}
		if len(signedDirs) != 2 {
			t.Fatalf("expected 2 signed dirs, got %v", signedDirs)
		}
		if !restarted {
			t.Error("expected daemon restart to be invoked")
		}
		if !strings.Contains(out.String(), "certificate root = H") {
			t.Errorf("expected requirement output in stdout, got:\n%s", out.String())
		}
	})

	t.Run("setup_fails_if_cdhash_persists", func(t *testing.T) {
		home := t.TempDir()
		localBin := filepath.Join(home, ".local", "bin")
		_ = os.MkdirAll(localBin, 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbard"), []byte("bin"), 0755)

		var out bytes.Buffer
		r := &codesignSetupRunner{
			goos:    "darwin",
			homeDir: home,
			stdout:  &out,
			bootstrapIdentity: func(ctx context.Context, h, custom string) (string, string, bool, error) {
				return version.DefaultLocalCodesignIdentity, "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678", false, nil
			},
			codesignDir: func(ctx context.Context, dir string, binaries []string) {},
			inspectReq: func(ctx context.Context, filePath string) (string, error) {
				return `designated => identifier "com.marcinbak.ackbard" and cdhash H"deadbeef"`, nil
			},
			restartDaemon: func(w io.Writer) error {
				t.Fatal("daemon should not restart when verification fails")
				return nil
			},
		}

		if err := r.run(nil); err == nil || !strings.Contains(err.Error(), "cdhash") {
			t.Fatalf("expected cdhash error, got %v", err)
		}
	})

	t.Run("verify_mode", func(t *testing.T) {
		home := t.TempDir()
		localBin := filepath.Join(home, ".local", "bin")
		_ = os.MkdirAll(localBin, 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbard"), []byte("bin"), 0755)

		var out bytes.Buffer
		r := &codesignSetupRunner{
			goos:               "darwin",
			homeDir:            home,
			stdout:             &out,
			isKeychainUnlocked: func(ctx context.Context, h string) bool { return true },
			resolveIdentity: func(ctx context.Context, h string) (string, string, bool) {
				return version.DefaultLocalCodesignIdentity, "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678", true
			},
			verifySig: func(ctx context.Context, filePath string) error { return nil },
			inspectReq: func(ctx context.Context, filePath string) (string, error) {
				return `designated => identifier "com.marcinbak.ackbard" and certificate root = H"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"`, nil
			},
		}

		if err := r.run([]string{"--verify"}); err != nil {
			t.Fatalf("--verify failed: %v", err)
		}

		r.inspectReq = func(ctx context.Context, filePath string) (string, error) {
			return `designated => cdhash H"1234"`, nil
		}
		if err := r.run([]string{"--verify"}); err == nil || !strings.Contains(err.Error(), "cdhash") {
			t.Fatalf("expected cdhash error in verify mode, got %v", err)
		}
	})
}

func TestRestartLocalDaemonAfterCodesignWith(t *testing.T) {
	t.Run("not_running_noop", func(t *testing.T) {
		var out bytes.Buffer
		err := restartLocalDaemonAfterCodesignWith(
			&out,
			func() (string, string, string, bool) { return "", "", "", false },
			func() (bool, bool, error) { t.Fatal("drain should not be called"); return false, false, nil },
			func() (bool, bool, error) { t.Fatal("restart should not be called"); return false, false, nil },
			func(h, d string) { t.Fatal("spawn should not be called") },
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("deferred_skips_restart", func(t *testing.T) {
		var out bytes.Buffer
		err := restartLocalDaemonAfterCodesignWith(
			&out,
			func() (string, string, string, bool) { return "1.0", "mac", "Mac", true },
			func() (bool, bool, error) { return true, false, nil },
			func() (bool, bool, error) { t.Fatal("restart should not be called"); return false, false, nil },
			func(h, d string) { t.Fatal("spawn should not be called") },
		)
		if err != nil || !strings.Contains(out.String(), "Active headless") {
			t.Fatalf("err=%v out=%s", err, out.String())
		}
	})

	t.Run("managed_restart_error_propagates", func(t *testing.T) {
		var out bytes.Buffer
		err := restartLocalDaemonAfterCodesignWith(
			&out,
			func() (string, string, string, bool) { return "1.0", "mac", "Mac", true },
			func() (bool, bool, error) { return false, false, nil },
			func() (bool, bool, error) { return true, false, errors.New("kickstart failed") },
			func(h, d string) { t.Fatal("spawn should not be called") },
		)
		if err == nil || !strings.Contains(err.Error(), "kickstart failed") {
			t.Fatalf("expected error, got %v", err)
		}
	})

	t.Run("unmanaged_spawns", func(t *testing.T) {
		var out bytes.Buffer
		spawnedHost := ""
		err := restartLocalDaemonAfterCodesignWith(
			&out,
			func() (string, string, string, bool) { return "1.0", "macmini", "Mac Mini", true },
			func() (bool, bool, error) { return false, false, nil },
			func() (bool, bool, error) { return false, false, nil },
			func(h, d string) { spawnedHost = h + ":" + d },
		)
		if err != nil || spawnedHost != "macmini:Mac Mini" {
			t.Fatalf("err=%v spawned=%q", err, spawnedHost)
		}
	})
}
