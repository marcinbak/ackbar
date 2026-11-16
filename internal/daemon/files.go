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
	case ".avif":
		return "image/avif"
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
	if isTestEnv() {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if strings.ToLower(app) == "preview" {
			cmd = exec.Command("open", "-a", "Preview", filePath)
		} else {
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
	cwdParam := r.URL.Query().Get("cwd")

	// Determine session and host
	var sess *Session
	if sessionID != "" {
		sess = s.resolveSessionForFile(sessionID)
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
			q := r.URL.Query()
			q.Del("host")
			targetURL := fmt.Sprintf("%s/v1/files/content?%s", strings.TrimSuffix(hostRec.URL, "/"), q.Encode())
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
	effectiveSess, origCwd, err := s.applyEffectiveCwd(sess, cwdParam, targetHost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	filePath, err := s.resolveFilePathWithOrigCwd(effectiveSess, reqPath, origCwd)
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
		if ext == ".svg" {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox")
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
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
		Cwd       string `json:"cwd"`
	}

	if r.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil && len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &req)
		}
	}

	if req.Path == "" {
		req.Path = r.URL.Query().Get("path")
		if req.Path == "" {
			http.Error(w, "Missing path parameter", http.StatusBadRequest)
			return
		}
	}
	if req.SessionID == "" {
		if req.SessionID = r.URL.Query().Get("session_id"); req.SessionID == "" {
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
	if req.Cwd == "" {
		req.Cwd = r.URL.Query().Get("cwd")
	}

	// Resolve session and host
	var sess *Session
	if req.SessionID != "" {
		sess = s.resolveSessionForFile(req.SessionID)
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
		targetPath := stripFileScheme(req.Path)
		cleanTarget, line, col := splitFilePathAndLine(targetPath)
		effectiveSess, origCwd, err := s.applyEffectiveCwd(sess, req.Cwd, targetHost)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		baseResolved := cleanTarget
		if (targetHost == "" || s.isLocalHost(targetHost)) || effectiveSess != nil {
			if resolved, err := s.resolveFilePathWithOrigCwd(effectiveSess, cleanTarget, origCwd); err == nil {
				baseResolved = resolved
			}
		}

		// Verify local file exists before opening to prevent VS Code "file does not exist" modals
		if (targetHost == "" || s.isLocalHost(targetHost)) && filepath.IsAbs(baseResolved) {
			if _, err := os.Stat(baseResolved); err != nil && os.IsNotExist(err) {
				http.Error(w, fmt.Sprintf("File not found: %s", req.Path), http.StatusNotFound)
				return
			}
		}

		pathForVSCode := baseResolved
		if line > 0 && col > 0 {
			pathForVSCode = fmt.Sprintf("%s:%d:%d", baseResolved, line, col)
		} else if line > 0 {
			pathForVSCode = fmt.Sprintf("%s:%d", baseResolved, line)
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
		if req.Cwd != "" {
			contentURL += fmt.Sprintf("&cwd=%s", url.QueryEscape(req.Cwd))
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

		hostCacheRoot := filepath.Join(homeDir, ".cache", "ackbar", "remote", sanitizeHostCacheSubdir(targetHost))
		cleanRemotePath, _, _ := splitFilePathAndLine(stripFileScheme(req.Path))
		if unescaped, err := url.PathUnescape(cleanRemotePath); err == nil {
			cleanRemotePath = unescaped
		}
		cleanRemotePath = filepath.Clean(cleanRemotePath)
		cleanRemotePath = strings.TrimPrefix(cleanRemotePath, filepath.VolumeName(cleanRemotePath))
		cleanRemotePath = strings.TrimLeft(cleanRemotePath, "/\\")

		parts := strings.Split(cleanRemotePath, string(filepath.Separator))
		var safeParts []string
		for _, p := range parts {
			if p == ".." || p == "." || p == "" {
				continue
			}
			safeParts = append(safeParts, p)
		}
		if len(safeParts) == 0 {
			safeParts = []string{"staged_file"}
		}
		cachedFilePath := filepath.Join(hostCacheRoot, filepath.Join(safeParts...))
		if !strings.HasPrefix(cachedFilePath, hostCacheRoot+string(filepath.Separator)) {
			http.Error(w, "Invalid remote path for caching", http.StatusBadRequest)
			return
		}

		if err := os.MkdirAll(filepath.Dir(cachedFilePath), 0755); err != nil {
			http.Error(w, fmt.Sprintf("Failed to create cache dir: %v", err), http.StatusInternalServerError)
			return
		}

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
	effectiveSess, origCwd, err := s.applyEffectiveCwd(sess, req.Cwd, targetHost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	filePath, err := s.resolveFilePathWithOrigCwd(effectiveSess, req.Path, origCwd)
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
