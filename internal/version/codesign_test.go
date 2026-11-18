package version

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setTestCodesignHooks(t *testing.T, goos string, runner func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)) {
	t.Helper()
	codesignHooksMu.Lock()
	prev := codesignHooks
	codesignHooks = codesignRunner{
		goos:   goos,
		runCmd: runner,
	}
	codesignHooksMu.Unlock()
	t.Cleanup(func() {
		codesignHooksMu.Lock()
		codesignHooks = prev
		codesignHooksMu.Unlock()
	})
}

func TestBundleIdentifierForBinary(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ackbard", "com.marcinbak.ackbard"},
		{"ackbar", "com.marcinbak.ackbar"},
		{"ackbar-hook", "com.marcinbak.ackbar-hook"},
		{"ackbar-relay", "com.marcinbak.ackbar-relay"},
		{"ackbard.tmp-12345", "com.marcinbak.ackbard"},
		{"ackbar.new", "com.marcinbak.ackbar"},
		{"ackbar-hook.sync-999", "com.marcinbak.ackbar-hook"},
		{"ackbar-relay.cs-abcd", "com.marcinbak.ackbar-relay"},
		{"/tmp/dir/ackbard.new.cs-xyz", "com.marcinbak.ackbard"},
		{"ackbar_custom", "com.marcinbak.ackbar_custom"},
		{"", ""},
		{".", ""},
		{".tmp-123", ""},
		{"ackbar;rm", ""},
		{`ackbar"bad`, ""},
		{"ackbar bad", ""},
		{"Ackbar", ""},
	}
	for _, tc := range cases {
		if got := BundleIdentifierForBinary(tc.in); got != tc.want {
			t.Errorf("BundleIdentifierForBinary(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveDarwinSigningIdentity(t *testing.T) {
	const (
		shaLocal = "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678"
		shaDevID = "1111111111111111111111111111111111111111"
		shaApple = "2222222222222222222222222222222222222222"
	)

	t.Run("disabled_by_env", func(t *testing.T) {
		for _, disabled := range []string{"-", "none", "NONE", "adhoc"} {
			t.Setenv("ACKBAR_CODESIGN_IDENTITY", disabled)
			called := false
			setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
				called = true
				return nil, nil
			})
			id, sha, ok := ResolveDarwinSigningIdentity(context.Background(), t.TempDir())
			if ok || id != "" || sha != "" || called {
				t.Fatalf("disabled=%q got (%q, %q, %v) called=%v, want empty/false", disabled, id, sha, ok, called)
			}
		}
	})

	t.Run("auto_select_priority", func(t *testing.T) {
		t.Setenv("ACKBAR_CODESIGN_IDENTITY", "")
		home := t.TempDir()

		outAll := "  1) " + strings.ToLower(shaApple) + ` "Apple Development: Alice (TEAM1)"` + "\n" +
			"  2) " + shaDevID + ` "Developer ID Application: Acme (TEAM2)"` + "\n" +
			"  3) " + strings.ToLower(shaLocal) + ` "Ackbar Local Code Signing"` + "\n" +
			"  4) 3333333333333333333333333333333333333333 \"Revoked Cert\" (CSSMERR_TP_CERT_REVOKED)\n"

		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			return []byte(outAll), nil
		})
		id, sha, ok := ResolveDarwinSigningIdentity(context.Background(), home)
		if !ok || id != DefaultLocalCodesignIdentity || sha != shaLocal {
			t.Fatalf("got (%q, %q, %v), want (%q, %q, true)", id, sha, ok, DefaultLocalCodesignIdentity, shaLocal)
		}

		outNoLocal := "  1) " + shaApple + ` "Apple Development: Alice (TEAM1)"` + "\n" +
			"  2) " + shaDevID + ` "Developer ID Application: Acme (TEAM2)"` + "\n"
		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			return []byte(outNoLocal), nil
		})
		id, sha, ok = ResolveDarwinSigningIdentity(context.Background(), home)
		if !ok || id != "Developer ID Application: Acme (TEAM2)" || sha != shaDevID {
			t.Fatalf("got (%q, %q, %v), want Developer ID Application", id, sha, ok)
		}

		outOnlyApple := "  1) " + shaApple + ` "Apple Development: Alice (TEAM1)"` + "\n"
		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			return []byte(outOnlyApple), nil
		})
		id, sha, ok = ResolveDarwinSigningIdentity(context.Background(), home)
		if !ok || id != "Apple Development: Alice (TEAM1)" || sha != shaApple {
			t.Fatalf("got (%q, %q, %v), want Apple Development", id, sha, ok)
		}
	})

	t.Run("config_file_and_stale_identity", func(t *testing.T) {
		t.Setenv("ACKBAR_CODESIGN_IDENTITY", "")
		home := t.TempDir()
		cfgPath := filepath.Join(home, filepath.FromSlash(CodesignIdentityRelPath))
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, []byte(strings.ToLower(shaApple)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}

		out := "  1) " + shaLocal + ` "Ackbar Local Code Signing"` + "\n" +
			"  2) " + shaApple + ` "Apple Development: Alice (TEAM1)"` + "\n"
		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			return []byte(out), nil
		})

		id, sha, ok := ResolveDarwinSigningIdentity(context.Background(), home)
		if !ok || id != "Apple Development: Alice (TEAM1)" || sha != shaApple {
			t.Fatalf("config SHA1 match got (%q, %q, %v)", id, sha, ok)
		}

		// Stale deleted identity should return ok=false rather than failing updates
		if err := os.WriteFile(cfgPath, []byte("Deleted Cert Name\n"), 0600); err != nil {
			t.Fatal(err)
		}
		id, sha, ok = ResolveDarwinSigningIdentity(context.Background(), home)
		if ok || id != "" || sha != "" {
			t.Fatalf("stale identity got (%q, %q, %v), want empty/false", id, sha, ok)
		}

		// Env overrides config file
		t.Setenv("ACKBAR_CODESIGN_IDENTITY", "Ackbar Local Code Signing")
		id, sha, ok = ResolveDarwinSigningIdentity(context.Background(), home)
		if !ok || id != DefaultLocalCodesignIdentity || sha != shaLocal {
			t.Fatalf("env override got (%q, %q, %v)", id, sha, ok)
		}
	})
}

