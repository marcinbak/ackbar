package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	plistAbandonTrueRegex  = regexp.MustCompile(`(?s)<key>\s*AbandonProcessGroup\s*</key>\s*<true\s*/>`)
	plistAbandonFalseRegex = regexp.MustCompile(`(?s)(<key>\s*AbandonProcessGroup\s*</key>\s*)<false\s*/>`)
)

// EnsureServiceProcessIsolation sets systemd KillMode=process and launchd AbandonProcessGroup
// on regular writable unit files so daemon restarts do not signal detached --headless-worker processes.
func EnsureServiceProcessIsolation(home string) (bool, error) {
	if home == "" {
		return false, nil
	}
	sysChanged, sysErr := ensureSystemdServiceIsolation(home)
	macChanged, macErr := ensureLaunchdIsolation(home)
	return sysChanged || macChanged, errors.Join(sysErr, macErr)
}

func isRegularWritableFile(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0200 == 0 {
		return false
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func writeFileAtomic0644(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	_, wErr := tmp.Write(data)
	cErr := tmp.Close()
	if wErr != nil || cErr != nil {
		_ = os.Remove(tmpName)
		return errors.Join(wErr, cErr)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Chmod(path, 0644)
}

func ensureSystemdServiceIsolation(home string) (bool, error) {
	unitPath := filepath.Join(home, ".config", "systemd", "user", "ackbard.service")
	if !isRegularWritableFile(unitPath) {
		return false, nil
	}
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return false, err
	}
	updated, changed := ensureSystemdKillModeProcess(string(data))
	if !changed {
		return false, nil
	}
	if err := writeFileAtomic0644(unitPath, []byte(updated)); err != nil {
		return false, err
	}
	if runtime.GOOS == "linux" {
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	return true, nil
}

func ensureSystemdKillModeProcess(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines)+1)
	hasService, inService, wroteKillMode, changed := false, false, false, false

	insertBeforeBlank := func(dest []string) []string {
		idx := len(dest)
		for idx > 0 && strings.TrimSpace(dest[idx-1]) == "" {
			idx--
		}
		res := append([]string{}, dest[:idx]...)
		res = append(res, "KillMode=process")
		return append(res, dest[idx:]...)
	}

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inService && !wroteKillMode {
				out, wroteKillMode, changed = insertBeforeBlank(out), true, true
			}
			inService = trimmed == "[Service]"
			hasService = hasService || inService
			out = append(out, rawLine)
			continue
		}
		if inService && strings.HasPrefix(trimmed, "KillMode") && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(trimmed, "KillMode")), "=") {
			if !wroteKillMode {
				if trimmed != "KillMode=process" {
					out, changed = append(out, "KillMode=process"), true
				} else {
					out = append(out, rawLine)
				}
				wroteKillMode = true
			} else {
				changed = true
			}
			continue
		}
		out = append(out, rawLine)
	}
	if !hasService {
		return content, false
	}
	if inService && !wroteKillMode {
		out, changed = insertBeforeBlank(out), true
	}
	if !changed {
		return content, false
	}
	return strings.Join(out, "\n"), true
}

func ensureLaunchdIsolation(home string) (bool, error) {
	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	anyChanged := false
	var errs []error
	for _, name := range []string{"com.marcinbak.ackbard.plist", "homebrew.mxcl.ackbar.plist"} {
		p := filepath.Join(agentsDir, name)
		if !isRegularWritableFile(p) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if updated, changed := ensurePlistAbandonProcessGroup(string(data)); changed {
			if err := writeFileAtomic0644(p, []byte(updated)); err != nil {
				errs = append(errs, err)
			} else {
				anyChanged = true
			}
		}
	}
	return anyChanged, errors.Join(errs...)
}

func ensurePlistAbandonProcessGroup(content string) (string, bool) {
	if plistAbandonTrueRegex.MatchString(content) {
		return content, false
	}
	if plistAbandonFalseRegex.MatchString(content) {
		return plistAbandonFalseRegex.ReplaceAllString(content, "${1}<true/>"), true
	}
	lastDict := strings.LastIndex(content, "</dict>")
	if lastDict < 0 {
		return content, false
	}
	lineStart := strings.LastIndexByte(content[:lastDict], '\n') + 1
	insertPos := lastDict
	if strings.TrimSpace(content[lineStart:lastDict]) == "" {
		insertPos = lineStart
	}
	indent := "\t"
	if idx := strings.LastIndex(content[:insertPos], "<key>"); idx >= 0 {
		ks := strings.LastIndexByte(content[:idx], '\n') + 1
		if strings.TrimSpace(content[ks:idx]) == "" {
			indent = content[ks:idx]
		}
	}
	prefix := ""
	if insertPos > 0 && content[insertPos-1] != '\n' {
		prefix = "\n"
	}
	snippet := prefix + indent + "<key>AbandonProcessGroup</key>\n" + indent + "<true/>\n"
	return content[:insertPos] + snippet + content[insertPos:], true
}

