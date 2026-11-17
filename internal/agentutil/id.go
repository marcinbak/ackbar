package agentutil

import (
	"regexp"
	"strings"
)

var safeSessionIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-\.:]{0,255}$`)

func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func IsSafeSessionID(s string) bool {
	if s == "" || len(s) > 256 || s == "." || strings.Contains(s, "..") {
		return false
	}
	return safeSessionIDRegex.MatchString(s)
}

func IsSafeGrokSessionID(s string) bool {
	if s == "" || len(s) > 128 || s == "." || strings.Contains(s, "..") {
		return false
	}
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

func IsRawSessionName(n string) bool {
	n = strings.TrimSpace(n)
	n = strings.TrimSuffix(n, ":")
	n = strings.TrimSpace(n)
	switch n {
	case "", "antigravity", "claude-code", "codex", "grok", "opencode", "cli", "mock-agent":
		return true
	}
	if strings.HasPrefix(n, "ackbar-") || strings.HasPrefix(n, "proc-") || IsUUID(n) {
		return true
	}
	if len(n) >= 36 && IsUUID(n[:36]) {
		return true
	}
	if (strings.HasPrefix(n, "<") && strings.HasSuffix(n, ">")) ||
		(strings.HasPrefix(n, "(") && strings.HasSuffix(n, ")")) {
		inner := strings.TrimSpace(n[1 : len(n)-1])
		if IsUUID(inner) || (len(inner) >= 36 && IsUUID(inner[:36])) {
			return true
		}
	}
	for _, p := range []string{
		"antigravity (", "claude-code (", "codex (", "grok (", "opencode (",
		"Claude Code (", "Antigravity (", "xAI Grok (", "OpenCode (",
	} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return isGenericDirSlug(n)
}

func isGenericDirSlug(n string) bool {
	lastHyphen := strings.LastIndex(n, "-")
	if lastHyphen <= 0 || lastHyphen == len(n)-1 {
		return false
	}
	for _, c := range n[lastHyphen+1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	prefix := n[:lastHyphen]
	return prefix == strings.ToLower(prefix) && !strings.Contains(prefix, " ") && !strings.Contains(prefix, "_")
}

func TruncateTitle(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if maxRunes <= 0 {
		maxRunes = 50
	}
	lines := strings.Split(s, "\n")
	cleanLines := make([]string, 0, len(lines))
	for _, l := range lines {
		if trimmed := strings.TrimSpace(l); trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}
	if len(cleanLines) == 0 {
		return ""
	}
	res := cleanLines[0]
	if len([]rune(res)) < 6 && len(cleanLines) > 1 {
		res = res + " " + cleanLines[1]
	}
	runes := []rune(res)
	if len(runes) > maxRunes {
		if maxRunes <= 3 {
			return string(runes[:maxRunes])
		}
		return string(runes[:maxRunes-3]) + "..."
	}
	return res
}