func TestIsLoginKeychainUnlocked(t *testing.T) {
	home := t.TempDir()
	called := false
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if IsLoginKeychainUnlocked(context.Background(), home) || called {
		t.Fatalf("expected false without keychain files and no command invocation")
	}

	kcDir := filepath.Join(home, "Library", "Keychains")
	if err := os.MkdirAll(kcDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyKC := filepath.Join(kcDir, "login.keychain")
	if err := os.WriteFile(legacyKC, []byte("kc"), 0600); err != nil {
		t.Fatal(err)
	}

	var gotPath string
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		gotPath = args[len(args)-1]
		return nil, nil
	})
	if !IsLoginKeychainUnlocked(context.Background(), home) || gotPath != legacyKC {
		t.Fatalf("legacy keychain fallback failed: gotPath=%q", gotPath)
	}

	dbKC := filepath.Join(kcDir, "login.keychain-db")
	if err := os.WriteFile(dbKC, []byte("kc"), 0600); err != nil {
		t.Fatal(err)
	}
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		gotPath = args[len(args)-1]
		return nil, errors.New("keychain locked")
	})
	if IsLoginKeychainUnlocked(context.Background(), home) || gotPath != dbKC {
		t.Fatalf("expected false when login.keychain-db locked, gotPath=%q", gotPath)
	}
}

