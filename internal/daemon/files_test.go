package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectMIMEType(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
	}{
		{".png", "image/png"},
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".gif", "image/gif"},
		{".webp", "image/webp"},
		{".svg", "image/svg+xml"},
		{".html", "text/html; charset=utf-8"},
		{".htm", "text/html; charset=utf-8"},
		{".pdf", "application/pdf"},
		{".json", "application/json"},
		{".txt", "text/plain; charset=utf-8"},
		{".md", "text/markdown; charset=utf-8"},
		{".csv", "text/csv; charset=utf-8"},
	}

	for _, tt := range tests {
		got := detectMIMEType(tt.ext)
		if got != tt.expected {
			t.Errorf("detectMIMEType(%q) = %q, expected %q", tt.ext, got, tt.expected)
		}
	}
}

func TestResolveFilePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-files-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	server := &Server{}
	sess := &Session{
		ID:  "sess-1",
		Cwd: tmpDir,
	}

	// 1. Relative path within session cwd
	p, err := server.resolveFilePath(sess, "hello.txt")
	if err != nil {
		t.Fatalf("Unexpected error resolving relative path: %v", err)
	}
	if p != testFile {
		t.Errorf("Expected %q, got %q", testFile, p)
	}

	// 2. Absolute path within session cwd
	p2, err := server.resolveFilePath(sess, testFile)
	if err != nil {
		t.Fatalf("Unexpected error resolving absolute path: %v", err)
	}
	if p2 != testFile {
		t.Errorf("Expected %q, got %q", testFile, p2)
	}

	// 3. Sensitive path traversal blocked
	_, err = server.resolveFilePath(sess, "../../etc/shadow")
	if err == nil {
		t.Errorf("Expected error for sensitive path, got nil")
	}

	_, err = server.resolveFilePath(sess, "~/.ssh/id_rsa")
	if err == nil {
		t.Errorf("Expected error for .ssh path, got nil")
	}
}

func TestResolveFilePath_CrossOSHomeFallback(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		t.Skip("UserHomeDir not available")
	}
	cleanHome := filepath.Clean(homeDir)
	currentUser := filepath.Base(cleanHome)
	if currentUser == "" || currentUser == "/" || currentUser == "." {
		t.Skip("Cannot determine current user from home directory")
	}

	server := &Server{}
	sess := &Session{ID: "sess-cross-os"}

	// Determine the opposite OS prefix
	var altPath string
	if strings.HasPrefix(cleanHome, "/home/") {
		altPath = "/Users/" + currentUser + "/test_cross_os_image.png"
	} else if strings.HasPrefix(cleanHome, "/Users/") {
		altPath = "/home/" + currentUser + "/test_cross_os_image.png"
	} else {
		altPath = "/Users/" + currentUser + "/test_cross_os_image.png"
	}

	expectedPath := filepath.Join(cleanHome, "test_cross_os_image.png")

	resolved, err := server.resolveFilePath(sess, altPath)
	if err != nil {
		t.Fatalf("Failed to resolve cross-OS path %q: %v", altPath, err)
	}
	if resolved != expectedPath {
		t.Errorf("Expected resolved path %q, got %q", expectedPath, resolved)
	}

	// Verify sensitive path with cross-OS prefix is still blocked
	var altSensitive string
	if strings.HasPrefix(cleanHome, "/home/") {
		altSensitive = "/Users/" + currentUser + "/.ssh/id_rsa"
	} else {
		altSensitive = "/home/" + currentUser + "/.ssh/id_rsa"
	}
	_, err = server.resolveFilePath(sess, altSensitive)
	if err == nil {
		t.Errorf("Expected sensitive cross-OS path %q to be blocked, got nil error", altSensitive)
	}

	// Verify other user home path is blocked
	otherUserPath := "/Users/someotheruser999/image.png"
	_, err = server.resolveFilePath(sess, otherUserPath)
	if err == nil {
		t.Errorf("Expected other user path %q to be blocked, got nil error", otherUserPath)
	}
}

func TestHandleFileContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-files-serve-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	htmlFile := filepath.Join(tmpDir, "index.html")
	htmlContent := "<h1>Hello Ackbar</h1>"
	if err := os.WriteFile(htmlFile, []byte(htmlContent), 0644); err != nil {
		t.Fatalf("Failed to write html file: %v", err)
	}

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	sess := &Session{
		ID:    "test-session-123",
		Name:  "Test Session",
		Cwd:   tmpDir,
		Agent: "claude-code",
	}
	_ = db.SaveSession(sess)

	// Test 1: Serve local HTML file
	req := httptest.NewRequest("GET", "/v1/files/content?path=index.html&session_id=test-session-123", nil)
	rec := httptest.NewRecorder()
	server.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Expected text/html Content-Type, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != htmlContent {
		t.Errorf("Expected %q, got %q", htmlContent, rec.Body.String())
	}

	// Test 2: Download parameter sets Content-Disposition
	reqDl := httptest.NewRequest("GET", "/v1/files/content?path=index.html&session_id=test-session-123&download=1", nil)
	recDl := httptest.NewRecorder()
	server.Mux().ServeHTTP(recDl, reqDl)

	if recDl.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", recDl.Code)
	}
	if !strings.Contains(recDl.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("Expected attachment Content-Disposition, got %q", recDl.Header().Get("Content-Disposition"))
	}

	// Test 3: Missing path returns 400
	reqNoPath := httptest.NewRequest("GET", "/v1/files/content", nil)
	recNoPath := httptest.NewRecorder()
	server.Mux().ServeHTTP(recNoPath, reqNoPath)
	if recNoPath.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request, got %d", recNoPath.Code)
	}

	// Test 4: Missing file returns 404
	reqMissing := httptest.NewRequest("GET", "/v1/files/content?path=nonexistent.png&session_id=test-session-123", nil)
	recMissing := httptest.NewRecorder()
	server.Mux().ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found, got %d", recMissing.Code)
	}
}

func TestHandleFileOpen_VSCode(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-files-open-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	server := NewServer(db)

	// 1. Valid existing local file
	body := strings.NewReader(fmt.Sprintf(`{"path":%q,"app":"vscode"}`, testFile))
	req := httptest.NewRequest("POST", "/v1/files/open", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}
	if resp["status"] != "opened" {
		t.Errorf("Expected status 'opened', got %v", resp["status"])
	}
	if resp["app"] != "vscode" {
		t.Errorf("Expected app 'vscode', got %v", resp["app"])
	}
	if resp["path"] != testFile {
		t.Errorf("Expected path %q, got %v", testFile, resp["path"])
	}

	// 2. Non-existent local file returns 404
	bodyMissing := strings.NewReader(`{"path":"/nonexistent/path/file.go","app":"vscode"}`)
	reqMissing := httptest.NewRequest("POST", "/v1/files/open", bodyMissing)
	reqMissing.Header.Set("Content-Type", "application/json")
	recMissing := httptest.NewRecorder()

	server.Mux().ServeHTTP(recMissing, reqMissing)

	if recMissing.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found for missing file, got %d: %s", recMissing.Code, recMissing.Body.String())
	}
}

func TestHandleFileContent_RemoteProxy(t *testing.T) {
	// Remote daemon mock
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/files/content" && r.URL.Query().Get("path") == "remote/chart.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("fake-png-data"))
			return
		}
		http.NotFound(w, r)
	}))
	defer remoteServer.Close()

	tmpDir, err := os.MkdirTemp("", "ackbar-remote-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	// Register remote host in DB
	_ = db.SaveHost(&HostRecord{
		Name: "devbox",
		URL:  remoteServer.URL,
	})

	server := NewServer(db)
	sess := &Session{
		ID:   "remote-sess-1",
		Host: "devbox",
	}
	_ = db.SaveSession(sess)

	req := httptest.NewRequest("GET", "/v1/files/content?path=remote/chart.png&session_id=remote-sess-1", nil)
	rec := httptest.NewRecorder()
	server.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from proxied file, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("Expected image/png Content-Type, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "fake-png-data" {
		t.Errorf("Expected 'fake-png-data', got %q", rec.Body.String())
	}
}

