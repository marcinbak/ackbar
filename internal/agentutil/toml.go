package agentutil

import "strings"

func StripTOMLComment(line string) string {
	inQuote := false
	var quoteChar rune
	for i, r := range line {
		if inQuote {
			if r == quoteChar && (i == 0 || line[i-1] != '\\') {
				inQuote = false
			}
		} else if r == '"' || r == '\'' {
			inQuote = true
			quoteChar = r
		} else if r == '#' {
			return strings.TrimSpace(line[:i])
		}
	}
	return strings.TrimSpace(line)
}
