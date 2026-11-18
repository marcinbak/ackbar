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

	t.Run("setup_signs_verifies_all_binaries_and_restarts", func(t *testing.T) {
		home := t.TempDir()
		localBin := filepath.Join(home, ".local", "bin")
		brewBin := filepath.Join(t.TempDir(), "opt", "ackbar", "bin")
		_ = os.MkdirAll(localBin, 0755)
		_ = os.MkdirAll(brewBin, 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbard"), []byte("bin"), 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbar"), []byte("bin"), 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbar-hook"), []byte("bin"), 0755)
		_ = os.WriteFile(filepath.Join(brewBin, "ackbard"), []byte("bin"), 0755)

		t.Setenv("ACKBAR_CODESIGN_IDENTITY", "none")

		var gotIdentityArg string
		var signedDirs []string
		var verifiedSigPaths, inspectedPaths []string
		restarted := false
		var out bytes.Buffer

		const wantSHA = "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678"
		r := &codesignSetupRunner{
			goos:               "darwin",
			homeDir:            home,
			homebrewOptBinDirs: []string{brewBin},
			stdout:             &out,
			bootstrapIdentity: func(ctx context.Context, h, custom string) (string, string, bool, error) {
				gotIdentityArg = custom
				return version.DefaultLocalCodesignIdentity, wantSHA, true, nil
			},
			codesignDirStrict: func(ctx context.Context, dir string, binaries []string, explicitSHA1 string) ([]string, error) {
				if explicitSHA1 != wantSHA {
					t.Fatalf("got explicitSHA1 %q, want %q", explicitSHA1, wantSHA)
				}
				signedDirs = append(signedDirs, dir)
				var present []string
				for _, b := range binaries {
					if _, err := os.Stat(filepath.Join(dir, b)); err == nil {
						present = append(present, b)
					}
				}
				return present, nil
			},
			verifySig: func(ctx context.Context, filePath string) error {
				verifiedSigPaths = append(verifiedSigPaths, filePath)
				return nil
			},
			inspectReq: func(ctx context.Context, filePath string) (string, error) {
				inspectedPaths = append(inspectedPaths, filePath)
				return `designated => identifier "com.marcinbak.ackbar" and certificate root = H"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"`, nil
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
		if len(verifiedSigPaths) != 4 || len(inspectedPaths) != 4 {
			t.Fatalf("expected all 4 installed binaries verified, got sig=%v req=%v", verifiedSigPaths, inspectedPaths)
		}
		if !restarted {
			t.Error("expected daemon restart to be invoked")
		}
		if !strings.Contains(out.String(), "ACKBAR_CODESIGN_IDENTITY") {
			t.Errorf("expected env override warning in stdout, got:\n%s", out.String())
		}
	})

	t.Run("setup_propagates_strict_signing_error", func(t *testing.T) {
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
			codesignDirStrict: func(ctx context.Context, dir string, binaries []string, explicitSHA1 string) ([]string, error) {
				return nil, errors.New("login keychain is locked")
			},
			restartDaemon: func(w io.Writer) error {
				t.Fatal("daemon should not restart on signing failure")
				return nil
			},
		}

		err := r.run(nil)
		if err == nil || !strings.Contains(err.Error(), "login keychain is locked") {
			t.Fatalf("expected propagated signing error, got %v", err)
		}
		if strings.Contains(out.String(), "Atomically signed") {
			t.Errorf("must not print success on signing error: %s", out.String())
		}
	})

	t.Run("setup_fails_if_non_ackbard_binary_fails_verification_or_has_cdhash", func(t *testing.T) {
		home := t.TempDir()
		localBin := filepath.Join(home, ".local", "bin")
		_ = os.MkdirAll(localBin, 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbard"), []byte("bin"), 0755)
		_ = os.WriteFile(filepath.Join(localBin, "ackbar-hook"), []byte("bin"), 0755)

		var out bytes.Buffer
		r := &codesignSetupRunner{
			goos:    "darwin",
			homeDir: home,
			stdout:  &out,
			bootstrapIdentity: func(ctx context.Context, h, custom string) (string, string, bool, error) {
				return version.DefaultLocalCodesignIdentity, "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678", false, nil
			},
			codesignDirStrict: func(ctx context.Context, dir string, binaries []string, explicitSHA1 string) ([]string, error) {
				return []string{"ackbard", "ackbar-hook"}, nil
			},
			verifySig: func(ctx context.Context, filePath string) error {
				return nil
			},
			inspectReq: func(ctx context.Context, filePath string) (string, error) {
				if strings.HasSuffix(filePath, "ackbar-hook") {
					return `designated => identifier "com.marcinbak.ackbar-hook" and cdhash H"deadbeef"`, nil
				}
				return `designated => identifier "com.marcinbak.ackbard" and certificate root = H"a1b2"`, nil
			},
			restartDaemon: func(w io.Writer) error {
				t.Fatal("daemon should not restart when verification fails")
				return nil
			},
		}

		if err := r.run(nil); err == nil || !strings.Contains(err.Error(), "cdhash") {
			t.Fatalf("expected cdhash error on ackbar-hook, got %v", err)
		}

		r.verifySig = func(ctx context.Context, filePath string) error {
			if strings.HasSuffix(filePath, "ackbar-hook") {
				return errors.New("invalid signature")
			}
			return nil
		}
		if err := r.run(nil); err == nil || !strings.Contains(err.Error(), "invalid signature") {
			t.Fatalf("expected verifySig error on ackbar-hook, got %v", err)
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

func TestResolveAckbardBinaryAndDaemonRestart(t *testing.T) {
	home := t.TempDir()
	brewBin := filepath.Join(t.TempDir(), "opt", "ackbar", "bin")
	_ = os.MkdirAll(brewBin, 0755)

	if got := resolveAckbardBinary(home, []string{brewBin}); got != "ackbard" {
		t.Fatalf("expected PATH fallback 'ackbard', got %q", got)
	}

	brewAckbard := filepath.Join(brewBin, "ackbard")
	_ = os.WriteFile(brewAckbard, []byte("bin"), 0755)
	if got := resolveAckbardBinary(home, []string{brewBin}); got != brewAckbard {
		t.Fatalf("expected Homebrew binary %q, got %q", brewAckbard, got)
	}

	localBin := filepath.Join(home, ".local", "bin")
	_ = os.MkdirAll(localBin, 0755)
	localAckbard := filepath.Join(localBin, "ackbard")
	_ = os.WriteFile(localAckbard, []byte("bin"), 0755)
	if got := resolveAckbardBinary(home, []string{brewBin}); got != localAckbard {
		t.Fatalf("expected local binary %q, got %q", localAckbard, got)
	}

	t.Run("not_running_noop", func(t *testing.T) {
		var out bytes.Buffer
		err := restartLocalDaemonAfterCodesignWith(
			&out,
			func() (string, string, string, bool) { return "", "", "", false },
			func() (bool, bool, error) { t.Fatal("drain should not be called"); return false, false, nil },
			func() (bool, bool, error) { t.Fatal("restart should not be called"); return false, false, nil },
			func(h, d string) error { t.Fatal("spawn should not be called"); return nil },
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
			func(h, d string) error { t.Fatal("spawn should not be called"); return nil },
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
			func(h, d string) error { t.Fatal("spawn should not be called"); return nil },
		)
		if err == nil || !strings.Contains(err.Error(), "kickstart failed") {
			t.Fatalf("expected error, got %v", err)
		}
	})

	t.Run("unmanaged_spawns_and_propagates_spawn_error", func(t *testing.T) {
		var out bytes.Buffer
		spawnedHost := ""
		err := restartLocalDaemonAfterCodesignWith(
			&out,
			func() (string, string, string, bool) { return "1.0", "macmini", "Mac Mini", true },
			func() (bool, bool, error) { return false, false, nil },
			func() (bool, bool, error) { return false, false, nil },
			func(h, d string) error { spawnedHost = h + ":" + d; return nil },
		)
		if err != nil || spawnedHost != "macmini:Mac Mini" {
			t.Fatalf("err=%v spawned=%q", err, spawnedHost)
		}

		err = restartLocalDaemonAfterCodesignWith(
			&out,
			func() (string, string, string, bool) { return "1.0", "macmini", "Mac Mini", true },
			func() (bool, bool, error) { return false, false, nil },
			func() (bool, bool, error) { return false, false, nil },
			func(h, d string) error { return errors.New("exec spawn boom") },
		)
		if err == nil || !strings.Contains(err.Error(), "exec spawn boom") {
			t.Fatalf("expected spawn error propagation, got %v", err)
		}
	})
}
