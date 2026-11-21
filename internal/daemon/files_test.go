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
	"time"

	"ackbar/internal/agents/antigravity"
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
	_ = server.RegisterPlugin(antigravity.New())
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
	_ = server.RegisterPlugin(antigravity.New())
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
	testMissingFile := filepath.Join(tmpDir, "missing.go")
	bodyMissing := strings.NewReader(fmt.Sprintf(`{"path":%q,"app":"vscode"}`, testMissingFile))
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
const { preprocessCarouselBlocks, preprocessLatexMath, convertLatexToHtml, processMarkdownAlerts, preprocessMarkdownExtensions, renderSlideContent } = await import('./web/js/utils.js');

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

// Regression tests for inline code and LaTeX math interaction
const m6 = preprocessMarkdownExtensions('Cost is $10 for \x60item_a\x60 and $20 for \x60item_b\x60');
if (!m6.includes('\x60item_a\x60') || !m6.includes('\x60item_b\x60') || m6.includes('math-inline') || m6.includes('<sub>') || m6.includes('MDCODE') || m6.includes('MD_CODE_TOKEN')) {
  throw new Error('m6 inline code was corrupted or placeholder leaked: ' + m6);
}

const manySpans = Array.from({length: 60}, (_, i) => '\x60code_' + i + '\x60').join(' ') + ' costs $50 and $100';
const m7 = preprocessMarkdownExtensions(manySpans);
if (!m7.includes('\x60code_55\x60') || m7.includes('MDCODE') || m7.includes('MD_CODE_TOKEN') || m7.includes('math-inline') || m7.includes('<sub>')) {
  throw new Error('m7 multi-span unmasking failed or placeholder leaked: ' + m7);
}

const s1 = renderSlideContent('$10 and \x60slide_code\x60 and $20');
if (s1.includes('SLIDECODE') || s1.includes('SLIDE_CODE_TOKEN') || s1.includes('math-inline') || s1.includes('<sub>') || !s1.includes('\x60slide_code\x60')) {
  throw new Error('s1 renderSlideContent corrupted code or leaked placeholder: ' + s1);
}

const t7 = preprocessCarouselBlocks('` + "````carousel\\n$10 and `slide_code` and $20\\n<!-- slide -->\\nSlide 2\\n````" + `');
if (t7.includes('SLIDECODE') || t7.includes('SLIDE_CODE_TOKEN') || t7.includes('math-inline') || t7.includes('<sub>') || !t7.includes('\x60slide_code\x60')) {
  throw new Error('t7 carousel slide corrupted code or leaked placeholder: ' + t7);
}

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

func TestEmbeddedWebStaticFiles_ContainCarouselSupport(t *testing.T) {
	TestChatCarousel(t)
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

func TestDetectMIMEType_AVIF(t *testing.T) {
	if got := detectMIMEType(".avif"); got != "image/avif" {
		t.Fatalf("Expected image/avif, got %s", got)
	}
}

func TestHandleFileAndDocumentContent_RemoteProxyStripsHost(t *testing.T) {
	var forwardedFileQuery, forwardedDocQuery string
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/files/content" {
			forwardedFileQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("file-content"))
			return
		}
		if r.URL.Path == "/v1/documents/content" {
			forwardedDocQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"content": "doc-content"})
			return
		}
		http.NotFound(w, r)
	}))
	defer remoteServer.Close()

	tmpDir, err := os.MkdirTemp("", "ackbar-remote-proxy-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	_ = db.SaveHost(&HostRecord{
		Name: "worker-1",
		URL:  remoteServer.URL,
	})

	server := NewServer(db)

	// 1. /v1/files/content proxy
	{
		req := httptest.NewRequest("GET", "/v1/files/content?path=chart.png&host=worker-1&session_id=sess-123", nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d", rec.Code)
		}
		if strings.Contains(forwardedFileQuery, "host=") {
			t.Fatalf("Expected forwarded query not to contain host=, got %q", forwardedFileQuery)
		}
		if !strings.Contains(forwardedFileQuery, "path=chart.png") {
			t.Fatalf("Expected forwarded query to contain path=chart.png, got %q", forwardedFileQuery)
		}
	}

	// 2. /v1/documents/content proxy
	{
		req := httptest.NewRequest("GET", "/v1/documents/content?path=notes.md&host=worker-1&session_id=sess-123", nil)
		rec := httptest.NewRecorder()
		server.Mux().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d", rec.Code)
		}
		if strings.Contains(forwardedDocQuery, "host=") {
			t.Fatalf("Expected forwarded query not to contain host=, got %q", forwardedDocQuery)
		}
		if !strings.Contains(forwardedDocQuery, "path=notes.md") {
			t.Fatalf("Expected forwarded query to contain path=notes.md, got %q", forwardedDocQuery)
		}
	}
}

