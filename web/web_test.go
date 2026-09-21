package web_test

import (
	"io"
	"io/fs"
	"testing"

	"ackbar/web"
)

func TestGetFS(t *testing.T) {
	embeddedFS := web.GetFS()
	if embeddedFS == nil {
		t.Fatal("expected non-nil fs.FS from web.GetFS()")
	}

	requiredFiles := []string{
		"index.html",
		"style.css",
		"manifest.json",
		"app.js",
		"js/api.js",
		"js/app.js",
		"js/auth.js",
		"js/chat.js",
		"js/context-menu.js",
		"js/details.js",
		"js/modals.js",
		"js/palette.js",
		"js/sse.js",
		"js/state.js",
		"js/statusbar.js",
		"js/tabs.js",
		"js/terminal.js",
		"js/tree.js",
		"js/utils.js",
	}

	for _, path := range requiredFiles {
		t.Run(path, func(t *testing.T) {
			f, err := embeddedFS.Open(path)
			if err != nil {
				t.Fatalf("failed to open embedded file %s: %v", path, err)
			}
			defer f.Close()

			stat, err := f.Stat()
			if err != nil {
				t.Fatalf("failed to stat embedded file %s: %v", path, err)
			}
			if stat.IsDir() {
				t.Fatalf("expected %s to be a file, got directory", path)
			}
			if stat.Size() == 0 {
				t.Fatalf("embedded file %s is empty", path)
			}

			content, err := io.ReadAll(f)
			if err != nil {
				t.Fatalf("failed to read embedded file %s: %v", path, err)
			}
			if len(content) == 0 {
				t.Fatalf("read zero bytes from embedded file %s", path)
			}
		})
	}

	// Verify walking embedded FS
	entriesCount := 0
	err := fs.WalkDir(embeddedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			entriesCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk embeddedFS: %v", err)
	}

	if entriesCount < len(requiredFiles) {
		t.Fatalf("expected at least %d files, walked %d", len(requiredFiles), entriesCount)
	}
}