func TestCodesignBinaryFile_NoOpsAndPersistentRequirement(t *testing.T) {
	// Default test environment with nil runner never executes external binaries
	setTestCodesignHooks(t, "darwin", nil)
	if err := CodesignBinaryFile(context.Background(), "/tmp/ackbard", "ackbard"); err != nil {
		t.Fatalf("expected nil no-op in test env without mock runner, got %v", err)
	}

	// Non-darwin is always a no-op
	nonDarwinCalled := false
	setTestCodesignHooks(t, "linux", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		nonDarwinCalled = true
		return nil, nil
	})
	if err := CodesignBinaryFile(context.Background(), "/tmp/ackbard", "ackbard"); err != nil || nonDarwinCalled {
		t.Fatalf("expected non-darwin no-op, err=%v called=%v", err, nonDarwinCalled)
	}

	// Locked keychain is a no-op
	home := t.TempDir()
	t.Setenv("HOME", home)
	kcDir := filepath.Join(home, "Library", "Keychains")
	_ = os.MkdirAll(kcDir, 0700)
	_ = os.WriteFile(filepath.Join(kcDir, "login.keychain-db"), []byte("kc"), 0600)

	var cmds [][]string
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, append([]string{name}, args...))
		if name == "security" && args[0] == "show-keychain-info" {
			return nil, errors.New("locked")
		}
		return nil, nil
	})
	if err := CodesignBinaryFile(context.Background(), "/tmp/ackbard", "ackbard"); err != nil {
		t.Fatalf("expected nil on locked keychain, got %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected only show-keychain-info when locked, got %v", cmds)
	}

	// Unlocked keychain + cdhash triggers requirement pinning without cdhash
	const sha = "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678"
	cmds = nil
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, append([]string{name}, args...))
		switch name {
		case "security":
			if args[0] == "find-identity" {
				return []byte("  1) " + sha + ` "Ackbar Local Code Signing"` + "\n"), nil
			}
			return nil, nil
		case "codesign":
			if args[0] == "-d" {
				return []byte(`designated => identifier "com.marcinbak.ackbard" and cdhash H"deadbeef"` + "\n"), nil
			}
			return nil, nil
		}
		return nil, nil
	})

	target := filepath.Join(t.TempDir(), "ackbard.tmp-123")
	if err := os.WriteFile(target, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := CodesignBinaryFile(context.Background(), target, "ackbard.tmp-123"); err != nil {
		t.Fatalf("CodesignBinaryFile failed: %v", err)
	}
	if len(cmds) != 5 {
		t.Fatalf("expected 5 commands (show-keychain-info, find-identity, codesign, codesign -d -r-, codesign -r=...), got %d: %v", len(cmds), cmds)
	}
	pinCmd := cmds[4]
	joinedPin := strings.Join(pinCmd, " ")
	wantReq := `-r=designated => identifier "com.marcinbak.ackbard" and certificate root = H"` + strings.ToLower(sha) + `"`
	if !strings.Contains(joinedPin, "--sign "+sha) || !strings.Contains(joinedPin, wantReq) {
		t.Fatalf("unexpected pinning command: %v", pinCmd)
	}

	// Already persistent requirement (no cdhash) skips second signing pass
	cmds = nil
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, append([]string{name}, args...))
		if name == "security" && args[0] == "find-identity" {
			return []byte("  1) " + sha + ` "Developer ID Application: Acme"` + "\n"), nil
		}
		if name == "codesign" && args[0] == "-d" {
			return []byte(`designated => identifier "com.marcinbak.ackbard" and anchor apple generic`), nil
		}
		return nil, nil
	})
	if err := CodesignBinaryFile(context.Background(), target, "ackbard"); err != nil {
		t.Fatalf("CodesignBinaryFile failed: %v", err)
	}
	if len(cmds) != 4 {
		t.Fatalf("expected 4 commands when no cdhash present, got %d: %v", len(cmds), cmds)
	}
}

