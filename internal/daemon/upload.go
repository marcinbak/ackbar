package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultUploadDir = "/tmp/ackbar-uploads"
	maxUploadSize    = 32 << 20 // 32 MB
	maxUploadAge     = 7 * 24 * time.Hour
)

// Allowed file extensions for multimodal coding agent attachments
var allowedExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".webp": true,
	".gif":  true,
	".bmp":  true,
	".svg":  true,
	".pdf":  true,
}

var safeFilenameRegex = regexp.MustCompile(`[^a-zA-Z0-9_\.\-]`)

// UploadResponse is returned to the client upon successful upload
type UploadResponse struct {
	Status   string `json:"status"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Host     string `json:"host"`
}

// handleUpload handles POST /v1/uploads for clipboard images, PDFs, and dropped files
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request size to prevent DoS
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	hostParam := r.URL.Query().Get("host")
	if hostParam == "" {
		hostParam = "local"
	}
	suggestedFilename := r.URL.Query().Get("filename")
	if suggestedFilename == "" {
		suggestedFilename = r.Header.Get("X-Filename")
	}

	contentType := r.Header.Get("Content-Type")
	var fileReader io.Reader
	var originalName string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			http.Error(w, fmt.Sprintf("Failed to parse multipart form: %v", err), http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "Missing 'file' in multipart form data", http.StatusBadRequest)
			return
		}
		defer file.Close()
		fileReader = file
		originalName = header.Filename
	} else {
		// Raw binary stream
		fileReader = r.Body
		originalName = suggestedFilename
	}

	if originalName == "" {
		originalName = suggestedFilename
	}

	// Resolve extension from filename or Content-Type
	ext := strings.ToLower(filepath.Ext(originalName))
	if ext == "" {
		ext = mimeToExt(contentType)
	}

	if !allowedExtensions[ext] {
		http.Error(w, fmt.Sprintf("Invalid file extension '%s'. Allowed: png, jpg, jpeg, webp, gif, bmp, svg, pdf", ext), http.StatusBadRequest)
		return
	}

	// Generate safe destination filename
	timestamp := time.Now().Format("20060102_150405")
	cleanBase := safeFilenameRegex.ReplaceAllString(filepath.Base(originalName), "_")
	cleanBase = strings.TrimSuffix(cleanBase, filepath.Ext(cleanBase))

	var targetFilename string
	if cleanBase == "" || cleanBase == "blob" || cleanBase == "image" || cleanBase == "paste" {
		targetFilename = fmt.Sprintf("paste_%s%s", timestamp, ext)
	} else {
		targetFilename = fmt.Sprintf("%s_%s%s", cleanBase, timestamp, ext)
	}

	uploadDir := defaultUploadDir
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create upload directory: %v", err), http.StatusInternalServerError)
		return
	}

	localDestPath := filepath.Join(uploadDir, targetFilename)
	out, err := os.Create(localDestPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create file: %v", err), http.StatusInternalServerError)
		return
	}
	defer out.Close()

	written, err := io.Copy(out, fileReader)
	if err != nil {
		_ = os.Remove(localDestPath)
		http.Error(w, fmt.Sprintf("Failed to save upload: %v", err), http.StatusInternalServerError)
		return
	}

	finalPath := localDestPath

	// If target is a remote compute host (e.g. Legion), transfer the file to the remote host
	if hostParam != "local" {
		remotePath, rerr := s.transferUploadToRemote(hostParam, localDestPath, targetFilename)
		if rerr != nil {
			log.Printf("[Upload] Warning: failed to transfer upload to remote host '%s': %v (falling back to local path)", hostParam, rerr)
		} else {
			finalPath = remotePath
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(UploadResponse{
		Status:   "ok",
		Path:     finalPath,
		Filename: targetFilename,
		Size:     written,
		Host:     hostParam,
	})
}

// transferUploadToRemote copies the uploaded file to the remote host's /tmp/ackbar-uploads directory
func (s *Server) transferUploadToRemote(hostName, localPath, filename string) (string, error) {
	remoteDir := "/tmp/ackbar-uploads"
	remotePath := filepath.Join(remoteDir, filename)

	// Check if host has an SSH target
	sshTarget := hostName
	if s.db != nil {
		if hosts, err := s.db.ListHosts(); err == nil {
			for _, h := range hosts {
				if h.Name == hostName && h.SSHTarget != "" {
					sshTarget = h.SSHTarget
					break
				}
			}
		}
	}

	// 1. Ensure remote directory exists
	mkdirCmd := exec.Command("ssh", sshTarget, fmt.Sprintf("mkdir -p %q", remoteDir))
	if out, err := mkdirCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ssh mkdir failed: %v (%s)", err, string(out))
	}

	// 2. SCP upload to remote host
	scpTarget := fmt.Sprintf("%s:%s", sshTarget, remotePath)
	scpCmd := exec.Command("scp", localPath, scpTarget)
	if out, err := scpCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("scp failed: %v (%s)", err, string(out))
	}

	return remotePath, nil
}

// mimeToExt maps common MIME types to file extensions
func mimeToExt(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if idx := strings.Index(mime, ";"); idx != -1 {
		mime = mime[:idx]
	}
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/bmp", "image/x-ms-bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	case "application/pdf":
		return ".pdf"
	default:
		return ".png"
	}
}

// StartUploadCleaner starts a background goroutine to prune files older than 7 days
func StartUploadCleaner(dir string, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			PruneOldUploads(dir, maxUploadAge)
		}
	}()
}

// PruneOldUploads removes files older than maxAge from the upload directory
func PruneOldUploads(dir string, maxAge time.Duration) {
	if dir == "" {
		dir = defaultUploadDir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
