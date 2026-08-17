package daemon

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RotatingFileWriter implements an io.WriteCloser that automatically rotates
// log files daily and when exceeding a maximum file size, pruning older logs.
type RotatingFileWriter struct {
	Dir          string
	Prefix       string
	MaxAgeDays   int
	MaxSizeBytes int64

	mu          sync.Mutex
	currentFile *os.File
	currentDay  string
	currentSize int64
	currentPart int
}

// NewRotatingFileWriter creates and initializes a RotatingFileWriter.
func NewRotatingFileWriter(dir, prefix string, maxAgeDays int, maxSizeBytes int64) (*RotatingFileWriter, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 7
	}
	if maxSizeBytes <= 0 {
		maxSizeBytes = 10 * 1024 * 1024 // 10 MB default
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory %q: %w", dir, err)
	}

	w := &RotatingFileWriter{
		Dir:          dir,
		Prefix:       prefix,
		MaxAgeDays:   maxAgeDays,
		MaxSizeBytes: maxSizeBytes,
	}

	if err := w.rotateLocked(); err != nil {
		return nil, err
	}

	return w, nil
}

// Write writes log data to the active log file, rotating if the date changed or size exceeded.
func (w *RotatingFileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if w.currentFile == nil || today != w.currentDay || (w.currentSize+int64(len(p)) > w.MaxSizeBytes && w.currentSize > 0) {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}

	n, err = w.currentFile.Write(p)
	w.currentSize += int64(n)
	return n, err
}

// rotateLocked rotates to a new or current daily log file and triggers cleanup.
func (w *RotatingFileWriter) rotateLocked() error {
	today := time.Now().Format("2006-01-02")
	if w.currentFile != nil {
		_ = w.currentFile.Close()
		w.currentFile = nil
	}

	if today != w.currentDay {
		w.currentDay = today
		w.currentPart = 0
	} else {
		w.currentPart++
	}

	fileName := fmt.Sprintf("%s-%s.log", w.Prefix, w.currentDay)
	if w.currentPart > 0 {
		fileName = fmt.Sprintf("%s-%s.%d.log", w.Prefix, w.currentDay, w.currentPart)
	}

	fullPath := filepath.Join(w.Dir, fileName)
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file %q: %w", fullPath, err)
	}

	fi, err := f.Stat()
	if err == nil {
		w.currentSize = fi.Size()
	} else {
		w.currentSize = 0
	}

	w.currentFile = f

	// Maintain convenience symlink: <prefix>.log -> <current-file>
	symlinkPath := filepath.Join(w.Dir, fmt.Sprintf("%s.log", w.Prefix))
	_ = os.Remove(symlinkPath)
	_ = os.Symlink(fileName, symlinkPath)

	// Clean up old log files asynchronously
	go w.pruneOldLogs()

	return nil
}

// pruneOldLogs deletes logs older than MaxAgeDays.
func (w *RotatingFileWriter) pruneOldLogs() {
	cutoff := time.Now().AddDate(0, 0, -w.MaxAgeDays)
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, w.Prefix+"-") || !strings.HasSuffix(name, ".log") {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(w.Dir, name))
		}
	}
}

// Close closes the underlying open log file.
func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentFile != nil {
		err := w.currentFile.Close()
		w.currentFile = nil
		return err
	}
	return nil
}

// SetupDaemonLogger configures standard logger to write to both stderr and rotating log files.
func SetupDaemonLogger(logDir string) (io.Closer, error) {
	if logDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		logDir = filepath.Join(home, ".config", "ackbar", "logs")
	}

	rotator, err := NewRotatingFileWriter(logDir, "ackbard", 7, 10*1024*1024)
	if err != nil {
		return nil, err
	}

	mw := io.MultiWriter(os.Stderr, rotator)
	log.SetOutput(mw)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	return rotator, nil
}
