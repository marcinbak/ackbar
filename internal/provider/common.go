package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(u string) bool {
	return uuidRegex.MatchString(u)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func lookPathInStandardDirs(binName string) bool {
	if _, err := exec.LookPath(binName); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidates := []string{
			filepath.Join(home, ".local", "bin", binName),
			filepath.Join(home, ".npm-global", "bin", binName),
			filepath.Join(home, "bin", binName),
			filepath.Join(home, ".cargo", "bin", binName),
			"/opt/homebrew/bin/" + binName,
			"/usr/local/bin/" + binName,
			"/usr/bin/" + binName,
			"/bin/" + binName,
		}
		for _, c := range candidates {
			if stat, err := os.Stat(c); err == nil && !stat.IsDir() {
				return true
			}
		}
	}
	return false
}

var safeIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.:]+$`)

// isSafeSessionID validates session IDs against path traversal and shell/newline injection.
func isSafeSessionID(id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	return safeIDRegex.MatchString(id)
}

// cleanInjectedXMLTags strips LLM environment/system XML wrappers from prompt text.
func cleanInjectedXMLTags(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	xmlTags := []string{
		"environment_context",
		"recommended_plugins",
		"permissions instructions",
		"permissions_instructions",
		"collaboration_mode",
		"apps_instructions",
		"plugins_instructions",
		"skills_instructions",
		"multi_agent_mode",
	}

	clean := trimmed
	for _, tag := range xmlTags {
		startTag := "<" + tag + ">"
		endTag := "</" + tag + ">"
		for {
			sIdx := strings.Index(clean, startTag)
			if sIdx == -1 {
				break
			}
			relEnd := strings.Index(clean[sIdx+len(startTag):], endTag)
			if relEnd == -1 {
				clean = clean[:sIdx]
				break
			}
			eIdx := sIdx + len(startTag) + relEnd
			clean = clean[:sIdx] + clean[eIdx+len(endTag):]
		}
	}

	return strings.TrimSpace(clean)
}
