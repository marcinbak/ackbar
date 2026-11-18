package agentutil

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ackbar/internal/skills"
	"ackbar/internal/version"
)

var (
	skillVersionRegex = regexp.MustCompile(`(?m)^version:\s*".*?"\s*$`)
	rulesBlockRegex   = regexp.MustCompile(`(?s)<!-- ackbar-rules:v([^>]+) -->.*?<!-- /ackbar-rules -->`)
)

func StampSkillVersion(content []byte, ver string) []byte {
	str := string(content)
	vTag := fmt.Sprintf("version: %q", ver)
	if skillVersionRegex.MatchString(str) {
		return []byte(skillVersionRegex.ReplaceAllString(str, vTag))
	}
	if strings.Contains(str, "argument-hint:") {
		return []byte(strings.Replace(str, "argument-hint:", vTag+"\nargument-hint:", 1))
	}
	parts := strings.SplitN(str, "---", 3)
	if len(parts) >= 3 {
		return []byte(parts[0] + "---\n" + vTag + "\n" + strings.TrimLeft(parts[1], "\r\n") + "---" + parts[2])
	}
	return []byte("---\n" + vTag + "\n---\n\n" + str)
}

func ExtractSkillVersion(content string) string {
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "version:") {
			val := strings.TrimPrefix(l, "version:")
			return strings.Trim(strings.TrimSpace(val), "\"'")
		}
	}
	return ""
}

func ExtractRulesVersion(content string) string {
	matches := rulesBlockRegex.FindStringSubmatch(content)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func BuildMarkdownRulesBlock(ver string) string {
	return fmt.Sprintf(`<!-- ackbar-rules:v%s -->
## Communication & Documentation Rules (Ackbar Managed)

### Style (ASD-STE100 & Strunk Heuristic)
- Eliminate conversational filler, greetings, sign-offs, hedging, and meta-commentary.
- Lead with the answer, command, or code in the first sentence.
- Write in ASD-STE100 style: active voice, literal phrasing, simplest word choice, no metaphors, no synonyms for stylistic variety.
- State limitations as technical constraints, never apologies.
- Prefer tables and bullet points over prose paragraphs. Keep bullets under 20 words unless explaining a complex causal chain.

### Anti-Redundancy (Single Source of Truth)
- Never duplicate information across code comments, `+"`docs/*.md`"+`, PR descriptions, and chat.
- Code comments: document *why* (invariants/constraints), never *what* the code literally does.
- Docs (`+"`docs/*.md`"+`): document current architecture using tables and file links; never paste large code blocks or changelog history.
- PR bodies: max 5 bullets covering intent, blast radius, and verification gates.
- Chat replies: report only the outcome, key decisions, and links to changed files/artifacts.

### Host & Filesystem Safety
- On macOS, never run unscoped recursive searches (`+"`find`"+`, `+"`rg`"+`, `+"`fd`"+`, `+"`mdfind`"+`) rooted at `+"`~`"+` or `+"`/Users/<user>`"+`; scope searches to the workspace, `+"`~/.claude`"+`, `+"`~/.gemini`"+`, or `+"`~/Work`"+` to prevent macOS TCC prompts on `+"`~/Pictures`"+`, `+"`~/Music`"+`, and `+"`~/Movies`"+`.
<!-- /ackbar-rules -->`, ver)
}

func UpsertMarkdownRulesBlock(filePath, ver string) error {
	targetPath := filePath
	if resolved, err := filepath.EvalSymlinks(filePath); err == nil {
		targetPath = resolved
	}
	parentDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", parentDir, err)
	}
	if ver == "" {
		ver = version.Version
	}
	newBlock := BuildMarkdownRulesBlock(ver)

	var newContent string
	data, err := os.ReadFile(targetPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read rules file %s: %w", targetPath, err)
		}
		newContent = newBlock + "\n"
	} else {
		existing := string(data)
		if rulesBlockRegex.MatchString(existing) {
			newContent = rulesBlockRegex.ReplaceAllLiteralString(existing, newBlock)
		} else if len(existing) == 0 {
			newContent = newBlock + "\n"
		} else if strings.HasSuffix(existing, "\n\n") {
			newContent = existing + newBlock + "\n"
		} else if strings.HasSuffix(existing, "\n") {
			newContent = existing + "\n" + newBlock + "\n"
		} else {
			newContent = existing + "\n\n" + newBlock + "\n"
		}
	}

	if err := WriteFileAtomic(targetPath, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("failed to atomically update rules file %s: %w", targetPath, err)
	}
	return nil
}

func InstallEmbeddedSkill(targetSkillDir string) error {
	skillFiles, err := skills.GetSkillFiles()
	if err != nil {
		return fmt.Errorf("failed to retrieve embedded skill files: %w", err)
	}
	if err := os.MkdirAll(targetSkillDir, 0o755); err != nil {
		return fmt.Errorf("failed to create skill directory %s: %w", targetSkillDir, err)
	}
	for relPath, content := range skillFiles {
		filePath := filepath.Join(targetSkillDir, relPath)
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", filePath, err)
		}
		fileBytes := content
		if relPath == "SKILL.md" {
			fileBytes = StampSkillVersion(content, version.Version)
		}
		if err := WriteFileAtomic(filePath, fileBytes, 0o644); err != nil {
			return fmt.Errorf("failed to write skill file %s: %w", filePath, err)
		}
	}
	return nil
}
