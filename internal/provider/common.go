package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
