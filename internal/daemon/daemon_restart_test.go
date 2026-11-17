package daemon

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDaemonRestartScripts_SyntaxAndManagedIsolation(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{
			name:   "remote_binary_restart",
			script: buildRemoteBinaryRestartScript(shellSingleQuote("tok-secret"), " --host-name macmini"),
		},
		{
			name:   "remote_self_update",
			script: buildRemoteSelfUpdateScript(shellSingleQuote("tok-secret"), " --host-name macmini"),
		},
		{
			name:   "local_async_restart",
			script: buildLocalAsyncRestartScript(4242, "/opt/homebrew/opt/ackbar/bin/ackbard", []string{"--host-name", "macmini"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := exec.Command("sh", "-n", "-c", tc.script).CombinedOutput(); err != nil {
				t.Fatalf("sh -n syntax check failed: %v\noutput: %s\nscript:\n%s", err, string(out), tc.script)
			}

			parts := strings.Split(tc.script, "\nelse\n")
			if len(parts) != 2 {
				t.Fatalf("expected exactly one top-level '\\nelse\\n' separator, got %d parts in:\n%s", len(parts), tc.script)
			}
			managedSection := parts[0]
			unmanagedSection := parts[1]

			if strings.Contains(managedSection, "nohup") {
				t.Fatalf("managed section must contain zero 'nohup' fallbacks:\n%s", managedSection)
			}
			if !strings.Contains(unmanagedSection, "nohup") {
				t.Fatalf("unmanaged else section must retain 'nohup' launch:\n%s", unmanagedSection)
			}

			idxSystemd := strings.Index(managedSection, "systemctl --user is-enabled ackbard.service")
			idxLaunchAgent := strings.Index(managedSection, `com\.marcinbak\.ackbard`)
			idxHomebrew := strings.Index(managedSection, `homebrew\.mxcl\.ackbar`)
			if idxSystemd == -1 || idxLaunchAgent == -1 || idxHomebrew == -1 {
				t.Fatalf("missing service manager check(s): systemd=%d launchAgent=%d homebrew=%d", idxSystemd, idxLaunchAgent, idxHomebrew)
			}
			if !(idxSystemd < idxLaunchAgent && idxLaunchAgent < idxHomebrew) {
				t.Fatalf("expected service manager order systemd -> com.marcinbak.ackbard -> homebrew.mxcl.ackbar, got %d, %d, %d", idxSystemd, idxLaunchAgent, idxHomebrew)
			}

			if gotPolls := strings.Count(managedSection, `while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done`); gotPolls != 3 {
				t.Fatalf("expected 3 POSIX 5s readiness poll loops in managed branches, got %d", gotPolls)
			}
		})
	}
}

func TestBuildLocalAsyncRestartScript_SingleQuotesHazardousExePath(t *testing.T) {
	exe := "/tmp/My Dir/$PWN/`id`/o'hare/ackbard"
	rawArgs := []string{"--display-name", "Mac $HOME `uname`"}
	script := buildLocalAsyncRestartScript(9999, exe, rawArgs)

	if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("sh -n failed for hazardous path: %v (%s)", err, string(out))
	}

	expectedCmd := "nohup '/tmp/My Dir/$PWN/`id`/o'\\''hare/ackbard' '--display-name' 'Mac $HOME `uname`' >/dev/null 2>&1 < /dev/null &"
	if !strings.Contains(script, expectedCmd) {
		t.Fatalf("expected single-quoted command:\n%s\nin script:\n%s", expectedCmd, script)
	}
}