func TestHandleFileOpen_RemoteDownload(t *testing.T) {
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/files/content" && r.URL.Query().Get("path") == "results/report.pdf" {
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4 test data"))
			return
		}
		http.NotFound(w, r)
	}))
	defer remoteServer.Close()

	tmpDir, err := os.MkdirTemp("", "ackbar-remote-open-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	_ = db.SaveHost(&HostRecord{
		Name: "gpu-node",
		URL:  remoteServer.URL,
	})

	server := NewServer(db)

	body := strings.NewReader(`{"path":"results/report.pdf","host":"gpu-node","app":"browser"}`)
	req := httptest.NewRequest("POST", "/v1/files/open", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}
	if resp["status"] != "opened" {
		t.Errorf("Expected status 'opened', got %v", resp["status"])
	}
	if resp["remote"] != true {
		t.Errorf("Expected remote=true, got %v", resp["remote"])
	}
	if resp["cached_path"] == nil || resp["cached_path"] == "" {
		t.Errorf("Expected cached_path to be non-empty")
	}

	// Verify cached file was written to disk
	cachedPath := resp["cached_path"].(string)
	data, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("Failed to read cached file: %v", err)
	}
	if string(data) != "%PDF-1.4 test data" {
		t.Errorf("Cached content mismatch: expected '%%PDF-1.4 test data', got %q", string(data))
	}
}

