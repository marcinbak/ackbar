package version

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	DefaultLocalCodesignIdentity = "Ackbar Local Code Signing"
	CodesignIdentityRelPath      = ".config/ackbar/codesign-identity"
)

type codesignRunner struct {
	goos   string
	runCmd func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

type codesignIdentity struct {
	SHA1 string
	Name string
}

var (
	codesignHooksMu sync.RWMutex
	codesignHooks   = codesignRunner{}

	validBinaryNameRegex    = regexp.MustCompile(`^[a-z0-9_-]+$`)
	validHexSHA1Regex       = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)
	securityIdentityLineReg = regexp.MustCompile(`^\s*\d+\)\s+([0-9A-Fa-f]{40})\s+"([^"]+)"\s*$`)
)

func isTestEnv() bool {
	return flag.Lookup("test.v") != nil || os.Getenv("ACKBAR_TEST_MODE") == "1"
}

func currentCodesignHooks() codesignRunner {
	codesignHooksMu.RLock()
	h := codesignHooks
	codesignHooksMu.RUnlock()
	if h.goos == "" {
		h.goos = runtime.GOOS
	}
	return h
}

func execCodesignCmd(ctx context.Context, hooks codesignRunner, env []string, name string, args ...string) ([]byte, error) {
	if hooks.runCmd != nil {
		return hooks.runCmd(ctx, env, name, args...)
	}
	if isTestEnv() {
		return nil, fmt.Errorf("external codesign command %q disabled in test environment", name)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.CombinedOutput()
}

// BundleIdentifierForBinary maps an executable or staging filename to a validated macOS bundle identifier.
func BundleIdentifierForBinary(binName string) string {
	cleanName := filepath.Base(strings.TrimSpace(binName))
	for _, marker := range []string{".tmp-", ".sync-", ".cs-"} {
		if idx := strings.Index(cleanName, marker); idx > 0 {
			cleanName = cleanName[:idx]
		}
	}
	cleanName = strings.TrimSuffix(cleanName, ".new")
	if !validBinaryNameRegex.MatchString(cleanName) {
		return ""
	}
	switch cleanName {
	case "ackbard":
		return "com.marcinbak.ackbard"
	case "ackbar":
		return "com.marcinbak.ackbar"
	case "ackbar-hook":
		return "com.marcinbak.ackbar-hook"
	case "ackbar-relay":
		return "com.marcinbak.ackbar-relay"
	default:
		return "com.marcinbak." + cleanName
	}
}

func isDisabledCodesignIdentity(val string) bool {
	return val == "-" || strings.EqualFold(val, "none") || strings.EqualFold(val, "adhoc")
}

func isValidHexSHA1(val string) bool {
	return validHexSHA1Regex.MatchString(val)
}

func parseSecurityIdentities(output string) []codesignIdentity {
	var list []codesignIdentity
	for _, line := range strings.Split(output, "\n") {
		m := securityIdentityLineReg.FindStringSubmatch(line)
		if len(m) == 3 {
			list = append(list, codesignIdentity{
				SHA1: strings.ToUpper(m[1]),
				Name: m[2],
			})
		}
	}
	return list
}

func listValidDarwinIdentities(ctx context.Context, hooks codesignRunner) ([]codesignIdentity, error) {
	qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := execCodesignCmd(qCtx, hooks, nil, "security", "find-identity", "-v", "-p", "codesigning")
	if err != nil {
		return nil, err
	}
	return parseSecurityIdentities(string(out)), nil
}

func matchExplicitIdentity(identities []codesignIdentity, configured string) (codesignIdentity, bool) {
	wantSHA := isValidHexSHA1(configured)
	for _, id := range identities {
		if wantSHA {
			if strings.EqualFold(id.SHA1, configured) {
				return id, true
			}
		} else if id.Name == configured {
			return id, true
		}
	}
	return codesignIdentity{}, false
}

// ResolveDarwinSigningIdentity resolves the macOS code signing identity name and 40-hex SHA-1 fingerprint.
func ResolveDarwinSigningIdentity(ctx context.Context, home string) (identity string, certSHA1 string, ok bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	configured := strings.TrimSpace(os.Getenv("ACKBAR_CODESIGN_IDENTITY"))
	if isDisabledCodesignIdentity(configured) {
		return "", "", false
	}
	if configured == "" && home != "" {
		cfgPath := filepath.Join(home, filepath.FromSlash(CodesignIdentityRelPath))
		if data, err := os.ReadFile(cfgPath); err == nil {
			configured = strings.TrimSpace(string(data))
		}
		if isDisabledCodesignIdentity(configured) {
			return "", "", false
		}
	}

	hooks := currentCodesignHooks()
	identities, err := listValidDarwinIdentities(ctx, hooks)
	if err != nil || len(identities) == 0 {
		return "", "", false
	}

	if configured != "" {
		if matched, found := matchExplicitIdentity(identities, configured); found {
			return matched.Name, matched.SHA1, true
		}
		return "", "", false
	}

	for _, id := range identities {
		if id.Name == DefaultLocalCodesignIdentity {
			return id.Name, id.SHA1, true
		}
	}
	for _, id := range identities {
		if strings.HasPrefix(id.Name, "Developer ID Application:") {
			return id.Name, id.SHA1, true
		}
	}
	for _, id := range identities {
		if strings.HasPrefix(id.Name, "Apple Development:") {
			return id.Name, id.SHA1, true
		}
	}
	return "", "", false
}

func resolveLoginKeychainPath(home string) string {
	for _, name := range []string{"login.keychain-db", "login.keychain"} {
		p := filepath.Join(home, "Library", "Keychains", name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// IsLoginKeychainUnlocked reports whether the user's login keychain can be accessed without GUI prompts.
func IsLoginKeychainUnlocked(ctx context.Context, home string) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil || h == "" {
			return false
		}
		home = h
	}
	keychainPath := resolveLoginKeychainPath(home)
	if keychainPath == "" {
		return false
	}
	hooks := currentCodesignHooks()
	qCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := execCodesignCmd(qCtx, hooks, nil, "security", "show-keychain-info", keychainPath)
	return err == nil
}

// InspectCodesignRequirement returns the embedded designated requirement output for filePath.
func InspectCodesignRequirement(ctx context.Context, filePath string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	hooks := currentCodesignHooks()
	qCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := execCodesignCmd(qCtx, hooks, nil, "codesign", "-d", "-r-", filePath)
	if err != nil {
		return string(out), fmt.Errorf("codesign -d -r- failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// VerifyBinarySignature validates the Mach-O signature of filePath.
func VerifyBinarySignature(ctx context.Context, filePath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	hooks := currentCodesignHooks()
	qCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := execCodesignCmd(qCtx, hooks, nil, "codesign", "--verify", "--strict", filePath)
	if err != nil {
		return fmt.Errorf("codesign verify failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CodesignBinaryFile signs a staged Mach-O binary on darwin with a persistent certificate-root requirement.
func CodesignBinaryFile(ctx context.Context, filePath, binName string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	hooks := currentCodesignHooks()
	if hooks.goos != "darwin" || (hooks.runCmd == nil && isTestEnv()) {
		return nil
	}
	if filePath == "" {
		return fmt.Errorf("empty binary path")
	}
	bundleID := BundleIdentifierForBinary(binName)
	if bundleID == "" {
		return fmt.Errorf("invalid binary name %q for codesign identifier", binName)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	if !IsLoginKeychainUnlocked(ctx, home) {
		return nil
	}
	identity, certSHA1, ok := ResolveDarwinSigningIdentity(ctx, home)
	if !ok {
		return nil
	}
	signArg := certSHA1
	if signArg == "" {
		signArg = identity
	}

	sCtx, sCancel := context.WithTimeout(ctx, 5*time.Second)
	out, err := execCodesignCmd(sCtx, hooks, nil, "codesign", "--force", "--sign", signArg, "--identifier", bundleID, filePath)
	sCancel()
	if err != nil {
		return fmt.Errorf("codesign failed for %s: %w (%s)", filePath, err, strings.TrimSpace(string(out)))
	}

	reqOut, err := InspectCodesignRequirement(ctx, filePath)
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(reqOut), "cdhash") && isValidHexSHA1(certSHA1) {
		// Pin tccd to the signing certificate root instead of a per-build cdhash so updates preserve TCC grants.
		reqArg := fmt.Sprintf(`-r=designated => identifier "%s" and certificate root = H"%s"`, bundleID, strings.ToLower(certSHA1))
		rCtx, rCancel := context.WithTimeout(ctx, 5*time.Second)
		rOut, rErr := execCodesignCmd(rCtx, hooks, nil, "codesign", "--force", "--sign", signArg, "--identifier", bundleID, reqArg, filePath)
		rCancel()
		if rErr != nil {
			return fmt.Errorf("codesign requirement pinning failed for %s: %w (%s)", filePath, rErr, strings.TrimSpace(string(rOut)))
		}
	}
	return nil
}

// CodesignInstalledBinaryAtomic signs a copy of targetPath before replacing it so live vnodes stay valid on Apple Silicon.
func CodesignInstalledBinaryAtomic(ctx context.Context, targetPath, binName string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	hooks := currentCodesignHooks()
	if hooks.goos != "darwin" || (hooks.runCmd == nil && isTestEnv()) {
		return nil
	}
	if targetPath == "" {
		return fmt.Errorf("empty binary path")
	}
	realTarget := targetPath
	if resolved, err := filepath.EvalSymlinks(targetPath); err == nil && resolved != "" {
		realTarget = resolved
	}
	if binName == "" {
		binName = filepath.Base(realTarget)
	}
	if BundleIdentifierForBinary(binName) == "" {
		return fmt.Errorf("invalid binary name %q for codesign identifier", binName)
	}
	home, _ := os.UserHomeDir()
	if home == "" || !IsLoginKeychainUnlocked(ctx, home) {
		return nil
	}
	if _, _, ok := ResolveDarwinSigningIdentity(ctx, home); !ok {
		return nil
	}

	src, err := os.Open(realTarget)
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(realTarget), filepath.Base(binName)+".cs-*")
	if err != nil {
		_ = src.Close()
		return err
	}
	tmpPath := tmpFile.Name()

	_, copyErr := io.Copy(tmpFile, src)
	srcCloseErr := src.Close()
	tmpCloseErr := tmpFile.Close()
	if copyErr != nil || srcCloseErr != nil || tmpCloseErr != nil {
		_ = os.Remove(tmpPath)
		if copyErr != nil {
			return copyErr
		}
		if srcCloseErr != nil {
			return srcCloseErr
		}
		return tmpCloseErr
	}

	if err := CodesignBinaryFile(ctx, tmpPath, binName); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, realTarget); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// CodesignInstalledDir atomically signs all existing Ackbar binaries inside dir on best-effort basis.
func CodesignInstalledDir(ctx context.Context, dir string, binaries []string) {
	if dir == "" {
		return
	}
	for _, binName := range binaries {
		p := filepath.Join(dir, binName)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			_ = CodesignInstalledBinaryAtomic(ctx, p, binName)
		}
	}
}

func writeCodesignConfigFile(home, identity string) error {
	cfgPath := filepath.Join(home, filepath.FromSlash(CodesignIdentityRelPath))
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(cfgPath), ".codesign-identity.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.WriteString(identity + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, cfgPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Chmod(cfgPath, 0600)
}

func generateSelfSignedCodesignCert(commonName string) (certPEM, keyPEM []byte, certSHA1 string, err error) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, "", err
	}
	now := time.Now().UTC()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &privKey.PublicKey, privKey)
	if err != nil {
		return nil, nil, "", err
	}
	keyDER := x509.MarshalPKCS1PrivateKey(privKey)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})
	sum := sha1.Sum(derBytes)
	certSHA1 = strings.ToUpper(hex.EncodeToString(sum[:]))
	return certPEM, keyPEM, certSHA1, nil
}

// BootstrapLocalCodesignIdentity ensures a trusted persistent macOS code signing identity exists and is configured.
func BootstrapLocalCodesignIdentity(ctx context.Context, home, customIdentity string) (identityName string, certSHA1 string, created bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	hooks := currentCodesignHooks()
	if hooks.goos != "darwin" {
		return "", "", false, fmt.Errorf("local code signing setup is only supported on macOS (darwin)")
	}
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil || home == "" {
			return "", "", false, fmt.Errorf("unable to determine home directory")
		}
	}

	customIdentity = strings.TrimSpace(customIdentity)
	identities, listErr := listValidDarwinIdentities(ctx, hooks)

	if customIdentity != "" {
		if listErr != nil {
			return "", "", false, fmt.Errorf("failed to query codesigning identities: %w", listErr)
		}
		matched, found := matchExplicitIdentity(identities, customIdentity)
		if !found {
			return "", "", false, fmt.Errorf("codesigning identity %q not found in Keychain", customIdentity)
		}
		if err := writeCodesignConfigFile(home, customIdentity); err != nil {
			return "", "", false, fmt.Errorf("failed to persist codesigning identity: %w", err)
		}
		return matched.Name, matched.SHA1, false, nil
	}

	if matched, found := matchExplicitIdentity(identities, DefaultLocalCodesignIdentity); found {
		if err := writeCodesignConfigFile(home, DefaultLocalCodesignIdentity); err != nil {
			return "", "", false, fmt.Errorf("failed to persist codesigning identity: %w", err)
		}
		return matched.Name, matched.SHA1, false, nil
	}

	certPEM, keyPEM, expectedSHA1, err := generateSelfSignedCodesignCert(DefaultLocalCodesignIdentity)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to generate local codesigning certificate: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "ackbar-codesign-*")
	if err != nil {
		return "", "", false, err
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0700); err != nil {
		return "", "", false, err
	}

	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	p12Path := filepath.Join(tmpDir, "cert.p12")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return "", "", false, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return "", "", false, err
	}

	passBytes := make([]byte, 24)
	if _, err := rand.Read(passBytes); err != nil {
		return "", "", false, err
	}
	passphrase := hex.EncodeToString(passBytes)
	p12Env := []string{"ACKBAR_P12_PASS=" + passphrase}

	p12Ctx, p12Cancel := context.WithTimeout(ctx, 10*time.Second)
	p12Out, p12Err := execCodesignCmd(p12Ctx, hooks, p12Env,
		"openssl", "pkcs12", "-export",
		"-out", p12Path,
		"-inkey", keyPath,
		"-in", certPath,
		"-name", DefaultLocalCodesignIdentity,
		"-passout", "env:ACKBAR_P12_PASS",
		"-legacy",
	)
	p12Cancel()
	if p12Err != nil {
		// LibreSSL rejects -legacy while OpenSSL 3+ requires it for macOS Keychain PKCS#12 compatibility.
		p12Ctx2, p12Cancel2 := context.WithTimeout(ctx, 10*time.Second)
		p12Out2, p12Err2 := execCodesignCmd(p12Ctx2, hooks, p12Env,
			"openssl", "pkcs12", "-export",
			"-out", p12Path,
			"-inkey", keyPath,
			"-in", certPath,
			"-name", DefaultLocalCodesignIdentity,
			"-passout", "env:ACKBAR_P12_PASS",
		)
		p12Cancel2()
		if p12Err2 != nil {
			return "", "", false, fmt.Errorf("openssl pkcs12 export failed: %w (%s; fallback: %s)", p12Err2, strings.TrimSpace(string(p12Out)), strings.TrimSpace(string(p12Out2)))
		}
	}

	keychainPath := resolveLoginKeychainPath(home)
	if keychainPath == "" {
		keychainPath = filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	}

	impCtx, impCancel := context.WithTimeout(ctx, 10*time.Second)
	impOut, impErr := execCodesignCmd(impCtx, hooks, nil,
		"security", "import", p12Path,
		"-k", keychainPath,
		"-P", passphrase,
		"-T", "/usr/bin/codesign",
	)
	impCancel()
	if impErr != nil {
		return "", "", false, fmt.Errorf("security import failed: %w (%s)", impErr, strings.TrimSpace(string(impOut)))
	}

	trustCtx, trustCancel := context.WithTimeout(ctx, 60*time.Second)
	trustOut, trustErr := execCodesignCmd(trustCtx, hooks, nil,
		"security", "add-trusted-cert",
		"-r", "trustRoot",
		"-p", "codeSign",
		"-k", keychainPath,
		certPath,
	)
	trustCancel()
	if trustErr != nil {
		return "", "", false, fmt.Errorf("security add-trusted-cert failed: %w (%s)", trustErr, strings.TrimSpace(string(trustOut)))
	}

	postIdentities, err := listValidDarwinIdentities(ctx, hooks)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to verify imported codesigning identity: %w", err)
	}
	matched, found := matchExplicitIdentity(postIdentities, expectedSHA1)
	if !found {
		matched, found = matchExplicitIdentity(postIdentities, DefaultLocalCodesignIdentity)
	}
	if !found {
		return "", "", false, fmt.Errorf("created certificate %q (%s) not found in valid codesigning identities", DefaultLocalCodesignIdentity, expectedSHA1)
	}

	if err := writeCodesignConfigFile(home, DefaultLocalCodesignIdentity); err != nil {
		return "", "", false, fmt.Errorf("failed to persist codesigning identity: %w", err)
	}
	return matched.Name, matched.SHA1, true, nil
}