func TestResolveHost_DisplayName_And_SSHTarget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-resolvehost-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	_ = db.SaveHost(&HostRecord{
		Name:      "macmini-1",
		SSHTarget: "dev@192.168.1.50",
		URL:       "http://192.168.1.50:7777",
	})
	_ = db.SaveHost(&HostRecord{
		Name:      "cloud-server",
		SSHTarget: "admin@remote.infra.example.com",
		URL:       "http://remote.infra.example.com:7777",
	})

	server := NewServer(db)
	server.hostHealthMu.Lock()
	server.hostHealthMap["macmini-1"] = &HostHealth{
		DisplayName: "Living Room Mac Health",
	}
	server.hostHealthMap["cloud-server"] = &HostHealth{
		DisplayName: "Production Cloud",
	}
	server.hostHealthMu.Unlock()

	// Dynamic Health DisplayName match
	if h := server.resolveHost("Living Room Mac Health"); h == nil || h.Name != "macmini-1" {
		t.Fatalf("Expected macmini-1 by Health DisplayName, got %+v", h)
	}
	if h := server.resolveHost("Production Cloud"); h == nil || h.Name != "cloud-server" {
		t.Fatalf("Expected cloud-server by Health DisplayName, got %+v", h)
	}

	// SSHTarget extracted hostname match
	if h := server.resolveHost("192.168.1.50"); h == nil || h.Name != "macmini-1" {
		t.Fatalf("Expected macmini-1 by SSHTarget hostname, got %+v", h)
	}
	if h := server.resolveHost("dev@192.168.1.50"); h == nil || h.Name != "macmini-1" {
		t.Fatalf("Expected macmini-1 by full SSHTarget, got %+v", h)
	}
	if h := server.resolveHost("remote.infra.example.com"); h == nil || h.Name != "cloud-server" {
		t.Fatalf("Expected cloud-server by SSHTarget host, got %+v", h)
	}
}

