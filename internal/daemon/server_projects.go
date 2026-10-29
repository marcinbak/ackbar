package daemon

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type DocumentItem struct {
	Title         string `json:"title"`
	Path          string `json:"path"`
	RelPath       string `json:"rel_path"`
	Category      string `json:"category"`
	CategoryLabel string `json:"category_label"`
	Priority      int    `json:"priority"`
	Size          int64  `json:"size"`
	ModTime       string `json:"mod_time,omitempty"`
}

func classifyDoc(name string) (category, label string, priority int) {
	nl := strings.ToLower(name)
	switch {
	case nl == "task.md" || nl == "implementation_plan.md" || nl == "walkthrough.md":
		return "plan", "Active Plan", 10
	case strings.Contains(nl, "plan") || strings.Contains(nl, "todo") || strings.Contains(nl, "backlog"):
		return "plan", "Plan / Task", 8
	case nl == "agents.md" || nl == "claude.md" || nl == "architecture.md":
		return "guidelines", "Guidelines", 7
	case nl == "readme.md" || strings.HasPrefix(nl, "readme"):
		return "project", "Readme", 5
	case strings.Contains(nl, "prd") || strings.Contains(nl, "rfc") || strings.Contains(nl, "spec"):
		return "project", "Specification", 6
	case strings.Contains(nl, "guide") || strings.Contains(nl, "handover"):
		return "guidelines", "Guide", 6
	default:
		return "other", "Document", 2
	}
}

func getDocPriority(name string) int {
	_, _, prio := classifyDoc(name)
	return prio
}

func (s *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cwd := r.URL.Query().Get("cwd")
	agent := r.URL.Query().Get("agent")
	nativeID := r.URL.Query().Get("native_id")
	sessionID := r.URL.Query().Get("session_id")

	// If sessionID is provided, attempt to resolve session metadata
	if sessionID != "" {
		if sess, err := s.db.GetSession(sessionID); err == nil && sess != nil {
			if cwd == "" {
				cwd = sess.Cwd
			}
			if agent == "" {
				agent = sess.Agent
			}
			if nativeID == "" {
				nativeID = sess.NativeID
			}
		}
	}

	if cwd == "" {
		cwd = os.Getenv("HOME")
	}

	var docs []DocumentItem
	seen := make(map[string]bool)

	addDoc := func(fullPath, title, relPath, category, categoryLabel string, priority int) {
		if seen[fullPath] {
			return
		}
		fi, err := os.Stat(fullPath)
		if err != nil || fi.IsDir() {
			return
		}
		seen[fullPath] = true
		docs = append(docs, DocumentItem{
			Title:         title,
			Path:          fullPath,
			RelPath:       relPath,
			Category:      category,
			CategoryLabel: categoryLabel,
			Priority:      priority,
			Size:          fi.Size(),
			ModTime:       fi.ModTime().Format(time.RFC3339),
		})
	}

	// 1. Scan root project directory for markdown files
	if entries, err := os.ReadDir(cwd); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			nameLower := strings.ToLower(name)
			if strings.HasSuffix(nameLower, ".md") || strings.HasSuffix(nameLower, ".markdown") {
				full := filepath.Join(cwd, name)
				cat, label, prio := classifyDoc(name)
				addDoc(full, name, name, cat, label, prio)
			}
		}
	}

	// 2. Scan docs/ and doc/ subdirectories (up to 3 levels deep)
	for _, docDirName := range []string{"docs", "doc", "documentation"} {
		docDir := filepath.Join(cwd, docDirName)
		if dirExists(docDir) {
			_ = filepath.WalkDir(docDir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d == nil {
					return nil
				}
				if d.IsDir() {
					rel, _ := filepath.Rel(docDir, p)
					if strings.Count(rel, string(filepath.Separator)) > 3 {
						return filepath.SkipDir
					}
					return nil
				}
				nameLower := strings.ToLower(d.Name())
				if strings.HasSuffix(nameLower, ".md") || strings.HasSuffix(nameLower, ".markdown") {
					rel, _ := filepath.Rel(cwd, p)
					addDoc(p, d.Name(), rel, "docs", "Project Docs", 4)
				}
				return nil
			})
		}
	}

	home, _ := os.UserHomeDir()
	// 3. Scan .claude/ directory if present
	(&ClaudeAgentAdapter{}).ListWorkspaceDocuments(home, cwd, nativeID, addDoc)

	// 4. Scan agent-specific conversation/brain documents
	if agent != "" && agent != "claude-code" {
		s.agentAdapter(agent).ListWorkspaceDocuments(home, cwd, nativeID, addDoc)
	}

	// Sort by Priority descending, then ModTime descending, then Title
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Priority != docs[j].Priority {
			return docs[i].Priority > docs[j].Priority
		}
		if docs[i].ModTime != docs[j].ModTime {
			return docs[i].ModTime > docs[j].ModTime
		}
		return docs[i].Title < docs[j].Title
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(docs)
}

