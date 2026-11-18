package grok

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ackbar/internal/agentutil"
)

func inspectTOMLConfig(data []byte) (mcpInstalled bool, mcpCmd string, claudeHooksDisabled bool) {
	currentSection, command, args, enabled := "", "", []string{}, true
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := agentutil.StripTOMLComment(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line[1:len(line)-1], " \"'")
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if (currentSection == "" && key == "compat.claude.hooks") || (currentSection == "compat.claude" && key == "hooks") {
			if strings.EqualFold(val, "false") {
				claudeHooksDisabled = true
			}
		}
		if currentSection == "mcp_servers.ackbar" {
			if key == "command" {
				command = strings.Trim(val, " \"'")
			} else if key == "enabled" && strings.EqualFold(val, "false") {
				enabled = false
			} else if key == "args" {
				for _, item := range strings.Split(strings.Trim(val, "[] "), ",") {
					if c := strings.Trim(strings.TrimSpace(item), " \"'"); c != "" {
						args = append(args, c)
					}
				}
			}
		}
	}
	if command != "" && strings.Contains(command, "ackbar") && enabled {
		mcpInstalled = true
		mcpCmd = command
		if len(args) > 0 {
			mcpCmd = strings.TrimSpace(command + " " + strings.Join(args, " "))
		}
	}
	return mcpInstalled, mcpCmd, claudeHooksDisabled
}

func upsertCompatClaudeHooks(content string) string {
	lines := strings.Split(content, "\n")
	secIdx := -1
	for i, l := range lines {
		if strings.EqualFold(agentutil.StripTOMLComment(l), "[compat.claude]") {
			secIdx = i
			break
		}
	}
	if secIdx != -1 {
		for j := secIdx + 1; j < len(lines); j++ {
			trimmed := agentutil.StripTOMLComment(lines[j])
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				break
			}
			if p := strings.SplitN(trimmed, "=", 2); len(p) == 2 && strings.TrimSpace(p[0]) == "hooks" {
				lines[j] = "hooks = false"
				return strings.Join(lines, "\n")
			}
		}
		lines = append(lines[:secIdx+1], append([]string{"hooks = false"}, lines[secIdx+1:]...)...)
		return strings.Join(lines, "\n")
	}
	for i, l := range lines {
		if p := strings.SplitN(agentutil.StripTOMLComment(l), "=", 2); len(p) == 2 && strings.TrimSpace(p[0]) == "compat.claude.hooks" {
			lines[i] = "compat.claude.hooks = false"
			return strings.Join(lines, "\n")
		}
	}
	clean := strings.TrimRight(content, "\r\n")
	block := "[compat.claude]\nhooks = false\n"
	if len(clean) > 0 {
		return clean + "\n\n" + block
	}
	return block
}

func upsertAckbarMCPServer(content, quotedBin string) string {
	lines := strings.Split(content, "\n")
	secIdx := -1
	for i, l := range lines {
		if strings.EqualFold(agentutil.StripTOMLComment(l), "[mcp_servers.ackbar]") {
			secIdx = i
			break
		}
	}
	if secIdx != -1 {
		cmdIdx, argsIdx, endIdx := -1, -1, len(lines)
		for j := secIdx + 1; j < len(lines); j++ {
			trimmed := agentutil.StripTOMLComment(lines[j])
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				endIdx = j
				break
			}
			if p := strings.SplitN(trimmed, "=", 2); len(p) == 2 {
				k := strings.TrimSpace(p[0])
				if k == "command" {
					cmdIdx = j
				} else if k == "args" {
					argsIdx = j
				}
			}
		}
		if cmdIdx != -1 {
			lines[cmdIdx] = "command = " + quotedBin
		}
		if argsIdx != -1 {
			lines[argsIdx] = `args = ["mcp"]`
		}
		if cmdIdx == -1 {
			lines = append(lines[:secIdx+1], append([]string{"command = " + quotedBin}, lines[secIdx+1:]...)...)
			endIdx++
			if argsIdx != -1 {
				argsIdx++
			}
		}
		if argsIdx == -1 {
			pos := secIdx + 2
			if pos > endIdx {
				pos = endIdx
			}
			lines = append(lines[:pos], append([]string{`args = ["mcp"]`}, lines[pos:]...)...)
		}
		return strings.Join(lines, "\n")
	}
	clean := strings.TrimRight(content, "\r\n")
	block := fmt.Sprintf("[mcp_servers.ackbar]\ncommand = %s\nargs = [\"mcp\"]\n", quotedBin)
	if len(clean) > 0 {
		return clean + "\n\n" + block
	}
	return block
}

func UpsertTOMLConfig(configPath, ackbarBin string) error {
	target := configPath
	if res, err := filepath.EvalSymlinks(configPath); err == nil {
		target = res
	}
	fileMode, content := os.FileMode(0o600), ""
	if fi, err := os.Stat(target); err == nil {
		fileMode = fi.Mode().Perm()
		if data, err := os.ReadFile(target); err == nil {
			content = string(data)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %w", target, err)
	}
	content = upsertCompatClaudeHooks(content)
	content = upsertAckbarMCPServer(content, strconv.Quote(ackbarBin))
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", parent, err)
	}
	return agentutil.WriteFileAtomic(target, []byte(content), fileMode)
}