func TestCodesignInstalledBinaryAtomic_And_Dir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	kcDir := filepath.Join(home, "Library", "Keychains")
	_ = os.MkdirAll(kcDir, 0700)
	_ = os.WriteFile(filepath.Join(kcDir, "login.keychain-db"), []byte("kc"), 0600)

	binDir := t.TempDir()
	ackbardPath := filepath.Join(binDir, "ackbard")
	ackbarPath := filepath.Join(binDir, "ackbar")
	_ = os.WriteFile(ackbardPath, []byte("old-ackbard"), 0644)
	_ = os.WriteFile(ackbarPath, []byte("old-ackbar"), 0644)

	const sha = "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678"
	var signedPaths []string
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if name == "security" && args[0] == "find-identity" {
			return []byte("  1) " + sha + ` "Ackbar Local Code Signing"` + "\n"), nil
		}
		if name == "codesign" && args[0] == "--force" {
			signedPaths = append(signedPaths, args[len(args)-1])
		}
		if name == "codesign" && args[0] == "-d" {
			return []byte(`designated => identifier "x" and anchor apple`), nil
		}
		return nil, nil
	})

	CodesignInstalledDir(context.Background(), binDir, TargetBinaries)

	if len(signedPaths) != 2 {
		t.Fatalf("expected 2 signed temp paths, got %v", signedPaths)
	}
	for _, p := range signedPaths {
		if !strings.Contains(filepath.Base(p), ".cs-") {
			t.Errorf("expected atomic temp file *.cs-*, got %q", p)
		}
	}
	for _, target := range []string{ackbardPath, ackbarPath} {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("stat %s: %v", target, err)
		}
		if info.Mode().Perm() != 0755 {
			t.Errorf("%s mode = %o, want 0755", target, info.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(binDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".cs-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}

	// Failure removes temp file and leaves original untouched
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if name == "security" && args[0] == "find-identity" {
			return []byte("  1) " + sha + ` "Ackbar Local Code Signing"` + "\n"), nil
		}
		if name == "codesign" && args[0] == "--force" {
			return []byte("sign boom"), errors.New("exit 1")
		}
		return nil, nil
	})
	if err := CodesignInstalledBinaryAtomic(context.Background(), ackbardPath, "ackbard"); err == nil {
		t.Fatal("expected error when codesign fails")
	}
	entries, _ = os.ReadDir(binDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".cs-") {
			t.Errorf("leftover temp file after failure: %s", e.Name())
		}
	}
}

