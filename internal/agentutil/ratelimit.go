package agentutil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	epochPipeRegex        = regexp.MustCompile(`\|([0-9]{10})\b`)
	relativeDurationRegex = regexp.MustCompile(`(?i)(?:in|after)\s+((?:\d+\s*(?:hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\s*)+)`)
	relHourRegex          = regexp.MustCompile(`(?i)(\d+)\s*(?:hours?|hrs?|h)\b`)
	relMinRegex           = regexp.MustCompile(`(?i)(\d+)\s*(?:minutes?|mins?|m)\b`)
	relSecRegex           = regexp.MustCompile(`(?i)(\d+)\s*(?:seconds?|secs?|s)\b`)
	monthClockRegex       = regexp.MustCompile(`(?i)(?:resets|until|retry)\s+(?:at\s+)?([A-Za-z]{3,9})\s+(\d{1,2})(?:st|nd|rd|th)?(?:,)?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	clock12hRegex         = regexp.MustCompile(`(?i)(?:resets|until|retry)\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\b`)
	clock24hRegex         = regexp.MustCompile(`(?i)(?:resets|until|retry)\s+(?:at\s+)?(\d{1,2}):(\d{2})\b`)
)

// ParseRateLimitResetTime extracts a reset timestamp with a +1m buffer, falling back to +1h.
func ParseRateLimitResetTime(text string, now time.Time) (time.Time, bool) {
	if m := epochPipeRegex.FindStringSubmatch(text); len(m) > 1 {
		if ts, err := strconv.ParseInt(m[1], 10, 64); err == nil && ts > 0 {
			return time.Unix(ts, 0).Add(time.Minute), true
		}
	}

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
			return now.Add(d).Add(time.Minute), true
		}
	}

	if m := monthClockRegex.FindStringSubmatch(text); len(m) > 1 {
		monthPrefix := m[1]
		if len(monthPrefix) > 3 {
			monthPrefix = monthPrefix[:3]
		}
		normalizedMonth := strings.ToUpper(monthPrefix[:1]) + strings.ToLower(monthPrefix[1:])
		if tMonth, err := time.Parse("Jan", normalizedMonth); err == nil {
			day, _ := strconv.Atoi(m[2])
			hour, _ := strconv.Atoi(m[3])
			min := 0
			if m[4] != "" {
				min, _ = strconv.Atoi(m[4])
			}
			ampm := strings.ToLower(m[5])
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
			return candidate.Add(time.Minute), true
		}
	}

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
		return candidate.Add(time.Minute), true
	}

	if m := clock24hRegex.FindStringSubmatch(text); len(m) > 1 {
		hour, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate.Add(time.Minute), true
	}

	return now.Add(time.Hour), false
}

func DetectDefaultRateLimitAt(text string, now time.Time) (bool, time.Time, string) {
	if text == "" {
		return false, time.Time{}, ""
	}
	lower := strings.ToLower(text)
	var prefix string
	switch {
	case strings.Contains(lower, "you've hit your usage limit") || strings.Contains(lower, "hit your usage limit") || strings.Contains(lower, "usage limit reached"):
		prefix = "Usage limit reached"
	case strings.Contains(lower, "rate limit reached") || strings.Contains(lower, "rate_limit_exceeded") || strings.Contains(lower, "rate limit exceeded"):
		prefix = "Rate limit reached"
	case strings.Contains(lower, "quota exceeded"):
		prefix = "Quota exceeded"
	default:
		return false, time.Time{}, ""
	}
	resetAt, _ := ParseRateLimitResetTime(text, now)
	return true, resetAt, fmt.Sprintf("%s · resets %s", prefix, resetAt.Local().Format("15:04"))
}

func DetectDefaultRateLimit(text string) (resetAt int64, matched bool) {
	limited, t, _ := DetectDefaultRateLimitAt(text, time.Now())
	if !limited {
		return 0, false
	}
	return t.Unix(), true
}
