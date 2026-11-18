package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ackbar/internal/version"
)

type codesignSetupRunner struct {
	goos               string
	homeDir            string
	homebrewOptBinDirs []string
	stdout             io.Writer
	bootstrapIdentity  func(ctx context.Context, home, customIdentity string) (string, string, bool, error)
	isKeychainUnlocked func(ctx context.Context, home string) bool
	resolveIdentity    func(ctx context.Context, home string) (string, string, bool)
	codesignDir        func(ctx context.Context, dir string, binaries []string)
	inspectReq         func(ctx context.Context, filePath string) (string, error)
	verifySig          func(ctx context.Context, filePath string) error
	restartDaemon      func(out io.Writer) error
}

func newDefaultCodesignSetupRunner() *codesignSetupRunner {
	home, _ := os.UserHomeDir()
	return &codesignSetupRunner{
		goos:               runtime.GOOS,
		homeDir:            home,
		homebrewOptBinDirs: []string{"/opt/homebrew/opt/ackbar/bin", "/usr/local/opt/ackbar/bin"},
		stdout:             os.Stdout,
		bootstrapIdentity:  version.BootstrapLocalCodesignIdentity,
		isKeychainUnlocked: version.IsLoginKeychainUnlocked,
		resolveIdentity:    version.ResolveDarwinSigningIdentity,
		codesignDir:        version.CodesignInstalledDir,
		inspectReq:         version.InspectCodesignRequirement,
		verifySig:          version.VerifyBinarySignature,
		restartDaemon:      restartLocalDaemonAfterCodesign,
	}
}

func runSetupCodesignCmd(args []string) {
	if err := runSetupCodesign(args); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func runSetupCodesign(args []string) error {
	return newDefaultCodesignSetupRunner().run(args)
}

func (r *codesignSetupRunner) run(args []string) error {
	fs := flag.NewFlagSet("setup-codesign", flag.ContinueOnError)
	var flagErrBuf bytes.Buffer
	fs.SetOutput(&flagErrBuf)
	identityFlag := fs.String("identity", "", "Existing Keychain code signing identity name or 40-hex SHA-1")
	verifyFlag := fs.Bool("verify", false, "Verify configured signing identity and installed binaries")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(r.stdout, `Usage: ackbar setup-codesign [flags]

Configure a persistent macOS code signing identity so TCC permissions survive Ackbar updates.

Flags:
  --identity <name|sha1>  Use an existing Keychain code signing identity
  --verify                Verify identity and installed binary signatures without modifying Keychain
  -h, --help              Show this help message`)
			return nil
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(flagErrBuf.String()))
	}

	if r.goos != "darwin" {
		return fmt.Errorf("code signing setup is only supported on macOS (current OS: %s)", r.goos)
	}
	if r.homeDir == "" {
		return fmt.Errorf("unable to determine user home directory")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if *verifyFlag {
		return r.runVerify(ctx)
	}

	idName, sha1Hex, created, err := r.bootstrapIdentity(ctx, r.homeDir, *identityFlag)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(r.stdout, "✅ Created and trusted local signing identity %q (%s)\n", idName, sha1Hex)
	} else {
		fmt.Fprintf(r.stdout, "✅ Configured signing identity %q (%s)\n", idName, sha1Hex)
	}

	dirs := r.installDirs()
	if len(dirs) == 0 {
		fmt.Fprintln(r.stdout, "ℹ️  No installed Ackbar binaries found in ~/.local/bin or Homebrew opt/ackbar/bin.")
		return nil
	}

	for _, dir := range dirs {
		r.codesignDir(ctx, dir, version.TargetBinaries)
		var signedNames []string
		for _, binName := range version.TargetBinaries {
			p := filepath.Join(dir, binName)
			if info, statErr := os.Stat(p); statErr == nil && !info.IsDir() {
				signedNames = append(signedNames, binName)
			}
		}
		if len(signedNames) > 0 {
			fmt.Fprintf(r.stdout, "✅ Atomically signed binaries in %s: %s\n", dir, strings.Join(signedNames, ", "))
		}

		ackbardPath := filepath.Join(dir, "ackbard")
		if info, statErr := os.Stat(ackbardPath); statErr == nil && !info.IsDir() {
			reqOut, reqErr := r.inspectReq(ctx, ackbardPath)
			if reqErr != nil {
				return fmt.Errorf("failed to verify ackbard designated requirement (%s): %w", ackbardPath, reqErr)
			}
			trimmedReq := strings.TrimSpace(reqOut)
			if strings.Contains(strings.ToLower(trimmedReq), "cdhash") {
				return fmt.Errorf("ackbard (%s) designated requirement still pins cdhash: %s", ackbardPath, trimmedReq)
			}
			fmt.Fprintf(r.stdout, "✅ Verified persistent requirement (%s): %s\n", ackbardPath, trimmedReq)
		}
	}

	if r.restartDaemon != nil {
		if err := r.restartDaemon(r.stdout); err != nil {
			return err
		}
	}
	return nil
}