func TestBootstrapLocalCodesignIdentity(t *testing.T) {
	t.Run("rejects_non_darwin", func(t *testing.T) {
		setTestCodesignHooks(t, "linux", nil)
		if _, _, _, err := BootstrapLocalCodesignIdentity(context.Background(), t.TempDir(), ""); err == nil {
			t.Fatal("expected error on non-darwin")
		}
	})

	t.Run("custom_and_existing_identity", func(t *testing.T) {
		const shaCustom = "9999999999999999999999999999999999999999"
		const shaLocal = "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678"
		home := t.TempDir()

		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			return []byte("  1) " + shaCustom + ` "Developer ID Application: Acme"` + "\n  2) " + shaLocal + ` "Ackbar Local Code Signing"` + "\n"), nil
		})

		if _, _, _, err := BootstrapLocalCodesignIdentity(context.Background(), home, "Missing Identity"); err == nil {
			t.Fatal("expected error for missing custom identity")
		}

		id, sha, created, err := BootstrapLocalCodesignIdentity(context.Background(), home, "Developer ID Application: Acme")
		if err != nil || created || id != "Developer ID Application: Acme" || sha != shaCustom {
			t.Fatalf("custom identity got (%q, %q, %v, %v)", id, sha, created, err)
		}
		cfgPath := filepath.Join(home, filepath.FromSlash(CodesignIdentityRelPath))
		if info, err := os.Stat(cfgPath); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("config file mode=%v err=%v", info, err)
		}

		id, sha, created, err = BootstrapLocalCodesignIdentity(context.Background(), home, "")
		if err != nil || created || id != DefaultLocalCodesignIdentity || sha != shaLocal {
			t.Fatalf("existing default got (%q, %q, %v, %v)", id, sha, created, err)
		}
	})

	t.Run("creates_self_signed_cert_and_falls_back_from_legacy", func(t *testing.T) {
		home := t.TempDir()
		kcDir := filepath.Join(home, "Library", "Keychains")
		_ = os.MkdirAll(kcDir, 0700)
		kcPath := filepath.Join(kcDir, "login.keychain-db")
		_ = os.WriteFile(kcPath, []byte("kc"), 0600)

		var generatedSHA1 string
		var sawLegacyAttempt, sawFallbackAttempt, sawImport, sawTrust bool
		var observedTempDir string

		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			switch name {
			case "security":
				switch args[0] {
				case "find-identity":
					if sawTrust && generatedSHA1 != "" {
						return []byte("  1) " + generatedSHA1 + ` "` + DefaultLocalCodesignIdentity + `"` + "\n"), nil
					}
					return []byte("0 valid identities found\n"), nil
				case "import":
					sawImport = true
					joined := strings.Join(args, " ")
					if !strings.Contains(joined, "-T /usr/bin/codesign") || !strings.Contains(joined, "-k "+kcPath) {
						t.Fatalf("unexpected security import args: %v", args)
					}
					return nil, nil
				case "add-trusted-cert":
					sawTrust = true
					joined := strings.Join(args, " ")
					if !strings.Contains(joined, "-r trustRoot -p codeSign -k "+kcPath) {
						t.Fatalf("unexpected add-trusted-cert args: %v", args)
					}
					return nil, nil
				}
			case "openssl":
				hasLegacy := false
				var certPath, keyPath string
				for i, a := range args {
					if a == "-legacy" {
						hasLegacy = true
					}
					if a == "-in" && i+1 < len(args) {
						certPath = args[i+1]
					}
					if a == "-inkey" && i+1 < len(args) {
						keyPath = args[i+1]
					}
				}
				if hasLegacy {
					sawLegacyAttempt = true
					return []byte("unknown option -legacy"), errors.New("exit 1")
				}
				sawFallbackAttempt = true

				// Verify passphrase was passed via env, never on CLI args
				if len(env) != 1 || !strings.HasPrefix(env[0], "ACKBAR_P12_PASS=") {
					t.Fatalf("expected ACKBAR_P12_PASS env, got %v", env)
				}
				pass := strings.TrimPrefix(env[0], "ACKBAR_P12_PASS=")
				if strings.Contains(strings.Join(args, " "), pass) {
					t.Fatalf("passphrase leaked into openssl CLI args: %v", args)
				}

				observedTempDir = filepath.Dir(certPath)
				if info, err := os.Stat(observedTempDir); err != nil || info.Mode().Perm() != 0700 {
					t.Fatalf("temp dir mode=%v err=%v, want 0700", info, err)
				}
				if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("key.pem mode=%v err=%v, want 0600", info, err)
				}

				certBytes, err := os.ReadFile(certPath)
				if err != nil {
					t.Fatalf("read cert.pem: %v", err)
				}
				block, _ := pem.Decode(certBytes)
				if block == nil {
					t.Fatal("failed to decode cert.pem")
				}
				cert, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					t.Fatalf("ParseCertificate: %v", err)
				}
				if cert.Subject.CommonName != DefaultLocalCodesignIdentity || cert.IsCA || !cert.BasicConstraintsValid {
					t.Fatalf("unexpected cert subject/constraints: CN=%q IsCA=%v Basic=%v", cert.Subject.CommonName, cert.IsCA, cert.BasicConstraintsValid)
				}
				if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageCodeSigning {
					t.Fatalf("unexpected key usage: %v ext=%v", cert.KeyUsage, cert.ExtKeyUsage)
				}
				pub, ok := cert.PublicKey.(*rsa.PublicKey)
				if !ok || pub.N.BitLen() != 2048 {
					t.Fatalf("expected RSA-2048 public key, got %T", cert.PublicKey)
				}
				if cert.NotAfter.Sub(cert.NotBefore) < 9*365*24*time.Hour {
					t.Fatalf("expected ~10y validity, got %v", cert.NotAfter.Sub(cert.NotBefore))
				}
				sum := sha1.Sum(block.Bytes)
				generatedSHA1 = strings.ToUpper(hex.EncodeToString(sum[:]))
				return nil, nil
			}
			return nil, errors.New("unexpected command")
		})

		id, sha, created, err := BootstrapLocalCodesignIdentity(context.Background(), home, "")
		if err != nil {
			t.Fatalf("BootstrapLocalCodesignIdentity failed: %v", err)
		}
		if !created || id != DefaultLocalCodesignIdentity || sha != generatedSHA1 {
			t.Fatalf("got (%q, %q, %v), want (%q, %q, true)", id, sha, created, DefaultLocalCodesignIdentity, generatedSHA1)
		}
		if !sawLegacyAttempt || !sawFallbackAttempt || !sawImport || !sawTrust {
			t.Fatalf("missing bootstrap stage: legacy=%v fallback=%v import=%v trust=%v", sawLegacyAttempt, sawFallbackAttempt, sawImport, sawTrust)
		}
		if _, err := os.Stat(observedTempDir); !os.IsNotExist(err) {
			t.Fatalf("expected temp dir %s to be removed after return", observedTempDir)
		}
		cfgData, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(CodesignIdentityRelPath)))
		if err != nil || strings.TrimSpace(string(cfgData)) != DefaultLocalCodesignIdentity {
			t.Fatalf("unexpected persisted config: data=%q err=%v", string(cfgData), err)
		}
	})
}

