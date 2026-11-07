package version

import (
	"archive/tar"
	"bufio"
	"bytes"
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
	"strings"
	"time"
)

var TargetBinaries = []string{"ackbar", "ackbard", "ackbar-hook", "ackbar-relay"}

// UpdateResult captures the outcome of an update execution.
type UpdateResult struct {
	PreviousVersion string   `json:"previous_version"`
	NewVersion      string   `json:"new_version"`
	Method          string   `json:"method"` // "homebrew" or "standalone"
	TargetDir       string   `json:"target_dir,omitempty"`
	UpdatedBinaries []string `json:"updated_binaries,omitempty"`
	Message         string   `json:"message"`
}

// UpdateToLatest checks the installation type and runs either Homebrew upgrade or prebuilt binary download.
func UpdateToLatest(ctx context.Context, destDir string) (*UpdateResult, error) {
	installType, realPath := DetectInstallType()
	var res *UpdateResult
	var err error
	if installType == "homebrew" {
		res, err = RunHomebrewUpgrade(ctx)
	} else {
		res, err = DownloadAndInstall(ctx, nil, destDir)
	}
	if err != nil {
		return nil, err
	}

	// Ensure secondary binary locations (~/.local/bin or /opt/homebrew/opt/ackbar/bin) do not stay stale
	// if both exist on the same host.
	syncSecondaryInstallLocations(ctx, res, realPath)
	return res, nil
}

func findBrewBinary() string {
	candidates := []string{
		"/opt/homebrew/bin/brew",
		"/usr/local/bin/brew",
		"/home/linuxbrew/.linuxbrew/bin/brew",
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath("brew"); err == nil {
		return p
	}
	return ""
}

func probeInstalledBinaryVersion(ctx context.Context, binPath string) string {
	if binPath == "" {
		return ""
	}
	pCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pCtx, binPath, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) > 0 {
		return CleanVersion(fields[len(fields)-1])
	}
	return ""
}

func resolveHomebrewBinDir() string {
	candidates := []string{
		"/opt/homebrew/opt/ackbar/bin",
		"/usr/local/opt/ackbar/bin",
		"/home/linuxbrew/.linuxbrew/opt/ackbar/bin",
	}
	for _, c := range candidates {
		if realDir, err := filepath.EvalSymlinks(c); err == nil && realDir != "" {
			if st, serr := os.Stat(realDir); serr == nil && st.IsDir() {
				return realDir
			}
		}
	}
	_, realPath := DetectInstallType()
	if realPath != "" {
		return filepath.Dir(realPath)
	}
	return ""
}

