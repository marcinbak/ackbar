package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helper to setup a bare remote and an initial clone
func setupTestGitRepos(t *testing.T, tempHome string) (remoteURL string, localPath string) {
	t.Helper()

	// 1. Create bare remote repository
	remoteDir := filepath.Join(tempHome, "remote.git")
	if err := os.MkdirAll(remoteDir, 0755); err != nil {
		t.Fatalf("failed to create remote dir: %v", err)
	}
	cmdInitBare := exec.Command("git", "init", "--bare", remoteDir)
	if out, err := cmdInitBare.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare failed: %v (%s)", err, out)
	}

	// 2. Clone to a temporary workspace to create initial commit and install.sh
	tempSeed := filepath.Join(tempHome, "seed-clone")
	cmdCloneSeed := exec.Command("git", "clone", remoteDir, tempSeed)
	if out, err := cmdCloneSeed.CombinedOutput(); err != nil {
		t.Fatalf("git clone seed failed: %v (%s)", err, out)
	}

	// Configure git user in seed
	_ = exec.Command("git", "-C", tempSeed, "config", "user.name", "Ackbar Tester").Run()
	_ = exec.Command("git", "-C", tempSeed, "config", "user.email", "tester@ackbar.local").Run()

	// Create install.sh in seed
	installSh := `#!/usr/bin/env bash
set -e
echo "installed_ok" > "$HOME/installed_marker.txt"
if [ -n "$ACKBAR_CLAUDE_DIRS" ]; then
  echo "$ACKBAR_CLAUDE_DIRS" > "$HOME/claude_dirs.txt"
fi
`
	if err := os.WriteFile(filepath.Join(tempSeed, "install.sh"), []byte(installSh), 0755); err != nil {
		t.Fatalf("failed to write install.sh: %v", err)
	}

	// Add README
	if err := os.WriteFile(filepath.Join(tempSeed, "README.md"), []byte("# Skills Repo\n"), 0644); err != nil {
		t.Fatalf("failed to write README: %v", err)
	}

	_ = exec.Command("git", "-C", tempSeed, "add", ".").Run()
	cmdCommit := exec.Command("git", "-C", tempSeed, "commit", "-m", "Initial commit")
	if out, err := cmdCommit.CombinedOutput(); err != nil {
		t.Fatalf("seed commit failed: %v (%s)", err, out)
	}

	cmdPush := exec.Command("git", "-C", tempSeed, "push", "origin", "HEAD")
	if out, err := cmdPush.CombinedOutput(); err != nil {
		t.Fatalf("seed push failed: %v (%s)", err, out)
	}

	// 3. Clone to local repository under ~/Work/Personal/skills
	localDir := filepath.Join(tempHome, "Work", "Personal", "skills")
	if err := os.MkdirAll(filepath.Dir(localDir), 0755); err != nil {
		t.Fatalf("failed to create personal dir: %v", err)
	}
	cmdCloneLocal := exec.Command("git", "clone", remoteDir, localDir)
	if out, err := cmdCloneLocal.CombinedOutput(); err != nil {
		t.Fatalf("git clone local failed: %v (%s)", err, out)
	}

	_ = exec.Command("git", "-C", localDir, "config", "user.name", "Ackbar Tester").Run()
	_ = exec.Command("git", "-C", localDir, "config", "user.email", "tester@ackbar.local").Run()

	return remoteDir, localDir
}

