package version

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// IsHomebrewInstalled reports whether the running binary is managed by Homebrew.
func IsHomebrewInstalled(execPath string) bool {
	cleanPath := filepath.Clean(execPath)
	return strings.Contains(cleanPath, "/Cellar/ackbar/") ||
		strings.Contains(cleanPath, "/opt/homebrew/Cellar/ackbar/") ||
		strings.Contains(cleanPath, "/opt/homebrew/opt/ackbar/") ||
		strings.Contains(cleanPath, "/usr/local/Cellar/ackbar/") ||
		strings.Contains(cleanPath, "/usr/local/opt/ackbar/")
}

// ExtractTarGz extracts a .tar.gz archive into destDir and returns the list of extracted file paths.
func ExtractTarGz(tarGzPath, destDir string) ([]string, error) {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var extracted []string

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create extraction directory: %w", err)
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed reading archive header: %w", err)
		}

		cleanName := filepath.Clean(header.Name)
		targetPath := filepath.Join(destDir, cleanName)

		rel, err := filepath.Rel(destDir, targetPath)
		if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
			return nil, fmt.Errorf("insecure path in archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return nil, fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return nil, fmt.Errorf("failed to create parent dir for %s: %w", targetPath, err)
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return nil, fmt.Errorf("failed to create file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return nil, fmt.Errorf("failed writing file %s: %w", targetPath, err)
			}
			outFile.Close()
			extracted = append(extracted, targetPath)
		}
	}

	return extracted, nil
}

// VerifyChecksum computes SHA256 of the file and compares it with expected.
func VerifyChecksum(filePath, expectedSHA256 string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	sum := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(sum, strings.TrimSpace(expectedSHA256)) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedSHA256, sum)
	}
	return nil
}

// ParseChecksums parses a standard checksums.txt file mapping filename -> sha256.
func ParseChecksums(content string) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			hash := fields[0]
			name := filepath.Base(fields[1])
			result[name] = hash
		}
	}
	return result
}

// DownloadFile downloads a URL to a local destination file.
// Uses a dedicated http.Client so the caller's context controls the overall download timeout.
func DownloadFile(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("ackbar/%s", Version))

	dlClient := &http.Client{}
	resp, err := dlClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// RunUpdate performs discovery and executes the upgrade.