func TestResolveFilePath_FallbacksAndSecurity(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	tmpDir, err := os.MkdirTemp("", "ackbar-resolve-fallbacks-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	repoDir := filepath.Join(tmpDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("Failed to create repo dir: %v", err)
	}

	server := &Server{}
	_ = server.RegisterPlugin(antigravity.New())
	sess := &Session{
		ID:  "sess-fallback-1",
		Cwd: repoDir,
	}

	// 1. %20 unescape vs literal % precedence
	spaceFile := filepath.Join(repoDir, "My Chart.png")
	_ = os.WriteFile(spaceFile, []byte("space"), 0644)

	percentLiteralFile := filepath.Join(repoDir, "My%20Chart.png")
	_ = os.WriteFile(percentLiteralFile, []byte("literal"), 0644)

	// Literal % exists, so requesting literal My%20Chart.png returns literal
	resolvedLiteral, err := server.resolveFilePath(sess, "My%20Chart.png")
	if err != nil {
		t.Fatalf("Failed to resolve literal %%: %v", err)
	}
	if resolvedLiteral != percentLiteralFile {
		t.Errorf("Expected literal %q, got %q", percentLiteralFile, resolvedLiteral)
	}

	// When literal % does NOT exist, percent-decoded variant resolves
	_ = os.Remove(percentLiteralFile)
	resolvedDecoded, err := server.resolveFilePath(sess, "My%20Chart.png")
	if err != nil {
		t.Fatalf("Failed to resolve decoded %%: %v", err)
	}
	if resolvedDecoded != spaceFile {
		t.Errorf("Expected decoded space file %q, got %q", spaceFile, resolvedDecoded)
	}

	// 2. Deleted worktree -> repo root fallback
	repoHeroFile := filepath.Join(repoDir, "hero.png")
	_ = os.WriteFile(repoHeroFile, []byte("hero"), 0644)

	deletedWorktreeReq := filepath.Join(repoDir, ".worktrees", "deleted-branch", "hero.png")
	resolvedFromDeleted, err := server.resolveFilePath(sess, deletedWorktreeReq)
	if err != nil {
		t.Fatalf("Failed to resolve from deleted worktree: %v", err)
	}
	if resolvedFromDeleted != repoHeroFile {
		t.Errorf("Expected repo root %q, got %q", repoHeroFile, resolvedFromDeleted)
	}

	// 3. Active worktree discovery from repo sess.Cwd (newest ModTime wins)
	wt1Dir := filepath.Join(repoDir, ".worktrees", "branch-old")
	wt2Dir := filepath.Join(repoDir, ".worktrees", "branch-new")
	_ = os.MkdirAll(wt1Dir, 0755)
	_ = os.MkdirAll(wt2Dir, 0755)

	wt1File := filepath.Join(wt1Dir, "widget.png")
	wt2File := filepath.Join(wt2Dir, "widget.png")
	_ = os.WriteFile(wt1File, []byte("old"), 0644)
	time.Sleep(10 * time.Millisecond)
	_ = os.WriteFile(wt2File, []byte("new"), 0644)

	resolvedActiveWt, err := server.resolveFilePath(sess, "widget.png")
	if err != nil {
		t.Fatalf("Failed to resolve active worktree file: %v", err)
	}
	if resolvedActiveWt != wt2File {
		t.Errorf("Expected newest worktree %q, got %q", wt2File, resolvedActiveWt)
	}

	// 4. Antigravity brain and subagent fallback
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		parentUUID := "11111111-2222-3333-4444-555555555555"
		subUUID := "66666666-7777-8888-9999-000000000000"
		bDir := filepath.Join(homeDir, ".gemini", "antigravity", "brain")
		parentLogDir := filepath.Join(bDir, parentUUID, ".system_generated", "subagents")
		_ = os.MkdirAll(parentLogDir, 0755)
		subDir := filepath.Join(bDir, subUUID)
		_ = os.MkdirAll(subDir, 0755)

		subImg := filepath.Join(subDir, "gen_diagram.png")
		_ = os.WriteFile(subImg, []byte("diagram"), 0644)

		subDescriptor := fmt.Sprintf(`{"role":"subagent","typeName":"coder"}`)
		_ = os.WriteFile(filepath.Join(parentLogDir, subUUID+".json"), []byte(subDescriptor), 0644)

		parentSess := &Session{
			ID:       "sess-agy-parent",
			NativeID: parentUUID,
			Cwd:      repoDir,
		}
		resolvedSubImg, err := server.resolveFilePath(parentSess, "gen_diagram.png")
		if err != nil {
			t.Fatalf("Failed to resolve subagent image from parent session: %v", err)
		}
		if resolvedSubImg != subImg {
			t.Errorf("Expected subagent image %q, got %q", subImg, resolvedSubImg)
		}

		// Subagent session resolution inheriting parent Cwd
		subSessResolved := server.resolveSessionForFile(subUUID)
		if subSessResolved == nil || subSessResolved.NativeID != subUUID {
			t.Fatalf("Expected subagent session resolved, got %+v", subSessResolved)
		}
	}

	// 5. Symlink escape blocked (symlink pointing to sensitive path ~/.ssh or outside permitted)
	symlinkPath := filepath.Join(repoDir, "evil_link.png")
	if homeDir != "" {
		sshDir := filepath.Join(homeDir, ".ssh")
		_ = os.MkdirAll(sshDir, 0700)
		_ = os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte("secret"), 0600)

		_ = os.Symlink(filepath.Join(sshDir, "id_rsa"), symlinkPath)
		defer os.Remove(symlinkPath)
		_, err = server.resolveFilePath(sess, "evil_link.png")
		if err == nil {
			t.Fatalf("Expected symlink to ~/.ssh to be blocked with error, got nil")
		}
	}
}

func TestHandleFileContent_SVG_SecurityHeaders(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-svg-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svgPath := filepath.Join(tmpDir, "icon.svg")
	_ = os.WriteFile(svgPath, []byte("<svg></svg>"), 0644)

	server := &Server{}
	_ = server.RegisterPlugin(antigravity.New())
	sess := &Session{ID: "sess-svg", Cwd: tmpDir}

	req := httptest.NewRequest("GET", "/v1/files/content?path=icon.svg", nil)
	rec := httptest.NewRecorder()
	server.handleFileContent(rec, req)

	// Since sess is nil from query, provide cwd query param
	req = httptest.NewRequest("GET", fmt.Sprintf("/v1/files/content?path=icon.svg&cwd=%s", url.QueryEscape(tmpDir)), nil)
	rec = httptest.NewRecorder()
	server.handleFileContent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg+xml") {
		t.Errorf("Expected image/svg+xml, got %q", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Expected CSP default-src 'none', got %q", csp)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Errorf("Expected X-Content-Type-Options: nosniff, got %q", nosniff)
	}
	_ = sess
}

