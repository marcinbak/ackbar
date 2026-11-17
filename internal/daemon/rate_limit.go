package daemon

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agentutil"
)

var (
	relHourRegex = regexp.MustCompile(`(?i)(\d+)\s*(?:hours?|hrs?|h)\b`)
	relMinRegex  = regexp.MustCompile(`(?i)(\d+)\s*(?:minutes?|mins?|m)\b`)
	relSecRegex  = regexp.MustCompile(`(?i)(\d+)\s*(?:seconds?|secs?|s)\b`)
)

func ParseRateLimitResetTime(text string, now time.Time) time.Time {
	t, _ := agentutil.ParseRateLimitResetTime(text, now)
	return t
}

// ParseDelayDuration parses relative durations like "1h", "30m", "45s", "1h30m", "2 hours", "15 minutes".
func ParseDelayDuration(input string) (time.Duration, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return 0, fmt.Errorf("empty duration")
	}

	// Try standard Go time.ParseDuration first
	if d, err := time.ParseDuration(trimmed); err == nil && d > 0 {
		return d, nil
	}

	// Parse human phrases e.g. "30 minutes", "1 hour", "2 hrs"
	var d time.Duration
	if hm := relHourRegex.FindStringSubmatch(trimmed); len(hm) > 1 {
		if h, err := strconv.Atoi(hm[1]); err == nil {
			d += time.Duration(h) * time.Hour
		}
	}
	if mm := relMinRegex.FindStringSubmatch(trimmed); len(mm) > 1 {
		if min, err := strconv.Atoi(mm[1]); err == nil {
			d += time.Duration(min) * time.Minute
		}
	}
	if sm := relSecRegex.FindStringSubmatch(trimmed); len(sm) > 1 {
		if s, err := strconv.Atoi(sm[1]); err == nil {
			d += time.Duration(s) * time.Second
		}
	}

	checkRemainder := trimmed
	checkRemainder = relHourRegex.ReplaceAllString(checkRemainder, "")
	checkRemainder = relMinRegex.ReplaceAllString(checkRemainder, "")
	checkRemainder = relSecRegex.ReplaceAllString(checkRemainder, "")
	if strings.TrimSpace(checkRemainder) != "" {
		return 0, fmt.Errorf("invalid duration format: %s", input)
	}

	if d > 0 {
		return d, nil
	}

	return 0, fmt.Errorf("invalid duration format: %s", input)
}

// ParseSlashDelayCommand parses inline slash command /delay <duration> [prompt].
// Supports single-token (/delay 1h Continue) and multi-token durations (/delay 2 hours run tests, /delay 1h 30m check status).
func ParseSlashDelayCommand(input string) (time.Duration, string, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/delay") {
		return 0, "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/delay"))
	if rest == "" {
		return 0, "", false
	}

	fields := strings.Fields(rest)
	maxTokens := len(fields)
	if maxTokens > 4 {
		maxTokens = 4
	}

	for i := maxTokens; i >= 1; i-- {
		candidate := strings.Join(fields[:i], " ")
		if d, err := ParseDelayDuration(candidate); err == nil && d > 0 {
			prompt := strings.TrimSpace(strings.Join(fields[i:], " "))
			return d, prompt, true
		}
	}

	return 0, "", false
}
