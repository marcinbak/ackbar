package skills

import (
	"strings"
	"testing"

	"ackbar/internal/version"
)

func TestSkills_GetSkillMD(t *testing.T) {
	md, err := GetSkillMD()
	if err != nil {
		t.Fatalf("GetSkillMD failed: %v", err)
	}

	if !strings.Contains(md, "name: ackbar-tasks") {
		t.Errorf("Expected skill name in frontmatter, got:\n%s", md)
	}
	if !strings.Contains(md, "version: \""+version.Version+"\"") {
		t.Errorf("Expected injected version %s in frontmatter, got:\n%s", version.Version, md)
	}
	if !strings.Contains(md, "report_blocker") {
		t.Errorf("Expected report_blocker tool reference in skill, got:\n%s", md)
	}
}

func TestSkills_GetSkillFiles(t *testing.T) {
	files, err := GetSkillFiles()
	if err != nil {
		t.Fatalf("GetSkillFiles failed: %v", err)
	}

	if _, ok := files["SKILL.md"]; !ok {
		t.Errorf("Expected SKILL.md in files map, got: %v", files)
	}
}