// RunHomebrewUpgrade upgrades Ackbar via Homebrew, falling back to direct release asset installation
// if Homebrew's tap formula lags behind the latest GitHub Release or if brew fails.
func RunHomebrewUpgrade(ctx context.Context) (*UpdateResult, error) {
	release, _ := CheckLatestRelease(ctx, true)
	targetVer := ""
	if release != nil {
		targetVer = CleanVersion(release.Version)
	}

	brewBin := findBrewBinary()
	var brewOut string
	var brewErr error

	if brewBin != "" {
		// Refresh Homebrew tap metadata first so recently merged formula updates are discovered
		updCtx, updCancel := context.WithTimeout(ctx, 30*time.Second)
		_ = exec.CommandContext(updCtx, brewBin, "update").Run()
		updCancel()

		cmd := exec.CommandContext(ctx, brewBin, "upgrade", "ackbar")
		out, err := cmd.CombinedOutput()
		brewOut = strings.TrimSpace(string(out))
		brewErr = err
	} else {
		brewErr = fmt.Errorf("brew executable not found")
	}

	// Verify whether the installed binary actually reached targetVer
	brewBinDir := resolveHomebrewBinDir()
	installedVer := ""
	if brewBinDir != "" {
		installedVer = probeInstalledBinaryVersion(ctx, filepath.Join(brewBinDir, "ackbard"))
	}
	if installedVer == "" {
		for _, candidate := range []string{"/opt/homebrew/bin/ackbard", "/usr/local/bin/ackbard"} {
			if v := probeInstalledBinaryVersion(ctx, candidate); v != "" {
				installedVer = v
				break
			}
		}
	}

	// If brew succeeded AND installed binary is at least targetVer, return success.
	if brewErr == nil && installedVer != "" && (targetVer == "" || !IsNewer(targetVer, installedVer)) {
		return &UpdateResult{
			PreviousVersion: Version,
			NewVersion:      installedVer,
			Method:          "homebrew",
			TargetDir:       brewBinDir,
			Message:         fmt.Sprintf("Homebrew upgrade complete (v%s):\n%s", installedVer, brewOut),
		}, nil
	}

	// Fallback: if brew failed OR Formula/ackbar.rb in tap is still older than the GitHub Release,
	// download and install the prebuilt release binaries directly into the Homebrew Cellar bin directory.
	if release != nil && release.AssetURL != "" {
		fallbackDir := brewBinDir
		if fallbackDir == "" {
			if home, _ := os.UserHomeDir(); home != "" {
				fallbackDir = filepath.Join(home, ".local", "bin")
			}
		}
		dlRes, dlErr := DownloadAndInstall(ctx, release, fallbackDir)
		if dlErr == nil {
			dlRes.Method = "homebrew+release-fallback"
			dlRes.Message = fmt.Sprintf("Upgraded Homebrew installation directly from GitHub Release v%s into %s", release.Version, fallbackDir)
			return dlRes, nil
		}
	}

	if brewErr != nil {
		return nil, fmt.Errorf("brew upgrade ackbar failed: %s (%w)", brewOut, brewErr)
	}

	newVer := installedVer
	if newVer == "" {
		newVer = targetVer
	}
	if newVer == "" {
		newVer = "latest"
	}
	return &UpdateResult{
		PreviousVersion: Version,
		NewVersion:      newVer,
		Method:          "homebrew",
		TargetDir:       brewBinDir,
		Message:         fmt.Sprintf("Homebrew upgrade complete:\n%s", brewOut),
	}, nil
}

// syncSecondaryInstallLocations ensures that if a machine has both ~/.local/bin/ackbard and
// a Homebrew installation, both binary directories are updated to prevent split-brain versions.
func syncSecondaryInstallLocations(ctx context.Context, primaryRes *UpdateResult, primaryExePath string) {
	if primaryRes == nil || primaryRes.TargetDir == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	secondaryDirs := []string{
		filepath.Join(home, ".local", "bin"),
	}
	if brewDir := resolveHomebrewBinDir(); brewDir != "" {
		secondaryDirs = append(secondaryDirs, brewDir)
	}

	for _, secDir := range secondaryDirs {
		if secDir == "" || filepath.Clean(secDir) == filepath.Clean(primaryRes.TargetDir) {
			continue
		}
		secAckbard := filepath.Join(secDir, "ackbard")
		if _, err := os.Stat(secAckbard); err != nil {
			continue
		}
		// Copy updated binaries from primaryRes.TargetDir into secDir atomically
		for _, binName := range TargetBinaries {
			srcPath := filepath.Join(primaryRes.TargetDir, binName)
			data, rErr := os.ReadFile(srcPath)
			if rErr != nil {
				continue
			}
			tmpFile, tErr := os.CreateTemp(secDir, binName+".sync-*")
			if tErr != nil {
				continue
			}
			tmpPath := tmpFile.Name()
			_, wErr := tmpFile.Write(data)
			_ = tmpFile.Close()
			if wErr != nil {
				_ = os.Remove(tmpPath)
				continue
			}
			_ = os.Chmod(tmpPath, 0755)
			_ = os.Rename(tmpPath, filepath.Join(secDir, binName))
		}
	}
}

// DownloadAndInstallForPlatform downloads and extracts the release archive for a specific OS/architecture into destDir.
func DownloadAndInstallForPlatform(ctx context.Context, targetVersion, goos, goarch, destDir string) (*UpdateResult, error) {
	rel, err := BuildReleaseInfoForPlatform(ctx, targetVersion, goos, goarch)
	if err != nil {
		return nil, err
	}
	return DownloadAndInstall(ctx, rel, destDir)
}