func RunUpdate(ctx context.Context, force bool, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}

	fmt.Fprintf(out, "🔍 Checking for latest Ackbar release on GitHub...\n")
	res, err := CheckLatestRelease(ctx, true)
	if err != nil && res == nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !force && !res.UpdateAvailable {
		fmt.Fprintf(out, "✅ Ackbar is already up to date (current: v%s, latest: v%s).\n", Version, res.LatestVersion)
		return nil
	}

	fmt.Fprintf(out, "🚀 Found new version: v%s (currently running v%s)\n", res.LatestVersion, Version)

	execPath, err := os.Executable()
	if err == nil {
		execPath, _ = filepath.EvalSymlinks(execPath)
	}

	// 1. Homebrew installation
	if IsHomebrewInstalled(execPath) {
		fmt.Fprintf(out, "🍺 Detected Homebrew installation. Upgrading via brew...\n")
		cmd := exec.CommandContext(ctx, "brew", "upgrade", "ackbar")
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("brew upgrade failed: %w", err)
		}
		_ = exec.CommandContext(ctx, "brew", "services", "restart", "ackbar").Run()
		fmt.Fprintf(out, "✅ Successfully updated Ackbar via Homebrew!\n")
		return nil
	}

	// 2. Standalone prebuilt binary installation
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	asset := FindAssetForPlatform(res.ReleaseInfo, goos, goarch)
	if asset == nil {
		return fmt.Errorf("no prebuilt release archive found for %s/%s in release v%s", goos, goarch, res.LatestVersion)
	}

	tmpDir, err := os.MkdirTemp("", "ackbar-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, filepath.Base(asset.Name))
	fmt.Fprintf(out, "⬇️  Downloading %s...\n", asset.Name)
	if err := DownloadFile(ctx, asset.BrowserDownloadURL, archivePath); err != nil {
		return fmt.Errorf("failed to download archive: %w", err)
	}

	// Verify Checksums if available
	chkAsset := FindChecksumsAsset(res.ReleaseInfo)
	if chkAsset != nil {
		chkPath := filepath.Join(tmpDir, "checksums.txt")
		if err := DownloadFile(ctx, chkAsset.BrowserDownloadURL, chkPath); err != nil {
			return fmt.Errorf("failed to download checksums: %w", err)
		}
		content, err := os.ReadFile(chkPath)
		if err != nil {
			return fmt.Errorf("failed to read checksums: %w", err)
		}
		checksumMap := ParseChecksums(string(content))
		expectedHash, ok := checksumMap[asset.Name]
		if !ok {
			expectedHash, ok = checksumMap[filepath.Base(asset.Name)]
		}
		if !ok {
			return fmt.Errorf("asset %s not found in checksums.txt", asset.Name)
		}
		fmt.Fprintf(out, "🔒 Verifying SHA256 checksum...\n")
		if err := VerifyChecksum(archivePath, expectedHash); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
		fmt.Fprintf(out, "  ✓ Checksum verified\n")
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	fmt.Fprintf(out, "📦 Extracting release archive...\n")
	extractedFiles, err := ExtractTarGz(archivePath, extractDir)
	if err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}

	// Target directory resolution: default to ~/.local/bin if current exec is temp/dev
	targetDir := filepath.Dir(execPath)
	if targetDir == "" || strings.Contains(targetDir, "go-build") || strings.HasPrefix(targetDir, "/tmp") {
		home, _ := os.UserHomeDir()
		if home != "" {
			targetDir = filepath.Join(home, ".local", "bin")
		} else {
			targetDir = "/usr/local/bin"
		}
	}
	_ = os.MkdirAll(targetDir, 0755)

	binaries := []string{"ackbar", "ackbard", "ackbar-hook", "ackbar-relay"}
	installedCount := 0

	for _, binName := range binaries {
		var srcPath string
		for _, f := range extractedFiles {
			if filepath.Base(f) == binName {
				srcPath = f
				break
			}
		}
		if srcPath == "" {
			continue
		}

		dstPath := filepath.Join(targetDir, binName)
		tmpDst := dstPath + ".new"

		if err := copyExecutable(srcPath, tmpDst); err != nil {
			return fmt.Errorf("failed installing %s: %w", binName, err)
		}

		if err := os.Rename(tmpDst, dstPath); err != nil {
			// On Windows or cross-device rename, copy over
			if err := copyExecutable(srcPath, dstPath); err != nil {
				return fmt.Errorf("failed replacing %s: %w", dstPath, err)
			}
			_ = os.Remove(tmpDst)
		}
		fmt.Fprintf(out, "  ✓ Installed %s -> %s\n", binName, dstPath)
		installedCount++
	}

	if installedCount == 0 {
		return fmt.Errorf("no binaries were found in release archive to install")
	}

	// 3. Seamless Daemon Restart
	fmt.Fprintf(out, "🔄 Restarting ackbard daemon service...\n")
	restartDaemonService()

	fmt.Fprintf(out, "\n🎉 Successfully updated Ackbar from v%s to v%s!\n", Version, res.LatestVersion)
	return nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, 0755)
}

func restartDaemonService() {
	// First signal HTTP shutdown so running daemon exits cleanly
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, _ = client.Post("http://127.0.0.1:7777/v1/shutdown", "application/json", nil)
	time.Sleep(300 * time.Millisecond)

	switch runtime.GOOS {
	case "darwin":
		uid := os.Getuid()
		_ = exec.Command("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/com.marcinbak.ackbard", uid)).Run()
	case "linux":
		_ = exec.Command("systemctl", "--user", "restart", "ackbard").Run()
	}
}
