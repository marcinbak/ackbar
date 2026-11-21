package agentutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
)

var SafeCLIArgRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-\.:/]{0,127}$`)

func IsSafeCLIArg(s string) bool {
	return SafeCLIArgRegex.MatchString(s)
}

func standardBinaryDirs(home string) []string {
	var dirs []string
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, "bin"),
			filepath.Join(home, ".cargo", "bin"),
		)
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin")
	return dirs
}

func isExecutableFile(path string) bool {
	stat, err := os.Stat(path)
	if err != nil || stat.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || stat.Mode()&0o111 != 0
}

func LookPathInStandardDirs(name string) (string, error) {
	return ResolveBinary(name)
}

func ResolveBinary(name string, extraDirs ...string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("empty binary name")
	}
	if filepath.IsAbs(trimmed) {
		if isExecutableFile(trimmed) {
			return trimmed, nil
		}
		return "", exec.ErrNotFound
	}
	if strings.ContainsAny(trimmed, "/\\") {
		return "", exec.ErrNotFound
	}
	if p, err := exec.LookPath(trimmed); err == nil && p != "" {
		return p, nil
	}
	for _, dir := range extraDirs {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, trimmed)
		if isExecutableFile(cand) {
			return cand, nil
		}
	}
	for _, dir := range standardBinaryDirs(RealUserHome()) {
		cand := filepath.Join(dir, trimmed)
		if isExecutableFile(cand) {
			return cand, nil
		}
	}
	return "", exec.ErrNotFound
}

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func IsAckbarInternalProcess(cmdLine string) bool {
	return IsAckbarProcess("", cmdLine)
}

func IsAckbarProcess(binLower, cmdLower string) bool {
	if strings.HasPrefix(strings.ToLower(binLower), "ackbar") {
		return true
	}
	fields := strings.Fields(strings.ToLower(cmdLower))
	for i := 0; i < len(fields) && i < 3; i++ {
		f := fields[i]
		if strings.HasPrefix(f, "-") {
			continue
		}
		base := filepath.Base(f)
		if base == "sh" || base == "bash" || base == "zsh" || base == "env" {
			continue
		}
		return strings.HasPrefix(base, "ackbar")
	}
	return false
}

var interactiveSessionValueFlags = map[string]bool{
	"--session-id": true, "-s": true, "--resume": true, "-r": true,
	"--conversation": true, "-c": true, "--conversation-id": true,
}

var cliValueFlags = map[string]bool{
	"--model": true, "-m": true, "--permission-mode": true, "--effort": true,
	"--name": true, "-n": true, "--add-dir": true, "--allowedtools": true,
	"--disallowedtools": true, "--append-system-prompt": true, "--system-prompt": true,
	"--settings": true, "--mcp-config": true, "--agents": true, "--agent": true,
	"--worktree": true, "-w": true,
}

func CheckIgnoredCLIArgs(fullCmd string, matchBin func(field, base string) bool, ignoredSubcommands, extraFlags map[string]bool) bool {
	fields := strings.Fields(fullCmd)
	if len(fields) == 0 {
		return false
	}
	argIdx := -1
	for i, f := range fields {
		base := strings.ToLower(filepath.Base(f))
		if matchBin(f, base) {
			argIdx = i + 1
			break
		}
	}
	if argIdx == -1 || argIdx >= len(fields) {
		return false
	}
	return checkIgnoredArgsSlice(fields[argIdx:], ignoredSubcommands, extraFlags)
}

func checkIgnoredArgsSlice(args []string, ignoredSubcommands, extraFlags map[string]bool) bool {
	hasSessionFlag := false
	for i := 0; i < len(args); i++ {
		a := strings.ToLower(strings.TrimSpace(args[i]))
		if strings.HasPrefix(a, "-") {
			if a == "--" {
				return false
			}
			if a == "-v" || a == "--version" || a == "-h" || a == "--help" || a == "-p" || a == "--print" || a == "--input-format" || a == "--output-format" {
				return true
			}
			if extraFlags != nil && extraFlags[a] {
				return true
			}
			if a == "-i" || a == "--prompt-interactive" {
				return false
			}
			if interactiveSessionValueFlags[a] {
				hasSessionFlag = true
				i++
				continue
			}
			if cliValueFlags[a] {
				i++
				continue
			}
			continue
		}
		if !hasSessionFlag && ignoredSubcommands != nil && ignoredSubcommands[a] {
			return true
		}
		return false
	}
	return false
}
