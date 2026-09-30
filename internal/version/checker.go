package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	// defaultAPIBaseURL is the upstream GitHub releases endpoint
	defaultAPIBaseURL = "https://api.github.com/repos/marcinbak/ackbar/releases/latest"
	apiBaseURL        = defaultAPIBaseURL

	// defaultRawVersionURL is the fallback endpoint if releases API is rate limited
	defaultRawVersionURL = "https://raw.githubusercontent.com/marcinbak/ackbar/main/VERSION"
	rawVersionURL        = defaultRawVersionURL

	httpClient = &http.Client{Timeout: 5 * time.Second}

	cacheMu      sync.RWMutex
	cachedResult *UpdateCheckResult
	cachedAt     time.Time
	cacheTTL     = 1 * time.Hour
)

type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type ReleaseInfo struct {
	Version     string         `json:"version"`
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	PublishedAt time.Time      `json:"published_at"`
	ReleaseURL  string         `json:"release_url"`
	Changelog   string         `json:"changelog"`
	Assets      []ReleaseAsset `json:"assets"`
}

type UpdateCheckResult struct {
	CurrentVersion  string       `json:"current_version"`
	UpdateAvailable bool         `json:"update_available"`
	LatestVersion   string       `json:"latest_version"`
	ReleaseInfo     *ReleaseInfo `json:"release_info,omitempty"`
	CheckedAt       time.Time    `json:"checked_at"`
	Error           string       `json:"error,omitempty"`
}

type githubReleaseResponse struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// GetCachedRelease returns the cached release check result if valid, or nil. Never blocks on network I/O.
func GetCachedRelease() *UpdateCheckResult {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	if cachedResult != nil && time.Since(cachedAt) < cacheTTL {
		return cachedResult
	}
	return nil
}

// CheckLatestRelease queries GitHub for the latest release, caching results for 1 hour.
func CheckLatestRelease(ctx context.Context, forceRefresh bool) (*UpdateCheckResult, error) {
	if !forceRefresh {
		if cached := GetCachedRelease(); cached != nil {
			return cached, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create update request: %w", err)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("ackbar/%s", Version))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := httpClient.Do(req)
	var relInfo *ReleaseInfo
	var checkErr error

	if err == nil && resp.StatusCode == http.StatusOK {
		var ghResp githubReleaseResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&ghResp); decodeErr == nil {
			vClean := strings.TrimPrefix(ghResp.TagName, "v")
			vClean = strings.TrimPrefix(vClean, "V")

			relInfo = &ReleaseInfo{
				Version:     vClean,
				TagName:     ghResp.TagName,
				Name:        ghResp.Name,
				PublishedAt: ghResp.PublishedAt,
				ReleaseURL:  ghResp.HTMLURL,
				Changelog:   ghResp.Body,
			}
			for _, a := range ghResp.Assets {
				relInfo.Assets = append(relInfo.Assets, ReleaseAsset{
					Name:               a.Name,
					BrowserDownloadURL: a.BrowserDownloadURL,
					Size:               a.Size,
				})
			}
		} else {
			checkErr = fmt.Errorf("failed to decode GitHub release: %w", decodeErr)
		}
		_ = resp.Body.Close()
	} else {
		if resp != nil {
			_ = resp.Body.Close()
		}
		// Fallback to checking raw VERSION file on GitHub in case of rate limits or failures
		if rawVersion := fetchRawVersion(ctx); rawVersion != "" {
			relInfo = &ReleaseInfo{
				Version:    rawVersion,
				TagName:    "v" + rawVersion,
				Name:       "v" + rawVersion,
				ReleaseURL: "https://github.com/marcinbak/ackbar/releases",
			}
		} else if err != nil {
			checkErr = fmt.Errorf("failed to fetch upstream release: %w", err)
		} else {
			checkErr = fmt.Errorf("upstream release returned HTTP %d", resp.StatusCode)
		}
	}

	result := &UpdateCheckResult{
		CurrentVersion: Version,
		CheckedAt:      time.Now(),
	}

	if relInfo != nil {
		result.LatestVersion = relInfo.Version
		result.UpdateAvailable = IsNewer(relInfo.Version, Version)
		result.ReleaseInfo = relInfo
	} else {
		result.LatestVersion = Version
		result.UpdateAvailable = false
		if checkErr != nil {
			result.Error = checkErr.Error()
		}
	}

	cacheMu.Lock()
	cachedResult = result
	if checkErr != nil {
		// Cache failures for only 2 minutes so network interruptions recover quickly
		cachedAt = time.Now().Add(-cacheTTL + 2*time.Minute)
	} else {
		cachedAt = time.Now()
	}
	cacheMu.Unlock()

	return result, checkErr
}

func fetchRawVersion(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawVersionURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", fmt.Sprintf("ackbar/%s", Version))
	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// FindAssetForPlatform finds the prebuilt archive asset for given GOOS and GOARCH.
func FindAssetForPlatform(info *ReleaseInfo, goos, goarch string) *ReleaseAsset {
	if info == nil {
		return nil
	}
	targetSuffix := fmt.Sprintf("_%s_%s.tar.gz", goos, goarch)
	for _, a := range info.Assets {
		if strings.HasSuffix(a.Name, targetSuffix) {
			return &a
		}
	}
	return nil
}

// FindChecksumsAsset finds checksums.txt in release assets.
func FindChecksumsAsset(info *ReleaseInfo) *ReleaseAsset {
	if info == nil {
		return nil
	}
	for _, a := range info.Assets {
		if a.Name == "checksums.txt" {
			return &a
		}
	}
	return nil
}