func (r *codesignSetupRunner) runVerify(ctx context.Context) error {
	unlocked := r.isKeychainUnlocked(ctx, r.homeDir)
	fmt.Fprintf(r.stdout, "Keychain unlocked: %v\n", unlocked)

	idName, sha1Hex, ok := r.resolveIdentity(ctx, r.homeDir)
	if !ok {
		return fmt.Errorf("no valid macOS code signing identity resolved (run 'ackbar setup-codesign')")
	}
	fmt.Fprintf(r.stdout, "Signing identity:  %s (%s)\n", idName, sha1Hex)

	dirs := r.installDirs()
	verifiedAckbard := false
	for _, dir := range dirs {
		for _, binName := range version.TargetBinaries {
			p := filepath.Join(dir, binName)
			info, err := os.Stat(p)
			if err != nil || info.IsDir() {
				continue
			}
			if err := r.verifySig(ctx, p); err != nil {
				return fmt.Errorf("signature verification failed for %s: %w", p, err)
			}
			reqOut, reqErr := r.inspectReq(ctx, p)
			if reqErr != nil {
				return fmt.Errorf("failed to read requirement for %s: %w", p, reqErr)
			}
			trimmedReq := strings.TrimSpace(reqOut)
			if strings.Contains(strings.ToLower(trimmedReq), "cdhash") {
				return fmt.Errorf("binary %s is pinned to per-build cdhash (%s); run 'ackbar setup-codesign' to re-sign", p, trimmedReq)
			}
			fmt.Fprintf(r.stdout, "✅ %s: %s\n", p, trimmedReq)
			if binName == "ackbard" {
				verifiedAckbard = true
			}
		}
	}
	if !verifiedAckbard {
		return fmt.Errorf("no installed ackbard binary found in ~/.local/bin or Homebrew opt/ackbar/bin")
	}
	return nil
}

func (r *codesignSetupRunner) installDirs() []string {
	var dirs []string
	seen := make(map[string]bool)
	addDir := func(d string) {
		if d == "" {
			return
		}
		if realDir, err := filepath.EvalSymlinks(d); err == nil && realDir != "" {
			d = realDir
		}
		d = filepath.Clean(d)
		if info, err := os.Stat(d); err == nil && info.IsDir() && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	if r.homeDir != "" {
		addDir(filepath.Join(r.homeDir, ".local", "bin"))
	}
	for _, brewDir := range r.homebrewOptBinDirs {
		addDir(brewDir)
	}
	return dirs
}

func restartLocalDaemonAfterCodesign(out io.Writer) error {
	token := localDaemonAuthToken()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	return restartLocalDaemonAfterCodesignWith(
		out,
		func() (string, string, string, bool) {
			return probeDaemonVersion(httpClient, localDaemonBaseURL, token)
		},
		func() (bool, bool, error) {
			return requestDaemonDrainShutdown(httpClient, localDaemonBaseURL, token)
		},
		tryRestartManagedDaemonService,
		func(runningHost, runningDisplayName string) {
			home, _ := os.UserHomeDir()
			ackbardBin := "ackbard"
			if home != "" {
				cand := filepath.Join(home, ".local", "bin", "ackbard")
				if _, err := os.Stat(cand); err == nil {
					ackbardBin = cand
				}
			}
			args := buildDaemonRestartArgs(home, runningHost, runningDisplayName)
			_ = exec.Command(ackbardBin, args...).Start()
		},
	)
}

func restartLocalDaemonAfterCodesignWith(
	out io.Writer,
	probeFn func() (ver, host, disp string, ok bool),
	drainFn func() (deferred, unauth bool, err error),
	restartManagedFn func() (managed, ready bool, err error),
	spawnFn func(runningHost, runningDisplayName string),
) error {
	_, runningHost, runningDisplayName, ok := probeFn()
	if !ok {
		return nil
	}
	fmt.Fprintln(out, "🔄 Restarting local ackbard daemon...")
	deferred, unauth, _ := drainFn()
	if unauth {
		fmt.Fprintln(out, "⚠️ Local ackbard requires ACKBAR_TOKEN to restart automatically; restart ackbard manually.")
		return nil
	}
	if deferred {
		fmt.Fprintln(out, "⏳ Active headless session turn(s) in progress — ackbard will restart automatically once the current turn finishes.")
		return nil
	}
	time.Sleep(400 * time.Millisecond)
	managed, ready, restartErr := restartManagedFn()
	if !managed {
		spawnFn(runningHost, runningDisplayName)
		return nil
	}
	if restartErr != nil {
		return fmt.Errorf("managed ackbard service restart failed: %w", restartErr)
	}
	if !ready {
		fmt.Fprintln(out, "⚠️ Managed ackbard service (launchd/systemd) is still starting on 127.0.0.1:7777...")
	}
	return nil
}
