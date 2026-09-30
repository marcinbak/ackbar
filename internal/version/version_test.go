package version

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		name string
		vA   string
		vB   string
		want int
	}{
		{"identical dates", "20260930.01", "20260930.01", 0},
		{"identical with v prefix", "v20260930.01", "20260930.01", 0},
		{"identical with V prefix", "V20260930.01", "v20260930.01", 0},
		{"newer revision same day", "20260930.02", "20260930.01", 1},
		{"older revision same day", "20260930.01", "20260930.02", -1},
		{"newer day", "20261001.01", "20260930.09", 1},
		{"older day", "20260929.02", "20260930.01", -1},
		{"snapshot vs release", "20260930.01-snapshot", "20260930.01", -1},
		{"release vs snapshot", "20260930.01", "20260930.01-snapshot", 1},
		{"semver standard", "1.2.3", "1.2.2", 1},
		{"semver minor bump", "1.3.0", "1.2.9", 1},
		{"semver major bump", "2.0.0", "1.99.99", 1},
		{"semver equal", "v1.2.3", "1.2.3", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compare(tc.vA, tc.vB)
			if got != tc.want {
				t.Errorf("Compare(%q, %q) = %d; want %d", tc.vA, tc.vB, got, tc.want)
			}
		})
	}
}

func TestIsNewer(t *testing.T) {
	if !IsNewer("20260930.02", "20260930.01") {
		t.Errorf("Expected 20260930.02 to be newer than 20260930.01")
	}
	if IsNewer("20260930.01", "20260930.02") {
		t.Errorf("Did not expect 20260930.01 to be newer than 20260930.02")
	}
	if IsNewer("20260930.01", "20260930.01") {
		t.Errorf("Identical versions should not be newer")
	}
	if !IsNewer("20260930.01", "unknown") {
		t.Errorf("Valid version should be newer than 'unknown'")
	}
	if !IsNewer("20260930.01", "") {
		t.Errorf("Valid version should be newer than empty string")
	}
	if IsNewer("", "20260930.01") {
		t.Errorf("Empty version should not be newer")
	}
}

func TestCheckLatestRelease_MockServer(t *testing.T) {
	published := time.Now().Add(-2 * time.Hour)
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := githubReleaseResponse{
			TagName:     "v20991231.01",
			Name:        "v20991231.01",
			HTMLURL:     "https://github.com/marcinbak/ackbar/releases/tag/v20991231.01",
			Body:        "Awesome new features",
			PublishedAt: published,
			Assets: []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
				Size               int64  `json:"size"`
			}{
				{
					Name:               "ackbar_20991231.01_darwin_arm64.tar.gz",
					BrowserDownloadURL: "https://example.com/download/darwin_arm64.tar.gz",
					Size:               1234567,
				},
				{
					Name:               "ackbar_20991231.01_linux_amd64.tar.gz",
					BrowserDownloadURL: "https://example.com/download/linux_amd64.tar.gz",
					Size:               2345678,
				},
				{
					Name:               "checksums.txt",
					BrowserDownloadURL: "https://example.com/download/checksums.txt",
					Size:               418,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	// Override endpoints for test
	origBaseURL := apiBaseURL
	apiBaseURL = mockServer.URL
	defer func() {
		apiBaseURL = origBaseURL
	}()

	res, err := CheckLatestRelease(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckLatestRelease failed: %v", err)
	}

	if !res.UpdateAvailable {
		t.Errorf("Expected UpdateAvailable to be true for 20991231.01")
	}
	if res.LatestVersion != "20991231.01" {
		t.Errorf("Expected LatestVersion 20991231.01, got %s", res.LatestVersion)
	}
	if res.ReleaseInfo == nil {
		t.Fatalf("Expected ReleaseInfo to not be nil")
	}
	if res.ReleaseInfo.Changelog != "Awesome new features" {
		t.Errorf("Expected changelog 'Awesome new features', got %q", res.ReleaseInfo.Changelog)
	}

	// Test Platform asset lookup
	armAsset := FindAssetForPlatform(res.ReleaseInfo, "darwin", "arm64")
	if armAsset == nil || armAsset.Name != "ackbar_20991231.01_darwin_arm64.tar.gz" {
		t.Errorf("Expected darwin_arm64 asset, got: %+v", armAsset)
	}

	linuxAsset := FindAssetForPlatform(res.ReleaseInfo, "linux", "amd64")
	if linuxAsset == nil || linuxAsset.Name != "ackbar_20991231.01_linux_amd64.tar.gz" {
		t.Errorf("Expected linux_amd64 asset, got: %+v", linuxAsset)
	}

	chkAsset := FindChecksumsAsset(res.ReleaseInfo)
	if chkAsset == nil || chkAsset.Name != "checksums.txt" {
		t.Errorf("Expected checksums.txt asset, got: %+v", chkAsset)
	}

	// Test caching without forceRefresh
	cached, err := CheckLatestRelease(context.Background(), false)
	if err != nil {
		t.Fatalf("Cached CheckLatestRelease failed: %v", err)
	}
	if cached != res {
		t.Errorf("Expected returned pointer to match cached result")
	}
}
