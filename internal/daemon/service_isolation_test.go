package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEnsureServiceProcessIsolation_SystemdUnit(t *testing.T) {
	home := t.TempDir()
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	unitPath := filepath.Join(unitDir, "ackbard.service")

	// Missing unit file is a clean no-op
	changed, err := EnsureServiceProcessIsolation(home)
	if err != nil || changed {
		t.Fatalf("expected no-op on missing unit, got changed=%v err=%v", changed, err)
	}

	// Adds KillMode=process when missing under [Service]
	initial := "[Unit]\nDescription=Ackbar Daemon\n\n[Service]\nExecStart=/usr/local/bin/ackbard\nRestart=always\n\n[Install]\nWantedBy=default.target\n"
	if err := os.WriteFile(unitPath, []byte(initial), 0644); err != nil {
		t.Fatalf("write unit: %v", err)
	}
	changed, err = EnsureServiceProcessIsolation(home)
	if err != nil || !changed {
		t.Fatalf("expected changed=true when adding KillMode=process, got changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(unitPath)
	if !strings.Contains(string(got), "[Service]\nExecStart=/usr/local/bin/ackbard\nRestart=always\nKillMode=process\n") {
		t.Fatalf("unexpected systemd unit content after insert:\n%s", string(got))
	}

	// Idempotent second call
	changed, err = EnsureServiceProcessIsolation(home)
	if err != nil || changed {
		t.Fatalf("expected idempotent changed=false, got changed=%v err=%v", changed, err)
	}

	// Replaces KillMode=control-group with KillMode=process
	withControlGroup := "[Service]\nExecStart=/usr/local/bin/ackbard\nKillMode=control-group\n"
	if err := os.WriteFile(unitPath, []byte(withControlGroup), 0644); err != nil {
		t.Fatalf("write unit: %v", err)
	}
	changed, err = EnsureServiceProcessIsolation(home)
	if err != nil || !changed {
		t.Fatalf("expected changed=true when replacing KillMode=control-group, got changed=%v err=%v", changed, err)
	}
	got2, _ := os.ReadFile(unitPath)
	if strings.Contains(string(got2), "control-group") || !strings.Contains(string(got2), "KillMode=process") {
		t.Fatalf("expected KillMode=control-group replaced, got:\n%s", string(got2))
	}

	// Ignores symlinks
	realTarget := filepath.Join(t.TempDir(), "readonly.service")
	_ = os.WriteFile(realTarget, []byte("[Service]\nExecStart=/bin/true\n"), 0644)
	_ = os.Remove(unitPath)
	if err := os.Symlink(realTarget, unitPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	changed, err = EnsureServiceProcessIsolation(home)
	if err != nil || changed {
		t.Fatalf("expected symlink skipped, got changed=%v err=%v", changed, err)
	}
	targetData, _ := os.ReadFile(realTarget)
	if strings.Contains(string(targetData), "KillMode=process") {
		t.Fatalf("symlink target must never be modified")
	}
}

func TestEnsureServiceProcessIsolation_LaunchdPlist(t *testing.T) {
	home := t.TempDir()
	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mainPlist := filepath.Join(agentsDir, "com.marcinbak.ackbard.plist")
	brewPlist := filepath.Join(agentsDir, "homebrew.mxcl.ackbar.plist")

	missingKeyPlist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.marcinbak.ackbard</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/usr/local/bin:/usr/bin</string>
	</dict>
</dict>
</plist>
`
	if err := os.WriteFile(mainPlist, []byte(missingKeyPlist), 0644); err != nil {
		t.Fatalf("write plist: %v", err)
	}

	falseKeyPlist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>homebrew.mxcl.ackbar</string>
	<key>AbandonProcessGroup</key>
	<false/>
</dict>
</plist>
`
	if err := os.WriteFile(brewPlist, []byte(falseKeyPlist), 0644); err != nil {
		t.Fatalf("write brew plist: %v", err)
	}

	changed, err := EnsureServiceProcessIsolation(home)
	if err != nil || !changed {
		t.Fatalf("expected changed=true for launchd plists, got changed=%v err=%v", changed, err)
	}

	gotMain, _ := os.ReadFile(mainPlist)
	if !plistAbandonTrueRegex.MatchString(string(gotMain)) {
		t.Fatalf("expected AbandonProcessGroup true injected before final </dict>, got:\n%s", string(gotMain))
	}
	if strings.LastIndex(string(gotMain), "AbandonProcessGroup") < strings.Index(string(gotMain), "EnvironmentVariables") {
		t.Fatalf("AbandonProcessGroup injected into nested dict instead of top-level dict:\n%s", string(gotMain))
	}

	gotBrew, _ := os.ReadFile(brewPlist)
	if strings.Contains(string(gotBrew), "<false/>") || !plistAbandonTrueRegex.MatchString(string(gotBrew)) {
		t.Fatalf("expected <false/> updated to <true/>, got:\n%s", string(gotBrew))
	}

	// Idempotent on second call
	changed, err = EnsureServiceProcessIsolation(home)
	if err != nil || changed {
		t.Fatalf("expected idempotent changed=false on second call, got changed=%v err=%v", changed, err)
	}

	// Skips symlinks (e.g. Homebrew Cellar symlink) and read-only files
	cellarTarget := filepath.Join(t.TempDir(), "cellar.plist")
	_ = os.WriteFile(cellarTarget, []byte(missingKeyPlist), 0644)
	_ = os.Remove(brewPlist)
	if err := os.Symlink(cellarTarget, brewPlist); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	_ = os.WriteFile(mainPlist, []byte(missingKeyPlist), 0644)
	_ = os.Chmod(mainPlist, 0444)
	t.Cleanup(func() { _ = os.Chmod(mainPlist, 0644) })

	changed, err = EnsureServiceProcessIsolation(home)
	if err != nil || changed {
		t.Fatalf("expected symlinks and read-only plists skipped, got changed=%v err=%v", changed, err)
	}
	cellarData, _ := os.ReadFile(cellarTarget)
	if strings.Contains(string(cellarData), "AbandonProcessGroup") {
		t.Fatalf("symlinked Cellar plist must never be modified")
	}
}

func TestDaemonSafeShellHelpers_SkipsHeadlessWorkerPIDs(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep not installed")
	}
	tmpDir := t.TempDir()
	procName := fmt.Sprintf("ackt%d", os.Getpid()%100000)
	scriptPath := filepath.Join(tmpDir, procName)
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 30\n"), 0755); err != nil {
		t.Fatalf("write test script: %v", err)
	}

	workerCmd := exec.Command(scriptPath, "--headless-worker", "--worker-spec", "/tmp/w.json")
	if err := workerCmd.Start(); err != nil {
		t.Fatalf("start worker proc: %v", err)
	}
	defer func() {
		if workerCmd.Process != nil {
			_ = workerCmd.Process.Kill()
			_ = workerCmd.Wait()
		}
	}()

	daemonCmd := exec.Command(scriptPath, "--host", "127.0.0.1", "--port", "7777")
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon proc: %v", err)
	}
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- daemonCmd.Wait() }()
	defer func() {
		if daemonCmd.Process != nil {
			_ = syscall.Kill(daemonCmd.Process.Pid, syscall.SIGKILL)
		}
	}()

	waitForWorkerCond(t, 2*time.Second, "both test processes visible to pgrep", func() bool {
		out, err := exec.Command("pgrep", "-x", procName).Output()
		if err != nil {
			return false
		}
		lines := strings.Fields(strings.TrimSpace(string(out)))
		return len(lines) >= 2
	})

	checkBefore := exec.Command("sh", "-c", DaemonSafeShellHelpers()+"\nhas_non_worker_ackbard")
	checkBefore.Env = append(os.Environ(), "ACKBAR_DAEMON_PROC_NAME="+procName)
	if err := checkBefore.Run(); err != nil {
		t.Fatalf("expected has_non_worker_ackbard to succeed while daemon proc is alive: %v", err)
	}

	killCmd := exec.Command("sh", "-c", DaemonSafeShellHelpers()+"\nsignal_non_worker_ackbard 15")
	killCmd.Env = append(os.Environ(), "ACKBAR_DAEMON_PROC_NAME="+procName)
	if out, err := killCmd.CombinedOutput(); err != nil {
		t.Fatalf("signal_non_worker_ackbard failed: %v (%s)", err, string(out))
	}

	select {
	case <-daemonDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected non-worker daemon process PID %d to be signaled and exit", daemonCmd.Process.Pid)
	}

	if !isProcessAlive(workerCmd.Process.Pid) {
		t.Fatalf("expected --headless-worker PID %d to survive signal_non_worker_ackbard", workerCmd.Process.Pid)
	}

	checkAfter := exec.Command("sh", "-c", DaemonSafeShellHelpers()+"\nhas_non_worker_ackbard")
	checkAfter.Env = append(os.Environ(), "ACKBAR_DAEMON_PROC_NAME="+procName)
	if err := checkAfter.Run(); err == nil {
		t.Fatalf("expected has_non_worker_ackbard to return non-zero when only --headless-worker remains")
	}
}

