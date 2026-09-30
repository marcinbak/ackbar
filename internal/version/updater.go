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
	installType, _ := DetectInstallType()
	if installType == "homebrew" {
		return RunHomebrewUpgrade(ctx)
	}
	return DownloadAndInstall(ctx, nil, destDir)
}

// RunHomebrewUpgrade upgrades Ackbar via Homebrew.
func RunHomebrewUpgrade(ctx context.Context) (*UpdateResult, error) {
	cmd := exec.CommandContext(ctx, "brew", "upgrade", "ackbar")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("brew upgrade ackbar failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Try restarting background service if managed by brew services
	_ = exec.CommandContext(ctx, "brew", "services", "restart", "ackbar").Run()

	return &UpdateResult{
		PreviousVersion: Version,
		NewVersion:      "latest",
		Method:          "homebrew",
		Message:         fmt.Sprintf("Homebrew upgrade complete:\n%s", strings.TrimSpace(string(out))),
	}, nil
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
