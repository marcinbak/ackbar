package daemon

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	epochPipeRegex = regexp.MustCompile(`\|([0-9]{10})\b`)

	relativeDurationRegex = regexp.MustCompile(`(?i)(?:in|after)\s+((?:\d+\s*(?:hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\s*)+)`)
	relHourRegex          = regexp.MustCompile(`(?i)(\d+)\s*(?:hours?|hrs?|h)\b`)
	relMinRegex           = regexp.MustCompile(`(?i)(\d+)\s*(?:minutes?|mins?|m)\b`)
	relSecRegex           = regexp.MustCompile(`(?i)(\d+)\s*(?:seconds?|secs?|s)\b`)

	monthClockRegex = regexp.MustCompile(`(?i)(?:resets|until|retry)\s+(?:at\s+)?([A-Za-z]{3,9})\s+(\d{1,2})(?:st|nd|rd|th)?(?:,)?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	clock12hRegex   = regexp.MustCompile(`(?i)(?:resets|until|retry)\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\b`)
	clock24hRegex   = regexp.MustCompile(`(?i)(?:resets|until|retry)\s+(?:at\s+)?(\d{1,2}):(\d{2})\b`)
)

// ParseRateLimitResetTime extracts the reset time from rate limit text, adding a +1m safety buffer.
// If no explicit reset time can be parsed, it falls back to now.Add(1 * time.Hour).
func ParseRateLimitResetTime(text string, now time.Time) time.Time {
	// 1. Pipe-delimited unix epoch seconds (e.g. Claude AI usage limit reached|1762736400)
	if m := epochPipeRegex.FindStringSubmatch(text); len(m) > 1 {
		if ts, err := strconv.ParseInt(m[1], 10, 64); err == nil && ts > 0 {
			return time.Unix(ts, 0).Add(1 * time.Minute)
		}
	}

	// 2. Relative durations (e.g. "in 1h 20m", "try again in 30 minutes", "retry after 60s")
	if m := relativeDurationRegex.FindStringSubmatch(text); len(m) > 1 {
		durStr := m[1]
		var d time.Duration
		if hm := relHourRegex.FindStringSubmatch(durStr); len(hm) > 1 {
			if h, err := strconv.Atoi(hm[1]); err == nil {
				d += time.Duration(h) * time.Hour
			}
		}
		if mm := relMinRegex.FindStringSubmatch(durStr); len(mm) > 1 {
			if min, err := strconv.Atoi(mm[1]); err == nil {
				d += time.Duration(min) * time.Minute
			}
		}
		if sm := relSecRegex.FindStringSubmatch(durStr); len(sm) > 1 {
			if s, err := strconv.Atoi(sm[1]); err == nil {
				d += time.Duration(s) * time.Second
			}
		}
		if d > 0 {
			return now.Add(d).Add(1 * time.Minute)
		}
	}

	// 3. Month + Day + Clock time (e.g. "resets Oct 10 at 2:00pm", "resets Nov 12 at 14:00")
	if m := monthClockRegex.FindStringSubmatch(text); len(m) > 1 {
		monthStr := m[1]
		dayStr := m[2]
		hourStr := m[3]
		minStr := m[4]
		ampm := strings.ToLower(m[5])

		monthPrefix := monthStr
		if len(monthPrefix) > 3 {
			monthPrefix = monthPrefix[:3]
		}
		normalizedMonth := strings.ToUpper(monthPrefix[:1]) + strings.ToLower(monthPrefix[1:])
		if tMonth, err := time.Parse("Jan", normalizedMonth); err == nil {
			day, _ := strconv.Atoi(dayStr)
			hour, _ := strconv.Atoi(hourStr)
			min := 0
			if minStr != "" {
				min, _ = strconv.Atoi(minStr)
			}
			if ampm == "pm" && hour < 12 {
				hour += 12
			} else if ampm == "am" && hour == 12 {
				hour = 0
			}

			year := now.Year()
			candidate := time.Date(year, tMonth.Month(), day, hour, min, 0, 0, now.Location())
			if candidate.Before(now) {
				candidate = time.Date(year+1, tMonth.Month(), day, hour, min, 0, 0, now.Location())
			}
			return candidate.Add(1 * time.Minute)
		}
	}

	// 4. 12-hour clock (e.g. "resets 2am", "resets at 2:30 PM", "until 3:00 PM")
	if m := clock12hRegex.FindStringSubmatch(text); len(m) > 1 {
		hour, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		ampm := strings.ToLower(m[3])
		if ampm == "pm" && hour < 12 {
			hour += 12
		} else if ampm == "am" && hour == 12 {
			hour = 0
		}

		candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate.Add(1 * time.Minute)
	}

	// 5. 24-hour clock (e.g. "resets 14:00", "resets at 02:30")
	if m := clock24hRegex.FindStringSubmatch(text); len(m) > 1 {
		hour, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])

		candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate.Add(1 * time.Minute)
	}

	// 6. Fallback
	return now.Add(1 * time.Hour)
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
