package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"ackbar/internal/version"
)

//go:embed ackbar-tasks/*
var embeddedSkills embed.FS

// GetSkillFS returns the embedded filesystem of skills
func GetSkillFS() embed.FS {
	return embeddedSkills
}

var versionRegex = regexp.MustCompile(`(?m)^version:\s*".*?"\s*$`)

// GetSkillMD returns the primary SKILL.md content, stamped with current Ackbar version
func GetSkillMD() (string, error) {
	data, err := embeddedSkills.ReadFile("ackbar-tasks/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("failed to read embedded SKILL.md: %w", err)
	}

	content := string(data)
	vTag := fmt.Sprintf("version: \"%s\"", version.Version)
	if versionRegex.MatchString(content) {
		content = versionRegex.ReplaceAllString(content, vTag)
	} else if strings.Contains(content, "argument-hint:") {
		content = strings.Replace(content, "argument-hint:", vTag+"\nargument-hint:", 1)
	}

	return content, nil
}

// GetSkillFiles returns a map of all files under ackbar-tasks/
func GetSkillFiles() (map[string][]byte, error) {
	files := make(map[string][]byte)

	err := fs.WalkDir(embeddedSkills, "ackbar-tasks", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		data, err := embeddedSkills.ReadFile(path)
		if err != nil {
			return err
		}

		relPath := strings.TrimPrefix(path, "ackbar-tasks/")
		if relPath == "SKILL.md" {
			// Version-stamped
			if stamped, err := GetSkillMD(); err == nil {
				data = []byte(stamped)
			}
		}
		files[relPath] = data
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk embedded skills: %w", err)
	}

	return files, nil
}