func TestUpdaterPaths_CodesignStagedBinaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	kcDir := filepath.Join(home, "Library", "Keychains")
	_ = os.MkdirAll(kcDir, 0700)
	_ = os.WriteFile(filepath.Join(kcDir, "login.keychain-db"), []byte("kc"), 0600)

	const sha = "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678"
	var signed []string
	setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if name == "security" && args[0] == "find-identity" {
			return []byte("  1) " + sha + ` "Ackbar Local Code Signing"` + "\n"), nil
		}
		if name == "codesign" && args[0] == "--force" {
			signed = append(signed, args[len(args)-1])
		}
		if name == "codesign" && args[0] == "-d" {
			return []byte(`designated => identifier "x" and anchor apple`), nil
		}
		return nil, nil
	})

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, name := range []string{"ackbar", "ackbard"} {
		body := []byte("#!/bin/sh\n")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gw.Close()

	primaryDir := t.TempDir()
	extracted, err := ExtractTarGz(&buf, primaryDir, []string{"ackbar", "ackbard"})
	if err != nil || len(extracted) != 2 {
		t.Fatalf("ExtractTarGz got (%v, %v)", extracted, err)
	}
	if len(signed) != 2 {
		t.Fatalf("expected 2 staged .tmp-* binaries signed during ExtractTarGz, got %v", signed)
	}
	for _, p := range signed {
		if !strings.Contains(filepath.Base(p), ".tmp-") {
			t.Errorf("expected staged .tmp-* path, got %q", p)
		}
	}

	secDir := filepath.Join(home, ".local", "bin")
	_ = os.MkdirAll(secDir, 0755)
	_ = os.WriteFile(filepath.Join(secDir, "ackbard"), []byte("stale"), 0755)
	signed = nil
	syncSecondaryInstallLocations(context.Background(), &UpdateResult{TargetDir: primaryDir}, "")
	if len(signed) != 2 {
		t.Fatalf("expected 2 staged .sync-* binaries signed during syncSecondaryInstallLocations, got %v", signed)
	}
	for _, p := range signed {
		if !strings.Contains(filepath.Base(p), ".sync-") {
			t.Errorf("expected staged .sync-* path, got %q", p)
		}
	}

	// Cross-host staging (signLocal=false) must never invoke local codesigning
	var crossBuf bytes.Buffer
	gw2 := gzip.NewWriter(&crossBuf)
	tw2 := tar.NewWriter(gw2)
	body := []byte("#!/bin/sh\n")
	_ = tw2.WriteHeader(&tar.Header{Name: "ackbard", Mode: 0755, Size: int64(len(body))})
	_, _ = tw2.Write(body)
	_ = tw2.Close()
	_ = gw2.Close()

	signed = nil
	crossDir := t.TempDir()
	crossExtracted, err := extractTarGz(&crossBuf, crossDir, []string{"ackbard"}, false)
	if err != nil || len(crossExtracted) != 1 {
		t.Fatalf("extractTarGz(signLocal=false) got (%v, %v)", crossExtracted, err)
	}
	if len(signed) != 0 {
		t.Fatalf("expected zero codesign invocations when signLocal=false, got %v", signed)
	}
}

