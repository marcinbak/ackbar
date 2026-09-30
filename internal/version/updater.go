package version

import (
	"archive/tar"
	"compress/gzip"
	"context"
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

	extracted, err := ExtractTarGz(resp.Body, destDir, TargetBinaries)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack and install binaries: %w", err)
	}

	// Gracefully shutdown local daemon if running so next invocation or service manager reloads new binary
	shutdownClient := &http.Client{Timeout: 1 * time.Second}
	_, _ = shutdownClient.Post("http://127.0.0.1:7777/v1/shutdown", "application/json", nil)

	return &UpdateResult{
		PreviousVersion: Version,
		NewVersion:      release.Version,
		Method:          "standalone",
		TargetDir:       destDir,
		UpdatedBinaries: extracted,
		Message:         fmt.Sprintf("Installed v%s (%d binaries) into %s", release.Version, len(extracted), destDir),
	}, nil
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

		baseName := filepath.Base(header.Name)
		if !targetMap[baseName] {
			continue
		}

		destPath := filepath.Join(destDir, baseName)
		tempPath := fmt.Sprintf("%s.tmp-%d", destPath, time.Now().UnixNano())

		outFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return extracted, fmt.Errorf("failed to create temporary file for %s: %w", baseName, err)
		}

		_, copyErr := io.Copy(outFile, tarReader)
		closeErr := outFile.Close()
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