func TestChatCarousel(t *testing.T) {
	// Find project root (where web/ directory lives)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, "web", "js", "utils.js")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("Could not locate project root from %s", wd)
		}
		root = parent
	}

	// 1. Verify web/js/utils.js
	utilsJs, err := os.ReadFile(filepath.Join(root, "web", "js", "utils.js"))
	if err != nil {
		t.Fatalf("Failed to read utils.js: %v", err)
	}
	utilsStr := string(utilsJs)
	if !strings.Contains(utilsStr, "preprocessCarouselBlocks") {
		t.Errorf("utils.js missing preprocessCarouselBlocks")
	}
	if !strings.Contains(utilsStr, "chat-carousel") {
		t.Errorf("utils.js missing chat-carousel class emission")
	}
	if !strings.Contains(utilsStr, "chat-carousel-slides") {
		t.Errorf("utils.js missing chat-carousel-slides class emission")
	}
	if !strings.Contains(utilsStr, "chat-carousel-slide") {
		t.Errorf("utils.js missing chat-carousel-slide class emission")
	}

	// 2. Verify web/js/chat.js
	chatJs, err := os.ReadFile(filepath.Join(root, "web", "js", "chat.js"))
	if err != nil {
		t.Fatalf("Failed to read chat.js: %v", err)
	}
	chatStr := string(chatJs)
	if !strings.Contains(chatStr, "chat-file-preview-caption") {
		t.Errorf("chat.js missing chat-file-preview-caption")
	}
	if !strings.Contains(chatStr, ".chat-carousel") {
		t.Errorf("chat.js missing .chat-carousel initialization")
	}
	if !strings.Contains(chatStr, "chat-carousel-prev") || !strings.Contains(chatStr, "chat-carousel-next") {
		t.Errorf("chat.js missing chat-carousel navigation controls")
	}
	if !strings.Contains(chatStr, "chat-carousel-mode-btn") {
		t.Errorf("chat.js missing chat-carousel-mode-btn")
	}

	// 3. Verify web/js/details.js
	detailsJs, err := os.ReadFile(filepath.Join(root, "web", "js", "details.js"))
	if err != nil {
		t.Fatalf("Failed to read details.js: %v", err)
	}
	detailsStr := string(detailsJs)
	if !strings.Contains(detailsStr, "linkifyChatFiles(docContainer") {
		t.Errorf("details.js missing linkifyChatFiles(docContainer)")
	}

	// 4. Verify web/style.css
	styleCss, err := os.ReadFile(filepath.Join(root, "web", "style.css"))
	if err != nil {
		t.Fatalf("Failed to read style.css: %v", err)
	}
	styleStr := string(styleCss)
	if !strings.Contains(styleStr, ".chat-file-preview-caption") {
		t.Errorf("style.css missing .chat-file-preview-caption")
	}
	if !strings.Contains(styleStr, ".chat-carousel") {
		t.Errorf("style.css missing .chat-carousel")
	}
	if !strings.Contains(styleStr, `data-carousel-mode="grid"`) {
		t.Errorf("style.css missing grid carousel mode styles")
	}

	// 5. If Node is present, execute JS unit tests for carousel preprocessor, LaTeX math, and alerts
	if nodePath, err := exec.LookPath("node"); err == nil && nodePath != "" {
		script := `
globalThis.window = {
  marked: { parse: (s) => s.replace(/` + "```(\\w+)?\\n([\\s\\S]*?)\\n```" + `/g, '<pre><code class="language-$1">$2</code></pre>') },
  addEventListener: () => {}
};
globalThis.localStorage = { getItem: () => null, setItem: () => {} };
globalThis.document = {
  createElement: () => ({ setAttribute: () => {}, querySelectorAll: () => [] }),
  getElementById: () => null,
  querySelector: () => null,
  querySelectorAll: () => [],
  addEventListener: () => {}
};
const { preprocessCarouselBlocks, preprocessLatexMath, convertLatexToHtml, processMarkdownAlerts, preprocessMarkdownExtensions } = await import('./web/js/utils.js');

const t1 = preprocessCarouselBlocks('` + "````carousel\\n![A](a.png)\\n<!-- slide -->\\n![B](b.png)\\n````" + `');
if (!t1.includes('class="chat-carousel"')) throw new Error('t1 missing chat-carousel');
const slidesT1 = (t1.match(/class="chat-carousel-slide"/g) || []).length;
if (slidesT1 !== 2) throw new Error('t1 expected 2 slides, got ' + slidesT1);

const t2 = preprocessCarouselBlocks('Intro\\n\\n![A](a.png)\\n<!-- slide -->\\n![B](b.png)\\n\\nOutro');
if (!t2.includes('class="chat-carousel"')) throw new Error('t2 missing chat-carousel');
if (!t2.includes('Intro')) throw new Error('t2 missing Intro');
if (!t2.includes('Outro')) throw new Error('t2 missing Outro');

const t3 = preprocessCarouselBlocks('` + "```markdown\\n<!-- slide -->\\n```" + `');
if (!t3.includes('<!-- slide -->') || t3.includes('chat-carousel')) throw new Error('t3 corrupted markdown code block');

// CTX-1 regression: unfenced slide with code block unmasked before renderSlideContent
const t4 = preprocessCarouselBlocks('![A](a.png)\\n<!-- slide -->\\n` + "```js\\nconst x = 1;\\n```" + `');
if (!t4.includes('class="chat-carousel"')) throw new Error('t4 missing chat-carousel');
if (!t4.includes('<pre><code class="language-js">const x = 1;</code></pre>')) throw new Error('t4 code block was not parsed into pre/code');

// CTX-2 regression: unfenced slides with multi-paragraph headings and prose kept in slides
const t5 = preprocessCarouselBlocks('# Option 1\\n\\nDescription 1\\n<!-- slide -->\\n# Option 2\\n\\nDescription 2');
if (!t5.includes('class="chat-carousel"')) throw new Error('t5 missing chat-carousel');
const slidesT5 = t5.match(/<div class="chat-carousel-slide"[^>]*>([\s\S]*?)<\/div>/g) || [];
if (slidesT5.length !== 2) throw new Error('t5 expected 2 slides, got ' + slidesT5.length);
if (!slidesT5[0].includes('Option 1') || !slidesT5[0].includes('Description 1')) throw new Error('t5 slide 0 missing Option 1 or Description 1: ' + slidesT5[0]);
if (!slidesT5[1].includes('Option 2') || !slidesT5[1].includes('Description 2')) throw new Error('t5 slide 1 missing Option 2 or Description 2: ' + slidesT5[1]);

// CTX-2 regression: code blocks in carousel slides must not have math processed
const t6 = preprocessMarkdownExtensions('` + "````carousel\\n```bash\\n# $x = y + 1$\\n```\\n````" + `');
if (t6.includes('math-inline')) throw new Error('t6 code block inside carousel was corrupted by math: ' + t6);
if (!t6.includes('<pre><code class="language-bash"># $x = y + 1$</code></pre>')) throw new Error('t6 code block missing expected markup: ' + t6);

// LaTeX Math Unit Tests
const m1 = preprocessLatexMath('Formula: $\\rightarrow$');
if (!m1.includes('<span class="math-inline">→</span>')) throw new Error('m1 expected → arrow, got ' + m1);

const m2 = preprocessLatexMath('Formula: $\\longleftrightarrow$');
if (!m2.includes('<span class="math-inline">⟷</span>')) throw new Error('m2 expected ⟷ arrow, got ' + m2);

const m3 = preprocessLatexMath('Complexity: $O(n \\log n)$');
if (!m3.includes('<span class="math-inline">O(n log n)</span>')) throw new Error('m3 expected O(n log n), got ' + m3);

const m4 = preprocessLatexMath('Cost is $50 USD and $100');
if (m4.includes('math-inline') || !m4.includes('$50 USD and $100')) throw new Error('m4 currency was unexpectedly modified: ' + m4);

const m5 = preprocessMarkdownExtensions('` + "```js\\nconst x = \"$foo\";\\n```" + `\\nHere is $\\rightarrow$');
if (!m5.includes('const x = "$foo";') || !m5.includes('<span class="math-inline">→</span>')) throw new Error('m5 code block was modified or math was not processed: ' + m5);

// GFM Alerts Unit Tests
const a1 = processMarkdownAlerts('<blockquote><p>[!NOTE]<br>Useful information here.</p></blockquote>');
if (!a1.includes('markdown-alert-note') || !a1.includes('ℹ️') || !a1.includes('Useful information here.')) throw new Error('a1 alert NOTE failed: ' + a1);

const a2 = processMarkdownAlerts('<blockquote><p>[!WARNING]<br>Watch out!</p></blockquote>');
if (!a2.includes('markdown-alert-warning') || !a2.includes('⚠️') || !a2.includes('Watch out!')) throw new Error('a2 alert WARNING failed: ' + a2);

console.log('NODE_CAROUSEL_TESTS_PASS');
`
		cmd := exec.Command(nodePath, "--input-type=module", "-e", script)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Node carousel tests failed: %v\nOutput: %s", err, string(out))
		}
		if !strings.Contains(string(out), "NODE_CAROUSEL_TESTS_PASS") {
			t.Errorf("Expected node output to contain NODE_CAROUSEL_TESTS_PASS, got: %s", string(out))
		}
	}
}

