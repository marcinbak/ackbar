package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotatingFileWriter(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ackbar_log_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Max size = 50 bytes for quick testing of size-based rotation
	rotator, err := NewRotatingFileWriter(tempDir, "ackbard", 7, 50)
	if err != nil {
		t.Fatalf("Failed to create rotator: %v", err)
	}
	defer rotator.Close()

	msg1 := []byte("First log message line.\n")
	if _, err := rotator.Write(msg1); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	symlink := filepath.Join(tempDir, "ackbard.log")
	if _, err := os.Lstat(symlink); err != nil {
		t.Errorf("Symlink ackbard.log was not created: %v", err)
	}

	// Write enough to trigger size rotation
	msg2 := []byte("Second message that is long enough to trigger immediate rotation.\n")
	if _, err := rotator.Write(msg2); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	logCount := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ackbard-") && strings.HasSuffix(e.Name(), ".log") {
			logCount++
		}
	}

	if logCount < 2 {
		t.Errorf("Expected at least 2 rotated log files, found %d", logCount)
	}
}

func TestPruneOldLogs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ackbar_prune_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create an old file
	oldFile := filepath.Join(tempDir, "ackbard-2020-01-01.log")
	if err := os.WriteFile(oldFile, []byte("old log"), 0644); err != nil {
		t.Fatalf("Failed to create old log file: %v", err)
	}
	// Modify mtime to 10 days ago
	oldTime := time.Now().AddDate(0, 0, -10)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	rotator, err := NewRotatingFileWriter(tempDir, "ackbard", 7, 1024*1024)
	if err != nil {
		t.Fatalf("Failed to create rotator: %v", err)
	}
	defer rotator.Close()

	rotator.pruneOldLogs()

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("Expected old log file to be pruned, but it still exists")
	}
}