// DaemonSafeShellHelpers returns POSIX shell functions for self-healing service units
// and signaling/detecting ackbard daemon PIDs without touching --headless-worker processes.
func DaemonSafeShellHelpers() string {
	return `ackbard_pid_cmdline() {
  p="$1"
  if [ -r "/proc/$p/cmdline" ]; then tr '\0' ' ' < "/proc/$p/cmdline" 2>/dev/null || true
  else ps -p "$p" -o args= 2>/dev/null || ps -p "$p" -o command= 2>/dev/null || true; fi
}
signal_non_worker_ackbard() {
  sig="$1"; proc_name="${ACKBAR_DAEMON_PROC_NAME:-ackbard}"
  for p in $(pgrep -x "$proc_name" 2>/dev/null || true); do
    if [ -z "$p" ] || [ "$p" = "$$" ]; then continue; fi
    cmd=$(ackbard_pid_cmdline "$p")
    if [ -z "$cmd" ]; then continue; fi
    case "$cmd" in *--headless-worker*) continue ;; esac
    kill "-$sig" "$p" 2>/dev/null || true
  done
}
has_non_worker_ackbard() {
  proc_name="${ACKBAR_DAEMON_PROC_NAME:-ackbard}"
  for p in $(pgrep -x "$proc_name" 2>/dev/null || true); do
    if [ -z "$p" ]; then continue; fi
    cmd=$(ackbard_pid_cmdline "$p")
    if [ -z "$cmd" ]; then continue; fi
    case "$cmd" in *--headless-worker*) continue ;; esac
    return 0
  done
  return 1
}
self_heal_ackbard_service() {
  UNIT_FILE="$HOME/.config/systemd/user/ackbard.service"
  if [ -f "$UNIT_FILE" ] && [ ! -L "$UNIT_FILE" ] && [ -w "$UNIT_FILE" ]; then
    if grep -q '^\[Service\]' "$UNIT_FILE" && ! awk '/^\[Service\]/{s=1;next}/^\[/{s=0}s&&/^KillMode=process[[:space:]]*$/{f=1}END{exit !f}' "$UNIT_FILE"; then
      TMP_UNIT=$(mktemp "${UNIT_FILE}.tmp.XXXXXX")
      awk '/^\[Service\]/{print;s=1;next}/^\[/{if(s&&!d){print "KillMode=process";d=1}s=0}s&&/^KillMode=/{if(!d){print "KillMode=process";d=1}next}{print}END{if(s&&!d)print "KillMode=process"}' "$UNIT_FILE" > "$TMP_UNIT" && chmod 644 "$TMP_UNIT" && mv -f "$TMP_UNIT" "$UNIT_FILE" || rm -f "$TMP_UNIT"
    fi
  fi
  for PLIST in "$HOME/Library/LaunchAgents/com.marcinbak.ackbard.plist" "$HOME/Library/LaunchAgents/homebrew.mxcl.ackbar.plist"; do
    if [ -f "$PLIST" ] && [ ! -L "$PLIST" ] && [ -w "$PLIST" ] && ! tr '\n' ' ' < "$PLIST" | grep -qE '<key>[[:space:]]*AbandonProcessGroup[[:space:]]*</key>[[:space:]]*<true[[:space:]]*/>'; then
      if command -v plutil >/dev/null 2>&1; then
        TMP_PLIST=$(mktemp "${PLIST}.tmp.XXXXXX")
        cp -f "$PLIST" "$TMP_PLIST" && plutil -replace AbandonProcessGroup -bool true "$TMP_PLIST" >/dev/null 2>&1 && chmod 644 "$TMP_PLIST" && mv -f "$TMP_PLIST" "$PLIST" || rm -f "$TMP_PLIST"
      fi
    fi
  done
}`
}