func (s *Server) handleDocumentContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	docPath := r.URL.Query().Get("path")
	if docPath == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	content, err := os.ReadFile(docPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read file: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"path":    docPath,
		"content": string(content),
	})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		nodes, err := s.db.ListNodes()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nodes)

	case http.MethodPost:
		var node TreeNode
		if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		if node.Path == "" {
			http.Error(w, "Missing node path", http.StatusBadRequest)
			return
		}
		node.CreatedAt = time.Now()
		if err := s.db.SaveNode(&node); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"saved"}`))

	case http.MethodDelete:
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "Missing path parameter", http.StatusBadRequest)
			return
		}
		if err := s.db.DeleteNode(path); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"deleted"}`))

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleNodeMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if req.OldPath == "" || req.NewPath == "" {
		http.Error(w, "Missing old_path or new_path", http.StatusBadRequest)
		return
	}

	if err := s.db.MoveNode(req.OldPath, req.NewPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"moved"}`))
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path       string `json:"path"`        // Tree path, e.g. "Work/ProjectY/ProjectY-web"
		Name       string `json:"name"`        // Folder name or absolute/relative directory path
		ProjectDir string `json:"project_dir"` // Alternative directory field sent by Web GUI
		GitURL     string `json:"git_url"`     // Optional git origin
		BaseDir    string `json:"base_dir"`    // Optional base directory, defaults to ~/Projects
		CloneRepo  bool   `json:"clone_repo"`  // If true, clone repository into workspace directory
		Host       string `json:"host"`        // Optional target host ("local" or remote host alias)
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "Missing path", http.StatusBadRequest)
		return
	}

	// 1. Forward to remote host if specified
	if req.Host != "" && req.Host != "local" {
		hostRec, err := s.db.GetHost(req.Host)
		if err == nil && hostRec != nil && hostRec.URL != "" {
			targetURL := strings.TrimSuffix(hostRec.URL, "/") + "/v1/projects/create"
			remotePayload, _ := json.Marshal(map[string]interface{}{
				"path":        req.Path,
				"project_dir": req.ProjectDir,
				"name":        req.Name,
				"git_url":     req.GitURL,
				"base_dir":    req.BaseDir,
				"clone_repo":  req.CloneRepo,
			})
			resp, rErr := http.Post(targetURL, "application/json", bytes.NewBuffer(remotePayload))
			if rErr != nil {
				log.Printf("[Daemon] Warning: failed to forward project create to remote host %s: %v", req.Host, rErr)
			} else {
				_ = resp.Body.Close()
			}
		}
	}

	home, _ := os.UserHomeDir()

	var targetDir string
	var gitURL string
	alreadyExisted := false
	cloned := false

	folderInput := req.Name
	if folderInput == "" && req.ProjectDir != "" {
		folderInput = req.ProjectDir
	}

	if folderInput != "" {
		if strings.HasPrefix(folderInput, "~/") && home != "" {
			folderInput = filepath.Join(home, folderInput[2:])
		}

		if filepath.IsAbs(folderInput) || dirExists(folderInput) {
			targetDir = filepath.Clean(folderInput)
		} else {
			baseDir := req.BaseDir
			if baseDir == "" {
				if home != "" {
					baseDir = filepath.Join(home, "Projects")
				} else {
					baseDir = "."
				}
			}
			targetDir = filepath.Join(baseDir, folderInput)
		}

		alreadyExisted = dirExists(targetDir)
		hasGit := dirExists(filepath.Join(targetDir, ".git"))

		// If clone requested and directory is not yet a git repository
		if req.GitURL != "" && req.CloneRepo && !hasGit {
			log.Printf("[Daemon] Cloning repo %s into %s...", req.GitURL, targetDir)
			_ = os.MkdirAll(filepath.Dir(targetDir), 0755)
			out, cloneErr := exec.Command("git", "clone", req.GitURL, targetDir).CombinedOutput()
			if cloneErr != nil {
				log.Printf("[Daemon] Warning: git clone failed: %s (%v)", strings.TrimSpace(string(out)), cloneErr)
			} else {
				cloned = true
				alreadyExisted = true
			}
		}

		if !alreadyExisted {
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				log.Printf("[Daemon] Notice: could not create local directory %q: %v", targetDir, err)
			} else {
				alreadyExisted = true
			}
		}

		gitURL = req.GitURL
		if gitURL == "" && dirExists(filepath.Join(targetDir, ".git")) {
			out, err := exec.Command("git", "-C", targetDir, "remote", "get-url", "origin").Output()
			if err == nil {
				gitURL = strings.TrimSpace(string(out))
			}
		}

		targetDir = expandPath(targetDir)
	} else {
		gitURL = req.GitURL
	}

	node := &TreeNode{
		Path:       req.Path,
		ProjectDir: targetDir,
		GitURL:     gitURL,
		CreatedAt:  time.Now(),
	}

	if err := s.db.SaveNode(node); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save tree node: %v", err), http.StatusInternalServerError)
		return
	}

	// Auto-bind any existing sessions whose Cwd matches targetDir
	sessions, err := s.db.ListSessions()
	if err == nil {
		for _, sess := range sessions {
			if sess.Cwd != "" && sameOrSubDir(sess.Cwd, targetDir) {
				if sess.NodePath == "" || len(req.Path) >= len(sess.NodePath) {
					sess.NodePath = req.Path
					_ = s.db.SaveSession(sess)
					s.broadcast(sess)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "created",
		"path":            req.Path,
		"project_dir":     targetDir,
		"already_existed": alreadyExisted,
		"cloned":          cloned,
	})
}

func canonicalProjectRoot(dir, cleanHome string) (string, bool) {
	curr := filepath.Clean(dir)
	for curr != "" && curr != "/" && curr != "." && curr != cleanHome {
		if filepath.Base(filepath.Dir(curr)) == ".worktrees" {
			return curr, true
		}
		if _, err := os.Stat(filepath.Join(curr, ".git")); err == nil {
			return curr, true
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return dir, false
}

func isBroadHomeContainer(dir, cleanHome string) bool {
	if cleanHome == "" || filepath.Dir(dir) != cleanHome {
		return false
	}
	switch strings.ToLower(filepath.Base(dir)) {
	case "work", "projects", "code", "repos", "desktop", "documents", "downloads", "src", "dev", "workspace", "workspaces":
		return true
	}
	return false
}

func sameOrRelatedDir(dirA, dirB string) bool {
	if dirA == "" || dirB == "" {
		return false
	}
	a := expandPath(dirA)
	b := expandPath(dirB)
	if a == "" || b == "" || a == "/" || b == "/" || a == "." || b == "." {
		return false
	}
	home, _ := os.UserHomeDir()
	cleanHome := ""
	if home != "" {
		cleanHome = filepath.Clean(home)
		if a == cleanHome || b == cleanHome {
			return false
		}
	}
	if a == b {
		return true
	}
	rootA, markedA := canonicalProjectRoot(a, cleanHome)
	rootB, markedB := canonicalProjectRoot(b, cleanHome)
	if markedA || markedB {
		return markedA && markedB && rootA == rootB
	}
	if isBroadHomeContainer(a, cleanHome) || isBroadHomeContainer(b, cleanHome) {
		return false
	}
	return sameOrSubDir(a, b) || sameOrSubDir(b, a)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	cleaned := filepath.Clean(path)
	if eval, err := filepath.EvalSymlinks(cleaned); err == nil {
		return eval
	}
	return cleaned
}

func sameOrSubDir(cwd, projDir string) bool {
	if cwd == "" || projDir == "" {
		return false
	}
	c := expandPath(cwd)
	p := expandPath(projDir)

	if c == p {
		return true
	}
	if strings.HasPrefix(c, p+"/") {
		return true
	}
	return false
}

func (s *Server) resolveSessionNodePath(cwd string) string {
	if cwd == "" {
		return ""
	}
	nodes, err := s.db.ListNodes()
	if err != nil || len(nodes) == 0 {
		return ""
	}

	// 1. Longest exact or sub-directory match against registered node.ProjectDir
	var bestMatch string
	var bestLen int
	for _, n := range nodes {
		if n.ProjectDir != "" && sameOrSubDir(cwd, n.ProjectDir) {
			if len(n.ProjectDir) > bestLen {
				bestMatch = n.Path
				bestLen = len(n.ProjectDir)
			}
		}
	}
	if bestMatch != "" {
		return bestMatch
	}

	// 2. Segment match: check from leaf to group prefixes
	cleanCwd := strings.ToLower(filepath.Clean(cwd))
	cwdParts := strings.Split(cleanCwd, string(filepath.Separator))

	// Check if any registered node leaf matches cwd (e.g. "ngl-android" in "Modemobile/NGL/ngl-android")
	for _, n := range nodes {
		leaf := strings.ToLower(filepath.Base(n.Path))
		if len(leaf) > 3 {
			for _, part := range cwdParts {
				if part == leaf {
					return n.Path
				}
			}
		}
	}

	// Check if any group prefix matches cwd (e.g. "Modemobile" in "/home/dev4u/Work/modemobile")
	for _, n := range nodes {
		parts := strings.Split(n.Path, "/")
		for _, seg := range parts {
			segLower := strings.ToLower(seg)
			if len(segLower) > 3 {
				for _, part := range cwdParts {
					if part == segLower {
						return seg
					}
				}
			}
		}
	}

	return ""
}

func (s *Server) handleEditorOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	host := r.URL.Query().Get("host")
	if path == "" {
		var bodyData struct {
			Path string `json:"path"`
			Host string `json:"host"`
		}
		_ = json.NewDecoder(r.Body).Decode(&bodyData)
		path = bodyData.Path
		if host == "" {
			host = bodyData.Host
		}
	}
	if path == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	isLocal := host == "" || host == "local" || host == "localhost" || host == "127.0.0.1" || (os.Getenv("ACKBAR_HOST") != "" && host == os.Getenv("ACKBAR_HOST"))
	if isLocal && filepath.IsAbs(path) {
		if _, err := os.Stat(path); err != nil && os.IsNotExist(err) {
			http.Error(w, fmt.Sprintf("File not found: %s", path), http.StatusNotFound)
			return
		}
	}

	uri, err := LaunchVSCode(path, host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened", "path": path, "host": host, "uri": uri})
}

func cleanEnvForVSCode(env []string) []string {
	var cleaned []string
	for _, e := range env {
		if strings.HasPrefix(e, "VSCODE_IPC_HOOK_CLI=") ||
			strings.HasPrefix(e, "ELECTRON_RUN_AS_NODE=") ||
			strings.HasPrefix(e, "NODE_OPTIONS=") {
			continue
		}
		cleaned = append(cleaned, e)
	}
	return cleaned
}

func isTestEnv() bool {
	return flag.Lookup("test.v") != nil || os.Getenv("ACKBAR_TEST_MODE") == "1"
}

// LaunchVSCodeFunc allows mocking or overriding VS Code launching in tests.
var LaunchVSCodeFunc = defaultLaunchVSCode

func LaunchVSCode(path, host string) (string, error) {
	return LaunchVSCodeFunc(path, host)
}

func defaultLaunchVSCode(path, host string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}

	var vscodeURI string
	isRemote := host != "" && host != "local" && host != "localhost" && host != "127.0.0.1" && (os.Getenv("ACKBAR_HOST") == "" || host != os.Getenv("ACKBAR_HOST"))

	if isRemote {
		hostLabel := host
		formattedPath := path
		if !strings.HasPrefix(formattedPath, "/") {
			formattedPath = "/" + formattedPath
		}
		vscodeURI = fmt.Sprintf("vscode://vscode-remote/ssh-remote+%s%s", hostLabel, formattedPath)
	} else {
		formattedPath := path
		if !strings.HasPrefix(formattedPath, "/") {
			formattedPath = "/" + formattedPath
		}
		vscodeURI = fmt.Sprintf("vscode://file%s", formattedPath)
	}

	// Under automated tests (e.g. go test), do not launch real external GUI processes
	if isTestEnv() {
		return vscodeURI, nil
	}

	var launchErr error
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("open", vscodeURI)
		if err := cmd.Start(); err == nil {
			return vscodeURI, nil
		} else {
			launchErr = err
		}
	} else if runtime.GOOS == "linux" {
		cmd := exec.Command("xdg-open", vscodeURI)
		if err := cmd.Start(); err == nil {
			return vscodeURI, nil
		} else {
			launchErr = err
		}
	}

	codeBin := findCodeBinary()
	var cliCmd *exec.Cmd
	if isRemote {
		cliCmd = exec.Command(codeBin, "--remote", fmt.Sprintf("ssh-remote+%s", host), path)
	} else {
		cliCmd = exec.Command(codeBin, path)
	}
	cliCmd.Env = cleanEnvForVSCode(os.Environ())

	if err := cliCmd.Start(); err == nil {
		return vscodeURI, nil
	} else {
		if launchErr != nil {
			return vscodeURI, fmt.Errorf("failed to open via URL (%v) and CLI (%s: %v)", launchErr, codeBin, err)
		}
		return vscodeURI, fmt.Errorf("failed to launch VS Code (%s): %v", codeBin, err)
	}
}

func findCodeBinary() string {
	if p, err := exec.LookPath("code"); err == nil {
		return p
	}
	candidates := []string{
		"/usr/local/bin/code",
		"/opt/homebrew/bin/code",
		"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
		"/Applications/Visual Studio Code - Insiders.app/Contents/Resources/app/bin/code",
		"/Applications/Cursor.app/Contents/Resources/app/bin/cursor",
		"/Applications/Windsurf.app/Contents/Resources/app/bin/windsurf",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "code"
}