func TestSplitFilePathAndLine(t *testing.T) {
	tests := []struct {
		input    string
		wantPath string
		wantLine int
		wantCol  int
	}{
		{
			input:    "/path/to/file.go#L75-L176",
			wantPath: "/path/to/file.go",
			wantLine: 75,
			wantCol:  0,
		},
		{
			input:    "/path/to/file.go#L75",
			wantPath: "/path/to/file.go",
			wantLine: 75,
			wantCol:  0,
		},
		{
			input:    "/path/to/file.go:75:10",
			wantPath: "/path/to/file.go",
			wantLine: 75,
			wantCol:  10,
		},
		{
			input:    "/tmp/test.go:75-176",
			wantPath: "/tmp/test.go",
			wantLine: 75,
			wantCol:  0,
		},
		{
			input:    "/path/to/file.go:75",
			wantPath: "/path/to/file.go",
			wantLine: 75,
			wantCol:  0,
		},
		{
			input:    "/path/to/file.go",
			wantPath: "/path/to/file.go",
			wantLine: 0,
			wantCol:  0,
		},
	}

	for _, tc := range tests {
		gotPath, gotLine, gotCol := splitFilePathAndLine(tc.input)
		if gotPath != tc.wantPath || gotLine != tc.wantLine || gotCol != tc.wantCol {
			t.Errorf("splitFilePathAndLine(%q) = (%q, %d, %d); want (%q, %d, %d)",
				tc.input, gotPath, gotLine, gotCol, tc.wantPath, tc.wantLine, tc.wantCol)
		}
	}
}

