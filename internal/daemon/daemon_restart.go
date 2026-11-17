package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// systemdManagedShellCond matches enabled, dotfile-linked, static, or currently active/activating user units.
const systemdManagedShellCond = `command -v systemctl >/dev/null 2>&1 && (systemctl --user is-enabled ackbard.service >/dev/null 2>&1 || case "$(systemctl --user is-enabled ackbard.service 2>/dev/null)" in linked*|static) true ;; *) false ;; esac || case "$(systemctl --user is-active ackbard.service 2>/dev/null)" in active|activating|reloading) true ;; *) false ;; esac)`

func resolveRestartExecutable() string {
	exe, err := os.Executable()
	if err == nil && exe != "" {
		// Homebrew resolves os.Executable() into the old version's Cellar path; launch via the active opt/bin symlink so upgrades take effect.
		if strings.Contains(exe, "/Cellar/ackbar/") {
			for _, candidate := range []string{
				"/opt/homebrew/opt/ackbar/bin/ackbard",
				"/opt/homebrew/bin/ackbard",
				"/usr/local/opt/ackbar/bin/ackbard",
				"/usr/local/bin/ackbard",
			} {
				if _, serr := os.Stat(candidate); serr == nil {
					return candidate
				}
			}
		}
		return exe
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		return filepath.Join(home, ".local", "bin", "ackbard")
	}
	return "ackbard"
}

func restartLocalDaemonAsync() {
	script := buildLocalAsyncRestartScript(os.Getpid(), resolveRestartExecutable(), os.Args[1:])
	cmd := exec.Command("sh", "-c", script)
	_ = cmd.Start()
}

// Managed branches in all restart scripts deliberately omit nohup fallbacks because launchd KeepAlive
// and systemd Restart=always collide with any unmanaged daemon on 127.0.0.1:7777.
func buildRemoteBinaryRestartScript(quotedAuthToken, hostFlagArg string) string {
	if quotedAuthToken == "" {
		quotedAuthToken = "''"
	}
	return fmt.Sprintf(`set -e
export PATH="$HOME/.local/bin:$HOME/go/bin:/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
for b in ackbard ackbar ackbar-hook ackbar-relay; do
  if [ -f "$HOME/.local/bin/$b.new" ]; then
    chmod +x "$HOME/.local/bin/$b.new"
    mv -f "$HOME/.local/bin/$b.new" "$HOME/.local/bin/$b"
    for brewdir in /opt/homebrew/opt/ackbar/bin /usr/local/opt/ackbar/bin; do
      if [ -d "$brewdir" ] && [ -w "$brewdir" ]; then
        cp -f "$HOME/.local/bin/$b" "$brewdir/$b.tmp" && mv -f "$brewdir/$b.tmp" "$brewdir/$b" || true
      fi
    done
  fi
done
AUTH_TOKEN=%s
REQ_TOKEN="$AUTH_TOKEN"
if [ -z "$REQ_TOKEN" ]; then
  REQ_TOKEN="$ACKBAR_TOKEN"
fi
DRAIN_RES=""
if [ -n "$REQ_TOKEN" ]; then
  DRAIN_RES=$(curl -fsS -H "Authorization: Bearer $REQ_TOKEN" -X POST "http://127.0.0.1:7777/v1/shutdown?drain=true" 2>/dev/null || true)
fi
if [ -z "$REQ_TOKEN" ]; then
  DRAIN_RES=$(curl -fsS -X POST "http://127.0.0.1:7777/v1/shutdown?drain=true" 2>/dev/null || true)
fi
if echo "$DRAIN_RES" | grep -q '"restart_deferred":true'; then
  echo "RESTART_DEFERRED"
  exit 0
fi
if %s; then
  pkill -15 -x ackbard 2>/dev/null || true
  sleep 0.3
  pkill -9 -x ackbard 2>/dev/null || true
  systemctl --user restart ackbard.service >/dev/null
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
elif command -v launchctl >/dev/null 2>&1 && launchctl list 2>/dev/null | grep -Eq '(^|[[:space:]])com\.marcinbak\.ackbard($|[[:space:]])'; then
  pkill -15 -x ackbard 2>/dev/null || true
  sleep 0.3
  pkill -9 -x ackbard 2>/dev/null || true
  (launchctl kickstart -k "gui/$(id -u)/com.marcinbak.ackbard" >/dev/null || launchctl kickstart -k "user/$(id -u)/com.marcinbak.ackbard" >/dev/null)
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
elif command -v launchctl >/dev/null 2>&1 && launchctl list 2>/dev/null | grep -Eq '(^|[[:space:]])homebrew\.mxcl\.ackbar($|[[:space:]])'; then
  pkill -15 -x ackbard 2>/dev/null || true
  sleep 0.3
  pkill -9 -x ackbard 2>/dev/null || true
  export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"
  brew services restart ackbar >/dev/null
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
else
  pkill -15 -x ackbard 2>/dev/null || true
  sleep 0.3
  pkill -9 -x ackbard 2>/dev/null || true
  sleep 0.2
  nohup "$HOME/.local/bin/ackbard"%s >/dev/null 2>&1 < /dev/null &
fi`, quotedAuthToken, systemdManagedShellCond, hostFlagArg)
}

