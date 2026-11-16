package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

// detectMIMEType returns an appropriate Content-Type header value for a given file extension
func detectMIMEType(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".bmp":
		return "image/bmp"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".pdf":
		return "application/pdf"
	case ".json":
		return "application/json"
	case ".txt", ".log":
		return "text/plain; charset=utf-8"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}

// sanitizeHostCacheSubdir converts a host name to a safe directory name
func sanitizeHostCacheSubdir(host string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, host)
}

// OpenFileLocallyFunc allows mocking or overriding local file opening in tests.
var OpenFileLocallyFunc = defaultOpenFileLocally

// openFileLocally opens a local file using the system's default or requested application
func openFileLocally(filePath string, app string) error {
	return OpenFileLocallyFunc(filePath, app)
}

func defaultOpenFileLocally(filePath string, app string) error {
	// Under automated tests (e.g. go test), do not launch real external GUI processes
	if isTestEnv() {
		return nil
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		switch strings.ToLower(app) {
		case "preview":
			cmd = exec.Command("open", "-a", "Preview", filePath)
		case "browser":
			cmd = exec.Command("open", filePath)
		default:
			cmd = exec.Command("open", filePath)
		}
	case "linux":
		cmd = exec.Command("xdg-open", filePath)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", filePath)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	return cmd.Start()
}

// handleFileContent serves the raw contents of a file with appropriate MIME headers,
// proxying to remote host daemons when the session resides on another host.
func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("id")
	}
	hostParam := r.URL.Query().Get("host")

	// Determine session and host
	var sess *Session
	if sessionID != "" {
		sess = s.resolveSession(sessionID)
	}

	targetHost := hostParam
	if targetHost == "" && sess != nil {
		targetHost = sess.Host
	}
	if targetHost == "" && sessionID != "" {
		parts := strings.Split(sessionID, ":")
		if len(parts) >= 2 && !strings.Contains(parts[1], "/") {
			targetHost = parts[1]
		}
	}

	// If remote host, proxy request
	if targetHost != "" && !s.isLocalHost(targetHost) {
		hostRec := s.resolveHost(targetHost)
		if hostRec != nil && hostRec.URL != "" {
			targetURL := fmt.Sprintf("%s/v1/files/content?%s", strings.TrimSuffix(hostRec.URL, "/"), r.URL.RawQuery)
			fwdReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if auth := r.Header.Get("Authorization"); auth != "" {
				fwdReq.Header.Set("Authorization", auth)
			} else if s.configuredToken != "" {
				fwdReq.Header.Set("Authorization", "Bearer "+s.configuredToken)
			}
			resp, err := http.DefaultClient.Do(fwdReq)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to fetch file from remote host: %v", err), http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()

			for k, vv := range resp.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
		http.Error(w, fmt.Sprintf("Remote host %q not found or unreachable", targetHost), http.StatusNotFound)
		return
	}

	// Local file resolution
	filePath, err := s.resolveFilePath(sess, reqPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	fi, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if fi.IsDir() {
		http.Error(w, "Path is a directory", http.StatusBadRequest)
		return
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if ctype := detectMIMEType(ext); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}

	if r.URL.Query().Get("download") == "1" || r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(filePath)))
	}
	w.Header().Set("Accept-Ranges", "bytes")

	http.ServeFile(w, r, filePath)
}

