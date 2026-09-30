package version

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed VERSION
var rawVersion string

// Version follows date-based versioning YYYYMMDD.rev loaded from the VERSION file
var Version = strings.TrimSpace(rawVersion)

var (
	GitHubOwner   = "marcinbak"
	GitHubRepo    = "ackbar"
	ReleaseAPI    = "https://api.github.com/repos/marcinbak/ackbar/releases/latest"
	RawVersionURL = "https://raw.githubusercontent.com/marcinbak/ackbar/main/VERSION"
)

// ReleaseInfo contains discovery metadata for an available Ackbar release.
type ReleaseInfo struct {
	Version     string    `json:"version"`
	TagName     string    `json:"tag_name"`
	ReleaseURL  string    `json:"release_url"`
	PublishedAt time.Time `json:"published_at"`
	Changelog   string    `json:"changelog"`
	AssetURL    string    `json:"asset_url"`
	AssetName   string    `json:"asset_name"`
	IsNewer     bool      `json:"is_newer"`
	IsHomebrew  bool      `json:"is_homebrew"`
	InstallType string    `json:"install_type"`
	CheckedAt   time.Time `json:"checked_at"`
}

var (
	cacheMu       sync.RWMutex
	cachedRelease *ReleaseInfo
	cacheTTL      = 1 * time.Hour
)

// CleanVersion removes 'v' prefixes and whitespace from version strings.
func CleanVersion(v string) string {
	v = strings.TrimSpace(v)
	return strings.TrimPrefix(v, "v")
}

// Compare compares two version strings chronologically.
// Supports Ackbar date-based versions (YYYYMMDD.rev, e.g. 20260930.02 vs 20260930.01)
// as well as standard semantic versions (v1.2.3 vs v1.2.4).
// Returns:
//
//	 1 if vA > vB
//	-1 if vA < vB
//	 0 if vA == vB
func Compare(vA, vB string) int {
	a := CleanVersion(vA)
	b := CleanVersion(vB)
	if a == b {
		return 0
	}
	if a == "" && b != "" {
		return -1
	}
	if a != "" && b == "" {
		return 1
	}

	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := 0; i < maxLen; i++ {
		var segA, segB string
		if i < len(partsA) {
			segA = partsA[i]
		}
		if i < len(partsB) {
			segB = partsB[i]
		}

		numA, errA := strconv.ParseInt(segA, 10, 64)
		numB, errB := strconv.ParseInt(segB, 10, 64)

		if errA == nil && errB == nil {
			if numA > numB {
				return 1
			}
			if numA < numB {
				return -1
			}
			continue
		}

		// Fallback to lexicographical comparison for non-numeric components
		if segA > segB {
			return 1
		}
		if segA < segB {
			return -1
		}
	}

	return 0
}

// IsNewer returns true if candidate is newer than baseline.
func IsNewer(candidate, baseline string) bool {
	return Compare(candidate, baseline) > 0
}

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// CheckLatestRelease queries GitHub Releases (or cached state) to discover if an update is available.
func CheckLatestRelease(ctx context.Context, forceRefresh bool) (*ReleaseInfo, error) {
	cacheMu.RLock()
	if !forceRefresh && cachedRelease != nil && time.Since(cachedRelease.CheckedAt) < cacheTTL {
		info := *cachedRelease
		info.IsNewer = IsNewer(info.Version, Version)
		cacheMu.RUnlock()
		return &info, nil
	}
	cacheMu.RUnlock()

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ReleaseAPI, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", fmt.Sprintf("ackbar/%s", Version))

	resp, err := client.Do(req)
	var ghRel githubRelease
	var targetVer string

	if err == nil && resp.StatusCode == http.StatusOK {
		decErr := json.NewDecoder(resp.Body).Decode(&ghRel)
		resp.Body.Close()
		if decErr == nil && ghRel.TagName != "" {
			targetVer = CleanVersion(ghRel.TagName)
		}
	} else {
		if resp != nil {
			resp.Body.Close()
		}
		// Fallback: check raw VERSION on main branch
		rawReq, rawErr := http.NewRequestWithContext(ctx, http.MethodGet, RawVersionURL, nil)
		if rawErr == nil {
			rawReq.Header.Set("User-Agent", fmt.Sprintf("ackbar/%s", Version))
			if rawResp, doErr := client.Do(rawReq); doErr == nil && rawResp.StatusCode == http.StatusOK {
				bodyBytes, _ := io.ReadAll(rawResp.Body)
				rawResp.Body.Close()
				rawVal := strings.TrimSpace(string(bodyBytes))
				if rawVal != "" {
					targetVer = CleanVersion(rawVal)
					ghRel.TagName = "v" + targetVer
					ghRel.HTMLURL = fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", GitHubOwner, GitHubRepo, ghRel.TagName)
					ghRel.PublishedAt = time.Now()
				}
			}
		}
	}

	if targetVer == "" {
		return nil, fmt.Errorf("unable to retrieve latest release version from GitHub")
	}

	installType, _ := DetectInstallType()
	isHomebrew := installType == "homebrew"

	info := &ReleaseInfo{
		Version:     targetVer,
		TagName:     ghRel.TagName,
		ReleaseURL:  ghRel.HTMLURL,
		PublishedAt: ghRel.PublishedAt,
		Changelog:   strings.TrimSpace(ghRel.Body),
		IsNewer:     IsNewer(targetVer, Version),
		IsHomebrew:  isHomebrew,
		InstallType: installType,
		CheckedAt:   time.Now(),
	}

	// Match asset for current runtime OS and Architecture
	matchedAsset, matchedURL := resolvePlatformAsset(ghRel.Assets, targetVer, runtime.GOOS, runtime.GOARCH)
	info.AssetName = matchedAsset
	info.AssetURL = matchedURL

	cacheMu.Lock()
	cachedRelease = info
	cacheMu.Unlock()

	return info, nil
}

// resolvePlatformAsset matches the release asset corresponding to the target OS and architecture.
func resolvePlatformAsset(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}, version, goos, goarch string) (string, string) {
	expectedSuffix := fmt.Sprintf("%s_%s.tar.gz", goos, goarch)
	for _, a := range assets {
		if strings.HasSuffix(a.Name, expectedSuffix) {
			return a.Name, a.BrowserDownloadURL
		}
	}

	// Fallback: standard GitHub Release asset URL format created by GoReleaser
	fallbackName := fmt.Sprintf("ackbar_%s_%s_%s.tar.gz", version, goos, goarch)
	fallbackURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/%s", GitHubOwner, GitHubRepo, version, fallbackName)
	return fallbackName, fallbackURL
}

// DetectInstallType detects whether Ackbar was installed via Homebrew or as a standalone binary in local bin.
func DetectInstallType() (string, string) {
	exe, err := os.Executable()
	if err != nil {
		return "standalone", ""
	}

	realPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		realPath = exe
	}

	// Homebrew installation paths on Apple Silicon, Intel macOS, and Linux
	if strings.Contains(realPath, "/opt/homebrew/") ||
		strings.Contains(realPath, "/Cellar/") ||
		strings.Contains(realPath, "/home/linuxbrew/") {
		return "homebrew", realPath
	}

	return "standalone", realPath
}