func buildRemoteSelfUpdateScript(quotedAuthToken, hostFlagArg string) string {
	if quotedAuthToken == "" {
		quotedAuthToken = "''"
	}
	return fmt.Sprintf(`set -e
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
AUTH_TOKEN=%s
UPD_OUT=$(if [ -n "$AUTH_TOKEN" ]; then ACKBAR_TOKEN="$AUTH_TOKEN" ackbar update --force 2>&1; else ackbar update --force 2>&1; fi)
echo "$UPD_OUT"
if echo "$UPD_OUT" | grep -q "restart automatically once the current turn finishes"; then
  echo "RESTART_DEFERRED"
  exit 0
fi
if %s; then
  systemctl --user restart ackbard.service >/dev/null
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
elif command -v launchctl >/dev/null 2>&1 && launchctl list 2>/dev/null | grep -Eq '(^|[[:space:]])com\.marcinbak\.ackbard($|[[:space:]])'; then
  (launchctl kickstart -k "gui/$(id -u)/com.marcinbak.ackbard" >/dev/null || launchctl kickstart -k "user/$(id -u)/com.marcinbak.ackbard" >/dev/null)
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
elif command -v launchctl >/dev/null 2>&1 && launchctl list 2>/dev/null | grep -Eq '(^|[[:space:]])homebrew\.mxcl\.ackbar($|[[:space:]])'; then
  export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"
  brew services restart ackbar >/dev/null
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
else
  pgrep -x ackbard >/dev/null 2>&1 || (nohup "$HOME/.local/bin/ackbard"%s >/dev/null 2>&1 < /dev/null &)
fi`, quotedAuthToken, systemdManagedShellCond, hostFlagArg)
}

func buildLocalAsyncRestartScript(pid int, exe string, rawArgs []string) string {
	cmdLine := shellSingleQuote(exe)
	if quotedArgs := shellQuoteArgs(rawArgs); quotedArgs != "" {
		cmdLine += " " + quotedArgs
	}
	return fmt.Sprintf(`sleep 0.5
if %s; then
  kill -15 %d 2>/dev/null || true
  sleep 0.3
  kill -9 %d 2>/dev/null || true
  systemctl --user restart ackbard.service >/dev/null 2>&1 || true
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
elif command -v launchctl >/dev/null 2>&1 && launchctl list 2>/dev/null | grep -Eq '(^|[[:space:]])com\.marcinbak\.ackbard($|[[:space:]])'; then
  kill -15 %d 2>/dev/null || true
  sleep 0.3
  kill -9 %d 2>/dev/null || true
  launchctl kickstart -k "gui/$(id -u)/com.marcinbak.ackbard" >/dev/null 2>&1 || launchctl kickstart -k "user/$(id -u)/com.marcinbak.ackbard" >/dev/null 2>&1 || true
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
elif command -v launchctl >/dev/null 2>&1 && launchctl list 2>/dev/null | grep -Eq '(^|[[:space:]])homebrew\.mxcl\.ackbar($|[[:space:]])'; then
  kill -15 %d 2>/dev/null || true
  sleep 0.3
  kill -9 %d 2>/dev/null || true
  export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"
  brew services restart ackbar >/dev/null 2>&1 || true
  i=0; while [ "$i" -lt 25 ]; do pgrep -x ackbard >/dev/null 2>&1 && break; sleep 0.2; i=$((i + 1)); done
else
  kill -15 %d 2>/dev/null || true
  sleep 0.3
  kill -9 %d 2>/dev/null || true
  sleep 0.2
  nohup %s >/dev/null 2>&1 < /dev/null &
fi`, systemdManagedShellCond, pid, pid, pid, pid, pid, pid, pid, pid, cmdLine)
}