// DownloadAndInstall downloads the prebuilt release tarball from GitHub and installs binaries to destDir.
func DownloadAndInstall(ctx context.Context, release *ReleaseInfo, destDir string) (*UpdateResult, error) {
	if release == nil {
		var err error
		release, err = CheckLatestRelease(ctx, true)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch release information: %w", err)
		}
	}

	if release.AssetURL == "" {
		return nil, fmt.Errorf("no release asset URL available for %s", release.Version)
	}

	if destDir == "" {
		exe, err := os.Executable()
		if err == nil {
			realPath, err := filepath.EvalSymlinks(exe)
			if err == nil && realPath != "" {
				destDir = filepath.Dir(realPath)
			} else {
				destDir = filepath.Dir(exe)
			}
		}
		if destDir == "" || destDir == "." {
			home, _ := os.UserHomeDir()
			if home != "" {
				destDir = filepath.Join(home, ".local", "bin")
			}
		}
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target install directory %s: %w", destDir, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.AssetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("ackbar/%s", Version))

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download release package: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with HTTP %d from %s", resp.StatusCode, release.AssetURL)
	}

	archiveBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read release package stream: %w", err)
	}

	// Verify SHA-256 against release checksums.txt if available
	if err := verifyArchiveChecksum(ctx, release, archiveBytes); err != nil {
		return nil, fmt.Errorf("security verification failed: %w", err)
	}

	extracted, err := ExtractTarGz(bytes.NewReader(archiveBytes), destDir, TargetBinaries)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack and install binaries: %w", err)
	}

	if len(extracted) == 0 {
		return nil, fmt.Errorf("no target binaries found in release archive for %s", release.Version)
	}

	return &UpdateResult{
		PreviousVersion: Version,
		NewVersion:      release.Version,
		Method:          "standalone",
		TargetDir:       destDir,
		UpdatedBinaries: extracted,
		Message:         fmt.Sprintf("Installed v%s (%d binaries) into %s", release.Version, len(extracted), destDir),
	}, nil
}

// verifyArchiveChecksum checks the SHA-256 hash of archiveBytes against release.ChecksumURL.
func verifyArchiveChecksum(ctx context.Context, release *ReleaseInfo, archiveBytes []byte) error {
	if release.ChecksumURL == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.ChecksumURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", fmt.Sprintf("ackbar/%s", Version))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	hasher := sha256.New()
	hasher.Write(archiveBytes)
	actualHash := hex.EncodeToString(hasher.Sum(nil))

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			expectedHash := parts[0]
			assetName := strings.TrimPrefix(parts[1], "*")
			if assetName == release.AssetName {
				if !strings.EqualFold(expectedHash, actualHash) {
					return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", release.AssetName, expectedHash, actualHash)
				}
				return nil
			}
		}
	}

	return nil
}

// ExtractTarGz extracts specified binaryNames from a gzipped tar archive into destDir atomically.
func ExtractTarGz(r io.Reader, destDir string, binaryNames []string) ([]string, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("invalid gzip archive: %w", err)
	}
	defer gzr.Close()

	tarReader := tar.NewReader(gzr)
	targetMap := make(map[string]bool)
	for _, b := range binaryNames {
		targetMap[b] = true
	}

	var extracted []string

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return extracted, fmt.Errorf("tar read error: %w", err)
		}

		// Skip non-regular files to prevent symlink or directory replacement attacks
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}

		baseName := filepath.Base(header.Name)
		if !targetMap[baseName] {
			continue
		}

		destPath := filepath.Join(destDir, baseName)
		tempFile, err := os.CreateTemp(destDir, baseName+".tmp-*")
		if err != nil {
			return extracted, fmt.Errorf("failed to create temporary file for %s: %w", baseName, err)
		}
		tempPath := tempFile.Name()

		_, copyErr := io.Copy(tempFile, tarReader)
		closeErr := tempFile.Close()
		if copyErr != nil {
			_ = os.Remove(tempPath)
			return extracted, fmt.Errorf("failed to write %s: %w", baseName, copyErr)
		}
		if closeErr != nil {
			_ = os.Remove(tempPath)
			return extracted, fmt.Errorf("failed to close %s: %w", baseName, closeErr)
		}

		// Set executable permissions
		_ = os.Chmod(tempPath, 0755)

		// Atomic replace
		if err := os.Rename(tempPath, destPath); err != nil {
			_ = os.Remove(tempPath)
			return extracted, fmt.Errorf("failed to replace %s: %w", destPath, err)
		}

		extracted = append(extracted, baseName)
	}

	return extracted, nil
}
