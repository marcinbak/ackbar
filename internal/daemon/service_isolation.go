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

func isRegularWritableFile(path string) (os.FileMode, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0200 == 0 {
		return 0, false
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return 0, false
	}
	_ = f.Close()
	perm := info.Mode().Perm()
	if perm == 0 {
		perm = 0644
	}
	return perm, true
}

func writeFileAtomicWithMode(path string, data []byte, perm os.FileMode) error {
	if perm == 0 {
		perm = 0644
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(perm); err != nil {
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
	return os.Chmod(path, perm)
}

func rewriteRegularFile(path string, transform func(string) (string, bool)) (bool, error) {
	perm, ok := isRegularWritableFile(path)
	if !ok {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	updated, changed := transform(string(data))
	if !changed {
		return false, nil
	}
	if err := writeFileAtomicWithMode(path, []byte(updated), perm); err != nil {
		return false, err
	}
	return true, nil
}

func ensureSystemdServiceIsolation(home string) (bool, error) {
	unitPath := filepath.Join(home, ".config", "systemd", "user", "ackbard.service")
	changed, err := rewriteRegularFile(unitPath, ensureSystemdKillModeProcess)
	if err != nil || !changed {
		return changed, err
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

const launchAgentReloadMarkerRel = ".config/ackbar/.reload-launchagent"

// LaunchAgentReloadMarkerPath returns the marker file written when com.marcinbak.ackbard.plist
// is rewritten on disk so the next external service restart reloads the plist via bootout/bootstrap.
func LaunchAgentReloadMarkerPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, launchAgentReloadMarkerRel)
}

func ensureLaunchdIsolation(home string) (bool, error) {
	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	anyChanged := false
	var errs []error
	for _, name := range []string{"com.marcinbak.ackbard.plist", "homebrew.mxcl.ackbar.plist"} {
		changed, err := rewriteRegularFile(filepath.Join(agentsDir, name), ensurePlistAbandonProcessGroup)
		if changed && name == "com.marcinbak.ackbard.plist" {
			marker := LaunchAgentReloadMarkerPath(home)
			if marker != "" {
				_ = os.MkdirAll(filepath.Dir(marker), 0755)
				_ = os.WriteFile(marker, []byte("1\n"), 0600)
			}
		}
		anyChanged = anyChanged || changed
		if err != nil {
			errs = append(errs, err)
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
  else ps -ww -p "$p" -o args= 2>/dev/null || ps -ww -p "$p" -o command= 2>/dev/null || true; fi
}
ackbard_pid_argv0() {
  p="$1"
  if [ -r "/proc/$p/cmdline" ]; then tr '\0' '\n' < "/proc/$p/cmdline" 2>/dev/null | sed -n '1p' || true
  else ps -ww -p "$p" -o comm= 2>/dev/null || true; fi
}
ackbard_candidate_pids() {
  if [ "$(uname -s)" = "Darwin" ]; then pgrep -a -f -- "$1" 2>/dev/null || true
  else pgrep -f -- "$1" 2>/dev/null || true; fi
}
signal_non_worker_ackbard() {
  sig="$1"; proc_name="${ACKBAR_DAEMON_PROC_NAME:-ackbard}"
  for p in $(ackbard_candidate_pids "$proc_name"); do
    if [ -z "$p" ] || [ "$p" = "$$" ]; then continue; fi
    argv0="$(ackbard_pid_argv0 "$p")"
    [ "${argv0##*/}" = "$proc_name" ] || continue
    cmd=$(ackbard_pid_cmdline "$p")
    if [ -z "$cmd" ]; then continue; fi
    case "$cmd" in *--headless-worker*) continue ;; esac
    kill "-$sig" "$p" 2>/dev/null || true
  done
}
has_non_worker_ackbard() {
  proc_name="${ACKBAR_DAEMON_PROC_NAME:-ackbard}"
  for p in $(ackbard_candidate_pids "$proc_name"); do
    if [ -z "$p" ]; then continue; fi
    argv0="$(ackbard_pid_argv0 "$p")"
    [ "${argv0##*/}" = "$proc_name" ] || continue
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
      cp -pf "$UNIT_FILE" "$TMP_UNIT" && awk '/^\[Service\]/{print;s=1;next}/^\[/{if(s&&!d){print "KillMode=process";d=1}s=0}s&&/^KillMode=/{if(!d){print "KillMode=process";d=1}next}{print}END{if(s&&!d)print "KillMode=process"}' "$UNIT_FILE" > "$TMP_UNIT" && mv -f "$TMP_UNIT" "$UNIT_FILE" || rm -f "$TMP_UNIT"
    fi
  fi
  for PLIST in "$HOME/Library/LaunchAgents/com.marcinbak.ackbard.plist" "$HOME/Library/LaunchAgents/homebrew.mxcl.ackbar.plist"; do
    if [ -f "$PLIST" ] && [ ! -L "$PLIST" ] && [ -w "$PLIST" ] && ! tr '\n' ' ' < "$PLIST" | grep -qE '<key>[[:space:]]*AbandonProcessGroup[[:space:]]*</key>[[:space:]]*<true[[:space:]]*/>'; then
      if command -v plutil >/dev/null 2>&1; then
        ORIG_MODE=$(if [ "$(uname -s)" = "Darwin" ]; then stat -f '%Lp' "$PLIST"; else stat -c '%a' "$PLIST"; fi 2>/dev/null || true)
        TMP_PLIST=$(mktemp "${PLIST}.tmp.XXXXXX")
        if cp -pf "$PLIST" "$TMP_PLIST" && plutil -replace AbandonProcessGroup -bool true "$TMP_PLIST" >/dev/null 2>&1 && { [ -z "$ORIG_MODE" ] || chmod "$ORIG_MODE" "$TMP_PLIST"; } && mv -f "$TMP_PLIST" "$PLIST"; then
          case "$PLIST" in *com.marcinbak.ackbard.plist) mkdir -p "$HOME/.config/ackbar" && printf '1\n' > "$HOME/.config/ackbar/.reload-launchagent" && chmod 600 "$HOME/.config/ackbar/.reload-launchagent" || true ;; esac
        else
          rm -f "$TMP_PLIST"
        fi
      fi
    fi
  done
  return 0
}
reload_launchagent_if_changed() {
  MARKER="$HOME/.config/ackbar/.reload-launchagent"
  PLIST="$HOME/Library/LaunchAgents/com.marcinbak.ackbard.plist"
  [ -f "$MARKER" ] && [ -f "$PLIST" ] || return 1
  UID_NUM="$(id -u)"
  launchctl bootout "gui/$UID_NUM" "$PLIST" >/dev/null 2>&1 || launchctl bootout "user/$UID_NUM" "$PLIST" >/dev/null 2>&1 || true
  sleep 0.2
  if launchctl bootstrap "gui/$UID_NUM" "$PLIST" >/dev/null 2>&1 || launchctl bootstrap "user/$UID_NUM" "$PLIST" >/dev/null 2>&1; then
    rm -f "$MARKER"
    (launchctl kickstart "gui/$UID_NUM/com.marcinbak.ackbard" >/dev/null || launchctl kickstart "user/$UID_NUM/com.marcinbak.ackbard" >/dev/null)
    return 0
  fi
  return 1
}
codesign_darwin_staged_binary() {
  BIN_FILE="$1"
  BIN_NAME="$2"
  [ -f "$BIN_FILE" ] || return 0
  [ "$(uname -s)" = "Darwin" ] || return 0
  command -v codesign >/dev/null 2>&1 || return 0
  command -v security >/dev/null 2>&1 || return 0
  case "$BIN_NAME" in
    ""|*[!a-z0-9_-]*) return 0 ;;
  esac
  KEYCHAIN="$HOME/Library/Keychains/login.keychain-db"
  if [ ! -f "$KEYCHAIN" ] && [ -f "$HOME/Library/Keychains/login.keychain" ]; then
    KEYCHAIN="$HOME/Library/Keychains/login.keychain"
  fi
  security show-keychain-info "$KEYCHAIN" >/dev/null 2>&1 || return 0
  CS_IDENT="${ACKBAR_CODESIGN_IDENTITY:-}"
  if [ -z "$CS_IDENT" ] && [ -f "$HOME/.config/ackbar/codesign-identity" ]; then
    CS_IDENT=$(tr -d '\r\n' < "$HOME/.config/ackbar/codesign-identity" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' || true)
  fi
  case "$CS_IDENT" in
    -|none|None|NONE|adhoc|Adhoc|ADHOC) return 0 ;;
  esac
  CS_SHA1=$(security find-identity -v -p codesigning 2>/dev/null | TARGET_IDENT="$CS_IDENT" awk '
    /^[[:space:]]*[0-9]+\)[[:space:]]+[0-9A-Fa-f]{40}[[:space:]]+"[^"]+"[[:space:]]*$/ {
      sha = toupper($2)
      line = $0
      sub(/^[[:space:]]*[0-9]+\)[[:space:]]+[0-9A-Fa-f]{40}[[:space:]]+"/, "", line)
      sub(/"[[:space:]]*$/, "", line)
      t = ENVIRON["TARGET_IDENT"]
      if (t != "") {
        if (toupper(t) == sha || t == line) { print sha; found = 1; exit }
      } else {
        if (line == "Ackbar Local Code Signing" && p1 == "") { p1 = sha }
        else if (index(line, "Developer ID Application:") == 1 && p2 == "") { p2 = sha }
        else if (index(line, "Apple Development:") == 1 && p3 == "") { p3 = sha }
      }
    }
    END {
      if (!found && ENVIRON["TARGET_IDENT"] == "") {
        if (p1 != "") print p1
        else if (p2 != "") print p2
        else if (p3 != "") print p3
      }
    }' || true)
  [ -n "$CS_SHA1" ] || return 0
  CS_BUNDLE_ID="com.marcinbak.$BIN_NAME"
  if codesign --force --sign "$CS_SHA1" --identifier "$CS_BUNDLE_ID" "$BIN_FILE" >/dev/null 2>&1; then
    CS_REQ=$(codesign -d -r- "$BIN_FILE" 2>&1 || true)
    case "$CS_REQ" in
      *[Cc][Dd][Hh][Aa][Ss][Hh]*)
        CS_SHA1_LOWER=$(printf '%s' "$CS_SHA1" | tr 'A-F' 'a-f')
        codesign --force --sign "$CS_SHA1" --identifier "$CS_BUNDLE_ID" -r="designated => identifier \"$CS_BUNDLE_ID\" and certificate root = H\"$CS_SHA1_LOWER\"" "$BIN_FILE" >/dev/null 2>&1 || true
        ;;
    esac
  fi
  return 0
}`
}