func TestSkillsRepo_Validation(t *testing.T) {
	tempHome := t.TempDir()

	// validateRepoPath tests
	valid, err := validateRepoPath(tempHome, "~/Work/skills")
	if err != nil || valid != filepath.Join(tempHome, "Work", "skills") {
		t.Errorf("expected valid path ~/Work/skills, got %s (err: %v)", valid, err)
	}

	validRel, err := validateRepoPath(tempHome, "Work/skills")
	if err != nil || validRel != filepath.Join(tempHome, "Work", "skills") {
		t.Errorf("expected valid relative path, got %s (err: %v)", validRel, err)
	}

	// Reject starts with -
	if _, err := validateRepoPath(tempHome, "-dangerous"); err == nil {
		t.Errorf("expected error for path starting with '-'")
	}

	// Reject home directory itself
	if _, err := validateRepoPath(tempHome, "~"); err == nil {
		t.Errorf("expected error for path equal to home directory")
	}

	// Reject path outside home
	if _, err := validateRepoPath(tempHome, "/var/log"); err == nil {
		t.Errorf("expected error for path outside home")
	}

	// Reject sensitive directories
	if _, err := validateRepoPath(tempHome, "~/.ssh/id_rsa"); err == nil {
		t.Errorf("expected error for path inside ~/.ssh")
	}
	if _, err := validateRepoPath(tempHome, "~/.claude/something"); err == nil {
		t.Errorf("expected error for path inside ~/.claude")
	}

	// validateGitURL tests
	if err := validateGitURL("https://github.com/org/repo.git"); err != nil {
		t.Errorf("expected valid https url, got: %v", err)
	}
	if err := validateGitURL("git@github.com:org/repo.git"); err != nil {
		t.Errorf("expected valid git@ url, got: %v", err)
	}
	localDir := filepath.Join(tempHome, "fake-repo.git")
	_ = os.MkdirAll(localDir, 0755)
	if err := validateGitURL(localDir); err != nil {
		t.Errorf("expected valid local path git url for existing dir, got: %v", err)
	}
	if err := validateGitURL("/nonexistent/path/repo.git"); err == nil {
		t.Errorf("expected error for non-existent local git directory")
	}
	if err := validateGitURL("-u dangerous"); err == nil {
		t.Errorf("expected error for git url starting with '-'")
	}
	if err := validateGitURL("ftp://bad.com/repo"); err == nil {
		t.Errorf("expected error for ftp url")
	}
	if err := validateGitURL(""); err == nil {
		t.Errorf("expected error for empty url")
	}
}

