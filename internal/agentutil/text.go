package agentutil

import "strings"

func StripXMLBlock(text, tag string) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	for {
		start := strings.Index(text, startTag)
		if start == -1 {
			break
		}
		end := strings.Index(text[start:], endTag)
		if end == -1 {
			text = text[:start]
			break
		}
		text = text[:start] + text[start+end+len(endTag):]
	}
	return text
}

var cliEnvXMLTags = []string{
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

func CleanCLIEnvironmentTags(text string) string {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return ""
	}
	for _, tag := range cliEnvXMLTags {
		clean = StripXMLBlock(clean, tag)
	}
	return strings.TrimSpace(clean)
}

func ExtractTailLinesSlice(lines []string, count int) []string {
	if count <= 0 {
		return nil
	}
	lastNonEmpty := len(lines) - 1
	for lastNonEmpty >= 0 && strings.TrimSpace(lines[lastNonEmpty]) == "" {
		lastNonEmpty--
	}
	if lastNonEmpty < 0 {
		return nil
	}
	startIdx := lastNonEmpty - count + 1
	if startIdx < 0 {
		startIdx = 0
	}
	return lines[startIdx : lastNonEmpty+1]
}

func ExtractTailLines(text string, maxLines int) string {
	tail := ExtractTailLinesSlice(strings.Split(text, "\n"), maxLines)
	if len(tail) == 0 {
		return ""
	}
	return strings.Join(tail, "\n")
}

func ExtractTailText(data []byte, maxBytes int) string {
	if maxBytes <= 0 || len(data) == 0 {
		return ""
	}
	if len(data) <= maxBytes {
		return string(data)
	}
	return string(data[len(data)-maxBytes:])
}

func EnsureDoubleNewlineSeparation(a, b string) string {
	trailing := 0
	for i := len(a) - 1; i >= 0; i-- {
		if a[i] == '\n' {
			trailing++
		} else if a[i] != '\r' {
			break
		}
	}
	leading := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			leading++
		} else if b[i] != '\r' {
			break
		}
	}
	needed := 2 - (trailing + leading)
	if needed <= 0 {
		return b
	}
	return strings.Repeat("\n", needed) + b
}