func TestLaunchVSCode_LineRanges(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		host    string
		wantURI string
	}{
		{
			name:    "local file with hash range",
			path:    "/path/to/file.go#L75-L176",
			host:    "local",
			wantURI: "vscode://file/path/to/file.go:75",
		},
		{
			name:    "local file with colon line and col",
			path:    "/path/to/file.go:75:10",
			host:    "local",
			wantURI: "vscode://file/path/to/file.go:75:10",
		},
		{
			name:    "remote file with hash line",
			path:    "/path/to/file.go#L42",
			host:    "remote-host",
			wantURI: "vscode://vscode-remote/ssh-remote+remote-host/path/to/file.go:42",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uri, err := LaunchVSCode(tc.path, tc.host)
			if err != nil {
				t.Fatalf("LaunchVSCode failed: %v", err)
			}
			if uri != tc.wantURI {
				t.Errorf("LaunchVSCode(%q, %q) = %q; want %q", tc.path, tc.host, uri, tc.wantURI)
			}
		})
	}
}

func TestHandleDocumentContent_ResolutionAndSecurity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-doc-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	docsDir := filepath.Join(tmpDir, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatalf("Failed to create docs dir: %v", err)
	}

	guideFile := filepath.Join(docsDir, "guide.md")
	guideContent := "# Guide Content\nWelcome to documentation."
	if err := os.WriteFile(guideFile, []byte(guideContent), 0644); err != nil {
		t.Fatalf("Failed to write test guide file: %v", err)
	}

	dbFile := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	sess := &Session{
		ID:  "sess-doc-1",
		Cwd: tmpDir,
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	server := NewServer(db)

	// 1. Relative path resolved via cwd query param
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/v1/documents/content?path=docs/guide.md&cwd=%s", url.QueryEscape(tmpDir)), nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK with cwd param, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
			t.Fatalf("Failed to decode JSON: %v", err)
		}
		if res["path"] != guideFile {
			t.Errorf("Expected path %q, got %q", guideFile, res["path"])
		}
		if res["content"] != guideContent {
			t.Errorf("Expected content %q, got %q", guideContent, res["content"])
		}
	}

	// 2. Relative path resolved via session_id query param
	{
		req := httptest.NewRequest("GET", "/v1/documents/content?path=docs/guide.md&session_id=sess-doc-1", nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK with session_id param, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
			t.Fatalf("Failed to decode JSON: %v", err)
		}
		if res["path"] != guideFile {
			t.Errorf("Expected path %q, got %q", guideFile, res["path"])
		}
		if res["content"] != guideContent {
			t.Errorf("Expected content %q, got %q", guideContent, res["content"])
		}
	}

	// 3. Path with #L10-L20 line anchor strips anchor and returns 200 OK
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/v1/documents/content?path=%s&session_id=sess-doc-1", url.QueryEscape("docs/guide.md#L10-L20")), nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK with line anchor, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
			t.Fatalf("Failed to decode JSON: %v", err)
		}
		if res["path"] != guideFile {
			t.Errorf("Expected path %q, got %q", guideFile, res["path"])
		}
		if res["content"] != guideContent {
			t.Errorf("Expected content %q, got %q", guideContent, res["content"])
		}
	}

	// 4. Sensitive path (~/.ssh/id_rsa) returns 403 Forbidden
	{
		req := httptest.NewRequest("GET", "/v1/documents/content?path=~/.ssh/id_rsa&session_id=sess-doc-1", nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("Expected 403 Forbidden for sensitive path, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 5. Unpermitted cwd outside home/temp/session returns 403 Forbidden (S1/C1)
	{
		req := httptest.NewRequest("GET", "/v1/documents/content?path=passwd&cwd=/etc", nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("Expected 403 Forbidden for unpermitted cwd, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 6. Both session_id and subdirectory cwd with relative path returns 200 OK (C2/L1)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/v1/documents/content?path=guide.md&cwd=%s&session_id=sess-doc-1", url.QueryEscape(docsDir)), nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK with session_id and subdirectory cwd, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
			t.Fatalf("Failed to decode JSON: %v", err)
		}
		if res["path"] != guideFile {
			t.Errorf("Expected path %q, got %q", guideFile, res["path"])
		}
		if res["content"] != guideContent {
			t.Errorf("Expected content %q, got %q", guideContent, res["content"])
		}
	}

	// 7. Requesting a directory returns 400 Bad Request (S2)
	{
		req := httptest.NewRequest("GET", "/v1/documents/content?path=docs&session_id=sess-doc-1", nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Expected 400 Bad Request for directory path, got %d: %s", rec.Code, rec.Body.String())
		}
	}
}