func TestServiceIsolation_PreservesRestrictive0600Mode(t *testing.T) {
	home := t.TempDir()
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	_ = os.MkdirAll(unitDir, 0755)
	_ = os.MkdirAll(agentsDir, 0755)

	unitPath := filepath.Join(unitDir, "ackbard.service")
	plistPath := filepath.Join(agentsDir, "com.marcinbak.ackbard.plist")

	unitBody := "[Service]\nEnvironment=ACKBAR_TOKEN=secret\nExecStart=/usr/local/bin/ackbard\n"
	plistBody := "<?xml version=\"1.0\"?>\n<plist version=\"1.0\">\n<dict>\n\t<key>Label</key>\n\t<string>com.marcinbak.ackbard</string>\n</dict>\n</plist>\n"
	_ = os.WriteFile(unitPath, []byte(unitBody), 0600)
	_ = os.Chmod(unitPath, 0600)
	_ = os.WriteFile(plistPath, []byte(plistBody), 0600)
	_ = os.Chmod(plistPath, 0600)

	changed, err := EnsureServiceProcessIsolation(home)
	if err != nil || !changed {
		t.Fatalf("EnsureServiceProcessIsolation: changed=%v err=%v", changed, err)
	}
	if fi, err := os.Stat(unitPath); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("expected systemd unit to retain 0600 mode, got %v (err=%v)", fi, err)
	}
	if fi, err := os.Stat(plistPath); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("expected launchd plist to retain 0600 mode, got %v (err=%v)", fi, err)
	}

	// Verify shell self_heal_ackbard_service also preserves 0600 for both systemd and launchd (via mock plutil)
	_ = os.WriteFile(unitPath, []byte(unitBody), 0600)
	_ = os.Chmod(unitPath, 0600)
	_ = os.WriteFile(plistPath, []byte(plistBody), 0600)
	_ = os.Chmod(plistPath, 0600)

	mockBin := t.TempDir()
	mockPlutil := filepath.Join(mockBin, "plutil")
	_ = os.WriteFile(mockPlutil, []byte("#!/bin/sh\nfor target in \"$@\"; do :; done\n[ -f \"$target\" ] || exit 1\nsed 's|</dict>|\\t<key>AbandonProcessGroup</key>\\n\\t<true/>\\n</dict>|' \"$target\" > \"$target.out\" && mv -f \"$target.out\" \"$target\"\n"), 0755)

	cmd := exec.Command("sh", "-c", DaemonSafeShellHelpers()+"\nself_heal_ackbard_service")
	cmd.Env = []string{"HOME=" + home, "PATH=" + mockBin + ":" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self_heal_ackbard_service failed: %v (%s)", err, string(out))
	}
	if fi, err := os.Stat(unitPath); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("expected shell self-heal systemd unit to retain 0600, got %v (err=%v)", fi, err)
	}
	if unitOut, _ := os.ReadFile(unitPath); !strings.Contains(string(unitOut), "KillMode=process") {
		t.Fatalf("expected shell self-heal to add KillMode=process, got:\n%s", string(unitOut))
	}
	if fi, err := os.Stat(plistPath); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("expected shell self-heal launchd plist to retain 0600, got %v (err=%v)", fi, err)
	}
	if plistOut, _ := os.ReadFile(plistPath); !plistAbandonTrueRegex.MatchString(string(plistOut)) {
		t.Fatalf("expected shell self-heal to add AbandonProcessGroup true, got:\n%s", string(plistOut))
	}
}