func TestHandleFileOpen_CwdOverrideAndRemotePathSanitization(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpDir, err := os.MkdirTemp("", "ackbar-fileopen-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	repoDir := filepath.Join(tmpDir, "myrepo")
	subDir := filepath.Join(repoDir, "assets")
	_ = os.MkdirAll(subDir, 0755)

	shotFile := filepath.Join(subDir, "My Shot.png")
	_ = os.WriteFile(shotFile, []byte("shot"), 0644)

	server := &Server{}
	_ = server.RegisterPlugin(antigravity.New())

	// 1. Local open with cwd override and file://%20 URL
	bodyData, _ := json.Marshal(map[string]interface{}{
		"path": "file://assets/My%20Shot.png",
		"cwd":  repoDir,
		"app":  "preview",
	})
	req := httptest.NewRequest("POST", "/v1/files/open", strings.NewReader(string(bodyData)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleFileOpen(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for local open with cwd override, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["path"] != shotFile {
		t.Errorf("Expected path %q, got %q", shotFile, resp["path"])
	}

	// 2. Remote staging with directory traversal attempt in path
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-file-content"))
	}))
	defer remoteServer.Close()

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	_ = db.SaveHost(&HostRecord{
		Name: "remote-box",
		URL:  remoteServer.URL,
	})

	serverWithDB := NewServer(db)

	traversalBody, _ := json.Marshal(map[string]interface{}{
		"path": "../../../../etc/shadow",
		"host": "remote-box",
		"app":  "preview",
	})
	req2 := httptest.NewRequest("POST", "/v1/files/open", strings.NewReader(string(traversalBody)))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	serverWithDB.handleFileOpen(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with sanitized staging, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var resp2 map[string]interface{}
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	cachedPath, ok := resp2["cached_path"].(string)
	if !ok || cachedPath == "" {
		t.Fatalf("Expected cached_path in response, got %+v", resp2)
	}
	homeDir, _ := os.UserHomeDir()
	expectedPrefix := filepath.Join(homeDir, ".cache", "ackbar", "remote", "remote-box")
	if !strings.HasPrefix(cachedPath, expectedPrefix) {
		t.Errorf("Expected cachedPath %q to stay within %q", cachedPath, expectedPrefix)
	}
	if strings.Contains(cachedPath, "..") {
		t.Errorf("Expected cachedPath %q not to contain ..", cachedPath)
	}
}

func TestIsPathWithinOrEqual_Root(t *testing.T) {
	if !isPathWithinOrEqual("/app/hero.png", "/") {
		t.Errorf("Expected /app/hero.png to be within /")
	}
	if !isPathWithinOrEqual("/", "/") {
		t.Errorf("Expected / to be within /")
	}
	if isPathWithinOrEqual("/other", "/app") {
		t.Errorf("Expected /other not to be within /app")
	}
}

func TestResolveFilePath_GlobWildcardRejection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ackbar-glob-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	repoDir := filepath.Join(tmpDir, "repo")
	wtDir := filepath.Join(repoDir, ".worktrees", "wt1")
	_ = os.MkdirAll(wtDir, 0755)
	secretFile := filepath.Join(wtDir, "secret.png")
	_ = os.WriteFile(secretFile, []byte("secret"), 0644)

	server := &Server{}
	_ = server.RegisterPlugin(antigravity.New())
	sess := &Session{ID: "sess-glob", Cwd: repoDir}

	// 1. Wildcard in path must NOT match files inside .worktrees via glob expansion
	resolved, err := server.resolveFilePath(sess, "*.png")
	if err == nil && resolved == secretFile {
		t.Fatalf("Expected *.png not to match inside worktree via glob fallback, got %q", resolved)
	}

	// 2. Traversal with wildcard (e.g. ../../../tmp/*) must not match via worktree/brain fallbacks
	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/files/content?path=%s&cwd=%s", url.QueryEscape("../../../tmp/*"), url.QueryEscape(repoDir)), nil)
	rec := httptest.NewRecorder()
	server.handleFileContent(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("Expected 404 or 403 for wildcard traversal path, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFileOpen_VSCode_SecurityAndRemote(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	server := &Server{}
	_ = server.RegisterPlugin(antigravity.New())

	// 1. Leading dash rejected with 400
	dashBody, _ := json.Marshal(map[string]interface{}{
		"path": "--diff file1 file2",
		"app":  "vscode",
	})
	req := httptest.NewRequest("POST", "/v1/files/open", strings.NewReader(string(dashBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleFileOpen(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for leading dash path, got %d", rec.Code)
	}

	// 2. Local app=vscode with sensitive path ~/.ssh/id_rsa returns 403 Forbidden
	sshDir := filepath.Join(homeDir, ".ssh")
	_ = os.MkdirAll(sshDir, 0700)
	idRsa := filepath.Join(sshDir, "id_rsa")
	_ = os.WriteFile(idRsa, []byte("key"), 0600)

	sshBody, _ := json.Marshal(map[string]interface{}{
		"path": idRsa,
		"app":  "vscode",
	})
	reqSSH := httptest.NewRequest("POST", "/v1/files/open", strings.NewReader(string(sshBody)))
	reqSSH.Header.Set("Content-Type", "application/json")
	recSSH := httptest.NewRecorder()
	server.handleFileOpen(recSSH, reqSSH)
	if recSSH.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden for ~/.ssh/id_rsa in vscode, got %d: %s", recSSH.Code, recSSH.Body.String())
	}

	// 3. Remote app=vscode with remote-only cwd does not fail local root validation
	remoteBody, _ := json.Marshal(map[string]interface{}{
		"path": "src/main.rs:42:5",
		"cwd":  "/remote/only/unique/directory",
		"host": "remote-devbox",
		"app":  "vscode",
	})
	reqRemote := httptest.NewRequest("POST", "/v1/files/open", strings.NewReader(string(remoteBody)))
	reqRemote.Header.Set("Content-Type", "application/json")
	recRemote := httptest.NewRecorder()
	server.handleFileOpen(recRemote, reqRemote)
	if recRemote.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for remote vscode open, got %d: %s", recRemote.Code, recRemote.Body.String())
	}
	var respRemote map[string]interface{}
	_ = json.Unmarshal(recRemote.Body.Bytes(), &respRemote)
	expectedPath := "/remote/only/unique/directory/src/main.rs:42:5"
	if respRemote["path"] != expectedPath {
		t.Errorf("Expected path %q, got %q", expectedPath, respRemote["path"])
	}
	if uri, ok := respRemote["uri"].(string); !ok || !strings.Contains(uri, "ssh-remote+remote-devbox") {
		t.Errorf("Expected remote vscode URI with ssh-remote, got %q", uri)
	}

	// 4. Remote app=vscode with sensitive path /etc/shadow returns 403 Forbidden
	shadowBody, _ := json.Marshal(map[string]interface{}{
		"path": "/etc/shadow",
		"host": "remote-devbox",
		"app":  "vscode",
	})
	reqShadow := httptest.NewRequest("POST", "/v1/files/open", strings.NewReader(string(shadowBody)))
	reqShadow.Header.Set("Content-Type", "application/json")
	recShadow := httptest.NewRecorder()
	server.handleFileOpen(recShadow, reqShadow)
	if recShadow.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden for remote /etc/shadow in vscode, got %d: %s", recShadow.Code, recShadow.Body.String())
	}
}

func TestHandleFileContent_ProxiedSVG_SecurityHeaders(t *testing.T) {
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<svg></svg>"))
	}))
	defer remoteServer.Close()

	tmpDir, err := os.MkdirTemp("", "ackbar-svg-proxy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := InitDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	_ = db.SaveHost(&HostRecord{
		Name: "svg-host",
		URL:  remoteServer.URL,
	})

	server := NewServer(db)

	req := httptest.NewRequest("GET", "/v1/files/content?path=logo.svg&host=svg-host", nil)
	rec := httptest.NewRecorder()
	server.handleFileContent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for proxied SVG, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg+xml") {
		t.Errorf("Expected Content-Type image/svg+xml, got %q", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Expected CSP default-src 'none', got %q", csp)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Errorf("Expected X-Content-Type-Options: nosniff, got %q", nosniff)
	}
}