func TestSkillsRepo_Discovery(t *testing.T) {
	tempHome := t.TempDir()
	db, err := InitDB(filepath.Join(tempHome, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	remoteURL, localRepo := setupTestGitRepos(t, tempHome)

	// 1. Discovery via DB settings
	_ = db.SetSettings(map[string]string{
		"skills_repo_path": localRepo,
		"skills_repo_url":  remoteURL,
	})
	discoveredPath, discoveredURL := DiscoverSkillsRepo(tempHome, db)
	if discoveredPath != localRepo {
		t.Errorf("expected discovered path %s, got %s", localRepo, discoveredPath)
	}
	if discoveredURL != remoteURL {
		t.Errorf("expected discovered url %s, got %s", remoteURL, discoveredURL)
	}

	// 2. Clear DB settings and test Symlink discovery
	_ = db.SetSettings(map[string]string{
		"skills_repo_path": "",
		"skills_repo_url":  "",
	})

	claudeSkillsDir := filepath.Join(tempHome, ".claude", "skills")
	_ = os.MkdirAll(claudeSkillsDir, 0755)
	targetSkillDir := filepath.Join(localRepo, "my-skill")
	_ = os.MkdirAll(targetSkillDir, 0755)
	_ = os.WriteFile(filepath.Join(targetSkillDir, "SKILL.md"), []byte("# My Skill\n"), 0644)

	symlinkPath := filepath.Join(claudeSkillsDir, "my-skill")
	if err := os.Symlink(targetSkillDir, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	discoveredSymPath, discoveredSymURL := DiscoverSkillsRepo(tempHome, db)
	if discoveredSymPath != localRepo {
		t.Errorf("expected discovered symlink repo %s, got %s", localRepo, discoveredSymPath)
	}
	if discoveredSymURL != remoteURL {
		t.Errorf("expected discovered symlink url %s, got %s", remoteURL, discoveredSymURL)
	}

	// 3. Marker file discovery
	_ = db.SetSettings(map[string]string{
		"skills_repo_path": "",
		"skills_repo_url":  "",
	})
	_ = os.Remove(symlinkPath)

	geminiSkillsDir := filepath.Join(tempHome, ".gemini", "config", "skills", "marker-skill")
	_ = os.MkdirAll(geminiSkillsDir, 0755)
	markerFile := filepath.Join(geminiSkillsDir, ".ackbar-skills-source")
	_ = os.WriteFile(markerFile, []byte(fmt.Sprintf("source_repo=%s\n", localRepo)), 0644)

	discoveredMarkerPath, _ := DiscoverSkillsRepo(tempHome, db)
	if discoveredMarkerPath != localRepo {
		t.Errorf("expected discovered marker repo %s, got %s", localRepo, discoveredMarkerPath)
	}

	// 4. JSON marker file discovery (.personal-config-managed)
	_ = db.SetSettings(map[string]string{
		"skills_repo_path": "",
		"skills_repo_url":  "",
	})
	_ = os.Remove(markerFile)

	jsonSkillDir := filepath.Join(tempHome, ".gemini", "antigravity-cli", "skills", "json-skill")
	_ = os.MkdirAll(jsonSkillDir, 0755)
	jsonMarkerFile := filepath.Join(jsonSkillDir, ".personal-config-managed")
	jsonContent := fmt.Sprintf(`{"source_repo": %q, "files": {}}`, localRepo)
	_ = os.WriteFile(jsonMarkerFile, []byte(jsonContent), 0644)

	discoveredJSONPath, _ := DiscoverSkillsRepo(tempHome, db)
	if discoveredJSONPath != localRepo {
		t.Errorf("expected discovered JSON marker repo %s, got %s", localRepo, discoveredJSONPath)
	}
}

func TestSkillsRepo_StatusAndDriftDetection(t *testing.T) {
	tempHome := t.TempDir()
	db, err := InitDB(filepath.Join(tempHome, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	remoteURL, localRepo := setupTestGitRepos(t, tempHome)
	_ = db.SetSettings(map[string]string{
		"skills_repo_path": localRepo,
		"skills_repo_url":  remoteURL,
	})

	// 1. Clean status
	status := GetSkillsRepoStatus(tempHome, db, false)
	if !status.Configured || !status.Cloned {
		t.Errorf("expected configured and cloned to be true, got configured=%v cloned=%v", status.Configured, status.Cloned)
	}
	if !status.InstallerFound {
		t.Errorf("expected installer_found to be true")
	}
	if status.Dirty {
		t.Errorf("expected clean working tree initially")
	}
	if status.DriftDetected {
		t.Errorf("expected no drift initially")
	}

	// 2. Push a new commit to remote from seed clone
	seedClone := filepath.Join(tempHome, "seed-clone")
	_ = os.WriteFile(filepath.Join(seedClone, "newfile.txt"), []byte("remote update"), 0644)
	_ = exec.Command("git", "-C", seedClone, "add", ".").Run()
	_ = exec.Command("git", "-C", seedClone, "commit", "-m", "Remote update commit").Run()
	_ = exec.Command("git", "-C", seedClone, "push", "origin", "HEAD").Run()

	// Fetch remote and inspect status
	statusAfterPush := GetSkillsRepoStatus(tempHome, db, true)
	if statusAfterPush.BehindCount != 1 {
		t.Errorf("expected BehindCount=1 after remote push, got %d", statusAfterPush.BehindCount)
	}
	if !statusAfterPush.DriftDetected {
		t.Errorf("expected drift detected when behind count > 0")
	}

	// 3. Dirty working tree detection
	_ = os.WriteFile(filepath.Join(localRepo, "local_dirty.txt"), []byte("dirty file"), 0644)
	statusDirty := GetSkillsRepoStatus(tempHome, db, false)
	if !statusDirty.Dirty {
		t.Errorf("expected dirty=true after local unstaged file created")
	}
	if !statusDirty.DriftDetected {
		t.Errorf("expected drift detected when dirty")
	}
}

func TestSkillsRepo_Sync_CleanFFAndInstaller(t *testing.T) {
	tempHome := t.TempDir()
	db, err := InitDB(filepath.Join(tempHome, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	remoteURL, localRepo := setupTestGitRepos(t, tempHome)
	_ = db.SetSettings(map[string]string{
		"skills_repo_path": localRepo,
		"skills_repo_url":  remoteURL,
	})

	// Add an account with custom config_dir
	_ = db.SaveAccount(&AgentAccount{
		Agent:       "claude-code",
		Name:        "work",
		DisplayName: "Work Account",
		ConfigDir:   filepath.Join(tempHome, ".claude-work"),
	})

	// Push a commit from seed
	seedClone := filepath.Join(tempHome, "seed-clone")
	_ = os.WriteFile(filepath.Join(seedClone, "feature.txt"), []byte("feature content"), 0644)
	_ = exec.Command("git", "-C", seedClone, "add", ".").Run()
	_ = exec.Command("git", "-C", seedClone, "commit", "-m", "Add feature").Run()
	_ = exec.Command("git", "-C", seedClone, "push", "origin", "HEAD").Run()

	// Sync local repository
	updatedStatus, err := SyncSkillsRepo(tempHome, db, SyncSkillsRepoOptions{})
	if err != nil {
		t.Fatalf("SyncSkillsRepo failed: %v", err)
	}

	if updatedStatus.BehindCount != 0 {
		t.Errorf("expected BehindCount=0 after pull, got %d", updatedStatus.BehindCount)
	}

	// Verify remote commit was pulled
	featureFile := filepath.Join(localRepo, "feature.txt")
	if _, err := os.Stat(featureFile); err != nil {
		t.Errorf("expected feature.txt to exist in local repo after sync: %v", err)
	}

	// Verify installer script executed
	markerFile := filepath.Join(tempHome, "installed_marker.txt")
	markerData, err := os.ReadFile(markerFile)
	if err != nil || strings.TrimSpace(string(markerData)) != "installed_ok" {
		t.Errorf("expected install.sh marker created with 'installed_ok', got: %s (err: %v)", string(markerData), err)
	}

	// Verify ACKBAR_CLAUDE_DIRS passed to installer
	claudeDirsFile := filepath.Join(tempHome, "claude_dirs.txt")
	claudeData, err := os.ReadFile(claudeDirsFile)
	if err != nil || !strings.Contains(string(claudeData), ".claude-work") {
		t.Errorf("expected claude_dirs.txt to contain .claude-work, got: %s (err: %v)", string(claudeData), err)
	}

	// Verify timestamps persisted
	lastSync, _ := db.GetSetting("skills_repo_last_sync")
	if lastSync == "" {
		t.Errorf("expected skills_repo_last_sync to be populated in DB")
	}
}

func TestSkillsRepo_Sync_DirtyPreservation(t *testing.T) {
	tempHome := t.TempDir()
	db, err := InitDB(filepath.Join(tempHome, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	remoteURL, localRepo := setupTestGitRepos(t, tempHome)
	_ = db.SetSettings(map[string]string{
		"skills_repo_path": localRepo,
		"skills_repo_url":  remoteURL,
	})

	// Make local repo dirty
	dirtyContent := "local uncommitted changes"
	dirtyFile := filepath.Join(localRepo, "dirty_work.txt")
	_ = os.WriteFile(dirtyFile, []byte(dirtyContent), 0644)

	// Sync
	status, err := SyncSkillsRepo(tempHome, db, SyncSkillsRepoOptions{})
	if err != nil {
		t.Fatalf("SyncSkillsRepo returned unexpected error on dirty tree: %v", err)
	}

	// Verify dirty file is preserved intact
	data, err := os.ReadFile(dirtyFile)
	if err != nil || string(data) != dirtyContent {
		t.Errorf("expected dirty file to be preserved, got: %s (err: %v)", string(data), err)
	}

	if !status.Dirty {
		t.Errorf("expected status.Dirty=true")
	}
}

func TestSkillsRepo_Sync_GuidedClone(t *testing.T) {
	tempHome := t.TempDir()
	db, err := InitDB(filepath.Join(tempHome, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	remoteURL, _ := setupTestGitRepos(t, tempHome)

	// Configure remote URL only
	_ = db.SetSettings(map[string]string{
		"skills_repo_url":  remoteURL,
		"skills_repo_path": "",
	})

	// 1. Without CloneIfMissing: returns error requiring confirmation
	statusPrompt, err := SyncSkillsRepo(tempHome, db, SyncSkillsRepoOptions{CloneIfMissing: false})
	if err == nil {
		t.Fatalf("expected error requesting confirmation to clone, got nil")
	}
	if statusPrompt.Cloned {
		t.Errorf("expected Cloned=false before clone")
	}
	if statusPrompt.SuggestedPath == "" {
		t.Errorf("expected SuggestedPath to be computed")
	}

	// 2. With CloneIfMissing: clones and executes installer
	targetPath := filepath.Join(tempHome, "Work", "Personal", "custom-skills")
	statusCloned, err := SyncSkillsRepo(tempHome, db, SyncSkillsRepoOptions{
		RepoPath:       targetPath,
		CloneIfMissing: true,
	})
	if err != nil {
		t.Fatalf("guided clone failed: %v", err)
	}

	if !statusCloned.Cloned {
		t.Errorf("expected Cloned=true after clone")
	}
	if statusCloned.RepoPath != targetPath {
		t.Errorf("expected RepoPath=%s, got %s", targetPath, statusCloned.RepoPath)
	}

	// Verify install.sh ran
	markerFile := filepath.Join(tempHome, "installed_marker.txt")
	if _, err := os.Stat(markerFile); err != nil {
		t.Errorf("expected install.sh to run after clone: %v", err)
	}
}

func TestSkillsRepo_FallbackSkillsCopy(t *testing.T) {
	tempHome := t.TempDir()
	repoDir := filepath.Join(tempHome, "Work", "Personal", "raw-skills")
	_ = os.MkdirAll(repoDir, 0755)

	// Create skills/my-raw-skill/SKILL.md
	skillDir := filepath.Join(repoDir, "skills", "my-raw-skill")
	_ = os.MkdirAll(skillDir, 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Raw Skill\n"), 0644)

	// Create plugins/my-plugin/skills/plugin-skill/SKILL.md
	plugSkillDir := filepath.Join(repoDir, "plugins", "my-plugin", "skills", "plugin-skill")
	_ = os.MkdirAll(plugSkillDir, 0755)
	_ = os.WriteFile(filepath.Join(plugSkillDir, "SKILL.md"), []byte("# Plugin Skill\n"), 0644)

	err := fallbackInstallSkills(tempHome, repoDir)
	if err != nil {
		t.Fatalf("fallbackInstallSkills failed: %v", err)
	}

	targets := []string{
		filepath.Join(tempHome, ".claude", "skills"),
		filepath.Join(tempHome, ".gemini", "config", "skills"),
		filepath.Join(tempHome, ".gemini", "antigravity-cli", "skills"),
		filepath.Join(tempHome, ".codex", "skills"),
	}

	for _, tDir := range targets {
		if _, err := os.Stat(filepath.Join(tDir, "my-raw-skill", "SKILL.md")); err != nil {
			t.Errorf("expected skill deployed to %s: %v", tDir, err)
		}
		if _, err := os.Stat(filepath.Join(tDir, "plugin-skill", "SKILL.md")); err != nil {
			t.Errorf("expected plugin skill deployed to %s: %v", tDir, err)
		}
	}
}

func TestSkillsRepo_MaybeAutoSync(t *testing.T) {
	tempHome := t.TempDir()
	db, err := InitDB(filepath.Join(tempHome, "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	remoteURL, localRepo := setupTestGitRepos(t, tempHome)
	_ = db.SetSettings(map[string]string{
		"skills_repo_path":       localRepo,
		"skills_repo_url":        remoteURL,
		"skills_repo_last_check": time.Now().Add(-25 * time.Hour).Format(time.RFC3339),
	})

	MaybeAutoSyncSkillsRepo(context.Background(), tempHome, db)

	// Should have executed install.sh
	markerFile := filepath.Join(tempHome, "installed_marker.txt")
	if _, err := os.Stat(markerFile); err != nil {
		t.Errorf("expected MaybeAutoSyncSkillsRepo to trigger sync and run installer: %v", err)
	}
}