// handleFileOpen handles requests to launch a file in an application (e.g. Preview, VS Code, Browser),
// supporting cross-machine staging so files on remote hosts can be opened locally in macOS native apps.
func (s *Server) handleFileOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path      string `json:"path"`
		SessionID string `json:"session_id"`
		Host      string `json:"host"`
		App       string `json:"app"` // "preview", "vscode", "browser", "default"
	}

	if r.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil && len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &req)
		}
	}

	// Query params fallback
	if req.Path == "" {
		req.Path = r.URL.Query().Get("path")
	}
	if req.SessionID == "" {
		req.SessionID = r.URL.Query().Get("session_id")
		if req.SessionID == "" {
			req.SessionID = r.URL.Query().Get("id")
		}
	}
	if req.Host == "" {
		req.Host = r.URL.Query().Get("host")
	}
	if req.App == "" {
		req.App = r.URL.Query().Get("app")
	}
	if req.App == "" {
		req.App = "default"
	}

	if req.Path == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	// Resolve session and host
	var sess *Session
	if req.SessionID != "" {
		sess = s.resolveSession(req.SessionID)
	}

	targetHost := req.Host
	if targetHost == "" && sess != nil {
		targetHost = sess.Host
	}
	if targetHost == "" && req.SessionID != "" {
		parts := strings.Split(req.SessionID, ":")
		if len(parts) >= 2 && !strings.Contains(parts[1], "/") {
			targetHost = parts[1]
		}
	}

	// If app is VS Code, delegate to LaunchVSCode
	if strings.ToLower(req.App) == "vscode" {
		targetPath := req.Path
		cleanTarget, line, col := splitFilePathAndLine(targetPath)
		var baseResolved string
		if sess != nil && !filepath.IsAbs(cleanTarget) {
			if resolved, err := s.resolveFilePath(sess, cleanTarget); err == nil {
				baseResolved = resolved
			} else {
				baseResolved = cleanTarget
			}
		} else {
			baseResolved = cleanTarget
		}

		// Verify local file exists before opening to prevent VS Code "file does not exist" modals
		if (targetHost == "" || s.isLocalHost(targetHost)) && filepath.IsAbs(baseResolved) {
			if _, err := os.Stat(baseResolved); err != nil && os.IsNotExist(err) {
				http.Error(w, fmt.Sprintf("File not found: %s", req.Path), http.StatusNotFound)
				return
			}
		}

		pathForVSCode := baseResolved
		if line > 0 {
			if col > 0 {
				pathForVSCode = fmt.Sprintf("%s:%d:%d", baseResolved, line, col)
			} else {
				pathForVSCode = fmt.Sprintf("%s:%d", baseResolved, line)
			}
		}

		uri, err := LaunchVSCode(pathForVSCode, targetHost)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to launch VS Code: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "opened",
			"app":    "vscode",
			"path":   pathForVSCode,
			"host":   targetHost,
			"uri":    uri,
		})
		return
	}

	// Cross-host handling:
	// If the file is on a remote host and the user wants to open it in a local viewer (Preview, browser, etc.),
	// the local daemon downloads the file content from the remote daemon and caches it locally before launching.
	if targetHost != "" && !s.isLocalHost(targetHost) {
		hostRec := s.resolveHost(targetHost)
		if hostRec == nil || hostRec.URL == "" {
			http.Error(w, fmt.Sprintf("Remote host %q not found or unreachable", targetHost), http.StatusNotFound)
			return
		}

		// Download remote file to local staging cache
		contentURL := fmt.Sprintf("%s/v1/files/content?path=%s", strings.TrimSuffix(hostRec.URL, "/"), url.QueryEscape(req.Path))
		if req.SessionID != "" {
			contentURL += fmt.Sprintf("&session_id=%s", url.QueryEscape(req.SessionID))
		}

		fwdReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, contentURL, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			fwdReq.Header.Set("Authorization", auth)
		} else if s.configuredToken != "" {
			fwdReq.Header.Set("Authorization", "Bearer "+s.configuredToken)
		}

		resp, err := http.DefaultClient.Do(fwdReq)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to fetch remote file: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyText, _ := io.ReadAll(resp.Body)
			http.Error(w, fmt.Sprintf("Remote file fetch failed (%d): %s", resp.StatusCode, string(bodyText)), resp.StatusCode)
			return
		}

		homeDir, err := os.UserHomeDir()
		if err != nil {
			http.Error(w, "Cannot determine user home directory for staging", http.StatusInternalServerError)
			return
		}

		cleanRemotePath := strings.TrimPrefix(filepath.Clean(req.Path), "/")
		cachedDir := filepath.Join(homeDir, ".cache", "ackbar", "remote", sanitizeHostCacheSubdir(targetHost), filepath.Dir(cleanRemotePath))
		if err := os.MkdirAll(cachedDir, 0755); err != nil {
			http.Error(w, fmt.Sprintf("Failed to create cache dir: %v", err), http.StatusInternalServerError)
			return
		}

		cachedFilePath := filepath.Join(cachedDir, filepath.Base(cleanRemotePath))
		outFile, err := os.Create(cachedFilePath)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to write cached file: %v", err), http.StatusInternalServerError)
			return
		}
		if _, err := io.Copy(outFile, resp.Body); err != nil {
			outFile.Close()
			http.Error(w, fmt.Sprintf("Failed to stream cached file: %v", err), http.StatusInternalServerError)
			return
		}
		outFile.Close()

		// Launch locally on user's machine
		if err := openFileLocally(cachedFilePath, req.App); err != nil {
			http.Error(w, fmt.Sprintf("Failed to open file: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "opened",
			"path":        req.Path,
			"cached_path": cachedFilePath,
			"host":        targetHost,
			"app":         req.App,
			"remote":      true,
		})
		return
	}

	// Local file handling
	filePath, err := s.resolveFilePath(sess, req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := openFileLocally(filePath, req.App); err != nil {
		http.Error(w, fmt.Sprintf("Failed to open file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "opened",
		"path":   filePath,
		"app":    req.App,
		"remote": false,
	})
}
