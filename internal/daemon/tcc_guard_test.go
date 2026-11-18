package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIsProtectedHomeWalkDir(t *testing.T) {
	home := "/Users/alice"

	cases := []struct {
		name      string
		candidate string
		homeDir   string
		baseName  string
		want      bool
	}{
		{name: "photoslibrary_bundle_nested", candidate: "/Users/alice/Work/repo/Photos Library.photoslibrary", homeDir: home, baseName: "Photos Library.photoslibrary", want: true},
		{name: "photolibrary_bundle_mixed_case", candidate: "/Users/alice/Archive/Old.PhotoLibrary", homeDir: home, baseName: "Old.PhotoLibrary", want: true},
		{name: "aplibrary_bundle", candidate: "/Users/alice/Work/Aperture.aplibrary", homeDir: home, baseName: "Aperture.aplibrary", want: true},
		{name: "musiclibrary_bundle", candidate: "/Users/alice/Work/Tunes.musiclibrary", homeDir: home, baseName: "Tunes.musiclibrary", want: true},
		{name: "tvlibrary_bundle", candidate: "/Users/alice/Work/Shows.tvlibrary", homeDir: home, baseName: "Shows.tvlibrary", want: true},
		{name: "theater_bundle", candidate: "/Users/alice/Work/iMovie.THEATER", homeDir: home, baseName: "iMovie.THEATER", want: true},
		{name: "bundle_without_home_dir", candidate: "/tmp/Export.photoslibrary", homeDir: "", baseName: "Export.photoslibrary", want: true},

		{name: "home_library", candidate: "/Users/alice/Library", homeDir: home, baseName: "Library", want: true},
		{name: "home_pictures", candidate: "/Users/alice/Pictures", homeDir: home, baseName: "Pictures", want: true},
		{name: "home_music", candidate: "/Users/alice/Music", homeDir: home, baseName: "Music", want: true},
		{name: "home_movies", candidate: "/Users/alice/Movies", homeDir: home, baseName: "Movies", want: true},
		{name: "home_desktop", candidate: "/Users/alice/Desktop", homeDir: home, baseName: "Desktop", want: true},
		{name: "home_documents", candidate: "/Users/alice/Documents", homeDir: home, baseName: "Documents", want: true},
		{name: "home_downloads", candidate: "/Users/alice/Downloads", homeDir: home, baseName: "Downloads", want: true},
		{name: "home_public", candidate: "/Users/alice/Public", homeDir: home, baseName: "Public", want: true},
		{name: "home_trash", candidate: "/Users/alice/.Trash", homeDir: home, baseName: ".Trash", want: true},
		{name: "home_trailing_slashes", candidate: "/Users/alice/pictures/", homeDir: "/Users/alice/", baseName: "pictures", want: true},

		{name: "home_root_itself", candidate: "/Users/alice", homeDir: home, baseName: "alice", want: false},
		{name: "home_work_dir", candidate: "/Users/alice/Work", homeDir: home, baseName: "Work", want: false},
		{name: "repo_assets_music", candidate: "/Users/alice/Work/repo/assets/music", homeDir: home, baseName: "music", want: false},
		{name: "repo_docs", candidate: "/Users/alice/Work/repo/docs", homeDir: home, baseName: "docs", want: false},
		{name: "repo_documents", candidate: "/Users/alice/Work/repo/documents", homeDir: home, baseName: "documents", want: false},
		{name: "repo_downloads", candidate: "/Users/alice/Work/repo/downloads", homeDir: home, baseName: "downloads", want: false},
		{name: "empty_home_rel_pictures", candidate: "Pictures", homeDir: "", baseName: "Pictures", want: false},
		{name: "empty_candidate", candidate: "", homeDir: home, baseName: "Pictures", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsProtectedHomeWalkDir(tc.candidate, tc.homeDir, tc.baseName)
			if got != tc.want {
				t.Errorf("IsProtectedHomeWalkDir(%q, %q, %q) = %v, want %v", tc.candidate, tc.homeDir, tc.baseName, got, tc.want)
			}
		})
	}
}

func TestPopulateFiles_SkipsTCCProtectedHomeDirs(t *testing.T) {
	simHome := t.TempDir()

	filesToCreate := []string{
		filepath.Join(simHome, "Pictures", "Photos Library.photoslibrary", "inner.jpg"),
		filepath.Join(simHome, "Music", "Media", "song.mp3"),
		filepath.Join(simHome, "Work", "repo", "assets", "Album.photoslibrary", "shot.jpg"),
		filepath.Join(simHome, "Work", "repo", "music", "track.txt"),
		filepath.Join(simHome, "README.md"),
	}
	for _, p := range filesToCreate {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte("data"), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	s := &Server{homeDir: simHome}
	suggestions := s.populateFiles(simHome)

	got := make(map[string]bool, len(suggestions))
	for _, f := range suggestions {
		got[f.Path] = true
	}

	if !got["Work/repo/music/track.txt"] {
		t.Errorf("expected Work/repo/music/track.txt to be kept, got %+v", suggestions)
	}
	if !got["README.md"] {
		t.Errorf("expected README.md to be kept, got %+v", suggestions)
	}
	for _, forbidden := range []string{
		"Pictures/Photos Library.photoslibrary/inner.jpg",
		"Music/Media/song.mp3",
		"Work/repo/assets/Album.photoslibrary/shot.jpg",
	} {
		if got[forbidden] {
			t.Errorf("expected TCC path %q to be skipped, got %+v", forbidden, suggestions)
		}
	}
}

func TestHandleDocuments_UsesEffectiveHomeAndSkipsTCCBundles(t *testing.T) {
	simHome := t.TempDir()

	mustWrite := func(rel string) {
		p := filepath.Join(simHome, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte("# doc\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	mustWrite("README.md")
	mustWrite(filepath.Join("docs", "guide.md"))
	mustWrite(filepath.Join("docs", "Library.photoslibrary", "hidden.md"))

	s := &Server{homeDir: simHome}
	req := httptest.NewRequest(http.MethodGet, "/v1/documents", nil)
	rec := httptest.NewRecorder()
	s.handleDocuments(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var docs []DocumentItem
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	seen := make(map[string]bool, len(docs))
	for _, d := range docs {
		seen[filepath.ToSlash(d.RelPath)] = true
	}
	if !seen["README.md"] || !seen["docs/guide.md"] {
		t.Errorf("expected README.md and docs/guide.md in docs, got %+v", docs)
	}
	if seen["docs/Library.photoslibrary/hidden.md"] {
		t.Errorf("expected TCC bundle doc to be skipped, got %+v", docs)
	}
}