func TestCodesignInstalledDirStrict(t *testing.T) {
	const sha = "A1B2C3D4E5F60718293A4B5C6D7E8F9012345678"

	t.Run("rejects_non_darwin", func(t *testing.T) {
		setTestCodesignHooks(t, "linux", nil)
		if _, err := CodesignInstalledDirStrict(context.Background(), t.TempDir(), TargetBinaries, sha); err == nil {
			t.Fatal("expected error on non-darwin")
		}
	})

	t.Run("rejects_locked_keychain", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		kcDir := filepath.Join(home, "Library", "Keychains")
		_ = os.MkdirAll(kcDir, 0700)
		_ = os.WriteFile(filepath.Join(kcDir, "login.keychain-db"), []byte("kc"), 0600)

		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			if name == "security" && args[0] == "show-keychain-info" {
				return []byte("locked"), errors.New("exit 1")
			}
			return nil, nil
		})
		_, err := CodesignInstalledDirStrict(context.Background(), t.TempDir(), TargetBinaries, sha)
		if err == nil || !strings.Contains(err.Error(), "login keychain is locked") {
			t.Fatalf("expected locked keychain error, got %v", err)
		}
	})

	t.Run("ignores_env_none_and_propagates_errors", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("ACKBAR_CODESIGN_IDENTITY", "none")
		kcDir := filepath.Join(home, "Library", "Keychains")
		_ = os.MkdirAll(kcDir, 0700)
		_ = os.WriteFile(filepath.Join(kcDir, "login.keychain-db"), []byte("kc"), 0600)

		binDir := t.TempDir()
		_ = os.WriteFile(filepath.Join(binDir, "ackbar"), []byte("a"), 0755)
		_ = os.WriteFile(filepath.Join(binDir, "ackbard"), []byte("b"), 0755)

		var signArgs []string
		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			if name == "security" && args[0] == "find-identity" {
				t.Fatal("find-identity should not be consulted when explicitSHA1 is provided")
			}
			if name == "codesign" && args[0] == "--force" {
				signArgs = append(signArgs, strings.Join(args, " "))
			}
			if name == "codesign" && args[0] == "-d" {
				return []byte(`designated => identifier "x" and certificate root = H"` + strings.ToLower(sha) + `"`), nil
			}
			return nil, nil
		})

		signed, err := CodesignInstalledDirStrict(context.Background(), binDir, TargetBinaries, strings.ToLower(sha))
		if err != nil {
			t.Fatalf("CodesignInstalledDirStrict failed: %v", err)
		}
		if len(signed) != 2 || signed[0] != "ackbar" || signed[1] != "ackbard" {
			t.Fatalf("unexpected signed slice: %v", signed)
		}
		for _, call := range signArgs {
			if !strings.Contains(call, "--sign "+sha) {
				t.Errorf("expected explicit uppercase SHA1 %s in %q", sha, call)
			}
		}

		// Propagate codesign failure immediately
		setTestCodesignHooks(t, "darwin", func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
			if name == "codesign" && args[0] == "--force" {
				return []byte("errSecInternalComponent"), errors.New("exit 1")
			}
			return nil, nil
		})
		if _, err := CodesignInstalledDirStrict(context.Background(), binDir, TargetBinaries, sha); err == nil || !strings.Contains(err.Error(), "errSecInternalComponent") {
			t.Fatalf("expected strict error propagation, got %v", err)
		}
	})
}
